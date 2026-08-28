package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) listMachines(w http.ResponseWriter, r *http.Request) {
	machines, err := s.store.ListMachines(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, machines)
}

func (s *server) createMachine(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name     string `json:"name"`
		Notes    string `json:"notes"`
		IsActive *bool  `json:"is_active"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}
	machine, enrollment, err := s.store.CreateMachine(r.Context(), store.CreateMachineInput{
		Name: input.Name, Notes: input.Notes, IsActive: isActive,
	}, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusCreated, machineEnrollmentResponse(machine, enrollment, s.installCommand(machine.ID, enrollment.Code)))
}

func (s *server) getMachine(w http.ResponseWriter, r *http.Request) {
	machineID, ok := pathID(w, r, "machineID")
	if !ok {
		return
	}
	machine, err := s.store.GetMachine(r.Context(), machineID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, machine)
}

func (s *server) updateMachine(w http.ResponseWriter, r *http.Request) {
	machineID, ok := pathID(w, r, "machineID")
	if !ok {
		return
	}
	var input struct {
		Name     string `json:"name"`
		Notes    string `json:"notes"`
		IsActive bool   `json:"is_active"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	machine, err := s.store.UpdateMachine(r.Context(), machineID, store.UpdateMachineInput{
		Name: input.Name, Notes: input.Notes, IsActive: input.IsActive,
	}, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if s.hub != nil && !machine.IsActive {
		s.hub.DisconnectMachine(machineID, "machine disabled")
	}
	writeSuccess(w, http.StatusOK, machine)
}

func (s *server) deleteMachine(w http.ResponseWriter, r *http.Request) {
	machineID, ok := pathID(w, r, "machineID")
	if !ok {
		return
	}
	if s.hub != nil {
		nodes, err := s.store.ListMachineNodes(r.Context(), machineID)
		if err != nil {
			handleStoreError(w, err)
			return
		}
		nodeIDs := make([]int64, 0, len(nodes))
		for _, node := range nodes {
			nodeIDs = append(nodeIDs, node.ID)
		}
		s.hub.ClearNodeDevices(r.Context(), nodeIDs)
	}
	if err := s.store.DeleteMachine(r.Context(), machineID, s.now()); err != nil {
		handleStoreError(w, err)
		return
	}
	if s.hub != nil {
		s.hub.DisconnectMachine(machineID, "machine deleted")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) createEnrollment(w http.ResponseWriter, r *http.Request) {
	machineID, ok := pathID(w, r, "machineID")
	if !ok {
		return
	}
	var input struct {
		RevokeExisting bool `json:"revoke_existing"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	enrollment, err := s.store.CreateEnrollment(r.Context(), machineID, input.RevokeExisting, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusCreated, map[string]any{
		"machine_id":      machineID,
		"token":           enrollment.Code,
		"token_type":      enrollment.TokenType,
		"expires_at":      enrollment.ExpiresAt,
		"install_command": s.installCommand(machineID, enrollment.Code),
	})
}

func (s *server) listMachineNodes(w http.ResponseWriter, r *http.Request) {
	machineID, ok := pathID(w, r, "machineID")
	if !ok {
		return
	}
	if _, err := s.store.GetMachine(r.Context(), machineID); err != nil {
		handleStoreError(w, err)
		return
	}
	nodes, err := s.store.ListMachineNodes(r.Context(), machineID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, nodes)
}

func (s *server) listUnassignedNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.ListUnassignedNodes(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, nodes)
}

func (s *server) createNode(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name    string          `json:"name"`
		Type    string          `json:"type"`
		Host    string          `json:"host"`
		Port    json.RawMessage `json:"port"`
		Show    *bool           `json:"show"`
		Enabled *bool           `json:"enabled"`
		Sort    int             `json:"sort"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	port, validPort := parseNodePort(input.Port)
	if !validPort {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "节点端口格式无效", map[string]string{"port": "必须是端口或端口范围"})
		return
	}
	show, enabled := true, true
	if input.Show != nil {
		show = *input.Show
	}
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	node, err := s.store.CreateNode(r.Context(), store.CreateNodeInput{
		Name: input.Name, Type: input.Type, Host: input.Host, Port: port,
		Show: show, Enabled: enabled, Sort: input.Sort,
	}, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusCreated, node)
}

func parseNodePort(raw json.RawMessage) (string, bool) {
	var stringValue string
	if err := json.Unmarshal(raw, &stringValue); err == nil {
		return stringValue, true
	}
	var integerValue int
	if err := json.Unmarshal(raw, &integerValue); err == nil {
		return strconv.Itoa(integerValue), true
	}
	return "", false
}

func (s *server) assignNode(w http.ResponseWriter, r *http.Request) {
	machineID, nodeID, ok := machineAndNodeIDs(w, r)
	if !ok {
		return
	}
	revision, ok := decodeNodeRevision(w, r)
	if !ok {
		return
	}
	if err := s.store.AssignNode(r.Context(), machineID, nodeID, revision, s.now()); err != nil {
		handleStoreError(w, err)
		return
	}
	if s.hub != nil {
		s.hub.NotifyMachineNodes(r.Context(), machineID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) unassignNode(w http.ResponseWriter, r *http.Request) {
	machineID, nodeID, ok := machineAndNodeIDs(w, r)
	if !ok {
		return
	}
	revision, ok := decodeNodeRevision(w, r)
	if !ok {
		return
	}
	if err := s.store.UnassignNode(r.Context(), machineID, nodeID, revision, s.now()); err != nil {
		handleStoreError(w, err)
		return
	}
	if s.hub != nil {
		s.hub.ClearNodeDevices(r.Context(), []int64{nodeID})
		s.hub.NotifyMachineNodes(r.Context(), machineID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) setNodeEnabled(w http.ResponseWriter, r *http.Request) {
	machineID, nodeID, ok := machineAndNodeIDs(w, r)
	if !ok {
		return
	}
	var input struct {
		Revision int64 `json:"revision"`
		Enabled  *bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	fields := map[string]string{}
	if input.Revision < 1 {
		fields["revision"] = "必须是正整数"
	}
	if input.Enabled == nil {
		fields["enabled"] = "必填"
	}
	if len(fields) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请提交有效的节点状态", fields)
		return
	}
	if err := s.store.SetNodeEnabled(r.Context(), machineID, nodeID, input.Revision, *input.Enabled, s.now()); err != nil {
		handleStoreError(w, err)
		return
	}
	if s.hub != nil {
		if !*input.Enabled {
			s.hub.ClearNodeDevices(r.Context(), []int64{nodeID})
		}
		s.hub.NotifyMachineNodes(r.Context(), machineID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeNodeRevision(w http.ResponseWriter, r *http.Request) (int64, bool) {
	var input struct {
		Revision int64 `json:"revision"`
	}
	if !decodeJSONLimit(w, r, &input, 1024) {
		return 0, false
	}
	if input.Revision < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "revision 必须是正整数", map[string]string{"revision": "必须是正整数"})
		return 0, false
	}
	return input.Revision, true
}

func (s *server) listHistory(w http.ResponseWriter, r *http.Request) {
	machineID, ok := pathID(w, r, "machineID")
	if !ok {
		return
	}
	if _, err := s.store.GetMachine(r.Context(), machineID); err != nil {
		handleStoreError(w, err)
		return
	}
	rangeHours := 1
	limit := 60
	var err error
	if value := r.URL.Query().Get("range_hours"); value != "" {
		rangeHours, err = strconv.Atoi(value)
		if err != nil || rangeHours < 1 || rangeHours > 24 {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "range_hours 必须在 1 到 24 之间", nil)
			return
		}
	}
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 10 || limit > 1440 {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "limit 必须在 10 到 1440 之间", nil)
			return
		}
	}
	history, err := s.store.ListLoadHistory(r.Context(), machineID, s.now().Add(-time.Duration(rangeHours)*time.Hour), limit)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, history)
}

func machineAndNodeIDs(w http.ResponseWriter, r *http.Request) (int64, int64, bool) {
	machineID, ok := pathID(w, r, "machineID")
	if !ok {
		return 0, 0, false
	}
	nodeID, ok := pathID(w, r, "nodeID")
	return machineID, nodeID, ok
}

func machineEnrollmentResponse(machine store.Machine, enrollment store.EnrollmentSecret, installCommand string) map[string]any {
	return map[string]any{
		"id":              machine.ID,
		"name":            machine.Name,
		"notes":           machine.Notes,
		"is_active":       machine.IsActive,
		"last_seen_at":    machine.LastSeenAt,
		"load_status":     machine.LoadStatus,
		"servers_count":   machine.ServersCount,
		"created_at":      machine.CreatedAt,
		"updated_at":      machine.UpdatedAt,
		"token":           enrollment.Code,
		"token_type":      enrollment.TokenType,
		"expires_at":      enrollment.ExpiresAt,
		"install_command": installCommand,
	}
}

func (s *server) installCommand(machineID int64, enrollmentCode string) string {
	return fmt.Sprintf(
		`(set -Eeuo pipefail; XBOARD_NODE_VERSION=%s; XBOARD_NODE_RELEASE_DIR="$(mktemp -d)"; trap 'unset XBOARD_NODE_RELEASE_TOKEN; rm -rf "$XBOARD_NODE_RELEASE_DIR"' EXIT; gh release download "$XBOARD_NODE_VERSION" -R Hao-Monster/Xboard-Node --pattern install.sh --pattern SHA256SUMS --dir "$XBOARD_NODE_RELEASE_DIR"; (cd "$XBOARD_NODE_RELEASE_DIR" && grep " install.sh$" SHA256SUMS | sha256sum -c -); XBOARD_NODE_RELEASE_TOKEN="$(gh auth token)"; export XBOARD_NODE_RELEASE_TOKEN; sudo --preserve-env=XBOARD_NODE_RELEASE_TOKEN bash "$XBOARD_NODE_RELEASE_DIR/install.sh" --version "$XBOARD_NODE_VERSION" --mode machine --panel %s --enrollment-code %s --machine-id %d)`,
		shellQuote(s.nodeRelease), shellQuote(s.panelURL), shellQuote(enrollmentCode), machineID,
	)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
