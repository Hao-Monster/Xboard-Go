package httpapi

import (
	"errors"
	"net/http"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type guestConfigResponse struct {
	TOSURL                    *string `json:"tos_url"`
	IsEmailVerify             int     `json:"is_email_verify"`
	IsInviteForce             int     `json:"is_invite_force"`
	EmailWhitelistSuffix      any     `json:"email_whitelist_suffix"`
	IsCaptcha                 int     `json:"is_captcha"`
	CaptchaType               string  `json:"captcha_type"`
	RecaptchaSiteKey          *string `json:"recaptcha_site_key"`
	RecaptchaV3SiteKey        *string `json:"recaptcha_v3_site_key"`
	RecaptchaV3ScoreThreshold float64 `json:"recaptcha_v3_score_threshold"`
	TurnstileSiteKey          *string `json:"turnstile_site_key"`
	AppName                   string  `json:"app_name"`
	AppDescription            *string `json:"app_description"`
	AppURL                    *string `json:"app_url"`
	Logo                      *string `json:"logo"`
	IsRecaptcha               int     `json:"is_recaptcha"`
}

func (s *server) getGuestConfig(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetSiteSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	emailWhitelistSuffix := any(0)
	if settings.EmailWhitelistEnabled {
		emailWhitelistSuffix = append([]string(nil), settings.EmailWhitelistSuffixes...)
	}
	writeSuccess(w, http.StatusOK, guestConfigResponse{
		TOSURL: nullablePublicString(settings.TOSURL), CaptchaType: "recaptcha",
		RecaptchaV3ScoreThreshold: 0.5, AppName: settings.AppName,
		AppDescription: nullablePublicString(settings.AppDescription), AppURL: nullablePublicString(settings.AppURL),
		Logo: nullablePublicString(settings.Logo), EmailWhitelistSuffix: emailWhitelistSuffix,
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
		TOSURL                     string    `json:"tos_url"`
		Logo                       string    `json:"logo"`
		StopRegister               *bool     `json:"stop_register"`
		EmailWhitelistEnabled      *bool     `json:"email_whitelist_enable"`
		EmailWhitelistSuffixes     *[]string `json:"email_whitelist_suffix"`
		GmailAliasLimitEnabled     *bool     `json:"email_gmail_limit_enable"`
		RegistrationIPLimitEnabled *bool     `json:"register_limit_by_ip_enable"`
		RegistrationIPLimitCount   *int      `json:"register_limit_count"`
		RegistrationIPLimitMinutes *int      `json:"register_limit_expire"`
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
		StopRegister: current.StopRegister, EmailWhitelistEnabled: current.EmailWhitelistEnabled,
		EmailWhitelistSuffixes: current.EmailWhitelistSuffixes, GmailAliasLimitEnabled: current.GmailAliasLimitEnabled,
		RegistrationIPLimitEnabled: current.RegistrationIPLimitEnabled,
		RegistrationIPLimitCount:   current.RegistrationIPLimitCount,
		RegistrationIPLimitMinutes: current.RegistrationIPLimitMinutes,
	}
	if input.StopRegister != nil {
		next.StopRegister = *input.StopRegister
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
	session, _ := sessionFromContext(r.Context())
	updated, err := s.store.UpdateSiteSettings(r.Context(), session.UserID, input.Revision, next, s.now())
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, http.StatusConflict, "settings_conflict", "设置已被其他管理员修改，请刷新后重试", nil)
		return
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, updated)
}

func nullablePublicString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
