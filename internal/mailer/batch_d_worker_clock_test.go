package mailer

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type clockAdvancingSender struct {
	t            *testing.T
	rawDB        *sql.DB
	peerWorker   *Worker
	advanceClock func(time.Duration) time.Time
	currentClock func() time.Time
	claimedAt    []int64
	messages     []Message
	inPeer       bool
}

func (s *clockAdvancingSender) Send(ctx context.Context, _ SMTPConfig, message Message) error {
	s.messages = append(s.messages, message)
	if !s.inPeer {
		var claimed int64
		if err := s.rawDB.QueryRowContext(ctx, `
			SELECT claimed_at FROM ticket_mail_outbox
			WHERE recipient = ? AND claim_token IS NOT NULL
		`, message.To).Scan(&claimed); err != nil {
			s.t.Fatalf("read claimed_at for %s: %v", message.To, err)
		}
		s.claimedAt = append(s.claimedAt, claimed)

		// When the 3rd (final) message is in flight at t0+62s, a peer worker running at currentClock()
		// must NOT be able to steal the just-claimed message due to a stale batch-start claimed_at=t0.
		if len(s.claimedAt) == 3 {
			s.inPeer = true
			_, _ = s.peerWorker.RunOnce(ctx, s.currentClock())
			s.inPeer = false
		}

		s.advanceClock(31 * time.Second)
	}
	return nil
}

func TestBL21MailerBatchUsesAdvancingClockPerMessage(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 10, 16, 16, 0, 0, 0, time.UTC)
	dbPath := filepath.ToSlash(filepath.Join(t.TempDir(), "bl21-mail-worker.db"))
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

	admin, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "bl21-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, t0)
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
	if _, err := database.UpdateTicketSettings(ctx, admin.ID, initial.Revision, store.SaveTicketSettingsInput{
		AppName: "Xboard", AppURL: "https://panel.example.test", SMTPEnabled: true,
		SMTPHost: "smtp.example.test", SMTPPort: 587, SMTPUsername: "mailer",
		SMTPEncryption: EncryptionStartTLS, SMTPFromAddress: "support@example.test",
		ReplaceSMTPPassword: true, SMTPPasswordCipher: passwordCipher,
	}, t0); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 3; i++ {
		u, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
			Email: fmt.Sprintf("bl21-user-%d@example.test", i), PasswordHash: "hash",
		}, t0)
		if err != nil {
			t.Fatal(err)
		}
		tk, err := database.CreateTicket(ctx, u.ID, store.SaveTicketInput{
			Subject: fmt.Sprintf("工单-%d", i), Level: store.TicketLevelHigh, Message: "问题",
		}, t0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.ReplyTicketAsAdmin(ctx, admin.ID, tk.ID, "回复", t0); err != nil {
			t.Fatal(err)
		}
	}

	current := t0
	clockFn := func() time.Time { return current }
	advanceFn := func(d time.Duration) time.Time {
		current = current.Add(d)
		return current
	}

	discardLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sender := &clockAdvancingSender{
		t:            t,
		rawDB:        rawDB,
		advanceClock: advanceFn,
		currentClock: clockFn,
	}
	worker := NewWorker(database, cipherBox, nil, nil, nil, sender, time.Second, discardLogger)
	worker.now = clockFn
	peerWorker := NewWorker(database, cipherBox, nil, nil, nil, sender, time.Second, discardLogger)
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
	if len(sender.messages) != 3 {
		t.Fatalf("total messages sent = %d, want 3 (no duplicate sends across workers)", len(sender.messages))
	}

	rows, err := rawDB.QueryContext(ctx, `SELECT id, sent_at FROM ticket_mail_outbox ORDER BY id`)
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
