package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSiteAccessSettingsValidateSwitchAndStayAtomic(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	if err := database.EnsureSiteAccessSettings(ctx, "admin", time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	administrator := createTicketTestUser(t, database, "site-access-admin@example.test", time.Unix(1, 0))
	current, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.SecurePath != "admin" || current.SafeModeEnabled || current.Revision != 1 {
		t.Fatalf("ensured site access settings = %#v", current)
	}

	input := siteSettingsSaveInput(current)
	input.AppURL = "https://Panel.Example.Test:8443/root"
	input.SafeModeEnabled = boolPointer(true)
	input.SecurePath = stringCopyPointer("secure-admin_01")
	updated, err := database.UpdateSiteSettings(ctx, administrator.ID, current.Revision, input, time.Unix(2, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !updated.SafeModeEnabled || updated.SecurePath != "secure-admin_01" {
		t.Fatalf("updated site access settings = %#v", updated)
	}

	for name, path := range map[string]string{
		"short":    "short",
		"reserved": "passport",
		"slash":    "secure/path",
		"unicode":  "安全后台路径",
		"too long": strings.Repeat("a", 65),
	} {
		t.Run(name, func(t *testing.T) {
			invalid := siteSettingsSaveInput(updated)
			invalid.SafeModeEnabled = boolPointer(true)
			invalid.SecurePath = stringCopyPointer(path)
			if _, err := database.UpdateSiteSettings(ctx, administrator.ID, updated.Revision, invalid, time.Unix(3, 0)); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("UpdateSiteSettings(%q) error = %v, want ErrInvalidInput", path, err)
			}
		})
	}
	missingURL := siteSettingsSaveInput(updated)
	missingURL.AppURL = ""
	missingURL.SafeModeEnabled = boolPointer(true)
	missingURL.SecurePath = stringCopyPointer(updated.SecurePath)
	if _, err := database.UpdateSiteSettings(ctx, administrator.ID, updated.Revision, missingURL, time.Unix(3, 0)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("safe mode without app URL error = %v, want ErrInvalidInput", err)
	}
	preserved, err := database.GetSiteSettings(ctx)
	if err != nil || preserved.Revision != updated.Revision || preserved.SecurePath != updated.SecurePath || !preserved.SafeModeEnabled {
		t.Fatalf("invalid site access writes changed settings: %#v err=%v", preserved, err)
	}
}

func TestSchemaV53AndCrossStoreSiteAccessProjection(t *testing.T) {
	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "site-access.db"))
	writer, err := OpenSQLite(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx := context.Background()
	if err := writer.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.db.ExecContext(ctx, `
		UPDATE app_settings SET app_name='Preserved V52', revision=41;
		ALTER TABLE app_settings DROP COLUMN safe_mode_enable;
		ALTER TABLE app_settings DROP COLUMN secure_path;
		PRAGMA user_version=52;
	`); err != nil {
		t.Fatal(err)
	}
	if err := writer.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	settings, err := writer.GetSiteSettings(ctx)
	if err != nil || settings.AppName != "Preserved V52" || settings.Revision != 41 || settings.SafeModeEnabled || settings.SecurePath != "" {
		t.Fatalf("v53 migration settings = %#v err=%v", settings, err)
	}
	if err := writer.EnsureSiteAccessSettings(ctx, "legacy-admin", time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenSQLite(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	access, err := reader.GetSiteAccessSettings(ctx)
	if err != nil || access.SecurePath != "legacy-admin" || access.SafeModeEnabled || access.AppURL != "" {
		t.Fatalf("cross-store access projection = %#v err=%v", access, err)
	}
	settings, _ = writer.GetSiteSettings(ctx)
	if settings.Revision != 41 {
		t.Fatalf("compatibility default changed administrator revision: %#v", settings)
	}
}

func BenchmarkSiteAccessSettingsRead(b *testing.B) {
	database, _ := newSiteSettingsBenchmarkStore(b)
	if err := database.EnsureSiteAccessSettings(context.Background(), "benchmark-admin", time.Unix(1, 0)); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := database.GetSiteAccessSettings(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}
