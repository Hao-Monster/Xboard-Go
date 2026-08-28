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

const LegacyNodeAgentSettingsSlice = "node-agent-settings-v1"

type LegacyNodeAgentSettings struct {
	ServerTokenHash   string `json:"-"`
	ServerTokenPrefix string `json:"server_token_prefix,omitempty"`
	PullInterval      int    `json:"server_pull_interval"`
	PushInterval      int    `json:"server_push_interval"`
	DeviceLimitMode   int    `json:"device_limit_mode"`
	WebSocketEnabled  bool   `json:"server_ws_enable"`
	WebSocketURL      string `json:"server_ws_url"`
}

type LegacyNodeAgentSettingsImport struct {
	Slice, SourceSHA256                      string
	SourceSize                               int64
	Settings                                 LegacyNodeAgentSettings
	Checksum                                 string
	RollbackBackupPath, RollbackBackupSHA256 string
}

type LegacyNodeAgentSettingsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Settings             LegacyDomainResult `json:"settings"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func LegacyNodeAgentSettingsChecksum(settings LegacyNodeAgentSettings) string {
	return legacyCanonicalChecksum(struct {
		ServerTokenConfigured bool   `json:"server_token_configured"`
		PullInterval          int    `json:"server_pull_interval"`
		PushInterval          int    `json:"server_push_interval"`
		DeviceLimitMode       int    `json:"device_limit_mode"`
		WebSocketEnabled      bool   `json:"server_ws_enable"`
		WebSocketURL          string `json:"server_ws_url"`
	}{
		ServerTokenConfigured: settings.ServerTokenHash != "",
		PullInterval:          settings.PullInterval,
		PushInterval:          settings.PushInterval,
		DeviceLimitMode:       settings.DeviceLimitMode,
		WebSocketEnabled:      settings.WebSocketEnabled,
		WebSocketURL:          settings.WebSocketURL,
	})
}

func ValidateLegacyNodeAgentSettingsData(settings LegacyNodeAgentSettings) error {
	if err := validateNodeAgentSettings(settings.PullInterval, settings.PushInterval, settings.DeviceLimitMode, settings.WebSocketURL); err != nil {
		return err
	}
	if settings.ServerTokenHash == "" {
		if settings.ServerTokenPrefix != "" {
			return fmt.Errorf("%w: legacy node token prefix exists without a digest", ErrInvalidInput)
		}
		return nil
	}
	if !validLowerSHA256(settings.ServerTokenHash) || len(settings.ServerTokenPrefix) < 1 || len(settings.ServerTokenPrefix) > 8 {
		return fmt.Errorf("%w: legacy node token metadata is invalid", ErrInvalidInput)
	}
	for _, value := range []byte(settings.ServerTokenPrefix) {
		if value < 0x21 || value > 0x7e {
			return fmt.Errorf("%w: legacy node token prefix is invalid", ErrInvalidInput)
		}
	}
	return nil
}

func (s *Store) LookupLegacyNodeAgentSettingsImport(ctx context.Context, sourceSHA256 string) (LegacyNodeAgentSettingsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyNodeAgentSettingsImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacyNodeAgentSettingsImport(ctx, s.db, sourceSHA256)
}

func lookupLegacyNodeAgentSettingsImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyNodeAgentSettingsImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice=? AND source_sha256=?`, LegacyNodeAgentSettingsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyNodeAgentSettingsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyNodeAgentSettingsImportReport{}, false, fmt.Errorf("lookup legacy node agent settings migration: %w", err)
	}
	var report LegacyNodeAgentSettingsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyNodeAgentSettingsImportReport{}, false, fmt.Errorf("decode legacy node agent settings migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyNodeAgentSettings(ctx context.Context, input LegacyNodeAgentSettingsImport, now time.Time) (LegacyNodeAgentSettingsImportReport, error) {
	if err := validateLegacyNodeAgentSettingsImport(input); err != nil {
		return LegacyNodeAgentSettingsImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyNodeAgentSettingsImportReport{}, fmt.Errorf("begin legacy node agent settings import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != CurrentSchemaVersion() {
		return LegacyNodeAgentSettingsImportReport{}, fmt.Errorf("legacy node agent settings import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyNodeAgentSettingsImportReport{}, fmt.Errorf("validate legacy node agent settings target schema: %w", err)
	}
	if existing, found, err := lookupLegacyNodeAgentSettingsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyNodeAgentSettingsImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyNodeAgentSettingsImportReport{}, err
		}
		return existing, nil
	}
	var runs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyNodeAgentSettingsSlice).Scan(&runs); err != nil {
		return LegacyNodeAgentSettingsImportReport{}, err
	}
	if runs != 0 {
		return LegacyNodeAgentSettingsImportReport{}, fmt.Errorf("%w: legacy node agent settings were already imported from another snapshot", ErrConflict)
	}
	var rows int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_agent_settings`).Scan(&rows); err != nil {
		return LegacyNodeAgentSettingsImportReport{}, err
	}
	if rows > 1 {
		return LegacyNodeAgentSettingsImportReport{}, fmt.Errorf("%w: node agent settings target is invalid", ErrConflict)
	}
	if rows == 1 {
		var revision int64
		if err := tx.QueryRowContext(ctx, `SELECT revision FROM node_agent_settings WHERE id=1`).Scan(&revision); err != nil || revision != 1 {
			return LegacyNodeAgentSettingsImportReport{}, fmt.Errorf("%w: legacy node agent settings import requires a pristine target", ErrConflict)
		}
		_, err = tx.ExecContext(ctx, `UPDATE node_agent_settings SET revision=2,server_token_hash=?,server_token_prefix=?,pull_interval=?,push_interval=?,device_limit_mode=?,websocket_enabled=?,websocket_url=?,updated_by=NULL,updated_at=? WHERE id=1 AND revision=1`,
			nullableLegacyNodeTokenHash(input.Settings.ServerTokenHash), input.Settings.ServerTokenPrefix, input.Settings.PullInterval, input.Settings.PushInterval,
			input.Settings.DeviceLimitMode, input.Settings.WebSocketEnabled, input.Settings.WebSocketURL, now.UTC().Unix())
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO node_agent_settings(id,revision,server_token_hash,server_token_prefix,pull_interval,push_interval,device_limit_mode,websocket_enabled,websocket_url,updated_by,updated_at) VALUES(1,1,?,?,?,?,?,?,?,NULL,?)`,
			nullableLegacyNodeTokenHash(input.Settings.ServerTokenHash), input.Settings.ServerTokenPrefix, input.Settings.PullInterval, input.Settings.PushInterval,
			input.Settings.DeviceLimitMode, input.Settings.WebSocketEnabled, input.Settings.WebSocketURL, now.UTC().Unix())
	}
	if err != nil {
		return LegacyNodeAgentSettingsImportReport{}, fmt.Errorf("write legacy node agent settings: %w", err)
	}
	target, err := readLegacyNodeAgentSettingsTarget(ctx, tx)
	if err != nil {
		return LegacyNodeAgentSettingsImportReport{}, err
	}
	if !sameLegacyNodeAgentSettings(input.Settings, target) {
		return LegacyNodeAgentSettingsImportReport{}, errors.New("legacy node agent settings target verification does not match source")
	}
	report := LegacyNodeAgentSettingsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Settings:  LegacyDomainResult{SourceRows: 1, TargetRows: 1, SourceChecksum: input.Checksum, TargetChecksum: LegacyNodeAgentSettingsChecksum(target)},
		AppliedAt: now.UTC(),
	}
	if report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		return LegacyNodeAgentSettingsImportReport{}, errors.New("legacy node agent settings target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyNodeAgentSettingsImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_migration_runs(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at) VALUES(?,?,?,?,?,?,?)`,
		report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyNodeAgentSettingsImportReport{}, fmt.Errorf("record legacy node agent settings migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyNodeAgentSettingsImportReport{}, err
	}
	return report, nil
}

