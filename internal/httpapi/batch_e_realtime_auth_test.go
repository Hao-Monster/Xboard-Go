package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/nodecoord"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type batchETestCoordinator struct {
	mu                 sync.Mutex
	instanceID         string
	published          []nodecoord.Event
	releasedNodes      []int64
	failNextReleaseFor map[int64]error
	peerHandler        func(nodecoord.Event)
}

func newBatchETestCoordinator(instanceID string) *batchETestCoordinator {
	return &batchETestCoordinator{
		instanceID:         instanceID,
		failNextReleaseFor: make(map[int64]error),
	}
}

func (c *batchETestCoordinator) InstanceID() string { return c.instanceID }
func (c *batchETestCoordinator) NewConnectionID() (string, error) {
	return "conn-" + uuid.NewString(), nil
}
func (c *batchETestCoordinator) ClaimMachine(context.Context, int64, []int64, string) error {
	return nil
}
func (c *batchETestCoordinator) ClaimNode(context.Context, int64, string) error { return nil }
func (c *batchETestCoordinator) ClaimMachineNodesIfOwned(context.Context, int64, []int64, string) (bool, error) {
	return true, nil
}
func (c *batchETestCoordinator) OwnsMachine(context.Context, int64, string) (bool, error) {
	return true, nil
}
func (c *batchETestCoordinator) OwnsMachineAndNodes(context.Context, int64, []int64, string) (bool, error) {
	return true, nil
}
func (c *batchETestCoordinator) OwnsNode(context.Context, int64, string) (bool, error) {
	return true, nil
}
func (c *batchETestCoordinator) Renew(_ context.Context, leases []nodecoord.Lease) ([]bool, error) {
	out := make([]bool, len(leases))
	for i := range out {
		out[i] = true
	}
	return out, nil
}
func (c *batchETestCoordinator) RenewNodes(_ context.Context, leases []nodecoord.NodeLease) ([]bool, error) {
	out := make([]bool, len(leases))
	for i := range out {
		out[i] = true
	}
	return out, nil
}
func (c *batchETestCoordinator) ReleaseMachineIfOwned(context.Context, int64, string) (bool, error) {
	return true, nil
}
func (c *batchETestCoordinator) ReleaseNodeIfOwned(_ context.Context, nodeID int64, _ string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err, exists := c.failNextReleaseFor[nodeID]; exists {
		delete(c.failNextReleaseFor, nodeID)
		return false, err
	}
	c.releasedNodes = append(c.releasedNodes, nodeID)
	return true, nil
}
func (c *batchETestCoordinator) RevokeMachine(context.Context, int64, string) error { return nil }
func (c *batchETestCoordinator) Publish(_ context.Context, event nodecoord.Event) error {
	c.mu.Lock()
	event.Source = c.instanceID
	c.published = append(c.published, event)
	peer := c.peerHandler
	c.mu.Unlock()
	if peer != nil {
		peer(event)
	}
	return nil
}
func (c *batchETestCoordinator) Start(_ context.Context, handler func(nodecoord.Event)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.peerHandler = handler
	return nil
}
func (c *batchETestCoordinator) Close() error { return nil }

func (c *batchETestCoordinator) PublishedEvents() []nodecoord.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]nodecoord.Event(nil), c.published...)
}

func (c *batchETestCoordinator) ReleasedNodes() []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int64(nil), c.releasedNodes...)
}

