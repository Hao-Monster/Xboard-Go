package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/google/uuid"
)

func plainHTTPRequest(api http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res := httptest.NewRecorder()
	api.ServeHTTP(res, req)
	return res
}

func TestAdminDiagnosticsInitializeScope(t *testing.T) {
	api, _ := newTestAPI(t)
	admin := loginAdmin(t, api)

	// 1. Initial scope generation
	res := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/diagnostics/initialize", "{}")
	if res.Code != http.StatusOK {
		t.Fatalf("first initialize status=%d body=%s", res.Code, res.Body)
	}

	var firstInit struct {
		Data struct {
			PanelScope string `json:"panel_scope"`
			Created    bool   `json:"created"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &firstInit); err != nil {
		t.Fatalf("unmarshal first init: %v", err)
	}
	if len(firstInit.Data.PanelScope) != 36 || !firstInit.Data.Created {
		t.Fatalf("unexpected first init: %+v", firstInit)
	}

	// 2. Idempotent call returns identical scope with created = false
	res2 := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/diagnostics/initialize", `{"scope":"00000000-0000-4000-8000-000000000099"}`)
	if res2.Code != http.StatusOK {
		t.Fatalf("second initialize status=%d body=%s", res2.Code, res2.Body)
	}

	var secondInit struct {
		Data struct {
			PanelScope string `json:"panel_scope"`
			Created    bool   `json:"created"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res2.Body.Bytes(), &secondInit); err != nil {
		t.Fatalf("unmarshal second init: %v", err)
	}
	if secondInit.Data.PanelScope != firstInit.Data.PanelScope || secondInit.Data.Created {
		t.Fatalf("idempotency broken: first=%+v second=%+v", firstInit, secondInit)
	}
}

func TestAdminDiagnosticsInitializeInvalidScopeCandidate(t *testing.T) {
	api, _ := newTestAPI(t)
	admin := loginAdmin(t, api)

	res := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/diagnostics/initialize", `{"scope":"invalid-uuid-format"}`)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for invalid scope candidate, got %d body=%s", res.Code, res.Body)
	}
}

