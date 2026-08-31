package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSchemaV54AddsConfigurationCompatibilityDefaultsAndConstraints(t *testing.T) {
	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "configuration-compatibility.db"))
	database, err := OpenSQLite(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	var version, withdrawLimit int
	var withdrawMethods, sidebarStyle, headerStyle string
	if err := database.db.QueryRowContext(ctx, `
		SELECT (SELECT user_version FROM pragma_user_version),
		       app.commission_withdraw_limit, app.commission_withdraw_method,
		       theme.sidebar_style, theme.header_style
		FROM app_settings app CROSS JOIN theme_settings theme
		WHERE app.id = 1 AND theme.id = 1
	`).Scan(&version, &withdrawLimit, &withdrawMethods, &sidebarStyle, &headerStyle); err != nil {
		t.Fatal(err)
	}
	if version != 56 || withdrawLimit != 10_000 || withdrawMethods != `["支付宝","USDT","Paypal"]` || sidebarStyle != "light" || headerStyle != "dark" {
		t.Fatalf("schema v54 defaults = version=%d limit=%d methods=%q sidebar=%q header=%q", version, withdrawLimit, withdrawMethods, sidebarStyle, headerStyle)
	}

	for name, statement := range map[string]string{
		"withdraw limit lower bound": `UPDATE app_settings SET commission_withdraw_limit = -1 WHERE id = 1`,
		"withdraw method json":       `UPDATE app_settings SET commission_withdraw_method = 'not-json' WHERE id = 1`,
		"withdraw method shape":      `UPDATE app_settings SET commission_withdraw_method = '{"name":"USDT"}' WHERE id = 1`,
		"withdraw method item type":  `UPDATE app_settings SET commission_withdraw_method = '[1]' WHERE id = 1`,
		"withdraw method whitespace": `UPDATE app_settings SET commission_withdraw_method = '[" USDT"]' WHERE id = 1`,
		"sidebar style":              `UPDATE theme_settings SET sidebar_style = 'system' WHERE id = 1`,
		"header style":               `UPDATE theme_settings SET header_style = 'system' WHERE id = 1`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := database.db.ExecContext(ctx, statement); err == nil {
				t.Fatal("invalid compatibility setting was accepted")
			}
		})
	}
}

