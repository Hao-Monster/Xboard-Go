package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestCouponUserAPIsQuoteAndCreateLegacyCompatibleOrder(t *testing.T) {
	api, database := newTestAPI(t)
	plan := createOrderAPIPlan(t, database, store.PlanPrices{"monthly": 100_000})
	user := createKnowledgeTestUser(t, database, "coupon-user@example.test", "coupon-user-password-123", 1, false)
	one := 1
	coupon, err := database.CreateCoupon(context.Background(), store.SaveCouponInput{
		Code: "FIXED123", Name: "固定 12.34", Type: store.CouponTypeFixed, Value: 1_234, Show: true,
		LimitUse: &one, LimitUseWithUser: &one, LimitPlanIDs: []int64{plan.ID}, LimitPeriods: []string{"monthly"},
		StartedAt: fixedNow().Add(-time.Hour), EndedAt: fixedNow().Add(time.Hour),
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	client := loginAs(t, api, user.email, user.password)
	quoted := client.request(t, api, http.MethodPost, "/api/v1/user/coupons/check",
		fmt.Sprintf(`{"code":%q,"plan_id":%d,"period":"month_price"}`, coupon.Code, plan.ID))
	if quoted.Code != http.StatusOK || !containsAll(quoted.Body.String(), `"coupon_discount_amount":1234`, `"total_after_coupon":98766`, `"code":"FIXED123"`) {
		t.Fatalf("modern coupon quote status=%d body=%s", quoted.Code, quoted.Body)
	}
	created := client.request(t, api, http.MethodPost, "/api/v1/orders",
		fmt.Sprintf(`{"plan_id":%d,"period":"month_price","coupon_code":%q}`, plan.ID, coupon.Code))
	if created.Code != http.StatusCreated || !containsAll(created.Body.String(), `"coupon_id":`+fmt.Sprint(coupon.ID), `"discount_amount":1234`, `"total_amount":98766`) {
		t.Fatalf("modern coupon order status=%d body=%s", created.Code, created.Body)
	}

	other := createKnowledgeTestUser(t, database, "coupon-legacy@example.test", "coupon-legacy-password-123", 1, false)
	legacyCoupon, err := database.CreateCoupon(context.Background(), store.SaveCouponInput{
		Code: "PERCENT15", Name: "比例 15%", Type: store.CouponTypePercentage, Value: 15, Show: true,
		LimitPlanIDs: []int64{plan.ID}, LimitPeriods: []string{"monthly"},
		StartedAt: fixedNow().Add(-time.Hour), EndedAt: fixedNow().Add(time.Hour),
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	authorization := loginLegacyBearer(t, api, other.email, other.password).Authorization
	legacyQuote := bearerRequest(api, http.MethodPost, "/api/v1/user/coupon/check", authorization,
		fmt.Sprintf(`{"code":%q,"plan_id":%d,"period":"month_price"}`, legacyCoupon.Code, plan.ID))
	if legacyQuote.Code != http.StatusOK || !containsAll(legacyQuote.Body.String(),
		`"status":"success"`, `"type":2`, `"value":15`, fmt.Sprintf(`"limit_plan_ids":["%d"]`, plan.ID), `"limit_period":["month_price"]`) {
		t.Fatalf("legacy coupon quote status=%d body=%s", legacyQuote.Code, legacyQuote.Body)
	}
	legacyOrder := bearerRequest(api, http.MethodPost, "/api/v1/user/order/save", authorization,
		fmt.Sprintf(`{"plan_id":%d,"period":"month_price","coupon_code":%q}`, plan.ID, legacyCoupon.Code))
	if legacyOrder.Code != http.StatusOK || !containsAll(legacyOrder.Body.String(), `"status":"success"`) {
		t.Fatalf("legacy coupon order status=%d body=%s", legacyOrder.Code, legacyOrder.Body)
	}
	invalid := bearerRequest(api, http.MethodPost, "/api/v1/user/coupon/check", authorization,
		fmt.Sprintf(`{"code":"NOPE","plan_id":%d,"period":"month_price"}`, plan.ID))
	if invalid.Code != http.StatusBadRequest || !containsAll(invalid.Body.String(), `"status":"fail"`, "优惠券无效") {
		t.Fatalf("legacy invalid coupon status=%d body=%s", invalid.Code, invalid.Body)
	}
}

func TestCouponAdministratorAPIsValidatePermissionsLegacyContractAndSafeCSV(t *testing.T) {
	api, database := newTestAPI(t)
	plan := createOrderAPIPlan(t, database, store.PlanPrices{"monthly": 100_000})
	admin := loginAdmin(t, api)
	user := createKnowledgeTestUser(t, database, "coupon-no-admin@example.test", "coupon-no-admin-password-123", 1, false)
	userClient := loginAs(t, api, user.email, user.password)
	started, ended := fixedNow().Add(-time.Hour).Unix(), fixedNow().Add(time.Hour).Unix()
	body := fmt.Sprintf(`{"code":"SAFE500","name":"=danger","type":1,"value":500,"show":true,"limit_use":3,"limit_use_with_user":2,"limit_plan_ids":[%d],"limit_period":["month_price"],"started_at":%d,"ended_at":%d}`, plan.ID, started, ended)
	forbidden := userClient.request(t, api, http.MethodPost, "/api/v1/admin/admin/coupons", body)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("non-admin create status=%d body=%s", forbidden.Code, forbidden.Body)
	}
	created := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/coupons", body)
	if created.Code != http.StatusCreated || !containsAll(created.Body.String(), `"code":"SAFE500"`, `"show":true`) {
		t.Fatalf("admin create status=%d body=%s", created.Code, created.Body)
	}
	var payload struct {
		Data store.Coupon `json:"data"`
	}
	decodeResponse(t, created, &payload)
	listed := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/coupons?query=safe&page=1&page_size=20", "")
	if listed.Code != http.StatusOK || !containsAll(listed.Body.String(), `"total":1`, `"code":"SAFE500"`) {
		t.Fatalf("admin list status=%d body=%s", listed.Code, listed.Body)
	}
	toggled := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/admin/coupons/%d/visibility", payload.Data.ID), `{"show":false}`)
	if toggled.Code != http.StatusOK || !containsAll(toggled.Body.String(), `"show":false`) {
		t.Fatalf("admin toggle status=%d body=%s", toggled.Code, toggled.Body)
	}
	updatedBody := fmt.Sprintf(`{"code":"SAFE500","name":"updated coupon","type":1,"value":500,"show":false,"limit_use":3,"limit_use_with_user":2,"limit_plan_ids":[%d],"limit_period":["monthly"],"started_at":%d,"ended_at":%d}`, plan.ID, started, ended)
	updated := admin.request(t, api, http.MethodPut, fmt.Sprintf("/api/v1/admin/admin/coupons/%d", payload.Data.ID), updatedBody)
	if updated.Code != http.StatusOK || !containsAll(updated.Body.String(), `"name":"updated coupon"`, `"show":false`) {
		t.Fatalf("admin update status=%d body=%s", updated.Code, updated.Body)
	}
	disposable := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/coupons", fmt.Sprintf(`{"code":"DELETE-ME","name":"temporary","type":1,"value":1,"started_at":%d,"ended_at":%d}`, started, ended))
	if disposable.Code != http.StatusCreated {
		t.Fatalf("admin disposable create status=%d body=%s", disposable.Code, disposable.Body)
	}
	var disposablePayload struct {
		Data store.Coupon `json:"data"`
	}
	decodeResponse(t, disposable, &disposablePayload)
	deleted := admin.request(t, api, http.MethodDelete, fmt.Sprintf("/api/v1/admin/admin/coupons/%d", disposablePayload.Data.ID), "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("admin delete status=%d body=%s", deleted.Code, deleted.Body)
	}

	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	legacyList := bearerRequest(api, http.MethodPost, "/api/v2/admin/coupon/fetch", authorization, `{"current":1,"pageSize":20}`)
	if legacyList.Code != http.StatusOK || !containsAll(legacyList.Body.String(), `"current_page":1`, `"per_page":20`, `"code":"SAFE500"`, `"name":"updated coupon"`, `"limit_period":["monthly"]`) {
		t.Fatalf("legacy list status=%d body=%s", legacyList.Code, legacyList.Body)
	}
	legacyToggle := bearerRequest(api, http.MethodPost, "/api/v2/admin/coupon/show", authorization, fmt.Sprintf(`{"id":%d}`, payload.Data.ID))
	if legacyToggle.Code != http.StatusOK || !containsAll(legacyToggle.Body.String(), `"data":true`) {
		t.Fatalf("legacy toggle status=%d body=%s", legacyToggle.Code, legacyToggle.Body)
	}

	batch := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/coupons/batch", fmt.Sprintf(`{"name":"=cmd|' /C calc'!A0","type":1,"value":100,"count":2,"started_at":%d,"ended_at":%d}`, started, ended))
	if batch.Code != http.StatusOK || !strings.HasPrefix(batch.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("batch CSV status=%d type=%q body=%s", batch.Code, batch.Header().Get("Content-Type"), batch.Body)
	}
	if strings.Contains(batch.Body.String(), "\n=cmd") || !strings.Contains(batch.Body.String(), "'=cmd") {
		t.Fatalf("batch CSV formula was not neutralized: %q", batch.Body.String())
	}
}
