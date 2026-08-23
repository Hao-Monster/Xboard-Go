package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	maxWebSocketMessage = 10 << 20
	webSocketWriteWait  = 10 * time.Second
	webSocketReadWait   = 90 * time.Second
	webSocketPingPeriod = 30 * time.Second
)

type wsEnvelope struct {
	Event     string `json:"event"`
	Data      any    `json:"data,omitempty"`
	Timestamp int64  `json:"timestamp,omitempty"`
}

type wsIncomingEnvelope struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data,omitempty"`
}

type wsNodeSnapshot struct {
	summary   machineNodeSummary
	config    map[string]any
	users     []store.RuntimeUser
	devices   map[int64][]string
	timestamp int64
}

type wsHub struct {
	mu             sync.RWMutex
	machines       map[int64]*wsConnection
	store          *store.Store
	now            func() time.Time
	logger         *slog.Logger
	allowedOrigins map[string]struct{}
	pushInterval   int
	pullInterval   int
	deviceSyncMu   sync.Mutex
}

type wsConnection struct {
	id        string
	machineID int64
	conn      *websocket.Conn
	hub       *wsHub
	writeCh   chan wsEnvelope
	done      chan struct{}
	closeOnce sync.Once
	nodesMu   sync.RWMutex
	nodeIDs   map[int64]struct{}
}

func newWSHub(database *store.Store, now func() time.Time, logger *slog.Logger, allowedOrigins map[string]struct{}, pushInterval, pullInterval int) *wsHub {
	return &wsHub{
		machines:       make(map[int64]*wsConnection),
		store:          database,
		now:            now,
		logger:         logger,
		allowedOrigins: allowedOrigins,
		pushInterval:   pushInterval,
		pullInterval:   pullInterval,
	}
}

func (h *wsHub) runUntil(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			goto shutdown
		case <-ticker.C:
			h.reconcileConnections(ctx)
		}
	}

shutdown:
	h.mu.Lock()
	connections := make([]*wsConnection, 0, len(h.machines))
	for _, connection := range h.machines {
		connections = append(connections, connection)
	}
	h.machines = make(map[int64]*wsConnection)
	h.mu.Unlock()
	for _, connection := range connections {
		connection.close(websocket.CloseGoingAway, "server shutdown")
	}
}

func (h *wsHub) reconcileConnections(ctx context.Context) {
	h.mu.RLock()
	machineIDs := make([]int64, 0, len(h.machines))
	for machineID := range h.machines {
		machineIDs = append(machineIDs, machineID)
	}
	h.mu.RUnlock()
	for _, machineID := range machineIDs {
		nodes, err := h.store.ListMachineNodes(ctx, machineID)
		if err != nil {
			h.logger.Warn("reconcile websocket nodes", "machine_id", machineID, "error", err)
			continue
		}
		nodeIDs := make([]int64, 0, len(nodes))
		for _, node := range nodes {
			if node.Enabled && node.RuntimeConfigured {
				nodeIDs = append(nodeIDs, node.ID)
			}
		}
		h.mu.RLock()
		connection := h.machines[machineID]
		h.mu.RUnlock()
		if connection != nil && !sameNodeIDs(connection.nodeIDList(), nodeIDs) {
			h.NotifyMachineNodes(ctx, machineID)
		}
	}
}

