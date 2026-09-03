package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestAdministratorOrderParityFiltersSortsDetailsAndProtectsPaidCommission(t *testing.T) {
	api, database := newTestAPI(t)
	plan := createOrderAPIPlan(t, database, store.PlanPrices{"monthly": 1_000})
	admin := loginAdmin(t, api)
	publicOrigin := "https://order-subscriptions.example.test"
	if _, err := database.UpdateLegacySiteSettings(t.Context(), 1, store.SaveLegacySiteSettingsInput{SubscribeURL: &publicOrigin}, fixedNow()); err != nil {
		t.Fatal(err)
	}
	generated := admin.request(t, api, http.MethodPost, "/api/v1/invitations", `{}`)
	if generated.Code != http.StatusOK {
		t.Fatalf("generate invitation status=%d body=%s", generated.Code, generated.Body)
	}
	var generatedPayload struct {
		Data struct {
			Code string `json:"code"`
		} `json:"data"`
	}
	decodeResponse(t, generated, &generatedPayload)
	registrationBody, _ := json.Marshal(map[string]string{
		"email": "order-parity-buyer@example.test", "password": "order-parity-password-123",
		"password_confirmation": "order-parity-password-123", "invite_code": generatedPayload.Data.Code,
	})
	registered := plainAPIRequest(api, http.MethodPost, "/api/v1/auth/register", string(registrationBody))
	if registered.Code != http.StatusOK {
		t.Fatalf("register invited buyer status=%d body=%s", registered.Code, registered.Body)
	}
	buyer, err := database.FindUserByEmail(t.Context(), "order-parity-buyer@example.test")
	if err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(t.Context(), store.CreateOrderInput{UserID: buyer.ID, PlanID: plan.ID, Period: "monthly"}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	order, err = database.CompleteOrder(t.Context(), order.TradeNo, "gateway-parity-callback", fixedNow().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if order.CommissionBalance <= 0 || order.InviteUserID == nil {
		t.Fatalf("invited order omitted commission: %#v", order)
	}

	listed := admin.request(t, api, http.MethodGet,
		"/api/v1/admin/admin/orders?status=3&status=4&type=1&type=2&period=monthly&commission_status=0&sort_by=total_amount&sort_desc=false", "")
	if listed.Code != http.StatusOK || !containsAll(listed.Body.String(), order.TradeNo, `"commission_status":0`) {
		t.Fatalf("multi-filtered administrator orders status=%d body=%s", listed.Code, listed.Body)
	}
	injection := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/orders?sort_by=total_amount%20DESC%3B%20DROP%20TABLE%20orders", "")
	if injection.Code != http.StatusUnprocessableEntity {
		t.Fatalf("untrusted sort status=%d body=%s", injection.Code, injection.Body)
	}
	detail := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/orders/"+order.TradeNo, "")
	if detail.Code != http.StatusOK || !containsAll(detail.Body.String(),
		`"callback_no":"gateway-parity-callback"`, `"invite_user":{"id":`, `"email":"admin@example.test"`,
		`"commission_log":[]`, `"subscribe_url":"https://order-subscriptions.example.test/s/`) {
		t.Fatalf("administrator order detail status=%d body=%s", detail.Code, detail.Body)
	}

	updated := admin.request(t, api, http.MethodPatch, "/api/v1/admin/admin/orders/"+order.TradeNo+"/commission", `{"commission_status":3}`)
	if updated.Code != http.StatusOK || !containsAll(updated.Body.String(), `"commission_status":3`,
		`"invite_user":{"id":`, `"commission_log":[]`, `"subscribe_url":"https://order-subscriptions.example.test/s/`) {
		t.Fatalf("modern commission update status=%d body=%s", updated.Code, updated.Body)
	}
	legacyAuthorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	legacyListed := bearerRequest(api, http.MethodPost, "/api/v2/admin/order/fetch", legacyAuthorization,
		`{"current":1,"pageSize":20,"filter":[{"id":"status","value":[3,4]},{"id":"type","value":[1,2]},{"id":"period","value":["month_price"]},{"id":"commission_status","value":[3]}],"sort":[{"id":"total_amount","desc":false}]}`)
	if legacyListed.Code != http.StatusOK || !strings.Contains(legacyListed.Body.String(), order.TradeNo) {
		t.Fatalf("legacy multi-filtered orders status=%d body=%s", legacyListed.Code, legacyListed.Body)
	}
	legacyUpdated := bearerRequest(api, http.MethodPost, "/api/v2/admin/order/update", legacyAuthorization,
		fmt.Sprintf(`{"trade_no":%q,"commission_status":1}`, order.TradeNo))
	if legacyUpdated.Code != http.StatusOK || !containsAll(legacyUpdated.Body.String(), `"data":true`, `"status":"success"`) {
		t.Fatalf("legacy commission update status=%d body=%s", legacyUpdated.Code, legacyUpdated.Body)
	}

	processed, err := database.ProcessCommissions(t.Context(), fixedNow().Add(73*time.Hour), 100)
	if err != nil || processed.Paid != 1 {
		t.Fatalf("ProcessCommissions() = (%#v, %v)", processed, err)
	}
	repeated, err := database.ProcessCommissions(t.Context(), fixedNow().Add(74*time.Hour), 100)
	if err != nil || repeated.Paid != 0 {
		t.Fatalf("repeated ProcessCommissions() = (%#v, %v)", repeated, err)
	}
	commissionSummary := admin.request(t, api, http.MethodGet, "/api/v1/invitations", "")
	if commissionSummary.Code != http.StatusOK || !containsAll(commissionSummary.Body.String(),
		fmt.Sprintf(`"valid_commission":%d`, order.CommissionBalance),
		fmt.Sprintf(`"available_commission":%d`, order.CommissionBalance),
		`"pending_commission":0`, `"commission_distribution_enabled":false`, `"commission_distribution_rates":[]`) {
		t.Fatalf("paid invitation summary status=%d body=%s", commissionSummary.Code, commissionSummary.Body)
	}
	commissionLogs := admin.request(t, api, http.MethodGet, "/api/v1/invitations/commissions?page=1&page_size=20", "")
	if commissionLogs.Code != http.StatusOK || !containsAll(commissionLogs.Body.String(), order.TradeNo,
		fmt.Sprintf(`"get_amount":%d`, order.CommissionBalance), `"total":1`) {
		t.Fatalf("paid commission logs status=%d body=%s", commissionLogs.Code, commissionLogs.Body)
	}
	transferAmount := order.CommissionBalance / 2
	transferred := admin.request(t, api, http.MethodPost, "/api/v1/invitations/transfer", fmt.Sprintf(`{"amount":%d}`, transferAmount))
	if transferred.Code != http.StatusOK || !containsAll(transferred.Body.String(),
		fmt.Sprintf(`"commission_balance":%d`, order.CommissionBalance-transferAmount),
		fmt.Sprintf(`"balance":%d`, transferAmount)) {
		t.Fatalf("positive commission transfer status=%d body=%s", transferred.Code, transferred.Body)
	}
	refreshedSummary := admin.request(t, api, http.MethodGet, "/api/v1/invitations", "")
	if refreshedSummary.Code != http.StatusOK || !strings.Contains(refreshedSummary.Body.String(),
		fmt.Sprintf(`"available_commission":%d`, order.CommissionBalance-transferAmount)) {
		t.Fatalf("refreshed invitation summary status=%d body=%s", refreshedSummary.Code, refreshedSummary.Body)
	}
	rollback := admin.request(t, api, http.MethodPatch, "/api/v1/admin/admin/orders/"+order.TradeNo+"/commission", `{"commission_status":1}`)
	if rollback.Code != http.StatusConflict || !containsAll(rollback.Body.String(), `"code":"order_state_conflict"`) {
		t.Fatalf("paid commission rollback status=%d body=%s", rollback.Code, rollback.Body)
	}
	buyerClient := loginAs(t, api, "order-parity-buyer@example.test", "order-parity-password-123")
	forbidden := buyerClient.request(t, api, http.MethodPatch, "/api/v1/admin/admin/orders/"+order.TradeNo+"/commission", `{"commission_status":3}`)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("non-administrator commission update status=%d body=%s", forbidden.Code, forbidden.Body)
	}
}

func TestOrderAPICompletesFreeOrderAndEnforcesOwnershipAndCSRF(t *testing.T) {
	api, database := newTestAPI(t)
	plan := createOrderAPIPlan(t, database, store.PlanPrices{"monthly": 0})
	owner := createKnowledgeTestUser(t, database, "order-owner@example.test", "order-owner-password-123", 1, false)
	other := createKnowledgeTestUser(t, database, "order-other@example.test", "order-other-password-123", 1, false)
	ownerClient := loginAs(t, api, owner.email, owner.password)
	otherClient := loginAs(t, api, other.email, other.password)

	withoutCSRF := plainRequest(api, http.MethodPost, "/api/v1/orders", fmt.Sprintf(`{"plan_id":%d,"period":"month_price"}`, plan.ID))
	if withoutCSRF.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated order status=%d body=%s", withoutCSRF.Code, withoutCSRF.Body)
	}
	created := ownerClient.request(t, api, http.MethodPost, "/api/v1/orders", fmt.Sprintf(`{"plan_id":%d,"period":"month_price"}`, plan.ID))
	if created.Code != http.StatusCreated || !containsAll(created.Body.String(), `"period":"monthly"`, `"total_amount":0`, `"status":0`) {
		t.Fatalf("create order status=%d body=%s", created.Code, created.Body)
	}
	var payload struct {
		Data store.Order `json:"data"`
	}
	decodeResponse(t, created, &payload)
	if payload.Data.TradeNo == "" {
		t.Fatal("created order did not return a trade number")
	}

	foreign := otherClient.request(t, api, http.MethodGet, "/api/v1/orders/"+payload.Data.TradeNo, "")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign detail status=%d body=%s", foreign.Code, foreign.Body)
	}
	checkout := ownerClient.request(t, api, http.MethodPost, "/api/v1/orders/"+payload.Data.TradeNo+"/checkout", `{}`)
	if checkout.Code != http.StatusOK || !containsAll(checkout.Body.String(), `"status":3`, `"callback_no":"`+payload.Data.TradeNo+`"`) {
		t.Fatalf("checkout status=%d body=%s", checkout.Code, checkout.Body)
	}
	repeated := ownerClient.request(t, api, http.MethodPost, "/api/v1/orders/"+payload.Data.TradeNo+"/checkout", `{}`)
	if repeated.Code != http.StatusOK || !containsAll(repeated.Body.String(), `"status":3`) {
		t.Fatalf("repeated checkout status=%d body=%s", repeated.Code, repeated.Body)
	}
	listed := ownerClient.request(t, api, http.MethodGet, "/api/v1/orders", "")
	if listed.Code != http.StatusOK || !containsAll(listed.Body.String(), payload.Data.TradeNo, `"name":"API order plan"`) {
		t.Fatalf("list orders status=%d body=%s", listed.Code, listed.Body)
	}
}

