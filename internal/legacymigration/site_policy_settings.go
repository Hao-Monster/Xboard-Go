package legacymigration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const legacySitePolicySettingsKeys = `'stop_register','email_verify','email_whitelist_enable','email_whitelist_suffix','email_gmail_limit_enable',
	'register_limit_by_ip_enable','register_limit_count','register_limit_expire',
	'password_limit_enable','password_limit_count','password_limit_expire',
	'invite_force','invite_gen_limit','invite_never_expire',
	'captcha_enable','captcha_type','recaptcha_key','recaptcha_site_key',
	'recaptcha_v3_secret_key','recaptcha_v3_site_key','recaptcha_v3_score_threshold',
	'turnstile_secret_key','turnstile_site_key','ticket_must_wait_reply'`

type SitePolicySettingsSnapshot struct {
	Path              string
	Size              int64
	SHA256            string
	Settings          store.LegacySitePolicySettings
	RecaptchaSecret   []byte `json:"-"`
	RecaptchaV3Secret []byte `json:"-"`
	TurnstileSecret   []byte `json:"-"`
	Checksum          string
}

func (snapshot *SitePolicySettingsSnapshot) ClearSecrets() {
	if snapshot == nil {
		return
	}
	zeroLegacyBytes(snapshot.RecaptchaSecret)
	zeroLegacyBytes(snapshot.RecaptchaV3Secret)
	zeroLegacyBytes(snapshot.TurnstileSecret)
	snapshot.RecaptchaSecret = nil
	snapshot.RecaptchaV3Secret = nil
	snapshot.TurnstileSecret = nil
}

func ReadSitePolicySettingsSnapshot(ctx context.Context, sourcePath string) (SitePolicySettingsSnapshot, error) {
	settings := store.DefaultLegacySitePolicySettings()
	var recaptchaSecret, recaptchaV3Secret, turnstileSecret []byte
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_settings", []string{"name", "value"}); err != nil {
			return err
		}
		budgetQuery := `
			SELECT COUNT(*), COALESCE(SUM(length(CAST(name AS BLOB)) + COALESCE(length(CAST(value AS BLOB)), 0)), 0)
			FROM v2_settings WHERE name IN (` + legacySitePolicySettingsKeys + `)
		`
		if err := validateLegacyQueryBudget(ctx, database, budgetQuery, 24, 32<<10, "legacy site policy settings"); err != nil {
			return err
		}
		rows, err := database.QueryContext(ctx, `SELECT name,CAST(value AS BLOB) FROM v2_settings WHERE name IN (`+legacySitePolicySettingsKeys+`) ORDER BY name`)
		if err != nil {
			return fmt.Errorf("read legacy site policy settings: %w", err)
		}
		defer rows.Close()
		seen := make(map[string]struct{}, 24)
		for rows.Next() {
			var name string
			var raw []byte
			if err := rows.Scan(&name, &raw); err != nil {
				return fmt.Errorf("scan legacy site policy setting: %w", err)
			}
			if _, exists := seen[name]; exists {
				zeroLegacyBytes(raw)
				return fmt.Errorf("legacy site policy settings contain duplicate %q rows", name)
			}
			seen[name] = struct{}{}
			if err := applyLegacySitePolicySetting(name, raw, &settings, &recaptchaSecret, &recaptchaV3Secret, &turnstileSecret); err != nil {
				zeroLegacyBytes(raw)
				return err
			}
			zeroLegacyBytes(raw)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate legacy site policy settings: %w", err)
		}
		settings, err = store.NormalizeLegacySitePolicySettings(settings)
		if err != nil {
			return fmt.Errorf("validate legacy site policy settings: %w", err)
		}
		return nil
	})
	if err != nil {
		zeroLegacyBytes(recaptchaSecret)
		zeroLegacyBytes(recaptchaV3Secret)
		zeroLegacyBytes(turnstileSecret)
		return SitePolicySettingsSnapshot{}, err
	}
	return SitePolicySettingsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Settings: settings, RecaptchaSecret: recaptchaSecret, RecaptchaV3Secret: recaptchaV3Secret,
		TurnstileSecret: turnstileSecret, Checksum: store.LegacySitePolicySettingsChecksum(settings),
	}, nil
}

