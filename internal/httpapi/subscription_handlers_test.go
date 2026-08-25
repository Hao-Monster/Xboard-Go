package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestClientSubscriptionMatchesLegacyTokenMethodEligibilityAndRouteContracts(t *testing.T) {
	api, database := newTestAPI(t)
	valid := createSubscriptionTestAccount(t, database, "subscriber@example.test", false, timePointerHTTP(fixedNow().Add(24*time.Hour)))
	expired := createSubscriptionTestAccount(t, database, "expired@example.test", false, timePointerHTTP(fixedNow().Add(-time.Second)))
	banned := createSubscriptionTestAccount(t, database, "banned@example.test", true, timePointerHTTP(fixedNow().Add(24*time.Hour)))

	tests := []struct {
		name         string
		method       string
		path         string
		headers      map[string]string
		wantStatus   int
		wantType     string
		wantCache    string
		wantBody     string
		wantUserInfo bool
	}{
		{name: "missing token", method: http.MethodGet, path: "/api/v1/client/subscribe", wantStatus: http.StatusForbidden, wantType: "application/json", wantCache: "no-cache, private", wantBody: `{"status":"fail","message":"token is null","data":null,"error":null}`},
		{name: "bad token", method: http.MethodGet, path: "/api/v1/client/subscribe?token=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", wantStatus: http.StatusForbidden, wantType: "application/json", wantCache: "no-cache, private", wantBody: `{"status":"fail","message":"token is error","data":null,"error":null}`},
		{name: "valid API", method: http.MethodGet, path: "/api/v1/client/subscribe?token=" + valid.SubscriptionToken, wantStatus: http.StatusOK, wantType: "text/plain; charset=utf-8", wantCache: "no-cache, private", wantUserInfo: true},
		{name: "valid dynamic", method: http.MethodGet, path: "/s/" + valid.SubscriptionToken, wantStatus: http.StatusOK, wantType: "text/plain; charset=utf-8", wantCache: "no-cache, private", wantUserInfo: true},
		{name: "wrong dynamic path", method: http.MethodGet, path: "/wrong/" + valid.SubscriptionToken, wantStatus: http.StatusNotFound, wantType: "text/plain; charset=utf-8", wantCache: "no-store, private", wantBody: "404 page not found\n"},
		{name: "valid HEAD", method: http.MethodHead, path: "/api/v1/client/subscribe?token=" + valid.SubscriptionToken, wantStatus: http.StatusMethodNotAllowed, wantType: "text/html; charset=utf-8", wantCache: "no-store, private"},
		{name: "invalid HEAD still token error", method: http.MethodHead, path: "/api/v1/client/subscribe?token=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", wantStatus: http.StatusForbidden, wantType: "application/json", wantCache: "no-cache, private", wantBody: `{"status":"fail","message":"token is error","data":null,"error":null}`},
		{name: "prefetch", method: http.MethodGet, path: "/api/v1/client/subscribe?token=" + valid.SubscriptionToken, headers: map[string]string{"Purpose": "prefetch"}, wantStatus: http.StatusTooEarly, wantType: "text/html; charset=utf-8", wantCache: "no-store, private"},
		{name: "sec prefetch", method: http.MethodGet, path: "/api/v1/client/subscribe?token=" + valid.SubscriptionToken, headers: map[string]string{"Sec-Purpose": "prefetch"}, wantStatus: http.StatusTooEarly, wantType: "text/html; charset=utf-8", wantCache: "no-store, private"},
		{name: "expired", method: http.MethodGet, path: "/api/v1/client/subscribe?token=" + expired.SubscriptionToken, wantStatus: http.StatusForbidden, wantType: "text/plain; charset=utf-8", wantCache: "no-cache, private"},
		{name: "banned", method: http.MethodGet, path: "/api/v1/client/subscribe?token=" + banned.SubscriptionToken, wantStatus: http.StatusForbidden, wantType: "text/plain; charset=utf-8", wantCache: "no-cache, private"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			for name, value := range test.headers {
				request.Header.Set(name, value)
			}
			response := httptest.NewRecorder()
			api.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body)
			}
			if got := response.Header().Get("Content-Type"); got != test.wantType {
				t.Errorf("Content-Type = %q, want %q", got, test.wantType)
			}
			if got := response.Header().Get("Cache-Control"); got != test.wantCache {
				t.Errorf("Cache-Control = %q, want %q", got, test.wantCache)
			}
			if test.wantBody != "" && response.Body.String() != test.wantBody {
				t.Errorf("body = %q, want %q", response.Body.String(), test.wantBody)
			}
			_, hasUserInfo := response.Header()["Subscription-Userinfo"]
			if hasUserInfo != test.wantUserInfo {
				t.Errorf("Subscription-Userinfo presence = %v, want %v", hasUserInfo, test.wantUserInfo)
			}
			if test.name == "valid HEAD" && response.Header().Get("Allow") != http.MethodGet {
				t.Errorf("Allow = %q, want GET", response.Header().Get("Allow"))
			}
			if response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Error("response is missing nosniff")
			}
		})
	}
}

