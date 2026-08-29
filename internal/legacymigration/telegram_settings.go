package legacymigration

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/Hao-Monster/Xboard-Go/internal/telegrambot"
)

type TelegramSettingsSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Settings store.LegacyTelegramSettings
	BotToken []byte `json:"-"`
	Checksum string
}

func (snapshot *TelegramSettingsSnapshot) ClearSecrets() {
	if snapshot == nil {
		return
	}
	for index := range snapshot.BotToken {
		snapshot.BotToken[index] = 0
	}
	snapshot.BotToken = nil
}

func ReadTelegramSettingsSnapshot(ctx context.Context, sourcePath string) (TelegramSettingsSnapshot, error) {
	var settings store.LegacyTelegramSettings
	var botToken []byte
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_settings", []string{"name", "value"}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(length(CAST(name AS BLOB)) + COALESCE(length(CAST(value AS BLOB)), 0)), 0)
			FROM v2_settings WHERE name IN ('telegram_bot_enable','telegram_bot_token','telegram_webhook_url','telegram_discuss_link')
		`, 4, 8_704, "legacy Telegram settings"); err != nil {
			return err
		}
		rows, err := database.QueryContext(ctx, `
			SELECT name,CAST(value AS BLOB) FROM v2_settings
			WHERE name IN ('telegram_bot_enable','telegram_bot_token','telegram_webhook_url','telegram_discuss_link') ORDER BY name
		`)
		if err != nil {
			return fmt.Errorf("read legacy Telegram settings: %w", err)
		}
		defer rows.Close()
		seen := make(map[string]struct{}, 4)
		for rows.Next() {
			var name string
			var raw []byte
			if err := rows.Scan(&name, &raw); err != nil {
				return fmt.Errorf("scan legacy Telegram setting: %w", err)
			}
			if _, exists := seen[name]; exists {
				zeroLegacyBytes(raw)
				return fmt.Errorf("legacy Telegram settings contain duplicate %q rows", name)
			}
			seen[name] = struct{}{}
			switch name {
			case "telegram_bot_enable":
				settings.BotEnabled = len(raw) > 0 && string(raw) != "0"
			case "telegram_bot_token":
				if len(raw) > 0 {
					if !telegrambot.ValidBotToken(raw) {
						zeroLegacyBytes(raw)
						return fmt.Errorf("legacy Telegram bot token format is invalid")
					}
					botToken = append([]byte(nil), raw...)
					settings.BotTokenConfigured = true
				}
			case "telegram_webhook_url":
				settings.WebhookURL = string(raw)
			case "telegram_discuss_link":
				settings.DiscussLink = string(raw)
			}
			zeroLegacyBytes(raw)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate legacy Telegram settings: %w", err)
		}
		if err := store.ValidateLegacyTelegramSettingsSource(settings); err != nil {
			return fmt.Errorf("validate legacy Telegram settings: %w", err)
		}
		return nil
	})
	if err != nil {
		zeroLegacyBytes(botToken)
		return TelegramSettingsSnapshot{}, err
	}
	return TelegramSettingsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Settings: settings, BotToken: botToken, Checksum: store.LegacyTelegramSettingsChecksum(settings),
	}, nil
}

func zeroLegacyBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
