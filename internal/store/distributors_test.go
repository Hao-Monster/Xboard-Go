package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCreateDistributorOrderBuildsIndependentCompletedSubscription(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	plan, distributor := createDistributorFixture(t, database, now)

	created, err := database.CreateDistributorOrder(ctx, CreateDistributorOrderInput{
		DistributorUserID: distributor.ID, PlanID: plan.ID, Period: "month_price",
	}, now)
	if err != nil {
		t.Fatalf("CreateDistributorOrder() error = %v", err)
	}
	if created.Order.Status != OrderStatusCompleted || created.Order.Type != OrderTypeNew || created.Order.PaidAt != nil ||
		created.Order.TotalAmount != 100_000 || created.Order.OriginalAmount != 100_000 || created.Order.DistributorOrderID == nil ||
		*created.Order.DistributorOrderID != created.Subscription.ID || created.Subscription.CustomerName != nil ||
		created.Subscription.DeliveryStatus != DistributorDeliveryPending || created.Subscription.SettlementStatus != DistributorSettlementUnsettled {
		t.Fatalf("created distributor order = %#v", created)
	}
	if created.Subscription.SubscriptionToken == "" || created.Subscription.SubscriberUUID == "" || created.Subscription.ClaimToken == "" {
		t.Fatalf("created secret identities were not generated: %#v", created.Subscription)
	}
	account, err := database.FindSubscriptionAccount(ctx, created.Subscription.SubscriptionToken)
	if err != nil || account.ID != created.Subscription.SubscriberUserID {
		t.Fatalf("internal subscription token lookup = %#v err=%v", account, err)
	}
	encoded, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), created.Subscription.SubscriptionToken) || strings.Contains(string(encoded), created.Subscription.SubscriberUUID) ||
		strings.Contains(string(encoded), created.Subscription.ClaimToken) {
		t.Fatalf("serialized distributor response exposed a secret: %s", encoded)
	}
	var kind string
	var planID, transferEnable, expiredAt int64
	var paidAt any
	if err := database.db.QueryRowContext(ctx, `
		SELECT u.account_kind,u.plan_id,u.transfer_enable,u.expired_at,o.paid_at
		FROM users u JOIN orders o ON o.id = ? WHERE u.id = ?
	`, created.Order.ID, created.Subscription.SubscriberUserID).Scan(&kind, &planID, &transferEnable, &expiredAt, &paidAt); err != nil {
		t.Fatal(err)
	}
	if kind != AccountKindInternalSubscription || planID != plan.ID || transferEnable != plan.TransferEnableGiB*bytesPerGiB || paidAt != nil ||
		expiredAt <= now.Unix() {
		t.Fatalf("internal subscription kind=%q plan=%d transfer=%d expiry=%d paid=%v", kind, planID, transferEnable, expiredAt, paidAt)
	}
	if _, err := database.GetAdminUser(ctx, created.Subscription.SubscriberUserID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("internal subscription leaked into user directory: %v", err)
	}

	second, err := database.CreateDistributorOrder(ctx, CreateDistributorOrderInput{
		DistributorUserID: distributor.ID, PlanID: plan.ID, Period: "monthly", CustomerName: pointerTo("  客户甲  "),
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if second.Subscription.SubscriberUserID == created.Subscription.SubscriberUserID ||
		second.Subscription.SubscriptionToken == created.Subscription.SubscriptionToken ||
		second.Subscription.SubscriberUUID == created.Subscription.SubscriberUUID ||
		second.Subscription.CustomerName == nil || *second.Subscription.CustomerName != "客户甲" {
		t.Fatalf("second purchase was not independent: first=%#v second=%#v", created.Subscription, second.Subscription)
	}
}

func TestDistributorSubscriptionConsumesPlanCapacityWithoutInflatingHumanUserCounts(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	plan, distributor := createDistributorFixture(t, database, now)
	if _, err := database.db.ExecContext(ctx, `UPDATE plans SET capacity_limit = 1 WHERE id = ?`, plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateDistributorOrder(ctx, CreateDistributorOrderInput{
		DistributorUserID: distributor.ID, PlanID: plan.ID, Period: "monthly",
	}, now); err != nil {
		t.Fatal(err)
	}
	offers, err := database.ListGuestPlanOffers(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 0 {
		t.Fatalf("capacity-exhausted distributor plan still offered: %#v", offers)
	}
	plans, err := database.ListPlans(ctx, now)
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans=%#v err=%v", plans, err)
	}
	if plans[0].UsersCount != 0 || plans[0].ActiveUsersCount != 0 || plans[0].CapacityUsersCount != 1 {
		t.Fatalf("plan counts=%#v, want human=0 active=0 capacity=1", plans[0])
	}
}

func TestRenewDistributorOrderIsNoOverflowOwnedAndIdempotent(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC)
	plan, distributor := createDistributorFixture(t, database, now)
	created, err := database.CreateDistributorOrder(ctx, CreateDistributorOrderInput{
		DistributorUserID: distributor.ID, PlanID: plan.ID, Period: "monthly",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	location := mustLocation(t, trafficResetLocationID)
	before := time.Date(2026, time.January, 31, 16, 30, 0, 0, location)
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET expired_at = ? WHERE id = ?`, before.Unix(), created.Subscription.SubscriberUserID); err != nil {
		t.Fatal(err)
	}
	idempotencyKey := "1e8db8ab-f8d2-46cb-9534-7b2d37f4e5b9" // gitleaks:allow -- deterministic UUID fixture
	renewed, err := database.RenewDistributorOrder(ctx, RenewDistributorOrderInput{
		DistributorUserID: distributor.ID, TradeNo: created.Order.TradeNo, Period: "month_price", IdempotencyKey: idempotencyKey,
	}, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("RenewDistributorOrder() error = %v", err)
	}
	wantAfter := time.Date(2026, time.February, 28, 16, 30, 0, 0, location)
	if renewed.Order.Type != OrderTypeRenewal || renewed.Order.Status != OrderStatusCompleted || renewed.Order.PaidAt != nil ||
		renewed.Order.EntitlementExpiredAtBefore == nil || renewed.Order.EntitlementExpiredAtAfter == nil ||
		!renewed.Order.EntitlementExpiredAtBefore.Equal(before) || !renewed.Order.EntitlementExpiredAtAfter.Equal(wantAfter) ||
		renewed.Subscription.ID != created.Subscription.ID || renewed.Subscription.SubscriptionToken != created.Subscription.SubscriptionToken {
		t.Fatalf("renewed order = %#v", renewed)
	}
	replayed, err := database.RenewDistributorOrder(ctx, RenewDistributorOrderInput{
		DistributorUserID: distributor.ID, TradeNo: created.Order.TradeNo, Period: "monthly", IdempotencyKey: idempotencyKey,
	}, now.Add(2*time.Hour))
	if err != nil || replayed.Order.ID != renewed.Order.ID {
		t.Fatalf("idempotent replay = %#v err=%v", replayed, err)
	}
	if _, err := database.RenewDistributorOrder(ctx, RenewDistributorOrderInput{
		DistributorUserID: distributor.ID, TradeNo: created.Order.TradeNo, Period: "quarterly", IdempotencyKey: idempotencyKey,
	}, now.Add(3*time.Hour)); !errors.Is(err, ErrDistributorRenewalMismatch) {
		t.Fatalf("mismatched idempotency error = %v", err)
	}

	other, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "other-dealer@example.test", PasswordHash: "hash", IsDistributor: true, DistributorName: "其他渠道",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RenewDistributorOrder(ctx, RenewDistributorOrderInput{
		DistributorUserID: other.ID, TradeNo: created.Order.TradeNo, Period: "monthly",
		IdempotencyKey: "6a441c5e-f645-4317-8e77-ed9d2bce2dbf", // gitleaks:allow -- deterministic UUID fixture
	}, now.Add(4*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner renewal error = %v", err)
	}
}

func TestDistributorSettlementIsPerFinancialOrderAndRepeatSafe(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, distributor := createDistributorFixture(t, database, now)
	created, err := database.CreateDistributorOrder(ctx, CreateDistributorOrderInput{DistributorUserID: distributor.ID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RenewDistributorOrder(ctx, RenewDistributorOrderInput{
		DistributorUserID: distributor.ID, TradeNo: created.Order.TradeNo, Period: "monthly",
		IdempotencyKey: "c5e79342-f17a-4bd4-99b4-6e42da7845ef", // gitleaks:allow -- deterministic UUID fixture
	}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "settlement-other@example.test", PasswordHash: "hash", IsDistributor: true, DistributorName: "其他渠道"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateDistributorOrder(ctx, CreateDistributorOrderInput{DistributorUserID: other.ID, PlanID: plan.ID, Period: "monthly"}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "settlement-admin@example.test", PasswordHash: "hash", IsAdmin: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := database.PreviewDistributorSettlement(ctx, distributor.ID)
	if err != nil || preview.Count != 2 || preview.TotalAmount != 200_000 || preview.SettledAt != nil {
		t.Fatalf("settlement preview = %#v err=%v", preview, err)
	}
	settledAt := now.Add(3 * time.Minute)
	settled, err := database.SettleDistributorOrders(ctx, distributor.ID, administrator.ID, settledAt)
	if err != nil || settled.Count != 2 || settled.TotalAmount != 200_000 || settled.SettledAt == nil || !settled.SettledAt.Equal(settledAt) {
		t.Fatalf("settlement result = %#v err=%v", settled, err)
	}
	var unpaid, legacyStatus int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE user_id = ? AND distributor_order_id IS NOT NULL AND paid_at IS NULL`, distributor.ID).Scan(&unpaid); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT settlement_status FROM distributor_subscriptions WHERE id = ?`, created.Subscription.ID).Scan(&legacyStatus); err != nil {
		t.Fatal(err)
	}
	if unpaid != 0 || legacyStatus != int(DistributorSettlementSettled) {
		t.Fatalf("settled state unpaid=%d legacy=%d", unpaid, legacyStatus)
	}
	repeated, err := database.SettleDistributorOrders(ctx, distributor.ID, administrator.ID, settledAt.Add(time.Minute))
	if err != nil || repeated.Count != 0 || repeated.TotalAmount != 0 || repeated.SettledAt != nil {
		t.Fatalf("repeat settlement = %#v err=%v", repeated, err)
	}
	otherPreview, err := database.PreviewDistributorSettlement(ctx, other.ID)
	if err != nil || otherPreview.Count != 1 || otherPreview.TotalAmount != 100_000 {
		t.Fatalf("other distributor settlement = %#v err=%v", otherPreview, err)
	}
}

func TestDistributorEntitlementRemarkClaimAndHWIDLifecycle(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 14, 0, 0, 0, time.UTC)
	plan, distributor := createDistributorFixture(t, database, now)
	created, err := database.CreateDistributorOrder(ctx, CreateDistributorOrderInput{DistributorUserID: distributor.ID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	remark, err := database.UpdateDistributorRemark(ctx, created.Order.ID, pointerTo("  已核对客户  "), now.Add(time.Minute))
	if err != nil || remark == nil || *remark != "已核对客户" {
		t.Fatalf("updated remark = %v err=%v", remark, err)
	}
	expires := now.AddDate(0, 2, 0)
	entitlement, err := database.UpdateDistributorEntitlement(ctx, created.Order.ID, UpdateDistributorEntitlementInput{
		TransferEnable: 200 * bytesPerGiB, ExpiredAt: &expires, SpeedLimit: 300, DeviceLimit: 5,
	}, now.Add(2*time.Minute))
	if err != nil || entitlement.TransferEnable != 200*bytesPerGiB || entitlement.ExpiredAt == nil || !entitlement.ExpiredAt.Equal(expires) ||
		entitlement.SpeedLimit != 300 || entitlement.DeviceLimit != 5 {
		t.Fatalf("updated entitlement = %#v err=%v", entitlement, err)
	}
	settings, err := database.UpdateDistributorHWIDSettings(ctx, created.Order.ID, true, 1, now.Add(3*time.Minute))
	if err != nil || !settings.Enabled || settings.Limit != 1 || settings.RegisteredCount != 0 {
		t.Fatalf("hwid settings = %#v err=%v", settings, err)
	}
	first := AuthorizeDistributorHWIDInput{
		SubscriberUserID: created.Subscription.SubscriberUserID, HWID: "ABCDEFGHIJ123456", DeviceOS: "Windows",
		OSVersion: "11", DeviceModel: "Desktop", UserAgent: "xboard-client/1", IPAddress: "192.0.2.10",
	}
	authorized, err := database.AuthorizeDistributorHWID(ctx, first, now.Add(4*time.Minute))
	if err != nil || !authorized.Allowed || !authorized.Enabled || authorized.LimitReached || authorized.NotSupported {
		t.Fatalf("first hwid authorization = %#v err=%v", authorized, err)
	}
	first.DeviceModel = "Updated desktop"
	if replayed, err := database.AuthorizeDistributorHWID(ctx, first, now.Add(5*time.Minute)); err != nil || !replayed.Allowed {
		t.Fatalf("known hwid authorization = %#v err=%v", replayed, err)
	}
	blocked, err := database.AuthorizeDistributorHWID(ctx, AuthorizeDistributorHWIDInput{
		SubscriberUserID: created.Subscription.SubscriberUserID, HWID: "ZYXWVUTSRQ654321",
	}, now.Add(6*time.Minute))
	if err != nil || blocked.Allowed || !blocked.LimitReached {
		t.Fatalf("overflow hwid authorization = %#v err=%v", blocked, err)
	}
	devices, err := database.ListDistributorHWIDDevices(ctx, created.Order.ID, "ABC")
	if err != nil || len(devices) != 1 || devices[0].DeviceModel == nil || *devices[0].DeviceModel != "Updated desktop" {
		t.Fatalf("hwid devices = %#v err=%v", devices, err)
	}
	if deleted, err := database.DeleteDistributorHWIDDevice(ctx, created.Order.ID, devices[0].ID); err != nil || !deleted {
		t.Fatalf("delete hwid = %t err=%v", deleted, err)
	}

	claim, err := database.ClaimDistributorSubscription(ctx, created.Subscription.ClaimToken, "198.51.100.3", "claim-client", now.Add(7*time.Minute))
	if err != nil || claim.SubscriptionToken != created.Subscription.SubscriptionToken || claim.OriginalTradeNo != created.Order.TradeNo {
		t.Fatalf("claim = %#v err=%v", claim, err)
	}
	if _, err := database.ClaimDistributorSubscription(ctx, created.Subscription.ClaimToken, "198.51.100.3", "claim-client", now.Add(8*time.Minute)); !errors.Is(err, ErrDistributorClaimConsumed) {
		t.Fatalf("repeated claim error = %v", err)
	}
}

func TestDistributorOrderListSearchOwnershipAndClose(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 16, 0, 0, 0, time.UTC)
	plan, distributor := createDistributorFixture(t, database, now)
	created, err := database.CreateDistributorOrder(ctx, CreateDistributorOrderInput{
		DistributorUserID: distributor.ID, PlanID: plan.ID, Period: "monthly", CustomerName: pointerTo("客户乙"),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := database.RenewDistributorOrder(ctx, RenewDistributorOrderInput{
		DistributorUserID: distributor.ID, TradeNo: created.Order.TradeNo, Period: "quarterly",
		IdempotencyKey: "893d0f39-c856-42c9-a13a-310c792632bc", // gitleaks:allow -- deterministic UUID fixture
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	page, err := database.ListDistributorOrders(ctx, DistributorOrderFilter{DistributorUserID: &distributor.ID, PageSize: 20}, now.Add(2*time.Minute))
	if err != nil || page.Total != 2 || len(page.Items) != 2 || page.Items[0].Order.ID != created.Order.ID || !page.Items[0].IsSubscriptionOrigin ||
		page.Items[1].Order.ID != renewed.Order.ID || page.Items[1].IsSubscriptionOrigin || !page.Items[0].CanViewSubscriptionQR ||
		!page.Items[0].CanRenew || page.Items[0].Entitlement.PlanName != plan.Name {
		t.Fatalf("distributor page = %#v err=%v", page, err)
	}
	searched, err := database.ListDistributorOrders(ctx, DistributorOrderFilter{
		DistributorUserID: &distributor.ID, Search: created.Order.TradeNo, PageSize: 20,
	}, now.Add(2*time.Minute))
	if err != nil || searched.Total != 2 {
		t.Fatalf("root trade search = %#v err=%v", searched, err)
	}
	customer, err := database.ListDistributorOrders(ctx, DistributorOrderFilter{
		DistributorUserID: &distributor.ID, Search: "客户乙", PageSize: 20,
	}, now.Add(2*time.Minute))
	if err != nil || customer.Total != 2 {
		t.Fatalf("customer search = %#v err=%v", customer, err)
	}
	closed, err := database.CloseDistributorDelivery(ctx, distributor.ID, created.Order.TradeNo, now.Add(3*time.Minute))
	if err != nil || closed.Subscription.DeliveryStatus != DistributorDeliveryClosed || closed.Subscription.ClosedAt == nil || closed.CanRenew {
		t.Fatalf("closed delivery = %#v err=%v", closed, err)
	}
	if _, err := database.ClaimDistributorSubscription(ctx, created.Subscription.ClaimToken, "", "", now.Add(4*time.Minute)); !errors.Is(err, ErrDistributorClaimConsumed) {
		t.Fatalf("closed claim error = %v", err)
	}
}

func TestConcurrentDistributorMutationsRemainAtomicAcrossConnections(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "distributors.db")
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
	now := time.Date(2026, 8, 26, 18, 0, 0, 0, time.UTC)
	plan, distributor := createDistributorFixture(t, first, now)
	created, err := first.CreateDistributorOrder(ctx, CreateDistributorOrderInput{
		DistributorUserID: distributor.ID, PlanID: plan.ID, Period: "monthly",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	administrator, err := first.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "concurrent-distributor-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.UpdateDistributorHWIDSettings(ctx, created.Order.ID, true, 1, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	hwidResults := make(chan struct {
		value DistributorHWIDAuthorization
		err   error
	}, 2)
	var wait sync.WaitGroup
	for index, database := range []*Store{first, second} {
		wait.Add(1)
		go func(index int, database *Store) {
			defer wait.Done()
			<-start
			value, authorizeErr := database.AuthorizeDistributorHWID(ctx, AuthorizeDistributorHWIDInput{
				SubscriberUserID: created.Subscription.SubscriberUserID,
				HWID:             []string{"CONCURRENTDEVICE01", "CONCURRENTDEVICE02"}[index],
			}, now.Add(2*time.Second))
			hwidResults <- struct {
				value DistributorHWIDAuthorization
				err   error
			}{value: value, err: authorizeErr}
		}(index, database)
	}
	close(start)
	wait.Wait()
	close(hwidResults)
	var allowed, limited int
	for result := range hwidResults {
		if result.err != nil {
			t.Fatalf("concurrent AuthorizeDistributorHWID() error = %v", result.err)
		}
		if result.value.Allowed {
			allowed++
		}
		if result.value.LimitReached {
			limited++
		}
	}
	if allowed != 1 || limited != 1 {
		t.Fatalf("concurrent HWID outcomes allowed=%d limited=%d", allowed, limited)
	}

	renewInput := RenewDistributorOrderInput{
		DistributorUserID: distributor.ID, TradeNo: created.Order.TradeNo, Period: "monthly",
		IdempotencyKey: "f8f4fa74-8970-4ff5-bf95-ddf64dc750bc", // gitleaks:allow -- deterministic UUID fixture
	}
	start = make(chan struct{})
	renewResults := make(chan struct {
		value DistributorOrder
		err   error
	}, 2)
	for _, database := range []*Store{first, second} {
		wait.Add(1)
		go func(database *Store) {
			defer wait.Done()
			<-start
			value, renewErr := database.RenewDistributorOrder(ctx, renewInput, now.Add(time.Minute))
			renewResults <- struct {
				value DistributorOrder
				err   error
			}{value: value, err: renewErr}
		}(database)
	}
	close(start)
	wait.Wait()
	close(renewResults)
	var renewalID int64
	for result := range renewResults {
		if result.err != nil {
			t.Fatalf("concurrent RenewDistributorOrder() error = %v", result.err)
		}
		if renewalID == 0 {
			renewalID = result.value.Order.ID
		} else if result.value.Order.ID != renewalID {
			t.Fatalf("concurrent renewal IDs = %d and %d", renewalID, result.value.Order.ID)
		}
	}

	start = make(chan struct{})
	settlementResults := make(chan struct {
		value DistributorSettlementSummary
		err   error
	}, 2)
	for _, database := range []*Store{first, second} {
		wait.Add(1)
		go func(database *Store) {
			defer wait.Done()
			<-start
			value, settleErr := database.SettleDistributorOrders(ctx, distributor.ID, administrator.ID, now.Add(2*time.Minute))
			settlementResults <- struct {
				value DistributorSettlementSummary
				err   error
			}{value: value, err: settleErr}
		}(database)
	}
	close(start)
	wait.Wait()
	close(settlementResults)
	var settledCount int64
	for result := range settlementResults {
		if result.err != nil {
			t.Fatalf("concurrent SettleDistributorOrders() error = %v", result.err)
		}
		settledCount += result.value.Count
	}
	if settledCount != 2 {
		t.Fatalf("concurrent settled count = %d, want original plus renewal", settledCount)
	}
	var renewalRows, deviceRows, unsettledRows int
	if err := first.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE distributor_idempotency_key = ?`, renewInput.IdempotencyKey).Scan(&renewalRows); err != nil {
		t.Fatal(err)
	}
	if err := first.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM distributor_hwid_devices WHERE subscription_id = ?`, created.Subscription.ID).Scan(&deviceRows); err != nil {
		t.Fatal(err)
	}
	if err := first.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE user_id = ? AND distributor_order_id IS NOT NULL AND paid_at IS NULL`, distributor.ID).Scan(&unsettledRows); err != nil {
		t.Fatal(err)
	}
	if renewalRows != 1 || deviceRows != 1 || unsettledRows != 0 {
		t.Fatalf("concurrent state renewal=%d devices=%d unsettled=%d", renewalRows, deviceRows, unsettledRows)
	}
}

func createDistributorFixture(t testing.TB, database *Store, now time.Time) (Plan, AdminUser) {
	t.Helper()
	group, err := database.CreateServerGroup(context.Background(), "Distributor group", now)
	if err != nil {
		t.Fatal(err)
	}
	speed, devices, capacity := 200, 3, 100
	plan, err := database.CreatePlan(context.Background(), SavePlanInput{
		Name: "Distributor plan", GroupID: &group.ID, TransferEnableGiB: 100,
		SpeedLimit: &speed, DeviceLimit: &devices, CapacityLimit: &capacity,
		Prices: PlanPrices{"monthly": 100_000, "quarterly": 270_000, "onetime": 1_000_000},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = database.SetPlanState(context.Background(), plan.ID, plan.Revision, PlanState{Show: true, Sell: true, Renew: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	distributor, err := database.CreateAdminUser(context.Background(), CreateAdminUserInput{
		Email: "dealer@example.test", PasswordHash: "hash", IsDistributor: true, DistributorName: "华东渠道",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	return plan, distributor
}
