package httpapi

import (
	"errors"
	"net/http"

	"github.com/Hao-Monster/Xboard-Go/internal/captcha"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type captchaTokens struct {
	Recaptcha   string
	RecaptchaV3 string
	Turnstile   string
}

func (s *server) verifyCaptcha(w http.ResponseWriter, r *http.Request, siteSettings store.SiteSettings, tokens captchaTokens, expectedAction string) bool {
	if !siteSettings.CaptchaEnabled {
		return true
	}
	if s.captchaVerifier == nil || s.settingsCipher == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "captcha_unavailable", "验证码服务暂不可用", nil)
		return false
	}
	secrets, err := s.store.GetCaptchaSecretCiphers(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return false
	}
	provider := captcha.Provider(siteSettings.CaptchaType)
	var token string
	var encryptedSecret []byte
	var purpose appsettings.SecretPurpose
	switch provider {
	case captcha.ProviderRecaptcha:
		token, encryptedSecret, purpose = tokens.Recaptcha, secrets.Recaptcha, appsettings.RecaptchaSecretPurpose
	case captcha.ProviderRecaptchaV3:
		token, encryptedSecret, purpose = tokens.RecaptchaV3, secrets.RecaptchaV3, appsettings.RecaptchaV3SecretPurpose
	case captcha.ProviderTurnstile:
		token, encryptedSecret, purpose = tokens.Turnstile, secrets.Turnstile, appsettings.TurnstileSecretPurpose
	default:
		writeAPIError(w, http.StatusServiceUnavailable, "captcha_unavailable", "验证码服务暂不可用", nil)
		return false
	}
	if token == "" {
		writeAPIError(w, http.StatusBadRequest, "captcha_invalid", "验证码有误", nil)
		return false
	}
	plaintext, err := s.settingsCipher.DecryptFor(purpose, encryptedSecret)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "captcha_unavailable", "验证码服务暂不可用", nil)
		return false
	}
	err = s.captchaVerifier.Verify(r.Context(), captcha.Verification{
		Provider: provider, Secret: plaintext, Token: token, RemoteIP: requestIP(r),
		ExpectedAction: expectedAction, ExpectedHostname: s.panelHostname, ScoreThreshold: siteSettings.RecaptchaV3ScoreThreshold,
	})
	for index := range plaintext {
		plaintext[index] = 0
	}
	switch {
	case err == nil:
		return true
	case errors.Is(err, captcha.ErrInvalid):
		writeAPIError(w, http.StatusBadRequest, "captcha_invalid", "验证码有误", nil)
	default:
		s.logger.Warn("CAPTCHA verification unavailable", "provider", provider)
		writeAPIError(w, http.StatusServiceUnavailable, "captcha_unavailable", "验证码服务暂不可用", nil)
	}
	return false
}
