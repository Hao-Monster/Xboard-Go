package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestAdminNodeManagementAPIListsAndMutatesWithRevisionProtection(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "API VLESS", Type: "vless", Host: "api-node.example.test", Port: "443", Show: true, Enabled: true, Sort: 10,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	admin := loginAdmin(t, api)

	listed := admin.request(t, api, http.MethodGet, "/api/v1/admin/nodes?page=1&page_size=500&q=API&type=vless&show=true", "")
	if listed.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body)
	}
	var page struct {
		Data store.AdminNodePage `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &page); err != nil || page.Data.Total != 1 || len(page.Data.Items) != 1 || page.Data.Items[0].ID != node.ID {
		t.Fatalf("list response=%s error=%v", listed.Body, err)
	}
	invalidPage := admin.request(t, api, http.MethodGet, "/api/v1/admin/nodes?page_size=501", "")
	if invalidPage.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid page status=%d body=%s", invalidPage.Code, invalidPage.Body)
	}

	updatePath := fmt.Sprintf("/api/v1/admin/nodes/%d", node.ID)
	updateBody := fmt.Sprintf(`{"revision":%d,"name":"API updated","host":"updated.example.test","port":"8443","show":true,"enabled":true,"sort":20,"machine_id":null}`, node.Revision)
	missingCSRFRequest := newTestRequest(http.MethodPatch, updatePath, strings.NewReader(updateBody))
	missingCSRFRequest.Header.Set("Content-Type", "application/json")
	admin.addCookies(missingCSRFRequest)
	missingCSRF := httptest.NewRecorder()
	api.ServeHTTP(missingCSRF, missingCSRFRequest)
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d body=%s", missingCSRF.Code, missingCSRF.Body)
	}

	updatedResponse := admin.request(t, api, http.MethodPatch, updatePath, updateBody)
	if updatedResponse.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updatedResponse.Code, updatedResponse.Body)
	}
	var updatedPayload struct {
		Data store.Node `json:"data"`
	}
	if err := json.Unmarshal(updatedResponse.Body.Bytes(), &updatedPayload); err != nil || updatedPayload.Data.Revision != 2 || updatedPayload.Data.Name != "API updated" {
		t.Fatalf("update response=%s error=%v", updatedResponse.Body, err)
	}
	stale := admin.request(t, api, http.MethodPatch, updatePath, updateBody)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "node_revision_conflict") {
		t.Fatalf("stale update status=%d body=%s", stale.Code, stale.Body)
	}

	copyResponse := admin.request(t, api, http.MethodPost, updatePath+"/copy", `{"revision":2}`)
	if copyResponse.Code != http.StatusCreated {
		t.Fatalf("copy status=%d body=%s", copyResponse.Code, copyResponse.Body)
	}
	var copyPayload struct {
		Data store.Node `json:"data"`
	}
	if err := json.Unmarshal(copyResponse.Body.Bytes(), &copyPayload); err != nil || copyPayload.Data.ID == node.ID || copyPayload.Data.Show {
		t.Fatalf("copy response=%s error=%v", copyResponse.Body, err)
	}

	bulkState := admin.request(t, api, http.MethodPost, "/api/v1/admin/nodes/bulk-state", fmt.Sprintf(
		`{"targets":[{"id":%d,"revision":2},{"id":%d,"revision":1}],"show":false}`, node.ID, copyPayload.Data.ID,
	))
	if bulkState.Code != http.StatusOK {
		t.Fatalf("bulk state status=%d body=%s", bulkState.Code, bulkState.Body)
	}
	updated, _ := database.GetNode(ctx, node.ID)
	copied, _ := database.GetNode(ctx, copyPayload.Data.ID)
	if updated.Show || copied.Show || updated.Revision != 3 || copied.Revision != 2 {
		t.Fatalf("bulk state nodes: updated=%#v copied=%#v", updated, copied)
	}

	reset := admin.request(t, api, http.MethodPost, "/api/v1/admin/nodes/bulk-reset-traffic", fmt.Sprintf(
		`{"targets":[{"id":%d,"revision":3},{"id":%d,"revision":2}]}`, node.ID, copied.ID,
	))
	if reset.Code != http.StatusOK {
		t.Fatalf("reset status=%d body=%s", reset.Code, reset.Body)
	}
	updated, _ = database.GetNode(ctx, node.ID)
	copied, _ = database.GetNode(ctx, copied.ID)
	if updated.TrafficUpload != 0 || copied.TrafficDownload != 0 || updated.Revision != 4 || copied.Revision != 3 {
		t.Fatalf("reset nodes: updated=%#v copied=%#v", updated, copied)
	}

	deleted := admin.request(t, api, http.MethodPost, "/api/v1/admin/nodes/bulk-delete", fmt.Sprintf(
		`{"targets":[{"id":%d,"revision":3}]}`, copied.ID,
	))
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body)
	}
	if _, err := database.GetNode(ctx, copied.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted node error=%v, want ErrNotFound", err)
	}
}

func TestAdminNodeParentOptionsAreBoundedSearchableAndAdministratorOnly(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	first, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "First VLESS", Type: "vless", Host: "first-parent.example.test", Port: "443", Show: true, Enabled: true, Sort: 1,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "Searchable VLESS", Type: "vless", Host: "searchable-parent.example.test", Port: "443", Show: true, Enabled: true, Sort: 2,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	admin := loginAdmin(t, api)
	path := fmt.Sprintf("/api/v1/admin/nodes/parent-options?type=vless&q=searchable&include_id=%d&exclude_id=%d", first.ID, second.ID)
	response := admin.request(t, api, http.MethodGet, path, "")
	if response.Code != http.StatusOK {
		t.Fatalf("parent options status=%d body=%s", response.Code, response.Body)
	}
	var payload struct {
		Data store.AdminNodeParentOptions `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload.Data.HasMore || len(payload.Data.Items) != 1 || payload.Data.Items[0].ID != first.ID {
		t.Fatalf("parent options response=%s error=%v", response.Body, err)
	}

	for _, invalidPath := range []string{
		"/api/v1/admin/nodes/parent-options",
		"/api/v1/admin/nodes/parent-options?type=unknown",
		"/api/v1/admin/nodes/parent-options?type=vless&include_id=0",
	} {
		invalid := admin.request(t, api, http.MethodGet, invalidPath, "")
		if invalid.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid parent path=%s status=%d body=%s", invalidPath, invalid.Code, invalid.Body)
		}
	}

	hasher := newHTTPAPITestPasswordHasher()
	passwordHash, err := hasher.Hash("ordinary-parent-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "parent-ordinary@example.test", PasswordHash: passwordHash,
	}, now); err != nil {
		t.Fatal(err)
	}
	ordinary := loginAs(t, api, "parent-ordinary@example.test", "ordinary-parent-password-123")
	forbidden := ordinary.request(t, api, http.MethodGet, "/api/v1/admin/nodes/parent-options?type=vless", "")
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("ordinary parent options status=%d body=%s", forbidden.Code, forbidden.Body)
	}
}

