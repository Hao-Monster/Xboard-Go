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

const LegacyConfigurationCompatibilitySettingsSlice = "configuration-compat-settings-v1"

type LegacyConfigurationCompatibilitySettings struct {
	CommissionWithdrawLimit  CurrencyAmount `json:"commission_withdraw_limit"`
	CommissionWithdrawMethod []string       `json:"commission_withdraw_method"`
	SidebarStyle             string         `json:"frontend_theme_sidebar"`
	HeaderStyle              string         `json:"frontend_theme_header"`
}

type LegacyConfigurationCompatibilitySettingsImport struct {
	Slice, SourceSHA256                      string
	SourceSize                               int64
	Settings                                 LegacyConfigurationCompatibilitySettings
	Checksum                                 string
	RollbackBackupPath, RollbackBackupSHA256 string
}

type LegacyConfigurationCompatibilitySettingsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Settings             LegacyDomainResult `json:"settings"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func NormalizeLegacyConfigurationCompatibilitySettings(settings LegacyConfigurationCompatibilitySettings) (LegacyConfigurationCompatibilitySettings, error) {
	settings.SidebarStyle = strings.TrimSpace(settings.SidebarStyle)
	settings.HeaderStyle = strings.TrimSpace(settings.HeaderStyle)
	settings.CommissionWithdrawMethod = append([]string{}, settings.CommissionWithdrawMethod...)
	if !validCommissionWithdrawLimit(settings.CommissionWithdrawLimit) ||
		!validCommissionWithdrawMethods(settings.CommissionWithdrawMethod) ||
		!validThemeLayoutStyle(settings.SidebarStyle) || !validThemeLayoutStyle(settings.HeaderStyle) {
		return LegacyConfigurationCompatibilitySettings{}, fmt.Errorf("%w: invalid legacy configuration compatibility settings", ErrInvalidInput)
	}
	return settings, nil
}

func LegacyConfigurationCompatibilitySettingsChecksum(settings LegacyConfigurationCompatibilitySettings) string {
	return legacyCanonicalChecksum(settings)
}

func (s *Store) LookupLegacyConfigurationCompatibilitySettingsImport(ctx context.Context, sourceSHA256 string) (LegacyConfigurationCompatibilitySettingsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, false, ErrInvalidInput
	}
	report, found, err := lookupLegacyConfigurationCompatibilitySettingsImport(ctx, s.db, sourceSHA256)
	if err != nil || !found {
		return report, found, err
	}
	target, err := readLegacyConfigurationCompatibilitySettingsTarget(ctx, s.db)
	if err != nil {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, false, fmt.Errorf("verify imported legacy configuration compatibility settings: %w", err)
	}
	if LegacyConfigurationCompatibilitySettingsChecksum(target) != report.Settings.TargetChecksum {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, false, fmt.Errorf("%w: imported legacy configuration compatibility settings no longer match their migration ledger", ErrConflict)
	}
	return report, true, nil
}

func lookupLegacyConfigurationCompatibilitySettingsImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyConfigurationCompatibilitySettingsImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `
		SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?
	`, LegacyConfigurationCompatibilitySettingsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, false, fmt.Errorf("lookup legacy configuration compatibility settings migration: %w", err)
	}
	var report LegacyConfigurationCompatibilitySettingsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, false, fmt.Errorf("decode legacy configuration compatibility settings migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyConfigurationCompatibilitySettings(ctx context.Context, input LegacyConfigurationCompatibilitySettingsImport, now time.Time) (LegacyConfigurationCompatibilitySettingsImportReport, error) {
	if err := validateLegacyConfigurationCompatibilitySettingsImport(input); err != nil {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, fmt.Errorf("begin legacy configuration compatibility settings import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != CurrentSchemaVersion() {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, fmt.Errorf("legacy configuration compatibility settings import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, fmt.Errorf("validate legacy configuration compatibility settings target schema: %w", err)
	}
	if existing, found, err := lookupLegacyConfigurationCompatibilitySettingsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, err
	} else if found {
		if existing.Settings.SourceChecksum != input.Checksum {
			return LegacyConfigurationCompatibilitySettingsImportReport{}, fmt.Errorf("%w: legacy configuration compatibility settings source options differ from their migration ledger", ErrConflict)
		}
		target, err := readLegacyConfigurationCompatibilitySettingsTarget(ctx, tx)
		if err != nil {
			return LegacyConfigurationCompatibilitySettingsImportReport{}, err
		}
		if LegacyConfigurationCompatibilitySettingsChecksum(target) != existing.Settings.TargetChecksum {
			return LegacyConfigurationCompatibilitySettingsImportReport{}, fmt.Errorf("%w: imported legacy configuration compatibility settings no longer match their migration ledger", ErrConflict)
		}
		if err := tx.Commit(); err != nil {
			return LegacyConfigurationCompatibilitySettingsImportReport{}, err
		}
		return existing, nil
	}
	var runs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyConfigurationCompatibilitySettingsSlice).Scan(&runs); err != nil {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, err
	}
	if runs != 0 {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, fmt.Errorf("%w: legacy configuration compatibility settings were already imported from another snapshot", ErrConflict)
	}
	current, err := readLegacyConfigurationCompatibilitySettingsTarget(ctx, tx)
	if err != nil {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, fmt.Errorf("read configuration compatibility migration target: %w", err)
	}
	defaults := LegacyConfigurationCompatibilitySettings{
		CommissionWithdrawLimit: defaultCommissionWithdrawLimit, CommissionWithdrawMethod: []string{"支付宝", "USDT", "Paypal"},
		SidebarStyle: "light", HeaderStyle: "dark",
	}
	if LegacyConfigurationCompatibilitySettingsChecksum(current) != LegacyConfigurationCompatibilitySettingsChecksum(defaults) {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, fmt.Errorf("%w: legacy configuration compatibility settings import requires pristine target fields", ErrConflict)
	}
	methodsJSON, err := json.Marshal(input.Settings.CommissionWithdrawMethod)
	if err != nil {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, err
	}
	appResult, err := tx.ExecContext(ctx, `
		UPDATE app_settings SET commission_withdraw_limit = ?, commission_withdraw_method = ?,
			revision = revision + 1, updated_by = NULL, updated_at = ?
		WHERE id = 1 AND commission_withdraw_limit = 10000 AND commission_withdraw_method = '["支付宝","USDT","Paypal"]'
	`, input.Settings.CommissionWithdrawLimit, string(methodsJSON), now.UTC().Unix())
	if err != nil {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, fmt.Errorf("write legacy commission compatibility settings: %w", err)
	}
	themeResult, err := tx.ExecContext(ctx, `
		UPDATE theme_settings SET sidebar_style = ?, header_style = ?, revision = revision + 1,
			updated_by = NULL, updated_at = ? WHERE id = 1 AND sidebar_style = 'light' AND header_style = 'dark'
	`, input.Settings.SidebarStyle, input.Settings.HeaderStyle, now.UTC().Unix())
	if err != nil {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, fmt.Errorf("write legacy frontend compatibility settings: %w", err)
	}
	appRows, appRowsErr := appResult.RowsAffected()
	themeRows, themeRowsErr := themeResult.RowsAffected()
	if appRowsErr != nil || themeRowsErr != nil || appRows != 1 || themeRows != 1 {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, errors.New("legacy configuration compatibility settings target changed during import")
	}
	target, err := readLegacyConfigurationCompatibilitySettingsTarget(ctx, tx)
	if err != nil {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, err
	}
	report := LegacyConfigurationCompatibilitySettingsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Settings: LegacyDomainResult{SourceRows: 1, TargetRows: 1, SourceChecksum: input.Checksum,
			TargetChecksum: LegacyConfigurationCompatibilitySettingsChecksum(target)},
		AppliedAt: now.UTC(),
	}
	if report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, errors.New("legacy configuration compatibility settings target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO legacy_migration_runs(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at)
		VALUES(?,?,?,?,?,?,?)
	`, report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, fmt.Errorf("record legacy configuration compatibility settings migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyConfigurationCompatibilitySettingsImportReport{}, err
	}
	return report, nil
}

