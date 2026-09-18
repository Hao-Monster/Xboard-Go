package store

import (
	"strings"
	"testing"
	"time"
)

type commissionTransferSnapshot struct {
	commissionBalance int64
	balance           int64
	adminRevision     int64
	commissionLogs    int64
	withdrawals       int64
	withdrawalEvents  int64
	adminAudits       int64
}

func readCommissionTransferSnapshot(t *testing.T, database *Store, userID int64) commissionTransferSnapshot {
	t.Helper()
	ctx := t.Context()
	var snapshot commissionTransferSnapshot
	if err := database.db.QueryRowContext(ctx, `
		SELECT commission_balance, balance, admin_revision FROM users WHERE id = ?
	`, userID).Scan(&snapshot.commissionBalance, &snapshot.balance, &snapshot.adminRevision); err != nil {
		t.Fatal(err)
	}
	for query, target := range map[string]*int64{
		`SELECT COUNT(*) FROM commission_logs`:              &snapshot.commissionLogs,
		`SELECT COUNT(*) FROM commission_withdrawals`:       &snapshot.withdrawals,
		`SELECT COUNT(*) FROM commission_withdrawal_events`: &snapshot.withdrawalEvents,
		`SELECT COUNT(*) FROM admin_audit_logs`:             &snapshot.adminAudits,
	} {
		if err := database.db.QueryRowContext(ctx, query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	return snapshot
}

func TestTransferCommissionFailureRollsBackFundsAndSingleRetrySucceeds(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "commission-transfer-retry@example.test", PasswordHash: "hash",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE users SET commission_balance = 500, balance = 125 WHERE id = ?
	`, user.ID); err != nil {
		t.Fatal(err)
	}
	before := readCommissionTransferSnapshot(t, database, user.ID)
	if before.commissionBalance+before.balance != 625 {
		t.Fatalf("initial total funds = %d, want 625", before.commissionBalance+before.balance)
	}

	if _, err := database.db.ExecContext(ctx, `
		CREATE TRIGGER fail_commission_transfer_after_update
		AFTER UPDATE OF commission_balance, balance ON users
		WHEN NEW.commission_balance < OLD.commission_balance AND NEW.balance > OLD.balance
		BEGIN
			SELECT RAISE(ABORT, 'injected commission transfer failure');
		END
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.TransferCommission(ctx, user.ID, 200, now.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "injected commission transfer failure") {
		t.Fatalf("TransferCommission() error = %v, want injected failure", err)
	}
	afterFailure := readCommissionTransferSnapshot(t, database, user.ID)
	if afterFailure != before {
		t.Fatalf("failed transfer changed funds or ledger/audit state: got %#v want %#v", afterFailure, before)
	}
	if afterFailure.commissionBalance+afterFailure.balance != 625 {
		t.Fatalf("total funds after failed transfer = %d, want 625", afterFailure.commissionBalance+afterFailure.balance)
	}

	if _, err := database.db.ExecContext(ctx, `DROP TRIGGER fail_commission_transfer_after_update`); err != nil {
		t.Fatal(err)
	}
	transferred, err := database.TransferCommission(ctx, user.ID, 200, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("retry TransferCommission() error = %v", err)
	}
	if transferred.CommissionBalance != 300 || transferred.Balance != 325 {
		t.Fatalf("retry TransferCommission() = %#v, want commission 300 and balance 325", transferred)
	}
	afterRetry := readCommissionTransferSnapshot(t, database, user.ID)
	wantAfterRetry := before
	wantAfterRetry.commissionBalance = 300
	wantAfterRetry.balance = 325
	wantAfterRetry.adminRevision++
	if afterRetry != wantAfterRetry {
		t.Fatalf("retry funds or ledger/audit state = %#v, want %#v", afterRetry, wantAfterRetry)
	}
	if afterRetry.commissionBalance+afterRetry.balance != 625 {
		t.Fatalf("total funds after retry = %d, want 625", afterRetry.commissionBalance+afterRetry.balance)
	}
}
