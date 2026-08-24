package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestNoticeAdminLifecycleAndUserVisibilityContract(t *testing.T) {
	api, _ := newTestAPI(t)
	admin := loginAdmin(t, api)

	created := admin.request(t, api, http.MethodPost, "/api/v1/admin/notices", `{
		"title":" Service update ","content":"**Available now**",
		"image_url":"https://cdn.example.test/update.png","tags":["news"," news ","service"],"show":true
	}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create notice status = %d; body=%s", created.Code, created.Body)
	}
	var firstPayload struct {
		Data store.Notice `json:"data"`
	}
	decodeResponse(t, created, &firstPayload)
	first := firstPayload.Data
	if first.Title != "Service update" || !first.Visible || len(first.Tags) != 2 || first.Revision != 1 {
		t.Fatalf("created notice = %#v", first)
	}

	hiddenResponse := admin.request(t, api, http.MethodPost, "/api/v1/admin/notices", `{
		"title":"Draft maintenance","content":"draft","image_url":"","tags":[],"show":false
	}`)
	if hiddenResponse.Code != http.StatusCreated {
		t.Fatalf("create hidden notice status = %d; body=%s", hiddenResponse.Code, hiddenResponse.Body)
	}
	var hiddenPayload struct {
		Data store.Notice `json:"data"`
	}
	decodeResponse(t, hiddenResponse, &hiddenPayload)
	hidden := hiddenPayload.Data

	adminList := admin.request(t, api, http.MethodGet, "/api/v1/admin/notices", "")
	if adminList.Code != http.StatusOK {
		t.Fatalf("list notices status = %d; body=%s", adminList.Code, adminList.Body)
	}
	var listed struct {
		Data []store.Notice `json:"data"`
	}
	decodeResponse(t, adminList, &listed)
	if len(listed.Data) != 2 || listed.Data[0].ID != hidden.ID || listed.Data[1].ID != first.ID {
		t.Fatalf("admin notice order = %#v", listed.Data)
	}

	stale := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/notices/%d", first.ID), `{
		"revision":99,"title":"stale","content":"stale","image_url":"","tags":[],"show":true
	}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale update status = %d; body=%s", stale.Code, stale.Body)
	}

	updated := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/notices/%d", first.ID), fmt.Sprintf(`{
		"revision":%d,"title":"Service update revised","content":"revised","image_url":"","tags":["release"],"show":true
	}`, first.Revision))
	if updated.Code != http.StatusOK {
		t.Fatalf("update notice status = %d; body=%s", updated.Code, updated.Body)
	}
	var updatedPayload struct {
		Data store.Notice `json:"data"`
	}
	decodeResponse(t, updated, &updatedPayload)
	first = updatedPayload.Data

	shown := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/notices/%d/visibility", hidden.ID), fmt.Sprintf(`{"revision":%d,"show":true}`, hidden.Revision))
	if shown.Code != http.StatusOK {
		t.Fatalf("show notice status = %d; body=%s", shown.Code, shown.Body)
	}
	var shownPayload struct {
		Data store.Notice `json:"data"`
	}
	decodeResponse(t, shown, &shownPayload)
	hidden = shownPayload.Data

	reordered := admin.request(t, api, http.MethodPut, "/api/v1/admin/notices/order", fmt.Sprintf(`{"ids":[%d,%d]}`, first.ID, hidden.ID))
	if reordered.Code != http.StatusOK {
		t.Fatalf("reorder notices status = %d; body=%s", reordered.Code, reordered.Body)
	}

	userList := admin.request(t, api, http.MethodGet, "/api/v1/notices?page=1", "")
	if userList.Code != http.StatusOK {
		t.Fatalf("visible notice list status = %d; body=%s", userList.Code, userList.Body)
	}
	var page struct {
		Data store.NoticePage `json:"data"`
	}
	decodeResponse(t, userList, &page)
	if page.Data.Total != 2 || page.Data.Page != 1 || page.Data.PageSize != 5 || len(page.Data.Items) != 2 || page.Data.Items[0].ID != first.ID {
		t.Fatalf("visible notice page = %#v", page.Data)
	}

	removed := admin.request(t, api, http.MethodDelete, fmt.Sprintf("/api/v1/admin/notices/%d?revision=%d", hidden.ID, hidden.Revision), "")
	if removed.Code != http.StatusNoContent {
		t.Fatalf("delete notice status = %d; body=%s", removed.Code, removed.Body)
	}
	staleDelete := admin.request(t, api, http.MethodDelete, fmt.Sprintf("/api/v1/admin/notices/%d?revision=%d", first.ID, first.Revision-1), "")
	if staleDelete.Code != http.StatusConflict {
		t.Fatalf("stale delete status = %d; body=%s", staleDelete.Code, staleDelete.Body)
	}
}

func TestNoticeEndpointsEnforceAuthenticationCSRFAndStrictValidation(t *testing.T) {
	api, _ := newTestAPI(t)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/notices", nil)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated visible list status = %d; body=%s", response.Code, response.Body)
	}

	admin := loginAdmin(t, api)
	createdUser := admin.request(t, api, http.MethodPost, "/api/v1/admin/users", `{
		"email":"notice-reader@example.test","password":"notice-reader-password-123","group_id":null,
		"transfer_enable":0,"expired_at":null,"speed_limit":0,"device_limit":0,"banned":false
	}`)
	if createdUser.Code != http.StatusCreated {
		t.Fatalf("create notice reader status = %d; body=%s", createdUser.Code, createdUser.Body)
	}
	reader := loginAs(t, api, "notice-reader@example.test", "notice-reader-password-123")
	if result := reader.request(t, api, http.MethodGet, "/api/v1/notices", ""); result.Code != http.StatusOK {
		t.Fatalf("ordinary user notice list status = %d; body=%s", result.Code, result.Body)
	}
	if result := reader.request(t, api, http.MethodGet, "/api/v1/admin/notices", ""); result.Code != http.StatusForbidden {
		t.Fatalf("ordinary user admin notice list status = %d; body=%s", result.Code, result.Body)
	}
	for name, body := range map[string]string{
		"empty title":   `{"title":"","content":"body","image_url":"","tags":[],"show":true}`,
		"unsafe image":  `{"title":"title","content":"body","image_url":"javascript:alert(1)","tags":[],"show":true}`,
		"unknown field": `{"title":"title","content":"body","image_url":"","tags":[],"show":true,"popup":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			result := admin.request(t, api, http.MethodPost, "/api/v1/admin/notices", body)
			want := http.StatusUnprocessableEntity
			if name == "unknown field" {
				want = http.StatusBadRequest
			}
			if result.Code != want {
				t.Fatalf("status = %d, want %d; body=%s", result.Code, want, result.Body)
			}
		})
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/notices", strings.NewReader(`{"title":"title","content":"body","image_url":"","tags":[],"show":true}`))
	request.Header.Set("Content-Type", "application/json")
	admin.addCookies(request)
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d; body=%s", response.Code, response.Body)
	}

	invalidPage := admin.request(t, api, http.MethodGet, "/api/v1/notices?page=0", "")
	if invalidPage.Code != http.StatusBadRequest {
		t.Fatalf("invalid page status = %d; body=%s", invalidPage.Code, invalidPage.Body)
	}
}
