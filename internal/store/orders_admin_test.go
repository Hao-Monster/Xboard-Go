package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSchemaV36AddsAdministratorOrderQueryIndexes(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	for _, index := range []string{
		"idx_orders_created", "idx_orders_type_created", "idx_orders_period_created",
		"idx_orders_total_amount", "idx_orders_status", "idx_orders_commission_balance",
		"idx_orders_commission_status", "idx_orders_commission_status_created", "idx_orders_admin_filters",
	} {
		if _, err := database.db.ExecContext(ctx, `DROP INDEX IF EXISTS `+index); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE app_settings SET app_name = 'V35 order board'; PRAGMA user_version = 35`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var version int
	var appName string
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT app_name FROM app_settings WHERE id = 1`).Scan(&appName); err != nil {
		t.Fatal(err)
	}
	if version != 36 || appName != "V35 order board" {
		t.Fatalf("migration version=%d app=%q", version, appName)
	}
	for _, index := range []string{
		"idx_orders_created", "idx_orders_type_created", "idx_orders_period_created",
		"idx_orders_total_amount", "idx_orders_status", "idx_orders_commission_balance",
		"idx_orders_commission_status", "idx_orders_commission_status_created", "idx_orders_admin_filters",
	} {
		var found int
		if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='index' AND name=?`, index).Scan(&found); err != nil || found != 1 {
			t.Fatalf("index %s count=%d error=%v", index, found, err)
		}
	}

	var plan strings.Builder
	rows, err := database.db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT id FROM orders ORDER BY total_amount, id LIMIT 20`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "idx_orders_total_amount") {
		t.Fatalf("total amount query plan = %q", plan.String())
	}

	plan.Reset()
	rows, err = database.db.QueryContext(ctx, `
		EXPLAIN QUERY PLAN SELECT COUNT(*) FROM orders
		WHERE status IN (3, 4) AND type IN (1, 2)
		  AND period IN ('monthly', 'yearly') AND commission_status IN (0, 1)
	`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "idx_orders_admin_filters") {
		t.Fatalf("administrator filter query plan = %q", plan.String())
	}
}

