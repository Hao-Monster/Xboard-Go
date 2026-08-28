package httpapi

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const maxAdminNodeDefinitionBody = 4 << 20

type adminNodeDefinitionRequest struct {
	Revision          *int64          `json:"revision"`
	Type              *string         `json:"type"`
	ExternalCode      nullableString  `json:"external_code"`
	SpecificKey       nullableString  `json:"specific_key"`
	ParentID          nullableInt64   `json:"parent_id"`
	Name              *string         `json:"name"`
	Rate              *float64        `json:"rate"`
	Tags              []string        `json:"tags"`
	Host              *string         `json:"host"`
	Port              json.RawMessage `json:"port"`
	ServerPort        *int            `json:"server_port"`
	ListenAddress     *string         `json:"listen_address"`
	ProtocolSettings  json.RawMessage `json:"protocol_settings"`
	Show              *bool           `json:"show"`
	Enabled           *bool           `json:"enabled"`
	Sort              *int            `json:"sort"`
	MachineID         nullableInt64   `json:"machine_id"`
	GroupIDs          []int64         `json:"group_ids"`
	RouteIDs          []int64         `json:"route_ids"`
	RateTimeEnabled   *bool           `json:"rate_time_enabled"`
	RateTimeEnable    *bool           `json:"rate_time_enable"`
	RateTimeRanges    json.RawMessage `json:"rate_time_ranges"`
	CustomOutbounds   json.RawMessage `json:"custom_outbounds"`
	CustomRoutes      json.RawMessage `json:"custom_routes"`
	CertificateConfig json.RawMessage `json:"certificate_config"`
	LegacyCertConfig  json.RawMessage `json:"cert_config"`
	TransferEnable    *int64          `json:"transfer_enable"`
}

func (s *server) createNode(w http.ResponseWriter, r *http.Request) {
	var request adminNodeDefinitionRequest
	if !decodeJSONLimit(w, r, &request, maxAdminNodeDefinitionBody) {
		return
	}
	if !request.isFullDefinition() {
		s.createBasicNode(w, r, request)
		return
	}
	input, ok := request.storeInput(w, false)
	if !ok {
		return
	}
	created, mutation, err := s.store.CreateAdminNodeDefinition(r.Context(), input, s.now())
	if err != nil {
		handleAdminNodeMutationError(w, err)
		return
	}
	s.publishAdminNodeMutation(r, mutation)
	writeSuccess(w, http.StatusCreated, created)
}

func (request adminNodeDefinitionRequest) isFullDefinition() bool {
	return request.ServerPort != nil || request.Rate != nil || request.ListenAddress != nil || len(request.ProtocolSettings) > 0 ||
		request.ParentID.Set || request.GroupIDs != nil || request.RouteIDs != nil || request.Tags != nil || request.TransferEnable != nil
}

func (s *server) createBasicNode(w http.ResponseWriter, r *http.Request, request adminNodeDefinitionRequest) {
	port, validPort := parseNodePort(request.Port)
	fields := map[string]string{}
	if request.Name == nil {
		fields["name"] = "必填"
	}
	if request.Type == nil {
		fields["type"] = "必填"
	}
	if request.Host == nil {
		fields["host"] = "必填"
	}
	if !validPort {
		fields["port"] = "必须是端口或端口范围"
	}
	if len(fields) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请提交完整且有效的节点字段", fields)
		return
	}
	show, enabled, sortPosition := true, true, 0
	if request.Show != nil {
		show = *request.Show
	}
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	if request.Sort != nil {
		sortPosition = *request.Sort
	}
	var machineID *int64
	if request.MachineID.Set {
		machineID = request.MachineID.Value
	}
	input, err := store.NewBasicAdminNodeDefinitionInput(store.CreateNodeInput{
		Name: *request.Name, Type: *request.Type, Host: *request.Host, Port: port,
		Show: show, Enabled: enabled, Sort: sortPosition, MachineID: machineID,
	})
	if err != nil {
		handleStoreError(w, err)
		return
	}
	created, mutation, err := s.store.CreateAdminNodeDefinition(r.Context(), input, s.now())
	if err != nil {
		handleAdminNodeMutationError(w, err)
		return
	}
	s.publishAdminNodeMutation(r, mutation)
	writeSuccess(w, http.StatusCreated, created)
}

func (s *server) getAdminNodeDefinition(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := pathID(w, r, "nodeID")
	if !ok {
		return
	}
	detail, err := s.store.GetAdminNodeDefinition(r.Context(), nodeID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, detail)
}

func (s *server) replaceAdminNodeDefinition(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := pathID(w, r, "nodeID")
	if !ok {
		return
	}
	var request adminNodeDefinitionRequest
	if !decodeJSONLimit(w, r, &request, maxAdminNodeDefinitionBody) {
		return
	}
	input, ok := request.storeInput(w, true)
	if !ok {
		return
	}
	updated, mutation, err := s.store.UpdateAdminNodeDefinition(r.Context(), nodeID, input, s.now())
	if err != nil {
		handleAdminNodeMutationError(w, err)
		return
	}
	s.publishAdminNodeMutation(r, mutation)
	writeSuccess(w, http.StatusOK, updated)
}

