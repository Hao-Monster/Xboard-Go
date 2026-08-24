package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestAdminUserLifecycleAndCursorAPI(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)

	createdResponse := admin.request(t, api, http.MethodPost, "/api/v1/admin/users", `{
		"email":" New.User@Example.Test ","password":"secure-password-123","group_id":7,
		"transfer_enable":1073741824,"expired_at":"2026-09-24T12:00:00Z","speed_limit":50,"device_limit":3,"banned":false
	}`)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", createdResponse.Code, createdResponse.Body)
	}
	if strings.Contains(createdResponse.Body.String(), "secure-password-123") || strings.Contains(createdResponse.Body.String(), "password_hash") {
		t.Fatal("create response exposed password material")
	}
	var createdPayload struct {
		Data store.AdminUser `json:"data"`
	}
	decodeResponse(t, createdResponse, &createdPayload)
	created := createdPayload.Data
	if created.Email != "new.user@example.test" || created.Revision != 1 || created.GroupID == nil || *created.GroupID != 7 {
		t.Fatalf("created user = %#v", created)
	}
	found, err := database.FindUserByEmail(context.Background(), created.Email)
	if err != nil || found.PasswordHash == "secure-password-123" {
		t.Fatalf("stored credential is not opaque: user=%#v err=%v", found, err)
	}

	listResponse := admin.request(t, api, http.MethodGet, "/api/v1/admin/users?limit=1&email_prefix=new&group_id=7&banned=false", "")
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", listResponse.Code, listResponse.Body)
	}
	var listPayload struct {
		Data store.AdminUserPage `json:"data"`
	}
	decodeResponse(t, listResponse, &listPayload)
	if len(listPayload.Data.Items) != 1 || listPayload.Data.Items[0].ID != created.ID {
		t.Fatalf("list payload = %#v", listPayload.Data)
	}

	detailResponse := admin.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/admin/users/%d", created.ID), "")
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("detail status = %d; body=%s", detailResponse.Code, detailResponse.Body)
	}

	updatedResponse := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", created.ID), fmt.Sprintf(`{
		"revision":%d,"email":"renamed@example.test","group_id":8,"transfer_enable":2147483648,
		"expired_at":null,"speed_limit":75,"device_limit":4,"banned":false
	}`, created.Revision))
	if updatedResponse.Code != http.StatusOK {
		t.Fatalf("update status = %d; body=%s", updatedResponse.Code, updatedResponse.Body)
	}
	var updatedPayload struct {
		Data store.AdminUser `json:"data"`
	}
	decodeResponse(t, updatedResponse, &updatedPayload)
	updated := updatedPayload.Data
	if updated.Email != "renamed@example.test" || updated.Revision != 2 || updated.GroupID == nil || *updated.GroupID != 8 || updated.ExpiredAt != nil {
		t.Fatalf("updated user = %#v", updated)
	}

	stale := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", created.ID), fmt.Sprintf(`{
		"revision":%d,"email":"stale@example.test","group_id":8,"transfer_enable":1,
		"expired_at":null,"speed_limit":0,"device_limit":0,"banned":false
	}`, created.Revision))
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "user_revision_conflict") {
		t.Fatalf("stale status = %d; body=%s", stale.Code, stale.Body)
	}

	reset := admin.request(t, api, http.MethodPut, fmt.Sprintf("/api/v1/admin/users/%d/password", created.ID), fmt.Sprintf(`{
		"revision":%d,"new_password":"another-secure-password-123"
	}`, updated.Revision))
	if reset.Code != http.StatusOK || strings.Contains(reset.Body.String(), "another-secure-password-123") {
		t.Fatalf("reset status = %d; body=%s", reset.Code, reset.Body)
	}
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"renamed@example.test","password":"another-secure-password-123"}`))
	login.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	api.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("reset password login status = %d; body=%s", loginResponse.Code, loginResponse.Body)
	}
}

