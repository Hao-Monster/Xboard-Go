package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/devicestate"
	"github.com/Hao-Monster/Xboard-Go/internal/nodecoord"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/google/uuid"
)

func TestINTNODE003RedisCoordinatedWebSocketsFenceAcrossInstancesAndRouteNotifications(t *testing.T) {
	redisURL := os.Getenv("XBOARD_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("XBOARD_TEST_REDIS_URL is not configured")
	}
	database := cloneHTTPAPITestDatabase(t)
	prefix := "xboard-go-httpapi-test:" + uuid.NewString() + ":"
	firstCoordinator := newHTTPAPITestCoordinator(t, redisURL, prefix, "first")
	secondCoordinator := newHTTPAPITestCoordinator(t, redisURL, prefix, "second")
	firstAPI, firstCancel := newCoordinatedWebSocketAPI(t, database, firstCoordinator)
	defer firstCancel()
	secondAPI, secondCancel := newCoordinatedWebSocketAPI(t, database, secondCoordinator)
	defer secondCancel()
	firstServer := httptest.NewServer(firstAPI)
	defer firstServer.Close()
	secondServer := httptest.NewServer(secondAPI)
	defer secondServer.Close()

	now := fixedNow()
	machine, enrollment, err := database.CreateMachine(context.Background(), store.CreateMachineInput{Name: "coordinated-machine", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(context.Background(), store.CreateNodeInput{
		Name: "coordinated-node", Type: "vless", Host: "coordinated.example.test", Port: "443",
		Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(context.Background(), node.ID, store.SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{7},
		Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateRuntimeUser(context.Background(), store.CreateRuntimeUserInput{
		Email: "coordinated-user@example.test", PasswordHash: "test-password-hash",
		UUID: "51991333-7581-47d3-ac93-45a4213e9436", GroupID: 7, TransferEnable: 1_000_000,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := database.ExchangeEnrollment(context.Background(), machine.ID, enrollment.Code, now)
	if err != nil {
		t.Fatal(err)
	}

	first := dialMachineWebSocket(t, firstServer.URL, machine.ID, credential.Token, "")
	defer first.Close()
	assertInitialMachineSync(t, first, machine.ID, node.ID, user.ID)
	second := dialMachineWebSocket(t, secondServer.URL, machine.ID, credential.Token, "")
	defer second.Close()
	assertInitialMachineSync(t, second, machine.ID, node.ID, user.ID)
	_ = first.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := first.ReadMessage(); err == nil {
		t.Fatal("connection on the first instance survived the second instance's replacement claim")
	}

	if err := second.WriteJSON(map[string]any{
		"event": "report.devices",
		"data": map[string]any{
			"node_id": node.ID,
			"devices": map[string]any{fmt.Sprint(user.ID): []string{"192.0.2.44"}},
		},
	}); err != nil {
		t.Fatalf("write current device report: %v", err)
	}
	if event := readWSEvent(t, second); event.Event != "sync.devices" {
		t.Fatalf("device report event = %q, want sync.devices", event.Event)
	}
	waitFor(t, 3*time.Second, func() bool {
		devices, err := database.ListUserDevices(context.Background(), []int64{user.ID}, now)
		return err == nil && len(devices[user.ID]) == 1 && devices[user.ID][0] == "192.0.2.44"
	})

	definition, err := database.GetAdminNodeDefinition(context.Background(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	admin := loginAdmin(t, firstAPI)
	createdUser := admin.request(t, firstAPI, http.MethodPost, "/api/v1/admin/users", `{
		"email":"coordinated-created@example.test","password":"coordinated-password-123","group_id":7,
		"transfer_enable":1000000,"speed_limit":10,"device_limit":2,"banned":false
	}`)
	if createdUser.Code != http.StatusCreated {
		t.Fatalf("cross-instance user create status=%d body=%s", createdUser.Code, createdUser.Body)
	}
	var createdUserPayload struct {
		Data store.AdminUser `json:"data"`
	}
	decodeResponse(t, createdUser, &createdUserPayload)
	usersEvent := readWSEvent(t, second)
	if usersEvent.Event != "sync.users" {
		t.Fatalf("cross-instance user create event = %q, want sync.users", usersEvent.Event)
	}
	var usersSnapshot struct {
		NodeID int64               `json:"node_id"`
		Users  []store.RuntimeUser `json:"users"`
	}
	decodeWSData(t, usersEvent.Data, &usersSnapshot)
	if usersSnapshot.NodeID != node.ID || !runtimeUsersContain(usersSnapshot.Users, createdUserPayload.Data.ID) {
		t.Fatalf("cross-instance user snapshot = %#v", usersSnapshot)
	}
	bannedUser := admin.request(t, firstAPI, http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", createdUserPayload.Data.ID), fmt.Sprintf(`{
		"revision":%d,"email":"coordinated-created@example.test","group_id":7,"transfer_enable":1000000,
		"expired_at":null,"speed_limit":10,"device_limit":2,"banned":true
	}`, createdUserPayload.Data.Revision))
	if bannedUser.Code != http.StatusOK {
		t.Fatalf("cross-instance user ban status=%d body=%s", bannedUser.Code, bannedUser.Body)
	}
	usersEvent = readWSEvent(t, second)
	if usersEvent.Event != "sync.users" {
		t.Fatalf("cross-instance user ban event = %q, want sync.users", usersEvent.Event)
	}
	decodeWSData(t, usersEvent.Data, &usersSnapshot)
	if runtimeUsersContain(usersSnapshot.Users, createdUserPayload.Data.ID) {
		t.Fatalf("banned user remained in cross-instance snapshot: %#v", usersSnapshot.Users)
	}
	if devicesEvent := readWSEvent(t, second); devicesEvent.Event != "sync.devices" {
		t.Fatalf("cross-instance user ban device event = %q, want sync.devices", devicesEvent.Event)
	}
	definitionBody, err := json.Marshal(map[string]any{
		"revision": definition.Revision, "type": definition.Type, "external_code": nil,
		"parent_id": definition.ParentID, "name": "coordinated-node-updated",
		"rate": definition.Rate, "tags": definition.Tags,
		"host": definition.Host, "port": definition.Port, "server_port": definition.ServerPort,
		"listen_address": "::", "protocol_settings": definition.ProtocolSettings,
		"show": definition.Show, "enabled": definition.Enabled, "sort": definition.Sort,
		"machine_id": definition.MachineID, "group_ids": definition.GroupIDs, "route_ids": definition.RouteIDs,
		"rate_time_enabled": definition.RateTimeEnabled, "rate_time_ranges": definition.RateTimeRanges,
		"custom_outbounds": definition.CustomOutbounds, "custom_routes": definition.CustomRoutes,
		"certificate_config": definition.CertificateConfig, "transfer_enable": definition.TransferEnable,
	})
	if err != nil {
		t.Fatal(err)
	}
	updated := admin.request(t, firstAPI, http.MethodPut, fmt.Sprintf("/api/v1/admin/nodes/%d", node.ID), string(definitionBody))
	if updated.Code != http.StatusOK {
		t.Fatalf("cross-instance node update status=%d body=%s", updated.Code, updated.Body)
	}
	for _, want := range []string{"sync.nodes", "sync.config", "sync.users", "sync.devices"} {
		_ = second.SetReadDeadline(time.Now().Add(3 * time.Second))
		var event wsEnvelope
		if err := second.ReadJSON(&event); err != nil {
			t.Fatalf("read cross-instance %s event: %v", want, err)
		}
		if event.Event != want {
			t.Fatalf("cross-instance full-sync event = %q, want %q", event.Event, want)
		}
	}

	secondNode, err := database.CreateNode(context.Background(), store.CreateNodeInput{
		Name: "coordinated-node-two", Type: "vless", Host: "coordinated-two.example.test", Port: "8443",
		Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(context.Background(), secondNode.ID, store.SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{7},
		Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":8443}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := firstCoordinator.Publish(context.Background(), nodecoord.Event{
		Kind: nodecoord.EventMachineNodes, MachineID: machine.ID,
	}); err != nil {
		t.Fatalf("publish cross-instance membership notification: %v", err)
	}
	nodesEvent := readWSEvent(t, second)
	if nodesEvent.Event != "sync.nodes" {
		t.Fatalf("membership event = %q, want sync.nodes", nodesEvent.Event)
	}
	for _, want := range []string{"sync.config", "sync.users", "sync.devices"} {
		if event := readWSEvent(t, second); event.Event != want {
			t.Fatalf("new node event = %q, want %q", event.Event, want)
		}
	}

	if err := second.WriteJSON(map[string]any{
		"event": "report.devices",
		"data": map[string]any{
			"node_id": secondNode.ID,
			"devices": map[string]any{fmt.Sprint(user.ID): []string{"192.0.2.45"}},
		},
	}); err != nil {
		t.Fatalf("write report for dynamically claimed node: %v", err)
	}
	foundSecondNode := false
	// Membership notifications and the one-second reconciliation loop may
	// legitimately enqueue an identical full sync while the device report is
	// being processed. Require the authoritative report contents instead of
	// assuming those independent producers have a fixed cross-goroutine order.
	for range 12 {
		event := readWSEvent(t, second)
		if event.Event != "sync.nodes" && event.Event != "sync.config" && event.Event != "sync.users" && event.Event != "sync.devices" {
			t.Fatalf("dynamically claimed node report event = %q, want an authoritative sync event", event.Event)
		}
		if event.Event != "sync.devices" {
			continue
		}
		var data struct {
			NodeID int64              `json:"node_id"`
			Users  map[int64][]string `json:"users"`
		}
		decodeWSData(t, event.Data, &data)
		if data.NodeID != secondNode.ID {
			continue
		}
		for _, address := range data.Users[user.ID] {
			if address == "192.0.2.45" {
				foundSecondNode = true
				break
			}
		}
		if foundSecondNode {
			break
		}
	}
	if !foundSecondNode {
		t.Fatal("dynamically claimed node did not receive the reported device in an authoritative event")
	}
	disabled := admin.request(t, firstAPI, http.MethodPatch, fmt.Sprintf("/api/v1/admin/machines/%d", machine.ID),
		`{"name":"coordinated-machine","notes":"","is_active":false}`)
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable machine status=%d body=%s", disabled.Code, disabled.Body)
	}
	_ = second.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := second.ReadMessage(); err == nil {
		t.Fatal("machine connection survived a cross-instance administrative revocation")
	}
}

func TestINTNODE003RedisCoordinatedLegacyNodeWebSocketsFenceAndRotateAcrossInstances(t *testing.T) {
	redisURL := os.Getenv("XBOARD_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("XBOARD_TEST_REDIS_URL is not configured")
	}
	database := cloneHTTPAPITestDatabase(t)
	prefix := "xboard-go-httpapi-legacy:" + uuid.NewString() + ":"
	firstCoordinator := newHTTPAPITestCoordinator(t, redisURL, prefix, "legacy-first")
	secondCoordinator := newHTTPAPITestCoordinator(t, redisURL, prefix, "legacy-second")
	firstAPI, firstCancel := newCoordinatedWebSocketAPI(t, database, firstCoordinator)
	defer firstCancel()
	secondAPI, secondCancel := newCoordinatedWebSocketAPI(t, database, secondCoordinator)
	defer secondCancel()
	firstServer := httptest.NewServer(firstAPI)
	defer firstServer.Close()
	secondServer := httptest.NewServer(secondAPI)
	defer secondServer.Close()

	ctx := context.Background()
	now := fixedNow()
	settings, err := database.GetNodeAgentSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	token := "redis-legacy-node-token-1234567890"
	settings, err = database.UpdateNodeAgentSettings(ctx, store.UpdateNodeAgentSettingsInput{
		Revision: settings.Revision, ServerToken: &token, PullInterval: 41, PushInterval: 37, WebSocketEnabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "redis-legacy-node", Type: "vless", Host: "redis-legacy.example.test", Port: "443", Show: true, Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(ctx, node.ID, store.SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{7},
		Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateRuntimeUser(ctx, store.CreateRuntimeUserInput{
		Email: "redis-legacy-user@example.test", PasswordHash: "test-password-hash",
		UUID: "8ce2ff89-3970-4c9e-91df-8682581d7da9", GroupID: 7, TransferEnable: 1_000_000,
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	first := dialLegacyNodeWebSocket(t, firstServer.URL, node.ID, token)
	defer first.Close()
	assertInitialLegacySyncWithIntervals(t, first, node.ID, user.ID, 37, 41)
	second := dialLegacyNodeWebSocket(t, secondServer.URL, node.ID, token)
	defer second.Close()
	assertInitialLegacySyncWithIntervals(t, second, node.ID, user.ID, 37, 41)
	_ = first.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := first.ReadMessage(); err == nil {
		t.Fatal("first instance legacy connection survived replacement")
	}

	admin := loginAdmin(t, firstAPI)
	rotated := admin.request(t, firstAPI, http.MethodPut, "/api/v1/admin/node-agent-settings", fmt.Sprintf(`{
		"revision":%d,"generate_server_token":true,"server_pull_interval":41,"server_push_interval":37,
		"device_limit_mode":0,"server_ws_enable":true
	}`, settings.Revision))
	if rotated.Code != http.StatusOK {
		t.Fatalf("rotate coordinated legacy token status=%d body=%s", rotated.Code, rotated.Body)
	}
	_ = second.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := second.ReadMessage(); err == nil {
		t.Fatal("second instance legacy connection survived cross-instance token rotation")
	}
}

func runtimeUsersContain(users []store.RuntimeUser, userID int64) bool {
	for _, user := range users {
		if user.ID == userID {
			return true
		}
	}
	return false
}

func TestINTNODE003RedisCoordinationFailureClosesBeforeWriteAndRecovers(t *testing.T) {
	redisURL := os.Getenv("XBOARD_TEST_REDIS_URL")
	if redisURL == "" || os.Getenv("XBOARD_TEST_REDIS_FAILURES") != "true" {
		t.Skip("destructive Redis failure injection is not enabled")
	}
	database := cloneHTTPAPITestDatabase(t)
	prefix := "xboard-go-httpapi-failure:" + uuid.NewString() + ":"
	proxy, proxiedRedisURL := newRedisFaultProxy(t, redisURL)
	coordinator := newHTTPAPITestCoordinator(t, proxiedRedisURL, prefix, "failure-owner")
	producer := newHTTPAPITestCoordinator(t, redisURL, prefix, "failure-producer")
	api, cancel := newCoordinatedWebSocketAPI(t, database, coordinator)
	defer cancel()
	server := httptest.NewServer(api)
	defer server.Close()

	now := fixedNow()
	machine, enrollment, err := database.CreateMachine(context.Background(), store.CreateMachineInput{Name: "failure-machine", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(context.Background(), store.CreateNodeInput{
		Name: "failure-node", Type: "vless", Host: "failure.example.test", Port: "443",
		Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(context.Background(), node.ID, store.SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{7},
		Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateRuntimeUser(context.Background(), store.CreateRuntimeUserInput{
		Email: "failure-user@example.test", PasswordHash: "test-password-hash",
		UUID: "74164608-6014-4df5-a526-cf53dcb095a6", GroupID: 7, TransferEnable: 1_000_000,
	}, now)
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
	proxy.Block()
	if err := connection.WriteJSON(map[string]any{
		"event": "report.devices",
		"data": map[string]any{
			"node_id": node.ID,
			"devices": map[string]any{fmt.Sprint(user.ID): []string{"192.0.2.99"}},
		},
	}); err != nil {
		t.Fatalf("write report during Redis pause: %v", err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(8 * time.Second))
	if _, _, err := connection.ReadMessage(); err == nil {
		t.Fatal("connection remained open after ownership verification could not reach Redis")
	}
	devices, err := database.ListUserDevices(context.Background(), []int64{user.ID}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices[user.ID]) != 0 {
		t.Fatalf("report was written without Redis ownership proof: %#v", devices[user.ID])
	}

	proxy.Unblock()
	recovered := dialMachineWebSocket(t, server.URL, machine.ID, credential.Token, "")
	defer recovered.Close()
	assertInitialMachineSync(t, recovered, machine.ID, node.ID, user.ID)
	waitFor(t, 5*time.Second, func() bool { return proxy.Active() >= 4 })
	received := make(chan wsEnvelope, 1)
	readErrors := make(chan error, 1)
	go func() {
		var event wsEnvelope
		if err := recovered.ReadJSON(&event); err != nil {
			readErrors <- err
			return
		}
		received <- event
	}()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-received:
			if event.Event != "sync.config" {
				t.Fatalf("post-recovery event = %q, want sync.config", event.Event)
			}
			return
		case err := <-readErrors:
			t.Fatalf("read post-recovery event: %v", err)
		case <-ticker.C:
			if err := producer.Publish(context.Background(), nodecoord.Event{
				Kind: nodecoord.EventNodeConfig, MachineID: machine.ID, NodeID: node.ID,
			}); err != nil {
				t.Fatalf("publish after Redis recovery: %v", err)
			}
		case <-timer.C:
			t.Fatal("timed out waiting for node coordination subscriber recovery")
		}
	}
}

type redisFaultProxy struct {
	listener net.Listener
	target   string
	blocked  atomic.Bool
	mu       sync.Mutex
	active   map[net.Conn]struct{}
}

func newRedisFaultProxy(t *testing.T, rawURL string) (*redisFaultProxy, string) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "redis" || parsed.Host == "" {
		t.Skip("Redis failure injection requires a redis:// integration URL")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for Redis fault proxy: %v", err)
	}
	proxy := &redisFaultProxy{listener: listener, target: parsed.Host, active: make(map[net.Conn]struct{})}
	go proxy.accept()
	t.Cleanup(func() {
		_ = listener.Close()
		proxy.Block()
	})
	parsed.Host = listener.Addr().String()
	return proxy, parsed.String()
}

func (p *redisFaultProxy) accept() {
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}
		if p.blocked.Load() {
			_ = client.Close()
			continue
		}
		upstream, err := net.DialTimeout("tcp", p.target, time.Second)
		if err != nil {
			_ = client.Close()
			continue
		}
		p.track(client, upstream)
		if p.blocked.Load() {
			_ = client.Close()
			_ = upstream.Close()
			continue
		}
		go p.pipe(client, upstream)
		go p.pipe(upstream, client)
	}
}

func (p *redisFaultProxy) pipe(destination, source net.Conn) {
	_, _ = io.Copy(destination, source)
	_ = destination.Close()
	_ = source.Close()
	p.mu.Lock()
	delete(p.active, destination)
	delete(p.active, source)
	p.mu.Unlock()
}

func (p *redisFaultProxy) track(connections ...net.Conn) {
	p.mu.Lock()
	for _, connection := range connections {
		p.active[connection] = struct{}{}
	}
	p.mu.Unlock()
}

func (p *redisFaultProxy) Block() {
	p.blocked.Store(true)
	p.mu.Lock()
	connections := make([]net.Conn, 0, len(p.active))
	for connection := range p.active {
		connections = append(connections, connection)
	}
	p.mu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}
}

func (p *redisFaultProxy) Unblock() { p.blocked.Store(false) }

func (p *redisFaultProxy) Active() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.active)
}

func newHTTPAPITestCoordinator(t *testing.T, redisURL, prefix, revision string) *nodecoord.RedisCoordinator {
	t.Helper()
	coordinator, err := nodecoord.NewRedis(context.Background(), nodecoord.Options{
		URL: redisURL, Prefix: prefix, Revision: revision,
		LeaseDuration: 10 * time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("nodecoord.NewRedis() error = %v", err)
	}
	t.Cleanup(func() {
		if err := coordinator.Close(); err != nil {
			t.Errorf("coordinator.Close() error = %v", err)
		}
	})
	return coordinator
}

func newCoordinatedWebSocketAPI(t *testing.T, database *store.Store, coordinator nodecoord.Coordinator, deviceStates ...devicestate.Service) (http.Handler, context.CancelFunc) {
	t.Helper()
	var deviceState devicestate.Service
	if len(deviceStates) > 0 {
		deviceState = deviceStates[0]
	}
	ctx, cancel := context.WithCancel(context.Background())
	handler := New(Dependencies{
		Context: ctx, Store: database, PasswordHasher: newHTTPAPITestPasswordHasher(), Now: fixedNow,
		PanelURL: "https://panel.example.test", NodeRelease: "v1.14.3",
		AllowedOrigins: []string{"https://panel.example.test"},
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)), WebSocketEnabled: true,
		NodeCoordinator: coordinator, DeviceState: deviceState,
	})
	return handler, cancel
}
