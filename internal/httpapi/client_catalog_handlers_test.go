package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientCatalogAdminUserQRAndRedirectContracts(t *testing.T) {
	api, _ := newTestAPI(t)
	admin := loginAdmin(t, api)
	listed := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/client-catalog", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"id":"karing"`) || !strings.Contains(listed.Body.String(), `"revision":1`) {
		t.Fatalf("admin catalog status=%d body=%s", listed.Code, listed.Body)
	}
	saved := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/client-catalog", `{
		"revision":1,"links":{"karing":{"android":{
			"direct":"https://downloads.example.test/karing.apk",
			"qr":"https://qr.example.test/karing",
			"cloud":"https://cloud.example.test/karing",
			"tutorial":"/guide/12/karing"
		}}}
	}`)
	if saved.Code != http.StatusOK || !strings.Contains(saved.Body.String(), `"revision":2`) {
		t.Fatalf("save catalog status=%d body=%s", saved.Code, saved.Body)
	}

	createdUser := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/users", `{
		"email":"client-reader@example.test","password":"client-reader-password-123","group_id":null,
		"transfer_enable":0,"expired_at":null,"speed_limit":0,"device_limit":0,"banned":false
	}`)
	if createdUser.Code != http.StatusCreated {
		t.Fatalf("create reader status=%d body=%s", createdUser.Code, createdUser.Body)
	}
	reader := loginAs(t, api, "client-reader@example.test", "client-reader-password-123")
	userCatalog := reader.request(t, api, http.MethodGet, "/api/v1/client-catalog", "")
	if userCatalog.Code != http.StatusOK || !strings.Contains(userCatalog.Body.String(), `"download_url":"https://panel.example.test/client-download/karing/android"`) {
		t.Fatalf("user catalog status=%d body=%s", userCatalog.Code, userCatalog.Body)
	}
	qr := reader.request(t, api, http.MethodGet, "/api/v1/client-catalog/qr?client=karing&platform=android", "")
	if qr.Code != http.StatusOK || !strings.Contains(qr.Body.String(), `data:image/svg+xml;base64,`) ||
		!strings.Contains(qr.Body.String(), `"download_url":"https://panel.example.test/client-link/karing/android/qr"`) {
		t.Fatalf("QR status=%d body=%s", qr.Code, qr.Body)
	}
	if denied := reader.request(t, api, http.MethodGet, "/api/v1/admin/admin/client-catalog", ""); denied.Code != http.StatusForbidden {
		t.Fatalf("ordinary user admin catalog status=%d", denied.Code)
	}

	redirect := httptest.NewRecorder()
	api.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, "/client-download/karing/android", nil))
	if redirect.Code != http.StatusFound || redirect.Header().Get("Location") != "https://downloads.example.test/karing.apk" ||
		redirect.Header().Get("Referrer-Policy") != "no-referrer" || redirect.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("download redirect status=%d location=%q", redirect.Code, redirect.Header().Get("Location"))
	}
	for _, path := range []string{"/client-download/unknown/android", "/client-link/karing/android/shell", "/client-link/karing/beos/cloud"} {
		response := httptest.NewRecorder()
		api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("unsafe route %s status=%d body=%s", path, response.Code, response.Body)
		}
	}
}

func TestClientCatalogRejectsCSRFUnsafeURLsUnknownFieldsAndStaleWrites(t *testing.T) {
	api, _ := newTestAPI(t)
	admin := loginAdmin(t, api)
	for name, body := range map[string]string{
		"unsafe url":     `{"revision":1,"links":{"karing":{"android":{"cloud":"javascript:alert(1)"}}}}`,
		"unknown client": `{"revision":1,"links":{"unknown":{"android":{"direct":"https://example.test/app"}}}}`,
		"unknown field":  `{"revision":1,"links":{},"popup":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/client-catalog", body)
			want := http.StatusUnprocessableEntity
			if name == "unknown field" {
				want = http.StatusBadRequest
			}
			if response.Code != want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, want, response.Body)
			}
		})
	}

	request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/admin/client-catalog", strings.NewReader(`{"revision":1,"links":{}}`))
	request.Header.Set("Content-Type", "application/json")
	admin.addCookies(request)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d body=%s", response.Code, response.Body)
	}

	first := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/client-catalog", `{"revision":1,"links":{}}`)
	if first.Code != http.StatusOK {
		t.Fatalf("first save status=%d body=%s", first.Code, first.Body)
	}
	stale := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/client-catalog", `{"revision":1,"links":{}}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale save status=%d body=%s", stale.Code, stale.Body)
	}
}

func TestClientCatalogGitHubResponseCannotBecomeOpenRedirect(t *testing.T) {
	api, _ := newTestAPIWithCatalogHTTP(t, func(_ *http.Request) (*http.Response, error) {
		body := `{"assets":[{"name":"karing_1_android_arm64-v8a.apk","browser_download_url":"https://attacker.example.test/payload.apk"}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/client-download/karing/android", nil))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Location") != "" ||
		response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("malicious asset status=%d location=%q body=%s", response.Code, response.Header().Get("Location"), response.Body)
	}
}
