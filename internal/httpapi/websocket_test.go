package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestDIFFNODE004MachineWebSocketAuthenticatesSyncsAndFencesReplacedConnection(t *testing.T) {
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
	handshakeRequest.Host = "attacker.example.test"
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
	if handshakeResponse.StatusCode != http.StatusOK || !handshake.WebSocket.Enabled || handshake.WebSocket.URL != "wss://panel.example.test/ws" {
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
	assigned := admin.request(t, api, http.MethodPut, fmt.Sprintf("/api/v1/admin/admin/machines/%d/nodes/%d", machine.ID, newNode.ID), `{"revision":1}`)
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
	assignedNode, err := database.GetNode(ctx, newNode.ID)
	if err != nil {
		t.Fatal(err)
	}
	disableAssignedNode := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/nodes/bulk-state", fmt.Sprintf(
		`{"targets":[{"id":%d,"revision":%d}],"enabled":false}`, newNode.ID, assignedNode.Revision))
	if disableAssignedNode.Code != http.StatusOK {
		t.Fatalf("disable assigned node status=%d body=%s", disableAssignedNode.Code, disableAssignedNode.Body)
	}
	deviceCleanupSeen := false
	nodeReconciliationSeen := false
syncCompleted:
	for range 12 {
		event := readWSEvent(t, second)
		switch event.Event {
		case "sync.devices":
			var data struct {
				NodeID int64              `json:"node_id"`
				Users  map[int64][]string `json:"users"`
			}
			decodeWSData(t, event.Data, &data)
			if data.NodeID == newNode.ID {
				continue
			}
			if data.NodeID != node.ID || len(data.Users[user.ID]) != 0 {
				t.Fatalf("disabled-node device cleanup = %#v", data)
			}
			deviceCleanupSeen = true
		case "sync.nodes":
			nodeIDs := machineNodeSnapshotIDs(t, event)
			switch {
			case reflect.DeepEqual(nodeIDs, []int64{node.ID}):
				nodeReconciliationSeen = true
			case reflect.DeepEqual(nodeIDs, []int64{node.ID, newNode.ID}):
				// The one-second reconciliation loop may have already queued a
				// duplicate assignment snapshot before the disable committed.
			default:
				t.Fatalf("disabled-node snapshot = %v", nodeIDs)
			}
		case "sync.config", "sync.users":
			assertNodeSyncTarget(t, event, newNode.ID)
		default:
			t.Fatalf("disabled-node event = %q", event.Event)
		}
		if deviceCleanupSeen && nodeReconciliationSeen {
			break syncCompleted
		}
	}
	if !deviceCleanupSeen || !nodeReconciliationSeen {
		t.Fatalf("disabled-node sync incomplete: devices=%t nodes=%t", deviceCleanupSeen, nodeReconciliationSeen)
	}
	waitFor(t, 2*time.Second, func() bool {
		devices, err := database.ListUserDevices(ctx, []int64{user.ID}, now)
		return err == nil && len(devices[user.ID]) == 0
	})
	deactivated := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/admin/machines/%d", machine.ID), `{
		"name":"ws-machine","notes":"","is_active":false
	}`)
	if deactivated.Code != http.StatusOK {
		t.Fatalf("deactivate machine status = %d; body=%s", deactivated.Code, deactivated.Body)
	}
	_ = second.SetReadDeadline(time.Now().Add(2 * time.Second))
	closed := false
	for range 12 {
		var event wsIncomingEnvelope
		if err := second.ReadJSON(&event); err != nil {
			closed = true
			break
		}
		assertQueuedMachineReconciliationEvent(t, event, node.ID, newNode.ID)
	}
	if !closed {
		t.Fatal("disabled machine websocket remained readable")
	}
}

func TestDIFFNODE004LegacyWebSocketSyncsFencesAndDisconnectsOnCredentialOrSettingChange(t *testing.T) {
	api, database, cancel := newWebSocketTestAPI(t)
	defer cancel()
	server := httptest.NewServer(api)
	defer server.Close()
	ctx := context.Background()
	now := fixedNow()
	settings, err := database.GetNodeAgentSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	legacyToken := "legacy-websocket-token-1234567890"
	settings, err = database.UpdateNodeAgentSettings(ctx, store.UpdateNodeAgentSettingsInput{
		Revision: settings.Revision, ServerToken: &legacyToken, PullInterval: 31, PushInterval: 29,
		WebSocketEnabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "legacy-ws-node", Type: "vless", Host: "legacy-ws.example.test", Port: "443", Show: true, Enabled: true,
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
		Email: "legacy-ws-user@example.test", PasswordHash: "test-password-hash",
		UUID: "741aec42-0f04-4f16-b68f-3619192091e0", GroupID: 7, TransferEnable: 1_000_000,
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	first := dialLegacyNodeWebSocket(t, server.URL, node.ID, legacyToken)
	defer first.Close()
	assertInitialLegacySync(t, first, node.ID, user.ID)
	second := dialLegacyNodeWebSocket(t, server.URL, node.ID, legacyToken)
	defer second.Close()
	assertInitialLegacySync(t, second, node.ID, user.ID)
	_ = first.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := first.ReadMessage(); err == nil {
		t.Fatal("first legacy connection survived replacement")
	}
	if err := second.WriteJSON(map[string]any{
		"event": "report.devices", "data": map[string]any{fmt.Sprint(user.ID): []string{"192.0.2.71"}},
	}); err != nil {
		t.Fatalf("write legacy device report: %v", err)
	}
	deviceEvent := readWSEvent(t, second)
	if deviceEvent.Event != "sync.devices" {
		t.Fatalf("legacy device report event=%q", deviceEvent.Event)
	}
	devices, err := database.ListUserDevices(ctx, []int64{user.ID}, now)
	if err != nil || len(devices[user.ID]) != 1 || devices[user.ID][0] != "192.0.2.71" {
		t.Fatalf("legacy websocket devices=%#v err=%v", devices, err)
	}

	admin := loginAdmin(t, api)
	definition, err := database.GetAdminNodeDefinition(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	disableNode := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/nodes/bulk-state", fmt.Sprintf(
		`{"targets":[{"id":%d,"revision":%d}],"enabled":false}`, node.ID, definition.Revision))
	if disableNode.Code != http.StatusOK {
		t.Fatalf("disable legacy node status=%d body=%s", disableNode.Code, disableNode.Body)
	}
	_ = second.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := second.ReadMessage(); err == nil {
		t.Fatal("legacy connection survived node disable")
	}
	reenableNode := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/nodes/bulk-state", fmt.Sprintf(
		`{"targets":[{"id":%d,"revision":%d}],"enabled":true}`, node.ID, definition.Revision+1))
	if reenableNode.Code != http.StatusOK {
		t.Fatalf("reenable legacy node status=%d body=%s", reenableNode.Code, reenableNode.Body)
	}
	active := dialLegacyNodeWebSocket(t, server.URL, node.ID, legacyToken)
	defer active.Close()
	assertInitialLegacySync(t, active, node.ID, user.ID)

	rotate := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/node-agent-settings", fmt.Sprintf(`{
		"revision":%d,"generate_server_token":true,"server_pull_interval":31,"server_push_interval":29,
		"device_limit_mode":0,"server_ws_enable":true
	}`, settings.Revision))
	if rotate.Code != http.StatusOK {
		t.Fatalf("rotate token status=%d body=%s", rotate.Code, rotate.Body)
	}
	rotated := decodeNodeAgentSettings(t, rotate.Body.Bytes())
	_ = active.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := active.ReadMessage(); err == nil {
		t.Fatal("legacy connection survived token rotation")
	}
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + fmt.Sprintf("/ws?node_id=%d", node.ID)
	_, response, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Authorization": []string{"Bearer " + legacyToken}})
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token dial response=%v err=%v", response, err)
	}

	current := dialLegacyNodeWebSocket(t, server.URL, node.ID, rotated.IssuedToken)
	defer current.Close()
	assertInitialLegacySync(t, current, node.ID, user.ID)
	disable := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/node-agent-settings", fmt.Sprintf(`{
		"revision":%d,"server_pull_interval":31,"server_push_interval":29,
		"device_limit_mode":0,"server_ws_enable":false
	}`, rotated.Revision))
	if disable.Code != http.StatusOK {
		t.Fatalf("disable websocket status=%d body=%s", disable.Code, disable.Body)
	}
	_ = current.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := current.ReadMessage(); err == nil {
		t.Fatal("legacy connection survived websocket disable")
	}
	_, response, err = websocket.DefaultDialer.Dial(wsURL, http.Header{"Authorization": []string{"Bearer " + rotated.IssuedToken}})
	if err == nil || response == nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled websocket dial response=%v err=%v", response, err)
	}
}

func TestLegacyNodeWebSocketFencesSharedSettingsChangeWithoutCoordinationEvent(t *testing.T) {
	database := cloneHTTPAPITestDatabase(t)
	api, cancel := newCoordinatedWebSocketAPI(t, database, nil)
	defer cancel()
	server := httptest.NewServer(api)
	defer server.Close()
	ctx := context.Background()
	now := fixedNow()
	settings, err := database.GetNodeAgentSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	legacyToken := "legacy-shared-revision-token-1234567890"
	settings, err = database.UpdateNodeAgentSettings(ctx, store.UpdateNodeAgentSettingsInput{
		Revision: settings.Revision, ServerToken: &legacyToken, PullInterval: 31, PushInterval: 29,
		WebSocketEnabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "legacy-shared-revision", Type: "vless", Host: "revision.example.test", Port: "443", Show: true, Enabled: true,
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
		Email: "legacy-shared-revision@example.test", PasswordHash: "hash",
		UUID: "ed45a4e3-01c8-409d-8528-28cc2217aac1", GroupID: 7, TransferEnable: 1_000_000,
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	connection := dialLegacyNodeWebSocket(t, server.URL, node.ID, legacyToken)
	defer connection.Close()
	assertInitialLegacySync(t, connection, node.ID, user.ID)
	rotatedToken := "legacy-shared-revision-rotated-123456"
	if _, err := database.UpdateNodeAgentSettings(ctx, store.UpdateNodeAgentSettingsInput{
		Revision: settings.Revision, ServerToken: &rotatedToken, PullInterval: 31, PushInterval: 29,
		WebSocketEnabled: true,
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(4 * time.Second))
	if _, _, err := connection.ReadMessage(); err == nil {
		t.Fatal("legacy websocket survived a shared settings revision change without a coordination event")
	}
}

func TestAdminNodeDefinitionUpdatePublishesOnlyRequiredMachineSnapshots(t *testing.T) {
	api, database, cancel := newWebSocketTestAPI(t)
	defer cancel()
	server := httptest.NewServer(api)
	defer server.Close()

	ctx := context.Background()
	now := fixedNow()
	group, err := database.CreateServerGroup(ctx, "definition users", now)
	if err != nil {
		t.Fatal(err)
	}
	machine, enrollment, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "definition-machine", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	input := store.SaveAdminNodeDefinitionInput{
		Type: "shadowsocks", Name: "Definition node", RateMicros: 1_000_000, Tags: []string{},
		Host: "definition.example.test", Port: "443", ServerPort: 443, ListenAddress: "0.0.0.0",
		ProtocolSettings: json.RawMessage(`{"cipher":"aes-128-gcm","plugin":"","plugin_opts":""}`),
		Show:             true, Enabled: true, MachineID: &machine.ID, GroupIDs: []int64{group.ID}, RouteIDs: []int64{},
		RateTimeRanges: json.RawMessage(`[]`), CustomOutbounds: json.RawMessage(`[]`), CustomRoutes: json.RawMessage(`[]`), CertificateConfig: json.RawMessage(`{}`),
	}
	created, _, err := database.CreateAdminNodeDefinition(ctx, input, now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateRuntimeUser(ctx, store.CreateRuntimeUserInput{
		Email: "definition-user@example.test", PasswordHash: "hash", UUID: "14a7e77f-2bc7-4ef7-a96c-259f76248989",
		GroupID: group.ID, TransferEnable: 1_000_000,
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
	assertInitialMachineSync(t, connection, machine.ID, created.ID, user.ID)
	admin := loginAdmin(t, api)
	body := func(revision int64, name, cipher string) string {
		payload := map[string]any{
			"revision": revision, "type": "shadowsocks", "external_code": nil, "parent_id": nil,
			"name": name, "rate": 1, "tags": []string{}, "host": "definition.example.test", "port": "443",
			"server_port": 443, "listen_address": "0.0.0.0",
			"protocol_settings": map[string]any{"cipher": cipher, "plugin": "", "plugin_opts": ""},
			"show":              true, "enabled": true, "sort": 0, "machine_id": machine.ID, "group_ids": []int64{group.ID}, "route_ids": []int64{},
			"rate_time_enabled": false, "rate_time_ranges": []any{}, "custom_outbounds": []any{}, "custom_routes": []any{},
			"certificate_config": map[string]any{}, "transfer_enable": 0,
		}
		encoded, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return string(encoded)
	}
	path := fmt.Sprintf("/api/v1/admin/admin/nodes/%d", created.ID)
	response := admin.request(t, api, http.MethodPut, path, body(created.Revision, created.Name, "aes-256-gcm"))
	if response.Code != http.StatusOK {
		t.Fatalf("protocol update status=%d body=%s", response.Code, response.Body)
	}
	config := readWSEvent(t, connection)
	users := readWSEvent(t, connection)
	devices := readWSEvent(t, connection)
	if config.Event != "sync.config" || users.Event != "sync.users" || devices.Event != "sync.devices" {
		t.Fatalf("protocol update events=%q/%q/%q, want config/users/devices", config.Event, users.Event, devices.Event)
	}
	var configData struct {
		Config struct {
			Cipher string `json:"cipher"`
		} `json:"config"`
	}
	decodeWSData(t, config.Data, &configData)
	if configData.Config.Cipher != "aes-256-gcm" {
		t.Fatalf("protocol update cipher=%q", configData.Config.Cipher)
	}

	response = admin.request(t, api, http.MethodPut, path, body(created.Revision+1, "Renamed definition node", "aes-256-gcm"))
	if response.Code != http.StatusOK {
		t.Fatalf("rename status=%d body=%s", response.Code, response.Body)
	}
	for index, expected := range []string{"sync.nodes", "sync.config", "sync.users", "sync.devices"} {
		if event := readWSEvent(t, connection); event.Event != expected {
			t.Fatalf("rename event %d=%q, want %q", index, event.Event, expected)
		}
	}
	_ = connection.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, _, err := connection.ReadMessage(); err == nil {
		t.Fatal("definition updates published an extra machine snapshot")
	}
}

func TestBulkBanPublishesBoundedRuntimeRemovalDelta(t *testing.T) {
	api, database, cancel := newWebSocketTestAPI(t)
	defer cancel()
	server := httptest.NewServer(api)
	defer server.Close()
	ctx := context.Background()
	now := fixedNow()
	machine, enrollment, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "bulk-ban-ws-machine", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "bulk-ban-ws-node", Type: "vless", Host: "bulk-ban.example.test", Port: "443",
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
	target, err := database.CreateRuntimeUser(ctx, store.CreateRuntimeUserInput{
		Email: "bulk-ban-ws-user@example.test", PasswordHash: "hash",
		UUID: "c90a829e-4421-40e8-8691-9d050a23cc9d", GroupID: 7, TransferEnable: 1_000_000,
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
	assertInitialMachineSync(t, connection, machine.ID, node.ID, target.ID)

	admin := loginAdmin(t, api)
	response := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/users/bulk/ban", fmt.Sprintf(
		`{"scope":"selected","user_ids":[%d],"idempotency_key":"u5-ws-bulk-ban-0001"}`, target.ID))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success_count":1`) {
		t.Fatalf("bulk ban status=%d body=%s", response.Code, response.Body)
	}
	delta := readWSEvent(t, connection)
	if delta.Event != "sync.user.delta" {
		t.Fatalf("bulk ban event = %q, want sync.user.delta", delta.Event)
	}
	var data struct {
		NodeID int64               `json:"node_id"`
		Action string              `json:"action"`
		Users  []store.RuntimeUser `json:"users"`
	}
	decodeWSData(t, delta.Data, &data)
	if data.NodeID != node.ID || data.Action != "remove" || len(data.Users) != 1 || data.Users[0].ID != target.ID || data.Users[0].UUID != target.UUID {
		t.Fatalf("bulk removal delta = %#v", data)
	}
	if devices := readWSEvent(t, connection); devices.Event != "sync.devices" {
		t.Fatalf("bulk ban device event = %q, want sync.devices", devices.Event)
	}
}

