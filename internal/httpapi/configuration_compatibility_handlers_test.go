package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestLegacyConfigurationFullFetchUsesBoundedSingleReadPerGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	started := make(chan string, len(legacyConfigGroupNames))
	release := make(chan struct{})
	type loadResult struct {
		groups map[string]any
		err    error
	}
	var callsLock sync.Mutex
	calls := make(map[string]int, len(legacyConfigGroupNames))
	loaded := make(chan loadResult, 1)
	go func() {
		groups, err := loadLegacyConfigGroups(ctx, legacyConfigGroupNames[:], func(ctx context.Context, name string) (map[string]any, error) {
			callsLock.Lock()
			calls[name]++
			callsLock.Unlock()
			started <- name
			select {
			case <-release:
				return map[string]any{"name": name}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
		loaded <- loadResult{groups: groups, err: err}
	}()
	for range legacyConfigFetchConcurrency {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("full configuration readers did not reach the concurrency limit")
		}
	}
	select {
	case name := <-started:
		t.Fatalf("full configuration fetch exceeded %d concurrent readers with %q", legacyConfigFetchConcurrency, name)
	default:
	}
	close(release)
	var result loadResult
	select {
	case result = <-loaded:
	case <-ctx.Done():
		t.Fatal("full configuration fetch did not complete")
	}
	if result.err != nil || len(result.groups) != len(legacyConfigGroupNames) {
		t.Fatalf("full configuration fetch groups=%d err=%v", len(result.groups), result.err)
	}
	for _, name := range legacyConfigGroupNames {
		if calls[name] != 1 {
			t.Errorf("full configuration fetch calls[%q]=%d, want 1", name, calls[name])
		}
	}
}

func TestLegacyConfigurationFetchReturnsAllTenGroupsAndNarrowProjections(t *testing.T) {
	api, _ := newTestAPI(t)
	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization

	response := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch", authorization, "")
	if response.Code != http.StatusOK {
		t.Fatalf("full legacy config status=%d body=%s", response.Code, response.Body)
	}
	var envelope struct {
		Data map[string]map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode full legacy config: %v body=%s", err, response.Body)
	}
	wantGroups := []string{"invite", "site", "subscribe", "frontend", "server", "email", "telegram", "app", "safe", "subscribe_template"}
	if len(envelope.Data) != len(wantGroups) {
		t.Fatalf("legacy config group count=%d groups=%v", len(envelope.Data), mapKeys(envelope.Data))
	}
	for _, group := range wantGroups {
		if envelope.Data[group] == nil {
			t.Fatalf("legacy config group %q is missing: groups=%v", group, mapKeys(envelope.Data))
		}
	}
	assertLegacyConfigKeys(t, envelope.Data["invite"], []string{
		"invite_force", "invite_commission", "invite_gen_limit", "invite_never_expire",
		"commission_first_time_enable", "commission_auto_check_enable", "commission_withdraw_limit",
		"commission_withdraw_method", "withdraw_close_enable", "commission_distribution_enable",
		"commission_distribution_l1", "commission_distribution_l2", "commission_distribution_l3",
	})
	assertLegacyConfigKeys(t, envelope.Data["site"], []string{
		"logo", "force_https", "stop_register", "app_name", "app_description", "app_url", "subscribe_url",
		"try_out_plan_id", "try_out_hour", "tos_url", "currency", "currency_symbol", "ticket_must_wait_reply",
	})
	assertLegacyConfigKeys(t, envelope.Data["frontend"], []string{
		"frontend_theme", "frontend_theme_sidebar", "frontend_theme_header", "frontend_theme_color", "frontend_background_url",
	})
	assertLegacyConfigKeys(t, envelope.Data["server"], []string{
		"server_token", "server_pull_interval", "server_push_interval", "device_limit_mode", "server_ws_enable", "server_ws_url",
	})
	assertLegacyConfigKeys(t, envelope.Data["safe"], []string{
		"email_verify", "safe_mode_enable", "secure_path", "email_whitelist_enable", "email_whitelist_suffix",
		"email_gmail_limit_enable", "captcha_enable", "captcha_type", "recaptcha_enable", "recaptcha_key",
		"recaptcha_site_key", "recaptcha_v3_secret_key", "recaptcha_v3_site_key", "recaptcha_v3_score_threshold",
		"turnstile_secret_key", "turnstile_site_key", "register_limit_by_ip_enable", "register_limit_count",
		"register_limit_expire", "password_limit_enable", "password_limit_count", "password_limit_expire",
	})
	assertLegacyConfigKeys(t, envelope.Data["subscribe_template"], []string{
		"subscribe_template_singbox", "subscribe_template_clash", "subscribe_template_clashmeta",
		"subscribe_template_stash", "subscribe_template_surge", "subscribe_template_surfboard",
	})
	for group, field := range map[string]string{
		"email": "email_password", "telegram": "telegram_bot_token", "server": "server_token",
		"safe": "recaptcha_key",
	} {
		if got, ok := envelope.Data[group][field].(string); !ok || got != "" {
			t.Fatalf("legacy secret %s.%s=%#v, want blank string", group, field, envelope.Data[group][field])
		}
	}

	for _, group := range wantGroups {
		narrow := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key="+group, authorization, "")
		if narrow.Code != http.StatusOK {
			t.Fatalf("legacy config key=%s status=%d body=%s", group, narrow.Code, narrow.Body)
		}
		var narrowEnvelope struct {
			Data map[string]map[string]any `json:"data"`
		}
		if err := json.Unmarshal(narrow.Body.Bytes(), &narrowEnvelope); err != nil {
			t.Fatalf("decode legacy config key=%s: %v body=%s", group, err, narrow.Body)
		}
		if len(narrowEnvelope.Data) != 1 || narrowEnvelope.Data[group] == nil {
			t.Fatalf("legacy config key=%s returned groups=%v", group, mapKeys(narrowEnvelope.Data))
		}
	}

	unknown := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=unknown", authorization, "")
	if unknown.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown legacy config group status=%d body=%s", unknown.Code, unknown.Body)
	}
	for _, target := range []string{
		"/api/v2/admin/config/fetch?key=",
		"/api/v2/admin/config/fetch?key=invite&key=site",
	} {
		response := bearerRequest(api, http.MethodGet, target, authorization, "")
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("ambiguous legacy config query %q status=%d body=%s", target, response.Code, response.Body)
		}
	}
}

