package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPublicOriginSettingsNormalizeValidateAndStayAtomic(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	administrator := createTicketTestUser(t, database, "public-origin-admin@example.test", time.Unix(1, 0))
	initial, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initial.ForceHTTPS || initial.SubscribeURL != "" {
		t.Fatalf("initial public origin settings = %#v", initial)
	}
	if cleared, err := NormalizeSubscribeURL("  \r\n "); err != nil || cleared != "" {
		t.Fatalf("NormalizeSubscribeURL(whitespace) = (%q, %v)", cleared, err)
	}
	input := siteSettingsSaveInput(initial)
	input.ForceHTTPS = boolPointer(true)
	input.SubscribeURL = stringCopyPointer(" https://one.example.test/root/ ,\nhttps://two.example.test, https://one.example.test/root ")
	updated, err := database.UpdateSiteSettings(ctx, administrator.ID, initial.Revision, input, time.Unix(2, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !updated.ForceHTTPS || updated.SubscribeURL != "https://one.example.test/root,https://two.example.test" {
		t.Fatalf("normalized public origin settings = %#v", updated)
	}

	for name, value := range map[string]string{
		"external HTTP": "http://unsafe.example.test",
		"userinfo":      "https://user@example.test",
		"query":         "https://example.test?next=bad",
		"fragment":      "https://example.test/#bad",
		"invalid port":  "https://example.test:99999",
		"relative":      "/subscription",
		"unsupported":   "ftp://example.test",
		"too many":      publicOriginList(33),
	} {
		t.Run(name, func(t *testing.T) {
			invalid := siteSettingsSaveInput(updated)
			invalid.SubscribeURL = stringCopyPointer(value)
			if _, err := database.UpdateSiteSettings(ctx, administrator.ID, updated.Revision, invalid, time.Unix(3, 0)); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("UpdateSiteSettings() error = %v, want ErrInvalidInput", err)
			}
		})
	}
	preserved, err := database.GetSiteSettings(ctx)
	if err != nil || preserved.Revision != updated.Revision || preserved.SubscribeURL != updated.SubscribeURL {
		t.Fatalf("invalid writes changed settings: %#v err=%v", preserved, err)
	}
}

func TestSchemaV52MigrationAddsSafePublicOriginDefaults(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	if _, err := database.db.ExecContext(ctx, `
		UPDATE app_settings SET app_name = 'Preserved V51', revision = 41;
		ALTER TABLE app_settings DROP COLUMN force_https;
		ALTER TABLE app_settings DROP COLUMN subscribe_url;
		PRAGMA user_version = 51;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if CurrentSchemaVersion() != 57 || settings.AppName != "Preserved V51" || settings.Revision != 41 || settings.ForceHTTPS || settings.SubscribeURL != "" {
		t.Fatalf("v52 migration result = %#v version=%d", settings, CurrentSchemaVersion())
	}
}

func TestPublicOriginSettingsAreImmediatelyVisibleAcrossStoreInstances(t *testing.T) {
	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "public-origins.db"))
	writer, err := OpenSQLite(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	reader, err := OpenSQLite(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	ctx := context.Background()
	if err := writer.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.BootstrapAdmin(ctx, "cross-store-origin@example.test", "opaque", time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	administrator, _ := writer.FindUserByEmail(ctx, "cross-store-origin@example.test")
	current, _ := writer.GetSiteSettings(ctx)
	input := siteSettingsSaveInput(current)
	input.SubscribeURL = stringCopyPointer("https://subscriptions.example.test")
	if _, err := writer.UpdateSiteSettings(ctx, administrator.ID, current.Revision, input, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	config, err := reader.GetSubscriptionRenderConfig(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if config.SubscribeURL != "https://subscriptions.example.test" {
		t.Fatalf("cross-store render config = %#v", config)
	}
}

func publicOriginList(count int) string {
	values := make([]string, count)
	for index := range values {
		values[index] = fmt.Sprintf("https://s%d.example.test", index)
	}
	return strings.Join(values, ",")
}

func boolPointer(value bool) *bool { return &value }
