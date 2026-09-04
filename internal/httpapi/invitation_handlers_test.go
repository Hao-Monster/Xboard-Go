package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestInvitationHTTPGenerationPVRegistrationAndPrivacy(t *testing.T) {
	api, database := newTestAPI(t)
	owner := loginAdmin(t, api)
	administrator, err := database.FindUserByEmail(t.Context(), "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	updateInvitationPolicyHTTP(t, database, administrator.ID, false, 1, false, fixedNow())

	withoutCSRF := owner
	withoutCSRF.csrf = ""
	if response := withoutCSRF.request(t, api, http.MethodPost, "/api/v1/invitations", `{}`); response.Code != http.StatusForbidden {
		t.Fatalf("generate without CSRF status=%d body=%s", response.Code, response.Body)
	}
	generated := owner.request(t, api, http.MethodPost, "/api/v1/invitations", `{}`)
	if generated.Code != http.StatusOK {
		t.Fatalf("generate status=%d body=%s", generated.Code, generated.Body)
	}
	var generatedPayload struct {
		Data struct {
			Code string `json:"code"`
		} `json:"data"`
	}
	decodeResponse(t, generated, &generatedPayload)
	code := generatedPayload.Data.Code
	if len(code) != 8 {
		t.Fatalf("generated code=%q", code)
	}
	limited := owner.request(t, api, http.MethodPost, "/api/v1/invitations", `{}`)
	assertAPIError(t, limited, http.StatusBadRequest, "invitation_code_limit", "已达到创建数量上限")

	viewBody, _ := json.Marshal(map[string]string{"invite_code": code})
	viewed := plainAPIRequest(api, http.MethodPost, "/api/v1/invitations/view", string(viewBody))
	if viewed.Code != http.StatusOK {
		t.Fatalf("view status=%d body=%s", viewed.Code, viewed.Body)
	}
	unknown := plainAPIRequest(api, http.MethodPost, "/api/v1/invitations/view", `{"invite_code":"Badc1234"}`)
	if unknown.Code != http.StatusOK || unknown.Body.String() != viewed.Body.String() {
		t.Fatalf("unknown view leaked existence: known=%s unknown=%s", viewed.Body, unknown.Body)
	}
	crossOriginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/invitations/view", strings.NewReader(string(viewBody)))
	crossOriginRequest.Header.Set("Content-Type", "application/json")
	crossOriginRequest.Header.Set("Origin", "https://attacker.example")
	crossOrigin := httptest.NewRecorder()
	api.ServeHTTP(crossOrigin, crossOriginRequest)
	if crossOrigin.Code != http.StatusForbidden {
		t.Fatalf("cross-origin view status=%d body=%s", crossOrigin.Code, crossOrigin.Body)
	}

	summary := owner.request(t, api, http.MethodGet, "/api/v1/invitations", "")
	if summary.Code != http.StatusOK {
		t.Fatalf("summary status=%d body=%s", summary.Code, summary.Body)
	}
	var summaryPayload struct {
		Data struct {
			Codes []struct {
				Code string `json:"code"`
				PV   int64  `json:"pv"`
			} `json:"codes"`
			InvitedCount int64 `json:"invited_count"`
		} `json:"data"`
	}
	decodeResponse(t, summary, &summaryPayload)
	if len(summaryPayload.Data.Codes) != 1 || summaryPayload.Data.Codes[0].Code != code || summaryPayload.Data.Codes[0].PV != 1 || summaryPayload.Data.InvitedCount != 0 {
		t.Fatalf("summary payload=%#v", summaryPayload.Data)
	}
	if strings.Contains(summary.Body.String(), `"user_id"`) || strings.Contains(summary.Body.String(), `"code_digest"`) || strings.Contains(summary.Body.String(), `"code_cipher"`) {
		t.Fatalf("summary leaked internal identifiers: %s", summary.Body)
	}
	if !containsAll(summary.Body.String(), `"valid_commission":0`, `"pending_commission":0`, `"commission_rate":10`, `"available_commission":0`) {
		t.Fatalf("summary omitted legacy commission statistics: %s", summary.Body)
	}
	commissionLogs := owner.request(t, api, http.MethodGet, "/api/v1/invitations/commissions?page=1&page_size=10", "")
	if commissionLogs.Code != http.StatusOK || !containsAll(commissionLogs.Body.String(), `"items":[]`, `"total":0`, `"page":1`, `"page_size":10`) {
		t.Fatalf("empty commission logs status=%d body=%s", commissionLogs.Code, commissionLogs.Body)
	}
	invalidTransfer := owner.request(t, api, http.MethodPost, "/api/v1/invitations/transfer", `{"amount":0}`)
	assertAPIError(t, invalidTransfer, http.StatusUnprocessableEntity, "validation_failed", "划转金额必须大于 0")
	legacyFetch := owner.request(t, api, http.MethodGet, "/api/v1/user/invite/fetch", "")
	if legacyFetch.Code != http.StatusOK || !containsAll(legacyFetch.Body.String(), `"stat":[0,0,0,10,0]`, `"codes":[`) {
		t.Fatalf("legacy invite fetch status=%d body=%s", legacyFetch.Code, legacyFetch.Body)
	}
	legacyDetails := owner.request(t, api, http.MethodGet, "/api/v1/user/invite/details?current=1&page_size=10", "")
	if legacyDetails.Code != http.StatusOK || legacyDetails.Body.String() != `{"data":[],"total":0}`+"\n" {
		t.Fatalf("legacy invite details status=%d body=%s", legacyDetails.Code, legacyDetails.Body)
	}

	updateInvitationPolicyHTTP(t, database, administrator.ID, true, 1, false, fixedNow().Add(time.Minute))
	missing := plainAPIRequest(api, http.MethodPost, "/api/v1/auth/register", `{"email":"missing-invite@example.test","password":"password-123","password_confirmation":"password-123"}`)
	assertAPIError(t, missing, http.StatusUnprocessableEntity, "invitation_code_required", "必须使用邀请码才可以注册")
	invalid := plainAPIRequest(api, http.MethodPost, "/api/v1/auth/register", `{"email":"invalid-invite@example.test","password":"password-123","password_confirmation":"password-123","invite_code":"Badc1234"}`)
	assertAPIError(t, invalid, http.StatusBadRequest, "invitation_code_invalid", "邀请码无效")
	validBody, _ := json.Marshal(map[string]string{
		"email": "invited@example.test", "password": "password-123", "password_confirmation": "password-123", "invite_code": code,
	})
	valid := plainAPIRequest(api, http.MethodPost, "/api/v1/auth/register", string(validBody))
	if valid.Code != http.StatusOK {
		t.Fatalf("valid invitation registration status=%d body=%s", valid.Code, valid.Body)
	}
	reusedBody, _ := json.Marshal(map[string]string{
		"email": "reused@example.test", "password": "password-123", "password_confirmation": "password-123", "invite_code": code,
	})
	reused := plainAPIRequest(api, http.MethodPost, "/api/v1/auth/register", string(reusedBody))
	assertAPIError(t, reused, http.StatusBadRequest, "invitation_code_invalid", "邀请码无效")

	summary = owner.request(t, api, http.MethodGet, "/api/v1/invitations", "")
	decodeResponse(t, summary, &summaryPayload)
	if len(summaryPayload.Data.Codes) != 0 || summaryPayload.Data.InvitedCount != 1 {
		t.Fatalf("consumed summary=%#v", summaryPayload.Data)
	}
	updateInvitationPolicyHTTP(t, database, administrator.ID, false, 1, false, fixedNow().Add(2*time.Minute))
	optional := plainAPIRequest(api, http.MethodPost, "/api/v1/auth/register", `{"email":"optional-invite@example.test","password":"password-123","password_confirmation":"password-123","invite_code":"NotARealCode"}`)
	if optional.Code != http.StatusOK {
		t.Fatalf("optional invalid status=%d body=%s", optional.Code, optional.Body)
	}
	summary = owner.request(t, api, http.MethodGet, "/api/v1/invitations", "")
	decodeResponse(t, summary, &summaryPayload)
	if summaryPayload.Data.InvitedCount != 1 {
		t.Fatalf("optional invalid changed invited count: %#v", summaryPayload.Data)
	}
}

func TestGuestInvitationForceAndProtectedSettings(t *testing.T) {
	api, _ := newTestAPI(t)
	administrator := loginAdmin(t, api)
	settings := administrator.request(t, api, http.MethodGet, "/api/v1/admin/admin/site-settings", "")
	if settings.Code != http.StatusOK {
		t.Fatal(settings.Body.String())
	}
	var payload struct {
		Data store.SiteSettings `json:"data"`
	}
	decodeResponse(t, settings, &payload)
	input := map[string]any{
		"revision": payload.Data.Revision, "app_name": payload.Data.AppName, "app_description": payload.Data.AppDescription,
		"app_url": payload.Data.AppURL, "tos_url": payload.Data.TOSURL, "logo": payload.Data.Logo,
		"stop_register": payload.Data.StopRegister, "email_verify": payload.Data.EmailVerificationEnabled,
		"email_whitelist_enable": payload.Data.EmailWhitelistEnabled, "email_whitelist_suffix": payload.Data.EmailWhitelistSuffixes,
		"email_gmail_limit_enable":    payload.Data.GmailAliasLimitEnabled,
		"register_limit_by_ip_enable": payload.Data.RegistrationIPLimitEnabled,
		"register_limit_count":        payload.Data.RegistrationIPLimitCount, "register_limit_expire": payload.Data.RegistrationIPLimitMinutes,
		"invite_force": true, "invite_gen_limit": 7, "invite_never_expire": true,
	}
	body, _ := json.Marshal(input)
	updated := administrator.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", string(body))
	if updated.Code != http.StatusOK {
		t.Fatalf("settings update status=%d body=%s", updated.Code, updated.Body)
	}
	guest := plainAPIRequest(api, http.MethodGet, "/api/v1/guest/comm/config", "")
	var guestPayload struct {
		Data struct {
			IsInviteForce int `json:"is_invite_force"`
		} `json:"data"`
	}
	decodeResponse(t, guest, &guestPayload)
	if guestPayload.Data.IsInviteForce != 1 {
		t.Fatalf("guest invitation force=%d", guestPayload.Data.IsInviteForce)
	}
}

func TestLegacyInvitationMutationRequiresBearerAndStrictTransferForm(t *testing.T) {
	api, database := newTestAPI(t)
	cookie := loginAdmin(t, api)
	administrator, err := database.FindUserByEmail(t.Context(), "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	updateInvitationPolicyHTTP(t, database, administrator.ID, false, 2, false, fixedNow())

	unsafeCookieGET := cookie.request(t, api, http.MethodGet, "/api/v1/user/invite/save", "")
	if unsafeCookieGET.Code != http.StatusForbidden || !strings.Contains(unsafeCookieGET.Body.String(), "旧版邀请码生成仅支持访问令牌") {
		t.Fatalf("cookie-authenticated legacy GET mutation status=%d body=%s", unsafeCookieGET.Code, unsafeCookieGET.Body)
	}
	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	created := bearerRequest(api, http.MethodGet, "/api/v1/user/invite/save", authorization, "")
	if created.Code != http.StatusOK || !containsAll(created.Body.String(), `"status":"success"`, `"data":true`) {
		t.Fatalf("bearer legacy invitation creation status=%d body=%s", created.Code, created.Body)
	}
	fetched := bearerRequest(api, http.MethodGet, "/api/v1/user/invite/fetch", authorization, "")
	if fetched.Code != http.StatusOK || !containsAll(fetched.Body.String(), `"codes":[{`, `"stat":[0,0,0,10,0]`) {
		t.Fatalf("bearer legacy invitation fetch status=%d body=%s", fetched.Code, fetched.Body)
	}

	wrongMediaType := bearerRequest(api, http.MethodPost, "/api/v1/user/transfer", authorization, `{"transfer_amount":1}`)
	if wrongMediaType.Code != http.StatusUnprocessableEntity {
		t.Fatalf("JSON legacy transfer status=%d body=%s", wrongMediaType.Code, wrongMediaType.Body)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/user/transfer", strings.NewReader("transfer_amount=1"))
	request.Header.Set("Authorization", authorization)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "佣金余额不足") {
		t.Fatalf("form legacy transfer status=%d body=%s", response.Code, response.Body)
	}
	modernOverdraw := cookie.request(t, api, http.MethodPost, "/api/v1/invitations/transfer", `{"amount":1}`)
	assertAPIError(t, modernOverdraw, http.StatusConflict, "insufficient_commission", "佣金余额不足")
}

func TestInvitationViewRateLimitAndStrictInput(t *testing.T) {
	api, _ := newTestAPI(t)
	for attempt := 1; attempt <= 60; attempt++ {
		response := plainAPIRequest(api, http.MethodPost, "/api/v1/invitations/view", `{"invite_code":"Badc1234"}`)
		if response.Code != http.StatusOK {
			t.Fatalf("view attempt %d status=%d body=%s", attempt, response.Code, response.Body)
		}
	}
	limited := plainAPIRequest(api, http.MethodPost, "/api/v1/invitations/view", `{"invite_code":"Badc1234"}`)
	assertAPIError(t, limited, http.StatusTooManyRequests, "invitation_view_rate_limited", "请求过于频繁，请稍后重试")
	if limited.Header().Get("Retry-After") != "900" {
		t.Fatalf("view Retry-After=%q", limited.Header().Get("Retry-After"))
	}

	unknownFieldRequest := httptest.NewRequest(http.MethodPost, "/api/v1/invitations/view", strings.NewReader(`{"invite_code":"Badc1234","unexpected":true}`))
	unknownFieldRequest.Header.Set("Content-Type", "application/json")
	unknownFieldRequest.RemoteAddr = "198.51.100.8:1234"
	unknownField := httptest.NewRecorder()
	api.ServeHTTP(unknownField, unknownFieldRequest)
	if unknownField.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", unknownField.Code, unknownField.Body)
	}
}

func TestInvitationHTTPFailsClosedWithoutProtectionButKeepsOptionalRegistration(t *testing.T) {
	api, _ := newTestAPIWithoutInvitationProtection(t)
	administrator := loginAdmin(t, api)

	if response := administrator.request(t, api, http.MethodGet, "/api/v1/invitations", ""); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unprotected invitation list status=%d body=%s", response.Code, response.Body)
	}
	settings := administrator.request(t, api, http.MethodGet, "/api/v1/admin/admin/site-settings", "")
	var payload struct {
		Data store.SiteSettings `json:"data"`
	}
	decodeResponse(t, settings, &payload)
	body, _ := json.Marshal(map[string]any{
		"revision": payload.Data.Revision, "app_name": payload.Data.AppName,
		"invite_force": true,
	})
	forced := administrator.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", string(body))
	assertAPIError(t, forced, http.StatusServiceUnavailable, "settings_encryption_unavailable", "服务器未配置邀请码加密密钥")

	optional := plainAPIRequest(api, http.MethodPost, "/api/v1/auth/register", `{"email":"optional-no-key@example.test","password":"password-123","password_confirmation":"password-123","invite_code":"Badc1234"}`)
	if optional.Code != http.StatusOK {
		t.Fatalf("optional unprotected registration status=%d body=%s", optional.Code, optional.Body)
	}
}

