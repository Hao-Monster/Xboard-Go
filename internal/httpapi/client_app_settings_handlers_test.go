package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type clientAppSettingsWire struct {
	Revision           int64  `json:"revision"`
	WindowsVersion     string `json:"windows_version"`
	WindowsDownloadURL string `json:"windows_download_url"`
	MacOSVersion       string `json:"macos_version"`
	MacOSDownloadURL   string `json:"macos_download_url"`
	AndroidVersion     string `json:"android_version"`
	AndroidDownloadURL string `json:"android_download_url"`
}

func TestClientAppSettingsModernLegacyAndVersionContracts(t *testing.T) {
	api, database := newTestAPI(t)
	createHTTPTestUser(t, database, "client-version-reader@example.test", "client-version-reader-password-123")
	administrator := loginAdmin(t, api)
	reader := loginAs(t, api, "client-version-reader@example.test", "client-version-reader-password-123")

	unauthenticated := testClient{}.request(t, api, http.MethodGet, "/api/v1/admin/client-app-settings", "")
	expectAPIError(t, unauthenticated, http.StatusUnauthorized, "unauthenticated")
	forbidden := reader.request(t, api, http.MethodGet, "/api/v1/admin/client-app-settings", "")
	expectAPIError(t, forbidden, http.StatusForbidden, "forbidden")

	initialResponse := administrator.request(t, api, http.MethodGet, "/api/v1/admin/client-app-settings", "")
	if initialResponse.Code != http.StatusOK {
		t.Fatalf("initial client app settings status=%d body=%s", initialResponse.Code, initialResponse.Body)
	}
	initial := decodeClientAppSettingsEnvelope(t, initialResponse)
	if initial.Revision != 1 || initial.WindowsVersion != "" || initial.WindowsDownloadURL != "" ||
		initial.MacOSVersion != "" || initial.MacOSDownloadURL != "" ||
		initial.AndroidVersion != "" || initial.AndroidDownloadURL != "" {
		t.Fatalf("initial client app settings=%#v", initial)
	}

	updatedResponse := administrator.request(t, api, http.MethodPut, "/api/v1/admin/client-app-settings", `{
		"revision":1,
		"windows_version":" 4.8.1 ","windows_download_url":" https://download.example.test/windows.exe ",
		"macos_version":"4.8.2","macos_download_url":"https://download.example.test/macos.dmg",
		"android_version":"4.8.3","android_download_url":"https://download.example.test/android.apk"
	}`)
	if updatedResponse.Code != http.StatusOK {
		t.Fatalf("update client app settings status=%d body=%s", updatedResponse.Code, updatedResponse.Body)
	}
	updated := decodeClientAppSettingsEnvelope(t, updatedResponse)
	if updated.Revision != 2 || updated.WindowsVersion != "4.8.1" ||
		updated.WindowsDownloadURL != "https://download.example.test/windows.exe" ||
		updated.MacOSVersion != "4.8.2" || updated.MacOSDownloadURL != "https://download.example.test/macos.dmg" ||
		updated.AndroidVersion != "4.8.3" || updated.AndroidDownloadURL != "https://download.example.test/android.apk" {
		t.Fatalf("updated client app settings=%#v", updated)
	}

	stale := administrator.request(t, api, http.MethodPut, "/api/v1/admin/client-app-settings", `{
		"revision":1,
		"windows_version":"","windows_download_url":"","macos_version":"","macos_download_url":"",
		"android_version":"","android_download_url":""
	}`)
	expectAPIError(t, stale, http.StatusConflict, "settings_conflict")
	for _, invalidBody := range []string{
		`{"revision":2,"windows_version":"4","windows_download_url":"http://download.example.test/a","macos_version":"","macos_download_url":"","android_version":"","android_download_url":""}`,
		`{"revision":2,"windows_version":"4","windows_download_url":"https://user:secret@download.example.test/a","macos_version":"","macos_download_url":"","android_version":"","android_download_url":""}`,
		`{"revision":2,"windows_version":"4","windows_download_url":"https://download.example.test/a#fragment","macos_version":"","macos_download_url":"","android_version":"","android_download_url":""}`,
	} {
		invalid := administrator.request(t, api, http.MethodPut, "/api/v1/admin/client-app-settings", invalidBody)
		expectAPIError(t, invalid, http.StatusUnprocessableEntity, "validation_failed")
	}
	unknown := administrator.request(t, api, http.MethodPut, "/api/v1/admin/client-app-settings", `{
		"revision":2,
		"windows_version":"4","windows_download_url":"","macos_version":"","macos_download_url":"",
		"android_version":"","android_download_url":"","unexpected":true
	}`)
	expectAPIError(t, unknown, http.StatusBadRequest, "invalid_json")
	invalidUTF8Body := []byte(`{"revision":2,"windows_version":"`)
	invalidUTF8Body = append(invalidUTF8Body, 0xff)
	invalidUTF8Body = append(invalidUTF8Body, []byte(`","windows_download_url":"","macos_version":"","macos_download_url":"","android_version":"","android_download_url":""}`)...)
	invalidUTF8Request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/client-app-settings", bytes.NewReader(invalidUTF8Body))
	invalidUTF8Request.Header.Set("Content-Type", "application/json")
	invalidUTF8Request.Header.Set("X-CSRF-Token", administrator.csrf)
	administrator.addCookies(invalidUTF8Request)
	invalidUTF8 := httptest.NewRecorder()
	api.ServeHTTP(invalidUTF8, invalidUTF8Request)
	expectAPIError(t, invalidUTF8, http.StatusBadRequest, "invalid_json")

	legacyLogin := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123")
	legacyFetch := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=app", legacyLogin.Authorization, "")
	if legacyFetch.Code != http.StatusOK || !containsAll(legacyFetch.Body.String(),
		`"windows_version":"4.8.1"`, `"windows_download_url":"https://download.example.test/windows.exe"`,
		`"macos_version":"4.8.2"`, `"macos_download_url":"https://download.example.test/macos.dmg"`,
		`"android_version":"4.8.3"`, `"android_download_url":"https://download.example.test/android.apk"`) {
		t.Fatalf("legacy app fetch status=%d body=%s", legacyFetch.Code, legacyFetch.Body)
	}
	if strings.Contains(legacyFetch.Body.String(), "revision") || strings.Contains(legacyFetch.Body.String(), "updated_at") {
		t.Fatalf("legacy app fetch disclosed internal fields: %s", legacyFetch.Body)
	}
	legacySaved := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/save", legacyLogin.Authorization,
		`{"windows_version":"5.0.0"}`)
	if legacySaved.Code != http.StatusOK || !containsAll(legacySaved.Body.String(), `"status":"success"`, `"data":true`) {
		t.Fatalf("legacy partial app save status=%d body=%s", legacySaved.Code, legacySaved.Body)
	}
	legacyAfter := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=app", legacyLogin.Authorization, "")
	if !containsAll(legacyAfter.Body.String(), `"windows_version":"5.0.0"`,
		`"macos_version":"4.8.2"`, `"android_version":"4.8.3"`) {
		t.Fatalf("legacy partial app save erased fields: %s", legacyAfter.Body)
	}
	mixed := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/save", legacyLogin.Authorization,
		`{"windows_version":"5.1.0","email_host":"smtp.example.test"}`)
	if mixed.Code != http.StatusUnprocessableEntity || !strings.Contains(mixed.Body.String(), `"status":"fail"`) {
		t.Fatalf("mixed legacy config status=%d body=%s", mixed.Code, mixed.Body)
	}

	userLogin := loginLegacyBearer(t, api, "client-version-reader@example.test", "client-version-reader-password-123")
	for _, version := range []string{"v1", "v2"} {
		ordinary := requestClientAppVersion(api, "/api/"+version+"/client/app/getVersion?token="+url.QueryEscape(userLogin.SubscriptionToken), "Xboard Client/1.0")
		if ordinary.Code != http.StatusOK || !containsAll(ordinary.Body.String(),
			`"windows_version":"5.0.0"`, `"windows_download_url":"https://download.example.test/windows.exe"`,
			`"macos_version":"4.8.2"`, `"macos_download_url":"https://download.example.test/macos.dmg"`,
			`"android_version":"4.8.3"`, `"android_download_url":"https://download.example.test/android.apk"`) {
			t.Fatalf("%s ordinary version status=%d body=%s", version, ordinary.Code, ordinary.Body)
		}
		windows := requestClientAppVersion(api, "/api/"+version+"/client/app/getVersion?token="+url.QueryEscape(userLogin.SubscriptionToken), "tidalab/4.0.0 Win64")
		if windows.Code != http.StatusOK || !containsAll(windows.Body.String(), `"version":"5.0.0"`,
			`"download_url":"https://download.example.test/windows.exe"`) ||
			strings.Contains(windows.Body.String(), "windows_version") || strings.Contains(windows.Body.String(), "macos_version") {
			t.Fatalf("%s Windows compatibility status=%d body=%s", version, windows.Code, windows.Body)
		}
		macos := requestClientAppVersion(api, "/api/"+version+"/client/app/getVersion?token="+url.QueryEscape(userLogin.SubscriptionToken), "tunnelab/4.0.0 Darwin")
		if macos.Code != http.StatusOK || !containsAll(macos.Body.String(), `"version":"4.8.2"`,
			`"download_url":"https://download.example.test/macos.dmg"`) {
			t.Fatalf("%s macOS compatibility status=%d body=%s", version, macos.Code, macos.Body)
		}
		caseSensitive := requestClientAppVersion(api, "/api/"+version+"/client/app/getVersion?token="+url.QueryEscape(userLogin.SubscriptionToken), "Tidalab/4.0.0 Win64")
		if !strings.Contains(caseSensitive.Body.String(), `"windows_version":"5.0.0"`) {
			t.Fatalf("%s changed legacy case-sensitive UA branch: %s", version, caseSensitive.Body)
		}
	}
	for name, input := range map[string]store.CreateAdminUserInput{
		"banned":  {Email: "client-version-banned@example.test", PasswordHash: "hash", Banned: true},
		"expired": {Email: "client-version-expired@example.test", PasswordHash: "hash", ExpiredAt: timePointer(fixedNow().Add(-time.Hour))},
	} {
		created, err := database.CreateAdminUsers(t.Context(), []store.CreateAdminUserInput{input}, fixedNow())
		if err != nil {
			t.Fatalf("create %s getVersion account: %v", name, err)
		}
		response := requestClientAppVersion(api, "/api/v2/client/app/getVersion?token="+url.QueryEscape(created[0].SubscriptionToken), "Xboard Client/1.0")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"windows_version":"5.0.0"`) {
			t.Fatalf("%s account changed legacy getVersion eligibility: status=%d body=%s", name, response.Code, response.Body)
		}
	}
	missingToken := requestClientAppVersion(api, "/api/v1/client/app/getVersion", "Xboard Client/1.0")
	if missingToken.Code != http.StatusForbidden || missingToken.Header().Get("Cache-Control") != "no-store, private" || !strings.Contains(missingToken.Body.String(), "token is null") {
		t.Fatalf("missing client token status=%d body=%s", missingToken.Code, missingToken.Body)
	}
	unknownToken := requestClientAppVersion(api, "/api/v2/client/app/getVersion?token=unknown", "Xboard Client/1.0")
	if unknownToken.Code != http.StatusForbidden || unknownToken.Header().Get("Cache-Control") != "no-store, private" || !strings.Contains(unknownToken.Body.String(), "token is error") {
		t.Fatalf("unknown client token status=%d body=%s", unknownToken.Code, unknownToken.Body)
	}

	audits, err := database.ListAdminAuditLogs(t.Context(), store.AdminAuditFilter{Page: 1, PageSize: 20, Query: "client-app-settings"})
	if err != nil || audits.Total == 0 || audits.Items[0].Route != "/api/v1/admin/client-app-settings" {
		t.Fatalf("modern client app settings audit=%#v err=%v", audits, err)
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func decodeClientAppSettingsEnvelope(t *testing.T, response *httptest.ResponseRecorder) clientAppSettingsWire {
	t.Helper()
	var envelope struct {
		Data clientAppSettingsWire `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode client app settings: %v; body=%s", err, response.Body)
	}
	return envelope.Data
}

func requestClientAppVersion(api http.Handler, path, userAgent string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("User-Agent", userAgent)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}