func sameNodeIDs(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (s *server) webSocket(w http.ResponseWriter, r *http.Request) {
	if !s.webSocketEnabled || s.hub == nil {
		http.NotFound(w, r)
		return
	}
	machineID, err := strconv.ParseInt(r.URL.Query().Get("machine_id"), 10, 64)
	if err != nil || machineID < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "machine_id 必须是正整数", nil)
		return
	}
	if !s.authenticateMachine(w, r, machineID) {
		return
	}
	if !s.allowServerRequest(w, r, s.handshakeRequests, machineID) {
		return
	}
	snapshots, err := s.hub.machineSnapshots(r.Context(), machineID)
	if err != nil {
		handleStoreError(w, err)
		return
	}

	upgrader := websocket.Upgrader{
		HandshakeTimeout: 15 * time.Second,
		ReadBufferSize:   4 << 10,
		WriteBufferSize:  4 << 10,
		CheckOrigin:      s.hub.originAllowed,
	}
	connection, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	nodeIDs := make(map[int64]struct{}, len(snapshots))
	for _, snapshot := range snapshots {
		nodeIDs[snapshot.summary.ID] = struct{}{}
	}
	client := &wsConnection{
		id:        uuid.NewString(),
		machineID: machineID,
		conn:      connection,
		hub:       s.hub,
		writeCh:   make(chan wsEnvelope, max(64, len(snapshots)*3+4)),
		done:      make(chan struct{}),
		nodeIDs:   nodeIDs,
	}
	old := s.hub.register(client)
	if old != nil {
		old.close(websocket.ClosePolicyViolation, "connection replaced")
	}

	go client.writeLoop()
	client.enqueue(initialAuthEnvelope(machineID, snapshots))
	for _, snapshot := range snapshots {
		client.enqueue(configSyncEnvelope(snapshot))
		client.enqueue(usersSyncEnvelope(snapshot))
		client.enqueue(devicesSyncEnvelope(snapshot))
	}
	s.hub.logger.Info("node websocket connected", "machine_id", machineID, "nodes", len(snapshots))
	client.readLoop()
	current, affectedUsers, clearErr := s.hub.unregisterAndClear(client)
	client.close(websocket.CloseNormalClosure, "")
	if clearErr != nil {
		s.hub.logger.Warn("clear disconnected node devices", "machine_id", machineID, "error", clearErr)
	} else if current {
		s.hub.NotifyDeviceStates(context.Background(), affectedUsers)
	}
	s.hub.logger.Info("node websocket disconnected", "machine_id", machineID, "current", current)
}

func (h *wsHub) originAllowed(r *http.Request) bool {
	origin := strings.TrimRight(strings.TrimSpace(r.Header.Get("Origin")), "/")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	_, allowed := h.allowedOrigins[parsed.Scheme+"://"+parsed.Host]
	return allowed
}

func (h *wsHub) machineSnapshots(ctx context.Context, machineID int64) ([]wsNodeSnapshot, error) {
	now := h.now()
	nodes, err := h.store.ListMachineNodes(ctx, machineID)
	if err != nil {
		return nil, err
	}
	snapshots := make([]wsNodeSnapshot, 0, len(nodes))
	for _, node := range nodes {
		if !node.Enabled || !node.RuntimeConfigured {
			continue
		}
		runtime, err := h.store.GetNodeRuntime(ctx, node.ID)
		if err != nil {
			return nil, err
		}
		config, err := nodeConfigObject(runtime, h.pushInterval, h.pullInterval)
		if err != nil {
			return nil, err
		}
		users, err := h.store.ListNodeRuntimeUsers(ctx, node.ID, now)
		if err != nil {
			return nil, err
		}
		userIDs := make([]int64, 0, len(users))
		for _, user := range users {
			userIDs = append(userIDs, user.ID)
		}
		devices, err := h.store.ListUserDevices(ctx, userIDs, now)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, wsNodeSnapshot{
			summary:   machineNodeSummary{ID: node.ID, Type: node.Type, Name: node.Name},
			config:    config,
			users:     users,
			devices:   devices,
			timestamp: now.Unix(),
		})
	}
	return snapshots, nil
}

func (h *wsHub) hasMachineNode(machineID, nodeID int64) bool {
	h.mu.RLock()
	connection := h.machines[machineID]
	h.mu.RUnlock()
	return connection != nil && connection.hasNode(nodeID)
}

func (h *wsHub) register(connection *wsConnection) *wsConnection {
	h.mu.Lock()
	old := h.machines[connection.machineID]
	h.machines[connection.machineID] = connection
	h.mu.Unlock()
	return old
}

func (h *wsHub) unregisterAndClear(connection *wsConnection) (bool, []int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.machines[connection.machineID] != connection {
		return false, nil, nil
	}
	delete(h.machines, connection.machineID)
	// Keep registration fenced while the former owner's device snapshot is
	// cleared. A reconnect cannot publish a new snapshot and then have it
	// removed by this stale close path.
	userIDs, err := h.store.ClearNodeDevices(context.Background(), connection.nodeIDList(), h.now())
	return true, userIDs, err
}

