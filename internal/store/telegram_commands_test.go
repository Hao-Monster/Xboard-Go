package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTelegramCommandSchemaMigratesAndRejectsInvalidQueueRows(t *testing.T) {
	database := newTestStore(t)
	if CurrentSchemaVersion() != 59 {
		t.Fatalf("CurrentSchemaVersion()=%d, want 59", CurrentSchemaVersion())
	}
	for _, name := range []string{
		"telegram_message_outbox", "idx_telegram_message_outbox_due", "idx_telegram_message_outbox_failed",
		"idx_users_telegram_admin_notify", "telegram_message_outbox_recipient_insert", "telegram_message_outbox_recipient_update",
	} {
		var exists bool
		if err := database.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name=?)`, name).Scan(&exists); err != nil || !exists {
			t.Fatalf("schema object %s exists=%t err=%v", name, exists, err)
		}
	}
	if _, err := database.db.Exec(`
		INSERT INTO telegram_message_outbox(source_kind,source_id,chat_id,text,available_at,created_at,updated_at)
		VALUES('shell',1,1,'unsafe',1,1,1)
	`); err == nil {
		t.Fatal("telegram outbox accepted an unknown executable source kind")
	}
	if _, err := database.db.Exec(`
		INSERT INTO telegram_message_outbox(source_kind,source_id,chat_id,text,available_at,created_at,updated_at)
		VALUES('ticket',1,1,'missing recipient',1,1,1)
	`); err == nil {
		t.Fatal("Telegram notification outbox accepted a missing recipient identity")
	}
	if _, err := database.db.Exec(`
		INSERT INTO telegram_message_outbox(source_kind,source_id,chat_id,text,available_at,created_at,updated_at,recipient_user_id)
		VALUES('command',1,1,'unexpected recipient',1,1,1,1)
	`); err == nil {
		t.Fatal("Telegram command outbox accepted a notification recipient identity")
	}
}

func TestSchemaV56ReplacesUnversionedTelegramOutboxWithoutDeliveringForgedRows(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	if _, err := database.db.ExecContext(ctx, `
		DROP TABLE telegram_message_outbox;
		CREATE TABLE telegram_message_outbox(id INTEGER PRIMARY KEY,source_kind TEXT,source_id INTEGER,chat_id INTEGER,text TEXT);
		INSERT INTO telegram_message_outbox VALUES(1,'command',1,42,'forged');
		PRAGMA user_version=55;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v55 forged Telegram outbox) error=%v", err)
	}
	var version, rows int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM telegram_message_outbox`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if version != 59 || rows != 0 {
		t.Fatalf("migration version=%d queue rows=%d", version, rows)
	}
}

func TestTelegramCommandAndOutboxHotPathsUseBoundedIndexes(t *testing.T) {
	database := newTestStore(t)
	assertQueryPlanContains(t, database, `
		EXPLAIN QUERY PLAN SELECT id,telegram_id FROM users
		WHERE subscription_token=? AND account_kind='human' AND banned=0
	`, "idx_users_subscription_token", "11111111111111111111111111111111")
	assertQueryPlanContains(t, database, `
		EXPLAIN QUERY PLAN SELECT email FROM users
		WHERE telegram_id=? AND account_kind='human' LIMIT 1
	`, "idx_users_unique_telegram_id", 778899)
	assertQueryPlanContains(t, database, `
		EXPLAIN QUERY PLAN SELECT id FROM telegram_message_outbox
		WHERE sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
		  AND attempt_count < 3 AND available_at <= ?
		ORDER BY available_at,id LIMIT 1
	`, "idx_telegram_message_outbox_due", time.Now().Unix())
}

func TestTelegramBindTrafficUnbindCommandsAreAtomicAndIdempotent(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-command-user@example.test", PasswordHash: "hash", TransferEnable: 10 << 30,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	token, err := database.GetAdminUserSubscriptionToken(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	chatID := int64(778899)
	claimID := "11111111111111111111111111111111"
	if state, err := database.ClaimTelegramWebhookUpdate(ctx, 1001, claimID, now); err != nil || state != TelegramWebhookClaimAcquired {
		t.Fatalf("claim bind update=(%d,%v)", state, err)
	}
	if err := database.ProcessTelegramMessageUpdate(ctx, TelegramMessageUpdateInput{
		UpdateID: 1001, ClaimID: claimID, ChatID: chatID, ChatType: "private",
		Text: "/bind https://panel.example.test/s/" + token, PanelURL: "https://panel.example.test",
	}, now); err != nil {
		t.Fatalf("ProcessTelegramMessageUpdate(bind) error=%v", err)
	}
	updated, err := database.GetAdminUser(ctx, user.ID)
	if err != nil || updated.TelegramID == nil || *updated.TelegramID != chatID {
		t.Fatalf("bound user=%#v err=%v", updated, err)
	}
	assertTelegramOutboxText(t, database, 1001, chatID, "绑定成功")
	if state, err := database.ClaimTelegramWebhookUpdate(ctx, 1001, "22222222222222222222222222222222", now.Add(time.Second)); err != nil || state != TelegramWebhookClaimCompleted {
		t.Fatalf("duplicate bind claim=(%d,%v)", state, err)
	}
	var bindRows int
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM telegram_message_outbox WHERE source_kind='command' AND source_id=1001`).Scan(&bindRows); err != nil || bindRows != 1 {
		t.Fatalf("bind outbox rows=%d err=%v", bindRows, err)
	}

	if _, err := database.db.Exec(`UPDATE users SET traffic_u=?,traffic_d=? WHERE id=?`, int64(2<<30), int64(1<<30), user.ID); err != nil {
		t.Fatal(err)
	}
	claimID = "33333333333333333333333333333333"
	if _, err := database.ClaimTelegramWebhookUpdate(ctx, 1002, claimID, now); err != nil {
		t.Fatal(err)
	}
	if err := database.ProcessTelegramMessageUpdate(ctx, TelegramMessageUpdateInput{
		UpdateID: 1002, ClaimID: claimID, ChatID: chatID, ChatType: "private", Text: "/traffic", PanelURL: "https://panel.example.test",
	}, now); err != nil {
		t.Fatal(err)
	}
	assertTelegramOutboxText(t, database, 1002, chatID, "已用流量：3.00G", "总流量：10.00G", "剩余流量：7.00G", "使用率：30.00%")

	claimID = "44444444444444444444444444444444"
	if _, err := database.ClaimTelegramWebhookUpdate(ctx, 1003, claimID, now); err != nil {
		t.Fatal(err)
	}
	if err := database.ProcessTelegramMessageUpdate(ctx, TelegramMessageUpdateInput{
		UpdateID: 1003, ClaimID: claimID, ChatID: chatID, ChatType: "private", Text: "/unbind", PanelURL: "https://panel.example.test",
	}, now); err != nil {
		t.Fatal(err)
	}
	updated, err = database.GetAdminUser(ctx, user.ID)
	if err != nil || updated.TelegramID != nil {
		t.Fatalf("unbound user=%#v err=%v", updated, err)
	}
	assertTelegramOutboxText(t, database, 1003, chatID, "解绑成功")
}

