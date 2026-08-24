package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTicketLifecycleMatchesLegacyRoleAndStateRules(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	user := createTicketTestUser(t, database, "ticket-user@example.test", now)
	admin := createTicketTestUser(t, database, "ticket-admin@example.test", now)

	ticket, err := database.CreateTicket(ctx, user.ID, SaveTicketInput{
		Subject: " Cannot connect ", Level: TicketLevelHigh, Message: " Initial message ",
	}, now)
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if ticket.Subject != "Cannot connect" || ticket.Status != TicketStatusOpen || ticket.ReplyStatus != TicketReplyWaiting || ticket.LastReplyUserID != user.ID {
		t.Fatalf("created ticket = %#v", ticket)
	}
	if _, err := database.CreateTicket(ctx, user.ID, SaveTicketInput{Subject: "duplicate", Level: TicketLevelLow, Message: "duplicate"}, now); !errors.Is(err, ErrOpenTicketExists) {
		t.Fatalf("duplicate CreateTicket() error = %v, want ErrOpenTicketExists", err)
	}

	userReplied, err := database.ReplyTicketAsUser(ctx, user.ID, ticket.ID, "user follow-up", now.Add(time.Minute))
	if err != nil || userReplied.ReplyStatus != TicketReplyWaiting || userReplied.LastReplyUserID != user.ID {
		t.Fatalf("ReplyTicketAsUser() = (%#v, %v)", userReplied, err)
	}
	closed, err := database.CloseTicketAsUser(ctx, user.ID, ticket.ID, now.Add(2*time.Minute))
	if err != nil || closed.Status != TicketStatusClosed {
		t.Fatalf("CloseTicketAsUser() = (%#v, %v)", closed, err)
	}
	if _, err := database.ReplyTicketAsUser(ctx, user.ID, ticket.ID, "must fail", now.Add(3*time.Minute)); !errors.Is(err, ErrTicketClosed) {
		t.Fatalf("closed ReplyTicketAsUser() error = %v, want ErrTicketClosed", err)
	}

	answered, err := database.ReplyTicketAsAdmin(ctx, admin.ID, ticket.ID, "administrator answer", now.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("ReplyTicketAsAdmin(closed) error = %v", err)
	}
	if answered.Status != TicketStatusClosed || answered.ReplyStatus != TicketReplyAnswered || answered.LastReplyUserID != admin.ID {
		t.Fatalf("admin reply changed legacy state unexpectedly: %#v", answered)
	}
	detail, err := database.GetUserTicket(ctx, user.ID, ticket.ID)
	if err != nil || len(detail.Messages) != 3 {
		t.Fatalf("GetUserTicket() messages = %d, error = %v", len(detail.Messages), err)
	}
	if !detail.Messages[0].IsMe || !detail.Messages[1].IsMe || detail.Messages[2].IsMe {
		t.Fatalf("message ownership = %#v", detail.Messages)
	}

	if _, err := database.CreateTicket(ctx, user.ID, SaveTicketInput{Subject: "next", Level: TicketLevelLow, Message: "allowed"}, now.Add(5*time.Minute)); err != nil {
		t.Fatalf("CreateTicket(after close) error = %v", err)
	}
}

