package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"unicode"

	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type guestConfigResponse struct {
	TOSURL                    *string               `json:"tos_url"`
	IsEmailVerify             int                   `json:"is_email_verify"`
	IsInviteForce             int                   `json:"is_invite_force"`
	EnableCouponSystem        int                   `json:"enable_coupon_system"`
	EmailWhitelistSuffix      any                   `json:"email_whitelist_suffix"`
	IsCaptcha                 int                   `json:"is_captcha"`
	CaptchaType               string                `json:"captcha_type"`
	RecaptchaSiteKey          *string               `json:"recaptcha_site_key"`
	RecaptchaV3SiteKey        *string               `json:"recaptcha_v3_site_key"`
	RecaptchaV3ScoreThreshold float64               `json:"recaptcha_v3_score_threshold"`
	TurnstileSiteKey          *string               `json:"turnstile_site_key"`
	AppName                   string                `json:"app_name"`
	AppDescription            *string               `json:"app_description"`
	AppURL                    *string               `json:"app_url"`
	Logo                      *string               `json:"logo"`
	IsRecaptcha               int                   `json:"is_recaptcha"`
	IsTelegram                int                   `json:"is_telegram"`
	TelegramDiscussLink       *string               `json:"telegram_discuss_link"`
	Theme                     store.ThemeAppearance `json:"theme"`
}

func (s *server) getGuestConfig(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetSiteSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	telegramSettings, err := s.store.GetTelegramSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	themeAppearance, err := s.store.GetActiveThemeAppearance(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if themeAppearance.Config.BackgroundURL != "" {
		themeAppearance.Config.BackgroundURL = themeAssetURL(themeAppearance.Name, themeAppearance.PackageSHA256, themeAppearance.Config.BackgroundURL)
	}
	emailWhitelistSuffix := any(0)
	if settings.EmailWhitelistEnabled {
		emailWhitelistSuffix = append([]string(nil), settings.EmailWhitelistSuffixes...)
	}
	writeSuccess(w, http.StatusOK, guestConfigResponse{
		TOSURL: nullablePublicString(settings.TOSURL), CaptchaType: settings.CaptchaType,
		IsEmailVerify:             boolToInt(settings.EmailVerificationEnabled),
		IsCaptcha:                 boolToInt(settings.CaptchaEnabled),
		IsRecaptcha:               boolToInt(settings.CaptchaEnabled),
		RecaptchaSiteKey:          nullablePublicString(settings.RecaptchaSiteKey),
		RecaptchaV3SiteKey:        nullablePublicString(settings.RecaptchaV3SiteKey),
		RecaptchaV3ScoreThreshold: settings.RecaptchaV3ScoreThreshold,
		TurnstileSiteKey:          nullablePublicString(settings.TurnstileSiteKey), AppName: settings.AppName,
		AppDescription: nullablePublicString(settings.AppDescription), AppURL: nullablePublicString(settings.AppURL),
		Logo: nullablePublicString(settings.Logo), EmailWhitelistSuffix: emailWhitelistSuffix,
		IsInviteForce:       boolToInt(settings.InvitationForceEnabled),
		EnableCouponSystem:  boolToInt(settings.CouponEnabled),
		IsTelegram:          boolToInt(telegramSettings.BotEnabled),
		TelegramDiscussLink: nullablePublicString(telegramSettings.DiscussLink),
		Theme:               themeAppearance,
	})
}

func (s *server) getSiteSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetSiteSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, settings)
}

