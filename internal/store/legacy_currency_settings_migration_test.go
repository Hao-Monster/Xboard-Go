package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestImportLegacyCurrencySettingsIsVerifiedComposableAndIdempotent(t *testing.T) {
	database := newTestStore(t)
	administrator, err := database.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "currency-admin@example.test", PasswordHash: "hash"}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	current, _ := database.GetSiteSettings(t.Context())
	name := "Existing Board"
	if _, err := database.UpdateSiteSettings(t.Context(), administrator.ID, current.Revision, SaveSiteSettingsInput{
		AppName: name, Currency: &current.Currency, CurrencySymbol: &current.CurrencySymbol,
		EmailWhitelistSuffixes: []string{"gmail.com"}, RegistrationIPLimitCount: 3, RegistrationIPLimitMinutes: 60,
		PasswordLimitEnabled: true, PasswordLimitCount: 5, PasswordLimitMinutes: 60,
	}, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	settings := LegacyCurrencySettings{Currency: "USD", CurrencySymbol: "$"}
	input := LegacyCurrencySettingsImport{
		Slice: LegacyCurrencySettingsSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 4096,
		Settings: settings, Checksum: LegacyCurrencySettingsChecksum(settings),
		RollbackBackupPath: "backup.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
	}
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	report, err := database.ImportLegacyCurrencySettings(context.Background(), input, now)
	if err != nil || report.Settings.SourceChecksum != report.Settings.TargetChecksum || report.Settings.SourceRows != 1 {
		t.Fatalf("ImportLegacyCurrencySettings()=(%#v,%v)", report, err)
	}
	updated, err := database.GetSiteSettings(context.Background())
	if err != nil || updated.AppName != name || updated.Currency != "USD" || updated.CurrencySymbol != "$" {
		t.Fatalf("currency settings=%#v err=%v", updated, err)
	}
	repeated, err := database.ImportLegacyCurrencySettings(context.Background(), input, now.Add(time.Hour))
	if err != nil || !repeated.AlreadyApplied || !repeated.AppliedAt.Equal(now) {
		t.Fatalf("idempotent import=(%#v,%v)", repeated, err)
	}
	input.SourceSHA256 = strings.Repeat("c", 64)
	if _, err := database.ImportLegacyCurrencySettings(context.Background(), input, now.Add(2*time.Hour)); !errors.Is(err, ErrConflict) {
		t.Fatalf("different source import error=%v, want ErrConflict", err)
	}
}

func TestImportLegacyCurrencySettingsRequiresPristineSafeFieldsAndDetectsDrift(t *testing.T) {
	database := newTestStore(t)
	input := LegacyCurrencySettingsImport{
		Slice: LegacyCurrencySettingsSlice, SourceSHA256: strings.Repeat("d", 64), SourceSize: 1024,
		Settings:           LegacyCurrencySettings{Currency: "EUR", CurrencySymbol: "€"},
		RollbackBackupPath: "backup.xbbackup", RollbackBackupSHA256: strings.Repeat("e", 64),
	}
	input.Checksum = LegacyCurrencySettingsChecksum(input.Settings)
	if _, err := database.db.Exec(`UPDATE app_settings SET currency='USD' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ImportLegacyCurrencySettings(t.Context(), input, time.Unix(1, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("non-pristine import error=%v, want ErrConflict", err)
	}

	clean := newTestStore(t)
	unsafe := input
	unsafe.SourceSHA256 = strings.Repeat("f", 64)
	unsafe.Settings.Currency = " us "
	unsafe.Checksum = LegacyCurrencySettingsChecksum(unsafe.Settings)
	if _, err := clean.ImportLegacyCurrencySettings(t.Context(), unsafe, time.Unix(1, 0)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsafe import error=%v, want ErrInvalidInput", err)
	}
	if _, err := clean.ImportLegacyCurrencySettings(t.Context(), input, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := clean.db.Exec(`UPDATE app_settings SET currency='GBP' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := clean.ImportLegacyCurrencySettings(t.Context(), input, time.Unix(3, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("drifted idempotent import error=%v, want ErrConflict", err)
	}
}
