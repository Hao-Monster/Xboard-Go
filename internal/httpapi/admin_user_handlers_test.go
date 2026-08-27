package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestAdminUserFullProfileModernAndLegacyUpdateParity(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	admin := loginAdmin(t, api)

	resetMethod, speedLimit, deviceLimit := 1, 125, 5
	planGroupID := int64(8)
	plan, err := database.CreatePlan(ctx, store.SavePlanInput{
		GroupID: &planGroupID, TransferEnableGiB: 64, Name: "U2 parity plan",
		SpeedLimit: &speedLimit, DeviceLimit: &deviceLimit, ResetTrafficMethod: &resetMethod,
		Prices: store.PlanPrices{}, Tags: []string{},
	}, now)
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	inviterResponse := admin.request(t, api, http.MethodPost, "/api/v1/admin/users", `{
		"email":"u2-inviter@example.test","password":"inviter-password-123","group_id":7,
		"transfer_enable":1,"speed_limit":0,"device_limit":0,"banned":false
	}`)
	if inviterResponse.Code != http.StatusCreated {
		t.Fatalf("create inviter status=%d body=%s", inviterResponse.Code, inviterResponse.Body)
	}
	createdResponse := admin.request(t, api, http.MethodPost, "/api/v1/admin/users", `{
		"email":"u2-before@example.test","password":"before-password-123","group_id":7,
		"transfer_enable":1024,"speed_limit":10,"device_limit":2,"banned":false
	}`)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create subject status=%d body=%s", createdResponse.Code, createdResponse.Body)
	}
	var createdPayload struct {
		Data store.AdminUser `json:"data"`
	}
	decodeResponse(t, createdResponse, &createdPayload)
	created := createdPayload.Data
	userClient := loginAccount(t, api, created.Email, "before-password-123")

	expiresAt := now.AddDate(0, 2, 0).Format(time.RFC3339)
	updatedResponse := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", created.ID), fmt.Sprintf(`{
		"revision":%d,"email":"u2-after@example.test","password":"after-password-123",
		"group_id":7,"plan_id":%d,"invite_user_email":"u2-inviter@example.test",
		"transfer_enable":999,"traffic_upload":11000,"traffic_download":22000,"expired_at":%q,
		"speed_limit":1,"device_limit":1,"banned":false,"is_staff":true,
		"balance":12345,"commission_type":2,"commission_rate":18,"commission_balance":6789,"discount":80,
		"telegram_id":778899,"remind_expire":false,"remind_traffic":true,"remarks":"U2 priority account"
	}`, created.Revision, plan.ID, expiresAt))
	if updatedResponse.Code != http.StatusOK {
		t.Fatalf("modern full update status=%d body=%s", updatedResponse.Code, updatedResponse.Body)
	}
	var updatedPayload struct {
		Data store.AdminUser `json:"data"`
	}
	decodeResponse(t, updatedResponse, &updatedPayload)
	updated := updatedPayload.Data
	if updated.Email != "u2-after@example.test" || !updated.IsStaff || updated.PlanID == nil || *updated.PlanID != plan.ID ||
		updated.GroupID == nil || *updated.GroupID != 8 || updated.TransferEnable != 64*1024*1024*1024 ||
		updated.SpeedLimit != speedLimit || updated.DeviceLimit != deviceLimit || updated.TrafficUsed != 33_000 ||
		updated.Balance != 12_345 || updated.CommissionBalance != 6_789 || updated.InviteUserEmail == nil ||
		*updated.InviteUserEmail != "u2-inviter@example.test" || updated.TelegramID == nil || *updated.TelegramID != 778899 ||
		updated.Remarks == nil || *updated.Remarks != "U2 priority account" || updated.NextResetAt == nil {
		t.Fatalf("modern full update = %#v", updated)
	}
	if stale := userClient.request(t, api, http.MethodGet, "/api/v1/auth/session", ""); stale.Code != http.StatusUnauthorized {
		t.Fatalf("modern full update retained old login: status=%d body=%s", stale.Code, stale.Body)
	}
	if login := loginAccountResponse(api, "u2-after@example.test", "after-password-123"); login.Code != http.StatusOK {
		t.Fatalf("updated password login status=%d body=%s", login.Code, login.Body)
	}
	missingPlan := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", updated.ID), fmt.Sprintf(`{
		"revision":%d,"email":%q,"group_id":8,"plan_id":999999,"transfer_enable":%d,
		"expired_at":%q,"speed_limit":125,"device_limit":5,"banned":false
	}`, updated.Revision, updated.Email, updated.TransferEnable, expiresAt))
	if missingPlan.Code != http.StatusUnprocessableEntity || !strings.Contains(missingPlan.Body.String(), "plan_not_found") {
		t.Fatalf("modern missing plan status=%d body=%s", missingPlan.Code, missingPlan.Body)
	}
	missingInviter := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", updated.ID), fmt.Sprintf(`{
		"revision":%d,"email":%q,"group_id":8,"plan_id":%d,"invite_user_email":"missing-u2@example.test",
		"transfer_enable":%d,"expired_at":%q,"speed_limit":125,"device_limit":5,"banned":false
	}`, updated.Revision, updated.Email, plan.ID, updated.TransferEnable, expiresAt))
	if missingInviter.Code != http.StatusUnprocessableEntity || !strings.Contains(missingInviter.Body.String(), "invite_user_not_found") {
		t.Fatalf("modern missing inviter status=%d body=%s", missingInviter.Code, missingInviter.Body)
	}
	afterRejected, err := database.GetAdminUser(ctx, updated.ID)
	if err != nil || afterRejected.Revision != updated.Revision || afterRejected.PlanID == nil || *afterRejected.PlanID != plan.ID {
		t.Fatalf("modern rejected reference update mutated user: %#v err=%v", afterRejected, err)
	}

	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	legacy := bearerRequest(api, http.MethodPost, "/api/v2/admin/user/update", authorization,
		fmt.Sprintf(`{"id":%d,"revision":%d,"balance":45.67,"commission_balance":8.09,"remarks":"legacy dirty update"}`, updated.ID, updated.Revision))
	if legacy.Code != http.StatusOK || !containsAll(legacy.Body.String(), `"status":"success"`, `"data":true`) {
		t.Fatalf("legacy dirty update status=%d body=%s", legacy.Code, legacy.Body)
	}
	fresh, err := database.GetAdminUser(ctx, updated.ID)
	if err != nil {
		t.Fatalf("GetAdminUser() error = %v", err)
	}
	if fresh.Balance != 4567 || fresh.CommissionBalance != 809 || fresh.Remarks == nil || *fresh.Remarks != "legacy dirty update" ||
		fresh.PlanID == nil || *fresh.PlanID != plan.ID || fresh.TransferEnable != updated.TransferEnable || fresh.TrafficUsed != updated.TrafficUsed {
		encoded, _ := json.Marshal(fresh)
		t.Fatalf("legacy dirty update did not preserve untouched fields: %s", encoded)
	}
	legacyNoChange := bearerRequest(api, http.MethodPost, "/api/v2/admin/user/update", authorization,
		fmt.Sprintf(`{"id":%d,"is_distributor":0,"distributor_name":""}`, fresh.ID))
	if legacyNoChange.Code != http.StatusOK || !containsAll(legacyNoChange.Body.String(), `"status":"success"`, `"data":true`) {
		t.Fatalf("legacy observed no-change payload status=%d body=%s", legacyNoChange.Code, legacyNoChange.Body)
	}
	fresh, err = database.GetAdminUser(ctx, fresh.ID)
	if err != nil || fresh.IsDistributor || fresh.DistributorName != nil || fresh.Balance != 4567 || fresh.CommissionBalance != 809 {
		t.Fatalf("legacy observed no-change payload mutated profile: %#v err=%v", fresh, err)
	}
	invalidMoney := bearerRequest(api, http.MethodPost, "/api/v2/admin/user/update", authorization,
		fmt.Sprintf(`{"id":%d,"revision":%d,"balance":1.009}`, fresh.ID, fresh.Revision))
	if invalidMoney.Code != http.StatusUnprocessableEntity {
		t.Fatalf("legacy unsafe money status=%d body=%s", invalidMoney.Code, invalidMoney.Body)
	}
	afterInvalid, err := database.GetAdminUser(ctx, fresh.ID)
	if err != nil || afterInvalid.Revision != fresh.Revision || afterInvalid.Balance != fresh.Balance {
		t.Fatalf("legacy unsafe money mutated user: %#v err=%v", afterInvalid, err)
	}
	audits, err := database.ListAdminAuditLogs(ctx, store.AdminAuditFilter{Page: 1, PageSize: 20, Query: "secure_admin"})
	if err != nil {
		t.Fatalf("ListAdminAuditLogs() error = %v", err)
	}
	if audits.Total != 3 || len(audits.Items) != 3 {
		t.Fatalf("legacy user update audits = %#v, want two successful and one rejected attempt", audits)
	}
	for _, item := range audits.Items {
		if item.Route != "/api/v2/{secure_admin}/user/update" || strings.Contains(item.Route, "/api/v2/admin/") {
			t.Fatalf("legacy user audit leaked the dynamic administrator path: %#v", item)
		}
	}
}

func loginAccountResponse(api http.Handler, email, password string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

func TestLegacyOptionalBoolAcceptsOnlyObservedBooleanRepresentations(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		input string
		want  bool
	}{
		{name: "boolean true", input: "true", want: true},
		{name: "boolean false", input: "false", want: false},
		{name: "numeric one", input: "1", want: true},
		{name: "numeric zero", input: "0", want: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var value legacyOptionalBool
			if err := json.Unmarshal([]byte(testCase.input), &value); err != nil {
				t.Fatalf("UnmarshalJSON(%s) error = %v", testCase.input, err)
			}
			if !value.Set || value.Value != testCase.want || value.Pointer() == nil || *value.Pointer() != testCase.want {
				t.Fatalf("UnmarshalJSON(%s) = %#v", testCase.input, value)
			}
		})
	}
	for _, input := range []string{"null", "2", `"1"`, "-1", "1.0"} {
		t.Run("reject "+input, func(t *testing.T) {
			var value legacyOptionalBool
			if err := json.Unmarshal([]byte(input), &value); err == nil {
				t.Fatalf("UnmarshalJSON(%s) unexpectedly succeeded: %#v", input, value)
			}
		})
	}
}

func TestAdminUserPagedQueryAndLegacyFetchAreAllowlistedAndSecretFree(t *testing.T) {
	api, _ := newTestAPI(t)
	admin := loginAdmin(t, api)
	for _, body := range []string{
		`{"email":"directory-alpha@example.test","password":"secure-password-123","group_id":7,"transfer_enable":1000,"speed_limit":0,"device_limit":0,"banned":false}`,
		`{"email":"directory-beta@example.test","password":"secure-password-123","group_id":8,"transfer_enable":2000,"speed_limit":0,"device_limit":0,"banned":false}`,
		`{"email":"other@example.test","password":"secure-password-123","group_id":7,"transfer_enable":3000,"speed_limit":0,"device_limit":0,"banned":true}`,
	} {
		response := admin.request(t, api, http.MethodPost, "/api/v1/admin/users", body)
		if response.Code != http.StatusCreated {
			t.Fatalf("create directory fixture status=%d body=%s", response.Code, response.Body)
		}
	}
	filters := url.QueryEscape(`[{"field":"email","operator":"contains","value":"directory"},{"field":"banned","operator":"eq","value":false}]`)
	response := admin.request(t, api, http.MethodGet,
		"/api/v1/admin/users?page=1&page_size=1&sort_by=transfer_enable&sort_desc=true&filters="+filters, "")
	if response.Code != http.StatusOK || !containsAll(response.Body.String(),
		`"total":2`, `"page":1`, `"page_size":1`, `directory-beta@example.test`, `"traffic_used":0`, `"group_name":"Test group 8"`) {
		t.Fatalf("modern paged directory status=%d body=%s", response.Code, response.Body)
	}
	if containsAll(response.Body.String(), `"subscription_token"`) || containsAll(response.Body.String(), `"subscribe_url"`) || containsAll(response.Body.String(), `"uuid"`) {
		t.Fatalf("modern directory exposed subscription secrets: %s", response.Body)
	}
	postQuery := admin.request(t, api, http.MethodPost, "/api/v1/admin/users/query",
		`{"page":1,"page_size":20,"sort_by":"id","sort_desc":true,"email_prefix":"","banned":false,"group_id":null,"filters":[{"field":"subscription_token","operator":"eq","value":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}]}`)
	if postQuery.Code != http.StatusOK || !containsAll(postQuery.Body.String(), `"items":[]`, `"total":0`, `"page":1`, `"page_size":20`) {
		t.Fatalf("modern POST query status=%d body=%s", postQuery.Code, postQuery.Body)
	}
	if containsAll(postQuery.Body.String(), `0123456789abcdef`) {
		t.Fatalf("modern POST query reflected secret: %s", postQuery.Body)
	}
	for _, path := range []string{
		"/api/v1/admin/users?page=1&page_size=20&sort_by=id%20DESC%3BDELETE%20FROM%20users",
		"/api/v1/admin/users?page=1&page_size=20&filters=" + url.QueryEscape(`[{"field":"password_hash","operator":"eq","value":"hash"}]`),
		"/api/v1/admin/users?page=1&page_size=20&filters=" + url.QueryEscape(`[{"field":"subscription_token","operator":"eq","value":"secret"}]`),
	} {
		invalid := admin.request(t, api, http.MethodGet, path, "")
		if invalid.Code != http.StatusUnprocessableEntity {
			t.Fatalf("unsafe query %q status=%d body=%s", path, invalid.Code, invalid.Body)
		}
	}

	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	legacy := bearerRequest(api, http.MethodPost, "/api/v2/admin/user/fetch", authorization,
		`{"current":1,"pageSize":1,"filter":[{"id":"email","value":"directory"},{"id":"transfer_enable","value":"gt:1500"},{"id":"banned","value":false}],"sort":[{"id":"banned","desc":false},{"id":"transfer_enable","desc":true}]}`)
	if legacy.Code != http.StatusOK || !containsAll(legacy.Body.String(),
		`"total":1`, `"current_page":1`, `"per_page":1`, `directory-beta@example.test`, `"total_used":0`, `"group":{"id":8,"name":"Test group 8"}`) {
		t.Fatalf("legacy directory status=%d body=%s", legacy.Code, legacy.Body)
	}
	if containsAll(legacy.Body.String(), `"token"`) || containsAll(legacy.Body.String(), `"subscribe_url"`) || containsAll(legacy.Body.String(), `"uuid"`) {
		t.Fatalf("legacy directory exposed subscription secrets: %s", legacy.Body)
	}
	legacyUnsafe := bearerRequest(api, http.MethodPost, "/api/v2/admin/user/fetch", authorization,
		`{"current":1,"pageSize":20,"filter":[{"id":"password_hash","value":"hash"}]}`)
	if legacyUnsafe.Code != http.StatusUnprocessableEntity {
		t.Fatalf("legacy unsafe filter status=%d body=%s", legacyUnsafe.Code, legacyUnsafe.Body)
	}
}

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
		"unknown field": `{"email":"safe@example.test","password":"secure-password-123","group_id":7,"transfer_enable":1,"speed_limit":0,"device_limit":0,"banned":false,"unexpected":true}`,
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
	selfDemotion := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", detail.ID), fmt.Sprintf(`{
		"revision":%d,"email":"admin@example.test","group_id":null,"transfer_enable":0,
		"expired_at":null,"speed_limit":0,"device_limit":0,"banned":false,"is_admin":false
	}`, detail.Revision))
	if selfDemotion.Code != http.StatusUnprocessableEntity || !strings.Contains(selfDemotion.Body.String(), "cannot_remove_admin_self") {
		t.Fatalf("self demotion status = %d; body=%s", selfDemotion.Code, selfDemotion.Body)
	}
	legacyAuthorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	legacySelfBan := bearerRequest(api, http.MethodPost, "/api/v2/admin/user/update", legacyAuthorization,
		fmt.Sprintf(`{"id":%d,"banned":true}`, detail.ID))
	if legacySelfBan.Code != http.StatusUnprocessableEntity || !strings.Contains(legacySelfBan.Body.String(), "不能封禁自己的当前账号") {
		t.Fatalf("legacy self ban status = %d; body=%s", legacySelfBan.Code, legacySelfBan.Body)
	}
	legacySelfDemotion := bearerRequest(api, http.MethodPost, "/api/v2/admin/user/update", legacyAuthorization,
		fmt.Sprintf(`{"id":%d,"is_admin":false}`, detail.ID))
	if legacySelfDemotion.Code != http.StatusUnprocessableEntity || !strings.Contains(legacySelfDemotion.Body.String(), "不能撤销当前登录账号") {
		t.Fatalf("legacy self demotion status = %d; body=%s", legacySelfDemotion.Code, legacySelfDemotion.Body)
	}
	afterSelfProtection, err := database.GetAdminUser(context.Background(), detail.ID)
	if err != nil || afterSelfProtection.Banned || !afterSelfProtection.IsAdmin || afterSelfProtection.Revision != detail.Revision {
		t.Fatalf("self-protection requests mutated administrator: %#v err=%v", afterSelfProtection, err)
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

func TestAdminDistributorRoleIsExposedAndRevokesLoginWhenDisabled(t *testing.T) {
	api, _ := newTestAPI(t)
	admin := loginAdmin(t, api)
	createdResponse := admin.request(t, api, http.MethodPost, "/api/v1/admin/users", `{
		"email":"dealer@example.test","password":"dealer-password-123","group_id":null,
		"transfer_enable":0,"expired_at":null,"speed_limit":0,"device_limit":0,"banned":false,
		"is_admin":false,"is_staff":true,"is_distributor":true,"distributor_name":"  华东渠道  "
	}`)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create distributor status = %d body=%s", createdResponse.Code, createdResponse.Body)
	}
	var createdPayload struct {
		Data store.AdminUser `json:"data"`
	}
	decodeResponse(t, createdResponse, &createdPayload)
	created := createdPayload.Data
	if !created.IsStaff || !created.IsDistributor || created.DistributorName == nil || *created.DistributorName != "华东渠道" {
		t.Fatalf("created distributor = %#v", created)
	}

	dealer := loginAccount(t, api, "dealer@example.test", "dealer-password-123")
	session := dealer.request(t, api, http.MethodGet, "/api/v1/auth/session", "")
	if session.Code != http.StatusOK || !strings.Contains(session.Body.String(), `"is_distributor":true`) ||
		!strings.Contains(session.Body.String(), `"distributor_name":"华东渠道"`) || !strings.Contains(session.Body.String(), `"is_staff":true`) {
		t.Fatalf("distributor session status=%d body=%s", session.Code, session.Body)
	}

	updated := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", created.ID), fmt.Sprintf(`{
		"revision":%d,"email":"dealer@example.test","group_id":null,"transfer_enable":0,
		"expired_at":null,"speed_limit":0,"device_limit":0,"banned":false,
		"is_admin":false,"is_staff":true,"is_distributor":false,"distributor_name":"不得保留"
	}`, created.Revision))
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"is_distributor":false`) ||
		!strings.Contains(updated.Body.String(), `"distributor_name":null`) {
		t.Fatalf("disable distributor status=%d body=%s", updated.Code, updated.Body)
	}
	if staleSession := dealer.request(t, api, http.MethodGet, "/api/v1/auth/session", ""); staleSession.Code != http.StatusUnauthorized {
		t.Fatalf("disabled distributor session status=%d body=%s", staleSession.Code, staleSession.Body)
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
	forbiddenQuery := userClient.request(t, api, http.MethodPost, "/api/v1/admin/users/query", `{"page":1,"page_size":20,"filters":[]}`)
	if forbiddenQuery.Code != http.StatusForbidden {
		t.Fatalf("non-admin query status = %d; body=%s", forbiddenQuery.Code, forbiddenQuery.Body)
	}
	userAuthorization := loginLegacyBearer(t, api, "session-user@example.test", "session-password-123").Authorization
	forbiddenLegacy := bearerRequest(api, http.MethodPost, "/api/v2/admin/user/fetch", userAuthorization, `{"current":1,"pageSize":20}`)
	if forbiddenLegacy.Code != http.StatusForbidden {
		t.Fatalf("non-admin legacy directory status = %d; body=%s", forbiddenLegacy.Code, forbiddenLegacy.Body)
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
