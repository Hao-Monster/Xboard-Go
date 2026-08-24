package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestLegacyBearerLoginAndSessionLifecycle(t *testing.T) {
	api, _ := newTestAPI(t)
	first := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123")
	if first.SubscriptionToken == "" || !first.IsAdmin || first.IsDistributor {
		t.Fatalf("legacy login data = %#v", first)
	}
	if !strings.HasPrefix(first.Authorization, "Bearer ") || len(strings.TrimPrefix(first.Authorization, "Bearer ")) != 48 {
		t.Fatalf("legacy bearer format is invalid")
	}
	if response := bearerRequest(api, http.MethodGet, "/api/v1/auth/session", first.Authorization, ""); response.Code != http.StatusOK {
		t.Fatalf("bearer session status = %d; body=%s", response.Code, response.Body)
	}

	before := bearerRequest(api, http.MethodGet, "/api/v1/user/getActiveSession", first.Authorization, "")
	if before.Code != http.StatusOK {
		t.Fatalf("legacy session list status = %d; body=%s", before.Code, before.Body)
	}
	var beforePayload struct {
		Data []legacyAccessTokenResponse `json:"data"`
	}
	decodeResponse(t, before, &beforePayload)
	if len(beforePayload.Data) != 1 || beforePayload.Data[0].ExpiresAt != nil || beforePayload.Data[0].Name == "" {
		t.Fatalf("legacy initial sessions = %#v", beforePayload.Data)
	}

	second := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123")
	after := bearerRequest(api, http.MethodGet, "/api/v1/user/getActiveSession", first.Authorization, "")
	var afterPayload struct {
		Data []legacyAccessTokenResponse `json:"data"`
	}
	decodeResponse(t, after, &afterPayload)
	if len(afterPayload.Data) != 2 {
		t.Fatalf("legacy session count = %d, want 2: %#v", len(afterPayload.Data), afterPayload.Data)
	}
	var removableID int64
	for _, token := range afterPayload.Data {
		if token.ID != beforePayload.Data[0].ID {
			removableID = token.ID
		}
		if len(token.Abilities) != 1 || token.Abilities[0] != "*" || token.TokenableType != "App\\Models\\User" || token.ExpiresAt != nil {
			t.Fatalf("legacy session shape = %#v", token)
		}
	}
	removed := bearerRequest(api, http.MethodPost, "/api/v1/user/removeActiveSession", first.Authorization, `{"session_id":`+strconv.FormatInt(removableID, 10)+`}`)
	if removed.Code != http.StatusOK {
		t.Fatalf("legacy remove status = %d; body=%s", removed.Code, removed.Body)
	}
	if response := bearerRequest(api, http.MethodGet, "/api/v1/auth/session", second.Authorization, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("removed bearer status = %d, want 401", response.Code)
	}

	loggedOut := bearerRequest(api, http.MethodPost, "/api/v1/user/logout", first.Authorization, "")
	if loggedOut.Code != http.StatusOK {
		t.Fatalf("legacy logout status = %d; body=%s", loggedOut.Code, loggedOut.Body)
	}
	if response := bearerRequest(api, http.MethodGet, "/api/v1/auth/session", first.Authorization, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("logged-out bearer status = %d, want 401", response.Code)
	}
	if response := bearerRequest(api, http.MethodPost, "/api/v1/user/logout", "", ""); response.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated legacy logout status = %d, want 403", response.Code)
	}
}

