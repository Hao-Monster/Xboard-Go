package httpapi

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestPlanEndpointsEnforceLifecycleVisibilityAndRevision(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	created := admin.request(t, api, http.MethodPost, "/api/v1/admin/plans", `{
		"name":"Pro","transfer_enable":100,"speed_limit":200,"device_limit":3,"capacity_limit":0,
		"reset_traffic_method":1,"prices":{"monthly":101,"quarterly":202,"half_yearly":303,"yearly":404,
		"two_yearly":505,"three_yearly":606,"onetime":707,"reset_traffic":808},"tags":["推荐"],
		"content":"{{transfer}}/{{speed}}/{{devices}}/{{reset_method}}"
	}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create plan status = %d; body=%s", created.Code, created.Body)
	}
	if !containsAll(created.Body.String(), `"users_count":0`, `"active_users_count":0`, `"capacity_users_count":0`) {
		t.Fatalf("create plan statistics body=%s", created.Body)
	}
	var payload struct {
		Data store.Plan `json:"data"`
	}
	decodeResponse(t, created, &payload)
	wantPrices := store.PlanPrices{
		"monthly": 101, "quarterly": 202, "half_yearly": 303, "yearly": 404,
		"two_yearly": 505, "three_yearly": 606, "onetime": 707, "reset_traffic": 808,
	}
	if payload.Data.Show || payload.Data.Sell || !payload.Data.Renew || len(payload.Data.Prices) != len(wantPrices) {
		t.Fatalf("created plan = %#v", payload.Data)
	}
	for period, want := range wantPrices {
		if payload.Data.Prices[period] != want {
			t.Fatalf("created plan price %q = %d, want %d", period, payload.Data.Prices[period], want)
		}
	}

	state := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/plans/%d/state", payload.Data.ID),
		fmt.Sprintf(`{"revision":%d,"show":true,"sell":true,"renew":false}`, payload.Data.Revision))
	if state.Code != http.StatusOK {
		t.Fatalf("update plan state status = %d; body=%s", state.Code, state.Body)
	}
	var statePayload struct {
		Data store.Plan `json:"data"`
	}
	decodeResponse(t, state, &statePayload)

	guest := plainRequest(api, http.MethodGet, "/api/v1/guest/plans", "")
	if guest.Code != http.StatusOK || !containsAll(guest.Body.String(), `"capacity_remaining":null`, `"can_purchase":true`,
		`"monthly":101`, `"quarterly":202`, `"half_yearly":303`, `"yearly":404`,
		`"two_yearly":505`, `"three_yearly":606`, `"onetime":707`, `"reset_traffic":808`,
		`"content":"100/200/3/按月"`) {
		t.Fatalf("guest plans status = %d; body=%s", guest.Code, guest.Body)
	}
	if strings.Contains(guest.Body.String(), "users_count") || strings.Contains(guest.Body.String(), "active_users_count") {
		t.Fatalf("guest plan leaked administrator statistics: body=%s", guest.Body)
	}
	user := createKnowledgeTestUser(t, database, "plan-user@example.test", "plan-user-password-123", 0, false)
	userClient := loginAs(t, api, user.email, user.password)
	userPlans := userClient.request(t, api, http.MethodGet, "/api/v1/plans", "")
	if userPlans.Code != http.StatusOK || !containsAll(userPlans.Body.String(), `"name":"Pro"`, `"can_purchase":true`) {
		t.Fatalf("user plans status = %d; body=%s", userPlans.Code, userPlans.Body)
	}
	stale := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/plans/%d", payload.Data.ID),
		fmt.Sprintf(`{"revision":%d,"name":"stale","transfer_enable":1,"prices":{},"tags":[],"content":"","force_update":false}`, payload.Data.Revision))
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale plan update status = %d; body=%s", stale.Code, stale.Body)
	}
	invalid := admin.request(t, api, http.MethodPost, "/api/v1/admin/plans", `{"name":"bad","transfer_enable":1,"prices":{"weekly":100}}`)
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid plan status = %d; body=%s", invalid.Code, invalid.Body)
	}
	listed := admin.request(t, api, http.MethodGet, "/api/v1/admin/plans", "")
	if listed.Code != http.StatusOK || !containsAll(listed.Body.String(), `"name":"Pro"`, `"revision":2`, `"users_count":0`, `"active_users_count":0`) {
		t.Fatalf("list plans status = %d; body=%s", listed.Code, listed.Body)
	}
	ordered := admin.request(t, api, http.MethodPut, "/api/v1/admin/plans/order", fmt.Sprintf(`{"ids":[%d]}`, payload.Data.ID))
	if ordered.Code != http.StatusOK {
		t.Fatalf("reorder plan status = %d; body=%s", ordered.Code, ordered.Body)
	}
	deleted := admin.request(t, api, http.MethodDelete, fmt.Sprintf("/api/v1/admin/plans/%d", payload.Data.ID), "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete plan status = %d; body=%s", deleted.Code, deleted.Body)
	}
	missing := admin.request(t, api, http.MethodDelete, fmt.Sprintf("/api/v1/admin/plans/%d", payload.Data.ID), "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("delete missing plan status = %d; body=%s", missing.Code, missing.Body)
	}
	_ = statePayload
}

func TestPlanWritesRequireAdministratorCSRFAndRejectUnknownFields(t *testing.T) {
	api, _ := newTestAPI(t)
	admin := loginAdmin(t, api)
	unknown := admin.request(t, api, http.MethodPost, "/api/v1/admin/plans", `{"name":"bad","transfer_enable":1,"unexpected":true}`)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d; body=%s", unknown.Code, unknown.Body)
	}
	unauthenticated := plainRequest(api, http.MethodPost, "/api/v1/admin/plans", `{"name":"bad","transfer_enable":1}`)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d; body=%s", unauthenticated.Code, unauthenticated.Body)
	}
}

func plainRequest(api http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}