func TestTelegramMessageOutboxClaimsRetriesCompletesAndCancels(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 11, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-outbox-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE app_settings SET telegram_bot_enable=1,telegram_bot_token_cipher=? WHERE id=1`, []byte(strings.Repeat("x", 33))); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO telegram_message_outbox(source_kind,source_id,chat_id,text,available_at,created_at,updated_at)
		VALUES('command',2001,778899,'queued response',?,?,?)
	`, now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	job, claimed, err := database.ClaimTelegramMessage(ctx, "claim-one", now, 30*time.Second)
	if err != nil || !claimed || job.Attempt != 1 || job.ChatID != 778899 || job.Text != "queued response" || len(job.BotTokenCipher) != 33 {
		t.Fatalf("ClaimTelegramMessage()=(%#v,%t,%v)", job, claimed, err)
	}
	if _, claimed, err := database.ClaimTelegramMessage(ctx, "claim-two", now.Add(29*time.Second), 30*time.Second); err != nil || claimed {
		t.Fatalf("active lease claim=(%t,%v)", claimed, err)
	}
	if err := database.FailTelegramMessage(ctx, job.ID, "claim-one", "upstream details", now.Add(time.Second), now); err != nil {
		t.Fatal(err)
	}
	job, claimed, err = database.ClaimTelegramMessage(ctx, "claim-two", now.Add(time.Second), 30*time.Second)
	if err != nil || !claimed || job.Attempt != 2 {
		t.Fatalf("retry claim=(%#v,%t,%v)", job, claimed, err)
	}
	if err := database.CompleteTelegramMessage(ctx, job.ID, "claim-two", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := database.ClaimTelegramMessage(ctx, "claim-three", now.Add(time.Hour), 30*time.Second); err != nil || claimed {
		t.Fatalf("sent message was reclaimed=(%t,%v)", claimed, err)
	}

	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO telegram_message_outbox(source_kind,source_id,chat_id,text,available_at,created_at,updated_at)
		VALUES('command',2002,778899,'lease me',?,?,?)
	`, now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	leased, claimed, err := database.ClaimTelegramMessage(ctx, "lease-one", now.Add(3*time.Second), 30*time.Second)
	if err != nil || !claimed || leased.Attempt != 1 {
		t.Fatalf("initial lease=(%#v,%t,%v)", leased, claimed, err)
	}
	reclaimed, claimed, err := database.ClaimTelegramMessage(ctx, "lease-two", now.Add(33*time.Second), 30*time.Second)
	if err != nil || !claimed || reclaimed.ID != leased.ID || reclaimed.Attempt != 2 {
		t.Fatalf("stale lease reclaim=(%#v,%t,%v)", reclaimed, claimed, err)
	}
	if err := database.CompleteTelegramMessage(ctx, leased.ID, "lease-one", now.Add(34*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale lease completion error=%v, want conflict", err)
	}
	if err := database.CompleteTelegramMessage(ctx, reclaimed.ID, "lease-two", now.Add(34*time.Second)); err != nil {
		t.Fatal(err)
	}

	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO telegram_message_outbox(source_kind,source_id,chat_id,text,available_at,created_at,updated_at)
		VALUES('command',2003,778899,'cancel me',?,?,?)
	`, now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	telegram, err := database.GetTrustedPlugin(ctx, TrustedPluginTelegram)
	if err != nil {
		t.Fatal(err)
	}
	disabledPlugin, err := database.UpdateTrustedPlugin(ctx, administrator.ID, telegram.Code, telegram.Revision, SaveTrustedPluginInput{
		Enabled: false, Config: telegram.Config,
	}, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var cancelled bool
	if err := database.db.QueryRowContext(ctx, `SELECT cancelled_at IS NOT NULL FROM telegram_message_outbox WHERE source_id=2003`).Scan(&cancelled); err != nil || !cancelled {
		t.Fatalf("plugin-disable cancellation=(%t,%v)", cancelled, err)
	}
	if _, err := database.UpdateTrustedPlugin(ctx, administrator.ID, telegram.Code, disabledPlugin.Revision, SaveTrustedPluginInput{
		Enabled: true, Config: telegram.Config,
	}, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO telegram_message_outbox(source_kind,source_id,chat_id,text,available_at,created_at,updated_at)
		VALUES('command',2004,778899,'cancel for bot setting',?,?,?)
	`, now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetTelegramSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTelegramSettings(ctx, administrator.ID, settings.Revision, SaveTelegramSettingsInput{BotEnabled: false}, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT cancelled_at IS NOT NULL FROM telegram_message_outbox WHERE source_id=2004`).Scan(&cancelled); err != nil || !cancelled {
		t.Fatalf("bot-disable cancellation=(%t,%v)", cancelled, err)
	}
	stats, err := database.GetTelegramQueueStats(ctx)
	if err != nil || stats.Pending != 0 || stats.Claimed != 0 || stats.Sent != 2 || stats.Failed != 0 || stats.OldestPendingAt != nil {
		t.Fatalf("Telegram queue stats=%#v err=%v", stats, err)
	}
}

func TestTelegramTicketReplyRequiresBoundAdministratorAndCommitsAtomically(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 13, 0, 0, 0, time.UTC)
	owner, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-ticket-owner@example.test", PasswordHash: "hash",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := database.CreateTicket(ctx, owner.ID, SaveTicketInput{
		Subject: "Telegram ticket", Level: TicketLevelMedium, Message: "initial",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-ticket-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	staff, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-ticket-staff@example.test", PasswordHash: "hash", IsStaff: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET telegram_id=CASE id WHEN ? THEN 8001 WHEN ? THEN 8002 END WHERE id IN (?,?)`, administrator.ID, staff.ID, administrator.ID, staff.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET telegram_id=8003 WHERE id=?`, owner.ID); err != nil {
		t.Fatal(err)
	}
	replyTo := fmt.Sprintf("📮 工单提醒 #%d", ticket.ID)
	processTelegramTestUpdate(t, database, 3001, 8002, "staff reply", replyTo, now)
	assertTelegramOutboxText(t, database, 3001, 8002, "无权回复工单")
	processTelegramTestUpdate(t, database, 3004, 8003, "ordinary reply", replyTo, now)
	assertTelegramOutboxText(t, database, 3004, 8003, "无权回复工单")
	processTelegramTestUpdate(t, database, 3002, 8001, "administrator reply", replyTo, now.Add(time.Second))
	assertTelegramOutboxText(t, database, 3002, 8001, fmt.Sprintf("工单 #%d 回复成功", ticket.ID))
	updated, err := database.GetAdminTicket(ctx, ticket.ID)
	if err != nil || len(updated.Messages) != 2 || updated.Messages[1].UserID != administrator.ID || updated.Messages[1].Message != "administrator reply" {
		t.Fatalf("ticket after Telegram reply=%#v err=%v", updated, err)
	}
	processTelegramTestUpdate(t, database, 3003, 8999, "unbound reply", replyTo, now.Add(2*time.Second))
	assertTelegramOutboxText(t, database, 3003, 8999, "请先绑定账号")
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET banned=1 WHERE id=?`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	processTelegramTestUpdate(t, database, 3005, 8001, "banned reply", replyTo, now.Add(3*time.Second))
	assertTelegramOutboxText(t, database, 3005, 8001, "请先绑定账号")
	updated, err = database.GetAdminTicket(ctx, ticket.ID)
	if err != nil || len(updated.Messages) != 2 {
		t.Fatalf("rejected Telegram replies mutated ticket=%#v err=%v", updated, err)
	}
	var auditCount int
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM admin_audit_logs
		WHERE administrator_id=? AND method='POST' AND route=? AND status_code=200
	`, administrator.ID, fmt.Sprintf("/api/v1/admin/tickets/%d/messages", ticket.ID)).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("Telegram ticket reply audit count=%d err=%v", auditCount, err)
	}
}

