package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/captcha"
)

type recordingCaptchaVerifier struct {
	mu     sync.Mutex
	result error
	inputs []captcha.Verification
}

func (verifier *recordingCaptchaVerifier) Verify(_ context.Context, input captcha.Verification) error {
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	input.Secret = append([]byte(nil), input.Secret...)
	verifier.inputs = append(verifier.inputs, input)
	return verifier.result
}

func (verifier *recordingCaptchaVerifier) snapshot() []captcha.Verification {
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	return append([]captcha.Verification(nil), verifier.inputs...)
}

func TestCaptchaAdminSecretsPublicContractAndProtectedActions(t *testing.T) {
	verifier := &recordingCaptchaVerifier{}
	api, _ := newTestAPIWithCaptcha(t, verifier)
	admin := loginAdmin(t, api)

	configured := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":1,"app_name":"Xboard-Go","app_description":"","app_url":"","tos_url":"","logo":"",
		"captcha_enable":true,"captcha_type":"recaptcha","recaptcha_site_key":"public-v2-site","recaptcha_secret":"private-v2-secret"
	}`)
	if configured.Code != http.StatusOK || strings.Contains(configured.Body.String(), "private-v2-secret") || strings.Contains(configured.Body.String(), "cipher") {
		t.Fatalf("configure reCAPTCHA status=%d body=%s", configured.Code, configured.Body)
	}
	settings := decodeSiteSettingsEnvelope(t, configured)
	if !settings.CaptchaEnabled || settings.CaptchaType != "recaptcha" || settings.RecaptchaSiteKey != "public-v2-site" || !settings.RecaptchaSecretConfigured {
		t.Fatalf("admin CAPTCHA settings = %#v", settings)
	}
	guestResponse := testClient{}.request(t, api, http.MethodGet, "/api/v1/guest/comm/config", "")
	guest := decodeGuestConfigEnvelope(t, guestResponse)
	if guest.IsCaptcha != 1 || guest.IsRecaptcha != 1 || guest.CaptchaType != "recaptcha" || stringValue(guest.RecaptchaSiteKey) != "public-v2-site" || strings.Contains(guestResponse.Body.String(), "private-v2-secret") {
		t.Fatalf("public CAPTCHA config = %#v body=%s", guest, guestResponse.Body)
	}

	missing := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{"email":"captcha-missing@example.test","password":"strong-password-123","password_confirmation":"strong-password-123"}`)
	expectAPIError(t, missing, http.StatusBadRequest, "captcha_invalid")
	if len(verifier.snapshot()) != 0 {
		t.Fatal("missing token reached the upstream verifier")
	}
	registered := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{"email":"captcha-ok@example.test","password":"strong-password-123","password_confirmation":"strong-password-123","recaptcha_data":"browser-v2-token"}`)
	if registered.Code != http.StatusOK {
		t.Fatalf("CAPTCHA registration status=%d body=%s", registered.Code, registered.Body)
	}
	inputs := verifier.snapshot()
	if len(inputs) != 1 || inputs[0].Provider != captcha.ProviderRecaptcha || inputs[0].Token != "browser-v2-token" || string(inputs[0].Secret) != "private-v2-secret" || inputs[0].ExpectedAction != "register" {
		t.Fatalf("registration CAPTCHA verification = %#v", inputs)
	}

	configuredV3 := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":2,"app_name":"Xboard-Go","app_description":"","app_url":"","tos_url":"","logo":"",
		"captcha_type":"recaptcha-v3","recaptcha_v3_site_key":"public-v3-site","recaptcha_v3_secret":"private-v3-secret","recaptcha_v3_score_threshold":0.7
	}`)
	if configuredV3.Code != http.StatusOK || strings.Contains(configuredV3.Body.String(), "private-v3-secret") {
		t.Fatalf("configure v3 status=%d body=%s", configuredV3.Code, configuredV3.Body)
	}
	passwordReset := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/password-reset/request", `{"email":"captcha-ok@example.test","recaptcha_v3_token":"browser-v3-token"}`)
	expectAPIError(t, passwordReset, http.StatusServiceUnavailable, "mail_unavailable")
	inputs = verifier.snapshot()
	last := inputs[len(inputs)-1]
	if last.Provider != captcha.ProviderRecaptchaV3 || last.Token != "browser-v3-token" || string(last.Secret) != "private-v3-secret" || last.ExpectedAction != "sendEmailVerify" || last.ScoreThreshold != 0.7 {
		t.Fatalf("password reset CAPTCHA verification = %#v", last)
	}
	legacyPasswordReset := testClient{}.request(t, api, http.MethodPost, "/api/v2/passport/comm/sendEmailVerify", `{"email":"captcha-ok@example.test","recaptcha_v3_token":"legacy-v3-token"}`)
	expectAPIError(t, legacyPasswordReset, http.StatusServiceUnavailable, "mail_unavailable")
	inputs = verifier.snapshot()
	last = inputs[len(inputs)-1]
	if last.Provider != captcha.ProviderRecaptchaV3 || last.Token != "legacy-v3-token" || string(last.Secret) != "private-v3-secret" || last.ExpectedAction != "sendEmailVerify" || last.ScoreThreshold != 0.7 {
		t.Fatalf("legacy email CAPTCHA verification = %#v", last)
	}

	configuredTurnstile := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":3,"app_name":"Xboard-Go","app_description":"","app_url":"","tos_url":"","logo":"",
		"captcha_type":"turnstile","turnstile_site_key":"public-turnstile-site","turnstile_secret":"private-turnstile-secret"
	}`)
	if configuredTurnstile.Code != http.StatusOK || strings.Contains(configuredTurnstile.Body.String(), "private-turnstile-secret") {
		t.Fatalf("configure Turnstile status=%d body=%s", configuredTurnstile.Code, configuredTurnstile.Body)
	}
	registrationEmail := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/registration-email/request", `{"email":"new-captcha@example.test","turnstile_token":"browser-turnstile-token"}`)
	expectAPIError(t, registrationEmail, http.StatusConflict, "registration_email_disabled")
	inputs = verifier.snapshot()
	last = inputs[len(inputs)-1]
	if last.Provider != captcha.ProviderTurnstile || last.Token != "browser-turnstile-token" || string(last.Secret) != "private-turnstile-secret" || last.ExpectedAction != "sendEmailVerify" {
		t.Fatalf("registration email CAPTCHA verification = %#v", last)
	}

	preserved := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":4,"app_name":"Xboard-Go","app_description":"","app_url":"","tos_url":"","logo":""
	}`)
	if preserved.Code != http.StatusOK || !decodeSiteSettingsEnvelope(t, preserved).TurnstileSecretConfigured {
		t.Fatalf("blank secret preservation status=%d body=%s", preserved.Code, preserved.Body)
	}
	clearWhileEnabled := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":5,"app_name":"Xboard-Go","app_description":"","app_url":"","tos_url":"","logo":"","clear_turnstile_secret":true
	}`)
	expectAPIError(t, clearWhileEnabled, http.StatusUnprocessableEntity, "validation_failed")
	disabledAndCleared := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":5,"app_name":"Xboard-Go","app_description":"","app_url":"","tos_url":"","logo":"","captcha_enable":false,"clear_turnstile_secret":true
	}`)
	if disabledAndCleared.Code != http.StatusOK || decodeSiteSettingsEnvelope(t, disabledAndCleared).TurnstileSecretConfigured {
		t.Fatalf("disable and clear status=%d body=%s", disabledAndCleared.Code, disabledAndCleared.Body)
	}
}

