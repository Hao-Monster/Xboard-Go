package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	missingCSRFRequest := httptest.NewRequest(http.MethodPatch, updatePath, strings.NewReader(updateBody))
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
