package legacymigration

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReadSitePolicySettingsSnapshotPreservesPoliciesAndHidesSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-site-policy-settings.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	recaptchaSecret := "recaptcha-secret-value"
	recaptchaV3Secret := "recaptcha-v3-secret-value"
	turnstileSecret := "turnstile-secret-value"
	if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('stop_register','1'),('email_verify','1'),('email_whitelist_enable','1'),
		('email_whitelist_suffix','["Example.COM"," mail.example ","example.com"]'),('email_gmail_limit_enable','1'),
		('register_limit_by_ip_enable','1'),('register_limit_count','7'),('register_limit_expire','120'),
		('password_limit_enable','1'),('password_limit_count','4'),('password_limit_expire','30'),
		('invite_force','1'),('invite_gen_limit','9'),('invite_never_expire','1'),
		('captcha_enable','1'),('captcha_type','turnstile'),
		('recaptcha_key',?),('recaptcha_site_key','recaptcha-site'),
		('recaptcha_v3_secret_key',?),('recaptcha_v3_site_key','recaptcha-v3-site'),
		('recaptcha_v3_score_threshold','0.7'),('turnstile_secret_key',?),('turnstile_site_key','turnstile-site'),
		('ticket_must_wait_reply','1'),('payment_secret',zeroblob(1048576))`, recaptchaSecret, recaptchaV3Secret, turnstileSecret); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()

	snapshot, err := ReadSitePolicySettingsSnapshot(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.ClearSecrets()
	settings := snapshot.Settings
	if !settings.StopRegister || !settings.EmailVerificationEnabled || !settings.EmailWhitelistEnabled || !settings.GmailAliasLimitEnabled ||
		!settings.RegistrationIPLimitEnabled || settings.RegistrationIPLimitCount != 7 || settings.RegistrationIPLimitMinutes != 120 ||
		!settings.PasswordLimitEnabled || settings.PasswordLimitCount != 4 || settings.PasswordLimitMinutes != 30 ||
		!settings.InvitationForceEnabled || settings.InvitationCodeLimit != 9 || !settings.InvitationNeverExpire ||
		!settings.CaptchaEnabled || settings.CaptchaType != "turnstile" || settings.RecaptchaSiteKey != "recaptcha-site" ||
		settings.RecaptchaV3SiteKey != "recaptcha-v3-site" || settings.RecaptchaV3ScoreThreshold != 0.7 ||
		settings.TurnstileSiteKey != "turnstile-site" || !settings.TicketMustWaitReply {
		t.Fatalf("settings=%#v", settings)
	}
	if got := strings.Join(settings.EmailWhitelistSuffixes, ","); got != "example.com,mail.example" {
		t.Fatalf("whitelist=%q", got)
	}
	if !settings.RecaptchaSecretConfigured || !settings.RecaptchaV3SecretConfigured || !settings.TurnstileSecretConfigured ||
		string(snapshot.RecaptchaSecret) != recaptchaSecret || string(snapshot.RecaptchaV3Secret) != recaptchaV3Secret || string(snapshot.TurnstileSecret) != turnstileSecret {
		t.Fatalf("secret metadata=%#v", settings)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{recaptchaSecret, recaptchaV3Secret, turnstileSecret} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("snapshot serialization exposed a CAPTCHA secret")
		}
	}
	if snapshot.Checksum == "" || snapshot.SHA256 == "" || snapshot.Size < 1 {
		t.Fatalf("snapshot identity=%#v", snapshot)
	}
}

func TestReadSitePolicySettingsSnapshotUsesLegacyDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-site-policy-defaults.db")
	database, _ := sql.Open("sqlite", "file:"+path)
	_, _ = database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT)`)
	_ = database.Close()
	snapshot, err := ReadSitePolicySettingsSnapshot(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.ClearSecrets()
	settings := snapshot.Settings
	if settings.StopRegister || settings.EmailVerificationEnabled || settings.EmailWhitelistEnabled || settings.GmailAliasLimitEnabled ||
		settings.RegistrationIPLimitEnabled || settings.RegistrationIPLimitCount != 3 || settings.RegistrationIPLimitMinutes != 60 ||
		!settings.PasswordLimitEnabled || settings.PasswordLimitCount != 5 || settings.PasswordLimitMinutes != 60 ||
		settings.InvitationForceEnabled || settings.InvitationCodeLimit != 5 || settings.InvitationNeverExpire ||
		settings.CaptchaEnabled || settings.CaptchaType != "recaptcha" || settings.RecaptchaV3ScoreThreshold != 0.5 || settings.TicketMustWaitReply ||
		len(settings.EmailWhitelistSuffixes) != 9 {
		t.Fatalf("defaults=%#v", settings)
	}
}