func TestValidSubscriptionDoesNotEraseFailedTokenRateLimit(t *testing.T) {
	_, database := newTestAPI(t)
	valid := createSubscriptionTestAccount(t, database, "limiter-subscriber@example.test", false, timePointerHTTP(fixedNow().Add(time.Hour)))
	api := &server{
		store: database, now: fixedNow, panelURL: "https://panel.example.test", logger: slog.Default(),
		subscriptionFailures: newAttemptLimiter(2, time.Minute),
	}
	request := func(token string) int {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/client/subscribe?token="+token, nil)
		r.RemoteAddr = "192.0.2.44:12345"
		w := httptest.NewRecorder()
		api.clientSubscription(w, r)
		return w.Code
	}

	if got := request(strings.Repeat("a", 32)); got != http.StatusForbidden {
		t.Fatalf("first bad token status = %d, want 403", got)
	}
	if got := request(valid.SubscriptionToken); got != http.StatusOK {
		t.Fatalf("valid token status = %d, want 200", got)
	}
	if got := request(strings.Repeat("b", 32)); got != http.StatusForbidden {
		t.Fatalf("second bad token status = %d, want 403", got)
	}
	if got := request(strings.Repeat("c", 32)); got != http.StatusTooManyRequests {
		t.Fatalf("bad token after valid-token interleave status = %d, want 429", got)
	}
}

func TestAdministratorSubscriptionSettingsAreAtomicAndChangeDynamicPath(t *testing.T) {
	api, database := newTestAPI(t)
	account := createSubscriptionTestAccount(t, database, "path-test@example.test", false, timePointerHTTP(fixedNow().Add(time.Hour)))
	admin := loginAdmin(t, api)

	get := admin.request(t, api, http.MethodGet, "/api/v1/admin/subscription-settings", "")
	if get.Code != http.StatusOK {
		t.Fatalf("GET settings = %d %s", get.Code, get.Body)
	}
	var initial struct {
		Data store.SubscriptionSettings `json:"data"`
	}
	decodeResponse(t, get, &initial)
	if initial.Data.Path != "s" || initial.Data.Revision != 1 || len(initial.Data.Templates) != len(store.SubscriptionTemplateNames) {
		t.Fatalf("initial settings = %#v", initial.Data)
	}

	body, err := json.Marshal(map[string]any{
		"revision": initial.Data.Revision, "path": "feeds_1", "show_info": true, "show_protocol": true,
		"templates": emptySubscriptionTemplates(),
	})
	if err != nil {
		t.Fatal(err)
	}
	update := admin.request(t, api, http.MethodPut, "/api/v1/admin/subscription-settings", string(body))
	if update.Code != http.StatusOK {
		t.Fatalf("PUT settings = %d %s", update.Code, update.Body)
	}
	var updated struct {
		Data store.SubscriptionSettings `json:"data"`
	}
	decodeResponse(t, update, &updated)
	if updated.Data.Revision != 2 || updated.Data.Path != "feeds_1" || !updated.Data.ShowInfo || !updated.Data.ShowProtocol {
		t.Fatalf("updated settings = %#v", updated.Data)
	}
	if got := requestSubscription(api, "/s/"+account.SubscriptionToken).Code; got != http.StatusNotFound {
		t.Fatalf("old dynamic path status = %d, want 404", got)
	}
	if got := requestSubscription(api, "/feeds_1/"+account.SubscriptionToken).Code; got != http.StatusOK {
		t.Fatalf("new dynamic path status = %d, want 200", got)
	}

	stale := admin.request(t, api, http.MethodPut, "/api/v1/admin/subscription-settings", string(body))
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "revision_conflict") {
		t.Fatalf("stale update = %d %s", stale.Code, stale.Body)
	}
	invalidBody, _ := json.Marshal(map[string]any{
		"revision": updated.Data.Revision, "path": "../unsafe", "show_info": false, "show_protocol": false,
		"templates": emptySubscriptionTemplates(),
	})
	invalid := admin.request(t, api, http.MethodPut, "/api/v1/admin/subscription-settings", string(invalidBody))
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid update = %d %s", invalid.Code, invalid.Body)
	}
	preserved, err := database.GetSubscriptionSettings(t.Context())
	if err != nil || preserved.Revision != updated.Data.Revision || preserved.Path != updated.Data.Path {
		t.Fatalf("invalid update changed settings: %#v err=%v", preserved, err)
	}
}

