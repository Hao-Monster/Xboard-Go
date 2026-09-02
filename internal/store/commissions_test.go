package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestSchemaV34PreservesV33DataAndAddsCommissionConstraints(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	removeSchemaV34ForMigrationTest(t, database)
	if _, err := database.db.ExecContext(ctx, `
		UPDATE app_settings SET app_name = 'V33 commission board', invite_commission = 17 WHERE id = 1;
		PRAGMA user_version = 33;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v33 to v34) error = %v", err)
	}
	var version, commissionAutoCheck, withdrawClose, distributionEnabled, l1, l2, l3, tables, indexes int
	var appName string
	if err := database.db.QueryRowContext(ctx, `
		SELECT app_name, commission_auto_check_enable, withdraw_close_enable,
			commission_distribution_enable, commission_distribution_l1,
			commission_distribution_l2, commission_distribution_l3
		FROM app_settings WHERE id = 1
	`).Scan(&appName, &commissionAutoCheck, &withdrawClose, &distributionEnabled, &l1, &l2, &l3); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='commission_logs'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='index' AND name IN ('idx_commission_logs_owner_created','idx_commission_logs_user')`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if version != CurrentSchemaVersion() || appName != "V33 commission board" || commissionAutoCheck != 1 || withdrawClose != 0 ||
		distributionEnabled != 0 || l1 != 100 || l2 != 0 || l3 != 0 || tables != 1 || indexes != 2 {
		t.Fatalf("v34 migration version=%d app=%q settings=%d/%d/%d/%d/%d/%d tables=%d indexes=%d", version, appName,
			commissionAutoCheck, withdrawClose, distributionEnabled, l1, l2, l3, tables, indexes)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE app_settings SET commission_distribution_l1 = 101 WHERE id = 1`); err == nil {
		t.Fatal("commission distribution percentage above 100 must be rejected")
	}
}

func TestCommissionSettingsAreRevisionSafeAndRejectUnsafeDistribution(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 29, 8, 0, 0, 0, time.UTC)
	initial, err := database.GetCommissionSettings(ctx)
	if err != nil {
		t.Fatalf("GetCommissionSettings() error = %v", err)
	}
	if initial.Revision != 1 || initial.InviteCommission != 10 || !initial.FirstTimeEnabled ||
		!initial.AutoCheckEnabled || initial.WithdrawClosed || initial.DistributionEnabled ||
		initial.DistributionL1 != 100 || initial.DistributionL2 != 0 || initial.DistributionL3 != 0 {
		t.Fatalf("initial commission settings = %#v", initial)
	}
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "commission-settings-admin@example.test", PasswordHash: "hash",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := database.UpdateCommissionSettings(ctx, administrator.ID, initial.Revision, SaveCommissionSettingsInput{
		InviteCommission: 25, FirstTimeEnabled: false, AutoCheckEnabled: false, WithdrawClosed: true,
		DistributionEnabled: true, DistributionL1: 50, DistributionL2: 30, DistributionL3: 20,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("UpdateCommissionSettings() error = %v", err)
	}
	if updated.Revision != 2 || updated.InviteCommission != 25 || updated.FirstTimeEnabled ||
		updated.AutoCheckEnabled || !updated.WithdrawClosed || !updated.DistributionEnabled ||
		updated.DistributionL1 != 50 || updated.DistributionL2 != 30 || updated.DistributionL3 != 20 ||
		!updated.UpdatedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("updated commission settings = %#v", updated)
	}
	if _, err := database.UpdateCommissionSettings(ctx, administrator.ID, initial.Revision, SaveCommissionSettingsInput{
		InviteCommission: 10, FirstTimeEnabled: true, AutoCheckEnabled: true,
		DistributionL1: 100,
	}, now.Add(2*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale UpdateCommissionSettings() error = %v, want ErrConflict", err)
	}
	for name, input := range map[string]SaveCommissionSettingsInput{
		"negative global":          {InviteCommission: -1, DistributionL1: 100},
		"global above one hundred": {InviteCommission: 101, DistributionL1: 100},
		"negative level":           {InviteCommission: 10, DistributionL1: -1},
		"level above one hundred":  {InviteCommission: 10, DistributionL1: 101},
		"distribution above one hundred": {
			InviteCommission: 10, DistributionEnabled: true, DistributionL1: 50, DistributionL2: 30, DistributionL3: 21,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := database.UpdateCommissionSettings(ctx, administrator.ID, updated.Revision, input, now.Add(3*time.Minute)); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("UpdateCommissionSettings(%#v) error = %v, want ErrInvalidInput", input, err)
			}
		})
	}
	preserved, err := database.GetCommissionSettings(ctx)
	if err != nil || !reflect.DeepEqual(preserved, updated) {
		t.Fatalf("invalid updates changed settings: got %#v want %#v err=%v", preserved, updated, err)
	}
}

func TestCommissionProcessingSummaryAndTransferAreExactlyOnce(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, invitedUserID := createOrderFixture(t, database, now, PlanPrices{"monthly": 1_000}, nil)
	inviter, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "commission-inviter@example.test", PasswordHash: "hash",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET invite_user_id = ? WHERE id = ?`, inviter.ID, invitedUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET commission_type = 1, commission_rate = 20 WHERE id = ?`, inviter.ID); err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: invitedUserID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if order.CommissionBalance != 200 {
		t.Fatalf("created commission = %d, want 200", order.CommissionBalance)
	}
	if _, err := database.CompleteOrder(ctx, order.TradeNo, "commission-callback", now); err != nil {
		t.Fatal(err)
	}

	early, err := database.ProcessCommissions(ctx, now.Add(72*time.Hour-time.Second), 100)
	if err != nil || early.Checked != 0 || early.Paid != 0 {
		t.Fatalf("early ProcessCommissions() = (%#v, %v)", early, err)
	}
	processed, err := database.ProcessCommissions(ctx, now.Add(72*time.Hour), 100)
	if err != nil || processed.Checked != 1 || processed.Paid != 1 || processed.Remaining != 0 {
		t.Fatalf("ProcessCommissions() = (%#v, %v)", processed, err)
	}
	repeated, err := database.ProcessCommissions(ctx, now.Add(73*time.Hour), 100)
	if err != nil || repeated.Checked != 0 || repeated.Paid != 0 || repeated.Remaining != 0 {
		t.Fatalf("repeated ProcessCommissions() = (%#v, %v)", repeated, err)
	}

	summary, err := database.GetInvitationSummary(ctx, inviter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.InvitedCount != 1 || summary.ValidCommission != 200 || summary.PendingCommission != 0 ||
		summary.CommissionRate != 20 || summary.AvailableCommission != 200 {
		t.Fatalf("commission summary = %#v", summary)
	}
	page, err := database.ListCommissionLogs(ctx, inviter.ID, 1, 10)
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].TradeNo != order.TradeNo ||
		page.Items[0].OrderAmount != 1_000 || page.Items[0].GetAmount != 200 {
		t.Fatalf("ListCommissionLogs() = (%#v, %v)", page, err)
	}
	transferred, err := database.TransferCommission(ctx, inviter.ID, CommissionTransferInput{Amount: 125, IdempotencyKey: "commission-transfer-basic-0001"}, now.Add(74*time.Hour))
	if err != nil || transferred.CommissionBalance != 75 || transferred.Balance != 125 {
		t.Fatalf("TransferCommission() = (%#v, %v)", transferred, err)
	}
	if _, err := database.TransferCommission(ctx, inviter.ID, CommissionTransferInput{Amount: 76, IdempotencyKey: "commission-transfer-overdraw-0001"}, now.Add(75*time.Hour)); !errors.Is(err, ErrInsufficientCommission) {
		t.Fatalf("overdraw transfer error = %v, want ErrInsufficientCommission", err)
	}
	if _, err := database.TransferCommission(ctx, inviter.ID, CommissionTransferInput{Amount: 0, IdempotencyKey: "commission-transfer-zero-0001"}, now.Add(75*time.Hour)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero transfer error = %v, want ErrInvalidInput", err)
	}
}

func TestCommissionDistributionAndConcurrentTransferPreserveMoney(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, buyerID := createOrderFixture(t, database, now, PlanPrices{"monthly": 10_000}, nil)
	level1, _ := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "commission-l1@example.test", PasswordHash: "hash"}, now)
	level2, _ := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "commission-l2@example.test", PasswordHash: "hash"}, now)
	level3, _ := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "commission-l3@example.test", PasswordHash: "hash"}, now)
	for _, relationship := range []struct{ child, parent int64 }{
		{buyerID, level1.ID}, {level1.ID, level2.ID}, {level2.ID, level3.ID},
	} {
		if _, err := database.db.ExecContext(ctx, `UPDATE users SET invite_user_id = ? WHERE id = ?`, relationship.parent, relationship.child); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET commission_type = 1 WHERE id = ?`, level1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE app_settings SET commission_distribution_enable = 1,
			commission_distribution_l1 = 50, commission_distribution_l2 = 30,
			commission_distribution_l3 = 20, withdraw_close_enable = 0 WHERE id = 1
	`); err != nil {
		t.Fatal(err)
	}
	summary, err := database.GetInvitationSummary(ctx, level1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.CommissionDistributionEnabled || !reflect.DeepEqual(summary.CommissionDistributionRates, []int{5, 3, 2}) {
		t.Fatalf("distribution summary = enabled %t rates %v, want true [5 3 2]", summary.CommissionDistributionEnabled, summary.CommissionDistributionRates)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: buyerID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CompleteOrder(ctx, order.TradeNo, "distribution-callback", now); err != nil {
		t.Fatal(err)
	}
	if result, err := database.ProcessCommissions(ctx, now.Add(72*time.Hour), 100); err != nil || result.Paid != 1 {
		t.Fatalf("ProcessCommissions(distribution) = (%#v, %v)", result, err)
	}
	for userID, want := range map[int64]int64{level1.ID: 500, level2.ID: 300, level3.ID: 200} {
		var got int64
		if err := database.db.QueryRowContext(ctx, `SELECT commission_balance FROM users WHERE id = ?`, userID).Scan(&got); err != nil || got != want {
			t.Fatalf("user %d commission balance = %d, want %d, err=%v", userID, got, want, err)
		}
	}

	var successes int
	var mutex sync.Mutex
	var wait sync.WaitGroup
	for index := range 10 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := database.TransferCommission(ctx, level1.ID, CommissionTransferInput{
				Amount: 75, IdempotencyKey: fmt.Sprintf("commission-transfer-concurrent-%02d", index),
			}, now.Add(73*time.Hour)); err == nil {
				mutex.Lock()
				successes++
				mutex.Unlock()
			} else if !errors.Is(err, ErrInsufficientCommission) {
				t.Errorf("concurrent transfer error = %v", err)
			}
		}()
	}
	wait.Wait()
	if successes != 6 {
		t.Fatalf("successful concurrent transfers = %d, want 6", successes)
	}
	var commissionBalance, balance int64
	if err := database.db.QueryRowContext(ctx, `SELECT commission_balance, balance FROM users WHERE id = ?`, level1.ID).Scan(&commissionBalance, &balance); err != nil {
		t.Fatal(err)
	}
	if commissionBalance != 50 || balance != 450 || commissionBalance+balance != 500 {
		t.Fatalf("concurrent transfer balances = commission %d + balance %d", commissionBalance, balance)
	}
}

func TestTransferCommissionIdempotencyAndLedger(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "transfer-ledger@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET commission_balance=1000,balance=250 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE app_settings SET currency='USD' WHERE id=1`); err != nil {
		t.Fatal(err)
	}

	input := CommissionTransferInput{Amount: 400, IdempotencyKey: "commission-transfer-retry-0001"}
	first, err := database.TransferCommission(ctx, user.ID, input, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if first.Idempotent || first.TransferID < 1 || first.Amount != 400 || first.Currency != "USD" ||
		first.CommissionBalanceBefore != 1000 || first.CommissionBalanceAfter != 600 ||
		first.BalanceBefore != 250 || first.BalanceAfter != 650 ||
		first.CommissionBalance != 600 || first.Balance != 650 {
		t.Fatalf("first transfer = %#v", first)
	}
	retry, err := database.TransferCommission(ctx, user.ID, input, now.Add(2*time.Minute))
	if err != nil || !retry.Idempotent || retry.TransferID != first.TransferID || !retry.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("retry transfer = (%#v, %v), first=%#v", retry, err, first)
	}
	if _, err := database.TransferCommission(ctx, user.ID, CommissionTransferInput{
		Amount: 401, IdempotencyKey: input.IdempotencyKey,
	}, now.Add(3*time.Minute)); !errors.Is(err, ErrCommissionTransferIdempotencyConflict) {
		t.Fatalf("conflicting retry error = %v", err)
	}
	var events, commissionBalance, balance int64
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM commission_transfer_events WHERE user_id=?`, user.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT commission_balance,balance FROM users WHERE id=?`, user.ID).Scan(&commissionBalance, &balance); err != nil {
		t.Fatal(err)
	}
	if events != 1 || commissionBalance != 600 || balance != 650 {
		t.Fatalf("retry ledger/balances = events %d, commission %d, balance %d", events, commissionBalance, balance)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE commission_transfer_events SET amount=amount WHERE id=?`, first.TransferID); err == nil {
		t.Fatal("commission transfer event update succeeded, want immutable ledger")
	}
}

func TestConcurrentCommissionTransferRetryAppliesOnce(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 27, 13, 0, 0, 0, time.UTC)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "transfer-race@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET commission_balance=500,balance=100 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	input := CommissionTransferInput{Amount: 300, IdempotencyKey: "commission-transfer-race-0001"}
	results := make(chan CommissionTransferResult, 12)
	errorsSeen := make(chan error, 12)
	var wait sync.WaitGroup
	for range 12 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := database.TransferCommission(ctx, user.ID, input, now.Add(time.Minute))
			if err != nil {
				errorsSeen <- err
				return
			}
			results <- result
		}()
	}
	wait.Wait()
	close(results)
	close(errorsSeen)
	for err := range errorsSeen {
		t.Errorf("concurrent retry error = %v", err)
	}
	var fresh, cached int
	var transferID int64
	for result := range results {
		if transferID == 0 {
			transferID = result.TransferID
		} else if result.TransferID != transferID {
			t.Errorf("concurrent retry transfer ID = %d, want %d", result.TransferID, transferID)
		}
		if result.Idempotent {
			cached++
		} else {
			fresh++
		}
	}
	var events, commissionBalance, balance int64
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM commission_transfer_events WHERE user_id=?`, user.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT commission_balance,balance FROM users WHERE id=?`, user.ID).Scan(&commissionBalance, &balance); err != nil {
		t.Fatal(err)
	}
	if fresh != 1 || cached != 11 || events != 1 || commissionBalance != 200 || balance != 400 {
		t.Fatalf("concurrent retries = fresh %d cached %d events %d balances %d/%d", fresh, cached, events, commissionBalance, balance)
	}
}

func TestConcurrentCommissionTransferRetryAcrossStoresAppliesOnce(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2026, 8, 27, 14, 0, 0, 0, time.UTC)
	databasePath := filepath.Join(t.TempDir(), "commission-transfer.db")
	first, err := OpenSQLite(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := first.CreateAdminUser(ctx, CreateAdminUserInput{Email: "transfer-multistore@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.ExecContext(ctx, `UPDATE users SET commission_balance=500,balance=100 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	second, err := OpenSQLite(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	input := CommissionTransferInput{Amount: 300, IdempotencyKey: "commission-transfer-multistore-0001"}
	results := make(chan CommissionTransferResult, 2)
	errorsSeen := make(chan error, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for _, database := range []*Store{first, second} {
		wait.Add(1)
		go func(database *Store) {
			defer wait.Done()
			<-start
			result, transferErr := database.TransferCommission(ctx, user.ID, input, now.Add(time.Minute))
			if transferErr != nil {
				errorsSeen <- transferErr
				return
			}
			results <- result
		}(database)
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsSeen)
	for transferErr := range errorsSeen {
		t.Errorf("multi-store concurrent retry error = %v", transferErr)
	}
	var fresh, cached int
	var transferID int64
	for result := range results {
		if transferID == 0 {
			transferID = result.TransferID
		} else if result.TransferID != transferID {
			t.Errorf("multi-store transfer ID = %d, want %d", result.TransferID, transferID)
		}
		if result.Idempotent {
			cached++
		} else {
			fresh++
		}
	}
	var events, commissionBalance, balance int64
	if err := first.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM commission_transfer_events WHERE user_id=?`, user.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := first.db.QueryRowContext(ctx, `SELECT commission_balance,balance FROM users WHERE id=?`, user.ID).Scan(&commissionBalance, &balance); err != nil {
		t.Fatal(err)
	}
	if fresh != 1 || cached != 1 || events != 1 || commissionBalance != 200 || balance != 400 {
		t.Fatalf("multi-store retries = fresh %d cached %d events %d balances %d/%d", fresh, cached, events, commissionBalance, balance)
	}
}
