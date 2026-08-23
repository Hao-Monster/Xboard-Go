package httpapi

import (
	"net/http"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) agentNodes(w http.ResponseWriter, r *http.Request) {
	machineID, ok := pathID(w, r, "machineID")
	if !ok {
		return
	}
	if !s.authenticateMachine(w, r, machineID) {
		return
	}
	nodes, err := s.store.ListMachineNodes(r.Context(), machineID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	type nodeSummary struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
		Name string `json:"name"`
	}
	summaries := make([]nodeSummary, 0, len(nodes))
	for _, node := range nodes {
		if !node.Enabled {
			continue
		}
		summaries = append(summaries, nodeSummary{ID: node.ID, Type: node.Type, Name: node.Name})
	}
	writeSuccess(w, http.StatusOK, map[string]any{
		"nodes": summaries,
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
	type usage struct {
		Total int64 `json:"total"`
		Used  int64 `json:"used"`
	}
	type network struct {
		InSpeed  float64 `json:"in_speed"`
		OutSpeed float64 `json:"out_speed"`
	}
	var input struct {
		CPU  *float64 `json:"cpu"`
		Mem  *usage   `json:"mem"`
		Swap *usage   `json:"swap"`
		Disk *usage   `json:"disk"`
		Net  *network `json:"net"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.CPU == nil || *input.CPU < 0 || *input.CPU > 100 || input.Mem == nil || !validUsage(*input.Mem) ||
		(input.Swap != nil && !validUsage(*input.Swap)) || (input.Disk != nil && !validUsage(*input.Disk)) ||
		(input.Net != nil && (input.Net.InSpeed < 0 || input.Net.OutSpeed < 0)) {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "机器状态数据超出允许范围", nil)
		return
	}
	if !s.authenticateMachine(w, r, machineID) {
		return
	}

	swap, disk := usage{}, usage{}
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
	writeSuccess(w, http.StatusOK, true)
}

func (s *server) authenticateMachine(w http.ResponseWriter, r *http.Request, machineID int64) bool {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 256 {
		writeAPIError(w, http.StatusForbidden, "invalid_machine_credential", "机器凭据无效或机器已停用", nil)
		return false
	}
	if _, err := s.store.AuthenticateMachine(r.Context(), machineID, parts[1], s.now()); err != nil {
		writeAPIError(w, http.StatusForbidden, "invalid_machine_credential", "机器凭据无效或机器已停用", nil)
		return false
	}
	return true
}

func validUsage(value struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
}) bool {
	return value.Total >= 0 && value.Used >= 0 && value.Used <= value.Total
}