func TestLegacyOrderAPIKeepsPeriodEnvelopeAndRejectsPaidCancellation(t *testing.T) {
	api, database := newTestAPI(t)
	plan := createOrderAPIPlan(t, database, store.PlanPrices{"monthly": 0})
	user := createKnowledgeTestUser(t, database, "legacy-order@example.test", "legacy-order-password-123", 1, false)
	authorization := loginLegacyBearer(t, api, user.email, user.password).Authorization

	created := bearerRequest(api, http.MethodPost, "/api/v1/user/order/save", authorization,
		fmt.Sprintf(`{"plan_id":%d,"period":"month_price"}`, plan.ID))
	if created.Code != http.StatusOK || !containsAll(created.Body.String(), `"message":"操作成功"`, `"status":"success"`) {
		t.Fatalf("legacy create status=%d body=%s", created.Code, created.Body)
	}
	var createdPayload struct {
		Data string `json:"data"`
	}
	decodeResponse(t, created, &createdPayload)
	checkout := bearerRequest(api, http.MethodPost, "/api/v1/user/order/checkout", authorization,
		fmt.Sprintf(`{"trade_no":%q}`, createdPayload.Data))
	if checkout.Code != http.StatusOK || checkout.Body.String() != "{\"data\":true,\"type\":-1}\n" {
		t.Fatalf("legacy checkout status=%d body=%q", checkout.Code, checkout.Body.String())
	}
	detail := bearerRequest(api, http.MethodGet, "/api/v1/user/order/detail?trade_no="+createdPayload.Data, authorization, "")
	if detail.Code != http.StatusOK || !containsAll(detail.Body.String(), `"period":"month_price"`, `"status":3`, `"plan"`,
		`"month_price":0`, `"payment":null`, `"try_out_plan_id":0`) {
		t.Fatalf("legacy detail status=%d body=%s", detail.Code, detail.Body)
	}
	cancel := bearerRequest(api, http.MethodPost, "/api/v1/user/order/cancel", authorization,
		fmt.Sprintf(`{"trade_no":%q}`, createdPayload.Data))
	if cancel.Code != http.StatusBadRequest || !containsAll(cancel.Body.String(), `"status":"fail"`) {
		t.Fatalf("legacy paid cancellation status=%d body=%s", cancel.Code, cancel.Body)
	}
	methods := bearerRequest(api, http.MethodGet, "/api/v1/user/order/getPaymentMethod", authorization, "")
	if methods.Code != http.StatusOK || !containsAll(methods.Body.String(), `"data":[]`) {
		t.Fatalf("legacy payment methods status=%d body=%s", methods.Code, methods.Body)
	}
}