func applyLegacySitePolicySetting(name string, raw []byte, settings *store.LegacySitePolicySettings, recaptchaSecret, recaptchaV3Secret, turnstileSecret *[]byte) error {
	if settings == nil || recaptchaSecret == nil || recaptchaV3Secret == nil || turnstileSecret == nil {
		return errors.New("legacy site policy settings destination is unavailable")
	}
	var err error
	switch name {
	case "stop_register":
		settings.StopRegister, err = parseLegacyPolicyBoolean(raw, settings.StopRegister)
	case "email_verify":
		settings.EmailVerificationEnabled, err = parseLegacyPolicyBoolean(raw, settings.EmailVerificationEnabled)
	case "email_whitelist_enable":
		settings.EmailWhitelistEnabled, err = parseLegacyPolicyBoolean(raw, settings.EmailWhitelistEnabled)
	case "email_whitelist_suffix":
		settings.EmailWhitelistSuffixes, err = parseLegacyEmailWhitelist(raw, settings.EmailWhitelistSuffixes)
	case "email_gmail_limit_enable":
		settings.GmailAliasLimitEnabled, err = parseLegacyPolicyBoolean(raw, settings.GmailAliasLimitEnabled)
	case "register_limit_by_ip_enable":
		settings.RegistrationIPLimitEnabled, err = parseLegacyPolicyBoolean(raw, settings.RegistrationIPLimitEnabled)
	case "register_limit_count":
		settings.RegistrationIPLimitCount, err = parseLegacyPolicyInteger(raw, settings.RegistrationIPLimitCount)
	case "register_limit_expire":
		settings.RegistrationIPLimitMinutes, err = parseLegacyPolicyInteger(raw, settings.RegistrationIPLimitMinutes)
	case "password_limit_enable":
		settings.PasswordLimitEnabled, err = parseLegacyPolicyBoolean(raw, settings.PasswordLimitEnabled)
	case "password_limit_count":
		settings.PasswordLimitCount, err = parseLegacyPolicyInteger(raw, settings.PasswordLimitCount)
	case "password_limit_expire":
		settings.PasswordLimitMinutes, err = parseLegacyPolicyInteger(raw, settings.PasswordLimitMinutes)
	case "invite_force":
		settings.InvitationForceEnabled, err = parseLegacyPolicyBoolean(raw, settings.InvitationForceEnabled)
	case "invite_gen_limit":
		settings.InvitationCodeLimit, err = parseLegacyPolicyInteger(raw, settings.InvitationCodeLimit)
	case "invite_never_expire":
		settings.InvitationNeverExpire, err = parseLegacyPolicyBoolean(raw, settings.InvitationNeverExpire)
	case "captcha_enable":
		settings.CaptchaEnabled, err = parseLegacyPolicyBoolean(raw, settings.CaptchaEnabled)
	case "captcha_type":
		settings.CaptchaType, err = parseLegacyPolicyText(raw, settings.CaptchaType, 64)
	case "recaptcha_key":
		*recaptchaSecret, settings.RecaptchaSecretConfigured, err = parseLegacyPolicySecret(raw)
	case "recaptcha_site_key":
		settings.RecaptchaSiteKey, err = parseLegacyPolicyText(raw, settings.RecaptchaSiteKey, 512)
	case "recaptcha_v3_secret_key":
		*recaptchaV3Secret, settings.RecaptchaV3SecretConfigured, err = parseLegacyPolicySecret(raw)
	case "recaptcha_v3_site_key":
		settings.RecaptchaV3SiteKey, err = parseLegacyPolicyText(raw, settings.RecaptchaV3SiteKey, 512)
	case "recaptcha_v3_score_threshold":
		settings.RecaptchaV3ScoreThreshold, err = parseLegacyPolicyFloat(raw, settings.RecaptchaV3ScoreThreshold)
	case "turnstile_secret_key":
		*turnstileSecret, settings.TurnstileSecretConfigured, err = parseLegacyPolicySecret(raw)
	case "turnstile_site_key":
		settings.TurnstileSiteKey, err = parseLegacyPolicyText(raw, settings.TurnstileSiteKey, 512)
	case "ticket_must_wait_reply":
		settings.TicketMustWaitReply, err = parseLegacyPolicyBoolean(raw, settings.TicketMustWaitReply)
	default:
		return fmt.Errorf("unsupported legacy site policy setting %q", name)
	}
	if err != nil {
		return fmt.Errorf("validate legacy %s: %w", name, err)
	}
	return nil
}

func parseLegacyPolicyBoolean(raw []byte, fallback bool) (bool, error) {
	if raw == nil || strings.EqualFold(strings.TrimSpace(string(raw)), "null") {
		return fallback, nil
	}
	return parseLegacyPublicOriginBoolean(string(raw))
}

func parseLegacyPolicyInteger(raw []byte, fallback int) (int, error) {
	if raw == nil || strings.EqualFold(strings.TrimSpace(string(raw)), "null") {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 32)
	if err != nil {
		return 0, errors.New("must be an integer")
	}
	return int(parsed), nil
}

func parseLegacyPolicyFloat(raw []byte, fallback float64) (float64, error) {
	if raw == nil || strings.EqualFold(strings.TrimSpace(string(raw)), "null") {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
	if err != nil {
		return 0, errors.New("must be a number")
	}
	return parsed, nil
}

func parseLegacyPolicyText(raw []byte, fallback string, limit int) (string, error) {
	if raw == nil || strings.EqualFold(strings.TrimSpace(string(raw)), "null") {
		return fallback, nil
	}
	value := strings.TrimSpace(string(raw))
	if !utf8.ValidString(value) || len(value) > limit || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", errors.New("contains invalid text")
	}
	return value, nil
}

func parseLegacyPolicySecret(raw []byte) ([]byte, bool, error) {
	if len(raw) == 0 || bytes.EqualFold(bytes.TrimSpace(raw), []byte("null")) {
		return nil, false, nil
	}
	if len(raw) > 4<<10 {
		return nil, false, errors.New("secret exceeds 4096 bytes")
	}
	return append([]byte(nil), raw...), true, nil
}

func parseLegacyEmailWhitelist(raw []byte, fallback []string) ([]string, error) {
	if raw == nil || strings.EqualFold(strings.TrimSpace(string(raw)), "null") {
		return append([]string(nil), fallback...), nil
	}
	value := strings.TrimSpace(string(raw))
	if !utf8.ValidString(value) || len(value) > 8<<10 {
		return nil, errors.New("email whitelist is invalid or too large")
	}
	if strings.HasPrefix(value, "[") {
		var suffixes []string
		if err := json.Unmarshal([]byte(value), &suffixes); err != nil {
			return nil, errors.New("email whitelist must be a string array")
		}
		return suffixes, nil
	}
	if json.Valid([]byte(value)) && value != "" {
		return nil, errors.New("email whitelist JSON must be an array")
	}
	if value == "" {
		return nil, nil
	}
	return strings.Split(value, ","), nil
}
