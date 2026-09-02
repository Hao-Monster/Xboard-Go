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

	"github.com/Hao-Monster/Xboard-Go/internal/devicestate"
	"github.com/Hao-Monster/Xboard-Go/internal/nodecoord"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	maxWebSocketMessage               = 10 << 20
	maxWebSocketMessagesPerConnection = 10_000
	maxWebSocketControlMessages       = 240
	maxWebSocketMessagesPerNode       = 240
	webSocketWriteWait                = 10 * time.Second
	webSocketReadWait                 = 90 * time.Second
	webSocketPingPeriod               = 30 * time.Second
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
	nodes          map[int64]*wsConnection
	store          *store.Store
	now            func() time.Time
	logger         *slog.Logger
	allowedOrigins map[string]struct{}
	deviceSyncMu   sync.Mutex
	coordinator    nodecoord.Coordinator
	deviceState    devicestate.Service
	registrationMu [64]sync.Mutex
}

type wsConnection struct {
	id                     string
	machineID              int64
	legacy                 bool
	legacySettingsRevision int64
	conn                   *websocket.Conn
	hub                    *wsHub
	writeCh                chan wsEnvelope
	done                   chan struct{}
	closeOnce              sync.Once
	nodesMu                sync.RWMutex
	nodeIDs                map[int64]struct{}
	messageRequests        *requestLimiter
	controlMessageRequests *requestLimiter
	nodeMessageRequests    *requestLimiter
}

func newWSHub(database *store.Store, now func() time.Time, logger *slog.Logger, allowedOrigins map[string]struct{}, coordinator nodecoord.Coordinator, deviceStates ...devicestate.Service) *wsHub {
	var deviceState devicestate.Service
	if len(deviceStates) > 0 {
		deviceState = deviceStates[0]
	}
	return &wsHub{
		machines:       make(map[int64]*wsConnection),
		nodes:          make(map[int64]*wsConnection),
		store:          database,
		now:            now,
		logger:         logger,
		allowedOrigins: allowedOrigins,
		coordinator:    coordinator,
		deviceState:    deviceState,
	}
}

func (h *wsHub) applyNodeReport(ctx context.Context, report store.NodeReportInput) (store.NodeReportResult, error) {
	if h.deviceState == nil {
		return h.store.ApplyNodeReport(ctx, report)
	}
	report.ExternalDeviceState = true
	result, err := h.store.ApplyNodeReport(ctx, report)
	if err != nil {
		return store.NodeReportResult{}, err
	}
	if len(report.Alive) == 0 && !report.ReplaceAllDevices {
		return result, nil
	}
	userIDs, err := h.deviceState.ReplaceNodeDevices(ctx, report.NodeID, report.Alive, report.ReplaceAllDevices, report.Now)
	if err != nil {
		return store.NodeReportResult{}, err
	}
	result.DeviceUserIDs = userIDs
	return result, nil
}

func (h *wsHub) listUserDevices(ctx context.Context, userIDs []int64, now time.Time) (map[int64][]string, error) {
	if h.deviceState != nil {
		return h.deviceState.ListUserDevices(ctx, userIDs, now)
	}
	return h.store.ListUserDevices(ctx, userIDs, now)
}

func (h *wsHub) clearDevices(ctx context.Context, nodeIDs []int64, now time.Time) ([]int64, error) {
	if h.deviceState != nil {
		return h.deviceState.ClearNodeDevices(ctx, nodeIDs, now)
	}
	return h.store.ClearNodeDevices(ctx, nodeIDs, now)
}

func (h *wsHub) runUntil(ctx context.Context) {
	reconcileTicker := time.NewTicker(time.Second)
	defer reconcileTicker.Stop()
	var renewTicker *time.Ticker
	var renew <-chan time.Time
	if h.coordinator != nil {
		renewTicker = time.NewTicker(nodecoord.RenewInterval)
		renew = renewTicker.C
		defer renewTicker.Stop()
	}
	for {
		select {
		case <-ctx.Done():
			goto shutdown
		case <-reconcileTicker.C:
			h.reconcileConnections(ctx)
		case <-renew:
			h.renewConnections(ctx)
		}
	}

shutdown:
	h.mu.Lock()
	unique := make(map[*wsConnection]struct{}, len(h.machines)+len(h.nodes))
	for _, connection := range h.machines {
		unique[connection] = struct{}{}
	}
	for _, connection := range h.nodes {
		unique[connection] = struct{}{}
	}
	h.machines = make(map[int64]*wsConnection)
	h.nodes = make(map[int64]*wsConnection)
	h.mu.Unlock()
	for connection := range unique {
		connection.close(websocket.CloseGoingAway, "server shutdown")
	}
}

