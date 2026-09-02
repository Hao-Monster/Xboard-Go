package store

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSchemaV59MigratesCommissionAndLifecycleWithoutChangingBalances(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "schema-v59-user@example.test", PasswordHash: "hash",
	}, time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET commission_balance=12345 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		DROP INDEX IF EXISTS idx_users_due_anonymization;
		DROP TABLE IF EXISTS user_lifecycle_events;
		DROP TABLE IF EXISTS commission_withdrawal_events;
		DROP TABLE IF EXISTS commission_withdrawals;
		ALTER TABLE users DROP COLUMN anonymized_at;
		ALTER TABLE users DROP COLUMN deletion_banned_snapshot;
		ALTER TABLE users DROP COLUMN deletion_due_at;
		ALTER TABLE users DROP COLUMN deletion_requested_at;
		ALTER TABLE users DROP COLUMN lifecycle_status;
		ALTER TABLE users DROP COLUMN frozen_commission_balance;
		PRAGMA user_version=59;
	`); err != nil {
		t.Fatalf("remove v60/v61 schema: %v", err)
	}
	if err := database.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate() v59 to v61 error = %v", err)
	}

	var version int
	var available, frozen int64
	var lifecycle string
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT commission_balance, frozen_commission_balance, lifecycle_status FROM users WHERE id=?`, user.ID).Scan(&available, &frozen, &lifecycle); err != nil {
		t.Fatal(err)
	}
	if version != CurrentSchemaVersion() || available != 12345 || frozen != 0 || lifecycle != UserLifecycleActive {
		t.Fatalf("migrated state = version %d available %d frozen %d lifecycle %q", version, available, frozen, lifecycle)
	}
	var tableCount int
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name IN ('commission_withdrawals','commission_withdrawal_events','user_lifecycle_events')
	`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 3 {
		t.Fatalf("migrated M1 tables = %d, want 3", tableCount)
	}
}

func TestCommissionWithdrawalFreezesOnceAndRejectRestoresExactlyOnce(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "withdraw-user@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "withdraw-admin@example.test", PasswordHash: "hash", IsAdmin: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE users SET commission_balance=12500 WHERE id=?;
		UPDATE app_settings SET currency='CNY', commission_withdraw_limit=10000,
			commission_withdraw_method='["USDT","银行转账"]' WHERE id=1
	`, user.ID); err != nil {
		t.Fatal(err)
	}

	input := CreateCommissionWithdrawalInput{
		IdempotencyKey: "test-0000000000000001", Method: "USDT",
		AccountCipher: []byte("encrypted-account"), AccountFingerprint: withdrawalFingerprint(1), AccountMasked: "****6789",
	}
	created, err := database.CreateCommissionWithdrawal(ctx, user.ID, input, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("CreateCommissionWithdrawal() error = %v", err)
	}
	if created.UserID != user.ID || created.Amount != 12500 || created.FeeBasisPoints != 0 || created.FeeAmount != 0 || created.NetAmount != 12500 || created.Currency != "CNY" || created.Status != CommissionWithdrawalPending ||
		created.Revision != 1 || created.Method != "USDT" || created.AccountMasked != "****6789" {
		t.Fatalf("created withdrawal = %#v", created)
	}
	repeated, err := database.CreateCommissionWithdrawal(ctx, user.ID, input, now.Add(2*time.Minute))
	if err != nil || repeated.ID != created.ID || repeated.Revision != 1 {
		t.Fatalf("repeated CreateCommissionWithdrawal() = (%#v, %v)", repeated, err)
	}
	changedAccount := input
	changedAccount.AccountCipher = []byte("encrypted-other-account")
	changedAccount.AccountFingerprint = withdrawalFingerprint(2)
	if _, err := database.CreateCommissionWithdrawal(ctx, user.ID, changedAccount, now.Add(2*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused idempotency key with another account error = %v, want ErrConflict", err)
	}
	var available, frozen int64
	if err := database.db.QueryRowContext(ctx, `SELECT commission_balance, frozen_commission_balance FROM users WHERE id=?`, user.ID).Scan(&available, &frozen); err != nil {
		t.Fatal(err)
	}
	if available != 0 || frozen != 12500 {
		t.Fatalf("frozen balances = available %d frozen %d", available, frozen)
	}
	if _, err := database.CreateCommissionWithdrawal(ctx, user.ID, CreateCommissionWithdrawalInput{
		IdempotencyKey: "test-0000000000000002", Method: "USDT", AccountCipher: []byte("other"), AccountFingerprint: withdrawalFingerprint(3), AccountMasked: "****0002",
	}, now.Add(3*time.Minute)); !errors.Is(err, ErrCommissionWithdrawalActive) {
		t.Fatalf("second active withdrawal error = %v, want ErrCommissionWithdrawalActive", err)
	}
	if _, err := database.ApproveCommissionWithdrawal(ctx, user.ID, created.ID, created.Revision, now.Add(3*time.Minute+30*time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-administrator approval error = %v, want ErrNotFound", err)
	}
	approved, err := database.ApproveCommissionWithdrawal(ctx, admin.ID, created.ID, created.Revision, now.Add(4*time.Minute))
	if err != nil || approved.Status != CommissionWithdrawalApproved || approved.Revision != 2 {
		t.Fatalf("ApproveCommissionWithdrawal() = (%#v, %v)", approved, err)
	}
	rejected, err := database.RejectCommissionWithdrawal(ctx, admin.ID, created.ID, approved.Revision, "账户信息无法验证", now.Add(5*time.Minute))
	if err != nil || rejected.Status != CommissionWithdrawalRejected || rejected.Revision != 3 {
		t.Fatalf("RejectCommissionWithdrawal() = (%#v, %v)", rejected, err)
	}
	if _, err := database.RejectCommissionWithdrawal(ctx, admin.ID, created.ID, rejected.Revision, "repeat", now.Add(6*time.Minute)); !errors.Is(err, ErrCommissionWithdrawalState) {
		t.Fatalf("repeated rejection error = %v, want ErrCommissionWithdrawalState", err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT commission_balance, frozen_commission_balance FROM users WHERE id=?`, user.ID).Scan(&available, &frozen); err != nil {
		t.Fatal(err)
	}
	if available != 12500 || frozen != 0 {
		t.Fatalf("restored balances = available %d frozen %d", available, frozen)
	}
}

func TestCommissionWithdrawalConcurrentCreateAndPaidTransitionPreserveMoney(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	user, _ := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "withdraw-race@example.test", PasswordHash: "hash"}, now)
	admin, _ := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "withdraw-race-admin@example.test", PasswordHash: "hash", IsAdmin: true}, now)
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET commission_balance=20000 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	var mutex sync.Mutex
	created := make([]CommissionWithdrawal, 0, 1)
	for index := range 10 {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			item, err := database.CreateCommissionWithdrawal(ctx, user.ID, CreateCommissionWithdrawalInput{
				IdempotencyKey: "withdrawal-race-key-0000000" + string(rune('A'+index)),
				Method:         "支付宝", AccountCipher: []byte("cipher"), AccountFingerprint: withdrawalFingerprint(byte(index + 10)), AccountMasked: "a***@example.test",
			}, now.Add(time.Minute))
			if err == nil {
				mutex.Lock()
				created = append(created, item)
				mutex.Unlock()
			} else if !errors.Is(err, ErrCommissionWithdrawalActive) && !errors.Is(err, ErrInsufficientCommission) {
				t.Errorf("concurrent create error = %v", err)
			}
		}(index)
	}
	wait.Wait()
	if len(created) != 1 {
		t.Fatalf("concurrent successful withdrawals = %d, want 1", len(created))
	}
	approved, err := database.ApproveCommissionWithdrawal(ctx, admin.ID, created[0].ID, 1, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	paid, err := database.PayCommissionWithdrawal(ctx, admin.ID, approved.ID, approved.Revision, "BANK-20260902-0001", now.Add(3*time.Minute))
	if err != nil || paid.Status != CommissionWithdrawalPaid || paid.Revision != 3 || paid.ExternalReference != "BANK-20260902-0001" {
		t.Fatalf("PayCommissionWithdrawal() = (%#v, %v)", paid, err)
	}
	var available, frozen int64
	if err := database.db.QueryRowContext(ctx, `SELECT commission_balance, frozen_commission_balance FROM users WHERE id=?`, user.ID).Scan(&available, &frozen); err != nil {
		t.Fatal(err)
	}
	if available != 0 || frozen != 0 {
		t.Fatalf("paid balances = available %d frozen %d", available, frozen)
	}
	if _, err := database.RejectCommissionWithdrawal(ctx, admin.ID, paid.ID, paid.Revision, "invalid", now.Add(4*time.Minute)); !errors.Is(err, ErrCommissionWithdrawalState) {
		t.Fatalf("paid rejection error = %v, want ErrCommissionWithdrawalState", err)
	}
	secondUser, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "withdraw-reference-race@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET commission_balance=15000 WHERE id=?`, secondUser.ID); err != nil {
		t.Fatal(err)
	}
	second, err := database.CreateCommissionWithdrawal(ctx, secondUser.ID, CreateCommissionWithdrawalInput{
		IdempotencyKey: "withdrawal-unique-reference-0001", Method: "支付宝", AccountCipher: []byte("cipher"), AccountFingerprint: withdrawalFingerprint(30), AccountMasked: "a***@example.test",
	}, now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	second, err = database.ApproveCommissionWithdrawal(ctx, admin.ID, second.ID, second.Revision, now.Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.PayCommissionWithdrawal(ctx, admin.ID, second.ID, second.Revision, paid.ExternalReference, now.Add(7*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate external reference error = %v, want ErrConflict", err)
	}
	if _, err := database.RejectCommissionWithdrawal(ctx, admin.ID, second.ID, second.Revision, "duplicate payment reference", now.Add(8*time.Minute)); err != nil {
		t.Fatalf("reject after duplicate reference conflict: %v", err)
	}
}

func withdrawalFingerprint(seed byte) []byte {
	return bytes.Repeat([]byte{seed}, 32)
}
