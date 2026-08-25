package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestBasicRegistrationContractAndStopPolicy(t *testing.T) {
	api, database := newTestAPI(t)

	registered := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{
		"email":"  NEW-USER@EXAMPLE.TEST  ",
		"password":"password-123",
		"password_confirmation":"password-123"
	}`)
	if registered.Code != http.StatusOK {
		t.Fatalf("registration status=%d body=%s", registered.Code, registered.Body)
	}
	user, err := database.FindUserByEmail(t.Context(), "new-user@example.test")
	if err != nil || user.Email != "new-user@example.test" || user.IsAdmin || user.Banned {
		t.Fatalf("registered user=%#v err=%v", user, err)
	}
	client := registrationClient(t, registered)
	if session := client.request(t, api, http.MethodGet, "/api/v1/auth/session", ""); session.Code != http.StatusOK || !strings.Contains(session.Body.String(), `"email":"new-user@example.test"`) {
		t.Fatalf("registered session status=%d body=%s", session.Code, session.Body)
	}

	duplicate := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{
		"email":"new-user@example.test","password":"password-123","password_confirmation":"password-123"
	}`)
	expectAPIError(t, duplicate, http.StatusBadRequest, "email_exists")

	for name, body := range map[string]string{
		"invalid email":         `{"email":"not-an-email","password":"password-123","password_confirmation":"password-123"}`,
		"overlong email":        `{"email":"` + strings.Repeat("a", 310) + `@example.test","password":"password-123","password_confirmation":"password-123"}`,
		"short password":        `{"email":"short@example.test","password":"1234567","password_confirmation":"1234567"}`,
		"overlong password":     `{"email":"long@example.test","password":"` + strings.Repeat("a", 1025) + `","password_confirmation":"` + strings.Repeat("a", 1025) + `"}`,
		"confirmation mismatch": `{"email":"mismatch@example.test","password":"password-123","password_confirmation":"different-123"}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", body)
			expectAPIError(t, response, http.StatusUnprocessableEntity, "validation_failed")
		})
	}
	unknown := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{
		"email":"unknown@example.test","password":"password-123","password_confirmation":"password-123","is_admin":true
	}`)
	expectAPIError(t, unknown, http.StatusBadRequest, "invalid_json")

	admin := loginAdmin(t, api)
	closed := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":1,"app_name":"Xboard-Go","app_description":"","app_url":"","tos_url":"","logo":"","stop_register":true
	}`)
	if closed.Code != http.StatusOK {
		t.Fatalf("close registration status=%d body=%s", closed.Code, closed.Body)
	}
	blocked := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{
		"email":"blocked@example.test","password":"password-123","password_confirmation":"password-123"
	}`)
	expectAPIError(t, blocked, http.StatusBadRequest, "registration_closed")
	if _, err := database.FindUserByEmail(t.Context(), "blocked@example.test"); err == nil {
		t.Fatal("closed registration created a user")
	}
}

func TestLegacyRegistrationReturnsPermanentBearerWithoutConfirmationField(t *testing.T) {
	api, database := newTestAPI(t)
	registered := testClient{}.request(t, api, http.MethodPost, "/api/v1/passport/auth/register", `{
		"email":"  LEGACY-REGISTER@EXAMPLE.TEST  ",
		"password":"legacy-password-123"
	}`)
	if registered.Code != http.StatusOK {
		t.Fatalf("legacy registration status=%d body=%s", registered.Code, registered.Body)
	}
	var payload struct {
		Data struct {
			Authorization     string `json:"auth_data"`
			SubscriptionToken string `json:"token"`
			IsAdmin           bool   `json:"is_admin"`
			IsDistributor     bool   `json:"is_distributor"`
		} `json:"data"`
	}
	decodeResponse(t, registered, &payload)
	if !strings.HasPrefix(payload.Data.Authorization, "Bearer ") || len(strings.TrimPrefix(payload.Data.Authorization, "Bearer ")) != 48 ||
		payload.Data.SubscriptionToken == "" || payload.Data.IsAdmin || payload.Data.IsDistributor {
		t.Fatalf("legacy registration payload=%#v", payload.Data)
	}
	if response := bearerRequest(api, http.MethodGet, "/api/v1/auth/session", payload.Data.Authorization, ""); response.Code != http.StatusOK {
		t.Fatalf("legacy registration bearer status=%d body=%s", response.Code, response.Body)
	}
	user, err := database.FindUserByEmail(t.Context(), "legacy-register@example.test")
	if err != nil || user.Email != "legacy-register@example.test" {
		t.Fatalf("legacy registered user=%#v err=%v", user, err)
	}
}

