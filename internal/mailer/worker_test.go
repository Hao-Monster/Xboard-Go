package mailer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type recordingSender struct {
	failure        error
	configurations []SMTPConfig
	messages       []Message
}

func TestWorkerDecryptsAndDeliversPasswordResetCodeBeforeTicketMail(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2026, 8, 25, 3, 0, 0, 0, time.UTC)
	database, err := store.OpenSQLite("file:" + filepath.Join(t.TempDir(), "password-reset-worker.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "reset-mail@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(ctx, user.ID, settings.Revision, store.SaveTicketSettingsInput{
		AppName: "Reset Board", AppURL: "https://panel.example.test", SMTPEnabled: true,
		SMTPHost: "mailpit", SMTPPort: 1025, SMTPEncryption: EncryptionNone, SMTPFromAddress: "support@example.test",
	}, now); err != nil {
		t.Fatal(err)
	}
	protector, err := security.NewPasswordResetProtector(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	const code = "384729"
	emailDigest, _ := protector.EmailDigest(user.Email)
	codeDigest, _ := protector.CodeDigest(user.Email, code)
	codeCipher, _ := protector.EncryptCode(user.Email, code)
	if queued, err := database.RequestPasswordReset(ctx, store.PasswordResetRequestInput{
		Email: user.Email, EmailDigest: emailDigest, CodeDigest: codeDigest, CodeCipher: codeCipher,
	}, now); err != nil || !queued {
		t.Fatalf("RequestPasswordReset() = (%v, %v)", queued, err)
	}

	sender := &recordingSender{failure: errors.New("temporary reset SMTP failure")}
	worker := NewWorker(database, nil, protector, nil, sender, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worked, err := worker.RunOnce(ctx, now)
	if !worked || err == nil {
		t.Fatalf("first RunOnce() = (%v, %v), want retryable failure", worked, err)
	}
	sender.failure = nil
	worked, err = worker.RunOnce(ctx, now.Add(20*time.Second))
	if !worked || err != nil {
		t.Fatalf("second RunOnce() = (%v, %v), want successful retry", worked, err)
	}
	if len(sender.messages) != 2 || sender.messages[1].To != user.Email || sender.messages[1].Subject != "Reset Board邮箱验证码" ||
		!strings.Contains(sender.messages[1].Text, code) || !strings.Contains(sender.messages[1].Text, "5 分钟") {
		t.Fatalf("password reset message = %#v", sender.messages)
	}
	if _, claimed, err := database.ClaimPasswordResetMail(ctx, "after-send", now.Add(21*time.Second), time.Minute); err != nil || claimed {
		t.Fatalf("completed password reset mail remained claimable: claimed=%v err=%v", claimed, err)
	}
}

func TestWorkerDecryptsAndDeliversRegistrationEmailCode(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	database, err := store.OpenSQLite("file:" + filepath.Join(t.TempDir(), "registration-email-worker.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	administrator, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "registration-mail-admin@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	ticketSettings, _ := database.GetTicketSettings(ctx)
	if _, err := database.UpdateTicketSettings(ctx, administrator.ID, ticketSettings.Revision, store.SaveTicketSettingsInput{
		AppName: "Registration Board", AppURL: "https://panel.example.test", SMTPEnabled: true,
		SMTPHost: "mailpit", SMTPPort: 1025, SMTPEncryption: EncryptionNone, SMTPFromAddress: "support@example.test",
	}, now); err != nil {
		t.Fatal(err)
	}
	siteSettings, _ := database.GetSiteSettings(ctx)
	if _, err := database.UpdateSiteSettings(ctx, administrator.ID, siteSettings.Revision, store.SaveSiteSettingsInput{
		AppName: siteSettings.AppName, AppDescription: siteSettings.AppDescription, AppURL: siteSettings.AppURL,
		TOSURL: siteSettings.TOSURL, Logo: siteSettings.Logo, EmailVerificationEnabled: true,
		EmailWhitelistSuffixes:     siteSettings.EmailWhitelistSuffixes,
		RegistrationIPLimitCount:   siteSettings.RegistrationIPLimitCount,
		RegistrationIPLimitMinutes: siteSettings.RegistrationIPLimitMinutes,
	}, now); err != nil {
		t.Fatal(err)
	}
	protector, err := security.NewRegistrationEmailProtector(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	const email = "registration-mail@example.test"
	const code = "592741"
	emailDigest, _ := protector.EmailDigest(email)
	codeDigest, _ := protector.CodeDigest(email, code)
	codeCipher, _ := protector.EncryptCode(email, code)
	if queued, err := database.RequestRegistrationEmailVerification(ctx, store.RegistrationEmailVerificationRequestInput{
		Email: email, SourceIP: "127.0.0.1", EmailDigest: emailDigest, CodeDigest: codeDigest, CodeCipher: codeCipher,
	}, now); err != nil || !queued {
		t.Fatalf("RequestRegistrationEmailVerification() = (%v, %v)", queued, err)
	}

	sender := &recordingSender{}
	worker := NewWorker(database, nil, nil, protector, sender, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worked, err := worker.RunOnce(ctx, now)
	if !worked || err != nil {
		t.Fatalf("RunOnce() = (%v, %v)", worked, err)
	}
	if len(sender.messages) != 1 || sender.messages[0].To != email || sender.messages[0].Subject != "Registration Board邮箱验证码" ||
		!strings.Contains(sender.messages[0].Text, code) || !strings.Contains(sender.messages[0].Text, "5 分钟") {
		t.Fatalf("registration verification message = %#v", sender.messages)
	}
	if _, claimed, err := database.ClaimRegistrationEmailVerificationMail(ctx, "after-send", now.Add(time.Second), time.Minute); err != nil || claimed {
		t.Fatalf("completed registration mail remained claimable: claimed=%v err=%v", claimed, err)
	}
}

func (sender *recordingSender) Send(_ context.Context, configuration SMTPConfig, message Message) error {
	sender.configurations = append(sender.configurations, configuration)
	sender.messages = append(sender.messages, message)
	return sender.failure
}

func TestWorkerDecryptsRetriesAndCompletesTicketNotification(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 13, 0, 0, 0, time.UTC)
	database, err := store.OpenSQLite("file:" + filepath.Join(t.TempDir(), "mail-worker.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "mail-user@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	administrator, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "mail-admin@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	cipherBox, err := appsettings.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	passwordCipher, err := cipherBox.Encrypt([]byte("smtp-password"))
	if err != nil {
		t.Fatal(err)
	}
	initial, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(ctx, administrator.ID, initial.Revision, store.SaveTicketSettingsInput{
		AppName: "Xboard", AppURL: "https://panel.example.test", SMTPEnabled: true,
		SMTPHost: "smtp.example.test", SMTPPort: 587, SMTPUsername: "mailer",
		SMTPEncryption: EncryptionStartTLS, SMTPFromAddress: "support@example.test",
		ReplaceSMTPPassword: true, SMTPPasswordCipher: passwordCipher,
	}, now); err != nil {
		t.Fatal(err)
	}
	ticket, err := database.CreateTicket(ctx, user.ID, store.SaveTicketInput{Subject: "无法连接", Level: store.TicketLevelHigh, Message: "初始问题"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ReplyTicketAsAdmin(ctx, administrator.ID, ticket.ID, "请重新登录后再试", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	sender := &recordingSender{failure: errors.New("temporary SMTP failure")}
	worker := NewWorker(database, cipherBox, nil, nil, sender, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worked, err := worker.RunOnce(ctx, now.Add(2*time.Minute))
	if !worked || err == nil {
		t.Fatalf("first RunOnce() = (%v, %v), want attempted failure", worked, err)
	}
	sender.failure = nil
	worked, err = worker.RunOnce(ctx, now.Add(4*time.Minute))
	if !worked || err != nil {
		t.Fatalf("second RunOnce() = (%v, %v), want successful retry", worked, err)
	}
	worked, err = worker.RunOnce(ctx, now.Add(5*time.Minute))
	if worked || err != nil {
		t.Fatalf("completed RunOnce() = (%v, %v), want empty queue", worked, err)
	}

	if len(sender.messages) != 2 || len(sender.configurations) != 2 {
		t.Fatalf("send attempts = %d, want 2", len(sender.messages))
	}
	configuration := sender.configurations[1]
	message := sender.messages[1]
	if configuration.Password != "smtp-password" || configuration.Encryption != EncryptionStartTLS || configuration.FromAddress != "support@example.test" {
		t.Fatalf("decrypted SMTP configuration = %#v", configuration)
	}
	if message.To != "mail-user@example.test" || message.Subject != "您在Xboard的工单得到了回复" ||
		!strings.Contains(message.Text, "主题：无法连接") || !strings.Contains(message.Text, "回复内容：请重新登录后再试") || !strings.Contains(message.Text, "https://panel.example.test") {
		t.Fatalf("notification message = %#v", message)
	}
}
