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

const LegacyRegistrationTrialSettingsSlice = "registration-trial-settings-v1"

type LegacyRegistrationTrialSettings struct {
	PlanID int64 `json:"try_out_plan_id"`
	Hours  int   `json:"try_out_hour"`
}

type LegacyRegistrationTrialSettingsImport struct {
	Slice, SourceSHA256                      string
	SourceSize                               int64
	Settings                                 LegacyRegistrationTrialSettings
	Checksum                                 string
	RollbackBackupPath, RollbackBackupSHA256 string
}

type LegacyRegistrationTrialSettingsImportReport struct {
	Slice                 string             `json:"slice"`
	SourceSHA256          string             `json:"source_sha256"`
	SourceSize            int64              `json:"source_size"`
	RollbackBackupPath    string             `json:"rollback_backup_path"`
	RollbackBackupSHA256  string             `json:"rollback_backup_sha256"`
	Settings              LegacyDomainResult `json:"settings"`
	NormalizedMissingPlan bool               `json:"normalized_missing_plan"`
	AppliedAt             time.Time          `json:"applied_at"`
	AlreadyApplied        bool               `json:"already_applied"`
}

func LegacyRegistrationTrialSettingsChecksum(settings LegacyRegistrationTrialSettings) string {
	return legacyCanonicalChecksum(settings)
}

func ValidateLegacyRegistrationTrialSettingsData(settings LegacyRegistrationTrialSettings) error {
	if settings.PlanID < 0 || settings.Hours < 1 || settings.Hours > 8760 {
		return fmt.Errorf("%w: invalid legacy registration trial settings", ErrInvalidInput)
	}
	return nil
}

func (s *Store) LookupLegacyRegistrationTrialSettingsImport(ctx context.Context, sourceSHA256 string) (LegacyRegistrationTrialSettingsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyRegistrationTrialSettingsImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacyRegistrationTrialSettingsImport(ctx, s.db, sourceSHA256)
}

func lookupLegacyRegistrationTrialSettingsImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyRegistrationTrialSettingsImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice=? AND source_sha256=?`, LegacyRegistrationTrialSettingsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyRegistrationTrialSettingsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyRegistrationTrialSettingsImportReport{}, false, fmt.Errorf("lookup legacy registration trial settings migration: %w", err)
	}
	var report LegacyRegistrationTrialSettingsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyRegistrationTrialSettingsImportReport{}, false, fmt.Errorf("decode legacy registration trial settings migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyRegistrationTrialSettings(ctx context.Context, input LegacyRegistrationTrialSettingsImport, now time.Time) (LegacyRegistrationTrialSettingsImportReport, error) {
	if err := validateLegacyRegistrationTrialSettingsImport(input); err != nil {
		return LegacyRegistrationTrialSettingsImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyRegistrationTrialSettingsImportReport{}, fmt.Errorf("begin legacy registration trial settings import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != CurrentSchemaVersion() {
		return LegacyRegistrationTrialSettingsImportReport{}, fmt.Errorf("legacy registration trial settings import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyRegistrationTrialSettingsImportReport{}, fmt.Errorf("validate legacy registration trial settings target schema: %w", err)
	}
	if existing, found, err := lookupLegacyRegistrationTrialSettingsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyRegistrationTrialSettingsImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyRegistrationTrialSettingsImportReport{}, err
		}
		return existing, nil
	}
	var runs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyRegistrationTrialSettingsSlice).Scan(&runs); err != nil {
		return LegacyRegistrationTrialSettingsImportReport{}, err
	}
	if runs != 0 {
		return LegacyRegistrationTrialSettingsImportReport{}, fmt.Errorf("%w: legacy registration trial settings were already imported from another snapshot", ErrConflict)
	}
	var currentPlanID int64
	var currentHours int
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT try_out_plan_id,try_out_hour,revision FROM app_settings WHERE id=1`).Scan(&currentPlanID, &currentHours, &revision); err != nil {
		return LegacyRegistrationTrialSettingsImportReport{}, err
	}
	if currentPlanID != 0 || currentHours != 1 {
		return LegacyRegistrationTrialSettingsImportReport{}, fmt.Errorf("%w: legacy registration trial settings import requires default target trial settings", ErrConflict)
	}
	target := input.Settings
	normalizedMissingPlan := false
	if target.PlanID > 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plans WHERE id=?)`, target.PlanID).Scan(&exists); err != nil {
			return LegacyRegistrationTrialSettingsImportReport{}, err
		}
		if !exists {
			target = LegacyRegistrationTrialSettings{PlanID: 0, Hours: 1}
			normalizedMissingPlan = true
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE app_settings SET try_out_plan_id=?,try_out_hour=?,revision=revision+1,updated_at=? WHERE id=1 AND revision=?`, target.PlanID, target.Hours, now.UTC().Unix(), revision)
	if err != nil {
		return LegacyRegistrationTrialSettingsImportReport{}, fmt.Errorf("write legacy registration trial settings: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		return LegacyRegistrationTrialSettingsImportReport{}, fmt.Errorf("%w: registration trial settings changed during import", ErrConflict)
	}
	var actual LegacyRegistrationTrialSettings
	if err := tx.QueryRowContext(ctx, `SELECT try_out_plan_id,try_out_hour FROM app_settings WHERE id=1`).Scan(&actual.PlanID, &actual.Hours); err != nil {
		return LegacyRegistrationTrialSettingsImportReport{}, err
	}
	if actual != target {
		return LegacyRegistrationTrialSettingsImportReport{}, errors.New("legacy registration trial settings target verification does not match effective target")
	}
	report := LegacyRegistrationTrialSettingsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Settings:              LegacyDomainResult{SourceRows: 1, TargetRows: 1, SourceChecksum: input.Checksum, TargetChecksum: LegacyRegistrationTrialSettingsChecksum(actual)},
		NormalizedMissingPlan: normalizedMissingPlan, AppliedAt: now.UTC(),
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyRegistrationTrialSettingsImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_migration_runs(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at) VALUES(?,?,?,?,?,?,?)`,
		report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyRegistrationTrialSettingsImportReport{}, fmt.Errorf("record legacy registration trial settings migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyRegistrationTrialSettingsImportReport{}, err
	}
	return report, nil
}

func validateLegacyRegistrationTrialSettingsImport(input LegacyRegistrationTrialSettingsImport) error {
	if input.Slice != LegacyRegistrationTrialSettingsSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) || strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 ||
		!validLowerSHA256(input.RollbackBackupSHA256) || input.Checksum != LegacyRegistrationTrialSettingsChecksum(input.Settings) {
		return fmt.Errorf("%w: invalid legacy registration trial settings import", ErrInvalidInput)
	}
	return ValidateLegacyRegistrationTrialSettingsData(input.Settings)
}