func TestRegistrationEmailAndSuccessfulIPPolicies(t *testing.T) {
	api, _ := newTestAPI(t)
	admin := loginAdmin(t, api)
	whitelist := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":1,"app_name":"Xboard-Go","app_description":"","app_url":"","tos_url":"","logo":"",
		"stop_register":false,"email_whitelist_enable":true,"email_whitelist_suffix":["allowed.test"],
		"email_gmail_limit_enable":false,"register_limit_by_ip_enable":false,"register_limit_count":2,"register_limit_expire":1
	}`)
	if whitelist.Code != http.StatusOK {
		t.Fatalf("save whitelist status=%d body=%s", whitelist.Code, whitelist.Body)
	}
	allowed := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{
		"email":"UPPER@ALLOWED.TEST","password":"password-123","password_confirmation":"password-123"
	}`)
	if allowed.Code != http.StatusOK {
		t.Fatalf("normalized allowlisted registration status=%d body=%s", allowed.Code, allowed.Body)
	}
	blocked := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{
		"email":"blocked@example.test","password":"password-123","password_confirmation":"password-123"
	}`)
	expectAPIError(t, blocked, http.StatusBadRequest, "email_domain_not_allowed")

	gmail := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":2,"app_name":"Xboard-Go","app_description":"","app_url":"","tos_url":"","logo":"",
		"stop_register":false,"email_whitelist_enable":false,"email_whitelist_suffix":["allowed.test"],
		"email_gmail_limit_enable":true,"register_limit_by_ip_enable":false,"register_limit_count":2,"register_limit_expire":1
	}`)
	if gmail.Code != http.StatusOK {
		t.Fatalf("save Gmail policy status=%d body=%s", gmail.Code, gmail.Body)
	}
	gmailAlias := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{
		"email":"first.last+tag@gmail.com","password":"password-123","password_confirmation":"password-123"
	}`)
	expectAPIError(t, gmailAlias, http.StatusBadRequest, "gmail_alias_not_allowed")
	nonGmailDot := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{
		"email":"first.last+tag@example.test","password":"password-123","password_confirmation":"password-123"
	}`)
	if nonGmailDot.Code != http.StatusOK {
		t.Fatalf("non-Gmail dot/plus registration status=%d body=%s", nonGmailDot.Code, nonGmailDot.Body)
	}

	ipPolicy := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":3,"app_name":"Xboard-Go","app_description":"","app_url":"","tos_url":"","logo":"",
		"stop_register":false,"email_whitelist_enable":false,"email_whitelist_suffix":["allowed.test"],
		"email_gmail_limit_enable":false,"register_limit_by_ip_enable":true,"register_limit_count":2,"register_limit_expire":1
	}`)
	if ipPolicy.Code != http.StatusOK {
		t.Fatalf("save IP policy status=%d body=%s", ipPolicy.Code, ipPolicy.Body)
	}
	duplicate := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{
		"email":"first.last+tag@example.test","password":"password-123","password_confirmation":"password-123"
	}`)
	expectAPIError(t, duplicate, http.StatusBadRequest, "email_exists")
	for _, email := range []string{"first-ip@example.test", "second-ip@example.test"} {
		response := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{
			"email":"`+email+`","password":"password-123","password_confirmation":"password-123"
		}`)
		if response.Code != http.StatusOK {
			t.Fatalf("IP quota registration %s status=%d body=%s", email, response.Code, response.Body)
		}
	}
	limited := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{
		"email":"third-ip@example.test","password":"password-123","password_confirmation":"password-123"
	}`)
	expectAPIError(t, limited, http.StatusTooManyRequests, "registration_ip_limited")
	if limited.Header().Get("Retry-After") != "60" {
		t.Fatalf("IP policy Retry-After=%q, want 60", limited.Header().Get("Retry-After"))
	}
}

