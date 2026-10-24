package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/diagnostics"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/google/uuid"
)

var (
	directDiagnosticIDRegex = regexp.MustCompile(`^[0-9a-f]{24}$`)
)

type initDiagnosticsScopeRequest struct {
	Scope string `json:"scope,omitempty"`
}

type initDiagnosticsScopeResponse struct {
	PanelScope string `json:"panel_scope"`
	Created    bool   `json:"created"`
}

type diagnosticsOrderLookupRequest struct {
	TradeNo *string `json:"trade_no,omitempty"`
	OrderID *int64  `json:"order_id,omitempty"`
}

type diagnosticsOrderLookupResponse struct {
	OrderID             int64  `json:"order_id"`
	TradeNo             string `json:"trade_no"`
	OrderStatus         int    `json:"order_status"`
	OrderType           string `json:"order_type"`
	CustomerDisplayName string `json:"customer_display_name"`
	SubscriptionName    string `json:"subscription_name"`
	DiagnosticID        string `json:"diagnostic_id"`
}

type adminDiagnosticsSettingsResponse struct {
	PanelScope             string `json:"panel_scope"`
	QueryEndpoint          string `json:"query_endpoint"`
	QueryUsername          string `json:"query_username"`
	HasQueryPassword       bool   `json:"has_query_password"`
	HasCursorSigningSecret bool   `json:"has_cursor_signing_secret"`
	StorageGeneration      int64  `json:"storage_generation"`
	Initialized            bool   `json:"initialized"`
}

type updateDiagnosticsSettingsRequest struct {
	QueryEndpoint       string  `json:"query_endpoint"`
	QueryUsername       string  `json:"query_username"`
	QueryPassword       *string `json:"query_password,omitempty"`
	CursorSigningSecret *string `json:"cursor_signing_secret,omitempty"`
	StorageGeneration   int64   `json:"storage_generation"`
	PanelScope          *string `json:"panel_scope,omitempty"`
}

type diagnosticsQueryRequest struct {
	TradeNo      *string `json:"trade_no,omitempty"`
	OrderID      *int64  `json:"order_id,omitempty"`
	DiagnosticID *string `json:"diagnostic_id,omitempty"`

	From        *string `json:"from,omitempty"`
	To          *string `json:"to,omitempty"`
	NodeID      *int64  `json:"node_id,omitempty"`
	Domain      *string `json:"domain,omitempty"`
	DomainMatch string  `json:"domain_match,omitempty"`
	TraceID     *string `json:"trace_id,omitempty"`
	HasError    *bool   `json:"has_error,omitempty"`
	ReasonCode  *string `json:"reason_code,omitempty"`

	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type diagnosticsQueryResponse struct {
	DiagnosticID  string                          `json:"diagnostic_id"`
	SnapshotID    string                          `json:"snapshot_id"`
	AsOf          string                          `json:"as_of"`
	Items         []diagnostics.ConnectionSummary `json:"items"`
	NextCursor    *string                         `json:"next_cursor"`
	Truncated     bool                            `json:"truncated"`
	ScanTruncated bool                            `json:"scan_truncated"`
	Coverage      diagnosticsCoverageResponse     `json:"coverage"`
}

type diagnosticsCoverageResponse struct {
	State   string                    `json:"state"`
	Reasons []string                  `json:"reasons"`
	Nodes   []diagnosticsNodeCoverage `json:"nodes"`
}

type diagnosticsNodeCoverage struct {
	NodeID             int64  `json:"node_id"`
	Capability         string `json:"capability"` // supported, unsupported
	Collector          string `json:"collector"`  // healthy, unknown, degraded
	KnownDroppedEvents *int64 `json:"known_dropped_events"`
}

type diagnosticsConnectionRequest struct {
	TradeNo      *string `json:"trade_no,omitempty"`
	OrderID      *int64  `json:"order_id,omitempty"`
	DiagnosticID *string `json:"diagnostic_id,omitempty"`

	NodeID       int64  `json:"node_id"`
	RuntimeID    string `json:"runtime_id"`
	ConnectionID string `json:"connection_id"`
}

type diagnosticsConnectionResponse struct {
	DiagnosticID string            `json:"diagnostic_id"`
	NodeID       int64             `json:"node_id"`
	RuntimeID    string            `json:"runtime_id"`
	ConnectionID string            `json:"connection_id"`
	Events       []json.RawMessage `json:"events"`
	DNSExchange  []json.RawMessage `json:"dns_exchange"`
}

func (s *server) initDiagnosticsScope(w http.ResponseWriter, r *http.Request) {
	var req initDiagnosticsScopeRequest
	if r.ContentLength > 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}

	scope, created, err := s.store.InitializeDiagnosticsScope(r.Context(), req.Scope, s.now())
	if err != nil {
		if errors.Is(err, diagnostics.ErrInvalidPanelScope) {
			writeAPIError(w, http.StatusUnprocessableEntity, "invalid_panel_scope", "panel scope 格式非法，须为有效的标准小写 UUIDv4", nil)
			return
		}
		handleDiagnosticsError(w, err)
		return
	}

	if created && s.hub != nil {
		s.hub.NotifyAllNodesFull(r.Context())
	}

	writeSuccess(w, http.StatusOK, initDiagnosticsScopeResponse{
		PanelScope: scope,
		Created:    created,
	})
}

