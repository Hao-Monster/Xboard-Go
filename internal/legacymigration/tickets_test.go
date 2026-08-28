package legacymigration

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestReadTicketsSnapshotStreamsMessagesAndDerivesMissingLastReply(t *testing.T) {
	path := createLegacyTicketsSnapshot(t)
	snapshot, err := ReadTicketsSnapshot(t.Context(), path)
	if err != nil {
		t.Fatalf("ReadTicketsSnapshot() error = %v", err)
	}
	if snapshot.Path != path || snapshot.Size < 512 || len(snapshot.SHA256) != 64 ||
		len(snapshot.Tickets) != 2 || snapshot.MessageRows != 3 || len(snapshot.TicketChecksum) != 64 || len(snapshot.MessageChecksum) != 64 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.Tickets[1].LastReplyUserID != 2 || snapshot.Tickets[1].ReplyStatus != store.TicketReplyWaiting {
		t.Fatalf("derived ticket = %#v", snapshot.Tickets[1])
	}

	session, err := snapshot.OpenMessageStream(t.Context())
	if err != nil {
		t.Fatalf("OpenMessageStream() error = %v", err)
	}
	defer session.Close()
	messages := make([]store.LegacyTicketMessage, 0, 3)
	if err := session.Stream(t.Context(), func(message store.LegacyTicketMessage) error {
		messages = append(messages, message)
		return nil
	}); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if len(messages) != 3 || messages[0].ID != 361 || messages[2].ID != 363 ||
		store.LegacyTicketMessagesChecksum(messages) != snapshot.MessageChecksum {
		t.Fatalf("streamed messages = %#v", messages)
	}
	if err := session.VerifyAndClose(t.Context()); err != nil {
		t.Fatalf("VerifyAndClose() error = %v", err)
	}
}

func TestReadTicketsSnapshotRejectsAmbiguousOrInvalidState(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		statement string
		contains  string
	}{
		{name: "orphan message", statement: `PRAGMA foreign_keys=OFF; UPDATE v2_ticket_message SET ticket_id=999 WHERE id=363`, contains: "missing ticket"},
		{name: "last author mismatch", statement: `UPDATE v2_ticket SET last_reply_user_id=1 WHERE id=160`, contains: "last reply"},
		{name: "reply mismatch", statement: `UPDATE v2_ticket SET reply_status=1 WHERE id=160`, contains: "reply status"},
		{name: "invalid level", statement: `UPDATE v2_ticket SET level=9 WHERE id=159`, contains: "invalid legacy ticket"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path := createLegacyTicketsSnapshot(t)
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(scenario.statement); err != nil {
				_ = database.Close()
				t.Fatal(err)
			}
			_ = database.Close()
			if _, err := ReadTicketsSnapshot(context.Background(), path); err == nil ||
				!strings.Contains(strings.ToLower(err.Error()), strings.ToLower(scenario.contains)) {
				t.Fatalf("ReadTicketsSnapshot() error = %v, want %q", err, scenario.contains)
			}
		})
	}
}

func TestTicketMessageStreamDetectsSourceMutationBeforeCommit(t *testing.T) {
	path := createLegacyTicketsSnapshot(t)
	snapshot, err := ReadTicketsSnapshot(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	session, err := snapshot.OpenMessageStream(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.Stream(t.Context(), func(store.LegacyTicketMessage) error { return nil }); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE v2_ticket_message SET message='changed after validation' WHERE id=363`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.VerifyAndClose(t.Context()); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("VerifyAndClose(changed source) error = %v", err)
	}
}

func createLegacyTicketsSnapshot(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-tickets.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_ticket (
			id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL, subject TEXT NOT NULL, level INTEGER NOT NULL,
			status INTEGER NOT NULL, reply_status INTEGER NOT NULL, created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL, last_reply_user_id INTEGER
		);
		CREATE TABLE v2_ticket_message (
			id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL, ticket_id INTEGER NOT NULL,
			message TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
		);
		INSERT INTO v2_ticket VALUES (159,2,'Closed legacy ticket',2,1,1,100,120,1);
		INSERT INTO v2_ticket VALUES (160,2,'Open legacy ticket',0,0,0,200,200,NULL);
		INSERT INTO v2_ticket_message VALUES (361,2,159,'Owner question',100,100);
		INSERT INTO v2_ticket_message VALUES (362,1,159,'Administrator answer',120,120);
		INSERT INTO v2_ticket_message VALUES (363,2,160,'Waiting for support',200,200);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return absolute
}
