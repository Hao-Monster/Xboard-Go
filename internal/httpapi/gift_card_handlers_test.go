package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestGiftCardModernAPIsCreateGenerateCheckRedeemAndHistory(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	user := createKnowledgeTestUser(t, database, "gift-api-user@example.test", "gift-api-user-password-123", 0, false)
	client := loginAs(t, api, user.email, user.password)
	created := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/gift-card/templates", `{
		"name":"API 礼品卡","type":1,"status":true,
		"rewards":{"balance":1234},
		"limits":{"max_use_per_user":1,"invite_reward_basis_points":0},"sort":3
	}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create template status=%d body=%s", created.Code, created.Body)
	}
	var templatePayload struct {
		Data store.GiftCardTemplate `json:"data"`
	}
	decodeResponse(t, created, &templatePayload)
	updatedTemplate := admin.request(t, api, http.MethodPut, fmt.Sprintf("/api/v1/admin/admin/gift-card/templates/%d", templatePayload.Data.ID), `{"name":"API 礼品卡（已更新）","sort":2}`)
	if updatedTemplate.Code != http.StatusOK || !containsAll(updatedTemplate.Body.String(), `"name":"API 礼品卡（已更新）"`, `"revision":2`) {
		t.Fatalf("update template status=%d body=%s", updatedTemplate.Code, updatedTemplate.Body)
	}
	templates := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/gift-card/templates?page=1&page_size=20&type=1&status=true", "")
	if templates.Code != http.StatusOK || !containsAll(templates.Body.String(), `"total":1`, `"name":"API 礼品卡（已更新）"`) {
		t.Fatalf("list templates status=%d body=%s", templates.Code, templates.Body)
	}
	generated := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/gift-card/codes/generate",
		fmt.Sprintf(`{"template_id":%d,"count":2,"prefix":"API","max_usage":1}`, templatePayload.Data.ID))
	if generated.Code != http.StatusCreated {
		t.Fatalf("generate code status=%d body=%s", generated.Code, generated.Body)
	}
	var codePayload struct {
		Data []store.GiftCardCode `json:"data"`
	}
	decodeResponse(t, generated, &codePayload)
	if len(codePayload.Data) != 2 {
		t.Fatalf("generated payload=%s", generated.Body)
	}
	toggled := admin.request(t, api, http.MethodPost, fmt.Sprintf("/api/v1/admin/admin/gift-card/codes/%d/toggle", codePayload.Data[1].ID), "")
	if toggled.Code != http.StatusOK || !containsAll(toggled.Body.String(), `"status":2`) {
		t.Fatalf("disable generated code status=%d body=%s", toggled.Code, toggled.Body)
	}
	deletedUnusedCode := admin.request(t, api, http.MethodDelete, fmt.Sprintf("/api/v1/admin/admin/gift-card/codes/%d", codePayload.Data[1].ID), "")
	if deletedUnusedCode.Code != http.StatusNoContent {
		t.Fatalf("delete unused code status=%d body=%s", deletedUnusedCode.Code, deletedUnusedCode.Body)
	}
	updated := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/admin/gift-card/codes/%d", codePayload.Data[0].ID), `{"expires_at":1800000000}`)
	if updated.Code != http.StatusOK || !containsAll(updated.Body.String(), `"expires_at":"2027-01-15T08:00:00Z"`) {
		t.Fatalf("update expiry status=%d body=%s", updated.Code, updated.Body)
	}
	cleared := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/admin/gift-card/codes/%d", codePayload.Data[0].ID), `{"expires_at":null}`)
	if cleared.Code != http.StatusOK || !containsAll(cleared.Body.String(), `"expires_at":null`) {
		t.Fatalf("clear expiry status=%d body=%s", cleared.Code, cleared.Body)
	}
	checked := client.request(t, api, http.MethodPost, "/api/v1/user/gift-card/check", fmt.Sprintf(`{"code":%q}`, codePayload.Data[0].Code))
	if checked.Code != http.StatusOK || !containsAll(checked.Body.String(), `"balance":1234`, `"can_redeem":true`) {
		t.Fatalf("check status=%d body=%s", checked.Code, checked.Body)
	}
	redeemed := client.request(t, api, http.MethodPost, "/api/v1/user/gift-card/redeem", fmt.Sprintf(`{"code":%q}`, codePayload.Data[0].Code))
	if redeemed.Code != http.StatusOK || !containsAll(redeemed.Body.String(), `"message":"兑换成功！"`, `"balance":1234`) {
		t.Fatalf("redeem status=%d body=%s", redeemed.Code, redeemed.Body)
	}
	var redeemPayload struct {
		Data struct {
			Usage store.GiftCardUsage `json:"usage"`
		} `json:"data"`
	}
	decodeResponse(t, redeemed, &redeemPayload)
	history := client.request(t, api, http.MethodGet, "/api/v1/user/gift-card/history?page=1&page_size=15", "")
	if history.Code != http.StatusOK || !containsAll(history.Body.String(), `"total":1`, `"template_name":"API 礼品卡（已更新）"`, codePayload.Data[0].Code[:8]+`****`) || strings.Contains(history.Body.String(), codePayload.Data[0].Code+`"`) {
		t.Fatalf("history status=%d body=%s", history.Code, history.Body)
	}
	detail := client.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/user/gift-card/history/%d", redeemPayload.Data.Usage.ID), "")
	if detail.Code != http.StatusOK || !containsAll(detail.Body.String(), `"code":"`+codePayload.Data[0].Code+`"`, `"balance":1234`) {
		t.Fatalf("history detail status=%d body=%s", detail.Code, detail.Body)
	}
	usageList := admin.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/admin/admin/gift-card/usages?page=1&page_size=20&user_id=%d", user.id), "")
	if usageList.Code != http.StatusOK || !containsAll(usageList.Body.String(), `"total":1`, `"user_email":"gift-api-user@example.test"`) {
		t.Fatalf("usage list status=%d body=%s", usageList.Code, usageList.Body)
	}
	statistics := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/gift-card/statistics", "")
	if statistics.Code != http.StatusOK || !containsAll(statistics.Body.String(), `"template_total":1`, `"code_total":1`, `"used_codes":1`, `"usage_total":1`) {
		t.Fatalf("statistics status=%d body=%s", statistics.Code, statistics.Body)
	}
	listed := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/gift-card/codes?page=1&page_size=20", "")
	if listed.Code != http.StatusOK || !containsAll(listed.Body.String(), `"usage_count":1`, `"status":1`) {
		t.Fatalf("admin code list status=%d body=%s", listed.Code, listed.Body)
	}
	deleteUsedCode := admin.request(t, api, http.MethodDelete, fmt.Sprintf("/api/v1/admin/admin/gift-card/codes/%d", codePayload.Data[0].ID), "")
	if deleteUsedCode.Code != http.StatusConflict {
		t.Fatalf("delete used code status=%d body=%s", deleteUsedCode.Code, deleteUsedCode.Body)
	}
	deleteReferencedTemplate := admin.request(t, api, http.MethodDelete, fmt.Sprintf("/api/v1/admin/admin/gift-card/templates/%d", templatePayload.Data.ID), "")
	if deleteReferencedTemplate.Code != http.StatusConflict {
		t.Fatalf("delete referenced template status=%d body=%s", deleteReferencedTemplate.Code, deleteReferencedTemplate.Body)
	}
	unusedTemplate := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/gift-card/templates", `{"name":"Unused API gift","type":1,"status":true,"rewards":{"balance":1}}`)
	if unusedTemplate.Code != http.StatusCreated {
		t.Fatalf("create unused template status=%d body=%s", unusedTemplate.Code, unusedTemplate.Body)
	}
	var unusedPayload struct {
		Data store.GiftCardTemplate `json:"data"`
	}
	decodeResponse(t, unusedTemplate, &unusedPayload)
	deletedTemplate := admin.request(t, api, http.MethodDelete, fmt.Sprintf("/api/v1/admin/admin/gift-card/templates/%d", unusedPayload.Data.ID), "")
	if deletedTemplate.Code != http.StatusNoContent {
		t.Fatalf("delete unused template status=%d body=%s", deletedTemplate.Code, deletedTemplate.Body)
	}
}

