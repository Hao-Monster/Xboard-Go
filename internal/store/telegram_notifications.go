package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

type telegramNotificationKind string

const (
	telegramNotificationTicket  telegramNotificationKind = "ticket"
	telegramNotificationPayment telegramNotificationKind = "payment"
)

var telegramLegacyLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func enqueueTelegramTicketNotificationTx(
	ctx context.Context,
	tx *sql.Tx,
	userID, ticketID, messageID int64,
	subject, message string,
	now time.Time,
) error {
	enabled, err := telegramNotificationEnabledTx(ctx, tx, telegramNotificationTicket)
	if err != nil || !enabled {
		return err
	}
	var email string
	var transferEnable, upload, download, balance, commissionBalance int64
	var expiredAt sql.NullInt64
	var planName sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT u.email,u.transfer_enable,u.traffic_u,u.traffic_d,u.expired_at,
		       u.balance,u.commission_balance,p.name
		FROM users u LEFT JOIN plans p ON p.id=u.plan_id
		WHERE u.id=? AND u.account_kind='human'
	`, userID).Scan(&email, &transferEnable, &upload, &download, &expiredAt, &balance, &commissionBalance, &planName); err != nil {
		return fmt.Errorf("read Telegram ticket notification subject: %w", err)
	}
	if transferEnable < 0 || upload < 0 || download < 0 || balance < 0 || commissionBalance < 0 ||
		upload > math.MaxInt64-download {
		return fmt.Errorf("invalid stored Telegram ticket notification values")
	}
	used := upload + download
	remaining := transferEnable - used

	var text strings.Builder
	fmt.Fprintf(&text, "📮 工单提醒 #%d\n", ticketID)
	text.WriteString("━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(&text, "📧 邮箱: %s\n", email)
	text.WriteString("📍 位置: 未知\n")
	if planName.Valid {
		fmt.Fprintf(&text, "📦 套餐: %s\n", planName.String)
		fmt.Fprintf(&text, "📊 流量: %sG / %sG (剩余/总计)\n", formatTelegramGiB(remaining), formatTelegramGiB(transferEnable))
		fmt.Fprintf(&text, "⬆️⬇️ 已用: %sG / %sG\n", formatTelegramGiB(upload), formatTelegramGiB(download))
		if expiredAt.Valid {
			fmt.Fprintf(&text, "⏰ 到期: %s\n", time.Unix(expiredAt.Int64, 0).In(telegramLegacyLocation).Format("2006-01-02 15:04:05"))
		} else {
			text.WriteString("⏰ 到期: 长期有效\n")
		}
	} else {
		text.WriteString("📦 套餐: 未订购任何套餐\n")
	}
	fmt.Fprintf(&text, "💰 余额: %s元\n", formatTelegramCents(balance))
	fmt.Fprintf(&text, "💸 佣金: %s元\n", formatTelegramCents(commissionBalance))
	text.WriteString("━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(&text, "📝 主题: %s\n", subject)
	fmt.Fprintf(&text, "💬 内容: %s", message)

	bounded, err := boundTelegramNotificationText(text.String())
	if err != nil {
		return err
	}
	return enqueueTelegramAdminNotificationTx(ctx, tx, telegramNotificationTicket, messageID, bounded, now)
}

func enqueueTelegramPaymentNotificationTx(ctx context.Context, tx *sql.Tx, order Order, payment Payment, now time.Time) error {
	enabled, err := telegramNotificationEnabledTx(ctx, tx, telegramNotificationPayment)
	if err != nil || !enabled {
		return err
	}
	text := fmt.Sprintf(
		"💰成功收款%s元\n———————————————\n支付接口：%s\n支付渠道：%s\n本站订单：%s",
		formatTelegramCents(order.TotalAmount), payment.Provider, payment.Name, order.TradeNo,
	)
	bounded, err := boundTelegramNotificationText(text)
	if err != nil {
		return err
	}
	return enqueueTelegramAdminNotificationTx(ctx, tx, telegramNotificationPayment, order.ID, bounded, now)
}

func telegramNotificationEnabledTx(ctx context.Context, tx *sql.Tx, kind telegramNotificationKind) (bool, error) {
	configPath := "$.enable_ticket_notify"
	if kind == telegramNotificationPayment {
		configPath = "$.enable_payment_notify"
	} else if kind != telegramNotificationTicket {
		return false, ErrInvalidInput
	}
	var enabled bool
	if err := tx.QueryRowContext(ctx, `
		SELECT p.enabled=1
		   AND json_extract(p.config_json,?)=1
		   AND s.telegram_bot_enable=1
		   AND s.telegram_bot_token_cipher IS NOT NULL
		FROM trusted_plugins p CROSS JOIN app_settings s
		WHERE p.code='telegram' AND s.id=1
	`, configPath).Scan(&enabled); err != nil {
		return false, fmt.Errorf("read Telegram notification gate: %w", err)
	}
	return enabled, nil
}

func enqueueTelegramAdminNotificationTx(
	ctx context.Context,
	tx *sql.Tx,
	kind telegramNotificationKind,
	sourceID int64,
	text string,
	now time.Time,
) error {
	if (kind != telegramNotificationTicket && kind != telegramNotificationPayment) || sourceID < 1 || now.Unix() < 0 ||
		text == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > maxTelegramMessageRunes ||
		len(text) > maxTelegramInboundTextBytes || strings.IndexByte(text, 0) >= 0 {
		return ErrInvalidInput
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO telegram_message_outbox(
			source_kind,source_id,chat_id,text,available_at,created_at,updated_at,recipient_user_id
		)
		SELECT ?,?,telegram_id,?,?,?,?,id
		FROM users INDEXED BY idx_users_telegram_admin_notify
		WHERE telegram_id IS NOT NULL AND account_kind='human' AND (is_admin=1 OR is_staff=1)
		ON CONFLICT(source_kind,source_id,chat_id) DO NOTHING
	`, kind, sourceID, text, now.Unix(), now.Unix(), now.Unix()); err != nil {
		return fmt.Errorf("enqueue Telegram administrator notification: %w", err)
	}
	return nil
}

func boundTelegramNotificationText(value string) (string, error) {
	if value == "" || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return "", ErrInvalidInput
	}
	if utf8.RuneCountInString(value) <= maxTelegramMessageRunes && len(value) <= maxTelegramInboundTextBytes {
		return value, nil
	}
	var bounded strings.Builder
	bounded.Grow(min(len(value), maxTelegramInboundTextBytes))
	runes := 0
	for _, character := range value {
		width := utf8.RuneLen(character)
		if runes >= maxTelegramMessageRunes || bounded.Len()+width > maxTelegramInboundTextBytes {
			break
		}
		bounded.WriteRune(character)
		runes++
	}
	if bounded.Len() == 0 {
		return "", ErrInvalidInput
	}
	return bounded.String(), nil
}

func formatTelegramGiB(value int64) string {
	return fmt.Sprintf("%.2f", float64(value)/float64(1<<30))
}

func formatTelegramCents(value int64) string {
	whole, fraction := value/100, value%100
	if fraction == 0 {
		return fmt.Sprintf("%d", whole)
	}
	if fraction%10 == 0 {
		return fmt.Sprintf("%d.%d", whole, fraction/10)
	}
	return fmt.Sprintf("%d.%02d", whole, fraction)
}
