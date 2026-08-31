package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTelegramTicketNotificationsPreserveLegacyRecipientsAndUserTriggers(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	enableTelegramNotificationDelivery(t, database)

	owner, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-ticket-owner@example.test", PasswordHash: "hash",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-notify-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	staff, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-notify-staff@example.test", PasswordHash: "hash", IsStaff: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	both, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-notify-both@example.test", PasswordHash: "hash", IsAdmin: true, IsStaff: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	bannedAdministrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-notify-banned@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-notify-ordinary@example.test", PasswordHash: "hash",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE users SET
			telegram_id=CASE id
				WHEN ? THEN 9201 WHEN ? THEN 9202 WHEN ? THEN 9203 WHEN ? THEN 9204 WHEN ? THEN 9205
			END,
			banned=CASE WHEN id=? THEN 1 ELSE banned END
		WHERE id IN (?,?,?,?,?)
	`, administrator.ID, staff.ID, both.ID, bannedAdministrator.ID, ordinary.ID, bannedAdministrator.ID,
		administrator.ID, staff.ID, both.ID, bannedAdministrator.ID, ordinary.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE users SET balance=1234,commission_balance=567,transfer_enable=?,traffic_u=?,traffic_d=? WHERE id=?
	`, int64(10<<30), int64(2<<30), int64(1<<30), owner.ID); err != nil {
		t.Fatal(err)
	}

	ticket, err := database.CreateTicket(ctx, owner.ID, SaveTicketInput{
		Subject: "无法连接 _edge_", Level: TicketLevelHigh, Message: "首次问题 *请协助*",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	initialMessageID := ticketMessageID(t, database, ticket.ID, 0)
	assertTelegramNotificationRows(t, database, "ticket", initialMessageID, []int64{9201, 9202, 9203, 9204},
		"📮 工单提醒 #", "telegram-ticket-owner@example.test", "未订购任何套餐", "余额: 12.34元",
		"佣金: 5.67元", "主题: 无法连接 _edge_", "内容: 首次问题 *请协助*")

	if _, err := database.ReplyTicketAsUser(ctx, owner.ID, ticket.ID, "用户补充信息", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	replyMessageID := ticketMessageID(t, database, ticket.ID, 1)
	assertTelegramNotificationRows(t, database, "ticket", replyMessageID, []int64{9201, 9202, 9203, 9204},
		"工单提醒", "内容: 用户补充信息")

	if _, err := database.ReplyTicketAsAdmin(ctx, administrator.ID, ticket.ID, "管理员答复", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM telegram_message_outbox WHERE source_kind='ticket'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 8 {
		t.Fatalf("ticket notification rows after administrator reply = %d, want 8", rows)
	}
}

func TestTelegramTicketNotificationRollsBackWithTicket(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 12, 30, 0, 0, time.UTC)
	enableTelegramNotificationDelivery(t, database)
	owner, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-ticket-rollback-owner@example.test", PasswordHash: "hash",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-ticket-rollback-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET telegram_id=9301 WHERE id=?`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		CREATE TRIGGER force_ticket_notification_failure
		BEFORE INSERT ON telegram_message_outbox WHEN NEW.source_kind='ticket'
		BEGIN SELECT RAISE(ABORT,'forced ticket notification failure'); END
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateTicket(ctx, owner.ID, SaveTicketInput{
		Subject: "必须回滚", Level: TicketLevelMedium, Message: "业务与通知必须原子",
	}, now); err == nil {
		t.Fatal("CreateTicket() succeeded despite forced Telegram outbox failure")
	}
	for _, table := range []string{"tickets", "ticket_messages", "telegram_message_outbox"} {
		var rows int
		if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 0 {
			t.Fatalf("%s rows after rollback = %d, want 0", table, rows)
		}
	}
}

func TestTelegramTicketNotificationIncludesPlanTrafficAndExpiry(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 12, 45, 0, 0, time.UTC)
	enableTelegramNotificationDelivery(t, database)
	plan, ownerID := createOrderFixture(t, database, now, PlanPrices{"monthly": 500}, nil)
	expiresAt := now.Add(30 * 24 * time.Hour)
	if _, err := database.db.ExecContext(ctx, `
		UPDATE users SET plan_id=?,transfer_enable=?,traffic_u=?,traffic_d=?,expired_at=? WHERE id=?
	`, plan.ID, int64(10<<30), int64(2<<30), int64(1<<30), expiresAt.Unix(), ownerID); err != nil {
		t.Fatal(err)
	}
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-plan-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET telegram_id=9351 WHERE id=?`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	ticket, err := database.CreateTicket(ctx, ownerID, SaveTicketInput{
		Subject: "套餐详情", Level: TicketLevelLow, Message: "检查流量与到期时间",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	assertTelegramNotificationRows(t, database, "ticket", ticketMessageID(t, database, ticket.ID, 0), []int64{9351},
		"套餐: Order plan", "流量: 7.00G / 10.00G", "已用: 2.00G / 1.00G",
		"到期: "+expiresAt.In(telegramLegacyLocation).Format("2006-01-02 15:04:05"))
}

func TestTelegramPaymentNotificationIsFirstCompletionOnly(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 13, 0, 0, 0, time.UTC)
	enableTelegramNotificationDelivery(t, database)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-payment-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	staff, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-payment-staff@example.test", PasswordHash: "hash", IsStaff: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET telegram_id=CASE id WHEN ? THEN 9401 WHEN ? THEN 9402 END WHERE id IN (?,?)`,
		administrator.ID, staff.ID, administrator.ID, staff.ID); err != nil {
		t.Fatal(err)
	}
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 100_000}, nil)
	method, err := database.CreatePayment(ctx, SavePaymentInput{
		Provider: PaymentProviderCoinPayments, Name: "CoinPayments 主通道", ConfigCiphertext: []byte("ciphertext"),
		HandlingFeeFixed: 123, Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	started, err := database.StartPaymentCheckout(ctx, StartPaymentCheckoutInput{
		UserID: userID, TradeNo: order.TradeNo, PaymentID: method.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	payload := sha256.Sum256([]byte("verified Telegram payment webhook"))
	input := CompletePaymentWebhookInput{
		PaymentID: method.ID, Provider: method.Provider, ExternalID: "telegram-payment-one", TradeNo: order.TradeNo,
		Amount: started.Attempt.ExpectedAmount, Currency: "CNY", PayloadSHA256: fmt.Sprintf("%x", payload),
	}
	wrong := input
	wrong.Amount++
	if _, err := database.CompletePaymentWebhook(ctx, wrong, now.Add(time.Second)); !errors.Is(err, ErrPaymentMismatch) {
		t.Fatalf("wrong payment notification callback error = %v, want ErrPaymentMismatch", err)
	}
	var prematureRows int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM telegram_message_outbox WHERE source_kind='payment'`).Scan(&prematureRows); err != nil {
		t.Fatal(err)
	}
	if prematureRows != 0 {
		t.Fatalf("payment notifications before valid settlement = %d, want 0", prematureRows)
	}
	if _, err := database.CompletePaymentWebhook(ctx, input, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	assertTelegramNotificationRows(t, database, "payment", order.ID, []int64{9401, 9402},
		"💰成功收款1000元", "支付接口：CoinPayments", "支付渠道：CoinPayments 主通道", "本站订单："+order.TradeNo)

	if _, err := database.CompletePaymentWebhook(ctx, input, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	retry := input
	retry.ExternalID = "telegram-payment-two"
	retryPayload := sha256.Sum256([]byte("second valid receipt for completed order"))
	retry.PayloadSHA256 = fmt.Sprintf("%x", retryPayload)
	if _, err := database.CompletePaymentWebhook(ctx, retry, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM telegram_message_outbox WHERE source_kind='payment' AND source_id=?`, order.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("payment notification rows after retries = %d, want 2", rows)
	}
}

func TestFormatTelegramCentsPreservesLegacyDisplay(t *testing.T) {
	for _, test := range []struct {
		cents int64
		want  string
	}{
		{cents: 0, want: "0"},
		{cents: 1, want: "0.01"},
		{cents: 10, want: "0.1"},
		{cents: 101, want: "1.01"},
		{cents: 1_000, want: "10"},
	} {
		if got := formatTelegramCents(test.cents); got != test.want {
			t.Fatalf("formatTelegramCents(%d) = %q, want %q", test.cents, got, test.want)
		}
	}
}

func TestTelegramNotificationsRespectIndependentFeatureGates(t *testing.T) {
	for _, test := range []struct {
		name   string
		update string
	}{
		{name: "plugin disabled", update: `UPDATE trusted_plugins SET enabled=0 WHERE code='telegram'`},
		{name: "ticket notifications disabled", update: `UPDATE trusted_plugins SET config_json=json_set(config_json,'$.enable_ticket_notify',json('false')) WHERE code='telegram'`},
		{name: "bot disabled", update: `UPDATE app_settings SET telegram_bot_enable=0 WHERE id=1`},
		{name: "bot token absent", update: `UPDATE app_settings SET telegram_bot_token_cipher=NULL WHERE id=1`},
	} {
		t.Run(test.name, func(t *testing.T) {
			database := newTestStore(t)
			ctx := context.Background()
			now := time.Date(2026, 8, 31, 14, 0, 0, 0, time.UTC)
			enableTelegramNotificationDelivery(t, database)
			owner, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
				Email: "telegram-gate-owner-" + strings.ReplaceAll(test.name, " ", "-") + "@example.test", PasswordHash: "hash",
			}, now)
			if err != nil {
				t.Fatal(err)
			}
			administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
				Email: "telegram-gate-admin-" + strings.ReplaceAll(test.name, " ", "-") + "@example.test", PasswordHash: "hash", IsAdmin: true,
			}, now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.db.ExecContext(ctx, `UPDATE users SET telegram_id=9501 WHERE id=?`, administrator.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := database.db.ExecContext(ctx, test.update); err != nil {
				t.Fatal(err)
			}
			if _, err := database.CreateTicket(ctx, owner.ID, SaveTicketInput{
				Subject: "门禁不影响工单", Level: TicketLevelLow, Message: "只抑制 Telegram 通知",
			}, now); err != nil {
				t.Fatalf("CreateTicket() error = %v", err)
			}
			var notifications int
			if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM telegram_message_outbox`).Scan(&notifications); err != nil {
				t.Fatal(err)
			}
			if notifications != 0 {
				t.Fatalf("Telegram notifications = %d, want 0", notifications)
			}
		})
	}
}

func TestTelegramPaymentNotificationGateAndAtomicRollback(t *testing.T) {
	t.Run("payment notification disabled", func(t *testing.T) {
		database := newTestStore(t)
		ctx := context.Background()
		now := time.Date(2026, 8, 31, 15, 0, 0, 0, time.UTC)
		enableTelegramNotificationDelivery(t, database)
		administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
			Email: "telegram-payment-gate-admin@example.test", PasswordHash: "hash", IsAdmin: true,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(ctx, `UPDATE users SET telegram_id=9601 WHERE id=?`, administrator.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(ctx, `UPDATE trusted_plugins SET config_json=json_set(config_json,'$.enable_payment_notify',json('false')) WHERE code='telegram'`); err != nil {
			t.Fatal(err)
		}
		order, input, userID := createTelegramPaymentNotificationFixture(t, database, now)
		completed, err := database.CompletePaymentWebhook(ctx, input, now.Add(time.Second))
		if err != nil || completed.Status != OrderStatusCompleted {
			t.Fatalf("CompletePaymentWebhook() = (%#v, %v)", completed, err)
		}
		fresh, err := database.GetUserOrder(ctx, userID, order.TradeNo)
		if err != nil || fresh.Status != OrderStatusCompleted {
			t.Fatalf("completed order = (%#v, %v)", fresh, err)
		}
		var notifications int
		if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM telegram_message_outbox`).Scan(&notifications); err != nil {
			t.Fatal(err)
		}
		if notifications != 0 {
			t.Fatalf("Telegram notifications = %d, want 0", notifications)
		}
	})

	t.Run("outbox failure rolls back settlement", func(t *testing.T) {
		database := newTestStore(t)
		ctx := context.Background()
		now := time.Date(2026, 8, 31, 15, 30, 0, 0, time.UTC)
		enableTelegramNotificationDelivery(t, database)
		administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
			Email: "telegram-payment-rollback-admin@example.test", PasswordHash: "hash", IsAdmin: true,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(ctx, `UPDATE users SET telegram_id=9701 WHERE id=?`, administrator.ID); err != nil {
			t.Fatal(err)
		}
		order, input, userID := createTelegramPaymentNotificationFixture(t, database, now)
		if _, err := database.db.ExecContext(ctx, `
			CREATE TRIGGER force_payment_notification_failure
			BEFORE INSERT ON telegram_message_outbox WHEN NEW.source_kind='payment'
			BEGIN SELECT RAISE(ABORT,'forced payment notification failure'); END
		`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.CompletePaymentWebhook(ctx, input, now.Add(time.Second)); err == nil {
			t.Fatal("CompletePaymentWebhook() succeeded despite forced Telegram outbox failure")
		}
		fresh, err := database.GetUserOrder(ctx, userID, order.TradeNo)
		if err != nil || fresh.Status != OrderStatusPending {
			t.Fatalf("order after rollback = (%#v, %v), want pending", fresh, err)
		}
		for _, table := range []string{"payment_webhook_receipts", "order_entitlement_events", "telegram_message_outbox"} {
			var rows int
			if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&rows); err != nil {
				t.Fatal(err)
			}
			if rows != 0 {
				t.Fatalf("%s rows after rollback = %d, want 0", table, rows)
			}
		}
	})
}