func (s *server) lookupDiagnosticsOrder(w http.ResponseWriter, r *http.Request) {
	var req diagnosticsOrderLookupRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	ident, err := s.store.ResolveDiagnosticsCustomerByOrder(r.Context(), store.DiagnosticsOrderLookupInput{
		TradeNo: req.TradeNo,
		OrderID: req.OrderID,
	})
	if err != nil {
		handleDiagnosticsError(w, err)
		return
	}

	writeSuccess(w, http.StatusOK, diagnosticsOrderLookupResponse{
		OrderID:             ident.OrderID,
		TradeNo:             ident.TradeNo,
		OrderStatus:         ident.OrderStatus,
		OrderType:           ident.OrderType,
		CustomerDisplayName: ident.CustomerDisplayName,
		SubscriptionName:    ident.SubscriptionName,
		DiagnosticID:        ident.DiagnosticID,
	})
}

func (s *server) getDiagnosticsSettings(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.GetDiagnosticsServerConfig(r.Context())
	if err != nil {
		handleDiagnosticsError(w, err)
		return
	}

	initialized := strings.TrimSpace(cfg.PanelScope) != ""
	writeSuccess(w, http.StatusOK, adminDiagnosticsSettingsResponse{
		PanelScope:             cfg.PanelScope,
		QueryEndpoint:          cfg.QueryEndpoint,
		QueryUsername:          cfg.QueryUsername,
		HasQueryPassword:       len(cfg.QueryPasswordCipher) > 0,
		HasCursorSigningSecret: len(cfg.CursorSigningSecretCipher) > 0,
		StorageGeneration:      cfg.StorageGeneration,
		Initialized:            initialized,
	})
}

func (s *server) updateDiagnosticsSettings(w http.ResponseWriter, r *http.Request) {
	var req updateDiagnosticsSettingsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	cfg, err := s.store.GetDiagnosticsServerConfig(r.Context())
	if err != nil {
		handleDiagnosticsError(w, err)
		return
	}

	if req.PanelScope != nil && strings.TrimSpace(*req.PanelScope) != "" && strings.TrimSpace(*req.PanelScope) != cfg.PanelScope {
		writeAPIError(w, http.StatusBadRequest, "panel_scope_immutable", "panel_scope 属于不可变全局权威标识，禁止在通用设置中修改", nil)
		return
	}

	generation := req.StorageGeneration
	if generation <= 0 {
		generation = cfg.StorageGeneration
		if generation <= 0 {
			generation = 1
		}
	}

	passwordCipher := cfg.QueryPasswordCipher
	if req.QueryPassword != nil {
		passwordCipher = []byte(*req.QueryPassword)
	}

	secretCipher := cfg.CursorSigningSecretCipher
	if req.CursorSigningSecret != nil {
		secretCipher = []byte(*req.CursorSigningSecret)
	}

	err = s.store.UpdateDiagnosticsServerConfig(
		r.Context(),
		strings.TrimSpace(req.QueryEndpoint),
		strings.TrimSpace(req.QueryUsername),
		passwordCipher,
		secretCipher,
		generation,
		s.now(),
	)
	if err != nil {
		handleDiagnosticsError(w, err)
		return
	}

	s.getDiagnosticsSettings(w, r)
}

