package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestAccountSessionsCanListAndRevokeOnlyOneSession(t *testing.T) {
	api, _ := newTestAPI(t)
	first := loginAdmin(t, api)
	second := loginAdmin(t, api)

	listed := first.request(t, api, http.MethodGet, "/api/v1/auth/sessions", "")
	if listed.Code != http.StatusOK {
		t.Fatalf("list sessions status = %d; body=%s", listed.Code, listed.Body)
	}
	var payload struct {
		Data []struct {
			ID        int64 `json:"id"`
			IsCurrent bool  `json:"is_current"`
		} `json:"data"`
	}
	decodeResponse(t, listed, &payload)
	if len(payload.Data) != 2 {
		t.Fatalf("session count = %d, want 2: %#v", len(payload.Data), payload.Data)
	}
	var currentID, otherID int64
	for _, session := range payload.Data {
		if session.IsCurrent {
			currentID = session.ID
		} else {
			otherID = session.ID
		}
	}
	if currentID == 0 || otherID == 0 {
		t.Fatalf("current/other session IDs not identified: %#v", payload.Data)
	}

	revoked := first.request(t, api, http.MethodDelete, "/api/v1/auth/sessions/"+strconv.FormatInt(otherID, 10), "")
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke other status = %d, want 204; body=%s", revoked.Code, revoked.Body)
	}
	if response := second.request(t, api, http.MethodGet, "/api/v1/auth/session", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked second session status = %d, want 401", response.Code)
	}
	if response := first.request(t, api, http.MethodGet, "/api/v1/auth/session", ""); response.Code != http.StatusOK {
		t.Fatalf("first session status = %d, want 200; body=%s", response.Code, response.Body)
	}

	revoked = first.request(t, api, http.MethodDelete, "/api/v1/auth/sessions/"+strconv.FormatInt(currentID, 10), "")
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke current status = %d, want 204; body=%s", revoked.Code, revoked.Body)
	}
	assertExpiredAuthCookies(t, revoked)
}

func TestLogoutRevokesOnlyCurrentSession(t *testing.T) {
	api, _ := newTestAPI(t)
	first := loginAdmin(t, api)
	second := loginAdmin(t, api)

	loggedOut := first.request(t, api, http.MethodPost, "/api/v1/auth/logout", `{}`)
	if loggedOut.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d; body=%s", loggedOut.Code, loggedOut.Body)
	}
	if response := first.request(t, api, http.MethodGet, "/api/v1/auth/session", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("logged out session status = %d, want 401", response.Code)
	}
	if response := second.request(t, api, http.MethodGet, "/api/v1/auth/session", ""); response.Code != http.StatusOK {
		t.Fatalf("other session status = %d, want 200; body=%s", response.Code, response.Body)
	}
}

func TestAccountCannotListOrRevokeAnotherUsersSession(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	adminUser, err := database.FindUserByEmail(context.Background(), "admin@example.test")
	if err != nil {
		t.Fatalf("FindUserByEmail(admin) error = %v", err)
	}
	if _, err := database.CreateRuntimeUser(context.Background(), store.CreateRuntimeUserInput{
		Email:          "other@example.test",
		PasswordHash:   adminUser.PasswordHash,
		UUID:           "11111111-1111-4111-8111-111111111111",
		GroupID:        1,
		TransferEnable: 1,
	}, fixedNow()); err != nil {
		t.Fatalf("CreateRuntimeUser(other) error = %v", err)
	}
	other := loginAccount(t, api, "other@example.test", "admin-password-123")

	listed := other.request(t, api, http.MethodGet, "/api/v1/auth/sessions", "")
	if listed.Code != http.StatusOK {
		t.Fatalf("other list sessions status = %d; body=%s", listed.Code, listed.Body)
	}
	var payload struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	decodeResponse(t, listed, &payload)
	if len(payload.Data) != 1 {
		t.Fatalf("other account session count = %d, want 1: %#v", len(payload.Data), payload.Data)
	}

	denied := admin.request(t, api, http.MethodDelete, "/api/v1/auth/sessions/"+strconv.FormatInt(payload.Data[0].ID, 10), "")
	if denied.Code != http.StatusNotFound {
		t.Fatalf("cross-account revocation status = %d, want 404; body=%s", denied.Code, denied.Body)
	}
	if response := other.request(t, api, http.MethodGet, "/api/v1/auth/session", ""); response.Code != http.StatusOK {
		t.Fatalf("other session changed after denied revocation: status=%d body=%s", response.Code, response.Body)
	}
}

