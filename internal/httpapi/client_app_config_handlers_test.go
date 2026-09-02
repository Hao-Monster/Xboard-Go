package httpapi

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	yaml "go.yaml.in/yaml/v3"
)

func TestLegacyClientAppConfigTokenAndAccountStateContracts(t *testing.T) {
	api, database := newTestAPI(t)
	available := createSubscriptionTestAccount(t, database, "app-config-available@example.test", false, timePointerHTTP(fixedNow().Add(time.Hour)))
	banned := createSubscriptionTestAccount(t, database, "app-config-banned@example.test", true, timePointerHTTP(fixedNow().Add(time.Hour)))

	for _, version := range []string{"v1", "v2"} {
		missing := requestSubscription(api, "/api/"+version+"/client/app/getConfig")
		if missing.Code != http.StatusForbidden || missing.Body.String() != `{"status":"fail","message":"token is null","data":null,"error":null}` {
			t.Fatalf("%s missing token status=%d body=%s", version, missing.Code, missing.Body)
		}
		unknown := requestSubscription(api, "/api/"+version+"/client/app/getConfig?token="+strings.Repeat("a", 32))
		if unknown.Code != http.StatusForbidden || unknown.Body.String() != `{"status":"fail","message":"token is error","data":null,"error":null}` {
			t.Fatalf("%s unknown token status=%d body=%s", version, unknown.Code, unknown.Body)
		}
	}

	v1Banned := requestSubscription(api, "/api/v1/client/app/getConfig?token="+banned.SubscriptionToken)
	if v1Banned.Code != http.StatusOK || v1Banned.Header().Get("Content-Type") != "text/html; charset=utf-8" || v1Banned.Header().Get("Cache-Control") != "no-store, private" {
		t.Fatalf("V1 banned status=%d headers=%v body=%s", v1Banned.Code, v1Banned.Header(), v1Banned.Body)
	}
	var v1Config map[string]any
	if err := yaml.Unmarshal(v1Banned.Body.Bytes(), &v1Config); err != nil {
		t.Fatal(err)
	}
	if len(v1Config["proxies"].([]any)) != 0 || len(v1Config["proxy-groups"].([]any)) != 3 || len(v1Config["rules"].([]any)) != 513 {
		t.Fatalf("V1 banned structure=%#v", v1Config)
	}
	for _, forbiddenHeader := range []string{"Subscription-Userinfo", "Content-Disposition", "Profile-Update-Interval", "Profile-Web-Page-Url"} {
		if v1Banned.Header().Get(forbiddenHeader) != "" {
			t.Fatalf("V1 app config unexpectedly set %s", forbiddenHeader)
		}
	}
	v2Banned := requestSubscription(api, "/api/v2/client/app/getConfig?token="+banned.SubscriptionToken)
	if v2Banned.Code != http.StatusOK || !strings.Contains(v2Banned.Body.String(), `"config_hash"`) {
		t.Fatalf("V2 banned status=%d body=%s", v2Banned.Code, v2Banned.Body)
	}
	v2Available := requestSubscription(api, "/api/v2/client/app/getConfig?token="+available.SubscriptionToken)
	if v2Available.Code != http.StatusOK {
		t.Fatalf("V2 available status=%d body=%s", v2Available.Code, v2Available.Body)
	}
}

func TestLegacyClientAppConfigInvalidTokenLimiterIsSharedAcrossVersions(t *testing.T) {
	_, database := newTestAPI(t)
	valid := createSubscriptionTestAccount(t, database, "app-config-limiter@example.test", false, timePointerHTTP(fixedNow().Add(time.Hour)))
	api := &server{
		store: database, now: fixedNow, logger: slog.Default(),
		subscriptionFailures: newAttemptLimiter(2, time.Minute),
	}
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.RemoteAddr = "192.0.2.80:23456"
		w := httptest.NewRecorder()
		if strings.HasPrefix(path, "/api/v1/") {
			api.legacyClientAppConfigV1(w, r)
		} else {
			api.legacyClientAppConfigV2(w, r)
		}
		return w
	}
	if got := request("/api/v1/client/app/getConfig?token=" + strings.Repeat("a", 32)); got.Code != http.StatusForbidden {
		t.Fatalf("first bad token status=%d body=%s", got.Code, got.Body)
	}
	if got := request("/api/v2/client/app/getConfig?token=" + valid.SubscriptionToken); got.Code != http.StatusOK {
		t.Fatalf("valid token status=%d body=%s", got.Code, got.Body)
	}
	if got := request("/api/v2/client/app/getConfig?token=" + strings.Repeat("b", 32)); got.Code != http.StatusForbidden {
		t.Fatalf("second bad token status=%d body=%s", got.Code, got.Body)
	}
	limited := request("/api/v1/client/app/getConfig?token=" + strings.Repeat("c", 32))
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") != "900" || !strings.Contains(limited.Body.String(), `"code":"subscription_rate_limited"`) {
		t.Fatalf("limited status=%d headers=%v body=%s", limited.Code, limited.Header(), limited.Body)
	}
}