func (s *server) resolveDiagnosticTarget(ctx context.Context, tradeNo *string, orderID *int64, diagnosticID *string) (string, string, *int64, error) {
	hasOrder := (tradeNo != nil && strings.TrimSpace(*tradeNo) != "") || (orderID != nil && *orderID > 0)
	hasDiag := diagnosticID != nil && strings.TrimSpace(*diagnosticID) != ""

	if (hasOrder && hasDiag) || (!hasOrder && !hasDiag) {
		return "", "", nil, diagnostics.ErrInvalidLookupInput
	}

	panelScope, err := s.store.GetDiagnosticsScope(ctx)
	if err != nil {
		return "", "", nil, err
	}

	if hasOrder {
		ident, err := s.store.ResolveDiagnosticsCustomerByOrder(ctx, store.DiagnosticsOrderLookupInput{
			TradeNo: tradeNo,
			OrderID: orderID,
		})
		if err != nil {
			return "", "", nil, err
		}
		oid := ident.OrderID
		return panelScope, ident.DiagnosticID, &oid, nil
	}

	cleanDiag := strings.TrimSpace(strings.ToLower(*diagnosticID))
	if !directDiagnosticIDRegex.MatchString(cleanDiag) {
		return "", "", nil, diagnostics.ErrInvalidLookupInput
	}
	return panelScope, cleanDiag, nil, nil
}

func (s *server) queryDiagnosticsOrder(w http.ResponseWriter, r *http.Request) {
	var req diagnosticsQueryRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	session, ok := r.Context().Value(sessionContextKey).(store.SessionUser)
	if !ok || session.UserID <= 0 {
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "请先登录", nil)
		return
	}
	adminID := session.UserID

	panelScope, diagnosticID, orderID, err := s.resolveDiagnosticTarget(r.Context(), req.TradeNo, req.OrderID, req.DiagnosticID)
	if err != nil {
		handleDiagnosticsError(w, err)
		return
	}

	cfg, err := s.store.GetDiagnosticsServerConfig(r.Context())
	if err != nil {
		handleDiagnosticsError(w, err)
		return
	}
	if strings.TrimSpace(cfg.QueryEndpoint) == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "diagnostics_storage_not_configured", "诊断存储查询端点尚未配置", nil)
		return
	}

	signingSecret := cfg.CursorSigningSecretCipher
	if len(signingSecret) == 0 {
		signingSecret = []byte(panelScope)
	}

	limit := req.Limit
	if limit <= 0 {
		limit = diagnostics.DefaultQueryPageSize
	} else if limit > diagnostics.MaxQueryPageSize {
		limit = diagnostics.MaxQueryPageSize
	}

	now := s.now()
	var (
		summaries     []diagnostics.ConnectionSummary
		snapshotID    string
		scanTruncated bool
		truncated     bool
		offset        int
	)

	if req.Cursor != "" {
		cursorPayload, err := diagnostics.ParseAndVerifyCursor(
			signingSecret,
			req.Cursor,
			adminID,
			panelScope,
			diagnosticID,
			cfg.StorageGeneration,
			now,
		)
		if err != nil {
			handleDiagnosticsError(w, err)
			return
		}

		items, snapTruncated, found := s.diagnosticsSnapshots.Get(adminID, cursorPayload.SnapshotID, now)
		if !found {
			writeAPIError(w, http.StatusUnprocessableEntity, "cursor_expired", "查询快照已过期，请刷新重新查询", nil)
			return
		}
		summaries = items
		snapshotID = cursorPayload.SnapshotID
		offset = cursorPayload.Offset
		truncated = snapTruncated
	} else {
		var filters diagnostics.QueryFilters
		if req.From != nil && *req.From != "" {
			if t, err := time.Parse(time.RFC3339Nano, *req.From); err == nil {
				filters.From = t
			} else if t, err := time.Parse(time.RFC3339, *req.From); err == nil {
				filters.From = t
			} else {
				writeAPIError(w, http.StatusUnprocessableEntity, "invalid_query_filters", "from 时间格式无效", nil)
				return
			}
		}
		if req.To != nil && *req.To != "" {
			if t, err := time.Parse(time.RFC3339Nano, *req.To); err == nil {
				filters.To = t
			} else if t, err := time.Parse(time.RFC3339, *req.To); err == nil {
				filters.To = t
			} else {
				writeAPIError(w, http.StatusUnprocessableEntity, "invalid_query_filters", "to 时间格式无效", nil)
				return
			}
		}
		filters.NodeID = req.NodeID
		filters.Domain = req.Domain
		filters.DomainMatch = req.DomainMatch
		filters.TraceID = req.TraceID
		filters.HasError = req.HasError
		filters.ReasonCode = req.ReasonCode

		if err := filters.Normalize(now); err != nil {
			handleDiagnosticsError(w, err)
			return
		}

		logsql, err := diagnostics.EncodeLogsQL(panelScope, diagnosticID, filters)
		if err != nil {
			handleDiagnosticsError(w, err)
			return
		}

		stream, err := s.diagnosticsClient.Query(
			r.Context(),
			cfg.QueryEndpoint,
			cfg.QueryUsername,
			string(cfg.QueryPasswordCipher),
			logsql,
		)
		if err != nil {
			handleDiagnosticsError(w, err)
			return
		}
		defer stream.Close()

		aggRes, err := diagnostics.AggregateConnectionEvents(stream, filters)
		if err != nil {
			handleDiagnosticsError(w, err)
			return
		}
		scanTruncated = aggRes.ScanTruncated

		snapshotID = uuid.NewString()
		allSummaries := aggRes.Summaries
		if len(allSummaries) > diagnostics.MaxSnapshotItems {
			truncated = true
			summaries = allSummaries[:diagnostics.MaxSnapshotItems]
		} else {
			summaries = allSummaries
		}
		s.diagnosticsSnapshots.Put(adminID, snapshotID, summaries, truncated, now)
		offset = 0
	}

	var pageItems []diagnostics.ConnectionSummary
	var nextCursor *string

	if offset < len(summaries) {
		end := offset + limit
		if end > len(summaries) {
			end = len(summaries)
		}
		pageItems = summaries[offset:end]
		if end < len(summaries) {
			newPayload := diagnostics.CursorPayload{
				AdminID:           adminID,
				OrderID:           orderID,
				DiagnosticID:      diagnosticID,
				PanelScope:        panelScope,
				SnapshotID:        snapshotID,
				StorageGeneration: cfg.StorageGeneration,
				Offset:            end,
				CreatedAt:         now,
			}
			if curStr, err := diagnostics.GenerateCursor(signingSecret, newPayload); err == nil {
				nextCursor = &curStr
			}
		}
	} else {
		pageItems = []diagnostics.ConnectionSummary{}
	}

	coverage := s.resolveCoverage(r.Context())

	writeSuccess(w, http.StatusOK, diagnosticsQueryResponse{
		DiagnosticID:  diagnosticID,
		SnapshotID:    snapshotID,
		AsOf:          now.UTC().Format(time.RFC3339Nano),
		Items:         pageItems,
		NextCursor:    nextCursor,
		Truncated:     truncated,
		ScanTruncated: scanTruncated,
		Coverage:      coverage,
	})
}

