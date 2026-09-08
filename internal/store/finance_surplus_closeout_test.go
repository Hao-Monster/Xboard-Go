package store

import (
	"testing"
	"time"
)

func TestFinanceSurplusCreditCompletesLegacyPlanChangeExactlyOnce(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	location := time.FixedZone("UTC+8", 8*60*60)
	paidAt := time.Date(2026, 4, 1, 0, 0, 0, 0, location)
	changeAt := time.Date(2026, 4, 16, 0, 0, 0, 0, location)
	oldExpiry := time.Date(2026, 5, 1, 0, 0, 0, 0, location)
	newExpiry := time.Date(2026, 5, 16, 0, 0, 0, 0, location)

	// Fixed legacy source: Xboard 8065164da6cd55be6015bdc8c6bc8811a67404de,
	// app/Services/OrderService.php, getSurplusValue/setOrderType/open.
	// The completed old order contributes total + balance + surplus - credit.
	// April has 30 days, with 15 days left: (int)(3001 * 15/30) = 1500 cents.
	// The 700-cent replacement consumes 700 and credits 800 only on completion;
	// the existing 137-cent wallet must therefore become 937, not 800 or 1737.
	const oldPrice, newPrice = int64(3_001), int64(700)
	const retainedBalance, wantSurplus, wantCredit, wantBalance = int64(137), int64(1_500), int64(800), int64(937)
	oldPlan, userID := createOrderFixture(t, database, paidAt, PlanPrices{"monthly": oldPrice}, nil)
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET balance = ? WHERE id = ?`, oldPrice+retainedBalance, userID); err != nil {
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
	newPlan, err := database.CreatePlan(ctx, SavePlanInput{
		Name: "Cheaper recurring plan", GroupID: oldPlan.GroupID, TransferEnableGiB: 50,
		Prices: PlanPrices{"monthly": newPrice},
	}, paidAt)
	if err != nil {
		t.Fatal(err)
	}
	newPlan, err = database.SetPlanState(ctx, newPlan.ID, newPlan.Revision, PlanState{Show: true, Sell: true, Renew: true}, paidAt)
	if err != nil {
		t.Fatal(err)
	}

	oldOrder, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: oldPlan.ID, Period: "monthly"}, paidAt)
	if err != nil {
		t.Fatal(err)
	}
	if oldOrder.Type != OrderTypeNew || oldOrder.Status != OrderStatusPending || oldOrder.OriginalAmount != oldPrice ||
		oldOrder.TotalAmount != 0 || oldOrder.BalanceAmount != oldPrice || oldOrder.SurplusAmount != 0 || oldOrder.SurplusCredit != 0 {
		t.Fatalf("old purchase did not preserve the balance-funded price: %+v", oldOrder)
	}
	oldOrder, err = database.CompleteOrder(ctx, oldOrder.TradeNo, "old-balance-paid", paidAt)
	if err != nil {
		t.Fatal(err)
	}
	if oldOrder.Status != OrderStatusCompleted || oldOrder.PaidAt == nil || !oldOrder.PaidAt.Equal(paidAt) {
		t.Fatalf("old purchase was not completed at the fixed start: %+v", oldOrder)
	}
	assertWalletAndEntitlement := func(wantWallet, wantPlanID int64, wantExpiry time.Time) {
		t.Helper()
		var wallet, planID, expiry int64
		if err := database.db.QueryRowContext(ctx, `SELECT balance, plan_id, expired_at FROM users WHERE id = ?`, userID).Scan(&wallet, &planID, &expiry); err != nil {
			t.Fatal(err)
		}
		if wallet != wantWallet || planID != wantPlanID || expiry != wantExpiry.Unix() {
			t.Fatalf("wallet/entitlement = %d/%d/%d, want %d/%d/%d", wallet, planID, expiry, wantWallet, wantPlanID, wantExpiry.Unix())
		}
	}
	assertWalletAndEntitlement(retainedBalance, oldPlan.ID, oldExpiry)

	changeOrder, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: newPlan.ID, Period: "monthly"}, changeAt)
	if err != nil {
		t.Fatal(err)
	}
	assertChangeOrder := func(order Order, wantStatus OrderStatus) {
		t.Helper()
		if order.ID != changeOrder.ID || order.Type != OrderTypeUpgrade || order.Status != wantStatus ||
			order.OriginalAmount != newPrice || order.TotalAmount != 0 || order.BalanceAmount != 0 ||
			order.SurplusAmount != wantSurplus || order.SurplusCredit != wantCredit ||
			len(order.SurplusOrderIDs) != 1 || order.SurplusOrderIDs[0] != oldOrder.ID {
			t.Fatalf("plan-change order does not match the fixed legacy surplus contract: %+v", order)
		}
	}
	assertPersistedOrders := func(wantOldStatus, wantChangeStatus OrderStatus) {
		t.Helper()
		storedOld, err := database.GetUserOrder(ctx, userID, oldOrder.TradeNo)
		if err != nil {
			t.Fatal(err)
		}
		if storedOld.Status != wantOldStatus || storedOld.TotalAmount != 0 || storedOld.BalanceAmount != oldPrice ||
			storedOld.SurplusCredit != 0 || storedOld.PaidAt == nil || !storedOld.PaidAt.Equal(paidAt) {
			t.Fatalf("old order status/funding facts changed incorrectly: %+v", storedOld)
		}
		storedChange, err := database.GetUserOrder(ctx, userID, changeOrder.TradeNo)
		if err != nil {
			t.Fatal(err)
		}
		assertChangeOrder(storedChange, wantChangeStatus)
	}
	assertChangeOrder(changeOrder, OrderStatusPending)
	assertPersistedOrders(OrderStatusCompleted, OrderStatusPending)
	assertWalletAndEntitlement(retainedBalance, oldPlan.ID, oldExpiry)

	completed, err := database.CompleteOrder(ctx, changeOrder.TradeNo, "plan-change-paid", changeAt)
	if err != nil {
		t.Fatal(err)
	}
	assertChangeOrder(completed, OrderStatusCompleted)
	assertPersistedOrders(OrderStatusDiscounted, OrderStatusCompleted)
	assertWalletAndEntitlement(wantBalance, newPlan.ID, newExpiry)
	if completed.PaidAt == nil || !completed.PaidAt.Equal(changeAt) || completed.CallbackNo != "plan-change-paid" {
		t.Fatalf("plan-change completion receipt = %+v", completed)
	}

	replayed, err := database.CompleteOrder(ctx, changeOrder.TradeNo, "replayed-completion", changeAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	assertChangeOrder(replayed, OrderStatusCompleted)
	assertPersistedOrders(OrderStatusDiscounted, OrderStatusCompleted)
	assertWalletAndEntitlement(wantBalance, newPlan.ID, newExpiry)
	if replayed.PaidAt == nil || !replayed.PaidAt.Equal(changeAt) || replayed.CallbackNo != "plan-change-paid" {
		t.Fatalf("duplicate completion changed the original receipt: %+v", replayed)
	}
	var orderCount int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE user_id = ?`, userID).Scan(&orderCount); err != nil {
		t.Fatal(err)
	}
	if orderCount != 2 {
		t.Fatalf("completed plan change persisted %d orders, want the two original business facts", orderCount)
	}
}
