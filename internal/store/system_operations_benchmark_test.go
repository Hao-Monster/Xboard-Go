package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func BenchmarkListAdminAuditLogsTenThousand(b *testing.B) {
	database, err := OpenSQLite("file:benchmark-system-audit?mode=memory&cache=shared")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		b.Fatal(err)
	}
	if _, err := database.BootstrapAdmin(ctx, "benchmark-admin@example.test", "hash", time.Now()); err != nil {
		b.Fatal(err)
	}
	admin, err := database.FindUserByEmail(ctx, "benchmark-admin@example.test")
	if err != nil {
		b.Fatal(err)
	}
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO admin_audit_logs (administrator_id, administrator_email, method, route, status_code, created_at)
		VALUES (?, ?, ?, ?, 200, ?)
	`)
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 10_000; index++ {
		method := "PATCH"
		if index%4 == 0 {
			method = "PUT"
		}
		if _, err := statement.ExecContext(ctx, admin.ID, admin.Email, method, fmt.Sprintf("/api/v1/admin/users/{userID}/benchmark-%05d", index), index); err != nil {
			b.Fatal(err)
		}
	}
	if err := statement.Close(); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}

	for _, benchmark := range []struct {
		name   string
		filter AdminAuditFilter
	}{
		{name: "Recent", filter: AdminAuditFilter{Page: 1, PageSize: 20}},
		{name: "Method", filter: AdminAuditFilter{Page: 1, PageSize: 20, Method: "PUT"}},
		{name: "Substring", filter: AdminAuditFilter{Page: 1, PageSize: 20, Query: "benchmark-09999"}},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			for index := 0; index < b.N; index++ {
				if _, err := database.ListAdminAuditLogs(ctx, benchmark.filter); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkSystemMailQueueTenThousand(b *testing.B) {
	database, err := OpenSQLite("file:benchmark-system-mail-queue?mode=memory&cache=shared")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		b.Fatal(err)
	}
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	if _, err := database.BootstrapAdmin(ctx, "benchmark-queue@example.test", "hash", now); err != nil {
		b.Fatal(err)
	}
	user, err := database.FindUserByEmail(ctx, "benchmark-queue@example.test")
	if err != nil {
		b.Fatal(err)
	}
	ticket, err := database.CreateTicket(ctx, user.ID, SaveTicketInput{
		Subject: "Benchmark queue", Level: TicketLevelMedium, Message: "initial",
	}, now)
	if err != nil {
		b.Fatal(err)
	}

	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	messageStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO ticket_messages (ticket_id, user_id, message, created_at, updated_at)
		VALUES (?, ?, 'benchmark', ?, ?)
	`)
	if err != nil {
		b.Fatal(err)
	}
	outboxStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO ticket_mail_outbox (
			ticket_message_id, attempt_count, available_at, claim_token, claimed_at,
			sent_at, failed_at, last_error, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 10_000; index++ {
		result, insertErr := messageStatement.ExecContext(ctx, ticket.ID, user.ID, now.Unix(), now.Unix())
		if insertErr != nil {
			b.Fatal(insertErr)
		}
		messageID, insertErr := result.LastInsertId()
		if insertErr != nil {
			b.Fatal(insertErr)
		}
		var claimToken, claimedAt, sentAt, failedAt, lastError any
		switch index % 4 {
		case 0:
			sentAt = now.Unix()
		case 1:
			failedAt, lastError = now.Unix(), "benchmark delivery failure"
		case 2:
			claimToken, claimedAt = fmt.Sprintf("claim-%d", index), now.Unix()
		}
		if _, insertErr := outboxStatement.ExecContext(
			ctx, messageID, 0, now.Unix(), claimToken, claimedAt,
			sentAt, failedAt, lastError, now.Unix(), now.Unix(),
		); insertErr != nil {
			b.Fatal(insertErr)
		}
	}
	if err := messageStatement.Close(); err != nil {
		b.Fatal(err)
	}
	if err := outboxStatement.Close(); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}

	b.Run("Stats", func(b *testing.B) {
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			if _, err := database.GetSystemQueueStats(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("FailedFirstPage", func(b *testing.B) {
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			if _, err := database.ListTicketMailFailures(ctx, 1, 20); err != nil {
				b.Fatal(err)
			}
		}
	})
}