func (request adminNodeDefinitionRequest) storeInput(w http.ResponseWriter, updating bool) (store.SaveAdminNodeDefinitionInput, bool) {
	fields := map[string]string{}
	port, validPort := parseNodePort(request.Port)
	if updating && (request.Revision == nil || *request.Revision < 1) {
		fields["revision"] = "必须是正整数"
	}
	if !updating && request.Revision != nil {
		fields["revision"] = "新建节点不得提交 revision"
	}
	if request.Type == nil {
		fields["type"] = "必填"
	}
	if request.Name == nil {
		fields["name"] = "必填"
	}
	if request.Host == nil {
		fields["host"] = "必填"
	}
	if !validPort {
		fields["port"] = "必须是端口或端口范围"
	}
	if request.Rate == nil || math.IsNaN(valueOrZero(request.Rate)) || math.IsInf(valueOrZero(request.Rate), 0) || valueOrZero(request.Rate) <= 0 || valueOrZero(request.Rate) > 1_000 {
		fields["rate"] = "必须大于 0 且不超过 1000"
	}
	if request.ServerPort == nil {
		fields["server_port"] = "必填"
	}
	if request.ListenAddress == nil {
		fields["listen_address"] = "必填"
	}
	if len(request.ProtocolSettings) == 0 {
		fields["protocol_settings"] = "必填"
	}
	if request.Show == nil {
		fields["show"] = "必填"
	}
	if request.Enabled == nil {
		fields["enabled"] = "必填"
	}
	if request.Sort == nil {
		fields["sort"] = "必填"
	}
	if !request.ParentID.Set {
		fields["parent_id"] = "必填，可为 null"
	}
	if !request.MachineID.Set {
		fields["machine_id"] = "必填，可为 null"
	}
	if request.Tags == nil {
		fields["tags"] = "必填"
	}
	if request.GroupIDs == nil {
		fields["group_ids"] = "必填"
	}
	if request.RouteIDs == nil {
		fields["route_ids"] = "必填"
	}
	rateTimeEnabled := request.RateTimeEnabled
	if rateTimeEnabled == nil {
		rateTimeEnabled = request.RateTimeEnable
	}
	if rateTimeEnabled == nil {
		fields["rate_time_enabled"] = "必填"
	}
	if len(request.RateTimeRanges) == 0 {
		fields["rate_time_ranges"] = "必填"
	}
	if len(request.CustomOutbounds) == 0 {
		fields["custom_outbounds"] = "必填"
	}
	if len(request.CustomRoutes) == 0 {
		fields["custom_routes"] = "必填"
	}
	certificate := request.CertificateConfig
	if len(certificate) == 0 {
		certificate = request.LegacyCertConfig
	}
	if len(certificate) == 0 {
		fields["certificate_config"] = "必填"
	}
	if request.TransferEnable == nil {
		fields["transfer_enable"] = "必填"
	}
	if !request.ExternalCode.Set && !request.SpecificKey.Set {
		fields["external_code"] = "必填，可为 null"
	}
	if len(fields) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请提交完整且有效的节点定义", fields)
		return store.SaveAdminNodeDefinitionInput{}, false
	}
	externalCode := request.ExternalCode.Value
	if !request.ExternalCode.Set {
		externalCode = request.SpecificKey.Value
	}
	result := store.SaveAdminNodeDefinitionInput{
		Type: *request.Type, ParentID: request.ParentID.Value, Name: *request.Name,
		RateMicros: int64(math.Round(*request.Rate * 1_000_000)), Tags: request.Tags, Host: *request.Host, Port: port,
		ServerPort: *request.ServerPort, ListenAddress: *request.ListenAddress, ProtocolSettings: request.ProtocolSettings,
		Show: *request.Show, Enabled: *request.Enabled, Sort: *request.Sort, MachineID: request.MachineID.Value,
		GroupIDs: request.GroupIDs, RouteIDs: request.RouteIDs, RateTimeEnabled: *rateTimeEnabled,
		RateTimeRanges: request.RateTimeRanges, CustomOutbounds: request.CustomOutbounds, CustomRoutes: request.CustomRoutes,
		CertificateConfig: certificate, TransferEnable: *request.TransferEnable,
	}
	if updating {
		result.Revision = *request.Revision
	}
	if externalCode != nil {
		result.ExternalCode = *externalCode
	}
	return result, true
}

func valueOrZero(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

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
	for _, sync := range mutation.FullSyncs {
		s.hub.NotifyNodeFull(r.Context(), sync.MachineID, sync.NodeID)
	}
}
