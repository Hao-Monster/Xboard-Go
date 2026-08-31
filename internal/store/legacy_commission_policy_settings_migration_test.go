package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestImportLegacyCommissionPolicySettingsIsComposableIdempotentAndDriftSafe(t *testing.T) {
	database := newTestStore(t)
	if _, err := database.db.Exec(`
		UPDATE app_settings SET invite_force=1,invite_gen_limit=9,invite_never_expire=1,
			commission_withdraw_limit=25050,commission_withdraw_method='["USDT"]',
			plan_change_enable=0,app_name='Migrated identity' WHERE id=1;
		UPDATE theme_settings SET sidebar_style='dark' WHERE id=1;
	`); err != nil {
		t.Fatal(err)
	}
	settings := nonDefaultLegacyCommissionPolicySettings()
	input := validLegacyCommissionPolicySettingsImport(settings)
	now := time.Date(2026, 8, 31, 16, 0, 0, 0, time.UTC)
	report, err := database.ImportLegacyCommissionPolicySettings(t.Context(), input, now)
	if err != nil || report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		t.Fatalf("ImportLegacyCommissionPolicySettings()=(%#v,%v)", report, err)
	}
	commission, err := database.GetCommissionSettings(t.Context())
	if err != nil || commission.Revision != 2 || commission.InviteCommission != 25 || commission.FirstTimeEnabled ||
		commission.AutoCheckEnabled || !commission.WithdrawClosed || !commission.DistributionEnabled ||
		commission.DistributionL1 != 50 || commission.DistributionL2 != 30 || commission.DistributionL3 != 20 ||
		commission.WithdrawLimit != 25050 || !reflect.DeepEqual(commission.WithdrawMethods, []string{"USDT"}) {
		t.Fatalf("commission=%#v err=%v", commission, err)
	}
	var inviteForce, inviteLimit, inviteNeverExpire, planChange int
	var appName, sidebar string
	if err := database.db.QueryRow(`SELECT invite_force,invite_gen_limit,invite_never_expire,plan_change_enable,app_name FROM app_settings WHERE id=1`).
		Scan(&inviteForce, &inviteLimit, &inviteNeverExpire, &planChange, &appName); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT sidebar_style FROM theme_settings WHERE id=1`).Scan(&sidebar); err != nil {
		t.Fatal(err)
	}
	if inviteForce != 1 || inviteLimit != 9 || inviteNeverExpire != 1 || planChange != 0 || appName != "Migrated identity" || sidebar != "dark" {
		t.Fatalf("unrelated fields=%d/%d/%d/%d/%q/%q", inviteForce, inviteLimit, inviteNeverExpire, planChange, appName, sidebar)
	}
	repeated, err := database.ImportLegacyCommissionPolicySettings(t.Context(), input, now.Add(time.Hour))
	if err != nil || !repeated.AlreadyApplied || !repeated.AppliedAt.Equal(now) {
		t.Fatalf("idempotent import=(%#v,%v)", repeated, err)
	}
	if _, err := database.db.Exec(`UPDATE app_settings SET commission_auto_check_enable=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.LookupLegacyCommissionPolicySettingsImport(t.Context(), input.SourceSHA256); !errors.Is(err, ErrConflict) {
		t.Fatalf("drifted lookup error=%v, want ErrConflict", err)
	}
}

