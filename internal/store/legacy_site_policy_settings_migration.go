package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const LegacySitePolicySettingsSlice = "site-policy-settings-v1"

type LegacySitePolicySettings struct {
	StopRegister                bool     `json:"stop_register"`
	EmailVerificationEnabled    bool     `json:"email_verify"`
	EmailWhitelistEnabled       bool     `json:"email_whitelist_enable"`
	EmailWhitelistSuffixes      []string `json:"email_whitelist_suffix"`
	GmailAliasLimitEnabled      bool     `json:"email_gmail_limit_enable"`
	RegistrationIPLimitEnabled  bool     `json:"register_limit_by_ip_enable"`
	RegistrationIPLimitCount    int      `json:"register_limit_count"`
	RegistrationIPLimitMinutes  int      `json:"register_limit_expire"`
	PasswordLimitEnabled        bool     `json:"password_limit_enable"`
	PasswordLimitCount          int      `json:"password_limit_count"`
	PasswordLimitMinutes        int      `json:"password_limit_expire"`
	InvitationForceEnabled      bool     `json:"invite_force"`
	InvitationCodeLimit         int      `json:"invite_gen_limit"`
	InvitationNeverExpire       bool     `json:"invite_never_expire"`
	CaptchaEnabled              bool     `json:"captcha_enable"`
	CaptchaType                 string   `json:"captcha_type"`
	RecaptchaSiteKey            string   `json:"recaptcha_site_key"`
	RecaptchaSecretConfigured   bool     `json:"recaptcha_secret_configured"`
	RecaptchaSecretCipher       []byte   `json:"-"`
	RecaptchaV3SiteKey          string   `json:"recaptcha_v3_site_key"`
	RecaptchaV3ScoreThreshold   float64  `json:"recaptcha_v3_score_threshold"`
	RecaptchaV3SecretConfigured bool     `json:"recaptcha_v3_secret_configured"`
	RecaptchaV3SecretCipher     []byte   `json:"-"`
	TurnstileSiteKey            string   `json:"turnstile_site_key"`
	TurnstileSecretConfigured   bool     `json:"turnstile_secret_configured"`
	TurnstileSecretCipher       []byte   `json:"-"`
	TicketMustWaitReply         bool     `json:"ticket_must_wait_reply"`
}

type LegacySitePolicySettingsImport struct {
	Slice, SourceSHA256                      string
	SourceSize                               int64
	Settings                                 LegacySitePolicySettings
	Checksum                                 string
	RollbackBackupPath, RollbackBackupSHA256 string
}

type LegacySitePolicySettingsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Settings             LegacyDomainResult `json:"settings"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func NormalizeLegacySitePolicySettings(settings LegacySitePolicySettings) (LegacySitePolicySettings, error) {
	settings.EmailWhitelistSuffixes = normalizeEmailWhitelistSuffixes(settings.EmailWhitelistSuffixes)
	settings.CaptchaType = strings.TrimSpace(settings.CaptchaType)
	settings.RecaptchaSiteKey = strings.TrimSpace(settings.RecaptchaSiteKey)
	settings.RecaptchaV3SiteKey = strings.TrimSpace(settings.RecaptchaV3SiteKey)
	settings.TurnstileSiteKey = strings.TrimSpace(settings.TurnstileSiteKey)
	if settings.EmailWhitelistEnabled && len(settings.EmailWhitelistSuffixes) == 0 ||
		len(settings.EmailWhitelistSuffixes) > maxEmailWhitelistItems ||
		len(strings.Join(settings.EmailWhitelistSuffixes, ",")) > maxEmailWhitelistBytes ||
		settings.RegistrationIPLimitCount < 1 || settings.RegistrationIPLimitCount > maxRegistrationIPCount ||
		settings.RegistrationIPLimitMinutes < 1 || settings.RegistrationIPLimitMinutes > maxRegistrationIPWindow ||
		settings.PasswordLimitCount < 1 || settings.PasswordLimitCount > maxPasswordLimitCount ||
		settings.PasswordLimitMinutes < 1 || settings.PasswordLimitMinutes > maxPasswordLimitWindow ||
		settings.InvitationCodeLimit < 0 || settings.InvitationCodeLimit > maxInvitationCodeLimit ||
		(settings.CaptchaType != "recaptcha" && settings.CaptchaType != "recaptcha-v3" && settings.CaptchaType != "turnstile") ||
		!validLegacyCaptchaSiteKey(settings.RecaptchaSiteKey) || !validLegacyCaptchaSiteKey(settings.RecaptchaV3SiteKey) || !validLegacyCaptchaSiteKey(settings.TurnstileSiteKey) ||
		math.IsNaN(settings.RecaptchaV3ScoreThreshold) || math.IsInf(settings.RecaptchaV3ScoreThreshold, 0) ||
		settings.RecaptchaV3ScoreThreshold <= 0 || settings.RecaptchaV3ScoreThreshold > 1 {
		return LegacySitePolicySettings{}, fmt.Errorf("%w: invalid legacy site policy settings", ErrInvalidInput)
	}
	for _, suffix := range settings.EmailWhitelistSuffixes {
		if !validEmailDomain(suffix) {
			return LegacySitePolicySettings{}, fmt.Errorf("%w: invalid legacy email whitelist suffix", ErrInvalidInput)
		}
	}
	if settings.CaptchaEnabled {
		configured := false
		switch settings.CaptchaType {
		case "recaptcha":
			configured = settings.RecaptchaSiteKey != "" && settings.RecaptchaSecretConfigured
		case "recaptcha-v3":
			configured = settings.RecaptchaV3SiteKey != "" && settings.RecaptchaV3SecretConfigured
		case "turnstile":
			configured = settings.TurnstileSiteKey != "" && settings.TurnstileSecretConfigured
		}
		if !configured {
			return LegacySitePolicySettings{}, fmt.Errorf("%w: enabled legacy CAPTCHA provider is incomplete", ErrInvalidInput)
		}
	}
	return settings, nil
}

func validLegacyCaptchaSiteKey(value string) bool {
	return utf8.ValidString(value) && len(value) <= maxCaptchaSiteKeyBytes && !containsUnsafeTicketControl(value, false)
}

func ValidateLegacySitePolicySettingsSource(settings LegacySitePolicySettings) error {
	_, err := NormalizeLegacySitePolicySettings(settings)
	return err
}

func ValidateLegacySitePolicySettingsData(settings LegacySitePolicySettings) error {
	normalized, err := NormalizeLegacySitePolicySettings(settings)
	if err != nil || !sameLegacySitePolicySettings(settings, normalized) ||
		!validSettingsCipherLength(settings.RecaptchaSecretCipher) || !validSettingsCipherLength(settings.RecaptchaV3SecretCipher) || !validSettingsCipherLength(settings.TurnstileSecretCipher) ||
		settings.RecaptchaSecretConfigured != (len(settings.RecaptchaSecretCipher) > 0) ||
		settings.RecaptchaV3SecretConfigured != (len(settings.RecaptchaV3SecretCipher) > 0) ||
		settings.TurnstileSecretConfigured != (len(settings.TurnstileSecretCipher) > 0) {
		return fmt.Errorf("%w: invalid legacy site policy settings", ErrInvalidInput)
	}
	return nil
}

func LegacySitePolicySettingsChecksum(settings LegacySitePolicySettings) string {
	return legacyCanonicalChecksum(settings)
}

func (s *Store) LookupLegacySitePolicySettingsImport(ctx context.Context, sourceSHA256 string) (LegacySitePolicySettingsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacySitePolicySettingsImportReport{}, false, ErrInvalidInput
	}
	report, found, err := lookupLegacySitePolicySettingsImport(ctx, s.db, sourceSHA256)
	if err != nil || !found {
		return report, found, err
	}
	settings, err := readLegacySitePolicySettingsTarget(ctx, s.db)
	if err != nil {
		return LegacySitePolicySettingsImportReport{}, false, fmt.Errorf("verify imported legacy site policy settings: %w", err)
	}
	if LegacySitePolicySettingsChecksum(settings) != report.Settings.TargetChecksum {
		return LegacySitePolicySettingsImportReport{}, false, fmt.Errorf("%w: imported legacy site policy settings no longer match their migration ledger", ErrConflict)
	}
	return report, true, nil
}

func lookupLegacySitePolicySettingsImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacySitePolicySettingsImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice=? AND source_sha256=?`, LegacySitePolicySettingsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacySitePolicySettingsImportReport{}, false, nil
	}
	if err != nil {
		return LegacySitePolicySettingsImportReport{}, false, fmt.Errorf("lookup legacy site policy settings migration: %w", err)
	}
	var report LegacySitePolicySettingsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacySitePolicySettingsImportReport{}, false, fmt.Errorf("decode legacy site policy settings migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacySitePolicySettings(ctx context.Context, input LegacySitePolicySettingsImport, now time.Time) (LegacySitePolicySettingsImportReport, error) {
	if err := validateLegacySitePolicySettingsImport(input); err != nil {
		return LegacySitePolicySettingsImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacySitePolicySettingsImportReport{}, fmt.Errorf("begin legacy site policy settings import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != CurrentSchemaVersion() {
		return LegacySitePolicySettingsImportReport{}, fmt.Errorf("legacy site policy settings import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacySitePolicySettingsImportReport{}, fmt.Errorf("validate legacy site policy settings target schema: %w", err)
	}
	if existing, found, err := lookupLegacySitePolicySettingsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacySitePolicySettingsImportReport{}, err
	} else if found {
		if existing.Settings.SourceChecksum != input.Checksum {
			return LegacySitePolicySettingsImportReport{}, fmt.Errorf("%w: legacy site policy settings source differs from its migration ledger", ErrConflict)
		}
		target, err := readLegacySitePolicySettingsTarget(ctx, tx)
		if err != nil {
			return LegacySitePolicySettingsImportReport{}, fmt.Errorf("verify imported legacy site policy settings: %w", err)
		}
		if LegacySitePolicySettingsChecksum(target) != existing.Settings.TargetChecksum {
			return LegacySitePolicySettingsImportReport{}, fmt.Errorf("%w: imported legacy site policy settings no longer match their migration ledger", ErrConflict)
		}
		if err := tx.Commit(); err != nil {
			return LegacySitePolicySettingsImportReport{}, err
		}
		return existing, nil
	}
	var runs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacySitePolicySettingsSlice).Scan(&runs); err != nil {
		return LegacySitePolicySettingsImportReport{}, err
	}
	if runs != 0 {
		return LegacySitePolicySettingsImportReport{}, fmt.Errorf("%w: legacy site policy settings were already imported from another snapshot", ErrConflict)
	}
	var revision int64
	current, err := readLegacySitePolicySettingsTargetWithRevision(ctx, tx, &revision)
	if err != nil {
		return LegacySitePolicySettingsImportReport{}, fmt.Errorf("read site policy settings migration target: %w", err)
	}
	defaults := DefaultLegacySitePolicySettings()
	if !sameLegacySitePolicySettings(current, defaults) {
		return LegacySitePolicySettingsImportReport{}, fmt.Errorf("%w: legacy site policy settings import requires pristine policy fields", ErrConflict)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE app_settings SET
			stop_register=?,email_verify=?,email_whitelist_enable=?,email_whitelist_suffix=?,email_gmail_limit_enable=?,
			register_limit_by_ip_enable=?,register_limit_count=?,register_limit_expire=?,
			password_limit_enable=?,password_limit_count=?,password_limit_expire=?,
			invite_force=?,invite_gen_limit=?,invite_never_expire=?,
			captcha_enable=?,captcha_type=?,recaptcha_site_key=?,recaptcha_secret_cipher=?,
			recaptcha_v3_site_key=?,recaptcha_v3_score_threshold=?,recaptcha_v3_secret_cipher=?,
			turnstile_site_key=?,turnstile_secret_cipher=?,ticket_must_wait_reply=?,
			revision=revision+1,updated_by=NULL,updated_at=?
		WHERE id=1 AND revision=?
	`, input.Settings.StopRegister, input.Settings.EmailVerificationEnabled, input.Settings.EmailWhitelistEnabled, strings.Join(input.Settings.EmailWhitelistSuffixes, ","), input.Settings.GmailAliasLimitEnabled,
		input.Settings.RegistrationIPLimitEnabled, input.Settings.RegistrationIPLimitCount, input.Settings.RegistrationIPLimitMinutes,
		input.Settings.PasswordLimitEnabled, input.Settings.PasswordLimitCount, input.Settings.PasswordLimitMinutes,
		input.Settings.InvitationForceEnabled, input.Settings.InvitationCodeLimit, input.Settings.InvitationNeverExpire,
		input.Settings.CaptchaEnabled, input.Settings.CaptchaType, input.Settings.RecaptchaSiteKey, nullableBytes(input.Settings.RecaptchaSecretCipher),
		input.Settings.RecaptchaV3SiteKey, input.Settings.RecaptchaV3ScoreThreshold, nullableBytes(input.Settings.RecaptchaV3SecretCipher),
		input.Settings.TurnstileSiteKey, nullableBytes(input.Settings.TurnstileSecretCipher), input.Settings.TicketMustWaitReply,
		now.UTC().Unix(), revision)
	if err != nil {
		return LegacySitePolicySettingsImportReport{}, fmt.Errorf("write legacy site policy settings: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return LegacySitePolicySettingsImportReport{}, errors.New("legacy site policy settings target changed during import")
	}
	target, err := readLegacySitePolicySettingsTarget(ctx, tx)
	if err != nil {
		return LegacySitePolicySettingsImportReport{}, fmt.Errorf("verify legacy site policy settings: %w", err)
	}
	if !sameLegacySitePolicySettings(input.Settings, target) {
		return LegacySitePolicySettingsImportReport{}, errors.New("legacy site policy settings target verification does not match source")
	}
	report := LegacySitePolicySettingsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Settings:  LegacyDomainResult{SourceRows: 1, TargetRows: 1, SourceChecksum: input.Checksum, TargetChecksum: LegacySitePolicySettingsChecksum(target)},
		AppliedAt: now.UTC(),
	}
	if report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		return LegacySitePolicySettingsImportReport{}, errors.New("legacy site policy settings target checksum does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacySitePolicySettingsImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_migration_runs(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at) VALUES(?,?,?,?,?,?,?)`,
		report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacySitePolicySettingsImportReport{}, fmt.Errorf("record legacy site policy settings migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacySitePolicySettingsImportReport{}, err
	}
	return report, nil
}

func DefaultLegacySitePolicySettings() LegacySitePolicySettings {
	return LegacySitePolicySettings{
		EmailWhitelistSuffixes:   strings.Split(defaultEmailWhitelistStorage, ","),
		RegistrationIPLimitCount: 3, RegistrationIPLimitMinutes: 60,
		PasswordLimitEnabled: true, PasswordLimitCount: 5, PasswordLimitMinutes: 60,
		InvitationCodeLimit: 5, CaptchaType: "recaptcha", RecaptchaV3ScoreThreshold: 0.5,
	}
}

func readLegacySitePolicySettingsTarget(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (LegacySitePolicySettings, error) {
	var revision int64
	return readLegacySitePolicySettingsTargetWithRevision(ctx, database, &revision)
}

func readLegacySitePolicySettingsTargetWithRevision(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, revision *int64) (LegacySitePolicySettings, error) {
	var settings LegacySitePolicySettings
	var whitelist string
	if err := database.QueryRowContext(ctx, `
		SELECT revision,stop_register,email_verify,email_whitelist_enable,email_whitelist_suffix,email_gmail_limit_enable,
		       register_limit_by_ip_enable,register_limit_count,register_limit_expire,
		       password_limit_enable,password_limit_count,password_limit_expire,
		       invite_force,invite_gen_limit,invite_never_expire,
		       captcha_enable,captcha_type,recaptcha_site_key,recaptcha_secret_cipher,
		       recaptcha_v3_site_key,recaptcha_v3_score_threshold,recaptcha_v3_secret_cipher,
		       turnstile_site_key,turnstile_secret_cipher,ticket_must_wait_reply
		FROM app_settings WHERE id=1
	`).Scan(revision, &settings.StopRegister, &settings.EmailVerificationEnabled, &settings.EmailWhitelistEnabled, &whitelist, &settings.GmailAliasLimitEnabled,
		&settings.RegistrationIPLimitEnabled, &settings.RegistrationIPLimitCount, &settings.RegistrationIPLimitMinutes,
		&settings.PasswordLimitEnabled, &settings.PasswordLimitCount, &settings.PasswordLimitMinutes,
		&settings.InvitationForceEnabled, &settings.InvitationCodeLimit, &settings.InvitationNeverExpire,
		&settings.CaptchaEnabled, &settings.CaptchaType, &settings.RecaptchaSiteKey, &settings.RecaptchaSecretCipher,
		&settings.RecaptchaV3SiteKey, &settings.RecaptchaV3ScoreThreshold, &settings.RecaptchaV3SecretCipher,
		&settings.TurnstileSiteKey, &settings.TurnstileSecretCipher, &settings.TicketMustWaitReply); err != nil {
		return LegacySitePolicySettings{}, err
	}
	settings.EmailWhitelistSuffixes = normalizeEmailWhitelistSuffixes(strings.Split(whitelist, ","))
	settings.RecaptchaSecretCipher = append([]byte(nil), settings.RecaptchaSecretCipher...)
	settings.RecaptchaV3SecretCipher = append([]byte(nil), settings.RecaptchaV3SecretCipher...)
	settings.TurnstileSecretCipher = append([]byte(nil), settings.TurnstileSecretCipher...)
	settings.RecaptchaSecretConfigured = len(settings.RecaptchaSecretCipher) > 0
	settings.RecaptchaV3SecretConfigured = len(settings.RecaptchaV3SecretCipher) > 0
	settings.TurnstileSecretConfigured = len(settings.TurnstileSecretCipher) > 0
	return settings, nil
}

func validateLegacySitePolicySettingsImport(input LegacySitePolicySettingsImport) error {
	if input.Slice != LegacySitePolicySettingsSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) || strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 ||
		!validLowerSHA256(input.RollbackBackupSHA256) || input.Checksum != LegacySitePolicySettingsChecksum(input.Settings) {
		return fmt.Errorf("%w: invalid legacy site policy settings import", ErrInvalidInput)
	}
	return ValidateLegacySitePolicySettingsData(input.Settings)
}

func sameLegacySitePolicySettings(left, right LegacySitePolicySettings) bool {
	if left.StopRegister != right.StopRegister || left.EmailVerificationEnabled != right.EmailVerificationEnabled ||
		left.EmailWhitelistEnabled != right.EmailWhitelistEnabled || left.GmailAliasLimitEnabled != right.GmailAliasLimitEnabled ||
		left.RegistrationIPLimitEnabled != right.RegistrationIPLimitEnabled || left.RegistrationIPLimitCount != right.RegistrationIPLimitCount || left.RegistrationIPLimitMinutes != right.RegistrationIPLimitMinutes ||
		left.PasswordLimitEnabled != right.PasswordLimitEnabled || left.PasswordLimitCount != right.PasswordLimitCount || left.PasswordLimitMinutes != right.PasswordLimitMinutes ||
		left.InvitationForceEnabled != right.InvitationForceEnabled || left.InvitationCodeLimit != right.InvitationCodeLimit || left.InvitationNeverExpire != right.InvitationNeverExpire ||
		left.CaptchaEnabled != right.CaptchaEnabled || left.CaptchaType != right.CaptchaType || left.RecaptchaSiteKey != right.RecaptchaSiteKey ||
		left.RecaptchaSecretConfigured != right.RecaptchaSecretConfigured || left.RecaptchaV3SiteKey != right.RecaptchaV3SiteKey ||
		left.RecaptchaV3ScoreThreshold != right.RecaptchaV3ScoreThreshold || left.RecaptchaV3SecretConfigured != right.RecaptchaV3SecretConfigured ||
		left.TurnstileSiteKey != right.TurnstileSiteKey || left.TurnstileSecretConfigured != right.TurnstileSecretConfigured || left.TicketMustWaitReply != right.TicketMustWaitReply ||
		len(left.EmailWhitelistSuffixes) != len(right.EmailWhitelistSuffixes) {
		return false
	}
	for index := range left.EmailWhitelistSuffixes {
		if left.EmailWhitelistSuffixes[index] != right.EmailWhitelistSuffixes[index] {
			return false
		}
	}
	return subtle.ConstantTimeCompare(left.RecaptchaSecretCipher, right.RecaptchaSecretCipher) == 1 &&
		subtle.ConstantTimeCompare(left.RecaptchaV3SecretCipher, right.RecaptchaV3SecretCipher) == 1 &&
		subtle.ConstantTimeCompare(left.TurnstileSecretCipher, right.TurnstileSecretCipher) == 1
}
