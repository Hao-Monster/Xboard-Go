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

const LegacySafeAccessSettingsSlice = "safe-access-settings-v1"

type LegacySafeAccessSettings struct {
	SafeModeEnabled bool   `json:"safe_mode_enable"`
	SecurePath      string `json:"secure_path"`
}

type LegacySafeAccessSettingsImport struct {
	Slice, SourceSHA256                      string
	SourceSize                               int64
	Settings                                 LegacySafeAccessSettings
	Checksum                                 string
	FallbackSecurePath                       string
	RollbackBackupPath, RollbackBackupSHA256 string
}

type LegacySafeAccessSettingsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Settings             LegacyDomainResult `json:"settings"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func NormalizeLegacySafeAccessSettings(settings LegacySafeAccessSettings) (LegacySafeAccessSettings, error) {
	settings.SecurePath = strings.TrimSpace(settings.SecurePath)
	if !validConfigurableSecurePath(settings.SecurePath) {
		return LegacySafeAccessSettings{}, fmt.Errorf("%w: invalid legacy secure admin path", ErrInvalidInput)
	}
	return settings, nil
}

func LegacySafeAccessSettingsChecksum(settings LegacySafeAccessSettings) string {
	return legacyCanonicalChecksum(settings)
}

func (s *Store) LookupLegacySafeAccessSettingsImport(ctx context.Context, sourceSHA256 string) (LegacySafeAccessSettingsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacySafeAccessSettingsImportReport{}, false, ErrInvalidInput
	}
	report, found, err := lookupLegacySafeAccessSettingsImport(ctx, s.db, sourceSHA256)
	if err != nil || !found {
		return report, found, err
	}
	settings, err := readLegacySafeAccessSettingsTarget(ctx, s.db)
	if err != nil {
		return LegacySafeAccessSettingsImportReport{}, false, fmt.Errorf("verify imported legacy safe access settings: %w", err)
	}
	if LegacySafeAccessSettingsChecksum(settings) != report.Settings.TargetChecksum {
		return LegacySafeAccessSettingsImportReport{}, false, fmt.Errorf("%w: imported legacy safe access settings no longer match their migration ledger", ErrConflict)
	}
	return report, true, nil
}

func lookupLegacySafeAccessSettingsImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacySafeAccessSettingsImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice=? AND source_sha256=?`, LegacySafeAccessSettingsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacySafeAccessSettingsImportReport{}, false, nil
	}
	if err != nil {
		return LegacySafeAccessSettingsImportReport{}, false, fmt.Errorf("lookup legacy safe access settings migration: %w", err)
	}
	var report LegacySafeAccessSettingsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacySafeAccessSettingsImportReport{}, false, fmt.Errorf("decode legacy safe access settings migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacySafeAccessSettings(ctx context.Context, input LegacySafeAccessSettingsImport, now time.Time) (LegacySafeAccessSettingsImportReport, error) {
	if err := validateLegacySafeAccessSettingsImport(input); err != nil {
		return LegacySafeAccessSettingsImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacySafeAccessSettingsImportReport{}, fmt.Errorf("begin legacy safe access settings import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != CurrentSchemaVersion() {
		return LegacySafeAccessSettingsImportReport{}, fmt.Errorf("legacy safe access settings import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacySafeAccessSettingsImportReport{}, fmt.Errorf("validate legacy safe access settings target schema: %w", err)
	}
	if existing, found, err := lookupLegacySafeAccessSettingsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacySafeAccessSettingsImportReport{}, err
	} else if found {
		if existing.Settings.SourceChecksum != input.Checksum {
			return LegacySafeAccessSettingsImportReport{}, fmt.Errorf("%w: legacy safe access settings source options differ from their migration ledger", ErrConflict)
		}
		settings, err := readLegacySafeAccessSettingsTarget(ctx, tx)
		if err != nil {
			return LegacySafeAccessSettingsImportReport{}, fmt.Errorf("verify imported legacy safe access settings: %w", err)
		}
		if LegacySafeAccessSettingsChecksum(settings) != existing.Settings.TargetChecksum {
			return LegacySafeAccessSettingsImportReport{}, fmt.Errorf("%w: imported legacy safe access settings no longer match their migration ledger", ErrConflict)
		}
		if err := tx.Commit(); err != nil {
			return LegacySafeAccessSettingsImportReport{}, err
		}
		return existing, nil
	}
	var runs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacySafeAccessSettingsSlice).Scan(&runs); err != nil {
		return LegacySafeAccessSettingsImportReport{}, err
	}
	if runs != 0 {
		return LegacySafeAccessSettingsImportReport{}, fmt.Errorf("%w: legacy safe access settings were already imported from another snapshot", ErrConflict)
	}
	var revision int64
	var safeModeEnabled bool
	var securePath, appURL string
	if err := tx.QueryRowContext(ctx, `SELECT revision,safe_mode_enable,secure_path,app_url FROM app_settings WHERE id=1`).Scan(&revision, &safeModeEnabled, &securePath, &appURL); err != nil {
		return LegacySafeAccessSettingsImportReport{}, fmt.Errorf("read safe access settings migration target: %w", err)
	}
	if safeModeEnabled || (securePath != "" && securePath != input.FallbackSecurePath) {
		return LegacySafeAccessSettingsImportReport{}, fmt.Errorf("%w: legacy safe access settings import requires pristine safe access fields", ErrConflict)
	}
	if input.Settings.SafeModeEnabled && !validHTTPURL(appURL) {
		return LegacySafeAccessSettingsImportReport{}, fmt.Errorf("%w: safe mode import requires an existing valid site URL", ErrConflict)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE app_settings SET safe_mode_enable=?,secure_path=?,revision=revision+1,updated_by=NULL,updated_at=?
		WHERE id=1 AND revision=? AND safe_mode_enable=0 AND (secure_path='' OR secure_path=?)
	`, input.Settings.SafeModeEnabled, input.Settings.SecurePath, now.UTC().Unix(), revision, input.FallbackSecurePath)
	if err != nil {
		return LegacySafeAccessSettingsImportReport{}, fmt.Errorf("write legacy safe access settings: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return LegacySafeAccessSettingsImportReport{}, errors.New("legacy safe access settings target changed during import")
	}
	target, err := readLegacySafeAccessSettingsTarget(ctx, tx)
	if err != nil {
		return LegacySafeAccessSettingsImportReport{}, fmt.Errorf("verify legacy safe access settings: %w", err)
	}
	report := LegacySafeAccessSettingsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Settings: LegacyDomainResult{
			SourceRows: 1, TargetRows: 1, SourceChecksum: input.Checksum,
			TargetChecksum: LegacySafeAccessSettingsChecksum(target),
		},
		AppliedAt: now.UTC(),
	}
	if report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		return LegacySafeAccessSettingsImportReport{}, errors.New("legacy safe access settings target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacySafeAccessSettingsImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_migration_runs(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at) VALUES(?,?,?,?,?,?,?)`,
		report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacySafeAccessSettingsImportReport{}, fmt.Errorf("record legacy safe access settings migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacySafeAccessSettingsImportReport{}, err
	}
	return report, nil
}

func readLegacySafeAccessSettingsTarget(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (LegacySafeAccessSettings, error) {
	var settings LegacySafeAccessSettings
	if err := database.QueryRowContext(ctx, `SELECT safe_mode_enable,secure_path FROM app_settings WHERE id=1`).Scan(&settings.SafeModeEnabled, &settings.SecurePath); err != nil {
		return LegacySafeAccessSettings{}, err
	}
	return settings, nil
}

func validateLegacySafeAccessSettingsImport(input LegacySafeAccessSettingsImport) error {
	normalized, err := NormalizeLegacySafeAccessSettings(input.Settings)
	if err != nil || normalized != input.Settings || input.Slice != LegacySafeAccessSettingsSlice ||
		!validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		!validPersistedSecurePath(input.FallbackSecurePath) || input.FallbackSecurePath == "" ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) || strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 ||
		!validLowerSHA256(input.RollbackBackupSHA256) || input.Checksum != LegacySafeAccessSettingsChecksum(input.Settings) {
		return fmt.Errorf("%w: invalid legacy safe access settings import", ErrInvalidInput)
	}
	return nil
}
