package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSiteAccessSettingsSwitchLegacyAdminPathWithoutWeakeningAuthorization(t *testing.T) {
	api, _ := newTestAPI(t)
	admin := loginAdmin(t, api)
	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization

	initial := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=safe", authorization, "")
	if initial.Code != http.StatusOK || !strings.Contains(initial.Body.String(), `"safe_mode_enable":false`) || !strings.Contains(initial.Body.String(), `"secure_path":"`+testAdminPath+`"`) {
		t.Fatalf("initial legacy safe settings status=%d body=%s", initial.Code, initial.Body)
	}
	updated := admin.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":1,"app_name":"Xboard-Go","app_description":"","app_url":"https://panel.example.test:8443/root",
		"safe_mode_enable":true,"secure_path":"rotated-admin-01","tos_url":"","logo":""
	}`)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"safe_mode_enable":true`) || !strings.Contains(updated.Body.String(), `"secure_path":"rotated-admin-01"`) {
		t.Fatalf("modern site access update status=%d body=%s", updated.Code, updated.Body)
	}

	oldPath := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=safe", authorization, "")
	if oldPath.Code != http.StatusNotFound {
		t.Fatalf("old legacy admin path status=%d want=404 body=%s", oldPath.Code, oldPath.Body)
	}
	unauthenticated := bearerRequest(api, http.MethodGet, "/api/v2/rotated-admin-01/config/fetch?key=safe", "", "")
	if unauthenticated.Code != http.StatusForbidden {
		t.Fatalf("new path without bearer status=%d want=403 body=%s", unauthenticated.Code, unauthenticated.Body)
	}
	current := bearerRequest(api, http.MethodGet, "/api/v2/rotated-admin-01/config/fetch?key=safe", authorization, "")
	if current.Code != http.StatusOK || !strings.Contains(current.Body.String(), `"safe_mode_enable":true`) {
		t.Fatalf("new legacy admin path status=%d body=%s", current.Code, current.Body)
	}
	oldModern := admin.rawRequest(t, api, http.MethodGet, "/api/v1/admin/"+testAdminPath+"/site-settings", "")
	if oldModern.Code != http.StatusNotFound {
		t.Fatalf("old modern administrator path status=%d want=404 body=%s", oldModern.Code, oldModern.Body)
	}
	currentModern := admin.rawRequest(t, api, http.MethodGet, "/api/v1/admin/rotated-admin-01/site-settings", "")
	if currentModern.Code != http.StatusOK {
		t.Fatalf("current modern administrator path status=%d body=%s", currentModern.Code, currentModern.Body)
	}

	legacyChanged := bearerRequest(api, http.MethodPost, "/api/v2/rotated-admin-01/config/save", authorization,
		`{"safe_mode_enable":false,"secure_path":"next-admin-path"}`)
	if legacyChanged.Code != http.StatusOK {
		t.Fatalf("legacy safe save status=%d body=%s", legacyChanged.Code, legacyChanged.Body)
	}
	previous := bearerRequest(api, http.MethodGet, "/api/v2/rotated-admin-01/config/fetch?key=safe", authorization, "")
	if previous.Code != http.StatusNotFound {
		t.Fatalf("previous dynamic path status=%d want=404 body=%s", previous.Code, previous.Body)
	}
	next := bearerRequest(api, http.MethodGet, "/api/v2/next-admin-path/config/fetch?key=safe", authorization, "")
	if next.Code != http.StatusOK || !strings.Contains(next.Body.String(), `"secure_path":"next-admin-path"`) {
		t.Fatalf("next dynamic path status=%d body=%s", next.Code, next.Body)
	}
	previousModern := admin.rawRequest(t, api, http.MethodGet, "/api/v1/admin/rotated-admin-01/site-settings", "")
	if previousModern.Code != http.StatusNotFound {
		t.Fatalf("previous modern administrator path status=%d want=404 body=%s", previousModern.Code, previousModern.Body)
	}
	nextModern := admin.rawRequest(t, api, http.MethodGet, "/api/v1/admin/next-admin-path/site-settings", "")
	if nextModern.Code != http.StatusOK {
		t.Fatalf("next modern administrator path status=%d body=%s", nextModern.Code, nextModern.Body)
	}
	audit := admin.rawRequest(t, api, http.MethodGet, "/api/v1/admin/next-admin-path/system/audit?page=1&page_size=20&query=site-settings", "")
	if audit.Code != http.StatusOK || strings.Contains(audit.Body.String(), testAdminPath) || strings.Contains(audit.Body.String(), "rotated-admin-01") || strings.Contains(audit.Body.String(), "next-admin-path") {
		t.Fatalf("administrator audit leaked a secure path or failed: status=%d body=%s", audit.Code, audit.Body)
	}

	for name, body := range map[string]string{
		"short path":        `{"revision":3,"app_name":"Xboard-Go","app_description":"","app_url":"https://panel.example.test","safe_mode_enable":false,"secure_path":"short","tos_url":"","logo":""}`,
		"missing safe host": `{"revision":3,"app_name":"Xboard-Go","app_description":"","app_url":"","safe_mode_enable":true,"secure_path":"next-admin-path","tos_url":"","logo":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := admin.rawRequest(t, api, http.MethodPut, "/api/v1/admin/next-admin-path/site-settings", body)
			expectAPIError(t, response, http.StatusUnprocessableEntity, "validation_failed")
		})
	}
	mixed := bearerRequest(api, http.MethodPost, "/api/v2/next-admin-path/config/save", authorization,
		`{"safe_mode_enable":false,"currency":"USD"}`)
	if mixed.Code != http.StatusUnprocessableEntity {
		t.Fatalf("mixed safe/site config status=%d body=%s", mixed.Code, mixed.Body)
	}
}

func TestPersistedMigratedSecurePathStartsWithoutDeploymentFallback(t *testing.T) {
	database := cloneHTTPAPITestDatabase(t)
	if err := database.EnsureSiteAccessSettings(context.Background(), "687ecc03", time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	api := New(Dependencies{
		Store: database, PasswordHasher: newHTTPAPITestPasswordHasher(), Now: fixedNow,
		PanelURL: "https://panel.example.test",
	})
	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/687ecc03/site-settings", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("migrated administrator path status=%d want=401 body=%s", response.Code, response.Body)
	}
}

func TestNewDatabaseRequiresExplicitCompliantSecurePath(t *testing.T) {
	database := cloneHTTPAPITestDatabase(t)
	defer func() {
		failure := recover()
		if failure == nil || !strings.Contains(fmt.Sprint(failure), "XBOARD_LEGACY_ADMIN_PATH is required") {
			t.Fatalf("startup panic=%v, want missing secure path error", failure)
		}
	}()
	_ = New(Dependencies{
		Store: database, PasswordHasher: newHTTPAPITestPasswordHasher(), Now: fixedNow,
		PanelURL: "https://panel.example.test",
	})
}

func TestModernAdminAPIRequiresCurrentSecurePathBeforeAuthorization(t *testing.T) {
	api, _ := newTestAPI(t)
	admin := loginAdmin(t, api)

	fixed := admin.rawRequest(t, api, http.MethodGet, "/api/v1/admin/site-settings", "")
	if fixed.Code != http.StatusNotFound {
		t.Fatalf("fixed modern administrator path status=%d want=404 body=%s", fixed.Code, fixed.Body)
	}
	wrong := admin.rawRequest(t, api, http.MethodGet, "/api/v1/admin/wrong-admin-path/site-settings", "")
	if wrong.Code != http.StatusNotFound {
		t.Fatalf("wrong modern administrator path status=%d want=404 body=%s", wrong.Code, wrong.Body)
	}
	current := admin.rawRequest(t, api, http.MethodGet, "/api/v1/admin/"+testAdminPath+"/site-settings", "")
	if current.Code != http.StatusOK || strings.Contains(current.Body.String(), "secure-admin-01") == false {
		t.Fatalf("current modern administrator path status=%d body=%s", current.Code, current.Body)
	}
}