func TestGiftCardLegacyContractsAndAdminPermission(t *testing.T) {
	api, database := newTestAPI(t)
	user := createKnowledgeTestUser(t, database, "gift-legacy-user@example.test", "gift-legacy-user-password-123", 0, false)
	userClient := loginAs(t, api, user.email, user.password)
	forbidden := userClient.request(t, api, http.MethodPost, "/api/v1/admin/admin/gift-card/templates", `{"name":"no","type":1,"rewards":{"balance":1}}`)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("non-admin create status=%d body=%s", forbidden.Code, forbidden.Body)
	}
	adminAuthorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	created := bearerRequest(api, http.MethodPost, "/api/v2/admin/gift-card/create-template", adminAuthorization,
		`{"name":"Legacy card","type":1,"status":true,"rewards":{"balance":500,"random_rewards":[]},"limits":{"max_use_per_user":1,"invite_reward_rate":0.25},"special_config":{"festival_bonus":1.5}}`)
	if created.Code != http.StatusOK || !containsAll(created.Body.String(), `"status":"success"`, `"name":"Legacy card"`) {
		t.Fatalf("legacy create status=%d body=%s", created.Code, created.Body)
	}
	var legacyTemplate struct {
		Data struct {
			ID            int64 `json:"id"`
			SpecialConfig struct {
				FestivalBonus float64 `json:"festival_bonus"`
			} `json:"special_config"`
		} `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &legacyTemplate); err != nil {
		t.Fatal(err)
	}
	if legacyTemplate.Data.SpecialConfig.FestivalBonus != 1.5 {
		t.Fatalf("legacy festival bonus = %v, want 1.5", legacyTemplate.Data.SpecialConfig.FestivalBonus)
	}
	generated := bearerRequest(api, http.MethodPost, "/api/v2/admin/gift-card/generate-codes", adminAuthorization,
		fmt.Sprintf(`{"template_id":%d,"count":2,"prefix":"LG","max_usage":1}`, legacyTemplate.Data.ID))
	if generated.Code != http.StatusOK || !containsAll(generated.Body.String(), `"count":2`, `"message":"生成成功"`) {
		t.Fatalf("legacy generate status=%d body=%s", generated.Code, generated.Body)
	}
	legacyCodes := bearerRequest(api, http.MethodGet, "/api/v2/admin/gift-card/codes?page=1&per_page=15", adminAuthorization, "")
	var codePage struct {
		Data []struct {
			ID      int64  `json:"id"`
			Code    string `json:"code"`
			BatchID string `json:"batch_id"`
			Status  int    `json:"status"`
		} `json:"data"`
		Total  int64  `json:"total"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(legacyCodes.Body.Bytes(), &codePage); err != nil {
		t.Fatal(err)
	}
	if legacyCodes.Code != http.StatusOK || codePage.Total != 2 || len(codePage.Data) != 2 || codePage.Status != "" || codePage.Data[0].BatchID == "" {
		t.Fatalf("legacy code page=%s", legacyCodes.Body)
	}
	disabled := bearerRequest(api, http.MethodPost, "/api/v2/admin/gift-card/update-code", adminAuthorization, fmt.Sprintf(`{"id":%d,"status":3}`, codePage.Data[1].ID))
	if disabled.Code != http.StatusOK || !containsAll(disabled.Body.String(), `"status":3`, `"status_name":"已禁用"`) {
		t.Fatalf("legacy disable status=%d body=%s", disabled.Code, disabled.Body)
	}
	filtered := bearerRequest(api, http.MethodGet, "/api/v2/admin/gift-card/codes?page=1&per_page=15&status=3", adminAuthorization, "")
	if filtered.Code != http.StatusOK || !containsAll(filtered.Body.String(), `"total":1`, fmt.Sprintf(`"id":%d`, codePage.Data[1].ID)) {
		t.Fatalf("legacy status filter=%s", filtered.Body)
	}
	enabled := bearerRequest(api, http.MethodPost, "/api/v2/admin/gift-card/toggle-code", adminAuthorization, fmt.Sprintf(`{"id":%d,"action":"enable"}`, codePage.Data[1].ID))
	if enabled.Code != http.StatusOK || !containsAll(enabled.Body.String(), `"message":"已启用"`) {
		t.Fatalf("legacy enable=%s", enabled.Body)
	}
	exported := bearerRequest(api, http.MethodGet, "/api/v2/admin/gift-card/export-codes?batch_id="+codePage.Data[0].BatchID, adminAuthorization, "")
	if exported.Code != http.StatusOK || exported.Header().Get("Content-Type") != "text/plain; charset=utf-8" || !strings.Contains(exported.Body.String(), codePage.Data[0].Code) {
		t.Fatalf("legacy export=%d %q", exported.Code, exported.Body.String())
	}
	types := bearerRequest(api, http.MethodGet, "/api/v2/admin/gift-card/types", adminAuthorization, "")
	if types.Code != http.StatusOK || !containsAll(types.Body.String(), `"1":"通用礼品卡"`, `"3":"盲盒礼品卡"`) {
		t.Fatalf("legacy admin types status=%d body=%s", types.Code, types.Body)
	}
	userAuthorization := loginLegacyBearer(t, api, user.email, user.password).Authorization
	userTypes := bearerRequest(api, http.MethodGet, "/api/v1/user/gift-card/types", userAuthorization, "")
	if userTypes.Code != http.StatusOK || !containsAll(userTypes.Body.String(), `"types"`, `"2":"套餐礼品卡"`) {
		t.Fatalf("legacy user types status=%d body=%s", userTypes.Code, userTypes.Body)
	}
	legacyList := bearerRequest(api, http.MethodGet, "/api/v2/admin/gift-card/templates?page=1&per_page=15", adminAuthorization, "")
	if legacyList.Code != http.StatusOK || !containsAll(legacyList.Body.String(), `"current_page":1`, `"per_page":15`, `"name":"Legacy card"`, `"theme_color"`) || strings.Contains(legacyList.Body.String(), `"status":"success"`) {
		t.Fatalf("legacy list status=%d body=%s", legacyList.Code, legacyList.Body)
	}
	checked := bearerRequest(api, http.MethodPost, "/api/v1/user/gift-card/check", userAuthorization, fmt.Sprintf(`{"code":%q}`, codePage.Data[0].Code))
	if checked.Code != http.StatusOK || !containsAll(checked.Body.String(), `"can_redeem":true`, `"template":{"background_image"`, `"type_name":"通用礼品卡"`) {
		t.Fatalf("legacy check=%s", checked.Body)
	}
	redeemed := bearerRequest(api, http.MethodPost, "/api/v1/user/gift-card/redeem", userAuthorization, fmt.Sprintf(`{"code":%q}`, codePage.Data[0].Code))
	if redeemed.Code != http.StatusOK || !containsAll(redeemed.Body.String(), `"message":"兑换成功！"`, `"balance":500`) {
		t.Fatalf("legacy redeem=%s", redeemed.Body)
	}
	history := bearerRequest(api, http.MethodGet, "/api/v1/user/gift-card/history?page=1&per_page=15", userAuthorization, "")
	if history.Code != http.StatusOK || !containsAll(history.Body.String(), `"pagination":{"current_page":1`, codePage.Data[0].Code[:8]+`****`, `"rewards_given":{"balance":500}`) || strings.Contains(history.Body.String(), codePage.Data[0].Code+`"`) {
		t.Fatalf("legacy history=%s", history.Body)
	}
}

