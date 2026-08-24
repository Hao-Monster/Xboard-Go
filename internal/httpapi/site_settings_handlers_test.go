package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestSiteSettingsAdminAndPublicContracts(t *testing.T) {
	api, database := newTestAPI(t)
	createHTTPTestUser(t, database, "site-reader@example.test", "site-reader-password-123")
	admin := loginAdmin(t, api)
	reader := loginAs(t, api, "site-reader@example.test", "site-reader-password-123")

	publicInitial := testClient{}.request(t, api, http.MethodGet, "/api/v1/guest/comm/config", "")
	if publicInitial.Code != http.StatusOK || publicInitial.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("initial public config status=%d cache=%q body=%s", publicInitial.Code, publicInitial.Header().Get("Cache-Control"), publicInitial.Body)
	}
	initialGuest := decodeGuestConfigEnvelope(t, publicInitial)
	if initialGuest.AppName != "Xboard-Go" || initialGuest.AppDescription != nil || initialGuest.AppURL != nil || initialGuest.TOSURL != nil || initialGuest.Logo != nil ||
		initialGuest.IsEmailVerify != 0 || initialGuest.IsCaptcha != 0 || initialGuest.CaptchaType != "recaptcha" || initialGuest.IsRecaptcha != 0 {
		t.Fatalf("initial guest config = %#v", initialGuest)
	}
	if strings.Contains(publicInitial.Body.String(), "revision") || strings.Contains(publicInitial.Body.String(), "smtp") || strings.Contains(publicInitial.Body.String(), "stop_register") {
		t.Fatalf("public config disclosed internal settings: %s", publicInitial.Body)
	}
	assertGuestConfigKeys(t, publicInitial)

	initialResponse := admin.request(t, api, http.MethodGet, "/api/v1/admin/site-settings", "")
	if initialResponse.Code != http.StatusOK {
		t.Fatalf("initial admin settings status=%d body=%s", initialResponse.Code, initialResponse.Body)
	}
	initial := decodeSiteSettingsEnvelope(t, initialResponse)
	if initial.Revision != 1 || initial.AppName != "Xboard-Go" {
		t.Fatalf("initial admin site settings = %#v", initial)
	}
	forbidden := reader.request(t, api, http.MethodGet, "/api/v1/admin/site-settings", "")
	expectAPIError(t, forbidden, http.StatusForbidden, "forbidden")

	updatedResponse := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":1,"app_name":"Example Board","app_description":"Fast and safe control plane",
		"app_url":"https://panel.example.test/","tos_url":"https://panel.example.test/terms/",
		"logo":"https://images.example.test/brand.svg","stop_register":true
	}`)
	if updatedResponse.Code != http.StatusOK {
		t.Fatalf("update admin settings status=%d body=%s", updatedResponse.Code, updatedResponse.Body)
	}
	updated := decodeSiteSettingsEnvelope(t, updatedResponse)
	if updated.Revision != 2 || updated.AppName != "Example Board" || updated.AppDescription != "Fast and safe control plane" ||
		updated.AppURL != "https://panel.example.test/" || updated.TOSURL != "https://panel.example.test/terms/" ||
		updated.Logo != "https://images.example.test/brand.svg" || !updated.StopRegister {
		t.Fatalf("updated admin settings = %#v", updated)
	}

	publicUpdated := testClient{}.request(t, api, http.MethodGet, "/api/v1/guest/comm/config", "")
	guest := decodeGuestConfigEnvelope(t, publicUpdated)
	if guest.AppName != updated.AppName || stringValue(guest.AppDescription) != updated.AppDescription ||
		stringValue(guest.AppURL) != updated.AppURL || stringValue(guest.TOSURL) != updated.TOSURL ||
		stringValue(guest.Logo) != updated.Logo {
		t.Fatalf("public config did not observe update: %#v", guest)
	}
	preservedResponse := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":2,"app_name":"Example Board","app_description":"Fast and safe control plane",
		"app_url":"https://panel.example.test/","tos_url":"https://panel.example.test/terms/",
		"logo":"https://images.example.test/brand.svg"
	}`)
	if preservedResponse.Code != http.StatusOK {
		t.Fatalf("legacy-shape settings update status=%d body=%s", preservedResponse.Code, preservedResponse.Body)
	}
	preserved := decodeSiteSettingsEnvelope(t, preservedResponse)
	if preserved.Revision != 3 || !preserved.StopRegister {
		t.Fatalf("legacy-shape settings update reopened registration: %#v", preserved)
	}

	stale := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":1,"app_name":"Stale","app_description":"","app_url":"","tos_url":""
	}`)
	expectAPIError(t, stale, http.StatusConflict, "settings_conflict")
	invalid := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":3,"app_name":"Example","app_description":"","app_url":"","tos_url":"",
		"logo":"https://user@example.test/logo.png"
	}`)
	expectAPIError(t, invalid, http.StatusUnprocessableEntity, "validation_failed")
	unknown := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":3,"app_name":"Example","app_description":"","app_url":"","tos_url":"","smtp_password":"secret"
	}`)
	expectAPIError(t, unknown, http.StatusBadRequest, "invalid_json")
	withoutCSRF := admin
	withoutCSRF.csrf = ""
	csrfRejected := withoutCSRF.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":3,"app_name":"No CSRF","app_description":"","app_url":"","tos_url":""
	}`)
	expectAPIError(t, csrfRejected, http.StatusForbidden, "csrf_failed")

	current, err := database.GetSiteSettings(t.Context())
	if err != nil || current.Revision != 3 || current.AppName != updated.AppName || !current.StopRegister {
		t.Fatalf("rejected updates changed persistent state: settings=%#v err=%v", current, err)
	}
}

type guestConfigContract struct {
	AppName              string  `json:"app_name"`
	AppDescription       *string `json:"app_description"`
	AppURL               *string `json:"app_url"`
	TOSURL               *string `json:"tos_url"`
	Logo                 *string `json:"logo"`
	IsEmailVerify        int     `json:"is_email_verify"`
	IsInviteForce        int     `json:"is_invite_force"`
	EmailWhitelistSuffix int     `json:"email_whitelist_suffix"`
	IsCaptcha            int     `json:"is_captcha"`
	CaptchaType          string  `json:"captcha_type"`
	RecaptchaSiteKey     *string `json:"recaptcha_site_key"`
	RecaptchaV3SiteKey   *string `json:"recaptcha_v3_site_key"`
	RecaptchaV3Threshold float64 `json:"recaptcha_v3_score_threshold"`
	TurnstileSiteKey     *string `json:"turnstile_site_key"`
	IsRecaptcha          int     `json:"is_recaptcha"`
}

func decodeSiteSettingsEnvelope(t *testing.T, response *httptest.ResponseRecorder) store.SiteSettings {
	t.Helper()
	var envelope struct {
		Data store.SiteSettings `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode site settings: %v", err)
	}
	return envelope.Data
}

func decodeGuestConfigEnvelope(t *testing.T, response *httptest.ResponseRecorder) guestConfigContract {
	t.Helper()
	var envelope struct {
		Data guestConfigContract `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode guest config: %v", err)
	}
	return envelope.Data
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func assertGuestConfigKeys(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"tos_url", "is_email_verify", "is_invite_force", "email_whitelist_suffix", "is_captcha", "captcha_type",
		"recaptcha_site_key", "recaptcha_v3_site_key", "recaptcha_v3_score_threshold", "turnstile_site_key",
		"app_name", "app_description", "app_url", "logo", "is_recaptcha",
	} {
		if _, ok := envelope.Data[key]; !ok {
			t.Errorf("guest config key %q is missing", key)
		}
	}
}