func TestTelegramNotificationMessagesAreBoundedAndRecipientScanUsesIndex(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 16, 0, 0, 0, time.UTC)
	enableTelegramNotificationDelivery(t, database)
	owner, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-bounds-owner@example.test", PasswordHash: "hash",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-bounds-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET telegram_id=9801 WHERE id=?`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateTicket(ctx, owner.ID, SaveTicketInput{
		Subject: "有界通知", Level: TicketLevelLow, Message: strings.Repeat("界", 20_000),
	}, now); err != nil {
		t.Fatal(err)
	}
	var runes, bytes int
	if err := database.db.QueryRowContext(ctx, `
		SELECT length(text),length(CAST(text AS BLOB)) FROM telegram_message_outbox WHERE source_kind='ticket'
	`).Scan(&runes, &bytes); err != nil {
		t.Fatal(err)
	}
	if runes > maxTelegramMessageRunes || bytes > maxTelegramInboundTextBytes {
		t.Fatalf("bounded Telegram text = %d runes/%d bytes", runes, bytes)
	}
	assertQueryPlanContains(t, database, `
		EXPLAIN QUERY PLAN SELECT telegram_id FROM users INDEXED BY idx_users_telegram_admin_notify
		WHERE telegram_id IS NOT NULL AND account_kind='human' AND (is_admin=1 OR is_staff=1)
	`, "idx_users_telegram_admin_notify")
	assertQueryPlanContains(t, database, `
		EXPLAIN QUERY PLAN SELECT o.id FROM telegram_message_outbox o INDEXED BY idx_telegram_message_outbox_due
		WHERE o.source_kind IN ('ticket','payment')
		  AND o.sent_at IS NULL AND o.failed_at IS NULL AND o.cancelled_at IS NULL
		  AND o.available_at <= ?
		  AND (o.recipient_user_id IS NULL OR NOT EXISTS (
			SELECT 1 FROM users u WHERE u.id=o.recipient_user_id AND u.telegram_id=o.chat_id
			  AND u.account_kind='human' AND (u.is_admin=1 OR u.is_staff=1)
		  ))
		ORDER BY o.available_at,o.id LIMIT 100
	`, "idx_telegram_message_outbox_due", now.Unix())
}