func (h *wsHub) owns(connection *wsConnection) bool {
	h.mu.RLock()
	owned := h.machines[connection.machineID] == connection
	h.mu.RUnlock()
	return owned
}

func (c *wsConnection) readLoop() {
	c.conn.SetReadLimit(maxWebSocketMessage)
	_ = c.conn.SetReadDeadline(time.Now().Add(webSocketReadWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(webSocketReadWait))
	})
	for {
		var message wsIncomingEnvelope
		if err := c.conn.ReadJSON(&message); err != nil {
			return
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(webSocketReadWait))
		if !c.hub.owns(c) {
			return
		}
		if err := c.handleMessage(message); err != nil {
			c.hub.logger.Warn("reject node websocket message", "machine_id", c.machineID, "event", message.Event, "error", err)
			c.close(websocket.ClosePolicyViolation, "invalid message")
			return
		}
	}
}

func (c *wsConnection) handleMessage(message wsIncomingEnvelope) error {
	switch message.Event {
	case "pong":
		return nil
	case "node.status":
		var identity struct {
			NodeID int64 `json:"node_id"`
		}
		if err := json.Unmarshal(message.Data, &identity); err != nil || !c.hasNode(identity.NodeID) {
			return errors.New("node.status has an invalid node_id")
		}
		validated, err := validateNodeReport(nodeReportPayload{Metrics: message.Data})
		if err != nil {
			return err
		}
		validated.MachineID = c.machineID
		validated.NodeID = identity.NodeID
		validated.Now = c.hub.now()
		_, err = c.hub.store.ApplyNodeReport(context.Background(), validated)
		return err
	case "report.devices":
		var payload struct {
			NodeID  int64               `json:"node_id"`
			Devices map[string][]string `json:"devices"`
		}
		if err := json.Unmarshal(message.Data, &payload); err != nil || payload.Devices == nil || !c.hasNode(payload.NodeID) {
			return errors.New("report.devices has an invalid node_id")
		}
		validated, err := validateNodeReport(nodeReportPayload{Alive: payload.Devices})
		if err != nil {
			return err
		}
		validated.MachineID = c.machineID
		validated.NodeID = payload.NodeID
		validated.ReplaceAllDevices = true
		validated.Now = c.hub.now()
		result, applyErr := c.hub.store.ApplyNodeReport(context.Background(), validated)
		if applyErr == nil {
			c.hub.NotifyDeviceStates(context.Background(), result.DeviceUserIDs)
		}
		return applyErr
	default:
		return nil
	}
}

func (c *wsConnection) writeLoop() {
	ticker := time.NewTicker(webSocketPingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case message := <-c.writeCh:
			if err := c.write(message); err != nil {
				c.close(websocket.CloseGoingAway, "write failed")
				return
			}
		case <-ticker.C:
			if err := c.write(wsEnvelope{Event: "ping", Timestamp: c.hub.now().Unix()}); err != nil {
				c.close(websocket.CloseGoingAway, "write failed")
				return
			}
		}
	}
}

func (c *wsConnection) write(message wsEnvelope) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(webSocketWriteWait)); err != nil {
		return err
	}
	return c.conn.WriteJSON(message)
}

func (c *wsConnection) enqueue(message wsEnvelope) bool {
	select {
	case <-c.done:
		return false
	case c.writeCh <- message:
		return true
	default:
		c.close(websocket.CloseTryAgainLater, "outbound queue full")
		return false
	}
}

func (c *wsConnection) close(code int, reason string) {
	c.closeOnce.Do(func() {
		close(c.done)
		deadline := time.Now().Add(webSocketWriteWait)
		_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), deadline)
		_ = c.conn.Close()
	})
}

func (c *wsConnection) hasNode(nodeID int64) bool {
	c.nodesMu.RLock()
	_, exists := c.nodeIDs[nodeID]
	c.nodesMu.RUnlock()
	return exists
}

