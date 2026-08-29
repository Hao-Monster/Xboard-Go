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
	user, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "reset-mail@example.test", PasswordHash: "hash", IsAdmin: true}, now)
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
	verifyTemplate, err := database.GetMailTemplate(ctx, "verify")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateMailTemplate(ctx, user.ID, "verify", verifyTemplate.Revision, store.SaveMailTemplateInput{
		Subject: "{{name}} - 自定义验证码", Content: "<p>自定义验证码：<strong>{{code}}</strong></p><p>{{url}}</p>",
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
	worker := NewWorker(database, nil, protector, nil, nil, sender, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worked, err := worker.RunOnce(ctx, now)
	if !worked || err == nil {
		t.Fatalf("first RunOnce() = (%v, %v), want retryable failure", worked, err)
	}
	sender.failure = nil
	worked, err = worker.RunOnce(ctx, now.Add(20*time.Second))
	if !worked || err != nil {
		t.Fatalf("second RunOnce() = (%v, %v), want successful retry", worked, err)
	}
	if len(sender.messages) != 2 || sender.messages[1].To != user.Email || sender.messages[1].Subject != "Reset Board - 自定义验证码" ||
		!strings.Contains(sender.messages[1].HTML, "自定义验证码") || !strings.Contains(sender.messages[1].Text, code) ||
		!strings.Contains(sender.messages[1].Text, "https://panel.example.test") {
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
		PasswordLimitEnabled:       siteSettings.PasswordLimitEnabled,
		PasswordLimitCount:         siteSettings.PasswordLimitCount,
		PasswordLimitMinutes:       siteSettings.PasswordLimitMinutes,
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
	worker := NewWorker(database, nil, nil, protector, nil, sender, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worked, err := worker.RunOnce(ctx, now)
	if !worked || err != nil {
		t.Fatalf("RunOnce() = (%v, %v)", worked, err)
	}
	if len(sender.messages) != 1 || sender.messages[0].To != email || sender.messages[0].Subject != "Registration Board - 邮箱验证码" || sender.messages[0].HTML == "" ||
		!strings.Contains(sender.messages[0].Text, code) || !strings.Contains(sender.messages[0].Text, "5 分钟") {
		t.Fatalf("registration verification message = %#v", sender.messages)
	}
	if _, claimed, err := database.ClaimRegistrationEmailVerificationMail(ctx, "after-send", now.Add(time.Second), time.Minute); err != nil || claimed {
		t.Fatalf("completed registration mail remained claimable: claimed=%v err=%v", claimed, err)
	}
}

func TestWorkerDecryptsAndDeliversOneTimeLoginLink(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2026, 8, 25, 5, 0, 0, 0, time.UTC)
	database, err := store.OpenSQLite("file:" + filepath.Join(t.TempDir(), "login-link-worker.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "login-link-worker@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	ticketSettings, _ := database.GetTicketSettings(ctx)
	if _, err := database.UpdateTicketSettings(ctx, user.ID, ticketSettings.Revision, store.SaveTicketSettingsInput{
		AppName: "Login Board", SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025,
		SMTPEncryption: EncryptionNone, SMTPFromAddress: "support@example.test",
	}, now); err != nil {
		t.Fatal(err)
	}
	siteSettings, _ := database.GetSiteSettings(ctx)
	if _, err := database.UpdateSiteSettings(ctx, user.ID, siteSettings.Revision, store.SaveSiteSettingsInput{
		AppName: siteSettings.AppName, RegistrationIPLimitCount: siteSettings.RegistrationIPLimitCount,
		RegistrationIPLimitMinutes: siteSettings.RegistrationIPLimitMinutes, InvitationCodeLimit: siteSettings.InvitationCodeLimit,
		PasswordLimitEnabled: siteSettings.PasswordLimitEnabled, PasswordLimitCount: siteSettings.PasswordLimitCount,
		PasswordLimitMinutes:  siteSettings.PasswordLimitMinutes,
		InvitationNeverExpire: siteSettings.InvitationNeverExpire, MailLoginEnabled: true,
	}, now); err != nil {
		t.Fatal(err)
	}
	protector, err := security.NewLoginLinkProtector(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	token, _ := protector.NewToken()
	emailDigest, _ := protector.EmailDigest(user.Email)
	tokenDigest, _ := protector.TokenDigest(security.LoginLinkPurposeEmail, token)
	tokenCipher, _ := protector.EncryptToken(user.ID, token)
	if queued, err := database.RequestMailLoginLink(ctx, store.MailLoginLinkRequestInput{
		Email: user.Email, ExpectedUserID: user.ID, EmailDigest: emailDigest, TokenDigest: tokenDigest, TokenCipher: tokenCipher,
		Redirect: "invite", LinkBaseURL: "https://panel.example.test",
	}, now); err != nil || !queued {
		t.Fatalf("RequestMailLoginLink() = (%v, %v)", queued, err)
	}

	sender := &recordingSender{failure: errors.New("temporary login link SMTP failure")}
	worker := NewWorker(database, nil, nil, nil, protector, sender, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worked, err := worker.RunOnce(ctx, now)
	if !worked || err == nil {
		t.Fatalf("first RunOnce() = (%v, %v), want retryable failure", worked, err)
	}
	sender.failure = nil
	worked, err = worker.RunOnce(ctx, now.Add(20*time.Second))
	if !worked || err != nil {
		t.Fatalf("second RunOnce() = (%v, %v)", worked, err)
	}
	expectedURL := "https://panel.example.test/#/login?verify=" + token + "&redirect=invite"
	message := sender.messages[len(sender.messages)-1]
	if len(sender.messages) != 2 || message.To != user.Email || message.Subject != "Login Board - 邮件登录" || message.HTML == "" ||
		!strings.Contains(message.Text, expectedURL) || !strings.Contains(message.Text, "5 分钟") ||
		!strings.Contains(message.Text, "只能使用一次") {
		t.Fatalf("login link message = %#v", sender.messages)
	}
	if _, claimed, err := database.ClaimLoginLinkMail(ctx, "after-send", now.Add(21*time.Second), time.Minute); err != nil || claimed {
		t.Fatalf("completed login link mail remained claimable: claimed=%v err=%v", claimed, err)
	}
}

func (sender *recordingSender) Send(_ context.Context, configuration SMTPConfig, message Message) error {
	sender.configurations = append(sender.configurations, configuration)
	sender.messages = append(sender.messages, message)
	return sender.failure
}

func TestWorkerRetriesAndDeliversBothSubscriptionReminderKinds(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2026, 8, 29, 3, 30, 0, 0, time.UTC)
	database, err := store.OpenSQLite("file:" + filepath.Join(t.TempDir(), "subscription-reminder-worker.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	administrator, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "reminder-worker-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	cipherBox, err := appsettings.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	passwordCipher, err := cipherBox.Encrypt([]byte("reminder-smtp-password"))
	if err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetMailSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateMailSettings(ctx, administrator.ID, settings.Revision, store.SaveMailSettingsInput{
		SMTPEnabled: true, SMTPHost: "smtp.example.test", SMTPPort: 587, SMTPUsername: "mailer",
		ReplaceSMTPPassword: true, SMTPPasswordCipher: passwordCipher, SMTPEncryption: EncryptionStartTLS,
		SMTPFromAddress: "support@example.test", RemindMailEnabled: true,
	}, now); err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(23 * time.Hour)
	recipient, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "both-reminders@example.test", PasswordHash: "hash", TransferEnable: 1_000, ExpiredAt: &expiresAt,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	upload, download := int64(400), int64(400)
	if _, _, err := database.UpdateAdminUser(ctx, recipient.ID, store.UpdateAdminUserInput{
		Revision: recipient.Revision, Email: recipient.Email, GroupID: recipient.GroupID,
		TransferEnable: recipient.TransferEnable, TrafficUpload: &upload, TrafficDownload: &download,
		ExpiredAt: recipient.ExpiredAt, SpeedLimit: recipient.SpeedLimit, DeviceLimit: recipient.DeviceLimit,
		Banned: recipient.Banned,
	}, now); err != nil {
		t.Fatal(err)
	}
	result, err := database.ScheduleSubscriptionReminders(ctx, now, "2026-08-29", 500)
	if err != nil || result.ExpireQueued != 1 || result.TrafficQueued != 1 {
		t.Fatalf("ScheduleSubscriptionReminders()=%#v err=%v", result, err)
	}

	sender := &recordingSender{failure: errors.New("temporary reminder SMTP failure")}
	worker := NewWorker(database, cipherBox, nil, nil, nil, sender, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worked, err := worker.RunOnce(ctx, now)
	if !worked || err == nil {
		t.Fatalf("first RunOnce()=(%v,%v), want retryable failure", worked, err)
	}
	sender.failure = nil
	for _, attemptAt := range []time.Time{now.Add(time.Minute), now.Add(time.Minute)} {
		worked, err = worker.RunOnce(ctx, attemptAt)
		if !worked || err != nil {
			t.Fatalf("successful RunOnce()=(%v,%v)", worked, err)
		}
	}
	worked, err = worker.RunOnce(ctx, now.Add(2*time.Minute))
	if worked || err != nil {
		t.Fatalf("completed RunOnce()=(%v,%v), want empty queue", worked, err)
	}
	if len(sender.messages) != 3 || len(sender.configurations) != 3 {
		t.Fatalf("send attempts=%d configurations=%d", len(sender.messages), len(sender.configurations))
	}
	if sender.configurations[2].Password != "reminder-smtp-password" || sender.configurations[2].Encryption != EncryptionStartTLS {
		t.Fatalf("decrypted reminder configuration=%#v", sender.configurations[2])
	}
	subjects := map[string]bool{}
	for _, message := range sender.messages[1:] {
		if message.To != recipient.Email {
			t.Fatalf("reminder recipient=%q want=%q", message.To, recipient.Email)
		}
		subjects[message.Subject] = true
	}
	if !subjects["Xboard-Go - 服务即将到期"] || !subjects["Xboard-Go - 流量使用提醒"] {
		t.Fatalf("reminder subjects=%#v", subjects)
	}
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
	worker := NewWorker(database, cipherBox, nil, nil, nil, sender, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	if message.To != "mail-user@example.test" || message.Subject != "Xboard - 站点通知" || message.HTML == "" ||
		!strings.Contains(message.Text, "主题：无法连接") || !strings.Contains(message.Text, "回复内容：请重新登录后再试") || !strings.Contains(message.Text, "https://panel.example.test") {
		t.Fatalf("notification message = %#v", message)
	}
}
