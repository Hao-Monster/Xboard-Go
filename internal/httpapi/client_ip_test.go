package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestClientIPTrustedProxySeparatesOrdinaryQuotas(t *testing.T) {
	for _, testCase := range []struct {
		name, peer, first, next string
		duplicate               bool
		independent             bool
	}{
		{name: "trusted", peer: "10.0.0.2:8080", first: "198.51.100.1", next: "198.51.100.2", independent: true},
		{name: "same-client", peer: "10.0.0.2:8080", first: "198.51.100.1", next: "198.51.100.1"},
		{name: "untrusted", peer: "192.0.2.2:8080", first: "198.51.100.1", next: "198.51.100.2"},
		{name: "rightmost-untrusted", peer: "10.0.0.2:8080", first: "203.0.113.1, 198.51.100.1, 10.0.0.3", next: "203.0.113.2, 198.51.100.1, 10.0.0.3"},
		{name: "trusted-chain", peer: "10.0.0.2:8080", first: "198.51.100.1, 10.0.0.3", next: "198.51.100.2, 10.0.0.3", independent: true},
		{name: "ipv6-canonical", peer: "[2001:db8:1::2]:8080", first: "2001:db8:2::1", next: "2001:0db8:0002:0000:0000:0000:0000:0001"},
		{name: "malformed", peer: "10.0.0.2:8080", first: "bad, 198.51.100.1", next: "also-bad, 198.51.100.2"},
		{name: "duplicate-header", peer: "10.0.0.2:8080", first: "198.51.100.1", next: "198.51.100.2", duplicate: true},
		{name: "bounded-header", peer: "10.0.0.2:8080", first: strings.Repeat("1", 4097), next: strings.Repeat("2", 4097)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			api, _ := newTestAPIWithTrustedProxyPrefixes(t, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("2001:db8:1::/48")})
			request := func(forwarded string) *httptest.ResponseRecorder {
				// Invalid input exercises only the cheap quota and validation path:
				// it cannot queue mail, calculate hashes or contact another service.
				r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password-reset/request", strings.NewReader(`{}`))
				r.RemoteAddr = testCase.peer
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("X-Forwarded-For", forwarded)
				if testCase.duplicate {
					r.Header.Add("X-Forwarded-For", "203.0.113.9")
				}
				response := httptest.NewRecorder()
				api.ServeHTTP(response, r)
				if r.RemoteAddr != testCase.peer {
					t.Fatal("client resolution rewrote the transport peer")
				}
				return response
			}
			for attempt := 0; attempt < 10; attempt++ {
				if response := request(testCase.first); response.Code != http.StatusUnprocessableEntity {
					t.Fatalf("validation request %d status=%d body=%s", attempt, response.Code, response.Body)
				}
			}
			response := request(testCase.next)
			if testCase.independent {
				if response.Code != http.StatusUnprocessableEntity {
					t.Fatalf("independent client shares proxy quota: status=%d body=%s", response.Code, response.Body)
				}
			} else {
				expectAPIError(t, response, http.StatusTooManyRequests, "password_reset_rate_limited")
			}
		})
	}
}