func (c *wsConnection) replaceNodes(nodeIDs []int64) {
	next := make(map[int64]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		next[nodeID] = struct{}{}
	}
	c.nodesMu.Lock()
	c.nodeIDs = next
	c.nodesMu.Unlock()
}

func (c *wsConnection) nodeIDList() []int64 {
	c.nodesMu.RLock()
	nodeIDs := make([]int64, 0, len(c.nodeIDs))
	for nodeID := range c.nodeIDs {
		nodeIDs = append(nodeIDs, nodeID)
	}
	c.nodesMu.RUnlock()
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })
	return nodeIDs
}

func initialAuthEnvelope(machineID int64, snapshots []wsNodeSnapshot) wsEnvelope {
	nodeIDs := make([]int64, 0, len(snapshots))
	for _, snapshot := range snapshots {
		nodeIDs = append(nodeIDs, snapshot.summary.ID)
	}
	return wsEnvelope{Event: "auth.success", Data: map[string]any{"machine_id": machineID, "node_ids": nodeIDs}}
}

func configSyncEnvelope(snapshot wsNodeSnapshot) wsEnvelope {
	return wsEnvelope{Event: "sync.config", Data: map[string]any{
		"node_id": snapshot.summary.ID, "config": snapshot.config, "timestamp": snapshot.timestamp,
	}}
}

func usersSyncEnvelope(snapshot wsNodeSnapshot) wsEnvelope {
	return wsEnvelope{Event: "sync.users", Data: map[string]any{
		"node_id": snapshot.summary.ID, "users": snapshot.users, "timestamp": snapshot.timestamp,
	}}
}

func devicesSyncEnvelope(snapshot wsNodeSnapshot) wsEnvelope {
	return wsEnvelope{Event: "sync.devices", Data: map[string]any{
		"node_id": snapshot.summary.ID, "users": snapshot.devices, "timestamp": snapshot.timestamp,
	}}
}

func nodesSyncEnvelope(snapshots []wsNodeSnapshot) wsEnvelope {
	nodes := make([]machineNodeSummary, 0, len(snapshots))
	for _, snapshot := range snapshots {
		nodes = append(nodes, snapshot.summary)
	}
	return wsEnvelope{Event: "sync.nodes", Data: map[string]any{"nodes": nodes}}
}

func (h *wsHub) NotifyMachineNodes(ctx context.Context, machineID int64) {
	snapshots, err := h.machineSnapshots(ctx, machineID)
	if err != nil {
		h.logger.Warn("prepare websocket node synchronization", "machine_id", machineID, "error", err)
		return
	}
	h.mu.RLock()
	connection := h.machines[machineID]
	h.mu.RUnlock()
	if connection == nil {
		return
	}
	previousNodes := make(map[int64]struct{})
	for _, nodeID := range connection.nodeIDList() {
		previousNodes[nodeID] = struct{}{}
	}
	removedNodes := make(map[int64]struct{}, len(previousNodes))
	for nodeID := range previousNodes {
		removedNodes[nodeID] = struct{}{}
	}
	nodeIDs := make([]int64, 0, len(snapshots))
	for _, snapshot := range snapshots {
		nodeIDs = append(nodeIDs, snapshot.summary.ID)
		delete(removedNodes, snapshot.summary.ID)
	}
	connection.replaceNodes(nodeIDs)
	connection.enqueue(nodesSyncEnvelope(snapshots))
	removedNodeIDs := make([]int64, 0, len(removedNodes))
	for nodeID := range removedNodes {
		removedNodeIDs = append(removedNodeIDs, nodeID)
	}
	if len(removedNodeIDs) > 0 {
		h.ClearNodeDevices(ctx, removedNodeIDs)
	}
	for _, snapshot := range snapshots {
		if !connection.hasNode(snapshot.summary.ID) {
			continue
		}
		if _, existed := previousNodes[snapshot.summary.ID]; !existed {
			connection.enqueue(configSyncEnvelope(snapshot))
			connection.enqueue(usersSyncEnvelope(snapshot))
			connection.enqueue(devicesSyncEnvelope(snapshot))
		}
	}
}

