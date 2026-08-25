package webui

import (
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
	token := "0123456789abcdef0123456789abcdef"
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