func TestThemeLayoutSettingsAreRevisionSafeAndRefreshAppearance(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "theme-layout-admin@example.test", PasswordHash: "hash",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := database.ListThemes(ctx)
	if err != nil || initial.Revision != 1 || initial.SidebarStyle != "light" || initial.HeaderStyle != "dark" {
		t.Fatalf("initial theme layout = %#v err=%v", initial, err)
	}
	if _, err := database.GetActiveThemeAppearance(ctx); err != nil {
		t.Fatal(err)
	}
	updated, err := database.UpdateThemeLayoutSettings(ctx, administrator.ID, initial.Revision, "dark", "light", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.SidebarStyle != "dark" || updated.HeaderStyle != "light" {
		t.Fatalf("updated theme layout = %#v", updated)
	}
	appearance, err := database.GetActiveThemeAppearance(ctx)
	if err != nil || appearance.Revision != 2 || appearance.SidebarStyle != "dark" || appearance.HeaderStyle != "light" {
		t.Fatalf("updated theme appearance = %#v err=%v", appearance, err)
	}
	if _, err := database.UpdateThemeLayoutSettings(ctx, administrator.ID, initial.Revision, "light", "dark", now.Add(2*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale theme layout update error = %v, want ErrConflict", err)
	}
	if _, err := database.UpdateThemeLayoutSettings(ctx, administrator.ID, updated.Revision, "system", "dark", now.Add(2*time.Minute)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid theme layout update error = %v, want ErrInvalidInput", err)
	}
}

func TestLegacyConfigurationCompatibilityPartialWritesAreAtomic(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "legacy-config-admin@example.test", PasswordHash: "hash",
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	forceInvitations := true
	withdrawLimit := CurrencyAmount(25_050)
	withdrawMethods := []string{"USDT", "Paypal"}
	invite, err := database.UpdateLegacyInvitationSettings(ctx, administrator.ID, SaveLegacyInvitationSettingsInput{
		InvitationForce: &forceInvitations, WithdrawLimit: &withdrawLimit, WithdrawMethods: &withdrawMethods,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if invite.Revision != 2 || !invite.InvitationForce || invite.WithdrawLimit != 25_050 || strings.Join(invite.WithdrawMethods, ",") != "USDT,Paypal" || invite.InviteCommission != 10 {
		t.Fatalf("legacy invite partial update = %#v", invite)
	}
	invalidLimit := CurrencyAmount(-1)
	if _, err := database.UpdateLegacyInvitationSettings(ctx, administrator.ID, SaveLegacyInvitationSettingsInput{WithdrawLimit: &invalidLimit}, now.Add(2*time.Minute)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid legacy invite update error = %v, want ErrInvalidInput", err)
	}
	preservedInvite, err := database.GetLegacyInvitationSettings(ctx)
	if err != nil || preservedInvite.Revision != invite.Revision || preservedInvite.WithdrawLimit != invite.WithdrawLimit {
		t.Fatalf("invalid invite update changed state: %#v err=%v", preservedInvite, err)
	}

	name, description, currency := "Compatibility Board", "Preserved partial site configuration", "usd"
	waitReply := true
	site, err := database.UpdateLegacySiteConfig(ctx, administrator.ID, SaveLegacySiteConfigInput{
		AppName: &name, AppDescription: &description, Currency: &currency, TicketMustWaitReply: &waitReply,
	}, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if site.Revision != 3 || site.AppName != name || site.AppDescription != description || site.Currency != "USD" || !site.TicketMustWaitReply || site.CurrencySymbol != "¥" {
		t.Fatalf("legacy site partial update = %#v", site)
	}
	badURL := "file:///etc/passwd"
	if _, err := database.UpdateLegacySiteConfig(ctx, administrator.ID, SaveLegacySiteConfigInput{AppURL: &badURL}, now.Add(4*time.Minute)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid legacy site update error = %v, want ErrInvalidInput", err)
	}
	preservedSite, err := database.GetLegacySiteConfig(ctx)
	if err != nil || preservedSite.Revision != site.Revision || preservedSite.AppURL != site.AppURL {
		t.Fatalf("invalid site update changed state: %#v err=%v", preservedSite, err)
	}

	dark, light, blue := "dark", "light", "blue"
	frontend, err := database.UpdateLegacyFrontendSettings(ctx, administrator.ID, SaveLegacyFrontendSettingsInput{
		SidebarStyle: &dark, HeaderStyle: &light, ThemeColor: &blue,
	}, now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if frontend.Theme != "Xboard" || frontend.SidebarStyle != "dark" || frontend.HeaderStyle != "light" || frontend.ThemeColor != "blue" {
		t.Fatalf("legacy frontend partial update = %#v", frontend)
	}
	externalBackground := "https://untrusted.example.test/background.png"
	if _, err := database.UpdateLegacyFrontendSettings(ctx, administrator.ID, SaveLegacyFrontendSettingsInput{BackgroundURL: &externalBackground}, now.Add(6*time.Minute)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsafe legacy background update error = %v, want ErrInvalidInput", err)
	}
	preservedFrontend, err := database.GetLegacyFrontendSettings(ctx)
	if err != nil || preservedFrontend != frontend {
		t.Fatalf("invalid frontend update changed state: %#v err=%v", preservedFrontend, err)
	}
}

func TestSchemaV54UpgradePreservesV53Rows(t *testing.T) {
	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "configuration-compatibility-upgrade.db"))
	database, err := OpenSQLite(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE app_settings SET app_name = 'Preserved V53', revision = 27;
		UPDATE theme_settings SET active_theme = 'Xboard', revision = 9;
		DROP TRIGGER app_settings_validate_commission_withdraw_method_insert;
		DROP TRIGGER app_settings_validate_commission_withdraw_method_update;
		ALTER TABLE app_settings DROP COLUMN commission_withdraw_limit;
		ALTER TABLE app_settings DROP COLUMN commission_withdraw_method;
		ALTER TABLE theme_settings DROP COLUMN sidebar_style;
		ALTER TABLE theme_settings DROP COLUMN header_style;
		PRAGMA user_version = 53;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	var version, appRevision, themeRevision, withdrawLimit int
	var appName, withdrawMethods, sidebarStyle, headerStyle string
	if err := database.db.QueryRowContext(ctx, `
		SELECT (SELECT user_version FROM pragma_user_version), app.app_name, app.revision,
		       app.commission_withdraw_limit, app.commission_withdraw_method,
		       theme.revision, theme.sidebar_style, theme.header_style
		FROM app_settings app CROSS JOIN theme_settings theme
		WHERE app.id = 1 AND theme.id = 1
	`).Scan(&version, &appName, &appRevision, &withdrawLimit, &withdrawMethods, &themeRevision, &sidebarStyle, &headerStyle); err != nil {
		t.Fatal(err)
	}
	if version != 56 || appName != "Preserved V53" || appRevision != 27 || themeRevision != 9 ||
		withdrawLimit != 10_000 || withdrawMethods != `["支付宝","USDT","Paypal"]` || sidebarStyle != "light" || headerStyle != "dark" {
		t.Fatalf("v54 upgrade = version=%d app=%q/%d theme=%d limit=%d methods=%q sidebar=%q header=%q",
			version, appName, appRevision, themeRevision, withdrawLimit, withdrawMethods, sidebarStyle, headerStyle)
	}
}
