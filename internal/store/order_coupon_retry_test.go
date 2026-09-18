package store

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

type orderCouponUserSnapshot struct {
	balance           int64
	commissionBalance int64
	adminRevision     int64
	updatedAt         int64
}

type orderStatusSnapshot struct {
	total      int64
	pending    int64
	processing int64
	cancelled  int64
	completed  int64
	discounted int64
}

type orderCouponRollbackSnapshot struct {
	user   orderCouponUserSnapshot
	coupon Coupon
	orders orderStatusSnapshot
}

func readOrderCouponRollbackSnapshot(t *testing.T, database *Store, userID, couponID int64) orderCouponRollbackSnapshot {
	t.Helper()
	ctx := t.Context()
	var snapshot orderCouponRollbackSnapshot
	if err := database.db.QueryRowContext(ctx, `
		SELECT balance, commission_balance, admin_revision, updated_at FROM users WHERE id = ?
	`, userID).Scan(&snapshot.user.balance, &snapshot.user.commissionBalance,
		&snapshot.user.adminRevision, &snapshot.user.updatedAt); err != nil {
		t.Fatal(err)
	}
	coupon, err := database.GetCoupon(ctx, couponID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.coupon = coupon
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0)
		FROM orders WHERE user_id = ?
	`, OrderStatusPending, OrderStatusProcessing, OrderStatusCancelled, OrderStatusCompleted,
		OrderStatusDiscounted, userID).Scan(&snapshot.orders.total, &snapshot.orders.pending,
		&snapshot.orders.processing, &snapshot.orders.cancelled, &snapshot.orders.completed,
		&snapshot.orders.discounted); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestCreateOrderFailureRollsBackBalanceAndCouponConsumptionThenRetrySucceeds(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 100_000}, nil)
	if _, err := database.db.ExecContext(ctx, `
		UPDATE users SET balance = 30000, commission_balance = 777, updated_at = ? WHERE id = ?
	`, now.Unix(), userID); err != nil {
		t.Fatal(err)
	}
	two, one := 2, 1
	coupon, err := database.CreateCoupon(ctx, SaveCouponInput{
		Code: "RETRYFIXED123", Name: "Rollback fixed discount", Type: CouponTypeFixed, Value: 12_345, Show: true,
		LimitUse: &two, LimitUseWithUser: &one, LimitPlanIDs: []int64{plan.ID}, LimitPeriods: []string{"monthly"},
		StartedAt: now.Add(-time.Hour), EndedAt: now.Add(24 * time.Hour),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	before := readOrderCouponRollbackSnapshot(t, database, userID, coupon.ID)
	if before.user.balance != 30_000 || before.user.commissionBalance != 777 ||
		before.coupon.LimitUse == nil || *before.coupon.LimitUse != 2 || before.orders.total != 0 {
		t.Fatalf("initial order/coupon snapshot = %#v", before)
	}

	trigger := fmt.Sprintf(`
		CREATE TRIGGER fail_fixture_order_insert
		BEFORE INSERT ON orders
		WHEN NEW.user_id = %d
		BEGIN SELECT RAISE(ABORT, 'injected order insert failure'); END
	`, userID)
	if _, err := database.db.ExecContext(ctx, trigger); err != nil {
		t.Fatal(err)
	}
	input := CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "month_price", CouponCode: coupon.Code}
	if _, err := database.CreateOrder(ctx, input, now.Add(time.Minute)); err == nil ||
		!strings.Contains(err.Error(), "create order") || !strings.Contains(err.Error(), "injected order insert failure") {
		t.Fatalf("CreateOrder() error = %v, want injected final insert failure", err)
	}
	afterFailure := readOrderCouponRollbackSnapshot(t, database, userID, coupon.ID)
	if !reflect.DeepEqual(afterFailure, before) {
		t.Fatalf("failed order changed wallet, revision, coupon, or order state: got %#v want %#v", afterFailure, before)
	}

	if _, err := database.db.ExecContext(ctx, `DROP TRIGGER fail_fixture_order_insert`); err != nil {
		t.Fatal(err)
	}
	retryTime := now.Add(2 * time.Minute)
	created, err := database.CreateOrder(ctx, input, retryTime)
	if err != nil {
		t.Fatalf("retry CreateOrder() error = %v", err)
	}
	if created.UserID != userID || created.PlanID != plan.ID || created.Type != OrderTypeNew ||
		created.Status != OrderStatusPending || created.Period != "monthly" || created.OriginalAmount != 100_000 ||
		created.DiscountAmount != 12_345 || created.BalanceAmount != 30_000 || created.TotalAmount != 57_655 ||
		created.CouponID == nil || *created.CouponID != coupon.ID || created.CommissionStatus == nil || *created.CommissionStatus != 0 ||
		!created.CreatedAt.Equal(retryTime) || !created.UpdatedAt.Equal(retryTime) {
		t.Fatalf("retry CreateOrder() = %#v, want fixed server-side price split", created)
	}
	persisted, err := database.GetUserOrder(ctx, userID, created.TradeNo)
	if err != nil {
		t.Fatalf("GetUserOrder() error = %v", err)
	}
	if persisted.ID != created.ID || persisted.UserID != userID || persisted.PlanID != plan.ID ||
		persisted.Type != OrderTypeNew || persisted.Status != OrderStatusPending || persisted.Period != "monthly" ||
		persisted.OriginalAmount != 100_000 || persisted.DiscountAmount != 12_345 || persisted.BalanceAmount != 30_000 ||
		persisted.TotalAmount != 57_655 || persisted.CouponID == nil || *persisted.CouponID != coupon.ID ||
		persisted.CommissionStatus == nil || *persisted.CommissionStatus != 0 ||
		!persisted.CreatedAt.Equal(retryTime) || !persisted.UpdatedAt.Equal(retryTime) {
		t.Fatalf("persisted retry order = %#v, want fixed server-side price split", persisted)
	}

	afterRetry := readOrderCouponRollbackSnapshot(t, database, userID, coupon.ID)
	wantAfterRetry := before
	wantAfterRetry.user.balance = 0
	wantAfterRetry.user.adminRevision++
	wantAfterRetry.user.updatedAt = retryTime.Unix()
	remaining := 1
	wantAfterRetry.coupon.LimitUse = &remaining
	wantAfterRetry.coupon.UpdatedAt = retryTime.UTC()
	wantAfterRetry.orders.total = 1
	wantAfterRetry.orders.pending = 1
	if !reflect.DeepEqual(afterRetry, wantAfterRetry) {
		t.Fatalf("retry wallet, revision, coupon, or order state = %#v, want %#v", afterRetry, wantAfterRetry)
	}
}
