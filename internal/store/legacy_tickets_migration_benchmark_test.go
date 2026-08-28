package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func BenchmarkImportLegacyTickets100kWithOneMillionMessages(b *testing.B) {
	const ticketCount = 100_000
	const messagesPerTicket = 10
	const messageCount = ticketCount * messagesPerTicket
	tickets := make([]LegacyTicket, ticketCount)
	for index := range tickets {
		id := int64(index + 1)
		tickets[index] = LegacyTicket{
			ID: id, UserID: id, Subject: "bounded migration benchmark", Level: TicketLevelMedium,
			Status: TicketStatusClosed, ReplyStatus: TicketReplyWaiting, LastReplyUserID: id,
			CreatedAt: id, UpdatedAt: id + messagesPerTicket - 1,
		}
	}
	ticketChecksum := LegacyTicketsChecksum(tickets)
	messageChecksum := NewLegacyTicketMessageChecksum()
	for index := 0; index < messageCount; index++ {
		messageChecksum.Add(benchmarkLegacyTicketMessage(index, messagesPerTicket))
	}
	sourceSHA := strings.Repeat("3", 64)
	b.ReportAllocs()
	b.SetBytes(messageCount * int64(len("bounded historical message")))
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		b.StopTimer()
		path := filepath.Join(b.TempDir(), "tickets.db")
		database, err := OpenSQLite("file:" + path)
		if err != nil {
			b.Fatal(err)
		}
		if err := database.Migrate(context.Background()); err != nil {
			_ = database.Close()
			b.Fatal(err)
		}
		populateLegacyTicketBenchmarkUsers(b, database, ticketCount, sourceSHA)
		input := LegacyTicketsImport{
			Slice: LegacyTicketsSlice, SourceSHA256: sourceSHA, SourceSize: 1,
			Tickets: tickets, TicketChecksum: ticketChecksum, MessageRows: messageCount, MessageChecksum: messageChecksum.Sum(),
			MessageStream: func(ctx context.Context, yield func(LegacyTicketMessage) error) error {
				for index := 0; index < messageCount; index++ {
					if index&8191 == 0 {
						if err := ctx.Err(); err != nil {
							return err
						}
					}
					if err := yield(benchmarkLegacyTicketMessage(index, messagesPerTicket)); err != nil {
						return err
					}
				}
				return nil
			},
			VerifySource:       func(context.Context) error { return nil },
			RollbackBackupPath: "/tmp/pre-benchmark-tickets.xbbackup", RollbackBackupSHA256: strings.Repeat("2", 64),
		}
		b.StartTimer()
		report, err := database.ImportLegacyTickets(context.Background(), input, time.Unix(2_000_000_000, 0))
		b.StopTimer()
		if closeErr := database.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			b.Fatal(err)
		}
		if report.Tickets.TargetRows != ticketCount || report.Messages.TargetRows != messageCount {
			b.Fatalf("report rows = %d/%d", report.Tickets.TargetRows, report.Messages.TargetRows)
		}
	}
}

func benchmarkLegacyTicketMessage(index, messagesPerTicket int) LegacyTicketMessage {
	ticketID := int64(index/messagesPerTicket + 1)
	id := int64(index + 1)
	return LegacyTicketMessage{
		ID: id, TicketID: ticketID, UserID: ticketID, Message: "bounded historical message",
		CreatedAt: ticketID + int64(index%messagesPerTicket), UpdatedAt: ticketID + int64(index%messagesPerTicket),
	}
}

func populateLegacyTicketBenchmarkUsers(b *testing.B, database *Store, count int, sourceSHA string) {
	b.Helper()
	tx, err := database.db.BeginTx(context.Background(), nil)
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	statement, err := tx.Prepare(`
		INSERT INTO users (id,email,password_hash,is_admin,banned,created_at,updated_at,subscription_token)
		VALUES (?,?,'hash',0,0,1,1,?)
	`)
	if err != nil {
		b.Fatal(err)
	}
	defer statement.Close()
	for index := 1; index <= count; index++ {
		if _, err := statement.Exec(index, fmt.Sprintf("ticket-%d@example.test", index), fmt.Sprintf("%032x", index)); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := tx.Exec(`
		INSERT INTO legacy_migration_runs
		(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at)
		VALUES (?, ?, 1, '/tmp/pre-users.xbbackup', ?, '{}', 1)
	`, LegacyHumanUsersSlice, sourceSHA, strings.Repeat("1", 64)); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
}