func TestBL13SaveNodeRuntimePublishesNodeFullAcrossReplicas(t *testing.T) {
	database := cloneHTTPAPITestDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	now := fixedNow()

	coordA := newBatchETestCoordinator("replica-a")
	apiA := New(Dependencies{
		Context:          ctx,
		Store:            database,
		PasswordHasher:   newHTTPAPITestPasswordHasher(),
		Now:              fixedNow,
		PanelURL:         "https://panel.example.test",
		AllowedOrigins:   []string{"https://panel.example.test"},
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		WebSocketEnabled: true,
		NodeCoordinator:  coordA,
	})
	defer func() {
		cancel()
		waitForHTTPAPIShutdown(t, apiA, 3*time.Second)
	}()

	machine, _, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "bl13-machine", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	nodeOnMachine, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "bl13-machine-node", Type: "vless", Host: "bl13-m.example.test", Port: "443",
		Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	standaloneNode, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "bl13-standalone-node", Type: "vless", Host: "bl13-s.example.test", Port: "8443",
		Show: true, Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	admin := loginAdmin(t, apiA)

	// Case 1: Node is connected to Replica B (so Replica A's local hub.hasNode(nodeID) is false).
	// Updating runtime on Replica A must publish EventNodeFull so Replica B pushes updated config/users.
	for _, targetNode := range []store.Node{nodeOnMachine, standaloneNode} {
		coordA.mu.Lock()
		coordA.published = nil
		coordA.mu.Unlock()

		resp := admin.request(t, apiA, http.MethodPut, fmt.Sprintf("/api/v1/admin/admin/nodes/%d/runtime", targetNode.ID), `{
			"rate": 1.5,
			"group_ids": [7],
			"route_ids": [],
			"config": {"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}
		}`)
		if resp.Code != http.StatusOK {
			t.Fatalf("saveNodeRuntime(%d) status = %d, body = %s", targetNode.ID, resp.Code, resp.Body.String())
		}

		events := coordA.PublishedEvents()
		foundNodeFull := false
		for _, ev := range events {
			if ev.Kind == nodecoord.EventNodeFull && ev.NodeID == targetNode.ID {
				foundNodeFull = true
				break
			}
		}
		if !foundNodeFull {
			t.Fatalf("saveNodeRuntime(%d) on non-owning replica published %#v, want EventNodeFull for node %d",
				targetNode.ID, events, targetNode.ID)
		}
	}
}

