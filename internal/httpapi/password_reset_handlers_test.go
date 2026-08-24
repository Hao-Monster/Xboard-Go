package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestPasswordResetRequestRouteRejectsUnavailableMailWithoutEnumeratingAccount(t *testing.T) {
	api, _ := newTestAPI(t)
	response := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/password-reset/request", `{"email":"admin@example.test"}`)
	expectAPIError(t, response, http.StatusServiceUnavailable, "mail_unavailable")
}

func TestPasswordResetRequestIsPrivatePersistentAndCooledDown(t *testing.T) {
	api, database := newTestAPI(t)
	enablePasswordResetSMTP(t, database)

	known := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/password-reset/request", `{"email":" ADMIN@EXAMPLE.TEST "}`)
	if known.Code != http.StatusAccepted {
		t.Fatalf("known request status=%d body=%s", known.Code, known.Body)
	}
	unknown := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/password-reset/request", `{"email":"unknown@example.test"}`)
	if unknown.Code != known.Code || unknown.Body.String() != known.Body.String() {
		t.Fatalf("account enumeration response: known=(%d,%s) unknown=(%d,%s)", known.Code, known.Body, unknown.Code, unknown.Body)
	}
	job, claimed, err := database.ClaimPasswordResetMail(t.Context(), "privacy-test", fixedNow(), time.Minute)
	if err != nil || !claimed || job.Recipient != "admin@example.test" {
		t.Fatalf("ClaimPasswordResetMail() = (%#v, %v, %v)", job, claimed, err)
	}
	if err := database.CompletePasswordResetMail(t.Context(), job.ID, "privacy-test", fixedNow()); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := database.ClaimPasswordResetMail(t.Context(), "unknown-check", fixedNow(), time.Minute); err != nil || claimed {
		t.Fatalf("unknown address created mail: claimed=%v err=%v", claimed, err)
	}

	cooledDown := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/password-reset/request", `{"email":"admin@example.test"}`)
	expectAPIError(t, cooledDown, http.StatusTooManyRequests, "password_reset_cooldown")
	if cooledDown.Header().Get("Retry-After") != "60" {
		t.Fatalf("cooldown Retry-After=%q, want 60", cooledDown.Header().Get("Retry-After"))
	}
}

func TestPasswordResetConfirmationChangesPasswordRevokesSessionsAndConsumesCode(t *testing.T) {
	api, database := newTestAPI(t)
	enablePasswordResetSMTP(t, database)
	oldSession := loginAdmin(t, api)
	accessToken := oldSession.request(t, api, http.MethodPost, "/api/v1/auth/access-tokens", `{"name":"password-reset-client"}`)
	if accessToken.Code != http.StatusCreated {
		t.Fatalf("create password-reset access token status=%d body=%s", accessToken.Code, accessToken.Body)
	}
	var accessTokenPayload struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	decodeResponse(t, accessToken, &accessTokenPayload)
	authorization := "Bearer " + accessTokenPayload.Data.Token

	request := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/password-reset/request", `{"email":"admin@example.test"}`)
	if request.Code != http.StatusAccepted {
		t.Fatalf("request status=%d body=%s", request.Code, request.Body)
	}
	code := claimPasswordResetCode(t, database, "admin@example.test")
	confirmation := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/password-reset/confirm", `{
		"email":"admin@example.test","email_code":"`+code+`","password":"new-admin-password-456"
	}`)
	if confirmation.Code != http.StatusOK {
		t.Fatalf("confirm status=%d body=%s", confirmation.Code, confirmation.Body)
	}
	if session := oldSession.request(t, api, http.MethodGet, "/api/v1/auth/session", ""); session.Code != http.StatusUnauthorized {
		t.Fatalf("old session remained active: status=%d body=%s", session.Code, session.Body)
	}
	if session := bearerRequest(api, http.MethodGet, "/api/v1/auth/session", authorization, ""); session.Code != http.StatusUnauthorized {
		t.Fatalf("password reset left access token active: status=%d body=%s", session.Code, session.Body)
	}
	oldLogin := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/login", `{"email":"admin@example.test","password":"admin-password-123"}`)
	expectAPIError(t, oldLogin, http.StatusUnauthorized, "invalid_credentials")
	newLogin := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/login", `{"email":"admin@example.test","password":"new-admin-password-456"}`)
	if newLogin.Code != http.StatusOK {
		t.Fatalf("new password login status=%d body=%s", newLogin.Code, newLogin.Body)
	}
	reused := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/password-reset/confirm", `{
		"email":"admin@example.test","email_code":"`+code+`","password":"another-password-789"
	}`)
	expectAPIError(t, reused, http.StatusBadRequest, "password_reset_invalid")
}

func TestPasswordResetLocksAfterThreeWrongCodesBeforeHashing(t *testing.T) {
	api, database := newTestAPI(t)
	enablePasswordResetSMTP(t, database)
	request := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/password-reset/request", `{"email":"admin@example.test"}`)
	if request.Code != http.StatusAccepted {
		t.Fatalf("request status=%d body=%s", request.Code, request.Body)
	}
	code := claimPasswordResetCode(t, database, "admin@example.test")
	wrongCodes := []string{"000000", "000001", "000002"}
	for index := range wrongCodes {
		if wrongCodes[index] == code {
			wrongCodes[index] = "999999"
		}
		response := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/password-reset/confirm", `{
			"email":"admin@example.test","email_code":"`+wrongCodes[index]+`","password":"new-admin-password-456"
		}`)
		expectAPIError(t, response, http.StatusBadRequest, "password_reset_invalid")
	}
	locked := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/password-reset/confirm", `{
		"email":"admin@example.test","email_code":"`+code+`","password":"new-admin-password-456"
	}`)
	expectAPIError(t, locked, http.StatusTooManyRequests, "password_reset_locked")
	if locked.Header().Get("Retry-After") != "300" {
		t.Fatalf("lock Retry-After=%q, want 300", locked.Header().Get("Retry-After"))
	}
}

func TestPasswordResetConfirmationDoesNotEnumerateUnknownAccountsThroughLockout(t *testing.T) {
	api, database := newTestAPI(t)
	enablePasswordResetSMTP(t, database)

	for _, email := range []string{"admin@example.test", "unknown@example.test"} {
		requested := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/password-reset/request", `{"email":"`+email+`"}`)
		if requested.Code != http.StatusAccepted {
			t.Fatalf("request %s status = %d, body = %s", email, requested.Code, requested.Body.String())
		}
		for attempt := 0; attempt < 3; attempt++ {
			response := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/password-reset/confirm", `{
				"email":"`+email+`","email_code":"000000","password":"new-admin-password-456"
			}`)
			expectAPIError(t, response, http.StatusBadRequest, "password_reset_invalid")
		}
		locked := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/password-reset/confirm", `{
			"email":"`+email+`","email_code":"000000","password":"new-admin-password-456"
		}`)
		expectAPIError(t, locked, http.StatusTooManyRequests, "password_reset_locked")
		if locked.Header().Get("Retry-After") != "300" {
			t.Fatalf("lock Retry-After for %s = %q, want 300", email, locked.Header().Get("Retry-After"))
		}
	}
}

func TestPasswordResetInvalidCodeSkipsPasswordHash(t *testing.T) {
	_, database := newTestAPI(t)
	enablePasswordResetSMTP(t, database)
	protector, err := security.NewPasswordResetProtector(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	emailDigest, _ := protector.EmailDigest("admin@example.test")
	codeDigest, _ := protector.CodeDigest("admin@example.test", "123456")
	codeCipher, _ := protector.EncryptCode("admin@example.test", "123456")
	if queued, err := database.RequestPasswordReset(t.Context(), store.PasswordResetRequestInput{
		Email: "admin@example.test", EmailDigest: emailDigest, CodeDigest: codeDigest, CodeCipher: codeCipher,
	}, fixedNow()); err != nil || !queued {
		t.Fatalf("RequestPasswordReset() = (%v, %v)", queued, err)
	}
	hasher := &countingRegistrationHasher{}
	api := &server{
		store: database, passwordHasher: hasher, now: fixedNow, passwordResetProtector: protector,
		passwordResetConfirmations: newRequestLimiter(20, 15*time.Minute), passwordHashSlots: make(chan struct{}, 2),
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password-reset/confirm", strings.NewReader(`{
		"email":"admin@example.test","email_code":"654321","password":"new-admin-password-456"
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.confirmPasswordReset(response, request)
	expectAPIError(t, response, http.StatusBadRequest, "password_reset_invalid")
	if hasher.calls != 0 {
		t.Fatalf("invalid code executed %d password hashes", hasher.calls)
	}
}