func TestNativeAccessTokenManagementIsExplicitAndCSRFProtected(t *testing.T) {
	api, _ := newTestAPI(t)
	client := loginAdmin(t, api)
	controlName := client.request(t, api, http.MethodPost, "/api/v1/auth/access-tokens", `{"name":"bad\tname"}`)
	if controlName.Code != http.StatusUnprocessableEntity {
		t.Fatalf("control-character token name status = %d, want 422; body=%s", controlName.Code, controlName.Body)
	}
	expiresAt := fixedNow().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	created := client.request(t, api, http.MethodPost, "/api/v1/auth/access-tokens", `{"name":"automation client","expires_at":"`+expiresAt+`"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create access token status = %d; body=%s", created.Code, created.Body)
	}
	var createdPayload struct {
		Data struct {
			ID        int64      `json:"id"`
			Name      string     `json:"name"`
			Token     string     `json:"token"`
			TokenType string     `json:"token_type"`
			ExpiresAt *time.Time `json:"expires_at"`
		} `json:"data"`
	}
	decodeResponse(t, created, &createdPayload)
	if createdPayload.Data.ID < 1 || createdPayload.Data.Name != "automation client" || len(createdPayload.Data.Token) != 48 ||
		createdPayload.Data.TokenType != "Bearer" || createdPayload.Data.ExpiresAt == nil {
		t.Fatalf("created access token = %#v", createdPayload.Data)
	}
	authorization := "Bearer " + createdPayload.Data.Token
	if response := bearerRequest(api, http.MethodGet, "/api/v1/auth/session", authorization, ""); response.Code != http.StatusOK {
		t.Fatalf("new access token session status = %d; body=%s", response.Code, response.Body)
	}

	listed := client.request(t, api, http.MethodGet, "/api/v1/auth/access-tokens", "")
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), createdPayload.Data.Token) || strings.Contains(listed.Body.String(), "token_hash") {
		t.Fatalf("access token list status=%d body=%s", listed.Code, listed.Body)
	}
	var listedPayload struct {
		Data []struct {
			ID        int64      `json:"id"`
			Name      string     `json:"name"`
			IsCurrent bool       `json:"is_current"`
			ExpiresAt *time.Time `json:"expires_at"`
		} `json:"data"`
	}
	decodeResponse(t, listed, &listedPayload)
	if len(listedPayload.Data) != 1 || listedPayload.Data[0].ID != createdPayload.Data.ID || listedPayload.Data[0].IsCurrent {
		t.Fatalf("listed access tokens = %#v", listedPayload.Data)
	}

	missingCSRF := httptest.NewRequest(http.MethodPost, "/api/v1/auth/access-tokens", strings.NewReader(`{"name":"denied"}`))
	missingCSRF.Header.Set("Content-Type", "application/json")
	client.addCookies(missingCSRF)
	missingCSRFResponse := httptest.NewRecorder()
	api.ServeHTTP(missingCSRFResponse, missingCSRF)
	if missingCSRFResponse.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d, want 403", missingCSRFResponse.Code)
	}

	deleted := client.request(t, api, http.MethodDelete, "/api/v1/auth/access-tokens/"+strconv.FormatInt(createdPayload.Data.ID, 10), "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete access token status = %d; body=%s", deleted.Code, deleted.Body)
	}
	if response := bearerRequest(api, http.MethodGet, "/api/v1/auth/session", authorization, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("deleted access token status = %d, want 401", response.Code)
	}
}

func TestAccessTokenAuthorizationAndOwnershipBoundaries(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	adminUser, err := database.FindUserByEmail(context.Background(), "admin@example.test")
	if err != nil {
		t.Fatalf("FindUserByEmail(admin) error = %v", err)
	}
	if _, err := database.CreateRuntimeUser(context.Background(), store.CreateRuntimeUserInput{
		Email:          "access-boundary@example.test",
		PasswordHash:   adminUser.PasswordHash,
		UUID:           "22222222-2222-4222-8222-222222222222",
		GroupID:        1,
		TransferEnable: 1,
	}, fixedNow()); err != nil {
		t.Fatalf("CreateRuntimeUser(other) error = %v", err)
	}
	other := loginAccount(t, api, "access-boundary@example.test", "admin-password-123")
	created := other.request(t, api, http.MethodPost, "/api/v1/auth/access-tokens", `{"name":"owned credential"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create owned access token status = %d; body=%s", created.Code, created.Body)
	}
	var createdPayload struct {
		Data struct {
			ID    int64  `json:"id"`
			Token string `json:"token"`
		} `json:"data"`
	}
	decodeResponse(t, created, &createdPayload)
	authorization := "Bearer " + createdPayload.Data.Token

	denied := admin.request(t, api, http.MethodDelete, "/api/v1/auth/access-tokens/"+strconv.FormatInt(createdPayload.Data.ID, 10), "")
	if denied.Code != http.StatusNotFound {
		t.Fatalf("cross-account access token revocation status = %d, want 404; body=%s", denied.Code, denied.Body)
	}
	if response := bearerRequest(api, http.MethodGet, "/api/v1/auth/session", authorization, ""); response.Code != http.StatusOK {
		t.Fatalf("denied revocation changed owned token: status=%d body=%s", response.Code, response.Body)
	}
	if response := bearerRequest(api, http.MethodGet, "/api/v1/admin/users", authorization, ""); response.Code != http.StatusForbidden {
		t.Fatalf("non-admin bearer reached administrator API: status=%d body=%s", response.Code, response.Body)
	}
	createdByBearer := bearerRequest(api, http.MethodPost, "/api/v1/auth/access-tokens", authorization, `{"name":"bearer automation"}`)
	if createdByBearer.Code != http.StatusCreated {
		t.Fatalf("explicit bearer write status = %d, want 201; body=%s", createdByBearer.Code, createdByBearer.Body)
	}

	malformed := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	malformed.Header.Set("Authorization", "Bearer invalid")
	admin.addCookies(malformed)
	malformedResponse := httptest.NewRecorder()
	api.ServeHTTP(malformedResponse, malformed)
	if malformedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("malformed bearer fell back to cookie session: status=%d, want 401", malformedResponse.Code)
	}
}

