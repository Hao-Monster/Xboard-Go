package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestRunCommandImportsLegacySitePolicySettingsWithoutExposingSecrets(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy-site-policy-settings.db")
	recaptchaSecret := "recaptcha-private-value"
	recaptchaV3Secret := "recaptcha-v3-private-value"
	turnstileSecret := "turnstile-private-value"
	source, err := sql.Open("sqlite", "file:"+sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('stop_register','1'),('email_whitelist_enable','1'),('email_whitelist_suffix','["example.com","mail.example"]'),
		('register_limit_by_ip_enable','1'),('register_limit_count','7'),('register_limit_expire','120'),
		('password_limit_count','4'),('password_limit_expire','30'),
		('invite_force','1'),('invite_gen_limit','9'),('invite_never_expire','1'),
		('captcha_enable','1'),('captcha_type','turnstile'),
		('recaptcha_site_key','recaptcha-site'),('recaptcha_key',?),
		('recaptcha_v3_site_key','recaptcha-v3-site'),('recaptcha_v3_secret_key',?),
		('turnstile_site_key','turnstile-site'),('turnstile_secret_key',?),
		('ticket_must_wait_reply','1')`, recaptchaSecret, recaptchaV3Secret, turnstileSecret); err != nil {
		t.Fatal(err)
	}
	_ = source.Close()
	targetPath := filepath.Join(directory, "target.db")
	target, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = target.Close()
	key := bytes.Repeat([]byte{0x51}, 32)
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	t.Setenv("XBOARD_SETTINGS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	backupPath := filepath.Join(directory, "pre-site-policy-settings.xbbackup")
	now := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)

	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), []string{
		"migration", "import-legacy-site-policy-settings", "--source", sourcePath,
		"--backup-output", backupPath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || stderr.Len() != 0 {
		t.Fatalf("runCommand() handled=%t err=%v stderr=%q", handled, err, stderr.String())
	}
	for _, secret := range []string{recaptchaSecret, recaptchaV3Secret, turnstileSecret, base64.StdEncoding.EncodeToString(key)} {
		if strings.Contains(stdout.String(), secret) {
			t.Fatal("migration output exposed CAPTCHA credential material")
		}
	}
	var result legacySitePolicySettingsMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.Action != "migration.import-legacy-site-policy-settings" || result.Result.AlreadyApplied || result.Result.Settings.SourceChecksum != result.Result.Settings.TargetChecksum {
		t.Fatalf("result=%#v", result)
	}
	inspection, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	site, err := inspection.GetSiteSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := inspection.GetTicketSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := inspection.GetCaptchaSecretCiphers(t.Context())
	_ = inspection.Close()
	if err != nil || !site.StopRegister || !site.EmailWhitelistEnabled || strings.Join(site.EmailWhitelistSuffixes, ",") != "example.com,mail.example" ||
		!site.RegistrationIPLimitEnabled || site.RegistrationIPLimitCount != 7 || site.RegistrationIPLimitMinutes != 120 ||
		site.PasswordLimitCount != 4 || site.PasswordLimitMinutes != 30 || !site.InvitationForceEnabled || site.InvitationCodeLimit != 9 || !site.InvitationNeverExpire ||
		!site.CaptchaEnabled || site.CaptchaType != "turnstile" || !site.RecaptchaSecretConfigured || !site.RecaptchaV3SecretConfigured || !site.TurnstileSecretConfigured || !ticket.TicketMustWaitReply {
		t.Fatalf("site=%#v ticket=%#v err=%v", site, ticket, err)
	}
	cipherBox, err := appsettings.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	for purpose, encrypted := range map[appsettings.SecretPurpose]struct {
		ciphertext []byte
		plaintext  string
	}{
		appsettings.RecaptchaSecretPurpose:   {secrets.Recaptcha, recaptchaSecret},
		appsettings.RecaptchaV3SecretPurpose: {secrets.RecaptchaV3, recaptchaV3Secret},
		appsettings.TurnstileSecretPurpose:   {secrets.Turnstile, turnstileSecret},
	} {
		plaintext, err := cipherBox.DecryptFor(purpose, encrypted.ciphertext)
		if err != nil || string(plaintext) != encrypted.plaintext {
			t.Fatalf("decrypt migrated %s secret error=%v", purpose, err)
		}
		clearMigrationKey(plaintext)
	}

	restoredPath := filepath.Join(directory, "restored-pre-site-policy-settings.db")
	if _, err := backup.Restore(t.Context(), backupPath, restoredPath); err != nil {
		t.Fatalf("Restore(rollback backup) error=%v", err)
	}
	restored, err := store.OpenSQLite("file:" + restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	restoredSite, settingsErr := restored.GetSiteSettings(t.Context())
	_, imported, lookupErr := restored.LookupLegacySitePolicySettingsImport(t.Context(), result.Source.SHA256)
	closeErr := restored.Close()
	if settingsErr != nil || lookupErr != nil || closeErr != nil || restoredSite.StopRegister || restoredSite.CaptchaEnabled || imported {
		t.Fatalf("restored state site=%#v imported=%t settingsErr=%v lookupErr=%v closeErr=%v", restoredSite, imported, settingsErr, lookupErr, closeErr)
	}

	stdout.Reset()
	handled, err = runCommand(context.Background(), []string{
		"migration", "import-legacy-site-policy-settings", "--source", sourcePath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now.Add(time.Minute) })
	if !handled || err != nil {
		t.Fatalf("idempotent run handled=%t err=%v", handled, err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || !result.Result.AlreadyApplied || result.Result.AppliedAt != now {
		t.Fatalf("idempotent result=%#v err=%v", result, err)
	}
}

func TestRunCommandRejectsSitePolicyMigrationWithoutOfflineConfirmationOrEncryptionKey(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy-site-policy-settings.db")
	source, _ := sql.Open("sqlite", "file:"+sourcePath)
	_, _ = source.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES ('captcha_enable','1'),('captcha_type','turnstile'),('turnstile_site_key','site'),('turnstile_secret_key','secret')`)
	_ = source.Close()
	var stdout, stderr bytes.Buffer
	if handled, err := runCommand(t.Context(), []string{"migration", "import-legacy-site-policy-settings", "--source", sourcePath}, &stdout, &stderr, time.Now); !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") {
		t.Fatalf("missing confirmation handled=%t err=%v", handled, err)
	}
	if handled, err := runCommand(t.Context(), []string{"migration", "import-legacy-site-policy-settings", "--source", sourcePath, "--confirm-offline"}, &stdout, &stderr, time.Now); !handled || err == nil || !strings.Contains(err.Error(), "XBOARD_SETTINGS_ENCRYPTION_KEY") {
		t.Fatalf("missing key handled=%t err=%v", handled, err)
	}
}