func TestAdminNodeManagementAPIRequiresAdministrator(t *testing.T) {
	api, database := newTestAPI(t)
	hasher := newHTTPAPITestPasswordHasher()
	passwordHash, err := hasher.Hash("ordinary-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateAdminUser(context.Background(), store.CreateAdminUserInput{
		Email: "node-ordinary@example.test", PasswordHash: passwordHash,
	}, fixedNow()); err != nil {
		t.Fatal(err)
	}
	ordinary := loginAs(t, api, "node-ordinary@example.test", "ordinary-password-123")
	response := ordinary.request(t, api, http.MethodGet, "/api/v1/admin/nodes", "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("ordinary user status=%d body=%s", response.Code, response.Body)
	}
}

func TestAdminNodeBasicCreateProducesEditableProtocolDefinition(t *testing.T) {
	api, _ := newTestAPI(t)
	admin := loginAdmin(t, api)
	created := admin.request(t, api, http.MethodPost, "/api/v1/admin/nodes", `{
		"name":"Basic VLESS","type":"vless","host":"basic.example.test","port":"2443",
		"show":true,"enabled":true,"sort":7
	}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create basic node status=%d body=%s", created.Code, created.Body)
	}
	var createdPayload struct {
		Data store.Node `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdPayload); err != nil || createdPayload.Data.ID < 1 {
		t.Fatalf("decode basic node response=%s error=%v", created.Body, err)
	}

	detail := admin.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/admin/nodes/%d", createdPayload.Data.ID), "")
	if detail.Code != http.StatusOK {
		t.Fatalf("read basic node definition status=%d body=%s", detail.Code, detail.Body)
	}
	var detailPayload struct {
		Data store.AdminNodeDefinition `json:"data"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &detailPayload); err != nil {
		t.Fatalf("decode basic node definition=%s error=%v", detail.Body, err)
	}
	definition := detailPayload.Data
	if definition.Type != "vless" || definition.ServerPort != 2443 || definition.ListenAddress != "0.0.0.0" || definition.Rate != 1 {
		t.Fatalf("unexpected basic node definition=%#v", definition)
	}
	var settings map[string]any
	if err := json.Unmarshal(definition.ProtocolSettings, &settings); err != nil || settings["network"] != "tcp" || settings["tls"] != float64(0) {
		t.Fatalf("unexpected VLESS defaults=%s error=%v", definition.ProtocolSettings, err)
	}
}

func TestAdminNodeDefinitionAPICreatesReadsAndUpdatesAtomically(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	group, err := database.CreateServerGroup(ctx, "API protocol users", now)
	if err != nil {
		t.Fatal(err)
	}
	route, err := database.CreateRoutingRule(ctx, store.SaveRoutingRuleInput{Remarks: "API route", Match: []string{"domain:example.test"}, Action: "direct"}, now)
	if err != nil {
		t.Fatal(err)
	}
	machine, _, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "API protocol edge", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	admin := loginAdmin(t, api)
	settings := `{"tls":0,"network":"tcp","network_settings":{},"flow":"","encryption":{"enabled":false,"encryption":"","decryption":""},"tls_settings":{"server_name":"","allow_insecure":false},"reality_settings":{},"utls":{"enabled":false,"fingerprint":"chrome"},"multiplex":{"enabled":false,"protocol":"smux","max_connections":4,"padding":false,"brutal":{"enabled":false,"up_mbps":100,"down_mbps":100}}}`
	body := fmt.Sprintf(`{
		"type":"vless","external_code":null,"parent_id":null,"name":"API full VLESS","rate":1.5,
		"tags":["edge"],"host":"full.example.test","port":"18443-18444","server_port":19443,
		"listen_address":"0.0.0.0","protocol_settings":%s,"show":true,"enabled":true,"sort":7,
		"machine_id":%d,"group_ids":[%d],"route_ids":[%d],"rate_time_enabled":true,
		"rate_time_ranges":[{"start":"00:00","end":"06:00","rate":0.5}],
		"custom_outbounds":[],"custom_routes":[],"certificate_config":{},"transfer_enable":1073741824
	}`, settings, machine.ID, group.ID, route.ID)
	createdResponse := admin.request(t, api, http.MethodPost, "/api/v1/admin/nodes", body)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create definition status=%d body=%s", createdResponse.Code, createdResponse.Body)
	}
	var createdPayload struct {
		Data store.AdminNodeDefinition `json:"data"`
	}
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &createdPayload); err != nil || createdPayload.Data.ID < 1 || createdPayload.Data.ListenAddress != "0.0.0.0" || createdPayload.Data.ServerPort != 19443 {
		t.Fatalf("create definition response=%s error=%v", createdResponse.Body, err)
	}

	path := fmt.Sprintf("/api/v1/admin/nodes/%d", createdPayload.Data.ID)
	readResponse := admin.request(t, api, http.MethodGet, path, "")
	if readResponse.Code != http.StatusOK {
		t.Fatalf("read definition status=%d body=%s", readResponse.Code, readResponse.Body)
	}
	var readPayload struct {
		Data store.AdminNodeDefinition `json:"data"`
	}
	if err := json.Unmarshal(readResponse.Body.Bytes(), &readPayload); err != nil || !reflect.DeepEqual(readPayload.Data, createdPayload.Data) {
		t.Fatalf("read definition response=%s error=%v", readResponse.Body, err)
	}

	updatedBody := strings.Replace(body, `"listen_address":"0.0.0.0"`, `"listen_address":"::"`, 1)
	updatedBody = strings.TrimSuffix(updatedBody, "}") + fmt.Sprintf(`,"revision":%d}`, createdPayload.Data.Revision)
	updatedResponse := admin.request(t, api, http.MethodPut, path, updatedBody)
	if updatedResponse.Code != http.StatusOK {
		t.Fatalf("update definition status=%d body=%s", updatedResponse.Code, updatedResponse.Body)
	}
	var updatedPayload struct {
		Data store.AdminNodeDefinition `json:"data"`
	}
	if err := json.Unmarshal(updatedResponse.Body.Bytes(), &updatedPayload); err != nil || updatedPayload.Data.Revision != 2 || updatedPayload.Data.ListenAddress != "::" {
		t.Fatalf("update definition response=%s error=%v", updatedResponse.Body, err)
	}

	invalidBody := strings.Replace(updatedBody, `"network_settings":{}`, `"network_settings":{},"unexpected":true`, 1)
	invalidBody = strings.Replace(invalidBody, fmt.Sprintf(`"revision":%d`, createdPayload.Data.Revision), `"revision":2`, 1)
	invalidResponse := admin.request(t, api, http.MethodPut, path, invalidBody)
	if invalidResponse.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid definition status=%d body=%s", invalidResponse.Code, invalidResponse.Body)
	}
	after, err := database.GetAdminNodeDefinition(ctx, createdPayload.Data.ID)
	if err != nil || after.Revision != 2 || after.ListenAddress != "::" {
		t.Fatalf("invalid write changed definition=%#v error=%v", after, err)
	}
}

func TestMachineNodeMutationsRequireCurrentAdministratorRevision(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	machine, _, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "revision-edge", IsActive: true}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "revision-node", Type: "vless", Host: "revision.test", Port: "443", Show: true, Enabled: true,
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	admin := loginAdmin(t, api)
	path := fmt.Sprintf("/api/v1/admin/machines/%d/nodes/%d", machine.ID, node.ID)

	missing := admin.request(t, api, http.MethodPut, path, `{}`)
	if missing.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing revision status=%d body=%s", missing.Code, missing.Body)
	}
	assigned := admin.request(t, api, http.MethodPut, path, `{"revision":1}`)
	if assigned.Code != http.StatusNoContent {
		t.Fatalf("assign status=%d body=%s", assigned.Code, assigned.Body)
	}
	stale := admin.request(t, api, http.MethodDelete, path, `{"revision":1}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale unassign status=%d body=%s", stale.Code, stale.Body)
	}
	unassigned := admin.request(t, api, http.MethodDelete, path, `{"revision":2}`)
	if unassigned.Code != http.StatusNoContent {
		t.Fatalf("unassign status=%d body=%s", unassigned.Code, unassigned.Body)
	}
	current, err := database.GetNode(ctx, node.ID)
	if err != nil || current.Revision != 3 || current.MachineID != nil {
		t.Fatalf("current node=%#v error=%v", current, err)
	}
}
