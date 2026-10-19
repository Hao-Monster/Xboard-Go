package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestGiftCardBearerCodeIdentityAcrossCheckRedeemAndHistory(t *testing.T) {
	for _, kind := range []store.GiftCardType{store.GiftCardTypeGeneral, store.GiftCardTypePlan, store.GiftCardTypeMystery, store.GiftCardTypePurchase} {
		t.Run(fmt.Sprintf("type_%d", kind), func(t *testing.T) {
			api, database := newTestAPI(t)
			user := createKnowledgeTestUser(t, database, "code-identity@example.test", "code-identity-password-123", 0, false)
			authorization := loginLegacyBearer(t, api, user.email, user.password).Authorization
			client := loginAs(t, api, user.email, user.password)
			admin, err := database.FindUserByEmail(t.Context(), "admin@example.test")
			if err != nil {
				t.Fatal(err)
			}
			plan := createOrderAPIPlan(t, database, store.PlanPrices{"monthly": 100, "onetime": 100})
			reward := store.GiftCardReward{Balance: 500}
			switch kind {
			case store.GiftCardTypePlan:
				reward = store.GiftCardReward{PlanID: &plan.ID, PlanValidityDays: 30}
			case store.GiftCardTypeMystery:
				reward = store.GiftCardReward{RandomRewards: []store.GiftCardRandomReward{{Weight: 1, Reward: store.GiftCardReward{Balance: 500}}}}
			case store.GiftCardTypePurchase:
				reward = store.GiftCardReward{PlanID: &plan.ID, PurchasePeriod: "onetime"}
			}
			template, err := database.CreateGiftCardTemplate(t.Context(), store.SaveGiftCardTemplateInput{
				Name: "Code identity", Type: kind, Status: true, Rewards: reward,
			}, admin.ID, fixedNow())
			if err != nil {
				t.Fatal(err)
			}
			// Both codes deliberately have the same masked prefix, so history must identify the exact code by ID.
			codes, err := database.GenerateGiftCardCodes(t.Context(), template.ID, store.GenerateGiftCardCodesInput{
				Count: 2, Prefix: "SAMECODE", MaxUsage: 1,
			}, fixedNow())
			if err != nil || len(codes) != 2 {
				t.Fatalf("generate codes count=%d error=%v", len(codes), err)
			}
			if codes[0].ID == codes[1].ID || codes[0].Code[:8] != codes[1].Code[:8] {
				t.Fatal("fixture must contain different codes with the same masked prefix")
			}
			for _, code := range codes {
				checked := bearerRequest(api, http.MethodPost, "/api/v1/user/gift-card/check", authorization, fmt.Sprintf(`{"code":%q}`, code.Code))
				if checked.Code != http.StatusOK {
					t.Fatalf("check status=%d body=%s", checked.Code, checked.Body)
				}
				var payload struct {
					Data struct {
						CodeInfo struct {
							CodeID int64  `json:"code_id"`
							Code   string `json:"code"`
						} `json:"code_info"`
						CanRedeem bool `json:"can_redeem"`
					} `json:"data"`
				}
				decodeResponse(t, checked, &payload)
				if payload.Data.CodeInfo.CodeID != code.ID || payload.Data.CodeInfo.Code != code.Code || !payload.Data.CanRedeem {
					t.Errorf("check code identity=%+v can_redeem=%t want code_id=%d", payload.Data.CodeInfo, payload.Data.CanRedeem, code.ID)
				}
			}

			modernCheck := client.request(t, api, http.MethodPost, "/api/v1/user/gift-card/check", fmt.Sprintf(`{"code":%q}`, codes[0].Code))
			var modernPreview struct {
				Data struct {
					CodeInfo  store.GiftCardCode     `json:"code_info"`
					Template  store.GiftCardTemplate `json:"template"`
					CanRedeem bool                   `json:"can_redeem"`
				} `json:"data"`
			}
			decodeResponse(t, modernCheck, &modernPreview)
			if modernCheck.Code != http.StatusOK || modernPreview.Data.CodeInfo.ID != codes[0].ID || modernPreview.Data.Template.Type != kind || !modernPreview.Data.CanRedeem {
				t.Fatalf("cookie check contract changed: status=%d body=%s", modernCheck.Code, modernCheck.Body)
			}
			redeemed := bearerRequest(api, http.MethodPost, "/api/v1/user/gift-card/redeem", authorization, fmt.Sprintf(`{"code":%q}`, codes[0].Code))
			if redeemed.Code != http.StatusOK {
				t.Fatalf("redeem status=%d body=%s", redeemed.Code, redeemed.Body)
			}
			var result struct {
				Data struct {
					CodeID  int64  `json:"code_id"`
					Message string `json:"message"`
				} `json:"data"`
			}
			decodeResponse(t, redeemed, &result)
			if result.Data.CodeID != codes[0].ID || result.Data.Message != "兑换成功！" {
				t.Errorf("redeem code_id=%d message=%q want code_id=%d", result.Data.CodeID, result.Data.Message, codes[0].ID)
			}

			history := bearerRequest(api, http.MethodGet, "/api/v1/user/gift-card/history", authorization, "")
			var legacyHistory struct {
				Data       []store.GiftCardUsage `json:"data"`
				Pagination struct {
					Total int64 `json:"total"`
				} `json:"pagination"`
			}
			decodeResponse(t, history, &legacyHistory)
			if history.Code != http.StatusOK || legacyHistory.Pagination.Total != 1 || len(legacyHistory.Data) != 1 {
				t.Fatalf("history status=%d body=%s", history.Code, history.Body)
			}
			usage := legacyHistory.Data[0]
			if usage.CodeID != codes[0].ID || usage.TemplateType != kind || usage.Code != "SAMECODE****" {
				t.Errorf("history code_id=%d type=%d code=%q want code_id=%d type=%d masked code", usage.CodeID, usage.TemplateType, usage.Code, codes[0].ID, kind)
			}
			if strings.Contains(history.Body.String(), codes[0].Code) || strings.Contains(history.Body.String(), codes[1].Code) {
				t.Error("history exposed full gift card code")
			}
			modernHistory := client.request(t, api, http.MethodGet, "/api/v1/user/gift-card/history", "")
			var modernUsages struct {
				Data store.GiftCardUsagePage `json:"data"`
			}
			decodeResponse(t, modernHistory, &modernUsages)
			if modernHistory.Code != http.StatusOK || modernUsages.Data.Total != 1 || len(modernUsages.Data.Items) != 1 {
				t.Fatalf("cookie history contract changed: status=%d body=%s", modernHistory.Code, modernHistory.Body)
			}
			if modernUsages.Data.Items[0].CodeID != codes[0].ID || modernUsages.Data.Items[0].ID != usage.ID || modernUsages.Data.Items[0].Code != "SAMECODE****" {
				t.Error("cookie history no longer identifies the same masked redemption")
			}
			other := createKnowledgeTestUser(t, database, "other-code-identity@example.test", "other-code-identity-password-123", 0, false)
			otherAuthorization := loginLegacyBearer(t, api, other.email, other.password).Authorization
			otherHistory := bearerRequest(api, http.MethodGet, "/api/v1/user/gift-card/history", otherAuthorization, "")
			var otherUsages struct {
				Data []store.GiftCardUsage `json:"data"`
			}
			decodeResponse(t, otherHistory, &otherUsages)
			if otherHistory.Code != http.StatusOK || len(otherUsages.Data) != 0 || strings.Contains(otherHistory.Body.String(), `"code_id"`) {
				t.Fatalf("other user saw code identity: status=%d body=%s", otherHistory.Code, otherHistory.Body)
			}
		})
	}
}

func TestGiftCardCodeIdentityRequiresAuthentication(t *testing.T) {
	api, _ := newTestAPI(t)
	for _, authorization := range []string{"", "Bearer invalid"} {
		for _, route := range []struct{ method, path, body string }{
			{http.MethodPost, "/api/v1/user/gift-card/check", `{"code":"SAMECODEINVALID"}`},
			{http.MethodPost, "/api/v1/user/gift-card/redeem", `{"code":"SAMECODEINVALID"}`},
			{http.MethodGet, "/api/v1/user/gift-card/history", ""},
		} {
			response := bearerRequest(api, route.method, route.path, authorization, route.body)
			if response.Code != http.StatusUnauthorized || strings.Contains(response.Body.String(), `"code_id"`) {
				t.Errorf("unauthenticated %s returned status=%d body=%s", route.path, response.Code, response.Body)
			}
		}
	}
}
