package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func lifecycleFixture(t *testing.T) (*Store, AdminUser, AdminUser, time.Time) {
	t.Helper()
	db := newTestStore(t)
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	admin, err := db.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "lifecycle-admin@example.test", PasswordHash: "hash", IsAdmin: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "private-user@example.test", PasswordHash: "secret-hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	return db, admin, user, now
}

func TestUserLifecycleRevokesCredentialsAndPreservesRecovery(t *testing.T) {
	db, admin, user, now := lifecycleFixture(t)
	ctx := t.Context()
	before, err := db.FindUserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.CreateSession(ctx, user.ID, "old-cookie", "csrf", now.Add(60*24*time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.CreateAccessToken(ctx, CreateAccessTokenInput{UserID: user.ID, TokenHash: strings.Repeat("b", 64), Name: "personal token"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `UPDATE users SET balance=12345,commission_balance=678,telegram_id=987,remarks='private note' WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	user, err = db.GetAdminUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: user.Revision, Action: "deactivate"}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := db.ChangeUserLifecycle(context.Background(), request, now)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent same request: %v", err)
		}
	}
	inactive, err := db.GetAdminUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !inactive.Banned || inactive.LifecycleStatus != "deactivated" || inactive.RestoreUntil == nil || !inactive.RestoreUntil.Equal(now.Add(UserRecoveryWindow)) {
		t.Fatalf("inactive=%+v", inactive)
	}
	if inactive.Balance != 12345 || inactive.CommissionBalance != 678 || inactive.Email != user.Email {
		t.Fatalf("deactivation destroyed recoverable facts: %+v", inactive)
	}
	if _, err = db.AuthenticateSession(ctx, "old-cookie", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old session remains: %v", err)
	}
	if _, err = db.AuthenticateAccessToken(ctx, strings.Repeat("b", 64), now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old access token remains: %v", err)
	}
	after, err := db.FindUserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.SubscriptionToken == before.SubscriptionToken {
		t.Fatal("subscription token not rotated")
	}
	if _, _, err = db.UpdateAdminUser(ctx, user.ID, UpdateAdminUserInput{Revision: inactive.Revision, Email: inactive.Email, Banned: false}, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("generic unban bypass: %v", err)
	}
	if _, err = db.db.ExecContext(ctx, `UPDATE users SET banned=0 WHERE id=?`, user.ID); err == nil {
		t.Fatal("DB invariant allows generic unban")
	}
	request.Action = "restore"
	request.Revision = inactive.Revision
	restored, _, err := db.ChangeUserLifecycle(ctx, request, now.Add(UserRecoveryWindow-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if restored.Banned || restored.LifecycleStatus != "active" || restored.Email != user.Email || restored.Balance != 12345 || restored.TelegramID == nil || restored.Remarks == nil {
		t.Fatalf("restore lost recoverable data: %+v", restored)
	}
	if _, err = db.AuthenticateSession(ctx, "old-cookie", now.Add(UserRecoveryWindow-time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore revived revoked session: %v", err)
	}
	var count int
	if err = db.db.QueryRowContext(ctx, `SELECT count(*) FROM user_lifecycle_events WHERE user_id=?`, user.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("audit count=%d err=%v", count, err)
	}
}

func TestUserLifecycleUnsettledWithdrawalBlocksAnonymization(t *testing.T) {
	db, user, admin, now := withdrawalFixture(t)
	ctx := t.Context()
	ticket, err := db.CreateCommissionWithdrawalTicket(ctx, user.ID, CommissionWithdrawalInput{Method: "USDT", Account: "private-wallet", RequestKey: "lifecycle-withdraw-key"}, now)
	if err != nil {
		t.Fatal(err)
	}
	current, err := db.GetAdminUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	inactive, _, err := db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: current.Revision, Action: "deactivate"}, now)
	if err != nil {
		t.Fatal(err)
	}
	request := UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: inactive.Revision, Action: "anonymize"}
	after := now.Add(UserRecoveryWindow)
	if _, _, err = db.ChangeUserLifecycle(ctx, request, after); !errors.Is(err, ErrConflict) {
		t.Fatalf("pending withdrawal anonymized %v", err)
	}
	approved, err := db.TransitionCommissionWithdrawal(ctx, admin.ID, ticket.ID, CommissionWithdrawalTransitionInput{Status: "approved"}, after)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = db.ChangeUserLifecycle(ctx, request, after); !errors.Is(err, ErrConflict) {
		t.Fatalf("approved withdrawal anonymized %v", err)
	}
	if approved.Withdrawal == nil || approved.Withdrawal.Account != "private-wallet" {
		t.Fatalf("blocked anonymization changed account %+v", approved)
	}
	paid, err := db.TransitionCommissionWithdrawal(ctx, admin.ID, ticket.ID, CommissionWithdrawalTransitionInput{Status: "paid", PaymentReference: "private-receipt"}, after)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = db.ChangeUserLifecycle(ctx, request, after); err != nil {
		t.Fatal(err)
	}
	var account, reference, status string
	var amount int64
	if err = db.db.QueryRowContext(ctx, `SELECT account,payment_reference,status,amount FROM commission_withdrawals WHERE id=?`, paid.Withdrawal.ID).Scan(&account, &reference, &status, &amount); err != nil {
		t.Fatal(err)
	}
	if account != "[anonymized]" || reference != "[anonymized]" || status != "paid" || amount != 25050 {
		t.Fatalf("settled withdrawal integrity %s %s %s %d", account, reference, status, amount)
	}
	assertWithdrawalFunds(t, db, user.ID, 0, 0, 25050)
}

func TestUserLifecycleCancelsNotificationsAndInvalidatesInFlightCSV(t *testing.T) {
	db, admin, user, now := lifecycleFixture(t)
	ctx := t.Context()
	if _, err := db.db.ExecContext(ctx, `UPDATE app_settings SET smtp_enabled=1,smtp_host='smtp.example.test',smtp_from_address='ops@example.test' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE users SET telegram_id=777 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	mail, err := db.CreateAdminUserBulkJob(ctx, CreateAdminUserBulkJobInput{Kind: AdminUserBulkKindMail, AdministratorID: admin.ID, Subject: "Notice", Content: "private mail", Scope: AdminUserBulkScope{Scope: AdminUserBulkScopeSelected, UserIDs: []int64{user.ID}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	claimedMail, claimed, err := db.ClaimAdminUserBulkMail(ctx, "initial-mail-claim", now, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("initial mail claim %t %v", claimed, err)
	}
	if active, err := db.AdminUserBulkMailClaimActive(ctx, mail.ID, claimedMail.Sequence, "initial-mail-claim"); err != nil || !active {
		t.Fatalf("active claim %t %v", active, err)
	}
	csv, err := db.CreateAdminUserBulkJob(ctx, CreateAdminUserBulkJobInput{Kind: AdminUserBulkKindCSV, AdministratorID: admin.ID, Scope: AdminUserBulkScope{Scope: AdminUserBulkScopeSelected, UserIDs: []int64{user.ID}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := db.ClaimAdminUserBulkCSV(ctx, csv.ID, "lifecycle-csv-claim", now, 2*time.Minute); err != nil || !claimed {
		t.Fatalf("CSV claim %t %v", claimed, err)
	}
	if _, err = db.db.ExecContext(ctx, `INSERT INTO telegram_message_outbox(source_kind,source_id,chat_id,text,available_at,created_at,updated_at) VALUES('command',77,777,'private chat',?,?,?)`, now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `INSERT INTO subscription_reminder_outbox(user_id,kind,reminder_day,recipient,app_name,available_at,created_at,updated_at) VALUES(?,'expire','2026-09-08',?,'Xboard',?,?,?)`, user.ID, user.Email, now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	inactive, _, err := db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: user.Revision, Action: "deactivate"}, now)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := db.GetAdminUserBulkJob(ctx, mail.ID)
	if err != nil || cancelled.CancelledCount != 1 || cancelled.ProcessedCount != 1 {
		t.Fatalf("mail not cancelled %+v %v", cancelled, err)
	}
	if active, err := db.AdminUserBulkMailClaimActive(ctx, mail.ID, claimedMail.Sequence, "initial-mail-claim"); err != nil || active {
		t.Fatalf("cancelled claim passes send boundary %t %v", active, err)
	}
	if _, claimed, err := db.ClaimAdminUserBulkMail(ctx, "lifecycle-mail-claim", now, time.Minute); err != nil || claimed {
		t.Fatalf("inactive mail claimed %t %v", claimed, err)
	}
	var count int
	if err = db.db.QueryRowContext(ctx, `SELECT count(*) FROM subscription_reminder_outbox WHERE user_id=? AND cancelled_at IS NOT NULL`, user.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("reminder not cancelled %d %v", count, err)
	}
	if _, _, err = db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: inactive.Revision, Action: "anonymize"}, now.Add(UserRecoveryWindow)); err != nil {
		t.Fatal(err)
	}
	// The CSV worker may already hold an old snapshot. Completion must not make
	// that stale export downloadable again after anonymization.
	if err = db.CompleteAdminUserBulkCSV(ctx, csv.ID, "lifecycle-csv-claim", "users.csv", "retained.csv", 100, strings.Repeat("a", 64), now.Add(UserRecoveryWindow+time.Hour), now.Add(UserRecoveryWindow)); err != nil {
		t.Fatal(err)
	}
	exported, err := db.GetAdminUserBulkJob(ctx, csv.ID)
	if err != nil || exported.OutputExpiresAt == nil || exported.OutputExpiresAt.Unix() != 0 {
		t.Fatalf("stale CSV revived %+v %v", exported, err)
	}
	var text string
	var chat int64
	if err = db.db.QueryRowContext(ctx, `SELECT text,chat_id FROM telegram_message_outbox WHERE source_kind='command' AND source_id=77`).Scan(&text, &chat); err != nil || text != "[anonymized]" || chat == 777 {
		t.Fatalf("Telegram PII retained %q %d %v", text, chat, err)
	}
}

func TestUserLifecycleBoundaryExplicitAnonymizationAndFactRetention(t *testing.T) {
	db, admin, user, now := lifecycleFixture(t)
	ctx := t.Context()
	if _, err := db.db.ExecContext(ctx, `UPDATE users SET balance=321,commission_balance=123,telegram_id=777,remarks='private remark' WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	user, err := db.GetAdminUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := db.CreatePlan(ctx, SavePlanInput{Name: "Lifecycle", TransferEnableGiB: 1, Prices: PlanPrices{}, Tags: []string{}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `INSERT INTO orders(user_id,plan_id,period,trade_no,original_amount,total_amount,type,status,created_at,updated_at) VALUES(?,?,'monthly',?,550,550,1,3,?,?)`, user.ID, plan.ID, strings.Repeat("a", 32), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	ticket, err := db.CreateTicket(ctx, user.ID, SaveTicketInput{Subject: "Private support", Level: 1, Message: "identity in message"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `INSERT INTO telegram_message_outbox(source_kind,source_id,recipient_user_id,chat_id,text,claim_token,claimed_at,available_at,created_at,updated_at) SELECT 'ticket',id,?,555666,'private user notification','admin-snapshot-claim',?,?,?,? FROM ticket_messages WHERE ticket_id=?`, admin.ID, now.Unix(), now.Unix(), now.Unix(), now.Unix(), ticket.ID); err != nil {
		t.Fatal(err)
	}
	csv, err := db.CreateAdminUserBulkJob(ctx, CreateAdminUserBulkJobInput{Kind: AdminUserBulkKindCSV, AdministratorID: admin.ID, Scope: AdminUserBulkScope{Scope: AdminUserBulkScopeSelected, UserIDs: []int64{user.ID}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	inactive, _, err := db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: user.Revision, Action: "deactivate"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		action string
		at     time.Time
	}{{"anonymize", now.Add(UserRecoveryWindow - time.Second)}, {"restore", now.Add(UserRecoveryWindow)}} {
		if _, _, err = db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: inactive.Revision, Action: scenario.action}, scenario.at); !errors.Is(err, ErrConflict) {
			t.Fatalf("%s boundary err=%v", scenario.action, err)
		}
	}
	untouched, err := db.GetAdminUser(ctx, user.ID)
	if err != nil || untouched.Email != user.Email || untouched.LifecycleStatus != "deactivated" {
		t.Fatalf("clock alone anonymized identity %+v %v", untouched, err)
	}
	anonymized, _, err := db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: inactive.Revision, Action: "anonymize"}, now.Add(UserRecoveryWindow))
	if err != nil {
		t.Fatal(err)
	}
	if anonymized.LifecycleStatus != "anonymized" || anonymized.Email == user.Email || anonymized.TelegramID != nil || anonymized.Remarks != nil || anonymized.Balance != 321 || anonymized.CommissionBalance != 123 {
		t.Fatalf("tombstone=%+v", anonymized)
	}
	identity, err := db.FindUserByID(ctx, user.ID)
	if err != nil || identity.PasswordHash != "!" || !identity.Banned {
		t.Fatalf("credentials=%+v %v", identity, err)
	}
	var count, total int
	if err = db.db.QueryRowContext(ctx, `SELECT count(*),sum(total_amount) FROM orders WHERE user_id=?`, user.ID).Scan(&count, &total); err != nil || count != 1 || total != 550 {
		t.Fatalf("orders lost: %d %d %v", count, total, err)
	}
	var subject, message string
	if err = db.db.QueryRowContext(ctx, `SELECT t.subject,m.message FROM tickets t JOIN ticket_messages m ON m.ticket_id=t.id WHERE t.id=?`, ticket.ID).Scan(&subject, &message); err != nil || subject != "[anonymized]" || message != "[anonymized]" {
		t.Fatalf("support PII remains %q %q %v", subject, message, err)
	}
	var adminChat int64
	var cancelled bool
	if err = db.db.QueryRowContext(ctx, `SELECT text,chat_id,(cancelled_at IS NOT NULL AND claim_token IS NULL) FROM telegram_message_outbox WHERE source_kind='ticket'`).Scan(&message, &adminChat, &cancelled); err != nil || message != "[anonymized]" || adminChat != 555666 || !cancelled {
		t.Fatalf("administrator snapshot not fenced or unrelated recipient modified: %q %d %t %v", message, adminChat, cancelled, err)
	}
	var email, token string
	if err = db.db.QueryRowContext(ctx, `SELECT email,subscription_token FROM admin_user_bulk_targets WHERE job_id=?`, csv.ID).Scan(&email, &token); err != nil || email == user.Email || token != "anonymized" {
		t.Fatalf("CSV snapshot PII remains %q %q %v", email, token, err)
	}
	var expires int64
	if err = db.db.QueryRowContext(ctx, `SELECT output_expires_at FROM admin_user_bulk_jobs WHERE id=?`, csv.ID).Scan(&expires); err != nil || expires != 0 {
		t.Fatalf("CSV not invalidated %d %v", expires, err)
	}
	if _, err = db.db.ExecContext(ctx, `UPDATE users SET email='resurrect@example.test' WHERE id=?`, user.ID); err == nil {
		t.Fatal("tombstone identity can be overwritten")
	}
	if _, _, err = db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: anonymized.Revision, Action: "restore"}, now.Add(UserRecoveryWindow)); !errors.Is(err, ErrConflict) {
		t.Fatalf("tombstone restored: %v", err)
	}
}

func TestUserLifecycleAuthorizationAndRollback(t *testing.T) {
	db, admin, user, now := lifecycleFixture(t)
	ctx := t.Context()
	for _, actor := range []int64{user.ID, 99999} {
		if _, _, err := db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: actor, UserID: admin.ID, Revision: admin.Revision, Action: "deactivate"}, now); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("unauthorized actor %d: %v", actor, err)
		}
	}
	if _, _, err := db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: admin.ID, Revision: admin.Revision, Action: "deactivate"}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("self deactivation %v", err)
	}
	if _, err := db.db.ExecContext(ctx, `CREATE TRIGGER lifecycle_audit_fail BEFORE INSERT ON user_lifecycle_events BEGIN SELECT RAISE(ABORT,'audit storage failed'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: user.Revision, Action: "deactivate"}, now); err == nil {
		t.Fatal("expected audit failure")
	}
	retained, err := db.GetAdminUser(ctx, user.ID)
	if err != nil || retained.Banned || retained.LifecycleStatus != "active" || retained.Revision != user.Revision {
		t.Fatalf("partial mutation committed: %+v %v", retained, err)
	}
}

func TestUserLifecycleIndependentWritersRestoreOriginalBan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lifecycle.db")
	first, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err = first.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	second, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	admin, err := first.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "writer-admin@example.test", PasswordHash: "hash", IsAdmin: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := first.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "writer-user@example.test", PasswordHash: "hash", Banned: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	request := UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: user.Revision, Action: "deactivate"}
	start := make(chan struct{})
	results := make(chan error, 8)
	for i := range 8 {
		go func() {
			<-start
			_, _, err := []*Store{first, second}[i%2].ChangeUserLifecycle(context.Background(), request, now)
			results <- err
		}()
	}
	close(start)
	for range 8 {
		if err := <-results; err != nil {
			t.Fatalf("independent writer %v", err)
		}
	}
	request.Revision++
	request.Action = "restore"
	restored, _, err := second.ChangeUserLifecycle(t.Context(), request, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Banned || restored.LifecycleStatus != "active" {
		t.Fatalf("restore bypassed pre-existing ban %+v", restored)
	}
	var count int
	if err = first.db.QueryRow(`SELECT count(*) FROM user_lifecycle_events WHERE user_id=?`, user.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("duplicate transitions %d %v", count, err)
	}
}
