package store

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
)

func TestSchemaV18MigrationPreservesV17DataAndAddsRegistrationVerification(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "schema-v17.db")
	database, err := OpenSQLite("file:" + filepath.ToSlash(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := t.Context()
	for step, schema := range []string{
		schemaV1, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6, schemaV7, schemaV7Constraints,
		schemaV8, schemaV9, schemaV10, schemaV11, schemaV12, schemaV13, schemaV14, schemaV15, schemaV16, schemaV17,
	} {
		if _, err := database.db.ExecContext(ctx, schema); err != nil {
			t.Fatalf("apply pre-v18 schema step %d: %v", step+1, err)
		}
	}
	if _, err := database.db.ExecContext(ctx, `PRAGMA user_version = 17`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, schemaV25); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "v17@example.test", PasswordHash: "preserved-hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `ALTER TABLE users DROP COLUMN last_login_at`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v17 to v18) error = %v", err)
	}
	var version, users, challenges, outbox int
	for query, target := range map[string]*int{
		`PRAGMA user_version`: &version,
		`SELECT COUNT(*) FROM users WHERE id = ` + fmt.Sprint(user.ID):                                    &users,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='registration_email_challenges'`:  &challenges,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='registration_email_mail_outbox'`: &outbox,
	} {
		if err := database.db.QueryRowContext(ctx, query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	settings, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion || users != 1 || challenges != 1 || outbox != 1 || settings.EmailVerificationEnabled {
		t.Fatalf("migration result version=%d users=%d tables=(%d,%d) email_verify=%t", version, users, challenges, outbox, settings.EmailVerificationEnabled)
	}
}

func TestRegistrationEmailVerificationIsPrivateLockedAndConsumedAtomically(t *testing.T) {
	database, protector, now := newRegistrationEmailStore(t)
	ctx := t.Context()

	existing := registrationEmailInput(t, protector, "registration-admin@example.test", "127.0.0.1", "123456")
	if queued, err := database.RequestRegistrationEmailVerification(ctx, existing, now); err != nil || queued {
		t.Fatalf("existing request = (%v, %v), want accepted without mail", queued, err)
	}
	var existingMail int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM registration_email_mail_outbox WHERE email_digest = ?`, existing.EmailDigest).Scan(&existingMail); err != nil || existingMail != 0 {
		t.Fatalf("existing mail count=%d err=%v", existingMail, err)
	}

	email := "new-registration@example.test"
	input := registrationEmailInput(t, protector, email, "127.0.0.1", "234567")
	if queued, err := database.RequestRegistrationEmailVerification(ctx, input, now); err != nil || !queued {
		t.Fatalf("available request = (%v, %v)", queued, err)
	}
	if _, err := database.RequestRegistrationEmailVerification(ctx, input, now.Add(time.Second)); !errors.Is(err, ErrRegistrationEmailVerificationLimited) {
		t.Fatalf("cooldown error = %v", err)
	}
	job, claimed, err := database.ClaimRegistrationEmailVerificationMail(ctx, "registration-test", now, time.Minute)
	if err != nil || !claimed || job.Recipient != email {
		t.Fatalf("claimed registration mail = (%#v, %v, %v)", job, claimed, err)
	}
	plaintext, err := protector.DecryptCode(email, job.CodeCipher)
	if err != nil || string(plaintext) != "234567" {
		t.Fatalf("decrypted registration code=%q err=%v", plaintext, err)
	}
	for index := range plaintext {
		plaintext[index] = 0
	}
	if err := database.CompleteRegistrationEmailVerificationMail(ctx, job.ID, "registration-test", now); err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 3; attempt++ {
		wrong, _ := protector.CodeDigest(email, fmt.Sprintf("%06d", attempt))
		if err := database.CheckRegistrationEmailVerification(ctx, input.EmailDigest, wrong, now.Add(time.Duration(attempt+1)*time.Second)); !errors.Is(err, ErrRegistrationEmailVerificationInvalid) {
			t.Fatalf("wrong challenge attempt %d error = %v", attempt+1, err)
		}
	}
	if err := database.CheckRegistrationEmailVerification(ctx, input.EmailDigest, input.CodeDigest, now.Add(4*time.Second)); !errors.Is(err, ErrRegistrationEmailVerificationLocked) {
		t.Fatalf("locked challenge error = %v", err)
	}

	replacementNow := now.Add(6 * time.Minute)
	replacement := registrationEmailInput(t, protector, email, "127.0.0.1", "345678")
	if queued, err := database.RequestRegistrationEmailVerification(ctx, replacement, replacementNow); err != nil || !queued {
		t.Fatalf("replacement request = (%v, %v)", queued, err)
	}
	if err := database.CheckRegistrationEmailVerification(ctx, replacement.EmailDigest, replacement.CodeDigest, replacementNow); err != nil {
		t.Fatalf("valid challenge check error = %v", err)
	}
	user, err := database.RegisterUserWithSession(ctx, RegisterUserInput{
		Email: email, PasswordHash: "new-hash", SourceIP: "127.0.0.1",
		EmailDigest: replacement.EmailDigest, EmailCodeDigest: replacement.CodeDigest,
	}, RegistrationSessionInput{
		TokenHash: strings.Repeat("a", 64), CSRFHash: strings.Repeat("b", 64), ExpiresAt: replacementNow.Add(time.Hour),
	}, replacementNow)
	if err != nil || user.Email != email {
		t.Fatalf("RegisterUserWithSession() = (%#v, %v)", user, err)
	}
	if err := database.CheckRegistrationEmailVerification(ctx, replacement.EmailDigest, replacement.CodeDigest, replacementNow); !errors.Is(err, ErrRegistrationEmailVerificationInvalid) {
		t.Fatalf("consumed challenge error = %v", err)
	}
	var sessions, challenges, liveCipher int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_sessions WHERE user_id = ?`, user.ID).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM registration_email_challenges WHERE email_digest = ?`, replacement.EmailDigest).Scan(&challenges); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM registration_email_mail_outbox WHERE email_digest = ? AND code_cipher IS NOT NULL`, replacement.EmailDigest).Scan(&liveCipher); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 || challenges != 0 || liveCipher != 0 {
		t.Fatalf("atomic registration state sessions=%d challenges=%d live_cipher=%d", sessions, challenges, liveCipher)
	}
}