func TestLegacyConfigurationSaveSupportsAllTenAtomicPartialGroups(t *testing.T) {
	api, database := newTestAPI(t)
	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization

	saves := []struct {
		name string
		body string
	}{
		{"invite", `{"commission_withdraw_limit":"250.50","commission_withdraw_method":["USDT"]}`},
		{"site", `{"app_name":"Compatibility Board","app_description":"Legacy contract","currency":"usd","force_https":1,"stop_register":0,"ticket_must_wait_reply":true}`},
		{"subscribe", `{"plan_change_enable":false,"subscribe_path":"legacy"}`},
		{"frontend", `{"frontend_theme_sidebar":"dark","frontend_theme_header":"light","frontend_theme_color":"blue"}`},
		{"server token", `{"server_token":"abcdefghijklmnop","server_pull_interval":90}`},
		{"server redacted token", `{"server_token":"","server_push_interval":120}`},
		{"email", `{"email_host":"smtp.example.test","email_port":587,"email_username":"bot@example.test","email_password":"mail-secret-value","email_encryption":"tls","email_from_address":"bot@example.test","remind_mail_enable":true}`},
		{"email redacted password", `{"email_password":"","email_username":"bot2@example.test"}`},
		{"telegram", `{"telegram_discuss_link":"https://t.me/xboard_compatibility"}`},
		{"app", `{"windows_version":"1.2.3","windows_download_url":"https://downloads.example.test/xboard.exe"}`},
		{"safe secret", `{"recaptcha_key":"recaptcha-secret-value","register_limit_count":4}`},
		{"safe redacted secret", `{"recaptcha_key":"","password_limit_count":7}`},
		{"subscribe template", `{"subscribe_template_singbox":"{\"outbounds\":[]}"}`},
	}
	for _, item := range saves {
		t.Run(item.name, func(t *testing.T) {
			response := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/save", authorization, item.body)
			if response.Code != http.StatusOK {
				t.Fatalf("legacy %s save status=%d body=%s", item.name, response.Code, response.Body)
			}
		})
	}

	mixed := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/save", authorization, `{"app_name":"Mixed","server_pull_interval":60}`)
	if mixed.Code != http.StatusUnprocessableEntity {
		t.Fatalf("mixed legacy groups status=%d body=%s", mixed.Code, mixed.Body)
	}
	audits, err := database.ListAdminAuditLogs(t.Context(), store.AdminAuditFilter{Page: 1, PageSize: 100, Query: "/config/save"})
	if err != nil || audits.Total != int64(len(saves)+1) {
		t.Fatalf("legacy config saves must emit exactly one audit each: total=%d want=%d err=%v", audits.Total, len(saves)+1, err)
	}

	response := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch", authorization, "")
	if response.Code != http.StatusOK {
		t.Fatalf("updated legacy config status=%d body=%s", response.Code, response.Body)
	}
	var envelope struct {
		Data map[string]map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode updated legacy config: %v body=%s", err, response.Body)
	}
	checks := []struct {
		group string
		field string
		want  any
	}{
		{"invite", "commission_withdraw_limit", 250.5},
		{"site", "app_name", "Compatibility Board"},
		{"site", "currency", "USD"},
		{"site", "force_https", float64(1)},
		{"site", "ticket_must_wait_reply", true},
		{"subscribe", "plan_change_enable", false},
		{"subscribe", "subscribe_path", "legacy"},
		{"frontend", "frontend_theme_sidebar", "dark"},
		{"frontend", "frontend_theme_header", "light"},
		{"frontend", "frontend_theme_color", "blue"},
		{"server", "server_pull_interval", float64(90)},
		{"server", "server_push_interval", float64(120)},
		{"email", "remind_mail_enable", true},
		{"telegram", "telegram_discuss_link", "https://t.me/xboard_compatibility"},
		{"app", "windows_version", "1.2.3"},
		{"safe", "register_limit_count", float64(4)},
		{"safe", "password_limit_count", float64(7)},
		{"subscribe_template", "subscribe_template_singbox", "{\n    \"outbounds\": []\n}"},
	}
	for _, check := range checks {
		if got := envelope.Data[check.group][check.field]; got != check.want {
			t.Errorf("updated legacy %s.%s=%#v, want %#v", check.group, check.field, got, check.want)
		}
	}
	for group, field := range map[string]string{"server": "server_token", "safe": "recaptcha_key"} {
		if got := envelope.Data[group][field]; got != "" {
			t.Errorf("legacy secret %s.%s=%#v, want blank", group, field, got)
		}
	}
	nodeSettings, err := database.GetNodeAgentSettings(t.Context())
	if err != nil || !nodeSettings.ServerTokenConfigured {
		t.Fatalf("blank legacy server token did not preserve configured secret: %#v err=%v", nodeSettings, err)
	}
	siteSettings, err := database.GetSiteSettings(t.Context())
	if err != nil || !siteSettings.RecaptchaSecretConfigured {
		t.Fatalf("blank legacy captcha secret did not preserve configured secret: %#v err=%v", siteSettings, err)
	}
	mailSettings, err := database.GetMailSettings(t.Context())
	if err != nil || !mailSettings.SMTPPasswordSet || mailSettings.SMTPUsername != "bot2@example.test" {
		t.Fatalf("blank legacy mail password did not preserve configured secret: %#v err=%v", mailSettings, err)
	}
}

func assertLegacyConfigKeys(t *testing.T, values map[string]any, fields []string) {
	t.Helper()
	for _, field := range fields {
		if _, exists := values[field]; !exists {
			t.Fatalf("legacy config field %q is missing from %v", field, mapKeys(values))
		}
	}
}

func mapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
