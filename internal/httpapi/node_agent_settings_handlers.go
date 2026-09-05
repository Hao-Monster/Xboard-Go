package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type nodeAgentSettingsResponse struct {
	store.NodeAgentSettings
	WebSocketAvailable         bool                    `json:"websocket_available"`
	IssuedToken                string                  `json:"issued_token,omitempty"`
	LegacyHTTPAuthSuccess      uint64                  `json:"legacy_http_auth_success_count"`
	LegacyWebSocketAuthSuccess uint64                  `json:"legacy_websocket_auth_success_count"`
	LegacyLastUsedAt           *time.Time              `json:"legacy_last_used_at"`
	NodeAuthTelemetry          store.NodeAuthTelemetry `json:"node_auth_telemetry"`
}

func (s *server) nodeAgentSettingsResponse(settings store.NodeAgentSettings, issuedToken string, telemetry store.NodeAuthTelemetry) nodeAgentSettingsResponse {
	return nodeAgentSettingsResponse{
		NodeAgentSettings: settings, WebSocketAvailable: s.webSocketEnabled, IssuedToken: issuedToken,
		LegacyHTTPAuthSuccess:      telemetry.LegacyGlobalToken.HTTPAuthSuccess,
		LegacyWebSocketAuthSuccess: telemetry.LegacyGlobalToken.WebSocketAuthSuccess,
		LegacyLastUsedAt:           telemetry.LegacyGlobalToken.LastUsedAt,
		NodeAuthTelemetry:          telemetry,
	}
}

func (s *server) getNodeAgentSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetNodeAgentSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	telemetry, err := s.nodeAuthTelemetry.snapshot(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, s.nodeAgentSettingsResponse(settings, "", telemetry))
}

func (s *server) updateNodeAgentSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Revision            int64   `json:"revision"`
		ServerToken         *string `json:"server_token"`
		GenerateServerToken bool    `json:"generate_server_token"`
		PullInterval        *int    `json:"server_pull_interval"`
		PushInterval        *int    `json:"server_push_interval"`
		DeviceLimitMode     *int    `json:"device_limit_mode"`
		WebSocketEnabled    *bool   `json:"server_ws_enable"`
		WebSocketURL        *string `json:"server_ws_url"`
	}
	if !decodeJSONLimit(w, r, &input, 8*1024) {
		return
	}
	if input.GenerateServerToken && input.ServerToken != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "通讯密钥不能同时生成和手动设置", nil)
		return
	}
	telemetry, err := s.nodeAuthTelemetry.snapshot(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	current, err := s.store.GetNodeAgentSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	next := store.UpdateNodeAgentSettingsInput{
		Revision: input.Revision, PullInterval: current.PullInterval, PushInterval: current.PushInterval,
		DeviceLimitMode: current.DeviceLimitMode, WebSocketEnabled: current.WebSocketEnabled, WebSocketURL: current.WebSocketURL,
	}
	if input.PullInterval != nil {
		next.PullInterval = *input.PullInterval
	}
	if input.PushInterval != nil {
		next.PushInterval = *input.PushInterval
	}
	if input.DeviceLimitMode != nil {
		next.DeviceLimitMode = *input.DeviceLimitMode
	}
	if input.WebSocketEnabled != nil {
		next.WebSocketEnabled = *input.WebSocketEnabled
	}
	if input.WebSocketURL != nil {
		next.WebSocketURL = *input.WebSocketURL
	}
	if !current.WebSocketEnabled && next.WebSocketEnabled && !s.webSocketEnabled {
		writeAPIError(w, http.StatusConflict, "websocket_unavailable", "当前部署未启用 WebSocket 服务能力", nil)
		return
	}

	issuedToken := ""
	if input.GenerateServerToken {
		generated, err := security.NewOpaqueToken(36)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
			return
		}
		issuedToken = generated.Plaintext
		next.ServerToken = &issuedToken
	} else if input.ServerToken != nil {
		next.ServerToken = input.ServerToken
		if *input.ServerToken != "" {
			issuedToken = *input.ServerToken
		}
	}
	session, ok := sessionFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	next.UpdatedBy = &session.UserID
	next.Audit = &store.AdminAuditInput{
		AdministratorID: session.UserID, AdministratorEmail: session.Email,
		Method: http.MethodPut, Route: "/api/v1/admin/node-agent-settings", StatusCode: http.StatusOK,
	}
	updated, err := s.store.UpdateNodeAgentSettings(r.Context(), next, s.now())
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, http.StatusConflict, "settings_conflict", "设置已被其他管理员修改，请刷新后重试", nil)
		return
	}
	if errors.Is(err, store.ErrInvalidInput) {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请检查节点配置", nil)
		return
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if s.hub != nil {
		if current.WebSocketEnabled && !updated.WebSocketEnabled {
			s.hub.DisconnectAll("websocket disabled")
		} else if next.ServerToken != nil {
			s.hub.DisconnectLegacy("server token changed")
		}
	}
	writeSuccess(w, http.StatusOK, s.nodeAgentSettingsResponse(updated, issuedToken, telemetry))
}
