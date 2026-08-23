package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

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
	writeSuccess(w, http.StatusOK, map[string]any{
		"nodes": nodes,
		"base_config": map[string]int{
			"push_interval": 60,
			"pull_interval": 60,
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
	nodes, err := s.activeMachineNodes(r.Context(), input.MachineID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": nodes,
		"base_config": map[string]int{
			"push_interval": 60,
			"pull_interval": 60,
		},
	})
}

func (s *server) xboardNodeHandshake(w http.ResponseWriter, r *http.Request) {
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
	if input.NodeID > 0 {
		node, err := s.store.GetNode(r.Context(), input.NodeID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			handleStoreError(w, err)
			return
		}
		if err != nil || node.MachineID == nil || *node.MachineID != input.MachineID || !node.Enabled {
			writeAPIError(w, http.StatusForbidden, "invalid_machine_node", "节点不属于当前机器或已停用", nil)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"websocket": map[string]bool{"enabled": false},
		"settings": map[string]int{
			"push_interval": 60,
			"pull_interval": 60,
		},
	})
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
	attemptKey := requestIP(r) + ":" + strconv.FormatInt(machineID, 10)
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
		if node.Enabled {
			summaries = append(summaries, machineNodeSummary{ID: node.ID, Type: node.Type, Name: node.Name})
		}
	}
	return summaries, nil
}

func validUsage(value resourceUsage) bool {
	return value.Total >= 0 && value.Used >= 0 && value.Used <= value.Total
}
