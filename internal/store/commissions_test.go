package store

import (
	"context"
	"errors"
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
	if err != nil || preserved != updated {
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
	transferred, err := database.TransferCommission(ctx, inviter.ID, 125, now.Add(74*time.Hour))
	if err != nil || transferred.CommissionBalance != 75 || transferred.Balance != 125 {
		t.Fatalf("TransferCommission() = (%#v, %v)", transferred, err)
	}
	if _, err := database.TransferCommission(ctx, inviter.ID, 76, now.Add(75*time.Hour)); !errors.Is(err, ErrInsufficientCommission) {
		t.Fatalf("overdraw transfer error = %v, want ErrInsufficientCommission", err)
	}
	if _, err := database.TransferCommission(ctx, inviter.ID, 0, now.Add(75*time.Hour)); !errors.Is(err, ErrInvalidInput) {
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
	for range 10 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := database.TransferCommission(ctx, level1.ID, 75, now.Add(73*time.Hour)); err == nil {
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
