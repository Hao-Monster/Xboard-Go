package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestSiteAccessSettingsSwitchLegacyAdminPathWithoutWeakeningAuthorization(t *testing.T) {
	api, _ := newTestAPI(t)
	admin := loginAdmin(t, api)
	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization

	initial := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=safe", authorization, "")
	if initial.Code != http.StatusOK || !strings.Contains(initial.Body.String(), `"safe_mode_enable":false`) || !strings.Contains(initial.Body.String(), `"secure_path":"admin"`) {
		t.Fatalf("initial legacy safe settings status=%d body=%s", initial.Code, initial.Body)
	}
	updated := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":1,"app_name":"Xboard-Go","app_description":"","app_url":"https://panel.example.test:8443/root",
		"safe_mode_enable":true,"secure_path":"secure-admin-01","tos_url":"","logo":""
	}`)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"safe_mode_enable":true`) || !strings.Contains(updated.Body.String(), `"secure_path":"secure-admin-01"`) {
		t.Fatalf("modern site access update status=%d body=%s", updated.Code, updated.Body)
	}

	oldPath := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=safe", authorization, "")
	if oldPath.Code != http.StatusNotFound {
		t.Fatalf("old legacy admin path status=%d want=404 body=%s", oldPath.Code, oldPath.Body)
	}
	unauthenticated := bearerRequest(api, http.MethodGet, "/api/v2/secure-admin-01/config/fetch?key=safe", "", "")
	if unauthenticated.Code != http.StatusForbidden {
		t.Fatalf("new path without bearer status=%d want=403 body=%s", unauthenticated.Code, unauthenticated.Body)
	}
	current := bearerRequest(api, http.MethodGet, "/api/v2/secure-admin-01/config/fetch?key=safe", authorization, "")
	if current.Code != http.StatusOK || !strings.Contains(current.Body.String(), `"safe_mode_enable":true`) {
		t.Fatalf("new legacy admin path status=%d body=%s", current.Code, current.Body)
	}

	legacyChanged := bearerRequest(api, http.MethodPost, "/api/v2/secure-admin-01/config/save", authorization,
		`{"safe_mode_enable":false,"secure_path":"next-admin-path"}`)
	if legacyChanged.Code != http.StatusOK {
		t.Fatalf("legacy safe save status=%d body=%s", legacyChanged.Code, legacyChanged.Body)
	}
	previous := bearerRequest(api, http.MethodGet, "/api/v2/secure-admin-01/config/fetch?key=safe", authorization, "")
	if previous.Code != http.StatusNotFound {
		t.Fatalf("previous dynamic path status=%d want=404 body=%s", previous.Code, previous.Body)
	}
	next := bearerRequest(api, http.MethodGet, "/api/v2/next-admin-path/config/fetch?key=safe", authorization, "")
	if next.Code != http.StatusOK || !strings.Contains(next.Body.String(), `"secure_path":"next-admin-path"`) {
		t.Fatalf("next dynamic path status=%d body=%s", next.Code, next.Body)
	}

	for name, body := range map[string]string{
		"short path":        `{"revision":3,"app_name":"Xboard-Go","app_description":"","app_url":"https://panel.example.test","safe_mode_enable":false,"secure_path":"short","tos_url":"","logo":""}`,
		"missing safe host": `{"revision":3,"app_name":"Xboard-Go","app_description":"","app_url":"","safe_mode_enable":true,"secure_path":"next-admin-path","tos_url":"","logo":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", body)
			expectAPIError(t, response, http.StatusUnprocessableEntity, "validation_failed")
		})
	}
	mixed := bearerRequest(api, http.MethodPost, "/api/v2/next-admin-path/config/save", authorization,
		`{"safe_mode_enable":false,"currency":"USD"}`)
	if mixed.Code != http.StatusUnprocessableEntity {
		t.Fatalf("mixed safe/site config status=%d body=%s", mixed.Code, mixed.Body)
	}
}
