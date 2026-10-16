package telegrambot

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type clockAdvancingTelegramSender struct {
	t            *testing.T
	rawDB        *sql.DB
	peerWorker   *Worker
	advanceClock func(time.Duration) time.Time
	currentClock func() time.Time
	claimedAt    []int64
	calls        int
	inPeer       bool
}

func (s *clockAdvancingTelegramSender) SendMessage(ctx context.Context, _ []byte, _ int64, _ string) error {
	s.calls++
	if !s.inPeer {
		var claimed int64
		if err := s.rawDB.QueryRowContext(ctx, `
			SELECT claimed_at FROM telegram_message_outbox
			WHERE claim_token IS NOT NULL
			ORDER BY id LIMIT 1
		`).Scan(&claimed); err != nil {
			s.t.Fatalf("read claimed_at: %v", err)
		}
		s.claimedAt = append(s.claimedAt, claimed)

		if len(s.claimedAt) == 3 {
			s.inPeer = true
			_, _ = s.peerWorker.RunOnce(ctx, s.currentClock())
			s.inPeer = false
		}

		s.advanceClock(31 * time.Second)
	}
	return nil
}

func TestBL21TelegramBatchUsesAdvancingClockPerMessage(t *testing.T) {
	ctx := t.Context()
	t0 := time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC)
	dbPath := filepath.ToSlash(filepath.Join(t.TempDir(), "bl21-telegram-worker.sqlite"))
	database, err := store.OpenSQLite("file:" + dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	rawDB, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	cipherBox, err := appsettings.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	tokenCipher, err := cipherBox.EncryptFor(appsettings.TelegramBotTokenPurpose, []byte(testBotToken))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "bl21-tg-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTelegramSettings(ctx, admin.ID, 1, store.SaveTelegramSettingsInput{
		BotEnabled: true, ReplaceBotToken: true, BotTokenCipher: tokenCipher,
	}, t0); err != nil {
		t.Fatal(err)
	}

	for i := int64(1); i <= 3; i++ {
		enqueueTelegramWorkerMessage(t, database, 9100+i, t0)
	}

	current := t0
	clockFn := func() time.Time { return current }
	advanceFn := func(d time.Duration) time.Time {
		current = current.Add(d)
		return current
	}

	discardLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sender := &clockAdvancingTelegramSender{
		t:            t,
		rawDB:        rawDB,
		advanceClock: advanceFn,
		currentClock: clockFn,
	}
	worker := NewWorker(database, cipherBox, sender, time.Second, discardLogger)
	worker.now = clockFn
	peerWorker := NewWorker(database, cipherBox, sender, time.Second, discardLogger)
	peerWorker.now = clockFn
	sender.peerWorker = peerWorker

	worker.runBatch(ctx)

	wantClaimed := []int64{
		t0.Unix(),
		t0.Add(31 * time.Second).Unix(),
		t0.Add(62 * time.Second).Unix(),
	}
	if len(sender.claimedAt) != 3 {
		t.Fatalf("claimedAt count = %d (%v), want 3", len(sender.claimedAt), sender.claimedAt)
	}
	for i, want := range wantClaimed {
		if sender.claimedAt[i] != want {
			t.Fatalf("item %d claimed_at = %d, want %d (all=%v)", i+1, sender.claimedAt[i], want, sender.claimedAt)
		}
	}
	if sender.calls != 3 {
		t.Fatalf("total messages sent = %d, want 3 (no duplicate sends across workers)", sender.calls)
	}

	rows, err := rawDB.QueryContext(ctx, `SELECT id, sent_at FROM telegram_message_outbox ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	wantSent := []int64{
		t0.Add(31 * time.Second).Unix(),
		t0.Add(62 * time.Second).Unix(),
		t0.Add(93 * time.Second).Unix(),
	}
	idx := 0
	for rows.Next() {
		var id, sentAt int64
		if err := rows.Scan(&id, &sentAt); err != nil {
			t.Fatal(err)
		}
		if sentAt != wantSent[idx] {
			t.Fatalf("row %d sent_at = %d, want %d", id, sentAt, wantSent[idx])
		}
		idx++
	}
}
