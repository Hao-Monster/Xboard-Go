package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCancelledPartiallyFundedUpgradeRestoresWalletWithoutConsumingSurplus(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "order-upgrade-balance.db")
	database, err := OpenSQLite("file:" + filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	location := time.FixedZone("UTC+8", 8*60*60)
	paidAt := time.Date(2026, 4, 1, 0, 0, 0, 0, location)
	changeAt := time.Date(2026, 4, 16, 0, 0, 0, 0, location)
	oldExpiry := time.Date(2026, 5, 1, 0, 0, 0, 0, location)
	newExpiry := time.Date(2026, 5, 16, 0, 0, 0, 0, location)

	// Legacy surplus is calculated from the paid recurring amount and time
	// remaining. Half of April leaves 1500 cents; the replacement therefore
	// reserves the existing 400-cent wallet and still requires 600 cents.
	const oldPrice, newPrice = int64(3_001), int64(2_500)
	const wallet, wantSurplus, wantPayable = int64(400), int64(1_500), int64(600)
	oldPlan, userID := createOrderFixture(t, database, paidAt, PlanPrices{"monthly": oldPrice}, nil)
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET balance = ? WHERE id = ?`, oldPrice+wallet, userID); err != nil {
		t.Fatal(err)
	}
	policy, err := database.GetSubscriptionPolicySettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.UpdateSubscriptionPolicySettings(ctx, userID, policy.Revision, SaveSubscriptionPolicySettingsInput{
		PlanChangeEnabled: true, SurplusEnabled: true, ResetTrafficMethod: policy.ResetTrafficMethod,
		NewOrderEventID: 0, RenewOrderEventID: 0, ChangeOrderEventID: 0,
		DefaultRemindExpire: policy.DefaultRemindExpire, DefaultRemindTraffic: policy.DefaultRemindTraffic,
	}, paidAt); err != nil {
		t.Fatal(err)
	}
	targetPlan, err := database.CreatePlan(ctx, SavePlanInput{
		Name: "Partially funded upgrade", GroupID: oldPlan.GroupID, TransferEnableGiB: 200,
		Prices: PlanPrices{"monthly": newPrice},
	}, paidAt)
	if err != nil {
		t.Fatal(err)
	}
	targetPlan, err = database.SetPlanState(ctx, targetPlan.ID, targetPlan.Revision, PlanState{Show: true, Sell: true, Renew: true}, paidAt)
	if err != nil {
		t.Fatal(err)
	}

	oldOrder, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: oldPlan.ID, Period: "monthly"}, paidAt)
	if err != nil {
		t.Fatal(err)
	}
	oldOrder, err = database.CompleteOrder(ctx, oldOrder.TradeNo, "old-wallet-payment", paidAt)
	if err != nil {
		t.Fatal(err)
	}

	assertAccount := func(wantWallet, wantPlanID int64, wantExpiry time.Time) {
		t.Helper()
		var gotWallet, gotPlanID, gotExpiry int64
		if err := database.db.QueryRowContext(ctx, `SELECT balance, plan_id, expired_at FROM users WHERE id = ?`, userID).
			Scan(&gotWallet, &gotPlanID, &gotExpiry); err != nil {
			t.Fatal(err)
		}
		if gotWallet != wantWallet || gotPlanID != wantPlanID || gotExpiry != wantExpiry.Unix() {
			t.Fatalf("wallet/entitlement=%d/%d/%d want=%d/%d/%d", gotWallet, gotPlanID, gotExpiry, wantWallet, wantPlanID, wantExpiry.Unix())
		}
	}
	assertUpgrade := func(order Order, wantStatus OrderStatus) {
		t.Helper()
		if order.Type != OrderTypeUpgrade || order.Status != wantStatus || order.OriginalAmount != newPrice ||
			order.SurplusAmount != wantSurplus || order.SurplusCredit != 0 || order.BalanceAmount != wallet ||
			order.TotalAmount != wantPayable || len(order.SurplusOrderIDs) != 1 || order.SurplusOrderIDs[0] != oldOrder.ID {
			t.Fatalf("partially funded upgrade=%+v", order)
		}
	}
	assertOrderStatus := func(tradeNo string, want OrderStatus) {
		t.Helper()
		order, err := database.GetUserOrder(ctx, userID, tradeNo)
		if err != nil || order.Status != want {
			t.Fatalf("order %s status=%v want=%v err=%v", tradeNo, order.Status, want, err)
		}
	}

	assertAccount(wallet, oldPlan.ID, oldExpiry)
	cancelledUpgrade, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: targetPlan.ID, Period: "monthly"}, changeAt)
	if err != nil {
		t.Fatal(err)
	}
	assertUpgrade(cancelledUpgrade, OrderStatusPending)
	assertAccount(0, oldPlan.ID, oldExpiry)
	assertOrderStatus(oldOrder.TradeNo, OrderStatusCompleted)

	cancelledUpgrade, err = database.CancelOrder(ctx, userID, cancelledUpgrade.TradeNo, changeAt)
	if err != nil {
		t.Fatal(err)
	}
	assertUpgrade(cancelledUpgrade, OrderStatusCancelled)
	assertAccount(wallet, oldPlan.ID, oldExpiry)
	assertOrderStatus(oldOrder.TradeNo, OrderStatusCompleted)

	replacement, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: targetPlan.ID, Period: "monthly"}, changeAt)
	if err != nil {
		t.Fatal(err)
	}
	assertUpgrade(replacement, OrderStatusPending)
	replacement, err = database.CompleteOrder(ctx, replacement.TradeNo, "upgrade-payment", changeAt)
	if err != nil {
		t.Fatal(err)
	}
	assertUpgrade(replacement, OrderStatusCompleted)
	assertAccount(0, targetPlan.ID, newExpiry)
	assertOrderStatus(oldOrder.TradeNo, OrderStatusDiscounted)
	assertOrderStatus(cancelledUpgrade.TradeNo, OrderStatusCancelled)

	var cancelledEvents, completedEvents int
	if err := database.db.QueryRowContext(ctx, `
		SELECT
			COUNT(CASE WHEN order_id = ? THEN 1 END),
			COUNT(CASE WHEN order_id = ? THEN 1 END)
		FROM order_entitlement_events
	`, cancelledUpgrade.ID, replacement.ID).Scan(&cancelledEvents, &completedEvents); err != nil {
		t.Fatal(err)
	}
	if cancelledEvents != 0 || completedEvents != 1 {
		t.Fatalf("entitlement events cancelled=%d completed=%d want=0/1", cancelledEvents, completedEvents)
	}
}