func TestTelegramNotificationRecipientChangesCancelPendingDelivery(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(testing.TB, *Store, int64, int64)
	}{
		{
			name: "role revoked",
			mutate: func(t testing.TB, database *Store, recipientID, _ int64) {
				t.Helper()
				if _, err := database.db.Exec(`UPDATE users SET is_admin=0 WHERE id=?`, recipientID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "chat reassigned",
			mutate: func(t testing.TB, database *Store, recipientID, replacementID int64) {
				t.Helper()
				if _, err := database.db.Exec(`UPDATE users SET telegram_id=NULL WHERE id=?`, recipientID); err != nil {
					t.Fatal(err)
				}
				if _, err := database.db.Exec(`UPDATE users SET telegram_id=9901 WHERE id=?`, replacementID); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			database := newTestStore(t)
			ctx := context.Background()
			now := time.Date(2026, 8, 31, 16, 15, 0, 0, time.UTC)
			enableTelegramNotificationDelivery(t, database)
			owner, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
				Email: "telegram-recipient-owner-" + strings.ReplaceAll(test.name, " ", "-") + "@example.test", PasswordHash: "hash",
			}, now)
			if err != nil {
				t.Fatal(err)
			}
			recipient, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
				Email: "telegram-recipient-admin-" + strings.ReplaceAll(test.name, " ", "-") + "@example.test", PasswordHash: "hash", IsAdmin: true,
			}, now)
			if err != nil {
				t.Fatal(err)
			}
			replacement, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
				Email: "telegram-recipient-replacement-" + strings.ReplaceAll(test.name, " ", "-") + "@example.test", PasswordHash: "hash", IsAdmin: true,
			}, now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.db.ExecContext(ctx, `UPDATE users SET telegram_id=9901 WHERE id=?`, recipient.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := database.CreateTicket(ctx, owner.ID, SaveTicketInput{
				Subject: "收件人绑定快照", Level: TicketLevelLow, Message: "不得投递给变更后的身份",
			}, now); err != nil {
				t.Fatal(err)
			}
			test.mutate(t, database, recipient.ID, replacement.ID)
			if job, found, err := database.ClaimTelegramMessage(ctx, "recipient-change-claim", now.Add(time.Second), 30*time.Second); err != nil || found {
				t.Fatalf("ClaimTelegramMessage() = (%#v,%t,%v), want no delivery", job, found, err)
			}
			var storedRecipient int64
			var cancelled bool
			if err := database.db.QueryRowContext(ctx, `
				SELECT recipient_user_id,cancelled_at IS NOT NULL FROM telegram_message_outbox WHERE source_kind='ticket'
			`).Scan(&storedRecipient, &cancelled); err != nil {
				t.Fatal(err)
			}
			if storedRecipient != recipient.ID || !cancelled {
				t.Fatalf("stored recipient/cancelled = %d/%t, want %d/true", storedRecipient, cancelled, recipient.ID)
			}
		})
	}
}

