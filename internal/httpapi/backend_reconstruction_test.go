package httpapi

import (
	"crypto/md5"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestImportedDigestLoginUpgradesOnlyAfterCorrectPassword(t *testing.T) {
	for _, path := range []string{"/api/v1/auth/login", "/api/v1/passport/auth/login"} {
		t.Run(path, func(t *testing.T) {
			api, db := newTestAPI(t)
			user, err := db.FindUserByEmail(t.Context(), "admin@example.test")
			if err != nil {
				t.Fatal(err)
			}
			sum := md5.Sum([]byte("legacy-password-123fixture-salt"))
			encoded, err := security.EncodeLegacyPassword("md5salt", "fixture-salt", fmt.Sprintf("%x", sum))
			if err != nil {
				t.Fatal(err)
			}
			if err = db.ChangePassword(t.Context(), user.ID, user.PasswordHash, encoded, fixedNow()); err != nil {
				t.Fatal(err)
			}
			for _, password := range []string{"incorrect-password", "legacy-password-123"} {
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(fmt.Sprintf(`{"email":"admin@example.test","password":%q}`, password)))
				req.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				api.ServeHTTP(response, req)
				updated, err := db.FindUserByID(t.Context(), user.ID)
				if err != nil {
					t.Fatal(err)
				}
				if password == "incorrect-password" {
					if response.Code == http.StatusOK || updated.PasswordHash != encoded {
						t.Fatal("failed login changed credentials or authenticated")
					}
				} else if response.Code != http.StatusOK || !strings.HasPrefix(updated.PasswordHash, "$argon2id$") || !security.DefaultPasswordHasher().Verify(password, updated.PasswordHash) {
					t.Fatalf("upgrade failed: status=%d", response.Code)
				}
			}
		})
	}
}

func TestBackendParityAdminEndpointsAuthorizationAndContracts(t *testing.T) {
	api, db := newTestAPI(t)
	admin := loginAdmin(t, api)
	plan, err := db.CreatePlan(t.Context(), store.SavePlanInput{Name: "Visibility API", TransferEnableGiB: 1}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"/api/v1/admin/admin/plans/visibility", "/api/v1/admin/admin/traffic-resets", "/api/v1/admin/admin/traffic-resets/stats"}
	for _, path := range paths {
		response := httptest.NewRecorder()
		api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code < 400 {
			t.Fatalf("unauthenticated access: %s %d", path, response.Code)
		}
		response = admin.request(t, api, http.MethodGet, path, "")
		if response.Code != 200 {
			t.Fatalf("administrator access: %s %d %s", path, response.Code, response.Body)
		}
	}
	body := fmt.Sprintf(`{"plan_id":%d,"customer_visibility":"selected","distributor_visibility":"none","customer_user_ids":[],"distributor_user_ids":[]}`, plan.ID)
	response := admin.request(t, api, http.MethodPost, paths[0], body)
	if response.Code != 200 {
		t.Fatalf("save: %d %s", response.Code, response.Body)
	}
	response = admin.request(t, api, http.MethodGet, fmt.Sprintf("%s?id=%d", paths[0], plan.ID), "")
	if response.Code != 200 || !containsAll(response.Body.String(), `"customer_users":[]`, `"distributor_users":[]`, `"customer_visibility":"selected"`) {
		t.Fatalf("detail: %d %s", response.Code, response.Body)
	}
	for _, suffix := range []string{"?per_page=10001", "?reset_type=invalid", "?start_date=2026-10-02&end_date=2026-10-01", "?user_id=-1"} {
		response = admin.request(t, api, http.MethodGet, paths[1]+suffix, "")
		if response.Code < 400 || response.Code >= 500 {
			t.Fatalf("invalid filter: %s %d", suffix, response.Code)
		}
	}
	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	for _, path := range []string{"/api/v2/admin/plan/visibility", "/api/v2/admin/plan/visibility/users?audience=customer&q=admin", "/api/v2/admin/traffic-reset/logs", "/api/v2/admin/traffic-reset/stats"} {
		response = bearerRequest(api, http.MethodGet, path, authorization, "")
		if response.Code != http.StatusOK {
			t.Fatalf("legacy endpoint %s: %d %s", path, response.Code, response.Body)
		}
		response = bearerRequest(api, http.MethodGet, path, "", "")
		if response.Code != http.StatusForbidden {
			t.Fatalf("legacy endpoint lacks authentication: %s %d", path, response.Code)
		}
	}
	response = bearerRequest(api, http.MethodPost, "/api/v2/admin/plan/visibility", authorization, body)
	if response.Code != http.StatusOK {
		t.Fatalf("legacy visibility save: %d %s", response.Code, response.Body)
	}
}