func TestNativeAccessTokenLogoutAndRevokeAllLeaveCookieSessionActive(t *testing.T) {
	api, _ := newTestAPI(t)
	client := loginAdmin(t, api)
	create := func(name string) string {
		t.Helper()
		response := client.request(t, api, http.MethodPost, "/api/v1/auth/access-tokens", `{"name":"`+name+`"}`)
		if response.Code != http.StatusCreated {
			t.Fatalf("create %s access token status = %d; body=%s", name, response.Code, response.Body)
		}
		var payload struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		decodeResponse(t, response, &payload)
		return "Bearer " + payload.Data.Token
	}

	loggedOutToken := create("native logout")
	remainingToken := create("revoke all")
	loggedOut := bearerRequest(api, http.MethodPost, "/api/v1/auth/logout", loggedOutToken, `{}`)
	if loggedOut.Code != http.StatusNoContent {
		t.Fatalf("native bearer logout status = %d, want 204; body=%s", loggedOut.Code, loggedOut.Body)
	}
	if response := bearerRequest(api, http.MethodGet, "/api/v1/auth/session", loggedOutToken, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("native bearer after logout status = %d, want 401", response.Code)
	}
	if response := bearerRequest(api, http.MethodGet, "/api/v1/auth/session", remainingToken, ""); response.Code != http.StatusOK {
		t.Fatalf("logout revoked another bearer: status=%d body=%s", response.Code, response.Body)
	}

	revokedAll := client.request(t, api, http.MethodDelete, "/api/v1/auth/access-tokens", "")
	if revokedAll.Code != http.StatusNoContent {
		t.Fatalf("revoke all access tokens status = %d, want 204; body=%s", revokedAll.Code, revokedAll.Body)
	}
	if response := bearerRequest(api, http.MethodGet, "/api/v1/auth/session", remainingToken, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("bearer after revoke all status = %d, want 401", response.Code)
	}
	if response := client.request(t, api, http.MethodGet, "/api/v1/auth/session", ""); response.Code != http.StatusOK {
		t.Fatalf("revoke all access tokens changed cookie session: status=%d body=%s", response.Code, response.Body)
	}
}

type legacyBearerLogin struct {
	Authorization     string
	SubscriptionToken string
	IsAdmin           bool
	IsDistributor     bool
}

type legacyAccessTokenResponse struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	Abilities     []string   `json:"abilities"`
	TokenableID   int64      `json:"tokenable_id"`
	TokenableType string     `json:"tokenable_type"`
	LastUsedAt    *time.Time `json:"last_used_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func loginLegacyBearer(t *testing.T, api http.Handler, email, password string) legacyBearerLogin {
	t.Helper()
	body, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/passport/auth/login", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("legacy login status = %d; body=%s", response.Code, response.Body)
	}
	var payload struct {
		Data struct {
			Authorization     string `json:"auth_data"`
			SubscriptionToken string `json:"token"`
			IsAdmin           bool   `json:"is_admin"`
			IsDistributor     bool   `json:"is_distributor"`
		} `json:"data"`
	}
	decodeResponse(t, response, &payload)
	return legacyBearerLogin(payload.Data)
}

func bearerRequest(api http.Handler, method, path, authorization, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

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
	accessToken := first.request(t, api, http.MethodPost, "/api/v1/auth/access-tokens", `{"name":"password-change-client"}`)
	if accessToken.Code != http.StatusCreated {
		t.Fatalf("create password-change access token status = %d; body=%s", accessToken.Code, accessToken.Body)
	}
	var accessTokenPayload struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	decodeResponse(t, accessToken, &accessTokenPayload)
	authorization := "Bearer " + accessTokenPayload.Data.Token

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
	if response := bearerRequest(api, http.MethodGet, "/api/v1/auth/session", authorization, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("access token after password change status = %d, want 401", response.Code)
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
