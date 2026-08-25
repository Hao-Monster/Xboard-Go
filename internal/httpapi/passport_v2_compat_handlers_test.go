package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
)

func TestPassportV1V2PublicContractsMatchLegacyValidationAndErrors(t *testing.T) {
	for _, version := range []string{"v1", "v2"} {
		api, _ := newTestAPI(t)
		assertLegacyJSON(t, testClient{}.request(t, api, http.MethodPost, "/api/"+version+"/passport/auth/login", `{"email":"invalid","password":"short"}`), http.StatusUnprocessableEntity,
			`{"message":"邮箱格式不正确 (and 1 more error)","errors":{"email":["邮箱格式不正确"],"password":["密码必须大于 8 个字符"]}}`)
		assertLegacyJSON(t, testClient{}.request(t, api, http.MethodPost, "/api/"+version+"/passport/auth/register", `{"email":"invalid","password":"short"}`), http.StatusUnprocessableEntity,
			`{"message":"邮箱格式不正确 (and 1 more error)","errors":{"email":["邮箱格式不正确"],"password":["密码必须大于 8 个字符"]}}`)
		assertLegacyJSON(t, testClient{}.request(t, api, http.MethodPost, "/api/"+version+"/passport/auth/loginWithMailLink", `{"email":"invalid"}`), http.StatusUnprocessableEntity,
			`{"message":"validation.email","errors":{"email":["validation.email"]}}`)
		assertLegacyJSON(t, testClient{}.request(t, api, http.MethodPost, "/api/"+version+"/passport/auth/getQuickLoginUrl", `{}`), http.StatusUnauthorized,
			`{"message":[401001,"授权失败，请先登录"]}`)
		assertLegacyJSON(t, testClient{}.request(t, api, http.MethodPost, "/api/"+version+"/passport/auth/getQuickLoginUrl", `{"auth_data":"Bearer invalid"}`), http.StatusUnauthorized,
			`{"message":[401200,"账号信息已过期，请重新登录"]}`)
		assertLegacyJSON(t, testClient{}.request(t, api, http.MethodGet, "/api/"+version+"/passport/auth/token2Login", ""), http.StatusBadRequest,
			`{"message":"Invalid request"}`)
		assertLegacyJSON(t, testClient{}.request(t, api, http.MethodGet, "/api/"+version+"/passport/auth/token2Login?verify=invalid", ""), http.StatusBadRequest,
			`{"message":"令牌有误"}`)
		assertLegacySuccess(t, testClient{}.request(t, api, http.MethodPost, "/api/"+version+"/passport/comm/pv", `{"invite_code":"NotARealCode"}`))
	}
}

func TestPassportV1V2LoginAndRegistrationIssueUsableLegacyCredentials(t *testing.T) {
	api, database := newTestAPI(t)
	for _, version := range []string{"v1", "v2"} {
		loggedIn := testClient{}.request(t, api, http.MethodPost, "/api/"+version+"/passport/auth/login", `{
			"email":"admin@example.test","password":"admin-password-123"
		}`)
		authorization := assertLegacyAuthEnvelope(t, loggedIn, true)
		if response := bearerRequest(api, http.MethodGet, "/api/v1/auth/session", authorization, ""); response.Code != http.StatusOK {
			t.Fatalf("%s login bearer status=%d body=%s", version, response.Code, response.Body)
		}

		email := "passport-register-" + version + "@example.test"
		registered := testClient{}.request(t, api, http.MethodPost, "/api/"+version+"/passport/auth/register", `{
			"email":"`+email+`","password":"legacy-password-123"
		}`)
		registrationAuthorization := assertLegacyAuthEnvelope(t, registered, false)
		if response := bearerRequest(api, http.MethodGet, "/api/v1/auth/session", registrationAuthorization, ""); response.Code != http.StatusOK {
			t.Fatalf("%s registration bearer status=%d body=%s", version, response.Code, response.Body)
		}
		if user, err := database.FindUserByEmail(t.Context(), email); err != nil || user.Email != email {
			t.Fatalf("%s registered user=%#v err=%v", version, user, err)
		}
	}
}