func TestTicketOwnershipFilteringAndAdminSearch(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	alice := createTicketTestUser(t, database, "alice-ticket@example.test", now)
	bob := createTicketTestUser(t, database, "bob-ticket@example.test", now)
	ticket, err := database.CreateTicket(ctx, alice.ID, SaveTicketInput{Subject: "Route outage", Level: TicketLevelMedium, Message: "help"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetUserTicket(ctx, bob.ID, ticket.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUserTicket(other user) error = %v, want ErrNotFound", err)
	}
	if _, err := database.ReplyTicketAsUser(ctx, bob.ID, ticket.ID, "IDOR", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReplyTicketAsUser(other user) error = %v, want ErrNotFound", err)
	}
	if _, err := database.CloseTicketAsUser(ctx, bob.ID, ticket.ID, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CloseTicketAsUser(other user) error = %v, want ErrNotFound", err)
	}

	status := TicketStatusOpen
	level := TicketLevelMedium
	for _, query := range []string{"Route", "ALICE-TICKET@EXAMPLE.TEST"} {
		page, err := database.ListAdminTickets(ctx, TicketFilter{Page: 1, PageSize: 20, Status: &status, Level: &level, Query: query})
		if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != ticket.ID || page.Items[0].UserEmail != alice.Email {
			t.Fatalf("ListAdminTickets(%q) = (%#v, %v)", query, page, err)
		}
	}
	userPage, err := database.ListUserTickets(ctx, alice.ID, 1, 20)
	if err != nil || userPage.Total != 1 || userPage.Items[0].UserEmail != "" {
		t.Fatalf("ListUserTickets() = (%#v, %v)", userPage, err)
	}
}

func TestOnlyOneOpenTicketCanBeCreatedConcurrently(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	user := createTicketTestUser(t, database, "ticket-race@example.test", now)

	var wait sync.WaitGroup
	errorsFound := make(chan error, 2)
	for index := range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := database.CreateTicket(ctx, user.ID, SaveTicketInput{Subject: string(rune('A' + index)), Level: TicketLevelLow, Message: "body"}, now)
			errorsFound <- err
		}()
	}
	wait.Wait()
	close(errorsFound)
	var success, conflicts int
	for err := range errorsFound {
		if err == nil {
			success++
		} else if errors.Is(err, ErrOpenTicketExists) {
			conflicts++
		} else {
			t.Fatalf("unexpected CreateTicket() error = %v", err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
}

func TestTicketValidationAndAutomaticClosureAreBounded(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	user := createTicketTestUser(t, database, "ticket-close@example.test", now)
	admin := createTicketTestUser(t, database, "ticket-closer@example.test", now)
	for _, input := range []SaveTicketInput{
		{Subject: "", Level: TicketLevelLow, Message: "message"},
		{Subject: "subject", Level: TicketLevel(3), Message: "message"},
		{Subject: "subject", Level: TicketLevelLow, Message: ""},
		{Subject: "bad\x00subject", Level: TicketLevelLow, Message: "message"},
		{Subject: "subject", Level: TicketLevelLow, Message: "bad\x00message"},
	} {
		if _, err := database.CreateTicket(ctx, user.ID, input, now); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("CreateTicket(%#v) error = %v, want ErrInvalidInput", input, err)
		}
	}
	ticket, _ := database.CreateTicket(ctx, user.ID, SaveTicketInput{Subject: "auto close", Level: TicketLevelLow, Message: "message"}, now)
	if _, err := database.ReplyTicketAsAdmin(ctx, admin.ID, ticket.ID, "answered", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if count, err := database.CloseStaleAnsweredTickets(ctx, now.Add(24*time.Hour), now.Add(25*time.Hour), 0); !errors.Is(err, ErrInvalidInput) || count != 0 {
		t.Fatalf("CloseStaleAnsweredTickets(limit=0) = (%d, %v)", count, err)
	}
	count, err := database.CloseStaleAnsweredTickets(ctx, now.Add(24*time.Hour), now.Add(25*time.Hour), 100)
	if err != nil || count != 1 {
		t.Fatalf("CloseStaleAnsweredTickets() = (%d, %v)", count, err)
	}
	detail, _ := database.GetAdminTicket(ctx, ticket.ID)
	if detail.Status != TicketStatusClosed {
		t.Fatalf("automatic close status = %d", detail.Status)
	}
}

func TestTicketThreadHasAnExplicitMessageBound(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	user := createTicketTestUser(t, database, "ticket-message-limit@example.test", now)
	ticket, err := database.CreateTicket(ctx, user.ID, SaveTicketInput{Subject: "bounded", Level: TicketLevelLow, Message: "initial"}, now)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index < maxTicketMessages; index++ {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ticket_messages (ticket_id, user_id, message, created_at, updated_at) VALUES (?, ?, 'message', ?, ?)`, ticket.ID, user.ID, now.Unix(), now.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ReplyTicketAsUser(ctx, user.ID, ticket.ID, "one too many", now); !errors.Is(err, ErrTicketMessageLimit) {
		t.Fatalf("ReplyTicketAsUser() error = %v, want ErrTicketMessageLimit", err)
	}
}

func TestTicketReadPathsUseBoundedOrderingIndexes(t *testing.T) {
	database := newTestStore(t)
	for name, query := range map[string]string{
		"user list":     `SELECT id FROM tickets WHERE user_id = 1 ORDER BY created_at DESC, id DESC LIMIT 20 OFFSET 0`,
		"admin status":  `SELECT id FROM tickets WHERE status = 0 ORDER BY updated_at DESC, id DESC LIMIT 20 OFFSET 0`,
		"stale replies": `SELECT id FROM tickets WHERE status = 0 AND reply_status = 1 AND updated_at <= 1 ORDER BY updated_at, id LIMIT 1000`,
		"messages":      `SELECT id FROM ticket_messages WHERE ticket_id = 1 ORDER BY id`,
	} {
		t.Run(name, func(t *testing.T) {
			rows, err := database.db.Query(`EXPLAIN QUERY PLAN ` + query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var plan strings.Builder
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plan.WriteString(detail)
			}
			if !strings.Contains(plan.String(), "idx_ticket") || strings.Contains(plan.String(), "USE TEMP B-TREE FOR ORDER BY") {
				t.Fatalf("ticket query does not use a covering ordering index: %s", plan.String())
			}
		})
	}
}

func TestMigrationFromV7AddsTicketAndSettingsTablesWithoutChangingExistingUsers(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "tickets-v7.db")
	database, err := OpenSQLite("file:" + filepath.ToSlash(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	for version, schema := range []string{schemaV1, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6, schemaV7} {
		if _, err := database.db.ExecContext(ctx, schema); err != nil {
			t.Fatalf("apply schema v%d: %v", version+1, err)
		}
	}
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO users (email, password_hash, subscription_token, created_at, updated_at)
		VALUES ('preserved-ticket-user@example.test', 'hash', ?, ?, ?)
	`, testSubscriptionToken(t), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, schemaV7Constraints); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `PRAGMA user_version = 7`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v7 to current) error = %v", err)
	}
	var version, userCount int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email = 'preserved-ticket-user@example.test'`).Scan(&userCount); err != nil {
		t.Fatal(err)
	}
	if version != 14 || userCount != 1 {
		t.Fatalf("schema version=%d preserved users=%d", version, userCount)
	}
	user, err := database.FindUserByEmail(ctx, "preserved-ticket-user@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateTicket(ctx, user.ID, SaveTicketInput{Subject: "migrated", Level: TicketLevelLow, Message: "works"}, now); err != nil {
		t.Fatalf("CreateTicket(after migration) error = %v", err)
	}
}

func createTicketTestUser(t *testing.T, database *Store, email string, now time.Time) AdminUser {
	t.Helper()
	user, err := database.CreateAdminUser(context.Background(), CreateAdminUserInput{Email: email, PasswordHash: "test-password-hash"}, now)
	if err != nil {
		t.Fatalf("CreateAdminUser(%q) error = %v", email, err)
	}
	return user
}
