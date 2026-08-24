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

	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type recordingSender struct {
	failure        error
	configurations []SMTPConfig
	messages       []Message
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
	worker := NewWorker(database, cipherBox, sender, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