func TestUserSubscriptionOverviewAndSecurityResetPreserveLegacyBusinessFlow(t *testing.T) {
	api, database := newTestAPI(t)
	created := createKnowledgeTestUser(t, database, "subscription-user@example.test", "subscription-user-password-123", 10<<30, false)
	ticketSettings, err := database.GetTicketSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(t.Context(), 1, ticketSettings.Revision, store.SaveTicketSettingsInput{
		AppName: ticketSettings.AppName, AppURL: "https://public.example.test/root/",
		TicketMustWaitReply: ticketSettings.TicketMustWaitReply, SMTPEnabled: ticketSettings.SMTPEnabled,
		SMTPHost: ticketSettings.SMTPHost, SMTPPort: ticketSettings.SMTPPort, SMTPUsername: ticketSettings.SMTPUsername,
		SMTPEncryption: ticketSettings.SMTPEncryption, SMTPFromAddress: ticketSettings.SMTPFromAddress,
	}, fixedNow()); err != nil {
		t.Fatal(err)
	}
	client := loginAs(t, api, created.email, created.password)
	before, err := database.GetSubscriptionAccount(t.Context(), created.id)
	if err != nil {
		t.Fatal(err)
	}

	overview := client.request(t, api, http.MethodGet, "/api/v1/subscription", "")
	if overview.Code != http.StatusOK || !containsAll(overview.Body.String(), before.SubscriptionToken,
		`"subscribe_url":"https://public.example.test/root/s/`, `"plan":null`, `"transfer_enable":10737418240`) {
		t.Fatalf("subscription overview status=%d body=%s", overview.Code, overview.Body)
	}
	if overview.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("subscription overview cache policy = %q", overview.Header().Get("Cache-Control"))
	}
	qr := client.request(t, api, http.MethodGet, "/api/v1/subscription/qr", "")
	if qr.Code != http.StatusOK || !containsAll(qr.Body.String(), `"subscribe_url":"https://public.example.test/root/s/`, `"qr_code":"data:image/svg+xml;base64,`) {
		t.Fatalf("subscription QR status=%d body=%s", qr.Code, qr.Body)
	}

	withoutCSRF := httptest.NewRequest(http.MethodPost, "/api/v1/subscription/security/reset", strings.NewReader(`{}`))
	withoutCSRF.Header.Set("Content-Type", "application/json")
	client.addCookies(withoutCSRF)
	rejected := httptest.NewRecorder()
	api.ServeHTTP(rejected, withoutCSRF)
	if rejected.Code != http.StatusForbidden {
		t.Fatalf("reset without CSRF status=%d body=%s", rejected.Code, rejected.Body)
	}

	reset := client.request(t, api, http.MethodPost, "/api/v1/subscription/security/reset", `{}`)
	if reset.Code != http.StatusOK {
		t.Fatalf("subscription reset status=%d body=%s", reset.Code, reset.Body)
	}
	var payload struct {
		Data struct {
			Token        string `json:"token"`
			UUID         string `json:"uuid"`
			SubscribeURL string `json:"subscribe_url"`
		} `json:"data"`
	}
	decodeResponse(t, reset, &payload)
	if payload.Data.Token == "" || payload.Data.Token == before.SubscriptionToken || payload.Data.UUID == before.UUID ||
		payload.Data.SubscribeURL != "https://public.example.test/root/s/"+payload.Data.Token {
		t.Fatalf("reset payload = %#v", payload.Data)
	}
	if old := requestSubscription(api, "/s/"+before.SubscriptionToken); old.Code != http.StatusForbidden || !strings.Contains(old.Body.String(), "token is error") {
		t.Fatalf("old subscription after reset status=%d body=%s", old.Code, old.Body)
	}
	if current := requestSubscription(api, "/s/"+payload.Data.Token); current.Code != http.StatusOK {
		t.Fatalf("current subscription after reset status=%d body=%s", current.Code, current.Body)
	}

	legacy := loginLegacyBearer(t, api, created.email, created.password)
	legacyOverview := bearerRequest(api, http.MethodGet, "/api/v1/user/getSubscribe", legacy.Authorization, "")
	if legacyOverview.Code != http.StatusOK || !containsAll(legacyOverview.Body.String(), `"message":"操作成功"`, payload.Data.Token, `"reset_day":null`) {
		t.Fatalf("legacy subscription overview status=%d body=%s", legacyOverview.Code, legacyOverview.Body)
	}
	if strings.Contains(legacyOverview.Body.String(), `"subscription_valid"`) || strings.Contains(legacyOverview.Body.String(), `"plan"`) {
		t.Fatalf("legacy no-plan response contains Go-only fields: %s", legacyOverview.Body)
	}
	legacyReset := bearerRequest(api, http.MethodGet, "/api/v1/user/resetSecurity", legacy.Authorization, "")
	if legacyReset.Code != http.StatusOK || !containsAll(legacyReset.Body.String(), `"message":"操作成功"`, `https://public.example.test/root/s/`) || strings.Contains(legacyReset.Body.String(), payload.Data.Token) {
		t.Fatalf("legacy subscription reset status=%d body=%s", legacyReset.Code, legacyReset.Body)
	}
}