func (s *server) connectionDiagnosticsOrder(w http.ResponseWriter, r *http.Request) {
	var req diagnosticsConnectionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.NodeID <= 0 || strings.TrimSpace(req.RuntimeID) == "" || strings.TrimSpace(req.ConnectionID) == "" {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_connection_params", "node_id, runtime_id, connection_id 均不能为空", nil)
		return
	}

	panelScope, diagnosticID, _, err := s.resolveDiagnosticTarget(r.Context(), req.TradeNo, req.OrderID, req.DiagnosticID)
	if err != nil {
		handleDiagnosticsError(w, err)
		return
	}

	cfg, err := s.store.GetDiagnosticsServerConfig(r.Context())
	if err != nil {
		handleDiagnosticsError(w, err)
		return
	}
	if strings.TrimSpace(cfg.QueryEndpoint) == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "diagnostics_storage_not_configured", "诊断存储查询端点尚未配置", nil)
		return
	}

	// Query events for this connection explicitly
	// We mandate panel_scope, node_id, runtime_id, and connection_id to avoid scanning other tenants
	connQuery := fmt.Sprintf(
		`panel_scope:%q AND node_id:%d AND runtime_id:%q AND connection_id:%q | sort by seq asc | limit %d`,
		panelScope,
		req.NodeID,
		req.RuntimeID,
		req.ConnectionID,
		diagnostics.MaxQueryReadLines,
	)

	stream, err := s.diagnosticsClient.Query(
		r.Context(),
		cfg.QueryEndpoint,
		cfg.QueryUsername,
		string(cfg.QueryPasswordCipher),
		connQuery,
	)
	if err != nil {
		handleDiagnosticsError(w, err)
		return
	}
	defer stream.Close()

	scanner := bufio.NewScanner(stream)
	rawEvents := make([]json.RawMessage, 0)
	dnsOpIDs := make(map[string]struct{})
	connOwnerVerified := false

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var item struct {
			EventFamily         string   `json:"event_family"`
			DiagnosticID        string   `json:"diagnostic_id"`
			ResolverOperationID string   `json:"resolver_operation_id"`
			SpanLinks           []string `json:"span_links"`
		}
		if err := json.Unmarshal(line, &item); err == nil {
			if item.EventFamily == "connection" {
				if item.DiagnosticID == diagnosticID {
					connOwnerVerified = true
				}
				if item.ResolverOperationID != "" && len(dnsOpIDs) < diagnostics.MaxDNSOperationsPerConn {
					dnsOpIDs[item.ResolverOperationID] = struct{}{}
				}
				for _, link := range item.SpanLinks {
					if strings.HasPrefix(link, "dns.exchange:") && len(dnsOpIDs) < diagnostics.MaxDNSOperationsPerConn {
						opID := strings.TrimPrefix(link, "dns.exchange:")
						if opID != "" {
							dnsOpIDs[opID] = struct{}{}
						}
					}
				}
			}
		}

		itemCopy := make([]byte, len(line))
		copy(itemCopy, line)
		rawEvents = append(rawEvents, json.RawMessage(itemCopy))
	}

	// Verify that this connection actually belongs to the caller's diagnosticID
	if len(rawEvents) > 0 && !connOwnerVerified {
		writeAPIError(w, http.StatusForbidden, "forbidden_connection_access", "连接不属于当前诊断客户", nil)
		return
	}

	dnsEvents := make([]json.RawMessage, 0)
	if len(dnsOpIDs) > 0 {
		var opConds []string
		for opID := range dnsOpIDs {
			opConds = append(opConds, fmt.Sprintf(`resolver_operation_id:%q`, opID))
		}
		dnsQuery := fmt.Sprintf(
			`panel_scope:%q AND node_id:%d AND runtime_id:%q AND (%s) | sort by operation_seq asc | limit 1000`,
			panelScope,
			req.NodeID,
			req.RuntimeID,
			strings.Join(opConds, " OR "),
		)

		dnsStream, err := s.diagnosticsClient.Query(
			r.Context(),
			cfg.QueryEndpoint,
			cfg.QueryUsername,
			string(cfg.QueryPasswordCipher),
			dnsQuery,
		)
		if err == nil {
			defer dnsStream.Close()
			dnsScanner := bufio.NewScanner(dnsStream)
			for dnsScanner.Scan() {
				line := dnsScanner.Bytes()
				if len(bytes.TrimSpace(line)) == 0 {
					continue
				}
				lineCopy := make([]byte, len(line))
				copy(lineCopy, line)
				dnsEvents = append(dnsEvents, json.RawMessage(lineCopy))
			}
		}
	}

	writeSuccess(w, http.StatusOK, diagnosticsConnectionResponse{
		DiagnosticID: diagnosticID,
		NodeID:       req.NodeID,
		RuntimeID:    req.RuntimeID,
		ConnectionID: req.ConnectionID,
		Events:       rawEvents,
		DNSExchange:  dnsEvents,
	})
}