func TestSchemaV57AddsAndValidatesTelegramNotificationRecipientIndex(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	if _, err := database.db.ExecContext(ctx, `
		DROP TRIGGER telegram_message_outbox_recipient_insert;
		DROP TRIGGER telegram_message_outbox_recipient_update;
		DROP INDEX idx_users_telegram_admin_notify;
		ALTER TABLE telegram_message_outbox DROP COLUMN recipient_user_id;
		PRAGMA user_version=56;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 58 {
		t.Fatalf("schema version = %d, want 58", version)
	}
	assertQueryPlanContains(t, database, `
		EXPLAIN QUERY PLAN SELECT telegram_id FROM users INDEXED BY idx_users_telegram_admin_notify
		WHERE telegram_id IS NOT NULL AND account_kind='human' AND (is_admin=1 OR is_staff=1)
	`, "idx_users_telegram_admin_notify")

	if _, err := database.db.ExecContext(ctx, `DROP INDEX idx_users_telegram_admin_notify`); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSchema(ctx, database.db, 57); err == nil || !strings.Contains(err.Error(), "Telegram administrator notification index") {
		t.Fatalf("ValidateSchema() error = %v, want Telegram notification index rejection", err)
	}
	if _, err := database.db.ExecContext(ctx, `
		CREATE INDEX idx_users_telegram_admin_notify ON users(telegram_id)
		WHERE telegram_id IS NOT NULL AND account_kind='human' AND (is_admin=1 OR is_staff=1);
		DROP TRIGGER telegram_message_outbox_recipient_insert;
	`); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSchema(ctx, database.db, 57); err == nil || !strings.Contains(err.Error(), "recipient trigger") {
		t.Fatalf("ValidateSchema() error = %v, want Telegram recipient trigger rejection", err)
	}
}

func enableTelegramNotificationDelivery(t testing.TB, database *Store) {
	t.Helper()
	if _, err := database.db.Exec(`
		UPDATE app_settings SET telegram_bot_enable=1,telegram_bot_token_cipher=? WHERE id=1
	`, []byte(strings.Repeat("x", 33))); err != nil {
		t.Fatal(err)
	}
}

func createTelegramPaymentNotificationFixture(t testing.TB, database *Store, now time.Time) (Order, CompletePaymentWebhookInput, int64) {
	t.Helper()
	ctx := context.Background()
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 100_000}, nil)
	method, err := database.CreatePayment(ctx, SavePaymentInput{
		Provider: PaymentProviderCoinPayments, Name: "CoinPayments", ConfigCiphertext: []byte("ciphertext"), Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	started, err := database.StartPaymentCheckout(ctx, StartPaymentCheckoutInput{
		UserID: userID, TradeNo: order.TradeNo, PaymentID: method.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	payload := sha256.Sum256([]byte("Telegram payment notification fixture"))
	return order, CompletePaymentWebhookInput{
		PaymentID: method.ID, Provider: method.Provider, ExternalID: "telegram-payment-fixture", TradeNo: order.TradeNo,
		Amount: started.Attempt.ExpectedAmount, Currency: "CNY", PayloadSHA256: fmt.Sprintf("%x", payload),
	}, userID
}

func ticketMessageID(t testing.TB, database *Store, ticketID int64, offset int) int64 {
	t.Helper()
	var id int64
	if err := database.db.QueryRow(`SELECT id FROM ticket_messages WHERE ticket_id=? ORDER BY id LIMIT 1 OFFSET ?`, ticketID, offset).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func assertTelegramNotificationRows(t testing.TB, database *Store, sourceKind string, sourceID int64, wantChatIDs []int64, contains ...string) {
	t.Helper()
	rows, err := database.db.Query(`
		SELECT chat_id,text FROM telegram_message_outbox
		WHERE source_kind=? AND source_id=? ORDER BY chat_id
	`, sourceKind, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	gotChatIDs := make([]int64, 0, len(wantChatIDs))
	for rows.Next() {
		var chatID int64
		var text string
		if err := rows.Scan(&chatID, &text); err != nil {
			t.Fatal(err)
		}
		gotChatIDs = append(gotChatIDs, chatID)
		for _, fragment := range contains {
			if !strings.Contains(text, fragment) {
				t.Fatalf("%s/%d chat %d text missing %q: %q", sourceKind, sourceID, chatID, fragment, text)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(gotChatIDs) != fmt.Sprint(wantChatIDs) {
		t.Fatalf("%s/%d chat IDs = %v, want %v", sourceKind, sourceID, gotChatIDs, wantChatIDs)
	}
}
