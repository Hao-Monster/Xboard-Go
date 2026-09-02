package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestPassportEmailCompatibilityRoutesValidateV1AndV2Requests(t *testing.T) {
	api, _ := newTestAPI(t)
	for _, version := range []string{"v1", "v2"} {
		sent := testClient{}.request(t, api, http.MethodPost, "/api/"+version+"/passport/comm/sendEmailVerify", `{"email":"invalid"}`)
		assertLegacyValidationMessage(t, sent, "邮箱格式不正确")

		forgot := testClient{}.request(t, api, http.MethodPost, "/api/"+version+"/passport/auth/forget", `{
			"email":"invalid","email_code":"x","password":"short"
		}`)
		assertLegacyValidationMessage(t, forgot, "邮箱格式不正确 (and 2 more errors)")
	}
	unknownField := testClient{}.request(t, api, http.MethodPost, "/api/v1/passport/comm/sendEmailVerify", `{"email":"admin@example.test","purpose":"reset"}`)
	expectAPIError(t, unknownField, http.StatusBadRequest, "invalid_json")
	oversized := testClient{}.request(t, api, http.MethodPost, "/api/v2/passport/auth/forget", `{"email":"admin@example.test","email_code":"123456","password":"`+strings.Repeat("x", maxJSONBody)+`"}`)
	expectAPIError(t, oversized, http.StatusRequestEntityTooLarge, "request_too_large")
}

