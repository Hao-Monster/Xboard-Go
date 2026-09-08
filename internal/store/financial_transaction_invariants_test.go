package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPaymentWebhookIndependentSQLiteWritersApplyEntitlementExactlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payments.db")
	first, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if err := first.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	second, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })

	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, first, now, PlanPrices{"monthly": 100_000}, nil)
	method, err := first.CreatePayment(t.Context(), SavePaymentInput{
		Provider: PaymentProviderCoinbase, Name: "Coinbase", ConfigCiphertext: []byte("ciphertext"), Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	order, err := first.CreateOrder(t.Context(), CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	started, err := first.StartPaymentCheckout(t.Context(), StartPaymentCheckoutInput{
		UserID: userID, TradeNo: order.TradeNo, PaymentID: method.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	before, err := first.GetAdminUser(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	payload := sha256.Sum256([]byte("verified cross-connection payment webhook"))
	input := CompletePaymentWebhookInput{
		PaymentID: method.ID, Provider: method.Provider, ExternalID: "cross-connection-charge", TradeNo: order.TradeNo,
		Amount: started.Attempt.ExpectedAmount, Currency: "CNY", PayloadSHA256: fmt.Sprintf("%x", payload),
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, database := range []*Store{first, second} {
		wait.Add(1)
		go func(database *Store) {
			defer wait.Done()
			<-start
			_, completeErr := database.CompletePaymentWebhook(context.Background(), input, now.Add(time.Second))
			results <- completeErr
		}(database)
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent CompletePaymentWebhook() error = %v", err)
		}
	}

	after, err := first.GetAdminUser(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision+1 || after.ResetCount != before.ResetCount+1 || after.PlanID == nil || *after.PlanID != plan.ID {
		t.Fatalf("entitlement applied more or less than once: before=%#v after=%#v", before, after)
	}
	completed, err := first.GetUserOrder(t.Context(), userID, order.TradeNo)
	if err != nil || completed.Status != OrderStatusCompleted || completed.CallbackNo != input.ExternalID {
		t.Fatalf("completed order = (%#v, %v)", completed, err)
	}
	var receipts, events int
	if err := first.db.QueryRowContext(t.Context(), `
		SELECT
			(SELECT COUNT(*) FROM payment_webhook_receipts WHERE order_id = ?),
			(SELECT COUNT(*) FROM order_entitlement_events WHERE order_id = ?)
	`, order.ID, order.ID).Scan(&receipts, &events); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 || events != 1 {
		t.Fatalf("cross-connection receipts=%d entitlement events=%d, want 1/1", receipts, events)
	}
}

func TestCommissionWithdrawalPaidAuditFailureRollsBackReceiptAndFrozenFunds(t *testing.T) {
	database, user, administrator, now := withdrawalFixture(t)
	ticket, err := database.CreateCommissionWithdrawalTicket(t.Context(), user.ID, CommissionWithdrawalInput{
		Method: "USDT", Account: "rollback-wallet", RequestKey: "rollback-request-key",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.TransitionCommissionWithdrawal(t.Context(), administrator.ID, ticket.ID,
		CommissionWithdrawalTransitionInput{Status: "approved"}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(t.Context(), `
		CREATE TRIGGER fail_paid_withdrawal_event
		BEFORE INSERT ON commission_withdrawal_events WHEN NEW.status = 'paid'
		BEGIN SELECT RAISE(ABORT, 'injected paid audit failure'); END
	`); err != nil {
		t.Fatal(err)
	}

	input := CommissionWithdrawalTransitionInput{Status: "paid", PaymentReference: "rollback-receipt"}
	if _, err := database.TransitionCommissionWithdrawal(t.Context(), administrator.ID, ticket.ID, input, now.Add(2*time.Second)); err == nil {
		t.Fatal("payment confirmation succeeded without its append-only audit event")
	}
	rolledBack, err := database.GetAdminTicket(t.Context(), ticket.ID)
	if err != nil || rolledBack.Withdrawal == nil || rolledBack.Withdrawal.Status != "approved" || rolledBack.Withdrawal.PaymentReference != "" {
		t.Fatalf("withdrawal after failed payment audit = (%#v, %v)", rolledBack.Withdrawal, err)
	}
	assertWithdrawalFunds(t, database, user.ID, 0, 25_050, 0)
	var events, audits int
	var receiptHash string
	if err := database.db.QueryRowContext(t.Context(), `
		SELECT payment_reference_hash,
			(SELECT COUNT(*) FROM commission_withdrawal_events WHERE withdrawal_id = commission_withdrawals.id),
			(SELECT COUNT(*) FROM admin_audit_logs WHERE route = '/api/v1/admin/tickets/{ticketID}/withdrawal')
		FROM commission_withdrawals WHERE ticket_id = ?
	`, ticket.ID).Scan(&receiptHash, &events, &audits); err != nil {
		t.Fatal(err)
	}
	if receiptHash != "" || events != 2 || audits != 1 {
		t.Fatalf("failed payment leaked receipt/audit state: hash=%q events=%d admin_audits=%d", receiptHash, events, audits)
	}

	if _, err := database.db.ExecContext(t.Context(), `DROP TRIGGER fail_paid_withdrawal_event`); err != nil {
		t.Fatal(err)
	}
	paid, err := database.TransitionCommissionWithdrawal(t.Context(), administrator.ID, ticket.ID, input, now.Add(3*time.Second))
	if err != nil || paid.Withdrawal == nil || paid.Withdrawal.Status != "paid" || paid.Withdrawal.PaymentReference != input.PaymentReference {
		t.Fatalf("payment retry after rollback = (%#v, %v)", paid.Withdrawal, err)
	}
	assertWithdrawalFunds(t, database, user.ID, 0, 0, 25_050)
}