func TestPassportV1V2MailAndQuickLinksKeepLegacyEnvelopesAndOneTimeExchange(t *testing.T) {
	for _, version := range []string{"v1", "v2"} {
		t.Run(version, func(t *testing.T) {
			api, database := newTestAPI(t)
			enablePasswordResetSMTP(t, database)
			enableMailLogin(t, database)

			requested := testClient{}.request(t, api, http.MethodPost, "/api/"+version+"/passport/auth/loginWithMailLink", `{
				"email":"admin@example.test","redirect":"knowledge"
			}`)
			assertLegacySuccess(t, requested)
			job, claimed, err := database.ClaimLoginLinkMail(t.Context(), "passport-v2-compat", fixedNow(), time.Minute)
			if err != nil || !claimed {
				t.Fatalf("%s claim login mail claimed=%v err=%v", version, claimed, err)
			}
			protector, err := security.NewLoginLinkProtector(make([]byte, 32))
			if err != nil {
				t.Fatal(err)
			}
			tokenBytes, err := protector.DecryptToken(job.UserID, job.TokenCipher)
			if err != nil {
				t.Fatal(err)
			}
			mailToken := string(tokenBytes)
			for index := range tokenBytes {
				tokenBytes[index] = 0
			}
			mailExchange := testClient{}.request(t, api, http.MethodGet, "/api/"+version+"/passport/auth/token2Login?verify="+url.QueryEscape(mailToken), "")
			mailAuthorization := assertLegacyTokenExchange(t, mailExchange)
			if response := bearerRequest(api, http.MethodGet, "/api/v1/auth/session", mailAuthorization, ""); response.Code != http.StatusOK {
				t.Fatalf("%s mail exchange bearer status=%d body=%s", version, response.Code, response.Body)
			}
			assertLegacyJSON(t, testClient{}.request(t, api, http.MethodGet, "/api/"+version+"/passport/auth/token2Login?verify="+url.QueryEscape(mailToken), ""), http.StatusBadRequest,
				`{"message":"令牌有误"}`)

			quick := bearerRequest(api, http.MethodPost, "/api/"+version+"/passport/auth/getQuickLoginUrl", mailAuthorization, "")
			if quick.Code != http.StatusOK {
				t.Fatalf("%s header quick link status=%d body=%s", version, quick.Code, quick.Body)
			}
			var quickPayload struct {
				Status  string  `json:"status"`
				Message string  `json:"message"`
				Data    string  `json:"data"`
				Error   *string `json:"error"`
			}
			decodeResponse(t, quick, &quickPayload)
			if quickPayload.Status != "success" || quickPayload.Message != "操作成功" || quickPayload.Error != nil {
				t.Fatalf("%s quick envelope=%#v body=%s", version, quickPayload, quick.Body)
			}
			quickToken, redirect := loginLinkURLValues(t, quickPayload.Data)
			if quickToken == "" || redirect != "dashboard" {
				t.Fatalf("%s quick link token=%q redirect=%q", version, quickToken, redirect)
			}
			quickExchange := testClient{}.request(t, api, http.MethodGet, "/api/"+version+"/passport/auth/token2Login?verify="+url.QueryEscape(quickToken), "")
			_ = assertLegacyTokenExchange(t, quickExchange)
		})
	}
}

func TestPassportV1V2InvitationPVIsNonEnumeratingAndIncrementsValidCodes(t *testing.T) {
	api, _ := newTestAPI(t)
	owner := loginAdmin(t, api)
	generated := owner.request(t, api, http.MethodPost, "/api/v1/invitations", `{}`)
	if generated.Code != http.StatusOK {
		t.Fatalf("generate invitation status=%d body=%s", generated.Code, generated.Body)
	}
	var generatedPayload struct {
		Data struct {
			Code string `json:"code"`
		} `json:"data"`
	}
	decodeResponse(t, generated, &generatedPayload)
	for _, version := range []string{"v1", "v2"} {
		known := testClient{}.request(t, api, http.MethodPost, "/api/"+version+"/passport/comm/pv", `{"invite_code":"`+generatedPayload.Data.Code+`"}`)
		unknown := testClient{}.request(t, api, http.MethodPost, "/api/"+version+"/passport/comm/pv", `{"invite_code":"Badc1234"}`)
		assertLegacySuccess(t, known)
		assertLegacySuccess(t, unknown)
		if known.Body.String() != unknown.Body.String() {
			t.Fatalf("%s PV enumerated invitation: known=%s unknown=%s", version, known.Body, unknown.Body)
		}
	}
	summary := owner.request(t, api, http.MethodGet, "/api/v1/invitations", "")
	var summaryPayload struct {
		Data struct {
			Codes []struct {
				PV int64 `json:"pv"`
			} `json:"codes"`
		} `json:"data"`
	}
	decodeResponse(t, summary, &summaryPayload)
	if len(summaryPayload.Data.Codes) != 1 || summaryPayload.Data.Codes[0].PV != 2 {
		t.Fatalf("invitation PV summary=%#v", summaryPayload.Data)
	}
}