func TestChangePasswordValidatesAndRevokesEverySession(t *testing.T) {
	api, _ := newTestAPI(t)
	first := loginAdmin(t, api)
	second := loginAdmin(t, api)

	wrong := first.request(t, api, http.MethodPut, "/api/v1/auth/password", `{"old_password":"wrong-password","new_password":"replacement-password-456"}`)
	if wrong.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong old password status = %d, want 422; body=%s", wrong.Code, wrong.Body)
	}
	if response := second.request(t, api, http.MethodGet, "/api/v1/auth/session", ""); response.Code != http.StatusOK {
		t.Fatalf("wrong password changed session state: %d", response.Code)
	}

	short := first.request(t, api, http.MethodPut, "/api/v1/auth/password", `{"old_password":"admin-password-123","new_password":"too-short"}`)
	if short.Code != http.StatusUnprocessableEntity {
		t.Fatalf("short new password status = %d, want 422; body=%s", short.Code, short.Body)
	}

	changed := first.request(t, api, http.MethodPut, "/api/v1/auth/password", `{"old_password":"admin-password-123","new_password":"replacement-password-456"}`)
	if changed.Code != http.StatusNoContent {
		t.Fatalf("change password status = %d, want 204; body=%s", changed.Code, changed.Body)
	}
	assertExpiredAuthCookies(t, changed)
	for name, client := range map[string]testClient{"first": first, "second": second} {
		if response := client.request(t, api, http.MethodGet, "/api/v1/auth/session", ""); response.Code != http.StatusUnauthorized {
			t.Fatalf("%s old session status = %d, want 401", name, response.Code)
		}
	}
	if response := loginWithPassword(t, api, "admin-password-123"); response.Code != http.StatusUnauthorized {
		t.Fatalf("old password login status = %d, want 401", response.Code)
	}
	if response := loginWithPassword(t, api, "replacement-password-456"); response.Code != http.StatusOK {
		t.Fatalf("new password login status = %d, want 200; body=%s", response.Code, response.Body)
	}
}

func TestAccountSecurityWritesRequireCSRF(t *testing.T) {
	api, _ := newTestAPI(t)
	client := loginAdmin(t, api)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/auth/password", strings.NewReader(`{"old_password":"admin-password-123","new_password":"replacement-password-456"}`))
	request.Header.Set("Content-Type", "application/json")
	client.addCookies(request)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d, want 403; body=%s", response.Code, response.Body)
	}
}

func loginWithPassword(t *testing.T, api http.Handler, password string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"admin@example.test","password":"`+password+`"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

func loginAccount(t *testing.T, api http.Handler, email, password string) testClient {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"`+email+`","password":"`+password+`"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login %s status = %d; body=%s", email, response.Code, response.Body)
	}
	var csrf string
	cookies := response.Result().Cookies()
	for _, cookie := range cookies {
		if cookie.Name == CSRFCookieName {
			csrf, _ = url.QueryUnescape(cookie.Value)
		}
	}
	if len(cookies) != 2 || csrf == "" {
		t.Fatalf("login %s cookies = %#v", email, cookies)
	}
	return testClient{cookies: cookies, csrf: csrf}
}

func assertExpiredAuthCookies(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	result := response.Result()
	cookies := result.Cookies()
	if len(cookies) != 2 {
		t.Fatalf("expired cookie count = %d, want 2: %#v", len(cookies), cookies)
	}
	seen := map[string]bool{}
	for _, cookie := range cookies {
		seen[cookie.Name] = cookie.MaxAge < 0 && cookie.Value == ""
	}
	if !seen[SessionCookieName] || !seen[CSRFCookieName] {
		t.Fatalf("auth cookies not expired: %#v", cookies)
	}
}
