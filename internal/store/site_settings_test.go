package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSiteSettingsShareOptimisticRevisionWithoutLosingFields(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	administrator := createTicketTestUser(t, database, "site-settings-admin@example.test", now)

	initial, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Revision != 1 || initial.AppName != "Xboard-Go" || initial.AppDescription != "" || initial.AppURL != "" || initial.TOSURL != "" || initial.Logo != "" {
		t.Fatalf("initial site settings = %#v", initial)
	}

	updated, err := database.UpdateSiteSettings(ctx, administrator.ID, initial.Revision, SaveSiteSettingsInput{
		AppName: "  Example Board  ", AppDescription: "  First line\nSecond line  ",
		AppURL: "https://panel.example.test/", TOSURL: "https://panel.example.test/terms/",
		Logo: " https://images.example.test/brand.svg?version=1#logo ", StopRegister: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.AppName != "Example Board" || updated.AppDescription != "First line\nSecond line" ||
		updated.AppURL != "https://panel.example.test/" || updated.TOSURL != "https://panel.example.test/terms/" ||
		updated.Logo != "https://images.example.test/brand.svg?version=1#logo" || !updated.StopRegister {
		t.Fatalf("normalized site settings = %#v", updated)
	}

	ticketSettings, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ticketSettings.Revision != updated.Revision || ticketSettings.AppName != updated.AppName || ticketSettings.AppURL != updated.AppURL {
		t.Fatalf("ticket settings did not observe shared identity: %#v", ticketSettings)
	}
	if _, err := database.UpdateTicketSettings(ctx, administrator.ID, initial.Revision, SaveTicketSettingsInput{AppName: "stale"}, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale cross-surface UpdateTicketSettings() error = %v, want ErrConflict", err)
	}

	ticketSettings, err = database.UpdateTicketSettings(ctx, administrator.ID, ticketSettings.Revision, SaveTicketSettingsInput{
		AppName: ticketSettings.AppName, AppURL: ticketSettings.AppURL,
		TicketMustWaitReply: ticketSettings.TicketMustWaitReply, SMTPEnabled: ticketSettings.SMTPEnabled,
		SMTPHost: ticketSettings.SMTPHost, SMTPPort: ticketSettings.SMTPPort, SMTPUsername: ticketSettings.SMTPUsername,
		SMTPEncryption: ticketSettings.SMTPEncryption, SMTPFromAddress: ticketSettings.SMTPFromAddress,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	afterTicketUpdate, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if afterTicketUpdate.Revision != ticketSettings.Revision || afterTicketUpdate.AppDescription != updated.AppDescription ||
		afterTicketUpdate.TOSURL != updated.TOSURL || afterTicketUpdate.Logo != updated.Logo || !afterTicketUpdate.StopRegister {
		t.Fatalf("ticket settings update lost site-only fields: %#v", afterTicketUpdate)
	}
}

func TestSiteSettingsRejectInvalidInputsAndResolveConcurrentRevision(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	administrator := createTicketTestUser(t, database, "site-settings-validation@example.test", now)

	valid := SaveSiteSettingsInput{
		AppName: "Example", AppDescription: "Description", AppURL: "https://panel.example.test",
		TOSURL: "https://panel.example.test/terms", Logo: "https://images.example.test/logo.png",
	}
	for name, mutate := range map[string]func(*SaveSiteSettingsInput){
		"empty name":          func(input *SaveSiteSettingsInput) { input.AppName = " " },
		"long name":           func(input *SaveSiteSettingsInput) { input.AppName = strings.Repeat("站", 101) },
		"name control":        func(input *SaveSiteSettingsInput) { input.AppName = "bad\nname" },
		"long description":    func(input *SaveSiteSettingsInput) { input.AppDescription = strings.Repeat("述", 501) },
		"description control": func(input *SaveSiteSettingsInput) { input.AppDescription = "bad\x00description" },
		"app URL scheme":      func(input *SaveSiteSettingsInput) { input.AppURL = "javascript:alert(1)" },
		"app URL userinfo":    func(input *SaveSiteSettingsInput) { input.AppURL = "https://user@example.test" },
		"long app URL": func(input *SaveSiteSettingsInput) {
			input.AppURL = "https://example.test/" + strings.Repeat("a", 2_048)
		},
		"TOS URL scheme": func(input *SaveSiteSettingsInput) { input.TOSURL = "ftp://example.test/terms" },
		"logo scheme":    func(input *SaveSiteSettingsInput) { input.Logo = "data:image/svg+xml,unsafe" },
		"logo userinfo":  func(input *SaveSiteSettingsInput) { input.Logo = "https://user@example.test/logo.png" },
		"long logo URL": func(input *SaveSiteSettingsInput) {
			input.Logo = "https://images.example.test/" + strings.Repeat("a", 2_048)
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := valid
			mutate(&input)
			if _, err := database.UpdateSiteSettings(ctx, administrator.ID, 1, input, now); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("UpdateSiteSettings() error = %v, want ErrInvalidInput", err)
			}
		})
	}
	if current, err := database.GetSiteSettings(ctx); err != nil || current.Revision != 1 {
		t.Fatalf("invalid updates changed state: settings=%#v err=%v", current, err)
	}

	var successes, conflicts int
	var mutex sync.Mutex
	var group sync.WaitGroup
	for index := 0; index < 2; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			input := valid
			input.AppName = fmt.Sprintf("Concurrent %d", index)
			_, err := database.UpdateSiteSettings(ctx, administrator.ID, 1, input, now)
			mutex.Lock()
			defer mutex.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrConflict):
				conflicts++
			default:
				t.Errorf("concurrent UpdateSiteSettings() error = %v", err)
			}
		}(index)
	}
	group.Wait()
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent outcomes: successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestSiteURLNormalizationPreservesLegacyURLSemantics(t *testing.T) {
	normalized, err := normalizeSiteSettings(SaveSiteSettingsInput{
		AppName: "Query Board",
		AppURL:  " https://panel.example.test/root/?next=/ ",
		TOSURL:  "https://panel.example.test/terms/#section/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.AppURL != "https://panel.example.test/root/?next=/" || normalized.TOSURL != "https://panel.example.test/terms/#section/" {
		t.Fatalf("normalized URLs = (%q, %q)", normalized.AppURL, normalized.TOSURL)
	}
}

func TestSchemaV14MigrationPreservesV13SiteSettings(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "schema-v13.db")
	database, err := OpenSQLite("file:" + filepath.ToSlash(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	for version, schema := range []string{
		schemaV1, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6, schemaV7, schemaV7Constraints,
		schemaV8, schemaV9, schemaV10, schemaV11, schemaV12, schemaV13,
	} {
		if _, err := database.db.ExecContext(ctx, schema); err != nil {
			t.Fatalf("apply pre-v14 schema step %d: %v", version+1, err)
		}
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE app_settings SET app_name = 'V13 Board', app_description = 'Preserved',
			app_url = 'https://v13.example.test/', tos_url = 'https://v13.example.test/terms/', revision = 12;
		PRAGMA user_version = 13;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v13 to v14) error = %v", err)
	}
	settings, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 15 || settings.Revision != 12 || settings.AppName != "V13 Board" || settings.AppDescription != "Preserved" ||
		settings.AppURL != "https://v13.example.test/" || settings.TOSURL != "https://v13.example.test/terms/" || settings.Logo != "" {
		t.Fatalf("v13 to v14 migration result: version=%d settings=%#v", version, settings)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE app_settings SET logo = ? WHERE id = 1`, strings.Repeat("a", 2_049)); err == nil {
		t.Fatal("database accepted an oversized logo")
	}
}

func TestSchemaV15MigrationPreservesV14SettingsAndDefaultsRegistrationOpen(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "schema-v14.db")
	database, err := OpenSQLite("file:" + filepath.ToSlash(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	for step, schema := range []string{
		schemaV1, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6, schemaV7, schemaV7Constraints,
		schemaV8, schemaV9, schemaV10, schemaV11, schemaV12, schemaV13, schemaV14,
	} {
		if _, err := database.db.ExecContext(ctx, schema); err != nil {
			t.Fatalf("apply pre-v15 schema step %d: %v", step+1, err)
		}
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE app_settings SET app_name = 'V14 Board', app_description = 'Preserved',
			app_url = 'https://v14.example.test/', tos_url = 'https://v14.example.test/terms/',
			logo = 'https://v14.example.test/logo.svg', revision = 17;
		PRAGMA user_version = 14;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v14 to v15) error = %v", err)
	}
	settings, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 15 || settings.Revision != 17 || settings.AppName != "V14 Board" || settings.AppDescription != "Preserved" ||
		settings.AppURL != "https://v14.example.test/" || settings.TOSURL != "https://v14.example.test/terms/" ||
		settings.Logo != "https://v14.example.test/logo.svg" || settings.StopRegister {
		t.Fatalf("v14 to v15 migration result: version=%d settings=%#v", version, settings)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE app_settings SET stop_register = 2 WHERE id = 1`); err == nil {
		t.Fatal("database accepted an invalid stop_register value")
	}
}

func TestSchemaV13MigrationPreservesV12SettingsAndOperationsData(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "schema-v12.db")
	database, err := OpenSQLite("file:" + filepath.ToSlash(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	for version, schema := range []string{
		schemaV1, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6, schemaV7, schemaV7Constraints,
		schemaV8, schemaV9, schemaV10, schemaV11, schemaV12,
	} {
		if _, err := database.db.ExecContext(ctx, schema); err != nil {
			t.Fatalf("apply pre-v13 schema step %d: %v", version+1, err)
		}
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE app_settings SET app_name = 'Preserved Board', app_url = 'https://preserved.example.test', revision = 9;
		INSERT INTO admin_audit_logs (administrator_email, method, route, status_code, created_at)
		VALUES ('removed-admin@example.test', 'PUT', '/api/v1/admin/ticket-settings', 200, 1);
		PRAGMA user_version = 12;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v12 to v13) error = %v", err)
	}

	settings, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var version, auditCount int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_audit_logs`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if version != 15 || settings.Revision != 9 || settings.AppName != "Preserved Board" ||
		settings.AppURL != "https://preserved.example.test" || settings.AppDescription != "" || settings.TOSURL != "" || settings.Logo != "" || auditCount != 1 {
		t.Fatalf("migration result: version=%d settings=%#v audits=%d", version, settings, auditCount)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE app_settings SET app_description = ? WHERE id = 1`, strings.Repeat("a", 501)); err == nil {
		t.Fatal("database accepted an oversized app_description")
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE app_settings SET tos_url = ? WHERE id = 1`, strings.Repeat("a", 2_049)); err == nil {
		t.Fatal("database accepted an oversized tos_url")
	}
}

func BenchmarkSiteSettingsRead(b *testing.B) {
	database, administratorID := newSiteSettingsBenchmarkStore(b)
	_ = administratorID
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := database.GetSiteSettings(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSiteSettingsUpdate(b *testing.B) {
	database, administratorID := newSiteSettingsBenchmarkStore(b)
	ctx := context.Background()
	revision := int64(1)
	input := SaveSiteSettingsInput{
		AppName: "Benchmark Board", AppDescription: "Benchmark description",
		AppURL: "https://benchmark.example.test", TOSURL: "https://benchmark.example.test/terms",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		settings, err := database.UpdateSiteSettings(ctx, administratorID, revision, input, time.Unix(int64(index+1), 0))
		if err != nil {
			b.Fatal(err)
		}
		revision = settings.Revision
	}
}

func newSiteSettingsBenchmarkStore(b *testing.B) (*Store, int64) {
	b.Helper()
	database, err := OpenSQLite(fmt.Sprintf("file:%s?mode=memory&cache=shared", b.Name()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		b.Fatal(err)
	}
	if _, err := database.BootstrapAdmin(ctx, "benchmark-admin@example.test", "benchmark-password-hash", time.Unix(1, 0)); err != nil {
		b.Fatal(err)
	}
	administrator, err := database.FindUserByEmail(ctx, "benchmark-admin@example.test")
	if err != nil {
		b.Fatal(err)
	}
	return database, administrator.ID
}
