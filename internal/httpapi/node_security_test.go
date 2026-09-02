package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/gorilla/websocket"
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

func TestSECNODE005WebSocketNodeMessageRateBoundary(t *testing.T) {
	api, database, cancel := newWebSocketTestAPI(t)
	defer cancel()
	server := httptest.NewServer(api)
	defer server.Close()

	now := fixedNow()
	machine, node := createWebSocketReportingNode(t, database, now)
	user, err := database.CreateRuntimeUser(context.Background(), store.CreateRuntimeUserInput{
		Email: "node-security-ws@example.test", PasswordHash: "test-password-hash",
		UUID: "0d5f6595-743f-4e10-98a3-dda5f7fbe0f4", GroupID: 7, TransferEnable: 1_000_000,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := database.CreateEnrollment(context.Background(), machine.ID, false, now)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := database.ExchangeEnrollment(context.Background(), machine.ID, enrollment.Code, now)
	if err != nil {
		t.Fatal(err)
	}
	connection := dialMachineWebSocket(t, server.URL, machine.ID, credential.Token, "")
	defer connection.Close()
	assertInitialMachineSync(t, connection, machine.ID, node.ID, user.ID)

	const perNodeMessageLimit = 240
	for sequence := 0; sequence <= perNodeMessageLimit; sequence++ {
		if err := connection.WriteJSON(map[string]any{
			"event": "node.status",
			"data":  map[string]any{"node_id": node.ID, "sequence": sequence},
		}); err != nil {
			if sequence <= perNodeMessageLimit-1 {
				t.Fatalf("accepted message %d write error: %v", sequence, err)
			}
			break
		}
	}
	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := connection.ReadMessage(); !websocket.IsCloseError(err, websocket.ClosePolicyViolation) {
		t.Fatalf("message above per-node limit error=%v, want policy-violation close", err)
	}

	state, err := database.GetNodeRuntimeState(context.Background(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	var metrics struct {
		Sequence int `json:"sequence"`
	}
	if err := json.Unmarshal(state.Metrics, &metrics); err != nil {
		t.Fatalf("decode persisted metrics: %v; metrics=%s", err, state.Metrics)
	}
	if metrics.Sequence != perNodeMessageLimit-1 {
		t.Fatalf("persisted sequence=%d, want rejected message to leave sequence %d", metrics.Sequence, perNodeMessageLimit-1)
	}
}

func TestSECNODE005WebSocketRateBucketsCountUnknownEventsAndScalePerNode(t *testing.T) {
	hub := &wsHub{now: fixedNow}
	connection := &wsConnection{
		hub: hub, nodeIDs: map[int64]struct{}{1: {}, 2: {}},
		messageRequests: newRequestLimiter(10, time.Minute), controlMessageRequests: newRequestLimiter(1, time.Minute),
		nodeMessageRequests: newRequestLimiter(1, time.Minute),
	}
	unknown := wsIncomingEnvelope{Event: "future.compat.event", Data: json.RawMessage(`{"value":1}`)}
	if !connection.allowIncomingMessage(unknown) {
		t.Fatal("first unknown compatibility event was unexpectedly limited")
	}
	nodeStatus := func(nodeID int64) wsIncomingEnvelope {
		return wsIncomingEnvelope{Event: "node.status", Data: json.RawMessage(fmt.Sprintf(`{"node_id":%d}`, nodeID))}
	}
	if !connection.allowIncomingMessage(nodeStatus(1)) || !connection.allowIncomingMessage(nodeStatus(2)) {
		t.Fatal("independent authorized nodes did not receive independent message budgets")
	}
	if connection.allowIncomingMessage(unknown) {
		t.Fatal("unknown event bypassed the exhausted connection control budget")
	}
	if connection.allowIncomingMessage(nodeStatus(1)) {
		t.Fatal("second node-1 write event bypassed its per-node budget")
	}
	if got := connection.messageRequests.entries["connection"].count; got != 5 {
		t.Fatalf("connection count=%d, want unknown and rejected node events to consume the shared budget", got)
	}
	if webSocketEventLogLabel("node.status") != "node.status" || webSocketEventLogLabel(strings.Repeat("attacker", 1_000)) != "unknown" {
		t.Fatal("websocket event logging did not preserve known labels while redacting attacker-controlled values")
	}
}

func TestSECNODE005WebSocketMessageSizeBoundary(t *testing.T) {
	api, database, cancel := newWebSocketTestAPI(t)
	defer cancel()
	server := httptest.NewServer(api)
	defer server.Close()

	now := fixedNow()
	machine, node := createWebSocketReportingNode(t, database, now)
	user, err := database.CreateRuntimeUser(context.Background(), store.CreateRuntimeUserInput{
		Email: "node-security-ws-size@example.test", PasswordHash: "test-password-hash",
		UUID: "6c61bb2d-cd16-416e-bf1b-2f95db2650f5", GroupID: 7, TransferEnable: 1_000_000,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := database.CreateEnrollment(context.Background(), machine.ID, false, now)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := database.ExchangeEnrollment(context.Background(), machine.ID, enrollment.Code, now)
	if err != nil {
		t.Fatal(err)
	}
	connection := dialMachineWebSocket(t, server.URL, machine.ID, credential.Token, "")
	defer connection.Close()
	assertInitialMachineSync(t, connection, machine.ID, node.ID, user.ID)

	const expectedMessageLimit = 10 << 20
	atLimit := paddedWebSocketJSONMessage(t, `{"event":"unknown","data":"`, `"}`, expectedMessageLimit)
	if err := connection.WriteMessage(websocket.TextMessage, atLimit); err != nil {
		t.Fatalf("write message at limit: %v", err)
	}
	if err := connection.WriteJSON(map[string]any{
		"event": "node.status",
		"data":  map[string]any{"node_id": node.ID, "size_boundary": true},
	}); err != nil {
		t.Fatalf("write status after at-limit message: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		state, stateErr := database.GetNodeRuntimeState(context.Background(), node.ID)
		return stateErr == nil && strings.Contains(string(state.Metrics), `"size_boundary":true`)
	})

	oversized := paddedWebSocketJSONMessage(t, `{"event":"unknown","data":"`, `"}`, expectedMessageLimit+1)
	if err := connection.WriteMessage(websocket.TextMessage, oversized); err != nil {
		t.Fatalf("write oversized message: %v", err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := connection.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
		t.Fatalf("oversized message error=%v, want message-too-big close", err)
	}
}

func paddedWebSocketJSONMessage(t *testing.T, prefix, suffix string, size int) []byte {
	t.Helper()
	padding := size - len(prefix) - len(suffix)
	if padding < 0 {
		t.Fatalf("JSON envelope is larger than requested %d-byte message", size)
	}
	return []byte(prefix + strings.Repeat("x", padding) + suffix)
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
		{name: "IPv4-mapped peer is canonicalized", remoteAddr: "[::ffff:192.0.2.10]:443", wantClient: "192.0.2.10", wantPeer: "192.0.2.10"},
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
	overlong := httptest.NewRequest(http.MethodGet, "/api/v2/server/config", nil)
	overlong.RemoteAddr = "10.0.0.2:8080"
	overlong.Header.Set("X-Forwarded-For", strings.Repeat("198.51.100.1,", 32)+"198.51.100.2")
	if client, peer := nodeRequestAddresses(overlong, trusted); client != "10.0.0.2" || peer != "10.0.0.2" {
		t.Fatalf("overlong forwarded hops=(%q,%q), want trusted peer fallback", client, peer)
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
	normalizedCredential := newNodeRequestLimitGroup(10, 10, 1, trusted)
	canonical := request("198.51.100.1", "credential-a")
	variant := request("198.51.100.2", "credential-a")
	variant.Header.Set("Authorization", "  bearer\tcredential-a  ")
	if !normalizedCredential.allow(canonical, 1, now) || normalizedCredential.allow(variant, 1, now) {
		t.Fatal("equivalent Bearer header formatting bypassed the credential bucket")
	}
}

func TestSECNODE005TrustedProxyIsAppliedAtNodeAuthenticationEntry(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	request := func(api http.Handler, remoteAddr, forwarded string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/server/handshake", strings.NewReader(`{"machine_id":999999}`))
		r.RemoteAddr = remoteAddr
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer invalid-machine-credential")
		r.Header.Set("X-Forwarded-For", forwarded)
		response := httptest.NewRecorder()
		api.ServeHTTP(response, r)
		return response
	}

	trustedAPI, _ := newTestAPIWithTrustedProxyPrefixes(t, trusted)
	for attempt := 0; attempt < 60; attempt++ {
		response := request(trustedAPI, "10.0.0.2:8080", "198.51.100.1")
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("trusted client attempt %d status=%d, want %d", attempt+1, response.Code, http.StatusUnauthorized)
		}
	}
	if response := request(trustedAPI, "10.0.0.2:8080", "198.51.100.2"); response.Code != http.StatusUnauthorized {
		t.Fatalf("independent forwarded client status=%d, want %d; body=%s", response.Code, http.StatusUnauthorized, response.Body)
	}

	untrustedAPI, _ := newTestAPIWithTrustedProxyPrefixes(t, trusted)
	for attempt := 0; attempt < 60; attempt++ {
		response := request(untrustedAPI, "192.0.2.10:8080", fmt.Sprintf("198.51.100.%d", attempt+1))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("untrusted peer attempt %d status=%d, want %d", attempt+1, response.Code, http.StatusUnauthorized)
		}
	}
	limited := request(untrustedAPI, "192.0.2.10:8080", "203.0.113.200")
	expectAPIError(t, limited, http.StatusTooManyRequests, "machine_auth_rate_limited")
}

func TestSECNODE005MachineAuthenticationLimitCannotBeBypassedWithMachineIDs(t *testing.T) {
	api, database := newTestAPI(t)
	machine, enrollment, err := database.CreateMachine(context.Background(), store.CreateMachineInput{
		Name: "node-security-auth-machine", IsActive: true,
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	credential, err := database.ExchangeEnrollment(context.Background(), machine.ID, enrollment.Code, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 60; attempt++ {
		response := agentRequest(api, http.MethodPost, "/api/v2/server/handshake", "invalid-machine-credential",
			fmt.Sprintf(`{"machine_id":%d}`, 10_000+attempt))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("invalid machine attempt %d status=%d, want %d; body=%s", attempt+1, response.Code, http.StatusUnauthorized, response.Body)
		}
	}
	limited := agentRequest(api, http.MethodPost, "/api/v2/server/handshake", credential.Token,
		fmt.Sprintf(`{"machine_id":%d}`, machine.ID))
	expectAPIError(t, limited, http.StatusTooManyRequests, "machine_auth_rate_limited")
}

func TestSECNODE005MachineAuthenticationFailuresRetainDirectPeerLimit(t *testing.T) {
	api, _ := newTestAPIWithTrustedProxyPrefixes(t, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	request := func(attempt int) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/server/handshake",
			strings.NewReader(fmt.Sprintf(`{"machine_id":%d}`, 20_000+attempt)))
		r.RemoteAddr = "10.0.0.2:8080"
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.18.%d.%d", attempt/256, attempt%256))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer invalid-machine-credential")
		response := httptest.NewRecorder()
		api.ServeHTTP(response, r)
		return response
	}
	for attempt := 0; attempt < 600; attempt++ {
		if response := request(attempt); response.Code != http.StatusUnauthorized {
			t.Fatalf("peer attempt %d status=%d, want %d; body=%s", attempt+1, response.Code, http.StatusUnauthorized, response.Body)
		}
	}
	expectAPIError(t, request(600), http.StatusTooManyRequests, "machine_auth_rate_limited")
}

func TestSECNODE005RequestLimiterWindowOverflowAndConcurrency(t *testing.T) {
	now := fixedNow()
	window := newRequestLimiter(2, time.Minute)
	if !window.take("client", now) || !window.take("client", now) || window.take("client", now) {
		t.Fatal("fixed-window limiter did not enforce its exact request boundary")
	}
	if window.take("client", now.Add(time.Minute-time.Nanosecond)) || !window.take("client", now.Add(time.Minute)) {
		t.Fatal("fixed-window limiter did not reset at the exact window boundary")
	}

	overflow := newRequestLimiter(2, time.Minute)
	for index := 0; index < 4096; index++ {
		if !overflow.take(fmt.Sprintf("client-%d", index), now) {
			t.Fatalf("tracked client %d was unexpectedly limited", index)
		}
	}
	if !overflow.take("overflow-a", now) || !overflow.take("overflow-b", now) || overflow.take("overflow-c", now) {
		t.Fatal("overflow clients did not share a fail-closed bounded bucket")
	}
	if got := len(overflow.entries); got != 4097 {
		t.Fatalf("tracked limiter entries=%d, want 4096 keys plus one overflow bucket", got)
	}

	concurrent := newRequestLimiter(50, time.Minute)
	var accepted atomic.Int64
	var wait sync.WaitGroup
	for range 100 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if concurrent.take("shared", now) {
				accepted.Add(1)
			}
		}()
	}
	wait.Wait()
	if got := accepted.Load(); got != 50 {
		t.Fatalf("concurrent accepted requests=%d, want 50", got)
	}
}

func TestSECNODE005RuntimeHTTPBodyBoundaries(t *testing.T) {
	api, database := newTestAPI(t)
	now := fixedNow()
	machine, enrollment, err := database.CreateMachine(context.Background(), store.CreateMachineInput{
		Name: "node-security-http-bodies", IsActive: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(context.Background(), store.CreateNodeInput{
		Name: "node-security-http-node", Type: "vless", Host: "security.example.test", Port: "443",
		Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(context.Background(), node.ID, store.SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{7}, Config: []byte(`{"protocol":"vless","server_port":443}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	credential, err := database.ExchangeEnrollment(context.Background(), machine.ID, enrollment.Code, now)
	if err != nil {
		t.Fatal(err)
	}

	const (
		controlBodyLimit = 1 << 20
		reportBodyLimit  = 8 << 20
		statusBodyLimit  = 64 << 10
	)
	status := `{"cpu":1,"mem":{"total":1,"used":0},"swap":{"total":0,"used":0},"disk":{"total":1,"used":0}}`
	cases := []struct {
		name  string
		path  string
		body  string
		limit int
	}{
		{name: "machine nodes", path: "/api/v2/server/machine/nodes", body: fmt.Sprintf(`{"machine_id":%d}`, machine.ID), limit: controlBodyLimit},
		{name: "machine status", path: "/api/v2/server/machine/status", body: fmt.Sprintf(`{"machine_id":%d,"cpu":1,"mem":{"total":1,"used":0}}`, machine.ID), limit: controlBodyLimit},
		{name: "aggregate report", path: "/api/v2/server/report", body: fmt.Sprintf(`{"machine_id":%d,"node_id":%d}`, machine.ID, node.ID), limit: reportBodyLimit},
		{name: "traffic push", path: fmt.Sprintf("/api/v2/server/push?machine_id=%d&node_id=%d", machine.ID, node.ID), body: `{}`, limit: reportBodyLimit},
		{name: "device alive", path: fmt.Sprintf("/api/v2/server/alive?machine_id=%d&node_id=%d", machine.ID, node.ID), body: `{}`, limit: reportBodyLimit},
		{name: "node status", path: fmt.Sprintf("/api/v2/server/status?machine_id=%d&node_id=%d", machine.ID, node.ID), body: status, limit: statusBodyLimit},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			atLimit := agentRequest(api, http.MethodPost, test.path, credential.Token, paddedNodeSecurityJSON(t, test.body, test.limit))
			if atLimit.Code != http.StatusOK {
				t.Fatalf("body at %d-byte limit status=%d, want %d; body=%s", test.limit, atLimit.Code, http.StatusOK, atLimit.Body)
			}
			overLimit := agentRequest(api, http.MethodPost, test.path, credential.Token, paddedNodeSecurityJSON(t, test.body, test.limit+1))
			expectAPIError(t, overLimit, http.StatusRequestEntityTooLarge, "request_too_large")
		})
	}
}

func TestSECNODE005RuntimeRateGroupBoundaries(t *testing.T) {
	type fixture struct {
		api        http.Handler
		machineID  int64
		nodeID     int64
		credential string
	}
	setup := func(t *testing.T) fixture {
		t.Helper()
		api, database := newTestAPI(t)
		now := fixedNow()
		machine, enrollment, err := database.CreateMachine(context.Background(), store.CreateMachineInput{
			Name: "node-security-rate", IsActive: true,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		node, err := database.CreateNode(context.Background(), store.CreateNodeInput{
			Name: "node-security-rate-node", Type: "vless", Host: "rate.example.test", Port: "443",
			Show: true, Enabled: true, MachineID: &machine.ID,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.SaveNodeRuntime(context.Background(), node.ID, store.SaveNodeRuntimeInput{
			RateMicros: 1_000_000, GroupIDs: []int64{7}, Config: []byte(`{"protocol":"vless","server_port":443}`),
		}, now); err != nil {
			t.Fatal(err)
		}
		credential, err := database.ExchangeEnrollment(context.Background(), machine.ID, enrollment.Code, now)
		if err != nil {
			t.Fatal(err)
		}
		return fixture{api: api, machineID: machine.ID, nodeID: node.ID, credential: credential.Token}
	}

	for _, test := range []struct {
		name   string
		limit  int
		method string
		path   func(fixture) string
		body   func(fixture) string
	}{
		{name: "machine", limit: 240, method: http.MethodPost, path: func(fixture) string { return "/api/v2/server/machine/nodes" }, body: func(value fixture) string { return fmt.Sprintf(`{"machine_id":%d}`, value.machineID) }},
		{name: "pull", limit: 600, method: http.MethodGet, path: func(value fixture) string {
			return fmt.Sprintf("/api/v2/server/config?machine_id=%d&node_id=%d", value.machineID, value.nodeID)
		}, body: func(fixture) string { return "" }},
		{name: "report", limit: 240, method: http.MethodPost, path: func(value fixture) string {
			return fmt.Sprintf("/api/v2/server/push?machine_id=%d&node_id=%d", value.machineID, value.nodeID)
		}, body: func(fixture) string { return `{}` }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := setup(t)
			for attempt := 1; attempt <= test.limit; attempt++ {
				response := agentRequest(value.api, test.method, test.path(value), value.credential, test.body(value))
				if response.Code != http.StatusOK {
					t.Fatalf("attempt %d status=%d, want %d; body=%s", attempt, response.Code, http.StatusOK, response.Body)
				}
			}
			limited := agentRequest(value.api, test.method, test.path(value), value.credential, test.body(value))
			expectAPIError(t, limited, http.StatusTooManyRequests, "server_rate_limited")
			if limited.Header().Get("Retry-After") != "60" {
				t.Fatalf("Retry-After=%q, want 60", limited.Header().Get("Retry-After"))
			}
		})
	}
}

func paddedNodeSecurityJSON(t *testing.T, base string, size int) string {
	t.Helper()
	if len(base) > size {
		t.Fatalf("base JSON is %d bytes, larger than requested %d-byte body", len(base), size)
	}
	return base + strings.Repeat(" ", size-len(base))
}

func BenchmarkSECNODE005NodeRequestLimitGroup(b *testing.B) {
	maximum := int(^uint(0) >> 1)
	for _, test := range []struct {
		name      string
		remote    string
		forwarded string
		trusted   []netip.Prefix
	}{
		{name: "direct", remote: "192.0.2.10:8080"},
		{name: "trusted-proxy", remote: "10.0.0.2:8080", forwarded: "198.51.100.1, 10.0.0.3", trusted: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}},
	} {
		b.Run(test.name, func(b *testing.B) {
			request := httptest.NewRequest(http.MethodGet, "/api/v2/server/config", nil)
			request.RemoteAddr = test.remote
			request.Header.Set("Authorization", "Bearer benchmark-credential")
			if test.forwarded != "" {
				request.Header.Set("X-Forwarded-For", test.forwarded)
			}
			limiter := newNodeRequestLimitGroup(maximum, maximum, maximum, test.trusted)
			now := fixedNow()
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(parallel *testing.PB) {
				for parallel.Next() {
					if !limiter.allow(request, 1, now) {
						b.Error("unbounded benchmark limiter rejected a request")
						return
					}
				}
			})
		})
	}
}

func BenchmarkSECNODE005AttemptLimiterFullMap(b *testing.B) {
	now := fixedNow()
	limiter := newAttemptLimiter(100, time.Minute)
	for index := 0; index < 4096; index++ {
		limiter.failed(fmt.Sprintf("client-%d", index), now)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if !limiter.allowed("client-0", now) {
			b.Fatal("benchmark key was unexpectedly limited")
		}
	}
}