func (s *server) resolveCoverage(ctx context.Context) diagnosticsCoverageResponse {
	targets, err := s.store.ListAllRuntimeNodeTargets(ctx)
	if err != nil {
		return diagnosticsCoverageResponse{
			State:   "unknown",
			Reasons: []string{"cannot_resolve_nodes"},
			Nodes:   []diagnosticsNodeCoverage{},
		}
	}

	hasUnsupported := false
	nodes := make([]diagnosticsNodeCoverage, 0, len(targets))

	for _, target := range targets {
		cap, found, _ := s.store.GetNodeDiagnosticsCapability(ctx, target.NodeID)
		isSupported := false
		if found && cap.SchemaVersion > 0 {
			for _, c := range cap.Capabilities {
				if c == "connection.v1" {
					isSupported = true
					break
				}
			}
		}

		capStr := "unsupported"
		if isSupported {
			capStr = "supported"
		} else {
			hasUnsupported = true
		}

		zero := int64(0)
		nodes = append(nodes, diagnosticsNodeCoverage{
			NodeID:             target.NodeID,
			Capability:         capStr,
			Collector:          "healthy",
			KnownDroppedEvents: &zero,
		})
	}

	state := "full"
	var reasons []string
	if hasUnsupported || len(nodes) == 0 {
		state = "partial"
		reasons = append(reasons, "node_unsupported")
	}

	return diagnosticsCoverageResponse{
		State:   state,
		Reasons: reasons,
		Nodes:   nodes,
	}
}

