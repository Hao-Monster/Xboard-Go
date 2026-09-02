package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestLegacyTrustedPluginCatalogMatchesTheFixedCoreSurface(t *testing.T) {
	api, _ := newTestAPI(t)
	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization

	types := bearerRequest(api, http.MethodGet, "/api/v2/admin/plugin/types", authorization, "")
	if types.Code != http.StatusOK || types.Body.String() != "{\"data\":[{\"description\":\"提供功能扩展的插件，如Telegram登录、邮件通知等\",\"icon\":\"🔧\",\"label\":\"功能\",\"value\":\"feature\"},{\"description\":\"提供支付接口的插件，如支付宝、微信支付等\",\"icon\":\"💳\",\"label\":\"支付方式\",\"value\":\"payment\"}]}\n" {
		t.Fatalf("legacy plugin types status=%d body=%s", types.Code, types.Body)
	}
	if response := bearerRequest(api, http.MethodGet, "/api/v2/admin/plugin/types?unexpected=true", authorization, ""); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("legacy plugin types accepted query status=%d body=%s", response.Code, response.Body)
	}

	listed := bearerRequest(api, http.MethodGet, "/api/v2/admin/plugin/getPlugins", authorization, "")
	if listed.Code != http.StatusOK {
		t.Fatalf("legacy plugin list status=%d body=%s", listed.Code, listed.Body)
	}
	var payload struct {
		Data []legacyTrustedPluginResponse `json:"data"`
	}
	decodeResponse(t, listed, &payload)
	if len(payload.Data) != 7 {
		t.Fatalf("legacy plugin count=%d want=7 body=%s", len(payload.Data), listed.Body)
	}
	want := map[string]struct {
		name, pluginType, version, description string
	}{
		"telegram":      {"Telegram Bot 集成", "feature", "1.0.1", "Telegram Bot 消息处理和命令系统"},
		"alipay_f2f":    {"AlipayF2F", "payment", "1.0.0", "AlipayF2F payment plugin"},
		"btcpay":        {"BTCPay", "payment", "1.0.0", "BTCPay payment plugin"},
		"coin_payments": {"CoinPayments", "payment", "1.0.0", "CoinPayments payment plugin"},
		"coinbase":      {"Coinbase", "payment", "1.0.0", "Coinbase payment plugin"},
		"epay":          {"EPay", "payment", "1.0.0", "EPay payment plugin"},
		"mgate":         {"MGate", "payment", "1.0.0", "MGate payment plugin"},
	}
	wantOrder := []string{"alipay_f2f", "btcpay", "coinbase", "coin_payments", "epay", "mgate", "telegram"}
	wantTelegramFields := map[string]struct{ fieldType, label, description string }{
		"enable_ticket_notify":  {"boolean", "开启工单通知", "是否开启工单创建和回复的 Telegram 通知功能"},
		"enable_payment_notify": {"boolean", "开启支付通知", "是否开启支付成功的 Telegram 通知功能"},
		"start_welcome_title":   {"string", "欢迎标题", "/start 命令显示的欢迎标题"},
		"start_bot_description": {"text", "机器人描述", "/start 命令显示的机器人功能介绍"},
		"start_bind_guide":      {"text", "绑定指导", "未绑定用户显示的绑定指导文本"},
		"start_unbind_guide":    {"text", "已绑定用户命令列表", "已绑定用户显示的命令列表"},
		"start_bind_commands":   {"text", "未绑定用户命令列表", "未绑定用户显示的命令列表"},
		"start_footer":          {"text", "底部提示", "/start 命令底部的提示信息"},
		"help_text":             {"text", "帮助文本", "未知命令时显示的帮助文本"},
	}
	for index, plugin := range payload.Data {
		if plugin.Code != wantOrder[index] {
			t.Fatalf("legacy plugin order[%d]=%q want=%q", index, plugin.Code, wantOrder[index])
		}
		expected, ok := want[plugin.Code]
		if !ok {
			t.Fatalf("legacy list exposed unknown plugin %#v", plugin)
		}
		if plugin.Name != expected.name || plugin.Type != expected.pluginType || plugin.Version != expected.version ||
			plugin.Description != expected.description || plugin.Author != "XBoard Team" || !plugin.IsInstalled ||
			!plugin.IsEnabled || !plugin.IsProtected || plugin.CanBeDeleted || plugin.NeedUpgrade ||
			plugin.AdminMenus != nil || plugin.AdminCRUD != nil {
			t.Fatalf("legacy plugin metadata mismatch: %#v", plugin)
		}
		if plugin.Code == store.TrustedPluginTelegram {
			if !containsAll(plugin.Readme, "/bind", "/traffic", "/getlatesturl", "/unbind") {
				t.Fatalf("Telegram legacy readme=%q", plugin.Readme)
			}
			config, ok := plugin.Config.(map[string]any)
			if !ok || len(config) != 9 {
				t.Fatalf("Telegram config=%#v want nine fields", plugin.Config)
			}
			for key, raw := range config {
				expected, found := wantTelegramFields[key]
				field, ok := raw.(map[string]any)
				if !found || !ok || field["type"] != expected.fieldType || field["label"] != expected.label ||
					field["description"] != expected.description || field["value"] == nil {
					t.Fatalf("Telegram legacy config field %q=%#v", key, raw)
				}
				if placeholder, ok := field["placeholder"].(string); !ok || placeholder != "" {
					t.Fatalf("Telegram legacy config placeholder %q=%#v", key, field["placeholder"])
				}
				if options, ok := field["options"].([]any); !ok || len(options) != 0 {
					t.Fatalf("Telegram legacy config options %q=%#v", key, field["options"])
				}
			}
		}
		if plugin.Code != store.TrustedPluginTelegram {
			if plugin.Readme != "" {
				t.Fatalf("payment plugin unexpected readme: %#v", plugin)
			}
			config, ok := plugin.Config.([]any)
			if !ok || len(config) != 0 {
				t.Fatalf("payment plugin leaked config: %#v", plugin)
			}
		}
		delete(want, plugin.Code)
	}
	if len(want) != 0 {
		t.Fatalf("legacy list missing plugins: %#v", want)
	}

	payment := bearerRequest(api, http.MethodGet, "/api/v2/admin/plugin/getPlugins?type=payment", authorization, "")
	decodeResponse(t, payment, &payload)
	if payment.Code != http.StatusOK || len(payload.Data) != 6 {
		t.Fatalf("payment plugin filter status=%d plugins=%d body=%s", payment.Code, len(payload.Data), payment.Body)
	}
	for _, path := range []string{
		"/api/v2/admin/plugin/getPlugins?type=unknown",
		"/api/v2/admin/plugin/getPlugins?type=feature&type=payment",
		"/api/v2/admin/plugin/getPlugins?unexpected=true",
	} {
		response := bearerRequest(api, http.MethodGet, path, authorization, "")
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("ambiguous legacy plugin query %s status=%d body=%s", path, response.Code, response.Body)
		}
	}
}

