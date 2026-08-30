package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestClientAppRuntimeSettingsProjectsOnlyPublicContractFields(t *testing.T) {
	database := newTestStore(t)
	if _, err := database.db.ExecContext(t.Context(), `
		UPDATE app_settings SET
			app_name='Runtime Board',app_description='Runtime description',app_url='https://runtime.example.test',
			logo='https://runtime.example.test/logo.png',tos_url='https://runtime.example.test/terms',
			currency='USD',currency_symbol='$',telegram_bot_enable=1,ticket_must_wait_reply=1,
			email_verify=1,invite_force=1,email_whitelist_enable=0,email_whitelist_suffix='example.test',
			captcha_enable=1,captcha_type='turnstile',recaptcha_site_key='public-recaptcha',
			recaptcha_secret_cipher=zeroblob(33),recaptcha_v3_site_key='public-v3',recaptcha_v3_score_threshold=0.7,
			turnstile_site_key='public-turnstile',turnstile_secret_cipher=zeroblob(33)
		WHERE id=1
	`); err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetClientAppRuntimeSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if settings.AppName != "Runtime Board" || settings.Currency != "USD" || settings.CurrencySymbol != "$" ||
		!settings.TelegramBotEnabled || !settings.TicketMustWaitReply || !settings.EmailVerificationEnabled ||
		!settings.InvitationForceEnabled || !settings.EmailWhitelistSuffixPresent || !settings.CaptchaEnabled ||
		settings.CaptchaType != "turnstile" || settings.RecaptchaSiteKey != "public-recaptcha" ||
		settings.RecaptchaV3SiteKey != "public-v3" || settings.TurnstileSiteKey != "public-turnstile" {
		t.Fatalf("runtime settings=%#v", settings)
	}
	assertQueryPlanContains(t, database, `EXPLAIN QUERY PLAN
		SELECT app_name FROM app_settings WHERE id = 1`, "USING INTEGER PRIMARY KEY")
}

func TestSchemaV50AddsCurrencyDefaultsAndDatabaseConstraints(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	if _, err := database.db.ExecContext(ctx, `
		ALTER TABLE app_settings DROP COLUMN currency_symbol;
		ALTER TABLE app_settings DROP COLUMN currency;
		PRAGMA user_version = 49;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v49 to v50) error=%v", err)
	}
	settings, err := database.GetSiteSettings(ctx)
	if err != nil || settings.Currency != "CNY" || settings.CurrencySymbol != "¥" {
		t.Fatalf("v50 defaults settings=%#v err=%v", settings, err)
	}
	for _, statement := range []string{
		`UPDATE app_settings SET currency='usd' WHERE id=1`,
		`UPDATE app_settings SET currency='US' WHERE id=1`,
		`UPDATE app_settings SET currency_symbol='` + strings.Repeat("a", 17) + `' WHERE id=1`,
		`UPDATE app_settings SET currency_symbol=char(0) WHERE id=1`,
	} {
		if _, err := database.db.ExecContext(ctx, statement); err == nil {
			t.Fatalf("database accepted invalid v50 statement %q", statement)
		}
	}
}

func TestUpdateLegacySiteSettingsIsPartialNormalizedAndAtomic(t *testing.T) {
	database := newTestStore(t)
	administrator, err := database.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "legacy-site@example.test", PasswordHash: "hash"}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	currency, symbol := " usd ", " $ "
	updated, err := database.UpdateLegacySiteSettings(t.Context(), administrator.ID, SaveLegacySiteSettingsInput{Currency: &currency, CurrencySymbol: &symbol}, time.Unix(2, 0))
	if err != nil || updated.Currency != "USD" || updated.CurrencySymbol != "$" || updated.AppName != "Xboard-Go" {
		t.Fatalf("legacy site update=%#v err=%v", updated, err)
	}
	invalid := "US"
	if _, err := database.UpdateLegacySiteSettings(t.Context(), administrator.ID, SaveLegacySiteSettingsInput{Currency: &invalid}, time.Unix(3, 0)); err == nil {
		t.Fatal("invalid legacy site currency was accepted")
	}
	preserved, err := database.GetSiteSettings(t.Context())
	if err != nil || preserved.Currency != "USD" || preserved.CurrencySymbol != "$" || preserved.Revision != updated.Revision {
		t.Fatalf("rejected update changed settings=%#v err=%v", preserved, err)
	}
}

func BenchmarkClientAppRuntimeSettingsRead(b *testing.B) {
	database := newTestStore(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := database.GetClientAppRuntimeSettings(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
