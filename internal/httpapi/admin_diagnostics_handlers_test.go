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

	"github.com/Hao-Monster/Xboard-Go/internal/diagnostics"
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

func TestAdminDiagnosticsQueryEndpointNotConfigured(t *testing.T) {
	api, _ := newTestAPI(t)
	admin := loginAdmin(t, api)

	// Initialize scope
	admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/diagnostics/initialize", "{}")

	res := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/query", `{"diagnostic_id":"111111111111111111111111"}`)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when query endpoint not configured, got %d body=%s", res.Code, res.Body)
	}
	assertAPIErrorCode(t, res, http.StatusServiceUnavailable, "diagnostics_storage_not_configured")
}

func TestAdminDiagnosticsQueryWorkflow(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	// 1. Initialize scope
	initRes := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/diagnostics/initialize", `{"scope":"00000000-0000-4000-8000-000000000001"}`)
	if initRes.Code != http.StatusOK {
		t.Fatalf("initialize scope failed: %d body=%s", initRes.Code, initRes.Body)
	}

	// 2. Create customer and order using existing helper
	plan := createOrderAPIPlan(t, database, store.PlanPrices{"monthly": 10000})
	buyer, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email:        "subscriber@example.test",
		PasswordHash: "hash_direct",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(ctx, store.CreateOrderInput{
		UserID: buyer.ID,
		PlanID: plan.ID,
		Period: "monthly",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CompleteOrder(ctx, order.TradeNo, "direct_callback", now); err != nil {
		t.Fatal(err)
	}

	// Lookup customer identity
	lookupRes := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/lookup", fmt.Sprintf(`{"order_id":%d}`, order.ID))
	if lookupRes.Code != http.StatusOK {
		t.Fatalf("lookup failed: %d body=%s", lookupRes.Code, lookupRes.Body)
	}
	var lookupData struct {
		Data struct {
			DiagnosticID string `json:"diagnostic_id"`
		} `json:"data"`
	}
	json.Unmarshal(lookupRes.Body.Bytes(), &lookupData)
	diagID := lookupData.Data.DiagnosticID

	// 3. Spin up mock VictoriaLogs server
	vlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		query := r.FormValue("query")

		if strings.Contains(query, "simulate_503") {
			http.Error(w, "upstream cluster down", http.StatusServiceUnavailable)
			return
		}

		if strings.Contains(query, `trace_id:"single-conn"`) || strings.Contains(query, `connection_id:"conn1"`) {
			// Detail events for connection 1
			w.Header().Set("Content-Type", "application/stream+json")
			fmt.Fprintf(w, `{"event_time":"2026-10-24T12:00:00Z","event_id":"e1","event_family":"connection","panel_scope":"00000000-0000-4000-8000-000000000001","node_id":1,"runtime_id":"rt1","event":"diag.connection.open","diagnostic_id":%q,"connection_id":"conn1","seq":1}`+"\n", diagID)
			fmt.Fprintf(w, `{"event_time":"2026-10-24T12:00:01Z","event_id":"e2","event_family":"connection","panel_scope":"00000000-0000-4000-8000-000000000001","node_id":1,"runtime_id":"rt1","event":"diag.dns.result","diagnostic_id":%q,"connection_id":"conn1","seq":2,"resolver_operation_id":"op1"}`+"\n", diagID)
			return
		}

		if strings.Contains(query, `resolver_operation_id:"op1"`) {
			// DNS resolver events
			w.Header().Set("Content-Type", "application/stream+json")
			fmt.Fprintf(w, `{"event_time":"2026-10-24T12:00:00.050Z","event_id":"dns1","event_family":"resolver_operation","panel_scope":"00000000-0000-4000-8000-000000000001","node_id":1,"runtime_id":"rt1","event":"diag.dns.exchange.result","resolver_operation_id":"op1","operation_seq":1}`+"\n")
			return
		}

		// Connection summary events
		w.Header().Set("Content-Type", "application/stream+json")
		// conn1: success
		fmt.Fprintf(w, `{"event_time":"2026-10-24T12:00:00Z","event_id":"ev1","event_family":"connection","panel_scope":"00000000-0000-4000-8000-000000000001","node_id":1,"runtime_id":"rt1","event":"diag.connection.open","diagnostic_id":%q,"connection_id":"conn1","target_id":0,"seq":1,"destination_host":"api.success.com","destination_port":443}`+"\n", diagID)
		fmt.Fprintf(w, `{"event_time":"2026-10-24T12:00:01Z","event_id":"ev2","event_family":"connection","panel_scope":"00000000-0000-4000-8000-000000000001","node_id":1,"runtime_id":"rt1","event":"diag.relay.result","diagnostic_id":%q,"connection_id":"conn1","target_id":0,"seq":2,"upload_bytes":100,"download_bytes":200,"transport_state":"transport_established"}`+"\n", diagID)
		// conn2: failure
		fmt.Fprintf(w, `{"event_time":"2026-10-24T12:01:00Z","event_id":"ev3","event_family":"connection","panel_scope":"00000000-0000-4000-8000-000000000001","node_id":1,"runtime_id":"rt1","event":"diag.connection.open","diagnostic_id":%q,"connection_id":"conn2","target_id":0,"seq":1,"destination_host":"api.fail.test","destination_port":443}`+"\n", diagID)
		fmt.Fprintf(w, `{"event_time":"2026-10-24T12:01:01Z","event_id":"ev4","event_family":"connection","panel_scope":"00000000-0000-4000-8000-000000000001","node_id":1,"runtime_id":"rt1","event":"diag.dial.result","diagnostic_id":%q,"connection_id":"conn2","target_id":0,"seq":2,"has_error":true,"error_stages":["dial"],"reason_codes":["connect_refused"]}`+"\n", diagID)
	}))
	defer vlServer.Close()

	// 4. Update diagnostics settings with the VL query endpoint
	updateRes := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/diagnostics/settings", fmt.Sprintf(`{
		"query_endpoint": %q,
		"query_username": "viewer",
		"query_password": "pwd",
		"cursor_signing_secret": "my-cursor-signing-secret-32bytes!"
	}`, vlServer.URL))
	if updateRes.Code != http.StatusOK {
		t.Fatalf("update settings failed: %d body=%s", updateRes.Code, updateRes.Body)
	}

	// 5. Query by Order ID (Full)
	queryRes := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/query", fmt.Sprintf(`{"order_id":%d}`, order.ID))
	if queryRes.Code != http.StatusOK {
		t.Fatalf("order query failed: %d body=%s", queryRes.Code, queryRes.Body)
	}

	var queryData struct {
		Data struct {
			DiagnosticID string `json:"diagnostic_id"`
			SnapshotID   string `json:"snapshot_id"`
			Items        []struct {
				ConnectionID string `json:"connection_id"`
				HasError     bool   `json:"has_error"`
			} `json:"items"`
			NextCursor *string `json:"next_cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(queryRes.Body.Bytes(), &queryData); err != nil {
		t.Fatalf("unmarshal query: %v", err)
	}
	if queryData.Data.DiagnosticID != diagID || len(queryData.Data.Items) != 2 {
		t.Fatalf("unexpected query result: %+v", queryData.Data)
	}

	// 6. Query with Filter (Error only)
	errQueryRes := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/query", fmt.Sprintf(`{"order_id":%d, "has_error":true}`, order.ID))
	if errQueryRes.Code != http.StatusOK {
		t.Fatalf("err query failed: %d body=%s", errQueryRes.Code, errQueryRes.Body)
	}
	var errData struct {
		Data struct {
			Items []struct {
				ConnectionID string `json:"connection_id"`
			} `json:"items"`
		} `json:"data"`
	}
	json.Unmarshal(errQueryRes.Body.Bytes(), &errData)
	if len(errData.Data.Items) != 1 || errData.Data.Items[0].ConnectionID != "conn2" {
		t.Fatalf("expected only conn2 in error filter: %+v", errData.Data)
	}

	// 7. Pagination with Cursor
	page1Res := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/query", fmt.Sprintf(`{"order_id":%d, "limit":1}`, order.ID))
	if page1Res.Code != http.StatusOK {
		t.Fatalf("page 1 failed: %d body=%s", page1Res.Code, page1Res.Body)
	}
	var page1Data struct {
		Data struct {
			Items []struct {
				ConnectionID string `json:"connection_id"`
			} `json:"items"`
			NextCursor *string `json:"next_cursor"`
		} `json:"data"`
	}
	json.Unmarshal(page1Res.Body.Bytes(), &page1Data)
	if len(page1Data.Data.Items) != 1 || page1Data.Data.NextCursor == nil {
		t.Fatalf("expected 1 item and next cursor: %+v", page1Data.Data)
	}

	page2Res := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/query", fmt.Sprintf(`{
		"order_id":%d,
		"cursor": %q,
		"limit": 1
	}`, order.ID, *page1Data.Data.NextCursor))
	if page2Res.Code != http.StatusOK {
		t.Fatalf("page 2 failed: %d body=%s", page2Res.Code, page2Res.Body)
	}
	var page2Data struct {
		Data struct {
			Items []struct {
				ConnectionID string `json:"connection_id"`
			} `json:"items"`
		} `json:"data"`
	}
	json.Unmarshal(page2Res.Body.Bytes(), &page2Data)
	if len(page2Data.Data.Items) != 1 || page2Data.Data.Items[0].ConnectionID == page1Data.Data.Items[0].ConnectionID {
		t.Fatalf("page 2 should have distinct item: %+v", page2Data.Data)
	}

	// 8. Connection Details & DNS Expansion
	connRes := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/connection", fmt.Sprintf(`{
		"order_id": %d,
		"node_id": 1,
		"runtime_id": "rt1",
		"connection_id": "conn1"
	}`, order.ID))
	if connRes.Code != http.StatusOK {
		t.Fatalf("conn detail failed: %d body=%s", connRes.Code, connRes.Body)
	}
	var connData struct {
		Data struct {
			ConnectionID string            `json:"connection_id"`
			Events       []json.RawMessage `json:"events"`
			DNSExchange  []json.RawMessage `json:"dns_exchange"`
		} `json:"data"`
	}
	json.Unmarshal(connRes.Body.Bytes(), &connData)
	if connData.Data.ConnectionID != "conn1" || len(connData.Data.Events) != 2 || len(connData.Data.DNSExchange) != 1 {
		t.Fatalf("unexpected conn details or DNS expansion: %+v", connData.Data)
	}
}

func TestAdminDiagnosticsConnectionForbiddenAccess(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	// Initialize scope
	admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/diagnostics/initialize", `{"scope":"00000000-0000-4000-8000-000000000001"}`)

	// Mock server that returns connection events belonging to another customer
	otherDiagID := "999999999999999999999999"
	vlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/stream+json")
		fmt.Fprintf(w, `{"event_time":"2026-10-24T12:00:00Z","event_id":"e1","event_family":"connection","panel_scope":"00000000-0000-4000-8000-000000000001","node_id":1,"runtime_id":"rt1","event":"diag.connection.open","diagnostic_id":%q,"connection_id":"conn99"}`+"\n", otherDiagID)
	}))
	defer vlServer.Close()

	database.UpdateDiagnosticsServerConfig(ctx, vlServer.URL, "user", []byte("pwd"), []byte("secret"), 1, now)

	// Admin requests connection "conn99" under a different diagnostic_id
	callerDiagID := "111111111111111111111111"
	res := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/connection", fmt.Sprintf(`{
		"diagnostic_id": %q,
		"node_id": 1,
		"runtime_id": "rt1",
		"connection_id": "conn99"
	}`, callerDiagID))

	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when accessing another tenant's connection, got %d body=%s", res.Code, res.Body)
	}
	assertAPIErrorCode(t, res, http.StatusForbidden, "forbidden_connection_access")
}

func TestAdminDiagnosticsQueryStorageFailures(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/diagnostics/initialize", `{"scope":"00000000-0000-4000-8000-000000000001"}`)

	vlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
	}))
	defer vlServer.Close()

	database.UpdateDiagnosticsServerConfig(ctx, vlServer.URL, "user", []byte("pwd"), []byte("secret"), 1, now)

	res := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/query", `{"diagnostic_id":"111111111111111111111111"}`)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for upstream unavailable, got %d body=%s", res.Code, res.Body)
	}
	assertAPIErrorCode(t, res, http.StatusServiceUnavailable, "diagnostics_storage_unavailable")
}

func TestAdminDiagnosticsInvalidCursor(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/diagnostics/initialize", `{"scope":"00000000-0000-4000-8000-000000000001"}`)
	database.UpdateDiagnosticsServerConfig(ctx, "http://127.0.0.1:9999", "user", []byte("pwd"), []byte("secret"), 1, now)

	res := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/query", `{
		"diagnostic_id":"111111111111111111111111",
		"cursor": "invalid-base64-or-tampered-cursor"
	}`)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for invalid cursor, got %d body=%s", res.Code, res.Body)
	}
	assertAPIErrorCode(t, res, http.StatusUnprocessableEntity, "cursor_invalid")
}

func TestAdminDiagnosticsStartSessionLifecycle(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	scope := "00000000-0000-4000-8000-000000000001"
	if _, _, err := database.InitializeDiagnosticsScope(ctx, scope, now); err != nil {
		t.Fatal(err)
	}

	plan := createOrderAPIPlan(t, database, store.PlanPrices{"monthly": 10000})
	buyer, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email:        "session_buyer@example.test",
		PasswordHash: "hash_direct",
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	order, err := database.CreateOrder(ctx, store.CreateOrderInput{
		UserID: buyer.ID,
		PlanID: plan.ID,
		Period: "monthly",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CompleteOrder(ctx, order.TradeNo, "callback", now); err != nil {
		t.Fatal(err)
	}

	// 1. Missing order context (direct diagnostic_id only) rejected
	badInput := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/start", `{"idempotency_key":"k1"}`)
	assertAPIErrorCode(t, badInput, http.StatusUnprocessableEntity, "invalid_lookup_input")

	// 2. Missing idempotency key rejected
	noIdemp := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/start", fmt.Sprintf(`{"order_id":%d}`, order.ID))
	assertAPIErrorCode(t, noIdemp, http.StatusUnprocessableEntity, "validation_failed")

	// 3. Successful Start Session
	startPayload := fmt.Sprintf(`{"order_id":%d,"idempotency_key":"key-001"}`, order.ID)
	res := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/start", startPayload)
	if res.Code != http.StatusOK {
		t.Fatalf("start session status=%d body=%s", res.Code, res.Body)
	}
	var startResp struct {
		Data startDiagnosticsSessionResponse `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &startResp); err != nil {
		t.Fatalf("unmarshal start resp: %v", err)
	}
	sess := startResp.Data
	if sess.SessionID == "" || sess.OrderID != order.ID || sess.State != diagnostics.SessionStatePending || sess.Revision != 1 {
		t.Fatalf("unexpected start response: %+v", sess)
	}
	if sess.ExpiresAt.Sub(sess.IssuedAt) > diagnostics.MaxSessionDuration {
		t.Fatalf("expires_at exceeded max duration: %v", sess.ExpiresAt.Sub(sess.IssuedAt))
	}

	// 4. Repeated start with same idempotency key and order returns existing session
	repeatRes := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/start", startPayload)
	if repeatRes.Code != http.StatusOK {
		t.Fatalf("repeat start status=%d body=%s", repeatRes.Code, repeatRes.Body)
	}
	var repeatResp struct {
		Data startDiagnosticsSessionResponse `json:"data"`
	}
	if err := json.Unmarshal(repeatRes.Body.Bytes(), &repeatResp); err != nil {
		t.Fatalf("unmarshal repeat resp: %v", err)
	}
	if repeatResp.Data.SessionID != sess.SessionID || !repeatResp.Data.ExpiresAt.Equal(sess.ExpiresAt) {
		t.Fatalf("idempotent start mismatch: original=%+v repeat=%+v", sess, repeatResp.Data)
	}

	// 5. Mismatched parameters with same idempotency key returns 409 idempotency_conflict
	buyer2, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email:        "session_buyer_2@example.test",
		PasswordHash: "hash_direct",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	order2, err := database.CreateOrder(ctx, store.CreateOrderInput{
		UserID: buyer2.ID,
		PlanID: plan.ID,
		Period: "monthly",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CompleteOrder(ctx, order2.TradeNo, "callback", now); err != nil {
		t.Fatal(err)
	}
	conflictRes := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/start", fmt.Sprintf(`{"order_id":%d,"idempotency_key":"key-001"}`, order2.ID))
	assertAPIErrorCode(t, conflictRes, http.StatusConflict, "idempotency_conflict")

	// 6. Query status of the active session
	statusRes := admin.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/admin/admin/order/diagnostics/status?order_id=%d", order.ID), "")
	if statusRes.Code != http.StatusOK {
		t.Fatalf("status res code=%d body=%s", statusRes.Code, statusRes.Body)
	}
	var statusResp struct {
		Data diagnosticsStatusResponse `json:"data"`
	}
	if err := json.Unmarshal(statusRes.Body.Bytes(), &statusResp); err != nil {
		t.Fatalf("unmarshal status resp: %v", err)
	}
	if !statusResp.Data.HasSession || statusResp.Data.Session == nil || statusResp.Data.Session.SessionID != sess.SessionID {
		t.Fatalf("expected active session in status: %+v", statusResp.Data)
	}

	// 7. Stop Session with wrong session_id returns 404
	stopWrong := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/stop", fmt.Sprintf(`{"order_id":%d,"session_id":"wrong-session-id","idempotency_key":"stop-k1"}`, order.ID))
	assertAPIErrorCode(t, stopWrong, http.StatusNotFound, "session_not_found")

	// 8. Stop Session successfully
	stopRes := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/stop", fmt.Sprintf(`{"order_id":%d,"session_id":%q,"idempotency_key":"stop-k1"}`, order.ID, sess.SessionID))
	if stopRes.Code != http.StatusOK {
		t.Fatalf("stop session status=%d body=%s", stopRes.Code, stopRes.Body)
	}
	var stopResp struct {
		Data startDiagnosticsSessionResponse `json:"data"`
	}
	if err := json.Unmarshal(stopRes.Body.Bytes(), &stopResp); err != nil {
		t.Fatalf("unmarshal stop resp: %v", err)
	}
	if stopResp.Data.Revision <= sess.Revision {
		t.Fatalf("stop must advance revision: %d <= %d", stopResp.Data.Revision, sess.Revision)
	}
	if stopResp.Data.State != diagnostics.SessionStateStopped && stopResp.Data.State != diagnostics.SessionStateStopping {
		t.Fatalf("expected stopped or stopping state, got %s", stopResp.Data.State)
	}
}

func TestAdminDiagnosticsConcurrencyLimit(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	scope := "00000000-0000-4000-8000-000000000001"
	if _, _, err := database.InitializeDiagnosticsScope(ctx, scope, now); err != nil {
		t.Fatal(err)
	}

	plan := createOrderAPIPlan(t, database, store.PlanPrices{"monthly": 10000})

	// Start 10 sessions for 10 distinct customers
	for i := 1; i <= 10; i++ {
		buyer, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
			Email:        fmt.Sprintf("limit_buyer_%d@example.test", i),
			PasswordHash: "hash_direct",
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		ord, err := database.CreateOrder(ctx, store.CreateOrderInput{
			UserID: buyer.ID,
			PlanID: plan.ID,
			Period: "monthly",
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.CompleteOrder(ctx, ord.TradeNo, "cb", now); err != nil {
			t.Fatal(err)
		}

		res := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/start", fmt.Sprintf(`{"order_id":%d,"idempotency_key":"key-%d"}`, ord.ID, i))
		if res.Code != http.StatusOK {
			t.Fatalf("session %d status=%d body=%s", i, res.Code, res.Body)
		}
	}

	// 11th customer session must hit limit
	buyer11, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email:        "limit_buyer_11@example.test",
		PasswordHash: "hash_direct",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	ord11, err := database.CreateOrder(ctx, store.CreateOrderInput{
		UserID: buyer11.ID,
		PlanID: plan.ID,
		Period: "monthly",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CompleteOrder(ctx, ord11.TradeNo, "cb", now); err != nil {
		t.Fatal(err)
	}

	res11 := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/start", fmt.Sprintf(`{"order_id":%d,"idempotency_key":"key-11"}`, ord11.ID))
	assertAPIErrorCode(t, res11, http.StatusConflict, "lease_limit_reached")
}

func TestAdminDiagnosticsStorageHealthCheck(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	scope := "00000000-0000-4000-8000-000000000001"
	if _, _, err := database.InitializeDiagnosticsScope(ctx, scope, now); err != nil {
		t.Fatal(err)
	}

	plan := createOrderAPIPlan(t, database, store.PlanPrices{"monthly": 10000})
	buyer, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email:        "health_buyer@example.test",
		PasswordHash: "hash_direct",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	ord, err := database.CreateOrder(ctx, store.CreateOrderInput{
		UserID: buyer.ID,
		PlanID: plan.ID,
		Period: "monthly",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CompleteOrder(ctx, ord.TradeNo, "cb", now); err != nil {
		t.Fatal(err)
	}

	healthState := "blocked"
	healthScope := scope
	healthAge := int64(10)
	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/diagnostics/health" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"schema_version":1,"panel_scope":%q,"storage_id":"st1","state":%q,"check_age_seconds":%d}`+"\n", healthScope, healthState, healthAge)
			return
		}
		http.NotFound(w, r)
	}))
	defer healthServer.Close()

	database.UpdateDiagnosticsServerConfig(ctx, healthServer.URL, "user", []byte("pwd"), []byte("secret"), 1, now)

	// When health is "blocked", start session fails with diagnostics_storage_not_ready
	resBlocked := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/start", fmt.Sprintf(`{"order_id":%d,"idempotency_key":"hk-1"}`, ord.ID))
	assertAPIErrorCode(t, resBlocked, http.StatusServiceUnavailable, "diagnostics_storage_not_ready")

	// When health is "ready", start session succeeds
	healthState = "ready"
	resReady := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/start", fmt.Sprintf(`{"order_id":%d,"idempotency_key":"hk-2"}`, ord.ID))
	if resReady.Code != http.StatusOK {
		t.Fatalf("start with ready health status=%d body=%s", resReady.Code, resReady.Body)
	}

	// When health degrades back to "blocked", an idempotent repeat start for the existing session succeeds without failure
	healthState = "blocked"
	resIdempAfterDegrade := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/order/diagnostics/start", fmt.Sprintf(`{"order_id":%d,"idempotency_key":"hk-2"}`, ord.ID))
	if resIdempAfterDegrade.Code != http.StatusOK {
		t.Fatalf("idempotent start after storage degradation must return existing session: status=%d body=%s", resIdempAfterDegrade.Code, resIdempAfterDegrade.Body)
	}
}
