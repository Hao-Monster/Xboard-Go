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

const LegacySubscriptionPolicySettingsSlice = "subscription-policy-settings-v1"

type LegacySubscriptionPolicySettings struct {
	PlanChangeEnabled    bool `json:"plan_change_enable"`
	SurplusEnabled       bool `json:"surplus_enable"`
	NewOrderEventID      int  `json:"new_order_event_id"`
	RenewOrderEventID    int  `json:"renew_order_event_id"`
	ChangeOrderEventID   int  `json:"change_order_event_id"`
	DefaultRemindExpire  bool `json:"default_remind_expire"`
	DefaultRemindTraffic bool `json:"default_remind_traffic"`
}

type LegacySubscriptionPolicySettingsImport struct {
	Slice, SourceSHA256                      string
	SourceSize                               int64
	Settings                                 LegacySubscriptionPolicySettings
	Checksum                                 string
	RollbackBackupPath, RollbackBackupSHA256 string
}

type LegacySubscriptionPolicySettingsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Settings             LegacyDomainResult `json:"settings"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func DefaultLegacySubscriptionPolicySettings() LegacySubscriptionPolicySettings {
	return LegacySubscriptionPolicySettings{
		PlanChangeEnabled: true, SurplusEnabled: true,
		DefaultRemindExpire: true, DefaultRemindTraffic: true,
	}
}

func NormalizeLegacySubscriptionPolicySettings(settings LegacySubscriptionPolicySettings) (LegacySubscriptionPolicySettings, error) {
	if !validSubscriptionEventID(settings.NewOrderEventID) || !validSubscriptionEventID(settings.RenewOrderEventID) ||
		!validSubscriptionEventID(settings.ChangeOrderEventID) {
		return LegacySubscriptionPolicySettings{}, fmt.Errorf("%w: invalid legacy subscription policy event", ErrInvalidInput)
	}
	return settings, nil
}

func ValidateLegacySubscriptionPolicySettingsData(settings LegacySubscriptionPolicySettings) error {
	normalized, err := NormalizeLegacySubscriptionPolicySettings(settings)
	if err != nil || normalized != settings {
		return fmt.Errorf("%w: invalid legacy subscription policy settings", ErrInvalidInput)
	}
	return nil
}

func LegacySubscriptionPolicySettingsChecksum(settings LegacySubscriptionPolicySettings) string {
	return legacyCanonicalChecksum(settings)
}

func (s *Store) LookupLegacySubscriptionPolicySettingsImport(ctx context.Context, sourceSHA256 string) (LegacySubscriptionPolicySettingsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacySubscriptionPolicySettingsImportReport{}, false, ErrInvalidInput
	}
	report, found, err := lookupLegacySubscriptionPolicySettingsImport(ctx, s.db, sourceSHA256)
	if err != nil || !found {
		return report, found, err
	}
	target, err := readLegacySubscriptionPolicySettingsTarget(ctx, s.db)
	if err != nil {
		return LegacySubscriptionPolicySettingsImportReport{}, false, fmt.Errorf("verify imported legacy subscription policy settings: %w", err)
	}
	if LegacySubscriptionPolicySettingsChecksum(target) != report.Settings.TargetChecksum {
		return LegacySubscriptionPolicySettingsImportReport{}, false, fmt.Errorf("%w: imported legacy subscription policy settings no longer match their migration ledger", ErrConflict)
	}
	return report, true, nil
}

func lookupLegacySubscriptionPolicySettingsImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacySubscriptionPolicySettingsImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice=? AND source_sha256=?`, LegacySubscriptionPolicySettingsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacySubscriptionPolicySettingsImportReport{}, false, nil
	}
	if err != nil {
		return LegacySubscriptionPolicySettingsImportReport{}, false, fmt.Errorf("lookup legacy subscription policy settings migration: %w", err)
	}
	var report LegacySubscriptionPolicySettingsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacySubscriptionPolicySettingsImportReport{}, false, fmt.Errorf("decode legacy subscription policy settings migration report: %w", err)
	}
	if report.Slice != LegacySubscriptionPolicySettingsSlice || report.SourceSHA256 != sourceSHA256 {
		return LegacySubscriptionPolicySettingsImportReport{}, false, fmt.Errorf("%w: invalid legacy subscription policy migration ledger", ErrConflict)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacySubscriptionPolicySettings(ctx context.Context, input LegacySubscriptionPolicySettingsImport, now time.Time) (LegacySubscriptionPolicySettingsImportReport, error) {
	if err := validateLegacySubscriptionPolicySettingsImport(input); err != nil {
		return LegacySubscriptionPolicySettingsImportReport{}, err
	}
	if now.Unix() < 0 {
		return LegacySubscriptionPolicySettingsImportReport{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacySubscriptionPolicySettingsImportReport{}, fmt.Errorf("begin legacy subscription policy settings import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != CurrentSchemaVersion() {
		return LegacySubscriptionPolicySettingsImportReport{}, fmt.Errorf("legacy subscription policy settings import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacySubscriptionPolicySettingsImportReport{}, fmt.Errorf("validate legacy subscription policy settings target schema: %w", err)
	}
	if existing, found, err := lookupLegacySubscriptionPolicySettingsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacySubscriptionPolicySettingsImportReport{}, err
	} else if found {
		if existing.Settings.SourceChecksum != input.Checksum {
			return LegacySubscriptionPolicySettingsImportReport{}, fmt.Errorf("%w: legacy subscription policy settings source differs from its migration ledger", ErrConflict)
		}
		target, err := readLegacySubscriptionPolicySettingsTarget(ctx, tx)
		if err != nil || LegacySubscriptionPolicySettingsChecksum(target) != existing.Settings.TargetChecksum {
			return LegacySubscriptionPolicySettingsImportReport{}, fmt.Errorf("%w: imported legacy subscription policy settings no longer match their migration ledger", ErrConflict)
		}
		if err := tx.Commit(); err != nil {
			return LegacySubscriptionPolicySettingsImportReport{}, err
		}
		return existing, nil
	}
	var runs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacySubscriptionPolicySettingsSlice).Scan(&runs); err != nil {
		return LegacySubscriptionPolicySettingsImportReport{}, err
	}
	if runs != 0 {
		return LegacySubscriptionPolicySettingsImportReport{}, fmt.Errorf("%w: legacy subscription policy settings were already imported from another snapshot", ErrConflict)
	}
	var revision int64
	current, err := readLegacySubscriptionPolicySettingsTargetWithRevision(ctx, tx, &revision)
	if err != nil {
		return LegacySubscriptionPolicySettingsImportReport{}, fmt.Errorf("read legacy subscription policy settings migration target: %w", err)
	}
	if current != DefaultLegacySubscriptionPolicySettings() {
		return LegacySubscriptionPolicySettingsImportReport{}, fmt.Errorf("%w: legacy subscription policy settings import requires pristine policy fields", ErrConflict)
	}
	result, err := tx.ExecContext(ctx, `UPDATE app_settings SET
		plan_change_enable=?,surplus_enable=?,new_order_event_id=?,renew_order_event_id=?,change_order_event_id=?,
		default_remind_expire=?,default_remind_traffic=?,revision=revision+1,updated_by=NULL,updated_at=?
		WHERE id=1 AND revision=?`,
		input.Settings.PlanChangeEnabled, input.Settings.SurplusEnabled, input.Settings.NewOrderEventID,
		input.Settings.RenewOrderEventID, input.Settings.ChangeOrderEventID,
		input.Settings.DefaultRemindExpire, input.Settings.DefaultRemindTraffic, now.UTC().Unix(), revision)
	if err != nil {
		return LegacySubscriptionPolicySettingsImportReport{}, fmt.Errorf("write legacy subscription policy settings: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return LegacySubscriptionPolicySettingsImportReport{}, errors.New("legacy subscription policy settings target changed during import")
	}
	target, err := readLegacySubscriptionPolicySettingsTarget(ctx, tx)
	if err != nil || target != input.Settings {
		return LegacySubscriptionPolicySettingsImportReport{}, errors.New("legacy subscription policy settings target verification does not match source")
	}
	report := LegacySubscriptionPolicySettingsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Settings:  LegacyDomainResult{SourceRows: 1, TargetRows: 1, SourceChecksum: input.Checksum, TargetChecksum: LegacySubscriptionPolicySettingsChecksum(target)},
		AppliedAt: now.UTC(),
	}
	if report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		return LegacySubscriptionPolicySettingsImportReport{}, errors.New("legacy subscription policy settings target checksum does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacySubscriptionPolicySettingsImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_migration_runs(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at) VALUES(?,?,?,?,?,?,?)`,
		report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacySubscriptionPolicySettingsImportReport{}, fmt.Errorf("record legacy subscription policy settings migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacySubscriptionPolicySettingsImportReport{}, err
	}
	return report, nil
}

func readLegacySubscriptionPolicySettingsTarget(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (LegacySubscriptionPolicySettings, error) {
	var revision int64
	return readLegacySubscriptionPolicySettingsTargetWithRevision(ctx, database, &revision)
}

func readLegacySubscriptionPolicySettingsTargetWithRevision(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, revision *int64) (LegacySubscriptionPolicySettings, error) {
	var settings LegacySubscriptionPolicySettings
	err := database.QueryRowContext(ctx, `SELECT revision,plan_change_enable,surplus_enable,new_order_event_id,renew_order_event_id,
		change_order_event_id,default_remind_expire,default_remind_traffic FROM app_settings WHERE id=1`).Scan(
		revision, &settings.PlanChangeEnabled, &settings.SurplusEnabled, &settings.NewOrderEventID,
		&settings.RenewOrderEventID, &settings.ChangeOrderEventID, &settings.DefaultRemindExpire, &settings.DefaultRemindTraffic)
	return settings, err
}

func validateLegacySubscriptionPolicySettingsImport(input LegacySubscriptionPolicySettingsImport) error {
	if input.Slice != LegacySubscriptionPolicySettingsSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) || strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 ||
		!validLowerSHA256(input.RollbackBackupSHA256) || input.Checksum != LegacySubscriptionPolicySettingsChecksum(input.Settings) {
		return fmt.Errorf("%w: invalid legacy subscription policy settings import", ErrInvalidInput)
	}
	return ValidateLegacySubscriptionPolicySettingsData(input.Settings)
}
