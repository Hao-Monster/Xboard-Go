package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClientAppSettingsNormalizeAndUseIndependentCAS(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "client-app-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := database.GetClientAppSettings(ctx)
	if err != nil || initial.Revision != 1 || initial.WindowsVersion != "" || !initial.UpdatedAt.Equal(time.Unix(0, 0).UTC()) {
		t.Fatalf("initial settings=%#v err=%v", initial, err)
	}
	updated, err := database.UpdateClientAppSettings(ctx, administrator.ID, initial.Revision, SaveClientAppSettingsInput{
		WindowsVersion: " 4.8.1 ", WindowsDownloadURL: " https://download.example.test/windows.exe ",
		MacOSVersion: " 4.8.2 ", MacOSDownloadURL: " https://download.example.test/macos.dmg ",
		AndroidVersion: " 4.8.3 ", AndroidDownloadURL: " https://download.example.test/android.apk ",
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.WindowsVersion != "4.8.1" ||
		updated.WindowsDownloadURL != "https://download.example.test/windows.exe" ||
		updated.MacOSVersion != "4.8.2" || updated.AndroidVersion != "4.8.3" ||
		!updated.UpdatedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("updated settings=%#v", updated)
	}
	if _, err := database.UpdateClientAppSettings(ctx, administrator.ID, initial.Revision, SaveClientAppSettingsInput{}, now.Add(2*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale UpdateClientAppSettings() error=%v, want ErrConflict", err)
	}

	windowsVersion := "5.0.0"
	legacy, err := database.UpdateLegacyClientAppSettings(ctx, administrator.ID, SaveLegacyClientAppSettingsInput{
		WindowsVersion: &windowsVersion,
	}, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Revision != 3 || legacy.WindowsVersion != windowsVersion || legacy.MacOSVersion != "4.8.2" ||
		legacy.AndroidDownloadURL != "https://download.example.test/android.apk" {
		t.Fatalf("legacy partial update=%#v", legacy)
	}
}

func TestClientAppSettingsRejectUnsafeAndOversizedValues(t *testing.T) {
	database := newTestStore(t)
	now := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	administrator, _ := database.CreateAdminUser(t.Context(), CreateAdminUserInput{
		Email: "client-app-validation@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	initial, _ := database.GetClientAppSettings(t.Context())
	for name, input := range map[string]SaveClientAppSettingsInput{
		"insecure URL":      {WindowsDownloadURL: "http://download.example.test/app"},
		"URL credentials":   {WindowsDownloadURL: "https://user:secret@download.example.test/app"},
		"URL fragment":      {WindowsDownloadURL: "https://download.example.test/app#secret"},
		"control version":   {WindowsVersion: "4.0\nmalformed"},
		"oversized version": {WindowsVersion: strings.Repeat("a", maxClientAppVersionBytes+1)},
		"oversized URL":     {WindowsDownloadURL: "https://download.example.test/" + strings.Repeat("a", maxClientAppURLBytes)},
		"invalid UTF-8":     {WindowsVersion: string([]byte{0xff})},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := database.UpdateClientAppSettings(t.Context(), administrator.ID, initial.Revision, input, now); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("UpdateClientAppSettings() error=%v, want ErrInvalidInput", err)
			}
		})
	}
	if _, err := database.UpdateLegacyClientAppSettings(t.Context(), administrator.ID, SaveLegacyClientAppSettingsInput{}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty UpdateLegacyClientAppSettings() error=%v, want ErrInvalidInput", err)
	}
}

func TestSchemaV49CreatesAndValidatesClientAppSettingsSingleton(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	if _, err := database.db.ExecContext(ctx, `DROP TABLE client_app_settings; PRAGMA user_version = 48`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v48 to v49) error=%v", err)
	}
	var version, rows, mailTemplates int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM client_app_settings`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mail_templates`).Scan(&mailTemplates); err != nil {
		t.Fatal(err)
	}
	if version != 49 || rows != 1 || mailTemplates != 5 {
		t.Fatalf("schema version=%d client app rows=%d mail templates=%d", version, rows, mailTemplates)
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		t.Fatalf("ValidateCurrentSchema() error=%v", err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM client_app_settings WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := database.ValidateCurrentSchema(ctx); err == nil || !strings.Contains(err.Error(), "exactly one row") {
		t.Fatalf("ValidateCurrentSchema() missing singleton error=%v", err)
	}
}

func TestClientAppSettingsLookupUsesSingletonPrimaryKey(t *testing.T) {
	database := newTestStore(t)
	assertQueryPlanContains(t, database, `EXPLAIN QUERY PLAN SELECT revision, windows_version FROM client_app_settings WHERE id = 1`, "USING INTEGER PRIMARY KEY")
	assertQueryPlanContains(t, database, `EXPLAIN QUERY PLAN SELECT 1 FROM users WHERE subscription_token = ? AND account_kind IN ('human', 'internal_subscription') LIMIT 1`, "idx_users_subscription_token", "00000000000000000000000000000001")
	if exists, err := database.ClientAppVersionTokenExists(t.Context(), "not-a-token"); err != nil || exists {
		t.Fatalf("malformed client app version token exists=%t err=%v", exists, err)
	}
}

func assertQueryPlanContains(t *testing.T, database *Store, query, expected string, arguments ...any) {
	t.Helper()
	rows, err := database.db.QueryContext(t.Context(), query, arguments...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexed := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		indexed = indexed || strings.Contains(detail, expected)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Fatalf("query plan for %q did not contain %q", query, expected)
	}
}