func TestRegistrationEmailVerificationRequiresSMTPAndBlocksDisablingIt(t *testing.T) {
	database, protector, now := newRegistrationEmailStore(t)
	admin, err := database.FindUserByEmail(t.Context(), "registration-admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	ticketSettings, err := database.GetTicketSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.UpdateTicketSettings(t.Context(), admin.ID, ticketSettings.Revision, SaveTicketSettingsInput{
		AppName: "Registration Test", AppURL: "https://panel.example.test", SMTPEnabled: false,
		SMTPHost: "mailpit", SMTPPort: 1025, SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, now)
	if !errors.Is(err, ErrRegistrationEmailVerificationNeedsMail) {
		t.Fatalf("disable SMTP error = %v", err)
	}
	pending := registrationEmailInput(t, protector, "disable-registration@example.test", "127.0.0.1", "678901")
	if queued, err := database.RequestRegistrationEmailVerification(t.Context(), pending, now); err != nil || !queued {
		t.Fatalf("pending verification request = (%v, %v)", queued, err)
	}
	siteSettings, err := database.GetSiteSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateSiteSettings(t.Context(), admin.ID, siteSettings.Revision, SaveSiteSettingsInput{
		AppName: siteSettings.AppName, AppDescription: siteSettings.AppDescription, AppURL: siteSettings.AppURL,
		TOSURL: siteSettings.TOSURL, Logo: siteSettings.Logo, StopRegister: siteSettings.StopRegister,
		EmailVerificationEnabled: false, EmailWhitelistEnabled: siteSettings.EmailWhitelistEnabled,
		EmailWhitelistSuffixes: siteSettings.EmailWhitelistSuffixes, GmailAliasLimitEnabled: siteSettings.GmailAliasLimitEnabled,
		RegistrationIPLimitEnabled: siteSettings.RegistrationIPLimitEnabled,
		RegistrationIPLimitCount:   siteSettings.RegistrationIPLimitCount, RegistrationIPLimitMinutes: siteSettings.RegistrationIPLimitMinutes,
		PasswordLimitEnabled: siteSettings.PasswordLimitEnabled, PasswordLimitCount: siteSettings.PasswordLimitCount,
		PasswordLimitMinutes: siteSettings.PasswordLimitMinutes,
	}, now); err != nil {
		t.Fatal(err)
	}
	var challenges, liveCipher int
	if err := database.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM registration_email_challenges`).Scan(&challenges); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM registration_email_mail_outbox WHERE code_cipher IS NOT NULL`).Scan(&liveCipher); err != nil {
		t.Fatal(err)
	}
	if challenges != 0 || liveCipher != 0 {
		t.Fatalf("disabled verification retained challenges=%d live_cipher=%d", challenges, liveCipher)
	}
	ticketSettings, err = database.GetTicketSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(t.Context(), admin.ID, ticketSettings.Revision, SaveTicketSettingsInput{
		AppName: "Registration Test", AppURL: "https://panel.example.test", SMTPEnabled: false,
		SMTPHost: "mailpit", SMTPPort: 1025, SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, now); err != nil {
		t.Fatalf("disable SMTP after verification off: %v", err)
	}
}

