package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestTelegramTicketNotificationCarriesPerRequestLocation(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 20, 0, 0, 0, time.UTC)
	enableTelegramNotificationDelivery(t, database)
	owner, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "location-owner@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "location-admin@example.test", PasswordHash: "hash", IsAdmin: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET telegram_id=9601 WHERE id=?`, administrator.ID); err != nil {
		t.Fatal(err)
	}

	ticket, err := database.CreateTicket(ctx, owner.ID, SaveTicketInput{
		Subject: "位置对照", Level: TicketLevelLow, Message: "首次内容", NotificationLocation: "中国江苏省南京市",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	assertTelegramNotificationRows(t, database, "ticket", ticketMessageID(t, database, ticket.ID, 0), []int64{9601}, "位置: 中国江苏省南京市")

	if _, err := database.ReplyTicketAsUserWithNotificationLocation(ctx, owner.ID, ticket.ID, "补充内容", "美国【Level3】", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertTelegramNotificationRows(t, database, "ticket", ticketMessageID(t, database, ticket.ID, 1), []int64{9601}, "位置: 美国【Level3】")
}

func TestTelegramTicketNotificationRejectsUnsafeLocation(t *testing.T) {
	database := newTestStore(t)
	owner, err := database.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "invalid-location@example.test", PasswordHash: "hash"}, time.Unix(1_800_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, location := range []string{"unsafe\x00location", "spoofed\nfield", strings.Repeat("x", maxTelegramNotificationLocationBytes+1)} {
		if _, err := database.CreateTicket(t.Context(), owner.ID, SaveTicketInput{
			Subject: "invalid", Level: TicketLevelLow, Message: "invalid", NotificationLocation: location,
		}, time.Unix(1_800_000_001, 0)); err == nil {
			t.Fatalf("CreateTicket() accepted unsafe notification location of %d bytes", len(location))
		}
	}
}

func TestTelegramAdministratorReplyToOwnTicketDoesNotNotifyAdministrators(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 21, 0, 0, 0, time.UTC)
	enableTelegramNotificationDelivery(t, database)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "location-self-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET telegram_id=9602 WHERE id=?`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	ticket, err := database.CreateTicket(ctx, administrator.ID, SaveTicketInput{
		Subject: "管理员自己的工单", Level: TicketLevelLow, Message: "首次内容", NotificationLocation: "中国江苏省南京市",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ReplyTicketAsAdmin(ctx, administrator.ID, ticket.ID, "管理员回复", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	messageID := ticketMessageID(t, database, ticket.ID, 1)
	var notifications int
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM telegram_message_outbox WHERE source_kind='ticket' AND source_id=?
	`, messageID).Scan(&notifications); err != nil {
		t.Fatal(err)
	}
	if notifications != 0 {
		t.Fatalf("administrator reply to own ticket queued %d administrator notifications", notifications)
	}
}
