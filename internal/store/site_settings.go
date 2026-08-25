package store

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxSiteAppNameRunes     = 100
	maxSiteDescriptionRunes = 500
	maxSiteURLBytes         = 2_048
	maxEmailWhitelistItems  = 100
	maxEmailWhitelistBytes  = 8_192
	maxRegistrationIPCount  = 100
	maxRegistrationIPWindow = 10_080
	maxInvitationCodeLimit  = 100
	maxPasswordLimitCount   = 20
	maxPasswordLimitWindow  = 1_440
	maxCaptchaSiteKeyBytes  = 512
	maxSettingsCipherBytes  = 8_192
	minSettingsCipherBytes  = 33
)

const defaultEmailWhitelistStorage = "gmail.com,qq.com,163.com,yahoo.com,sina.com,126.com,outlook.com,yeah.net,foxmail.com"

func (s *Store) GetSiteSettings(ctx context.Context) (SiteSettings, error) {
	settings, err := scanSiteSettings(s.db.QueryRowContext(ctx, `
		SELECT revision, app_name, app_description, app_url, tos_url, logo, stop_register,
		       email_verify, email_whitelist_enable, email_whitelist_suffix, email_gmail_limit_enable,
		       register_limit_by_ip_enable, register_limit_count, register_limit_expire,
		       password_limit_enable, password_limit_count, password_limit_expire,
		       invite_force, invite_gen_limit, invite_never_expire, login_with_mail_link_enable, traffic_reset_method,
		       captcha_enable, captcha_type, recaptcha_site_key, recaptcha_secret_cipher,
		       recaptcha_v3_site_key, recaptcha_v3_score_threshold, recaptcha_v3_secret_cipher,
		       turnstile_site_key, turnstile_secret_cipher, updated_at
		FROM app_settings WHERE id = 1
	`))
	if err != nil {
		return SiteSettings{}, fmt.Errorf("get site settings: %w", err)
	}
	return settings, nil
}

