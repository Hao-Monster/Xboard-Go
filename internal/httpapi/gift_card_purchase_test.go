package httpapi

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestGiftPurchaseAPIAdminToUserOrderLifecycle(t *testing.T) {
	api, db := newTestAPI(t)
	admin := loginAdmin(t, api)
	plan := createOrderAPIPlan(t, db, store.PlanPrices{"monthly": 100, "onetime": 100})
	user := createKnowledgeTestUser(t, db, "purchase-api@example.test", "purchase-password-123", 0, false)
	client := loginAs(t, api, user.email, user.password)
	body := fmt.Sprintf(`{"name":"购买码","type":4,"status":true,"rewards":{"plan_id":%d,"purchase_period":"onetime"}}`, plan.ID)
	forbidden := client.request(t, api, http.MethodPost, "/api/v1/admin/admin/gift-card/templates", body)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("unprivileged create=%d", forbidden.Code)
	}
	created := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/gift-card/templates", body)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", created.Code, created.Body)
	}
	var template struct {
		Data store.GiftCardTemplate `json:"data"`
	}
	decodeResponse(t, created, &template)
	if template.Data.Rewards.PurchasePeriod != "onetime" {
		t.Fatal("lost template period")
	}
	legacy := legacyGiftCardTemplate(template.Data)
	if legacy["rewards"].(map[string]any)["purchase_period"] != "onetime" {
		t.Fatal("legacy lost period")
	}
	generated := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/gift-card/codes/generate", fmt.Sprintf(`{"template_id":%d,"count":2,"max_usage":1}`, template.Data.ID))
	if generated.Code != http.StatusCreated {
		t.Fatalf("generate=%d %s", generated.Code, generated.Body)
	}
	var codes struct {
		Data []store.GiftCardCode `json:"data"`
	}
	decodeResponse(t, generated, &codes)
	if len(codes.Data) != 2 || codes.Data[0].PurchaseSnapshot == nil {
		t.Fatal("missing issued snapshot")
	}
	check := client.request(t, api, http.MethodPost, "/api/v1/user/gift-card/check", fmt.Sprintf(`{"code":%q}`, codes.Data[0].Code))
	if check.Code != http.StatusOK || !containsAll(check.Body.String(), `"can_redeem":true`, `"purchase_preview"`, `"expires_after":null`) {
		t.Fatalf("preview=%d %s", check.Code, check.Body)
	}
	var first store.GiftCardUsage
	for _, code := range []string{codes.Data[0].Code, codes.Data[0].Code, codes.Data[1].Code} {
		redeemed := client.request(t, api, http.MethodPost, "/api/v1/user/gift-card/redeem", fmt.Sprintf(`{"code":%q}`, code))
		if redeemed.Code != http.StatusOK {
			t.Fatalf("redeem=%d %s", redeemed.Code, redeemed.Body)
		}
		var result struct {
			Data struct {
				Usage store.GiftCardUsage `json:"usage"`
			} `json:"data"`
		}
		decodeResponse(t, redeemed, &result)
		if result.Data.Usage.OrderTradeNo == "" {
			t.Fatal("missing order")
		}
		if first.ID == 0 {
			first = result.Data.Usage
		} else if code == codes.Data[0].Code && result.Data.Usage.ID != first.ID {
			t.Fatal("retry created new usage")
		}
	}
	history := client.request(t, api, http.MethodGet, "/api/v1/user/gift-card/history", "")
	if history.Code != http.StatusOK || !containsAll(history.Body.String(), `"total":2`, first.OrderTradeNo) {
		t.Fatalf("history %d %s", history.Code, history.Body)
	}
	order, err := db.GetUserOrder(t.Context(), user.id, first.OrderTradeNo)
	if err != nil {
		t.Fatal(err)
	}
	if order.Source != "gift_card_purchase" || order.PaidAt != nil || order.TotalAmount != 0 {
		t.Fatalf("order=%+v", order)
	}
	legacyOrder := legacyOrderResponseOf(order)
	if legacyOrder.Source != "gift_card_purchase" || legacyOrder.PurchaseSnapshot == nil {
		t.Fatal("legacy order lost source")
	}
	for _, path := range []string{"/api/v1/orders", "/api/v1/orders/" + first.OrderTradeNo} {
		response := client.request(t, api, http.MethodGet, path, "")
		if response.Code != http.StatusOK || !containsAll(response.Body.String(), `"source":"gift_card_purchase"`, first.OrderTradeNo, `"purchase_snapshot"`) {
			t.Fatalf("order response %s: %d %s", path, response.Code, response.Body)
		}
	}
	response := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/orders/"+first.OrderTradeNo, "")
	if response.Code != http.StatusOK || !containsAll(response.Body.String(), `"source":"gift_card_purchase"`, codes.Data[0].BatchNo) {
		t.Fatalf("admin order: %d %s", response.Code, response.Body)
	}
	other := createKnowledgeTestUser(t, db, "other-purchase-api@example.test", "other-password-123", 0, false)
	otherClient := loginAs(t, api, other.email, other.password)
	denied := otherClient.request(t, api, http.MethodPost, "/api/v1/user/gift-card/redeem", fmt.Sprintf(`{"code":%q}`, codes.Data[0].Code))
	if denied.Code == http.StatusOK {
		t.Fatal("other user reused consumed code")
	}
}