func TestAdministratorCanAssignListCompleteAndCancelOrders(t *testing.T) {
	api, database := newTestAPI(t)
	plan := createOrderAPIPlan(t, database, store.PlanPrices{"monthly": 1_000})
	user := createKnowledgeTestUser(t, database, "assigned-order@example.test", "assigned-order-password-123", 1, false)
	admin := loginAdmin(t, api)

	assigned := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/orders",
		fmt.Sprintf(`{"email":%q,"plan_id":%d,"period":"month_price","total_amount":99}`, user.email, plan.ID))
	if assigned.Code != http.StatusCreated || !containsAll(assigned.Body.String(), `"total_amount":99`, `"status":0`) {
		t.Fatalf("assign status=%d body=%s", assigned.Code, assigned.Body)
	}
	var payload struct {
		Data store.Order `json:"data"`
	}
	decodeResponse(t, assigned, &payload)
	if len(payload.Data.TradeNo) != 32 {
		t.Fatalf("assigned trade number=%q, want legacy 32-character shape", payload.Data.TradeNo)
	}
	listed := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/orders?status=0&query=assigned-order", "")
	if listed.Code != http.StatusOK || !containsAll(listed.Body.String(), payload.Data.TradeNo, user.email, `"plan_name":"API order plan"`) {
		t.Fatalf("admin list status=%d body=%s", listed.Code, listed.Body)
	}
	paid := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/orders/"+payload.Data.TradeNo+"/paid", `{}`)
	if paid.Code != http.StatusOK || !containsAll(paid.Body.String(), `"status":3`, `"callback_no":"manual_operation"`) {
		t.Fatalf("admin paid status=%d body=%s", paid.Code, paid.Body)
	}
	detail := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/orders/"+payload.Data.TradeNo, "")
	if detail.Code != http.StatusOK || !containsAll(detail.Body.String(), user.email, `"entitlement_expired_at_after"`) {
		t.Fatalf("admin detail status=%d body=%s", detail.Code, detail.Body)
	}

	second := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/orders",
		fmt.Sprintf(`{"email":%q,"plan_id":%d,"period":"month_price","total_amount":50}`, user.email, plan.ID))
	if second.Code != http.StatusCreated {
		t.Fatalf("second assign status=%d body=%s", second.Code, second.Body)
	}
	decodeResponse(t, second, &payload)
	cancelled := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/orders/"+payload.Data.TradeNo+"/cancel", `{}`)
	if cancelled.Code != http.StatusOK || !containsAll(cancelled.Body.String(), `"status":2`) {
		t.Fatalf("admin cancel status=%d body=%s", cancelled.Code, cancelled.Body)
	}
	userClient := loginAs(t, api, user.email, user.password)
	forbidden := userClient.request(t, api, http.MethodGet, "/api/v1/admin/admin/orders", "")
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("non-admin list status=%d body=%s", forbidden.Code, forbidden.Body)
	}
}

