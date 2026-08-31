package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/publicurl"
)

const (
	maxTelegramInboundTextBytes = 16 << 10
	maxTelegramMessageRunes     = 4_096
)

var telegramTicketReplyPattern = regexp.MustCompile(`(📮.*?工单提醒.*?#?|工单ID: ?)(\d+)`)

type telegramPluginConfig struct {
	EnableTicketNotify  bool   `json:"enable_ticket_notify"`
	EnablePaymentNotify bool   `json:"enable_payment_notify"`
	StartWelcomeTitle   string `json:"start_welcome_title"`
	StartBotDescription string `json:"start_bot_description"`
	StartBindGuide      string `json:"start_bind_guide"`
	StartUnbindGuide    string `json:"start_unbind_guide"`
	StartBindCommands   string `json:"start_bind_commands"`
	StartFooter         string `json:"start_footer"`
	HelpText            string `json:"help_text"`
}

func (s *Store) ProcessTelegramMessageUpdate(ctx context.Context, input TelegramMessageUpdateInput, now time.Time) error {
	input.ChatType = strings.TrimSpace(input.ChatType)
	input.PanelURL = strings.TrimSpace(input.PanelURL)
	if input.UpdateID < 1 || !telegramProvisionIDPattern.MatchString(input.ClaimID) || input.ChatID == 0 ||
		!validTelegramCommandChatType(input.ChatType) || len(input.PanelURL) > maxTelegramURLBytes || now.Unix() < 0 {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Telegram message update: %w", err)
	}
	defer tx.Rollback()
	var completed bool
	if err := tx.QueryRowContext(ctx, `
		SELECT completed FROM telegram_webhook_updates WHERE update_id=? AND claim_id=?
	`, input.UpdateID, input.ClaimID).Scan(&completed); errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	} else if err != nil {
		return fmt.Errorf("read Telegram message update claim: %w", err)
	}
	if completed {
		return nil
	}

	var enabled bool
	var configJSON string
	if err := tx.QueryRowContext(ctx, `SELECT enabled,config_json FROM trusted_plugins WHERE code='telegram'`).Scan(&enabled, &configJSON); err != nil {
		return fmt.Errorf("read Telegram plugin: %w", err)
	}
	reply := ""
	if enabled {
		var config telegramPluginConfig
		if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
			return fmt.Errorf("decode Telegram plugin config: %w", err)
		}
		reply, err = processTelegramCommandTx(ctx, tx, input, config, now)
		if err != nil {
			return err
		}
	}
	if reply != "" {
		reply = truncateRunes(reply, maxTelegramMessageRunes)
		if !utf8.ValidString(reply) || len(reply) > maxTelegramInboundTextBytes || strings.IndexByte(reply, 0) >= 0 {
			return fmt.Errorf("invalid Telegram command response")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO telegram_message_outbox(
				source_kind,source_id,chat_id,text,available_at,created_at,updated_at
			) VALUES('command',?,?,?,?,?,?)
		`, input.UpdateID, input.ChatID, reply, now.Unix(), now.Unix(), now.Unix()); err != nil {
			return fmt.Errorf("enqueue Telegram command response: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE telegram_webhook_updates SET completed=1,updated_at=?
		WHERE update_id=? AND claim_id=? AND completed=0
	`, now.Unix(), input.UpdateID, input.ClaimID)
	if err != nil {
		return fmt.Errorf("complete Telegram message update: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Telegram message update: %w", err)
	}
	return nil
}

func validTelegramCommandChatType(value string) bool {
	switch value {
	case "private", "group", "supergroup", "channel":
		return true
	default:
		return false
	}
}

func processTelegramCommandTx(ctx context.Context, tx *sql.Tx, input TelegramMessageUpdateInput, config telegramPluginConfig, now time.Time) (string, error) {
	if !validTelegramInboundText(input.Text) {
		return "", nil
	}
	if input.ReplyText != "" {
		return telegramTicketReplyTx(ctx, tx, input.ChatID, input.Text, input.ReplyText, now)
	}
	fields := strings.Fields(input.Text)
	if len(fields) == 0 {
		return "", nil
	}
	command := fields[0]
	var botUsername string
	if separator := strings.IndexByte(command, '@'); separator >= 0 {
		if err := tx.QueryRowContext(ctx, `SELECT telegram_bot_username FROM app_settings WHERE id=1`).Scan(&botUsername); err != nil {
			return "", fmt.Errorf("read Telegram bot username: %w", err)
		}
		if botUsername == "" || !strings.EqualFold(command[separator+1:], botUsername) {
			return "", nil
		}
		command = command[:separator]
	}
	if command == "/start" {
		return telegramStartResponse(ctx, tx, input.ChatID, config)
	}
	knownPrivate := command == "/bind" || command == "/traffic" || command == "/getlatesturl" || command == "/unbind"
	if knownPrivate && input.ChatType != "private" {
		return "请在私聊中使用此命令", nil
	}
	switch command {
	case "/bind":
		if len(fields) < 2 {
			return "参数有误，请携带订阅地址发送", nil
		}
		token, ok := telegramSubscriptionToken(fields[1])
		if !ok {
			return "订阅地址无效", nil
		}
		return bindTelegramUserTx(ctx, tx, input.ChatID, token, now)
	case "/traffic":
		return telegramTrafficResponse(ctx, tx, input.ChatID)
	case "/getlatesturl":
		return telegramSubscriptionResponse(ctx, tx, input.ChatID, input.PanelURL)
	case "/unbind":
		return unbindTelegramUserTx(ctx, tx, input.ChatID, now)
	default:
		if input.ChatType == "private" {
			return config.HelpText, nil
		}
		return "", nil
	}
}

