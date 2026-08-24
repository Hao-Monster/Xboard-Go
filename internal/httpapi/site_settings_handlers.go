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
	EmailWhitelistSuffix      int     `json:"email_whitelist_suffix"`
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
	writeSuccess(w, http.StatusOK, guestConfigResponse{
		TOSURL: nullablePublicString(settings.TOSURL), CaptchaType: "recaptcha",
		RecaptchaV3ScoreThreshold: 0.5, AppName: settings.AppName,
		AppDescription: nullablePublicString(settings.AppDescription), AppURL: nullablePublicString(settings.AppURL),
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
		Revision       int64  `json:"revision"`
		AppName        string `json:"app_name"`
		AppDescription string `json:"app_description"`
		AppURL         string `json:"app_url"`
		TOSURL         string `json:"tos_url"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	updated, err := s.store.UpdateSiteSettings(r.Context(), session.UserID, input.Revision, store.SaveSiteSettingsInput{
		AppName: input.AppName, AppDescription: input.AppDescription, AppURL: input.AppURL, TOSURL: input.TOSURL,
	}, s.now())
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