func (h *wsHub) reconcileConnections(ctx context.Context) {
	h.mu.RLock()
	machineIDs := make([]int64, 0, len(h.machines))
	legacyConnections := make(map[*wsConnection]struct{})
	for machineID := range h.machines {
		machineIDs = append(machineIDs, machineID)
	}
	for _, connection := range h.nodes {
		if connection.legacy {
			legacyConnections[connection] = struct{}{}
		}
	}
	h.mu.RUnlock()
	if len(legacyConnections) > 0 {
		settings, err := h.store.GetNodeAgentSettings(ctx)
		if err != nil {
			h.logger.Warn("reconcile legacy websocket settings", "connections", len(legacyConnections), "error", err)
			for connection := range legacyConnections {
				connection.close(websocket.CloseTryAgainLater, "settings unavailable")
			}
		} else {
			for connection := range legacyConnections {
				if connection.legacySettingsRevision != settings.Revision {
					connection.close(websocket.ClosePolicyViolation, "credential or settings changed")
				}
			}
		}
	}
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
			h.notifyMachineNodes(ctx, machineID, false)
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
	settings, err := s.store.GetNodeAgentSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if !settings.WebSocketEnabled {
		http.NotFound(w, r)
		return
	}
	machineID, nodeID := int64(0), int64(0)
	legacy := r.URL.Query().Get("machine_id") == ""
	var snapshots []wsNodeSnapshot
	if legacy {
		nodeID, err = strconv.ParseInt(r.URL.Query().Get("node_id"), 10, 64)
		if err != nil || nodeID < 1 {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "node_id 必须是正整数", nil)
			return
		}
		if !s.authenticateLegacyNode(w, r, nodeID) || !s.authorizeLegacyNode(w, r, nodeID) || !s.allowServerRequest(w, r, s.handshakeRequests, 0) {
			return
		}
		node, nodeErr := s.store.GetNode(r.Context(), nodeID)
		if nodeErr != nil {
			handleStoreError(w, nodeErr)
			return
		}
		snapshot, snapshotErr := s.hub.nodeSnapshot(r.Context(), node, s.now())
		if snapshotErr != nil {
			handleStoreError(w, snapshotErr)
			return
		}
		snapshots = []wsNodeSnapshot{snapshot}
	} else {
		machineID, err = strconv.ParseInt(r.URL.Query().Get("machine_id"), 10, 64)
		if err != nil || machineID < 1 {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "machine_id 必须是正整数", nil)
			return
		}
		if !s.authenticateMachine(w, r, machineID) || !s.allowServerRequest(w, r, s.handshakeRequests, machineID) {
			return
		}
		snapshots, err = s.hub.machineSnapshots(r.Context(), machineID)
		if err != nil {
			handleStoreError(w, err)
			return
		}
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
	connectionID := uuid.NewString()
	if s.hub.coordinator != nil {
		connectionID, err = s.hub.coordinator.NewConnectionID()
		if err != nil {
			s.hub.logger.Error("generate node websocket connection ID", "error", err)
			_ = connection.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "coordination unavailable"), time.Now().Add(webSocketWriteWait))
			_ = connection.Close()
			return
		}
	}
	client := &wsConnection{
		id: connectionID, machineID: machineID, legacy: legacy, legacySettingsRevision: settings.Revision,
		conn: connection, hub: s.hub, writeCh: make(chan wsEnvelope, max(64, len(snapshots)*3+4)),
		done: make(chan struct{}), nodeIDs: nodeIDs,
		messageRequests:        newRequestLimiter(maxWebSocketMessagesPerConnection, time.Minute),
		controlMessageRequests: newRequestLimiter(maxWebSocketControlMessages, time.Minute),
		nodeMessageRequests:    newRequestLimiter(maxWebSocketMessagesPerNode, time.Minute),
	}
	registrationID := machineID
	if legacy {
		registrationID = nodeID
	}
	registrationLock := &s.hub.registrationMu[uint64(registrationID)%uint64(len(s.hub.registrationMu))]
	registrationLock.Lock()
	if s.hub.coordinator != nil {
		claimErr := error(nil)
		if legacy {
			claimErr = s.hub.coordinator.ClaimNode(r.Context(), nodeID, client.id)
		} else {
			claimErr = s.hub.coordinator.ClaimMachine(r.Context(), machineID, client.nodeIDList(), client.id)
		}
		if claimErr != nil {
			registrationLock.Unlock()
			s.hub.logger.Warn("claim node websocket ownership", "machine_id", machineID, "node_id", nodeID, "error", claimErr)
			client.close(websocket.CloseTryAgainLater, "coordination unavailable")
			return
		}
	}
	replaced := s.hub.register(client)
	registrationLock.Unlock()
	for _, old := range replaced {
		old.close(websocket.ClosePolicyViolation, "connection replaced")
	}
	if s.hub.coordinator != nil {
		var owned bool
		var verifyErr error
		if legacy {
			owned, verifyErr = s.hub.coordinator.OwnsNode(r.Context(), nodeID, client.id)
		} else if len(client.nodeIDList()) == 0 {
			owned, verifyErr = s.hub.coordinator.OwnsMachine(r.Context(), machineID, client.id)
		} else {
			owned, verifyErr = s.hub.coordinator.OwnsMachineAndNodes(r.Context(), machineID, client.nodeIDList(), client.id)
		}
		if verifyErr != nil || !owned {
			s.hub.unregister(client)
			s.hub.logger.Warn("verify new node websocket ownership", "machine_id", machineID, "error", verifyErr)
			client.close(websocket.ClosePolicyViolation, "connection replaced")
			return
		}
	}
	if legacy {
		parts := strings.Fields(r.Header.Get("Authorization"))
		valid := len(parts) == 2
		if valid {
			valid, err = s.store.AuthenticateLegacyNodeToken(r.Context(), parts[1])
		}
		if err != nil || !valid {
			s.hub.unregister(client)
			client.close(websocket.ClosePolicyViolation, "credential replaced")
			return
		}
	}

	go client.writeLoop()
	if legacy {
		client.enqueue(legacyAuthEnvelope(nodeID))
	} else {
		client.enqueue(initialAuthEnvelope(machineID, snapshots))
	}
	for _, snapshot := range snapshots {
		client.enqueue(configSyncEnvelope(snapshot))
		client.enqueue(usersSyncEnvelope(snapshot))
		if !legacy {
			client.enqueue(devicesSyncEnvelope(snapshot))
		}
	}
	s.hub.logger.Info("node websocket connected", "machine_id", machineID, "node_id", nodeID, "legacy", legacy, "nodes", len(snapshots))
	client.readLoop()
	current, affectedUsers, clearErr := s.hub.unregisterAndClear(client)
	client.close(websocket.CloseNormalClosure, "")
	if clearErr != nil {
		s.hub.logger.Warn("clear disconnected node devices", "machine_id", machineID, "error", clearErr)
	} else if current {
		s.hub.NotifyDeviceStates(context.Background(), affectedUsers)
	}
	s.hub.logger.Info("node websocket disconnected", "machine_id", machineID, "node_id", nodeID, "legacy", legacy, "current", current)
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
	eligible := 0
	for _, node := range nodes {
		if node.Enabled && node.RuntimeConfigured {
			eligible++
		}
	}
	snapshots := make([]wsNodeSnapshot, 0, eligible)
	if eligible == 0 {
		return snapshots, nil
	}
	settings, err := h.store.GetNodeAgentSettings(ctx)
	if err != nil {
		return nil, err
	}
	for _, node := range nodes {
		if !node.Enabled || !node.RuntimeConfigured {
			continue
		}
		snapshot, err := h.nodeSnapshotWithIntervals(ctx, node, now, settings.PushInterval, settings.PullInterval)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func (h *wsHub) nodeSnapshot(ctx context.Context, node store.Node, now time.Time) (wsNodeSnapshot, error) {
	settings, err := h.store.GetNodeAgentSettings(ctx)
	if err != nil {
		return wsNodeSnapshot{}, err
	}
	return h.nodeSnapshotWithIntervals(ctx, node, now, settings.PushInterval, settings.PullInterval)
}

func (h *wsHub) nodeSnapshotWithIntervals(ctx context.Context, node store.Node, now time.Time, pushInterval, pullInterval int) (wsNodeSnapshot, error) {
	runtime, err := h.store.GetNodeRuntime(ctx, node.ID)
	if err != nil {
		return wsNodeSnapshot{}, err
	}
	config, err := nodeConfigObject(runtime, pushInterval, pullInterval)
	if err != nil {
		return wsNodeSnapshot{}, err
	}
	users, err := h.store.ListNodeRuntimeUsers(ctx, node.ID, now)
	if err != nil {
		return wsNodeSnapshot{}, err
	}
	userIDs := make([]int64, 0, len(users))
	for _, user := range users {
		userIDs = append(userIDs, user.ID)
	}
	devices, err := h.listUserDevices(ctx, userIDs, now)
	if err != nil {
		return wsNodeSnapshot{}, err
	}
	return wsNodeSnapshot{
		summary:   machineNodeSummary{ID: node.ID, Type: node.Type, Name: node.Name},
		config:    config,
		users:     users,
		devices:   devices,
		timestamp: now.Unix(),
	}, nil
}

func (h *wsHub) hasMachineNode(machineID, nodeID int64) bool {
	h.mu.RLock()
	connection := h.nodes[nodeID]
	h.mu.RUnlock()
	return connection != nil && !connection.legacy && connection.machineID == machineID && connection.hasNode(nodeID)
}

func (h *wsHub) hasNode(nodeID int64) bool {
	h.mu.RLock()
	connection := h.nodes[nodeID]
	h.mu.RUnlock()
	return connection != nil && connection.hasNode(nodeID)
}

func (h *wsHub) register(connection *wsConnection) []*wsConnection {
	h.mu.Lock()
	replaced := make(map[*wsConnection]struct{})
	if !connection.legacy {
		if old := h.machines[connection.machineID]; old != nil && old != connection {
			replaced[old] = struct{}{}
		}
		h.machines[connection.machineID] = connection
	}
	for _, nodeID := range connection.nodeIDList() {
		if old := h.nodes[nodeID]; old != nil && old != connection {
			replaced[old] = struct{}{}
		}
		h.nodes[nodeID] = connection
	}
	h.mu.Unlock()
	result := make([]*wsConnection, 0, len(replaced))
	for old := range replaced {
		result = append(result, old)
	}
	return result
}

func (h *wsHub) unregisterAndClear(connection *wsConnection) (bool, []int64, error) {
	if h.coordinator != nil {
		h.mu.Lock()
		currentMachine := !connection.legacy && h.machines[connection.machineID] == connection
		if currentMachine {
			delete(h.machines, connection.machineID)
		}
		ownedNodes := make([]int64, 0, len(connection.nodeIDList()))
		for _, nodeID := range connection.nodeIDList() {
			if h.nodes[nodeID] == connection {
				delete(h.nodes, nodeID)
				ownedNodes = append(ownedNodes, nodeID)
			}
		}
		if !currentMachine && len(ownedNodes) == 0 {
			h.mu.Unlock()
			return false, nil, nil
		}
		h.mu.Unlock()

		releasedNodes := make([]int64, 0, len(ownedNodes))
		var releaseErrors []error
		for _, nodeID := range ownedNodes {
			released, err := h.coordinator.ReleaseNodeIfOwned(context.Background(), nodeID, connection.id)
			if err != nil {
				releaseErrors = append(releaseErrors, err)
				continue
			}
			if released {
				releasedNodes = append(releasedNodes, nodeID)
			}
		}
		if currentMachine {
			if _, err := h.coordinator.ReleaseMachineIfOwned(context.Background(), connection.machineID, connection.id); err != nil {
				releaseErrors = append(releaseErrors, err)
			}
		}
		if len(releasedNodes) == 0 {
			return true, nil, errors.Join(releaseErrors...)
		}
		userIDs, clearErr := h.clearDevices(context.Background(), releasedNodes, h.now())
		return true, userIDs, errors.Join(append(releaseErrors, clearErr)...)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	currentMachine := !connection.legacy && h.machines[connection.machineID] == connection
	if currentMachine {
		delete(h.machines, connection.machineID)
	}
	ownedNodes := make([]int64, 0, len(connection.nodeIDList()))
	for _, nodeID := range connection.nodeIDList() {
		if h.nodes[nodeID] == connection {
			delete(h.nodes, nodeID)
			ownedNodes = append(ownedNodes, nodeID)
		}
	}
	if !currentMachine && len(ownedNodes) == 0 {
		return false, nil, nil
	}
	// Keep registration fenced while the former owner's device snapshot is
	// cleared. A reconnect cannot publish a new snapshot and then have it
	// removed by this stale close path.
	userIDs, err := h.clearDevices(context.Background(), ownedNodes, h.now())
	return true, userIDs, err
}

func (h *wsHub) unregister(connection *wsConnection) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	removed := false
	if !connection.legacy && h.machines[connection.machineID] == connection {
		delete(h.machines, connection.machineID)
		removed = true
	}
	for _, nodeID := range connection.nodeIDList() {
		if h.nodes[nodeID] == connection {
			delete(h.nodes, nodeID)
			removed = true
		}
	}
	return removed
}

func (h *wsHub) owns(connection *wsConnection) bool {
	h.mu.RLock()
	owned := connection.legacy || h.machines[connection.machineID] == connection
	for _, nodeID := range connection.nodeIDList() {
		owned = owned && h.nodes[nodeID] == connection
	}
	h.mu.RUnlock()
	return owned
}

func (h *wsHub) renewConnections(ctx context.Context) {
	h.mu.RLock()
	connections := make([]*wsConnection, 0, len(h.machines))
	legacyConnections := make([]*wsConnection, 0)
	for _, connection := range h.machines {
		connections = append(connections, connection)
	}
	for _, connection := range h.nodes {
		if connection.legacy {
			legacyConnections = append(legacyConnections, connection)
		}
	}
	h.mu.RUnlock()
	if len(connections) == 0 && len(legacyConnections) == 0 {
		return
	}
	leases := make([]nodecoord.Lease, len(connections))
	for index, connection := range connections {
		leases[index] = nodecoord.Lease{
			MachineID: connection.machineID, NodeIDs: connection.nodeIDList(), ConnectionID: connection.id,
		}
	}
	renewed, err := h.coordinator.Renew(ctx, leases)
	if err != nil {
		h.logger.Warn("renew node websocket ownership", "connections", len(connections), "error", err)
	}
	for index, connection := range connections {
		if err != nil || index >= len(renewed) || !renewed[index] {
			if h.owns(connection) {
				connection.close(websocket.CloseTryAgainLater, "coordination unavailable")
			}
		}
	}
	nodeLeases := make([]nodecoord.NodeLease, 0, len(legacyConnections))
	for _, connection := range legacyConnections {
		nodeIDs := connection.nodeIDList()
		if len(nodeIDs) == 1 {
			nodeLeases = append(nodeLeases, nodecoord.NodeLease{NodeID: nodeIDs[0], ConnectionID: connection.id})
		}
	}
	nodesRenewed, nodeErr := h.coordinator.RenewNodes(ctx, nodeLeases)
	if nodeErr != nil {
		h.logger.Warn("renew legacy node websocket ownership", "connections", len(nodeLeases), "error", nodeErr)
	}
	for index, connection := range legacyConnections {
		if nodeErr != nil || index >= len(nodesRenewed) || !nodesRenewed[index] {
			if h.owns(connection) {
				connection.close(websocket.CloseTryAgainLater, "coordination unavailable")
			}
		}
	}
}

func (h *wsHub) handleCoordinationEvent(event nodecoord.Event) {
	if h.coordinator == nil || event.Source == h.coordinator.InstanceID() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch event.Kind {
	case nodecoord.EventReplacement:
		h.closeReplacedConnections(event)
	case nodecoord.EventMachineNodes:
		h.notifyMachineNodes(ctx, event.MachineID, false)
	case nodecoord.EventDisconnectMachine:
		h.disconnectMachine(event.MachineID, event.Reason, false)
	case nodecoord.EventNodeFull:
		h.notifyNodeFull(ctx, event.MachineID, event.NodeID, false)
	case nodecoord.EventNodeConfig:
		h.notifyNodeConfig(ctx, event.MachineID, event.NodeID, false)
	case nodecoord.EventDeviceUsers:
		h.notifyDeviceStates(ctx, event.UserIDs, false)
	case nodecoord.EventRefreshGroups:
		h.refreshGroupUsers(ctx, event.GroupIDs, event.DevicesCleared)
	case nodecoord.EventDisconnectNodes:
		h.disconnectNodes(event.NodeIDs, event.Reason, false)
	case nodecoord.EventDisconnectLegacy:
		h.disconnectLegacy(event.Reason, false)
	case nodecoord.EventDisconnectAll:
		h.disconnectAll(event.Reason, false)
	}
}

func (h *wsHub) closeReplacedConnections(event nodecoord.Event) {
	h.mu.RLock()
	connections := make(map[*wsConnection]struct{})
	if event.MachineID > 0 {
		if connection := h.machines[event.MachineID]; connection != nil && connection.id != event.ConnectionID {
			connections[connection] = struct{}{}
		}
	}
	if len(event.NodeIDs) > 0 {
		for _, nodeID := range event.NodeIDs {
			if connection := h.nodes[nodeID]; connection != nil && connection.id != event.ConnectionID {
				connections[connection] = struct{}{}
			}
		}
	}
	h.mu.RUnlock()
	for connection := range connections {
		connection.close(websocket.ClosePolicyViolation, "connection replaced")
	}
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
		if !c.allowIncomingMessage(message) {
			c.hub.logger.Warn("rate limit node websocket message", "machine_id", c.machineID, "event", webSocketEventLogLabel(message.Event))
			c.close(websocket.ClosePolicyViolation, "rate limit exceeded")
			return
		}
		if c.hub.coordinator != nil {
			owned, err := c.ownsIncomingMessage(message)
			if err != nil {
				c.hub.logger.Warn("verify node websocket ownership", "machine_id", c.machineID, "event", webSocketEventLogLabel(message.Event), "error", err)
				c.close(websocket.CloseTryAgainLater, "coordination unavailable")
				return
			}
			if !owned {
				c.close(websocket.ClosePolicyViolation, "connection replaced")
				return
			}
		}
		if err := c.handleMessage(message); err != nil {
			c.hub.logger.Warn("reject node websocket message", "machine_id", c.machineID, "event", webSocketEventLogLabel(message.Event), "error", err)
			c.close(websocket.ClosePolicyViolation, "invalid message")
			return
		}
	}
}

