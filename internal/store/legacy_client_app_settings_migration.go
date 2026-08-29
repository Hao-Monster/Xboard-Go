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

const LegacyClientAppSettingsSlice = "client-app-settings-v1"

type LegacyClientAppSettings struct {
	WindowsVersion     string `json:"windows_version"`
	WindowsDownloadURL string `json:"windows_download_url"`
	MacOSVersion       string `json:"macos_version"`
	MacOSDownloadURL   string `json:"macos_download_url"`
	AndroidVersion     string `json:"android_version"`
	AndroidDownloadURL string `json:"android_download_url"`
}

type LegacyClientAppSettingsImport struct {
	Slice, SourceSHA256                      string
	SourceSize                               int64
	Settings                                 LegacyClientAppSettings
	Checksum                                 string
	RollbackBackupPath, RollbackBackupSHA256 string
}

type LegacyClientAppSettingsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Settings             LegacyDomainResult `json:"settings"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func NormalizeLegacyClientAppSettings(settings LegacyClientAppSettings) (LegacyClientAppSettings, error) {
	normalized, err := normalizeClientAppSettings(SaveClientAppSettingsInput(settings))
	if err != nil {
		return LegacyClientAppSettings{}, err
	}
	return LegacyClientAppSettings(normalized), nil
}

func LegacyClientAppSettingsChecksum(settings LegacyClientAppSettings) string {
	return legacyCanonicalChecksum(settings)
}

func (s *Store) LookupLegacyClientAppSettingsImport(ctx context.Context, sourceSHA256 string) (LegacyClientAppSettingsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyClientAppSettingsImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacyClientAppSettingsImport(ctx, s.db, sourceSHA256)
}

func lookupLegacyClientAppSettingsImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyClientAppSettingsImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice=? AND source_sha256=?`, LegacyClientAppSettingsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyClientAppSettingsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyClientAppSettingsImportReport{}, false, fmt.Errorf("lookup legacy client app settings migration: %w", err)
	}
	var report LegacyClientAppSettingsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyClientAppSettingsImportReport{}, false, fmt.Errorf("decode legacy client app settings migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyClientAppSettings(ctx context.Context, input LegacyClientAppSettingsImport, now time.Time) (LegacyClientAppSettingsImportReport, error) {
	if err := validateLegacyClientAppSettingsImport(input); err != nil {
		return LegacyClientAppSettingsImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyClientAppSettingsImportReport{}, fmt.Errorf("begin legacy client app settings import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != CurrentSchemaVersion() {
		return LegacyClientAppSettingsImportReport{}, fmt.Errorf("legacy client app settings import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyClientAppSettingsImportReport{}, fmt.Errorf("validate legacy client app settings target schema: %w", err)
	}
	if existing, found, err := lookupLegacyClientAppSettingsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyClientAppSettingsImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyClientAppSettingsImportReport{}, err
		}
		return existing, nil
	}
	var runs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyClientAppSettingsSlice).Scan(&runs); err != nil {
		return LegacyClientAppSettingsImportReport{}, err
	}
	if runs != 0 {
		return LegacyClientAppSettingsImportReport{}, fmt.Errorf("%w: legacy client app settings were already imported from another snapshot", ErrConflict)
	}
	current, err := readClientAppSettings(ctx, tx)
	if err != nil {
		return LegacyClientAppSettingsImportReport{}, fmt.Errorf("read client app settings migration target: %w", err)
	}
	if current.Revision != 1 || current.WindowsVersion != "" || current.WindowsDownloadURL != "" ||
		current.MacOSVersion != "" || current.MacOSDownloadURL != "" || current.AndroidVersion != "" ||
		current.AndroidDownloadURL != "" || !current.UpdatedAt.Equal(time.Unix(0, 0).UTC()) {
		return LegacyClientAppSettingsImportReport{}, fmt.Errorf("%w: legacy client app settings import requires a pristine target", ErrConflict)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE client_app_settings SET windows_version=?,windows_download_url=?,macos_version=?,macos_download_url=?,
		android_version=?,android_download_url=?,revision=2,updated_by=NULL,updated_at=? WHERE id=1 AND revision=1
	`, input.Settings.WindowsVersion, input.Settings.WindowsDownloadURL, input.Settings.MacOSVersion, input.Settings.MacOSDownloadURL,
		input.Settings.AndroidVersion, input.Settings.AndroidDownloadURL, now.UTC().Unix())
	if err != nil {
		return LegacyClientAppSettingsImportReport{}, fmt.Errorf("write legacy client app settings: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return LegacyClientAppSettingsImportReport{}, errors.New("legacy client app settings target changed during import")
	}
	target, err := readClientAppSettings(ctx, tx)
	if err != nil {
		return LegacyClientAppSettingsImportReport{}, fmt.Errorf("verify legacy client app settings: %w", err)
	}
	targetSettings := LegacyClientAppSettings{
		WindowsVersion: target.WindowsVersion, WindowsDownloadURL: target.WindowsDownloadURL,
		MacOSVersion: target.MacOSVersion, MacOSDownloadURL: target.MacOSDownloadURL,
		AndroidVersion: target.AndroidVersion, AndroidDownloadURL: target.AndroidDownloadURL,
	}
	report := LegacyClientAppSettingsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Settings:  LegacyDomainResult{SourceRows: 1, TargetRows: 1, SourceChecksum: input.Checksum, TargetChecksum: LegacyClientAppSettingsChecksum(targetSettings)},
		AppliedAt: now.UTC(),
	}
	if report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		return LegacyClientAppSettingsImportReport{}, errors.New("legacy client app settings target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyClientAppSettingsImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_migration_runs(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at) VALUES(?,?,?,?,?,?,?)`,
		report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyClientAppSettingsImportReport{}, fmt.Errorf("record legacy client app settings migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyClientAppSettingsImportReport{}, err
	}
	return report, nil
}

func validateLegacyClientAppSettingsImport(input LegacyClientAppSettingsImport) error {
	normalized, err := NormalizeLegacyClientAppSettings(input.Settings)
	if err != nil || normalized != input.Settings || input.Slice != LegacyClientAppSettingsSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) || strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 ||
		!validLowerSHA256(input.RollbackBackupSHA256) || input.Checksum != LegacyClientAppSettingsChecksum(input.Settings) {
		return fmt.Errorf("%w: invalid legacy client app settings import", ErrInvalidInput)
	}
	return nil
}