func TestRegistrationEmailVerificationConcurrentRequestAndConsumptionAreSingleWinner(t *testing.T) {
	database, protector, now := newRegistrationEmailStore(t)
	ctx := t.Context()
	const email = "concurrent-registration@example.test"
	inputs := []RegistrationEmailVerificationRequestInput{
		registrationEmailInput(t, protector, email, "127.0.0.1", "456789"),
		registrationEmailInput(t, protector, email, "127.0.0.1", "567890"),
	}
	requestErrors := make(chan error, len(inputs))
	winners := make(chan RegistrationEmailVerificationRequestInput, 1)
	var group sync.WaitGroup
	for _, input := range inputs {
		input := input
		group.Add(1)
		go func() {
			defer group.Done()
			queued, err := database.RequestRegistrationEmailVerification(ctx, input, now)
			if err == nil && queued {
				winners <- input
			}
			requestErrors <- err
		}()
	}
	group.Wait()
	close(requestErrors)
	close(winners)
	var accepted, limited int
	for err := range requestErrors {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrRegistrationEmailVerificationLimited):
			limited++
		default:
			t.Fatalf("concurrent request error = %v", err)
		}
	}
	if accepted != 1 || limited != 1 {
		t.Fatalf("concurrent request results accepted=%d limited=%d", accepted, limited)
	}
	winner, ok := <-winners
	if !ok {
		t.Fatal("concurrent registration request had no winner")
	}
	var challenges, mail int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM registration_email_challenges`).Scan(&challenges); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM registration_email_mail_outbox WHERE cancelled_at IS NULL`).Scan(&mail); err != nil {
		t.Fatal(err)
	}
	if challenges != 1 || mail != 1 {
		t.Fatalf("concurrent request state challenges=%d mail=%d", challenges, mail)
	}

	registrationErrors := make(chan error, 2)
	for index := 0; index < 2; index++ {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := database.RegisterUserWithSession(ctx, RegisterUserInput{
				Email: email, PasswordHash: "hash", SourceIP: "127.0.0.1",
				EmailDigest: winner.EmailDigest, EmailCodeDigest: winner.CodeDigest,
			}, RegistrationSessionInput{
				TokenHash: strings.Repeat(string(rune('a'+index)), 64),
				CSRFHash:  strings.Repeat(string(rune('c'+index)), 64),
				ExpiresAt: now.Add(time.Hour),
			}, now)
			registrationErrors <- err
		}()
	}
	group.Wait()
	close(registrationErrors)
	var registered, rejected int
	for err := range registrationErrors {
		switch {
		case err == nil:
			registered++
		case errors.Is(err, ErrRegistrationEmailVerificationInvalid):
			rejected++
		default:
			t.Fatalf("concurrent registration error = %v", err)
		}
	}
	user, err := database.FindUserByEmail(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	var sessions int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_sessions WHERE user_id = ?`, user.ID).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if registered != 1 || rejected != 1 || sessions != 1 {
		t.Fatalf("concurrent consumption registered=%d rejected=%d sessions=%d", registered, rejected, sessions)
	}
}

func newRegistrationEmailStore(t testing.TB) (*Store, *security.RegistrationEmailProtector, time.Time) {
	t.Helper()
	database, err := OpenSQLite(fmt.Sprintf("file:registration-email-%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	if _, err := database.BootstrapAdmin(t.Context(), "registration-admin@example.test", "admin-hash", now); err != nil {
		t.Fatal(err)
	}
	admin, _ := database.FindUserByEmail(t.Context(), "registration-admin@example.test")
	ticketSettings, _ := database.GetTicketSettings(t.Context())
	if _, err := database.UpdateTicketSettings(t.Context(), admin.ID, ticketSettings.Revision, SaveTicketSettingsInput{
		AppName: "Registration Test", AppURL: "https://panel.example.test", SMTPEnabled: true,
		SMTPHost: "mailpit", SMTPPort: 1025, SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, now); err != nil {
		t.Fatal(err)
	}
	siteSettings, _ := database.GetSiteSettings(t.Context())
	if _, err := database.UpdateSiteSettings(t.Context(), admin.ID, siteSettings.Revision, SaveSiteSettingsInput{
		AppName: siteSettings.AppName, AppDescription: siteSettings.AppDescription, AppURL: siteSettings.AppURL,
		TOSURL: siteSettings.TOSURL, Logo: siteSettings.Logo, StopRegister: siteSettings.StopRegister,
		EmailVerificationEnabled: true, EmailWhitelistEnabled: siteSettings.EmailWhitelistEnabled,
		EmailWhitelistSuffixes: siteSettings.EmailWhitelistSuffixes, GmailAliasLimitEnabled: siteSettings.GmailAliasLimitEnabled,
		RegistrationIPLimitEnabled: siteSettings.RegistrationIPLimitEnabled,
		RegistrationIPLimitCount:   siteSettings.RegistrationIPLimitCount, RegistrationIPLimitMinutes: siteSettings.RegistrationIPLimitMinutes,
		PasswordLimitEnabled: siteSettings.PasswordLimitEnabled, PasswordLimitCount: siteSettings.PasswordLimitCount,
		PasswordLimitMinutes: siteSettings.PasswordLimitMinutes,
	}, now); err != nil {
		t.Fatal(err)
	}
	protector, err := security.NewRegistrationEmailProtector(bytes.Repeat([]byte{0x63}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return database, protector, now
}

func registrationEmailInput(t testing.TB, protector *security.RegistrationEmailProtector, email, sourceIP, code string) RegistrationEmailVerificationRequestInput {
	t.Helper()
	emailDigest, err := protector.EmailDigest(email)
	if err != nil {
		t.Fatal(err)
	}
	codeDigest, err := protector.CodeDigest(email, code)
	if err != nil {
		t.Fatal(err)
	}
	codeCipher, err := protector.EncryptCode(email, code)
	if err != nil {
		t.Fatal(err)
	}
	return RegistrationEmailVerificationRequestInput{
		Email: email, SourceIP: sourceIP, EmailDigest: emailDigest, CodeDigest: codeDigest, CodeCipher: codeCipher,
	}
}

func BenchmarkRegistrationEmailChallengeCheck(b *testing.B) {
	database, protector, now := newRegistrationEmailStore(b)
	input := registrationEmailInput(b, protector, "benchmark-registration@example.test", "127.0.0.1", "738291")
	if queued, err := database.RequestRegistrationEmailVerification(b.Context(), input, now); err != nil || !queued {
		b.Fatalf("RequestRegistrationEmailVerification() = (%v, %v)", queued, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := database.CheckRegistrationEmailVerification(b.Context(), input.EmailDigest, input.CodeDigest, now); err != nil {
			b.Fatal(err)
		}
	}
}