func (c *wsConnection) allowIncomingMessage(message wsIncomingEnvelope) bool {
	now := c.hub.now()
	connectionAllowed := c.messageRequests.take("connection", now)
	eventAllowed := true
	if nodeID, scoped := c.incomingMessageNodeID(message); scoped {
		eventAllowed = c.nodeMessageRequests.take(strconv.FormatInt(nodeID, 10), now)
	} else {
		eventAllowed = c.controlMessageRequests.take("control", now)
	}
	return connectionAllowed && eventAllowed
}

func webSocketEventLogLabel(event string) string {
	switch event {
	case "pong", "node.status", "report.devices":
		return event
	default:
		return "unknown"
	}
}

func (c *wsConnection) incomingMessageNodeID(message wsIncomingEnvelope) (int64, bool) {
	if message.Event != "node.status" && message.Event != "report.devices" {
		return 0, false
	}
	if c.legacy {
		nodeIDs := c.nodeIDList()
		if len(nodeIDs) == 1 {
			return nodeIDs[0], true
		}
		return 0, false
	}
	var identity struct {
		NodeID int64 `json:"node_id"`
	}
	if err := json.Unmarshal(message.Data, &identity); err != nil || identity.NodeID < 1 || !c.hasNode(identity.NodeID) {
		return 0, false
	}
	return identity.NodeID, true
}