func TestLegacyTrustedPluginEnableDisableAndTelegramConfigUseTheVersionedStore(t *testing.T) {
	api, database := newTestAPI(t)
	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization

	disabled := bearerRequest(api, http.MethodPost, "/api/v2/admin/plugin/disable", authorization, `{"code":"epay"}`)
	if disabled.Code != http.StatusOK || disabled.Body.String() != "{\"message\":\"插件禁用成功\"}\n" {
		t.Fatalf("legacy disable status=%d body=%s", disabled.Code, disabled.Body)
	}
	epay, err := database.GetTrustedPlugin(t.Context(), store.TrustedPluginEPay)
	if err != nil || epay.Enabled || epay.Revision != 2 {
		t.Fatalf("EPay after legacy disable=(%#v,%v)", epay, err)
	}
	providers := bearerRequest(api, http.MethodGet, "/api/v2/admin/payment/getPaymentMethods", authorization, "")
	if providers.Code != http.StatusOK || strings.Contains(providers.Body.String(), `"provider":"EPay"`) {
		t.Fatalf("legacy payment provider gate status=%d body=%s", providers.Code, providers.Body)
	}

	noOp := bearerRequest(api, http.MethodPost, "/api/v2/admin/plugin/disable", authorization, `{"code":"epay"}`)
	epay, err = database.GetTrustedPlugin(t.Context(), store.TrustedPluginEPay)
	if noOp.Code != http.StatusOK || err != nil || epay.Revision != 2 {
		t.Fatalf("idempotent legacy disable status=%d plugin=%#v err=%v body=%s", noOp.Code, epay, err, noOp.Body)
	}
	enabled := bearerRequest(api, http.MethodPost, "/api/v2/admin/plugin/enable", authorization, `{"code":"epay"}`)
	epay, err = database.GetTrustedPlugin(t.Context(), store.TrustedPluginEPay)
	if enabled.Code != http.StatusOK || err != nil || !epay.Enabled || epay.Revision != 3 {
		t.Fatalf("legacy enable status=%d plugin=%#v err=%v body=%s", enabled.Code, epay, err, enabled.Body)
	}
	foreignOriginRequest := newTestRequest(http.MethodPost, "/api/v2/admin/plugin/disable", strings.NewReader(`{"code":"epay"}`))
	foreignOriginRequest.Header.Set("Authorization", authorization)
	foreignOriginRequest.Header.Set("Content-Type", "application/json")
	foreignOriginRequest.Header.Set("Origin", "https://untrusted.example.test")
	foreignOriginResponse := httptest.NewRecorder()
	api.ServeHTTP(foreignOriginResponse, foreignOriginRequest)
	epay, err = database.GetTrustedPlugin(t.Context(), store.TrustedPluginEPay)
	if foreignOriginResponse.Code != http.StatusForbidden || err != nil || !epay.Enabled || epay.Revision != 3 {
		t.Fatalf("foreign-origin disable status=%d plugin=%#v err=%v body=%s", foreignOriginResponse.Code, epay, err, foreignOriginResponse.Body)
	}
	for _, body := range []string{`{"code":"shell"}`, `{"code":"epay","unknown":true}`, `{"code":"epay"}{"code":"epay"}`} {
		response := bearerRequest(api, http.MethodPost, "/api/v2/admin/plugin/enable", authorization, body)
		if response.Code < 400 || response.Code >= 500 {
			t.Fatalf("invalid enable body %q status=%d body=%s", body, response.Code, response.Body)
		}
	}
	unsupportedMedia := bearerRequest(api, http.MethodPost, "/api/v2/admin/plugin/enable", authorization, "")
	if unsupportedMedia.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("legacy enable without JSON content type status=%d body=%s", unsupportedMedia.Code, unsupportedMedia.Body)
	}
	oversized := bearerRequest(api, http.MethodPost, "/api/v2/admin/plugin/enable", authorization, strings.Repeat("x", trustedPluginRequestLimit+1))
	if oversized.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized legacy enable status=%d body=%s", oversized.Code, oversized.Body)
	}

	config := bearerRequest(api, http.MethodGet, "/api/v2/admin/plugin/config?code=telegram", authorization, "")
	if config.Code != http.StatusOK {
		t.Fatalf("legacy Telegram config status=%d body=%s", config.Code, config.Body)
	}
	var configPayload struct {
		Data map[string]legacyPluginConfigFieldPayload `json:"data"`
	}
	decodeResponse(t, config, &configPayload)
	wantConfigOrder := []string{"enable_ticket_notify", "enable_payment_notify", "start_welcome_title", "start_bot_description", "start_bind_guide", "start_unbind_guide", "start_bind_commands", "start_footer", "help_text"}
	previous := -1
	for _, key := range wantConfigOrder {
		index := strings.Index(config.Body.String(), `"`+key+`"`)
		if index <= previous {
			t.Fatalf("legacy Telegram config order=%s", config.Body)
		}
		previous = index
	}
	values := make(map[string]any, len(configPayload.Data))
	for key, field := range configPayload.Data {
		values[key] = field.Value
	}
	values["help_text"] = "兼容配置帮助"
	encoded, err := json.Marshal(map[string]any{"code": "telegram", "config": values})
	if err != nil {
		t.Fatal(err)
	}
	updated := bearerRequest(api, http.MethodPost, "/api/v2/admin/plugin/config", authorization, string(encoded))
	if updated.Code != http.StatusOK || updated.Body.String() != "{\"message\":\"配置更新成功\"}\n" {
		t.Fatalf("legacy Telegram config update status=%d body=%s", updated.Code, updated.Body)
	}
	telegram, err := database.GetTrustedPlugin(t.Context(), store.TrustedPluginTelegram)
	if err != nil || telegram.Config["help_text"] != "兼容配置帮助" || telegram.Revision != 2 {
		t.Fatalf("Telegram after legacy config update=(%#v,%v)", telegram, err)
	}
	invalidUTF8 := newTestRequest(http.MethodPost, "/api/v2/admin/plugin/config", bytes.NewReader([]byte{'{', '"', 'c', 'o', 'd', 'e', '"', ':', '"', 't', 'e', 'l', 'e', 'g', 'r', 'a', 'm', '"', ',', '"', 'c', 'o', 'n', 'f', 'i', 'g', '"', ':', '{', '"', 'h', 'e', 'l', 'p', '_', 't', 'e', 'x', 't', '"', ':', '"', 0xff, '"', '}', '}'}))
	invalidUTF8.Header.Set("Authorization", authorization)
	invalidUTF8.Header.Set("Content-Type", "application/json")
	invalidUTF8Response := httptest.NewRecorder()
	api.ServeHTTP(invalidUTF8Response, invalidUTF8)
	if invalidUTF8Response.Code != http.StatusBadRequest || !strings.Contains(invalidUTF8Response.Body.String(), "有效 UTF-8") {
		t.Fatalf("invalid UTF-8 config status=%d body=%s", invalidUTF8Response.Code, invalidUTF8Response.Body)
	}
	for _, request := range []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/v2/admin/plugin/config", ""},
		{http.MethodGet, "/api/v2/admin/plugin/config?code=epay", ""},
		{http.MethodGet, "/api/v2/admin/plugin/config?code=telegram&code=epay", ""},
		{http.MethodPost, "/api/v2/admin/plugin/config", `{"code":"epay","config":{}}`},
		{http.MethodPost, "/api/v2/admin/plugin/config", `{"code":"telegram","config":{},"unknown":true}`},
	} {
		response := bearerRequest(api, request.method, request.path, authorization, request.body)
		if response.Code < 400 || response.Code >= 500 {
			t.Fatalf("rejected legacy config %s %s status=%d body=%s", request.method, request.path, response.Code, response.Body)
		}
	}
	audits, err := database.ListAdminAuditLogs(t.Context(), store.AdminAuditFilter{Page: 1, PageSize: 100, Query: "/plugin/"})
	if err != nil {
		t.Fatal(err)
	}
	wantAuditRoutes := map[string]bool{
		"/api/v2/{secure_admin}/plugin/enable":  false,
		"/api/v2/{secure_admin}/plugin/disable": false,
		"/api/v2/{secure_admin}/plugin/config":  false,
	}
	for _, audit := range audits.Items {
		if _, expected := wantAuditRoutes[audit.Route]; !expected || strings.Contains(audit.Route, "/api/v2/admin/") {
			t.Fatalf("unexpected or unsafe legacy plugin audit: %#v", audit)
		}
		wantAuditRoutes[audit.Route] = true
	}
	for route, found := range wantAuditRoutes {
		if !found {
			t.Fatalf("missing legacy plugin audit route %s in %#v", route, audits.Items)
		}
	}
	telegram, err = database.GetTrustedPlugin(t.Context(), store.TrustedPluginTelegram)
	if err != nil || telegram.Revision != 2 || telegram.Config["help_text"] != "兼容配置帮助" {
		t.Fatalf("rejected legacy configs mutated Telegram=(%#v,%v)", telegram, err)
	}
}

