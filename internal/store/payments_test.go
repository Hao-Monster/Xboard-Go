package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestStartPaymentCheckoutCalculatesFeeBindsOrderAndReusesCreatedAttempt(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 100_000}, nil)
	method, err := database.CreatePayment(ctx, SavePaymentInput{
		Provider: PaymentProviderCoinPayments, Name: "CoinPayments", ConfigCiphertext: []byte("ciphertext"),
		HandlingFeeFixed: 123, HandlingFeeBasisPoints: 250, Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	started, err := database.StartPaymentCheckout(ctx, StartPaymentCheckoutInput{UserID: userID, TradeNo: order.TradeNo, PaymentID: method.ID}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if started.Cached || started.Attempt.ExpectedAmount != 102_623 || started.Order.HandlingAmount == nil || *started.Order.HandlingAmount != 2_623 ||
		started.Order.PaymentID == nil || *started.Order.PaymentID != method.ID || len(started.Attempt.IdempotencyKey) != 64 {
		t.Fatalf("StartPaymentCheckout() = %#v", started)
	}
	if _, err := database.StartPaymentCheckout(ctx, StartPaymentCheckoutInput{UserID: userID, TradeNo: order.TradeNo, PaymentID: method.ID}, now.Add(2*time.Second)); !errors.Is(err, ErrPaymentInProgress) {
		t.Fatalf("concurrent StartPaymentCheckout() error = %v, want ErrPaymentInProgress", err)
	}
	completed, err := database.CompletePaymentCheckout(ctx, started.Attempt.ID, started.Attempt.IdempotencyKey, 1,
		"https://checkout.example.test/pay/one", "invoice-one", now.Add(3*time.Second))
	if err != nil || completed.Status != PaymentCheckoutCreated {
		t.Fatalf("CompletePaymentCheckout() = (%#v, %v)", completed, err)
	}
	if _, err := database.CancelOrder(ctx, userID, order.TradeNo, now.Add(3*time.Second)); !errors.Is(err, ErrPaymentInProgress) {
		t.Fatalf("CancelOrder(active payment checkout) error = %v, want ErrPaymentInProgress", err)
	}
	if _, err := database.CancelAdminOrder(ctx, order.TradeNo, now.Add(3*time.Second)); !errors.Is(err, ErrPaymentInProgress) {
		t.Fatalf("CancelAdminOrder(active payment checkout) error = %v, want ErrPaymentInProgress", err)
	}
	method, err = database.UpdatePayment(ctx, method.ID, method.Revision, SavePaymentInput{
		Provider: method.Provider, Name: method.Name, ConfigCiphertext: method.ConfigCiphertext,
		HandlingFeeFixed: 999, HandlingFeeBasisPoints: method.HandlingFeeBasisPoints, Enabled: true,
	}, now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	reused, err := database.StartPaymentCheckout(ctx, StartPaymentCheckoutInput{UserID: userID, TradeNo: order.TradeNo, PaymentID: method.ID}, now.Add(4*time.Second))
	if err != nil || !reused.Cached || reused.Attempt.ResponseData != completed.ResponseData || reused.Attempt.IdempotencyKey != started.Attempt.IdempotencyKey {
		t.Fatalf("cached StartPaymentCheckout() = (%#v, %v)", reused, err)
	}
	if reused.Order.HandlingAmount == nil || *reused.Order.HandlingAmount != 2_623 || reused.Attempt.ExpectedAmount != 102_623 {
		t.Fatalf("cached checkout price changed after fee edit: %#v", reused)
	}
	secondMethod, err := database.CreatePayment(ctx, SavePaymentInput{
		Provider: PaymentProviderEPay, Name: "EPay", ConfigCiphertext: []byte("ciphertext-two"), Enabled: true,
	}, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	secondStarted, err := database.StartPaymentCheckout(ctx, StartPaymentCheckoutInput{UserID: userID, TradeNo: order.TradeNo, PaymentID: secondMethod.ID}, now.Add(6*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CompletePaymentCheckout(ctx, secondStarted.Attempt.ID, secondStarted.Attempt.IdempotencyKey, 1,
		"https://checkout.example.test/pay/two", "invoice-two", now.Add(7*time.Second)); err != nil {
		t.Fatal(err)
	}
	rebound, err := database.StartPaymentCheckout(ctx, StartPaymentCheckoutInput{UserID: userID, TradeNo: order.TradeNo, PaymentID: method.ID}, now.Add(8*time.Second))
	if err != nil || !rebound.Cached || rebound.Order.PaymentID == nil || *rebound.Order.PaymentID != method.ID ||
		rebound.Order.HandlingAmount == nil || *rebound.Order.HandlingAmount != 2_623 {
		t.Fatalf("rebound cached StartPaymentCheckout() = (%#v, %v)", rebound, err)
	}
}

func TestDisabledTrustedPaymentPluginIsHiddenAndCannotStartCheckout(t *testing.T) {
	tests := []struct {
		provider PaymentProvider
		code     string
	}{
		{PaymentProviderAlipayF2F, TrustedPluginAlipayF2F},
		{PaymentProviderBTCPay, TrustedPluginBTCPay},
		{PaymentProviderCoinPayments, TrustedPluginCoinPayments},
		{PaymentProviderCoinbase, TrustedPluginCoinbase},
		{PaymentProviderEPay, TrustedPluginEPay},
		{PaymentProviderMGate, TrustedPluginMGate},
	}
	for _, test := range tests {
		t.Run(string(test.provider), func(t *testing.T) {
			database := newTestStore(t)
			ctx := context.Background()
			now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
			plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 1_000}, nil)
			method, err := database.CreatePayment(ctx, SavePaymentInput{
				Provider: test.provider, Name: string(test.provider), ConfigCiphertext: []byte("ciphertext"), Enabled: true,
			}, now)
			if err != nil {
				t.Fatal(err)
			}
			order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now)
			if err != nil {
				t.Fatal(err)
			}
			started, err := database.StartPaymentCheckout(ctx, StartPaymentCheckoutInput{
				UserID: userID, TradeNo: order.TradeNo, PaymentID: method.ID,
			}, now.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
				Email: "payment-plugin-admin@example.test", PasswordHash: "hash", IsAdmin: true,
			}, now)
			if err != nil {
				t.Fatal(err)
			}
			code, mapped := TrustedPluginCodeForPaymentProvider(test.provider)
			if !mapped || code != test.code {
				t.Fatalf("TrustedPluginCodeForPaymentProvider(%q)=(%q,%t), want (%q,true)", test.provider, code, mapped, test.code)
			}
			plugin, err := database.GetTrustedPlugin(ctx, test.code)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.UpdateTrustedPlugin(ctx, administrator.ID, plugin.Code, plugin.Revision, SaveTrustedPluginInput{
				Enabled: false, Config: plugin.Config,
			}, now.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			methods, err := database.ListEnabledPayments(ctx)
			if err != nil || len(methods) != 0 {
				t.Fatalf("ListEnabledPayments() = (%#v, %v), want empty", methods, err)
			}
			payload := sha256.Sum256([]byte("verified callback for " + test.code))
			completed, err := database.CompletePaymentWebhook(ctx, CompletePaymentWebhookInput{
				PaymentID: method.ID, Provider: method.Provider, ExternalID: "settled-" + test.code, TradeNo: order.TradeNo,
				Amount: started.Attempt.ExpectedAmount, Currency: "CNY", PayloadSHA256: fmt.Sprintf("%x", payload),
			}, now.Add(3*time.Second))
			if err != nil || completed.Status != OrderStatusCompleted {
				t.Fatalf("CompletePaymentWebhook(disabled %s) = (%#v, %v)", test.code, completed, err)
			}
			newOrder, err := database.CreateOrder(ctx, CreateOrderInput{
				UserID: userID, PlanID: plan.ID, Period: "monthly",
			}, now.Add(4*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.StartPaymentCheckout(ctx, StartPaymentCheckoutInput{
				UserID: userID, TradeNo: newOrder.TradeNo, PaymentID: method.ID,
			}, now.Add(5*time.Second)); !errors.Is(err, ErrPaymentUnavailable) {
				t.Fatalf("StartPaymentCheckout(disabled %s) error = %v, want ErrPaymentUnavailable", test.code, err)
			}
		})
	}
}

func TestPaymentProviderCannotChangeWhileCheckoutIsAwaitingCallback(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 1_000}, nil)
	method, err := database.CreatePayment(ctx, SavePaymentInput{
		Provider: PaymentProviderCoinPayments, Name: "CoinPayments", ConfigCiphertext: []byte("ciphertext"), Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.StartPaymentCheckout(ctx, StartPaymentCheckoutInput{
		UserID: userID, TradeNo: order.TradeNo, PaymentID: method.ID,
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	updated, err := database.UpdatePayment(ctx, method.ID, method.Revision, SavePaymentInput{
		Provider: method.Provider, Name: "Updated display name", ConfigCiphertext: method.ConfigCiphertext, Enabled: true,
	}, now.Add(2*time.Second))
	if err != nil || updated.Revision != method.Revision+1 {
		t.Fatalf("UpdatePayment(metadata with pending checkout) = (%#v, %v)", updated, err)
	}
	_, err = database.UpdatePayment(ctx, method.ID, updated.Revision, SavePaymentInput{
		Provider: method.Provider, Name: updated.Name, ConfigCiphertext: []byte("replacement-ciphertext"), Enabled: true,
	}, now.Add(3*time.Second))
	if !errors.Is(err, ErrPaymentConfigInUse) {
		t.Fatalf("UpdatePayment(config with pending checkout) error = %v, want ErrPaymentConfigInUse", err)
	}
	_, err = database.UpdatePayment(ctx, method.ID, updated.Revision, SavePaymentInput{
		Provider: PaymentProviderEPay, Name: "EPay", ConfigCiphertext: []byte("replacement-ciphertext"), Enabled: true,
	}, now.Add(3*time.Second))
	if !errors.Is(err, ErrPaymentConfigInUse) {
		t.Fatalf("UpdatePayment(provider with pending checkout) error = %v, want ErrPaymentConfigInUse", err)
	}
	payload := sha256.Sum256([]byte("verified callback"))
	if _, err := database.CompletePaymentWebhook(ctx, CompletePaymentWebhookInput{
		PaymentID: method.ID, Provider: method.Provider, ExternalID: "settled-payment", TradeNo: order.TradeNo,
		Amount: 1_000, Currency: "CNY", PayloadSHA256: fmt.Sprintf("%x", payload),
	}, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdatePayment(ctx, method.ID, updated.Revision, SavePaymentInput{
		Provider: PaymentProviderEPay, Name: "EPay", ConfigCiphertext: []byte("replacement-ciphertext"), Enabled: true,
	}, now.Add(5*time.Second)); !errors.Is(err, ErrPaymentConfigInUse) {
		t.Fatalf("UpdatePayment(provider after settlement) error = %v, want ErrPaymentConfigInUse", err)
	}
}

func TestFailedPaymentCheckoutDoesNotBlockOrderCancellation(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 1_000}, nil)
	method, err := database.CreatePayment(ctx, SavePaymentInput{
		Provider: PaymentProviderEPay, Name: "EPay", ConfigCiphertext: []byte("ciphertext"), Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	started, err := database.StartPaymentCheckout(ctx, StartPaymentCheckoutInput{
		UserID: userID, TradeNo: order.TradeNo, PaymentID: method.ID,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.FailPaymentCheckout(ctx, started.Attempt.ID, started.Attempt.IdempotencyKey, "provider_error", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	updated, err := database.UpdatePayment(ctx, method.ID, method.Revision, SavePaymentInput{
		Provider: PaymentProviderCoinbase, Name: "Coinbase", ConfigCiphertext: []byte("replacement-ciphertext"), Enabled: true,
	}, now.Add(3*time.Second))
	if err != nil || updated.Provider != PaymentProviderCoinbase {
		t.Fatalf("UpdatePayment(failed checkout) = (%#v, %v)", updated, err)
	}
	cancelled, err := database.CancelOrder(ctx, userID, order.TradeNo, now.Add(3*time.Second))
	if err != nil || cancelled.Status != OrderStatusCancelled {
		t.Fatalf("CancelOrder(failed payment checkout) = (%#v, %v)", cancelled, err)
	}
}

func TestPaymentCheckoutRejectsUnavailableForeignFreeAndOverflowingOrders(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(100, 0)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 100}, nil)
	method, err := database.CreatePayment(ctx, SavePaymentInput{Provider: PaymentProviderEPay, Name: "EPay", ConfigCiphertext: []byte("ciphertext")}, now)
	if err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]StartPaymentCheckoutInput{
		"disabled": {UserID: userID, TradeNo: order.TradeNo, PaymentID: method.ID},
		"foreign":  {UserID: userID + 999, TradeNo: order.TradeNo, PaymentID: method.ID},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := database.StartPaymentCheckout(ctx, input, now); !errors.Is(err, ErrPaymentUnavailable) && !errors.Is(err, ErrNotFound) {
				t.Fatalf("StartPaymentCheckout() error = %v", err)
			}
		})
	}
	if _, err := database.SetPaymentEnabled(ctx, method.ID, true, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE orders SET total_amount = 0 WHERE id = ?`, order.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.StartPaymentCheckout(ctx, StartPaymentCheckoutInput{UserID: userID, TradeNo: order.TradeNo, PaymentID: method.ID}, now); !errors.Is(err, ErrOrderState) {
		t.Fatalf("free StartPaymentCheckout() error = %v, want ErrOrderState", err)
	}
}

func TestCompletePaymentWebhookValidatesBindingAndIsExactlyOnceUnderConcurrency(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 100_000}, nil)
	method, err := database.CreatePayment(ctx, SavePaymentInput{
		Provider: PaymentProviderCoinbase, Name: "Coinbase", ConfigCiphertext: []byte("ciphertext"), Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	started, err := database.StartPaymentCheckout(ctx, StartPaymentCheckoutInput{UserID: userID, TradeNo: order.TradeNo, PaymentID: method.ID}, now)
	if err != nil {
		t.Fatal(err)
	}
	payload := sha256.Sum256([]byte("verified webhook body"))
	input := CompletePaymentWebhookInput{
		PaymentID: method.ID, Provider: method.Provider, ExternalID: "charge-one", TradeNo: order.TradeNo,
		Amount: started.Attempt.ExpectedAmount, Currency: "CNY", PayloadSHA256: fmt.Sprintf("%x", payload),
	}
	wrong := input
	wrong.Amount++
	if _, err := database.CompletePaymentWebhook(ctx, wrong, now); !errors.Is(err, ErrPaymentMismatch) {
		t.Fatalf("wrong amount webhook error = %v, want ErrPaymentMismatch", err)
	}
	if _, err := database.SetPaymentEnabled(ctx, method.ID, false, now); err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	errorsOut := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, completeErr := database.CompletePaymentWebhook(ctx, input, now.Add(time.Second))
			errorsOut <- completeErr
		}()
	}
	wait.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatalf("concurrent CompletePaymentWebhook() error = %v", err)
		}
	}
	changedPayload := input
	changedHash := sha256.Sum256([]byte("verified webhook retry with additional metadata"))
	changedPayload.PayloadSHA256 = fmt.Sprintf("%x", changedHash)
	if _, err := database.CompletePaymentWebhook(ctx, changedPayload, now.Add(2*time.Second)); err != nil {
		t.Fatalf("verified retry with unchanged business binding error = %v", err)
	}
	completed, err := database.GetUserOrder(ctx, userID, order.TradeNo)
	if err != nil || completed.Status != OrderStatusCompleted || completed.CallbackNo != input.ExternalID {
		t.Fatalf("completed order = (%#v, %v)", completed, err)
	}
	var receipts, events int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM payment_webhook_receipts WHERE order_id = ?`, order.ID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM order_entitlement_events WHERE order_id = ?`, order.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 || events != 1 {
		t.Fatalf("webhook receipts=%d entitlement events=%d, want 1/1", receipts, events)
	}
}

func TestPaymentWebhookSettlesAnEarlierCreatedMethodAfterUserSwitchesMethod(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 100_000}, nil)
	first, err := database.CreatePayment(ctx, SavePaymentInput{
		Provider: PaymentProviderCoinPayments, Name: "CoinPayments", ConfigCiphertext: []byte("ciphertext-one"),
		HandlingFeeFixed: 123, HandlingFeeBasisPoints: 250, Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.CreatePayment(ctx, SavePaymentInput{
		Provider: PaymentProviderEPay, Name: "EPay", ConfigCiphertext: []byte("ciphertext-two"),
		HandlingFeeFixed: 50, Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	firstAttempt, err := database.StartPaymentCheckout(ctx, StartPaymentCheckoutInput{
		UserID: userID, TradeNo: order.TradeNo, PaymentID: first.ID,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CompletePaymentCheckout(ctx, firstAttempt.Attempt.ID, firstAttempt.Attempt.IdempotencyKey,
		1, "https://checkout.example.test/first", "invoice-first", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.StartPaymentCheckout(ctx, StartPaymentCheckoutInput{
		UserID: userID, TradeNo: order.TradeNo, PaymentID: second.ID,
	}, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	payload := sha256.Sum256([]byte("first provider callback"))
	completed, err := database.CompletePaymentWebhook(ctx, CompletePaymentWebhookInput{
		PaymentID: first.ID, Provider: first.Provider, ExternalID: "first-settlement", TradeNo: order.TradeNo,
		Amount: firstAttempt.Attempt.ExpectedAmount, Currency: "CNY", PayloadSHA256: fmt.Sprintf("%x", payload),
	}, now.Add(4*time.Second))
	if err != nil {
		t.Fatalf("CompletePaymentWebhook(earlier method) error = %v", err)
	}
	if completed.Status != OrderStatusCompleted || completed.PaymentID == nil || *completed.PaymentID != first.ID ||
		completed.HandlingAmount == nil || *completed.HandlingAmount != 2_623 {
		t.Fatalf("completed order = %#v", completed)
	}
}

func TestPaymentCRUDOrderingVisibilityAndEncryptedConfigBoundary(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

	first, err := database.CreatePayment(ctx, SavePaymentInput{
		Provider: PaymentProviderCoinPayments, Name: "CoinPayments", Icon: "https://cdn.example.test/coin.svg",
		ConfigCiphertext: []byte("encrypted-provider-config-one"), NotifyDomain: "https://pay.example.test",
		HandlingFeeFixed: 123, HandlingFeeBasisPoints: 250, Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.CreatePayment(ctx, SavePaymentInput{
		Provider: PaymentProviderEPay, Name: "易支付", ConfigCiphertext: []byte("encrypted-provider-config-two"),
		HandlingFeeBasisPoints: 10_000,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if first.UUID == "" || first.UUID == second.UUID || first.SortPosition != 1 || second.SortPosition != 2 {
		t.Fatalf("created payments = %#v / %#v", first, second)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || containsBytes(encoded, first.ConfigCiphertext) {
		t.Fatalf("payment JSON leaked encrypted configuration: %s", encoded)
	}

	page, err := database.ListPayments(ctx, PaymentFilter{Page: 1, PageSize: 20, Query: "coin"})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != first.ID {
		t.Fatalf("ListPayments() = (%#v, %v)", page, err)
	}
	enabled, err := database.ListEnabledPayments(ctx)
	if err != nil || len(enabled) != 1 || enabled[0].ID != first.ID {
		t.Fatalf("ListEnabledPayments() = (%#v, %v)", enabled, err)
	}
	second, err = database.UpdatePayment(ctx, second.ID, second.Revision, SavePaymentInput{
		Provider: second.Provider, Name: "易支付更新", ConfigCiphertext: second.ConfigCiphertext,
		HandlingFeeFixed: 1, HandlingFeeBasisPoints: 1, Enabled: true,
	}, now.Add(2*time.Second))
	if err != nil || second.Revision != 2 {
		t.Fatalf("UpdatePayment() = (%#v, %v)", second, err)
	}
	if _, err := database.UpdatePayment(ctx, second.ID, 1, SavePaymentInput{
		Provider: second.Provider, Name: second.Name, ConfigCiphertext: second.ConfigCiphertext,
	}, now.Add(3*time.Second)); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale UpdatePayment() error = %v, want ErrRevisionConflict", err)
	}
	if err := database.ReorderPayments(ctx, []int64{second.ID, first.ID}, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	ordered, err := database.ListPayments(ctx, PaymentFilter{Page: 1, PageSize: 20})
	if err != nil || len(ordered.Items) != 2 || ordered.Items[0].ID != second.ID || ordered.Items[1].ID != first.ID {
		t.Fatalf("reordered payments = (%#v, %v)", ordered, err)
	}
	if err := database.ReorderPayments(ctx, []int64{first.ID}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("partial ReorderPayments() error = %v, want ErrInvalidInput", err)
	}
}

func TestPaymentFeeUsesOverflowSafeIntegerHalfUp(t *testing.T) {
	for _, test := range []struct {
		name       string
		amount     int64
		fixed      int64
		basisPoint int64
		want       int64
	}{
		{name: "legacy example", amount: 100_000, fixed: 123, basisPoint: 250, want: 2_623},
		{name: "half up", amount: 1, basisPoint: 5_000, want: 1},
		{name: "maximum percent", amount: maxOrderMoneyCents, basisPoint: 10_000, want: maxOrderMoneyCents},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := PaymentHandlingFee(test.amount, test.fixed, test.basisPoint)
			if err != nil || got != test.want {
				t.Fatalf("PaymentHandlingFee() = (%d, %v), want %d", got, err, test.want)
			}
		})
	}
	if _, err := PaymentHandlingFee(maxOrderMoneyCents, 1, 10_000); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("overflow PaymentHandlingFee() error = %v, want ErrInvalidInput", err)
	}
}

func TestPaymentDeletionAndOrderReferencesAreRestrictedInDatabase(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(100, 0)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 1_000}, nil)
	payment, err := database.CreatePayment(ctx, SavePaymentInput{
		Provider: PaymentProviderCoinbase, Name: "Coinbase", ConfigCiphertext: []byte("encrypted-config"), Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE orders SET payment_id = ? WHERE id = ?`, payment.ID, order.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.DeletePayment(ctx, payment.ID); !errors.Is(err, ErrPaymentReferenced) {
		t.Fatalf("DeletePayment(referenced) error = %v, want ErrPaymentReferenced", err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM payments WHERE id = ?`, payment.ID); err == nil {
		t.Fatal("database allowed deleting a referenced payment")
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE orders SET payment_id = 999999 WHERE id = ?`, order.ID); err == nil {
		t.Fatal("database allowed a dangling order payment reference")
	}
}

func TestSchemaV31PreservesV30OrdersAndAddsPaymentConstraints(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(100, 0)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 100}, nil)
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	removeSchemaV32ForMigrationTest(t, database)
	if _, err := database.db.ExecContext(ctx, `
		DROP TRIGGER trg_orders_payment_insert;
		DROP TRIGGER trg_orders_payment_update;
		DROP TRIGGER trg_payments_delete_restrict;
		DROP TABLE payment_webhook_receipts;
		DROP TABLE payment_checkout_attempts;
		DROP TABLE payments;
		DROP INDEX idx_orders_payment_status;
		PRAGMA user_version = 30;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var version, triggerCount int
	var tradeNo string
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT trade_no FROM orders WHERE id = ?`, order.ID).Scan(&tradeNo); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='trigger' AND name IN ('trg_orders_payment_insert','trg_orders_payment_update','trg_payments_delete_restrict')`).Scan(&triggerCount); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion || tradeNo != order.TradeNo || triggerCount != 3 {
		t.Fatalf("migration version=%d trade=%q/%q triggers=%d", version, tradeNo, order.TradeNo, triggerCount)
	}
}

func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return false
	}
	for index := 0; index+len(needle) <= len(haystack); index++ {
		matched := true
		for offset := range needle {
			if haystack[index+offset] != needle[offset] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
