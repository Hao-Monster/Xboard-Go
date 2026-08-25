package store

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestCaptchaSettingsPersistPublicFieldsAndOpaqueSecrets(t *testing.T) {
	database := newTestStore(t)
	admin := createTicketTestUser(t, database, "captcha-settings-admin@example.test", time.Unix(1, 0))
	initial, err := database.GetSiteSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if initial.CaptchaEnabled || initial.CaptchaType != "recaptcha" || initial.RecaptchaV3ScoreThreshold != 0.5 || initial.RecaptchaSecretConfigured || initial.RecaptchaV3SecretConfigured || initial.TurnstileSecretConfigured {
		t.Fatalf("initial CAPTCHA settings = %#v", initial)
	}
	input := siteSettingsSaveInput(initial)
	input.CaptchaEnabled = true
	input.CaptchaType = "turnstile"
	input.RecaptchaSiteKey = "recaptcha-site"
	input.RecaptchaV3SiteKey = "recaptcha-v3-site"
	input.RecaptchaV3ScoreThreshold = 0.7
	input.TurnstileSiteKey = "turnstile-site"
	input.ReplaceRecaptchaSecret = true
	input.RecaptchaSecretCipher = bytes.Repeat([]byte{0x11}, 64)
	input.ReplaceRecaptchaV3Secret = true
	input.RecaptchaV3SecretCipher = bytes.Repeat([]byte{0x22}, 64)
	input.ReplaceTurnstileSecret = true
	input.TurnstileSecretCipher = bytes.Repeat([]byte{0x33}, 64)
	updated, err := database.UpdateSiteSettings(t.Context(), admin.ID, initial.Revision, input, time.Unix(2, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !updated.CaptchaEnabled || updated.CaptchaType != "turnstile" || updated.TurnstileSiteKey != "turnstile-site" || !updated.RecaptchaSecretConfigured || !updated.RecaptchaV3SecretConfigured || !updated.TurnstileSecretConfigured {
		t.Fatalf("updated CAPTCHA settings = %#v", updated)
	}
	secrets, err := database.GetCaptchaSecretCiphers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(secrets.Recaptcha, bytes.Repeat([]byte{0x11}, 64)) || !bytes.Equal(secrets.RecaptchaV3, bytes.Repeat([]byte{0x22}, 64)) || !bytes.Equal(secrets.Turnstile, bytes.Repeat([]byte{0x33}, 64)) {
		t.Fatalf("stored CAPTCHA secret ciphers = %#v", secrets)
	}

	preserve := siteSettingsSaveInput(updated)
	preserve.CaptchaEnabled = false
	preserved, err := database.UpdateSiteSettings(t.Context(), admin.ID, updated.Revision, preserve, time.Unix(3, 0))
	if err != nil || preserved.CaptchaEnabled || !preserved.TurnstileSecretConfigured {
		t.Fatalf("preserve update = %#v err=%v", preserved, err)
	}
	clear := siteSettingsSaveInput(preserved)
	clear.ReplaceTurnstileSecret = true
	cleared, err := database.UpdateSiteSettings(t.Context(), admin.ID, preserved.Revision, clear, time.Unix(4, 0))
	if err != nil || cleared.TurnstileSecretConfigured {
		t.Fatalf("clear update = %#v err=%v", cleared, err)
	}
}

func TestCaptchaSettingsValidateProviderConfigurationAndBounds(t *testing.T) {
	database := newTestStore(t)
	admin := createTicketTestUser(t, database, "captcha-validation-admin@example.test", time.Unix(1, 0))
	current, _ := database.GetSiteSettings(t.Context())
	valid := siteSettingsSaveInput(current)
	valid.CaptchaType = "recaptcha"
	valid.RecaptchaV3ScoreThreshold = 0.5
	for name, mutate := range map[string]func(*SaveSiteSettingsInput){
		"unknown type":        func(input *SaveSiteSettingsInput) { input.CaptchaType = "hcaptcha" },
		"negative threshold":  func(input *SaveSiteSettingsInput) { input.RecaptchaV3ScoreThreshold = -0.01 },
		"threshold above one": func(input *SaveSiteSettingsInput) { input.RecaptchaV3ScoreThreshold = 1.01 },
		"long site key":       func(input *SaveSiteSettingsInput) { input.RecaptchaSiteKey = string(bytes.Repeat([]byte{'x'}, 513)) },
		"enabled missing site key": func(input *SaveSiteSettingsInput) {
			input.CaptchaEnabled = true
			input.ReplaceRecaptchaSecret = true
			input.RecaptchaSecretCipher = bytes.Repeat([]byte{0x44}, 64)
		},
		"enabled missing secret": func(input *SaveSiteSettingsInput) { input.CaptchaEnabled = true; input.RecaptchaSiteKey = "site" },
	} {
		t.Run(name, func(t *testing.T) {
			input := valid
			mutate(&input)
			if _, err := database.UpdateSiteSettings(t.Context(), admin.ID, current.Revision, input, time.Unix(2, 0)); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("UpdateSiteSettings() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func siteSettingsSaveInput(settings SiteSettings) SaveSiteSettingsInput {
	return SaveSiteSettingsInput{
		AppName: settings.AppName, AppDescription: settings.AppDescription, AppURL: settings.AppURL, TOSURL: settings.TOSURL, Logo: settings.Logo,
		StopRegister: settings.StopRegister, EmailVerificationEnabled: settings.EmailVerificationEnabled,
		EmailWhitelistEnabled: settings.EmailWhitelistEnabled, EmailWhitelistSuffixes: append([]string(nil), settings.EmailWhitelistSuffixes...), GmailAliasLimitEnabled: settings.GmailAliasLimitEnabled,
		RegistrationIPLimitEnabled: settings.RegistrationIPLimitEnabled, RegistrationIPLimitCount: settings.RegistrationIPLimitCount, RegistrationIPLimitMinutes: settings.RegistrationIPLimitMinutes,
		PasswordLimitEnabled: settings.PasswordLimitEnabled, PasswordLimitCount: settings.PasswordLimitCount, PasswordLimitMinutes: settings.PasswordLimitMinutes,
		InvitationForceEnabled: settings.InvitationForceEnabled, InvitationCodeLimit: settings.InvitationCodeLimit, InvitationNeverExpire: settings.InvitationNeverExpire,
		MailLoginEnabled: settings.MailLoginEnabled, CaptchaEnabled: settings.CaptchaEnabled, CaptchaType: settings.CaptchaType,
		RecaptchaSiteKey: settings.RecaptchaSiteKey, RecaptchaV3SiteKey: settings.RecaptchaV3SiteKey, RecaptchaV3ScoreThreshold: settings.RecaptchaV3ScoreThreshold, TurnstileSiteKey: settings.TurnstileSiteKey,
	}
}