func updateInvitationPolicyHTTP(t testing.TB, database *store.Store, administratorID int64, force bool, limit int, neverExpire bool, now time.Time) {
	t.Helper()
	settings, err := database.GetSiteSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.UpdateSiteSettings(t.Context(), administratorID, settings.Revision, store.SaveSiteSettingsInput{
		AppName: settings.AppName, AppDescription: settings.AppDescription, AppURL: settings.AppURL,
		TOSURL: settings.TOSURL, Logo: settings.Logo, StopRegister: settings.StopRegister,
		EmailVerificationEnabled: settings.EmailVerificationEnabled,
		EmailWhitelistEnabled:    settings.EmailWhitelistEnabled, EmailWhitelistSuffixes: settings.EmailWhitelistSuffixes,
		GmailAliasLimitEnabled:     settings.GmailAliasLimitEnabled,
		RegistrationIPLimitEnabled: settings.RegistrationIPLimitEnabled,
		RegistrationIPLimitCount:   settings.RegistrationIPLimitCount, RegistrationIPLimitMinutes: settings.RegistrationIPLimitMinutes,
		PasswordLimitEnabled: settings.PasswordLimitEnabled, PasswordLimitCount: settings.PasswordLimitCount,
		PasswordLimitMinutes:   settings.PasswordLimitMinutes,
		InvitationForceEnabled: force, InvitationCodeLimit: limit, InvitationNeverExpire: neverExpire,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
}

func assertAPIError(t *testing.T, response *httptest.ResponseRecorder, status int, code, message string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d want=%d body=%s", response.Code, status, response.Body)
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeResponse(t, response, &payload)
	if payload.Error.Code != code || payload.Error.Message != message {
		t.Fatalf("error=%#v want code=%q message=%q", payload.Error, code, message)
	}
}

func plainAPIRequest(api http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}