func TestLegacyRuntimePluginMutationRoutesFailClosedWithoutReadingTheBody(t *testing.T) {
	api, database := newTestAPI(t)
	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	before, err := database.ListTrustedPlugins(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"upload", "delete", "install", "uninstall", "upgrade"} {
		var response *httptest.ResponseRecorder
		if action == "upload" {
			request := newTestRequest(http.MethodPost, "/api/v2/admin/plugin/upload", nil)
			request.Body = failIfReadBody{}
			request.Header.Set("Authorization", authorization)
			request.Header.Set("Content-Type", "multipart/form-data; boundary=untrusted")
			response = httptest.NewRecorder()
			api.ServeHTTP(response, request)
		} else {
			response = bearerRequest(api, http.MethodPost, "/api/v2/admin/plugin/"+action, authorization, strings.Repeat("x", int(maxJSONBody)+1))
		}
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "不支持运行时") {
			t.Fatalf("unsafe plugin action %s status=%d body=%s", action, response.Code, response.Body)
		}
	}
	after, err := database.ListTrustedPlugins(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("unsafe routes changed trusted plugins: before=%#v after=%#v", before, after)
	}

	audits, err := database.ListAdminAuditLogs(t.Context(), store.AdminAuditFilter{Page: 1, PageSize: 20, Query: "/plugin/"})
	if err != nil || audits.Total != 5 {
		t.Fatalf("unsafe plugin audit total=%d items=%#v err=%v", audits.Total, audits.Items, err)
	}
	for _, audit := range audits.Items {
		if strings.Contains(audit.Route, "/api/v2/admin/") || audit.StatusCode != http.StatusConflict {
			t.Fatalf("unsafe plugin audit leaked dynamic path or status: %#v", audit)
		}
	}
}

type failIfReadBody struct{}

func (failIfReadBody) Read([]byte) (int, error) {
	panic("unsafe runtime plugin route read the request body")
}

func (failIfReadBody) Close() error { return nil }

type legacyPluginConfigFieldPayload struct {
	Type        string   `json:"type"`
	Label       string   `json:"label"`
	Placeholder string   `json:"placeholder"`
	Description string   `json:"description"`
	Value       any      `json:"value"`
	Options     []string `json:"options"`
}