func TestLegacyDynamicAdministratorOrderRoutesPreserveV2Contracts(t *testing.T) {
	api, database := newTestAPI(t)
	plan := createOrderAPIPlan(t, database, store.PlanPrices{"monthly": 1_000})
	user := createKnowledgeTestUser(t, database, "legacy-admin-order@example.test", "legacy-admin-order-password-123", 1, false)
	adminAuthorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	userAuthorization := loginLegacyBearer(t, api, user.email, user.password).Authorization
	const prefix = "/api/v2/admin/order"

	forbidden := bearerRequest(api, http.MethodPost, prefix+"/fetch", userAuthorization, `{"current":1,"pageSize":10}`)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("non-admin legacy fetch status=%d body=%s", forbidden.Code, forbidden.Body)
	}
	assigned := bearerRequest(api, http.MethodPost, prefix+"/assign", adminAuthorization,
		fmt.Sprintf(`{"email":%q,"plan_id":%d,"period":"month_price","total_amount":99}`, user.email, plan.ID))
	if assigned.Code != http.StatusOK || !containsAll(assigned.Body.String(), `"status":"success"`, `"message":"操作成功"`) {
		t.Fatalf("legacy administrator assign status=%d body=%s", assigned.Code, assigned.Body)
	}
	var assignedPayload struct {
		Data string `json:"data"`
	}
	decodeResponse(t, assigned, &assignedPayload)
	if len(assignedPayload.Data) != 32 {
		t.Fatalf("legacy administrator trade number=%q", assignedPayload.Data)
	}

	listed := bearerRequest(api, http.MethodPost, prefix+"/fetch", adminAuthorization,
		`{"current":1,"pageSize":10,"filter":[{"id":"status","value":0},{"id":"email","value":"legacy-admin-order"}]}`)
	if listed.Code != http.StatusOK || !containsAll(listed.Body.String(), `"current_page":1`, `"per_page":10`, assignedPayload.Data, `"period":"month_price"`, user.email) {
		t.Fatalf("legacy administrator fetch status=%d body=%s", listed.Code, listed.Body)
	}
	var page struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	decodeResponse(t, listed, &page)
	if len(page.Data) != 1 {
		t.Fatalf("legacy administrator fetch items=%d body=%s", len(page.Data), listed.Body)
	}
	detail := bearerRequest(api, http.MethodPost, prefix+"/detail", adminAuthorization, fmt.Sprintf(`{"id":%d}`, page.Data[0].ID))
	if detail.Code != http.StatusOK || !containsAll(detail.Body.String(), assignedPayload.Data, `"plan":{"capacity_limit":null`, `"prices":{"monthly":10}`,
		`"user":{"email":`, `"invite_user":null`, `"commission_log":[]`, `"subscribe_url":null`,
		`"actual_commission_balance":null`, `"entitlement_expired_at_before":null`, `"entitlement_expired_at_after":null`) {
		t.Fatalf("legacy administrator detail status=%d body=%s", detail.Code, detail.Body)
	}
	paid := bearerRequest(api, http.MethodPost, prefix+"/paid", adminAuthorization, fmt.Sprintf(`{"trade_no":%q}`, assignedPayload.Data))
	if paid.Code != http.StatusOK || !containsAll(paid.Body.String(), `"data":true`, `"status":"success"`) {
		t.Fatalf("legacy administrator paid status=%d body=%s", paid.Code, paid.Body)
	}
	completedDetail := bearerRequest(api, http.MethodPost, prefix+"/detail", adminAuthorization, fmt.Sprintf(`{"id":%d}`, page.Data[0].ID))
	if completedDetail.Code != http.StatusOK || !containsAll(completedDetail.Body.String(),
		`"subscribe_url":"https://panel.example.test/s/`, `"commission_log":[]`) {
		t.Fatalf("legacy completed administrator detail status=%d body=%s", completedDetail.Code, completedDetail.Body)
	}
	repeated := bearerRequest(api, http.MethodPost, prefix+"/paid", adminAuthorization, fmt.Sprintf(`{"trade_no":%q}`, assignedPayload.Data))
	if repeated.Code != http.StatusBadRequest || !containsAll(repeated.Body.String(), `"status":"fail"`, "只能对待支付的订单进行操作") {
		t.Fatalf("legacy repeated paid status=%d body=%s", repeated.Code, repeated.Body)
	}

	second := bearerRequest(api, http.MethodPost, prefix+"/assign", adminAuthorization,
		fmt.Sprintf(`{"email":%q,"plan_id":%d,"period":"month_price","total_amount":50}`, user.email, plan.ID))
	decodeResponse(t, second, &assignedPayload)
	cancelled := bearerRequest(api, http.MethodPost, prefix+"/cancel", adminAuthorization, fmt.Sprintf(`{"trade_no":%q}`, assignedPayload.Data))
	if cancelled.Code != http.StatusOK || !containsAll(cancelled.Body.String(), `"data":true`, `"status":"success"`) {
		t.Fatalf("legacy administrator cancel status=%d body=%s", cancelled.Code, cancelled.Body)
	}
	audit, err := database.ListAdminAuditLogs(context.Background(), store.AdminAuditFilter{Page: 1, PageSize: 20, Query: "secure_admin"})
	if err != nil {
		t.Fatal(err)
	}
	if audit.Total != 5 || len(audit.Items) != 5 {
		t.Fatalf("legacy administrator mutation audit=%#v, want five assign/paid/cancel attempts", audit)
	}
	for _, item := range audit.Items {
		if !strings.HasPrefix(item.Route, "/api/v2/{secure_admin}/order/") || strings.Contains(item.Route, "/api/v2/admin/") {
			t.Fatalf("legacy administrator audit leaked dynamic path: %#v", item)
		}
	}
}

func createOrderAPIPlan(t *testing.T, database *store.Store, prices store.PlanPrices) store.Plan {
	t.Helper()
	groupID := int64(1)
	plan, err := database.CreatePlan(context.Background(), store.SavePlanInput{
		Name: "API order plan", GroupID: &groupID, TransferEnableGiB: 100, Prices: prices,
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	plan, err = database.SetPlanState(context.Background(), plan.ID, plan.Revision,
		store.PlanState{Show: true, Sell: true, Renew: true}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
