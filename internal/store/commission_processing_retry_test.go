package store

import (
	"strings"
	"testing"
	"time"
)

func TestCommissionProcessingFailureRollsBackCreditAndRetryPaysExactlyOnce(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 9, 9, 3, 0, 0, 0, time.UTC)
	plan, buyerID := createOrderFixture(t, database, now, PlanPrices{"monthly": 1_000}, nil)
	inviter, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "commission-retry-inviter@example.test", PasswordHash: "hash",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET invite_user_id = ? WHERE id = ?`, inviter.ID, buyerID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET commission_type = 1, commission_rate = 20 WHERE id = ?`, inviter.ID); err != nil {
		t.Fatal(err)
	}

	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: buyerID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if order.CommissionBalance != 200 {
		t.Fatalf("created commission = %d, want 200", order.CommissionBalance)
	}
	if _, err := database.CompleteOrder(ctx, order.TradeNo, "commission-retry-callback", now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		CREATE TRIGGER fail_commission_log_insert
		BEFORE INSERT ON commission_logs
		BEGIN SELECT RAISE(ABORT, 'injected commission log failure'); END
	`); err != nil {
		t.Fatal(err)
	}

	maturedAt := now.Add(commissionConfirmationDelay)
	if _, err := database.ProcessCommissions(ctx, maturedAt, 100); err == nil || !strings.Contains(err.Error(), "record paid commission") {
		t.Fatalf("ProcessCommissions() error = %v, want injected commission log failure", err)
	}
	assertCommissionRetryState(t, database, inviter.ID, order.ID, 0, 0, 0, 0, 0)

	if _, err := database.db.ExecContext(ctx, `DROP TRIGGER fail_commission_log_insert`); err != nil {
		t.Fatal(err)
	}
	retried, err := database.ProcessCommissions(ctx, maturedAt, 100)
	if err != nil || retried.Checked != 1 || retried.Paid != 1 || retried.Remaining != 0 {
		t.Fatalf("ProcessCommissions(retry) = (%#v, %v), want checked/paid/remaining 1/1/0", retried, err)
	}
	assertCommissionRetryState(t, database, inviter.ID, order.ID, 200, 2, 200, 1, 200)

	repeated, err := database.ProcessCommissions(ctx, maturedAt.Add(time.Minute), 100)
	if err != nil || repeated.Checked != 0 || repeated.Paid != 0 || repeated.Remaining != 0 {
		t.Fatalf("ProcessCommissions(repeated) = (%#v, %v), want 0/0/0", repeated, err)
	}
	assertCommissionRetryState(t, database, inviter.ID, order.ID, 200, 2, 200, 1, 200)
}

func assertCommissionRetryState(t *testing.T, database *Store, inviterID, orderID, commissionBalance int64, commissionStatus int, actualCommission int64, logs int, loggedAmount int64) {
	t.Helper()
	var gotCommissionBalance, balance, gotActualCommission, gotLoggedAmount int64
	var orderStatus, gotCommissionStatus, gotLogs, entitlementEvents int
	var actualCommissionIsNull bool
	if err := database.db.QueryRowContext(t.Context(), `
		SELECT u.commission_balance, u.balance, o.status, o.commission_status,
		       o.actual_commission_balance IS NULL,
		       COALESCE(o.actual_commission_balance, 0),
		       (SELECT COUNT(*) FROM commission_logs WHERE order_id = o.id),
		       COALESCE((SELECT SUM(get_amount) FROM commission_logs WHERE order_id = o.id), 0),
		       (SELECT COUNT(*) FROM order_entitlement_events WHERE order_id = o.id)
		FROM users u CROSS JOIN orders o
		WHERE u.id = ? AND o.id = ?
	`, inviterID, orderID).Scan(
		&gotCommissionBalance, &balance, &orderStatus, &gotCommissionStatus, &actualCommissionIsNull, &gotActualCommission,
		&gotLogs, &gotLoggedAmount, &entitlementEvents,
	); err != nil {
		t.Fatal(err)
	}
	if gotCommissionBalance != commissionBalance || balance != 0 || orderStatus != int(OrderStatusCompleted) || gotCommissionStatus != commissionStatus ||
		actualCommissionIsNull != (commissionStatus == 0) ||
		gotActualCommission != actualCommission || gotLogs != logs || gotLoggedAmount != loggedAmount || entitlementEvents != 1 {
		t.Fatalf("commission retry state = commission/balance %d/%d order/status/actual/null %d/%d/%d/%t logs/amount %d/%d entitlement events %d",
			gotCommissionBalance, balance, orderStatus, gotCommissionStatus, gotActualCommission, actualCommissionIsNull, gotLogs, gotLoggedAmount, entitlementEvents)
	}
}
