package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestImportLegacySafeAccessSettingsIsComposableAndIdempotent(t *testing.T) {
	database := newTestStore(t)
	if _, err := database.db.Exec(`UPDATE app_settings SET app_url='https://panel.example.test' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	settings := LegacySafeAccessSettings{SafeModeEnabled: true, SecurePath: "secure-admin_01"}
	input := validLegacySafeAccessSettingsImport(settings)
	now := time.Date(2026, 8, 30, 13, 0, 0, 0, time.UTC)
	report, err := database.ImportLegacySafeAccessSettings(t.Context(), input, now)
	if err != nil || report.Settings.SourceRows != 1 || report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		t.Fatalf("ImportLegacySafeAccessSettings()=(%#v,%v)", report, err)
	}
	updated, err := database.GetSiteSettings(t.Context())
	if err != nil || !updated.SafeModeEnabled || updated.SecurePath != settings.SecurePath {
		t.Fatalf("safe access settings=%#v err=%v", updated, err)
	}
	repeated, err := database.ImportLegacySafeAccessSettings(t.Context(), input, now.Add(time.Hour))
	if err != nil || !repeated.AlreadyApplied || !repeated.AppliedAt.Equal(now) {
		t.Fatalf("idempotent import=(%#v,%v)", repeated, err)
	}
	changedOptions := input
	changedOptions.Settings.SecurePath = "different-admin"
	changedOptions.Checksum = LegacySafeAccessSettingsChecksum(changedOptions.Settings)
	if _, err := database.ImportLegacySafeAccessSettings(t.Context(), changedOptions, now.Add(2*time.Hour)); !errors.Is(err, ErrConflict) {
		t.Fatalf("same source with different effective path error=%v, want ErrConflict", err)
	}
	input.SourceSHA256 = strings.Repeat("f", 64)
	if _, err := database.ImportLegacySafeAccessSettings(t.Context(), input, now.Add(2*time.Hour)); !errors.Is(err, ErrConflict) {
		t.Fatalf("different source import error=%v, want ErrConflict", err)
	}
}

func TestImportLegacySafeAccessSettingsAcceptsOnlyPristineFallbackAndDetectsDrift(t *testing.T) {
	settings := LegacySafeAccessSettings{SecurePath: "secure-admin_01"}
	input := validLegacySafeAccessSettingsImport(settings)

	fromFallback := newTestStore(t)
	if err := fromFallback.EnsureSiteAccessSettings(t.Context(), "admin", time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := fromFallback.ImportLegacySafeAccessSettings(t.Context(), input, time.Unix(2, 0)); err != nil {
		t.Fatalf("fallback target import error=%v", err)
	}

	nonPristine := newTestStore(t)
	if _, err := nonPristine.db.Exec(`UPDATE app_settings SET secure_path='operator-path' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := nonPristine.ImportLegacySafeAccessSettings(t.Context(), input, time.Unix(1, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("non-pristine import error=%v, want ErrConflict", err)
	}

	missingURL := newTestStore(t)
	safeInput := validLegacySafeAccessSettingsImport(LegacySafeAccessSettings{SafeModeEnabled: true, SecurePath: "secure-admin_01"})
	if _, err := missingURL.ImportLegacySafeAccessSettings(t.Context(), safeInput, time.Unix(1, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("safe mode without site URL error=%v, want ErrConflict", err)
	}

	clean := newTestStore(t)
	if _, err := clean.ImportLegacySafeAccessSettings(t.Context(), input, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := clean.db.Exec(`UPDATE app_settings SET secure_path='drifted-path' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := clean.ImportLegacySafeAccessSettings(t.Context(), input, time.Unix(3, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("drifted idempotent import error=%v, want ErrConflict", err)
	}
}

func validLegacySafeAccessSettingsImport(settings LegacySafeAccessSettings) LegacySafeAccessSettingsImport {
	return LegacySafeAccessSettingsImport{
		Slice: LegacySafeAccessSettingsSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 1024,
		Settings: settings, Checksum: LegacySafeAccessSettingsChecksum(settings), FallbackSecurePath: "admin",
		RollbackBackupPath: "backup.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
	}
}
