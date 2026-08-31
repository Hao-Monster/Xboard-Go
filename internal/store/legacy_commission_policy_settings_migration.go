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

const LegacyCommissionPolicySettingsSlice = "commission-policy-settings-v1"

type LegacyCommissionPolicySettings struct {
	InviteCommission    int  `json:"invite_commission"`
	FirstTimeEnabled    bool `json:"commission_first_time_enable"`
	AutoCheckEnabled    bool `json:"commission_auto_check_enable"`
	WithdrawClosed      bool `json:"withdraw_close_enable"`
	DistributionEnabled bool `json:"commission_distribution_enable"`
	DistributionL1      int  `json:"commission_distribution_l1"`
	DistributionL2      int  `json:"commission_distribution_l2"`
	DistributionL3      int  `json:"commission_distribution_l3"`
}

type LegacyCommissionPolicySettingsImport struct {
	Slice, SourceSHA256                      string
	SourceSize                               int64
	Settings                                 LegacyCommissionPolicySettings
	Checksum                                 string
	RollbackBackupPath, RollbackBackupSHA256 string
}

type LegacyCommissionPolicySettingsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Settings             LegacyDomainResult `json:"settings"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

// DefaultLegacyCommissionPolicySettings mirrors the old controller and
// shipped administration UI. Missing distribution levels rendered as zero.
func DefaultLegacyCommissionPolicySettings() LegacyCommissionPolicySettings {
	return LegacyCommissionPolicySettings{
		InviteCommission: 10, FirstTimeEnabled: true, AutoCheckEnabled: true,
	}
}

// The Go schema stores 100 for L1 so a newly created configuration is explicit.
// The migration must recognize that pristine state without treating it as the
// old source default, whose shipped UI displayed a missing L1 as zero.
func pristineLegacyCommissionPolicySettingsTarget() LegacyCommissionPolicySettings {
	settings := DefaultLegacyCommissionPolicySettings()
	settings.DistributionL1 = 100
	return settings
}

func NormalizeLegacyCommissionPolicySettings(settings LegacyCommissionPolicySettings) (LegacyCommissionPolicySettings, error) {
	percentages := [...]int{
		settings.InviteCommission,
		settings.DistributionL1,
		settings.DistributionL2,
		settings.DistributionL3,
	}
	for _, percentage := range percentages {
		if percentage < 0 || percentage > 100 {
			return LegacyCommissionPolicySettings{}, fmt.Errorf("%w: invalid legacy commission percentage", ErrInvalidInput)
		}
	}
	if settings.DistributionL1+settings.DistributionL2+settings.DistributionL3 > 100 {
		return LegacyCommissionPolicySettings{}, fmt.Errorf("%w: invalid legacy commission distribution", ErrInvalidInput)
	}
	return settings, nil
}

func ValidateLegacyCommissionPolicySettingsData(settings LegacyCommissionPolicySettings) error {
	normalized, err := NormalizeLegacyCommissionPolicySettings(settings)
	if err != nil || normalized != settings {
		return fmt.Errorf("%w: invalid legacy commission policy settings", ErrInvalidInput)
	}
	return nil
}

func LegacyCommissionPolicySettingsChecksum(settings LegacyCommissionPolicySettings) string {
	return legacyCanonicalChecksum(settings)
}

func (s *Store) LookupLegacyCommissionPolicySettingsImport(ctx context.Context, sourceSHA256 string) (LegacyCommissionPolicySettingsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyCommissionPolicySettingsImportReport{}, false, ErrInvalidInput
	}
	report, found, err := lookupLegacyCommissionPolicySettingsImport(ctx, s.db, sourceSHA256)
	if err != nil || !found {
		return report, found, err
	}
	target, err := readLegacyCommissionPolicySettingsTarget(ctx, s.db)
	if err != nil {
		return LegacyCommissionPolicySettingsImportReport{}, false, fmt.Errorf("verify imported legacy commission policy settings: %w", err)
	}
	if LegacyCommissionPolicySettingsChecksum(target) != report.Settings.TargetChecksum {
		return LegacyCommissionPolicySettingsImportReport{}, false, fmt.Errorf("%w: imported legacy commission policy settings no longer match their migration ledger", ErrConflict)
	}
	return report, true, nil
}

func lookupLegacyCommissionPolicySettingsImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyCommissionPolicySettingsImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice=? AND source_sha256=?`, LegacyCommissionPolicySettingsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyCommissionPolicySettingsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyCommissionPolicySettingsImportReport{}, false, fmt.Errorf("lookup legacy commission policy settings migration: %w", err)
	}
	var report LegacyCommissionPolicySettingsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyCommissionPolicySettingsImportReport{}, false, fmt.Errorf("decode legacy commission policy settings migration report: %w", err)
	}
	if report.Slice != LegacyCommissionPolicySettingsSlice || report.SourceSHA256 != sourceSHA256 {
		return LegacyCommissionPolicySettingsImportReport{}, false, fmt.Errorf("%w: invalid legacy commission policy migration ledger", ErrConflict)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyCommissionPolicySettings(ctx context.Context, input LegacyCommissionPolicySettingsImport, now time.Time) (LegacyCommissionPolicySettingsImportReport, error) {
	if err := validateLegacyCommissionPolicySettingsImport(input); err != nil {
		return LegacyCommissionPolicySettingsImportReport{}, err
	}
	if now.Unix() < 0 {
		return LegacyCommissionPolicySettingsImportReport{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyCommissionPolicySettingsImportReport{}, fmt.Errorf("begin legacy commission policy settings import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != CurrentSchemaVersion() {
		return LegacyCommissionPolicySettingsImportReport{}, fmt.Errorf("legacy commission policy settings import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyCommissionPolicySettingsImportReport{}, fmt.Errorf("validate legacy commission policy settings target schema: %w", err)
	}
	if existing, found, err := lookupLegacyCommissionPolicySettingsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyCommissionPolicySettingsImportReport{}, err
	} else if found {
		if existing.Settings.SourceChecksum != input.Checksum {
			return LegacyCommissionPolicySettingsImportReport{}, fmt.Errorf("%w: legacy commission policy settings source differs from its migration ledger", ErrConflict)
		}
		target, err := readLegacyCommissionPolicySettingsTarget(ctx, tx)
		if err != nil || LegacyCommissionPolicySettingsChecksum(target) != existing.Settings.TargetChecksum {
			return LegacyCommissionPolicySettingsImportReport{}, fmt.Errorf("%w: imported legacy commission policy settings no longer match their migration ledger", ErrConflict)
		}
		if err := tx.Commit(); err != nil {
			return LegacyCommissionPolicySettingsImportReport{}, err
		}
		return existing, nil
	}
	var runs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyCommissionPolicySettingsSlice).Scan(&runs); err != nil {
		return LegacyCommissionPolicySettingsImportReport{}, err
	}
	if runs != 0 {
		return LegacyCommissionPolicySettingsImportReport{}, fmt.Errorf("%w: legacy commission policy settings were already imported from another snapshot", ErrConflict)
	}
	var revision int64
	current, err := readLegacyCommissionPolicySettingsTargetWithRevision(ctx, tx, &revision)
	if err != nil {
		return LegacyCommissionPolicySettingsImportReport{}, fmt.Errorf("read legacy commission policy settings migration target: %w", err)
	}
	if current != pristineLegacyCommissionPolicySettingsTarget() {
		return LegacyCommissionPolicySettingsImportReport{}, fmt.Errorf("%w: legacy commission policy settings import requires pristine policy fields", ErrConflict)
	}
	result, err := tx.ExecContext(ctx, `UPDATE app_settings SET
		invite_commission=?,commission_first_time_enable=?,commission_auto_check_enable=?,withdraw_close_enable=?,
		commission_distribution_enable=?,commission_distribution_l1=?,commission_distribution_l2=?,commission_distribution_l3=?,
		revision=revision+1,updated_by=NULL,updated_at=? WHERE id=1 AND revision=?`,
		input.Settings.InviteCommission, input.Settings.FirstTimeEnabled, input.Settings.AutoCheckEnabled,
		input.Settings.WithdrawClosed, input.Settings.DistributionEnabled, input.Settings.DistributionL1,
		input.Settings.DistributionL2, input.Settings.DistributionL3, now.UTC().Unix(), revision)
	if err != nil {
		return LegacyCommissionPolicySettingsImportReport{}, fmt.Errorf("write legacy commission policy settings: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return LegacyCommissionPolicySettingsImportReport{}, errors.New("legacy commission policy settings target changed during import")
	}
	target, err := readLegacyCommissionPolicySettingsTarget(ctx, tx)
	if err != nil || target != input.Settings {
		return LegacyCommissionPolicySettingsImportReport{}, errors.New("legacy commission policy settings target verification does not match source")
	}
	report := LegacyCommissionPolicySettingsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Settings: LegacyDomainResult{SourceRows: 1, TargetRows: 1, SourceChecksum: input.Checksum,
			TargetChecksum: LegacyCommissionPolicySettingsChecksum(target)},
		AppliedAt: now.UTC(),
	}
	if report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		return LegacyCommissionPolicySettingsImportReport{}, errors.New("legacy commission policy settings target checksum does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyCommissionPolicySettingsImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_migration_runs(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at) VALUES(?,?,?,?,?,?,?)`,
		report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyCommissionPolicySettingsImportReport{}, fmt.Errorf("record legacy commission policy settings migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyCommissionPolicySettingsImportReport{}, err
	}
	return report, nil
}

func readLegacyCommissionPolicySettingsTarget(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (LegacyCommissionPolicySettings, error) {
	var revision int64
	return readLegacyCommissionPolicySettingsTargetWithRevision(ctx, database, &revision)
}

func readLegacyCommissionPolicySettingsTargetWithRevision(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, revision *int64) (LegacyCommissionPolicySettings, error) {
	var settings LegacyCommissionPolicySettings
	err := database.QueryRowContext(ctx, `SELECT revision,invite_commission,commission_first_time_enable,
		commission_auto_check_enable,withdraw_close_enable,commission_distribution_enable,
		commission_distribution_l1,commission_distribution_l2,commission_distribution_l3
		FROM app_settings WHERE id=1`).Scan(revision, &settings.InviteCommission, &settings.FirstTimeEnabled,
		&settings.AutoCheckEnabled, &settings.WithdrawClosed, &settings.DistributionEnabled,
		&settings.DistributionL1, &settings.DistributionL2, &settings.DistributionL3)
	return settings, err
}

func validateLegacyCommissionPolicySettingsImport(input LegacyCommissionPolicySettingsImport) error {
	if input.Slice != LegacyCommissionPolicySettingsSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) || strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 ||
		!validLowerSHA256(input.RollbackBackupSHA256) || input.Checksum != LegacyCommissionPolicySettingsChecksum(input.Settings) {
		return fmt.Errorf("%w: invalid legacy commission policy settings import", ErrInvalidInput)
	}
	return ValidateLegacyCommissionPolicySettingsData(input.Settings)
}
