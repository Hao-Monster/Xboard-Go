package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestImportLegacyConfigurationCompatibilitySettingsIsAtomicIdempotentAndDetectsDrift(t *testing.T) {
	database := newTestStore(t)
	settings := LegacyConfigurationCompatibilitySettings{
		CommissionWithdrawLimit: 25_050, CommissionWithdrawMethod: []string{"USDT"},
		SidebarStyle: "dark", HeaderStyle: "light",
	}
	input := validLegacyConfigurationCompatibilitySettingsImport(settings)
	now := time.Date(2026, 8, 30, 15, 0, 0, 0, time.UTC)
	report, err := database.ImportLegacyConfigurationCompatibilitySettings(t.Context(), input, now)
	if err != nil || report.Settings.SourceChecksum != report.Settings.TargetChecksum || report.Settings.SourceRows != 1 {
		t.Fatalf("ImportLegacyConfigurationCompatibilitySettings()=(%#v,%v)", report, err)
	}
	invite, err := database.GetLegacyInvitationSettings(t.Context())
	if err != nil || invite.WithdrawLimit != 25_050 || strings.Join(invite.WithdrawMethods, ",") != "USDT" {
		t.Fatalf("imported commission compatibility settings=%#v err=%v", invite, err)
	}
	frontend, err := database.GetLegacyFrontendSettings(t.Context())
	if err != nil || frontend.SidebarStyle != "dark" || frontend.HeaderStyle != "light" {
		t.Fatalf("imported frontend compatibility settings=%#v err=%v", frontend, err)
	}
	repeated, err := database.ImportLegacyConfigurationCompatibilitySettings(t.Context(), input, now.Add(time.Hour))
	if err != nil || !repeated.AlreadyApplied || !repeated.AppliedAt.Equal(now) {
		t.Fatalf("idempotent import=(%#v,%v)", repeated, err)
	}
	if _, err := database.db.Exec(`UPDATE theme_settings SET header_style='dark' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ImportLegacyConfigurationCompatibilitySettings(t.Context(), input, now.Add(2*time.Hour)); !errors.Is(err, ErrConflict) {
		t.Fatalf("drifted import error=%v, want ErrConflict", err)
	}
}

func TestImportLegacyConfigurationCompatibilitySettingsRequiresPristineValidatedTarget(t *testing.T) {
	settings := LegacyConfigurationCompatibilitySettings{
		CommissionWithdrawLimit: 25_050, CommissionWithdrawMethod: []string{"USDT"},
		SidebarStyle: "dark", HeaderStyle: "light",
	}
	input := validLegacyConfigurationCompatibilitySettingsImport(settings)
	nonPristine := newTestStore(t)
	if _, err := nonPristine.db.Exec(`UPDATE app_settings SET commission_withdraw_limit=10100 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := nonPristine.ImportLegacyConfigurationCompatibilitySettings(t.Context(), input, time.Unix(1, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("non-pristine import error=%v, want ErrConflict", err)
	}
	invalid := input
	invalid.Settings.CommissionWithdrawMethod = []string{""}
	invalid.Checksum = LegacyConfigurationCompatibilitySettingsChecksum(invalid.Settings)
	if _, err := newTestStore(t).ImportLegacyConfigurationCompatibilitySettings(t.Context(), invalid, time.Unix(1, 0)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid import error=%v, want ErrInvalidInput", err)
	}
}

func validLegacyConfigurationCompatibilitySettingsImport(settings LegacyConfigurationCompatibilitySettings) LegacyConfigurationCompatibilitySettingsImport {
	return LegacyConfigurationCompatibilitySettingsImport{
		Slice: LegacyConfigurationCompatibilitySettingsSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 1024,
		Settings: settings, Checksum: LegacyConfigurationCompatibilitySettingsChecksum(settings),
		RollbackBackupPath: "backup.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
	}
}
