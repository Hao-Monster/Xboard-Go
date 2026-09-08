package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestUserLifecycleHTTPPermissionsSessionsAndLegacyDestroy(t *testing.T) {
	api, db := newTestAPI(t)
	admin := loginAdmin(t, api)
	password := "lifecycle-http-password-123"
	create := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/users", fmt.Sprintf(`{"email":"lifecycle-http@example.test","password":%q,"transfer_enable":0,"banned":false}`, password))
	if create.Code != http.StatusCreated {
		t.Fatalf("create %d %s", create.Code, create.Body)
	}
	var payload struct {
		Data store.AdminUser `json:"data"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	user := payload.Data
	client := loginAccount(t, api, user.Email, password)
	bearer := loginLegacyBearer(t, api, user.Email, password)
	path := fmt.Sprintf("/api/v1/admin/admin/users/%d/deactivate", user.ID)
	body := fmt.Sprintf(`{"revision":%d}`, user.Revision)
	for _, action := range []string{"deactivate", "restore", "anonymize"} {
		route := fmt.Sprintf("/api/v1/admin/admin/users/%d/%s", user.ID, action)
		denied := client.request(t, api, http.MethodPost, route, body)
		if denied.Code != http.StatusForbidden {
			t.Fatalf("ordinary %s: %d %s", action, denied.Code, denied.Body)
		}
		visitor := httptest.NewRecorder()
		api.ServeHTTP(visitor, httptest.NewRequest(http.MethodPost, route, strings.NewReader(body)))
		if visitor.Code != http.StatusUnauthorized {
			t.Fatalf("visitor %s: %d", action, visitor.Code)
		}
	}
	withoutCSRF := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	admin.addCookies(withoutCSRF)
	denied := httptest.NewRecorder()
	api.ServeHTTP(denied, withoutCSRF)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("missing csrf %d", denied.Code)
	}
	changed := admin.request(t, api, http.MethodPost, path, body)
	if changed.Code != http.StatusOK || !strings.Contains(changed.Body.String(), `"lifecycle_status":"deactivated"`) {
		t.Fatalf("deactivate %d %s", changed.Code, changed.Body)
	}
	if response := client.request(t, api, http.MethodGet, "/api/v1/auth/session", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked cookie %d %s", response.Code, response.Body)
	}
	if response := bearerRequest(api, http.MethodGet, "/api/v1/user/info", bearer.Authorization, ""); response.Code == http.StatusOK {
		t.Fatalf("revoked bearer accepted %s", response.Body)
	}
	replay := admin.request(t, api, http.MethodPost, path, body)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay %d %s", replay.Code, replay.Body)
	}
	if response := admin.request(t, api, http.MethodPost, fmt.Sprintf("/api/v1/admin/admin/users/%d/anonymize", user.ID), fmt.Sprintf(`{"revision":%d}`, user.Revision+1)); response.Code != http.StatusConflict {
		t.Fatalf("early anonymization %d %s", response.Code, response.Body)
	}
	restored := admin.request(t, api, http.MethodPost, fmt.Sprintf("/api/v1/admin/admin/users/%d/restore", user.ID), fmt.Sprintf(`{"revision":%d}`, user.Revision+1))
	if restored.Code != http.StatusOK || !strings.Contains(restored.Body.String(), `"lifecycle_status":"active"`) {
		t.Fatalf("restore %d %s", restored.Code, restored.Body)
	}
	if response := loginAccountResponse(api, user.Email, password); response.Code != http.StatusOK {
		t.Fatalf("fresh login after restore %d %s", response.Code, response.Body)
	}
	legacyAdmin := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123")
	legacy := bearerRequest(api, http.MethodPost, "/api/v2/admin/user/destroy", legacyAdmin.Authorization, fmt.Sprintf(`{"id":%d}`, user.ID))
	if legacy.Code != http.StatusOK || !strings.Contains(legacy.Body.String(), `"data":true`) {
		t.Fatalf("legacy destroy %d %s", legacy.Code, legacy.Body)
	}
	retained, err := db.GetAdminUser(t.Context(), user.ID)
	if err != nil || retained.LifecycleStatus != "deactivated" || retained.Email != user.Email {
		t.Fatalf("legacy destroyed identity %+v %v", retained, err)
	}
}

func TestUserLifecycleHTTPExplicitAnonymizationAfterDeadline(t *testing.T) {
	api, db := newTestAPI(t)
	admin := loginAdmin(t, api)
	now := fixedNow()
	user, err := db.CreateAdminUser(t.Context(), store.CreateAdminUserInput{Email: "retained-http@example.test", PasswordHash: "hash"}, now.Add(-store.UserRecoveryWindow))
	if err != nil {
		t.Fatal(err)
	}
	inactive, _, err := db.ChangeUserLifecycle(t.Context(), store.UserLifecycleInput{AdministratorID: 1, UserID: user.ID, Revision: user.Revision, Action: "deactivate"}, now.Add(-store.UserRecoveryWindow))
	if err != nil {
		t.Fatal(err)
	}
	response := admin.request(t, api, http.MethodPost, fmt.Sprintf("/api/v1/admin/admin/users/%d/anonymize", user.ID), fmt.Sprintf(`{"revision":%d}`, inactive.Revision))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"lifecycle_status":"anonymized"`) || strings.Contains(response.Body.String(), user.Email) {
		t.Fatalf("explicit anonymization %d %s", response.Code, response.Body)
	}
	reset := admin.request(t, api, http.MethodPut, fmt.Sprintf("/api/v1/admin/admin/users/%d/password", user.ID), fmt.Sprintf(`{"revision":%d,"new_password":"resurrection-password-123"}`, inactive.Revision+1))
	if reset.Code != http.StatusConflict {
		t.Fatalf("tombstone password reset %d %s", reset.Code, reset.Body)
	}
}
