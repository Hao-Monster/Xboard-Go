package webui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandlerServesFrontendAndDelegatesBackend(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.html", "<main>Xboard-Go</main>")
	writeTestFile(t, root, "assets/app-123.js", "console.log('ok')")
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	})
	handler, err := New(root, api)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for _, test := range []struct {
		path      string
		wantBody  string
		wantCache string
	}{
		{path: "/", wantBody: "Xboard-Go", wantCache: "no-store"},
		{path: "/users/42", wantBody: "Xboard-Go", wantCache: "no-store"},
		{path: "/assets/app-123.js", wantBody: "console.log", wantCache: "public, max-age=31536000, immutable"},
		{path: "/api/v1/auth/session", wantBody: `"status":"success"`},
		{path: "/client-download/karing/android", wantBody: `"status":"success"`},
		{path: "/client-link/karing/android/qr", wantBody: `"status":"success"`},
		{path: "/guide/1/article", wantBody: `"status":"success"`},
		{path: "/guide/1/content", wantBody: `"status":"success"`},
		{path: "/knowledge-attachments/550e8400-e29b-41d4-a716-446655440000", wantBody: `"status":"success"`},
		{path: "/guide-attachments/550e8400-e29b-41d4-a716-446655440000", wantBody: `"status":"success"`},
		{path: "/healthz", wantBody: `"status":"success"`},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), test.wantBody) {
			t.Fatalf("GET %s = %d %q", test.path, recorder.Code, recorder.Body.String())
		}
		if test.wantCache != "" && recorder.Header().Get("Cache-Control") != test.wantCache {
			t.Fatalf("GET %s Cache-Control = %q", test.path, recorder.Header().Get("Cache-Control"))
		}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/missing.js", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/../index.html", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("traversal path status = %d", recorder.Code)
	}
}

func TestHandlerRejectsUnsafeMethodsAndMissingBuild(t *testing.T) {
	if _, err := New(t.TempDir(), http.NotFoundHandler()); err == nil {
		t.Fatal("New() accepted a build without index.html")
	}
	root := t.TempDir()
	writeTestFile(t, root, "index.html", "ok")
	handler, err := New(root, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/account", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST frontend route status = %d", recorder.Code)
	}
	if recorder.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("frontend response is missing Content-Security-Policy")
	}
	if !strings.Contains(recorder.Header().Get("Content-Security-Policy"), "img-src 'self' data: https: http:") {
		t.Fatal("frontend policy blocks administrator-configured notice images")
	}
	csp := recorder.Header().Get("Content-Security-Policy")
	for _, required := range []string{
		"script-src 'self' https://www.google.com/recaptcha/ https://www.gstatic.com/recaptcha/ https://www.recaptcha.net/recaptcha/ https://challenges.cloudflare.com/turnstile/",
		"frame-src https://www.google.com/recaptcha/ https://recaptcha.google.com/recaptcha/ https://www.recaptcha.net/recaptcha/ https://challenges.cloudflare.com/turnstile/",
	} {
		if !strings.Contains(csp, required) {
			t.Fatalf("frontend CSP is missing fixed CAPTCHA allowlist %q: %s", required, csp)
		}
	}
	if strings.Contains(csp, "script-src 'self' https:;") || strings.Contains(csp, "frame-src https:;") {
		t.Fatalf("frontend CSP has an overbroad CAPTCHA source: %s", csp)
	}
}

func TestDynamicSubscriptionPathsAreDelegatedWithoutCatchingOrdinarySPARoutes(t *testing.T) {
	token := "0123456789abcdef0123456789abcdef" // gitleaks:allow -- deterministic route fixture
	for _, test := range []struct {
		path string
		want bool
	}{
		{path: "/s/" + token, want: true},
		{path: "/custom-path/" + token, want: true},
		{path: "/custom-path/ABCDEF0123456789ABCDEF0123456789", want: false},
		{path: "/account/" + token + "/extra", want: false},
		{path: "/account/profile", want: false},
	} {
		if got := isBackendPath(test.path); got != test.want {
			t.Errorf("isBackendPath(%q) = %v, want %v", test.path, got, test.want)
		}
	}
}

func TestFrontendAccessCheckerProtectsEverySPAEntryWithoutTaxingAssetsOrAPI(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.html", "protected")
	writeTestFile(t, root, "assets/app.js", "asset")
	checks := 0
	handler, err := New(root, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), func(request *http.Request) (bool, error) {
		checks++
		return HostMatchesURL(request.Host, "https://panel.example.test:8443"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path, host string
		status     int
	}{
		{path: "/", host: "PANEL.EXAMPLE.TEST.:443", status: http.StatusOK},
		{path: "/account", host: "attacker.example.test", status: http.StatusForbidden},
		{path: "/index.html", host: "attacker.example.test", status: http.StatusForbidden},
		{path: "/assets/app.js", host: "attacker.example.test", status: http.StatusOK},
		{path: "/api/v1/auth/session", host: "attacker.example.test", status: http.StatusNoContent},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.Host = test.host
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("GET %s Host %q status=%d want=%d body=%s", test.path, test.host, response.Code, test.status, response.Body)
		}
	}
	if checks != 3 {
		t.Fatalf("frontend access checks = %d, want 3 HTML entries only", checks)
	}
}

func TestHostMatchesURLHandlesPortsIPv6AndRejectsMalformedInputs(t *testing.T) {
	for _, test := range []struct {
		host, configured string
		want             bool
	}{
		{host: "panel.example.test:1234", configured: "https://panel.example.test/root", want: true},
		{host: "PANEL.EXAMPLE.TEST.", configured: "https://panel.example.test", want: true},
		{host: "[0:0:0:0:0:0:0:1]:8080", configured: "http://[::1]:7080", want: true},
		{host: "attacker.example.test", configured: "https://panel.example.test", want: false},
		{host: "panel.example.test", configured: "", want: false},
		{host: "user@panel.example.test", configured: "https://panel.example.test", want: false},
	} {
		if got := HostMatchesURL(test.host, test.configured); got != test.want {
			t.Errorf("HostMatchesURL(%q, %q) = %v, want %v", test.host, test.configured, got, test.want)
		}
	}
}

func TestFrontendAccessCheckerFailsClosedOnSettingsError(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.html", "protected")
	handler, err := New(root, http.NotFoundHandler(), func(*http.Request) (bool, error) {
		return false, errors.New("settings unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/account", nil))
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "settings unavailable") {
		t.Fatalf("settings failure status=%d body=%q", response.Code, response.Body.String())
	}
}

func writeTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
