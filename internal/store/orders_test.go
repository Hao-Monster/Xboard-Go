package store

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCreateOrderUsesServerPriceBalanceAndDatabaseActiveOrderInvariant(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 34, 56, 0, time.UTC)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 1_000}, nil)
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET balance = 300 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}

	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "month_price"}, now)
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}
	if order.Type != OrderTypeNew || order.Status != OrderStatusPending || order.Period != "monthly" ||
		order.OriginalAmount != 1_000 || order.BalanceAmount != 300 || order.TotalAmount != 700 ||
		order.CommissionStatus == nil || *order.CommissionStatus != 0 {
		t.Fatalf("created order = %#v", order)
	}
	if matched := regexp.MustCompile(`^[0-9]{25}$`).MatchString(order.TradeNo); !matched {
		t.Fatalf("trade number %q does not preserve the 25-digit contract", order.TradeNo)
	}
	var balance int64
	if err := database.db.QueryRowContext(ctx, `SELECT balance FROM users WHERE id = ?`, userID).Scan(&balance); err != nil || balance != 0 {
		t.Fatalf("balance after order = %d, error=%v", balance, err)
	}
	if _, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now); !errors.Is(err, ErrActiveOrderExists) {
		t.Fatalf("second CreateOrder() error = %v, want ErrActiveOrderExists", err)
	}

	cancelled, err := database.CancelOrder(ctx, userID, order.TradeNo, now.Add(time.Minute))
	if err != nil || cancelled.Status != OrderStatusCancelled {
		t.Fatalf("CancelOrder() = (%#v, %v)", cancelled, err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT balance FROM users WHERE id = ?`, userID).Scan(&balance); err != nil || balance != 300 {
		t.Fatalf("balance after cancellation = %d, error=%v", balance, err)
	}
	if _, err := database.CancelOrder(ctx, userID, order.TradeNo, now.Add(2*time.Minute)); !errors.Is(err, ErrOrderState) {
		t.Fatalf("second CancelOrder() error = %v, want ErrOrderState", err)
	}
}

func TestOrderCommissionPreservesLegacyInviterRelationship(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 1_000}, nil)
	inviter, err := database.CreateRuntimeUser(ctx, CreateRuntimeUserInput{
		Email: "order-inviter@example.test", PasswordHash: "hash", GroupID: *plan.GroupID,
		UUID: "76a84e86-5e8a-4e51-8b4b-351da0625532", TransferEnable: 1,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET invite_user_id = ? WHERE id = ?`, inviter.ID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET commission_type = 2, commission_rate = 20 WHERE id = ?`, inviter.ID); err != nil {
		t.Fatal(err)
	}

	first, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.InviteUserID == nil || *first.InviteUserID != inviter.ID || first.CommissionBalance != 200 || first.CommissionRate != nil {
		t.Fatalf("first commission=%#v, want inviter relationship, 200 cents, and legacy null rate", first)
	}
	if _, err := database.CompleteOrder(ctx, first.TradeNo, first.TradeNo, now); err != nil {
		t.Fatal(err)
	}

	second, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if second.InviteUserID == nil || *second.InviteUserID != inviter.ID || second.CommissionBalance != 0 || second.CommissionRate != nil {
		t.Fatalf("second commission=%#v, want ineligible one-time commission with inviter retained", second)
	}
}

func TestAddOrderMonthsMatchesLegacyCarbonOverflow(t *testing.T) {
	t.Parallel()
	location := mustLocation(t, trafficResetLocationID)
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "2025-01-31 12:00:00", want: "2025-03-03 12:00:00"},
		{input: "2024-01-31 12:00:00", want: "2024-03-02 12:00:00"},
		{input: "2025-08-31 12:00:00", want: "2025-10-01 12:00:00"},
	} {
		input, err := time.ParseInLocation(time.DateTime, test.input, location)
		if err != nil {
			t.Fatal(err)
		}
		if actual := addOrderMonths(input, 1).In(location).Format(time.DateTime); actual != test.want {
			t.Errorf("addOrderMonths(%s, 1) = %s, want Carbon result %s", test.input, actual, test.want)
		}
	}
}

func TestCompleteFreeRenewalIsExactlyOnceAndAppliesPlanEntitlements(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	speed, devices, resetMethod := 250, 4, 1
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 0}, func(input *SavePlanInput) {
		input.SpeedLimit = &speed
		input.DeviceLimit = &devices
		input.ResetTrafficMethod = &resetMethod
	})
	expiry := now.Add(48 * time.Hour)
	if _, err := database.db.ExecContext(ctx, `
		UPDATE users SET plan_id = ?, expired_at = ?, traffic_u = 123, traffic_d = 456,
			speed_limit = 99, device_limit = 1 WHERE id = ?
	`, plan.ID, expiry.Unix(), userID); err != nil {
		t.Fatal(err)
	}

	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "month_price"}, now)
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}
	if order.Type != OrderTypeRenewal || order.TotalAmount != 0 {
		t.Fatalf("free renewal order = %#v", order)
	}

	var wg sync.WaitGroup
	results := make(chan Order, 2)
	errorsFound := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completed, completionErr := database.CompleteOrder(ctx, order.TradeNo, order.TradeNo, now.Add(time.Minute))
			results <- completed
			errorsFound <- completionErr
		}()
	}
	wg.Wait()
	close(results)
	close(errorsFound)
	for completionErr := range errorsFound {
		if completionErr != nil {
			t.Fatalf("concurrent CompleteOrder() error = %v", completionErr)
		}
	}
	for completed := range results {
		if completed.Status != OrderStatusCompleted || completed.PaidAt == nil || completed.CallbackNo != order.TradeNo {
			t.Fatalf("completed order = %#v", completed)
		}
	}

	var planID int64
	var expiredAt int64
	var transfer, upload, download int64
	var gotSpeed, gotDevices int
	if err := database.db.QueryRowContext(ctx, `
		SELECT plan_id, expired_at, transfer_enable, traffic_u, traffic_d, speed_limit, device_limit
		FROM users WHERE id = ?
	`, userID).Scan(&planID, &expiredAt, &transfer, &upload, &download, &gotSpeed, &gotDevices); err != nil {
		t.Fatal(err)
	}
	wantExpiry := expiry.In(mustLocation(t, trafficResetLocationID)).AddDate(0, 1, 0).Unix()
	if planID != plan.ID || expiredAt != wantExpiry || transfer != plan.TransferEnableGiB*bytesPerGiB ||
		upload != 123 || download != 456 || gotSpeed != speed || gotDevices != devices {
		t.Fatalf("renewed entitlement plan=%d expiry=%d/%d transfer=%d u=%d d=%d speed=%d devices=%d",
			planID, expiredAt, wantExpiry, transfer, upload, download, gotSpeed, gotDevices)
	}
}

func TestOrderKindsResetAndTimeoutBoundary(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 500, "onetime": 800, "reset_traffic": 100}, nil)
	expiry := now.Add(24 * time.Hour)
	if _, err := database.db.ExecContext(ctx, `
		UPDATE users SET plan_id = ?, expired_at = ?, traffic_u = 12, traffic_d = 34 WHERE id = ?
	`, plan.ID, expiry.Unix(), userID); err != nil {
		t.Fatal(err)
	}

	reset, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "reset_price"}, now)
	if err != nil || reset.Type != OrderTypeResetTraffic {
		t.Fatalf("reset CreateOrder() = (%#v, %v)", reset, err)
	}
	if _, err := database.CancelOrder(ctx, userID, reset.TradeNo, now); err != nil {
		t.Fatal(err)
	}

	old, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	result, err := database.ProcessStaleOrders(ctx, now, 100)
	if err != nil || result.Cancelled != 1 || result.Completed != 0 {
		t.Fatalf("ProcessStaleOrders() = (%#v, %v)", result, err)
	}
	got, err := database.GetUserOrder(ctx, userID, old.TradeNo)
	if err != nil || got.Status != OrderStatusCancelled {
		t.Fatalf("timed-out order = (%#v, %v)", got, err)
	}
}

func TestActiveOrderInvariantHoldsAcrossIndependentSQLiteConnections(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orders.db")
	first, err := OpenSQLite("file:" + filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := OpenSQLite("file:" + filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, first, now, PlanPrices{"monthly": 1_000}, nil)
	if _, err := first.db.ExecContext(ctx, `UPDATE users SET balance = 500 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}

	starts := make(chan struct{})
	results := make(chan error, 2)
	for _, database := range []*Store{first, second} {
		go func(database *Store) {
			<-starts
			_, createErr := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now)
			results <- createErr
		}(database)
	}
	close(starts)
	var succeeded, conflicted int
	for range 2 {
		switch createErr := <-results; {
		case createErr == nil:
			succeeded++
		case errors.Is(createErr, ErrActiveOrderExists):
			conflicted++
		default:
			t.Fatalf("concurrent CreateOrder() error = %v", createErr)
		}
	}
	var active int
	var balance int64
	if err := first.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE user_id = ? AND status IN (0, 1)`, userID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := first.db.QueryRowContext(ctx, `SELECT balance FROM users WHERE id = ?`, userID).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if succeeded != 1 || conflicted != 1 || active != 1 || balance != 0 {
		t.Fatalf("concurrent outcome succeeded=%d conflicted=%d active=%d balance=%d", succeeded, conflicted, active, balance)
	}
}

func TestProcessingOrderRecoveryAndResetPurchaseAreIdempotent(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	speed, devices := 300, 6
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 0, "reset_traffic": 0}, func(input *SavePlanInput) {
		input.SpeedLimit = &speed
		input.DeviceLimit = &devices
	})
	expiry := now.Add(30 * 24 * time.Hour)
	if _, err := database.db.ExecContext(ctx, `
		UPDATE users SET plan_id = ?, expired_at = ?, traffic_u = 100, traffic_d = 200 WHERE id = ?
	`, plan.ID, expiry.Unix(), userID); err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "reset_price"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE orders SET status = 1, paid_at = ?, callback_no = ? WHERE id = ?
	`, now.Unix(), "imported_callback", order.ID); err != nil {
		t.Fatal(err)
	}
	result, err := database.ProcessStaleOrders(ctx, now.Add(time.Minute), 100)
	if err != nil || result.Completed != 1 || result.Cancelled != 0 {
		t.Fatalf("ProcessStaleOrders(processing) = (%#v, %v)", result, err)
	}
	result, err = database.ProcessStaleOrders(ctx, now.Add(2*time.Minute), 100)
	if err != nil || result.Completed != 0 || result.Cancelled != 0 {
		t.Fatalf("second ProcessStaleOrders() = (%#v, %v)", result, err)
	}
	var upload, download, expiredAt, resetCount int64
	if err := database.db.QueryRowContext(ctx, `
		SELECT traffic_u, traffic_d, expired_at, reset_count FROM users WHERE id = ?
	`, userID).Scan(&upload, &download, &expiredAt, &resetCount); err != nil {
		t.Fatal(err)
	}
	var events int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM order_entitlement_events WHERE order_id = ?`, order.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if upload != 0 || download != 0 || expiredAt != expiry.Unix() || resetCount != 1 || events != 1 {
		t.Fatalf("reset recovery u=%d d=%d expiry=%d/%d count=%d events=%d", upload, download, expiredAt, expiry.Unix(), resetCount, events)
	}
}

func TestSchemaV29PreservesV28DataAndAddsFinancialConstraints(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(100, 0)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "v28-order@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	removeSchemaV32ForMigrationTest(t, database)
	if _, err := database.db.ExecContext(ctx, `
		DROP INDEX idx_users_directory_balance;
		DROP INDEX idx_users_directory_commission_balance;
		DROP TRIGGER trg_orders_payment_insert;
		DROP TRIGGER trg_orders_payment_update;
		DROP TRIGGER trg_payments_delete_restrict;
		DROP TABLE payment_webhook_receipts;
		DROP TABLE payment_checkout_attempts;
		DROP TABLE payments;
		DROP INDEX idx_orders_payment_status;
		DROP TRIGGER trg_orders_coupon_insert;
		DROP TRIGGER trg_orders_coupon_update;
		DROP TRIGGER trg_coupons_delete_restrict;
		DROP TABLE coupons;
		DROP INDEX idx_orders_coupon_user_status;
		ALTER TABLE app_settings DROP COLUMN coupon_enabled;
		DROP TABLE order_entitlement_events;
		DROP TABLE orders;
		ALTER TABLE users DROP COLUMN balance;
		ALTER TABLE users DROP COLUMN discount;
		ALTER TABLE users DROP COLUMN commission_type;
		ALTER TABLE users DROP COLUMN commission_rate;
		ALTER TABLE users DROP COLUMN commission_balance;
		ALTER TABLE app_settings DROP COLUMN plan_change_enable;
		ALTER TABLE app_settings DROP COLUMN surplus_enable;
		ALTER TABLE app_settings DROP COLUMN new_order_event_id;
		ALTER TABLE app_settings DROP COLUMN renew_order_event_id;
		ALTER TABLE app_settings DROP COLUMN change_order_event_id;
		ALTER TABLE app_settings DROP COLUMN commission_first_time_enable;
		ALTER TABLE app_settings DROP COLUMN invite_commission;
		PRAGMA user_version = 28;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var version int
	var email string
	var balance int64
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT email, balance FROM users WHERE id = ?`, user.ID).Scan(&email, &balance); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion || email != user.Email || balance != 0 {
		t.Fatalf("migration version=%d email=%q balance=%d", version, email, balance)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET balance = -1 WHERE id = ?`, user.ID); err == nil {
		t.Fatal("negative balance bypassed schema constraint")
	}
}