func TestPassportEmailCompatibilityExistingAccountResetsThroughV2AndRevokesCredentials(t *testing.T) {
	api, database := newTestAPI(t)
	enablePasswordResetSMTP(t, database)
	oldSession := loginAdmin(t, api)
	accessToken := oldSession.request(t, api, http.MethodPost, "/api/v1/auth/access-tokens", `{"name":"legacy-password-reset"}`)
	if accessToken.Code != http.StatusCreated {
		t.Fatalf("create access token status=%d body=%s", accessToken.Code, accessToken.Body)
	}
	var tokenPayload struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	decodeResponse(t, accessToken, &tokenPayload)

	sent := testClient{}.request(t, api, http.MethodPost, "/api/v1/passport/comm/sendEmailVerify", `{"email":" ADMIN@EXAMPLE.TEST "}`)
	assertLegacySuccess(t, sent)
	code := claimPasswordResetCode(t, database, "admin@example.test")

	registrationProtector, err := security.NewRegistrationEmailProtector(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	emailDigest, _ := registrationProtector.EmailDigest("admin@example.test")
	codeDigest, _ := registrationProtector.CodeDigest("admin@example.test", code)
	if err := database.CheckRegistrationEmailVerification(t.Context(), emailDigest, codeDigest, fixedNow()); !errors.Is(err, store.ErrRegistrationEmailVerificationDisabled) && !errors.Is(err, store.ErrRegistrationEmailVerificationInvalid) {
		t.Fatalf("password reset code crossed into registration purpose: %v", err)
	}

	cooled := testClient{}.request(t, api, http.MethodPost, "/api/v2/passport/comm/sendEmailVerify", `{"email":"admin@example.test"}`)
	assertLegacyError(t, cooled, http.StatusBadRequest, "passport_email_cooldown", "验证码已发送，请过一会儿再请求")

	reset := testClient{}.request(t, api, http.MethodPost, "/api/v2/passport/auth/forget", `{
		"email":"admin@example.test","email_code":"`+code+`","password":"new-legacy-password-456"
	}`)
	assertLegacySuccess(t, reset)
	if response := oldSession.request(t, api, http.MethodGet, "/api/v1/auth/session", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("legacy reset left cookie session active: status=%d body=%s", response.Code, response.Body)
	}
	if response := bearerRequest(api, http.MethodGet, "/api/v1/auth/session", "Bearer "+tokenPayload.Data.Token, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("legacy reset left bearer active: status=%d body=%s", response.Code, response.Body)
	}
	reused := testClient{}.request(t, api, http.MethodPost, "/api/v1/passport/auth/forget", `{
		"email":"admin@example.test","email_code":"`+code+`","password":"another-password-789"
	}`)
	assertLegacyError(t, reused, http.StatusBadRequest, "password_reset_invalid", "邮箱验证码有误")
}

func TestPassportEmailCompatibilityUnknownAccountIssuesRegistrationOnlyCode(t *testing.T) {
	api, database := newTestAPI(t)
	enablePasswordResetSMTP(t, database)
	enableRegistrationEmailVerification(t, database)
	email := "legacy-register@example.test"

	sent := testClient{}.request(t, api, http.MethodPost, "/api/v2/passport/comm/sendEmailVerify", `{"email":"`+email+`"}`)
	assertLegacySuccess(t, sent)
	code := claimRegistrationEmailCode(t, database, email)

	passwordProtector, err := security.NewPasswordResetProtector(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	emailDigest, _ := passwordProtector.EmailDigest(email)
	codeDigest, _ := passwordProtector.CodeDigest(email, code)
	if _, err := database.CheckPasswordResetChallenge(t.Context(), emailDigest, codeDigest, fixedNow()); !errors.Is(err, store.ErrPasswordResetInvalid) {
		t.Fatalf("registration code crossed into password reset purpose: %v", err)
	}
	if _, claimed, err := database.ClaimPasswordResetMail(t.Context(), "no-reset-mail", fixedNow(), time.Minute); err != nil || claimed {
		t.Fatalf("unknown compatibility request queued password reset mail: claimed=%v err=%v", claimed, err)
	}

	forgot := testClient{}.request(t, api, http.MethodPost, "/api/v1/passport/auth/forget", `{
		"email":"`+email+`","email_code":"`+code+`","password":"password-456"
	}`)
	assertLegacyError(t, forgot, http.StatusBadRequest, "password_reset_invalid", "邮箱验证码有误")

	registered := testClient{}.request(t, api, http.MethodPost, "/api/v1/passport/auth/register", `{
		"email":"`+email+`","email_code":"`+code+`","password":"password-456"
	}`)
	if registered.Code != http.StatusOK {
		t.Fatalf("legacy registration status=%d body=%s", registered.Code, registered.Body)
	}
}

func TestPassportEmailCompatibilityUnknownIneligibleAccountKeepsGenericCooldownWithoutMail(t *testing.T) {
	api, database := newTestAPI(t)
	enablePasswordResetSMTP(t, database)
	email := "legacy-unknown@example.test"
	known := testClient{}.request(t, api, http.MethodPost, "/api/v1/passport/comm/sendEmailVerify", `{"email":"admin@example.test"}`)
	assertLegacySuccess(t, known)
	_ = claimPasswordResetCode(t, database, "admin@example.test")

	sent := testClient{}.request(t, api, http.MethodPost, "/api/v1/passport/comm/sendEmailVerify", `{"email":"`+email+`"}`)
	assertLegacySuccess(t, sent)
	if sent.Body.String() != known.Body.String() {
		t.Fatalf("compatibility send enumerated account: known=%s unknown=%s", known.Body, sent.Body)
	}
	if _, claimed, err := database.ClaimPasswordResetMail(t.Context(), "unknown-password-mail", fixedNow(), time.Minute); err != nil || claimed {
		t.Fatalf("ineligible unknown address queued password mail: claimed=%v err=%v", claimed, err)
	}
	if _, claimed, err := database.ClaimRegistrationEmailVerificationMail(t.Context(), "unknown-registration-mail", fixedNow(), time.Minute); err != nil || claimed {
		t.Fatalf("ineligible unknown address queued registration mail: claimed=%v err=%v", claimed, err)
	}
	cooled := testClient{}.request(t, api, http.MethodPost, "/api/v2/passport/comm/sendEmailVerify", `{"email":"`+email+`"}`)
	assertLegacyError(t, cooled, http.StatusBadRequest, "passport_email_cooldown", "验证码已发送，请过一会儿再请求")
	knownCooled := testClient{}.request(t, api, http.MethodPost, "/api/v2/passport/comm/sendEmailVerify", `{"email":"admin@example.test"}`)
	assertLegacyError(t, knownCooled, http.StatusBadRequest, "passport_email_cooldown", "验证码已发送，请过一会儿再请求")
	if cooled.Body.String() != knownCooled.Body.String() || cooled.Header().Get("Retry-After") != knownCooled.Header().Get("Retry-After") {
		t.Fatalf("compatibility cooldown enumerated account: known=(%s,%s) unknown=(%s,%s)",
			knownCooled.Header().Get("Retry-After"), knownCooled.Body, cooled.Header().Get("Retry-After"), cooled.Body)
	}
}

func TestPassportForgetCompatibilityKeepsPersistentThreeAttemptLock(t *testing.T) {
	api, database := newTestAPI(t)
	enablePasswordResetSMTP(t, database)
	sent := testClient{}.request(t, api, http.MethodPost, "/api/v1/passport/comm/sendEmailVerify", `{"email":"admin@example.test"}`)
	assertLegacySuccess(t, sent)
	code := claimPasswordResetCode(t, database, "admin@example.test")
	for _, wrongCode := range []string{"000000", "000001", "000002"} {
		if wrongCode == code {
			wrongCode = "999999"
		}
		response := testClient{}.request(t, api, http.MethodPost, "/api/v1/passport/auth/forget", `{
			"email":"admin@example.test","email_code":"`+wrongCode+`","password":"new-password-456"
		}`)
		assertLegacyError(t, response, http.StatusBadRequest, "password_reset_invalid", "邮箱验证码有误")
	}
	locked := testClient{}.request(t, api, http.MethodPost, "/api/v2/passport/auth/forget", `{
		"email":"admin@example.test","email_code":"`+code+`","password":"new-password-456"
	}`)
	assertLegacyError(t, locked, http.StatusTooManyRequests, "password_reset_locked", "重置失败，请稍后再试")
	if locked.Header().Get("Retry-After") != "300" {
		t.Fatalf("legacy lock Retry-After=%q, want 300", locked.Header().Get("Retry-After"))
	}
}

func TestPassportEmailCompatibilityRejectsUntrustedOrigins(t *testing.T) {
	api, _ := newTestAPI(t)
	for _, path := range []string{
		"/api/v1/passport/comm/sendEmailVerify", "/api/v2/passport/comm/sendEmailVerify",
		"/api/v1/passport/auth/forget", "/api/v2/passport/auth/forget",
	} {
		request := newTestRequest(http.MethodPost, path, strings.NewReader(`{"email":"admin@example.test"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "https://attacker.example.test")
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		assertLegacyError(t, response, http.StatusForbidden, "invalid_origin", "请求来源不受信任")
	}
}

func TestPassportEmailCompatibilitySharesBoundedSourceLimitAcrossVersions(t *testing.T) {
	api, _ := newTestAPI(t)
	for attempt := range 10 {
		version := "v1"
		if attempt%2 == 1 {
			version = "v2"
		}
		response := testClient{}.request(t, api, http.MethodPost, "/api/"+version+"/passport/comm/sendEmailVerify", `{"email":"admin@example.test"}`)
		expectAPIError(t, response, http.StatusServiceUnavailable, "mail_unavailable")
	}
	limited := testClient{}.request(t, api, http.MethodPost, "/api/v2/passport/comm/sendEmailVerify", `{"email":"admin@example.test"}`)
	assertLegacyError(t, limited, http.StatusTooManyRequests, "passport_email_rate_limited", "请求过于频繁，请稍后重试")
	if limited.Header().Get("Retry-After") != "900" {
		t.Fatalf("legacy source limit Retry-After=%q, want 900", limited.Header().Get("Retry-After"))
	}
}

func claimRegistrationEmailCode(t *testing.T, database *store.Store, email string) string {
	t.Helper()
	job, claimed, err := database.ClaimRegistrationEmailVerificationMail(t.Context(), "passport-registration", fixedNow(), time.Minute)
	if err != nil || !claimed || job.Recipient != email {
		t.Fatalf("ClaimRegistrationEmailVerificationMail() = (%#v, %v, %v)", job, claimed, err)
	}
	protector, err := security.NewRegistrationEmailProtector(make([]byte, 32))
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
	if err := database.CompleteRegistrationEmailVerificationMail(t.Context(), job.ID, "passport-registration", fixedNow()); err != nil {
		t.Fatal(err)
	}
	return code
}

func assertLegacyValidationMessage(t *testing.T, response *httptest.ResponseRecorder, message string) {
	t.Helper()
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("validation status=%d body=%s", response.Code, response.Body)
	}
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Message != message {
		t.Fatalf("validation message=%q, want %q; body=%s", payload.Message, message, response.Body)
	}
}

func assertLegacySuccess(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("legacy success status=%d body=%s", response.Code, response.Body)
	}
	var payload struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Data    bool   `json:"data"`
		Error   any    `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "success" || payload.Message != "操作成功" || !payload.Data || payload.Error != nil {
		t.Fatalf("legacy success payload=%#v body=%s", payload, response.Body)
	}
}

func assertLegacyError(t *testing.T, response *httptest.ResponseRecorder, status int, code, message string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("legacy error status=%d, want %d; body=%s", response.Code, status, response.Body)
	}
	var payload struct {
		Message string `json:"message"`
		Error   struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Message != message || payload.Error.Code != code {
		t.Fatalf("legacy error payload=%#v, want code=%q message=%q; body=%s", payload, code, message, response.Body)
	}
}
