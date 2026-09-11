package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func withdrawalFixture(t *testing.T) (*Store, AdminUser, AdminUser, time.Time) {
	t.Helper()
	database := newTestStore(t)
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	user := createTicketTestUser(t, database, "withdrawal-owner@example.test", now)
	admin := createTicketTestUser(t, database, "withdrawal-admin@example.test", now)
	if _, err := database.db.Exec(`UPDATE users SET is_admin = 1 WHERE id = ?`, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`UPDATE users SET commission_balance = 25050 WHERE id = ?`, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`UPDATE app_settings SET withdraw_close_enable = 0, commission_withdraw_limit = 10000, commission_withdraw_method = '["USDT"]' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	return database, user, admin, now
}

func assertWithdrawalFunds(t *testing.T, db *Store, userID, available, frozen, paid int64) {
	t.Helper()
	var gotAvailable, gotFrozen, gotPaid int64
	if err := db.db.QueryRow(`SELECT commission_balance,
	 (SELECT COALESCE(SUM(amount),0) FROM commission_withdrawals WHERE user_id = users.id AND status IN ('pending','approved')),
	 (SELECT COALESCE(SUM(amount),0) FROM commission_withdrawals WHERE user_id = users.id AND status = 'paid')
	 FROM users WHERE id = ?`, userID).Scan(&gotAvailable, &gotFrozen, &gotPaid); err != nil {
		t.Fatal(err)
	}
	if gotAvailable != available || gotFrozen != frozen || gotPaid != paid {
		t.Fatalf("available/frozen/paid = %d/%d/%d, want %d/%d/%d", gotAvailable, gotFrozen, gotPaid, available, frozen, paid)
	}
}

func TestCommissionWithdrawalApprovalPaymentAndReceiptIdempotency(t *testing.T) {
	db, user, admin, now := withdrawalFixture(t)
	ctx := t.Context()
	input := CommissionWithdrawalInput{Method: "USDT", Account: "wallet-42", RequestKey: "request-key-0001"}
	ticket, err := db.CreateCommissionWithdrawalTicket(ctx, user.ID, input, now)
	if err != nil || ticket.Withdrawal == nil || ticket.Withdrawal.Amount != 25050 || ticket.Withdrawal.Status != "pending" {
		t.Fatalf("create = %#v, %v", ticket, err)
	}
	assertWithdrawalFunds(t, db, user.ID, 0, 25050, 0)
	if _, err := db.TransferCommission(ctx, user.ID, 1, now); !errors.Is(err, ErrInsufficientCommission) {
		t.Fatalf("frozen commission spent: %v", err)
	}
	if _, err := db.TransitionCommissionWithdrawal(ctx, admin.ID, ticket.ID, CommissionWithdrawalTransitionInput{Status: "paid", PaymentReference: "receipt-1"}, now); !errors.Is(err, ErrCommissionWithdrawalState) {
		t.Fatalf("paid before approval = %v", err)
	}
	if _, err := db.TransitionCommissionWithdrawal(ctx, user.ID, ticket.ID, CommissionWithdrawalTransitionInput{Status: "approved"}, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-admin approval = %v", err)
	}
	for range 2 {
		approved, err := db.TransitionCommissionWithdrawal(ctx, admin.ID, ticket.ID, CommissionWithdrawalTransitionInput{Status: "approved"}, now.Add(time.Minute))
		if err != nil || approved.Withdrawal.Status != "approved" {
			t.Fatalf("approve = %#v, %v", approved, err)
		}
	}
	assertWithdrawalFunds(t, db, user.ID, 0, 25050, 0)
	for range 2 {
		paid, err := db.TransitionCommissionWithdrawal(ctx, admin.ID, ticket.ID, CommissionWithdrawalTransitionInput{Status: "paid", PaymentReference: "receipt-1"}, now.Add(2*time.Minute))
		if err != nil || paid.Withdrawal.Status != "paid" || paid.Withdrawal.PaymentReference != "receipt-1" {
			t.Fatalf("payment confirmation = %#v, %v", paid, err)
		}
	}
	assertWithdrawalFunds(t, db, user.ID, 0, 0, 25050)
	for _, status := range []string{"rejected", "paid"} {
		_, err := db.TransitionCommissionWithdrawal(ctx, admin.ID, ticket.ID, CommissionWithdrawalTransitionInput{Status: status, PaymentReference: map[string]string{"paid": "different-receipt"}[status]}, now.Add(3*time.Minute))
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("terminal conflicting %s = %v", status, err)
		}
	}
	duplicate, err := db.CreateCommissionWithdrawalTicket(ctx, user.ID, input, now.Add(4*time.Minute))
	if err != nil || duplicate.ID != ticket.ID || duplicate.Withdrawal.Status != "paid" {
		t.Fatalf("durable create replay = %#v, %v", duplicate, err)
	}
	var events, audits, deltaSum int64
	if err := db.db.QueryRow(`SELECT COUNT(*), SUM(available_delta + frozen_delta + paid_delta) FROM commission_withdrawal_events`).Scan(&events, &deltaSum); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM admin_audit_logs WHERE route = '/api/v1/admin/tickets/{ticketID}/withdrawal'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if events != 3 || deltaSum != 0 || audits != 2 {
		t.Fatalf("audit events=%d net delta=%d admin audits=%d", events, deltaSum, audits)
	}
}

func TestCommissionWithdrawalRejectionRefundsOnceAndTicketClosureNeverSettles(t *testing.T) {
	for _, approve := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "approved"}[approve], func(t *testing.T) {
			db, user, admin, now := withdrawalFixture(t)
			input := CommissionWithdrawalInput{Method: "USDT", Account: "wallet-42"}
			ticket, err := db.CreateCommissionWithdrawalTicket(t.Context(), user.ID, input, now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.CloseTicketAsUser(t.Context(), user.ID, ticket.ID, now); err != nil {
				t.Fatal(err)
			}
			assertWithdrawalFunds(t, db, user.ID, 0, 25050, 0)
			if duplicate, err := db.CreateCommissionWithdrawalTicket(t.Context(), user.ID, input, now); err != nil || duplicate.ID != ticket.ID {
				t.Fatalf("legacy replay after close = %#v, %v", duplicate, err)
			}
			if approve {
				if _, err := db.TransitionCommissionWithdrawal(t.Context(), admin.ID, ticket.ID, CommissionWithdrawalTransitionInput{Status: "approved"}, now); err != nil {
					t.Fatal(err)
				}
			}
			for range 3 {
				rejected, err := db.TransitionCommissionWithdrawal(t.Context(), admin.ID, ticket.ID, CommissionWithdrawalTransitionInput{Status: "rejected"}, now)
				if err != nil || rejected.Withdrawal.Status != "rejected" || rejected.Status != TicketStatusClosed {
					t.Fatalf("reject = %#v, %v", rejected, err)
				}
			}
			assertWithdrawalFunds(t, db, user.ID, 25050, 0, 0)
			if _, err := db.TransferCommission(t.Context(), user.ID, 25050, now); err != nil {
				t.Fatalf("released funds unavailable: %v", err)
			}
		})
	}
}

func TestCommissionWithdrawalAuditFailuresRollBackEveryFinancialMutation(t *testing.T) {
	db, user, admin, now := withdrawalFixture(t)
	if _, err := db.db.Exec(`CREATE TRIGGER fail_withdrawal_event BEFORE INSERT ON commission_withdrawal_events BEGIN SELECT RAISE(ABORT, 'injected audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	input := CommissionWithdrawalInput{Method: "USDT", Account: "wallet-42"}
	if _, err := db.CreateCommissionWithdrawalTicket(t.Context(), user.ID, input, now); err == nil {
		t.Fatal("creation succeeded without an audit event")
	}
	assertWithdrawalFunds(t, db, user.ID, 25050, 0, 0)
	var tickets int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM tickets`).Scan(&tickets); err != nil || tickets != 0 {
		t.Fatalf("failed creation retained ticket=%d: %v", tickets, err)
	}
	if _, err := db.db.Exec(`DROP TRIGGER fail_withdrawal_event`); err != nil {
		t.Fatal(err)
	}
	ticket, err := db.CreateCommissionWithdrawalTicket(t.Context(), user.ID, input, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`CREATE TRIGGER fail_withdrawal_event BEFORE INSERT ON commission_withdrawal_events BEGIN SELECT RAISE(ABORT, 'injected audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.TransitionCommissionWithdrawal(t.Context(), admin.ID, ticket.ID, CommissionWithdrawalTransitionInput{Status: "rejected"}, now); err == nil {
		t.Fatal("rejection succeeded without an audit event")
	}
	assertWithdrawalFunds(t, db, user.ID, 0, 25050, 0)
}

func TestCommissionWithdrawalLedgerConstraintsAndLegacyTicketIsolation(t *testing.T) {
	db, user, admin, now := withdrawalFixture(t)
	legacy, err := db.CreateTicket(t.Context(), user.ID, SaveTicketInput{Subject: commissionWithdrawalSubject, Level: TicketLevelHigh, Message: "historical request"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.TransitionCommissionWithdrawal(t.Context(), admin.ID, legacy.ID, CommissionWithdrawalTransitionInput{Status: "approved"}, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("historical ticket acquired financial semantics: %v", err)
	}
	if _, err := db.CloseTicketAsAdmin(t.Context(), legacy.ID, now); err != nil {
		t.Fatal(err)
	}
	ticket, err := db.CreateCommissionWithdrawalTicket(t.Context(), user.ID, CommissionWithdrawalInput{Method: "USDT", Account: "wallet-42"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE commission_withdrawals SET amount = 1`,
		`UPDATE commission_withdrawals SET status = 'paid', payment_reference = 'unapproved'`,
		`DELETE FROM commission_withdrawals`,
		`UPDATE commission_withdrawal_events SET actor_id = 999`,
		`DELETE FROM commission_withdrawal_events`,
	} {
		if _, err := db.db.Exec(statement); err == nil {
			t.Fatalf("invalid ledger operation accepted: %s", statement)
		}
	}
	if _, err := db.GetUserTicket(t.Context(), admin.ID, ticket.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("withdrawal account exposed to another user: %v", err)
	}
	assertWithdrawalFunds(t, db, user.ID, 0, 25050, 0)
}

func TestCommissionWithdrawalIndependentWritersConserveFundsAndRefundOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "withdrawal.db")
	first, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	second, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	user := createTicketTestUser(t, first, "race-owner@example.test", now)
	admin := createTicketTestUser(t, first, "race-admin@example.test", now)
	if _, err := first.db.Exec(`UPDATE users SET commission_balance = 50000, is_admin = CASE WHEN id = ? THEN 1 ELSE 0 END`, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.Exec(`UPDATE app_settings SET withdraw_close_enable = 0, commission_withdraw_limit = 10000, commission_withdraw_method = '["USDT"]'`); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 12)
	var wg sync.WaitGroup
	for index := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			db := []*Store{first, second}[index%2]
			if index%3 == 0 {
				_, err := db.TransferCommission(context.Background(), user.ID, 1000, now)
				if errors.Is(err, ErrInsufficientCommission) {
					err = nil
				}
				results <- err
			} else {
				_, err := db.CreateCommissionWithdrawalTicket(context.Background(), user.ID, CommissionWithdrawalInput{Method: "USDT", Account: "race-wallet", RequestKey: "race-request-key"}, now)
				results <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	var available, balance, frozen, count, ticketID int64
	if err := first.db.QueryRow(`SELECT commission_balance, balance FROM users WHERE id = ?`, user.ID).Scan(&available, &balance); err != nil {
		t.Fatal(err)
	}
	if err := first.db.QueryRow(`SELECT COUNT(*), SUM(amount), MIN(ticket_id) FROM commission_withdrawals WHERE user_id = ?`, user.ID).Scan(&count, &frozen, &ticketID); err != nil {
		t.Fatal(err)
	}
	if count != 1 || available+balance+frozen != 50000 || available != 0 {
		t.Fatalf("race balance=%d available=%d frozen=%d count=%d", balance, available, frozen, count)
	}
	results = make(chan error, 8)
	for index := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := []*Store{first, second}[index%2].TransitionCommissionWithdrawal(context.Background(), admin.ID, ticketID, CommissionWithdrawalTransitionInput{Status: "rejected"}, now)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertWithdrawalFunds(t, first, user.ID, frozen, 0, 0)
	var rejectionEvents int
	if err := first.db.QueryRow(`SELECT COUNT(*) FROM commission_withdrawal_events WHERE status = 'rejected'`).Scan(&rejectionEvents); err != nil || rejectionEvents != 1 {
		t.Fatalf("rejection events=%d: %v", rejectionEvents, err)
	}
}

func TestCommissionWithdrawalRejectOverflowAndRequestMismatchPreserveFrozenFunds(t *testing.T) {
	db, user, admin, now := withdrawalFixture(t)
	input := CommissionWithdrawalInput{Method: "USDT", Account: "wallet-42", RequestKey: "immutable-request-key"}
	ticket, err := db.CreateCommissionWithdrawalTicket(t.Context(), user.ID, input, now)
	if err != nil {
		t.Fatal(err)
	}
	input.Account = "different-wallet"
	if _, err := db.CreateCommissionWithdrawalTicket(t.Context(), user.ID, input, now); !errors.Is(err, ErrCommissionWithdrawalReplay) {
		t.Fatalf("request key reused with different content: %v", err)
	}
	if _, err := db.db.Exec(`UPDATE users SET commission_balance = ? WHERE id = ?`, maxOrderMoneyCents, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.TransitionCommissionWithdrawal(t.Context(), admin.ID, ticket.ID, CommissionWithdrawalTransitionInput{Status: "rejected"}, now); !errors.Is(err, ErrCommissionWithdrawalState) {
		t.Fatalf("refund overflow accepted: %v", err)
	}
	assertWithdrawalFunds(t, db, user.ID, maxOrderMoneyCents, 25050, 0)
}

func TestCommissionWithdrawalReceiptDeduplicationSurvivesAccountAnonymization(t *testing.T) {
	db, user, admin, now := withdrawalFixture(t)
	ticket, err := db.CreateCommissionWithdrawalTicket(t.Context(), user.ID, CommissionWithdrawalInput{Method: "USDT", Account: "wallet-private"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []CommissionWithdrawalTransitionInput{{Status: "approved"}, {Status: "paid", PaymentReference: "receipt-private"}} {
		if _, err := db.TransitionCommissionWithdrawal(t.Context(), admin.ID, ticket.ID, input, now); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := anonymizeCommissionWithdrawalsTx(t.Context(), tx, user.ID, now.Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	anonymized, err := db.GetAdminTicket(t.Context(), ticket.ID)
	if err != nil || anonymized.Withdrawal.Account != "[anonymized]" || anonymized.Withdrawal.PaymentReference != "[anonymized]" {
		t.Fatalf("anonymized withdrawal=%#v, %v", anonymized, err)
	}
	assertWithdrawalFunds(t, db, user.ID, 0, 0, 25050)
	for _, statement := range []string{
		`UPDATE commission_withdrawals SET account = 'restored-private-account'`,
		`UPDATE commission_withdrawals SET payment_reference_hash = 'different-hash'`,
		`UPDATE commission_withdrawals SET anonymized_at = NULL`,
	} {
		if _, err := db.db.Exec(statement); err == nil {
			t.Fatalf("anonymization was reversible: %s", statement)
		}
	}
	other := createTicketTestUser(t, db, "receipt-reuser@example.test", now)
	if _, err := db.db.Exec(`UPDATE users SET commission_balance = 25050 WHERE id = ?`, other.ID); err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateCommissionWithdrawalTicket(t.Context(), other.ID, CommissionWithdrawalInput{Method: "USDT", Account: "other-wallet"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.TransitionCommissionWithdrawal(t.Context(), admin.ID, second.ID, CommissionWithdrawalTransitionInput{Status: "approved"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.TransitionCommissionWithdrawal(t.Context(), admin.ID, second.ID, CommissionWithdrawalTransitionInput{Status: "paid", PaymentReference: "receipt-private"}, now); !errors.Is(err, ErrCommissionWithdrawalReplay) {
		t.Fatalf("receipt reused after anonymization: %v", err)
	}
	assertWithdrawalFunds(t, db, other.ID, 0, 25050, 0)
}

func TestCommissionWithdrawalPaymentAndRejectionRaceHasOnlyOneTerminalOutcome(t *testing.T) {
	db, user, admin, now := withdrawalFixture(t)
	ticket, err := db.CreateCommissionWithdrawalTicket(t.Context(), user.ID, CommissionWithdrawalInput{Method: "USDT", Account: "wallet-42"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.TransitionCommissionWithdrawal(t.Context(), admin.ID, ticket.ID, CommissionWithdrawalTransitionInput{Status: "approved"}, now); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, input := range []CommissionWithdrawalTransitionInput{{Status: "paid", PaymentReference: "race-receipt"}, {Status: "rejected"}} {
		go func() {
			<-start
			_, err := db.TransitionCommissionWithdrawal(t.Context(), admin.ID, ticket.ID, input, now)
			results <- err
		}()
	}
	close(start)
	var successes, conflicts int
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if errors.Is(err, ErrCommissionWithdrawalState) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	result, err := db.GetAdminTicket(t.Context(), ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Withdrawal.Status == "paid" {
		assertWithdrawalFunds(t, db, user.ID, 0, 0, 25050)
	} else if result.Withdrawal.Status == "rejected" {
		assertWithdrawalFunds(t, db, user.ID, 25050, 0, 0)
	} else {
		t.Fatalf("no terminal outcome: %#v", result.Withdrawal)
	}
}

func TestCommissionWithdrawalMigrationDoesNotSettleHistoricalTicket(t *testing.T) {
	db, user, _, now := withdrawalFixture(t)
	if _, err := db.CreateTicket(t.Context(), user.ID, SaveTicketInput{Subject: commissionWithdrawalSubject, Level: TicketLevelHigh, Message: "historical account"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`DROP TRIGGER users_money_admin_revision; DROP TABLE commission_withdrawal_events; DROP TABLE commission_withdrawals; PRAGMA user_version = 59`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatalf("repeated migration: %v", err)
	}
	if err := db.ValidateCurrentSchema(t.Context()); err != nil {
		t.Fatalf("upgraded schema validation: %v", err)
	}
	before, err := db.GetAdminUser(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE users SET balance=balance+1 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	after, err := db.GetAdminUser(t.Context(), user.ID)
	if err != nil || after.Revision != before.Revision+1 || after.Balance != before.Balance+1 {
		t.Fatalf("upgrade did not protect financial edit snapshots: %#v, %v", after, err)
	}
	assertWithdrawalFunds(t, db, user.ID, 25050, 0, 0)
	var tickets, withdrawals int
	if err := db.db.QueryRow(`SELECT (SELECT COUNT(*) FROM tickets), (SELECT COUNT(*) FROM commission_withdrawals)`).Scan(&tickets, &withdrawals); err != nil || tickets != 1 || withdrawals != 0 {
		t.Fatalf("historical migration tickets=%d withdrawals=%d: %v", tickets, withdrawals, err)
	}
}

func TestMigrationRepairsMissingCommissionWithdrawalTablesAtV63(t *testing.T) {
	db, _, _, _ := withdrawalFixture(t)
	if _, err := db.db.Exec(`DROP TRIGGER users_money_admin_revision; DROP TRIGGER commission_withdrawals_no_delete; DROP TRIGGER commission_withdrawal_events_no_delete; DROP TRIGGER commission_withdrawal_events_no_update; DROP TRIGGER commission_withdrawals_receipt_immutable; DROP TRIGGER commission_withdrawals_transition; DROP TRIGGER commission_withdrawals_account_immutable; DROP TRIGGER commission_withdrawals_immutable; DROP TABLE commission_withdrawal_events; DROP TABLE commission_withdrawals; PRAGMA user_version = 63`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate(v63 missing ledger tables): %v", err)
	}
	if err := db.ValidateCurrentSchema(t.Context()); err != nil {
		t.Fatalf("repaired schema validation: %v", err)
	}
}
