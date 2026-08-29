package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const maxTelegramURLBytes = 2_048

const (
	telegramWebhookClaimStaleAfter  = 2 * time.Minute
	telegramWebhookReceiptRetention = 30 * 24 * time.Hour
	telegramWebhookPruneBatch       = 128
)

var (
	telegramProvisionIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
	telegramUsernamePattern    = regexp.MustCompile(`^[0-9A-Za-z_]{5,64}$`)
)

type TelegramWebhookClaimState int

const (
	TelegramWebhookClaimAcquired TelegramWebhookClaimState = iota + 1
	TelegramWebhookClaimInProgress
	TelegramWebhookClaimCompleted
)

func (s *Store) GetTelegramSettings(ctx context.Context) (TelegramSettings, error) {
	return readTelegramSettings(ctx, s.db)
}

func readTelegramSettings(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (TelegramSettings, error) {
	var settings TelegramSettings
	var tokenCipher []byte
	var configuredAt sql.NullInt64
	var updatedAt int64
	if err := database.QueryRowContext(ctx, `
		SELECT revision, telegram_bot_enable, telegram_bot_token_cipher, telegram_webhook_url,
		       telegram_discuss_link, telegram_bot_username, telegram_webhook_configured_at, updated_at
		FROM app_settings WHERE id = 1
	`).Scan(&settings.Revision, &settings.BotEnabled, &tokenCipher, &settings.WebhookURL,
		&settings.DiscussLink, &settings.BotUsername, &configuredAt, &updatedAt); err != nil {
		return TelegramSettings{}, fmt.Errorf("get Telegram settings: %w", err)
	}
	settings.BotTokenSet = len(tokenCipher) > 0
	if configuredAt.Valid {
		value := time.Unix(configuredAt.Int64, 0).UTC()
		settings.WebhookConfiguredAt = &value
	}
	settings.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return settings, nil
}

func (s *Store) GetTelegramSecretCiphers(ctx context.Context) (TelegramSecretCiphers, error) {
	var secrets TelegramSecretCiphers
	if err := s.db.QueryRowContext(ctx, `
		SELECT telegram_bot_enable, telegram_bot_token_cipher, telegram_webhook_secret_cipher,
		       telegram_webhook_pending_secret_cipher,
		       COALESCE(telegram_webhook_provision_id, '')
		FROM app_settings WHERE id = 1
	`).Scan(&secrets.BotEnabled, &secrets.BotToken, &secrets.WebhookSecret, &secrets.PendingWebhookSecret, &secrets.ProvisionID); err != nil {
		return TelegramSecretCiphers{}, fmt.Errorf("get Telegram secret ciphers: %w", err)
	}
	secrets.BotToken = append([]byte(nil), secrets.BotToken...)
	secrets.WebhookSecret = append([]byte(nil), secrets.WebhookSecret...)
	secrets.PendingWebhookSecret = append([]byte(nil), secrets.PendingWebhookSecret...)
	return secrets, nil
}

func (s *Store) UpdateTelegramSettings(ctx context.Context, administratorID, revision int64, input SaveTelegramSettingsInput, now time.Time) (TelegramSettings, error) {
	if administratorID < 1 || revision < 1 {
		return TelegramSettings{}, ErrInvalidInput
	}
	normalized, err := normalizeTelegramSettings(input)
	if err != nil {
		return TelegramSettings{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TelegramSettings{}, fmt.Errorf("begin update Telegram settings: %w", err)
	}
	defer tx.Rollback()
	var currentRevision int64
	var currentToken []byte
	var currentWebhookURL string
	if err := tx.QueryRowContext(ctx, `
		SELECT revision, telegram_bot_token_cipher, telegram_webhook_url FROM app_settings WHERE id = 1
	`).Scan(&currentRevision, &currentToken, &currentWebhookURL); err != nil {
		return TelegramSettings{}, fmt.Errorf("read current Telegram settings: %w", err)
	}
	if currentRevision != revision {
		return TelegramSettings{}, ErrConflict
	}
	tokenAvailable := len(currentToken) > 0
	if normalized.ReplaceBotToken {
		tokenAvailable = len(normalized.BotTokenCipher) > 0
	}
	if normalized.BotEnabled && !tokenAvailable {
		return TelegramSettings{}, ErrInvalidInput
	}
	resetWebhook := normalized.ReplaceBotToken || currentWebhookURL != normalized.WebhookURL
	result, err := tx.ExecContext(ctx, `
		UPDATE app_settings
		SET telegram_bot_enable = ?,
		    telegram_bot_token_cipher = CASE WHEN ? THEN ? ELSE telegram_bot_token_cipher END,
		    telegram_webhook_url = ?, telegram_discuss_link = ?,
		    telegram_webhook_secret_cipher = CASE WHEN ? THEN NULL ELSE telegram_webhook_secret_cipher END,
		    telegram_webhook_pending_secret_cipher = CASE WHEN ? THEN NULL ELSE telegram_webhook_pending_secret_cipher END,
		    telegram_webhook_provision_id = CASE WHEN ? THEN NULL ELSE telegram_webhook_provision_id END,
		    telegram_bot_username = CASE WHEN ? THEN '' ELSE telegram_bot_username END,
		    telegram_webhook_configured_at = CASE WHEN ? THEN NULL ELSE telegram_webhook_configured_at END,
		    updated_by = ?, updated_at = ?, revision = revision + 1
		WHERE id = 1 AND revision = ?
	`, normalized.BotEnabled, normalized.ReplaceBotToken, nullableBytes(normalized.BotTokenCipher),
		normalized.WebhookURL, normalized.DiscussLink,
		resetWebhook, resetWebhook, resetWebhook, resetWebhook, resetWebhook,
		administratorID, now.UTC().Unix(), revision)
	if err != nil {
		return TelegramSettings{}, fmt.Errorf("update Telegram settings: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return TelegramSettings{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return TelegramSettings{}, fmt.Errorf("commit Telegram settings: %w", err)
	}
	return s.GetTelegramSettings(ctx)
}

func (s *Store) BeginTelegramWebhookProvision(ctx context.Context, administratorID, revision int64, provisionID string, secretCipher []byte, now time.Time) (TelegramSecretCiphers, error) {
	if administratorID < 1 || revision < 1 || !telegramProvisionIDPattern.MatchString(provisionID) || !validSettingsCipherLength(secretCipher) || len(secretCipher) == 0 {
		return TelegramSecretCiphers{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TelegramSecretCiphers{}, fmt.Errorf("begin Telegram webhook provision: %w", err)
	}
	defer tx.Rollback()
	var tokenCipher, pendingSecretCipher []byte
	var botEnabled bool
	var existingProvisionID string
	if err := tx.QueryRowContext(ctx, `
		SELECT telegram_bot_token_cipher, telegram_bot_enable,
		       telegram_webhook_pending_secret_cipher, COALESCE(telegram_webhook_provision_id, '')
		FROM app_settings WHERE id = 1 AND revision = ?
	`, revision).Scan(&tokenCipher, &botEnabled, &pendingSecretCipher, &existingProvisionID); errors.Is(err, sql.ErrNoRows) {
		return TelegramSecretCiphers{}, ErrConflict
	} else if err != nil {
		return TelegramSecretCiphers{}, fmt.Errorf("read Telegram webhook settings: %w", err)
	}
	if len(tokenCipher) == 0 || !botEnabled {
		return TelegramSecretCiphers{}, ErrInvalidInput
	}
	if len(pendingSecretCipher) > 0 || existingProvisionID != "" {
		if !validSettingsCipherLength(pendingSecretCipher) || !telegramProvisionIDPattern.MatchString(existingProvisionID) {
			return TelegramSecretCiphers{}, errors.New("Telegram webhook provision state is invalid")
		}
		if err := tx.Commit(); err != nil {
			return TelegramSecretCiphers{}, fmt.Errorf("commit Telegram webhook provision lookup: %w", err)
		}
		return TelegramSecretCiphers{
			BotEnabled: true, BotToken: append([]byte(nil), tokenCipher...),
			PendingWebhookSecret: append([]byte(nil), pendingSecretCipher...), ProvisionID: existingProvisionID,
		}, nil
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE app_settings
		SET telegram_webhook_pending_secret_cipher = ?, telegram_webhook_provision_id = ?,
		    updated_by = ?, updated_at = ?
		WHERE id = 1 AND revision = ? AND telegram_bot_enable = 1 AND telegram_bot_token_cipher IS NOT NULL
		  AND telegram_webhook_pending_secret_cipher IS NULL AND telegram_webhook_provision_id IS NULL
	`, secretCipher, provisionID, administratorID, now.UTC().Unix(), revision)
	if err != nil {
		return TelegramSecretCiphers{}, fmt.Errorf("store Telegram webhook provision: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return TelegramSecretCiphers{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return TelegramSecretCiphers{}, fmt.Errorf("commit Telegram webhook provision: %w", err)
	}
	return TelegramSecretCiphers{
		BotEnabled: true, BotToken: append([]byte(nil), tokenCipher...),
		PendingWebhookSecret: append([]byte(nil), secretCipher...), ProvisionID: provisionID,
	}, nil
}

func (s *Store) CompleteTelegramWebhookProvision(ctx context.Context, provisionID, username string, now time.Time) (TelegramSettings, error) {
	username = strings.TrimSpace(username)
	if !telegramProvisionIDPattern.MatchString(provisionID) || !validTelegramBotUsername(username) {
		return TelegramSettings{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE app_settings
		SET telegram_webhook_secret_cipher = telegram_webhook_pending_secret_cipher,
		    telegram_webhook_pending_secret_cipher = NULL,
		    telegram_bot_username = ?, telegram_webhook_configured_at = ?,
		    telegram_webhook_provision_id = NULL, updated_at = ?, revision = revision + 1
		WHERE id = 1 AND telegram_webhook_provision_id = ?
		  AND telegram_bot_enable = 1 AND telegram_bot_token_cipher IS NOT NULL
		  AND telegram_webhook_pending_secret_cipher IS NOT NULL
	`, username, now.UTC().Unix(), now.UTC().Unix(), provisionID)
	if err != nil {
		return TelegramSettings{}, fmt.Errorf("complete Telegram webhook provision: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return TelegramSettings{}, ErrConflict
	}
	return s.GetTelegramSettings(ctx)
}

func (s *Store) TelegramUserAvailable(ctx context.Context, telegramID int64, now time.Time) (bool, error) {
	if telegramID < 1 {
		return false, ErrInvalidInput
	}
	var available bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM users
			WHERE telegram_id = ? AND banned = 0 AND transfer_enable > 0
			  AND (expired_at IS NULL OR expired_at > ?)
		)
	`, telegramID, now.UTC().Unix()).Scan(&available); err != nil {
		return false, fmt.Errorf("read Telegram user availability: %w", err)
	}
	return available, nil
}

func (s *Store) ClaimTelegramWebhookUpdate(ctx context.Context, updateID int64, claimID string, now time.Time) (TelegramWebhookClaimState, error) {
	if updateID < 1 || !telegramProvisionIDPattern.MatchString(claimID) {
		return 0, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin Telegram webhook update claim: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM telegram_webhook_updates WHERE update_id IN (
			SELECT update_id FROM telegram_webhook_updates
			WHERE updated_at < ? ORDER BY updated_at, update_id LIMIT ?
		)
	`, now.UTC().Add(-telegramWebhookReceiptRetention).Unix(), telegramWebhookPruneBatch); err != nil {
		return 0, fmt.Errorf("prune Telegram webhook updates: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO telegram_webhook_updates(update_id, claim_id, completed, updated_at)
		VALUES(?, ?, 0, ?) ON CONFLICT(update_id) DO NOTHING
	`, updateID, claimID, now.UTC().Unix())
	if err != nil {
		return 0, fmt.Errorf("claim Telegram webhook update: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 1 {
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("commit Telegram webhook update claim: %w", err)
		}
		return TelegramWebhookClaimAcquired, nil
	}
	var completed bool
	if err := tx.QueryRowContext(ctx, `SELECT completed FROM telegram_webhook_updates WHERE update_id = ?`, updateID).Scan(&completed); err != nil {
		return 0, fmt.Errorf("read Telegram webhook update claim: %w", err)
	}
	if completed {
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("commit completed Telegram webhook update lookup: %w", err)
		}
		return TelegramWebhookClaimCompleted, nil
	}
	staleBefore := now.UTC().Add(-telegramWebhookClaimStaleAfter).Unix()
	result, err = tx.ExecContext(ctx, `
		UPDATE telegram_webhook_updates SET claim_id = ?, updated_at = ?
		WHERE update_id = ? AND completed = 0 AND updated_at <= ?
	`, claimID, now.UTC().Unix(), updateID, staleBefore)
	if err != nil {
		return 0, fmt.Errorf("reclaim Telegram webhook update: %w", err)
	}
	rows, _ = result.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit Telegram webhook update lookup: %w", err)
	}
	if rows == 1 {
		return TelegramWebhookClaimAcquired, nil
	}
	return TelegramWebhookClaimInProgress, nil
}

func (s *Store) ReleaseTelegramWebhookUpdate(ctx context.Context, updateID int64, claimID string) error {
	if updateID < 1 || !telegramProvisionIDPattern.MatchString(claimID) {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM telegram_webhook_updates WHERE update_id = ? AND claim_id = ? AND completed = 0`, updateID, claimID); err != nil {
		return fmt.Errorf("release Telegram webhook update: %w", err)
	}
	return nil
}

func (s *Store) CompleteTelegramWebhookUpdate(ctx context.Context, updateID int64, claimID string, now time.Time) error {
	if updateID < 1 || !telegramProvisionIDPattern.MatchString(claimID) {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE telegram_webhook_updates SET completed = 1, updated_at = ?
		WHERE update_id = ? AND claim_id = ? AND completed = 0
	`, now.UTC().Unix(), updateID, claimID)
	if err != nil {
		return fmt.Errorf("complete Telegram webhook update: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

func normalizeTelegramSettings(input SaveTelegramSettingsInput) (SaveTelegramSettingsInput, error) {
	input.WebhookURL = strings.TrimRight(strings.TrimSpace(input.WebhookURL), "/")
	input.DiscussLink = strings.TrimRight(strings.TrimSpace(input.DiscussLink), "/")
	if input.ReplaceBotToken && !validSettingsCipherLength(input.BotTokenCipher) {
		return SaveTelegramSettingsInput{}, ErrInvalidInput
	}
	if !validTelegramWebhookBaseURL(input.WebhookURL) || !validTelegramDiscussLink(input.DiscussLink) {
		return SaveTelegramSettingsInput{}, ErrInvalidInput
	}
	return input, nil
}

func validTelegramWebhookBaseURL(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > maxTelegramURLBytes || !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n\t") {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Opaque == "" && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil &&
		parsed.RawQuery == "" && !parsed.ForceQuery && parsed.Fragment == ""
}

func validTelegramDiscussLink(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > maxTelegramURLBytes || !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n\t") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Opaque != "" || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Path == "" || parsed.Path == "/" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "t.me" || host == "telegram.me" || host == "www.t.me" || host == "www.telegram.me"
}

func validTelegramBotUsername(username string) bool {
	return telegramUsernamePattern.MatchString(username) && strings.HasSuffix(strings.ToLower(username), "bot")
}