type startDiagnosticsSessionRequest struct {
	OrderID        *int64  `json:"order_id,omitempty"`
	TradeNo        *string `json:"trade_no,omitempty"`
	IdempotencyKey string  `json:"idempotency_key"`
}

type startDiagnosticsSessionResponse struct {
	SessionID     string                              `json:"session_id"`
	DiagnosticID  string                              `json:"diagnostic_id"`
	OrderID       int64                               `json:"order_id"`
	State         diagnostics.SessionState            `json:"state"`
	IssuedAt      time.Time                           `json:"issued_at"`
	ExpiresAt     time.Time                           `json:"expires_at"`
	Revision      int64                               `json:"revision"`
	TargetNodeIDs []int64                             `json:"target_node_ids"`
	NodeAcks      map[int64]diagnostics.NodeAckRecord `json:"node_acks,omitempty"`
}

type stopDiagnosticsSessionRequest struct {
	OrderID        *int64  `json:"order_id,omitempty"`
	TradeNo        *string `json:"trade_no,omitempty"`
	SessionID      string  `json:"session_id"`
	IdempotencyKey string  `json:"idempotency_key"`
}

type diagnosticsStatusResponse struct {
	OrderID      int64                       `json:"order_id"`
	TradeNo      string                      `json:"trade_no"`
	DiagnosticID string                      `json:"diagnostic_id"`
	HasSession   bool                        `json:"has_session"`
	Session      *diagnostics.Session        `json:"session,omitempty"`
	Coverage     diagnosticsCoverageResponse `json:"coverage"`
}

func (s *server) startDiagnosticsSession(w http.ResponseWriter, r *http.Request) {
	var req startDiagnosticsSessionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	session, ok := r.Context().Value(sessionContextKey).(store.SessionUser)
	if !ok || session.UserID <= 0 {
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "请先登录", nil)
		return
	}
	adminID := session.UserID

	hasOrder := (req.TradeNo != nil && strings.TrimSpace(*req.TradeNo) != "") || (req.OrderID != nil && *req.OrderID > 0)
	if !hasOrder {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_lookup_input", "输入参数无效（必须在 trade_no 与 order_id 中二选一）", nil)
		return
	}

	idempKey := strings.TrimSpace(req.IdempotencyKey)
	if idempKey == "" {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "idempotency_key 不能为空", nil)
		return
	}

	ident, err := s.store.ResolveDiagnosticsCustomerByOrder(r.Context(), store.DiagnosticsOrderLookupInput{
		TradeNo: req.TradeNo,
		OrderID: req.OrderID,
	})
	if err != nil {
		handleDiagnosticsError(w, err)
		return
	}

	// Read storage health before starting new lease if storage is configured
	cfg, err := s.store.GetDiagnosticsServerConfig(r.Context())
	if err == nil && strings.TrimSpace(cfg.QueryEndpoint) != "" {
		healthCtx, healthCancel := context.WithTimeout(r.Context(), 1*time.Second)
		defer healthCancel()
		if err := s.diagnosticsClient.CheckHealth(healthCtx, cfg.QueryEndpoint, cfg.QueryUsername, string(cfg.QueryPasswordCipher), ident.PanelScope); err != nil {
			handleDiagnosticsError(w, err)
			return
		}
	}

	var targetNodeIDs []int64
	if ident.SubscriberUserID > 0 {
		user, err := s.store.GetAdminUser(r.Context(), ident.SubscriberUserID)
		if err == nil && user.GroupID != nil && *user.GroupID > 0 {
			groupTargets, _ := s.store.ListRuntimeNodeTargetsForGroups(r.Context(), []int64{*user.GroupID})
			for _, t := range groupTargets {
				targetNodeIDs = append(targetNodeIDs, t.NodeID)
			}
		}
	}
	if len(targetNodeIDs) == 0 {
		allTargets, err := s.store.ListAllRuntimeNodeTargets(r.Context())
		if err == nil {
			for _, t := range allTargets {
				targetNodeIDs = append(targetNodeIDs, t.NodeID)
			}
		}
	}

	sess, isNew, err := s.diagnosticsLease.StartSession(r.Context(), diagnostics.StartSessionInput{
		DiagnosticID:   ident.DiagnosticID,
		OperatorID:     adminID,
		IdempotencyKey: idempKey,
		TargetNodeIDs:  targetNodeIDs,
		Now:            s.now(),
	})
	if err != nil {
		handleDiagnosticsError(w, err)
		return
	}

	if isNew && s.hub != nil {
		s.hub.NotifyDiagnosticsControl(r.Context(), s.diagnosticsLease.ControlEpoch(), s.diagnosticsLease.ControlVersion())
	}

	writeSuccess(w, http.StatusOK, startDiagnosticsSessionResponse{
		SessionID:     sess.SessionID,
		DiagnosticID:  sess.DiagnosticID,
		OrderID:       ident.OrderID,
		State:         sess.State,
		IssuedAt:      sess.IssuedAt,
		ExpiresAt:     sess.ExpiresAt,
		Revision:      sess.Revision,
		TargetNodeIDs: sess.TargetNodeIDs,
		NodeAcks:      sess.NodeAcks,
	})
}