func TestOrderHotPathQueriesUseDedicatedIndexes(t *testing.T) {
	database := newTestStore(t)
	for _, test := range []struct {
		name  string
		query string
		args  []any
		index string
	}{
		{name: "active invariant", query: `SELECT 1 FROM orders WHERE user_id = ? AND status IN (0, 1)`, args: []any{1}, index: "idx_orders_user_active"},
		{name: "user history", query: `SELECT id FROM orders WHERE user_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, args: []any{1, 100}, index: "idx_orders_user_created"},
		{name: "status pagination", query: `SELECT id FROM orders WHERE status = ? ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, args: []any{3, 20, 0}, index: "idx_orders_status_created"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows, err := database.db.Query(`EXPLAIN QUERY PLAN `+test.query, test.args...)
			if err != nil {
				t.Fatal(err)
			}
			var plan strings.Builder
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					_ = rows.Close()
					t.Fatal(err)
				}
				plan.WriteString(detail)
				plan.WriteByte('\n')
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(plan.String(), test.index) {
				t.Fatalf("query plan does not use %s:\n%s", test.index, plan.String())
			}
		})
	}
}

func createOrderFixture(t testing.TB, database *Store, now time.Time, prices PlanPrices, mutate func(*SavePlanInput)) (Plan, int64) {
	t.Helper()
	ctx := context.Background()
	group, err := database.CreateServerGroup(ctx, "Order group", now)
	if err != nil {
		t.Fatal(err)
	}
	input := SavePlanInput{Name: "Order plan", GroupID: &group.ID, TransferEnableGiB: 100, Prices: prices}
	if mutate != nil {
		mutate(&input)
	}
	plan, err := database.CreatePlan(ctx, input, now)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = database.SetPlanState(ctx, plan.ID, plan.Revision, PlanState{Show: true, Sell: true, Renew: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateRuntimeUser(ctx, CreateRuntimeUserInput{
		Email: "order-user@example.test", PasswordHash: "hash", GroupID: group.ID,
		UUID: "020d6dd3-bc81-43c7-8011-71845ef9df88", TransferEnable: 1,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	return plan, user.ID
}

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return location
}
