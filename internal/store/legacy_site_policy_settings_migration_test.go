package store

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestImportLegacySitePolicySettingsIsComposableIdempotentAndDriftSafe(t *testing.T) {
	database := newTestStore(t)
	if _, err := database.db.Exec(`UPDATE app_settings SET app_name='Migrated identity',currency='USD' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	settings := nonDefaultLegacySitePolicySettings()
	input := validLegacySitePolicySettingsImport(settings)
	now := time.Date(2026, 8, 31, 8, 0, 0, 0, time.UTC)
	report, err := database.ImportLegacySitePolicySettings(t.Context(), input, now)
	if err != nil || report.Settings.SourceRows != 1 || report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		t.Fatalf("ImportLegacySitePolicySettings()=(%#v,%v)", report, err)
	}
	site, err := database.GetSiteSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := database.GetTicketSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := database.GetCaptchaSecretCiphers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if site.AppName != "Migrated identity" || site.Currency != "USD" || !site.StopRegister || !site.EmailVerificationEnabled ||
		!site.EmailWhitelistEnabled || strings.Join(site.EmailWhitelistSuffixes, ",") != "example.com,mail.example" ||
		!site.GmailAliasLimitEnabled || !site.RegistrationIPLimitEnabled || site.RegistrationIPLimitCount != 7 || site.RegistrationIPLimitMinutes != 120 ||
		!site.PasswordLimitEnabled || site.PasswordLimitCount != 4 || site.PasswordLimitMinutes != 30 ||
		!site.InvitationForceEnabled || site.InvitationCodeLimit != 9 || !site.InvitationNeverExpire ||
		!site.CaptchaEnabled || site.CaptchaType != "turnstile" || !site.RecaptchaSecretConfigured || !site.RecaptchaV3SecretConfigured || !site.TurnstileSecretConfigured ||
		!ticket.TicketMustWaitReply || !bytes.Equal(secrets.Recaptcha, settings.RecaptchaSecretCipher) ||
		!bytes.Equal(secrets.RecaptchaV3, settings.RecaptchaV3SecretCipher) || !bytes.Equal(secrets.Turnstile, settings.TurnstileSecretCipher) {
		t.Fatalf("site=%#v ticket=%#v secrets=%#v", site, ticket, secrets)
	}
	repeated, err := database.ImportLegacySitePolicySettings(t.Context(), input, now.Add(time.Hour))
	if err != nil || !repeated.AlreadyApplied || !repeated.AppliedAt.Equal(now) {
		t.Fatalf("idempotent import=(%#v,%v)", repeated, err)
	}
	lookedUp, found, err := database.LookupLegacySitePolicySettingsImport(t.Context(), input.SourceSHA256)
	if err != nil || !found || !lookedUp.AlreadyApplied || lookedUp.Settings.TargetChecksum != report.Settings.TargetChecksum {
		t.Fatalf("lookup import=(%#v,%t,%v)", lookedUp, found, err)
	}
	if _, err := database.db.Exec(`UPDATE app_settings SET ticket_must_wait_reply=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.LookupLegacySitePolicySettingsImport(t.Context(), input.SourceSHA256); !errors.Is(err, ErrConflict) {
		t.Fatalf("drifted lookup error=%v, want ErrConflict", err)
	}
	if _, err := database.ImportLegacySitePolicySettings(t.Context(), input, now.Add(2*time.Hour)); !errors.Is(err, ErrConflict) {
		t.Fatalf("drifted import error=%v, want ErrConflict", err)
	}
}

func TestImportLegacySitePolicySettingsRejectsNonPristineAndDifferentSource(t *testing.T) {
	settings := nonDefaultLegacySitePolicySettings()
	input := validLegacySitePolicySettingsImport(settings)
	nonPristine := newTestStore(t)
	if _, err := nonPristine.db.Exec(`UPDATE app_settings SET invite_gen_limit=6 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := nonPristine.ImportLegacySitePolicySettings(t.Context(), input, time.Unix(1, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("non-pristine import error=%v, want ErrConflict", err)
	}

	clean := newTestStore(t)
	if _, err := clean.ImportLegacySitePolicySettings(t.Context(), input, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	input.SourceSHA256 = strings.Repeat("f", 64)
	if _, err := clean.ImportLegacySitePolicySettings(t.Context(), input, time.Unix(3, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("different source import error=%v, want ErrConflict", err)
	}
}

func nonDefaultLegacySitePolicySettings() LegacySitePolicySettings {
	return LegacySitePolicySettings{
		StopRegister: true, EmailVerificationEnabled: true, EmailWhitelistEnabled: true,
		EmailWhitelistSuffixes: []string{"example.com", "mail.example"}, GmailAliasLimitEnabled: true,
		RegistrationIPLimitEnabled: true, RegistrationIPLimitCount: 7, RegistrationIPLimitMinutes: 120,
		PasswordLimitEnabled: true, PasswordLimitCount: 4, PasswordLimitMinutes: 30,
		InvitationForceEnabled: true, InvitationCodeLimit: 9, InvitationNeverExpire: true,
		CaptchaEnabled: true, CaptchaType: "turnstile", RecaptchaSiteKey: "recaptcha-site",
		RecaptchaSecretConfigured: true, RecaptchaSecretCipher: bytes.Repeat([]byte{0x11}, 64),
		RecaptchaV3SiteKey: "recaptcha-v3-site", RecaptchaV3ScoreThreshold: 0.7,
		RecaptchaV3SecretConfigured: true, RecaptchaV3SecretCipher: bytes.Repeat([]byte{0x22}, 64),
		TurnstileSiteKey: "turnstile-site", TurnstileSecretConfigured: true, TurnstileSecretCipher: bytes.Repeat([]byte{0x33}, 64),
		TicketMustWaitReply: true,
	}
}

func validLegacySitePolicySettingsImport(settings LegacySitePolicySettings) LegacySitePolicySettingsImport {
	return LegacySitePolicySettingsImport{
		Slice: LegacySitePolicySettingsSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 1024,
		Settings: settings, Checksum: LegacySitePolicySettingsChecksum(settings),
		RollbackBackupPath: "backup.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
	}
}
