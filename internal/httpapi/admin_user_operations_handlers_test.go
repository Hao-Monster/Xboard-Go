package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestAdminUserOperationEndpointsResetAndScopeRelatedData(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	admin := loginAdmin(t, api)
	publicOrigin := "https://admin-subscriptions.example.test"
	if _, err := database.UpdateLegacySiteSettings(ctx, 1, store.SaveLegacySiteSettingsInput{SubscribeURL: &publicOrigin}, now); err != nil {
		t.Fatal(err)
	}
	groupID := int64(7)
	resetMethod := 1
	plan, err := database.CreatePlan(ctx, store.SavePlanInput{
		GroupID: &groupID, TransferEnableGiB: 32, Name: "U4 API plan",
		ResetTrafficMethod: &resetMethod, Prices: store.PlanPrices{}, Tags: []string{},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := now.AddDate(0, 2, 0)
	account, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "u4-api@example.test", PasswordHash: "hash", GroupID: &groupID, PlanID: &plan.ID,
		TransferEnable: 32 << 30, ExpiredAt: &expiresAt,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	upload, download := int64(321), int64(654)
	if _, _, err := database.UpdateAdminUser(ctx, account.ID, store.UpdateAdminUserInput{
		Revision: account.Revision, Email: account.Email, GroupID: account.GroupID, PlanIDSet: true, PlanID: account.PlanID,
		TransferEnable: account.TransferEnable, TrafficUpload: &upload, TrafficDownload: &download,
		ExpiredAt: account.ExpiredAt, SpeedLimit: account.SpeedLimit, DeviceLimit: account.DeviceLimit, Banned: account.Banned,
	}, now); err != nil {
		t.Fatal(err)
	}
	injectedTarget := admin.request(t, api, http.MethodPost, fmt.Sprintf("/api/v1/admin/users/%d/orders", account.ID),
		fmt.Sprintf(`{"email":"other@example.test","plan_id":%d,"period":"monthly","total_amount":1300}`, plan.ID))
	if injectedTarget.Code != http.StatusBadRequest {
		t.Fatalf("scoped order accepted client target status=%d body=%s", injectedTarget.Code, injectedTarget.Body)
	}
	assigned := admin.request(t, api, http.MethodPost, fmt.Sprintf("/api/v1/admin/users/%d/orders", account.ID),
		fmt.Sprintf(`{"plan_id":%d,"period":"monthly","total_amount":1300}`, plan.ID))
	if assigned.Code != http.StatusCreated || !strings.Contains(assigned.Body.String(), fmt.Sprintf(`"user_id":%d`, account.ID)) {
		t.Fatalf("scoped order status=%d body=%s", assigned.Code, assigned.Body)
	}

	missingKey := admin.request(t, api, http.MethodPost, fmt.Sprintf("/api/v1/admin/users/%d/traffic-reset", account.ID), `{"reason":"manual check"}`)
	if missingKey.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing idempotency key status=%d body=%s", missingKey.Code, missingKey.Body)
	}
	reset := adminOperationRequest(t, admin, api, http.MethodPost,
		fmt.Sprintf("/api/v1/admin/users/%d/traffic-reset", account.ID), `{"reason":"manual check"}`, "u4-http-reset-0001")
	if reset.Code != http.StatusOK || !containsAll(reset.Body.String(), `"upload_before":321`, `"download_before":654`, `"idempotent":false`) {
		t.Fatalf("reset status=%d body=%s", reset.Code, reset.Body)
	}
	retry := adminOperationRequest(t, admin, api, http.MethodPost,
		fmt.Sprintf("/api/v1/admin/users/%d/traffic-reset", account.ID), `{"reason":"manual check"}`, "u4-http-reset-0001")
	if retry.Code != http.StatusOK || !strings.Contains(retry.Body.String(), `"idempotent":true`) {
		t.Fatalf("retry status=%d body=%s", retry.Code, retry.Body)
	}

	for name, path := range map[string]string{
		"orders":      fmt.Sprintf("/api/v1/admin/users/%d/orders?page=1&page_size=20", account.ID),
		"invitations": fmt.Sprintf("/api/v1/admin/users/%d/invitations?page=1&page_size=20", account.ID),
		"traffic":     fmt.Sprintf("/api/v1/admin/users/%d/traffic?page=1&page_size=20", account.ID),
		"resets":      fmt.Sprintf("/api/v1/admin/users/%d/traffic-resets?page=1&page_size=20", account.ID),
	} {
		t.Run(name, func(t *testing.T) {
			response := admin.request(t, api, http.MethodGet, path, "")
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"page":1`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
		})
	}
	traffic := admin.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/admin/users/%d/traffic?page=1&page_size=20", account.ID), "")
	if !containsAll(traffic.Body.String(), `"items":[]`, `"total":0`) {
		t.Fatalf("traffic body=%s", traffic.Body)
	}
	history := admin.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/admin/users/%d/traffic-resets?page=1&page_size=20", account.ID), "")
	if !containsAll(history.Body.String(), `"reason":"manual check"`, `"administrator_email":"admin@example.test"`) {
		t.Fatalf("history body=%s", history.Body)
	}

	token, err := database.GetAdminUserSubscriptionToken(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	subscription := admin.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/admin/users/%d/subscription-url", account.ID), "")
	if subscription.Code != http.StatusOK || subscription.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(subscription.Body.String(), token) || !strings.Contains(subscription.Body.String(), "https://admin-subscriptions.example.test/s/") {
		t.Fatalf("subscription status=%d headers=%v body=%s", subscription.Code, subscription.Header(), subscription.Body)
	}
	listed := admin.request(t, api, http.MethodGet, "/api/v1/admin/users?page=1&page_size=20", "")
	if strings.Contains(listed.Body.String(), token) {
		t.Fatalf("ordinary user directory leaked token: %s", listed.Body)
	}
}

func TestAdministratorSubscriptionSecurityResetInvalidatesTheOldCredentials(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	administrator := loginAdmin(t, api)
	account, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "admin-reset-subscription@example.test", PasswordHash: "hash", TransferEnable: 8 << 30,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	oldToken, err := database.GetAdminUserSubscriptionToken(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}

	resetPath := fmt.Sprintf("/api/v1/admin/users/%d/subscription-security/reset", account.ID)
	reset := administrator.request(t, api, http.MethodPost, resetPath, fmt.Sprintf(`{"revision":%d}`, account.Revision))
	if reset.Code != http.StatusOK || !containsAll(reset.Body.String(), `"status":"success"`, `"data":true`) || reset.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("administrator subscription reset status=%d headers=%v body=%s", reset.Code, reset.Header(), reset.Body)
	}
	updated, err := database.GetAdminUser(ctx, account.ID)
	if err != nil || updated.Revision != account.Revision+1 {
		t.Fatalf("administrator subscription reset user=(%#v,%v)", updated, err)
	}
	newToken, err := database.GetAdminUserSubscriptionToken(ctx, account.ID)
	if err != nil || newToken == oldToken {
		t.Fatalf("administrator subscription reset token changed=%t err=%v", newToken != oldToken, err)
	}
	if old := requestSubscription(api, "/s/"+oldToken); old.Code != http.StatusForbidden {
		t.Fatalf("old subscription after administrator reset status=%d body=%s", old.Code, old.Body)
	}
	if current := requestSubscription(api, "/s/"+newToken); current.Code != http.StatusOK {
		t.Fatalf("new subscription after administrator reset status=%d body=%s", current.Code, current.Body)
	}
	stale := administrator.request(t, api, http.MethodPost, resetPath, fmt.Sprintf(`{"revision":%d}`, account.Revision))
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale administrator subscription reset status=%d body=%s", stale.Code, stale.Body)
	}
	invalid := administrator.request(t, api, http.MethodPost, resetPath, fmt.Sprintf(`{"revision":%d,"unexpected":true}`, updated.Revision))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("administrator subscription reset accepted unknown field status=%d body=%s", invalid.Code, invalid.Body)
	}

	legacyAccount, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "legacy-admin-reset-subscription@example.test", PasswordHash: "hash", TransferEnable: 8 << 30,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	legacyOldToken, err := database.GetAdminUserSubscriptionToken(ctx, legacyAccount.ID)
	if err != nil {
		t.Fatal(err)
	}
	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	foreignOriginRequest := httptest.NewRequest(http.MethodPost, "/api/v2/admin/user/resetSecret", strings.NewReader(fmt.Sprintf(`{"id":%d}`, legacyAccount.ID)))
	foreignOriginRequest.Header.Set("Authorization", authorization)
	foreignOriginRequest.Header.Set("Content-Type", "application/json")
	foreignOriginRequest.Header.Set("Origin", "https://untrusted.example.test")
	foreignOriginResponse := httptest.NewRecorder()
	api.ServeHTTP(foreignOriginResponse, foreignOriginRequest)
	if foreignOriginResponse.Code != http.StatusForbidden {
		t.Fatalf("legacy administrator reset accepted foreign origin status=%d body=%s", foreignOriginResponse.Code, foreignOriginResponse.Body)
	}
	if currentToken, tokenErr := database.GetAdminUserSubscriptionToken(ctx, legacyAccount.ID); tokenErr != nil || currentToken != legacyOldToken {
		t.Fatalf("foreign-origin legacy reset mutated token changed=%t err=%v", currentToken != legacyOldToken, tokenErr)
	}
	legacyReset := bearerRequest(api, http.MethodPost, "/api/v2/admin/user/resetSecret", authorization, fmt.Sprintf(`{"id":%d}`, legacyAccount.ID))
	if legacyReset.Code != http.StatusOK || !containsAll(legacyReset.Body.String(), `"status":"success"`, `"message":"操作成功"`, `"data":true`) {
		t.Fatalf("legacy administrator subscription reset status=%d body=%s", legacyReset.Code, legacyReset.Body)
	}
	legacyNewToken, err := database.GetAdminUserSubscriptionToken(ctx, legacyAccount.ID)
	if err != nil || legacyNewToken == legacyOldToken {
		t.Fatalf("legacy administrator reset token changed=%t err=%v", legacyNewToken != legacyOldToken, err)
	}
	if old := requestSubscription(api, "/s/"+legacyOldToken); old.Code != http.StatusForbidden {
		t.Fatalf("old legacy subscription after administrator reset status=%d body=%s", old.Code, old.Body)
	}

	audits, err := database.ListAdminAuditLogs(ctx, store.AdminAuditFilter{Page: 1, PageSize: 20, Query: "subscription-security/reset"})
	if err != nil || audits.Total != 3 {
		t.Fatalf("modern administrator subscription reset audits=%#v err=%v", audits, err)
	}
	for _, audit := range audits.Items {
		if audit.Route != "/api/v1/admin/users/{userID}/subscription-security/reset" || strings.Contains(audit.Route, fmt.Sprint(account.ID)) {
			t.Fatalf("unsafe modern administrator subscription reset audit=%#v", audit)
		}
	}
	legacyAudits, err := database.ListAdminAuditLogs(ctx, store.AdminAuditFilter{Page: 1, PageSize: 20, Query: "resetSecret"})
	if err != nil || legacyAudits.Total != 2 {
		t.Fatalf("legacy administrator subscription reset audits=%#v err=%v", legacyAudits, err)
	}
	for _, audit := range legacyAudits.Items {
		if audit.Route != "/api/v2/{secure_admin}/user/resetSecret" || strings.Contains(audit.Route, "/api/v2/admin/") {
			t.Fatalf("unsafe legacy administrator subscription reset audit=%#v", audit)
		}
	}
}

func TestLegacyAdminTrafficResetAndTrafficHistoryCompatibility(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	groupID := int64(7)
	plan, err := database.CreatePlan(ctx, store.SavePlanInput{GroupID: &groupID, TransferEnableGiB: 8, Name: "U4 legacy plan", Prices: store.PlanPrices{}, Tags: []string{}}, now)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := now.AddDate(0, 1, 0)
	account, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "u4-legacy@example.test", PasswordHash: "hash", GroupID: &groupID, PlanID: &plan.ID,
		TransferEnable: 8 << 30, ExpiredAt: &expiresAt,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	upload, download := int64(44), int64(55)
	if _, _, err := database.UpdateAdminUser(ctx, account.ID, store.UpdateAdminUserInput{
		Revision: account.Revision, Email: account.Email, GroupID: account.GroupID, PlanIDSet: true, PlanID: account.PlanID,
		TransferEnable: account.TransferEnable, TrafficUpload: &upload, TrafficDownload: &download,
		ExpiredAt: account.ExpiredAt, SpeedLimit: account.SpeedLimit, DeviceLimit: account.DeviceLimit, Banned: account.Banned,
	}, now); err != nil {
		t.Fatal(err)
	}
	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	reset := bearerRequest(api, http.MethodPost, "/api/v2/admin/traffic-reset/reset-user", authorization,
		fmt.Sprintf(`{"user_id":%d,"reason":"legacy reset"}`, account.ID))
	if reset.Code != http.StatusOK || !containsAll(reset.Body.String(), `"message":"流量重置成功"`, `"user_id":`, `"reset_time":`, `"next_reset_at":`) ||
		strings.Contains(reset.Body.String(), `"upload_before"`) || strings.Contains(reset.Body.String(), `"status"`) {
		t.Fatalf("legacy reset status=%d body=%s", reset.Code, reset.Body)
	}
	history := bearerRequest(api, http.MethodGet, fmt.Sprintf("/api/v2/admin/traffic-reset/user/%d/history?limit=10", account.ID), authorization, "")
	if history.Code != http.StatusOK || !containsAll(history.Body.String(),
		`"data":{"history":[`, `"old_traffic":{"download":55,"formatted":"99 B","total":99,"upload":44}`,
		`"trigger_source":"manual"`, `"trigger_source_name":"手动触发"`, `"reason":"legacy reset"`,
		`"admin_email":"admin@example.test"`, `"user":{"email":"u4-legacy@example.test"`) ||
		strings.Contains(history.Body.String(), `"items"`) {
		t.Fatalf("legacy history status=%d body=%s", history.Code, history.Body)
	}
	invalidHistory := bearerRequest(api, http.MethodGet, fmt.Sprintf("/api/v2/admin/traffic-reset/user/%d/history?limit=51", account.ID), authorization, "")
	if invalidHistory.Code != http.StatusUnprocessableEntity {
		t.Fatalf("legacy history accepted limit 51 status=%d body=%s", invalidHistory.Code, invalidHistory.Body)
	}
	traffic := bearerRequest(api, http.MethodPost, "/api/v2/admin/stat/getStatUser", authorization,
		fmt.Sprintf(`{"user_id":%d,"pageSize":10,"page":1}`, account.ID))
	if traffic.Code != http.StatusOK || strings.TrimSpace(traffic.Body.String()) != `{"data":[],"total":0}` {
		t.Fatalf("legacy traffic status=%d body=%s", traffic.Code, traffic.Body)
	}
	audits, err := database.ListAdminAuditLogs(ctx, store.AdminAuditFilter{Page: 1, PageSize: 20, Query: "traffic-reset"})
	if err != nil {
		t.Fatal(err)
	}
	if audits.Total != 1 || len(audits.Items) != 1 || audits.Items[0].Route != "/api/v2/{secure_admin}/traffic-reset/reset-user" || strings.Contains(audits.Items[0].Route, "/api/v2/admin/") {
		t.Fatalf("legacy traffic reset audit = %#v", audits)
	}
}

func adminOperationRequest(t *testing.T, client testClient, api http.Handler, method, path, body, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", client.csrf)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	client.addCookies(request)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}
