package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestPasswordLoginLimitMatchesLegacyPolicyWithoutIdentityBypasses(t *testing.T) {
	api, database := newTestAPI(t)
	administrator := loginAdmin(t, api)
	configured := administrator.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":1,"app_name":"Xboard-Go","app_description":"","app_url":"","tos_url":"","logo":"",
		"password_limit_enable":true,"password_limit_count":2,"password_limit_expire":1
	}`)
	if configured.Code != http.StatusOK {
		t.Fatalf("configure password limit status=%d body=%s", configured.Code, configured.Body)
	}

	assertPasswordLoginError(t, api, "/api/v1/auth/login", "admin@example.test", "wrong-password", http.StatusUnauthorized, "invalid_credentials", "邮箱或密码错误")
	if response := passwordLoginRequest(t, api, "/api/v1/auth/login", "admin@example.test", "admin-password-123"); response.Code != http.StatusOK {
		t.Fatalf("success before threshold status=%d body=%s", response.Code, response.Body)
	}
	assertPasswordLoginError(t, api, "/api/v1/auth/login", "admin@example.test", "still-wrong", http.StatusUnauthorized, "invalid_credentials", "邮箱或密码错误")
	limitedMessage := "密码错误次数过多，请 1 分钟后再试"
	limited := assertPasswordLoginError(t, api, "/api/v1/auth/login", "admin@example.test", "admin-password-123", http.StatusTooManyRequests, "login_rate_limited", limitedMessage)
	if limited.Header().Get("Retry-After") != "60" {
		t.Fatalf("account Retry-After=%q, want 60", limited.Header().Get("Retry-After"))
	}
	assertPasswordLoginError(t, api, "/api/v1/passport/auth/login", "  ADMIN@EXAMPLE.TEST  ", "admin-password-123", http.StatusTooManyRequests, "login_rate_limited", limitedMessage)

	for attempt := 1; attempt <= 2; attempt++ {
		assertPasswordLoginError(t, api, "/api/v1/auth/login", "missing@example.test", "wrong-password", http.StatusUnauthorized, "invalid_credentials", "邮箱或密码错误")
	}
	assertPasswordLoginError(t, api, "/api/v1/auth/login", "MISSING@EXAMPLE.TEST", "wrong-password", http.StatusTooManyRequests, "login_rate_limited", limitedMessage)

	hasher := security.NewPasswordHasher(security.PasswordParams{MemoryKiB: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	bannedHash, err := hasher.Hash("banned-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateAdminUser(t.Context(), store.CreateAdminUserInput{
		Email: "banned@example.test", PasswordHash: bannedHash, Banned: true,
	}, fixedNow()); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		assertPasswordLoginError(t, api, "/api/v1/auth/login", "banned@example.test", "banned-password-123", http.StatusUnauthorized, "invalid_credentials", "邮箱或密码错误")
	}
	assertPasswordLoginError(t, api, "/api/v1/auth/login", "banned@example.test", "banned-password-123", http.StatusTooManyRequests, "login_rate_limited", limitedMessage)

	disabled := administrator.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":2,"app_name":"Xboard-Go","app_description":"","app_url":"","tos_url":"","logo":"",
		"password_limit_enable":false
	}`)
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable password limit status=%d body=%s", disabled.Code, disabled.Body)
	}
	if response := passwordLoginRequest(t, api, "/api/v1/auth/login", "ADMIN@EXAMPLE.TEST", "admin-password-123"); response.Code != http.StatusOK {
		t.Fatalf("disabled password limit retained lock status=%d body=%s", response.Code, response.Body)
	}
}

func passwordLoginRequest(t *testing.T, api http.Handler, path, email, password string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

func assertPasswordLoginError(t *testing.T, api http.Handler, path, email, password string, status int, code, message string) *httptest.ResponseRecorder {
	t.Helper()
	response := passwordLoginRequest(t, api, path, email, password)
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode login error: %v body=%s", err, response.Body)
	}
	if response.Code != status || payload.Error.Code != code || payload.Error.Message != message {
		t.Fatalf("login error status=%d code=%q message=%q, want %d %q %q; body=%s", response.Code, payload.Error.Code, payload.Error.Message, status, code, message, response.Body)
	}
	return response
}
