package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) listAdminNodes(w http.ResponseWriter, r *http.Request) {
	filter, ok := decodeAdminNodeFilter(w, r)
	if !ok {
		return
	}
	page, err := s.store.ListAdminNodes(r.Context(), filter, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, page)
}

func decodeAdminNodeFilter(w http.ResponseWriter, r *http.Request) (store.AdminNodeFilter, bool) {
	query := r.URL.Query()
	filter := store.AdminNodeFilter{Page: 1, PageSize: 500, Query: query.Get("q"), Type: query.Get("type")}
	var err error
	if value := query.Get("page"); value != "" {
		filter.Page, err = strconv.Atoi(value)
		if err != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "page 必须是正整数", nil)
			return store.AdminNodeFilter{}, false
		}
	}
	if value := query.Get("page_size"); value != "" {
		filter.PageSize, err = strconv.Atoi(value)
		if err != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "page_size 必须是整数", nil)
			return store.AdminNodeFilter{}, false
		}
	}
	if filter.Show, err = optionalQueryBool(query.Get("show")); err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "show 必须是 true 或 false", nil)
		return store.AdminNodeFilter{}, false
	}
	if filter.Enabled, err = optionalQueryBool(query.Get("enabled")); err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "enabled 必须是 true 或 false", nil)
		return store.AdminNodeFilter{}, false
	}
	if value := query.Get("machine_id"); value != "" {
		machineID, parseErr := strconv.ParseInt(value, 10, 64)
		if parseErr != nil || machineID < 1 {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "machine_id 必须是正整数", nil)
			return store.AdminNodeFilter{}, false
		}
		filter.MachineID = &machineID
	}
	if value := query.Get("unassigned"); value != "" {
		parsed, parseErr := strconv.ParseBool(value)
		if parseErr != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "unassigned 必须是 true 或 false", nil)
			return store.AdminNodeFilter{}, false
		}
		filter.Unassigned = parsed
	}
	return filter, true
}

func optionalQueryBool(value string) (*bool, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func (s *server) updateAdminNode(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := pathID(w, r, "nodeID")
	if !ok {
		return
	}
	var input struct {
		Revision  *int64          `json:"revision"`
		Name      *string         `json:"name"`
		Host      *string         `json:"host"`
		Port      json.RawMessage `json:"port"`
		Show      *bool           `json:"show"`
		Enabled   *bool           `json:"enabled"`
		Sort      *int            `json:"sort"`
		MachineID nullableInt64   `json:"machine_id"`
	}
	if !decodeJSONLimit(w, r, &input, 16*1024) {
		return
	}
	port, validPort := parseNodePort(input.Port)
	fields := map[string]string{}
	if input.Revision == nil || *input.Revision < 1 {
		fields["revision"] = "必须是正整数"
	}
	if input.Name == nil {
		fields["name"] = "必填"
	}
	if input.Host == nil {
		fields["host"] = "必填"
	}
	if !validPort {
		fields["port"] = "必须是端口或端口范围"
	}
	if input.Show == nil {
		fields["show"] = "必填"
	}
	if input.Enabled == nil {
		fields["enabled"] = "必填"
	}
	if input.Sort == nil {
		fields["sort"] = "必填"
	}
	if !input.MachineID.Set {
		fields["machine_id"] = "必填，可为 null"
	} else if input.MachineID.Value != nil && *input.MachineID.Value < 1 {
		fields["machine_id"] = "必须是正整数或 null"
	}
	if len(fields) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请提交完整且有效的节点字段", fields)
		return
	}
	node, mutation, err := s.store.UpdateAdminNode(r.Context(), nodeID, store.UpdateAdminNodeInput{
		Revision: *input.Revision, Name: *input.Name, Host: *input.Host, Port: port,
		Show: *input.Show, Enabled: *input.Enabled, Sort: *input.Sort,
		MachineIDSet: true, MachineID: input.MachineID.Value,
	}, s.now())
	if err != nil {
		handleAdminNodeMutationError(w, err)
		return
	}
	s.publishAdminNodeMutation(r, mutation)
	writeSuccess(w, http.StatusOK, node)
}

