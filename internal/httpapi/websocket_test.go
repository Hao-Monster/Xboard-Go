package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/gorilla/websocket"
)

func TestMachineWebSocketAuthenticatesSyncsAndFencesReplacedConnection(t *testing.T) {
	api, database, cancel := newWebSocketTestAPI(t)
	defer cancel()
	server := httptest.NewServer(api)
	defer server.Close()

	ctx := context.Background()
	now := fixedNow()
	machine, enrollment, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "ws-machine", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "ws-node", Type: "vless", Host: "ws.example.test", Port: "443",
		Show: true, Enabled: true, MachineID: &machine.ID,
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
		Email: "ws-user@example.test", PasswordHash: "test-password-hash",
		UUID: "c90a829e-4421-40e8-8691-9d050a23cc9c", GroupID: 7, TransferEnable: 1_000_000,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := database.ExchangeEnrollment(ctx, machine.ID, enrollment.Code, now)
	if err != nil {
		t.Fatal(err)
	}

	handshakeBody := fmt.Sprintf(`{"machine_id":%d}`, machine.ID)
	handshakeRequest, err := http.NewRequest(http.MethodPost, server.URL+"/api/v2/server/handshake", strings.NewReader(handshakeBody))
	if err != nil {
		t.Fatal(err)
	}
	handshakeRequest.Header.Set("Content-Type", "application/json")
	handshakeRequest.Header.Set("Authorization", "Bearer "+credential.Token)
	handshakeResponse, err := http.DefaultClient.Do(handshakeRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer handshakeResponse.Body.Close()
	var handshake struct {
		WebSocket struct {
			Enabled bool   `json:"enabled"`
			URL     string `json:"ws_url"`
		} `json:"websocket"`
	}
	if err := json.NewDecoder(handshakeResponse.Body).Decode(&handshake); err != nil {
		t.Fatal(err)
	}
	if handshakeResponse.StatusCode != http.StatusOK || !handshake.WebSocket.Enabled || handshake.WebSocket.URL != "ws"+strings.TrimPrefix(server.URL, "http")+"/ws" {
		t.Fatalf("unexpected handshake: status=%d payload=%#v", handshakeResponse.StatusCode, handshake)
	}

	first := dialMachineWebSocket(t, server.URL, machine.ID, credential.Token, "")
	defer first.Close()
	assertInitialMachineSync(t, first, machine.ID, node.ID, user.ID)

	second := dialMachineWebSocket(t, server.URL, machine.ID, credential.Token, "")
	defer second.Close()
	assertInitialMachineSync(t, second, machine.ID, node.ID, user.ID)
	_ = first.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := first.ReadMessage(); err == nil {
		t.Fatal("replaced websocket remained readable")
	}

	if err := second.WriteJSON(map[string]any{
		"event": "report.devices",
		"data": map[string]any{
			"node_id": node.ID,
			"devices": map[string]any{fmt.Sprint(user.ID): []string{"192.0.2.20", "[2001:db8::20]:443"}},
		},
	}); err != nil {
		t.Fatalf("write device report: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		devices, err := database.ListUserDevices(ctx, []int64{user.ID}, now)
		return err == nil && len(devices[user.ID]) == 2
	})
	deviceSync := readWSEvent(t, second)
	if deviceSync.Event != "sync.devices" {
		t.Fatalf("device report response event = %q, want sync.devices", deviceSync.Event)
	}
	var deviceSyncData struct {
		NodeID int64              `json:"node_id"`
		Users  map[int64][]string `json:"users"`
	}
	decodeWSData(t, deviceSync.Data, &deviceSyncData)
	if deviceSyncData.NodeID != node.ID || len(deviceSyncData.Users[user.ID]) != 2 {
		t.Fatalf("unexpected device sync: %#v", deviceSyncData)
	}
	if err := second.WriteJSON(map[string]any{
		"event": "report.devices",
		"data":  map[string]any{"node_id": node.ID, "devices": map[string]any{}},
	}); err != nil {
		t.Fatalf("write empty device report: %v", err)
	}
	clearedSync := readWSEvent(t, second)
	if clearedSync.Event != "sync.devices" {
		t.Fatalf("empty device report event = %q, want sync.devices", clearedSync.Event)
	}
	var clearedSyncData struct {
		NodeID int64              `json:"node_id"`
		Users  map[int64][]string `json:"users"`
	}
	decodeWSData(t, clearedSync.Data, &clearedSyncData)
	if clearedSyncData.NodeID != node.ID || len(clearedSyncData.Users[user.ID]) != 0 {
		t.Fatalf("empty device report did not clear snapshot: %#v", clearedSyncData)
	}

	newNode, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "ws-added", Type: "vless", Host: "added.example.test", Port: "8443", Show: true, Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(ctx, newNode.ID, store.SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{7},
		Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":8443}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	admin := loginAdmin(t, api)
	assigned := admin.request(t, api, http.MethodPut, fmt.Sprintf("/api/v1/admin/machines/%d/nodes/%d", machine.ID, newNode.ID), `{}`)
	if assigned.Code != http.StatusNoContent {
		t.Fatalf("assign node status = %d; body=%s", assigned.Code, assigned.Body)
	}
	if event := readWSEvent(t, second); event.Event != "sync.nodes" {
		t.Fatalf("first assignment event = %q, want sync.nodes", event.Event)
	}
	if event := readWSEvent(t, second); event.Event != "sync.config" {
		t.Fatalf("second assignment event = %q, want sync.config", event.Event)
	}
	if event := readWSEvent(t, second); event.Event != "sync.users" {
		t.Fatalf("third assignment event = %q, want sync.users", event.Event)
	}
	if event := readWSEvent(t, second); event.Event != "sync.devices" {
		t.Fatalf("fourth assignment event = %q, want sync.devices", event.Event)
	}
	if _, err := database.ApplyNodeReport(ctx, store.NodeReportInput{
		MachineID: machine.ID, NodeID: newNode.ID,
		Alive: map[int64][]string{user.ID: {"192.0.2.30"}}, ReplaceAllDevices: true, Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.SetNodeEnabled(ctx, machine.ID, newNode.ID, false, now); err != nil {
		t.Fatal(err)
	}
	reconciled := readWSEvent(t, second)
	if reconciled.Event != "sync.nodes" {
		t.Fatalf("reconciliation event = %q, want sync.nodes", reconciled.Event)
	}
	var reconciledData struct {
		Nodes []machineNodeSummary `json:"nodes"`
	}
	decodeWSData(t, reconciled.Data, &reconciledData)
	if len(reconciledData.Nodes) != 1 || reconciledData.Nodes[0].ID != node.ID {
		t.Fatalf("reconciled nodes = %#v", reconciledData.Nodes)
	}
	if event := readWSEvent(t, second); event.Event != "sync.devices" {
		t.Fatalf("removed-node cleanup event = %q, want sync.devices", event.Event)
	}
	waitFor(t, 2*time.Second, func() bool {
		devices, err := database.ListUserDevices(ctx, []int64{user.ID}, now)
		return err == nil && len(devices[user.ID]) == 0
	})
	deactivated := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/machines/%d", machine.ID), `{
		"name":"ws-machine","notes":"","is_active":false
	}`)
	if deactivated.Code != http.StatusOK {
		t.Fatalf("deactivate machine status = %d; body=%s", deactivated.Code, deactivated.Body)
	}
	_ = second.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := second.ReadMessage(); err == nil {
		t.Fatal("disabled machine websocket remained readable")
	}
}

func TestRoutingRuleUpdatePushesCompatibleWebSocketConfig(t *testing.T) {
	api, database, cancel := newWebSocketTestAPI(t)
	defer cancel()
	server := httptest.NewServer(api)
	defer server.Close()
	ctx := context.Background()
	now := fixedNow()
	rule, err := database.CreateRoutingRule(ctx, store.SaveRoutingRuleInput{
		Remarks: "initial route", Match: []string{"example.com"}, Action: "direct",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	machine, enrollment, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "route-push-machine", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "route-push-node", Type: "vless", Host: "route-push.example.test", Port: "443", Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(ctx, node.ID, store.SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{7}, RouteIDs: []int64{rule.ID},
		Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateRuntimeUser(ctx, store.CreateRuntimeUserInput{
		Email: "route-push-user@example.test", PasswordHash: "hash", UUID: "89797085-3186-4434-980e-642583be8722", GroupID: 7, TransferEnable: 1,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := database.ExchangeEnrollment(ctx, machine.ID, enrollment.Code, now)
	if err != nil {
		t.Fatal(err)
	}
	connection := dialMachineWebSocket(t, server.URL, machine.ID, credential.Token, "")
	defer connection.Close()
	assertInitialMachineSync(t, connection, machine.ID, node.ID, user.ID)

	admin := loginAdmin(t, api)
	response := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/routing-rules/%d", rule.ID), `{
		"remarks":"updated route","match":["*.example.net"],"action":"proxy","action_value":"warp-out"
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("update route status = %d; body=%s", response.Code, response.Body)
	}
	event := readWSEvent(t, connection)
	if event.Event != "sync.config" {
		t.Fatalf("route update event = %q, want sync.config", event.Event)
	}
	var data struct {
		NodeID int64 `json:"node_id"`
		Config struct {
			Routes []struct {
				ID          int64    `json:"id"`
				Match       []string `json:"match"`
				Action      string   `json:"action"`
				ActionValue string   `json:"action_value"`
			} `json:"routes"`
		} `json:"config"`
	}
	decodeWSData(t, event.Data, &data)
	if data.NodeID != node.ID || len(data.Config.Routes) != 1 || data.Config.Routes[0].ID != rule.ID || data.Config.Routes[0].Action != "proxy" || data.Config.Routes[0].ActionValue != "warp-out" {
		t.Fatalf("pushed route config = %#v", data)
	}
	_ = connection.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, _, err := connection.ReadMessage(); err == nil {
		t.Fatal("routing-only update also pushed an unrelated users or devices snapshot")
	}
}

func TestMachineWebSocketRejectsInvalidCredentialAndOrigin(t *testing.T) {
	api, database, cancel := newWebSocketTestAPI(t)
	defer cancel()
	server := httptest.NewServer(api)
	defer server.Close()
	machine, enrollment, err := database.CreateMachine(context.Background(), store.CreateMachineInput{Name: "ws-security", IsActive: true}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	credential, err := database.ExchangeEnrollment(context.Background(), machine.ID, enrollment.Code, fixedNow())
	if err != nil {
		t.Fatal(err)
	}

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + fmt.Sprintf("/ws?machine_id=%d", machine.ID)
	invalidHeader := http.Header{"Authorization": []string{"Bearer invalid"}}
	if connection, response, err := websocket.DefaultDialer.Dial(wsURL, invalidHeader); err == nil {
		connection.Close()
		t.Fatal("invalid websocket credential was accepted")
	} else if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid credential response = %#v, err=%v", response, err)
	}

	untrustedHeader := http.Header{
		"Authorization": []string{"Bearer " + credential.Token},
		"Origin":        []string{"https://attacker.example.test"},
	}
	if connection, response, err := websocket.DefaultDialer.Dial(wsURL, untrustedHeader); err == nil {
		connection.Close()
		t.Fatal("untrusted websocket origin was accepted")
	} else if response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("untrusted origin response = %#v, err=%v", response, err)
	}

	trusted := dialMachineWebSocket(t, server.URL, machine.ID, credential.Token, "")
	defer trusted.Close()
	if event := readWSEvent(t, trusted); event.Event != "auth.success" {
		t.Fatalf("trusted websocket first event = %q, want auth.success", event.Event)
	}
	rotation, err := database.CreateEnrollment(context.Background(), machine.ID, true, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	rotated := agentRequest(api, http.MethodPost, "/api/v2/server/machine/enroll", "", fmt.Sprintf(`{"machine_id":%d,"enrollment_code":%q}`, machine.ID, rotation.Code))
	if rotated.Code != http.StatusOK {
		t.Fatalf("rotate machine credential status = %d; body=%s", rotated.Code, rotated.Body)
	}
	_ = trusted.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := trusted.ReadMessage(); err == nil {
		t.Fatal("credential rotation left the previous websocket connected")
	}
}

func TestMachineWebSocketRejectsDeviceReportWithoutSnapshot(t *testing.T) {
	api, database, cancel := newWebSocketTestAPI(t)
	defer cancel()
	server := httptest.NewServer(api)
	defer server.Close()

	now := fixedNow()
	machine, node := createWebSocketReportingNode(t, database, now)
	user, err := database.CreateRuntimeUser(context.Background(), store.CreateRuntimeUserInput{
		Email: "missing-snapshot@example.test", PasswordHash: "test-password-hash",
		UUID: "e34bccf1-1940-4f3e-9713-e24a65c2eaa4", GroupID: 7, TransferEnable: 1_000_000,
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
	if err := connection.WriteJSON(map[string]any{"event": "report.devices", "data": map[string]any{"node_id": node.ID}}); err != nil {
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := connection.ReadMessage(); err == nil {
		t.Fatal("device report without devices snapshot was accepted")
	}
}

func TestMachineWebSocketBroadcastsAuthoritativeDevicesAcrossMachines(t *testing.T) {
	api, database, cancel := newWebSocketTestAPI(t)
	defer cancel()
	server := httptest.NewServer(api)
	defer server.Close()

	ctx := context.Background()
	now := fixedNow()
	user, err := database.CreateRuntimeUser(ctx, store.CreateRuntimeUserInput{
		Email: "global-device-user@example.test", PasswordHash: "test-password-hash",
		UUID: "7554b652-8e56-471b-aae2-87bb6f89194e", GroupID: 7, TransferEnable: 1_000_000,
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	type edge struct {
		machine    store.Machine
		node       store.Node
		credential store.MachineCredential
		connection *websocket.Conn
	}
	edges := make([]edge, 2)
	for index := range edges {
		machine, enrollment, err := database.CreateMachine(ctx, store.CreateMachineInput{
			Name: fmt.Sprintf("global-device-machine-%d", index), IsActive: true,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		node, err := database.CreateNode(ctx, store.CreateNodeInput{
			Name: fmt.Sprintf("global-device-node-%d", index), Type: "vless",
			Host: fmt.Sprintf("global-device-%d.example.test", index), Port: fmt.Sprint(8443 + index),
			Show: true, Enabled: true, MachineID: &machine.ID,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.SaveNodeRuntime(ctx, node.ID, store.SaveNodeRuntimeInput{
			RateMicros: 1_000_000, GroupIDs: []int64{7},
			Config: []byte(fmt.Sprintf(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":%d}`, 8443+index)),
		}, now); err != nil {
			t.Fatal(err)
		}
		credential, err := database.ExchangeEnrollment(ctx, machine.ID, enrollment.Code, now)
		if err != nil {
			t.Fatal(err)
		}
		connection := dialMachineWebSocket(t, server.URL, machine.ID, credential.Token, "")
		t.Cleanup(func() { _ = connection.Close() })
		assertInitialMachineSync(t, connection, machine.ID, node.ID, user.ID)
		edges[index] = edge{machine: machine, node: node, credential: credential, connection: connection}
	}

	if err := edges[0].connection.WriteJSON(map[string]any{
		"event": "report.devices",
		"data": map[string]any{
			"node_id": edges[0].node.ID,
			"devices": map[string]any{fmt.Sprint(user.ID): []string{"192.0.2.44"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	for _, current := range edges {
		event := readWSEvent(t, current.connection)
		if event.Event != "sync.devices" {
			t.Fatalf("machine %d event = %q, want sync.devices", current.machine.ID, event.Event)
		}
		var payload struct {
			NodeID int64              `json:"node_id"`
			Users  map[int64][]string `json:"users"`
		}
		decodeWSData(t, event.Data, &payload)
		if payload.NodeID != current.node.ID || len(payload.Users[user.ID]) != 1 || payload.Users[user.ID][0] != "192.0.2.44" {
			t.Fatalf("machine %d device payload = %#v", current.machine.ID, payload)
		}
	}

	if err := edges[0].connection.Close(); err != nil {
		t.Fatal(err)
	}
	cleared := readWSEvent(t, edges[1].connection)
	if cleared.Event != "sync.devices" {
		t.Fatalf("disconnect event = %q, want sync.devices", cleared.Event)
	}
	var clearedPayload struct {
		NodeID int64              `json:"node_id"`
		Users  map[int64][]string `json:"users"`
	}
	decodeWSData(t, cleared.Data, &clearedPayload)
	if clearedPayload.NodeID != edges[1].node.ID || len(clearedPayload.Users[user.ID]) != 0 {
		t.Fatalf("disconnect device payload = %#v, want cleared devices", clearedPayload)
	}
}

func TestAdminUserBanPublishesIncrementalRemoval(t *testing.T) {
	api, database, cancel := newWebSocketTestAPI(t)
	defer cancel()
	server := httptest.NewServer(api)
	defer server.Close()

	ctx := context.Background()
	now := fixedNow()
	machine, enrollment, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "user-delta-machine", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "user-delta-node", Type: "vless", Host: "delta.example.test", Port: "443", Show: true, Enabled: true, MachineID: &machine.ID,
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
	credential, err := database.ExchangeEnrollment(ctx, machine.ID, enrollment.Code, now)
	if err != nil {
		t.Fatal(err)
	}
	admin := loginAdmin(t, api)
	createdResponse := admin.request(t, api, http.MethodPost, "/api/v1/admin/users", `{
		"email":"delta-user@example.test","password":"delta-password-123","group_id":7,
		"transfer_enable":1000000,"speed_limit":10,"device_limit":2,"banned":false
	}`)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create user status = %d body=%s", createdResponse.Code, createdResponse.Body)
	}
	var created struct {
		Data store.AdminUser `json:"data"`
	}
	decodeResponse(t, createdResponse, &created)

	connection := dialMachineWebSocket(t, server.URL, machine.ID, credential.Token, "")
	defer connection.Close()
	assertInitialMachineSync(t, connection, machine.ID, node.ID, created.Data.ID)

	banResponse := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", created.Data.ID), fmt.Sprintf(`{
		"revision":%d,"email":"delta-user@example.test","group_id":7,"transfer_enable":1000000,
		"expired_at":null,"speed_limit":10,"device_limit":2,"banned":true
	}`, created.Data.Revision))
	if banResponse.Code != http.StatusOK {
		t.Fatalf("ban status = %d body=%s", banResponse.Code, banResponse.Body)
	}
	event := readWSEvent(t, connection)
	if event.Event != "sync.user.delta" {
		t.Fatalf("event = %q, want sync.user.delta", event.Event)
	}
	var delta struct {
		NodeID int64               `json:"node_id"`
		Action string              `json:"action"`
		Users  []store.RuntimeUser `json:"users"`
	}
	decodeWSData(t, event.Data, &delta)
	if delta.NodeID != node.ID || delta.Action != "remove" || len(delta.Users) != 1 || delta.Users[0].ID != created.Data.ID || delta.Users[0].UUID == "" {
		t.Fatalf("delta payload = %#v", delta)
	}
	devicesEvent := readWSEvent(t, connection)
	if devicesEvent.Event != "sync.devices" {
		t.Fatalf("event after removal = %q, want sync.devices", devicesEvent.Event)
	}
	var devices struct {
		NodeID int64              `json:"node_id"`
		Users  map[int64][]string `json:"users"`
	}
	decodeWSData(t, devicesEvent.Data, &devices)
	if devices.NodeID != node.ID {
		t.Fatalf("device snapshot = %#v", devices)
	}
	if _, exists := devices.Users[created.Data.ID]; exists {
		t.Fatalf("revoked user remained in device snapshot: %#v", devices.Users)
	}
}

func TestWebSocketUnregisterFencesDeviceCleanupAgainstReconnect(t *testing.T) {
	_, database, cancel := newWebSocketTestAPI(t)
	defer cancel()
	ctx := context.Background()
	now := fixedNow()
	machine, node := createWebSocketReportingNode(t, database, now)
	user, err := database.CreateRuntimeUser(ctx, store.CreateRuntimeUserInput{
		Email: "fencing-user@example.test", PasswordHash: "test-password-hash",
		UUID: "aa7cf90f-3cbd-47ea-a43c-ab4729e5f709", GroupID: 7, TransferEnable: 1_000_000,
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	for iteration := range 100 {
		hub := newWSHub(database, fixedNow, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, 60, 60)
		old := &wsConnection{machineID: machine.ID, hub: hub, nodeIDs: map[int64]struct{}{node.ID: {}}}
		hub.register(old)
		if _, err := database.ApplyNodeReport(ctx, store.NodeReportInput{
			MachineID: machine.ID, NodeID: node.ID, Alive: map[int64][]string{user.ID: {"192.0.2.1"}},
			ReplaceAllDevices: true, Now: now,
		}); err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		var wait sync.WaitGroup
		errorsFound := make(chan error, 2)
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			_, _, err := hub.unregisterAndClear(old)
			if err != nil {
				errorsFound <- err
			}
		}()
		go func() {
			defer wait.Done()
			<-start
			fresh := &wsConnection{machineID: machine.ID, hub: hub, nodeIDs: map[int64]struct{}{node.ID: {}}}
			hub.register(fresh)
			_, err := database.ApplyNodeReport(ctx, store.NodeReportInput{
				MachineID: machine.ID, NodeID: node.ID, Alive: map[int64][]string{user.ID: {"192.0.2.2"}},
				ReplaceAllDevices: true, Now: now,
			})
			if err != nil {
				errorsFound <- err
			}
		}()
		close(start)
		wait.Wait()
		close(errorsFound)
		for err := range errorsFound {
			t.Fatalf("iteration %d error = %v", iteration, err)
		}
		devices, err := database.ListUserDevices(ctx, []int64{user.ID}, now)
		if err != nil || len(devices[user.ID]) != 1 || devices[user.ID][0] != "192.0.2.2" {
			t.Fatalf("iteration %d devices = %#v, err=%v", iteration, devices, err)
		}
	}
}

func newWebSocketTestAPI(t *testing.T) (http.Handler, *store.Store, context.CancelFunc) {
	t.Helper()
	database := cloneHTTPAPITestDatabase(t)
	hasher := newHTTPAPITestPasswordHasher()
	ctx, cancel := context.WithCancel(context.Background())
	handler := New(Dependencies{
		Context: ctx, Store: database, PasswordHasher: hasher, Now: fixedNow,
		PanelURL: "https://panel.example.test", NodeRelease: "v1.14.3",
		AllowedOrigins: []string{"https://panel.example.test"},
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)), WebSocketEnabled: true,
	})
	return handler, database, cancel
}

func createWebSocketReportingNode(t *testing.T, database *store.Store, now time.Time) (store.Machine, store.Node) {
	t.Helper()
	machine, _, err := database.CreateMachine(context.Background(), store.CreateMachineInput{Name: "fencing-machine", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(context.Background(), store.CreateNodeInput{
		Name: "fencing-node", Type: "vless", Host: "fencing.example.test", Port: "443",
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
	return machine, node
}

func dialMachineWebSocket(t *testing.T, serverURL string, machineID int64, token, origin string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + fmt.Sprintf("/ws?machine_id=%d", machineID)
	header := http.Header{"Authorization": []string{"Bearer " + token}}
	if origin != "" {
		header.Set("Origin", origin)
	}
	connection, response, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		if response != nil {
			t.Fatalf("websocket dial status = %d, err=%v", response.StatusCode, err)
		}
		t.Fatalf("websocket dial: %v", err)
	}
	return connection
}

func assertInitialMachineSync(t *testing.T, connection *websocket.Conn, machineID, nodeID, userID int64) {
	t.Helper()
	auth := readWSEvent(t, connection)
	if auth.Event != "auth.success" {
		t.Fatalf("first event = %q, want auth.success", auth.Event)
	}
	var authData struct {
		MachineID int64   `json:"machine_id"`
		NodeIDs   []int64 `json:"node_ids"`
	}
	decodeWSData(t, auth.Data, &authData)
	if authData.MachineID != machineID || len(authData.NodeIDs) != 1 || authData.NodeIDs[0] != nodeID {
		t.Fatalf("unexpected auth data: %#v", authData)
	}
	config := readWSEvent(t, connection)
	users := readWSEvent(t, connection)
	devices := readWSEvent(t, connection)
	if config.Event != "sync.config" || users.Event != "sync.users" || devices.Event != "sync.devices" {
		t.Fatalf("initial sync events = %q/%q/%q", config.Event, users.Event, devices.Event)
	}
	var usersData struct {
		NodeID int64               `json:"node_id"`
		Users  []store.RuntimeUser `json:"users"`
	}
	decodeWSData(t, users.Data, &usersData)
	if usersData.NodeID != nodeID || len(usersData.Users) != 1 || usersData.Users[0].ID != userID {
		t.Fatalf("unexpected users sync: %#v", usersData)
	}
	var devicesData struct {
		NodeID int64              `json:"node_id"`
		Users  map[int64][]string `json:"users"`
	}
	decodeWSData(t, devices.Data, &devicesData)
	if devicesData.NodeID != nodeID || len(devicesData.Users) != 0 {
		t.Fatalf("unexpected initial devices sync: %#v", devicesData)
	}
}

func readWSEvent(t *testing.T, connection *websocket.Conn) wsIncomingEnvelope {
	t.Helper()
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	var event wsIncomingEnvelope
	if err := connection.ReadJSON(&event); err != nil {
		t.Fatalf("read websocket event: %v", err)
	}
	return event
}

func decodeWSData(t *testing.T, data json.RawMessage, output any) {
	t.Helper()
	if err := json.Unmarshal(data, output); err != nil {
		t.Fatalf("decode websocket data: %v; data=%s", err, data)
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not met before timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
