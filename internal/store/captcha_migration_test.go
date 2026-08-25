package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrateV22ToV23AddsDisabledConstrainedCaptchaSettings(t *testing.T) {
	database, err := OpenSQLite("file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "v22.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	for step, schema := range []string{
		schemaV1, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6, schemaV7, schemaV7Constraints,
		schemaV8, schemaV9, schemaV10, schemaV11, schemaV12, schemaV13, schemaV14, schemaV15,
		schemaV16, schemaV17, schemaV18, schemaV19, schemaV20, schemaV21, schemaV22,
	} {
		if _, err := database.db.ExecContext(ctx, schema); err != nil {
			t.Fatalf("apply pre-v23 schema step %d: %v", step+1, err)
		}
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO users (email, password_hash, subscription_token, created_at, updated_at)
		VALUES ('preserved-v22@example.test', 'hash', 'fedcba9876543210fedcba9876543210', 1, 1);
		UPDATE app_settings SET app_name = 'V22 Board', revision = 41;
		PRAGMA user_version = 22;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v22 to v23) error = %v", err)
	}
	settings, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var version, users int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email = 'preserved-v22@example.test'`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion || users != 1 || settings.AppName != "V22 Board" || settings.Revision != 41 || settings.CaptchaEnabled || settings.CaptchaType != "recaptcha" || settings.RecaptchaV3ScoreThreshold != 0.5 {
		t.Fatalf("v22 to v23 migration version=%d users=%d settings=%#v", version, users, settings)
	}
	for _, statement := range []string{
		`UPDATE app_settings SET captcha_enable = 2 WHERE id = 1`,
		`UPDATE app_settings SET captcha_type = 'hcaptcha' WHERE id = 1`,
		`UPDATE app_settings SET recaptcha_v3_score_threshold = 0 WHERE id = 1`,
		`UPDATE app_settings SET recaptcha_v3_score_threshold = 1.01 WHERE id = 1`,
		`UPDATE app_settings SET turnstile_site_key = printf('%0513d', 1) WHERE id = 1`,
		`UPDATE app_settings SET turnstile_secret_cipher = X'01' WHERE id = 1`,
	} {
		if _, err := database.db.ExecContext(ctx, statement); err == nil {
			t.Fatalf("database accepted invalid v23 statement %q", statement)
		}
	}
}
