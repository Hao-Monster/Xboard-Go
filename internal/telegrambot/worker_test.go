package telegrambot

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type recordingMessageSender struct {
	failure error
	token   string
	chatID  int64
	text    string
	calls   int
}

func (sender *recordingMessageSender) SendMessage(_ context.Context, token []byte, chatID int64, text string) error {
	sender.calls++
	sender.token = string(token)
	sender.chatID = chatID
	sender.text = text
	return sender.failure
}

func TestWorkerDeliversPersistentTelegramMessageAndBoundsRetries(t *testing.T) {
	database, cipherBox, now := telegramWorkerStore(t)
	sender := &recordingMessageSender{}
	worker := NewWorker(database, cipherBox, sender, time.Second, slog.Default())

	worked, err := worker.RunOnce(t.Context(), now)
	if err != nil || !worked || sender.calls != 1 || sender.token != testBotToken || sender.chatID != 778899 || sender.text != "请先绑定账号" {
		t.Fatalf("RunOnce(success)=(%t,%v) sender=%#v", worked, err, sender)
	}
	if worked, err := worker.RunOnce(t.Context(), now); err != nil || worked {
		t.Fatalf("sent queue RunOnce()=(%t,%v)", worked, err)
	}

	enqueueTelegramWorkerMessage(t, database, 9002, now.Add(time.Minute))
	sender.failure = errors.New("upstream body with secret")
	for attempt, offset := range []time.Duration{time.Minute, time.Minute + 10*time.Second, time.Minute + 40*time.Second} {
		worked, err := worker.RunOnce(t.Context(), now.Add(offset))
		if !worked || err == nil {
			t.Fatalf("RunOnce(failure %d)=(%t,%v)", attempt+1, worked, err)
		}
	}
	if worked, err := worker.RunOnce(t.Context(), now.Add(time.Hour)); err != nil || worked {
		t.Fatalf("terminal failed queue RunOnce()=(%t,%v)", worked, err)
	}
}

func telegramWorkerStore(t *testing.T) (*store.Store, *appsettings.Cipher, time.Time) {
	t.Helper()
	database, err := store.OpenSQLite(filepath.Join(t.TempDir(), "worker.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	cipherBox, err := appsettings.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	tokenCipher, err := cipherBox.EncryptFor(appsettings.TelegramBotTokenPurpose, []byte(testBotToken))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(t.Context(), store.CreateAdminUserInput{
		Email: "telegram-worker-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTelegramSettings(t.Context(), administrator.ID, 1, store.SaveTelegramSettingsInput{
		BotEnabled: true, ReplaceBotToken: true, BotTokenCipher: tokenCipher,
	}, now); err != nil {
		t.Fatal(err)
	}
	enqueueTelegramWorkerMessage(t, database, 9001, now)
	return database, cipherBox, now
}

func enqueueTelegramWorkerMessage(t *testing.T, database *store.Store, updateID int64, now time.Time) {
	t.Helper()
	claimID := "11111111111111111111111111111111"
	if state, err := database.ClaimTelegramWebhookUpdate(t.Context(), updateID, claimID, now); err != nil || state != store.TelegramWebhookClaimAcquired {
		t.Fatalf("ClaimTelegramWebhookUpdate()=(%d,%v)", state, err)
	}
	if err := database.ProcessTelegramMessageUpdate(t.Context(), store.TelegramMessageUpdateInput{
		UpdateID: updateID, ClaimID: claimID, ChatID: 778899, ChatType: "private", Text: "/traffic", PanelURL: "https://panel.example.test",
	}, now); err != nil {
		t.Fatal(err)
	}
}