func TestCaptchaVerificationErrorsFailClosedAndRemainSanitized(t *testing.T) {
	verifier := &recordingCaptchaVerifier{result: captcha.ErrInvalid}
	api, _ := newTestAPIWithCaptcha(t, verifier)
	admin := loginAdmin(t, api)
	configured := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":1,"app_name":"Xboard-Go","app_description":"","app_url":"","tos_url":"","logo":"",
		"captcha_enable":true,"captcha_type":"recaptcha","recaptcha_site_key":"site","recaptcha_secret":"server-secret"
	}`)
	if configured.Code != http.StatusOK {
		t.Fatalf("configure status=%d body=%s", configured.Code, configured.Body)
	}
	tooLongSecret := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{"revision":2,"app_name":"Xboard-Go","app_description":"","app_url":"","tos_url":"","logo":"","recaptcha_secret":"`+strings.Repeat("x", 4097)+`"}`)
	expectAPIError(t, tooLongSecret, http.StatusUnprocessableEntity, "validation_failed")
	invalid := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{"email":"invalid-captcha@example.test","password":"strong-password-123","password_confirmation":"strong-password-123","recaptcha_data":"invalid-token"}`)
	expectAPIError(t, invalid, http.StatusBadRequest, "captcha_invalid")
	if strings.Contains(invalid.Body.String(), "server-secret") || strings.Contains(invalid.Body.String(), "invalid-token") {
		t.Fatalf("invalid response leaked credential: %s", invalid.Body)
	}
	verifier.mu.Lock()
	verifier.result = errors.New("upstream detail with secret")
	verifier.mu.Unlock()
	unavailable := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{"email":"unavailable-captcha@example.test","password":"strong-password-123","password_confirmation":"strong-password-123","recaptcha_data":"unavailable-token"}`)
	expectAPIError(t, unavailable, http.StatusServiceUnavailable, "captcha_unavailable")
	if strings.Contains(unavailable.Body.String(), "upstream detail") || strings.Contains(unavailable.Body.String(), "unavailable-token") {
		t.Fatalf("unavailable response leaked details: %s", unavailable.Body)
	}
}
