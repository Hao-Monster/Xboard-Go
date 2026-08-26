package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/payment"
	"github.com/Hao-Monster/Xboard-Go/internal/security"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestPrepareSQLiteDirectory(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "nested", "xboard.db")
	if err := prepareSQLiteDirectory("file:" + databasePath); err != nil {
		t.Fatalf("prepareSQLiteDirectory() error = %v", err)
	}
	info, err := os.Stat(filepath.Dir(databasePath))
	if err != nil || !info.IsDir() {
		t.Fatalf("database directory was not created: info=%v err=%v", info, err)
	}
	if err := prepareSQLiteDirectory("file:memory?mode=memory&cache=shared"); err != nil {
		t.Fatalf("memory DSN should be a no-op: %v", err)
	}
	if err := os.WriteFile(databasePath, []byte("database"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(databasePath+"-wal", []byte("wal"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := secureSQLiteFiles("file:" + databasePath); err != nil {
		t.Fatalf("secureSQLiteFiles() error = %v", err)
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{filepath.Dir(databasePath), databasePath, databasePath + "-wal"} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			want := os.FileMode(0o600)
			if info.IsDir() {
				want = 0o700
			}
			if info.Mode().Perm() != want {
				t.Fatalf("%s permissions = %o, want %o", path, info.Mode().Perm(), want)
			}
		}
	}
}

func TestInitializeInvitationProtectorFailsClosedForStoredCodes(t *testing.T) {
	ctx := t.Context()
	database, err := store.OpenSQLite("file:" + filepath.Join(t.TempDir(), "invitations.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 6, 0, 0, 0, time.UTC)
	owner, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "invitation-main@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x42}, 32)
	protector, err := security.NewInvitationProtector(key)
	if err != nil {
		t.Fatal(err)
	}
	const code = "Abcd1234"
	digest, err := protector.CodeDigest(code)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := protector.EncryptCode(owner.ID, code)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateInvitationCode(ctx, owner.ID, store.CreateInvitationCodeInput{
		CodeDigest: digest, CodeCipher: ciphertext,
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := initializeInvitationProtector(ctx, database, nil); err == nil {
		t.Fatal("initializeInvitationProtector() accepted a missing key")
	}
	if _, err := initializeInvitationProtector(ctx, database, bytes.Repeat([]byte{0x24}, 32)); err == nil {
		t.Fatal("initializeInvitationProtector() accepted the wrong key")
	}
	if initialized, err := initializeInvitationProtector(ctx, database, key); err != nil || initialized == nil {
		t.Fatalf("initializeInvitationProtector() rejected the matching key: protector=%v err=%v", initialized, err)
	}
}

func TestInitializeLoginLinkProtectorFailsClosedForQueuedMail(t *testing.T) {
	ctx := t.Context()
	database, err := store.OpenSQLite("file:" + filepath.Join(t.TempDir(), "login-links.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 7, 0, 0, 0, time.UTC)
	owner, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "login-link-main@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	ticketSettings, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(ctx, owner.ID, ticketSettings.Revision, store.SaveTicketSettingsInput{
		AppName: "Xboard-Go", SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025,
		SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, now); err != nil {
		t.Fatal(err)
	}
	siteSettings, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateSiteSettings(ctx, owner.ID, siteSettings.Revision, store.SaveSiteSettingsInput{
		AppName: siteSettings.AppName, RegistrationIPLimitCount: siteSettings.RegistrationIPLimitCount,
		RegistrationIPLimitMinutes: siteSettings.RegistrationIPLimitMinutes, InvitationCodeLimit: siteSettings.InvitationCodeLimit,
		PasswordLimitEnabled: siteSettings.PasswordLimitEnabled, PasswordLimitCount: siteSettings.PasswordLimitCount,
		PasswordLimitMinutes:  siteSettings.PasswordLimitMinutes,
		InvitationNeverExpire: siteSettings.InvitationNeverExpire, MailLoginEnabled: true,
	}, now); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x52}, 32)
	protector, err := security.NewLoginLinkProtector(key)
	if err != nil {
		t.Fatal(err)
	}
	token, err := protector.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	emailDigest, _ := protector.EmailDigest(owner.Email)
	tokenDigest, _ := protector.TokenDigest(security.LoginLinkPurposeEmail, token)
	ciphertext, _ := protector.EncryptToken(owner.ID, token)
	if queued, err := database.RequestMailLoginLink(ctx, store.MailLoginLinkRequestInput{
		Email: owner.Email, ExpectedUserID: owner.ID, EmailDigest: emailDigest, TokenDigest: tokenDigest, TokenCipher: ciphertext,
		Redirect: "dashboard", LinkBaseURL: "https://panel.example.test",
	}, now); err != nil || !queued {
		t.Fatalf("RequestMailLoginLink() queued=%v err=%v", queued, err)
	}
	if _, err := initializeLoginLinkProtector(ctx, database, nil); err == nil {
		t.Fatal("initializeLoginLinkProtector() accepted a missing key")
	}
	if _, err := initializeLoginLinkProtector(ctx, database, bytes.Repeat([]byte{0x24}, 32)); err == nil {
		t.Fatal("initializeLoginLinkProtector() accepted the wrong key")
	}
	if initialized, err := initializeLoginLinkProtector(ctx, database, key); err != nil || initialized == nil {
		t.Fatalf("initializeLoginLinkProtector() rejected the matching key: protector=%v err=%v", initialized, err)
	}
}

func TestInitializeSettingsCipherFailsClosedForStoredCredentials(t *testing.T) {
	ctx := context.Background()
	database, err := store.OpenSQLite("file:" + filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "settings-main@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x42}, 32)
	cipherBox, err := appsettings.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := cipherBox.Encrypt([]byte("smtp-password"))
	if err != nil {
		t.Fatal(err)
	}
	initial, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(ctx, administrator.ID, initial.Revision, store.SaveTicketSettingsInput{
		AppName: "Xboard", SMTPEnabled: true, SMTPHost: "smtp.example.test", SMTPPort: 587,
		SMTPUsername: "mailer", SMTPEncryption: "starttls", SMTPFromAddress: "support@example.test",
		ReplaceSMTPPassword: true, SMTPPasswordCipher: ciphertext,
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := initializeSettingsCipher(ctx, database, nil); err == nil {
		t.Fatal("initializeSettingsCipher() accepted a missing key")
	}
	if _, err := initializeSettingsCipher(ctx, database, bytes.Repeat([]byte{0x24}, 32)); err == nil {
		t.Fatal("initializeSettingsCipher() accepted the wrong key")
	}
	if _, err := initializeSettingsCipher(ctx, database, key); err != nil {
		t.Fatalf("initializeSettingsCipher() rejected the matching key: %v", err)
	}
}

func TestInitializeSettingsCipherFailsClosedForStoredCaptchaCredentials(t *testing.T) {
	ctx := context.Background()
	database, err := store.OpenSQLite("file:" + filepath.Join(t.TempDir(), "captcha-settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "captcha-main@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x63}, 32)
	cipherBox, err := appsettings.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := cipherBox.EncryptFor(appsettings.TurnstileSecretPurpose, []byte("turnstile-secret"))
	if err != nil {
		t.Fatal(err)
	}
	current, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := saveSiteSettingsForMainTest(current)
	input.CaptchaEnabled = true
	input.CaptchaType = "turnstile"
	input.TurnstileSiteKey = "turnstile-site-key"
	input.ReplaceTurnstileSecret = true
	input.TurnstileSecretCipher = ciphertext
	if _, err := database.UpdateSiteSettings(ctx, administrator.ID, current.Revision, input, now); err != nil {
		t.Fatal(err)
	}
	if _, err := initializeSettingsCipher(ctx, database, nil); err == nil {
		t.Fatal("initializeSettingsCipher() accepted a missing key for CAPTCHA credentials")
	}
	if _, err := initializeSettingsCipher(ctx, database, bytes.Repeat([]byte{0x24}, 32)); err == nil {
		t.Fatal("initializeSettingsCipher() accepted the wrong key for CAPTCHA credentials")
	}
	if initialized, err := initializeSettingsCipher(ctx, database, key); err != nil || initialized == nil {
		t.Fatalf("initializeSettingsCipher() rejected the CAPTCHA key: cipher=%v err=%v", initialized, err)
	}
}

func TestInitializeSettingsCipherFailsClosedForStoredPaymentCredentials(t *testing.T) {
	ctx := context.Background()
	database, err := store.OpenSQLite("file:" + filepath.Join(t.TempDir(), "payment-settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x71}, 32)
	cipherBox, err := appsettings.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]string{"url": "https://epay.example.test", "pid": "1001", "key": "payment-secret", "type": "alipay"}
	ciphertext, err := payment.SealConfig(cipherBox, store.PaymentProviderEPay, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreatePayment(ctx, store.SavePaymentInput{
		Provider: store.PaymentProviderEPay, Name: "EPay", ConfigCiphertext: ciphertext, Enabled: true,
	}, time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := initializeSettingsCipher(ctx, database, nil); err == nil {
		t.Fatal("initializeSettingsCipher() accepted a missing key for payment credentials")
	}
	if _, err := initializeSettingsCipher(ctx, database, bytes.Repeat([]byte{0x24}, 32)); err == nil {
		t.Fatal("initializeSettingsCipher() accepted the wrong key for payment credentials")
	}
	if initialized, err := initializeSettingsCipher(ctx, database, key); err != nil || initialized == nil {
		t.Fatalf("initializeSettingsCipher() rejected the payment key: cipher=%v err=%v", initialized, err)
	}
}

func saveSiteSettingsForMainTest(settings store.SiteSettings) store.SaveSiteSettingsInput {
	return store.SaveSiteSettingsInput{
		AppName: settings.AppName, AppDescription: settings.AppDescription, AppURL: settings.AppURL, TOSURL: settings.TOSURL, Logo: settings.Logo,
		StopRegister: settings.StopRegister, EmailVerificationEnabled: settings.EmailVerificationEnabled,
		EmailWhitelistEnabled: settings.EmailWhitelistEnabled, EmailWhitelistSuffixes: settings.EmailWhitelistSuffixes, GmailAliasLimitEnabled: settings.GmailAliasLimitEnabled,
		RegistrationIPLimitEnabled: settings.RegistrationIPLimitEnabled, RegistrationIPLimitCount: settings.RegistrationIPLimitCount, RegistrationIPLimitMinutes: settings.RegistrationIPLimitMinutes,
		PasswordLimitEnabled: settings.PasswordLimitEnabled, PasswordLimitCount: settings.PasswordLimitCount, PasswordLimitMinutes: settings.PasswordLimitMinutes,
		InvitationForceEnabled: settings.InvitationForceEnabled, InvitationCodeLimit: settings.InvitationCodeLimit, InvitationNeverExpire: settings.InvitationNeverExpire,
		MailLoginEnabled: settings.MailLoginEnabled, CaptchaEnabled: settings.CaptchaEnabled, CaptchaType: settings.CaptchaType,
		RecaptchaSiteKey: settings.RecaptchaSiteKey, RecaptchaV3SiteKey: settings.RecaptchaV3SiteKey,
		RecaptchaV3ScoreThreshold: settings.RecaptchaV3ScoreThreshold, TurnstileSiteKey: settings.TurnstileSiteKey,
	}
}

func TestRunHealthcheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	t.Setenv("XBOARD_HEALTH_URL", server.URL)
	if err := runHealthcheck(); err != nil {
		t.Fatalf("runHealthcheck() error = %v", err)
	}
}