func TestLegacyClientAppConfigV1MatchesRepresentativeNodeOracle(t *testing.T) {
	api, database := newTestAPI(t)
	group, err := database.CreateServerGroup(t.Context(), "App config users", fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateAdminUser(t.Context(), store.CreateAdminUserInput{
		Email: "app-config-nodes@example.test", PasswordHash: "opaque", GroupID: &group.ID,
		TransferEnable: 10 << 30, ExpiredAt: timePointerHTTP(fixedNow().Add(time.Hour)),
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	account, err := database.GetSubscriptionAccount(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	definitions := []struct {
		typeName string
		name     string
		host     string
		port     string
		settings string
	}{
		{"shadowsocks", "SS Oracle", "ss.example.test", "1443", `{"cipher":"aes-256-gcm","plugin":"v2ray-plugin","plugin_opts":"mode=websocket;tls=true;host=ss.example.test;path=/ss"}`},
		{"shadowsocks", "SS Unsupported", "bad-ss.example.test", "1444", `{"cipher":"2022-blake3-aes-128-gcm","plugin":"","plugin_opts":""}`},
		{"vmess", "VMess Oracle", "vmess.example.test", "2443", `{"tls":1,"network":"ws","network_settings":{"path":"/vm","headers":{"Host":"edge.example.test"}},"tls_settings":{"allow_insecure":true,"server_name":"sni.example.test","ech":{"enabled":false,"config":"","query_server_name":"","key":""}},"utls":{"enabled":false,"fingerprint":"chrome"},"multiplex":{"enabled":false,"protocol":"smux","max_connections":4,"padding":false,"brutal":{"enabled":false,"up_mbps":100,"down_mbps":100}}}`},
		{"trojan", "Trojan Oracle", "trojan.example.test", "3443", `{"tls":1,"network":"grpc","network_settings":{"serviceName":"oracle-grpc"},"tls_settings":{"allow_insecure":false,"server_name":"trojan-sni.example.test","ech":{"enabled":false,"config":"","query_server_name":"","key":""}},"reality_settings":{"server_name":"","server_port":443,"public_key":"","private_key":"","short_id":"","allow_insecure":false},"utls":{"enabled":false,"fingerprint":"chrome"},"multiplex":{"enabled":false,"protocol":"smux","max_connections":4,"padding":false,"brutal":{"enabled":false,"up_mbps":100,"down_mbps":100}}}`},
		{"vless", "VLESS Ignored", "vless.example.test", "4443", `{"tls":0,"network":"tcp","network_settings":{},"flow":"","encryption":{"enabled":false,"encryption":"","decryption":""},"tls_settings":{"server_name":"","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}},"reality_settings":{"server_name":"","server_port":443,"public_key":"","private_key":"","short_id":"","allow_insecure":false},"utls":{"enabled":false,"fingerprint":"chrome"},"multiplex":{"enabled":false,"protocol":"smux","max_connections":4,"padding":false,"brutal":{"enabled":false,"up_mbps":100,"down_mbps":100}}}`},
	}
	for index, definition := range definitions {
		input, err := store.NewBasicAdminNodeDefinitionInput(store.CreateNodeInput{
			Type: definition.typeName, Name: definition.name, Host: definition.host, Port: definition.port,
			Show: true, Enabled: true, Sort: (index + 1) * 10,
		})
		if err != nil {
			t.Fatal(err)
		}
		input.GroupIDs = []int64{group.ID}
		input.ProtocolSettings = json.RawMessage(definition.settings)
		if _, _, err := database.CreateAdminNodeDefinition(t.Context(), input, fixedNow()); err != nil {
			t.Fatalf("create %s: %v", definition.name, err)
		}
	}

	response := requestSubscription(api, "/api/v1/client/app/getConfig?token="+account.SubscriptionToken)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var config map[string]any
	if err := yaml.Unmarshal(response.Body.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	proxies := config["proxies"].([]any)
	if len(proxies) != 3 {
		t.Fatalf("proxies=%#v", proxies)
	}
	want := []string{"SS Oracle", "VMess Oracle", "Trojan Oracle"}
	for index, raw := range proxies {
		if raw.(map[string]any)["name"] != want[index] {
			t.Fatalf("proxy order=%#v", proxies)
		}
	}
	for _, raw := range config["proxy-groups"].([]any) {
		entries := raw.(map[string]any)["proxies"].([]any)
		for _, name := range want {
			if !containsAnyStringHTTP(entries, name) {
				t.Fatalf("group entries=%#v missing=%s", entries, name)
			}
		}
	}
}

func TestLegacyClientAppConfigV2ShapeSettingsAndPHPHash(t *testing.T) {
	api, database := newTestAPI(t)
	account := createSubscriptionTestAccount(t, database, "app-config-v2@example.test", false, timePointerHTTP(fixedNow().Add(time.Hour)))
	initialResponse := requestSubscription(api, "/api/v2/client/app/getConfig?token="+account.SubscriptionToken)
	var initialEnvelope struct {
		Data legacyV2AppConfig `json:"data"`
	}
	if initialResponse.Code != http.StatusOK || json.Unmarshal(initialResponse.Body.Bytes(), &initialEnvelope) != nil || initialEnvelope.Data.ConfigHash == "" {
		t.Fatalf("initial V2 response status=%d body=%s", initialResponse.Code, initialResponse.Body)
	}
	administrator := loginAdmin(t, api)
	updated := administrator.request(t, api, http.MethodPut, "/api/v1/admin/site-settings", `{
		"revision":1,"app_name":"Oracle Board","app_description":"Oracle 描述","app_url":"https://app.oracle.test",
		"tos_url":"https://app.oracle.test/tos","logo":"https://app.oracle.test/logo.png",
		"currency":"usd","currency_symbol":"$","email_whitelist_suffix":["oracle.test"]
	}`)
	if updated.Code != http.StatusOK {
		t.Fatalf("update site settings status=%d body=%s", updated.Code, updated.Body)
	}
	adminRecord, err := database.FindUserByEmail(t.Context(), "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	commissionSettings, err := database.GetCommissionSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	withdrawLimit := store.CurrencyAmount(25050)
	withdrawMethods := []string{"USDT", "Bank"}
	if _, err := database.UpdateCommissionSettings(t.Context(), adminRecord.ID, commissionSettings.Revision, store.SaveCommissionSettingsInput{
		InviteCommission: commissionSettings.InviteCommission, FirstTimeEnabled: commissionSettings.FirstTimeEnabled,
		AutoCheckEnabled: commissionSettings.AutoCheckEnabled, WithdrawLimit: &withdrawLimit, WithdrawMethods: &withdrawMethods,
		WithdrawClosed: commissionSettings.WithdrawClosed, DistributionEnabled: commissionSettings.DistributionEnabled,
		DistributionL1: commissionSettings.DistributionL1, DistributionL2: commissionSettings.DistributionL2, DistributionL3: commissionSettings.DistributionL3,
	}, fixedNow()); err != nil {
		t.Fatal(err)
	}
	response := requestSubscription(api, "/api/v2/client/app/getConfig?token="+account.SubscriptionToken)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Cache-Control") != "no-store, private" {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body)
	}
	var envelope struct {
		Data legacyV2AppConfig `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	rawBody := response.Body.String()
	orderedKeys := []string{`"app_info"`, `"features"`, `"ui_config"`, `"business_rules"`, `"server_config"`, `"security_config"`, `"payment_config"`, `"notification_config"`, `"cache_config"`, `"last_updated"`, `"config_hash"`}
	previous := -1
	for _, key := range orderedKeys {
		position := strings.Index(rawBody, key)
		if position <= previous {
			t.Fatalf("V2 top-level key %s is missing or out of order in %s", key, rawBody)
		}
		previous = position
	}
	for _, secret := range []string{"telegram_bot_token", "smtp_password", "settings_encryption_key", "subscription_token"} {
		if strings.Contains(rawBody, secret) {
			t.Fatalf("V2 response leaked %q: %s", secret, rawBody)
		}
	}
	config := envelope.Data
	if config.AppInfo.AppName != "Oracle Board" || config.AppInfo.AppDescription != "Oracle 描述" || config.AppInfo.Version != "1.0.0" ||
		config.PaymentConfig.Currency != "USD" || config.PaymentConfig.CurrencySymbol != "$" ||
		config.PaymentConfig.MinWithdrawAmount != 250.5 || config.PaymentConfig.WithdrawFeeRate != 0 || strings.Join(config.PaymentConfig.WithdrawMethods, ",") != "USDT,Bank" ||
		config.SecurityConfig.EmailWhitelistSuffix != 1 || config.LastUpdated != fixedNow().Unix() || len(config.ConfigHash) != 32 {
		t.Fatalf("V2 config=%#v", config)
	}
	if config.ConfigHash == initialEnvelope.Data.ConfigHash {
		t.Fatalf("V2 config hash did not change after public settings changed: %s", config.ConfigHash)
	}
	wantHash := config.ConfigHash
	config.ConfigHash = ""
	canonical, err := marshalPHPJSON(config)
	if err != nil {
		t.Fatal(err)
	}
	digest := md5.Sum(canonical)
	if got := hex.EncodeToString(digest[:]); got != wantHash {
		t.Fatalf("config hash=%s want=%s canonical=%s", wantHash, got, canonical)
	}
}

func TestMarshalPHPJSONMatchesFixedPHPOracle(t *testing.T) {
	encoded, err := marshalPHPJSON(struct {
		URL     string `json:"url"`
		Unicode string `json:"unicode"`
		HTML    string `json:"html"`
	}{URL: "https://example.test/a", Unicode: "测试", HTML: "<&>"})
	if err != nil {
		t.Fatal(err)
	}
	digest := md5.Sum(encoded)
	if got := hex.EncodeToString(digest[:]); got != "92690644fdf6e7ff09208828210350eb" {
		t.Fatalf("PHP JSON hash=%s encoded=%s", got, encoded)
	}
}

func TestLegacyV2AppConfigHashIsDeterministicWithM1WithdrawalPolicy(t *testing.T) {
	oracleRecaptchaSiteKey := "6LeIxAcTAAAAA" + "JcZVRqyHh71UMIEGNQ_MXjiZKhI"
	config := newLegacyV2AppConfig(store.ClientAppRuntimeSettings{
		AppName: "XBoard", AppDescription: "XBoard is best!", AppURL: "https://app.example.com",
		Logo: "https://example.com/logo.png", TOSURL: "https://example.com/tos",
		Currency: "CNY", CurrencySymbol: "¥", CommissionWithdrawLimit: 10000,
		CommissionWithdrawMethods: []string{"支付宝", "USDT", "Paypal"}, EmailWhitelistSuffixPresent: true,
		CaptchaType: "recaptcha", RecaptchaSiteKey: oracleRecaptchaSiteKey,
		RecaptchaV3SiteKey:        oracleRecaptchaSiteKey,
		RecaptchaV3ScoreThreshold: 0.5, TurnstileSiteKey: "0x4AAAAAAAABkMYinukE8nzUg",
	}, 1_788_047_439)
	encoded, err := marshalPHPJSON(config)
	if err != nil {
		t.Fatal(err)
	}
	digest := md5.Sum(encoded)
	if got := hex.EncodeToString(digest[:]); got != "6d0342d76eb8a9432e6c4d455b8041d6" {
		t.Fatalf("M1 client config hash=%s encoded=%s", got, encoded)
	}
}

func containsAnyStringHTTP(values []any, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