func TestAdminUserAPIRejectsUnsafeAndAmbiguousChanges(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)

	for name, body := range map[string]string{
		"weak password": `{"email":"weak@example.test","password":"short","group_id":7,"transfer_enable":1,"speed_limit":0,"device_limit":0,"banned":false}`,
		"invalid email": `{"email":"not-an-email","password":"secure-password-123","group_id":7,"transfer_enable":1,"speed_limit":0,"device_limit":0,"banned":false}`,
		"unknown field": `{"email":"safe@example.test","password":"secure-password-123","group_id":7,"transfer_enable":1,"speed_limit":0,"device_limit":0,"banned":false,"is_admin":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := admin.request(t, api, http.MethodPost, "/api/v1/admin/users", body)
			if response.Code != http.StatusUnprocessableEntity && response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d; body=%s", response.Code, response.Body)
			}
		})
	}

	adminUser, err := database.FindUserByEmail(context.Background(), "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	detail, err := database.GetAdminUser(context.Background(), adminUser.ID)
	if err != nil {
		t.Fatal(err)
	}
	selfBan := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", detail.ID), fmt.Sprintf(`{
		"revision":%d,"email":"admin@example.test","group_id":null,"transfer_enable":0,
		"expired_at":null,"speed_limit":0,"device_limit":0,"banned":true
	}`, detail.Revision))
	if selfBan.Code != http.StatusUnprocessableEntity || !strings.Contains(selfBan.Body.String(), "cannot_ban_self") {
		t.Fatalf("self ban status = %d; body=%s", selfBan.Code, selfBan.Body)
	}

	missingRevision := admin.request(t, api, http.MethodPatch, "/api/v1/admin/users/999", `{
		"email":"missing@example.test","group_id":null,"transfer_enable":0,"expired_at":null,"speed_limit":0,"device_limit":0,"banned":false
	}`)
	if missingRevision.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing revision status = %d; body=%s", missingRevision.Code, missingRevision.Body)
	}
	invalidFilter := admin.request(t, api, http.MethodGet, "/api/v1/admin/users?banned=maybe", "")
	if invalidFilter.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid filter status = %d; body=%s", invalidFilter.Code, invalidFilter.Body)
	}
}

func TestInternalSubscriptionAccountIsHiddenAndCannotLogin(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	hasher := security.NewPasswordHasher(security.PasswordParams{MemoryKiB: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	hash, err := hasher.Hash("internal-password-123")
	if err != nil {
		t.Fatal(err)
	}
	internal, err := database.CreateRuntimeUser(ctx, store.CreateRuntimeUserInput{
		Email: "internal-login@example.test", PasswordHash: hash, AccountKind: store.AccountKindInternalSubscription,
		UUID: "7b4a5542-1101-40b8-8aab-6ea1a7e3d0d8", GroupID: 7, TransferEnable: 1_000,
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}

	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"internal-login@example.test","password":"internal-password-123"}`))
	login.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, login)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "invalid_credentials") {
		t.Fatalf("internal login status = %d; body=%s", response.Code, response.Body)
	}
	admin := loginAdmin(t, api)
	list := admin.request(t, api, http.MethodGet, "/api/v1/admin/users?email_prefix=internal-login", "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), "internal-login@example.test") {
		t.Fatalf("internal account leaked in list: status=%d body=%s", list.Code, list.Body)
	}
	detail := admin.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/admin/users/%d", internal.ID), "")
	if detail.Code != http.StatusNotFound {
		t.Fatalf("internal detail status = %d; body=%s", detail.Code, detail.Body)
	}
	groups, err := database.ListServerGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if group.ID == 7 && group.UsersCount != 0 {
			t.Fatalf("internal account leaked into group statistics: %#v", group)
		}
	}
}

func TestAdminUserPasswordResetRevokesExistingLogin(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	created := admin.request(t, api, http.MethodPost, "/api/v1/admin/users", `{
		"email":"session-user@example.test","password":"session-password-123","group_id":7,
		"transfer_enable":1,"speed_limit":0,"device_limit":0,"banned":false
	}`)
	var payload struct {
		Data store.AdminUser `json:"data"`
	}
	decodeResponse(t, created, &payload)
	userClient := loginAccount(t, api, "session-user@example.test", "session-password-123")
	forbidden := userClient.request(t, api, http.MethodGet, "/api/v1/admin/users", "")
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("non-admin directory status = %d; body=%s", forbidden.Code, forbidden.Body)
	}

	reset := admin.request(t, api, http.MethodPut, fmt.Sprintf("/api/v1/admin/users/%d/password", payload.Data.ID), fmt.Sprintf(`{"revision":%d,"new_password":"rotated-password-123"}`, payload.Data.Revision))
	if reset.Code != http.StatusOK {
		t.Fatalf("reset status = %d body=%s", reset.Code, reset.Body)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	userClient.addCookies(request)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("old session status = %d; body=%s", response.Code, response.Body)
	}
	_ = database
}