func validateLegacyNodeAgentSettingsImport(input LegacyNodeAgentSettingsImport) error {
	if input.Slice != LegacyNodeAgentSettingsSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) || strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 ||
		!validLowerSHA256(input.RollbackBackupSHA256) || input.Checksum != LegacyNodeAgentSettingsChecksum(input.Settings) {
		return fmt.Errorf("%w: invalid legacy node agent settings import", ErrInvalidInput)
	}
	return ValidateLegacyNodeAgentSettingsData(input.Settings)
}

func nullableLegacyNodeTokenHash(hash string) any {
	if hash == "" {
		return nil
	}
	return hash
}

func sameLegacyNodeAgentSettings(expected, actual LegacyNodeAgentSettings) bool {
	hashesMatch := subtle.ConstantTimeCompare([]byte(expected.ServerTokenHash), []byte(actual.ServerTokenHash)) == 1
	return hashesMatch && expected.ServerTokenPrefix == actual.ServerTokenPrefix &&
		expected.PullInterval == actual.PullInterval && expected.PushInterval == actual.PushInterval &&
		expected.DeviceLimitMode == actual.DeviceLimitMode && expected.WebSocketEnabled == actual.WebSocketEnabled &&
		expected.WebSocketURL == actual.WebSocketURL
}

func readLegacyNodeAgentSettingsTarget(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (LegacyNodeAgentSettings, error) {
	var target LegacyNodeAgentSettings
	var hash sql.NullString
	if err := database.QueryRowContext(ctx, `SELECT server_token_hash,server_token_prefix,pull_interval,push_interval,device_limit_mode,websocket_enabled,websocket_url FROM node_agent_settings WHERE id=1`).Scan(
		&hash, &target.ServerTokenPrefix, &target.PullInterval, &target.PushInterval, &target.DeviceLimitMode, &target.WebSocketEnabled, &target.WebSocketURL); err != nil {
		return LegacyNodeAgentSettings{}, fmt.Errorf("verify legacy node agent settings: %w", err)
	}
	if hash.Valid {
		target.ServerTokenHash = hash.String
	}
	return target, nil
}