func (s *Store) UpdateSiteSettings(ctx context.Context, administratorID, revision int64, input SaveSiteSettingsInput, now time.Time) (SiteSettings, error) {
	if administratorID < 1 || revision < 1 {
		return SiteSettings{}, ErrInvalidInput
	}
	normalized, err := normalizeSiteSettings(input)
	if err != nil {
		return SiteSettings{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SiteSettings{}, fmt.Errorf("begin site settings update: %w", err)
	}
	defer tx.Rollback()
	var currentEmailVerificationEnabled, currentMailLoginEnabled, smtpEnabled bool
	var currentTrafficResetMethod int
	var captchaSecrets CaptchaSecretCiphers
	if err := tx.QueryRowContext(ctx, `
		SELECT email_verify, login_with_mail_link_enable, smtp_enabled, traffic_reset_method,
		       recaptcha_secret_cipher, recaptcha_v3_secret_cipher, turnstile_secret_cipher
		FROM app_settings WHERE id = 1
	`).Scan(
		&currentEmailVerificationEnabled, &currentMailLoginEnabled, &smtpEnabled, &currentTrafficResetMethod,
		&captchaSecrets.Recaptcha, &captchaSecrets.RecaptchaV3, &captchaSecrets.Turnstile,
	); err != nil {
		return SiteSettings{}, fmt.Errorf("read registration email settings: %w", err)
	}
	if normalized.TrafficResetMethod == nil {
		normalized.TrafficResetMethod = &currentTrafficResetMethod
	}
	if normalized.ReplaceRecaptchaSecret {
		captchaSecrets.Recaptcha = append([]byte(nil), normalized.RecaptchaSecretCipher...)
	}
	if normalized.ReplaceRecaptchaV3Secret {
		captchaSecrets.RecaptchaV3 = append([]byte(nil), normalized.RecaptchaV3SecretCipher...)
	}
	if normalized.ReplaceTurnstileSecret {
		captchaSecrets.Turnstile = append([]byte(nil), normalized.TurnstileSecretCipher...)
	}
	if err := validateEnabledCaptcha(normalized, captchaSecrets); err != nil {
		return SiteSettings{}, err
	}
	if normalized.EmailVerificationEnabled && !smtpEnabled {
		return SiteSettings{}, ErrRegistrationEmailVerificationNeedsMail
	}
	if normalized.MailLoginEnabled && !smtpEnabled {
		return SiteSettings{}, ErrMailLoginNeedsMail
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE app_settings
		SET app_name = ?, app_description = ?, app_url = ?, tos_url = ?, logo = ?, stop_register = ?,
		    email_verify = ?, email_whitelist_enable = ?, email_whitelist_suffix = ?, email_gmail_limit_enable = ?,
		    register_limit_by_ip_enable = ?, register_limit_count = ?, register_limit_expire = ?,
		    password_limit_enable = ?, password_limit_count = ?, password_limit_expire = ?,
		    invite_force = ?, invite_gen_limit = ?, invite_never_expire = ?, login_with_mail_link_enable = ?, traffic_reset_method = ?,
		    captcha_enable = ?, captcha_type = ?, recaptcha_site_key = ?, recaptcha_secret_cipher = ?,
		    recaptcha_v3_site_key = ?, recaptcha_v3_score_threshold = ?, recaptcha_v3_secret_cipher = ?,
		    turnstile_site_key = ?, turnstile_secret_cipher = ?,
		    updated_by = ?, updated_at = ?, revision = revision + 1
		WHERE id = 1 AND revision = ?
	`, normalized.AppName, normalized.AppDescription, normalized.AppURL, normalized.TOSURL, normalized.Logo, normalized.StopRegister,
		normalized.EmailVerificationEnabled,
		normalized.EmailWhitelistEnabled, strings.Join(normalized.EmailWhitelistSuffixes, ","), normalized.GmailAliasLimitEnabled,
		normalized.RegistrationIPLimitEnabled, normalized.RegistrationIPLimitCount, normalized.RegistrationIPLimitMinutes,
		normalized.PasswordLimitEnabled, normalized.PasswordLimitCount, normalized.PasswordLimitMinutes,
		normalized.InvitationForceEnabled, normalized.InvitationCodeLimit, normalized.InvitationNeverExpire, normalized.MailLoginEnabled, *normalized.TrafficResetMethod,
		normalized.CaptchaEnabled, normalized.CaptchaType, normalized.RecaptchaSiteKey, nullableBytes(captchaSecrets.Recaptcha),
		normalized.RecaptchaV3SiteKey, normalized.RecaptchaV3ScoreThreshold, nullableBytes(captchaSecrets.RecaptchaV3),
		normalized.TurnstileSiteKey, nullableBytes(captchaSecrets.Turnstile),
		administratorID, now.Unix(), revision)
	if err != nil {
		return SiteSettings{}, fmt.Errorf("update site settings: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return SiteSettings{}, fmt.Errorf("count updated site settings: %w", err)
	}
	if rows != 1 {
		return SiteSettings{}, ErrConflict
	}
	if !normalized.RegistrationIPLimitEnabled {
		if _, err := tx.ExecContext(ctx, `DELETE FROM registration_ip_limits`); err != nil {
			return SiteSettings{}, fmt.Errorf("clear disabled registration IP limits: %w", err)
		}
	}
	if currentEmailVerificationEnabled && !normalized.EmailVerificationEnabled {
		if _, err := tx.ExecContext(ctx, `
			UPDATE registration_email_mail_outbox
			SET cancelled_at = ?, code_cipher = NULL, claim_token = NULL, claimed_at = NULL,
			    last_error = 'cancelled because registration email verification was disabled', updated_at = ?
			WHERE sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
		`, now.Unix(), now.Unix()); err != nil {
			return SiteSettings{}, fmt.Errorf("cancel disabled registration verification mail: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM registration_email_challenges`); err != nil {
			return SiteSettings{}, fmt.Errorf("clear disabled registration verification challenges: %w", err)
		}
	}
	if currentMailLoginEnabled && !normalized.MailLoginEnabled {
		if _, err := tx.ExecContext(ctx, `
			UPDATE login_link_mail_outbox
			SET cancelled_at = COALESCE(cancelled_at, ?), token_cipher = NULL,
			    claim_token = NULL, claimed_at = NULL,
			    last_error = 'cancelled because mail login was disabled', updated_at = ?
			WHERE sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
		`, now.Unix(), now.Unix()); err != nil {
			return SiteSettings{}, fmt.Errorf("cancel disabled mail login links: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM login_link_tokens WHERE purpose = 'email'`); err != nil {
			return SiteSettings{}, fmt.Errorf("revoke disabled mail login links: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM mail_login_request_limits`); err != nil {
			return SiteSettings{}, fmt.Errorf("clear disabled mail login cooldowns: %w", err)
		}
	}
	settings, err := scanSiteSettings(tx.QueryRowContext(ctx, `
		SELECT revision, app_name, app_description, app_url, tos_url, logo, stop_register,
		       email_verify, email_whitelist_enable, email_whitelist_suffix, email_gmail_limit_enable,
		       register_limit_by_ip_enable, register_limit_count, register_limit_expire,
		       password_limit_enable, password_limit_count, password_limit_expire,
		       invite_force, invite_gen_limit, invite_never_expire, login_with_mail_link_enable, traffic_reset_method,
		       captcha_enable, captcha_type, recaptcha_site_key, recaptcha_secret_cipher,
		       recaptcha_v3_site_key, recaptcha_v3_score_threshold, recaptcha_v3_secret_cipher,
		       turnstile_site_key, turnstile_secret_cipher, updated_at
		FROM app_settings WHERE id = 1
	`))
	if err != nil {
		return SiteSettings{}, fmt.Errorf("get updated site settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SiteSettings{}, fmt.Errorf("commit site settings update: %w", err)
	}
	return settings, nil
}

func normalizeSiteSettings(input SaveSiteSettingsInput) (SaveSiteSettingsInput, error) {
	input.AppName = strings.TrimSpace(input.AppName)
	input.AppDescription = strings.TrimSpace(input.AppDescription)
	input.AppURL = strings.TrimSpace(input.AppURL)
	input.TOSURL = strings.TrimSpace(input.TOSURL)
	input.Logo = strings.TrimSpace(input.Logo)
	input.CaptchaType = strings.TrimSpace(input.CaptchaType)
	input.RecaptchaSiteKey = strings.TrimSpace(input.RecaptchaSiteKey)
	input.RecaptchaV3SiteKey = strings.TrimSpace(input.RecaptchaV3SiteKey)
	input.TurnstileSiteKey = strings.TrimSpace(input.TurnstileSiteKey)
	if input.CaptchaType == "" {
		input.CaptchaType = "recaptcha"
	}
	if input.RecaptchaV3ScoreThreshold == 0 {
		input.RecaptchaV3ScoreThreshold = 0.5
	}
	input.EmailWhitelistSuffixes = normalizeEmailWhitelistSuffixes(input.EmailWhitelistSuffixes)
	if !utf8.ValidString(input.AppName) || !utf8.ValidString(input.AppDescription) ||
		!utf8.ValidString(input.AppURL) || !utf8.ValidString(input.TOSURL) || !utf8.ValidString(input.Logo) ||
		utf8.RuneCountInString(input.AppName) < 1 || utf8.RuneCountInString(input.AppName) > maxSiteAppNameRunes ||
		utf8.RuneCountInString(input.AppDescription) > maxSiteDescriptionRunes ||
		containsUnsafeTicketControl(input.AppName, false) || containsUnsafeTicketControl(input.AppDescription, true) ||
		len(input.AppURL) > maxSiteURLBytes || len(input.TOSURL) > maxSiteURLBytes || len(input.Logo) > maxSiteURLBytes ||
		(input.AppURL != "" && !validHTTPURL(input.AppURL)) ||
		(input.TOSURL != "" && !validHTTPURL(input.TOSURL)) ||
		(input.Logo != "" && !validHTTPURL(input.Logo)) ||
		(input.EmailWhitelistEnabled && len(input.EmailWhitelistSuffixes) == 0) ||
		len(input.EmailWhitelistSuffixes) > maxEmailWhitelistItems ||
		len(strings.Join(input.EmailWhitelistSuffixes, ",")) > maxEmailWhitelistBytes ||
		input.RegistrationIPLimitCount < 1 || input.RegistrationIPLimitCount > maxRegistrationIPCount ||
		input.RegistrationIPLimitMinutes < 1 || input.RegistrationIPLimitMinutes > maxRegistrationIPWindow ||
		input.PasswordLimitCount < 1 || input.PasswordLimitCount > maxPasswordLimitCount ||
		input.PasswordLimitMinutes < 1 || input.PasswordLimitMinutes > maxPasswordLimitWindow ||
		(input.CaptchaType != "recaptcha" && input.CaptchaType != "recaptcha-v3" && input.CaptchaType != "turnstile") ||
		!utf8.ValidString(input.RecaptchaSiteKey) || !utf8.ValidString(input.RecaptchaV3SiteKey) || !utf8.ValidString(input.TurnstileSiteKey) ||
		len(input.RecaptchaSiteKey) > maxCaptchaSiteKeyBytes || len(input.RecaptchaV3SiteKey) > maxCaptchaSiteKeyBytes || len(input.TurnstileSiteKey) > maxCaptchaSiteKeyBytes ||
		containsUnsafeTicketControl(input.RecaptchaSiteKey, false) || containsUnsafeTicketControl(input.RecaptchaV3SiteKey, false) || containsUnsafeTicketControl(input.TurnstileSiteKey, false) ||
		input.RecaptchaV3ScoreThreshold <= 0 || input.RecaptchaV3ScoreThreshold > 1 ||
		(input.ReplaceRecaptchaSecret && !validSettingsCipherLength(input.RecaptchaSecretCipher)) ||
		(input.ReplaceRecaptchaV3Secret && !validSettingsCipherLength(input.RecaptchaV3SecretCipher)) ||
		(input.ReplaceTurnstileSecret && !validSettingsCipherLength(input.TurnstileSecretCipher)) {
		return SaveSiteSettingsInput{}, fmt.Errorf("%w: invalid site settings", ErrInvalidInput)
	}
	if input.TrafficResetMethod != nil && (*input.TrafficResetMethod < 0 || *input.TrafficResetMethod > 4) {
		return SaveSiteSettingsInput{}, fmt.Errorf("%w: invalid site settings", ErrInvalidInput)
	}
	if input.InvitationCodeLimit < 0 || input.InvitationCodeLimit > maxInvitationCodeLimit {
		return SaveSiteSettingsInput{}, fmt.Errorf("%w: invalid site settings", ErrInvalidInput)
	}
	for _, suffix := range input.EmailWhitelistSuffixes {
		if !validEmailDomain(suffix) {
			return SaveSiteSettingsInput{}, fmt.Errorf("%w: invalid email whitelist suffix", ErrInvalidInput)
		}
	}
	return input, nil
}

func (s *Store) GetCaptchaSecretCiphers(ctx context.Context) (CaptchaSecretCiphers, error) {
	var secrets CaptchaSecretCiphers
	if err := s.db.QueryRowContext(ctx, `
		SELECT recaptcha_secret_cipher, recaptcha_v3_secret_cipher, turnstile_secret_cipher
		FROM app_settings WHERE id = 1
	`).Scan(&secrets.Recaptcha, &secrets.RecaptchaV3, &secrets.Turnstile); err != nil {
		return CaptchaSecretCiphers{}, fmt.Errorf("get CAPTCHA secret ciphers: %w", err)
	}
	secrets.Recaptcha = append([]byte(nil), secrets.Recaptcha...)
	secrets.RecaptchaV3 = append([]byte(nil), secrets.RecaptchaV3...)
	secrets.Turnstile = append([]byte(nil), secrets.Turnstile...)
	return secrets, nil
}

func validSettingsCipherLength(value []byte) bool {
	return len(value) == 0 || (len(value) >= minSettingsCipherBytes && len(value) <= maxSettingsCipherBytes)
}

func validateEnabledCaptcha(input SaveSiteSettingsInput, secrets CaptchaSecretCiphers) error {
	if !input.CaptchaEnabled {
		return nil
	}
	configured := false
	switch input.CaptchaType {
	case "recaptcha":
		configured = input.RecaptchaSiteKey != "" && len(secrets.Recaptcha) > 0
	case "recaptcha-v3":
		configured = input.RecaptchaV3SiteKey != "" && len(secrets.RecaptchaV3) > 0
	case "turnstile":
		configured = input.TurnstileSiteKey != "" && len(secrets.Turnstile) > 0
	}
	if !configured {
		return fmt.Errorf("%w: selected CAPTCHA provider is not fully configured", ErrInvalidInput)
	}
	return nil
}

func normalizeEmailWhitelistSuffixes(values []string) []string {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized
}

func validEmailDomain(value string) bool {
	if len(value) < 3 || len(value) > 253 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") || !strings.Contains(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func scanSiteSettings(row rowScanner) (SiteSettings, error) {
	var settings SiteSettings
	var updatedAt int64
	var suffixStorage string
	var recaptchaSecretCipher, recaptchaV3SecretCipher, turnstileSecretCipher []byte
	if err := row.Scan(
		&settings.Revision, &settings.AppName, &settings.AppDescription, &settings.AppURL, &settings.TOSURL, &settings.Logo,
		&settings.StopRegister, &settings.EmailVerificationEnabled, &settings.EmailWhitelistEnabled, &suffixStorage, &settings.GmailAliasLimitEnabled,
		&settings.RegistrationIPLimitEnabled, &settings.RegistrationIPLimitCount, &settings.RegistrationIPLimitMinutes,
		&settings.PasswordLimitEnabled, &settings.PasswordLimitCount, &settings.PasswordLimitMinutes,
		&settings.InvitationForceEnabled, &settings.InvitationCodeLimit, &settings.InvitationNeverExpire,
		&settings.MailLoginEnabled, &settings.TrafficResetMethod,
		&settings.CaptchaEnabled, &settings.CaptchaType, &settings.RecaptchaSiteKey, &recaptchaSecretCipher,
		&settings.RecaptchaV3SiteKey, &settings.RecaptchaV3ScoreThreshold, &recaptchaV3SecretCipher,
		&settings.TurnstileSiteKey, &turnstileSecretCipher,
		&updatedAt,
	); err != nil {
		return SiteSettings{}, err
	}
	settings.EmailWhitelistSuffixes = normalizeEmailWhitelistSuffixes(strings.Split(suffixStorage, ","))
	settings.RecaptchaSecretConfigured = len(recaptchaSecretCipher) > 0
	settings.RecaptchaV3SecretConfigured = len(recaptchaV3SecretCipher) > 0
	settings.TurnstileSecretConfigured = len(turnstileSecretCipher) > 0
	settings.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return settings, nil
}
