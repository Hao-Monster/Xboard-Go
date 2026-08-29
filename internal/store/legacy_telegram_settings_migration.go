package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const LegacyTelegramSettingsSlice = "telegram-settings-v1"

type LegacyTelegramSettings struct {
	BotEnabled         bool   `json:"telegram_bot_enable"`
	BotTokenConfigured bool   `json:"telegram_bot_token_configured"`
	BotTokenCipher     []byte `json:"-"`
	WebhookURL         string `json:"telegram_webhook_url"`
	DiscussLink        string `json:"telegram_discuss_link"`
}

type LegacyTelegramSettingsImport struct {
	Slice, SourceSHA256                      string
	SourceSize                               int64
	Settings                                 LegacyTelegramSettings
	Checksum                                 string
	RollbackBackupPath, RollbackBackupSHA256 string
}

type LegacyTelegramSettingsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Settings             LegacyDomainResult `json:"settings"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func LegacyTelegramSettingsChecksum(settings LegacyTelegramSettings) string {
	return legacyCanonicalChecksum(struct {
		BotEnabled         bool   `json:"telegram_bot_enable"`
		BotTokenConfigured bool   `json:"telegram_bot_token_configured"`
		WebhookURL         string `json:"telegram_webhook_url"`
		DiscussLink        string `json:"telegram_discuss_link"`
	}{
		BotEnabled: settings.BotEnabled, BotTokenConfigured: settings.BotTokenConfigured,
		WebhookURL: settings.WebhookURL, DiscussLink: settings.DiscussLink,
	})
}

func ValidateLegacyTelegramSettingsData(settings LegacyTelegramSettings) error {
	if err := ValidateLegacyTelegramSettingsSource(settings); err != nil || !validSettingsCipherLength(settings.BotTokenCipher) || settings.BotTokenConfigured != (len(settings.BotTokenCipher) > 0) {
		return fmt.Errorf("%w: invalid legacy Telegram settings", ErrInvalidInput)
	}
	return nil
}

func ValidateLegacyTelegramSettingsSource(settings LegacyTelegramSettings) error {
	if (settings.BotEnabled && !settings.BotTokenConfigured) ||
		!validTelegramWebhookBaseURL(settings.WebhookURL) || !validTelegramDiscussLink(settings.DiscussLink) {
		return fmt.Errorf("%w: invalid legacy Telegram settings", ErrInvalidInput)
	}
	return nil
}

func (s *Store) LookupLegacyTelegramSettingsImport(ctx context.Context, sourceSHA256 string) (LegacyTelegramSettingsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyTelegramSettingsImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacyTelegramSettingsImport(ctx, s.db, sourceSHA256)
}

func lookupLegacyTelegramSettingsImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyTelegramSettingsImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice=? AND source_sha256=?`, LegacyTelegramSettingsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyTelegramSettingsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyTelegramSettingsImportReport{}, false, fmt.Errorf("lookup legacy Telegram settings migration: %w", err)
	}
	var report LegacyTelegramSettingsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyTelegramSettingsImportReport{}, false, fmt.Errorf("decode legacy Telegram settings migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyTelegramSettings(ctx context.Context, input LegacyTelegramSettingsImport, now time.Time) (LegacyTelegramSettingsImportReport, error) {
	if err := validateLegacyTelegramSettingsImport(input); err != nil {
		return LegacyTelegramSettingsImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyTelegramSettingsImportReport{}, fmt.Errorf("begin legacy Telegram settings import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != CurrentSchemaVersion() {
		return LegacyTelegramSettingsImportReport{}, fmt.Errorf("legacy Telegram settings import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyTelegramSettingsImportReport{}, fmt.Errorf("validate legacy Telegram settings target schema: %w", err)
	}
	if existing, found, err := lookupLegacyTelegramSettingsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyTelegramSettingsImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyTelegramSettingsImportReport{}, err
		}
		return existing, nil
	}
	var runs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyTelegramSettingsSlice).Scan(&runs); err != nil {
		return LegacyTelegramSettingsImportReport{}, err
	}
	if runs != 0 {
		return LegacyTelegramSettingsImportReport{}, fmt.Errorf("%w: legacy Telegram settings were already imported from another snapshot", ErrConflict)
	}
	var enabled bool
	var tokenCipher, webhookSecret, pendingWebhookSecret []byte
	var webhookURL, discussLink, provisionID, username string
	var configuredAt sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT telegram_bot_enable,telegram_bot_token_cipher,telegram_webhook_url,telegram_discuss_link,
		       telegram_webhook_secret_cipher,telegram_webhook_pending_secret_cipher,
		       COALESCE(telegram_webhook_provision_id,''),telegram_bot_username,telegram_webhook_configured_at
		FROM app_settings WHERE id=1
	`).Scan(&enabled, &tokenCipher, &webhookURL, &discussLink, &webhookSecret, &pendingWebhookSecret, &provisionID, &username, &configuredAt); err != nil {
		return LegacyTelegramSettingsImportReport{}, fmt.Errorf("read Telegram settings migration target: %w", err)
	}
	if enabled || len(tokenCipher) != 0 || webhookURL != "" || discussLink != "" || len(webhookSecret) != 0 || len(pendingWebhookSecret) != 0 || provisionID != "" || username != "" || configuredAt.Valid {
		return LegacyTelegramSettingsImportReport{}, fmt.Errorf("%w: legacy Telegram settings import requires a pristine Telegram target", ErrConflict)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE app_settings SET telegram_bot_enable=?,telegram_bot_token_cipher=?,telegram_webhook_url=?,telegram_discuss_link=?,
		telegram_webhook_secret_cipher=NULL,telegram_webhook_pending_secret_cipher=NULL,
		telegram_webhook_provision_id=NULL,telegram_bot_username='',telegram_webhook_configured_at=NULL,
		updated_by=NULL,updated_at=?,revision=revision+1 WHERE id=1
	`, input.Settings.BotEnabled, nullableBytes(input.Settings.BotTokenCipher), input.Settings.WebhookURL, input.Settings.DiscussLink, now.UTC().Unix()); err != nil {
		return LegacyTelegramSettingsImportReport{}, fmt.Errorf("write legacy Telegram settings: %w", err)
	}
	target, err := readLegacyTelegramSettingsTarget(ctx, tx)
	if err != nil {
		return LegacyTelegramSettingsImportReport{}, err
	}
	if !sameLegacyTelegramSettings(input.Settings, target) {
		return LegacyTelegramSettingsImportReport{}, errors.New("legacy Telegram settings target verification does not match source")
	}
	report := LegacyTelegramSettingsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Settings:  LegacyDomainResult{SourceRows: 1, TargetRows: 1, SourceChecksum: input.Checksum, TargetChecksum: LegacyTelegramSettingsChecksum(target)},
		AppliedAt: now.UTC(),
	}
	if report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		return LegacyTelegramSettingsImportReport{}, errors.New("legacy Telegram settings target checksum does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyTelegramSettingsImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_migration_runs(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at) VALUES(?,?,?,?,?,?,?)`,
		report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyTelegramSettingsImportReport{}, fmt.Errorf("record legacy Telegram settings migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyTelegramSettingsImportReport{}, err
	}
	return report, nil
}

