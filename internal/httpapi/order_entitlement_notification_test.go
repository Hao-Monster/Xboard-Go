package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/nodecoord"
	"github.com/Hao-Monster/Xboard-Go/internal/payment"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type entitlementCoordinator struct {
	nodecoord.Coordinator
	events []nodecoord.Event
}

func (c *entitlementCoordinator) Publish(_ context.Context, event nodecoord.Event) error {
	c.events = append(c.events, event)
	return nil
}

func entitlementTestServer(t *testing.T) (*server, *entitlementCoordinator, *bytes.Buffer) {
	t.Helper()
	_, db := newTestAPI(t)
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	coord := &entitlementCoordinator{}
	cipher, err := appsettings.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := &server{store: db, now: fixedNow, logger: logger, panelURL: "https://panel.example.test", settingsCipher: cipher, paymentGateway: payment.NewService(payment.Options{}), paymentWebhookRequests: newRequestLimiter(100, time.Minute), orderRequests: newRequestLimitGroup(100, 100)}
	s.hub = newWSHub(db, s.now, logger, nil, coord)
	return s, coord, logs
}

func entitlementConnection(t *testing.T, s *server, groupID int64) *wsConnection {
	t.Helper()
	machine, node := createWebSocketReportingNode(t, s.store, s.now())
	if _, err := s.store.SaveNodeRuntime(t.Context(), node.ID, store.SaveNodeRuntimeInput{RateMicros: 1000000, GroupIDs: []int64{groupID}, Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}`)}, s.now()); err != nil {
		t.Fatal(err)
	}
	c := &wsConnection{machineID: machine.ID, hub: s.hub, nodeIDs: map[int64]struct{}{node.ID: {}}, writeCh: make(chan wsEnvelope, 16), done: make(chan struct{})}
	s.hub.nodes[node.ID] = c
	return c
}

func assertEntitlementDelta(t *testing.T, c *wsConnection, action string, userID int64, uuid string) store.RuntimeUser {
	t.Helper()
	found := 0
	var notified store.RuntimeUser
	for len(c.writeCh) > 0 {
		event := <-c.writeCh
		if event.Event != "sync.user.delta" {
			continue
		}
		data := event.Data.(map[string]any)
		users := data["users"].([]store.RuntimeUser)
		if data["action"] != action || len(users) != 1 || users[0].ID != userID || users[0].UUID != uuid {
			t.Fatalf("unexpected delta=%+v", data)
		}
		found++
		notified = users[0]
	}
	if found != 1 {
		t.Fatalf("delta count=%d want 1", found)
	}
	return notified
}

func TestBL01PaymentWebhookNotifiesGroupsExactlyOnce(t *testing.T) {
	s, coord, _ := entitlementTestServer(t)
	ctx := t.Context()
	oldGroup, err := s.store.CreateServerGroup(ctx, "QA old group", fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	plan := createOrderAPIPlan(t, s.store, store.PlanPrices{"monthly": 1000})
	user, err := s.store.CreateRuntimeUser(ctx, store.CreateRuntimeUserInput{Email: "QA-entitlement@example.test", PasswordHash: "hash", GroupID: oldGroup.ID, UUID: "a06b7b33-c9a6-482c-a1c5-dd590c221839", TransferEnable: 100}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	oldConn := entitlementConnection(t, s, oldGroup.ID)
	newConn := entitlementConnection(t, s, *plan.GroupID)
	order, err := s.store.CreateOrder(ctx, store.CreateOrderInput{UserID: user.ID, PlanID: plan.ID, Period: "monthly"}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	method := createPaymentAPIMethod(t, s.store, store.PaymentProviderCoinPayments, map[string]string{"coinpayments_merchant_id": "merchant-one", "coinpayments_ipn_secret": "QA-secret", "coinpayments_currency": "CNY"}, 0, 0)
	if _, err := s.store.StartPaymentCheckout(ctx, store.StartPaymentCheckoutInput{UserID: user.ID, TradeNo: order.TradeNo, PaymentID: method.ID}, fixedNow()); err != nil {
		t.Fatal(err)
	}
	invoke := func(external string) {
		body := "merchant=merchant-one&status=100&amount1=10.00&currency1=CNY&item_number=" + order.TradeNo + "&txn_id=" + external
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HMAC", paymentWebhookHMAC("QA-secret", body))
		r.SetPathValue("method", string(method.Provider))
		r.SetPathValue("uuid", method.UUID)
		w := httptest.NewRecorder()
		s.paymentWebhook(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("callback status=%d body=%s", w.Code, w.Body)
		}
	}
	invoke("QA-one")
	assertEntitlementDelta(t, oldConn, "remove", user.ID, user.UUID)
	assertEntitlementDelta(t, newConn, "add", user.ID, user.UUID)
	if len(coord.events) != 1 || coord.events[0].Kind != nodecoord.EventRefreshGroups || len(coord.events[0].GroupIDs) != 2 {
		t.Fatalf("coordination=%+v", coord.events)
	}
	groups := map[int64]bool{}
	for _, id := range coord.events[0].GroupIDs {
		groups[id] = true
	}
	if !groups[oldGroup.ID] || !groups[*plan.GroupID] {
		t.Fatalf("groups=%v", groups)
	}
	invoke("QA-one")
	invoke("QA-second-receipt")
	if len(oldConn.writeCh) != 0 || len(newConn.writeCh) != 0 || len(coord.events) != 1 {
		t.Fatal("duplicate callback notified nodes")
	}
}

func TestBL01DistributorHandlersNotifySubscriber(t *testing.T) {
	s, coord, _ := entitlementTestServer(t)
	ctx := t.Context()
	plan := createOrderAPIPlan(t, s.store, store.PlanPrices{"monthly": 1000})
	if err := s.store.SavePlanVisibility(ctx, store.SavePlanVisibilityInput{PlanID: plan.ID, CustomerVisibility: "all", DistributorVisibility: "all", CustomerUserIDs: []int64{}, DistributorUserIDs: []int64{}}, fixedNow()); err != nil {
		t.Fatal(err)
	}
	dealer, err := s.store.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "QA-dealer@example.test", PasswordHash: "hash", IsDistributor: true, DistributorName: "QA dealer"}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	conn := entitlementConnection(t, s, *plan.GroupID)
	invoke := func(handler http.HandlerFunc, body, trade string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.SetPathValue("tradeNo", trade)
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(context.WithValue(ctx, sessionContextKey, store.SessionUser{UserID: dealer.ID, IsDistributor: true}))
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code >= 400 {
			t.Fatalf("handler=%d body=%s", w.Code, w.Body)
		}
		return w
	}
	invoke(s.createDistributorOrder, fmt.Sprintf(`{"plan_id":%d,"period":"monthly"}`, plan.ID), "")
	page, err := s.store.ListDistributorOrders(ctx, store.DistributorOrderFilter{DistributorUserID: &dealer.ID, Page: 1, PageSize: 10}, fixedNow())
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("orders=%+v err=%v", page, err)
	}
	order, err := s.store.GetDistributorOrderByID(ctx, page.Items[0].Order.ID, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	assertEntitlementDelta(t, conn, "add", order.Subscription.SubscriberUserID, order.Subscription.SubscriberUUID)
	if order.Subscription.SubscriberUserID == dealer.ID {
		t.Fatal("fixture subscriber must differ from dealer")
	}
	body := `{"period":"monthly","idempotency_key":"df2bbdb9-ff10-42fb-aef9-de86dd250701"}`
	invoke(s.renewDistributorOrder, body, order.Order.TradeNo)
	assertEntitlementDelta(t, conn, "add", order.Subscription.SubscriberUserID, order.Subscription.SubscriberUUID)
	invoke(s.renewDistributorOrder, body, order.Order.TradeNo)
	if len(conn.writeCh) != 0 || len(coord.events) != 2 {
		t.Fatalf("renewal replay emitted event: %+v", coord.events)
	}
}

func TestBL02LatePaymentLogsWithoutRevivingOrder(t *testing.T) {
	s, coord, logs := entitlementTestServer(t)
	ctx := t.Context()
	plan := createOrderAPIPlan(t, s.store, store.PlanPrices{"monthly": 1000})
	user, err := s.store.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "QA-late@example.test", PasswordHash: "hash"}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	order, err := s.store.CreateOrder(ctx, store.CreateOrderInput{UserID: user.ID, PlanID: plan.ID, Period: "monthly"}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	method := createPaymentAPIMethod(t, s.store, store.PaymentProviderCoinPayments, map[string]string{"coinpayments_merchant_id": "merchant-one", "coinpayments_ipn_secret": "QA-secret", "coinpayments_currency": "CNY"}, 0, 0)
	if _, err := s.store.StartPaymentCheckout(ctx, store.StartPaymentCheckoutInput{UserID: user.ID, TradeNo: order.TradeNo, PaymentID: method.ID}, fixedNow()); err != nil {
		t.Fatal(err)
	}
	if batch, err := s.store.ProcessStaleOrders(ctx, fixedNow().Add(151*time.Minute), 10); err != nil || batch.Cancelled != 1 {
		t.Fatalf("cancel=%+v err=%v", batch, err)
	}
	body := "merchant=merchant-one&status=100&amount1=10.00&currency1=CNY&item_number=" + order.TradeNo + "&txn_id=QA-late&private_payload=do-not-log"
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HMAC", paymentWebhookHMAC("QA-secret", body))
	r.SetPathValue("method", string(method.Provider))
	r.SetPathValue("uuid", method.UUID)
	s.now = func() time.Time { return fixedNow().Add(152 * time.Minute) }
	w := httptest.NewRecorder()
	s.paymentWebhook(w, r)
	if w.Code != http.StatusConflict || w.Body.String() != "fail" {
		t.Fatalf("late response=%d %s", w.Code, w.Body)
	}
	got, err := s.store.GetUserOrder(ctx, user.ID, order.TradeNo)
	if err != nil || got.Status != store.OrderStatusCancelled || len(coord.events) != 0 {
		t.Fatalf("late state=%+v events=%+v err=%v", got, coord.events, err)
	}
	if !containsAll(logs.String(), `"level":"ERROR"`, `"trade_no":"`+order.TradeNo+`"`, `"external_id":"QA-late"`, `"provider":"CoinPayments"`, `"payment_id":`) || strings.Contains(logs.String(), "do-not-log") {
		t.Fatalf("unexpected log=%s", logs.String())
	}
}

func TestBL01ManualAndFreeCompletionHandlers(t *testing.T) {
	for _, entry := range []string{"admin", "legacy_admin", "free", "legacy_free"} {
		t.Run(entry, func(t *testing.T) {
			s, coord, _ := entitlementTestServer(t)
			price := int64(1000)
			if strings.Contains(entry, "free") {
				price = 0
			}
			plan := createOrderAPIPlan(t, s.store, store.PlanPrices{"monthly": price})
			user, err := s.store.CreateRuntimeUser(t.Context(), store.CreateRuntimeUserInput{Email: "QA-manual@example.test", PasswordHash: "hash", GroupID: *plan.GroupID, UUID: "b65be836-b1e1-4d4f-981b-042ae3c8d960", TransferEnable: 1}, fixedNow())
			if err != nil {
				t.Fatal(err)
			}
			order, err := s.store.CreateOrder(t.Context(), store.CreateOrderInput{UserID: user.ID, PlanID: plan.ID, Period: "monthly"}, fixedNow())
			if err != nil {
				t.Fatal(err)
			}
			conn := entitlementConnection(t, s, *plan.GroupID)
			invoke := func() *httptest.ResponseRecorder {
				body := "{}"
				if strings.HasPrefix(entry, "legacy_") {
					body = fmt.Sprintf(`{"trade_no":%q}`, order.TradeNo)
				}
				r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				r.SetPathValue("tradeNo", order.TradeNo)
				r = r.WithContext(context.WithValue(r.Context(), sessionContextKey, store.SessionUser{UserID: user.ID, IsAdmin: true}))
				w := httptest.NewRecorder()
				switch entry {
				case "admin":
					s.paidAdminOrder(w, r)
				case "legacy_admin":
					s.legacyPaidAdminOrder(w, r)
				case "free":
					s.checkoutUserOrder(w, r)
				case "legacy_free":
					s.legacyCheckoutUserOrder(w, r)
				}
				return w
			}
			w := invoke()
			if w.Code != http.StatusOK {
				t.Fatalf("completion=%d %s", w.Code, w.Body)
			}
			assertEntitlementDelta(t, conn, "add", user.ID, user.UUID)
			got, err := s.store.GetUserOrder(t.Context(), user.ID, order.TradeNo)
			if err != nil || got.Status != store.OrderStatusCompleted {
				t.Fatalf("completed order=%+v err=%v", got, err)
			}
			w = invoke()
			if strings.Contains(entry, "admin") && w.Code < 400 {
				t.Fatal("admin repeated completion should fail")
			}
			if len(conn.writeCh) != 0 || len(coord.events) != 1 {
				t.Fatal("repeated completion notified")
			}
		})
	}
}

func TestBL01DistributorEntitlementUpdateHandlers(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			s, coord, _ := entitlementTestServer(t)
			ctx := t.Context()
			plan := createOrderAPIPlan(t, s.store, store.PlanPrices{"monthly": 1000})
			if err := s.store.SavePlanVisibility(ctx, store.SavePlanVisibilityInput{PlanID: plan.ID, CustomerVisibility: "all", DistributorVisibility: "all", CustomerUserIDs: []int64{}, DistributorUserIDs: []int64{}}, fixedNow()); err != nil {
				t.Fatal(err)
			}
			dealer, err := s.store.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "QA-edit@example.test", PasswordHash: "hash", IsDistributor: true, DistributorName: "QA"}, fixedNow())
			if err != nil {
				t.Fatal(err)
			}
			order, err := s.store.CreateDistributorOrder(ctx, store.CreateDistributorOrderInput{DistributorUserID: dealer.ID, PlanID: plan.ID, Period: "monthly"}, fixedNow())
			if err != nil {
				t.Fatal(err)
			}
			conn := entitlementConnection(t, s, *plan.GroupID)
			body := `{"transfer_enable":123456,"expired_at":null,"speed_limit":42,"device_limit":7}`
			if legacy {
				body = fmt.Sprintf(`{"order_id":%d,"transfer_enable":123456,"expired_at":null,"speed_limit":42,"device_limit":7}`, order.Order.ID)
			}
			r := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body))
			r.SetPathValue("orderID", fmt.Sprint(order.Order.ID))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			if legacy {
				s.legacyAdminUpdateDistributorEntitlement(w, r)
			} else {
				s.updateAdminDistributorEntitlement(w, r)
			}
			if w.Code != http.StatusOK {
				t.Fatalf("update=%d %s", w.Code, w.Body)
			}
			notified := assertEntitlementDelta(t, conn, "add", order.Subscription.SubscriberUserID, order.Subscription.SubscriberUUID)
			if notified.SpeedLimit != 42 || notified.DeviceLimit != 7 {
				t.Fatalf("stale runtime entitlement=%+v", notified)
			}
			updated, err := s.store.GetDistributorEntitlement(ctx, order.Order.ID)
			if err != nil || updated.TransferEnable != 123456 || updated.ExpiredAt != nil || updated.SpeedLimit != 42 || updated.DeviceLimit != 7 || len(coord.events) != 1 {
				t.Fatalf("updated=%+v events=%+v err=%v", updated, coord.events, err)
			}
		})
	}
}
