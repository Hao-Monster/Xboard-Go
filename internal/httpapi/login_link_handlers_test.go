package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestQuickLoginLinkRequiresSessionAndCSRFAndExchangesOnce(t *testing.T) {
	api, _ := newTestAPI(t)
	unauthenticated := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/quick-link", `{"redirect":"invite"}`)
	expectAPIError(t, unauthenticated, http.StatusUnauthorized, "unauthenticated")

	client := loginAdmin(t, api)
	missingCSRFRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/quick-link", strings.NewReader(`{"redirect":"invite"}`))
	missingCSRFRequest.Header.Set("Content-Type", "application/json")
	client.addCookies(missingCSRFRequest)
	missingCSRF := httptest.NewRecorder()
	api.ServeHTTP(missingCSRF, missingCSRFRequest)
	expectAPIError(t, missingCSRF, http.StatusForbidden, "csrf_failed")

	created := client.request(t, api, http.MethodPost, "/api/v1/auth/quick-link", `{"redirect":"invite"}`)
	if created.Code != http.StatusOK {
		t.Fatalf("quick link status=%d body=%s", created.Code, created.Body)
	}
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			URL       string `json:"url"`
			ExpiresIn int    `json:"expires_in"`
		} `json:"data"`
	}
	decodeResponse(t, created, &payload)
	token, redirect := loginLinkURLValues(t, payload.Data.URL)
	if payload.Status != "success" || payload.Data.ExpiresIn != 60 || redirect != "invite" {
		t.Fatalf("quick link payload=%#v token=%q redirect=%q", payload, token, redirect)
	}

	exchanged := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/login-link/exchange", `{"token":"`+token+`"}`)
	if exchanged.Code != http.StatusOK || exchanged.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("exchange status=%d cache=%q body=%s", exchanged.Code, exchanged.Header().Get("Cache-Control"), exchanged.Body)
	}
	var exchangePayload struct {
		Status string `json:"status"`
		Data   struct {
			ID       int64  `json:"id"`
			Email    string `json:"email"`
			IsAdmin  bool   `json:"is_admin"`
			Redirect string `json:"redirect"`
		} `json:"data"`
	}
	decodeResponse(t, exchanged, &exchangePayload)
	if exchangePayload.Data.Email != "admin@example.test" || !exchangePayload.Data.IsAdmin || exchangePayload.Data.Redirect != "invite" {
		t.Fatalf("exchange payload=%#v", exchangePayload)
	}
	if len(exchanged.Result().Cookies()) != 2 {
		t.Fatalf("exchange cookies=%#v", exchanged.Result().Cookies())
	}

	replayed := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/login-link/exchange", `{"token":"`+token+`"}`)
	expectAPIError(t, replayed, http.StatusBadRequest, "login_link_invalid")
}

func TestQuickLoginLinkNormalizesUnsafeRedirectAndSupportsLegacyRoutes(t *testing.T) {
	api, _ := newTestAPI(t)
	client := loginAdmin(t, api)

	legacy := client.request(t, api, http.MethodPost, "/api/v1/user/getQuickLoginUrl", `{"redirect":"https://attacker.example.test/steal"}`)
	if legacy.Code != http.StatusOK {
		t.Fatalf("legacy quick link status=%d body=%s", legacy.Code, legacy.Body)
	}
	var payload struct {
		Data string `json:"data"`
	}
	decodeResponse(t, legacy, &payload)
	token, redirect := loginLinkURLValues(t, payload.Data)
	if redirect != "dashboard" {
		t.Fatalf("unsafe redirect normalized to %q", redirect)
	}

	redirectOnly := testClient{}.request(t, api, http.MethodGet, "/api/v1/passport/auth/token2Login?token="+token+"&redirect=https%3A%2F%2Fattacker.example.test", "")
	if redirectOnly.Code != http.StatusFound {
		t.Fatalf("legacy token redirect status=%d body=%s", redirectOnly.Code, redirectOnly.Body)
	}
	location := redirectOnly.Header().Get("Location")
	redirectedToken, redirectedPage := loginLinkURLValues(t, location)
	if redirectedToken != token || redirectedPage != "dashboard" {
		t.Fatalf("legacy redirect location=%q token=%q redirect=%q", location, redirectedToken, redirectedPage)
	}

	verified := testClient{}.request(t, api, http.MethodGet, "/api/v1/passport/auth/token2Login?verify="+token, "")
	if verified.Code != http.StatusOK || verified.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("legacy verify status=%d referrer=%q body=%s", verified.Code, verified.Header().Get("Referrer-Policy"), verified.Body)
	}
	var verifiedPayload struct {
		Data struct {
			Authorization string `json:"auth_data"`
			Token         string `json:"token"`
		} `json:"data"`
	}
	decodeResponse(t, verified, &verifiedPayload)
	if !strings.HasPrefix(verifiedPayload.Data.Authorization, "Bearer ") || verifiedPayload.Data.Token == "" {
		t.Fatalf("legacy verify payload=%#v", verifiedPayload.Data)
	}
	if response := bearerRequest(api, http.MethodGet, "/api/v1/auth/session", verifiedPayload.Data.Authorization, ""); response.Code != http.StatusOK {
		t.Fatalf("legacy exchanged bearer status=%d body=%s", response.Code, response.Body)
	}
}

