package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestRemoteHandlerServesOnlyAuthorizedEntriesAndDelegatesBackend(t *testing.T) {
	type observedRequest struct {
		method, path, rawQuery, cookie, authorization, forwardedFor string
	}
	var observedMu sync.Mutex
	var observed []observedRequest
	frontend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observedMu.Lock()
		observed = append(observed, observedRequest{
			method: r.Method, path: r.URL.Path, rawQuery: r.URL.RawQuery,
			cookie: r.Header.Get("Cookie"), authorization: r.Header.Get("Authorization"), forwardedFor: r.Header.Get("X-Forwarded-For"),
		})
		observedMu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Set-Cookie", "frontend-secret=must-not-escape")
		_, _ = w.Write([]byte("<main>split frontend</main>"))
	}))
	defer frontend.Close()

	apiCalls := 0
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		apiCalls++
		w.WriteHeader(http.StatusNoContent)
	})
	handler, err := NewRemote(frontend.URL, api, func(*http.Request) (FrontendAccess, error) {
		return FrontendAccess{Allowed: true, SecurePath: "secure-admin-01"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		method, path string
		wantStatus   int
		wantBody     string
	}{
		{method: http.MethodGet, path: "/?browser=must-not-forward", wantStatus: http.StatusOK, wantBody: "split frontend"},
		{method: http.MethodGet, path: "/secure-admin-01/", wantStatus: http.StatusOK, wantBody: "split frontend"},
		{method: http.MethodHead, path: "/index.html", wantStatus: http.StatusOK},
		{method: http.MethodGet, path: "/wrong-admin-path/", wantStatus: http.StatusNotFound},
		{method: http.MethodGet, path: "/assets/app.js", wantStatus: http.StatusNotFound},
		{method: http.MethodPost, path: "/secure-admin-01/", wantStatus: http.StatusMethodNotAllowed},
		{method: http.MethodGet, path: "/api/v1/auth/session", wantStatus: http.StatusNoContent},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		request.Header.Set("Cookie", "session=must-not-forward")
		request.Header.Set("Authorization", "Bearer must-not-forward")
		request.Header.Set("X-Forwarded-For", "203.0.113.10")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), test.wantBody) {
			t.Errorf("%s %s = %d %q, want %d containing %q", test.method, test.path, response.Code, response.Body.String(), test.wantStatus, test.wantBody)
		}
		if test.wantStatus == http.StatusOK {
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Security-Policy") == "" {
				t.Errorf("%s is missing frontend cache or security headers", test.path)
			}
			if response.Header().Get("Set-Cookie") != "" {
				t.Errorf("%s copied an upstream Set-Cookie header", test.path)
			}
		}
	}
	if apiCalls != 1 {
		t.Fatalf("API calls = %d, want 1", apiCalls)
	}
	observedMu.Lock()
	defer observedMu.Unlock()
	if len(observed) != 3 {
		t.Fatalf("remote index requests = %d, want 3", len(observed))
	}
	for _, request := range observed {
		if request.method != http.MethodGet || request.path != "/index.html" || request.rawQuery != "" || request.cookie != "" || request.authorization != "" || request.forwardedFor != "" {
			t.Errorf("unsafe remote index request: %#v", request)
		}
	}
}

func TestRemoteHandlerWithoutAccessResolverPreservesUnrestrictedSPAFallback(t *testing.T) {
	frontend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("index"))
	}))
	defer frontend.Close()

	handler, err := NewRemote(frontend.URL, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/account/profile", nil))
	if response.Code != http.StatusOK || response.Body.String() != "index" {
		t.Fatalf("unrestricted SPA fallback = %d %q, want 200 index", response.Code, response.Body.String())
	}
}

func TestRemoteHandlerPreservesSafeModeAndSecurePathFailureSemantics(t *testing.T) {
	frontend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("index"))
	}))
	defer frontend.Close()

	handler, err := NewRemote(frontend.URL, http.NotFoundHandler(), func(request *http.Request) (FrontendAccess, error) {
		return FrontendAccess{
			Allowed:    HostMatchesURL(request.Host, "https://panel.example.test"),
			SecurePath: "secure-admin-01",
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path, host string
		want       int
	}{
		{path: "/", host: "attacker.example.test", want: http.StatusForbidden},
		{path: "/secure-admin-01/", host: "attacker.example.test", want: http.StatusForbidden},
		{path: "/wrong-admin-path/", host: "attacker.example.test", want: http.StatusNotFound},
		{path: "/secure-admin-01/", host: "panel.example.test", want: http.StatusOK},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.Host = test.host
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Errorf("GET %s Host %q = %d, want %d", test.path, test.host, response.Code, test.want)
		}
	}
}

func TestRemoteHandlerFailsClosedForInvalidOriginsAndUpstreamResponses(t *testing.T) {
	for _, origin := range []string{
		"", "frontend:8080", "ftp://frontend", "http://user@frontend", "http://frontend/path",
		"http://frontend?query=1", "http://frontend#fragment",
	} {
		if _, err := NewRemote(origin, http.NotFoundHandler()); err == nil {
			t.Errorf("NewRemote accepted unsafe origin %q", origin)
		}
	}

	for _, test := range []struct {
		name    string
		handler http.Handler
	}{
		{name: "status", handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "private upstream failure", http.StatusServiceUnavailable)
		})},
		{name: "redirect", handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/other", http.StatusFound)
		})},
		{name: "content type", handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"secret":"must-not-escape"}`))
		})},
		{name: "size", handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(strings.Repeat("x", remoteIndexMaxBytes+1)))
		})},
	} {
		t.Run(test.name, func(t *testing.T) {
			frontend := httptest.NewServer(test.handler)
			defer frontend.Close()
			handler, err := NewRemote(frontend.URL, http.NotFoundHandler())
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
			if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "private upstream failure") || strings.Contains(response.Body.String(), "must-not-escape") {
				t.Fatalf("upstream failure response = %d %q", response.Code, response.Body.String())
			}
		})
	}
}