func (h *wsHub) DisconnectMachine(machineID int64, reason string) {
	h.mu.RLock()
	connection := h.machines[machineID]
	h.mu.RUnlock()
	if connection != nil {
		connection.close(websocket.ClosePolicyViolation, reason)
	}
}

func (h *wsHub) ClearNodeDevices(ctx context.Context, nodeIDs []int64) {
	affectedUsers, err := h.store.ClearNodeDevices(ctx, nodeIDs, h.now())
	if err != nil {
		h.logger.Warn("clear removed node devices", "node_ids", nodeIDs, "error", err)
		return
	}
	h.NotifyDeviceStates(ctx, affectedUsers)
}

func (h *wsHub) NotifyNodeFull(ctx context.Context, machineID, nodeID int64) {
	snapshots, err := h.machineSnapshots(ctx, machineID)
	if err != nil {
		h.logger.Warn("prepare websocket node full synchronization", "machine_id", machineID, "node_id", nodeID, "error", err)
		return
	}
	h.mu.RLock()
	connection := h.machines[machineID]
	h.mu.RUnlock()
	if connection == nil || !connection.hasNode(nodeID) {
		return
	}
	for _, snapshot := range snapshots {
		if snapshot.summary.ID == nodeID {
			connection.enqueue(configSyncEnvelope(snapshot))
			connection.enqueue(usersSyncEnvelope(snapshot))
			connection.enqueue(devicesSyncEnvelope(snapshot))
			return
		}
	}
}

// NotifyDeviceStates publishes authoritative snapshots only to runtime nodes
// whose groups contain an affected user. The device mutex preserves publish
// order when reports and disconnect cleanup happen concurrently.
func (h *wsHub) NotifyDeviceStates(ctx context.Context, userIDs []int64) {
	if len(userIDs) == 0 {
		return
	}
	h.deviceSyncMu.Lock()
	defer h.deviceSyncMu.Unlock()

	targetNodeIDs, err := h.store.ListRuntimeNodeIDsForUsers(ctx, userIDs)
	if err != nil {
		h.logger.Warn("resolve websocket device synchronization targets", "error", err)
		return
	}
	targets := make(map[int64]struct{}, len(targetNodeIDs))
	for _, nodeID := range targetNodeIDs {
		targets[nodeID] = struct{}{}
	}
	now := h.now()
	h.mu.RLock()
	connections := make(map[int64]*wsConnection, len(h.machines))
	for machineID, connection := range h.machines {
		connections[machineID] = connection
	}
	h.mu.RUnlock()

	for machineID, expected := range connections {
		for _, nodeID := range expected.nodeIDList() {
			if _, targeted := targets[nodeID]; !targeted {
				continue
			}
			node, err := h.store.GetNode(ctx, nodeID)
			if err != nil {
				h.logger.Warn("authorize websocket device synchronization", "machine_id", machineID, "node_id", nodeID, "error", err)
				continue
			}
			if node.MachineID == nil || *node.MachineID != machineID || !node.Enabled || !node.RuntimeConfigured {
				continue
			}
			users, err := h.store.ListNodeRuntimeUsers(ctx, nodeID, now)
			if err != nil {
				h.logger.Warn("list websocket device synchronization users", "machine_id", machineID, "node_id", nodeID, "error", err)
				continue
			}
			nodeUserIDs := make([]int64, 0, len(users))
			for _, user := range users {
				nodeUserIDs = append(nodeUserIDs, user.ID)
			}
			devices, err := h.store.ListUserDevices(ctx, nodeUserIDs, now)
			if err != nil {
				h.logger.Warn("prepare websocket device synchronization", "machine_id", machineID, "node_id", nodeID, "error", err)
				continue
			}
			snapshot := wsNodeSnapshot{summary: machineNodeSummary{ID: nodeID}, devices: devices, timestamp: now.Unix()}
			h.mu.RLock()
			current := h.machines[machineID]
			h.mu.RUnlock()
			if current != expected {
				break
			}
			current.enqueue(devicesSyncEnvelope(snapshot))
		}
	}
}