func TestListAdminOrdersCombinesLegacyMultiFiltersAndAllowlistedSort(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 8, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 100, "yearly": 300, "quarterly": 200}, nil)

	fixtures := []struct {
		tradeNo         string
		period          string
		orderType       OrderType
		status          OrderStatus
		commissionState int
		amount          int64
		createdAt       time.Time
	}{
		{"2026082700000000000000001", "monthly", OrderTypeNew, OrderStatusCompleted, 0, 100, now},
		{"2026082700000000000000002", "yearly", OrderTypeRenewal, OrderStatusCompleted, 1, 100, now.Add(time.Second)},
		{"2026082700000000000000003", "quarterly", OrderTypeUpgrade, OrderStatusCompleted, 3, 200, now.Add(2 * time.Second)},
		{"2026082700000000000000004", "monthly", OrderTypeNew, OrderStatusCancelled, 0, 50, now.Add(3 * time.Second)},
	}
	for _, fixture := range fixtures {
		if _, err := database.db.ExecContext(ctx, `
			INSERT INTO orders (
				user_id, plan_id, period, trade_no, original_amount, total_amount, type, status,
				commission_status, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, userID, plan.ID, fixture.period, fixture.tradeNo, fixture.amount, fixture.amount,
			fixture.orderType, fixture.status, fixture.commissionState, fixture.createdAt.Unix(), fixture.createdAt.Unix()); err != nil {
			t.Fatal(err)
		}
	}

	page, err := database.ListAdminOrders(ctx, AdminOrderFilter{
		Page: 1, PageSize: 20,
		Statuses:           []OrderStatus{OrderStatusCompleted},
		Types:              []OrderType{OrderTypeNew, OrderTypeRenewal},
		Periods:            []string{"monthly", "year_price"},
		CommissionStatuses: []int{0, 1},
		SortBy:             AdminOrderSortTotalAmount,
		SortDescending:     false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 2 || page.Items[0].TradeNo != fixtures[0].tradeNo || page.Items[1].TradeNo != fixtures[1].tradeNo {
		t.Fatalf("multi-filtered page = %#v", page)
	}
	page, err = database.ListAdminOrders(ctx, AdminOrderFilter{
		Page: 1, PageSize: 20,
		Statuses:           []OrderStatus{OrderStatusCompleted},
		Types:              []OrderType{OrderTypeNew, OrderTypeRenewal},
		Periods:            []string{"monthly", "yearly"},
		CommissionStatuses: []int{0, 1},
		SortBy:             AdminOrderSortTotalAmount,
		SortDescending:     true,
	})
	if err != nil || page.Total != 2 || len(page.Items) != 2 ||
		page.Items[0].TradeNo != fixtures[1].tradeNo || page.Items[1].TradeNo != fixtures[0].tradeNo {
		t.Fatalf("descending stable page = (%#v, %v)", page, err)
	}

	for name, filter := range map[string]AdminOrderFilter{
		"invalid status":     {Statuses: []OrderStatus{99}},
		"invalid type":       {Types: []OrderType{99}},
		"invalid period":     {Periods: []string{"not-a-period"}},
		"invalid commission": {CommissionStatuses: []int{4}},
		"untrusted sort":     {SortBy: AdminOrderSortField("total_amount DESC; DROP TABLE orders")},
		"too many statuses":  {Statuses: []OrderStatus{0, 1, 2, 3, 4, 0}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := database.ListAdminOrders(ctx, filter); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("ListAdminOrders(%#v) error = %v, want ErrInvalidInput", filter, err)
			}
		})
	}
}

func TestUpdateAdminOrderCommissionStatusRejectsPaidRollback(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 1_000}, nil)
	inviter, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "admin-order-inviter@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	const tradeNo = "2026082700000000000000010"
	result, err := database.db.ExecContext(ctx, `
		INSERT INTO orders (
			user_id, plan_id, period, trade_no, original_amount, total_amount, type, status,
			commission_status, invite_user_id, commission_balance, paid_at, callback_no, created_at, updated_at
		) VALUES (?, ?, 'monthly', ?, 1000, 1000, 1, 3, 0, ?, 200, ?, 'paid', ?, ?)
	`, userID, plan.ID, tradeNo, inviter.ID, now.Unix(), now.Unix(), now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	orderID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}

	updated, err := database.UpdateAdminOrderCommissionStatus(ctx, tradeNo, 1, now.Add(time.Minute))
	if err != nil || updated.CommissionStatus == nil || *updated.CommissionStatus != 1 {
		t.Fatalf("UpdateAdminOrderCommissionStatus(1) = (%#v, %v)", updated, err)
	}
	if _, err := database.UpdateAdminOrderCommissionStatus(ctx, tradeNo, 3, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("UpdateAdminOrderCommissionStatus(3) error = %v", err)
	}
	if _, err := database.UpdateAdminOrderCommissionStatus(ctx, tradeNo, 0, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("UpdateAdminOrderCommissionStatus(0) error = %v", err)
	}
	if _, err := database.UpdateAdminOrderCommissionStatus(ctx, tradeNo, 2, now.Add(4*time.Minute)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("administrator-selected paid state error = %v, want ErrInvalidInput", err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE orders SET commission_status = 2 WHERE id = ?`, orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateAdminOrderCommissionStatus(ctx, tradeNo, 1, now.Add(5*time.Minute)); !errors.Is(err, ErrOrderState) {
		t.Fatalf("paid rollback error = %v, want ErrOrderState", err)
	}
	var state int
	if err := database.db.QueryRowContext(ctx, `SELECT commission_status FROM orders WHERE id = ?`, orderID).Scan(&state); err != nil || state != 2 {
		t.Fatalf("paid commission state = %d, error = %v", state, err)
	}
}

func TestGetAdminOrderDetailIncludesInviterAndCommissionLedger(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 1_000}, nil)
	inviter, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "detail-inviter@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	const tradeNo = "2026082700000000000000020"
	result, err := database.db.ExecContext(ctx, `
		INSERT INTO orders (
			user_id, plan_id, period, trade_no, original_amount, total_amount, type, status,
			commission_status, invite_user_id, commission_balance, actual_commission_balance,
			paid_at, callback_no, created_at, updated_at
		) VALUES (?, ?, 'monthly', ?, 1000, 1000, 1, 3, 2, ?, 200, 200, ?, 'gateway-callback', ?, ?)
	`, userID, plan.ID, tradeNo, inviter.ID, now.Unix(), now.Unix(), now.Add(time.Minute).Unix())
	if err != nil {
		t.Fatal(err)
	}
	orderID, _ := result.LastInsertId()
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO commission_logs (
			order_id, invite_user_id, user_id, trade_no, order_amount, get_amount, created_at, updated_at
		) VALUES (?, ?, ?, ?, 1000, 200, ?, ?)
	`, orderID, inviter.ID, userID, tradeNo, now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}

	detail, err := database.GetAdminOrderDetail(ctx, tradeNo)
	if err != nil {
		t.Fatal(err)
	}
	if detail.InviteUser == nil || detail.InviteUser.ID != inviter.ID || detail.InviteUser.Email != inviter.Email ||
		len(detail.CommissionLog) != 1 || detail.CommissionLog[0].TradeNo != tradeNo || detail.CommissionLog[0].GetAmount != 200 {
		t.Fatalf("administrator order detail = %#v", detail)
	}
}
