package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const LegacySubscriptionConfigSlice = "subscription-config-v1"

type LegacySubscriptionConfig struct {
	Path         string            `json:"path"`
	ShowInfo     bool              `json:"show_info"`
	ShowProtocol bool              `json:"show_protocol"`
	Templates    map[string]string `json:"templates"`
}

type LegacySubscriptionConfigImport struct {
	Slice                string
	SourceSHA256         string
	SourceSize           int64
	Config               LegacySubscriptionConfig
	Checksum             string
	RollbackBackupPath   string
	RollbackBackupSHA256 string
}

type LegacySubscriptionConfigImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Config               LegacyDomainResult `json:"config"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func LegacySubscriptionConfigChecksum(config LegacySubscriptionConfig) string {
	return legacyCanonicalChecksum(config)
}

func ValidateLegacySubscriptionConfigData(config LegacySubscriptionConfig) error {
	normalized, err := normalizeSubscriptionSettings(SaveSubscriptionSettingsInput{
		Path: config.Path, ShowInfo: config.ShowInfo, ShowProtocol: config.ShowProtocol, Templates: config.Templates,
	})
	if err != nil {
		return err
	}
	if normalized.Path != config.Path {
		return fmt.Errorf("%w: legacy subscription path requires normalization", ErrInvalidInput)
	}
	return nil
}

func (s *Store) LookupLegacySubscriptionConfigImport(ctx context.Context, sourceSHA256 string) (LegacySubscriptionConfigImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacySubscriptionConfigImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacySubscriptionConfigImport(ctx, s.db, sourceSHA256)
}

func lookupLegacySubscriptionConfigImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacySubscriptionConfigImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `
		SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?
	`, LegacySubscriptionConfigSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacySubscriptionConfigImportReport{}, false, nil
	}
	if err != nil {
		return LegacySubscriptionConfigImportReport{}, false, fmt.Errorf("lookup legacy subscription config migration: %w", err)
	}
	var report LegacySubscriptionConfigImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacySubscriptionConfigImportReport{}, false, fmt.Errorf("decode legacy subscription config migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacySubscriptionConfig(ctx context.Context, input LegacySubscriptionConfigImport, now time.Time) (LegacySubscriptionConfigImportReport, error) {
	normalized, err := validateLegacySubscriptionConfigImport(input)
	if err != nil {
		return LegacySubscriptionConfigImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacySubscriptionConfigImportReport{}, fmt.Errorf("begin legacy subscription config import: %w", err)
	}
	defer tx.Rollback()

	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return LegacySubscriptionConfigImportReport{}, fmt.Errorf("read legacy subscription config target schema: %w", err)
	}
	if version != CurrentSchemaVersion() {
		return LegacySubscriptionConfigImportReport{}, fmt.Errorf("legacy subscription config import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacySubscriptionConfigImportReport{}, fmt.Errorf("validate legacy subscription config target schema: %w", err)
	}
	if existing, found, err := lookupLegacySubscriptionConfigImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacySubscriptionConfigImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacySubscriptionConfigImportReport{}, fmt.Errorf("commit idempotent legacy subscription config import: %w", err)
		}
		return existing, nil
	}

	var otherRuns int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacySubscriptionConfigSlice).Scan(&otherRuns); err != nil {
		return LegacySubscriptionConfigImportReport{}, fmt.Errorf("count legacy subscription config migrations: %w", err)
	}
	if otherRuns != 0 {
		return LegacySubscriptionConfigImportReport{}, fmt.Errorf("%w: legacy subscription config was already imported from another snapshot", ErrConflict)
	}
	current, err := readSubscriptionSettings(ctx, tx)
	if err != nil {
		return LegacySubscriptionConfigImportReport{}, fmt.Errorf("read target subscription config: %w", err)
	}
	if current.Revision != 1 || current.Path != "s" || current.ShowInfo || current.ShowProtocol || !subscriptionTemplatesEmpty(current.Templates) {
		return LegacySubscriptionConfigImportReport{}, fmt.Errorf("%w: legacy subscription config import requires the default target", ErrConflict)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE subscription_settings
		SET path = ?, show_info = ?, show_protocol = ?, revision = 2, updated_by = NULL, updated_at = ?
		WHERE id = 1 AND revision = 1
	`, normalized.Path, normalized.ShowInfo, normalized.ShowProtocol, now.UTC().Unix()); err != nil {
		return LegacySubscriptionConfigImportReport{}, fmt.Errorf("import legacy subscription settings: %w", err)
	}
	statement, err := tx.PrepareContext(ctx, `UPDATE subscription_templates SET content = ?, updated_at = ? WHERE name = ?`)
	if err != nil {
		return LegacySubscriptionConfigImportReport{}, fmt.Errorf("prepare legacy subscription template import: %w", err)
	}
	defer statement.Close()
	for _, name := range SubscriptionTemplateNames {
		result, err := statement.ExecContext(ctx, normalized.Templates[name], now.UTC().Unix(), name)
		if err != nil {
			return LegacySubscriptionConfigImportReport{}, fmt.Errorf("import legacy subscription template %s: %w", name, err)
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			return LegacySubscriptionConfigImportReport{}, fmt.Errorf("import legacy subscription template %s: unexpected row count", name)
		}
	}
	targetSettings, err := readSubscriptionSettings(ctx, tx)
	if err != nil {
		return LegacySubscriptionConfigImportReport{}, fmt.Errorf("verify imported legacy subscription config: %w", err)
	}
	target := legacySubscriptionConfigFromSettings(targetSettings)
	report := LegacySubscriptionConfigImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Config: LegacyDomainResult{
			SourceRows: 1, TargetRows: 1, SourceChecksum: input.Checksum,
			TargetChecksum: LegacySubscriptionConfigChecksum(target),
		},
		AppliedAt: now.UTC(),
	}
	if report.Config.SourceChecksum != report.Config.TargetChecksum {
		return LegacySubscriptionConfigImportReport{}, errors.New("legacy subscription config target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacySubscriptionConfigImportReport{}, fmt.Errorf("encode legacy subscription config migration report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO legacy_migration_runs
		(slice, source_sha256, source_size, rollback_backup_path, rollback_backup_sha256, report_json, applied_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacySubscriptionConfigImportReport{}, fmt.Errorf("record legacy subscription config migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacySubscriptionConfigImportReport{}, fmt.Errorf("commit legacy subscription config import: %w", err)
	}
	return report, nil
}

func validateLegacySubscriptionConfigImport(input LegacySubscriptionConfigImport) (SaveSubscriptionSettingsInput, error) {
	if input.Slice != LegacySubscriptionConfigSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) ||
		strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 || !validLowerSHA256(input.RollbackBackupSHA256) ||
		input.Checksum != LegacySubscriptionConfigChecksum(input.Config) {
		return SaveSubscriptionSettingsInput{}, fmt.Errorf("%w: invalid legacy subscription config import", ErrInvalidInput)
	}
	normalized, err := normalizeSubscriptionSettings(SaveSubscriptionSettingsInput{
		Path: input.Config.Path, ShowInfo: input.Config.ShowInfo, ShowProtocol: input.Config.ShowProtocol,
		Templates: input.Config.Templates,
	})
	if err != nil {
		return SaveSubscriptionSettingsInput{}, err
	}
	if err := ValidateLegacySubscriptionConfigData(input.Config); err != nil {
		return SaveSubscriptionSettingsInput{}, err
	}
	return normalized, nil
}

func legacySubscriptionConfigFromSettings(settings SubscriptionSettings) LegacySubscriptionConfig {
	templates := make(map[string]string, len(SubscriptionTemplateNames))
	for _, name := range SubscriptionTemplateNames {
		templates[name] = settings.Templates[name]
	}
	return LegacySubscriptionConfig{
		Path: settings.Path, ShowInfo: settings.ShowInfo, ShowProtocol: settings.ShowProtocol, Templates: templates,
	}
}

func subscriptionTemplatesEmpty(templates map[string]string) bool {
	if len(templates) != len(SubscriptionTemplateNames) {
		return false
	}
	for _, name := range SubscriptionTemplateNames {
		if templates[name] != "" {
			return false
		}
	}
	return true
}
