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

	"github.com/Hao-Monster/Xboard-Go/internal/theme"
)

const LegacyThemeSettingsSlice = "theme-settings-v1"

type LegacyThemeSettings struct {
	ActiveTheme string       `json:"active_theme"`
	Config      theme.Config `json:"config"`
}

type LegacyThemeSettingsImport struct {
	Slice, SourceSHA256                      string
	SourceSize                               int64
	Settings                                 LegacyThemeSettings
	Checksum                                 string
	RollbackBackupPath, RollbackBackupSHA256 string
}

type LegacyThemeSettingsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Settings             LegacyDomainResult `json:"settings"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func NormalizeLegacyThemeSettings(settings LegacyThemeSettings) (LegacyThemeSettings, error) {
	settings.ActiveTheme = strings.TrimSpace(settings.ActiveTheme)
	settings.Config.ThemeColor = strings.TrimSpace(settings.Config.ThemeColor)
	settings.Config.BackgroundURL = strings.TrimSpace(settings.Config.BackgroundURL)
	settings.Config.FontScale = strings.TrimSpace(settings.Config.FontScale)
	settings.Config.Radius = strings.TrimSpace(settings.Config.Radius)
	if settings.ActiveTheme == "" {
		settings.ActiveTheme = "Xboard"
	}
	if settings.Config.ThemeColor == "" {
		settings.Config.ThemeColor = "default"
	}
	if settings.Config.FontScale == "" {
		settings.Config.FontScale = "normal"
	}
	if settings.Config.Radius == "" {
		settings.Config.Radius = "rounded"
	}
	if settings.ActiveTheme != "Xboard" || settings.Config.BackgroundURL != "" || settings.Config.FontScale != "normal" || settings.Config.Radius != "rounded" {
		return LegacyThemeSettings{}, fmt.Errorf("%w: legacy theme requires executable or unsupported behavior", ErrInvalidInput)
	}
	switch settings.Config.ThemeColor {
	case "default", "blue", "black", "darkblue":
	default:
		return LegacyThemeSettings{}, fmt.Errorf("%w: legacy Xboard theme color is invalid", ErrInvalidInput)
	}
	return settings, nil
}

func LegacyThemeSettingsChecksum(settings LegacyThemeSettings) string {
	return legacyCanonicalChecksum(settings)
}

func (s *Store) LookupLegacyThemeSettingsImport(ctx context.Context, sourceSHA256 string) (LegacyThemeSettingsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyThemeSettingsImportReport{}, false, ErrInvalidInput
	}
	report, found, err := lookupLegacyThemeSettingsImport(ctx, s.db, sourceSHA256)
	if err != nil || !found {
		return report, found, err
	}
	settings, err := readLegacyThemeSettingsTarget(ctx, s.db)
	if err != nil {
		return LegacyThemeSettingsImportReport{}, false, fmt.Errorf("verify imported legacy theme settings: %w", err)
	}
	if LegacyThemeSettingsChecksum(settings) != report.Settings.TargetChecksum {
		return LegacyThemeSettingsImportReport{}, false, fmt.Errorf("%w: imported legacy theme settings no longer match their migration ledger", ErrConflict)
	}
	return report, true, nil
}

func lookupLegacyThemeSettingsImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyThemeSettingsImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice=? AND source_sha256=?`, LegacyThemeSettingsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyThemeSettingsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyThemeSettingsImportReport{}, false, fmt.Errorf("lookup legacy theme settings migration: %w", err)
	}
	var report LegacyThemeSettingsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyThemeSettingsImportReport{}, false, fmt.Errorf("decode legacy theme settings migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyThemeSettings(ctx context.Context, input LegacyThemeSettingsImport, now time.Time) (LegacyThemeSettingsImportReport, error) {
	if err := validateLegacyThemeSettingsImport(input); err != nil || now.Unix() < 0 {
		if err != nil {
			return LegacyThemeSettingsImportReport{}, err
		}
		return LegacyThemeSettingsImportReport{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyThemeSettingsImportReport{}, fmt.Errorf("begin legacy theme settings import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != CurrentSchemaVersion() {
		return LegacyThemeSettingsImportReport{}, fmt.Errorf("legacy theme settings import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyThemeSettingsImportReport{}, fmt.Errorf("validate legacy theme settings target schema: %w", err)
	}
	if existing, found, err := lookupLegacyThemeSettingsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyThemeSettingsImportReport{}, err
	} else if found {
		if existing.Settings.SourceChecksum != input.Checksum {
			return LegacyThemeSettingsImportReport{}, fmt.Errorf("%w: legacy theme settings source options differ from their migration ledger", ErrConflict)
		}
		settings, err := readLegacyThemeSettingsTarget(ctx, tx)
		if err != nil {
			return LegacyThemeSettingsImportReport{}, fmt.Errorf("verify imported legacy theme settings: %w", err)
		}
		if LegacyThemeSettingsChecksum(settings) != existing.Settings.TargetChecksum {
			return LegacyThemeSettingsImportReport{}, fmt.Errorf("%w: imported legacy theme settings no longer match their migration ledger", ErrConflict)
		}
		if err := tx.Commit(); err != nil {
			return LegacyThemeSettingsImportReport{}, err
		}
		return existing, nil
	}
	var runs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyThemeSettingsSlice).Scan(&runs); err != nil {
		return LegacyThemeSettingsImportReport{}, err
	}
	if runs != 0 {
		return LegacyThemeSettingsImportReport{}, fmt.Errorf("%w: legacy theme settings were already imported from another snapshot", ErrConflict)
	}
	catalog, err := readThemeCatalog(ctx, tx)
	if err != nil {
		return LegacyThemeSettingsImportReport{}, fmt.Errorf("read theme migration target: %w", err)
	}
	if len(catalog.Themes) != 1 || catalog.ActiveTheme != "Xboard" || catalog.Revision != 1 {
		return LegacyThemeSettingsImportReport{}, fmt.Errorf("%w: legacy theme settings import requires a pristine target", ErrConflict)
	}
	current := catalog.Themes[0]
	defaultConfig := theme.Config{ThemeColor: "default", BackgroundURL: "", FontScale: "normal", Radius: "rounded"}
	if current.Name != "Xboard" || !current.IsSystem || current.Revision != 1 || current.Config != defaultConfig || !current.UpdatedAt.Equal(time.Unix(0, 0).UTC()) {
		return LegacyThemeSettingsImportReport{}, fmt.Errorf("%w: legacy theme settings import requires a pristine target", ErrConflict)
	}
	configJSON, err := json.Marshal(input.Settings.Config)
	if err != nil {
		return LegacyThemeSettingsImportReport{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE themes SET config_json=?, revision=2, updated_by=NULL, updated_at=?
		WHERE name='Xboard' COLLATE NOCASE AND revision=1 AND is_system=1
	`, string(configJSON), now.UTC().Unix())
	if err != nil {
		return LegacyThemeSettingsImportReport{}, fmt.Errorf("write legacy theme settings: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return LegacyThemeSettingsImportReport{}, errors.New("legacy theme settings target changed during import")
	}
	target, err := readTheme(ctx, tx, "Xboard")
	if err != nil {
		return LegacyThemeSettingsImportReport{}, fmt.Errorf("verify legacy theme settings: %w", err)
	}
	targetSettings := LegacyThemeSettings{ActiveTheme: catalog.ActiveTheme, Config: target.Config}
	report := LegacyThemeSettingsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Settings:  LegacyDomainResult{SourceRows: 1, TargetRows: 1, SourceChecksum: input.Checksum, TargetChecksum: LegacyThemeSettingsChecksum(targetSettings)},
		AppliedAt: now.UTC(),
	}
	if report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		return LegacyThemeSettingsImportReport{}, errors.New("legacy theme settings target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyThemeSettingsImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_migration_runs(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at) VALUES(?,?,?,?,?,?,?)`,
		report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyThemeSettingsImportReport{}, fmt.Errorf("record legacy theme settings migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyThemeSettingsImportReport{}, err
	}
	return report, nil
}

func readLegacyThemeSettingsTarget(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (LegacyThemeSettings, error) {
	var settings LegacyThemeSettings
	var configJSON string
	if err := database.QueryRowContext(ctx, `
		SELECT configured.active_theme, installed.config_json
		FROM theme_settings configured
		JOIN themes installed ON installed.name = configured.active_theme COLLATE NOCASE
		WHERE configured.id = 1
	`).Scan(&settings.ActiveTheme, &configJSON); err != nil {
		return LegacyThemeSettings{}, err
	}
	if err := json.Unmarshal([]byte(configJSON), &settings.Config); err != nil {
		return LegacyThemeSettings{}, fmt.Errorf("decode imported legacy theme settings target: %w", err)
	}
	normalized, err := NormalizeLegacyThemeSettings(settings)
	if err != nil || normalized != settings {
		if err == nil {
			err = ErrInvalidInput
		}
		return LegacyThemeSettings{}, fmt.Errorf("validate imported legacy theme settings target: %w", err)
	}
	return settings, nil
}

func validateLegacyThemeSettingsImport(input LegacyThemeSettingsImport) error {
	normalized, err := NormalizeLegacyThemeSettings(input.Settings)
	if err != nil || normalized != input.Settings || input.Slice != LegacyThemeSettingsSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) || strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 ||
		!validLowerSHA256(input.RollbackBackupSHA256) || input.Checksum != LegacyThemeSettingsChecksum(input.Settings) {
		return fmt.Errorf("%w: invalid legacy theme settings import", ErrInvalidInput)
	}
	return nil
}