func TestRegistrationPolicyRejectionSkipsPasswordHash(t *testing.T) {
	_, database := newTestAPI(t)
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
		TOSURL: settings.TOSURL, Logo: settings.Logo, StopRegister: false,
		EmailWhitelistSuffixes:     settings.EmailWhitelistSuffixes,
		RegistrationIPLimitEnabled: true, RegistrationIPLimitCount: 2, RegistrationIPLimitMinutes: 60,
		PasswordLimitEnabled: settings.PasswordLimitEnabled, PasswordLimitCount: settings.PasswordLimitCount,
		PasswordLimitMinutes: settings.PasswordLimitMinutes,
	}, fixedNow()); err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"precheck-first@example.test", "precheck-second@example.test"} {
		if _, err := database.RegisterUser(t.Context(), store.RegisterUserInput{
			Email: email, PasswordHash: "hash", SourceIP: "192.0.2.1",
		}, fixedNow()); err != nil {
			t.Fatal(err)
		}
	}
	hasher := &countingRegistrationHasher{}
	api := &server{
		store: database, passwordHasher: hasher, now: fixedNow,
		registrationRequests: newRequestLimiter(20, 15*time.Minute), passwordHashSlots: make(chan struct{}, 2),
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(`{
		"email":"precheck-third@example.test","password":"password-123","password_confirmation":"password-123"
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.register(response, request)
	expectAPIError(t, response, http.StatusTooManyRequests, "registration_ip_limited")
	if hasher.calls != 0 {
		t.Fatalf("IP policy rejection executed %d password hashes", hasher.calls)
	}
}

type countingRegistrationHasher struct{ calls int }

func (h *countingRegistrationHasher) Hash(string) (string, error) {
	h.calls++
	return "hash", nil
}

func (*countingRegistrationHasher) Verify(string, string) bool { return false }

func TestRegistrationRejectsUntrustedOriginAndExcessiveRequests(t *testing.T) {
	api, _ := newTestAPI(t)
	originRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(`{
		"email":"origin@example.test","password":"password-123","password_confirmation":"password-123"
	}`))
	originRequest.Header.Set("Content-Type", "application/json")
	originRequest.Header.Set("Origin", "https://attacker.example.test")
	originResponse := httptest.NewRecorder()
	api.ServeHTTP(originResponse, originRequest)
	expectAPIError(t, originResponse, http.StatusForbidden, "invalid_origin")

	for attempt := 1; attempt <= 20; attempt++ {
		response := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{
			"email":"invalid","password":"password-123","password_confirmation":"password-123"
		}`)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("registration attempt %d status=%d body=%s", attempt, response.Code, response.Body)
		}
	}
	limited := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{
		"email":"limited@example.test","password":"password-123","password_confirmation":"password-123"
	}`)
	expectAPIError(t, limited, http.StatusTooManyRequests, "registration_rate_limited")
	if limited.Header().Get("Retry-After") != "900" {
		t.Fatalf("Retry-After=%q, want 900", limited.Header().Get("Retry-After"))
	}
}

func TestRequestIPCanonicalizesPeerAndIgnoresUntrustedForwardingHeaders(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "[2001:0db8:0000:0000:0000:0000:0000:0001]:4321"
	request.Header.Set("X-Forwarded-For", "198.51.100.25")
	request.Header.Set("X-Real-IP", "198.51.100.26")
	if got := requestIP(request); got != "2001:db8::1" {
		t.Fatalf("requestIP() = %q, want canonical peer IP", got)
	}
	request.RemoteAddr = "192.0.2.60"
	if got := requestIP(request); got != "192.0.2.60" {
		t.Fatalf("requestIP() without port = %q", got)
	}
}

func TestRegistrationHashConcurrencyIsMemoryBounded(t *testing.T) {
	api := &server{passwordHashSlots: make(chan struct{}, 2)}
	firstRelease, first := api.beginPasswordHash()
	secondRelease, second := api.beginPasswordHash()
	thirdRelease, third := api.beginPasswordHash()
	if !first || !second || third {
		t.Fatalf("hash slots first=%t second=%t third=%t", first, second, third)
	}
	thirdRelease()
	firstRelease()
	reusedRelease, reused := api.beginPasswordHash()
	if !reused {
		t.Fatal("released registration hash slot was not reusable")
	}
	reusedRelease()
	secondRelease()
}

func TestRegistrationRechecksClosureAfterPasswordHash(t *testing.T) {
	_, database := newTestAPI(t)
	hasher := &blockingRegistrationHasher{started: make(chan struct{}), release: make(chan struct{})}
	api := &server{
		store: database, passwordHasher: hasher, now: fixedNow,
		registrationRequests: newRequestLimiter(20, 15*time.Minute), passwordHashSlots: make(chan struct{}, 2),
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(`{
		"email":"closure-race@example.test","password":"password-123","password_confirmation":"password-123"
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		api.register(response, request)
	}()
	<-hasher.started
	released := false
	defer func() {
		if !released {
			close(hasher.release)
		}
	}()

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
		TOSURL: settings.TOSURL, Logo: settings.Logo, StopRegister: true,
		EmailWhitelistEnabled: settings.EmailWhitelistEnabled, EmailWhitelistSuffixes: settings.EmailWhitelistSuffixes,
		GmailAliasLimitEnabled:     settings.GmailAliasLimitEnabled,
		RegistrationIPLimitEnabled: settings.RegistrationIPLimitEnabled,
		RegistrationIPLimitCount:   settings.RegistrationIPLimitCount,
		RegistrationIPLimitMinutes: settings.RegistrationIPLimitMinutes,
		PasswordLimitEnabled:       settings.PasswordLimitEnabled,
		PasswordLimitCount:         settings.PasswordLimitCount,
		PasswordLimitMinutes:       settings.PasswordLimitMinutes,
	}, fixedNow()); err != nil {
		t.Fatal(err)
	}
	close(hasher.release)
	released = true
	<-done

	expectAPIError(t, response, http.StatusBadRequest, "registration_closed")
	if _, err := database.FindUserByEmail(t.Context(), "closure-race@example.test"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("registration closure race persisted a user: %v", err)
	}
}

type blockingRegistrationHasher struct {
	started chan struct{}
	release chan struct{}
}

func (h *blockingRegistrationHasher) Hash(string) (string, error) {
	close(h.started)
	<-h.release
	return "argon2id-test-hash", nil
}

func (*blockingRegistrationHasher) Verify(string, string) bool { return false }

func registrationClient(t *testing.T, response *httptest.ResponseRecorder) testClient {
	t.Helper()
	cookies := response.Result().Cookies()
	var csrf string
	for _, cookie := range cookies {
		if cookie.Name == CSRFCookieName {
			if cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge != 12*60*60 {
				t.Fatalf("unsafe CSRF cookie = %#v", cookie)
			}
			csrf, _ = url.QueryUnescape(cookie.Value)
		} else if cookie.Name == SessionCookieName {
			if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge != 12*60*60 {
				t.Fatalf("unsafe session cookie = %#v", cookie)
			}
		}
	}
	if len(cookies) != 2 || csrf == "" {
		t.Fatalf("registration cookies=%#v", cookies)
	}
	return testClient{cookies: cookies, csrf: csrf}
}
