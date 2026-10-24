package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/diagnostics"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
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

func (s *server) initDiagnosticsScope(w http.ResponseWriter, r *http.Request) {
	var req initDiagnosticsScopeRequest
	// Scope candidate is optional; empty JSON or empty scope generates a new UUIDv4
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
		// panel_scope adds diagnostic_id to every runtime user; push a full resync so already-connected
		// nodes hot-update the mapping instead of waiting for reconnect or the next pull interval.
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

	// Strictly prohibit altering panel_scope via general settings update
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
	default:
		recordDiagnosticError(w, err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
	}
}