func (s *server) stopDiagnosticsSession(w http.ResponseWriter, r *http.Request) {
	var req stopDiagnosticsSessionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	session, ok := r.Context().Value(sessionContextKey).(store.SessionUser)
	if !ok || session.UserID <= 0 {
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "请先登录", nil)
		return
	}
	adminID := session.UserID

	hasOrder := (req.TradeNo != nil && strings.TrimSpace(*req.TradeNo) != "") || (req.OrderID != nil && *req.OrderID > 0)
	if !hasOrder {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_lookup_input", "输入参数无效（必须在 trade_no 与 order_id 中二选一）", nil)
		return
	}

	cleanSessionID := strings.TrimSpace(req.SessionID)
	if cleanSessionID == "" {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "session_id 不能为空", nil)
		return
	}

	ident, err := s.store.ResolveDiagnosticsCustomerByOrder(r.Context(), store.DiagnosticsOrderLookupInput{
		TradeNo: req.TradeNo,
		OrderID: req.OrderID,
	})
	if err != nil {
		handleDiagnosticsError(w, err)
		return
	}

	existing, found, err := s.diagnosticsLease.GetSession(r.Context(), ident.DiagnosticID)
	if err != nil {
		handleDiagnosticsError(w, err)
		return
	}
	if !found || existing.SessionID != cleanSessionID {
		writeAPIError(w, http.StatusNotFound, "session_not_found", "诊断会话不存在或不属于该订单客户", nil)
		return
	}

	sess, err := s.diagnosticsLease.StopSession(r.Context(), diagnostics.StopSessionInput{
		DiagnosticID:   ident.DiagnosticID,
		SessionID:      cleanSessionID,
		OperatorID:     adminID,
		IdempotencyKey: req.IdempotencyKey,
		Now:            s.now(),
	})
	if err != nil {
		handleDiagnosticsError(w, err)
		return
	}

	if s.hub != nil {
		s.hub.NotifyDiagnosticsControl(r.Context(), s.diagnosticsLease.ControlEpoch(), s.diagnosticsLease.ControlVersion())
	}

	writeSuccess(w, http.StatusOK, startDiagnosticsSessionResponse{
		SessionID:     sess.SessionID,
		DiagnosticID:  sess.DiagnosticID,
		OrderID:       ident.OrderID,
		State:         sess.State,
		IssuedAt:      sess.IssuedAt,
		ExpiresAt:     sess.ExpiresAt,
		Revision:      sess.Revision,
		TargetNodeIDs: sess.TargetNodeIDs,
		NodeAcks:      sess.NodeAcks,
	})
}

func (s *server) getDiagnosticsSessionStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := r.Context().Value(sessionContextKey).(store.SessionUser)
	if !ok || session.UserID <= 0 {
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "请先登录", nil)
		return
	}

	var orderID *int64
	var tradeNo *string
	if rawID := r.URL.Query().Get("order_id"); rawID != "" {
		id, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil || id < 1 {
			writeAPIError(w, http.StatusUnprocessableEntity, "invalid_lookup_input", "order_id 必须是正整数", nil)
			return
		}
		orderID = &id
	}
	if tn := strings.TrimSpace(r.URL.Query().Get("trade_no")); tn != "" {
		tradeNo = &tn
	}
	if orderID == nil && tradeNo == nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_lookup_input", "必须提供 order_id 或 trade_no", nil)
		return
	}

	ident, err := s.store.ResolveDiagnosticsCustomerByOrder(r.Context(), store.DiagnosticsOrderLookupInput{
		TradeNo: tradeNo,
		OrderID: orderID,
	})
	if err != nil {
		handleDiagnosticsError(w, err)
		return
	}

	coverage := s.resolveCoverage(r.Context())
	sess, found, err := s.diagnosticsLease.GetSession(r.Context(), ident.DiagnosticID)
	if err != nil {
		handleDiagnosticsError(w, err)
		return
	}

	resp := diagnosticsStatusResponse{
		OrderID:      ident.OrderID,
		TradeNo:      ident.TradeNo,
		DiagnosticID: ident.DiagnosticID,
		HasSession:   found,
		Coverage:     coverage,
	}
	if found {
		resp.Session = &sess
	}

	writeSuccess(w, http.StatusOK, resp)
}

func handleDiagnosticsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, diagnostics.ErrInvalidLookupInput):
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_lookup_input", "输入参数无效（必须在 trade_no 与 order_id 中二选一）", nil)
	case errors.Is(err, diagnostics.ErrOrderNotFound):
		writeAPIError(w, http.StatusNotFound, "order_not_found", "订单不存在", nil)
	case errors.Is(err, diagnostics.ErrSubscriptionUserMissing):
		writeAPIError(w, http.StatusConflict, "subscription_user_missing", "分销订单关联的订阅用户丢失或损坏，拒绝回退至分销商", nil)
	case errors.Is(err, diagnostics.ErrDiagnosticsNotInitialized):
		writeAPIError(w, http.StatusServiceUnavailable, "diagnostics_not_initialized", "客户连接诊断尚未完成 scope 初始化", nil)
	case errors.Is(err, diagnostics.ErrInvalidPanelScope):
		writeAPIError(w, http.StatusServiceUnavailable, "invalid_panel_scope", "diagnostics panel_scope 配置非法", nil)
	case errors.Is(err, diagnostics.ErrDiagnosticsStorageNotReady):
		writeAPIError(w, http.StatusServiceUnavailable, "diagnostics_storage_not_ready", "诊断日志存储未就绪或健康检查失败", nil)
	case errors.Is(err, diagnostics.ErrStorageNotConfigured):
		writeAPIError(w, http.StatusServiceUnavailable, "diagnostics_storage_not_configured", "诊断存储查询端点尚未配置", nil)
	case errors.Is(err, diagnostics.ErrStorageUnavailable):
		writeAPIError(w, http.StatusServiceUnavailable, "diagnostics_storage_unavailable", "诊断日志存储后端服务不可用", nil)
	case errors.Is(err, diagnostics.ErrStorageTimeout):
		writeAPIError(w, http.StatusGatewayTimeout, "diagnostics_storage_timeout", "诊断日志存储查询超时", nil)
	case errors.Is(err, diagnostics.ErrLeaseLimitReached):
		writeAPIError(w, http.StatusConflict, "lease_limit_reached", "活跃诊断会话已达系统上限（最多10个）", nil)
	case errors.Is(err, diagnostics.ErrIdempotencyConflict):
		writeAPIError(w, http.StatusConflict, "idempotency_conflict", "相同幂等键绑定了不同的诊断参数", nil)
	case errors.Is(err, diagnostics.ErrSnapshotConflict):
		writeAPIError(w, http.StatusConflict, "snapshot_conflict", "租约快照冲突或不合法", nil)
	case errors.Is(err, diagnostics.ErrCursorExpired):
		writeAPIError(w, http.StatusUnprocessableEntity, "cursor_expired", "查询快照游标已过期，请刷新重新查询", nil)
	case errors.Is(err, diagnostics.ErrCursorInvalid):
		writeAPIError(w, http.StatusUnprocessableEntity, "cursor_invalid", "查询快照游标非法或签名不符", nil)
	case errors.Is(err, diagnostics.ErrInvalidQueryFilters):
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_query_filters", err.Error(), nil)
	default:
		recordDiagnosticError(w, err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
	}
}
