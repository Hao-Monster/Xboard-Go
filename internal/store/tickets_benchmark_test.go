package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func BenchmarkAdminTicketListTenThousand(b *testing.B) {
	database := newTicketBenchmarkStore(b)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "ticket-benchmark@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		b.Fatal(err)
	}
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO tickets (user_id, subject, level, status, reply_status, last_reply_user_id, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?, ?, ?)
	`)
	if err != nil {
		b.Fatal(err)
	}
	for index := range 10_000 {
		subject := fmt.Sprintf("Archived issue %05d", index)
		if index == 9_999 {
			subject = "Needle route outage"
		}
		if _, err := statement.ExecContext(ctx, user.ID, subject, index%3, index%2, user.ID, now.Unix()+int64(index), now.Unix()+int64(index)); err != nil {
			b.Fatal(err)
		}
	}
	if err := statement.Close(); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	status := TicketStatusClosed
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		page, err := database.ListAdminTickets(ctx, TicketFilter{Page: 1, PageSize: 20, Status: &status, Query: "Needle"})
		if err != nil || page.Total != 1 {
			b.Fatalf("ListAdminTickets() total=%d error=%v", page.Total, err)
		}
	}
}

func BenchmarkTicketDetailFiveHundredMessages(b *testing.B) {
	database := newTicketBenchmarkStore(b)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "ticket-detail@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		b.Fatal(err)
	}
	ticket, err := database.CreateTicket(ctx, user.ID, SaveTicketInput{Subject: "Long thread", Level: TicketLevelLow, Message: "initial"}, now)
	if err != nil {
		b.Fatal(err)
	}
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	for index := 1; index < 500; index++ {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ticket_messages (ticket_id, user_id, message, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, ticket.ID, user.ID, "message", now.Unix()+int64(index), now.Unix()+int64(index)); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		detail, err := database.GetAdminTicket(ctx, ticket.ID)
		if err != nil || len(detail.Messages) != 500 {
			b.Fatalf("GetAdminTicket() messages=%d error=%v", len(detail.Messages), err)
		}
	}
}

func newTicketBenchmarkStore(b *testing.B) *Store {
	b.Helper()
	database, err := OpenSQLite(fmt.Sprintf("file:ticket-benchmark-%s?mode=memory&cache=shared", b.Name()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(context.Background()); err != nil {
		b.Fatal(err)
	}
	return database
}
