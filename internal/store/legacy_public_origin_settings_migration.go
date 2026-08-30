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

const LegacyPublicOriginSettingsSlice = "public-origin-settings-v1"

type LegacyPublicOriginSettings struct {
	ForceHTTPS   bool   `json:"force_https"`
	SubscribeURL string `json:"subscribe_url"`
}

type LegacyPublicOriginSettingsImport struct {
	Slice, SourceSHA256                      string
	SourceSize                               int64
	Settings                                 LegacyPublicOriginSettings
	Checksum                                 string
	RollbackBackupPath, RollbackBackupSHA256 string
}

type LegacyPublicOriginSettingsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Settings             LegacyDomainResult `json:"settings"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func NormalizeLegacyPublicOriginSettings(settings LegacyPublicOriginSettings) (LegacyPublicOriginSettings, error) {
	normalized, err := normalizeSubscribeURLStorage(settings.SubscribeURL)
	if err != nil {
		return LegacyPublicOriginSettings{}, err
	}
	settings.SubscribeURL = normalized
	return settings, nil
}

func LegacyPublicOriginSettingsChecksum(settings LegacyPublicOriginSettings) string {
	return legacyCanonicalChecksum(settings)
}

func (s *Store) LookupLegacyPublicOriginSettingsImport(ctx context.Context, sourceSHA256 string) (LegacyPublicOriginSettingsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyPublicOriginSettingsImportReport{}, false, ErrInvalidInput
	}
	report, found, err := lookupLegacyPublicOriginSettingsImport(ctx, s.db, sourceSHA256)
	if err != nil || !found {
		return report, found, err
	}
	settings, err := readLegacyPublicOriginSettingsTarget(ctx, s.db)
	if err != nil {
		return LegacyPublicOriginSettingsImportReport{}, false, fmt.Errorf("verify imported legacy public origin settings: %w", err)
	}
	if LegacyPublicOriginSettingsChecksum(settings) != report.Settings.TargetChecksum {
		return LegacyPublicOriginSettingsImportReport{}, false, fmt.Errorf("%w: imported legacy public origin settings no longer match their migration ledger", ErrConflict)
	}
	return report, true, nil
}

func lookupLegacyPublicOriginSettingsImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyPublicOriginSettingsImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice=? AND source_sha256=?`, LegacyPublicOriginSettingsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyPublicOriginSettingsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyPublicOriginSettingsImportReport{}, false, fmt.Errorf("lookup legacy public origin settings migration: %w", err)
	}
	var report LegacyPublicOriginSettingsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyPublicOriginSettingsImportReport{}, false, fmt.Errorf("decode legacy public origin settings migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyPublicOriginSettings(ctx context.Context, input LegacyPublicOriginSettingsImport, now time.Time) (LegacyPublicOriginSettingsImportReport, error) {
	if err := validateLegacyPublicOriginSettingsImport(input); err != nil {
		return LegacyPublicOriginSettingsImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyPublicOriginSettingsImportReport{}, fmt.Errorf("begin legacy public origin settings import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != CurrentSchemaVersion() {
		return LegacyPublicOriginSettingsImportReport{}, fmt.Errorf("legacy public origin settings import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyPublicOriginSettingsImportReport{}, fmt.Errorf("validate legacy public origin settings target schema: %w", err)
	}
	if existing, found, err := lookupLegacyPublicOriginSettingsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyPublicOriginSettingsImportReport{}, err
	} else if found {
		settings, err := readLegacyPublicOriginSettingsTarget(ctx, tx)
		if err != nil {
			return LegacyPublicOriginSettingsImportReport{}, fmt.Errorf("verify imported legacy public origin settings: %w", err)
		}
		if LegacyPublicOriginSettingsChecksum(settings) != existing.Settings.TargetChecksum {
			return LegacyPublicOriginSettingsImportReport{}, fmt.Errorf("%w: imported legacy public origin settings no longer match their migration ledger", ErrConflict)
		}
		if err := tx.Commit(); err != nil {
			return LegacyPublicOriginSettingsImportReport{}, err
		}
		return existing, nil
	}
	var runs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyPublicOriginSettingsSlice).Scan(&runs); err != nil {
		return LegacyPublicOriginSettingsImportReport{}, err
	}
	if runs != 0 {
		return LegacyPublicOriginSettingsImportReport{}, fmt.Errorf("%w: legacy public origin settings were already imported from another snapshot", ErrConflict)
	}
	var revision int64
	var forceHTTPS bool
	var subscribeURL string
	if err := tx.QueryRowContext(ctx, `SELECT revision,force_https,subscribe_url FROM app_settings WHERE id=1`).Scan(&revision, &forceHTTPS, &subscribeURL); err != nil {
		return LegacyPublicOriginSettingsImportReport{}, fmt.Errorf("read public origin settings migration target: %w", err)
	}
	if forceHTTPS || subscribeURL != "" {
		return LegacyPublicOriginSettingsImportReport{}, fmt.Errorf("%w: legacy public origin settings import requires pristine public origin fields", ErrConflict)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE app_settings SET force_https=?,subscribe_url=?,revision=revision+1,updated_by=NULL,updated_at=?
		WHERE id=1 AND revision=? AND force_https=0 AND subscribe_url=''
	`, input.Settings.ForceHTTPS, input.Settings.SubscribeURL, now.UTC().Unix(), revision)
	if err != nil {
		return LegacyPublicOriginSettingsImportReport{}, fmt.Errorf("write legacy public origin settings: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return LegacyPublicOriginSettingsImportReport{}, errors.New("legacy public origin settings target changed during import")
	}
	target, err := readLegacyPublicOriginSettingsTarget(ctx, tx)
	if err != nil {
		return LegacyPublicOriginSettingsImportReport{}, fmt.Errorf("verify legacy public origin settings: %w", err)
	}
	report := LegacyPublicOriginSettingsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Settings: LegacyDomainResult{
			SourceRows: 1, TargetRows: 1, SourceChecksum: input.Checksum,
			TargetChecksum: LegacyPublicOriginSettingsChecksum(target),
		},
		AppliedAt: now.UTC(),
	}
	if report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		return LegacyPublicOriginSettingsImportReport{}, errors.New("legacy public origin settings target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyPublicOriginSettingsImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_migration_runs(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at) VALUES(?,?,?,?,?,?,?)`,
		report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyPublicOriginSettingsImportReport{}, fmt.Errorf("record legacy public origin settings migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyPublicOriginSettingsImportReport{}, err
	}
	return report, nil
}

func readLegacyPublicOriginSettingsTarget(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (LegacyPublicOriginSettings, error) {
	var settings LegacyPublicOriginSettings
	if err := database.QueryRowContext(ctx, `SELECT force_https,subscribe_url FROM app_settings WHERE id=1`).Scan(&settings.ForceHTTPS, &settings.SubscribeURL); err != nil {
		return LegacyPublicOriginSettings{}, err
	}
	return settings, nil
}

func validateLegacyPublicOriginSettingsImport(input LegacyPublicOriginSettingsImport) error {
	normalized, err := NormalizeLegacyPublicOriginSettings(input.Settings)
	if err != nil || normalized != input.Settings || input.Slice != LegacyPublicOriginSettingsSlice ||
		!validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) || strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 ||
		!validLowerSHA256(input.RollbackBackupSHA256) || input.Checksum != LegacyPublicOriginSettingsChecksum(input.Settings) {
		return fmt.Errorf("%w: invalid legacy public origin settings import", ErrInvalidInput)
	}
	return nil
}