func TestAdminDiagnosticsOrderLookup(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	ctx := context.Background()
	now := time.Now()

	// 0. Lookup before scope init should return 503
	orderIDBeforeInit := int64(9001)
	preRes := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/lookup", fmt.Sprintf(`{"order_id":%d}`, orderIDBeforeInit))
	if preRes.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 before init, got %d body=%s", preRes.Code, preRes.Body)
	}

	// 1. Initialize scope
	scope := "00000000-0000-4000-8000-000000000001"
	_, _, err := database.InitializeDiagnosticsScope(ctx, scope, now)
	if err != nil {
		t.Fatalf("init scope failed: %v", err)
	}

	// 2. Set up fixtures using Store methods
	plan := createOrderAPIPlan(t, database, store.PlanPrices{"monthly": 10000})
	if err := database.SavePlanVisibility(ctx, store.SavePlanVisibilityInput{
		PlanID: plan.ID, CustomerVisibility: "all", DistributorVisibility: "all",
		CustomerUserIDs: []int64{}, DistributorUserIDs: []int64{},
	}, now); err != nil {
		t.Fatalf("save plan visibility: %v", err)
	}

	// Direct Buyer
	buyer, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "direct_buyer@example.test",
		PasswordHash: "hash_direct",
	}, now)
	if err != nil {
		t.Fatalf("create direct buyer: %v", err)
	}

	directOrder, err := database.CreateOrder(ctx, store.CreateOrderInput{
		UserID: buyer.ID,
		PlanID: plan.ID,
		Period: "monthly",
	}, now)
	if err != nil {
		t.Fatalf("create direct order: %v", err)
	}
	_, err = database.CompleteOrder(ctx, directOrder.TradeNo, "direct_callback", now)
	if err != nil {
		t.Fatalf("complete direct order: %v", err)
	}

	// Distributor Partner
	distributor, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "distributor_partner@example.test",
		PasswordHash: "hash_dist",
		IsDistributor: true,
		DistributorName: "Channel North",
	}, now)
	if err != nil {
		t.Fatalf("create distributor user: %v", err)
	}

	// Distributor Initial Order
	custName := "Acme Corp Customer"
	distOrder, err := database.CreateDistributorOrder(ctx, store.CreateDistributorOrderInput{
		DistributorUserID: distributor.ID,
		PlanID: plan.ID,
		Period: "monthly",
		CustomerName: &custName,
	}, now)
	if err != nil {
		t.Fatalf("create distributor initial order: %v", err)
	}

	// Distributor Renewal Order
	renewOrder, err := database.RenewDistributorOrder(ctx, store.RenewDistributorOrderInput{
		DistributorUserID: distributor.ID,
		TradeNo: distOrder.Order.TradeNo,
		Period: "monthly",
		IdempotencyKey: uuid.NewString(),
	}, now)
	if err != nil {
		t.Fatalf("renew distributor order: %v", err)
	}

	// 3. Test Direct Order lookup by trade_no
	lookupDirect := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/lookup", fmt.Sprintf(`{"trade_no":%q}`, directOrder.TradeNo))
	if lookupDirect.Code != http.StatusOK {
		t.Fatalf("lookup direct order status=%d body=%s", lookupDirect.Code, lookupDirect.Body)
	}

	bodyDirect := lookupDirect.Body.String()
	// Security audit: strictly verify NO UUID, token, subscribe_url, password in body
	for _, sensitive := range []string{"token", "subscribe_url", "password", "hash_direct"} {
		if strings.Contains(strings.ToLower(bodyDirect), sensitive) {
			t.Fatalf("SECURITY VIOLATION: lookup response leaked sensitive keyword %q: %s", sensitive, bodyDirect)
		}
	}

	var cardDirect struct {
		Data struct {
			OrderID             int64  `json:"order_id"`
			TradeNo             string `json:"trade_no"`
			OrderType           string `json:"order_type"`
			CustomerDisplayName string `json:"customer_display_name"`
			SubscriptionName    string `json:"subscription_name"`
			DiagnosticID        string `json:"diagnostic_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(lookupDirect.Body.Bytes(), &cardDirect); err != nil {
		t.Fatalf("unmarshal direct card: %v", err)
	}
	if cardDirect.Data.OrderID != directOrder.ID || cardDirect.Data.OrderType != "direct" {
		t.Fatalf("card direct mismatch: %+v", cardDirect)
	}
	if cardDirect.Data.CustomerDisplayName != "direct_buyer@example.test" {
		t.Fatalf("expected buyer email as display name, got: %s", cardDirect.Data.CustomerDisplayName)
	}
	if len(cardDirect.Data.DiagnosticID) != 24 {
		t.Fatalf("expected 24hex diagnostic_id, got: %s", cardDirect.Data.DiagnosticID)
	}

	// 4. Test Distributor Initial Order lookup by order_id
	lookupInitial := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/lookup", fmt.Sprintf(`{"order_id":%d}`, distOrder.Order.ID))
	if lookupInitial.Code != http.StatusOK {
		t.Fatalf("lookup distributor initial status=%d body=%s", lookupInitial.Code, lookupInitial.Body)
	}
	var cardInitial struct {
		Data struct {
			OrderID             int64  `json:"order_id"`
			OrderType           string `json:"order_type"`
			CustomerDisplayName string `json:"customer_display_name"`
			DiagnosticID        string `json:"diagnostic_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(lookupInitial.Body.Bytes(), &cardInitial); err != nil {
		t.Fatalf("unmarshal initial card: %v", err)
	}
	if cardInitial.Data.OrderType != "distributor_initial" || cardInitial.Data.CustomerDisplayName != "Acme Corp Customer" {
		t.Fatalf("initial card mismatch: %+v", cardInitial)
	}

	// 5. Test Distributor Renewal Order lookup
	lookupRenew := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/lookup", fmt.Sprintf(`{"trade_no":%q}`, renewOrder.Order.TradeNo))
	if lookupRenew.Code != http.StatusOK {
		t.Fatalf("lookup distributor renewal status=%d body=%s", lookupRenew.Code, lookupRenew.Body)
	}
	var cardRenew struct {
		Data struct {
			OrderType    string `json:"order_type"`
			DiagnosticID string `json:"diagnostic_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(lookupRenew.Body.Bytes(), &cardRenew); err != nil {
		t.Fatalf("unmarshal renew card: %v", err)
	}
	if cardRenew.Data.OrderType != "distributor_renewal" || cardRenew.Data.DiagnosticID != cardInitial.Data.DiagnosticID {
		t.Fatalf("renewal diagnostic_id must equal initial: initial=%q renewal=%q", cardInitial.Data.DiagnosticID, cardRenew.Data.DiagnosticID)
	}

	// 6. Non-existent order returns 404
	lookupMissing := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/lookup", `{"order_id":999999}`)
	if lookupMissing.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing order, got %d body=%s", lookupMissing.Code, lookupMissing.Body)
	}

	// 7. Invalid input returns 422
	lookupDual := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/lookup", fmt.Sprintf(`{"order_id":%d,"trade_no":%q}`, directOrder.ID, directOrder.TradeNo))
	if lookupDual.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for dual inputs, got %d body=%s", lookupDual.Code, lookupDual.Body)
	}
}

func TestAdminDiagnosticsSettingsSecurity(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	ctx := context.Background()

	scope := "00000000-0000-4000-8000-000000000001"
	_, _, err := database.InitializeDiagnosticsScope(ctx, scope, time.Now())
	if err != nil {
		t.Fatalf("init scope failed: %v", err)
	}

	// GET settings
	getRes := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/diagnostics/settings", "")
	if getRes.Code != http.StatusOK {
		t.Fatalf("get settings status=%d body=%s", getRes.Code, getRes.Body)
	}

	var settings struct {
		Data struct {
			PanelScope       string `json:"panel_scope"`
			HasQueryPassword bool   `json:"has_query_password"`
			Initialized      bool   `json:"initialized"`
		} `json:"data"`
	}
	if err := json.Unmarshal(getRes.Body.Bytes(), &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	if settings.Data.PanelScope != scope || !settings.Data.Initialized || settings.Data.HasQueryPassword {
		t.Fatalf("unexpected settings: %+v", settings)
	}

	// Prohibit changing panel_scope via PUT
	putTamper := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/diagnostics/settings", `{"panel_scope":"00000000-0000-4000-8000-000000000099"}`)
	if putTamper.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for tampering panel_scope, got %d body=%s", putTamper.Code, putTamper.Body)
	}

	// Legitimate PUT updating endpoint and credentials
	putRes := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/diagnostics/settings", `{
		"query_endpoint": "http://127.0.0.1:9428",
		"query_username": "vl_reader",
		"query_password": "super_secret_password",
		"cursor_signing_secret": "signing_key",
		"storage_generation": 2
	}`)
	if putRes.Code != http.StatusOK {
		t.Fatalf("legitimate put status=%d body=%s", putRes.Code, putRes.Body)
	}

	bodyStr := putRes.Body.String()
	if strings.Contains(bodyStr, "super_secret_password") || strings.Contains(bodyStr, "signing_key") {
		t.Fatalf("SECURITY VIOLATION: secret leaked in settings response: %s", bodyStr)
	}

	var updated struct {
		Data struct {
			PanelScope             string `json:"panel_scope"`
			QueryEndpoint          string `json:"query_endpoint"`
			QueryUsername          string `json:"query_username"`
			HasQueryPassword       bool   `json:"has_query_password"`
			HasCursorSigningSecret bool   `json:"has_cursor_signing_secret"`
			StorageGeneration      int64  `json:"storage_generation"`
		} `json:"data"`
	}
	if err := json.Unmarshal(putRes.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal updated settings: %v", err)
	}
	if updated.Data.PanelScope != scope || !updated.Data.HasQueryPassword || !updated.Data.HasCursorSigningSecret || updated.Data.StorageGeneration != 2 {
		t.Fatalf("updated settings mismatch: %+v", updated)
	}
}

func TestAdminDiagnosticsUnauthorizedAccess(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()

	// Anonymous access
	anonRes := plainHTTPRequest(api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/lookup", `{"order_id":9001}`)
	if anonRes.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for anonymous access, got %d body=%s", anonRes.Code, anonRes.Body)
	}

	// Normal user (non-admin) access
	now := time.Now()
	hasher := newHTTPAPITestPasswordHasher()
	pwdHash, err := hasher.Hash("password123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	_, err = database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "normal_user@example.test",
		PasswordHash: pwdHash,
		IsAdmin: false,
	}, now)
	if err != nil {
		t.Fatalf("create normal user: %v", err)
	}

	userClient := loginAs(t, api, "normal_user@example.test", "password123")
	userRes := userClient.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/lookup", `{"order_id":9001}`)
	if userRes.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin user access, got %d body=%s", userRes.Code, userRes.Body)
	}
}
