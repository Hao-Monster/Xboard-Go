package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfiguredSupportOriginOnlyExtendsFramePolicy(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.html", "ok")
	origin := "https://chat.example.test:8443"
	handler, err := New(root, http.NotFoundHandler(), func(*http.Request) (FrontendAccess, error) {
		return FrontendAccess{Allowed: true, SecurePath: "admin", ChatwootOrigin: origin}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/", "/index.html", "/admin/"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, route, nil))
		expected := strings.Replace(contentSecurityPolicy, "; style-src", " "+origin+"; style-src", 1)
		if recorder.Code != 200 || recorder.Header().Get("Content-Security-Policy") != expected {
			t.Fatalf("%s policy or response mismatch: %d %s", route, recorder.Code, recorder.Header().Get("Content-Security-Policy"))
		}
	}
	origin = ""
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Header().Get("Content-Security-Policy") != contentSecurityPolicy {
		t.Fatal("clearing configuration did not restore restrictive policy")
	}
}

func TestSupportFrameOriginRejectsPolicyInjection(t *testing.T) {
	for _, value := range []string{"", "http://chat.example.test", "https://*.example.test", "https://chat.example.test;script-src", "https://user:pass@chat.example.test", "https://chat.example.test/path", "https://chat.example.test?x=1", "https://chat.example.test#x", "https://chat.example.test\n"} {
		if got := safeFrameOrigin(value); got != "" {
			t.Fatalf("accepted unsafe origin %q: %q", value, got)
		}
	}
	if got := safeFrameOrigin("https://chat.example.test/"); got != "https://chat.example.test" {
		t.Fatalf("trailing slash = %q", got)
	}
}
