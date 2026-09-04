package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestNodeAgentSettingsAdminContractKeepsTokenOneTimeAndDeploymentBounded(t *testing.T) {
	api, database := newTestAPI(t)
	createHTTPTestUser(t, database, "node-settings-reader@example.test", "node-settings-reader-password-123")
	admin := loginAdmin(t, api)
	reader := loginAs(t, api, "node-settings-reader@example.test", "node-settings-reader-password-123")

	initialResponse := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/node-agent-settings", "")
	if initialResponse.Code != http.StatusOK {
		t.Fatalf("initial settings status=%d body=%s", initialResponse.Code, initialResponse.Body)
	}
	initial := decodeNodeAgentSettings(t, initialResponse.Body.Bytes())
	if initial.Revision != 1 || initial.PullInterval != 60 || initial.PushInterval != 60 || initial.ServerTokenConfigured || initial.WebSocketAvailable {
		t.Fatalf("initial settings=%#v", initial)
	}
	if strings.Contains(initialResponse.Body.String(), "server_token_hash") || strings.Contains(initialResponse.Body.String(), "issued_token") {
		t.Fatalf("initial response exposed credential material: %s", initialResponse.Body)
	}
	expectAPIError(t, reader.request(t, api, http.MethodGet, "/api/v1/admin/admin/node-agent-settings", ""), http.StatusForbidden, "forbidden")

	token := "legacy-node-token-1234567890"
	replaced := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/node-agent-settings", `{
		"revision":1,"server_token":"legacy-node-token-1234567890","server_pull_interval":30,
		"server_push_interval":45,"device_limit_mode":1,"server_ws_enable":false,
		"server_ws_url":"wss://panel.example.test/ws"
	}`)
	if replaced.Code != http.StatusOK {
		t.Fatalf("replace token status=%d body=%s", replaced.Code, replaced.Body)
	}
	replacedSettings := decodeNodeAgentSettings(t, replaced.Body.Bytes())
	if replacedSettings.Revision != 2 || !replacedSettings.ServerTokenConfigured || replacedSettings.ServerTokenPrefix != token[:8] ||
		replacedSettings.IssuedToken != token || replacedSettings.PullInterval != 30 || replacedSettings.PushInterval != 45 || replacedSettings.DeviceLimitMode != 1 {
		t.Fatalf("replaced settings=%#v", replacedSettings)
	}
	if valid, err := database.AuthenticateLegacyNodeToken(t.Context(), token); err != nil || !valid {
		t.Fatalf("persisted token auth=(%t,%v)", valid, err)
	}
	audits, err := database.ListAdminAuditLogs(t.Context(), store.AdminAuditFilter{Page: 1, PageSize: 20, Query: "node-agent-settings"})
	if err != nil {
		t.Fatal(err)
	}
	if audits.Total != 1 || len(audits.Items) != 1 || audits.Items[0].Route != "/api/v1/admin/node-agent-settings" || audits.Items[0].StatusCode != http.StatusOK {
		t.Fatalf("successful settings audit=%#v, want one atomic entry", audits)
	}
	node, err := database.CreateNode(t.Context(), store.CreateNodeInput{
		Name: "legacy telemetry", Type: "vless", Host: "telemetry.example.test", Port: "443", Show: true, Enabled: true,
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(t.Context(), node.ID, store.SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{7}, Config: []byte(`{"protocol":"vless","server_port":443}`),
	}, fixedNow()); err != nil {
		t.Fatal(err)
	}
	handshake := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v2/server/handshake?node_id=%d", node.ID), nil)
	request.Header.Set("Authorization", "Bearer "+token)
	api.ServeHTTP(handshake, request)
	if handshake.Code != http.StatusOK {
		t.Fatalf("legacy telemetry handshake status=%d body=%s", handshake.Code, handshake.Body)
	}

	readBack := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/node-agent-settings", "")
	if readBack.Code != http.StatusOK || strings.Contains(readBack.Body.String(), token) || strings.Contains(readBack.Body.String(), "server_token_hash") || strings.Contains(readBack.Body.String(), "issued_token") {
		t.Fatalf("read-back exposed one-time token: status=%d body=%s", readBack.Code, readBack.Body)
	}
	readBackSettings := decodeNodeAgentSettings(t, readBack.Body.Bytes())
	if readBackSettings.LegacyHTTPAuthSuccess != 1 || readBackSettings.LegacyWebSocketAuthSuccess != 0 || readBackSettings.LegacyLastUsedAt == nil {
		t.Fatalf("legacy telemetry=%#v", readBackSettings)
	}

	stale := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/node-agent-settings", `{
		"revision":1,"server_pull_interval":60,"server_push_interval":60,"device_limit_mode":0,"server_ws_enable":false
	}`)
	expectAPIError(t, stale, http.StatusConflict, "settings_conflict")

	generated := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/node-agent-settings", `{
		"revision":2,"generate_server_token":true,"server_pull_interval":60,"server_push_interval":60,
		"device_limit_mode":0,"server_ws_enable":false
	}`)
	if generated.Code != http.StatusOK {
		t.Fatalf("generate token status=%d body=%s", generated.Code, generated.Body)
	}
	generatedSettings := decodeNodeAgentSettings(t, generated.Body.Bytes())
	if generatedSettings.Revision != 3 || len(generatedSettings.IssuedToken) != 48 || generatedSettings.IssuedToken == token {
		t.Fatalf("generated settings=%#v", generatedSettings)
	}
	if valid, _ := database.AuthenticateLegacyNodeToken(t.Context(), token); valid {
		t.Fatal("replaced token remained valid")
	}

	unavailable := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/node-agent-settings", `{
		"revision":3,"server_pull_interval":60,"server_push_interval":60,"device_limit_mode":0,"server_ws_enable":true
	}`)
	expectAPIError(t, unavailable, http.StatusConflict, "websocket_unavailable")

	clear := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/node-agent-settings", `{
		"revision":3,"server_token":"","server_pull_interval":60,"server_push_interval":60,
		"device_limit_mode":0,"server_ws_enable":false
	}`)
	if clear.Code != http.StatusOK {
		t.Fatalf("clear token status=%d body=%s", clear.Code, clear.Body)
	}
	cleared := decodeNodeAgentSettings(t, clear.Body.Bytes())
	if cleared.Revision != 4 || cleared.ServerTokenConfigured || cleared.ServerTokenPrefix != "" || cleared.IssuedToken != "" {
		t.Fatalf("cleared settings=%#v", cleared)
	}

	unknown := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/node-agent-settings", `{
		"revision":4,"server_token_hash":"attacker-controlled","server_pull_interval":60,"server_push_interval":60
	}`)
	expectAPIError(t, unknown, http.StatusBadRequest, "invalid_json")
	withoutCSRF := admin
	withoutCSRF.csrf = ""
	expectAPIError(t, withoutCSRF.request(t, api, http.MethodPut, "/api/v1/admin/admin/node-agent-settings", `{"revision":4}`), http.StatusForbidden, "csrf_failed")
}

func TestNodeAgentSettingsCanPreserveUnavailableWebSocketButCannotEnableIt(t *testing.T) {
	api, database := newTestAPI(t)
	initial, err := database.GetNodeAgentSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stored, err := database.UpdateNodeAgentSettings(t.Context(), store.UpdateNodeAgentSettingsInput{
		Revision: initial.Revision, PullInterval: 60, PushInterval: 60, WebSocketEnabled: true,
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	admin := loginAdmin(t, api)

	preserved := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/node-agent-settings", fmt.Sprintf(`{
		"revision":%d,"server_pull_interval":31,"server_push_interval":29,
		"device_limit_mode":0,"server_ws_enable":true,"server_ws_url":""
	}`, stored.Revision))
	if preserved.Code != http.StatusOK {
		t.Fatalf("preserve unavailable WebSocket status=%d body=%s", preserved.Code, preserved.Body)
	}
	updated := decodeNodeAgentSettings(t, preserved.Body.Bytes())
	if !updated.WebSocketEnabled || updated.WebSocketAvailable || updated.PullInterval != 31 || updated.PushInterval != 29 {
		t.Fatalf("preserved settings=%#v", updated)
	}

	disabled := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/node-agent-settings", fmt.Sprintf(`{
		"revision":%d,"server_pull_interval":31,"server_push_interval":29,
		"device_limit_mode":0,"server_ws_enable":false,"server_ws_url":""
	}`, updated.Revision))
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable unavailable WebSocket status=%d body=%s", disabled.Code, disabled.Body)
	}
	disabledSettings := decodeNodeAgentSettings(t, disabled.Body.Bytes())
	reenabled := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/node-agent-settings", fmt.Sprintf(`{
		"revision":%d,"server_pull_interval":31,"server_push_interval":29,
		"device_limit_mode":0,"server_ws_enable":true,"server_ws_url":""
	}`, disabledSettings.Revision))
	expectAPIError(t, reenabled, http.StatusConflict, "websocket_unavailable")
}

type nodeAgentSettingsContract struct {
	store.NodeAgentSettings
	WebSocketAvailable         bool       `json:"websocket_available"`
	IssuedToken                string     `json:"issued_token"`
	LegacyHTTPAuthSuccess      uint64     `json:"legacy_http_auth_success_count"`
	LegacyWebSocketAuthSuccess uint64     `json:"legacy_websocket_auth_success_count"`
	LegacyLastUsedAt           *time.Time `json:"legacy_last_used_at"`
}

func decodeNodeAgentSettings(t *testing.T, body []byte) nodeAgentSettingsContract {
	t.Helper()
	var envelope struct {
		Data nodeAgentSettingsContract `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode node agent settings: %v", err)
	}
	return envelope.Data
}