func TestImportLegacyCommissionPolicySettingsHandlesSourceDefaultsAndRejectsConflicts(t *testing.T) {
	defaults := DefaultLegacyCommissionPolicySettings()
	input := validLegacyCommissionPolicySettingsImport(defaults)
	database := newTestStore(t)
	report, err := database.ImportLegacyCommissionPolicySettings(t.Context(), input, time.Unix(1, 0))
	if err != nil || report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		t.Fatalf("default import=(%#v,%v)", report, err)
	}
	commission, err := database.GetCommissionSettings(t.Context())
	if err != nil || commission.DistributionL1 != 0 || commission.DistributionEnabled {
		t.Fatalf("legacy source defaults=%#v err=%v", commission, err)
	}
	runtimeNow := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	plan, buyerID := createOrderFixture(t, database, runtimeNow, PlanPrices{"monthly": 1_000}, nil)
	inviter, err := database.CreateAdminUser(t.Context(), CreateAdminUserInput{
		Email: "legacy-policy-default-inviter@example.test", PasswordHash: "hash",
	}, runtimeNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(t.Context(), `UPDATE users SET invite_user_id=? WHERE id=?`, inviter.ID, buyerID); err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(t.Context(), CreateOrderInput{
		UserID: buyerID, PlanID: plan.ID, Period: "monthly",
	}, runtimeNow)
	if err != nil || order.CommissionBalance != 100 {
		t.Fatalf("legacy-default commission order=%#v err=%v", order, err)
	}
	if _, err := database.CompleteOrder(t.Context(), order.TradeNo, "legacy-policy-default", runtimeNow); err != nil {
		t.Fatal(err)
	}
	if result, err := database.ProcessCommissions(t.Context(), runtimeNow.Add(72*time.Hour), 100); err != nil || result.Paid != 1 {
		t.Fatalf("ProcessCommissions(legacy defaults)=(%#v,%v)", result, err)
	}
	var balance int64
	if err := database.db.QueryRowContext(t.Context(), `SELECT commission_balance FROM users WHERE id=?`, inviter.ID).Scan(&balance); err != nil || balance != 100 {
		t.Fatalf("legacy-default inviter commission balance=%d err=%v, want 100", balance, err)
	}

	nonPristine := newTestStore(t)
	if _, err := nonPristine.db.Exec(`UPDATE app_settings SET commission_distribution_l1=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := nonPristine.ImportLegacyCommissionPolicySettings(t.Context(), input, time.Unix(2, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("non-pristine import error=%v, want ErrConflict", err)
	}

	changedSource := newTestStore(t)
	if _, err := changedSource.ImportLegacyCommissionPolicySettings(t.Context(), input, time.Unix(3, 0)); err != nil {
		t.Fatal(err)
	}
	changed := input
	changed.Settings.InviteCommission = 11
	changed.Checksum = LegacyCommissionPolicySettingsChecksum(changed.Settings)
	if _, err := changedSource.ImportLegacyCommissionPolicySettings(t.Context(), changed, time.Unix(4, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed same-source error=%v, want ErrConflict", err)
	}

	differentSource := newTestStore(t)
	if _, err := differentSource.ImportLegacyCommissionPolicySettings(t.Context(), input, time.Unix(5, 0)); err != nil {
		t.Fatal(err)
	}
	input.SourceSHA256 = strings.Repeat("f", 64)
	if _, err := differentSource.ImportLegacyCommissionPolicySettings(t.Context(), input, time.Unix(6, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("different-source error=%v, want ErrConflict", err)
	}

	invalid := validLegacyCommissionPolicySettingsImport(defaults)
	invalid.Settings.DistributionL1 = 50
	invalid.Settings.DistributionL2 = 30
	invalid.Settings.DistributionL3 = 21
	invalid.Checksum = LegacyCommissionPolicySettingsChecksum(invalid.Settings)
	if _, err := newTestStore(t).ImportLegacyCommissionPolicySettings(t.Context(), invalid, time.Unix(7, 0)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid distribution error=%v, want ErrInvalidInput", err)
	}
}

func nonDefaultLegacyCommissionPolicySettings() LegacyCommissionPolicySettings {
	return LegacyCommissionPolicySettings{
		InviteCommission: 25, FirstTimeEnabled: false, AutoCheckEnabled: false,
		WithdrawClosed: true, DistributionEnabled: true,
		DistributionL1: 50, DistributionL2: 30, DistributionL3: 20,
	}
}

func validLegacyCommissionPolicySettingsImport(settings LegacyCommissionPolicySettings) LegacyCommissionPolicySettingsImport {
	return LegacyCommissionPolicySettingsImport{
		Slice: LegacyCommissionPolicySettingsSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 1024,
		Settings: settings, Checksum: LegacyCommissionPolicySettingsChecksum(settings),
		RollbackBackupPath: "backup.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
	}
}
