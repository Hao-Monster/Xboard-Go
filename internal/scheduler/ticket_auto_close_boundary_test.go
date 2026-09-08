package scheduler

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestWorkerTicketAutoCloseUsesStrictAdminReplyAgeAndIgnoresRecentUserReply(t *testing.T) {
	database, err := store.OpenSQLite(fmt.Sprintf("file:worker-ticket-boundaries-%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	admin, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "worker-ticket-boundary-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	type ticketCase struct {
		name         string
		adminReplyAt time.Time
		userReplyAt  *time.Time
		wantStatus   store.TicketStatus
		wantReply    store.TicketReplyStatus
	}
	recentUserReplyAt := now.Add(-23 * time.Hour)
	staleUserReplyAt := now.Add(-25 * time.Hour)
	cases := []ticketCase{
		{name: "one second before 24 hours", adminReplyAt: now.Add(-24*time.Hour + time.Second), wantStatus: store.TicketStatusOpen, wantReply: store.TicketReplyAnswered},
		{name: "exactly 24 hours", adminReplyAt: now.Add(-24 * time.Hour), wantStatus: store.TicketStatusClosed, wantReply: store.TicketReplyAnswered},
		{name: "one second beyond 24 hours", adminReplyAt: now.Add(-24*time.Hour - time.Second), wantStatus: store.TicketStatusClosed, wantReply: store.TicketReplyAnswered},
		{name: "recent user reply is not stale", adminReplyAt: now.Add(-25 * time.Hour), userReplyAt: &recentUserReplyAt, wantStatus: store.TicketStatusOpen, wantReply: store.TicketReplyWaiting},
		{name: "stale user reply is not auto closed", adminReplyAt: now.Add(-26 * time.Hour), userReplyAt: &staleUserReplyAt, wantStatus: store.TicketStatusOpen, wantReply: store.TicketReplyWaiting},
	}

	tickets := make([]struct {
		testCase ticketCase
		ticket   store.Ticket
		user     store.AdminUser
	}, 0, len(cases))
	for index, testCase := range cases {
		user, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
			Email:        fmt.Sprintf("worker-ticket-boundary-user-%d@example.test", index),
			PasswordHash: "hash",
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		ticket, err := database.CreateTicket(ctx, user.ID, store.SaveTicketInput{
			Subject: fmt.Sprintf("boundary-%d", index), Level: store.TicketLevelLow, Message: "initial",
		}, now.Add(-30*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.ReplyTicketAsAdmin(ctx, admin.ID, ticket.ID, "answer", testCase.adminReplyAt); err != nil {
			t.Fatal(err)
		}
		if testCase.userReplyAt != nil {
			if _, err := database.ReplyTicketAsUser(ctx, user.ID, ticket.ID, "recent user reply", *testCase.userReplyAt); err != nil {
				t.Fatal(err)
			}
		}
		tickets = append(tickets, struct {
			testCase ticketCase
			ticket   store.Ticket
			user     store.AdminUser
		}{testCase: testCase, ticket: ticket, user: user})
	}

	worker := NewWorker(database, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worker.now = func() time.Time { return now }
	worker.applyDue(ctx)

	for _, item := range tickets {
		t.Run(item.testCase.name, func(t *testing.T) {
			updated, err := database.GetAdminTicket(ctx, item.ticket.ID)
			if err != nil {
				t.Fatal(err)
			}
			if updated.Status != item.testCase.wantStatus || updated.ReplyStatus != item.testCase.wantReply {
				t.Fatalf("ticket status=%d reply_status=%d, want status=%d reply_status=%d",
					updated.Status, updated.ReplyStatus, item.testCase.wantStatus, item.testCase.wantReply)
			}
			if item.testCase.userReplyAt != nil && updated.LastReplyUserID != item.user.ID {
				t.Fatalf("last reply author changed to %d, want owner %d", updated.LastReplyUserID, item.user.ID)
			}
		})
	}
}
