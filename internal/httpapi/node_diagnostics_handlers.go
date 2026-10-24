package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/diagnostics"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	nodeDiagnosticsSchemaVersion      = 1
	maxNodeDiagnosticsBody            = 16 << 10
	maxNodeDiagnosticsCapabilities    = 32
	maxNodeDiagnosticsCapabilityBytes = 64
)

var (
	errInvalidNodeDiagnosticsCapability = errors.New("invalid node diagnostics capability")
	nodeDiagnosticsRuntimeIDPattern     = regexp.MustCompile(`^[0-9a-f]{32}$`)
	nodeDiagnosticsKernelPattern        = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	nodeDiagnosticsCapabilityPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]*\.v[1-9][0-9]*$`)
)

type nodeDiagnosticsBootstrapResponse struct {
	SchemaVersion      int    `json:"schema_version"`
	PanelScope         string `json:"panel_scope"`
	WebSocketEnabled   bool   `json:"websocket_enabled"`
	NodeID             int64  `json:"node_id"`
	DiagnosticsEnabled bool   `json:"diagnostics_enabled"`
}

// nodeDiagnosticsCapabilityPayload is shared by REST and WebSocket capability reports.
// node_id is optional for REST (authoritative value comes from the authenticated query) and
// required for machine-mode WebSocket messages.
type nodeDiagnosticsCapabilityPayload struct {
	NodeID        int64    `json:"node_id,omitempty"`
	SchemaVersion int      `json:"schema_version"`
	RuntimeID     string   `json:"runtime_id"`
	Kernel        string   `json:"kernel"`
	Capabilities  []string `json:"capabilities"`
}

// normalizeNodeDiagnosticsCapability validates a self-reported capability set.
// The reported runtime_id is recorded for display only; it must never be treated as a
// registered diagnostics runtime or WS owner (design: capability self-report cannot replace runtime).
func normalizeNodeDiagnosticsCapability(nodeID int64, payload nodeDiagnosticsCapabilityPayload, now time.Time) (store.NodeDiagnosticsCapability, error) {
	if payload.SchemaVersion != nodeDiagnosticsSchemaVersion {
		return store.NodeDiagnosticsCapability{}, fmt.Errorf("%w: unsupported schema_version %d", errInvalidNodeDiagnosticsCapability, payload.SchemaVersion)
	}
	if !nodeDiagnosticsRuntimeIDPattern.MatchString(payload.RuntimeID) {
		return store.NodeDiagnosticsCapability{}, fmt.Errorf("%w: runtime_id must be 32 lowercase hex characters", errInvalidNodeDiagnosticsCapability)
	}
	if !nodeDiagnosticsKernelPattern.MatchString(payload.Kernel) {
		return store.NodeDiagnosticsCapability{}, fmt.Errorf("%w: kernel is invalid", errInvalidNodeDiagnosticsCapability)
	}
	if len(payload.Capabilities) > maxNodeDiagnosticsCapabilities {
		return store.NodeDiagnosticsCapability{}, fmt.Errorf("%w: at most %d capabilities", errInvalidNodeDiagnosticsCapability, maxNodeDiagnosticsCapabilities)
	}
	seen := make(map[string]struct{}, len(payload.Capabilities))
	capabilities := make([]string, 0, len(payload.Capabilities))
	for _, capability := range payload.Capabilities {
		if len(capability) > maxNodeDiagnosticsCapabilityBytes || !nodeDiagnosticsCapabilityPattern.MatchString(capability) {
			return store.NodeDiagnosticsCapability{}, fmt.Errorf("%w: capability name is invalid", errInvalidNodeDiagnosticsCapability)
		}
		if _, duplicate := seen[capability]; duplicate {
			continue
		}
		seen[capability] = struct{}{}
		capabilities = append(capabilities, capability)
	}
	return store.NodeDiagnosticsCapability{
		NodeID:        nodeID,
		RuntimeID:     payload.RuntimeID,
		SchemaVersion: payload.SchemaVersion,
		Kernel:        payload.Kernel,
		Capabilities:  capabilities,
		UpdatedAt:     now,
	}, nil
}

// diagnosticsScopeForNode returns the panel_scope for node-facing responses.
// Unexpected read failures are logged and degrade to "diagnostics disabled" so node
// business synchronization is never blocked by the diagnostics subsystem.
func (s *server) diagnosticsScopeForNode(r *http.Request) string {
	scope, err := s.store.GetDiagnosticsScope(r.Context())
	if err != nil {
		if !errors.Is(err, diagnostics.ErrDiagnosticsNotInitialized) {
			s.logger.WarnContext(r.Context(), "diagnostics.panel_scope", "outcome", "unavailable", "error", err)
		}
		return ""
	}
	return scope
}

// authenticateDiagnosticsNode authenticates a node diagnostics REST request and applies the
// shared node report rate limit. V2 strictly requires machine credentials and never falls back
// to the legacy shared token; V1 uses the legacy shared token with its existing broad trust boundary.
func (s *server) authenticateDiagnosticsNode(w http.ResponseWriter, r *http.Request, isV2 bool) (int64, bool) {
	nodeID, err := strconv.ParseInt(r.URL.Query().Get("node_id"), 10, 64)
	if err != nil || nodeID < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "node_id 必须是正整数", nil)
		return 0, false
	}

	requestIdentity := int64(0)
	if isV2 {
		machineValue := r.URL.Query().Get("machine_id")
		if machineValue == "" {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "V2 接口必须提供 machine_id", nil)
			return 0, false
		}
		machineID, err := strconv.ParseInt(machineValue, 10, 64)
		if err != nil || machineID < 1 {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "machine_id 必须是正整数", nil)
			return 0, false
		}
		if !s.authenticateMachine(w, r, machineID) || !s.authorizeMachineNode(w, r, machineID, nodeID) {
			return 0, false
		}
		requestIdentity = machineID
	} else if !s.authenticateLegacyNode(w, r, nodeID) || !s.authorizeLegacyNode(w, r, nodeID) {
		return 0, false
	}

	// Isolated bucket: diagnostics retries must never consume the traffic report budget.
	if !s.allowServerRequest(w, r, s.diagnosticsRequests, requestIdentity) {
		return 0, false
	}
	return nodeID, true
}

func (s *server) nodeDiagnosticsBootstrapV1(w http.ResponseWriter, r *http.Request) {
	s.handleNodeDiagnosticsBootstrap(w, r, false)
}

func (s *server) nodeDiagnosticsBootstrapV2(w http.ResponseWriter, r *http.Request) {
	s.handleNodeDiagnosticsBootstrap(w, r, true)
}

func (s *server) handleNodeDiagnosticsBootstrap(w http.ResponseWriter, r *http.Request, isV2 bool) {
	nodeID, ok := s.authenticateDiagnosticsNode(w, r, isV2)
	if !ok {
		return
	}

	settings, err := s.store.GetNodeAgentSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	panelScope := s.diagnosticsScopeForNode(r)

	writeSuccess(w, http.StatusOK, nodeDiagnosticsBootstrapResponse{
		SchemaVersion:      nodeDiagnosticsSchemaVersion,
		PanelScope:         panelScope,
		WebSocketEnabled:   s.webSocketEnabled && settings.WebSocketEnabled,
		NodeID:             nodeID,
		DiagnosticsEnabled: panelScope != "",
	})
}

func (s *server) nodeDiagnosticsCapabilitiesV1(w http.ResponseWriter, r *http.Request) {
	s.handleNodeDiagnosticsCapabilities(w, r, false)
}

func (s *server) nodeDiagnosticsCapabilitiesV2(w http.ResponseWriter, r *http.Request) {
	s.handleNodeDiagnosticsCapabilities(w, r, true)
}

func (s *server) handleNodeDiagnosticsCapabilities(w http.ResponseWriter, r *http.Request, isV2 bool) {
	nodeID, ok := s.authenticateDiagnosticsNode(w, r, isV2)
	if !ok {
		return
	}

	var payload nodeDiagnosticsCapabilityPayload
	// Unknown fields are tolerated so newer nodes can add metadata without breaking older panels.
	if !decodeJSONLimitAllowUnknown(w, r, &payload, maxNodeDiagnosticsBody) {
		return
	}
	if payload.NodeID != 0 && payload.NodeID != nodeID {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请求体 node_id 与认证节点不一致", nil)
		return
	}
	capability, err := normalizeNodeDiagnosticsCapability(nodeID, payload, s.now())
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), nil)
		return
	}
	if err := s.store.SaveNodeDiagnosticsCapability(r.Context(), capability); err != nil {
		handleStoreError(w, err)
		return
	}

	writeSuccess(w, http.StatusOK, map[string]any{"saved": true})
}

func (s *server) nodeDiagnosticsSnapshotV1(w http.ResponseWriter, r *http.Request) {
	s.handleNodeDiagnosticsControlUnavailable(w, r, false)
}

func (s *server) nodeDiagnosticsSnapshotV2(w http.ResponseWriter, r *http.Request) {
	s.handleNodeDiagnosticsControlUnavailable(w, r, true)
}

func (s *server) nodeDiagnosticsAckV1(w http.ResponseWriter, r *http.Request) {
	s.handleNodeDiagnosticsControlUnavailable(w, r, false)
}

func (s *server) nodeDiagnosticsAckV2(w http.ResponseWriter, r *http.Request) {
	s.handleNodeDiagnosticsControlUnavailable(w, r, true)
}

// handleNodeDiagnosticsControlUnavailable serves snapshot/ACK until the authoritative lease service (D5)
// exists. Returning a fabricated complete snapshot would let nodes revoke/accept leases from a fake epoch,
// and acknowledging arbitrary ACKs would defeat forgery checks, so authenticated callers get an explicit
// 503 that nodes must treat as a failed request (never as an empty authoritative snapshot).
func (s *server) handleNodeDiagnosticsControlUnavailable(w http.ResponseWriter, r *http.Request, isV2 bool) {
	if _, ok := s.authenticateDiagnosticsNode(w, r, isV2); !ok {
		return
	}
	writeAPIError(w, http.StatusServiceUnavailable, "diagnostics_control_unavailable", "诊断租约控制面尚未启用", nil)
}
