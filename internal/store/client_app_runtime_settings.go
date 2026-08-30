package store

import (
	"context"
	"fmt"
	"strings"
)

func (s *Store) GetClientAppRuntimeSettings(ctx context.Context) (ClientAppRuntimeSettings, error) {
	var settings ClientAppRuntimeSettings
	var emailWhitelistSuffixes string
	if err := s.db.QueryRowContext(ctx, `
		SELECT app_name, app_description, app_url, logo, tos_url, currency, currency_symbol,
		       telegram_bot_enable, ticket_must_wait_reply, email_verify, invite_force,
		       email_whitelist_suffix, captcha_enable, captcha_type, recaptcha_site_key,
		       recaptcha_v3_site_key, recaptcha_v3_score_threshold, turnstile_site_key
		FROM app_settings WHERE id = 1
	`).Scan(
		&settings.AppName, &settings.AppDescription, &settings.AppURL, &settings.Logo,
		&settings.TOSURL, &settings.Currency, &settings.CurrencySymbol,
		&settings.TelegramBotEnabled, &settings.TicketMustWaitReply,
		&settings.EmailVerificationEnabled, &settings.InvitationForceEnabled,
		&emailWhitelistSuffixes, &settings.CaptchaEnabled, &settings.CaptchaType,
		&settings.RecaptchaSiteKey, &settings.RecaptchaV3SiteKey,
		&settings.RecaptchaV3ScoreThreshold, &settings.TurnstileSiteKey,
	); err != nil {
		return ClientAppRuntimeSettings{}, fmt.Errorf("get client app runtime settings: %w", err)
	}
	settings.EmailWhitelistSuffixPresent = strings.TrimSpace(emailWhitelistSuffixes) != ""
	return settings, nil
}