func (s *server) updateSiteSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Revision                   int64     `json:"revision"`
		AppName                    string    `json:"app_name"`
		AppDescription             string    `json:"app_description"`
		AppURL                     string    `json:"app_url"`
		SafeModeEnabled            *bool     `json:"safe_mode_enable"`
		SecurePath                 *string   `json:"secure_path"`
		ForceHTTPS                 *bool     `json:"force_https"`
		SubscribeURL               *string   `json:"subscribe_url"`
		TOSURL                     string    `json:"tos_url"`
		Logo                       string    `json:"logo"`
		Currency                   *string   `json:"currency"`
		CurrencySymbol             *string   `json:"currency_symbol"`
		StopRegister               *bool     `json:"stop_register"`
		EmailVerificationEnabled   *bool     `json:"email_verify"`
		EmailWhitelistEnabled      *bool     `json:"email_whitelist_enable"`
		EmailWhitelistSuffixes     *[]string `json:"email_whitelist_suffix"`
		GmailAliasLimitEnabled     *bool     `json:"email_gmail_limit_enable"`
		RegistrationIPLimitEnabled *bool     `json:"register_limit_by_ip_enable"`
		RegistrationIPLimitCount   *int      `json:"register_limit_count"`
		RegistrationIPLimitMinutes *int      `json:"register_limit_expire"`
		PasswordLimitEnabled       *bool     `json:"password_limit_enable"`
		PasswordLimitCount         *int      `json:"password_limit_count"`
		PasswordLimitMinutes       *int      `json:"password_limit_expire"`
		InvitationForceEnabled     *bool     `json:"invite_force"`
		InvitationCodeLimit        *int      `json:"invite_gen_limit"`
		InvitationNeverExpire      *bool     `json:"invite_never_expire"`
		MailLoginEnabled           *bool     `json:"login_with_mail_link_enable"`
		TrialPlanID                *int64    `json:"try_out_plan_id"`
		TrialHours                 *int      `json:"try_out_hour"`
		TrafficResetMethod         *int      `json:"traffic_reset_method"`
		CouponEnabled              *bool     `json:"coupon_enabled"`
		CaptchaEnabled             *bool     `json:"captcha_enable"`
		CaptchaType                *string   `json:"captcha_type"`
		RecaptchaSiteKey           *string   `json:"recaptcha_site_key"`
		RecaptchaSecret            *string   `json:"recaptcha_secret"`
		ClearRecaptchaSecret       *bool     `json:"clear_recaptcha_secret"`
		RecaptchaV3SiteKey         *string   `json:"recaptcha_v3_site_key"`
		RecaptchaV3ScoreThreshold  *float64  `json:"recaptcha_v3_score_threshold"`
		RecaptchaV3Secret          *string   `json:"recaptcha_v3_secret"`
		ClearRecaptchaV3Secret     *bool     `json:"clear_recaptcha_v3_secret"`
		TurnstileSiteKey           *string   `json:"turnstile_site_key"`
		TurnstileSecret            *string   `json:"turnstile_secret"`
		ClearTurnstileSecret       *bool     `json:"clear_turnstile_secret"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	current, err := s.store.GetSiteSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	next := store.SaveSiteSettingsInput{
		AppName: input.AppName, AppDescription: input.AppDescription, AppURL: input.AppURL, TOSURL: input.TOSURL, Logo: input.Logo,
		SafeModeEnabled: &current.SafeModeEnabled, SecurePath: &current.SecurePath,
		ForceHTTPS: &current.ForceHTTPS, SubscribeURL: &current.SubscribeURL,
		Currency: &current.Currency, CurrencySymbol: &current.CurrencySymbol,
		StopRegister: current.StopRegister, EmailVerificationEnabled: current.EmailVerificationEnabled,
		EmailWhitelistEnabled:  current.EmailWhitelistEnabled,
		EmailWhitelistSuffixes: current.EmailWhitelistSuffixes, GmailAliasLimitEnabled: current.GmailAliasLimitEnabled,
		RegistrationIPLimitEnabled: current.RegistrationIPLimitEnabled,
		RegistrationIPLimitCount:   current.RegistrationIPLimitCount,
		RegistrationIPLimitMinutes: current.RegistrationIPLimitMinutes,
		PasswordLimitEnabled:       current.PasswordLimitEnabled,
		PasswordLimitCount:         current.PasswordLimitCount,
		PasswordLimitMinutes:       current.PasswordLimitMinutes,
		InvitationForceEnabled:     current.InvitationForceEnabled,
		InvitationCodeLimit:        current.InvitationCodeLimit,
		InvitationNeverExpire:      current.InvitationNeverExpire,
		MailLoginEnabled:           current.MailLoginEnabled,
		TrialPlanID:                &current.TrialPlanID,
		TrialHours:                 &current.TrialHours,
		TrafficResetMethod:         &current.TrafficResetMethod,
		CouponEnabled:              &current.CouponEnabled,
		CaptchaEnabled:             current.CaptchaEnabled,
		CaptchaType:                current.CaptchaType,
		RecaptchaSiteKey:           current.RecaptchaSiteKey,
		RecaptchaV3SiteKey:         current.RecaptchaV3SiteKey,
		RecaptchaV3ScoreThreshold:  current.RecaptchaV3ScoreThreshold,
		TurnstileSiteKey:           current.TurnstileSiteKey,
	}
	if input.Currency != nil {
		next.Currency = input.Currency
	}
	if input.ForceHTTPS != nil {
		next.ForceHTTPS = input.ForceHTTPS
	}
	if input.SafeModeEnabled != nil {
		next.SafeModeEnabled = input.SafeModeEnabled
	}
	if input.SecurePath != nil {
		next.SecurePath = input.SecurePath
	}
	if input.SubscribeURL != nil {
		next.SubscribeURL = input.SubscribeURL
	}
	if input.CurrencySymbol != nil {
		next.CurrencySymbol = input.CurrencySymbol
	}
	if input.StopRegister != nil {
		next.StopRegister = *input.StopRegister
	}
	if input.EmailVerificationEnabled != nil {
		next.EmailVerificationEnabled = *input.EmailVerificationEnabled
	}
	if input.EmailWhitelistEnabled != nil {
		next.EmailWhitelistEnabled = *input.EmailWhitelistEnabled
	}
	if input.EmailWhitelistSuffixes != nil {
		next.EmailWhitelistSuffixes = *input.EmailWhitelistSuffixes
	}
	if input.GmailAliasLimitEnabled != nil {
		next.GmailAliasLimitEnabled = *input.GmailAliasLimitEnabled
	}
	if input.RegistrationIPLimitEnabled != nil {
		next.RegistrationIPLimitEnabled = *input.RegistrationIPLimitEnabled
	}
	if input.RegistrationIPLimitCount != nil {
		next.RegistrationIPLimitCount = *input.RegistrationIPLimitCount
	}
	if input.RegistrationIPLimitMinutes != nil {
		next.RegistrationIPLimitMinutes = *input.RegistrationIPLimitMinutes
	}
	if input.PasswordLimitEnabled != nil {
		next.PasswordLimitEnabled = *input.PasswordLimitEnabled
	}
	if input.PasswordLimitCount != nil {
		next.PasswordLimitCount = *input.PasswordLimitCount
	}
	if input.PasswordLimitMinutes != nil {
		next.PasswordLimitMinutes = *input.PasswordLimitMinutes
	}
	if input.InvitationForceEnabled != nil {
		next.InvitationForceEnabled = *input.InvitationForceEnabled
	}
	if input.InvitationCodeLimit != nil {
		next.InvitationCodeLimit = *input.InvitationCodeLimit
	}
	if input.InvitationNeverExpire != nil {
		next.InvitationNeverExpire = *input.InvitationNeverExpire
	}
	if input.MailLoginEnabled != nil {
		next.MailLoginEnabled = *input.MailLoginEnabled
	}
	if input.TrialPlanID != nil {
		next.TrialPlanID = input.TrialPlanID
	}
	if input.TrialHours != nil {
		next.TrialHours = input.TrialHours
	}
	if input.TrafficResetMethod != nil {
		next.TrafficResetMethod = input.TrafficResetMethod
	}
	if input.CouponEnabled != nil {
		next.CouponEnabled = input.CouponEnabled
	}
	if input.CaptchaEnabled != nil {
		next.CaptchaEnabled = *input.CaptchaEnabled
	}
	if input.CaptchaType != nil {
		next.CaptchaType = *input.CaptchaType
	}
	if input.RecaptchaSiteKey != nil {
		next.RecaptchaSiteKey = *input.RecaptchaSiteKey
	}
	if input.RecaptchaV3SiteKey != nil {
		next.RecaptchaV3SiteKey = *input.RecaptchaV3SiteKey
	}
	if input.RecaptchaV3ScoreThreshold != nil {
		if *input.RecaptchaV3ScoreThreshold <= 0 {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请检查验证码设置", nil)
			return
		}
		next.RecaptchaV3ScoreThreshold = *input.RecaptchaV3ScoreThreshold
	}
	if input.TurnstileSiteKey != nil {
		next.TurnstileSiteKey = *input.TurnstileSiteKey
	}
	if !s.applyCaptchaSecretInput(w, input.RecaptchaSecret, input.ClearRecaptchaSecret, appsettings.RecaptchaSecretPurpose, &next.ReplaceRecaptchaSecret, &next.RecaptchaSecretCipher) ||
		!s.applyCaptchaSecretInput(w, input.RecaptchaV3Secret, input.ClearRecaptchaV3Secret, appsettings.RecaptchaV3SecretPurpose, &next.ReplaceRecaptchaV3Secret, &next.RecaptchaV3SecretCipher) ||
		!s.applyCaptchaSecretInput(w, input.TurnstileSecret, input.ClearTurnstileSecret, appsettings.TurnstileSecretPurpose, &next.ReplaceTurnstileSecret, &next.TurnstileSecretCipher) {
		return
	}
	if next.EmailVerificationEnabled && s.registrationEmailProtector == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "settings_encryption_unavailable", "服务器未配置注册验证码加密密钥", nil)
		return
	}
	if next.InvitationForceEnabled && s.invitationProtector == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "settings_encryption_unavailable", "服务器未配置邀请码加密密钥", nil)
		return
	}
	if next.MailLoginEnabled && s.loginLinkProtector == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "settings_encryption_unavailable", "服务器未配置登录链接加密密钥", nil)
		return
	}
	session, _ := sessionFromContext(r.Context())
	updated, err := s.store.UpdateSiteSettings(r.Context(), session.UserID, input.Revision, next, s.now())
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, http.StatusConflict, "settings_conflict", "设置已被其他管理员修改，请刷新后重试", nil)
		return
	}
	if errors.Is(err, store.ErrRegistrationEmailVerificationNeedsMail) {
		writeAPIError(w, http.StatusConflict, "registration_email_requires_smtp", "启用注册邮箱验证前必须先启用 SMTP 邮件服务", nil)
		return
	}
	if errors.Is(err, store.ErrMailLoginNeedsMail) {
		writeAPIError(w, http.StatusConflict, "mail_login_requires_smtp", "启用邮件链接登录前必须先启用 SMTP 邮件服务", nil)
		return
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, updated)
}

func (s *server) applyCaptchaSecretInput(w http.ResponseWriter, secret *string, clear *bool, purpose appsettings.SecretPurpose, replace *bool, ciphertext *[]byte) bool {
	clearRequested := clear != nil && *clear
	value := ""
	if secret != nil {
		value = strings.TrimSpace(*secret)
	}
	if clearRequested && value != "" {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "验证码密钥不能同时替换和清除", nil)
		return false
	}
	if clearRequested {
		*replace = true
		*ciphertext = nil
		return true
	}
	if value == "" {
		return true
	}
	if len(value) > 4<<10 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "验证码密钥格式无效", nil)
		return false
	}
	if s.settingsCipher == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "settings_encryption_unavailable", "服务器未配置设置加密密钥", nil)
		return false
	}
	encrypted, err := s.settingsCipher.EncryptFor(purpose, []byte(value))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return false
	}
	*replace = true
	*ciphertext = encrypted
	return true
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullablePublicString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