func TestGiftCardCSVGenerationAndExportIncludeSafeTemplateName(t *testing.T) {
	api, _ := newTestAPI(t)
	admin := loginAdmin(t, api)
	created := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/gift-card/templates", `{
		"name":"=CSV formula","type":1,"status":true,"rewards":{"balance":100}
	}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create template status=%d body=%s", created.Code, created.Body)
	}
	var templatePayload struct {
		Data store.GiftCardTemplate `json:"data"`
	}
	decodeResponse(t, created, &templatePayload)

	downloaded := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/gift-card/codes/generate",
		fmt.Sprintf(`{"template_id":%d,"count":1,"prefix":"CSV","max_usage":1,"download_csv":true}`, templatePayload.Data.ID))
	if downloaded.Code != http.StatusOK || downloaded.Header().Get("Content-Type") != "text/csv; charset=utf-8" ||
		!containsAll(downloaded.Body.String(), "兑换码,有效期,最大使用次数", "CSV", "'=CSV formula") {
		t.Fatalf("generated CSV status=%d body=%q", downloaded.Code, downloaded.Body.String())
	}

	generated := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/gift-card/codes/generate",
		fmt.Sprintf(`{"template_id":%d,"count":1,"prefix":"LIST","max_usage":1}`, templatePayload.Data.ID))
	if generated.Code != http.StatusCreated {
		t.Fatalf("generate list-export code status=%d body=%s", generated.Code, generated.Body)
	}
	var codePayload struct {
		Data []store.GiftCardCode `json:"data"`
	}
	decodeResponse(t, generated, &codePayload)
	if len(codePayload.Data) != 1 || codePayload.Data[0].TemplateName != "=CSV formula" {
		t.Fatalf("generated code payload=%s", generated.Body)
	}
	exported := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/gift-card/codes/export?batch_no="+codePayload.Data[0].BatchNo, "")
	if exported.Code != http.StatusOK || exported.Header().Get("Content-Type") != "text/csv; charset=utf-8" ||
		!containsAll(exported.Body.String(), codePayload.Data[0].Code, "'=CSV formula") {
		t.Fatalf("exported CSV status=%d body=%q", exported.Code, exported.Body.String())
	}
}