func (s *server) copyAdminNode(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := pathID(w, r, "nodeID")
	if !ok {
		return
	}
	var input struct {
		Revision int64 `json:"revision"`
	}
	if !decodeJSONLimit(w, r, &input, 1024) {
		return
	}
	if input.Revision < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "revision 必须是正整数", nil)
		return
	}
	node, mutation, err := s.store.CopyAdminNode(r.Context(), nodeID, input.Revision, s.now())
	if err != nil {
		handleAdminNodeMutationError(w, err)
		return
	}
	s.publishAdminNodeMutation(r, mutation)
	writeSuccess(w, http.StatusCreated, node)
}

func (s *server) reorderAdminNodes(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Targets []store.AdminNodeRevision `json:"targets"`
	}
	if !decodeJSONLimit(w, r, &input, 32*1024) {
		return
	}
	mutation, err := s.store.ReorderAdminNodes(r.Context(), input.Targets, s.now())
	if err != nil {
		handleAdminNodeMutationError(w, err)
		return
	}
	s.publishAdminNodeMutation(r, mutation)
	writeSuccess(w, http.StatusOK, mutation)
}

func (s *server) updateAdminNodeStates(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Targets   []store.AdminNodeRevision `json:"targets"`
		Show      *bool                     `json:"show"`
		Enabled   *bool                     `json:"enabled"`
		MachineID nullableInt64             `json:"machine_id"`
	}
	if !decodeJSONLimit(w, r, &input, 32*1024) {
		return
	}
	if input.Show == nil && input.Enabled == nil && !input.MachineID.Set {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "至少提交一个状态字段", nil)
		return
	}
	if input.MachineID.Set && input.MachineID.Value != nil && *input.MachineID.Value < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "machine_id 必须是正整数或 null", nil)
		return
	}
	mutation, err := s.store.UpdateAdminNodeStates(r.Context(), store.AdminNodeStateInput{
		Targets: input.Targets, Show: input.Show, Enabled: input.Enabled,
		MachineIDSet: input.MachineID.Set, MachineID: input.MachineID.Value,
	}, s.now())
	if err != nil {
		handleAdminNodeMutationError(w, err)
		return
	}
	s.publishAdminNodeMutation(r, mutation)
	writeSuccess(w, http.StatusOK, mutation)
}

func (s *server) resetAdminNodeTraffic(w http.ResponseWriter, r *http.Request) {
	targets, ok := decodeAdminNodeTargets(w, r)
	if !ok {
		return
	}
	mutation, err := s.store.ResetAdminNodeTraffic(r.Context(), targets, s.now())
	if err != nil {
		handleAdminNodeMutationError(w, err)
		return
	}
	s.publishAdminNodeMutation(r, mutation)
	writeSuccess(w, http.StatusOK, mutation)
}

func (s *server) deleteAdminNodes(w http.ResponseWriter, r *http.Request) {
	targets, ok := decodeAdminNodeTargets(w, r)
	if !ok {
		return
	}
	mutation, err := s.store.DeleteAdminNodes(r.Context(), targets, s.now())
	if err != nil {
		handleAdminNodeMutationError(w, err)
		return
	}
	s.publishAdminNodeMutation(r, mutation)
	w.WriteHeader(http.StatusNoContent)
}

func decodeAdminNodeTargets(w http.ResponseWriter, r *http.Request) ([]store.AdminNodeRevision, bool) {
	var input struct {
		Targets []store.AdminNodeRevision `json:"targets"`
	}
	if !decodeJSONLimit(w, r, &input, 32*1024) {
		return nil, false
	}
	return input.Targets, true
}

func handleAdminNodeMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, http.StatusConflict, "node_revision_conflict", "节点已被其他管理员修改或仍被子节点引用，请刷新后重试", nil)
		return
	}
	handleStoreError(w, err)
}

func (s *server) publishAdminNodeMutation(r *http.Request, mutation store.AdminNodeMutation) {
	if s.hub == nil {
		return
	}
	if len(mutation.ClearNodeIDs) > 0 {
		s.hub.ClearNodeDevices(r.Context(), mutation.ClearNodeIDs)
	}
	if len(mutation.AffectedUserIDs) > 0 {
		s.hub.NotifyDeviceStates(r.Context(), mutation.AffectedUserIDs)
	}
	for _, machineID := range mutation.MachineIDs {
		s.hub.NotifyMachineNodes(r.Context(), machineID)
	}
}