func TestNODE007TrafficReportPublishesReplayableExceededUserRemoval(t *testing.T) {
	api, database, cancel := newWebSocketTestAPI(t)
	defer cancel()
	server := httptest.NewServer(api)
	defer server.Close()
	ctx := context.Background()
	now := fixedNow()
	machine, node := createWebSocketReportingNode(t, database, now)
	user, err := database.CreateRuntimeUser(ctx, store.CreateRuntimeUserInput{
		Email: "traffic-exceeded-ws-user@example.test", PasswordHash: "hash",
		UUID: "f72c9919-9554-4d89-8f63-5ca869a7e687", GroupID: 7, TransferEnable: 100,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := database.CreateEnrollment(ctx, machine.ID, false, now)
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

	reportBody := fmt.Sprintf(`{
		"machine_id":%d,"node_id":%d,
		"report_id":"2ad55447-6b9b-4df0-a1d1-a74980393796",
		"traffic":{"%d":[40,60]}
	}`, machine.ID, node.ID, user.ID)
	for attempt := 1; attempt <= 2; attempt++ {
		response := agentRequest(api, http.MethodPost, "/api/v2/server/report", credential.Token, reportBody)
		if response.Code != http.StatusOK {
			t.Fatalf("report attempt %d status=%d body=%s", attempt, response.Code, response.Body)
		}
		delta := readWSEvent(t, connection)
		if delta.Event != "sync.user.delta" {
			t.Fatalf("report attempt %d event=%q, want sync.user.delta", attempt, delta.Event)
		}
		var data struct {
			NodeID int64                        `json:"node_id"`
			Action string                       `json:"action"`
			Users  []map[string]json.RawMessage `json:"users"`
		}
		decodeWSData(t, delta.Data, &data)
		var removedID int64
		if len(data.Users) == 1 {
			if rawID, exists := data.Users[0]["id"]; exists {
				if err := json.Unmarshal(rawID, &removedID); err != nil {
					t.Fatal(err)
				}
			}
		}
		if data.NodeID != node.ID || data.Action != "remove" || len(data.Users) != 1 || len(data.Users[0]) != 1 || removedID != user.ID {
			t.Fatalf("report attempt %d removal delta=%#v", attempt, data)
		}
	}
	traffic, err := database.GetRuntimeUserTraffic(ctx, user.ID)
	if err != nil || traffic.Upload != 40 || traffic.Download != 60 {
		t.Fatalf("idempotent exceeded traffic=%#v error=%v", traffic, err)
	}
}

func TestNODE007TrafficExceededRemovalIsBoundedAt501Users(t *testing.T) {
	api, database, cancel := newWebSocketTestAPI(t)
	defer cancel()
	server := httptest.NewServer(api)
	defer server.Close()
	ctx := context.Background()
	now := fixedNow()
	machine, node := createWebSocketReportingNode(t, database, now)
	traffic := make(map[string][2]int64, 501)
	userIDs := make(map[int64]struct{}, 501)
	for index := 0; index < 501; index++ {
		user, err := database.CreateRuntimeUser(ctx, store.CreateRuntimeUserInput{
			Email: fmt.Sprintf("traffic-exceeded-batch-%03d@example.test", index), PasswordHash: "hash",
			UUID: uuid.NewString(), GroupID: 7, TransferEnable: 1,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		traffic[strconv.FormatInt(user.ID, 10)] = [2]int64{1, 0}
		userIDs[user.ID] = struct{}{}
	}
	enrollment, err := database.CreateEnrollment(ctx, machine.ID, false, now)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := database.ExchangeEnrollment(ctx, machine.ID, enrollment.Code, now)
	if err != nil {
		t.Fatal(err)
	}
	connection := dialMachineWebSocket(t, server.URL, machine.ID, credential.Token, "")
	defer connection.Close()
	for _, event := range []string{"auth.success", "sync.config", "sync.users", "sync.devices"} {
		if actual := readWSEvent(t, connection); actual.Event != event {
			t.Fatalf("initial event=%q, want %q", actual.Event, event)
		}
	}
	payload, err := json.Marshal(map[string]any{
		"machine_id": machine.ID, "node_id": node.ID,
		"report_id": "b2c11b72-506a-4619-8996-4b29aaff2399", "traffic": traffic,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := agentRequest(api, http.MethodPost, "/api/v2/server/report", credential.Token, string(payload))
	if response.Code != http.StatusOK {
		t.Fatalf("batched report status=%d body=%s", response.Code, response.Body)
	}
	removed := make(map[int64]struct{}, 501)
	for eventIndex, wantCount := range []int{500, 1} {
		delta := readWSEvent(t, connection)
		var data struct {
			NodeID int64                `json:"node_id"`
			Action string               `json:"action"`
			Users  []removedRuntimeUser `json:"users"`
		}
		decodeWSData(t, delta.Data, &data)
		if delta.Event != "sync.user.delta" || data.NodeID != node.ID || data.Action != "remove" || len(data.Users) != wantCount {
			t.Fatalf("removal event %d=%q %#v, want %d users", eventIndex, delta.Event, data, wantCount)
		}
		for _, user := range data.Users {
			removed[user.ID] = struct{}{}
		}
	}
	if !reflect.DeepEqual(removed, userIDs) {
		t.Fatalf("bounded removal users=%d, want %d exact users", len(removed), len(userIDs))
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
	response := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/admin/routing-rules/%d", rule.ID), `{
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

func TestDIFFNODE004MachineWebSocketRejectsInvalidCredentialOriginAndRevokedCredential(t *testing.T) {
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
	createdResponse := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/users", `{
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

	banResponse := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/admin/users/%d", created.Data.ID), fmt.Sprintf(`{
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
		hub := newWSHub(database, fixedNow, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
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

func dialLegacyNodeWebSocket(t *testing.T, serverURL string, nodeID int64, token string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + fmt.Sprintf("/ws?node_id=%d", nodeID)
	connection, response, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		if response != nil {
			t.Fatalf("legacy websocket dial status=%d err=%v", response.StatusCode, err)
		}
		t.Fatalf("legacy websocket dial: %v", err)
	}
	return connection
}

func assertInitialLegacySync(t *testing.T, connection *websocket.Conn, nodeID, userID int64) {
	assertInitialLegacySyncWithIntervals(t, connection, nodeID, userID, 29, 31)
}

func assertInitialLegacySyncWithIntervals(t *testing.T, connection *websocket.Conn, nodeID, userID int64, pushInterval, pullInterval int) {
	t.Helper()
	auth := readWSEvent(t, connection)
	var authData struct {
		NodeID int64 `json:"node_id"`
	}
	decodeWSData(t, auth.Data, &authData)
	if auth.Event != "auth.success" || authData.NodeID != nodeID {
		t.Fatalf("legacy auth event=%q data=%#v", auth.Event, authData)
	}
	config := readWSEvent(t, connection)
	users := readWSEvent(t, connection)
	if config.Event != "sync.config" || users.Event != "sync.users" {
		t.Fatalf("legacy initial sync=%q/%q", config.Event, users.Event)
	}
	var configData struct {
		NodeID int64 `json:"node_id"`
		Config struct {
			BaseConfig struct {
				PushInterval int `json:"push_interval"`
				PullInterval int `json:"pull_interval"`
			} `json:"base_config"`
		} `json:"config"`
	}
	decodeWSData(t, config.Data, &configData)
	if configData.NodeID != nodeID || configData.Config.BaseConfig.PushInterval != pushInterval || configData.Config.BaseConfig.PullInterval != pullInterval {
		t.Fatalf("legacy config sync=%#v", configData)
	}
	var usersData struct {
		NodeID int64               `json:"node_id"`
		Users  []store.RuntimeUser `json:"users"`
	}
	decodeWSData(t, users.Data, &usersData)
	if usersData.NodeID != nodeID || len(usersData.Users) != 1 || usersData.Users[0].ID != userID {
		t.Fatalf("legacy users sync=%#v", usersData)
	}
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

func machineNodeSnapshotIDs(t *testing.T, event wsIncomingEnvelope) []int64 {
	t.Helper()
	var data struct {
		Nodes []machineNodeSummary `json:"nodes"`
	}
	decodeWSData(t, event.Data, &data)
	nodeIDs := make([]int64, len(data.Nodes))
	for index, node := range data.Nodes {
		nodeIDs[index] = node.ID
	}
	return nodeIDs
}

func assertNodeSyncTarget(t *testing.T, event wsIncomingEnvelope, nodeID int64) {
	t.Helper()
	var data struct {
		NodeID int64 `json:"node_id"`
	}
	decodeWSData(t, event.Data, &data)
	if data.NodeID != nodeID {
		t.Fatalf("%s target = %d, want node %d", event.Event, data.NodeID, nodeID)
	}
}

func assertQueuedMachineReconciliationEvent(t *testing.T, event wsIncomingEnvelope, activeNodeID, staleNodeID int64) {
	t.Helper()
	switch event.Event {
	case "sync.nodes":
		nodeIDs := machineNodeSnapshotIDs(t, event)
		if !reflect.DeepEqual(nodeIDs, []int64{activeNodeID}) &&
			!reflect.DeepEqual(nodeIDs, []int64{activeNodeID, staleNodeID}) {
			t.Fatalf("queued machine node snapshot = %v", nodeIDs)
		}
	case "sync.config", "sync.users", "sync.devices":
		var data struct {
			NodeID int64 `json:"node_id"`
		}
		decodeWSData(t, event.Data, &data)
		if data.NodeID != activeNodeID && data.NodeID != staleNodeID {
			t.Fatalf("queued %s target = %d", event.Event, data.NodeID)
		}
	default:
		t.Fatalf("event queued before machine close = %q", event.Event)
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
