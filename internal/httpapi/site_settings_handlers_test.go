package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestSiteSettingsRegistrationTrialContractValidatesPlanAndStaysPrivate(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	now := time.Date(2026, 8, 29, 13, 0, 0, 0, time.UTC)
	plan, err := database.CreatePlan(t.Context(), store.SavePlanInput{
		Name: "API trial", TransferEnableGiB: 8, Prices: store.PlanPrices{}, Tags: []string{},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	configured := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", fmt.Sprintf(`{
		"revision":1,"app_name":"Xboard-Go","app_description":"","app_url":"","tos_url":"","logo":"",
		"try_out_plan_id":%d,"try_out_hour":48
	}`, plan.ID))
	if configured.Code != http.StatusOK {
		t.Fatalf("configure trial status=%d body=%s", configured.Code, configured.Body)
	}
	settings := decodeSiteSettingsEnvelope(t, configured)
	if settings.TrialPlanID != plan.ID || settings.TrialHours != 48 {
		t.Fatalf("configured trial settings=%#v", settings)
	}
	invalid := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", `{
		"revision":2,"app_name":"Xboard-Go","app_description":"","app_url":"","tos_url":"","logo":"",
		"try_out_plan_id":999999,"try_out_hour":0
	}`)
	expectAPIError(t, invalid, http.StatusUnprocessableEntity, "validation_failed")
	public := testClient{}.request(t, api, http.MethodGet, "/api/v1/guest/comm/config", "")
	if strings.Contains(public.Body.String(), "try_out_plan_id") || strings.Contains(public.Body.String(), "try_out_hour") {
		t.Fatalf("public config disclosed trial policy: %s", public.Body)
	}
}

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
		initialGuest.IsEmailVerify != 0 || initialGuest.EnableCouponSystem != 1 || initialGuest.IsCaptcha != 0 || initialGuest.CaptchaType != "recaptcha" || initialGuest.IsRecaptcha != 0 {
		t.Fatalf("initial guest config = %#v", initialGuest)
	}
	if string(initialGuest.EmailWhitelistSuffix) != "0" {
		t.Fatalf("disabled public email whitelist = %s, want 0", initialGuest.EmailWhitelistSuffix)
	}
	if strings.Contains(publicInitial.Body.String(), "smtp") || strings.Contains(publicInitial.Body.String(), "stop_register") {
		t.Fatalf("public config disclosed internal settings: %s", publicInitial.Body)
	}
	assertGuestConfigKeys(t, publicInitial)

	initialResponse := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/site-settings", "")
	if initialResponse.Code != http.StatusOK {
		t.Fatalf("initial admin settings status=%d body=%s", initialResponse.Code, initialResponse.Body)
	}
	initial := decodeSiteSettingsEnvelope(t, initialResponse)
	if initial.Revision != 1 || initial.AppName != "Xboard-Go" || !initial.CouponEnabled || !initial.PasswordLimitEnabled ||
		initial.PasswordLimitCount != 5 || initial.PasswordLimitMinutes != 60 || initial.SafeModeEnabled || initial.SecurePath != "admin" ||
		initial.ForceHTTPS || initial.SubscribeURL != "" {
		t.Fatalf("initial admin site settings = %#v", initial)
	}
	forbidden := reader.request(t, api, http.MethodGet, "/api/v1/admin/admin/site-settings", "")
	expectAPIError(t, forbidden, http.StatusForbidden, "forbidden")
	requiresSMTP := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", `{
		"revision":1,"app_name":"Xboard-Go","app_description":"","app_url":"","tos_url":"","logo":"",
		"email_verify":true
	}`)
	expectAPIError(t, requiresSMTP, http.StatusConflict, "registration_email_requires_smtp")
	mailLoginRequiresSMTP := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", `{
		"revision":1,"app_name":"Xboard-Go","app_description":"","app_url":"","tos_url":"","logo":"",
		"login_with_mail_link_enable":true
	}`)
	expectAPIError(t, mailLoginRequiresSMTP, http.StatusConflict, "mail_login_requires_smtp")

	updatedResponse := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", `{
		"revision":1,"app_name":"Example Board","app_description":"Fast and safe control plane",
		"app_url":"https://panel.example.test/","tos_url":"https://panel.example.test/terms/",
		"safe_mode_enable":false,"secure_path":"admin",
		"force_https":true,"subscribe_url":" https://subscribe-a.example.test/, https://subscribe-b.example.test/root ",
		"logo":"https://images.example.test/brand.svg","currency":" usd ","currency_symbol":" $ ","stop_register":true,
		"email_whitelist_enable":true,"email_whitelist_suffix":[" Allowed.Test ","allowed.test","GMAIL.COM"],
		"email_gmail_limit_enable":true,"register_limit_by_ip_enable":true,
		"register_limit_count":2,"register_limit_expire":30,
		"password_limit_enable":true,"password_limit_count":2,"password_limit_expire":30
		,"coupon_enabled":false
	}`)
	if updatedResponse.Code != http.StatusOK {
		t.Fatalf("update admin settings status=%d body=%s", updatedResponse.Code, updatedResponse.Body)
	}
	updated := decodeSiteSettingsEnvelope(t, updatedResponse)
	if updated.Revision != 2 || updated.AppName != "Example Board" || updated.AppDescription != "Fast and safe control plane" ||
		updated.AppURL != "https://panel.example.test/" || updated.TOSURL != "https://panel.example.test/terms/" ||
		updated.Logo != "https://images.example.test/brand.svg" || updated.Currency != "USD" || updated.CurrencySymbol != "$" || !updated.StopRegister || !updated.EmailWhitelistEnabled ||
		len(updated.EmailWhitelistSuffixes) != 2 || updated.EmailWhitelistSuffixes[0] != "allowed.test" || updated.EmailWhitelistSuffixes[1] != "gmail.com" ||
		!updated.GmailAliasLimitEnabled || !updated.RegistrationIPLimitEnabled || updated.RegistrationIPLimitCount != 2 ||
		updated.RegistrationIPLimitMinutes != 30 || !updated.PasswordLimitEnabled || updated.PasswordLimitCount != 2 ||
		updated.PasswordLimitMinutes != 30 || updated.CouponEnabled || !updated.ForceHTTPS ||
		updated.SubscribeURL != "https://subscribe-a.example.test,https://subscribe-b.example.test/root" || updated.SafeModeEnabled || updated.SecurePath != "admin" {
		t.Fatalf("updated admin settings = %#v", updated)
	}

	publicUpdated := testClient{}.request(t, api, http.MethodGet, "/api/v1/guest/comm/config", "")
	guest := decodeGuestConfigEnvelope(t, publicUpdated)
	if guest.AppName != updated.AppName || stringValue(guest.AppDescription) != updated.AppDescription ||
		stringValue(guest.AppURL) != updated.AppURL || stringValue(guest.TOSURL) != updated.TOSURL ||
		stringValue(guest.Logo) != updated.Logo || guest.EnableCouponSystem != 0 {
		t.Fatalf("public config did not observe update: %#v", guest)
	}
	var publicSuffixes []string
	if err := json.Unmarshal(guest.EmailWhitelistSuffix, &publicSuffixes); err != nil || len(publicSuffixes) != 2 || publicSuffixes[0] != "allowed.test" || publicSuffixes[1] != "gmail.com" {
		t.Fatalf("public whitelist suffixes = %q, decoded=%#v err=%v", guest.EmailWhitelistSuffix, publicSuffixes, err)
	}
	for _, internalKey := range []string{"safe_mode_enable", "secure_path", "force_https", "subscribe_url", "email_whitelist_enable", "email_gmail_limit_enable", "register_limit_by_ip_enable", "register_limit_count", "register_limit_expire", "password_limit_enable", "password_limit_count", "password_limit_expire", "login_with_mail_link_enable"} {
		if strings.Contains(publicUpdated.Body.String(), internalKey) {
			t.Fatalf("public config disclosed internal policy %q: %s", internalKey, publicUpdated.Body)
		}
	}
	preservedResponse := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", `{
		"revision":2,"app_name":"Example Board","app_description":"Fast and safe control plane",
		"app_url":"https://panel.example.test/","tos_url":"https://panel.example.test/terms/",
		"logo":"https://images.example.test/brand.svg"
	}`)
	if preservedResponse.Code != http.StatusOK {
		t.Fatalf("legacy-shape settings update status=%d body=%s", preservedResponse.Code, preservedResponse.Body)
	}
	preserved := decodeSiteSettingsEnvelope(t, preservedResponse)
	if preserved.Revision != 3 || preserved.Currency != "USD" || preserved.CurrencySymbol != "$" || !preserved.StopRegister || !preserved.EmailWhitelistEnabled ||
		len(preserved.EmailWhitelistSuffixes) != 2 || !preserved.GmailAliasLimitEnabled || !preserved.RegistrationIPLimitEnabled ||
		preserved.RegistrationIPLimitCount != 2 || preserved.RegistrationIPLimitMinutes != 30 ||
		!preserved.PasswordLimitEnabled || preserved.PasswordLimitCount != 2 || preserved.PasswordLimitMinutes != 30 || preserved.CouponEnabled ||
		!preserved.ForceHTTPS || preserved.SubscribeURL != updated.SubscribeURL || preserved.SafeModeEnabled || preserved.SecurePath != "admin" {
		t.Fatalf("legacy-shape settings update lost registration policy fields: %#v", preserved)
	}

	stale := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", `{
		"revision":1,"app_name":"Stale","app_description":"","app_url":"","tos_url":""
	}`)
	expectAPIError(t, stale, http.StatusConflict, "settings_conflict")
	invalid := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", `{
		"revision":3,"app_name":"Example","app_description":"","app_url":"","tos_url":"",
		"logo":"https://user@example.test/logo.png"
	}`)
	expectAPIError(t, invalid, http.StatusUnprocessableEntity, "validation_failed")
	invalidCurrency := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", `{
		"revision":3,"app_name":"Example","app_description":"","app_url":"","tos_url":"","logo":"",
		"currency":"US","currency_symbol":"$"
	}`)
	expectAPIError(t, invalidCurrency, http.StatusUnprocessableEntity, "validation_failed")
	invalidWhitelist := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", `{
		"revision":3,"app_name":"Example","app_description":"","app_url":"","tos_url":"","logo":"",
		"email_whitelist_enable":true,"email_whitelist_suffix":["*.example.test"]
	}`)
	expectAPIError(t, invalidWhitelist, http.StatusUnprocessableEntity, "validation_failed")
	invalidSubscribeURL := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", `{
		"revision":3,"app_name":"Example","app_description":"","app_url":"","tos_url":"","logo":"",
		"subscribe_url":"http://external.example.test"
	}`)
	expectAPIError(t, invalidSubscribeURL, http.StatusUnprocessableEntity, "validation_failed")
	invalidIPLimit := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", `{
		"revision":3,"app_name":"Example","app_description":"","app_url":"","tos_url":"","logo":"",
		"register_limit_count":0
	}`)
	expectAPIError(t, invalidIPLimit, http.StatusUnprocessableEntity, "validation_failed")
	invalidPasswordLimit := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", `{
		"revision":3,"app_name":"Example","app_description":"","app_url":"","tos_url":"","logo":"",
		"password_limit_count":0
	}`)
	expectAPIError(t, invalidPasswordLimit, http.StatusUnprocessableEntity, "validation_failed")
	unknown := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", `{
		"revision":3,"app_name":"Example","app_description":"","app_url":"","tos_url":"","smtp_password":"secret"
	}`)
	expectAPIError(t, unknown, http.StatusBadRequest, "invalid_json")
	withoutCSRF := admin
	withoutCSRF.csrf = ""
	csrfRejected := withoutCSRF.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", `{
		"revision":3,"app_name":"No CSRF","app_description":"","app_url":"","tos_url":""
	}`)
	expectAPIError(t, csrfRejected, http.StatusForbidden, "csrf_failed")

	current, err := database.GetSiteSettings(t.Context())
	if err != nil || current.Revision != 3 || current.AppName != updated.AppName || !current.StopRegister || !current.ForceHTTPS || current.SubscribeURL != updated.SubscribeURL ||
		!current.EmailWhitelistEnabled || !current.GmailAliasLimitEnabled || !current.RegistrationIPLimitEnabled {
		t.Fatalf("rejected updates changed persistent state: settings=%#v err=%v", current, err)
	}
}

type guestConfigContract struct {
	AppName              string          `json:"app_name"`
	AppDescription       *string         `json:"app_description"`
	AppURL               *string         `json:"app_url"`
	TOSURL               *string         `json:"tos_url"`
	Logo                 *string         `json:"logo"`
	IsEmailVerify        int             `json:"is_email_verify"`
	IsInviteForce        int             `json:"is_invite_force"`
	EnableCouponSystem   int             `json:"enable_coupon_system"`
	EmailWhitelistSuffix json.RawMessage `json:"email_whitelist_suffix"`
	IsCaptcha            int             `json:"is_captcha"`
	CaptchaType          string          `json:"captcha_type"`
	RecaptchaSiteKey     *string         `json:"recaptcha_site_key"`
	RecaptchaV3SiteKey   *string         `json:"recaptcha_v3_site_key"`
	RecaptchaV3Threshold float64         `json:"recaptcha_v3_score_threshold"`
	TurnstileSiteKey     *string         `json:"turnstile_site_key"`
	IsRecaptcha          int             `json:"is_recaptcha"`
	IsTelegram           int             `json:"is_telegram"`
	TelegramDiscussLink  *string         `json:"telegram_discuss_link"`
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
		"app_name", "app_description", "app_url", "logo", "is_recaptcha", "enable_coupon_system",
		"is_telegram", "telegram_discuss_link", "theme",
	} {
		if _, ok := envelope.Data[key]; !ok {
			t.Errorf("guest config key %q is missing", key)
		}
	}
}
