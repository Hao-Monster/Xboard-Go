package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestSubscriptionPolicySettingsModernAndLegacyContracts(t *testing.T) {
	api, database := newTestAPI(t)
	createHTTPTestUser(t, database, "subscription-policy-reader@example.test", "subscription-policy-reader-password-123")
	administrator := loginAdmin(t, api)
	reader := loginAs(t, api, "subscription-policy-reader@example.test", "subscription-policy-reader-password-123")

	unauthenticated := testClient{}.request(t, api, http.MethodGet, "/api/v1/admin/admin/subscription-policy-settings", "")
	expectAPIError(t, unauthenticated, http.StatusUnauthorized, "unauthenticated")
	forbidden := reader.request(t, api, http.MethodGet, "/api/v1/admin/admin/subscription-policy-settings", "")
	expectAPIError(t, forbidden, http.StatusForbidden, "forbidden")

	initialResponse := administrator.request(t, api, http.MethodGet, "/api/v1/admin/admin/subscription-policy-settings", "")
	if initialResponse.Code != http.StatusOK {
		t.Fatalf("GET subscription policy = %d %s", initialResponse.Code, initialResponse.Body)
	}
	initial := decodeSubscriptionPolicyEnvelope(t, initialResponse)
	if initial.Revision != 1 || !initial.PlanChangeEnabled || !initial.SurplusEnabled || initial.ResetTrafficMethod != 0 ||
		initial.NewOrderEventID != 0 || initial.RenewOrderEventID != 0 || initial.ChangeOrderEventID != 0 ||
		!initial.DefaultRemindExpire || !initial.DefaultRemindTraffic {
		t.Fatalf("initial subscription policy = %#v", initial)
	}

	updateBody := `{
		"revision":1,"plan_change_enable":false,"reset_traffic_method":4,"surplus_enable":false,
		"new_order_event_id":1,"renew_order_event_id":1,"change_order_event_id":1,
		"default_remind_expire":false,"default_remind_traffic":true
	}`
	updatedResponse := administrator.request(t, api, http.MethodPut, "/api/v1/admin/admin/subscription-policy-settings", updateBody)
	if updatedResponse.Code != http.StatusOK {
		t.Fatalf("PUT subscription policy = %d %s", updatedResponse.Code, updatedResponse.Body)
	}
	updated := decodeSubscriptionPolicyEnvelope(t, updatedResponse)
	if updated.Revision != 2 || updated.PlanChangeEnabled || updated.SurplusEnabled || updated.ResetTrafficMethod != 4 ||
		updated.NewOrderEventID != 1 || updated.RenewOrderEventID != 1 || updated.ChangeOrderEventID != 1 ||
		updated.DefaultRemindExpire || !updated.DefaultRemindTraffic {
		t.Fatalf("updated subscription policy = %#v", updated)
	}

	stale := administrator.request(t, api, http.MethodPut, "/api/v1/admin/admin/subscription-policy-settings", updateBody)
	expectAPIError(t, stale, http.StatusConflict, "settings_conflict")
	invalid := administrator.request(t, api, http.MethodPut, "/api/v1/admin/admin/subscription-policy-settings", `{
		"revision":2,"plan_change_enable":true,"reset_traffic_method":5,"surplus_enable":true,
		"new_order_event_id":2,"renew_order_event_id":0,"change_order_event_id":0,
		"default_remind_expire":true,"default_remind_traffic":true
	}`)
	expectAPIError(t, invalid, http.StatusUnprocessableEntity, "validation_failed")
	unknown := administrator.request(t, api, http.MethodPut, "/api/v1/admin/admin/subscription-policy-settings", `{
		"revision":2,"plan_change_enable":true,"reset_traffic_method":0,"surplus_enable":true,
		"new_order_event_id":0,"renew_order_event_id":0,"change_order_event_id":0,
		"default_remind_expire":true,"default_remind_traffic":true,"server_token":"forbidden"
	}`)
	expectAPIError(t, unknown, http.StatusBadRequest, "invalid_json")

	legacyAuthorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	legacyFetch := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=subscribe", legacyAuthorization, "")
	if legacyFetch.Code != http.StatusOK || !containsAll(legacyFetch.Body.String(),
		`"plan_change_enable":false`, `"reset_traffic_method":4`, `"surplus_enable":false`,
		`"new_order_event_id":1`, `"renew_order_event_id":1`, `"change_order_event_id":1`,
		`"show_info_to_server_enable":false`, `"show_protocol_to_server_enable":false`,
		`"default_remind_expire":false`, `"default_remind_traffic":true`, `"subscribe_path":"s"`) {
		t.Fatalf("legacy subscribe fetch status=%d body=%s", legacyFetch.Code, legacyFetch.Body)
	}
	if strings.Contains(legacyFetch.Body.String(), "revision") || strings.Contains(legacyFetch.Body.String(), "templates") {
		t.Fatalf("legacy subscribe fetch disclosed internal fields: %s", legacyFetch.Body)
	}

	legacySaved := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/save", legacyAuthorization, `{
		"plan_change_enable":true,"reset_traffic_method":"2","surplus_enable":true,
		"new_order_event_id":"0","renew_order_event_id":"0","change_order_event_id":"0",
		"show_info_to_server_enable":true,"show_protocol_to_server_enable":true,
		"default_remind_expire":true,"default_remind_traffic":false,"subscribe_path":"legacy_feed"
	}`)
	if legacySaved.Code != http.StatusOK || !containsAll(legacySaved.Body.String(), `"status":"success"`, `"data":true`) {
		t.Fatalf("legacy subscribe save status=%d body=%s", legacySaved.Code, legacySaved.Body)
	}
	legacyAfter := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=subscribe", legacyAuthorization, "")
	if legacyAfter.Code != http.StatusOK || !containsAll(legacyAfter.Body.String(),
		`"plan_change_enable":true`, `"reset_traffic_method":2`, `"surplus_enable":true`,
		`"show_info_to_server_enable":true`, `"show_protocol_to_server_enable":true`,
		`"default_remind_expire":true`, `"default_remind_traffic":false`, `"subscribe_path":"legacy_feed"`) {
		t.Fatalf("legacy subscribe fetch after save status=%d body=%s", legacyAfter.Code, legacyAfter.Body)
	}

	partial := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/save", legacyAuthorization, `{"surplus_enable":false}`)
	if partial.Code != http.StatusOK {
		t.Fatalf("legacy partial subscribe save status=%d body=%s", partial.Code, partial.Body)
	}
	beforeRejected := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=subscribe", legacyAuthorization, "").Body.String()
	rejected := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/save", legacyAuthorization, `{
		"plan_change_enable":false,"subscribe_path":"../unsafe"
	}`)
	if rejected.Code != http.StatusUnprocessableEntity || !strings.Contains(rejected.Body.String(), `"status":"fail"`) {
		t.Fatalf("legacy invalid subscribe save status=%d body=%s", rejected.Code, rejected.Body)
	}
	afterRejected := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=subscribe", legacyAuthorization, "").Body.String()
	if beforeRejected != afterRejected {
		t.Fatalf("rejected legacy save changed settings\nbefore=%s\nafter=%s", beforeRejected, afterRejected)
	}
	mixed := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/save", legacyAuthorization, `{
		"surplus_enable":true,"invite_commission":10
	}`)
	if mixed.Code != http.StatusUnprocessableEntity || !strings.Contains(mixed.Body.String(), `"status":"fail"`) {
		t.Fatalf("mixed legacy config status=%d body=%s", mixed.Code, mixed.Body)
	}
	invalidNumericString := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/save", legacyAuthorization, `{"reset_traffic_method":"02"}`)
	if invalidNumericString.Code != http.StatusBadRequest || !strings.Contains(invalidNumericString.Body.String(), `"status":"fail"`) {
		t.Fatalf("non-canonical legacy number status=%d body=%s", invalidNumericString.Code, invalidNumericString.Body)
	}

	legacyReaderAuthorization := loginLegacyBearer(t, api, "subscription-policy-reader@example.test", "subscription-policy-reader-password-123").Authorization
	legacyForbidden := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=subscribe", legacyReaderAuthorization, "")
	if legacyForbidden.Code != http.StatusForbidden {
		t.Fatalf("legacy non-admin subscribe fetch status=%d body=%s", legacyForbidden.Code, legacyForbidden.Body)
	}
	modernAudits, err := database.ListAdminAuditLogs(t.Context(), store.AdminAuditFilter{Page: 1, PageSize: 20, Query: "subscription-policy-settings"})
	if err != nil || modernAudits.Total < 3 || modernAudits.Items[0].Route != "/api/v1/admin/subscription-policy-settings" {
		t.Fatalf("modern subscription policy audits=%#v err=%v", modernAudits, err)
	}
	legacyAudits, err := database.ListAdminAuditLogs(t.Context(), store.AdminAuditFilter{Page: 1, PageSize: 20, Query: "/config/save"})
	if err != nil || legacyAudits.Total < 5 || legacyAudits.Items[0].Route != "/api/v2/{secure_admin}/config/save" {
		t.Fatalf("legacy subscription config audits=%#v err=%v", legacyAudits, err)
	}
}

type subscriptionPolicyResponse struct {
	Revision             int64  `json:"revision"`
	PlanChangeEnabled    bool   `json:"plan_change_enable"`
	ResetTrafficMethod   int    `json:"reset_traffic_method"`
	SurplusEnabled       bool   `json:"surplus_enable"`
	NewOrderEventID      int    `json:"new_order_event_id"`
	RenewOrderEventID    int    `json:"renew_order_event_id"`
	ChangeOrderEventID   int    `json:"change_order_event_id"`
	DefaultRemindExpire  bool   `json:"default_remind_expire"`
	DefaultRemindTraffic bool   `json:"default_remind_traffic"`
	UpdatedAt            string `json:"updated_at"`
}

func decodeSubscriptionPolicyEnvelope(t *testing.T, response *httptest.ResponseRecorder) subscriptionPolicyResponse {
	t.Helper()
	var envelope struct {
		Data subscriptionPolicyResponse `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode subscription policy: %v", err)
	}
	return envelope.Data
}
