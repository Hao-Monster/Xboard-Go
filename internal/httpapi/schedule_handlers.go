package httpapi

import (
	"net/http"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/schedule"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) getActivationSchedule(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := pathID(w, r, "nodeID")
	if !ok {
		return
	}
	if !s.requireLinkedNode(w, r, nodeID) {
		return
	}
	item, err := s.store.GetActivationSchedule(r.Context(), nodeID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, s.activationScheduleResponse(item))
}

func (s *server) saveActivationSchedule(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := pathID(w, r, "nodeID")
	if !ok {
		return
	}
	var input struct {
		ScheduleType string `json:"schedule_type"`
		Timezone     string `json:"timezone"`
		EnableTime   string `json:"enable_time"`
		DisableTime  string `json:"disable_time"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.ScheduleType != "daily" {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "当前仅允许保存 daily 激活计划", map[string]string{"schedule_type": "必须为 daily"})
		return
	}
	item, err := s.store.SaveDailySchedule(r.Context(), nodeID, input.Timezone, input.EnableTime, input.DisableTime, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, s.activationScheduleResponse(item))
}

func (s *server) deleteActivationSchedule(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := pathID(w, r, "nodeID")
	if !ok {
		return
	}
	if !s.requireLinkedNode(w, r, nodeID) {
		return
	}
	if err := s.store.DeleteActivationSchedule(r.Context(), nodeID); err != nil {
		handleStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) requireLinkedNode(w http.ResponseWriter, r *http.Request, nodeID int64) bool {
	node, err := s.store.GetNode(r.Context(), nodeID)
	if err != nil {
		handleStoreError(w, err)
		return false
	}
	if node.MachineID == nil {
		handleStoreError(w, store.ErrNodeNotLinked)
		return false
	}
	return true
}

func (s *server) activationScheduleResponse(item store.ActivationSchedule) map[string]any {
	phase := "inactive"
	var nextTransitionAt any
	if !item.NextTransitionAt.IsZero() {
		nextTransitionAt = item.NextTransitionAt
	}
	if item.ScheduleType == "daily" {
		location, err := time.LoadLocation(item.Timezone)
		if err == nil {
			window, err := schedule.NewDailyWindow(location, item.EnableTime, item.DisableTime)
			if err == nil && window.StateAt(s.now()).Enabled {
				phase = "active"
			}
		}
	} else if item.ScheduleType == "once" && item.EnableAt != nil && item.DisableAt != nil && !s.now().Before(*item.EnableAt) && s.now().Before(*item.DisableAt) {
		phase = "active"
	}
	return map[string]any{
		"server_id":           item.NodeID,
		"schedule_type":       item.ScheduleType,
		"timezone":            item.Timezone,
		"enable_time":         item.EnableTime,
		"disable_time":        item.DisableTime,
		"enable_at":           item.EnableAt,
		"disable_at":          item.DisableAt,
		"revision":            item.Revision,
		"next_transition_at":  nextTransitionAt,
		"next_target_enabled": item.NextTargetEnabled,
		"phase":               phase,
	}
}
