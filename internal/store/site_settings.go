package store

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
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
	maxCurrencySymbolBytes  = 16
	maxSettingsCipherBytes  = 8_192
	minSettingsCipherBytes  = 33
	maxSubscribeURLItems    = 32
	maxSubscribeURLBytes    = 8_192
)

const defaultEmailWhitelistStorage = "gmail.com,qq.com,163.com,yahoo.com,sina.com,126.com,outlook.com,yeah.net,foxmail.com"

func (s *Store) GetSiteSettings(ctx context.Context) (SiteSettings, error) {
	settings, err := scanSiteSettings(s.db.QueryRowContext(ctx, `
		SELECT revision, app_name, app_description, app_url, safe_mode_enable, secure_path, force_https, subscribe_url, tos_url, logo, currency, currency_symbol, stop_register,
		       email_verify, email_whitelist_enable, email_whitelist_suffix, email_gmail_limit_enable,
		       register_limit_by_ip_enable, register_limit_count, register_limit_expire,
		       password_limit_enable, password_limit_count, password_limit_expire,
		       invite_force, invite_gen_limit, invite_never_expire, login_with_mail_link_enable, try_out_plan_id, try_out_hour, traffic_reset_method, coupon_enabled,
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

func (s *Store) GetSiteAccessSettings(ctx context.Context) (SiteAccessSettings, error) {
	var settings SiteAccessSettings
	if err := s.db.QueryRowContext(ctx, `
		SELECT app_url, safe_mode_enable, secure_path FROM app_settings WHERE id = 1
	`).Scan(&settings.AppURL, &settings.SafeModeEnabled, &settings.SecurePath); err != nil {
		return SiteAccessSettings{}, fmt.Errorf("get site access settings: %w", err)
	}
	return settings, nil
}

func (s *Store) EnsureSiteAccessSettings(ctx context.Context, fallbackSecurePath string, now time.Time) error {
	fallbackSecurePath = strings.TrimSpace(fallbackSecurePath)
	if !validPersistedSecurePath(fallbackSecurePath) || fallbackSecurePath == "" || now.Unix() < 0 {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE app_settings
		SET secure_path = ?
		WHERE id = 1 AND secure_path = ''
	`, fallbackSecurePath); err != nil {
		return fmt.Errorf("ensure site access settings: %w", err)
	}
	return nil
}

func (s *Store) UpdateSiteSettings(ctx context.Context, administratorID, revision int64, input SaveSiteSettingsInput, now time.Time) (SiteSettings, error) {
	if administratorID < 1 || revision < 1 {
		return SiteSettings{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SiteSettings{}, fmt.Errorf("begin site settings update: %w", err)
	}
	defer tx.Rollback()
	var currentEmailVerificationEnabled, currentMailLoginEnabled, currentSafeModeEnabled, currentForceHTTPS, smtpEnabled bool
	var currentTrialPlanID int64
	var currentTrialHours, currentTrafficResetMethod int
	var currentCouponEnabled bool
	var currentCurrency, currentCurrencySymbol, currentSecurePath, currentSubscribeURL string
	var captchaSecrets CaptchaSecretCiphers
	if err := tx.QueryRowContext(ctx, `
		SELECT email_verify, login_with_mail_link_enable, safe_mode_enable, secure_path, force_https, subscribe_url, smtp_enabled, try_out_plan_id, try_out_hour, traffic_reset_method, coupon_enabled, currency, currency_symbol,
		       recaptcha_secret_cipher, recaptcha_v3_secret_cipher, turnstile_secret_cipher
		FROM app_settings WHERE id = 1
	`).Scan(
		&currentEmailVerificationEnabled, &currentMailLoginEnabled, &currentSafeModeEnabled, &currentSecurePath, &currentForceHTTPS, &currentSubscribeURL, &smtpEnabled, &currentTrialPlanID, &currentTrialHours, &currentTrafficResetMethod, &currentCouponEnabled, &currentCurrency, &currentCurrencySymbol,
		&captchaSecrets.Recaptcha, &captchaSecrets.RecaptchaV3, &captchaSecrets.Turnstile,
	); err != nil {
		return SiteSettings{}, fmt.Errorf("read registration email settings: %w", err)
	}
	if input.Currency == nil {
		input.Currency = &currentCurrency
	}
	if input.CurrencySymbol == nil {
		input.CurrencySymbol = &currentCurrencySymbol
	}
	if input.ForceHTTPS == nil {
		input.ForceHTTPS = &currentForceHTTPS
	}
	if input.SafeModeEnabled == nil {
		input.SafeModeEnabled = &currentSafeModeEnabled
	}
	if input.SecurePath == nil {
		input.SecurePath = &currentSecurePath
	}
	if input.SubscribeURL == nil {
		input.SubscribeURL = &currentSubscribeURL
	}
	normalized, err := normalizeSiteSettings(input)
	if err != nil {
		return SiteSettings{}, err
	}
	if err := ensureCurrencyChangeAllowed(ctx, tx, currentCurrency, *normalized.Currency); err != nil {
		return SiteSettings{}, err
	}
	if *normalized.SecurePath != currentSecurePath && !validConfigurableSecurePath(*normalized.SecurePath) {
		return SiteSettings{}, fmt.Errorf("%w: invalid secure admin path", ErrInvalidInput)
	}
	if normalized.TrafficResetMethod == nil {
		normalized.TrafficResetMethod = &currentTrafficResetMethod
	}
	if normalized.TrialPlanID == nil {
		normalized.TrialPlanID = &currentTrialPlanID
	}
	if normalized.TrialHours == nil {
		normalized.TrialHours = &currentTrialHours
	}
	if *normalized.TrialPlanID > 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plans WHERE id = ?)`, *normalized.TrialPlanID).Scan(&exists); err != nil {
			return SiteSettings{}, fmt.Errorf("validate registration trial plan: %w", err)
		}
		if !exists {
			return SiteSettings{}, fmt.Errorf("%w: registration trial plan does not exist", ErrInvalidInput)
		}
	}
	if normalized.CouponEnabled == nil {
		normalized.CouponEnabled = &currentCouponEnabled
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
		SET app_name = ?, app_description = ?, app_url = ?, safe_mode_enable = ?, secure_path = ?, force_https = ?, subscribe_url = ?, tos_url = ?, logo = ?, currency = ?, currency_symbol = ?, stop_register = ?,
		    email_verify = ?, email_whitelist_enable = ?, email_whitelist_suffix = ?, email_gmail_limit_enable = ?,
		    register_limit_by_ip_enable = ?, register_limit_count = ?, register_limit_expire = ?,
		    password_limit_enable = ?, password_limit_count = ?, password_limit_expire = ?,
		    invite_force = ?, invite_gen_limit = ?, invite_never_expire = ?, login_with_mail_link_enable = ?,
		    try_out_plan_id = ?, try_out_hour = ?, traffic_reset_method = ?, coupon_enabled = ?,
		    captcha_enable = ?, captcha_type = ?, recaptcha_site_key = ?, recaptcha_secret_cipher = ?,
		    recaptcha_v3_site_key = ?, recaptcha_v3_score_threshold = ?, recaptcha_v3_secret_cipher = ?,
		    turnstile_site_key = ?, turnstile_secret_cipher = ?,
		    updated_by = ?, updated_at = ?, revision = revision + 1
		WHERE id = 1 AND revision = ?
	`, normalized.AppName, normalized.AppDescription, normalized.AppURL, *normalized.SafeModeEnabled, *normalized.SecurePath, *normalized.ForceHTTPS, *normalized.SubscribeURL, normalized.TOSURL, normalized.Logo, *normalized.Currency, *normalized.CurrencySymbol, normalized.StopRegister,
		normalized.EmailVerificationEnabled,
		normalized.EmailWhitelistEnabled, strings.Join(normalized.EmailWhitelistSuffixes, ","), normalized.GmailAliasLimitEnabled,
		normalized.RegistrationIPLimitEnabled, normalized.RegistrationIPLimitCount, normalized.RegistrationIPLimitMinutes,
		normalized.PasswordLimitEnabled, normalized.PasswordLimitCount, normalized.PasswordLimitMinutes,
		normalized.InvitationForceEnabled, normalized.InvitationCodeLimit, normalized.InvitationNeverExpire, normalized.MailLoginEnabled,
		*normalized.TrialPlanID, *normalized.TrialHours, *normalized.TrafficResetMethod, *normalized.CouponEnabled,
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
	if *normalized.TrafficResetMethod != currentTrafficResetMethod {
		if err := rescheduleSystemTrafficResetUsers(ctx, tx, *normalized.TrafficResetMethod, now); err != nil {
			return SiteSettings{}, err
		}
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
		SELECT revision, app_name, app_description, app_url, safe_mode_enable, secure_path, force_https, subscribe_url, tos_url, logo, currency, currency_symbol, stop_register,
		       email_verify, email_whitelist_enable, email_whitelist_suffix, email_gmail_limit_enable,
		       register_limit_by_ip_enable, register_limit_count, register_limit_expire,
		       password_limit_enable, password_limit_count, password_limit_expire,
		       invite_force, invite_gen_limit, invite_never_expire, login_with_mail_link_enable, try_out_plan_id, try_out_hour, traffic_reset_method, coupon_enabled,
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

// UpdateLegacySiteSettings applies supported site fields from the old partial
// config endpoint without requiring a revision that endpoint cannot
// provide. The write lock and in-transaction revision predicate preserve the
// same lost-update protection as other legacy settings adapters.
func (s *Store) UpdateLegacySiteSettings(ctx context.Context, administratorID int64, input SaveLegacySiteSettingsInput, now time.Time) (SiteSettings, error) {
	if administratorID < 1 || now.Unix() < 0 || (input.Currency == nil && input.CurrencySymbol == nil && input.SafeModeEnabled == nil && input.SecurePath == nil && input.ForceHTTPS == nil && input.SubscribeURL == nil) {
		return SiteSettings{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SiteSettings{}, fmt.Errorf("begin legacy site settings update: %w", err)
	}
	defer tx.Rollback()
	var revision int64
	var appURL, currency, symbol, securePath, subscribeURL string
	var safeModeEnabled, forceHTTPS bool
	if err := tx.QueryRowContext(ctx, `
		SELECT revision, app_url, currency, currency_symbol, safe_mode_enable, secure_path, force_https, subscribe_url FROM app_settings WHERE id = 1
	`).Scan(&revision, &appURL, &currency, &symbol, &safeModeEnabled, &securePath, &forceHTTPS, &subscribeURL); err != nil {
		return SiteSettings{}, fmt.Errorf("read legacy site settings: %w", err)
	}
	currentCurrency := currency
	if input.Currency != nil {
		currency = strings.ToUpper(strings.TrimSpace(*input.Currency))
	}
	if input.CurrencySymbol != nil {
		symbol = strings.TrimSpace(*input.CurrencySymbol)
	}
	if input.SafeModeEnabled != nil {
		safeModeEnabled = *input.SafeModeEnabled
	}
	if input.SecurePath != nil {
		candidate := strings.TrimSpace(*input.SecurePath)
		if candidate != securePath && !validConfigurableSecurePath(candidate) {
			return SiteSettings{}, fmt.Errorf("%w: invalid secure admin path", ErrInvalidInput)
		}
		securePath = candidate
	}
	if input.ForceHTTPS != nil {
		forceHTTPS = *input.ForceHTTPS
	}
	if input.SubscribeURL != nil {
		var err error
		subscribeURL, err = normalizeSubscribeURLStorage(*input.SubscribeURL)
		if err != nil {
			return SiteSettings{}, err
		}
	}
	if !validCurrencyCode(currency) || !utf8.ValidString(symbol) || len(symbol) > maxCurrencySymbolBytes || strings.IndexFunc(symbol, unicode.IsControl) >= 0 ||
		!validPersistedSecurePath(securePath) || (safeModeEnabled && (appURL == "" || !validHTTPURL(appURL))) {
		return SiteSettings{}, fmt.Errorf("%w: invalid legacy site settings", ErrInvalidInput)
	}
	if err := ensureCurrencyChangeAllowed(ctx, tx, currentCurrency, currency); err != nil {
		return SiteSettings{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE app_settings
		SET currency = ?, currency_symbol = ?, safe_mode_enable = ?, secure_path = ?, force_https = ?, subscribe_url = ?, updated_by = ?, updated_at = ?, revision = revision + 1
		WHERE id = 1 AND revision = ?
	`, currency, symbol, safeModeEnabled, securePath, forceHTTPS, subscribeURL, administratorID, now.Unix(), revision)
	if err != nil {
		return SiteSettings{}, fmt.Errorf("update legacy site settings: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return SiteSettings{}, fmt.Errorf("count legacy site settings update: %w", err)
	}
	if changed != 1 {
		return SiteSettings{}, ErrConflict
	}
	settings, err := scanSiteSettings(tx.QueryRowContext(ctx, `
		SELECT revision, app_name, app_description, app_url, safe_mode_enable, secure_path, force_https, subscribe_url, tos_url, logo, currency, currency_symbol, stop_register,
		       email_verify, email_whitelist_enable, email_whitelist_suffix, email_gmail_limit_enable,
		       register_limit_by_ip_enable, register_limit_count, register_limit_expire,
		       password_limit_enable, password_limit_count, password_limit_expire,
		       invite_force, invite_gen_limit, invite_never_expire, login_with_mail_link_enable, try_out_plan_id, try_out_hour, traffic_reset_method, coupon_enabled,
		       captcha_enable, captcha_type, recaptcha_site_key, recaptcha_secret_cipher,
		       recaptcha_v3_site_key, recaptcha_v3_score_threshold, recaptcha_v3_secret_cipher,
		       turnstile_site_key, turnstile_secret_cipher, updated_at
		FROM app_settings WHERE id = 1
	`))
	if err != nil {
		return SiteSettings{}, fmt.Errorf("read updated legacy site settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SiteSettings{}, fmt.Errorf("commit legacy site settings update: %w", err)
	}
	return settings, nil
}

func ensureCurrencyChangeAllowed(ctx context.Context, tx *sql.Tx, current, next string) error {
	if current == next {
		return nil
	}
	var locked bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM orders)
			OR EXISTS(SELECT 1 FROM payment_checkout_attempts)
			OR EXISTS(SELECT 1 FROM commission_logs)
			OR EXISTS(SELECT 1 FROM commission_withdrawals)
			OR EXISTS(SELECT 1 FROM commission_transfer_events)
			OR EXISTS(SELECT 1 FROM admin_balance_adjustment_events)
			OR EXISTS(SELECT 1 FROM users WHERE balance<>0 OR commission_balance<>0 OR frozen_commission_balance<>0)
			OR EXISTS(SELECT 1 FROM gift_card_templates)
			OR EXISTS(SELECT 1 FROM gift_card_codes)
	`).Scan(&locked); err != nil {
		return fmt.Errorf("inspect deployment currency lock: %w", err)
	}
	if locked {
		return ErrCurrencyLocked
	}
	return nil
}

func normalizeSiteSettings(input SaveSiteSettingsInput) (SaveSiteSettingsInput, error) {
	input.AppName = strings.TrimSpace(input.AppName)
	input.AppDescription = strings.TrimSpace(input.AppDescription)
	input.AppURL = strings.TrimSpace(input.AppURL)
	if input.SafeModeEnabled == nil {
		safeModeEnabled := false
		input.SafeModeEnabled = &safeModeEnabled
	}
	if input.SecurePath == nil {
		input.SecurePath = stringCopyPointer("")
	}
	securePath := strings.TrimSpace(*input.SecurePath)
	input.SecurePath = &securePath
	if input.ForceHTTPS == nil {
		forceHTTPS := false
		input.ForceHTTPS = &forceHTTPS
	}
	if input.SubscribeURL == nil {
		input.SubscribeURL = stringCopyPointer("")
	}
	subscribeURL, err := normalizeSubscribeURLStorage(*input.SubscribeURL)
	if err != nil {
		return SaveSiteSettingsInput{}, err
	}
	input.SubscribeURL = &subscribeURL
	input.TOSURL = strings.TrimSpace(input.TOSURL)
	input.Logo = strings.TrimSpace(input.Logo)
	if input.Currency == nil {
		input.Currency = stringCopyPointer("CNY")
	}
	if input.CurrencySymbol == nil {
		input.CurrencySymbol = stringCopyPointer("¥")
	}
	currency := strings.ToUpper(strings.TrimSpace(*input.Currency))
	currencySymbol := strings.TrimSpace(*input.CurrencySymbol)
	input.Currency = &currency
	input.CurrencySymbol = &currencySymbol
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
		!validCurrencyCode(*input.Currency) || !utf8.ValidString(*input.CurrencySymbol) || len(*input.CurrencySymbol) > maxCurrencySymbolBytes || strings.IndexFunc(*input.CurrencySymbol, unicode.IsControl) >= 0 ||
		utf8.RuneCountInString(input.AppName) < 1 || utf8.RuneCountInString(input.AppName) > maxSiteAppNameRunes ||
		utf8.RuneCountInString(input.AppDescription) > maxSiteDescriptionRunes ||
		containsUnsafeTicketControl(input.AppName, false) || containsUnsafeTicketControl(input.AppDescription, true) ||
		len(input.AppURL) > maxSiteURLBytes || len(input.TOSURL) > maxSiteURLBytes || len(input.Logo) > maxSiteURLBytes ||
		(input.AppURL != "" && !validHTTPURL(input.AppURL)) ||
		(*input.SafeModeEnabled && input.AppURL == "") ||
		!validPersistedSecurePath(*input.SecurePath) ||
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
	if input.TrialPlanID != nil && *input.TrialPlanID < 0 {
		return SaveSiteSettingsInput{}, fmt.Errorf("%w: invalid registration trial plan", ErrInvalidInput)
	}
	if input.TrialHours != nil && (*input.TrialHours < 1 || *input.TrialHours > 8760) {
		return SaveSiteSettingsInput{}, fmt.Errorf("%w: invalid registration trial duration", ErrInvalidInput)
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

func normalizeSubscribeURLStorage(value string) (string, error) {
	if !utf8.ValidString(value) || len(value) > maxSubscribeURLBytes || strings.IndexByte(value, 0) >= 0 {
		return "", fmt.Errorf("%w: invalid subscription origins", ErrInvalidInput)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	raw := strings.FieldsFunc(value, func(character rune) bool {
		return character == ',' || character == '\r' || character == '\n'
	})
	if len(raw) > maxSubscribeURLItems {
		return "", fmt.Errorf("%w: too many subscription origins", ErrInvalidInput)
	}
	seen := make(map[string]struct{}, len(raw))
	normalized := make([]string, 0, len(raw))
	for _, candidate := range raw {
		candidate = strings.TrimSpace(candidate)
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.IsAbs() == false || parsed.Host == "" || parsed.Hostname() == "" || parsed.Opaque != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || !validPublicOriginPort(parsed.Port()) {
			return "", fmt.Errorf("%w: invalid subscription origin", ErrInvalidInput)
		}
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		if parsed.Scheme != "https" && (parsed.Scheme != "http" || !isLoopbackHostname(parsed.Hostname())) {
			return "", fmt.Errorf("%w: subscription origins require HTTPS", ErrInvalidInput)
		}
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		parsed.RawPath = ""
		candidate = parsed.String()
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		normalized = append(normalized, candidate)
	}
	storage := strings.Join(normalized, ",")
	if len(storage) > maxSubscribeURLBytes {
		return "", fmt.Errorf("%w: subscription origins are too large", ErrInvalidInput)
	}
	return storage, nil
}

// NormalizeSubscribeURL validates and canonicalizes the legacy comma/newline
// representation used by Xboard's offline migration reader.
func NormalizeSubscribeURL(value string) (string, error) {
	return normalizeSubscribeURLStorage(value)
}

func isLoopbackHostname(hostname string) bool {
	hostname = strings.TrimSuffix(strings.ToLower(hostname), ".")
	if hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") {
		return true
	}
	address := net.ParseIP(hostname)
	return address != nil && address.IsLoopback()
}

func validPublicOriginPort(value string) bool {
	if value == "" {
		return true
	}
	port, err := strconv.Atoi(value)
	return err == nil && port >= 1 && port <= 65_535
}

func validPersistedSecurePath(value string) bool {
	if len(value) > 64 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validConfigurableSecurePath(value string) bool {
	if len(value) < 8 || !validPersistedSecurePath(value) {
		return false
	}
	switch strings.ToLower(value) {
	case "client", "passport", "server":
		return false
	default:
		return true
	}
}

func validCurrencyCode(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
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
		&settings.Revision, &settings.AppName, &settings.AppDescription, &settings.AppURL, &settings.SafeModeEnabled, &settings.SecurePath, &settings.ForceHTTPS, &settings.SubscribeURL, &settings.TOSURL, &settings.Logo, &settings.Currency, &settings.CurrencySymbol,
		&settings.StopRegister, &settings.EmailVerificationEnabled, &settings.EmailWhitelistEnabled, &suffixStorage, &settings.GmailAliasLimitEnabled,
		&settings.RegistrationIPLimitEnabled, &settings.RegistrationIPLimitCount, &settings.RegistrationIPLimitMinutes,
		&settings.PasswordLimitEnabled, &settings.PasswordLimitCount, &settings.PasswordLimitMinutes,
		&settings.InvitationForceEnabled, &settings.InvitationCodeLimit, &settings.InvitationNeverExpire,
		&settings.MailLoginEnabled, &settings.TrialPlanID, &settings.TrialHours, &settings.TrafficResetMethod, &settings.CouponEnabled,
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
