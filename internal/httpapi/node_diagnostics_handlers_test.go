package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/diagnostics"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/gorilla/websocket"
)

const testDiagnosticsRuntimeID = "0123456789abcdef0123456789abcdef"

func enrollTestMachine(t *testing.T, api http.Handler, machine store.Machine, enrollment store.EnrollmentSecret) string {
	t.Helper()
	enrollBody := fmt.Sprintf(`{"machine_id":%d,"enrollment_code":%q}`, machine.ID, enrollment.Code)
	enrollRequest := httptest.NewRequest(http.MethodPost, "/api/v1/machines/enroll", strings.NewReader(enrollBody))
	enrollRequest.Header.Set("Content-Type", "application/json")
	enrollResponse := httptest.NewRecorder()
	api.ServeHTTP(enrollResponse, enrollRequest)
	if enrollResponse.Code != http.StatusOK {
		t.Fatalf("enroll status = %d; body=%s", enrollResponse.Code, enrollResponse.Body)
	}
	var enrolled struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(enrollResponse.Body.Bytes(), &enrolled); err != nil {
		t.Fatalf("decode enrolled token: %v", err)
	}
	return enrolled.Data.Token
}

type handshakeDiagnosticsView struct {
	Websocket struct {
		Enabled bool `json:"enabled"`
	} `json:"websocket"`
	Diagnostics *struct {
		Enabled       bool   `json:"enabled"`
		PanelScope    string `json:"panel_scope"`
		SchemaVersion int    `json:"schema_version"`
	} `json:"diagnostics"`
}

func decodeHandshakeDiagnostics(t *testing.T, response *httptest.ResponseRecorder) handshakeDiagnosticsView {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("handshake status=%d body=%s", response.Code, response.Body)
	}
	var view handshakeDiagnosticsView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode handshake: %v body=%s", err, response.Body)
	}
	if view.Diagnostics == nil {
		t.Fatalf("handshake has no diagnostics object: %s", response.Body)
	}
	return view
}

func assertAPIErrorCode(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d want %d body=%s", response.Code, status, response.Body)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Error.Code != code {
		t.Fatalf("error code=%q err=%v want %q body=%s", body.Error.Code, err, code, response.Body)
	}
}

func validCapabilityBody(runtimeID string) string {
	return fmt.Sprintf(`{
		"schema_version": 1,
		"runtime_id": %q,
		"kernel": "singbox",
		"capabilities": ["connection.v1", "dns.v1", "subject_lease.v1", "exception_stack.v1", "dns.v1"]
	}`, runtimeID)
}

// expectNoWSEvent reads until the deadline and fails if the forbidden event arrives.
// The connection is unusable for reads after this returns, so call it last.
func expectNoWSEvent(t *testing.T, connection *websocket.Conn, forbidden string, wait time.Duration) {
	t.Helper()
	_ = connection.SetReadDeadline(time.Now().Add(wait))
	for {
		var event wsIncomingEnvelope
		err := connection.ReadJSON(&event)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return
			}
			t.Fatalf("connection failed while waiting (closed by server?): %v", err)
		}
		if event.Event == forbidden {
			t.Fatalf("unexpected %s event: %s", forbidden, event.Data)
		}
	}
}

func readUntilWSEvent(t *testing.T, connection *websocket.Conn, want string, match func(json.RawMessage) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		event := readWSEvent(t, connection)
		if event.Event == want && (match == nil || match(event.Data)) {
			return
		}
	}
	t.Fatalf("did not receive matching %s event", want)
}

