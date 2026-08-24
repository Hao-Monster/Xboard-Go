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

func TestRegistrationHashConcurrencyIsMemoryBounded(t *testing.T) {
	api := &server{registrationHashSlots: make(chan struct{}, 2)}
	firstRelease, first := api.beginRegistrationHash()
	secondRelease, second := api.beginRegistrationHash()
	thirdRelease, third := api.beginRegistrationHash()
	if !first || !second || third {
		t.Fatalf("hash slots first=%t second=%t third=%t", first, second, third)
	}
	thirdRelease()
	firstRelease()
	reusedRelease, reused := api.beginRegistrationHash()
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
		registrationRequests: newRequestLimiter(20, 15*time.Minute), registrationHashSlots: make(chan struct{}, 2),
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