func TestReadSitePolicySettingsSnapshotRejectsUnsafeData(t *testing.T) {
	for name, rows := range map[string]string{
		"duplicate":          `('stop_register','0'),('stop_register','1')`,
		"invalid bool":       `('stop_register','yes')`,
		"invalid count":      `('password_limit_count','21')`,
		"invalid whitelist":  `('email_whitelist_enable','1'),('email_whitelist_suffix','["not-a-domain"]')`,
		"invalid threshold":  `('recaptcha_v3_score_threshold','0')`,
		"incomplete captcha": `('captcha_enable','1'),('captcha_type','turnstile'),('turnstile_site_key','site-only')`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy-invalid-site-policy.db")
			database, _ := sql.Open("sqlite", "file:"+path)
			if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT); INSERT INTO v2_settings(name,value) VALUES ` + rows); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()
			if _, err := ReadSitePolicySettingsSnapshot(t.Context(), path); err == nil {
				t.Fatal("invalid site policy settings snapshot was accepted")
			}
		})
	}
}

func TestParseLegacyPolicySecretPreservesCredentialBytesAndRecognizesNull(t *testing.T) {
	for _, raw := range [][]byte{nil, {}, []byte("null"), []byte(" \tNuLl\r\n")} {
		secret, configured, err := parseLegacyPolicySecret(raw)
		if err != nil || configured || secret != nil {
			t.Fatalf("parseLegacyPolicySecret(%q)=(%q,%t,%v)", raw, secret, configured, err)
		}
	}
	raw := []byte(" \tcredential-bytes\r\n")
	secret, configured, err := parseLegacyPolicySecret(raw)
	if err != nil || !configured || !bytes.Equal(secret, raw) {
		t.Fatalf("parseLegacyPolicySecret()=(%q,%t,%v)", secret, configured, err)
	}
	zeroLegacyBytes(secret)
}

func BenchmarkReadSitePolicySettingsSnapshot(b *testing.B) {
	path := filepath.Join(b.TempDir(), "legacy-site-policy-benchmark.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('stop_register','0'),('email_verify','0'),('email_whitelist_enable','1'),
		('email_whitelist_suffix','["example.com","mail.example"]'),('email_gmail_limit_enable','0'),
		('register_limit_by_ip_enable','1'),('register_limit_count','7'),('register_limit_expire','120'),
		('password_limit_enable','1'),('password_limit_count','4'),('password_limit_expire','30'),
		('invite_force','1'),('invite_gen_limit','9'),('invite_never_expire','0'),
		('captcha_enable','0'),('captcha_type','turnstile'),
		('recaptcha_key','recaptcha-secret'),('recaptcha_site_key','recaptcha-site'),
		('recaptcha_v3_secret_key','recaptcha-v3-secret'),('recaptcha_v3_site_key','recaptcha-v3-site'),
		('recaptcha_v3_score_threshold','0.7'),('turnstile_secret_key','turnstile-secret'),('turnstile_site_key','turnstile-site'),
		('ticket_must_wait_reply','1')`); err != nil {
		b.Fatal(err)
	}
	_ = database.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		snapshot, err := ReadSitePolicySettingsSnapshot(b.Context(), path)
		if err != nil {
			b.Fatal(err)
		}
		snapshot.ClearSecrets()
	}
}