func setupLegacyDiagnosticsNode(t *testing.T, database *store.Store, token string, wsEnabled bool) (store.Node, store.ServerGroup) {
	t.Helper()
	ctx := context.Background()
	now := fixedNow()
	settings, err := database.GetNodeAgentSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateNodeAgentSettings(ctx, store.UpdateNodeAgentSettingsInput{
		Revision: settings.Revision, ServerToken: &token, WebSocketEnabled: wsEnabled,
		PullInterval: 30, PushInterval: 30, DeviceLimitMode: 1,
	}, now); err != nil {
		t.Fatal(err)
	}
	group, err := database.CreateServerGroup(ctx, "diag-group-"+token[len(token)-4:], now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "diag-node", Type: "vless", Host: "diag.example.test", Port: "443", Show: true, Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(ctx, node.ID, store.SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{group.ID},
		Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	return node, group
}

func createDiagnosticsRuntimeUser(t *testing.T, database *store.Store, groupID int64, email, userUUID string) store.RuntimeUser {
	t.Helper()
	user, err := database.CreateRuntimeUser(context.Background(), store.CreateRuntimeUserInput{
		Email: email, PasswordHash: "hashed-pw", UUID: userUUID, GroupID: groupID,
		TransferEnable: 1_000_000, DeviceLimit: 2,
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func TestNodeDiagnosticsUsersSyncWithoutScopeAndWithScope(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	legacyToken := "legacy-sync-token-12345"
	node, group := setupLegacyDiagnosticsNode(t, database, legacyToken, false)
	user := createDiagnosticsRuntimeUser(t, database, group.ID, "user-sync@example.test", "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")

	userPaths := []string{
		fmt.Sprintf("/api/v1/server/UniProxy/user?node_id=%d&node_type=vless", node.ID),
		fmt.Sprintf("/api/v2/server/user?node_id=%d", node.ID),
	}
	handshakePath := fmt.Sprintf("/api/v2/server/handshake?node_id=%d", node.ID)

	// Uninitialized: no diagnostic_id on any pull path; handshake diagnostics disabled without scope.
	for _, path := range userPaths {
		response := agentRequest(api, http.MethodGet, path, legacyToken, "")
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "diagnostic_id") {
			t.Fatalf("uninitialized %s status=%d body=%s", path, response.Code, response.Body)
		}
	}
	before := decodeHandshakeDiagnostics(t, agentRequest(api, http.MethodGet, handshakePath, legacyToken, ""))
	if before.Diagnostics.Enabled || before.Diagnostics.PanelScope != "" || before.Diagnostics.SchemaVersion != 1 {
		t.Fatalf("uninitialized handshake diagnostics = %+v", *before.Diagnostics)
	}

	expectedScope := "00000000-0000-4000-8000-000000000001"
	if _, _, err := database.InitializeDiagnosticsScope(ctx, expectedScope, fixedNow()); err != nil {
		t.Fatalf("init diagnostics scope: %v", err)
	}
	expectedDiagID, err := diagnostics.ComputeDiagnosticID(expectedScope, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range userPaths {
		response := agentRequest(api, http.MethodGet, path, legacyToken, "")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), fmt.Sprintf(`"diagnostic_id":%q`, expectedDiagID)) {
			t.Fatalf("initialized %s status=%d body=%s", path, response.Code, response.Body)
		}
	}
	after := decodeHandshakeDiagnostics(t, agentRequest(api, http.MethodGet, handshakePath, legacyToken, ""))
	if !after.Diagnostics.Enabled || after.Diagnostics.PanelScope != expectedScope {
		t.Fatalf("initialized handshake diagnostics = %+v", *after.Diagnostics)
	}
}

func TestNodeDiagnosticsV1BootstrapCapabilitiesAndControlUnavailable(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	legacyToken := "legacy-test-token-7777"
	node, _ := setupLegacyDiagnosticsNode(t, database, legacyToken, false)
	panelScope := "00000000-0000-4000-8000-000000000002"
	if _, _, err := database.InitializeDiagnosticsScope(ctx, panelScope, fixedNow()); err != nil {
		t.Fatal(err)
	}
	query := fmt.Sprintf("?node_id=%d", node.ID)

	if response := agentRequest(api, http.MethodPost, "/api/v1/server/diagnostics/bootstrap"+query, "wrong-token-000000", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token bootstrap status=%d body=%s", response.Code, response.Body)
	}
	bootstrap := agentRequest(api, http.MethodPost, "/api/v1/server/diagnostics/bootstrap"+query, legacyToken, "")
	var bootstrapBody struct {
		Data nodeDiagnosticsBootstrapResponse `json:"data"`
	}
	if bootstrap.Code != http.StatusOK || json.Unmarshal(bootstrap.Body.Bytes(), &bootstrapBody) != nil {
		t.Fatalf("bootstrap status=%d body=%s", bootstrap.Code, bootstrap.Body)
	}
	if bootstrapBody.Data.NodeID != node.ID || bootstrapBody.Data.PanelScope != panelScope || !bootstrapBody.Data.DiagnosticsEnabled {
		t.Fatalf("bootstrap = %+v", bootstrapBody.Data)
	}

	capabilitiesPath := "/api/v1/server/diagnostics/capabilities" + query
	if response := agentRequest(api, http.MethodPost, capabilitiesPath, legacyToken, validCapabilityBody(testDiagnosticsRuntimeID)); response.Code != http.StatusOK {
		t.Fatalf("capabilities status=%d body=%s", response.Code, response.Body)
	}
	saved, found, err := database.GetNodeDiagnosticsCapability(ctx, node.ID)
	if err != nil || !found {
		t.Fatalf("saved capability found=%v err=%v", found, err)
	}
	if saved.RuntimeID != testDiagnosticsRuntimeID || saved.Kernel != "singbox" || len(saved.Capabilities) != 4 {
		t.Fatalf("saved capability (duplicates must collapse) = %+v", saved)
	}

	for name, body := range map[string]string{
		"bad runtime id":       validCapabilityBody("not-hex"),
		"unsupported schema":   strings.Replace(validCapabilityBody(testDiagnosticsRuntimeID), `"schema_version": 1`, `"schema_version": 2`, 1),
		"missing kernel":       strings.Replace(validCapabilityBody(testDiagnosticsRuntimeID), `"kernel": "singbox",`, ``, 1),
		"invalid capability":   strings.Replace(validCapabilityBody(testDiagnosticsRuntimeID), `"connection.v1"`, `"Connection V1"`, 1),
		"node id mismatch":     strings.Replace(validCapabilityBody(testDiagnosticsRuntimeID), `"schema_version": 1`, fmt.Sprintf(`"node_id": %d, "schema_version": 1`, node.ID+100), 1),
		"too many capabilities": fmt.Sprintf(`{"schema_version":1,"runtime_id":%q,"kernel":"singbox","capabilities":[%s]}`,
			testDiagnosticsRuntimeID, strings.TrimSuffix(strings.Repeat(`"x.v1",`, maxNodeDiagnosticsCapabilities+1), ",")),
	} {
		response := agentRequest(api, http.MethodPost, capabilitiesPath, legacyToken, body)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status=%d body=%s", name, response.Code, response.Body)
		}
	}
	unchanged, _, _ := database.GetNodeDiagnosticsCapability(ctx, node.ID)
	if unchanged.RuntimeID != testDiagnosticsRuntimeID || len(unchanged.Capabilities) != 4 {
		t.Fatalf("rejected reports must not overwrite stored capability: %+v", unchanged)
	}

	// Snapshot and ACK must authenticate first, then refuse explicitly until the lease service exists.
	for _, path := range []string{"/api/v1/server/diagnostics/snapshot", "/api/v1/server/diagnostics/ack"} {
		if response := agentRequest(api, http.MethodPost, path+query, "wrong-token-000000", `{}`); response.Code != http.StatusUnauthorized {
			t.Fatalf("%s wrong token status=%d body=%s", path, response.Code, response.Body)
		}
		assertAPIErrorCode(t, agentRequest(api, http.MethodPost, path+query, legacyToken, `{"request_id":"44444444444444444444444444444444"}`),
			http.StatusServiceUnavailable, "diagnostics_control_unavailable")
	}
}

func TestNodeDiagnosticsV2StrictMachineIDValidation(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()

	machine1, enroll1, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "mach-1", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	token1 := enrollTestMachine(t, api, machine1, enroll1)
	machine2, enroll2, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "mach-2", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	token2 := enrollTestMachine(t, api, machine2, enroll2)

	node1, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "node-1-mach-1", Type: "vless", Host: "n1.example.test", Port: "443", Show: true, Enabled: true, MachineID: &machine1.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(ctx, node1.ID, store.SaveNodeRuntimeInput{
		RateMicros: 1_000_000, Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}`),
	}, now); err != nil {
		t.Fatal(err)
	}

	for _, endpoint := range []string{"bootstrap", "capabilities", "snapshot", "ack"} {
		base := "/api/v2/server/diagnostics/" + endpoint
		// Missing machine_id must be rejected outright, never downgraded to the legacy shared token path.
		assertAPIErrorCode(t, agentRequest(api, http.MethodPost, fmt.Sprintf("%s?node_id=%d", base, node1.ID), token1, `{}`),
			http.StatusUnprocessableEntity, "validation_failed")
		if response := agentRequest(api, http.MethodPost, fmt.Sprintf("%s?machine_id=%d&node_id=%d", base, machine1.ID, node1.ID), token2, `{}`); response.Code != http.StatusUnauthorized {
			t.Fatalf("%s token mismatch status=%d body=%s", endpoint, response.Code, response.Body)
		}
		assertAPIErrorCode(t, agentRequest(api, http.MethodPost, fmt.Sprintf("%s?machine_id=%d&node_id=%d", base, machine2.ID, node1.ID), token2, `{}`),
			http.StatusForbidden, "invalid_machine_node")
	}

	valid := fmt.Sprintf("?machine_id=%d&node_id=%d", machine1.ID, node1.ID)
	if response := agentRequest(api, http.MethodPost, "/api/v2/server/diagnostics/bootstrap"+valid, token1, ""); response.Code != http.StatusOK {
		t.Fatalf("v2 bootstrap status=%d body=%s", response.Code, response.Body)
	}
	if response := agentRequest(api, http.MethodPost, "/api/v2/server/diagnostics/capabilities"+valid, token1, validCapabilityBody(testDiagnosticsRuntimeID)); response.Code != http.StatusOK {
		t.Fatalf("v2 capabilities status=%d body=%s", response.Code, response.Body)
	}
	saved, found, err := database.GetNodeDiagnosticsCapability(ctx, node1.ID)
	if err != nil || !found || saved.RuntimeID != testDiagnosticsRuntimeID {
		t.Fatalf("v2 saved capability=%+v found=%v err=%v", saved, found, err)
	}
	assertAPIErrorCode(t, agentRequest(api, http.MethodPost, "/api/v2/server/diagnostics/snapshot"+valid, token1, `{"request_id":"r"}`),
		http.StatusServiceUnavailable, "diagnostics_control_unavailable")
	assertAPIErrorCode(t, agentRequest(api, http.MethodPost, "/api/v2/server/diagnostics/ack"+valid, token1, `{}`),
		http.StatusServiceUnavailable, "diagnostics_control_unavailable")
}

func TestNodeDiagnosticsWebSocketInteractions(t *testing.T) {
	handler, database, cancel := newWebSocketTestAPI(t)
	defer cancel()
	ctx := context.Background()
	server := httptest.NewServer(handler)
	defer server.Close()

	legacyToken := "legacy-ws-token-8888"
	node, group := setupLegacyDiagnosticsNode(t, database, legacyToken, true)
	scope := "00000000-0000-4000-8000-000000000003"
	if _, _, err := database.InitializeDiagnosticsScope(ctx, scope, fixedNow()); err != nil {
		t.Fatal(err)
	}
	user := createDiagnosticsRuntimeUser(t, database, group.ID, "ws-user@example.test", "cccccccc-dddd-4eee-8fff-000000000001")

	conn := dialLegacyNodeWebSocket(t, server.URL, node.ID, legacyToken)
	defer conn.Close()

	// WS full sync carries diagnostic_id.
	if event := readWSEvent(t, conn); event.Event != "auth.success" {
		t.Fatalf("first event = %s", event.Event)
	}
	if event := readWSEvent(t, conn); event.Event != "sync.config" {
		t.Fatalf("second event = %s", event.Event)
	}
	usersEvent := readWSEvent(t, conn)
	expectedDiagID, _ := diagnostics.ComputeDiagnosticID(scope, user.ID)
	var users struct {
		Users []store.RuntimeUser `json:"users"`
	}
	decodeWSData(t, usersEvent.Data, &users)
	if usersEvent.Event != "sync.users" || len(users.Users) != 1 || users.Users[0].DiagnosticID == nil || *users.Users[0].DiagnosticID != expectedDiagID {
		t.Fatalf("sync.users = %s %s", usersEvent.Event, usersEvent.Data)
	}

	// An invalid capability report is dropped without closing the business connection.
	invalid := wsEnvelope{Event: "diagnostics.capabilities", Data: map[string]any{
		"schema_version": 1, "runtime_id": "bad", "kernel": "singbox", "capabilities": []string{"connection.v1"},
	}}
	if err := conn.WriteJSON(invalid); err != nil {
		t.Fatal(err)
	}
	valid := wsEnvelope{Event: "diagnostics.capabilities", Data: map[string]any{
		"schema_version": 1, "runtime_id": testDiagnosticsRuntimeID, "kernel": "singbox", "capabilities": []string{"connection.v1", "dns.v1"},
	}}
	if err := conn.WriteJSON(valid); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool {
		saved, found, err := database.GetNodeDiagnosticsCapability(ctx, node.ID)
		return err == nil && found && saved.RuntimeID == testDiagnosticsRuntimeID && len(saved.Capabilities) == 2
	})

	// Snapshot requests and ACKs are not answered with fabricated authority before D5,
	// and they must not tear down the connection.
	for _, message := range []wsEnvelope{
		{Event: "request.diagnostics", Data: map[string]any{"request_id": "44444444444444444444444444444444", "runtime_id": testDiagnosticsRuntimeID}},
		{Event: "diagnostics.ack", Data: map[string]any{"request_id": "44444444444444444444444444444444"}},
	} {
		if err := conn.WriteJSON(message); err != nil {
			t.Fatal(err)
		}
	}
	expectNoWSEvent(t, conn, "sync.diagnostics", 500*time.Millisecond)
}

func TestNodeDiagnosticsScopeInitializationResyncsConnectedNodes(t *testing.T) {
	handler, database, cancel := newWebSocketTestAPI(t)
	defer cancel()
	server := httptest.NewServer(handler)
	defer server.Close()

	legacyToken := "legacy-resync-token-4242"
	node, group := setupLegacyDiagnosticsNode(t, database, legacyToken, true)
	user := createDiagnosticsRuntimeUser(t, database, group.ID, "resync-user@example.test", "dddddddd-eeee-4fff-8000-000000000002")

	conn := dialLegacyNodeWebSocket(t, server.URL, node.ID, legacyToken)
	defer conn.Close()
	readWSEvent(t, conn) // auth.success
	readWSEvent(t, conn) // sync.config
	initial := readWSEvent(t, conn)
	if initial.Event != "sync.users" || strings.Contains(string(initial.Data), "diagnostic_id") {
		t.Fatalf("pre-initialization sync.users = %s %s", initial.Event, initial.Data)
	}

	admin := loginAdmin(t, handler)
	response := admin.request(t, handler, http.MethodPost, "/api/v1/admin/admin/diagnostics/initialize", `{}`)
	if response.Code != http.StatusOK {
		t.Fatalf("initialize status=%d body=%s", response.Code, response.Body)
	}
	var initialized struct {
		Data initDiagnosticsScopeResponse `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &initialized); err != nil || !initialized.Data.Created {
		t.Fatalf("initialize body=%s err=%v", response.Body, err)
	}
	expectedDiagID, err := diagnostics.ComputeDiagnosticID(initialized.Data.PanelScope, user.ID)
	if err != nil {
		t.Fatal(err)
	}

	readUntilWSEvent(t, conn, "sync.users", func(data json.RawMessage) bool {
		return strings.Contains(string(data), expectedDiagID)
	})
}

func TestNodeDiagnosticsWebSocketUserDeltaIncludesDiagnosticID(t *testing.T) {
	handler, database, cancel := newWebSocketTestAPI(t)
	defer cancel()
	ctx := context.Background()
	now := fixedNow()
	server := httptest.NewServer(handler)
	defer server.Close()

	scope := "00000000-0000-4000-8000-000000000004"
	if _, _, err := database.InitializeDiagnosticsScope(ctx, scope, now); err != nil {
		t.Fatal(err)
	}
	group, err := database.CreateServerGroup(ctx, "delta-group", now)
	if err != nil {
		t.Fatal(err)
	}
	machine, enrollment, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "delta-mach", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "delta-node", Type: "vless", Host: "delta.example.test", Port: "443", Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(ctx, node.ID, store.SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{group.ID},
		Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	credential, err := database.ExchangeEnrollment(ctx, machine.ID, enrollment.Code, now)
	if err != nil {
		t.Fatal(err)
	}

	admin := loginAdmin(t, handler)
	createdResponse := admin.request(t, handler, http.MethodPost, "/api/v1/admin/admin/users", fmt.Sprintf(`{
		"email":"diag-delta-user@example.test","password":"delta-password-123","group_id":%d,
		"transfer_enable":1000000,"speed_limit":10,"device_limit":2,"banned":false
	}`, group.ID))
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create user status=%d body=%s", createdResponse.Code, createdResponse.Body)
	}
	var created struct {
		Data store.AdminUser `json:"data"`
	}
	decodeResponse(t, createdResponse, &created)

	connection := dialMachineWebSocket(t, server.URL, machine.ID, credential.Token, "")
	defer connection.Close()
	assertInitialMachineSync(t, connection, machine.ID, node.ID, created.Data.ID)

	updateResponse := admin.request(t, handler, http.MethodPatch, fmt.Sprintf("/api/v1/admin/admin/users/%d", created.Data.ID), fmt.Sprintf(`{
		"revision":%d,"email":"diag-delta-user@example.test","group_id":%d,"transfer_enable":2000000,
		"expired_at":null,"speed_limit":20,"device_limit":3,"banned":false
	}`, created.Data.Revision, group.ID))
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update user status=%d body=%s", updateResponse.Code, updateResponse.Body)
	}

	event := readWSEvent(t, connection)
	if event.Event != "sync.user.delta" {
		t.Fatalf("event=%q, want sync.user.delta", event.Event)
	}
	var delta struct {
		NodeID int64               `json:"node_id"`
		Action string              `json:"action"`
		Users  []store.RuntimeUser `json:"users"`
	}
	decodeWSData(t, event.Data, &delta)
	expectedDiagID, err := diagnostics.ComputeDiagnosticID(scope, created.Data.ID)
	if err != nil {
		t.Fatal(err)
	}
	if delta.NodeID != node.ID || delta.Action != "add" || len(delta.Users) != 1 ||
		delta.Users[0].DiagnosticID == nil || *delta.Users[0].DiagnosticID != expectedDiagID {
		t.Fatalf("delta = %#v", delta)
	}
}
