package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/google/uuid"
)

const (
	maxReportBody     = 8 << 20
	maxReportUsers    = 100_000
	maxReportDevices  = 100_000
	maxDevicesPerUser = 64
	maxCPUCoreMetrics = 1_024
	maxTrafficEntry   = int64(math.MaxInt64 / 1_000)
	runtimeRateScale  = 1_000_000
)

type nodeReportPayload struct {
	MachineID int64               `json:"machine_id"`
	NodeID    int64               `json:"node_id"`
	NodeType  string              `json:"node_type"`
	Token     string              `json:"token"`
	ReportID  string              `json:"report_id"`
	Traffic   map[string][2]int64 `json:"traffic"`
	Alive     map[string][]string `json:"alive"`
	Online    map[string]int64    `json:"online"`
	Status    json.RawMessage     `json:"status"`
	Metrics   json.RawMessage     `json:"metrics"`
}

type nodeReportStatus struct {
	CPU          *float64       `json:"cpu"`
	Mem          *resourceUsage `json:"mem"`
	Swap         *resourceUsage `json:"swap"`
	Disk         *resourceUsage `json:"disk"`
	KernelStatus *bool          `json:"kernel_status,omitempty"`
}

func (s *server) saveNodeRuntime(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := pathID(w, r, "nodeID")
	if !ok {
		return
	}
	var input struct {
		Rate     float64         `json:"rate"`
		GroupIDs []int64         `json:"group_ids"`
		RouteIDs []int64         `json:"route_ids"`
		Config   json.RawMessage `json:"config"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if math.IsNaN(input.Rate) || math.IsInf(input.Rate, 0) || input.Rate <= 0 || input.Rate > 1_000 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "节点倍率必须大于 0 且不超过 1000", map[string]string{"rate": "超出允许范围"})
		return
	}
	rateMicros := int64(math.Round(input.Rate * runtimeRateScale))
	runtime, err := s.store.SaveNodeRuntime(r.Context(), nodeID, store.SaveNodeRuntimeInput{
		RateMicros: rateMicros,
		GroupIDs:   input.GroupIDs,
		RouteIDs:   input.RouteIDs,
		Config:     input.Config,
	}, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if s.hub != nil {
		if node, nodeErr := s.store.GetNode(r.Context(), nodeID); nodeErr == nil {
			machineID := int64(0)
			if node.MachineID != nil {
				machineID = *node.MachineID
			}
			if s.hub.hasNode(nodeID) {
				s.hub.NotifyNodeFull(r.Context(), machineID, nodeID)
			} else if machineID > 0 {
				s.hub.NotifyMachineNodes(r.Context(), machineID)
			}
		}
	}
	writeSuccess(w, http.StatusOK, map[string]any{
		"node_id":            runtime.NodeID,
		"rate":               float64(runtime.RateMicros) / runtimeRateScale,
		"group_ids":          runtime.GroupIDs,
		"route_ids":          runtime.RouteIDs,
		"runtime_configured": true,
		"updated_at":         runtime.UpdatedAt,
	})
}

func (s *server) xboardNodeConfig(w http.ResponseWriter, r *http.Request) {
	_, nodeID, requestIdentity, ok := s.authenticateNodeQuery(w, r)
	if !ok || !s.allowServerRequest(w, r, s.pullRequests, requestIdentity) {
		return
	}
	runtime, err := s.store.GetNodeRuntime(r.Context(), nodeID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	settings, err := s.store.GetNodeAgentSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	payload, err := nodeConfigObject(runtime, settings.PushInterval, settings.PullInterval)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "invalid_runtime_config", "节点运行时配置无效", nil)
		return
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	writeETagJSON(w, r, encoded)
}

func nodeConfigObject(runtime store.NodeRuntime, pushInterval, pullInterval int) (map[string]any, error) {
	var payload map[string]any
	if err := json.Unmarshal(runtime.Config, &payload); err != nil || payload == nil {
		return nil, errors.New("invalid runtime config")
	}
	payload["base_config"] = map[string]int{"push_interval": pushInterval, "pull_interval": pullInterval}
	if len(runtime.Routes) == 0 {
		delete(payload, "routes")
	} else {
		routes := make([]map[string]any, 0, len(runtime.Routes))
		for _, rule := range runtime.Routes {
			route := map[string]any{
				"id":     rule.ID,
				"match":  rule.Match,
				"action": rule.Action,
			}
			if rule.ActionValue != "" {
				route["action_value"] = rule.ActionValue
			}
			routes = append(routes, route)
		}
		payload["routes"] = routes
	}
	return payload, nil
}

func (s *server) xboardNodeUsers(w http.ResponseWriter, r *http.Request) {
	_, nodeID, requestIdentity, ok := s.authenticateNodeQuery(w, r)
	if !ok || !s.allowServerRequest(w, r, s.pullRequests, requestIdentity) {
		return
	}
	if _, err := s.store.GetNodeRuntime(r.Context(), nodeID); err != nil {
		handleStoreError(w, err)
		return
	}
	users, err := s.store.ListNodeRuntimeUsers(r.Context(), nodeID, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	encoded, err := json.Marshal(map[string]any{"users": users})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	writeETagJSON(w, r, encoded)
}

func (s *server) xboardNodeReport(w http.ResponseWriter, r *http.Request) {
	var payload nodeReportPayload
	if !decodeJSONLimit(w, r, &payload, maxReportBody) {
		return
	}
	if payload.NodeID < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "node_id 必须是正整数", nil)
		return
	}
	if payload.MachineID < 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "machine_id 不能是负数", nil)
		return
	}
	legacyAuth := payload.MachineID == 0
	requestIdentity := payload.MachineID
	if legacyAuth {
		if !s.authenticateLegacyNode(w, r, payload.NodeID) || !s.authorizeLegacyNode(w, r, payload.NodeID) {
			return
		}
	} else if !s.authenticateMachine(w, r, payload.MachineID) || !s.authorizeMachineNode(w, r, payload.MachineID, payload.NodeID) {
		return
	}
	if !s.allowServerRequest(w, r, s.reportRequests, requestIdentity) {
		return
	}
	report, err := validateNodeReport(payload)
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), nil)
		return
	}
	report.MachineID = payload.MachineID
	report.LegacyAuth = legacyAuth
	report.NodeID = payload.NodeID
	report.Now = s.now()
	result, err := s.applyNodeReport(r.Context(), report)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	s.notifyNodeReportResult(r.Context(), result)
	writeJSON(w, http.StatusOK, map[string]bool{"data": true})
}

func (s *server) xboardNodePush(w http.ResponseWriter, r *http.Request) {
	legacyAuth, nodeID, requestIdentity, ok := s.authenticateNodeQuery(w, r)
	if !ok || !s.allowServerRequest(w, r, s.reportRequests, requestIdentity) {
		return
	}
	var traffic map[string][2]int64
	if !decodeJSONLimit(w, r, &traffic, maxReportBody) {
		return
	}
	report, err := validateNodeReportWithPolicy(nodeReportPayload{Traffic: traffic}, false)
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), nil)
		return
	}
	report.NodeID, report.LegacyAuth, report.Now = nodeID, legacyAuth, s.now()
	if !legacyAuth {
		report.MachineID = requestIdentity
	}
	s.applyNodeReportResponse(w, r, report)
}

func (s *server) xboardNodeAlive(w http.ResponseWriter, r *http.Request) {
	legacyAuth, nodeID, requestIdentity, ok := s.authenticateNodeQuery(w, r)
	if !ok || !s.allowServerRequest(w, r, s.reportRequests, requestIdentity) {
		return
	}
	var alive map[string][]string
	if !decodeJSONLimit(w, r, &alive, maxReportBody) {
		return
	}
	report, err := validateNodeReport(nodeReportPayload{Alive: alive})
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), nil)
		return
	}
	report.NodeID, report.LegacyAuth, report.Now = nodeID, legacyAuth, s.now()
	if !legacyAuth {
		report.MachineID = requestIdentity
	}
	s.applyNodeReportResponse(w, r, report)
}

func (s *server) xboardNodeStatus(w http.ResponseWriter, r *http.Request) {
	legacyAuth, nodeID, requestIdentity, ok := s.authenticateNodeQuery(w, r)
	if !ok || !s.allowServerRequest(w, r, s.reportRequests, requestIdentity) {
		return
	}
	var status json.RawMessage
	if !decodeJSONLimit(w, r, &status, 64*1024) {
		return
	}
	report, err := validateNodeReport(nodeReportPayload{Status: status})
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), nil)
		return
	}
	report.NodeID, report.LegacyAuth, report.Now = nodeID, legacyAuth, s.now()
	if !legacyAuth {
		report.MachineID = requestIdentity
	}
	s.applyNodeReportResponse(w, r, report)
}

func (s *server) xboardNodeAliveList(w http.ResponseWriter, r *http.Request) {
	_, nodeID, requestIdentity, ok := s.authenticateNodeQuery(w, r)
	if !ok || !s.allowServerRequest(w, r, s.pullRequests, requestIdentity) {
		return
	}
	users, err := s.store.ListNodeRuntimeUsers(r.Context(), nodeID, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	userIDs := make([]int64, 0, len(users))
	for _, user := range users {
		if user.DeviceLimit > 0 {
			userIDs = append(userIDs, user.ID)
		}
	}
	alive, err := s.listUserDevices(r.Context(), userIDs, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"alive": alive})
}

func (s *server) applyNodeReportResponse(w http.ResponseWriter, r *http.Request, report store.NodeReportInput) {
	result, err := s.applyNodeReport(r.Context(), report)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	s.notifyNodeReportResult(r.Context(), result)
	writeJSON(w, http.StatusOK, map[string]bool{"data": true})
}

func (s *server) notifyNodeReportResult(ctx context.Context, result store.NodeReportResult) {
	if s.hub == nil {
		return
	}
	s.hub.NotifyTrafficExceeded(ctx, result.ExceededUsers)
	s.hub.NotifyDeviceStates(ctx, result.DeviceUserIDs)
}

func (s *server) applyNodeReport(ctx context.Context, report store.NodeReportInput) (store.NodeReportResult, error) {
	if s.deviceState == nil {
		return s.store.ApplyNodeReport(ctx, report)
	}
	report.ExternalDeviceState = true
	result, err := s.store.ApplyNodeReport(ctx, report)
	if err != nil {
		return store.NodeReportResult{}, err
	}
	if len(report.Alive) == 0 && !report.ReplaceAllDevices {
		return result, nil
	}
	userIDs, err := s.deviceState.ReplaceNodeDevices(ctx, report.NodeID, report.Alive, report.ReplaceAllDevices, report.Now)
	if err != nil {
		return store.NodeReportResult{}, err
	}
	result.DeviceUserIDs = userIDs
	return result, nil
}

func (s *server) listUserDevices(ctx context.Context, userIDs []int64, now time.Time) (map[int64][]string, error) {
	if s.deviceState != nil {
		return s.deviceState.ListUserDevices(ctx, userIDs, now)
	}
	return s.store.ListUserDevices(ctx, userIDs, now)
}

func validateNodeReport(payload nodeReportPayload) (store.NodeReportInput, error) {
	return validateNodeReportWithPolicy(payload, true)
}

func validateNodeReportWithPolicy(payload nodeReportPayload, requireTrafficReportID bool) (store.NodeReportInput, error) {
	normalizedReportID := ""
	if payload.ReportID != "" {
		parsedReportID, err := uuid.Parse(payload.ReportID)
		if err != nil {
			return store.NodeReportInput{}, errors.New("report_id 必须是 UUID")
		}
		normalizedReportID = parsedReportID.String()
	}
	if requireTrafficReportID && len(payload.Traffic) > 0 && payload.ReportID == "" {
		return store.NodeReportInput{}, errors.New("包含流量时 report_id 必填")
	}
	if len(payload.Traffic) > maxReportUsers || len(payload.Alive) > maxReportUsers || len(payload.Online) > maxReportUsers {
		return store.NodeReportInput{}, errors.New("报告中的用户数量超出允许范围")
	}
	report := store.NodeReportInput{
		ReportID: normalizedReportID,
		Traffic:  make(map[int64]store.TrafficUsage, len(payload.Traffic)),
		Alive:    make(map[int64][]string, len(payload.Alive)),
		Online:   make(map[int64]int64, len(payload.Online)),
		Metrics:  payload.Metrics,
	}
	var totalUpload, totalDownload int64
	for rawID, value := range payload.Traffic {
		userID, err := positiveUserID(rawID)
		if err != nil || value[0] < 0 || value[1] < 0 || value[0] > maxTrafficEntry || value[1] > maxTrafficEntry ||
			totalUpload > math.MaxInt64-value[0] || totalDownload > math.MaxInt64-value[1] {
			return store.NodeReportInput{}, errors.New("traffic 包含无效或溢出的用户流量")
		}
		totalUpload += value[0]
		totalDownload += value[1]
		report.Traffic[userID] = store.TrafficUsage{Upload: value[0], Download: value[1]}
	}
	totalDevices := 0
	for rawID, values := range payload.Alive {
		userID, err := positiveUserID(rawID)
		totalDevices += len(values)
		if err != nil || len(values) > maxDevicesPerUser || totalDevices > maxReportDevices {
			return store.NodeReportInput{}, errors.New("alive 包含无效用户或设备数量超限")
		}
		normalized := make([]string, 0, len(values))
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			ip, err := normalizeReportIP(value)
			if err != nil {
				return store.NodeReportInput{}, errors.New("alive 包含无效 IP 地址")
			}
			if _, exists := seen[ip]; !exists {
				seen[ip] = struct{}{}
				normalized = append(normalized, ip)
			}
		}
		report.Alive[userID] = normalized
	}
	for rawID, count := range payload.Online {
		userID, err := positiveUserID(rawID)
		if err != nil || count < 0 || count > 1_000_000 {
			return store.NodeReportInput{}, errors.New("online 包含无效用户或连接数")
		}
		report.Online[userID] = count
	}
	if len(payload.Status) > 0 && !bytes.Equal(payload.Status, []byte("null")) {
		var status nodeReportStatus
		decoder := json.NewDecoder(bytes.NewReader(payload.Status))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&status); err != nil || status.CPU == nil || *status.CPU < 0 || *status.CPU > 100 || status.Mem == nil || status.Swap == nil || status.Disk == nil ||
			!validUsage(*status.Mem) || !validUsage(*status.Swap) || !validUsage(*status.Disk) {
			return store.NodeReportInput{}, errors.New("status 包含无效资源状态")
		}
		report.Status = payload.Status
	}
	if len(payload.Metrics) > 0 && !bytes.Equal(payload.Metrics, []byte("null")) {
		var metrics map[string]json.RawMessage
		if err := json.Unmarshal(payload.Metrics, &metrics); err != nil {
			return store.NodeReportInput{}, errors.New("metrics 必须是 JSON 对象")
		}
		if cores, ok := metrics["cpu_per_core"]; ok {
			var values []float64
			if err := json.Unmarshal(cores, &values); err != nil || len(values) > maxCPUCoreMetrics {
				return store.NodeReportInput{}, errors.New("metrics.cpu_per_core 超出允许范围")
			}
			for _, value := range values {
				if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
					return store.NodeReportInput{}, errors.New("metrics.cpu_per_core 包含无效值")
				}
			}
		}
		report.Metrics = payload.Metrics
	}
	return report, nil
}

func positiveUserID(value string) (int64, error) {
	if value == "" || strings.HasPrefix(value, "+") || (len(value) > 1 && value[0] == '0') {
		return 0, errors.New("invalid user id")
	}
	userID, err := strconv.ParseInt(value, 10, 64)
	if err != nil || userID < 1 {
		return 0, errors.New("invalid user id")
	}
	return userID, nil
}

func normalizeReportIP(value string) (string, error) {
	value = strings.TrimSpace(value)
	if address, err := netip.ParseAddr(value); err == nil && address.Zone() == "" {
		return address.Unmap().String(), nil
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		if address, err := netip.ParseAddr(host); err == nil && address.Zone() == "" {
			return address.Unmap().String(), nil
		}
	}
	return "", errors.New("invalid IP")
}

func machineNodeQuery(w http.ResponseWriter, r *http.Request) (int64, int64, bool) {
	machineID, err := strconv.ParseInt(r.URL.Query().Get("machine_id"), 10, 64)
	if err != nil || machineID < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "machine_id 必须是正整数", nil)
		return 0, 0, false
	}
	nodeID, err := strconv.ParseInt(r.URL.Query().Get("node_id"), 10, 64)
	if err != nil || nodeID < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "node_id 必须是正整数", nil)
		return 0, 0, false
	}
	return machineID, nodeID, true
}

func (s *server) authenticateNodeQuery(w http.ResponseWriter, r *http.Request) (bool, int64, int64, bool) {
	nodeID, err := strconv.ParseInt(r.URL.Query().Get("node_id"), 10, 64)
	if err != nil || nodeID < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "node_id 必须是正整数", nil)
		return false, 0, 0, false
	}
	machineValue := r.URL.Query().Get("machine_id")
	if machineValue == "" {
		if !s.authenticateLegacyNode(w, r, nodeID) || !s.authorizeLegacyNode(w, r, nodeID) {
			return false, 0, 0, false
		}
		return true, nodeID, 0, true
	}
	machineID, err := strconv.ParseInt(machineValue, 10, 64)
	if err != nil || machineID < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "machine_id 必须是正整数", nil)
		return false, 0, 0, false
	}
	if !s.authenticateMachine(w, r, machineID) || !s.authorizeMachineNode(w, r, machineID, nodeID) {
		return false, 0, 0, false
	}
	return false, nodeID, machineID, true
}

func (s *server) authorizeMachineNode(w http.ResponseWriter, r *http.Request, machineID, nodeID int64) bool {
	node, err := s.store.GetNode(r.Context(), nodeID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		handleStoreError(w, err)
		return false
	}
	if err != nil || node.MachineID == nil || *node.MachineID != machineID || !node.Enabled {
		writeAPIError(w, http.StatusForbidden, "invalid_machine_node", "节点不属于当前机器或已停用", nil)
		return false
	}
	return true
}

func writeETagJSON(w http.ResponseWriter, r *http.Request, payload []byte) {
	digest := sha256.Sum256(payload)
	etag := `"` + hex.EncodeToString(digest[:]) + `"`
	w.Header().Set("ETag", etag)
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(payload, '\n'))
}

func etagMatches(header, target string) bool {
	for _, value := range strings.Split(header, ",") {
		value = strings.TrimSpace(value)
		if value == "*" || value == target || strings.TrimPrefix(value, "W/") == target {
			return true
		}
	}
	return false
}

func decodeJSONLimit(w http.ResponseWriter, r *http.Request, target any, limit int64) bool {
	return decodeJSONLimitMode(w, r, target, limit, true)
}

func decodeJSONLimitAllowUnknown(w http.ResponseWriter, r *http.Request, target any, limit int64) bool {
	return decodeJSONLimitMode(w, r, target, limit, false)
}

func decodeJSONLimitMode(w http.ResponseWriter, r *http.Request, target any, limit int64, disallowUnknownFields bool) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "请求必须使用 application/json", nil)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	if disallowUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", fmt.Sprintf("请求不得超过 %d 字节", limit), nil)
			return false
		}
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式无效", nil)
		return false
	}
	if err := decoder.Decode(&struct{}{}); errors.Is(err, io.EOF) {
		return true
	} else {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", fmt.Sprintf("请求不得超过 %d 字节", limit), nil)
			return false
		}
	}
	writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求只能包含一个 JSON 对象", nil)
	return false
}
