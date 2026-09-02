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

func TestRegistrationEmailVerificationRouteMatchesOneTimeLegacyFlow(t *testing.T) {
	api, database := newTestAPI(t)
	enablePasswordResetSMTP(t, database)
	enableRegistrationEmailVerification(t, database)

	guest := testClient{}.request(t, api, http.MethodGet, "/api/v1/guest/comm/config", "")
	if guest.Code != http.StatusOK || !strings.Contains(guest.Body.String(), `"is_email_verify":1`) {
		t.Fatalf("guest config status=%d body=%s", guest.Code, guest.Body)
	}

	known := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/registration-email/request", `{"email":"admin@example.test"}`)
	unknown := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/registration-email/request", `{"email":"registration-flow@example.test"}`)
	if known.Code != http.StatusAccepted || unknown.Code != http.StatusAccepted || known.Body.String() != unknown.Body.String() {
		t.Fatalf("request privacy known=(%d,%s) unknown=(%d,%s)", known.Code, known.Body, unknown.Code, unknown.Body)
	}
	job, claimed, err := database.ClaimRegistrationEmailVerificationMail(t.Context(), "http-registration", fixedNow(), time.Minute)
	if err != nil || !claimed || job.Recipient != "registration-flow@example.test" {
		t.Fatalf("ClaimRegistrationEmailVerificationMail() = (%#v, %v, %v)", job, claimed, err)
	}
	protector, err := security.NewRegistrationEmailProtector(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := protector.DecryptCode(job.Recipient, job.CodeCipher)
	if err != nil {
		t.Fatal(err)
	}
	code := string(plaintext)
	for index := range plaintext {
		plaintext[index] = 0
	}
	if err := database.CompleteRegistrationEmailVerificationMail(t.Context(), job.ID, "http-registration", fixedNow()); err != nil {
		t.Fatal(err)
	}

	cooldown := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/registration-email/request", `{"email":"registration-flow@example.test"}`)
	expectAPIError(t, cooldown, http.StatusTooManyRequests, "registration_email_cooldown")
	if cooldown.Header().Get("Retry-After") != "60" {
		t.Fatalf("cooldown Retry-After=%q", cooldown.Header().Get("Retry-After"))
	}
	missing := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{
		"email":"registration-flow@example.test","password":"password-123","password_confirmation":"password-123"
	}`)
	expectAPIError(t, missing, http.StatusUnprocessableEntity, "validation_failed")
	wrongCode := "000000"
	if wrongCode == code {
		wrongCode = "999999"
	}
	wrong := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{
		"email":"registration-flow@example.test","email_code":"`+wrongCode+`","password":"password-123","password_confirmation":"password-123"
	}`)
	expectAPIError(t, wrong, http.StatusBadRequest, "registration_email_invalid")
	registered := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{
		"email":"registration-flow@example.test","email_code":"`+code+`","password":"password-123","password_confirmation":"password-123"
	}`)
	if registered.Code != http.StatusOK {
		t.Fatalf("verified registration status=%d body=%s", registered.Code, registered.Body)
	}
	reused := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/register", `{
		"email":"registration-flow@example.test","email_code":"`+code+`","password":"password-123","password_confirmation":"password-123"
	}`)
	expectAPIError(t, reused, http.StatusBadRequest, "registration_email_invalid")
}

func TestRegistrationEmailWrongCodesLockBeforePasswordHash(t *testing.T) {
	_, database := newTestAPI(t)
	enablePasswordResetSMTP(t, database)
	enableRegistrationEmailVerification(t, database)
	protector, _ := security.NewRegistrationEmailProtector(make([]byte, 32))
	email := "registration-lock@example.test"
	code := "384729"
	emailDigest, _ := protector.EmailDigest(email)
	codeDigest, _ := protector.CodeDigest(email, code)
	codeCipher, _ := protector.EncryptCode(email, code)
	if queued, err := database.RequestRegistrationEmailVerification(t.Context(), store.RegistrationEmailVerificationRequestInput{
		Email: email, SourceIP: "192.0.2.1", EmailDigest: emailDigest, CodeDigest: codeDigest, CodeCipher: codeCipher,
	}, fixedNow()); err != nil || !queued {
		t.Fatalf("RequestRegistrationEmailVerification() = (%v, %v)", queued, err)
	}
	hasher := &countingRegistrationHasher{}
	api := &server{
		store: database, passwordHasher: hasher, now: fixedNow,
		registrationEmailProtector: protector, registrationRequests: newRequestLimiter(20, 15*time.Minute),
		passwordHashSlots: make(chan struct{}, 2),
	}
	register := func(body string) *httptest.ResponseRecorder {
		request := newTestRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		api.register(response, request)
		return response
	}
	for attempt := 0; attempt < 3; attempt++ {
		response := register(`{
			"email":"` + email + `","email_code":"00000` + string(rune('0'+attempt)) + `","password":"password-123","password_confirmation":"password-123"
		}`)
		expectAPIError(t, response, http.StatusBadRequest, "registration_email_invalid")
	}
	locked := register(`{
		"email":"` + email + `","email_code":"` + code + `","password":"password-123","password_confirmation":"password-123"
	}`)
	expectAPIError(t, locked, http.StatusTooManyRequests, "registration_email_locked")
	if hasher.calls != 0 {
		t.Fatalf("invalid registration email codes executed %d password hashes", hasher.calls)
	}
}

func TestRegistrationEmailRequestRejectsUntrustedOriginsAndBoundsRequests(t *testing.T) {
	api, _ := newTestAPI(t)
	originRequest := newTestRequest(http.MethodPost, "/api/v1/auth/registration-email/request", strings.NewReader(`{"email":"origin@example.test"}`))
	originRequest.Header.Set("Content-Type", "application/json")
	originRequest.Header.Set("Origin", "https://attacker.example.test")
	originResponse := httptest.NewRecorder()
	api.ServeHTTP(originResponse, originRequest)
	expectAPIError(t, originResponse, http.StatusForbidden, "invalid_origin")

	for attempt := 1; attempt <= 10; attempt++ {
		response := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/registration-email/request", `{"email":"invalid"}`)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("registration email request %d status=%d body=%s", attempt, response.Code, response.Body)
		}
	}
	limited := testClient{}.request(t, api, http.MethodPost, "/api/v1/auth/registration-email/request", `{"email":"limited@example.test"}`)
	expectAPIError(t, limited, http.StatusTooManyRequests, "registration_email_rate_limited")
	if limited.Header().Get("Retry-After") != "900" {
		t.Fatalf("registration email Retry-After=%q", limited.Header().Get("Retry-After"))
	}
}

func enableRegistrationEmailVerification(t *testing.T, database *store.Store) {
	t.Helper()
	admin, err := database.FindUserByEmail(t.Context(), "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetSiteSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.UpdateSiteSettings(t.Context(), admin.ID, settings.Revision, store.SaveSiteSettingsInput{
		AppName: settings.AppName, AppDescription: settings.AppDescription, AppURL: settings.AppURL,
		TOSURL: settings.TOSURL, Logo: settings.Logo, StopRegister: false,
		EmailVerificationEnabled: true, EmailWhitelistEnabled: settings.EmailWhitelistEnabled,
		EmailWhitelistSuffixes: settings.EmailWhitelistSuffixes, GmailAliasLimitEnabled: settings.GmailAliasLimitEnabled,
		RegistrationIPLimitEnabled: settings.RegistrationIPLimitEnabled,
		RegistrationIPLimitCount:   settings.RegistrationIPLimitCount, RegistrationIPLimitMinutes: settings.RegistrationIPLimitMinutes,
		PasswordLimitEnabled: settings.PasswordLimitEnabled, PasswordLimitCount: settings.PasswordLimitCount,
		PasswordLimitMinutes: settings.PasswordLimitMinutes,
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
}
