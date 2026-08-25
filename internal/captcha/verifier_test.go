package captcha

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestVerifierAcceptsAllProvidersAndSendsBoundedFormData(t *testing.T) {
	tests := []struct {
		name      string
		provider  Provider
		response  string
		threshold float64
	}{
		{name: "recaptcha-v2", provider: ProviderRecaptcha, response: `{"success":true}`},
		{name: "recaptcha-v3", provider: ProviderRecaptchaV3, response: `{"success":true,"score":0.9,"action":"register"}`, threshold: 0.7},
		{name: "turnstile", provider: ProviderTurnstile, response: `{"success":true,"action":"register"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
					t.Fatalf("request = %s content-type=%q", r.Method, r.Header.Get("Content-Type"))
				}
				body, _ := io.ReadAll(r.Body)
				values, err := url.ParseQuery(string(body))
				if err != nil || values.Get("secret") != "server-secret" || values.Get("response") != "browser-token" || values.Get("remoteip") != "192.0.2.10" {
					t.Fatalf("verification form = %q err=%v", body, err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()
			service, err := New(Options{HTTPClient: server.Client(), RecaptchaEndpoint: server.URL, RecaptchaV3Endpoint: server.URL, TurnstileEndpoint: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			err = service.Verify(t.Context(), Verification{Provider: test.provider, Secret: []byte("server-secret"), Token: "browser-token", RemoteIP: "192.0.2.10", ExpectedAction: "register", ScoreThreshold: test.threshold})
			if err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
		})
	}
}

func TestVerifierRejectsInvalidScoreActionAndProviderResponses(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
		response string
	}{
		{name: "provider rejection", provider: ProviderRecaptcha, response: `{"success":false,"error-codes":["invalid-input-response"]}`},
		{name: "v3 low score", provider: ProviderRecaptchaV3, response: `{"success":true,"score":0.49,"action":"register"}`},
		{name: "v3 wrong action", provider: ProviderRecaptchaV3, response: `{"success":true,"score":0.9,"action":"sendEmailVerify"}`},
		{name: "turnstile wrong action", provider: ProviderTurnstile, response: `{"success":true,"action":"login"}`},
		{name: "wrong hostname", provider: ProviderRecaptcha, response: `{"success":true,"hostname":"attacker.example"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(test.response)) }))
			defer server.Close()
			service, _ := New(Options{HTTPClient: server.Client(), RecaptchaEndpoint: server.URL, RecaptchaV3Endpoint: server.URL, TurnstileEndpoint: server.URL})
			input := Verification{Provider: test.provider, Secret: []byte("secret"), Token: "token", ExpectedAction: "register", ScoreThreshold: 0.5}
			if test.name == "wrong hostname" {
				input.ExpectedHostname = "panel.example.test"
			}
			err := service.Verify(t.Context(), input)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Verify() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestVerifierFailsClosedForTransportStatusMalformedAndOversizedResponses(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{name: "upstream status", handler: func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "upstream detail must not escape", http.StatusBadGateway)
		}},
		{name: "malformed JSON", handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not-json")) }},
		{name: "oversized response", handler: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBytes+1)))
		}},
		{name: "timeout", handler: func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(100 * time.Millisecond)
			_, _ = w.Write([]byte(`{"success":true}`))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			client := server.Client()
			client.Timeout = 20 * time.Millisecond
			service, _ := New(Options{HTTPClient: client, RecaptchaEndpoint: server.URL})
			err := service.Verify(context.Background(), Verification{Provider: ProviderRecaptcha, Secret: []byte("secret-value"), Token: "token-value"})
			if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "secret-value") || strings.Contains(err.Error(), "token-value") || strings.Contains(err.Error(), "upstream detail") {
				t.Fatalf("Verify() error = %v, want sanitized ErrUnavailable", err)
			}
		})
	}
}

func TestVerifierRejectsInvalidInputsBeforeNetwork(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	service, _ := New(Options{HTTPClient: server.Client(), RecaptchaEndpoint: server.URL})
	for _, input := range []Verification{
		{Provider: Provider("unknown"), Secret: []byte("secret"), Token: "token"},
		{Provider: ProviderRecaptcha, Secret: nil, Token: "token"},
		{Provider: ProviderRecaptcha, Secret: []byte("secret"), Token: ""},
		{Provider: ProviderRecaptcha, Secret: []byte("secret"), Token: strings.Repeat("x", maxTokenBytes+1)},
		{Provider: ProviderRecaptchaV3, Secret: []byte("secret"), Token: "token", ExpectedAction: "bad action", ScoreThreshold: 0.5},
		{Provider: ProviderRecaptchaV3, Secret: []byte("secret"), Token: "token", ExpectedAction: "register", ScoreThreshold: 1.1},
	} {
		if err := service.Verify(t.Context(), input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Verify(%#v) error = %v, want ErrInvalid", input, err)
		}
	}
	if requests != 0 {
		t.Fatalf("invalid inputs caused %d upstream requests", requests)
	}
}
