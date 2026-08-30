package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestImportLegacyPublicOriginSettingsIsComposableWithFrozenContentSliceAndIdempotent(t *testing.T) {
	database := newTestStore(t)
	contentNow := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	if _, err := database.ImportLegacyContent(t.Context(), validLegacyContentImport(t), contentNow); err != nil {
		t.Fatal(err)
	}
	settings := LegacyPublicOriginSettings{
		ForceHTTPS: true, SubscribeURL: "https://one.example.test,https://two.example.test/root",
	}
	input := LegacyPublicOriginSettingsImport{
		Slice: LegacyPublicOriginSettingsSlice, SourceSHA256: strings.Repeat("d", 64), SourceSize: 4096,
		Settings: settings, Checksum: LegacyPublicOriginSettingsChecksum(settings),
		RollbackBackupPath: "backup.xbbackup", RollbackBackupSHA256: strings.Repeat("e", 64),
	}
	now := contentNow.Add(time.Hour)
	report, err := database.ImportLegacyPublicOriginSettings(context.Background(), input, now)
	if err != nil || report.Settings.SourceRows != 1 || report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		t.Fatalf("ImportLegacyPublicOriginSettings()=(%#v,%v)", report, err)
	}
	updated, err := database.GetSiteSettings(context.Background())
	if err != nil || updated.AppName != "Legacy Board" || !updated.ForceHTTPS || updated.SubscribeURL != settings.SubscribeURL {
		t.Fatalf("public origin settings=%#v err=%v", updated, err)
	}
	repeated, err := database.ImportLegacyPublicOriginSettings(context.Background(), input, now.Add(time.Hour))
	if err != nil || !repeated.AlreadyApplied || !repeated.AppliedAt.Equal(now) {
		t.Fatalf("idempotent import=(%#v,%v)", repeated, err)
	}
	input.SourceSHA256 = strings.Repeat("f", 64)
	if _, err := database.ImportLegacyPublicOriginSettings(context.Background(), input, now.Add(2*time.Hour)); !errors.Is(err, ErrConflict) {
		t.Fatalf("different source import error=%v, want ErrConflict", err)
	}
}

func TestImportLegacyPublicOriginSettingsRequiresPristineSafeFieldsAndDetectsDrift(t *testing.T) {
	settings := LegacyPublicOriginSettings{ForceHTTPS: true, SubscribeURL: "https://subscriptions.example.test"}
	input := LegacyPublicOriginSettingsImport{
		Slice: LegacyPublicOriginSettingsSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 1024,
		Settings: settings, Checksum: LegacyPublicOriginSettingsChecksum(settings),
		RollbackBackupPath: "backup.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
	}
	nonPristine := newTestStore(t)
	if _, err := nonPristine.db.Exec(`UPDATE app_settings SET force_https=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := nonPristine.ImportLegacyPublicOriginSettings(t.Context(), input, time.Unix(1, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("non-pristine import error=%v, want ErrConflict", err)
	}

	clean := newTestStore(t)
	unsafe := input
	unsafe.SourceSHA256 = strings.Repeat("c", 64)
	unsafe.Settings.SubscribeURL = "http://external.example.test"
	unsafe.Checksum = LegacyPublicOriginSettingsChecksum(unsafe.Settings)
	if _, err := clean.ImportLegacyPublicOriginSettings(t.Context(), unsafe, time.Unix(1, 0)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsafe import error=%v, want ErrInvalidInput", err)
	}
	if _, err := clean.ImportLegacyPublicOriginSettings(t.Context(), input, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := clean.db.Exec(`UPDATE app_settings SET subscribe_url='https://drift.example.test' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := clean.ImportLegacyPublicOriginSettings(t.Context(), input, time.Unix(3, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("drifted idempotent import error=%v, want ErrConflict", err)
	}
}