func telegramTicketReplyTx(ctx context.Context, tx *sql.Tx, chatID int64, message, replyText string, now time.Time) (string, error) {
	if !validTelegramInboundText(replyText) {
		return "", nil
	}
	matches := telegramTicketReplyPattern.FindStringSubmatch(replyText)
	if len(matches) != 3 {
		return "", nil
	}
	var administratorID int64
	var isAdministrator bool
	var administratorEmail string
	err := tx.QueryRowContext(ctx, `
		SELECT id,is_admin,email FROM users
		WHERE telegram_id=? AND account_kind='human' AND banned=0 LIMIT 1
	`, chatID).Scan(&administratorID, &isAdministrator, &administratorEmail)
	if errors.Is(err, sql.ErrNoRows) {
		return "请先绑定账号", nil
	}
	if err != nil {
		return "", fmt.Errorf("read Telegram ticket administrator: %w", err)
	}
	if !isAdministrator {
		return "无权回复工单", nil
	}
	ticketID, err := strconv.ParseInt(matches[2], 10, 64)
	if err != nil || ticketID < 1 {
		return "工单不存在", nil
	}
	message = strings.TrimSpace(message)
	if !validTicketMessage(message) {
		return "回复内容无效", nil
	}
	if _, err := replyTicketTx(ctx, tx, 0, ticketID, administratorID, false, message, now); err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			return "工单不存在", nil
		case errors.Is(err, ErrInvalidInput):
			return "回复内容无效", nil
		case errors.Is(err, ErrTicketMessageLimit):
			return "工单消息数量已达上限", nil
		default:
			return "", err
		}
	}
	if err := insertAdminAudit(ctx, tx, AdminAuditInput{
		AdministratorID: administratorID, AdministratorEmail: administratorEmail,
		Method: "POST", Route: fmt.Sprintf("/api/v1/admin/tickets/%d/messages", ticketID), StatusCode: 200,
	}, now); err != nil {
		return "", fmt.Errorf("record Telegram ticket reply audit: %w", err)
	}
	return fmt.Sprintf("工单 #%d 回复成功", ticketID), nil
}

func telegramStartResponse(ctx context.Context, tx *sql.Tx, chatID int64, config telegramPluginConfig) (string, error) {
	welcome := config.StartWelcomeTitle + "\n\n" + config.StartBotDescription + "\n\n"
	var email string
	err := tx.QueryRowContext(ctx, `
		SELECT email FROM users WHERE telegram_id=? AND account_kind='human' LIMIT 1
	`, chatID).Scan(&email)
	if err == nil {
		welcome += "✅ 您已绑定账号：" + email + "\n\n" + config.StartUnbindGuide
	} else if errors.Is(err, sql.ErrNoRows) {
		welcome += config.StartBindGuide + "\n\n" + config.StartBindCommands
	} else {
		return "", fmt.Errorf("read Telegram binding: %w", err)
	}
	welcome += "\n\n" + config.StartFooter
	return strings.ReplaceAll(welcome, `\n`, "\n"), nil
}