func TestLegacyPassportQuickLinkAuthenticatesBodyBearerWithoutCookieCSRF(t *testing.T) {
	api, _ := newTestAPI(t)
	credential := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123")
	created := bearerRequest(api, http.MethodPost, "/api/v1/passport/auth/getQuickLoginUrl", "",
		`{"auth_data":"`+credential.Authorization+`","redirect":"invite"}`)
	if created.Code != http.StatusOK {
		t.Fatalf("legacy passport quick link status=%d body=%s", created.Code, created.Body)
	}
	var payload struct {
		Data string `json:"data"`
	}
	decodeResponse(t, created, &payload)
	_, redirect := loginLinkURLValues(t, payload.Data)
	if redirect != "invite" {
		t.Fatalf("legacy passport redirect=%q", redirect)
	}
	denied := bearerRequest(api, http.MethodPost, "/api/v1/passport/auth/getQuickLoginUrl", "",
		`{"auth_data":"Bearer invalid","redirect":"invite"}`)
	assertLegacyJSON(t, denied, http.StatusUnauthorized, `{"message":[401200,"账号信息已过期，请重新登录"]}`)
}

func TestMailLoginLinkDoesNotEnumerateAccountsAndHonorsPersistentCooldown(t *testing.T) {
	api, database := newTestAPI(t)
	enablePasswordResetSMTP(t, database)
	enableMailLogin(t, database)

	known := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/mail-link/request", `{"email":" ADMIN@EXAMPLE.TEST ","redirect":"invite"}`)
	unknown := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/mail-link/request", `{"email":"unknown@example.test","redirect":"invite"}`)
	if known.Code != http.StatusAccepted || unknown.Code != known.Code || unknown.Body.String() != known.Body.String() {
		t.Fatalf("enumerating responses known=(%d,%s) unknown=(%d,%s)", known.Code, known.Body, unknown.Code, unknown.Body)
	}

	job, claimed, err := database.ClaimLoginLinkMail(t.Context(), "http-mail-test", fixedNow(), time.Minute)
	if err != nil || !claimed || job.Recipient != "admin@example.test" || job.AppURL != "https://panel.example.test" {
		t.Fatalf("ClaimLoginLinkMail() job=%#v claimed=%v err=%v", job, claimed, err)
	}
	protector, err := security.NewLoginLinkProtector(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	tokenBytes, err := protector.DecryptToken(job.UserID, job.TokenCipher)
	if err != nil {
		t.Fatalf("DecryptToken() error=%v", err)
	}
	token := string(tokenBytes)
	for index := range tokenBytes {
		tokenBytes[index] = 0
	}

	cooledKnown := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/mail-link/request", `{"email":"admin@example.test"}`)
	cooledUnknown := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/mail-link/request", `{"email":"unknown@example.test"}`)
	for label, response := range map[string]*httptest.ResponseRecorder{"known": cooledKnown, "unknown": cooledUnknown} {
		expectAPIError(t, response, http.StatusTooManyRequests, "mail_login_cooldown")
		if response.Header().Get("Retry-After") != "60" {
			t.Fatalf("%s cooldown Retry-After=%q", label, response.Header().Get("Retry-After"))
		}
	}

	exchanged := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/login-link/exchange", `{"token":"`+token+`"}`)
	if exchanged.Code != http.StatusOK {
		t.Fatalf("mail link exchange status=%d body=%s", exchanged.Code, exchanged.Body)
	}
}

func TestLegacyMailLoginLinkPreservesSuccessStatusAndCooldownMessage(t *testing.T) {
	api, database := newTestAPI(t)
	enablePasswordResetSMTP(t, database)
	enableMailLogin(t, database)

	accepted := testClient{}.request(t, api, http.MethodPost, "/api/v1/passport/auth/loginWithMailLink", `{"email":"admin@example.test"}`)
	if accepted.Code != http.StatusOK {
		t.Fatalf("legacy mail link status=%d body=%s", accepted.Code, accepted.Body)
	}
	cooled := testClient{}.request(t, api, http.MethodPost, "/api/v1/passport/auth/loginWithMailLink", `{"email":"admin@example.test"}`)
	expectAPIError(t, cooled, http.StatusTooManyRequests, "mail_login_cooldown")
	if !strings.Contains(cooled.Body.String(), `"message":"发送频繁，请稍后再试"`) {
		t.Fatalf("legacy cooldown body=%s", cooled.Body)
	}
}

func enableMailLogin(t *testing.T, database *store.Store) {
	t.Helper()
	administrator, err := database.FindUserByEmail(t.Context(), "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetSiteSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateSiteSettings(t.Context(), administrator.ID, settings.Revision, store.SaveSiteSettingsInput{
		AppName: settings.AppName, AppDescription: settings.AppDescription, AppURL: settings.AppURL,
		TOSURL: settings.TOSURL, Logo: settings.Logo, StopRegister: settings.StopRegister,
		EmailVerificationEnabled: settings.EmailVerificationEnabled, EmailWhitelistEnabled: settings.EmailWhitelistEnabled,
		EmailWhitelistSuffixes: settings.EmailWhitelistSuffixes, GmailAliasLimitEnabled: settings.GmailAliasLimitEnabled,
		RegistrationIPLimitEnabled: settings.RegistrationIPLimitEnabled, RegistrationIPLimitCount: settings.RegistrationIPLimitCount,
		RegistrationIPLimitMinutes: settings.RegistrationIPLimitMinutes, InvitationForceEnabled: settings.InvitationForceEnabled,
		PasswordLimitEnabled: settings.PasswordLimitEnabled, PasswordLimitCount: settings.PasswordLimitCount,
		PasswordLimitMinutes: settings.PasswordLimitMinutes,
		InvitationCodeLimit:  settings.InvitationCodeLimit, InvitationNeverExpire: settings.InvitationNeverExpire, MailLoginEnabled: true,
	}, fixedNow()); err != nil {
		t.Fatal(err)
	}
}

func TestMailLoginLinkDisabledAndInputFailuresAreExplicit(t *testing.T) {
	api, _ := newTestAPI(t)
	disabled := testClient{}.request(t, api, http.MethodPost, "/api/v1/passport/auth/loginWithMailLink", `{"email":"admin@example.test"}`)
	expectAPIError(t, disabled, http.StatusNotFound, "mail_login_disabled")

	invalidEmail := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/mail-link/request", `{"email":"not-an-email"}`)
	expectAPIError(t, invalidEmail, http.StatusUnprocessableEntity, "validation_failed")
	invalidToken := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/login-link/exchange", `{"token":"ABC"}`)
	expectAPIError(t, invalidToken, http.StatusBadRequest, "login_link_invalid")
}

func loginLinkURLValues(t *testing.T, rawURL string) (string, string) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse login link %q: %v", rawURL, err)
	}
	fragment := strings.TrimPrefix(parsed.Fragment, "/login?")
	values, err := url.ParseQuery(fragment)
	if err != nil {
		t.Fatalf("parse login link query %q: %v", rawURL, err)
	}
	return values.Get("verify"), values.Get("redirect")
}