func validateLegacyTelegramSettingsImport(input LegacyTelegramSettingsImport) error {
	if input.Slice != LegacyTelegramSettingsSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) || strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 ||
		!validLowerSHA256(input.RollbackBackupSHA256) || input.Checksum != LegacyTelegramSettingsChecksum(input.Settings) {
		return fmt.Errorf("%w: invalid legacy Telegram settings import", ErrInvalidInput)
	}
	return ValidateLegacyTelegramSettingsData(input.Settings)
}

func sameLegacyTelegramSettings(expected, actual LegacyTelegramSettings) bool {
	return expected.BotEnabled == actual.BotEnabled && expected.BotTokenConfigured == actual.BotTokenConfigured && expected.WebhookURL == actual.WebhookURL && expected.DiscussLink == actual.DiscussLink &&
		subtle.ConstantTimeCompare(expected.BotTokenCipher, actual.BotTokenCipher) == 1
}

func readLegacyTelegramSettingsTarget(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (LegacyTelegramSettings, error) {
	var target LegacyTelegramSettings
	if err := database.QueryRowContext(ctx, `SELECT telegram_bot_enable,telegram_bot_token_cipher,telegram_webhook_url,telegram_discuss_link FROM app_settings WHERE id=1`).Scan(
		&target.BotEnabled, &target.BotTokenCipher, &target.WebhookURL, &target.DiscussLink); err != nil {
		return LegacyTelegramSettings{}, fmt.Errorf("verify legacy Telegram settings: %w", err)
	}
	target.BotTokenCipher = append([]byte(nil), target.BotTokenCipher...)
	target.BotTokenConfigured = len(target.BotTokenCipher) > 0
	return target, nil
}