func bindTelegramUserTx(ctx context.Context, tx *sql.Tx, chatID int64, token string, now time.Time) (string, error) {
	var userID int64
	var bound sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		SELECT id,telegram_id FROM users
		WHERE subscription_token=? AND account_kind='human' AND banned=0
	`, token).Scan(&userID, &bound)
	if errors.Is(err, sql.ErrNoRows) {
		return "用户不存在", nil
	}
	if err != nil {
		return "", fmt.Errorf("read Telegram bind target: %w", err)
	}
	if bound.Valid {
		return "该账号已经绑定了Telegram账号", nil
	}
	var other bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM users WHERE telegram_id=? AND id<>? AND account_kind='human')
	`, chatID, userID).Scan(&other); err != nil {
		return "", fmt.Errorf("check Telegram binding uniqueness: %w", err)
	}
	if other {
		return "该Telegram账号已绑定其他账号", nil
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE users SET telegram_id=?,admin_revision=admin_revision+1,updated_at=?
		WHERE id=? AND telegram_id IS NULL AND banned=0 AND account_kind='human'
	`, chatID, now.Unix(), userID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return "该Telegram账号已绑定其他账号", nil
		}
		return "", fmt.Errorf("bind Telegram user: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return "设置失败", nil
	}
	return "绑定成功", nil
}

func telegramTrafficResponse(ctx context.Context, tx *sql.Tx, chatID int64) (string, error) {
	var upload, download, total int64
	err := tx.QueryRowContext(ctx, `
		SELECT traffic_u,traffic_d,transfer_enable FROM users
		WHERE telegram_id=? AND account_kind='human' LIMIT 1
	`, chatID).Scan(&upload, &download, &total)
	if errors.Is(err, sql.ErrNoRows) {
		return "请先绑定账号", nil
	}
	if err != nil {
		return "", fmt.Errorf("read Telegram traffic: %w", err)
	}
	if upload < 0 || download < 0 || total < 0 || upload > math.MaxInt64-download {
		return "", fmt.Errorf("invalid stored Telegram traffic")
	}
	used := upload + download
	remaining := total - used
	percentage := 0.0
	if total > 0 {
		percentage = float64(used) / float64(total) * 100
	}
	const gib = float64(1 << 30)
	return fmt.Sprintf(
		"📊 流量使用情况\n\n已用流量：%.2fG\n总流量：%.2fG\n剩余流量：%.2fG\n使用率：%.2f%%",
		float64(used)/gib, float64(total)/gib, float64(remaining)/gib, percentage,
	), nil
}

func telegramSubscriptionResponse(ctx context.Context, tx *sql.Tx, chatID int64, panelURL string) (string, error) {
	var token, pathValue, appURL, origins string
	var forceHTTPS bool
	err := tx.QueryRowContext(ctx, `
		SELECT u.subscription_token,ss.path,s.app_url,s.subscribe_url,s.force_https
		FROM users u CROSS JOIN subscription_settings ss CROSS JOIN app_settings s
		WHERE u.telegram_id=? AND u.account_kind='human' AND ss.id=1 AND s.id=1 LIMIT 1
	`, chatID).Scan(&token, &pathValue, &appURL, &origins, &forceHTTPS)
	if errors.Is(err, sql.ErrNoRows) {
		return "请先绑定账号", nil
	}
	if err != nil {
		return "", fmt.Errorf("read Telegram subscription: %w", err)
	}
	public, err := publicurl.BuildSubscription(publicurl.SubscriptionConfig{
		Origins: origins, AppURL: appURL, PanelURL: panelURL, Path: pathValue, ForceHTTPS: forceHTTPS,
	}, token, "")
	if err != nil {
		return "", fmt.Errorf("build Telegram subscription URL: %w", err)
	}
	return "🔗 您的订阅链接：\n\n" + public, nil
}

func unbindTelegramUserTx(ctx context.Context, tx *sql.Tx, chatID int64, now time.Time) (string, error) {
	result, err := tx.ExecContext(ctx, `
		UPDATE users SET telegram_id=NULL,admin_revision=admin_revision+1,updated_at=?
		WHERE telegram_id=? AND account_kind='human'
	`, now.Unix(), chatID)
	if err != nil {
		return "", fmt.Errorf("unbind Telegram user: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return "请先绑定账号", nil
	}
	if rows != 1 {
		return "解绑失败", nil
	}
	return "解绑成功", nil
}

func telegramSubscriptionToken(raw string) (string, bool) {
	if raw == "" || len(raw) > maxTelegramURLBytes || !utf8.ValidString(raw) || strings.IndexByte(raw, 0) >= 0 {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" {
		return "", false
	}
	if values, ok := parsed.Query()["token"]; ok {
		if len(values) != 1 || !validSubscriptionToken(values[0]) {
			return "", false
		}
		return values[0], true
	}
	token := path.Base(strings.TrimRight(parsed.Path, "/"))
	if !validSubscriptionToken(token) {
		return "", false
	}
	return token, true
}

func validTelegramInboundText(value string) bool {
	return value != "" && utf8.ValidString(value) && len(value) <= maxTelegramInboundTextBytes &&
		utf8.RuneCountInString(value) <= maxTelegramMessageRunes && strings.IndexByte(value, 0) < 0
}