func TestTelegramCommandResponsesMatchFixedPluginSemantics(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 14, 0, 0, 0, time.UTC)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-parity-user@example.test", PasswordHash: "hash", TransferEnable: 0,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	token, err := database.GetAdminUserSubscriptionToken(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE app_settings SET telegram_bot_username='xboard_test_bot' WHERE id=1`); err != nil {
		t.Fatal(err)
	}

	processTelegramTestUpdate(t, database, 4001, 8101, "/bind", "", now)
	assertTelegramOutboxText(t, database, 4001, 8101, "参数有误，请携带订阅地址发送")
	processTelegramTestUpdate(t, database, 4002, 8101, "/bind https://user:secret@panel.example.test/s/"+token, "", now)
	assertTelegramOutboxText(t, database, 4002, 8101, "订阅地址无效")
	processTelegramTestUpdate(t, database, 4003, 8101, "/bind https://panel.example.test/s/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "", now)
	assertTelegramOutboxText(t, database, 4003, 8101, "订阅地址无效")
	processTelegramTestUpdate(t, database, 4004, 8101, "/bind https://panel.example.test/s/11111111111111111111111111111111", "", now)
	assertTelegramOutboxText(t, database, 4004, 8101, "用户不存在")

	claimID := fmt.Sprintf("%032x", 4005)
	if state, err := database.ClaimTelegramWebhookUpdate(ctx, 4005, claimID, now); err != nil || state != TelegramWebhookClaimAcquired {
		t.Fatalf("group command claim=(%d,%v)", state, err)
	}
	if err := database.ProcessTelegramMessageUpdate(ctx, TelegramMessageUpdateInput{
		UpdateID: 4005, ClaimID: claimID, ChatID: -8101, ChatType: "group", Text: "/traffic", PanelURL: "https://panel.example.test",
	}, now); err != nil {
		t.Fatal(err)
	}
	assertTelegramOutboxText(t, database, 4005, -8101, "请在私聊中使用此命令")

	if _, err := database.db.ExecContext(ctx, `UPDATE users SET telegram_id=8101 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	processTelegramTestUpdate(t, database, 4006, 8101, "/start@xboard_test_bot", "", now)
	assertTelegramOutboxText(t, database, 4006, 8101, "您已绑定账号："+user.Email, "/traffic - 查看流量使用情况")
	processTelegramTestUpdate(t, database, 4007, 8101, "/traffic", "", now)
	assertTelegramOutboxText(t, database, 4007, 8101, "总流量：0.00G", "使用率：0.00%")
	processTelegramTestUpdate(t, database, 4008, 8101, "/getlatesturl", "", now)
	assertTelegramOutboxText(t, database, 4008, 8101, "🔗 您的订阅链接：", "https://panel.example.test/s/"+token)
	processTelegramTestUpdate(t, database, 4009, 8101, "/unknown", "", now)
	var help string
	if err := database.db.QueryRowContext(ctx, `SELECT text FROM telegram_message_outbox WHERE source_id=4009`).Scan(&help); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(help, `\n/traffic`) || strings.Contains(help, "\n/traffic") {
		t.Fatalf("unknown-command help newline parity=%q", help)
	}
	processTelegramTestUpdate(t, database, 4010, 8101, "/unbind@different_bot", "", now)
	var ignoredRows int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM telegram_message_outbox WHERE source_id=4010`).Scan(&ignoredRows); err != nil || ignoredRows != 0 {
		t.Fatalf("different-bot command outbox=%d err=%v", ignoredRows, err)
	}

	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-parity-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	plugin, err := database.GetTrustedPlugin(ctx, TrustedPluginTelegram)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTrustedPlugin(ctx, administrator.ID, plugin.Code, plugin.Revision, SaveTrustedPluginInput{
		Enabled: false, Config: plugin.Config,
	}, now); err != nil {
		t.Fatal(err)
	}
	processTelegramTestUpdate(t, database, 4011, 8101, "/unbind", "", now)
	var telegramID int64
	if err := database.db.QueryRowContext(ctx, `SELECT telegram_id FROM users WHERE id=?`, user.ID).Scan(&telegramID); err != nil || telegramID != 8101 {
		t.Fatalf("plugin-disabled command mutated binding=%d err=%v", telegramID, err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM telegram_message_outbox WHERE source_id=4011`).Scan(&ignoredRows); err != nil || ignoredRows != 0 {
		t.Fatalf("plugin-disabled command outbox=%d err=%v", ignoredRows, err)
	}
}

func processTelegramTestUpdate(t *testing.T, database *Store, updateID, chatID int64, text, replyText string, now time.Time) {
	t.Helper()
	claimID := fmt.Sprintf("%032x", updateID)
	if state, err := database.ClaimTelegramWebhookUpdate(t.Context(), updateID, claimID, now); err != nil || state != TelegramWebhookClaimAcquired {
		t.Fatalf("claim Telegram update %d=(%d,%v)", updateID, state, err)
	}
	if err := database.ProcessTelegramMessageUpdate(t.Context(), TelegramMessageUpdateInput{
		UpdateID: updateID, ClaimID: claimID, ChatID: chatID, ChatType: "private", Text: text, ReplyText: replyText,
		PanelURL: "https://panel.example.test",
	}, now); err != nil {
		t.Fatalf("process Telegram update %d: %v", updateID, err)
	}
}

func assertTelegramOutboxText(t *testing.T, database *Store, sourceID, chatID int64, contains ...string) {
	t.Helper()
	var text string
	if err := database.db.QueryRow(`
		SELECT text FROM telegram_message_outbox WHERE source_kind='command' AND source_id=? AND chat_id=?
	`, sourceID, chatID).Scan(&text); err != nil {
		t.Fatalf("read Telegram outbox text: %v", err)
	}
	for _, expected := range contains {
		if !strings.Contains(text, expected) {
			t.Fatalf("Telegram outbox text=%q, missing %q", text, expected)
		}
	}
}
