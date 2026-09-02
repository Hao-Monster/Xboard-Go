package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const maxNodeHandshakeBody = 64 << 10

type resourceUsage struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
}

type networkUsage struct {
	InSpeed  float64 `json:"in_speed"`
	OutSpeed float64 `json:"out_speed"`
}

type machineStatusPayload struct {
	CPU  *float64       `json:"cpu"`
	Mem  *resourceUsage `json:"mem"`
	Swap *resourceUsage `json:"swap"`
	Disk *resourceUsage `json:"disk"`
	Net  *networkUsage  `json:"net"`
}

type xboardNodeAuthPayload struct {
	MachineID int64  `json:"machine_id"`
	NodeID    int64  `json:"node_id"`
	NodeType  string `json:"node_type"`
	Token     string `json:"token"`
}

type xboardNodeMachineStatusPayload struct {
	MachineID int64          `json:"machine_id"`
	NodeID    int64          `json:"node_id"`
	Token     string         `json:"token"`
	CPU       *float64       `json:"cpu"`
	Mem       *resourceUsage `json:"mem"`
	Swap      *resourceUsage `json:"swap"`
	Disk      *resourceUsage `json:"disk"`
	Net       *networkUsage  `json:"net"`
}

type machineNodeSummary struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
}

func (s *server) agentNodes(w http.ResponseWriter, r *http.Request) {
	machineID, ok := pathID(w, r, "machineID")
	if !ok {
		return
	}
	if !s.authenticateMachine(w, r, machineID) {
		return
	}
	nodes, err := s.activeMachineNodes(r.Context(), machineID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	settings, err := s.store.GetNodeAgentSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, map[string]any{
		"nodes": nodes,
		"base_config": map[string]int{
			"push_interval": settings.PushInterval,
			"pull_interval": settings.PullInterval,
		},
	})
}

func (s *server) agentStatus(w http.ResponseWriter, r *http.Request) {
	machineID, ok := pathID(w, r, "machineID")
	if !ok {
		return
	}
	var input machineStatusPayload
	if !decodeJSON(w, r, &input) {
		return
	}
	s.recordMachineStatus(w, r, machineID, input, true)
}

func (s *server) xboardNodeMachineNodes(w http.ResponseWriter, r *http.Request) {
	var input xboardNodeAuthPayload
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.MachineID < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "machine_id 必须是正整数", nil)
		return
	}
	if !s.authenticateMachine(w, r, input.MachineID) {
		return
	}
	if !s.allowServerRequest(w, r, s.machineRequests, input.MachineID) {
		return
	}
	nodes, err := s.activeMachineNodes(r.Context(), input.MachineID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	settings, err := s.store.GetNodeAgentSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": nodes,
		"base_config": map[string]int{
			"push_interval": settings.PushInterval,
			"pull_interval": settings.PullInterval,
		},
	})
}

func (s *server) xboardNodeHandshake(w http.ResponseWriter, r *http.Request) {
	var input xboardNodeAuthPayload
	if r.Method == http.MethodGet {
		var ok bool
		if input.MachineID, ok = optionalPositiveQueryID(w, r, "machine_id"); !ok {
			return
		}
		if input.NodeID, ok = optionalPositiveQueryID(w, r, "node_id"); !ok {
			return
		}
	} else if !decodeJSONLimit(w, r, &input, maxNodeHandshakeBody) {
		return
	}
	if input.MachineID < 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "machine_id 不能是负数", nil)
		return
	}
	if input.NodeID < 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "node_id 不能是负数", nil)
		return
	}
	requestIdentity := input.MachineID
	if input.MachineID > 0 {
		if !s.authenticateMachine(w, r, input.MachineID) {
			return
		}
		if input.NodeID > 0 && !s.authorizeMachineNode(w, r, input.MachineID, input.NodeID) {
			return
		}
	} else {
		if !s.authenticateLegacyNode(w, r, input.NodeID) {
			return
		}
		if input.NodeID > 0 && !s.authorizeLegacyNode(w, r, input.NodeID) {
			return
		}
	}
	if !s.allowServerRequest(w, r, s.handshakeRequests, requestIdentity) {
		return
	}
	settings, err := s.store.GetNodeAgentSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	websocket := map[string]any{"enabled": false}
	if s.webSocketEnabled && settings.WebSocketEnabled {
		websocket = map[string]any{"enabled": true, "ws_url": s.requestWebSocketURL(r, settings.WebSocketURL)}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"websocket": websocket,
		"settings": map[string]int{
			"push_interval": settings.PushInterval,
			"pull_interval": settings.PullInterval,
		},
	})
}

func (s *server) requestWebSocketURL(_ *http.Request, configuredURL string) string {
	if configuredURL != "" {
		return strings.TrimRight(configuredURL, "/")
	}
	panel, err := url.Parse(s.panelURL)
	if err != nil || panel.Host == "" {
		return ""
	}
	scheme := "ws"
	if panel.Scheme == "https" {
		scheme = "wss"
	}
	return scheme + "://" + panel.Host + strings.TrimRight(panel.EscapedPath(), "/") + "/ws"
}