func TestPasswordResetRejectsUntrustedOriginsAndBoundsRequests(t *testing.T) {
	api, _ := newTestAPI(t)
	originRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password-reset/request", strings.NewReader(`{"email":"admin@example.test"}`))
	originRequest.Header.Set("Content-Type", "application/json")
	originRequest.Header.Set("Origin", "https://attacker.example.test")
	originResponse := httptest.NewRecorder()
	api.ServeHTTP(originResponse, originRequest)
	expectAPIError(t, originResponse, http.StatusForbidden, "invalid_origin")

	for attempt := 0; attempt < 10; attempt++ {
		response := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/password-reset/request", `{"email":"invalid"}`)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("bounded request %d status=%d body=%s", attempt+1, response.Code, response.Body)
		}
	}
	limited := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/password-reset/request", `{"email":"admin@example.test"}`)
	expectAPIError(t, limited, http.StatusTooManyRequests, "password_reset_rate_limited")
}

func enablePasswordResetSMTP(t *testing.T, database *store.Store) {
	t.Helper()
	administrator, err := database.FindUserByEmail(t.Context(), "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetTicketSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(t.Context(), administrator.ID, settings.Revision, store.SaveTicketSettingsInput{
		AppName: "Xboard-Go", AppURL: "https://panel.example.test", SMTPEnabled: true,
		SMTPHost: "mailpit", SMTPPort: 1025, SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, fixedNow()); err != nil {
		t.Fatal(err)
	}
}

func claimPasswordResetCode(t *testing.T, database *store.Store, email string) string {
	t.Helper()
	job, claimed, err := database.ClaimPasswordResetMail(t.Context(), "http-test-mail", fixedNow(), time.Minute)
	if err != nil || !claimed || job.Recipient != email {
		t.Fatalf("ClaimPasswordResetMail() = (%#v, %v, %v)", job, claimed, err)
	}
	protector, err := security.NewPasswordResetProtector(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := protector.DecryptCode(email, job.CodeCipher)
	if err != nil {
		t.Fatal(err)
	}
	code := string(plaintext)
	for index := range plaintext {
		plaintext[index] = 0
	}
	if err := database.CompletePasswordResetMail(t.Context(), job.ID, "http-test-mail", fixedNow()); err != nil {
		t.Fatal(err)
	}
	return code
}
