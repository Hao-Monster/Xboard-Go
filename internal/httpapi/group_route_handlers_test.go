package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestAdminGroupAndRoutingRuleLifecycleFeedsNodeConfig(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	admin := loginAdmin(t, api)

	createdGroup := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/server-groups", `{"name":"  Premium  "}`)
	if createdGroup.Code != http.StatusCreated {
		t.Fatalf("create group status = %d; body=%s", createdGroup.Code, createdGroup.Body)
	}
	var groupPayload struct {
		Data store.ServerGroup `json:"data"`
	}
	decodeResponse(t, createdGroup, &groupPayload)
	if groupPayload.Data.Name != "Premium" {
		t.Fatalf("created group = %#v", groupPayload.Data)
	}

	createdRoute := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/routing-rules", `{
		"remarks":"  Domestic direct  ",
		"match":[" example.cn ","","example.cn","10.0.0.0/8"],
		"action":"direct",
		"action_value":"ignored"
	}`)
	if createdRoute.Code != http.StatusCreated {
		t.Fatalf("create route status = %d; body=%s", createdRoute.Code, createdRoute.Body)
	}
	var routePayload struct {
		Data store.RoutingRule `json:"data"`
	}
	decodeResponse(t, createdRoute, &routePayload)
	if routePayload.Data.ActionValue != "" || len(routePayload.Data.Match) != 2 {
		t.Fatalf("created route = %#v", routePayload.Data)
	}

	machine, enrollment, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "route-edge", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := database.ExchangeEnrollment(ctx, machine.ID, enrollment.Code, now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "route-node", Type: "vless", Host: "route.example.test", Port: "443", Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	runtimePath := fmt.Sprintf("/api/v1/admin/admin/nodes/%d/runtime", node.ID)
	runtime := admin.request(t, api, http.MethodPut, runtimePath, fmt.Sprintf(`{
		"rate":1,"group_ids":[%d],"route_ids":[%d],
		"config":{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}
	}`, groupPayload.Data.ID, routePayload.Data.ID))
	if runtime.Code != http.StatusOK || !strings.Contains(runtime.Body.String(), fmt.Sprintf(`"route_ids":[%d]`, routePayload.Data.ID)) {
		t.Fatalf("save runtime status = %d; body=%s", runtime.Code, runtime.Body)
	}
	if _, err := database.CreateRuntimeUser(ctx, store.CreateRuntimeUserInput{
		Email: "premium-user@example.test", PasswordHash: "hash", UUID: "9eed5240-16b1-4a2b-a230-d76e2cf2e40f",
		GroupID: groupPayload.Data.ID, TransferEnable: 1,
	}, now); err != nil {
		t.Fatal(err)
	}

	configPath := fmt.Sprintf("/api/v2/server/config?machine_id=%d&node_id=%d", machine.ID, node.ID)
	firstConfig := agentRequest(api, http.MethodGet, configPath, credential.Token, "")
	if firstConfig.Code != http.StatusOK || !strings.Contains(firstConfig.Body.String(), `"routes":[{"action":"direct","id":`) ||
		!strings.Contains(firstConfig.Body.String(), `"match":["example.cn","10.0.0.0/8"]`) || strings.Contains(firstConfig.Body.String(), "remarks") {
		t.Fatalf("node config route contract status = %d; body=%s", firstConfig.Code, firstConfig.Body)
	}

	updatedRoute := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/admin/routing-rules/%d", routePayload.Data.ID), `{
		"remarks":"Proxy route","match":["*.example.com"],"action":"proxy","action_value":"warp-out"
	}`)
	if updatedRoute.Code != http.StatusOK {
		t.Fatalf("update route status = %d; body=%s", updatedRoute.Code, updatedRoute.Body)
	}
	secondConfig := agentRequest(api, http.MethodGet, configPath, credential.Token, "")
	if secondConfig.Code != http.StatusOK || !strings.Contains(secondConfig.Body.String(), `"action":"proxy"`) || !strings.Contains(secondConfig.Body.String(), `"action_value":"warp-out"`) {
		t.Fatalf("updated node config status = %d; body=%s", secondConfig.Code, secondConfig.Body)
	}
	if firstConfig.Header().Get("ETag") == secondConfig.Header().Get("ETag") {
		t.Fatal("routing rule update did not change config ETag")
	}

	groups := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/server-groups", "")
	if groups.Code != http.StatusOK {
		t.Fatalf("list groups status = %d; body=%s", groups.Code, groups.Body)
	}
	var groupsPayload struct {
		Data []store.ServerGroup `json:"data"`
	}
	decodeResponse(t, groups, &groupsPayload)
	var found *store.ServerGroup
	for index := range groupsPayload.Data {
		if groupsPayload.Data[index].ID == groupPayload.Data.ID {
			found = &groupsPayload.Data[index]
			break
		}
	}
	if found == nil || found.UsersCount != 1 || found.ServersCount != 1 {
		t.Fatalf("group counts = %#v", found)
	}

	if response := admin.request(t, api, http.MethodDelete, fmt.Sprintf("/api/v1/admin/admin/server-groups/%d", groupPayload.Data.ID), ""); response.Code != http.StatusConflict {
		t.Fatalf("delete referenced group status = %d; body=%s", response.Code, response.Body)
	}
	if response := admin.request(t, api, http.MethodDelete, fmt.Sprintf("/api/v1/admin/admin/routing-rules/%d", routePayload.Data.ID), ""); response.Code != http.StatusConflict {
		t.Fatalf("delete referenced route status = %d; body=%s", response.Code, response.Body)
	}
}

func TestAdminGroupAndRoutingRuleEndpointsRejectInvalidOrUntrustedWrites(t *testing.T) {
	api, _ := newTestAPI(t)
	admin := loginAdmin(t, api)

	for name, body := range map[string]string{
		"missing match":  `{"remarks":"missing","match":[],"action":"block","action_value":""}`,
		"missing target": `{"remarks":"proxy","match":["example.com"],"action":"proxy","action_value":""}`,
		"unknown action": `{"remarks":"unknown","match":["example.com"],"action":"unknown","action_value":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/routing-rules", body)
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusUnprocessableEntity, response.Body)
			}
		})
	}
	if response := admin.request(t, api, http.MethodPatch, "/api/v1/admin/admin/server-groups/999999", `{"name":"missing"}`); response.Code != http.StatusNotFound {
		t.Fatalf("update missing group status = %d; body=%s", response.Code, response.Body)
	}
	if response := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/server-groups", `{"name":"group","unexpected":true}`); response.Code != http.StatusBadRequest {
		t.Fatalf("unknown group field status = %d; body=%s", response.Code, response.Body)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/admin/server-groups", strings.NewReader(`{"name":"no csrf"}`))
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range admin.cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d; body=%s", response.Code, response.Body)
	}
}