func (s *server) authenticateLegacyNode(w http.ResponseWriter, r *http.Request, nodeID int64) bool {
	client, _ := nodeRequestAddresses(r, s.trustedProxyPrefixes)
	attemptKey := client + ":legacy"
	if !s.legacyNodeAuthFailures.allowed(attemptKey, s.now()) {
		w.Header().Set("Retry-After", "60")
		writeAPIError(w, http.StatusTooManyRequests, "node_auth_rate_limited", "节点认证失败次数过多，请稍后重试", nil)
		return false
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 256 {
		s.legacyNodeAuthFailures.failed(attemptKey, s.now())
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeAPIError(w, http.StatusUnauthorized, "invalid_node_credential", "节点凭据无效或未配置", nil)
		return false
	}
	valid, err := s.store.AuthenticateLegacyNodeToken(r.Context(), parts[1])
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return false
	}
	if !valid {
		s.legacyNodeAuthFailures.failed(attemptKey, s.now())
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeAPIError(w, http.StatusUnauthorized, "invalid_node_credential", "节点凭据无效或未配置", nil)
		return false
	}
	if r.URL.Path == "/ws" {
		s.legacyWebSocketAuthSuccess.Add(1)
	} else {
		s.legacyHTTPAuthSuccess.Add(1)
	}
	s.legacyLastUsedUnix.Store(s.now().Unix())
	return true
}

func (s *server) authorizeLegacyNode(w http.ResponseWriter, r *http.Request, nodeID int64) bool {
	node, err := s.store.GetNode(r.Context(), nodeID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		handleStoreError(w, err)
		return false
	}
	if err != nil || !node.Enabled || !node.RuntimeConfigured {
		writeAPIError(w, http.StatusForbidden, "invalid_node", "节点不存在、未配置或已停用", nil)
		return false
	}
	return true
}

func optionalPositiveQueryID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", name+" 必须是正整数", nil)
		return 0, false
	}
	return value, true
}

func (s *server) xboardNodeMachineStatus(w http.ResponseWriter, r *http.Request) {
	var input xboardNodeMachineStatusPayload
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.MachineID < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "machine_id 必须是正整数", nil)
		return
	}
	s.recordMachineStatus(w, r, input.MachineID, machineStatusPayload{
		CPU: input.CPU, Mem: input.Mem, Swap: input.Swap, Disk: input.Disk, Net: input.Net,
	}, false)
}

func (s *server) recordMachineStatus(w http.ResponseWriter, r *http.Request, machineID int64, input machineStatusPayload, enveloped bool) {
	if input.CPU == nil || *input.CPU < 0 || *input.CPU > 100 || input.Mem == nil || !validUsage(*input.Mem) ||
		(input.Swap != nil && !validUsage(*input.Swap)) || (input.Disk != nil && !validUsage(*input.Disk)) ||
		(input.Net != nil && (input.Net.InSpeed < 0 || input.Net.OutSpeed < 0)) {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "机器状态数据超出允许范围", nil)
		return
	}
	if !s.authenticateMachine(w, r, machineID) {
		return
	}
	if !s.allowServerRequest(w, r, s.machineRequests, machineID) {
		return
	}

	swap, disk := resourceUsage{}, resourceUsage{}
	if input.Swap != nil {
		swap = *input.Swap
	}
	if input.Disk != nil {
		disk = *input.Disk
	}
	status := store.MachineStatusInput{
		CPUPercent:  *input.CPU,
		MemoryTotal: input.Mem.Total,
		MemoryUsed:  input.Mem.Used,
		SwapTotal:   swap.Total,
		SwapUsed:    swap.Used,
		DiskTotal:   disk.Total,
		DiskUsed:    disk.Used,
	}
	if input.Net != nil {
		status.NetworkIn = &input.Net.InSpeed
		status.NetworkOut = &input.Net.OutSpeed
	}
	if err := s.store.RecordMachineStatus(r.Context(), machineID, status, s.now()); err != nil {
		handleStoreError(w, err)
		return
	}
	if enveloped {
		writeSuccess(w, http.StatusOK, true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"data": true})
}

func (s *server) authenticateMachine(w http.ResponseWriter, r *http.Request, machineID int64) bool {
	client, _ := nodeRequestAddresses(r, s.trustedProxyPrefixes)
	attemptKey := client + ":" + strconv.FormatInt(machineID, 10)
	if !s.machineAuthFailures.allowed(attemptKey, s.now()) {
		w.Header().Set("Retry-After", "60")
		writeAPIError(w, http.StatusTooManyRequests, "machine_auth_rate_limited", "机器认证失败次数过多，请稍后重试", nil)
		return false
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 256 {
		s.machineAuthFailures.failed(attemptKey, s.now())
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeAPIError(w, http.StatusUnauthorized, "invalid_machine_credential", "机器凭据无效或机器已停用", nil)
		return false
	}
	if _, err := s.store.AuthenticateMachine(r.Context(), machineID, parts[1], s.now()); errors.Is(err, store.ErrInvalidCredential) {
		s.machineAuthFailures.failed(attemptKey, s.now())
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeAPIError(w, http.StatusUnauthorized, "invalid_machine_credential", "机器凭据无效或机器已停用", nil)
		return false
	} else if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return false
	}
	return true
}

func (s *server) activeMachineNodes(ctx context.Context, machineID int64) ([]machineNodeSummary, error) {
	nodes, err := s.store.ListMachineNodes(ctx, machineID)
	if err != nil {
		return nil, err
	}
	summaries := make([]machineNodeSummary, 0, len(nodes))
	for _, node := range nodes {
		if node.Enabled && node.RuntimeConfigured {
			summaries = append(summaries, machineNodeSummary{ID: node.ID, Type: node.Type, Name: node.Name})
		}
	}
	return summaries, nil
}

func validUsage(value resourceUsage) bool {
	return value.Total >= 0 && value.Used >= 0 && value.Used <= value.Total
}