func (c *wsConnection) ownsIncomingMessage(message wsIncomingEnvelope) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if c.legacy {
		nodeIDs := c.nodeIDList()
		if len(nodeIDs) != 1 {
			return false, nil
		}
		return c.hub.coordinator.OwnsNode(ctx, nodeIDs[0], c.id)
	}
	if message.Event == "pong" {
		return c.hub.coordinator.OwnsMachine(ctx, c.machineID, c.id)
	}
	if message.Event != "node.status" && message.Event != "report.devices" {
		return true, nil
	}
	var identity struct {
		NodeID int64 `json:"node_id"`
	}
	if err := json.Unmarshal(message.Data, &identity); err != nil || identity.NodeID < 1 || !c.hasNode(identity.NodeID) {
		return true, nil
	}
	return c.hub.coordinator.OwnsMachineAndNodes(ctx, c.machineID, []int64{identity.NodeID}, c.id)
}

func (c *wsConnection) handleMessage(message wsIncomingEnvelope) error {
	nodeID := int64(0)
	if c.legacy {
		nodeIDs := c.nodeIDList()
		if len(nodeIDs) != 1 {
			return errors.New("legacy connection has an invalid node lease")
		}
		nodeID = nodeIDs[0]
	}
	switch message.Event {
	case "pong":
		return nil
	case "node.status":
		if !c.legacy {
			var identity struct {
				NodeID int64 `json:"node_id"`
			}
			if err := json.Unmarshal(message.Data, &identity); err != nil || !c.hasNode(identity.NodeID) {
				return errors.New("node.status has an invalid node_id")
			}
			nodeID = identity.NodeID
		}
		validated, err := validateNodeReport(nodeReportPayload{Metrics: message.Data})
		if err != nil {
			return err
		}
		validated.MachineID = c.machineID
		validated.LegacyAuth = c.legacy
		validated.NodeID = nodeID
		validated.Now = c.hub.now()
		_, err = c.hub.applyNodeReport(context.Background(), validated)
		return err
	case "report.devices":
		var payload struct {
			NodeID  int64               `json:"node_id"`
			Devices map[string][]string `json:"devices"`
		}
		if err := json.Unmarshal(message.Data, &payload); err != nil {
			return errors.New("report.devices has invalid data")
		}
		if c.legacy {
			if payload.Devices == nil {
				if err := json.Unmarshal(message.Data, &payload.Devices); err != nil {
					return errors.New("report.devices has invalid data")
				}
			}
			payload.NodeID = nodeID
		} else if payload.Devices == nil || !c.hasNode(payload.NodeID) {
			return errors.New("report.devices has an invalid node_id")
		}
		validated, err := validateNodeReport(nodeReportPayload{Alive: payload.Devices})
		if err != nil {
			return err
		}
		validated.MachineID = c.machineID
		validated.LegacyAuth = c.legacy
		validated.NodeID = payload.NodeID
		validated.ReplaceAllDevices = true
		validated.Now = c.hub.now()
		result, applyErr := c.hub.applyNodeReport(context.Background(), validated)
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

func legacyAuthEnvelope(nodeID int64) wsEnvelope {
	return wsEnvelope{Event: "auth.success", Data: map[string]any{"node_id": nodeID}}
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

func userDeltaEnvelope(nodeID int64, action string, users []store.RuntimeUser, timestamp int64) wsEnvelope {
	return wsEnvelope{Event: "sync.user.delta", Data: map[string]any{
		"node_id": nodeID, "action": action, "users": users, "timestamp": timestamp,
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
	h.notifyMachineNodes(ctx, machineID, true)
}

func (h *wsHub) notifyMachineNodes(ctx context.Context, machineID int64, broadcast bool) {
	if broadcast {
		defer h.publishCoordination(ctx, nodecoord.Event{Kind: nodecoord.EventMachineNodes, MachineID: machineID})
	}
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
	if h.coordinator != nil {
		claimed, err := h.coordinator.ClaimMachineNodesIfOwned(ctx, machineID, nodeIDs, connection.id)
		if err != nil || !claimed {
			h.logger.Warn("synchronize node websocket ownership", "machine_id", machineID, "error", err)
			connection.close(websocket.CloseTryAgainLater, "coordination unavailable")
			return
		}
	}
	replacedConnections := make(map[*wsConnection]struct{})
	h.mu.Lock()
	if h.machines[machineID] != connection {
		h.mu.Unlock()
		connection.close(websocket.ClosePolicyViolation, "connection replaced")
		return
	}
	for _, nodeID := range nodeIDs {
		if old := h.nodes[nodeID]; old != nil && old != connection {
			replacedConnections[old] = struct{}{}
		}
		h.nodes[nodeID] = connection
	}
	for nodeID := range removedNodes {
		if h.nodes[nodeID] == connection {
			delete(h.nodes, nodeID)
		}
	}
	h.mu.Unlock()
	connection.replaceNodes(nodeIDs)
	for replaced := range replacedConnections {
		replaced.close(websocket.ClosePolicyViolation, "connection replaced")
	}
	connection.enqueue(nodesSyncEnvelope(snapshots))
	removedNodeIDs := make([]int64, 0, len(removedNodes))
	for nodeID := range removedNodes {
		if h.coordinator == nil {
			removedNodeIDs = append(removedNodeIDs, nodeID)
			continue
		}
		released, err := h.coordinator.ReleaseNodeIfOwned(ctx, nodeID, connection.id)
		if err != nil {
			h.logger.Warn("release removed node websocket ownership", "machine_id", machineID, "node_id", nodeID, "error", err)
			connection.close(websocket.CloseTryAgainLater, "coordination unavailable")
			return
		}
		if released {
			removedNodeIDs = append(removedNodeIDs, nodeID)
		}
	}
	if len(removedNodeIDs) > 0 {
		h.clearNodeDevices(ctx, removedNodeIDs, broadcast)
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
	h.disconnectMachine(machineID, reason, true)
}

func (h *wsHub) DisconnectLegacy(reason string) {
	h.disconnectLegacy(reason, true)
}

func (h *wsHub) DisconnectNodes(nodeIDs []int64, reason string) {
	h.disconnectNodes(nodeIDs, reason, true)
}

func (h *wsHub) disconnectNodes(nodeIDs []int64, reason string, broadcast bool) {
	if broadcast {
		h.publishCoordination(context.Background(), nodecoord.Event{Kind: nodecoord.EventDisconnectNodes, NodeIDs: nodeIDs, Reason: reason})
	}
	h.mu.RLock()
	connections := make(map[*wsConnection]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if connection := h.nodes[nodeID]; connection != nil && connection.legacy {
			connections[connection] = struct{}{}
		}
	}
	h.mu.RUnlock()
	for connection := range connections {
		connection.close(websocket.ClosePolicyViolation, reason)
	}
}

func (h *wsHub) disconnectLegacy(reason string, broadcast bool) {
	if broadcast {
		h.publishCoordination(context.Background(), nodecoord.Event{Kind: nodecoord.EventDisconnectLegacy, Reason: reason})
	}
	h.mu.RLock()
	connections := make(map[*wsConnection]struct{})
	for _, connection := range h.nodes {
		if connection.legacy {
			connections[connection] = struct{}{}
		}
	}
	h.mu.RUnlock()
	for connection := range connections {
		connection.close(websocket.ClosePolicyViolation, reason)
	}
}

func (h *wsHub) DisconnectAll(reason string) {
	h.disconnectAll(reason, true)
}

func (h *wsHub) disconnectAll(reason string, broadcast bool) {
	if broadcast {
		h.publishCoordination(context.Background(), nodecoord.Event{Kind: nodecoord.EventDisconnectAll, Reason: reason})
	}
	h.mu.RLock()
	connections := make(map[*wsConnection]struct{}, len(h.machines)+len(h.nodes))
	for _, connection := range h.machines {
		connections[connection] = struct{}{}
	}
	for _, connection := range h.nodes {
		connections[connection] = struct{}{}
	}
	h.mu.RUnlock()
	for connection := range connections {
		connection.close(websocket.ClosePolicyViolation, reason)
	}
}

func (h *wsHub) disconnectMachine(machineID int64, reason string, broadcast bool) {
	if broadcast && h.coordinator != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := h.coordinator.RevokeMachine(ctx, machineID, reason); err != nil {
			h.logger.Warn("revoke node websocket ownership", "machine_id", machineID, "error", err)
		}
		cancel()
	}
	h.mu.RLock()
	connection := h.machines[machineID]
	h.mu.RUnlock()
	if connection != nil {
		connection.close(websocket.ClosePolicyViolation, reason)
	}
}

func (h *wsHub) ClearNodeDevices(ctx context.Context, nodeIDs []int64) {
	h.clearNodeDevices(ctx, nodeIDs, true)
}

func (h *wsHub) clearNodeDevices(ctx context.Context, nodeIDs []int64, broadcast bool) {
	affectedUsers, err := h.clearDevices(ctx, nodeIDs, h.now())
	if err != nil {
		h.logger.Warn("clear removed node devices", "node_ids", nodeIDs, "error", err)
		return
	}
	h.notifyDeviceStates(ctx, affectedUsers, broadcast)
}

func (h *wsHub) NotifyNodeFull(ctx context.Context, machineID, nodeID int64) {
	h.notifyNodeFull(ctx, machineID, nodeID, true)
}

func (h *wsHub) notifyNodeFull(ctx context.Context, machineID, nodeID int64, broadcast bool) {
	if broadcast {
		defer h.publishCoordination(ctx, nodecoord.Event{Kind: nodecoord.EventNodeFull, MachineID: machineID, NodeID: nodeID})
	}
	node, err := h.store.GetNode(ctx, nodeID)
	if err != nil || !node.Enabled || !node.RuntimeConfigured {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			h.logger.Warn("prepare websocket node full synchronization", "machine_id", machineID, "node_id", nodeID, "error", err)
		}
		return
	}
	snapshot, err := h.nodeSnapshot(ctx, node, h.now())
	if err != nil {
		h.logger.Warn("prepare websocket node full synchronization", "machine_id", machineID, "node_id", nodeID, "error", err)
		return
	}
	h.mu.RLock()
	connection := h.nodes[nodeID]
	h.mu.RUnlock()
	if connection == nil || !connection.hasNode(nodeID) {
		return
	}
	if !connection.legacy && (node.MachineID == nil || *node.MachineID != connection.machineID) {
		return
	}
	connection.enqueue(configSyncEnvelope(snapshot))
	connection.enqueue(usersSyncEnvelope(snapshot))
	if !connection.legacy {
		connection.enqueue(devicesSyncEnvelope(snapshot))
	}
}

func (h *wsHub) NotifyNodeConfig(ctx context.Context, machineID, nodeID int64) {
	h.notifyNodeConfig(ctx, machineID, nodeID, true)
}

func (h *wsHub) notifyNodeConfig(ctx context.Context, machineID, nodeID int64, broadcast bool) {
	if broadcast {
		defer h.publishCoordination(ctx, nodecoord.Event{Kind: nodecoord.EventNodeConfig, MachineID: machineID, NodeID: nodeID})
	}
	node, err := h.store.GetNode(ctx, nodeID)
	if err != nil || !node.Enabled || !node.RuntimeConfigured {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			h.logger.Warn("prepare websocket node config synchronization", "machine_id", machineID, "node_id", nodeID, "error", err)
		}
		return
	}
	runtime, err := h.store.GetNodeRuntime(ctx, nodeID)
	if err != nil {
		h.logger.Warn("prepare websocket node config synchronization", "machine_id", machineID, "node_id", nodeID, "error", err)
		return
	}
	settings, err := h.store.GetNodeAgentSettings(ctx)
	if err != nil {
		h.logger.Warn("prepare websocket node config settings", "node_id", nodeID, "error", err)
		return
	}
	config, err := nodeConfigObject(runtime, settings.PushInterval, settings.PullInterval)
	if err != nil {
		h.logger.Warn("prepare websocket node config synchronization", "machine_id", machineID, "node_id", nodeID, "error", err)
		return
	}
	h.mu.RLock()
	connection := h.nodes[nodeID]
	h.mu.RUnlock()
	if connection == nil || !connection.hasNode(nodeID) {
		return
	}
	if !connection.legacy && (node.MachineID == nil || *node.MachineID != connection.machineID) {
		return
	}
	connection.enqueue(configSyncEnvelope(wsNodeSnapshot{
		summary: machineNodeSummary{ID: node.ID, Type: node.Type, Name: node.Name},
		config:  config, timestamp: h.now().Unix(),
	}))
}

// NotifyDeviceStates publishes authoritative snapshots only to runtime nodes
// whose groups contain an affected user. The device mutex preserves publish
// order when reports and disconnect cleanup happen concurrently.
func (h *wsHub) NotifyDeviceStates(ctx context.Context, userIDs []int64) {
	h.notifyDeviceStates(ctx, userIDs, true)
}

func (h *wsHub) notifyDeviceStates(ctx context.Context, userIDs []int64, broadcast bool) {
	if len(userIDs) == 0 {
		return
	}
	if broadcast {
		defer h.publishCoordination(ctx, nodecoord.Event{Kind: nodecoord.EventDeviceUsers, UserIDs: uniquePositiveIDs(userIDs)})
	}
	targetNodeIDs, err := h.store.ListRuntimeNodeIDsForUsers(ctx, userIDs)
	if err != nil {
		h.logger.Warn("resolve websocket device synchronization targets", "error", err)
		return
	}
	h.notifyDeviceStatesToNodeIDs(ctx, targetNodeIDs)
}

func (h *wsHub) notifyDeviceStatesToNodeIDs(ctx context.Context, targetNodeIDs []int64) {
	if len(targetNodeIDs) == 0 {
		return
	}
	h.deviceSyncMu.Lock()
	defer h.deviceSyncMu.Unlock()

	now := h.now()
	for _, nodeID := range uniquePositiveIDs(targetNodeIDs) {
		h.mu.RLock()
		expected := h.nodes[nodeID]
		h.mu.RUnlock()
		if expected == nil {
			continue
		}
		node, err := h.store.GetNode(ctx, nodeID)
		if err != nil {
			h.logger.Warn("authorize websocket device synchronization", "machine_id", expected.machineID, "node_id", nodeID, "error", err)
			continue
		}
		if !node.Enabled || !node.RuntimeConfigured || (!expected.legacy && (node.MachineID == nil || *node.MachineID != expected.machineID)) {
			continue
		}
		users, err := h.store.ListNodeRuntimeUsers(ctx, nodeID, now)
		if err != nil {
			h.logger.Warn("list websocket device synchronization users", "machine_id", expected.machineID, "node_id", nodeID, "error", err)
			continue
		}
		nodeUserIDs := make([]int64, 0, len(users))
		for _, user := range users {
			nodeUserIDs = append(nodeUserIDs, user.ID)
		}
		devices, err := h.listUserDevices(ctx, nodeUserIDs, now)
		if err != nil {
			h.logger.Warn("prepare websocket device synchronization", "machine_id", expected.machineID, "node_id", nodeID, "error", err)
			continue
		}
		snapshot := wsNodeSnapshot{summary: machineNodeSummary{ID: nodeID}, devices: devices, timestamp: now.Unix()}
		h.mu.RLock()
		current := h.nodes[nodeID]
		h.mu.RUnlock()
		if current == expected {
			current.enqueue(devicesSyncEnvelope(snapshot))
		}
	}
}

func (h *wsHub) refreshGroupUsers(ctx context.Context, groupIDs []int64, devicesCleared bool) {
	targets, err := h.store.ListRuntimeNodeTargetsForGroups(ctx, uniquePositiveIDs(groupIDs))
	if err != nil {
		h.logger.Warn("resolve websocket group refresh targets", "error", err)
		return
	}
	now := h.now()
	nodeIDs := make([]int64, 0, len(targets))
	seenNodes := make(map[int64]struct{}, len(targets))
	for _, target := range targets {
		if _, seen := seenNodes[target.NodeID]; seen {
			continue
		}
		seenNodes[target.NodeID] = struct{}{}
		h.mu.RLock()
		connection := h.nodes[target.NodeID]
		h.mu.RUnlock()
		if connection == nil || !connection.hasNode(target.NodeID) {
			continue
		}
		users, err := h.store.ListNodeRuntimeUsers(ctx, target.NodeID, now)
		if err != nil {
			h.logger.Warn("prepare websocket group user refresh", "machine_id", target.MachineID, "node_id", target.NodeID, "error", err)
			continue
		}
		connection.enqueue(usersSyncEnvelope(wsNodeSnapshot{
			summary: machineNodeSummary{ID: target.NodeID}, users: users, timestamp: now.Unix(),
		}))
		nodeIDs = append(nodeIDs, target.NodeID)
	}
	if devicesCleared {
		h.notifyDeviceStatesToNodeIDs(ctx, nodeIDs)
	}
}

func (h *wsHub) publishCoordination(ctx context.Context, event nodecoord.Event) {
	if h.coordinator == nil {
		return
	}
	for _, candidate := range splitCoordinationEvent(event, 5_000) {
		if err := h.coordinator.Publish(ctx, candidate); err != nil {
			h.logger.Warn("publish node websocket coordination event", "kind", event.Kind, "error", err)
			return
		}
	}
}

func splitCoordinationEvent(event nodecoord.Event, chunkSize int) []nodecoord.Event {
	var values []int64
	switch event.Kind {
	case nodecoord.EventDeviceUsers:
		values = uniquePositiveIDs(event.UserIDs)
	case nodecoord.EventRefreshGroups:
		values = uniquePositiveIDs(event.GroupIDs)
	default:
		return []nodecoord.Event{event}
	}
	if len(values) == 0 {
		return nil
	}
	result := make([]nodecoord.Event, 0, (len(values)+chunkSize-1)/chunkSize)
	for start := 0; start < len(values); start += chunkSize {
		end := min(start+chunkSize, len(values))
		candidate := event
		if event.Kind == nodecoord.EventDeviceUsers {
			candidate.UserIDs = append([]int64(nil), values[start:end]...)
		} else {
			candidate.GroupIDs = append([]int64(nil), values[start:end]...)
		}
		result = append(result, candidate)
	}
	return result
}

func uniquePositiveIDs(values []int64) []int64 {
	unique := make(map[int64]struct{}, len(values))
	for _, value := range values {
		if value > 0 {
			unique[value] = struct{}{}
		}
	}
	result := make([]int64, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// NotifyUserMutation publishes an O(changed users) delta to every runtime node
// in the old or new group. A full snapshot remains the reconnect baseline.
func (h *wsHub) NotifyUserMutation(ctx context.Context, userID int64, previousUUID string, oldGroupID, newGroupID *int64, devicesCleared bool) {
	if devicesCleared && h.deviceState != nil {
		if _, err := h.deviceState.ClearUserDevices(ctx, []int64{userID}, h.now()); err != nil {
			h.logger.Warn("clear mutated user device state", "user_id", userID, "error", err)
		}
	}
	groupIDs := make([]int64, 0, 2)
	if oldGroupID != nil {
		groupIDs = append(groupIDs, *oldGroupID)
	}
	if newGroupID != nil && (oldGroupID == nil || *newGroupID != *oldGroupID) {
		groupIDs = append(groupIDs, *newGroupID)
	}
	if len(groupIDs) == 0 {
		return
	}
	defer h.publishCoordination(ctx, nodecoord.Event{
		Kind: nodecoord.EventRefreshGroups, GroupIDs: uniquePositiveIDs(groupIDs), DevicesCleared: devicesCleared,
	})
	targets, err := h.store.ListRuntimeNodeTargetsForGroups(ctx, groupIDs)
	if err != nil {
		h.logger.Warn("resolve websocket user delta targets", "user_id", userID, "error", err)
		return
	}
	now := h.now()
	for _, target := range targets {
		h.mu.RLock()
		connection := h.nodes[target.NodeID]
		h.mu.RUnlock()
		if connection == nil || !connection.hasNode(target.NodeID) {
			continue
		}
		user, err := h.store.GetNodeRuntimeUser(ctx, target.NodeID, userID, now)
		action := "add"
		users := []store.RuntimeUser{user}
		if errors.Is(err, store.ErrNotFound) {
			action = "remove"
			users = []store.RuntimeUser{{ID: userID, UUID: previousUUID}}
		} else if err != nil {
			h.logger.Warn("prepare websocket user delta", "machine_id", target.MachineID, "node_id", target.NodeID, "user_id", userID, "error", err)
			continue
		}
		connection.enqueue(userDeltaEnvelope(target.NodeID, action, users, now.Unix()))
	}
	if devicesCleared {
		nodeIDs := make([]int64, 0, len(targets))
		for _, target := range targets {
			nodeIDs = append(nodeIDs, target.NodeID)
		}
		h.notifyDeviceStatesToNodeIDs(ctx, nodeIDs)
	}
}

// NotifyBulkUserRemoval resolves node memberships once and publishes bounded
// removal chunks. This avoids one node lookup per user on large bans while
// preserving the next full pull as the reconnect baseline.
func (h *wsHub) NotifyBulkUserRemoval(ctx context.Context, users []store.AdminUserBulkTarget) error {
	if h.deviceState != nil {
		userIDs := make([]int64, 0, len(users))
		for _, user := range users {
			if user.Status != store.AdminUserBulkTargetSucceeded {
				continue
			}
			userID := user.UserID
			if userID == 0 {
				userID = user.Sequence
			}
			userIDs = append(userIDs, userID)
		}
		if _, err := h.deviceState.ClearUserDevices(ctx, uniquePositiveIDs(userIDs), h.now()); err != nil {
			return err
		}
	}
	byGroup := make(map[int64][]store.RuntimeUser)
	groupIDs := make([]int64, 0)
	seenGroups := make(map[int64]struct{})
	for _, user := range users {
		if user.Status != store.AdminUserBulkTargetSucceeded || user.GroupID == nil {
			continue
		}
		groupID := *user.GroupID
		userID := user.UserID
		if userID == 0 {
			// sequence is the immutable user ID snapshot; the nullable foreign key
			// may already have been cleared by a concurrent account deletion.
			userID = user.Sequence
		}
		byGroup[groupID] = append(byGroup[groupID], store.RuntimeUser{ID: userID, UUID: user.UUID})
		if _, exists := seenGroups[groupID]; !exists {
			seenGroups[groupID] = struct{}{}
			groupIDs = append(groupIDs, groupID)
		}
	}
	if len(groupIDs) == 0 {
		return nil
	}
	defer h.publishCoordination(ctx, nodecoord.Event{
		Kind: nodecoord.EventRefreshGroups, GroupIDs: uniquePositiveIDs(groupIDs), DevicesCleared: true,
	})
	targets, err := h.store.ListRuntimeNodeGroupTargetsForGroups(ctx, groupIDs)
	if err != nil {
		return err
	}
	byNode := make(map[int64][]store.RuntimeUser)
	for _, target := range targets {
		byNode[target.NodeID] = append(byNode[target.NodeID], byGroup[target.GroupID]...)
	}
	now := h.now()
	nodeIDs := make([]int64, 0, len(byNode))
	for nodeID, removals := range byNode {
		h.mu.RLock()
		connection := h.nodes[nodeID]
		h.mu.RUnlock()
		if connection == nil || !connection.hasNode(nodeID) {
			continue
		}
		for start := 0; start < len(removals); start += 500 {
			end := min(start+500, len(removals))
			connection.enqueue(userDeltaEnvelope(nodeID, "remove", removals[start:end], now.Unix()))
		}
		nodeIDs = append(nodeIDs, nodeID)
	}
	h.notifyDeviceStatesToNodeIDs(ctx, nodeIDs)
	return nil
}