func TestLegacySubscriptionResponsePreservesTimestampAndPlanJSONTypes(t *testing.T) {
	now := fixedNow()
	expires := now.Add(30 * 24 * time.Hour)
	reset := now.Add(6 * 24 * time.Hour)
	groupID := int64(3)
	resetMethod, capacity, device := 0, 0, 3
	response := legacySubscriptionResponse(userSubscriptionResponse{
		PlanID: &groupID, Token: strings.Repeat("b", 32), ExpiredAt: &expires,
		Upload: 1 << 30, Download: 2 << 30, TransferEnable: 10 << 30,
		Email: "legacy-contract@example.test", UUID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		DeviceLimit: 3, SpeedLimit: 100, NextResetAt: &reset, SubscribeURL: "https://panel.example.test/s/" + strings.Repeat("b", 32),
		ResetDay: &device,
		Plan: &store.Plan{
			ID: 3, GroupID: &groupID, TransferEnableGiB: 10, Name: "Legacy contract", Show: true,
			SortPosition: 1, Renew: true, ResetTrafficMethod: &resetMethod, CapacityLimit: &capacity,
			Prices: store.PlanPrices{"monthly": 1000}, Sell: true, DeviceLimit: &device,
			Revision: 9, CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
		},
	})
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if document["expired_at"] != float64(expires.Unix()) || document["next_reset_at"] != float64(reset.Unix()) {
		t.Fatalf("legacy timestamps are not Unix seconds: %s", encoded)
	}
	if _, exists := document["subscription_valid"]; exists {
		t.Fatalf("legacy response leaked Go-only subscription_valid: %s", encoded)
	}
	plan, ok := document["plan"].(map[string]any)
	if !ok || plan["created_at"] != float64(now.Add(-time.Hour).Unix()) || plan["updated_at"] != float64(now.Unix()) || plan["sell"] != float64(1) {
		t.Fatalf("legacy plan scalar types differ from Xboard: %s", encoded)
	}
	if plan["content"] != nil || plan["tags"] != nil {
		t.Fatalf("legacy empty optional plan fields must remain null: %s", encoded)
	}
	if _, exists := plan["revision"]; exists {
		t.Fatalf("legacy plan leaked Go-only revision: %s", encoded)
	}
}

func createSubscriptionTestAccount(t *testing.T, database *store.Store, email string, banned bool, expiredAt *time.Time) store.SubscriptionAccount {
	t.Helper()
	groupID := int64(3)
	user, err := database.CreateAdminUser(t.Context(), store.CreateAdminUserInput{
		Email: email, PasswordHash: "opaque-test-hash", GroupID: &groupID, TransferEnable: 10 << 30,
		ExpiredAt: expiredAt, Banned: banned,
	}, fixedNow())
	if err != nil {
		t.Fatalf("CreateAdminUser(%s): %v", email, err)
	}
	account, err := database.GetSubscriptionAccount(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("GetSubscriptionAccount(%d): %v", user.ID, err)
	}
	return account
}

func emptySubscriptionTemplates() map[string]string {
	templates := make(map[string]string, len(store.SubscriptionTemplateNames))
	for _, name := range store.SubscriptionTemplateNames {
		templates[name] = ""
	}
	return templates
}

func requestSubscription(api http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = "192.0.2.10:12345"
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

func timePointerHTTP(value time.Time) *time.Time { return &value }

func TestBoundedUTF8DoesNotSplitMultibyteInput(t *testing.T) {
	value := strings.Repeat("界", 100)
	bounded := boundedUTF8(value, 256)
	if !strings.HasPrefix(value, bounded) || len(bounded) > 256 || !utf8.ValidString(bounded) {
		t.Fatalf("boundedUTF8 produced invalid value %q", bounded)
	}
	if _, err := url.QueryUnescape(url.QueryEscape(bounded)); err != nil {
		t.Fatalf("bounded value cannot round-trip through query encoding: %v", err)
	}
}
