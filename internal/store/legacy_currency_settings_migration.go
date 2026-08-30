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

const LegacyCurrencySettingsSlice = "currency-settings-v1"

type LegacyCurrencySettings struct {
	Currency       string `json:"currency"`
	CurrencySymbol string `json:"currency_symbol"`
}

type LegacyCurrencySettingsImport struct {
	Slice, SourceSHA256                      string
	SourceSize                               int64
	Settings                                 LegacyCurrencySettings
	Checksum                                 string
	RollbackBackupPath, RollbackBackupSHA256 string
}

type LegacyCurrencySettingsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Settings             LegacyDomainResult `json:"settings"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func NormalizeLegacyCurrencySettings(settings LegacyCurrencySettings) (LegacyCurrencySettings, error) {
	settings.Currency = strings.ToUpper(strings.TrimSpace(settings.Currency))
	settings.CurrencySymbol = strings.TrimSpace(settings.CurrencySymbol)
	if !validCurrencyCode(settings.Currency) || !utf8.ValidString(settings.CurrencySymbol) ||
		len(settings.CurrencySymbol) > maxCurrencySymbolBytes || strings.IndexFunc(settings.CurrencySymbol, unicode.IsControl) >= 0 {
		return LegacyCurrencySettings{}, fmt.Errorf("%w: invalid legacy currency settings", ErrInvalidInput)
	}
	return settings, nil
}

func LegacyCurrencySettingsChecksum(settings LegacyCurrencySettings) string {
	return legacyCanonicalChecksum(settings)
}

func (s *Store) LookupLegacyCurrencySettingsImport(ctx context.Context, sourceSHA256 string) (LegacyCurrencySettingsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyCurrencySettingsImportReport{}, false, ErrInvalidInput
	}
	report, found, err := lookupLegacyCurrencySettingsImport(ctx, s.db, sourceSHA256)
	if err != nil || !found {
		return report, found, err
	}
	var currency, symbol string
	if err := s.db.QueryRowContext(ctx, `SELECT currency,currency_symbol FROM app_settings WHERE id=1`).Scan(&currency, &symbol); err != nil {
		return LegacyCurrencySettingsImportReport{}, false, fmt.Errorf("verify imported legacy currency settings: %w", err)
	}
	if LegacyCurrencySettingsChecksum(LegacyCurrencySettings{Currency: currency, CurrencySymbol: symbol}) != report.Settings.TargetChecksum {
		return LegacyCurrencySettingsImportReport{}, false, fmt.Errorf("%w: imported legacy currency settings no longer match their migration ledger", ErrConflict)
	}
	return report, true, nil
}

func lookupLegacyCurrencySettingsImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyCurrencySettingsImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice=? AND source_sha256=?`, LegacyCurrencySettingsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyCurrencySettingsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyCurrencySettingsImportReport{}, false, fmt.Errorf("lookup legacy currency settings migration: %w", err)
	}
	var report LegacyCurrencySettingsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyCurrencySettingsImportReport{}, false, fmt.Errorf("decode legacy currency settings migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyCurrencySettings(ctx context.Context, input LegacyCurrencySettingsImport, now time.Time) (LegacyCurrencySettingsImportReport, error) {
	if err := validateLegacyCurrencySettingsImport(input); err != nil {
		return LegacyCurrencySettingsImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyCurrencySettingsImportReport{}, fmt.Errorf("begin legacy currency settings import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != CurrentSchemaVersion() {
		return LegacyCurrencySettingsImportReport{}, fmt.Errorf("legacy currency settings import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyCurrencySettingsImportReport{}, fmt.Errorf("validate legacy currency settings target schema: %w", err)
	}
	if existing, found, err := lookupLegacyCurrencySettingsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyCurrencySettingsImportReport{}, err
	} else if found {
		var currency, symbol string
		if err := tx.QueryRowContext(ctx, `SELECT currency,currency_symbol FROM app_settings WHERE id=1`).Scan(&currency, &symbol); err != nil {
			return LegacyCurrencySettingsImportReport{}, fmt.Errorf("verify imported legacy currency settings: %w", err)
		}
		if LegacyCurrencySettingsChecksum(LegacyCurrencySettings{Currency: currency, CurrencySymbol: symbol}) != existing.Settings.TargetChecksum {
			return LegacyCurrencySettingsImportReport{}, fmt.Errorf("%w: imported legacy currency settings no longer match their migration ledger", ErrConflict)
		}
		if err := tx.Commit(); err != nil {
			return LegacyCurrencySettingsImportReport{}, err
		}
		return existing, nil
	}
	var runs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyCurrencySettingsSlice).Scan(&runs); err != nil {
		return LegacyCurrencySettingsImportReport{}, err
	}
	if runs != 0 {
		return LegacyCurrencySettingsImportReport{}, fmt.Errorf("%w: legacy currency settings were already imported from another snapshot", ErrConflict)
	}
	var revision int64
	var currency, symbol string
	if err := tx.QueryRowContext(ctx, `SELECT revision,currency,currency_symbol FROM app_settings WHERE id=1`).Scan(&revision, &currency, &symbol); err != nil {
		return LegacyCurrencySettingsImportReport{}, fmt.Errorf("read currency settings migration target: %w", err)
	}
	if currency != "CNY" || symbol != "¥" {
		return LegacyCurrencySettingsImportReport{}, fmt.Errorf("%w: legacy currency settings import requires pristine currency fields", ErrConflict)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE app_settings SET currency=?,currency_symbol=?,revision=revision+1,updated_by=NULL,updated_at=?
		WHERE id=1 AND revision=? AND currency='CNY' AND currency_symbol='¥'
	`, input.Settings.Currency, input.Settings.CurrencySymbol, now.UTC().Unix(), revision)
	if err != nil {
		return LegacyCurrencySettingsImportReport{}, fmt.Errorf("write legacy currency settings: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return LegacyCurrencySettingsImportReport{}, errors.New("legacy currency settings target changed during import")
	}
	if err := tx.QueryRowContext(ctx, `SELECT currency,currency_symbol FROM app_settings WHERE id=1`).Scan(&currency, &symbol); err != nil {
		return LegacyCurrencySettingsImportReport{}, fmt.Errorf("verify legacy currency settings: %w", err)
	}
	target := LegacyCurrencySettings{Currency: currency, CurrencySymbol: symbol}
	report := LegacyCurrencySettingsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Settings: LegacyDomainResult{
			SourceRows: 1, TargetRows: 1, SourceChecksum: input.Checksum,
			TargetChecksum: LegacyCurrencySettingsChecksum(target),
		},
		AppliedAt: now.UTC(),
	}
	if report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		return LegacyCurrencySettingsImportReport{}, errors.New("legacy currency settings target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyCurrencySettingsImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_migration_runs(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at) VALUES(?,?,?,?,?,?,?)`,
		report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyCurrencySettingsImportReport{}, fmt.Errorf("record legacy currency settings migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyCurrencySettingsImportReport{}, err
	}
	return report, nil
}

func validateLegacyCurrencySettingsImport(input LegacyCurrencySettingsImport) error {
	normalized, err := NormalizeLegacyCurrencySettings(input.Settings)
	if err != nil || normalized != input.Settings || input.Slice != LegacyCurrencySettingsSlice ||
		!validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) || strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 ||
		!validLowerSHA256(input.RollbackBackupSHA256) || input.Checksum != LegacyCurrencySettingsChecksum(input.Settings) {
		return fmt.Errorf("%w: invalid legacy currency settings import", ErrInvalidInput)
	}
	return nil
}
