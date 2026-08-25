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
)

const defaultEmailWhitelistStorage = "gmail.com,qq.com,163.com,yahoo.com,sina.com,126.com,outlook.com,yeah.net,foxmail.com"

func (s *Store) GetSiteSettings(ctx context.Context) (SiteSettings, error) {
	settings, err := scanSiteSettings(s.db.QueryRowContext(ctx, `
		SELECT revision, app_name, app_description, app_url, tos_url, logo, stop_register,
		       email_verify, email_whitelist_enable, email_whitelist_suffix, email_gmail_limit_enable,
		       register_limit_by_ip_enable, register_limit_count, register_limit_expire,
		       password_limit_enable, password_limit_count, password_limit_expire,
		       invite_force, invite_gen_limit, invite_never_expire, login_with_mail_link_enable, updated_at
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
	if err := tx.QueryRowContext(ctx, `SELECT email_verify, login_with_mail_link_enable, smtp_enabled FROM app_settings WHERE id = 1`).Scan(
		&currentEmailVerificationEnabled, &currentMailLoginEnabled, &smtpEnabled,
	); err != nil {
		return SiteSettings{}, fmt.Errorf("read registration email settings: %w", err)
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
		    invite_force = ?, invite_gen_limit = ?, invite_never_expire = ?, login_with_mail_link_enable = ?,
		    updated_by = ?, updated_at = ?, revision = revision + 1
		WHERE id = 1 AND revision = ?
	`, normalized.AppName, normalized.AppDescription, normalized.AppURL, normalized.TOSURL, normalized.Logo, normalized.StopRegister,
		normalized.EmailVerificationEnabled,
		normalized.EmailWhitelistEnabled, strings.Join(normalized.EmailWhitelistSuffixes, ","), normalized.GmailAliasLimitEnabled,
		normalized.RegistrationIPLimitEnabled, normalized.RegistrationIPLimitCount, normalized.RegistrationIPLimitMinutes,
		normalized.PasswordLimitEnabled, normalized.PasswordLimitCount, normalized.PasswordLimitMinutes,
		normalized.InvitationForceEnabled, normalized.InvitationCodeLimit, normalized.InvitationNeverExpire, normalized.MailLoginEnabled,
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
		       invite_force, invite_gen_limit, invite_never_expire, login_with_mail_link_enable, updated_at
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
		input.PasswordLimitMinutes < 1 || input.PasswordLimitMinutes > maxPasswordLimitWindow {
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
	if err := row.Scan(
		&settings.Revision, &settings.AppName, &settings.AppDescription, &settings.AppURL, &settings.TOSURL, &settings.Logo,
		&settings.StopRegister, &settings.EmailVerificationEnabled, &settings.EmailWhitelistEnabled, &suffixStorage, &settings.GmailAliasLimitEnabled,
		&settings.RegistrationIPLimitEnabled, &settings.RegistrationIPLimitCount, &settings.RegistrationIPLimitMinutes,
		&settings.PasswordLimitEnabled, &settings.PasswordLimitCount, &settings.PasswordLimitMinutes,
		&settings.InvitationForceEnabled, &settings.InvitationCodeLimit, &settings.InvitationNeverExpire,
		&settings.MailLoginEnabled,
		&updatedAt,
	); err != nil {
		return SiteSettings{}, err
	}
	settings.EmailWhitelistSuffixes = normalizeEmailWhitelistSuffixes(strings.Split(suffixStorage, ","))
	settings.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return settings, nil
}