func readLegacyConfigurationCompatibilitySettingsTarget(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (LegacyConfigurationCompatibilitySettings, error) {
	var settings LegacyConfigurationCompatibilitySettings
	var methodsJSON string
	if err := database.QueryRowContext(ctx, `
		SELECT app.commission_withdraw_limit, app.commission_withdraw_method,
		       theme.sidebar_style, theme.header_style
		FROM app_settings app CROSS JOIN theme_settings theme WHERE app.id = 1 AND theme.id = 1
	`).Scan(&settings.CommissionWithdrawLimit, &methodsJSON, &settings.SidebarStyle, &settings.HeaderStyle); err != nil {
		return LegacyConfigurationCompatibilitySettings{}, err
	}
	if err := json.Unmarshal([]byte(methodsJSON), &settings.CommissionWithdrawMethod); err != nil {
		return LegacyConfigurationCompatibilitySettings{}, err
	}
	return NormalizeLegacyConfigurationCompatibilitySettings(settings)
}

func validateLegacyConfigurationCompatibilitySettingsImport(input LegacyConfigurationCompatibilitySettingsImport) error {
	normalized, err := NormalizeLegacyConfigurationCompatibilitySettings(input.Settings)
	if err != nil || LegacyConfigurationCompatibilitySettingsChecksum(normalized) != LegacyConfigurationCompatibilitySettingsChecksum(input.Settings) ||
		input.Slice != LegacyConfigurationCompatibilitySettingsSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) ||
		strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 || !validLowerSHA256(input.RollbackBackupSHA256) ||
		input.Checksum != LegacyConfigurationCompatibilitySettingsChecksum(input.Settings) {
		return fmt.Errorf("%w: invalid legacy configuration compatibility settings import", ErrInvalidInput)
	}
	return nil
}
