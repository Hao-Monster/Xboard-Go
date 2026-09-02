package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestSECNODE005HandshakeBodyBoundary(t *testing.T) {
	api, database := newTestAPI(t)
	machine, enrollment, err := database.CreateMachine(context.Background(), store.CreateMachineInput{
		Name: "node-security-handshake", IsActive: true,
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	credential, err := database.ExchangeEnrollment(context.Background(), machine.ID, enrollment.Code, fixedNow())
	if err != nil {
		t.Fatal(err)
	}

	const legacyHandshakeBodyLimit = 64 << 10
	base := fmt.Sprintf(`{"machine_id":%d}`, machine.ID)
	for _, size := range []int{legacyHandshakeBodyLimit - 1, legacyHandshakeBodyLimit} {
		response := agentRequest(api, http.MethodPost, "/api/v2/server/handshake", credential.Token, paddedNodeSecurityJSON(t, base, size))
		if response.Code != http.StatusOK {
			t.Fatalf("handshake body size %d status=%d, want %d; body=%s", size, response.Code, http.StatusOK, response.Body)
		}
	}

	oversized := agentRequest(api, http.MethodPost, "/api/v2/server/handshake", credential.Token,
		paddedNodeSecurityJSON(t, base, legacyHandshakeBodyLimit+1))
	expectAPIError(t, oversized, http.StatusRequestEntityTooLarge, "request_too_large")
}

func TestSECNODE005TrustedProxyAddressBoundary(t *testing.T) {
	trusted := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("2001:db8:ffff::/48"),
	}
	for _, test := range []struct {
		name       string
		remoteAddr string
		forwarded  []string
		wantClient string
		wantPeer   string
	}{
		{name: "direct peer ignores spoofed header", remoteAddr: "192.0.2.10:443", forwarded: []string{"198.51.100.20"}, wantClient: "192.0.2.10", wantPeer: "192.0.2.10"},
		{name: "trusted proxy exposes client", remoteAddr: "10.0.0.2:8080", forwarded: []string{"198.51.100.20"}, wantClient: "198.51.100.20", wantPeer: "10.0.0.2"},
		{name: "trusted chain is stripped from right", remoteAddr: "10.0.0.2:8080", forwarded: []string{"198.51.100.20, 10.0.0.3"}, wantClient: "198.51.100.20", wantPeer: "10.0.0.2"},
		{name: "first untrusted intermediary is the client boundary", remoteAddr: "10.0.0.2:8080", forwarded: []string{"198.51.100.20, 192.0.2.30"}, wantClient: "192.0.2.30", wantPeer: "10.0.0.2"},
		{name: "duplicate headers fail closed", remoteAddr: "10.0.0.2:8080", forwarded: []string{"198.51.100.20", "203.0.113.40"}, wantClient: "10.0.0.2", wantPeer: "10.0.0.2"},
		{name: "invalid chain fails closed", remoteAddr: "10.0.0.2:8080", forwarded: []string{"invalid"}, wantClient: "10.0.0.2", wantPeer: "10.0.0.2"},
		{name: "trusted IPv6 proxy", remoteAddr: "[2001:db8:ffff::2]:8080", forwarded: []string{"2001:db8::20"}, wantClient: "2001:db8::20", wantPeer: "2001:db8:ffff::2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v2/server/config", nil)
			request.RemoteAddr = test.remoteAddr
			for _, value := range test.forwarded {
				request.Header.Add("X-Forwarded-For", value)
			}
			client, peer := nodeRequestAddresses(request, trusted)
			if client != test.wantClient || peer != test.wantPeer {
				t.Fatalf("nodeRequestAddresses()=(%q,%q), want (%q,%q)", client, peer, test.wantClient, test.wantPeer)
			}
		})
	}

	oversized := httptest.NewRequest(http.MethodGet, "/api/v2/server/config", nil)
	oversized.RemoteAddr = "10.0.0.2:8080"
	oversized.Header.Set("X-Forwarded-For", strings.Repeat("1", 4<<10+1))
	if client, peer := nodeRequestAddresses(oversized, trusted); client != "10.0.0.2" || peer != "10.0.0.2" {
		t.Fatalf("oversized forwarded chain=(%q,%q), want trusted peer fallback", client, peer)
	}
}

func TestSECNODE005NodeRateLimitUsesClientPeerAndCredentialBuckets(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	limiter := newNodeRequestLimitGroup(1, 4, 2, trusted)
	now := fixedNow()
	request := func(client, token string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v2/server/config", nil)
		r.RemoteAddr = "10.0.0.2:8080"
		r.Header.Set("X-Forwarded-For", client)
		r.Header.Set("Authorization", "Bearer "+token)
		return r
	}
	if !limiter.allow(request("198.51.100.1", "credential-a"), 1, now) ||
		!limiter.allow(request("198.51.100.2", "credential-b"), 2, now) {
		t.Fatal("independent clients and credentials behind one trusted peer were not allowed")
	}
	if limiter.allow(request("198.51.100.1", "credential-a"), 1, now) {
		t.Fatal("client bucket did not reject the second request")
	}
	if got := limiter.byPeer.entries["10.0.0.2"].count; got != 3 {
		t.Fatalf("peer count=%d, want rejected request to consume every bucket", got)
	}
	credentialKey := nodeCredentialLimitKey(request("198.51.100.1", "credential-a"), 1)
	if got := limiter.byCredential.entries[credentialKey].count; got != 2 {
		t.Fatalf("credential count=%d, want rejected request to consume every bucket", got)
	}

	peerLimited := newNodeRequestLimitGroup(10, 2, 10, trusted)
	if !peerLimited.allow(request("198.51.100.1", "credential-a"), 1, now) ||
		!peerLimited.allow(request("198.51.100.2", "credential-b"), 2, now) ||
		peerLimited.allow(request("198.51.100.3", "credential-c"), 3, now) {
		t.Fatal("direct peer bucket did not enforce its independent limit")
	}

	credentialLimited := newNodeRequestLimitGroup(10, 10, 2, trusted)
	if !credentialLimited.allow(request("198.51.100.1", "credential-a"), 1, now) ||
		!credentialLimited.allow(request("198.51.100.2", "credential-a"), 1, now) ||
		credentialLimited.allow(request("198.51.100.3", "credential-a"), 1, now) {
		t.Fatal("credential bucket did not enforce its independent limit")
	}
}

func paddedNodeSecurityJSON(t *testing.T, base string, size int) string {
	t.Helper()
	if len(base) > size {
		t.Fatalf("base JSON is %d bytes, larger than requested %d-byte body", len(base), size)
	}
	return base + strings.Repeat(" ", size-len(base))
}
