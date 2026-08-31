package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestImportLegacySubscriptionPolicySettingsIsComposableIdempotentAndDriftSafe(t *testing.T) {
	database := newTestStore(t)
	if _, err := database.db.Exec(`
		UPDATE app_settings SET app_name='Migrated identity',traffic_reset_method=4 WHERE id=1;
		UPDATE subscription_settings SET path='legacy_feed',show_info=1 WHERE id=1;
		UPDATE subscription_templates SET content='preserved-template' WHERE name='clash';
	`); err != nil {
		t.Fatal(err)
	}
	settings := nonDefaultLegacySubscriptionPolicySettings()
	input := validLegacySubscriptionPolicySettingsImport(settings)
	now := time.Date(2026, 8, 31, 14, 0, 0, 0, time.UTC)
	report, err := database.ImportLegacySubscriptionPolicySettings(t.Context(), input, now)
	if err != nil || report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		t.Fatalf("ImportLegacySubscriptionPolicySettings()=(%#v,%v)", report, err)
	}
	policy, err := database.GetSubscriptionPolicySettings(t.Context())
	if err != nil || policy.Revision != 2 || policy.PlanChangeEnabled || policy.ResetTrafficMethod != 4 ||
		policy.SurplusEnabled || policy.NewOrderEventID != 1 || policy.RenewOrderEventID != 0 || policy.ChangeOrderEventID != 1 ||
		policy.DefaultRemindExpire || !policy.DefaultRemindTraffic {
		t.Fatalf("policy=%#v err=%v", policy, err)
	}
	var appName string
	if err := database.db.QueryRow(`SELECT app_name FROM app_settings WHERE id=1`).Scan(&appName); err != nil || appName != "Migrated identity" {
		t.Fatalf("app_name=%q err=%v", appName, err)
	}
	var path, template string
	var showInfo bool
	if err := database.db.QueryRow(`SELECT path,show_info FROM subscription_settings WHERE id=1`).Scan(&path, &showInfo); err != nil || path != "legacy_feed" || !showInfo {
		t.Fatalf("subscription output path=%q show_info=%t err=%v", path, showInfo, err)
	}
	if err := database.db.QueryRow(`SELECT content FROM subscription_templates WHERE name='clash'`).Scan(&template); err != nil || template != "preserved-template" {
		t.Fatalf("subscription template=%q err=%v", template, err)
	}
	repeated, err := database.ImportLegacySubscriptionPolicySettings(t.Context(), input, now.Add(time.Hour))
	if err != nil || !repeated.AlreadyApplied || !repeated.AppliedAt.Equal(now) {
		t.Fatalf("idempotent import=(%#v,%v)", repeated, err)
	}
	if _, err := database.db.Exec(`UPDATE app_settings SET new_order_event_id=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.LookupLegacySubscriptionPolicySettingsImport(t.Context(), input.SourceSHA256); !errors.Is(err, ErrConflict) {
		t.Fatalf("drifted lookup error=%v, want ErrConflict", err)
	}
}

func TestImportLegacySubscriptionPolicySettingsRejectsNonPristineChangedAndDifferentSources(t *testing.T) {
	settings := nonDefaultLegacySubscriptionPolicySettings()
	input := validLegacySubscriptionPolicySettingsImport(settings)

	nonPristine := newTestStore(t)
	if _, err := nonPristine.db.Exec(`UPDATE app_settings SET default_remind_expire=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := nonPristine.ImportLegacySubscriptionPolicySettings(t.Context(), input, time.Unix(1, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("non-pristine import error=%v, want ErrConflict", err)
	}

	changedSource := newTestStore(t)
	if _, err := changedSource.ImportLegacySubscriptionPolicySettings(t.Context(), input, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	changed := input
	changed.Settings.DefaultRemindTraffic = false
	changed.Checksum = LegacySubscriptionPolicySettingsChecksum(changed.Settings)
	if _, err := changedSource.ImportLegacySubscriptionPolicySettings(t.Context(), changed, time.Unix(3, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed same-source error=%v, want ErrConflict", err)
	}

	differentSource := newTestStore(t)
	if _, err := differentSource.ImportLegacySubscriptionPolicySettings(t.Context(), input, time.Unix(4, 0)); err != nil {
		t.Fatal(err)
	}
	input.SourceSHA256 = strings.Repeat("f", 64)
	if _, err := differentSource.ImportLegacySubscriptionPolicySettings(t.Context(), input, time.Unix(5, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("different-source error=%v, want ErrConflict", err)
	}

	invalid := validLegacySubscriptionPolicySettingsImport(settings)
	invalid.Settings.NewOrderEventID = 2
	invalid.Checksum = LegacySubscriptionPolicySettingsChecksum(invalid.Settings)
	if _, err := newTestStore(t).ImportLegacySubscriptionPolicySettings(t.Context(), invalid, time.Unix(6, 0)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid event error=%v, want ErrInvalidInput", err)
	}
}

func nonDefaultLegacySubscriptionPolicySettings() LegacySubscriptionPolicySettings {
	return LegacySubscriptionPolicySettings{
		PlanChangeEnabled: false, SurplusEnabled: false,
		NewOrderEventID: 1, RenewOrderEventID: 0, ChangeOrderEventID: 1,
		DefaultRemindExpire: false, DefaultRemindTraffic: true,
	}
}

func validLegacySubscriptionPolicySettingsImport(settings LegacySubscriptionPolicySettings) LegacySubscriptionPolicySettingsImport {
	return LegacySubscriptionPolicySettingsImport{
		Slice: LegacySubscriptionPolicySettingsSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 1024,
		Settings: settings, Checksum: LegacySubscriptionPolicySettingsChecksum(settings),
		RollbackBackupPath: "backup.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
	}
}
