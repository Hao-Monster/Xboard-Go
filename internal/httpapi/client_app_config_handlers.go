package httpapi

import (
	"bytes"
	"crypto/md5" // Compatibility checksum required by the legacy client wire format.
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/Hao-Monster/Xboard-Go/internal/subscription"
)

func (s *server) legacyClientAppConfigV1(w http.ResponseWriter, r *http.Request) {
	account, ok := s.authenticateLegacyClientApp(w, r)
	if !ok {
		return
	}
	var source []store.SubscriptionNode
	var err error
	if account.AvailableAt(s.now()) && account.GroupID != nil {
		source, err = s.store.ListSubscriptionNodes(r.Context(), *account.GroupID)
		if err != nil {
			handleStoreError(w, err)
			return
		}
	}
	prepared, err := subscription.PrepareNodes(account, source, subscription.PrepareOptions{Now: s.now()})
	if err != nil {
		s.logger.Error("prepare legacy client app configuration", "user_id", account.ID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	body, err := s.legacyAppClashRenderer.Render(prepared)
	if err != nil {
		s.logger.Error("render legacy client app configuration", "user_id", account.ID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *server) legacyClientAppConfigV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticateLegacyClientApp(w, r); !ok {
		return
	}
	settings, err := s.store.GetClientAppRuntimeSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	config := newLegacyV2AppConfig(settings, s.now().Unix())
	encodedForHash, err := marshalPHPJSON(config)
	if err != nil {
		s.logger.Error("encode legacy V2 client app configuration hash", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	digest := md5.Sum(encodedForHash)
	config.ConfigHash = hex.EncodeToString(digest[:])
	body, err := json.Marshal(struct {
		Data legacyV2AppConfig `json:"data"`
	}{Data: config})
	if err != nil {
		s.logger.Error("encode legacy V2 client app configuration", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *server) authenticateLegacyClientApp(w http.ResponseWriter, r *http.Request) (store.SubscriptionAccount, bool) {
	w.Header().Set("Cache-Control", "no-store, private")
	token := r.URL.Query().Get("token")
	if token == "" {
		writeClientAppTokenError(w, "token is null")
		return store.SubscriptionAccount{}, false
	}
	requestKey := requestIP(r)
	if !s.subscriptionFailures.allowed(requestKey, s.now()) {
		w.Header().Set("Retry-After", "900")
		writeAPIError(w, http.StatusTooManyRequests, "subscription_rate_limited", "订阅令牌错误次数过多，请稍后重试", nil)
		return store.SubscriptionAccount{}, false
	}
	account, err := s.store.FindSubscriptionAccount(r.Context(), token)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalidInput) {
		s.subscriptionFailures.failed(requestKey, s.now())
		writeClientAppTokenError(w, "token is error")
		return store.SubscriptionAccount{}, false
	}
	if err != nil {
		handleStoreError(w, err)
		return store.SubscriptionAccount{}, false
	}
	return account, true
}

type legacyV2AppConfig struct {
	AppInfo            legacyV2AppInfo            `json:"app_info"`
	Features           legacyV2AppFeatures        `json:"features"`
	UIConfig           legacyV2UIConfig           `json:"ui_config"`
	BusinessRules      legacyV2BusinessRules      `json:"business_rules"`
	ServerConfig       legacyV2ServerConfig       `json:"server_config"`
	SecurityConfig     legacyV2SecurityConfig     `json:"security_config"`
	PaymentConfig      legacyV2PaymentConfig      `json:"payment_config"`
	NotificationConfig legacyV2NotificationConfig `json:"notification_config"`
	CacheConfig        legacyV2CacheConfig        `json:"cache_config"`
	LastUpdated        int64                      `json:"last_updated"`
	ConfigHash         string                     `json:"config_hash,omitempty"`
}

type legacyV2AppInfo struct {
	AppName        string `json:"app_name"`
	AppDescription string `json:"app_description"`
	AppURL         string `json:"app_url"`
	Logo           string `json:"logo"`
	Version        string `json:"version"`
}

type legacyV2AppFeatures struct {
	EnableRegister         bool `json:"enable_register"`
	EnableInviteSystem     bool `json:"enable_invite_system"`
	EnableTelegramBot      bool `json:"enable_telegram_bot"`
	EnableTicketSystem     bool `json:"enable_ticket_system"`
	TicketMustWaitReply    bool `json:"ticket_must_wait_reply"`
	EnableCommissionSystem bool `json:"enable_commission_system"`
	EnableTrafficLog       bool `json:"enable_traffic_log"`
	EnableKnowledgeBase    bool `json:"enable_knowledge_base"`
	EnableAnnouncements    bool `json:"enable_announcements"`
	EnableAutoRenewal      bool `json:"enable_auto_renewal"`
	EnableCouponSystem     bool `json:"enable_coupon_system"`
	EnableSpeedTest        bool `json:"enable_speed_test"`
	EnableServerPing       bool `json:"enable_server_ping"`
}

type legacyV2UIConfig struct {
	Theme      legacyV2Theme      `json:"theme"`
	HomeScreen legacyV2HomeScreen `json:"home_screen"`
	ServerList legacyV2ServerList `json:"server_list"`
}

type legacyV2Theme struct {
	PrimaryColor    string `json:"primary_color"`
	SecondaryColor  string `json:"secondary_color"`
	AccentColor     string `json:"accent_color"`
	BackgroundColor string `json:"background_color"`
	TextColor       string `json:"text_color"`
}

type legacyV2HomeScreen struct {
	ShowSpeedTest        bool   `json:"show_speed_test"`
	ShowTrafficChart     bool   `json:"show_traffic_chart"`
	ShowServerPing       bool   `json:"show_server_ping"`
	DefaultServerSort    string `json:"default_server_sort"`
	ShowConnectionStatus bool   `json:"show_connection_status"`
}

type legacyV2ServerList struct {
	ShowCountryFlags bool `json:"show_country_flags"`
	ShowPingValues   bool `json:"show_ping_values"`
	ShowTrafficUsage bool `json:"show_traffic_usage"`
	GroupByCountry   bool `json:"group_by_country"`
	ShowServerStatus bool `json:"show_server_status"`
}

type legacyV2BusinessRules struct {
	MinPasswordLength          int     `json:"min_password_length"`
	MaxLoginAttempts           int     `json:"max_login_attempts"`
	SessionTimeoutMinutes      int     `json:"session_timeout_minutes"`
	AutoDisconnectAfterMinutes int     `json:"auto_disconnect_after_minutes"`
	MaxConcurrentConnections   int     `json:"max_concurrent_connections"`
	TrafficWarningThreshold    float64 `json:"traffic_warning_threshold"`
	SubscriptionReminderDays   []int   `json:"subscription_reminder_days"`
	ConnectionTimeoutSeconds   int     `json:"connection_timeout_seconds"`
	HealthCheckIntervalSeconds int     `json:"health_check_interval_seconds"`
}

type legacyV2ServerConfig struct {
	DefaultKernel     string   `json:"default_kernel"`
	AutoSelectFastest bool     `json:"auto_select_fastest"`
	FallbackServers   []string `json:"fallback_servers"`
	EnableAutoSwitch  bool     `json:"enable_auto_switch"`
	SwitchThresholdMS int      `json:"switch_threshold_ms"`
}

type legacyV2SecurityConfig struct {
	TOSURL                    string  `json:"tos_url"`
	PrivacyPolicyURL          string  `json:"privacy_policy_url"`
	IsEmailVerify             int     `json:"is_email_verify"`
	IsInviteForce             int     `json:"is_invite_force"`
	EmailWhitelistSuffix      int     `json:"email_whitelist_suffix"`
	IsCaptcha                 int     `json:"is_captcha"`
	CaptchaType               string  `json:"captcha_type"`
	RecaptchaSiteKey          string  `json:"recaptcha_site_key"`
	RecaptchaV3SiteKey        string  `json:"recaptcha_v3_site_key"`
	RecaptchaV3ScoreThreshold float64 `json:"recaptcha_v3_score_threshold"`
	TurnstileSiteKey          string  `json:"turnstile_site_key"`
}

type legacyV2PaymentConfig struct {
	Currency          string   `json:"currency"`
	CurrencySymbol    string   `json:"currency_symbol"`
	WithdrawMethods   []string `json:"withdraw_methods"`
	MinWithdrawAmount float64  `json:"min_withdraw_amount"`
	WithdrawFeeRate   float64  `json:"withdraw_fee_rate"`
}

type legacyV2NotificationConfig struct {
	EnablePushNotifications  bool                         `json:"enable_push_notifications"`
	EnableEmailNotifications bool                         `json:"enable_email_notifications"`
	EnableSMSNotifications   bool                         `json:"enable_sms_notifications"`
	NotificationSchedule     legacyV2NotificationSchedule `json:"notification_schedule"`
}

type legacyV2NotificationSchedule struct {
	TrafficWarning     bool `json:"traffic_warning"`
	SubscriptionExpiry bool `json:"subscription_expiry"`
	ServerMaintenance  bool `json:"server_maintenance"`
	PromotionalOffers  bool `json:"promotional_offers"`
}

type legacyV2CacheConfig struct {
	ConfigCacheDuration     int `json:"config_cache_duration"`
	ServerListCacheDuration int `json:"server_list_cache_duration"`
	UserInfoCacheDuration   int `json:"user_info_cache_duration"`
}

func newLegacyV2AppConfig(settings store.ClientAppRuntimeSettings, nowUnix int64) legacyV2AppConfig {
	return legacyV2AppConfig{
		AppInfo: legacyV2AppInfo{AppName: settings.AppName, AppDescription: settings.AppDescription, AppURL: settings.AppURL, Logo: settings.Logo, Version: "1.0.0"},
		Features: legacyV2AppFeatures{
			EnableRegister: true, EnableInviteSystem: true, EnableTelegramBot: settings.TelegramBotEnabled,
			EnableTicketSystem: true, TicketMustWaitReply: settings.TicketMustWaitReply,
			EnableCommissionSystem: true, EnableTrafficLog: true, EnableKnowledgeBase: true,
			EnableAnnouncements: true, EnableAutoRenewal: false, EnableCouponSystem: true,
			EnableSpeedTest: true, EnableServerPing: true,
		},
		UIConfig: legacyV2UIConfig{
			Theme:      legacyV2Theme{PrimaryColor: "#00C851", SecondaryColor: "#007E33", AccentColor: "#FF6B35", BackgroundColor: "#F5F5F5", TextColor: "#333333"},
			HomeScreen: legacyV2HomeScreen{ShowSpeedTest: true, ShowTrafficChart: true, ShowServerPing: true, DefaultServerSort: "ping", ShowConnectionStatus: true},
			ServerList: legacyV2ServerList{ShowCountryFlags: true, ShowPingValues: true, ShowTrafficUsage: true, GroupByCountry: false, ShowServerStatus: true},
		},
		BusinessRules: legacyV2BusinessRules{
			MinPasswordLength: 8, MaxLoginAttempts: 5, SessionTimeoutMinutes: 30,
			AutoDisconnectAfterMinutes: 60, MaxConcurrentConnections: 3, TrafficWarningThreshold: 0.8,
			SubscriptionReminderDays: []int{7, 3, 1}, ConnectionTimeoutSeconds: 10, HealthCheckIntervalSeconds: 30,
		},
		ServerConfig: legacyV2ServerConfig{DefaultKernel: "clash", AutoSelectFastest: true, FallbackServers: []string{"server1", "server2"}, EnableAutoSwitch: true, SwitchThresholdMS: 1000},
		SecurityConfig: legacyV2SecurityConfig{
			TOSURL: settings.TOSURL, PrivacyPolicyURL: "https://example.com/privacy",
			IsEmailVerify: boolToInt(settings.EmailVerificationEnabled), IsInviteForce: boolToInt(settings.InvitationForceEnabled),
			EmailWhitelistSuffix: boolToInt(settings.EmailWhitelistSuffixPresent), IsCaptcha: boolToInt(settings.CaptchaEnabled),
			CaptchaType: settings.CaptchaType, RecaptchaSiteKey: settings.RecaptchaSiteKey,
			RecaptchaV3SiteKey: settings.RecaptchaV3SiteKey, RecaptchaV3ScoreThreshold: settings.RecaptchaV3ScoreThreshold,
			TurnstileSiteKey: settings.TurnstileSiteKey,
		},
		PaymentConfig: legacyV2PaymentConfig{
			Currency: settings.Currency, CurrencySymbol: settings.CurrencySymbol,
			WithdrawMethods:   append([]string(nil), settings.CommissionWithdrawMethods...),
			MinWithdrawAmount: float64(settings.CommissionWithdrawLimit) / 100, WithdrawFeeRate: 0,
		},
		NotificationConfig: legacyV2NotificationConfig{
			EnablePushNotifications: true, EnableEmailNotifications: true, EnableSMSNotifications: false,
			NotificationSchedule: legacyV2NotificationSchedule{TrafficWarning: true, SubscriptionExpiry: true, ServerMaintenance: true, PromotionalOffers: false},
		},
		CacheConfig: legacyV2CacheConfig{ConfigCacheDuration: 3600, ServerListCacheDuration: 1800, UserInfoCacheDuration: 900},
		LastUpdated: nowUnix,
	}
}

func marshalPHPJSON(value any) ([]byte, error) {
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	encoded := bytes.TrimSuffix(raw.Bytes(), []byte{'\n'})
	result := make([]byte, 0, len(encoded)+32)
	inString, escaped := false, false
	for index := 0; index < len(encoded); {
		current := encoded[index]
		if !inString {
			result = append(result, current)
			if current == '"' {
				inString = true
			}
			index++
			continue
		}
		if escaped {
			result = append(result, current)
			escaped = false
			index++
			continue
		}
		if current == '\\' {
			result = append(result, current)
			escaped = true
			index++
			continue
		}
		if current == '"' {
			result = append(result, current)
			inString = false
			index++
			continue
		}
		if current == '/' {
			result = append(result, '\\', '/')
			index++
			continue
		}
		if current < utf8.RuneSelf {
			result = append(result, current)
			index++
			continue
		}
		character, size := utf8.DecodeRune(encoded[index:])
		if character == utf8.RuneError && size == 1 {
			return nil, errors.New("cannot encode invalid UTF-8 as PHP JSON")
		}
		for _, unit := range utf16.Encode([]rune{character}) {
			result = appendPHPUnicodeEscape(result, unit)
		}
		index += size
	}
	return result, nil
}

func appendPHPUnicodeEscape(destination []byte, value uint16) []byte {
	const digits = "0123456789abcdef"
	return append(destination, '\\', 'u',
		digits[value>>12], digits[value>>8&0x0f], digits[value>>4&0x0f], digits[value&0x0f])
}