func TestPassportV1V2MutatingPostRoutesRejectUntrustedOrigins(t *testing.T) {
	api, _ := newTestAPI(t)
	for _, version := range []string{"v1", "v2"} {
		for _, path := range []string{
			"passport/auth/login", "passport/auth/register", "passport/auth/loginWithMailLink",
			"passport/auth/getQuickLoginUrl", "passport/comm/pv",
		} {
			request := httptest.NewRequest(http.MethodPost, "/api/"+version+"/"+path, strings.NewReader(`{}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "https://attacker.example.test")
			response := httptest.NewRecorder()
			api.ServeHTTP(response, request)
			expectAPIError(t, response, http.StatusForbidden, "invalid_origin")
		}
	}
}

func assertLegacyJSON(t *testing.T, response *httptest.ResponseRecorder, status int, expected string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("legacy status=%d, want %d; body=%s", response.Code, status, response.Body)
	}
	var actualValue, expectedValue any
	if err := json.Unmarshal(response.Body.Bytes(), &actualValue); err != nil {
		t.Fatalf("decode actual legacy JSON: %v; body=%s", err, response.Body)
	}
	if err := json.Unmarshal([]byte(expected), &expectedValue); err != nil {
		t.Fatalf("decode expected legacy JSON: %v", err)
	}
	actualJSON, _ := json.Marshal(actualValue)
	expectedJSON, _ := json.Marshal(expectedValue)
	if string(actualJSON) != string(expectedJSON) {
		t.Fatalf("legacy JSON=%s, want %s", actualJSON, expectedJSON)
	}
}

func assertLegacyAuthEnvelope(t *testing.T, response *httptest.ResponseRecorder, wantAdmin bool) string {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("legacy auth status=%d body=%s", response.Code, response.Body)
	}
	var payload struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Token         string `json:"token"`
			Authorization string `json:"auth_data"`
			IsAdmin       bool   `json:"is_admin"`
			IsDistributor bool   `json:"is_distributor"`
		} `json:"data"`
		Error any `json:"error"`
	}
	decodeResponse(t, response, &payload)
	if payload.Status != "success" || payload.Message != "操作成功" || payload.Error != nil || payload.Data.Token == "" ||
		!strings.HasPrefix(payload.Data.Authorization, "Bearer ") || payload.Data.IsAdmin != wantAdmin || payload.Data.IsDistributor {
		t.Fatalf("legacy auth envelope=%#v body=%s", payload, response.Body)
	}
	return payload.Data.Authorization
}

func assertLegacyTokenExchange(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("legacy token exchange status=%d cache=%q referrer=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Header().Get("Referrer-Policy"), response.Body)
	}
	var payload map[string]json.RawMessage
	decodeResponse(t, response, &payload)
	if len(payload) != 1 || payload["data"] == nil {
		t.Fatalf("legacy token exchange keys=%v body=%s", payload, response.Body)
	}
	var data struct {
		Token         string `json:"token"`
		Authorization string `json:"auth_data"`
		IsAdmin       bool   `json:"is_admin"`
		IsDistributor bool   `json:"is_distributor"`
	}
	if err := json.Unmarshal(payload["data"], &data); err != nil {
		t.Fatal(err)
	}
	if data.Token == "" || !strings.HasPrefix(data.Authorization, "Bearer ") || !data.IsAdmin || data.IsDistributor {
		t.Fatalf("legacy token exchange data=%#v", data)
	}
	return data.Authorization
}
