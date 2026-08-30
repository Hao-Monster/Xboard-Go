package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const LegacyMailSettingsSlice = "mail-settings-v1"

type LegacyMailSettings struct {
	SMTPEnabled            bool   `json:"smtp_enabled"`
	SMTPHost               string `json:"smtp_host"`
	SMTPPort               int    `json:"smtp_port"`
	SMTPUsername           string `json:"smtp_username"`
	SMTPPasswordConfigured bool   `json:"smtp_password_configured"`
	SMTPPasswordCipher     []byte `json:"-"`
	SMTPEncryption         string `json:"smtp_encryption"`
	SMTPFromAddress        string `json:"smtp_from_address"`
	RemindMailEnabled      bool   `json:"remind_mail_enable"`
}

type LegacyMailSettingsImport struct {
	Slice, SourceSHA256                      string
	SourceSize                               int64
	Settings                                 LegacyMailSettings
	Checksum                                 string
	RollbackBackupPath, RollbackBackupSHA256 string
}

type LegacyMailSettingsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Settings             LegacyDomainResult `json:"settings"`
	PasswordCipherSHA256 string             `json:"password_cipher_sha256,omitempty"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func DefaultLegacyMailSettings() LegacyMailSettings {
	return LegacyMailSettings{SMTPPort: 587, SMTPEncryption: "starttls"}
}

func NormalizeLegacyMailSettings(settings LegacyMailSettings) (LegacyMailSettings, error) {
	settings.SMTPHost = strings.TrimSpace(settings.SMTPHost)
	settings.SMTPUsername = strings.TrimSpace(settings.SMTPUsername)
	settings.SMTPEncryption = strings.ToLower(strings.TrimSpace(settings.SMTPEncryption))
	settings.SMTPFromAddress = strings.TrimSpace(settings.SMTPFromAddress)
	if !settings.SMTPEnabled {
		defaults := DefaultLegacyMailSettings()
		if settings.SMTPHost != "" || settings.SMTPPort != defaults.SMTPPort || settings.SMTPUsername != "" ||
			settings.SMTPPasswordConfigured || settings.SMTPEncryption != defaults.SMTPEncryption ||
			settings.SMTPFromAddress != "" || settings.RemindMailEnabled {
			return LegacyMailSettings{}, fmt.Errorf("%w: disabled legacy SMTP contains active or orphaned settings", ErrInvalidInput)
		}
		return settings, nil
	}
	if settings.SMTPEncryption == "none" {
		return LegacyMailSettings{}, fmt.Errorf("%w: cleartext legacy SMTP cannot be imported", ErrInvalidInput)
	}
	if settings.SMTPUsername != "" && !settings.SMTPPasswordConfigured {
		return LegacyMailSettings{}, fmt.Errorf("%w: legacy SMTP username requires a password", ErrInvalidInput)
	}
	normalized, err := normalizeSMTPSettings(smtpSettingsInput{
		Enabled: true, Host: settings.SMTPHost, Port: settings.SMTPPort, Username: settings.SMTPUsername,
		Encryption: settings.SMTPEncryption, FromAddress: settings.SMTPFromAddress,
	})
	if err != nil {
		return LegacyMailSettings{}, fmt.Errorf("%w: invalid legacy SMTP settings", ErrInvalidInput)
	}
	settings.SMTPHost, settings.SMTPPort, settings.SMTPUsername = normalized.Host, normalized.Port, normalized.Username
	settings.SMTPEncryption, settings.SMTPFromAddress = normalized.Encryption, normalized.FromAddress
	return settings, nil
}

func ValidateLegacyMailSettingsSource(settings LegacyMailSettings) error {
	_, err := NormalizeLegacyMailSettings(settings)
	return err
}

func ValidateLegacyMailSettingsData(settings LegacyMailSettings) error {
	normalized, err := NormalizeLegacyMailSettings(settings)
	if err != nil || !sameLegacyMailSettings(settings, normalized) ||
		!validSettingsCipherLength(settings.SMTPPasswordCipher) ||
		settings.SMTPPasswordConfigured != (len(settings.SMTPPasswordCipher) > 0) {
		return fmt.Errorf("%w: invalid legacy mail settings", ErrInvalidInput)
	}
	return nil
}

func LegacyMailSettingsChecksum(settings LegacyMailSettings) string {
	return legacyCanonicalChecksum(settings)
}

func (s *Store) LookupLegacyMailSettingsImport(ctx context.Context, sourceSHA256 string) (LegacyMailSettingsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyMailSettingsImportReport{}, false, ErrInvalidInput
	}
	report, found, err := lookupLegacyMailSettingsImport(ctx, s.db, sourceSHA256)
	if err != nil || !found {
		return report, found, err
	}
	target, err := readLegacyMailSettingsTarget(ctx, s.db)
	if err != nil {
		return LegacyMailSettingsImportReport{}, false, fmt.Errorf("verify imported legacy mail settings: %w", err)
	}
	if LegacyMailSettingsChecksum(target) != report.Settings.TargetChecksum {
		return LegacyMailSettingsImportReport{}, false, fmt.Errorf("%w: imported legacy mail settings no longer match their migration ledger", ErrConflict)
	}
	if smtpPasswordCipherChecksum(target.SMTPPasswordCipher) != report.PasswordCipherSHA256 {
		return LegacyMailSettingsImportReport{}, false, fmt.Errorf("%w: imported legacy SMTP credential no longer matches its migration ledger", ErrConflict)
	}
	return report, true, nil
}

func lookupLegacyMailSettingsImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyMailSettingsImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice=? AND source_sha256=?`, LegacyMailSettingsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyMailSettingsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyMailSettingsImportReport{}, false, fmt.Errorf("lookup legacy mail settings migration: %w", err)
	}
	var report LegacyMailSettingsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyMailSettingsImportReport{}, false, fmt.Errorf("decode legacy mail settings migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyMailSettings(ctx context.Context, input LegacyMailSettingsImport, now time.Time) (LegacyMailSettingsImportReport, error) {
	if err := validateLegacyMailSettingsImport(input); err != nil {
		return LegacyMailSettingsImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyMailSettingsImportReport{}, fmt.Errorf("begin legacy mail settings import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != CurrentSchemaVersion() {
		return LegacyMailSettingsImportReport{}, fmt.Errorf("legacy mail settings import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyMailSettingsImportReport{}, fmt.Errorf("validate legacy mail settings target schema: %w", err)
	}
	if existing, found, err := lookupLegacyMailSettingsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyMailSettingsImportReport{}, err
	} else if found {
		if existing.Settings.SourceChecksum != input.Checksum {
			return LegacyMailSettingsImportReport{}, fmt.Errorf("%w: legacy mail settings source differs from its migration ledger", ErrConflict)
		}
		target, err := readLegacyMailSettingsTarget(ctx, tx)
		if err != nil || LegacyMailSettingsChecksum(target) != existing.Settings.TargetChecksum {
			return LegacyMailSettingsImportReport{}, fmt.Errorf("%w: imported legacy mail settings no longer match their migration ledger", ErrConflict)
		}
		if smtpPasswordCipherChecksum(target.SMTPPasswordCipher) != existing.PasswordCipherSHA256 {
			return LegacyMailSettingsImportReport{}, fmt.Errorf("%w: imported legacy SMTP credential no longer matches its migration ledger", ErrConflict)
		}
		if err := tx.Commit(); err != nil {
			return LegacyMailSettingsImportReport{}, err
		}
		return existing, nil
	}
	var runs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyMailSettingsSlice).Scan(&runs); err != nil {
		return LegacyMailSettingsImportReport{}, err
	}
	if runs != 0 {
		return LegacyMailSettingsImportReport{}, fmt.Errorf("%w: legacy mail settings were already imported from another snapshot", ErrConflict)
	}
	var revision int64
	current, err := readLegacyMailSettingsTargetWithRevision(ctx, tx, &revision)
	if err != nil {
		return LegacyMailSettingsImportReport{}, fmt.Errorf("read legacy mail settings migration target: %w", err)
	}
	if !sameLegacyMailSettings(current, DefaultLegacyMailSettings()) {
		return LegacyMailSettingsImportReport{}, fmt.Errorf("%w: legacy mail settings import requires pristine mail fields", ErrConflict)
	}
	if !input.Settings.SMTPEnabled {
		if err := ensureSMTPCanBeDisabled(ctx, tx); err != nil {
			return LegacyMailSettingsImportReport{}, fmt.Errorf("%w: disabled legacy mail settings conflict with enabled mail-dependent policies", ErrConflict)
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE app_settings SET
		smtp_enabled=?,smtp_host=?,smtp_port=?,smtp_username=?,smtp_password_cipher=?,smtp_encryption=?,smtp_from_address=?,remind_mail_enable=?,
		revision=revision+1,updated_by=NULL,updated_at=? WHERE id=1 AND revision=?`,
		input.Settings.SMTPEnabled, input.Settings.SMTPHost, input.Settings.SMTPPort, input.Settings.SMTPUsername,
		nullableBytes(input.Settings.SMTPPasswordCipher), input.Settings.SMTPEncryption, input.Settings.SMTPFromAddress,
		input.Settings.RemindMailEnabled, now.UTC().Unix(), revision)
	if err != nil {
		return LegacyMailSettingsImportReport{}, fmt.Errorf("write legacy mail settings: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return LegacyMailSettingsImportReport{}, errors.New("legacy mail settings target changed during import")
	}
	target, err := readLegacyMailSettingsTarget(ctx, tx)
	if err != nil || !sameLegacyMailSettings(input.Settings, target) {
		return LegacyMailSettingsImportReport{}, errors.New("legacy mail settings target verification does not match source")
	}
	report := LegacyMailSettingsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Settings:             LegacyDomainResult{SourceRows: 1, TargetRows: 1, SourceChecksum: input.Checksum, TargetChecksum: LegacyMailSettingsChecksum(target)},
		PasswordCipherSHA256: smtpPasswordCipherChecksum(target.SMTPPasswordCipher),
		AppliedAt:            now.UTC(),
	}
	if report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		return LegacyMailSettingsImportReport{}, errors.New("legacy mail settings target checksum does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyMailSettingsImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_migration_runs(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at) VALUES(?,?,?,?,?,?,?)`,
		report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyMailSettingsImportReport{}, fmt.Errorf("record legacy mail settings migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyMailSettingsImportReport{}, err
	}
	return report, nil
}

func readLegacyMailSettingsTarget(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (LegacyMailSettings, error) {
	var revision int64
	return readLegacyMailSettingsTargetWithRevision(ctx, database, &revision)
}

func readLegacyMailSettingsTargetWithRevision(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, revision *int64) (LegacyMailSettings, error) {
	var settings LegacyMailSettings
	if err := database.QueryRowContext(ctx, `SELECT revision,smtp_enabled,smtp_host,smtp_port,smtp_username,smtp_password_cipher,smtp_encryption,smtp_from_address,remind_mail_enable FROM app_settings WHERE id=1`).Scan(
		revision, &settings.SMTPEnabled, &settings.SMTPHost, &settings.SMTPPort, &settings.SMTPUsername,
		&settings.SMTPPasswordCipher, &settings.SMTPEncryption, &settings.SMTPFromAddress, &settings.RemindMailEnabled); err != nil {
		return LegacyMailSettings{}, err
	}
	settings.SMTPPasswordCipher = append([]byte(nil), settings.SMTPPasswordCipher...)
	settings.SMTPPasswordConfigured = len(settings.SMTPPasswordCipher) > 0
	return settings, nil
}

func validateLegacyMailSettingsImport(input LegacyMailSettingsImport) error {
	if input.Slice != LegacyMailSettingsSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) || strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 ||
		!validLowerSHA256(input.RollbackBackupSHA256) || input.Checksum != LegacyMailSettingsChecksum(input.Settings) {
		return fmt.Errorf("%w: invalid legacy mail settings import", ErrInvalidInput)
	}
	return ValidateLegacyMailSettingsData(input.Settings)
}

func sameLegacyMailSettings(left, right LegacyMailSettings) bool {
	return left.SMTPEnabled == right.SMTPEnabled && left.SMTPHost == right.SMTPHost && left.SMTPPort == right.SMTPPort &&
		left.SMTPUsername == right.SMTPUsername && left.SMTPPasswordConfigured == right.SMTPPasswordConfigured &&
		left.SMTPEncryption == right.SMTPEncryption && left.SMTPFromAddress == right.SMTPFromAddress &&
		left.RemindMailEnabled == right.RemindMailEnabled && subtle.ConstantTimeCompare(left.SMTPPasswordCipher, right.SMTPPasswordCipher) == 1
}

func smtpPasswordCipherChecksum(ciphertext []byte) string {
	if len(ciphertext) == 0 {
		return ""
	}
	digest := sha256.Sum256(ciphertext)
	return fmt.Sprintf("%x", digest)
}