func TestBL14NotifyMachineNodesFailedReleaseRetriedOnDisconnect(t *testing.T) {
	database := cloneHTTPAPITestDatabase(t)
	ctx := context.Background()
	now := fixedNow()

	if err := database.EnsureNodeAgentSettings(ctx, store.NodeAgentSettingsDefaults{
		PullInterval: 60, PushInterval: 60, WebSocketEnabled: true,
	}, now); err != nil {
		t.Fatal(err)
	}

	machine, _, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "bl14-machine", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	node1, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "bl14-node-1", Type: "vless", Host: "bl14-1.example.test", Port: "443",
		Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	node2, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "bl14-node-2", Type: "vless", Host: "bl14-2.example.test", Port: "444",
		Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	node3, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "bl14-node-3", Type: "vless", Host: "bl14-3.example.test", Port: "445",
		Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []store.Node{node1, node2, node3} {
		if _, err := database.SaveNodeRuntime(ctx, n.ID, store.SaveNodeRuntimeInput{
			RateMicros: 1_000_000, GroupIDs: []int64{7},
			Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}`),
		}, now); err != nil {
			t.Fatal(err)
		}
	}
	user, err := database.CreateRuntimeUser(ctx, store.CreateRuntimeUserInput{
		Email: "bl14-user@example.test", PasswordHash: "hash",
		UUID: uuid.NewString(), GroupID: 7, TransferEnable: 1_000_000,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	user3, err := database.CreateRuntimeUser(ctx, store.CreateRuntimeUserInput{
		Email: "bl14-user3@example.test", PasswordHash: "hash",
		UUID: uuid.NewString(), GroupID: 7, TransferEnable: 1_000_000,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ApplyNodeReport(ctx, store.NodeReportInput{
		MachineID: machine.ID, NodeID: node2.ID,
		Alive: map[int64][]string{user.ID: {"198.51.100.77"}}, ReplaceAllDevices: true, Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ApplyNodeReport(ctx, store.NodeReportInput{
		MachineID: machine.ID, NodeID: node3.ID,
		Alive: map[int64][]string{user3.ID: {"198.51.100.88"}}, ReplaceAllDevices: true, Now: now,
	}); err != nil {
		t.Fatal(err)
	}

	coord := newBatchETestCoordinator("bl14-instance")
	hub := newWSHub(database, fixedNow, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, coord)

	// Dial a real local websocket pair so conn.close() works cleanly.
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var serverConn *websocket.Conn
	ready := make(chan struct{})
	wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, uErr := upgrader.Upgrade(w, r, nil)
		if uErr != nil {
			return
		}
		serverConn = c
		close(ready)
	}))
	defer wsSrv.Close()
	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+wsSrv.URL[4:], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()
	<-ready
	defer serverConn.Close()

	wsConn := &wsConnection{
		id:        "conn-bl14",
		machineID: machine.ID,
		conn:      serverConn,
		hub:       hub,
		writeCh:   make(chan wsEnvelope, 16),
		done:      make(chan struct{}),
		nodeIDs:   map[int64]struct{}{node1.ID: {}, node2.ID: {}, node3.ID: {}},
	}
	hub.register(wsConn)

	// Unassign both node2 and node3 from machine so notifyMachineNodes treats both as removed nodes.
	for _, removed := range []store.Node{node2, node3} {
		cur, gErr := database.GetNode(ctx, removed.ID)
		if gErr != nil {
			t.Fatal(gErr)
		}
		if uErr := database.UnassignNode(ctx, machine.ID, removed.ID, cur.Revision, now); uErr != nil {
			t.Fatal(uErr)
		}
	}

	// Inject a transient coordinator failure on ReleaseNodeIfOwned for node2 only.
	coord.failNextReleaseFor[node2.ID] = errors.New("redis timeout releasing node2")

	// notifyMachineNodes must still release node3 and clear node3's devices immediately, while recording node2 in pendingReleases and closing wsConn.
	hub.notifyMachineNodes(ctx, machine.ID, false)

	devicesMid, err := database.ListUserDevices(ctx, []int64{user.ID, user3.ID}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(devicesMid[user3.ID]) != 0 {
		t.Fatalf("node3 devices after notifyMachineNodes = %#v, want empty (immediately cleared)", devicesMid[user3.ID])
	}
	if len(devicesMid[user.ID]) != 1 {
		t.Fatalf("node2 devices before disconnect retry = %#v, want still present until confirmed release", devicesMid[user.ID])
	}

	// When the closed connection runs unregisterAndClear on disconnect, it must retry releasing node2 and clear node2's devices!
	current, _, clearErr := hub.unregisterAndClear(wsConn)
	if clearErr != nil {
		t.Fatalf("unregisterAndClear() unexpected error = %v", clearErr)
	}
	if !current {
		t.Fatalf("unregisterAndClear() current = false, want true")
	}

	released := coord.ReleasedNodes()
	foundNode2, foundNode3 := false, false
	for _, id := range released {
		if id == node2.ID {
			foundNode2 = true
		}
		if id == node3.ID {
			foundNode3 = true
		}
	}
	if !foundNode2 || !foundNode3 {
		t.Fatalf("coordinator.ReleasedNodes() = %v, want both %d and %d released", released, node2.ID, node3.ID)
	}

	devices, err := database.ListUserDevices(ctx, []int64{user.ID, user3.ID}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices[user.ID]) != 0 || len(devices[user3.ID]) != 0 {
		t.Fatalf("devices after disconnect = %#v, want empty", devices)
	}

	// Second unregisterAndClear must be a no-op without errors.
	currentAgain, _, clearErrAgain := hub.unregisterAndClear(wsConn)
	if clearErrAgain != nil || currentAgain {
		t.Fatalf("second unregisterAndClear() = (%v, %v), want (false, nil)", currentAgain, clearErrAgain)
	}
}

func TestBL15BearerTokenDoesNotCollideWithCookieSessionID(t *testing.T) {
	database := cloneHTTPAPITestDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	api := New(Dependencies{
		Context:        ctx,
		Store:          database,
		PasswordHasher: newHTTPAPITestPasswordHasher(),
		Now:            fixedNow,
		PanelURL:       "https://panel.example.test",
		AllowedOrigins: []string{"https://panel.example.test"},
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer func() {
		cancel()
		waitForHTTPAPIShutdown(t, api, 3*time.Second)
	}()

	// 1. Login via browser cookie session and find the active cookie session's integer ID.
	adminCookie := loginAdmin(t, api)
	listByCookieResp := adminCookie.request(t, api, http.MethodGet, "/api/v1/auth/sessions", "")
	if listByCookieResp.Code != http.StatusOK {
		t.Fatalf("listAccountSessions(cookie) status = %d", listByCookieResp.Code)
	}
	var cookieSessions struct {
		Data []store.AccountSession `json:"data"`
	}
	if err := json.Unmarshal(listByCookieResp.Body.Bytes(), &cookieSessions); err != nil {
		t.Fatal(err)
	}
	var targetCookieSessionID int64
	for _, s := range cookieSessions.Data {
		if s.IsCurrent {
			targetCookieSessionID = s.ID
			break
		}
	}
	if targetCookieSessionID == 0 {
		t.Fatalf("no current cookie session found in %#v", cookieSessions.Data)
	}

	// 2. Create personal access tokens until access_tokens.id matches targetCookieSessionID (colliding integer ID!).
	var collidingToken string
	for attempt := 0; attempt < 5; attempt++ {
		createTokenResp := adminCookie.request(t, api, http.MethodPost, "/api/v1/auth/access-tokens", fmt.Sprintf(`{"name":"cli-token-%d"}`, attempt))
		if createTokenResp.Code != http.StatusCreated {
			t.Fatalf("createAccessToken status = %d, body = %s", createTokenResp.Code, createTokenResp.Body.String())
		}
		var createdToken struct {
			Data struct {
				ID    int64  `json:"id"`
				Token string `json:"token"`
			} `json:"data"`
		}
		if err := json.Unmarshal(createTokenResp.Body.Bytes(), &createdToken); err != nil {
			t.Fatal(err)
		}
		if createdToken.Data.ID == targetCookieSessionID {
			collidingToken = createdToken.Data.Token
			break
		}
	}
	if collidingToken == "" {
		t.Fatalf("failed to create access token with ID == %d", targetCookieSessionID)
	}

	// 3. Call GET /api/v1/auth/sessions using Bearer token with colliding ID.
	// Because the caller authenticated via an access token (NOT cookie session), all cookie sessions must have is_current=false!
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+collidingToken)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("listAccountSessions(bearer) status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var bearerSessions struct {
		Data []store.AccountSession `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &bearerSessions); err != nil {
		t.Fatal(err)
	}
	for _, s := range bearerSessions.Data {
		if s.IsCurrent {
			t.Fatalf("listAccountSessions(bearer) marked cookie session %d as IsCurrent=true when authenticated via Bearer access token", s.ID)
		}
	}

	// 4. Call DELETE /api/v1/auth/sessions/{id} using Bearer token.
	// Revoking cookie session via Bearer token must NOT emit Set-Cookie clearing cookies on the Bearer response.
	delReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/auth/sessions/%d", targetCookieSessionID), nil)
	delReq.Header.Set("Authorization", "Bearer "+collidingToken)
	delReq.Header.Set("Origin", "https://panel.example.test")
	delRec := httptest.NewRecorder()
	api.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("revokeAccountSession(bearer) status = %d, body = %s", delRec.Code, delRec.Body.String())
	}
	if cookies := delRec.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("revokeAccountSession(bearer) emitted Set-Cookie %#v, want none for Bearer auth", cookies)
	}
}
