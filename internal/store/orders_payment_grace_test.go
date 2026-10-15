package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBL02PaymentGrace(t *testing.T) {
	for _, tc := range []struct {
		name            string
		status          PaymentCheckoutStatus
		age, updatedAgo time.Duration
		protected       bool
	}{
		{"created", PaymentCheckoutCreated, 2*time.Hour + time.Second, 5 * time.Minute, true},
		{"creating", PaymentCheckoutCreating, 2*time.Hour + time.Second, time.Minute, true},
		{"failed", PaymentCheckoutFailed, 2*time.Hour + time.Second, time.Minute, false},
		{"expired_lease", PaymentCheckoutCreating, 2*time.Hour + time.Second, 2 * time.Minute, false},
		{"expired_grace", PaymentCheckoutCreated, 2*time.Hour + time.Second, 30 * time.Minute, false},
		{"hard_limit", PaymentCheckoutCreated, 150 * time.Minute, time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestStore(t)
			ctx := t.Context()
			created := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
			plan, userID := createOrderFixture(t, db, created, PlanPrices{"monthly": 1000}, nil)
			if _, err := db.db.ExecContext(ctx, `UPDATE users SET balance=300 WHERE id=?`, userID); err != nil {
				t.Fatal(err)
			}
			order, err := db.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, created)
			if err != nil {
				t.Fatal(err)
			}
			method, err := db.CreatePayment(ctx, SavePaymentInput{Provider: PaymentProviderCoinbase, Name: "QA grace", ConfigCiphertext: []byte("fixture"), Enabled: true}, created)
			if err != nil {
				t.Fatal(err)
			}
			attempt, err := db.StartPaymentCheckout(ctx, StartPaymentCheckoutInput{UserID: userID, TradeNo: order.TradeNo, PaymentID: method.ID}, created)
			if err != nil {
				t.Fatal(err)
			}
			now := created.Add(tc.age)
			if _, err := db.db.ExecContext(ctx, `UPDATE payment_checkout_attempts SET status=?, updated_at=? WHERE id=?`, tc.status, now.Add(-tc.updatedAgo).Unix(), attempt.Attempt.ID); err != nil {
				t.Fatal(err)
			}
			batch, err := db.ProcessStaleOrders(ctx, now, 1)
			if err != nil {
				t.Fatal(err)
			}
			wantCancelled := 1
			if tc.protected {
				wantCancelled = 0
			}
			if batch.Cancelled != wantCancelled || batch.Remaining != 0 {
				t.Fatalf("batch=%+v, want cancelled=%d remaining=0", batch, wantCancelled)
			}
			if tc.protected {
				batch, err = db.ProcessStaleOrders(ctx, created.Add(150*time.Minute), 1)
				if err != nil || batch.Cancelled != 1 {
					t.Fatalf("hard expiry batch=%+v err=%v", batch, err)
				}
			}
			batch, err = db.ProcessStaleOrders(ctx, created.Add(151*time.Minute), 1)
			if err != nil || batch.Cancelled != 0 {
				t.Fatalf("repeat=%+v err=%v", batch, err)
			}
			if tc.name == "created" {
				_, err := db.CompletePaymentWebhook(ctx, CompletePaymentWebhookInput{
					PaymentID: method.ID, Provider: method.Provider, ExternalID: "QA-late-after-refund", TradeNo: order.TradeNo,
					Amount: 700, Currency: "CNY", PayloadSHA256: strings.Repeat("a", 64),
				}, created.Add(152*time.Minute))
				if !errors.Is(err, ErrOrderState) {
					t.Fatalf("late settlement error=%v, want ErrOrderState", err)
				}
			}
			var balance, events, receipts int
			if err := db.db.QueryRowContext(ctx, `SELECT balance, (SELECT COUNT(*) FROM order_entitlement_events), (SELECT COUNT(*) FROM payment_webhook_receipts) FROM users WHERE id=?`, userID).Scan(&balance, &events, &receipts); err != nil {
				t.Fatal(err)
			}
			if balance != 300 || events != 0 || receipts != 0 {
				t.Fatalf("balance/events/receipts=%d/%d/%d", balance, events, receipts)
			}
			got, err := db.GetUserOrder(ctx, userID, order.TradeNo)
			if err != nil || got.Status != OrderStatusCancelled {
				t.Fatalf("order=%+v err=%v", got, err)
			}
		})
	}
}

func TestBL02CheckoutDeadlineAndGraceSettlement(t *testing.T) {
	db := newTestStore(t)
	ctx := t.Context()
	created := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, db, created, PlanPrices{"monthly": 1000}, nil)
	order, err := db.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, created)
	if err != nil {
		t.Fatal(err)
	}
	method, err := db.CreatePayment(ctx, SavePaymentInput{Provider: PaymentProviderCoinbase, Name: "QA grace", ConfigCiphertext: []byte("fixture"), Enabled: true}, created)
	if err != nil {
		t.Fatal(err)
	}
	input := StartPaymentCheckoutInput{UserID: userID, TradeNo: order.TradeNo, PaymentID: method.ID}
	started, err := db.StartPaymentCheckout(ctx, input, created.Add(119*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CompletePaymentCheckout(ctx, started.Attempt.ID, started.Attempt.IdempotencyKey, 1, "https://example.test/pay", "QA-grace", created.Add(119*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.StartPaymentCheckout(ctx, input, created.Add(2*time.Hour)); !errors.Is(err, ErrOrderState) {
		t.Errorf("expired checkout error=%v, want ErrOrderState", err)
	}
	if batch, err := db.ProcessStaleOrders(ctx, created.Add(121*time.Minute), 10); err != nil || batch.Cancelled != 0 || batch.Remaining != 0 {
		t.Fatalf("grace batch=%+v err=%v", batch, err)
	}
	completed, err := db.CompletePaymentWebhook(ctx, CompletePaymentWebhookInput{PaymentID: method.ID, Provider: method.Provider, ExternalID: "QA-grace", TradeNo: order.TradeNo, Amount: 1000, Currency: "CNY", PayloadSHA256: strings.Repeat("a", 64)}, created.Add(122*time.Minute))
	if err != nil || completed.Status != OrderStatusCompleted {
		t.Fatalf("grace settlement=%+v err=%v", completed, err)
	}
	var actualPlan, expiry, balance, events int64
	if err := db.db.QueryRowContext(ctx, `SELECT plan_id,expired_at,balance,(SELECT COUNT(*) FROM order_entitlement_events WHERE order_id=?) FROM users WHERE id=?`, order.ID, userID).Scan(&actualPlan, &expiry, &balance, &events); err != nil {
		t.Fatal(err)
	}
	if actualPlan != plan.ID || expiry != created.Add(122*time.Minute).AddDate(0, 1, 0).Unix() || balance != 0 || events != 1 {
		t.Fatalf("plan/expiry/balance/events=%d/%d/%d/%d", actualPlan, expiry, balance, events)
	}
}
