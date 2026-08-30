package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"unicode"

	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type legacyConfigInt int

func (value *legacyConfigInt) UnmarshalJSON(data []byte) error {
	var number int
	if err := json.Unmarshal(data, &number); err == nil {
		*value = legacyConfigInt(number)
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := strconv.Atoi(text)
	if err != nil || strconv.Itoa(parsed) != text {
		return errors.New("legacy config integer must be a canonical base-10 integer")
	}
	*value = legacyConfigInt(parsed)
	return nil
}

type legacyConfigBool bool

func (value *legacyConfigBool) UnmarshalJSON(data []byte) error {
	var boolean bool
	if err := json.Unmarshal(data, &boolean); err == nil {
		*value = legacyConfigBool(boolean)
		return nil
	}
	var number int
	if err := json.Unmarshal(data, &number); err != nil || (number != 0 && number != 1) {
		return errors.New("legacy config boolean must be true, false, 0, or 1")
	}
	*value = legacyConfigBool(number == 1)
	return nil
}

func legacyConfigBoolPointer(value *legacyConfigBool) *bool {
	if value == nil {
		return nil
	}
	result := bool(*value)
	return &result
}

func legacyConfigIntPointer(value *legacyConfigInt) *int {
	if value == nil {
		return nil
	}
	result := int(*value)
	return &result
}

type commissionSettingsRequest struct {
	Revision            *int64                `json:"revision,omitempty"`
	InviteCommission    *int                  `json:"invite_commission"`
	FirstTimeEnabled    *bool                 `json:"commission_first_time_enable"`
	AutoCheckEnabled    *bool                 `json:"commission_auto_check_enable"`
	WithdrawLimit       *store.CurrencyAmount `json:"commission_withdraw_limit"`
	WithdrawMethods     *[]string             `json:"commission_withdraw_method"`
	WithdrawClosed      *bool                 `json:"withdraw_close_enable"`
	DistributionEnabled *bool                 `json:"commission_distribution_enable"`
	DistributionL1      *int                  `json:"commission_distribution_l1"`
	DistributionL2      *int                  `json:"commission_distribution_l2"`
	DistributionL3      *int                  `json:"commission_distribution_l3"`
}

type legacyConfigSaveRequest struct {
	Logo                *string           `json:"logo"`
	Currency            *string           `json:"currency"`
	CurrencySymbol      *string           `json:"currency_symbol"`
	ForceHTTPS          *legacyConfigBool `json:"force_https"`
	StopRegister        *legacyConfigBool `json:"stop_register"`
	AppName             *string           `json:"app_name"`
	AppDescription      *string           `json:"app_description"`
	AppURL              *string           `json:"app_url"`
	SubscribeURL        *string           `json:"subscribe_url"`
	TrialPlanID         *int64            `json:"try_out_plan_id"`
	TrialHours          *int              `json:"try_out_hour"`
	TOSURL              *string           `json:"tos_url"`
	TicketMustWaitReply *bool             `json:"ticket_must_wait_reply"`

	InvitationForce       *bool                 `json:"invite_force"`
	InviteCommission      *int                  `json:"invite_commission"`
	InvitationCodeLimit   *int                  `json:"invite_gen_limit"`
	InvitationNeverExpire *bool                 `json:"invite_never_expire"`
	FirstTimeEnabled      *bool                 `json:"commission_first_time_enable"`
	AutoCheckEnabled      *bool                 `json:"commission_auto_check_enable"`
	WithdrawLimit         *store.CurrencyAmount `json:"commission_withdraw_limit"`
	WithdrawMethods       *[]string             `json:"commission_withdraw_method"`
	WithdrawClosed        *bool                 `json:"withdraw_close_enable"`
	DistributionEnabled   *bool                 `json:"commission_distribution_enable"`
	DistributionL1        *int                  `json:"commission_distribution_l1"`
	DistributionL2        *int                  `json:"commission_distribution_l2"`
	DistributionL3        *int                  `json:"commission_distribution_l3"`

	PlanChangeEnabled    *bool            `json:"plan_change_enable"`
	ResetTrafficMethod   *legacyConfigInt `json:"reset_traffic_method"`
	SurplusEnabled       *bool            `json:"surplus_enable"`
	NewOrderEventID      *legacyConfigInt `json:"new_order_event_id"`
	RenewOrderEventID    *legacyConfigInt `json:"renew_order_event_id"`
	ChangeOrderEventID   *legacyConfigInt `json:"change_order_event_id"`
	ShowInfo             *bool            `json:"show_info_to_server_enable"`
	ShowProtocol         *bool            `json:"show_protocol_to_server_enable"`
	DefaultRemindExpire  *bool            `json:"default_remind_expire"`
	DefaultRemindTraffic *bool            `json:"default_remind_traffic"`
	Path                 *string          `json:"subscribe_path"`

	ServerToken      *string `json:"server_token"`
	PullInterval     *int    `json:"server_pull_interval"`
	PushInterval     *int    `json:"server_push_interval"`
	DeviceLimitMode  *int    `json:"device_limit_mode"`
	WebSocketEnabled *bool   `json:"server_ws_enable"`
	WebSocketURL     *string `json:"server_ws_url"`

	FrontendTheme         *string `json:"frontend_theme"`
	FrontendSidebarStyle  *string `json:"frontend_theme_sidebar"`
	FrontendHeaderStyle   *string `json:"frontend_theme_header"`
	FrontendThemeColor    *string `json:"frontend_theme_color"`
	FrontendBackgroundURL *string `json:"frontend_background_url"`

	EmailHost         *string          `json:"email_host"`
	EmailPort         *legacyConfigInt `json:"email_port"`
	EmailUsername     *string          `json:"email_username"`
	EmailPassword     *string          `json:"email_password"`
	EmailEncryption   *string          `json:"email_encryption"`
	EmailFromAddress  *string          `json:"email_from_address"`
	RemindMailEnabled *bool            `json:"remind_mail_enable"`

	TelegramBotEnabled  *bool   `json:"telegram_bot_enable"`
	TelegramBotToken    *string `json:"telegram_bot_token"`
	TelegramWebhookURL  *string `json:"telegram_webhook_url"`
	TelegramDiscussLink *string `json:"telegram_discuss_link"`

	WindowsVersion     *string `json:"windows_version"`
	WindowsDownloadURL *string `json:"windows_download_url"`
	MacOSVersion       *string `json:"macos_version"`
	MacOSDownloadURL   *string `json:"macos_download_url"`
	AndroidVersion     *string `json:"android_version"`
	AndroidDownloadURL *string `json:"android_download_url"`

	EmailVerificationEnabled   *bool     `json:"email_verify"`
	SafeModeEnabled            *bool     `json:"safe_mode_enable"`
	SecurePath                 *string   `json:"secure_path"`
	EmailWhitelistEnabled      *bool     `json:"email_whitelist_enable"`
	EmailWhitelistSuffixes     *[]string `json:"email_whitelist_suffix"`
	GmailAliasLimitEnabled     *bool     `json:"email_gmail_limit_enable"`
	CaptchaEnabled             *bool     `json:"captcha_enable"`
	CaptchaType                *string   `json:"captcha_type"`
	RecaptchaEnabled           *bool     `json:"recaptcha_enable"`
	RecaptchaSecret            *string   `json:"recaptcha_key"`
	RecaptchaSiteKey           *string   `json:"recaptcha_site_key"`
	RecaptchaV3Secret          *string   `json:"recaptcha_v3_secret_key"`
	RecaptchaV3SiteKey         *string   `json:"recaptcha_v3_site_key"`
	RecaptchaV3ScoreThreshold  *float64  `json:"recaptcha_v3_score_threshold"`
	TurnstileSecret            *string   `json:"turnstile_secret_key"`
	TurnstileSiteKey           *string   `json:"turnstile_site_key"`
	RegistrationIPLimitEnabled *bool     `json:"register_limit_by_ip_enable"`
	RegistrationIPLimitCount   *int      `json:"register_limit_count"`
	RegistrationIPLimitMinutes *int      `json:"register_limit_expire"`
	PasswordLimitEnabled       *bool     `json:"password_limit_enable"`
	PasswordLimitCount         *int      `json:"password_limit_count"`
	PasswordLimitMinutes       *int      `json:"password_limit_expire"`

	SubscriptionTemplateSingbox   *string `json:"subscribe_template_singbox"`
	SubscriptionTemplateClash     *string `json:"subscribe_template_clash"`
	SubscriptionTemplateClashMeta *string `json:"subscribe_template_clashmeta"`
	SubscriptionTemplateStash     *string `json:"subscribe_template_stash"`
	SubscriptionTemplateSurge     *string `json:"subscribe_template_surge"`
	SubscriptionTemplateSurfboard *string `json:"subscribe_template_surfboard"`
}

func (input legacyConfigSaveRequest) hasInvite() bool {
	return input.InvitationForce != nil || input.InviteCommission != nil || input.InvitationCodeLimit != nil || input.InvitationNeverExpire != nil ||
		input.FirstTimeEnabled != nil || input.AutoCheckEnabled != nil ||
		input.WithdrawLimit != nil || input.WithdrawMethods != nil ||
		input.WithdrawClosed != nil || input.DistributionEnabled != nil || input.DistributionL1 != nil ||
		input.DistributionL2 != nil || input.DistributionL3 != nil
}

func (input legacyConfigSaveRequest) hasSite() bool {
	return input.Logo != nil || input.Currency != nil || input.CurrencySymbol != nil || input.ForceHTTPS != nil || input.StopRegister != nil ||
		input.AppName != nil || input.AppDescription != nil || input.AppURL != nil || input.SubscribeURL != nil ||
		input.TrialPlanID != nil || input.TrialHours != nil || input.TOSURL != nil || input.TicketMustWaitReply != nil
}

func (input legacyConfigSaveRequest) hasSafe() bool {
	return input.EmailVerificationEnabled != nil || input.SafeModeEnabled != nil || input.SecurePath != nil ||
		input.EmailWhitelistEnabled != nil || input.EmailWhitelistSuffixes != nil || input.GmailAliasLimitEnabled != nil ||
		input.CaptchaEnabled != nil || input.CaptchaType != nil || input.RecaptchaEnabled != nil ||
		input.RecaptchaSecret != nil || input.RecaptchaSiteKey != nil || input.RecaptchaV3Secret != nil ||
		input.RecaptchaV3SiteKey != nil || input.RecaptchaV3ScoreThreshold != nil || input.TurnstileSecret != nil || input.TurnstileSiteKey != nil ||
		input.RegistrationIPLimitEnabled != nil || input.RegistrationIPLimitCount != nil || input.RegistrationIPLimitMinutes != nil ||
		input.PasswordLimitEnabled != nil || input.PasswordLimitCount != nil || input.PasswordLimitMinutes != nil
}

func (input legacyConfigSaveRequest) hasTheme() bool {
	return input.FrontendTheme != nil || input.FrontendSidebarStyle != nil || input.FrontendHeaderStyle != nil ||
		input.FrontendThemeColor != nil || input.FrontendBackgroundURL != nil
}

func (input legacyConfigSaveRequest) hasServer() bool {
	return input.ServerToken != nil || input.PullInterval != nil || input.PushInterval != nil ||
		input.DeviceLimitMode != nil || input.WebSocketEnabled != nil || input.WebSocketURL != nil
}

func (input legacyConfigSaveRequest) hasSubscriptionTemplates() bool {
	return input.SubscriptionTemplateSingbox != nil || input.SubscriptionTemplateClash != nil ||
		input.SubscriptionTemplateClashMeta != nil || input.SubscriptionTemplateStash != nil ||
		input.SubscriptionTemplateSurge != nil || input.SubscriptionTemplateSurfboard != nil
}

func (input legacyConfigSaveRequest) hasSubscribe() bool {
	return input.PlanChangeEnabled != nil || input.ResetTrafficMethod != nil || input.SurplusEnabled != nil ||
		input.NewOrderEventID != nil || input.RenewOrderEventID != nil || input.ChangeOrderEventID != nil ||
		input.ShowInfo != nil || input.ShowProtocol != nil || input.DefaultRemindExpire != nil ||
		input.DefaultRemindTraffic != nil || input.Path != nil
}

func (input legacyConfigSaveRequest) hasEmail() bool {
	return input.EmailHost != nil || input.EmailPort != nil || input.EmailUsername != nil || input.EmailPassword != nil ||
		input.EmailEncryption != nil || input.EmailFromAddress != nil || input.RemindMailEnabled != nil
}

func (input legacyConfigSaveRequest) hasTelegram() bool {
	return input.TelegramBotEnabled != nil || input.TelegramBotToken != nil || input.TelegramWebhookURL != nil || input.TelegramDiscussLink != nil
}

func (input legacyConfigSaveRequest) hasClientApp() bool {
	return input.WindowsVersion != nil || input.WindowsDownloadURL != nil ||
		input.MacOSVersion != nil || input.MacOSDownloadURL != nil ||
		input.AndroidVersion != nil || input.AndroidDownloadURL != nil
}

func (input commissionSettingsRequest) complete() bool {
	return input.InviteCommission != nil && input.FirstTimeEnabled != nil && input.AutoCheckEnabled != nil &&
		input.WithdrawClosed != nil && input.DistributionEnabled != nil && input.DistributionL1 != nil &&
		input.DistributionL2 != nil && input.DistributionL3 != nil
}

func (input commissionSettingsRequest) storeInput() store.SaveCommissionSettingsInput {
	return store.SaveCommissionSettingsInput{
		InviteCommission: *input.InviteCommission, FirstTimeEnabled: *input.FirstTimeEnabled,
		AutoCheckEnabled: *input.AutoCheckEnabled, WithdrawClosed: *input.WithdrawClosed,
		WithdrawLimit: input.WithdrawLimit, WithdrawMethods: input.WithdrawMethods,
		DistributionEnabled: *input.DistributionEnabled, DistributionL1: *input.DistributionL1,
		DistributionL2: *input.DistributionL2, DistributionL3: *input.DistributionL3,
	}
}

func (s *server) getCommissionSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetCommissionSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, settings)
}

func (s *server) updateCommissionSettings(w http.ResponseWriter, r *http.Request) {
	var input commissionSettingsRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Revision == nil || !input.complete() {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请完整填写佣金设置", nil)
		return
	}
	session, _ := sessionFromContext(r.Context())
	updated, err := s.store.UpdateCommissionSettings(r.Context(), session.UserID, *input.Revision, input.storeInput(), s.now())
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, http.StatusConflict, "settings_conflict", "设置已被其他管理员修改，请刷新后重试", nil)
		return
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, updated)
}

func (s *server) legacyFetchConfigSettings(w http.ResponseWriter, r *http.Request) {
	keyValues, keyPresent := r.URL.Query()["key"]
	if keyPresent {
		if len(keyValues) != 1 || strings.TrimSpace(keyValues[0]) == "" {
			writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "不支持的配置组")
			return
		}
		key := strings.TrimSpace(keyValues[0])
		group, err := s.readLegacyConfigGroup(r.Context(), key)
		if errors.Is(err, errUnsupportedLegacyConfigGroup) {
			writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "不支持的配置组")
			return
		}
		if err != nil {
			writeLegacyInviteFailure(w, http.StatusInternalServerError, "配置读取失败")
			return
		}
		writeLegacySuccess(w, http.StatusOK, map[string]any{key: group})
		return
	}
	result, err := loadLegacyConfigGroups(r.Context(), legacyConfigGroupNames[:], s.readLegacyConfigGroup)
	if err != nil {
		writeLegacyInviteFailure(w, http.StatusInternalServerError, "配置读取失败")
		return
	}
	writeLegacySuccess(w, http.StatusOK, result)
}

var errUnsupportedLegacyConfigGroup = errors.New("unsupported legacy config group")

var legacyConfigGroupNames = [...]string{
	"invite", "site", "subscribe", "frontend", "server", "email", "telegram", "app", "safe", "subscribe_template",
}

const legacyConfigFetchConcurrency = 4

type legacyConfigGroupReader func(context.Context, string) (map[string]any, error)

func loadLegacyConfigGroups(ctx context.Context, names []string, read legacyConfigGroupReader) (map[string]any, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	values := make([]map[string]any, len(names))
	jobs := make(chan int)
	workerCount := min(legacyConfigFetchConcurrency, len(names))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	var firstErr error
	var errorOnce sync.Once
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range jobs {
				value, err := read(ctx, names[index])
				if err != nil {
					errorOnce.Do(func() {
						firstErr = err
						cancel()
					})
					continue
				}
				values[index] = value
			}
		}()
	}
dispatch:
	for index := range names {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	workers.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make(map[string]any, len(names))
	for index, name := range names {
		result[name] = values[index]
	}
	return result, nil
}

func (s *server) readLegacyConfigGroup(ctx context.Context, key string) (map[string]any, error) {
	switch key {
	case "invite":
		settings, err := s.store.GetLegacyInvitationSettings(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"invite_force": settings.InvitationForce, "invite_commission": settings.InviteCommission,
			"invite_gen_limit": settings.InvitationCodeLimit, "invite_never_expire": settings.InvitationNeverExpire,
			"commission_first_time_enable": settings.FirstTimeEnabled, "commission_auto_check_enable": settings.AutoCheckEnabled,
			"commission_withdraw_limit": settings.WithdrawLimit, "commission_withdraw_method": append([]string{}, settings.WithdrawMethods...),
			"withdraw_close_enable": settings.WithdrawClosed, "commission_distribution_enable": settings.DistributionEnabled,
			"commission_distribution_l1": settings.DistributionL1, "commission_distribution_l2": settings.DistributionL2,
			"commission_distribution_l3": settings.DistributionL3,
		}, nil
	case "site":
		settings, err := s.store.GetLegacySiteConfig(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"logo": settings.Logo, "force_https": boolToInt(settings.ForceHTTPS), "stop_register": boolToInt(settings.StopRegister),
			"app_name": settings.AppName, "app_description": settings.AppDescription, "app_url": settings.AppURL,
			"subscribe_url": settings.SubscribeURL, "try_out_plan_id": settings.TrialPlanID, "try_out_hour": settings.TrialHours,
			"tos_url": settings.TOSURL, "currency": settings.Currency, "currency_symbol": settings.CurrencySymbol,
			"ticket_must_wait_reply": settings.TicketMustWaitReply,
		}, nil
	case "subscribe":
		settings, err := s.store.GetLegacyAdminSubscriptionConfig(ctx)
		if err != nil {
			return nil, err
		}
		return legacySubscriptionConfigMap(settings), nil
	case "frontend":
		settings, err := s.store.GetLegacyFrontendSettings(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"frontend_theme": settings.Theme, "frontend_theme_sidebar": settings.SidebarStyle,
			"frontend_theme_header": settings.HeaderStyle, "frontend_theme_color": settings.ThemeColor,
			"frontend_background_url": settings.BackgroundURL,
		}, nil
	case "server":
		settings, err := s.store.GetNodeAgentSettings(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"server_token": "", "server_pull_interval": settings.PullInterval, "server_push_interval": settings.PushInterval,
			"device_limit_mode": settings.DeviceLimitMode, "server_ws_enable": settings.WebSocketEnabled, "server_ws_url": settings.WebSocketURL,
			"server_token_configured": settings.ServerTokenConfigured,
		}, nil
	case "email":
		settings, err := s.store.GetMailSettings(ctx)
		if err != nil {
			return nil, err
		}
		return legacyMailSettings(settings), nil
	case "telegram":
		settings, err := s.store.GetTelegramSettings(ctx)
		if err != nil {
			return nil, err
		}
		return legacyTelegramSettings(settings), nil
	case "app":
		settings, err := s.store.GetClientAppSettings(ctx)
		if err != nil {
			return nil, err
		}
		result := make(map[string]any)
		for name, value := range legacyClientAppSettings(settings) {
			result[name] = value
		}
		return result, nil
	case "safe":
		settings, err := s.store.GetSiteSettings(ctx)
		if err != nil {
			return nil, err
		}
		return legacySafeConfigMap(settings), nil
	case "subscribe_template":
		settings, err := s.store.GetSubscriptionSettings(ctx)
		if err != nil {
			return nil, err
		}
		return legacySubscriptionTemplates(settings.Templates), nil
	default:
		return nil, errUnsupportedLegacyConfigGroup
	}
}

func legacySubscriptionConfigMap(settings store.LegacyAdminSubscriptionConfig) map[string]any {
	return map[string]any{
		"plan_change_enable": settings.PlanChangeEnabled, "reset_traffic_method": settings.ResetTrafficMethod,
		"surplus_enable": settings.SurplusEnabled, "new_order_event_id": settings.NewOrderEventID,
		"renew_order_event_id": settings.RenewOrderEventID, "change_order_event_id": settings.ChangeOrderEventID,
		"show_info_to_server_enable": settings.ShowInfo, "show_protocol_to_server_enable": settings.ShowProtocol,
		"default_remind_expire": settings.DefaultRemindExpire, "default_remind_traffic": settings.DefaultRemindTraffic,
		"subscribe_path": settings.Path,
	}
}

func legacySafeConfigMap(settings store.SiteSettings) map[string]any {
	return map[string]any{
		"email_verify": settings.EmailVerificationEnabled, "safe_mode_enable": settings.SafeModeEnabled,
		"secure_path": settings.SecurePath, "email_whitelist_enable": settings.EmailWhitelistEnabled,
		"email_whitelist_suffix":   append([]string(nil), settings.EmailWhitelistSuffixes...),
		"email_gmail_limit_enable": settings.GmailAliasLimitEnabled,
		"captcha_enable":           settings.CaptchaEnabled, "captcha_type": settings.CaptchaType,
		"recaptcha_enable": settings.CaptchaEnabled, "recaptcha_key": "", "recaptcha_site_key": settings.RecaptchaSiteKey,
		"recaptcha_v3_secret_key": "", "recaptcha_v3_site_key": settings.RecaptchaV3SiteKey,
		"recaptcha_v3_score_threshold": settings.RecaptchaV3ScoreThreshold,
		"turnstile_secret_key":         "", "turnstile_site_key": settings.TurnstileSiteKey,
		"register_limit_by_ip_enable": settings.RegistrationIPLimitEnabled,
		"register_limit_count":        settings.RegistrationIPLimitCount, "register_limit_expire": settings.RegistrationIPLimitMinutes,
		"password_limit_enable": settings.PasswordLimitEnabled, "password_limit_count": settings.PasswordLimitCount,
		"password_limit_expire":          settings.PasswordLimitMinutes,
		"recaptcha_secret_configured":    settings.RecaptchaSecretConfigured,
		"recaptcha_v3_secret_configured": settings.RecaptchaV3SecretConfigured,
		"turnstile_secret_configured":    settings.TurnstileSecretConfigured,
	}
}

func legacySubscriptionTemplates(templates map[string]string) map[string]any {
	result := make(map[string]any, len(store.SubscriptionTemplateNames))
	for _, name := range store.SubscriptionTemplateNames {
		content := templates[name]
		if name == "singbox" && strings.TrimSpace(content) != "" {
			var formatted bytes.Buffer
			if err := json.Indent(&formatted, []byte(content), "", "    "); err == nil {
				content = formatted.String()
			}
		}
		result["subscribe_template_"+name] = content
	}
	return result
}

func (s *server) legacySaveConfigSettings(w http.ResponseWriter, r *http.Request) {
	var input legacyConfigSaveRequest
	if !decodeStrictUTF8JSONLimit(w, r, &input, maxSubscriptionSettingsBody) {
		return
	}
	site, safe, invite, subscribe := input.hasSite(), input.hasSafe(), input.hasInvite(), input.hasSubscribe()
	email, telegram, clientApp := input.hasEmail(), input.hasTelegram(), input.hasClientApp()
	themeConfig, serverConfig, templates := input.hasTheme(), input.hasServer(), input.hasSubscriptionTemplates()
	groupCount := 0
	for _, present := range []bool{site, safe, invite, subscribe, email, telegram, clientApp, themeConfig, serverConfig, templates} {
		if present {
			groupCount++
		}
	}
	if groupCount != 1 {
		writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "请提交单一配置组")
		return
	}
	session, _ := sessionFromContext(r.Context())
	if themeConfig {
		_, err := s.store.UpdateLegacyFrontendSettings(r.Context(), session.UserID, store.SaveLegacyFrontendSettingsInput{
			Theme: input.FrontendTheme, SidebarStyle: input.FrontendSidebarStyle, HeaderStyle: input.FrontendHeaderStyle,
			ThemeColor: input.FrontendThemeColor, BackgroundURL: input.FrontendBackgroundURL,
		}, s.now())
		if err != nil {
			status, message := http.StatusInternalServerError, "主题配置保存失败"
			if errors.Is(err, store.ErrNotFound) {
				status, message = http.StatusNotFound, "主题不存在"
			} else if errors.Is(err, store.ErrConflict) {
				status, message = http.StatusConflict, "主题已被其他管理员修改，请重试"
			} else if errors.Is(err, store.ErrInvalidInput) {
				status, message = http.StatusUnprocessableEntity, "主题配置无效"
			}
			writeLegacyInviteFailure(w, status, message)
			return
		}
		writeLegacySuccess(w, http.StatusOK, true)
		return
	}
	if site {
		_, err := s.store.UpdateLegacySiteConfig(r.Context(), session.UserID, store.SaveLegacySiteConfigInput{
			Logo: input.Logo, ForceHTTPS: legacyConfigBoolPointer(input.ForceHTTPS), StopRegister: legacyConfigBoolPointer(input.StopRegister),
			AppName: input.AppName, AppDescription: input.AppDescription, AppURL: input.AppURL,
			SubscribeURL: input.SubscribeURL, TrialPlanID: input.TrialPlanID, TrialHours: input.TrialHours,
			TOSURL: input.TOSURL, Currency: input.Currency, CurrencySymbol: input.CurrencySymbol,
			TicketMustWaitReply: input.TicketMustWaitReply,
		}, s.now())
		if err != nil {
			status, message := http.StatusUnprocessableEntity, "站点配置无效"
			if errors.Is(err, store.ErrConflict) {
				status, message = http.StatusConflict, "配置已被其他管理员修改，请重试"
			} else if !errors.Is(err, store.ErrInvalidInput) {
				status, message = http.StatusInternalServerError, "站点配置保存失败"
			}
			writeLegacyInviteFailure(w, status, message)
			return
		}
		writeLegacySuccess(w, http.StatusOK, true)
		return
	}
	if safe {
		err := s.saveLegacySafeConfig(r, session.UserID, input)
		if err != nil {
			status, message := http.StatusUnprocessableEntity, "安全配置无效"
			if errors.Is(err, store.ErrConflict) {
				status, message = http.StatusConflict, "配置已被其他管理员修改，请重试"
			} else if errors.Is(err, errLegacySettingsEncryptionUnavailable) {
				status, message = http.StatusServiceUnavailable, "服务器未配置设置加密密钥"
			} else if errors.Is(err, store.ErrRegistrationEmailVerificationNeedsMail) {
				status, message = http.StatusConflict, "启用注册邮箱验证前必须先启用 SMTP 邮件服务"
			} else if !errors.Is(err, store.ErrInvalidInput) {
				status, message = http.StatusInternalServerError, "安全配置保存失败"
			}
			writeLegacyInviteFailure(w, status, message)
			return
		}
		writeLegacySuccess(w, http.StatusOK, true)
		return
	}
	if serverConfig {
		current, err := s.store.GetNodeAgentSettings(r.Context())
		if err != nil {
			writeLegacyInviteFailure(w, http.StatusInternalServerError, "服务器配置读取失败")
			return
		}
		next := store.UpdateNodeAgentSettingsInput{
			Revision: current.Revision, PullInterval: current.PullInterval, PushInterval: current.PushInterval,
			DeviceLimitMode: current.DeviceLimitMode, WebSocketEnabled: current.WebSocketEnabled, WebSocketURL: current.WebSocketURL,
			UpdatedBy: &session.UserID,
			Audit: &store.AdminAuditInput{
				AdministratorID: session.UserID, AdministratorEmail: session.Email,
				Method: http.MethodPost, Route: "/api/v2/{secure_admin}/config/save", StatusCode: http.StatusOK,
			},
		}
		if input.ServerToken != nil && strings.TrimSpace(*input.ServerToken) != "" {
			next.ServerToken = input.ServerToken
		}
		if input.PullInterval != nil {
			next.PullInterval = *input.PullInterval
		}
		if input.PushInterval != nil {
			next.PushInterval = *input.PushInterval
		}
		if input.DeviceLimitMode != nil {
			next.DeviceLimitMode = *input.DeviceLimitMode
		}
		if input.WebSocketEnabled != nil {
			next.WebSocketEnabled = *input.WebSocketEnabled
		}
		if input.WebSocketURL != nil {
			next.WebSocketURL = *input.WebSocketURL
		}
		if !current.WebSocketEnabled && next.WebSocketEnabled && !s.webSocketEnabled {
			writeLegacyInviteFailure(w, http.StatusConflict, "当前部署未启用 WebSocket 服务能力")
			return
		}
		updated, err := s.store.UpdateNodeAgentSettings(r.Context(), next, s.now())
		if err != nil {
			status, message := http.StatusInternalServerError, "服务器配置保存失败"
			if errors.Is(err, store.ErrConflict) {
				status, message = http.StatusConflict, "配置已被其他管理员修改，请重试"
			} else if errors.Is(err, store.ErrInvalidInput) {
				status, message = http.StatusUnprocessableEntity, "服务器配置无效"
			}
			writeLegacyInviteFailure(w, status, message)
			return
		}
		markRequestMutationAudited(r)
		if s.hub != nil {
			if current.WebSocketEnabled && !updated.WebSocketEnabled {
				s.hub.DisconnectAll("websocket disabled")
			} else if next.ServerToken != nil {
				s.hub.DisconnectLegacy("server token changed")
			}
		}
		writeLegacySuccess(w, http.StatusOK, true)
		return
	}
	if clientApp {
		_, err := s.store.UpdateLegacyClientAppSettings(r.Context(), session.UserID, store.SaveLegacyClientAppSettingsInput{
			WindowsVersion: input.WindowsVersion, WindowsDownloadURL: input.WindowsDownloadURL,
			MacOSVersion: input.MacOSVersion, MacOSDownloadURL: input.MacOSDownloadURL,
			AndroidVersion: input.AndroidVersion, AndroidDownloadURL: input.AndroidDownloadURL,
		}, s.now())
		if err != nil {
			status, message := http.StatusUnprocessableEntity, "客户端应用配置无效"
			if errors.Is(err, store.ErrConflict) {
				status, message = http.StatusConflict, "配置已被其他管理员修改，请重试"
			} else if !errors.Is(err, store.ErrInvalidInput) {
				status, message = http.StatusInternalServerError, "客户端应用配置保存失败"
			}
			writeLegacyInviteFailure(w, status, message)
			return
		}
		writeLegacySuccess(w, http.StatusOK, true)
		return
	}
	if telegram {
		current, err := s.store.GetTelegramSettings(r.Context())
		if err != nil {
			writeLegacyInviteFailure(w, http.StatusInternalServerError, "Telegram 配置读取失败")
			return
		}
		request := telegramSettingsRequest{
			Revision: current.Revision, BotEnabled: current.BotEnabled,
			WebhookURL: current.WebhookURL, DiscussLink: current.DiscussLink,
		}
		if input.TelegramBotEnabled != nil {
			request.BotEnabled = *input.TelegramBotEnabled
		}
		if input.TelegramBotToken != nil && *input.TelegramBotToken != "" {
			request.BotToken = input.TelegramBotToken
		}
		if input.TelegramWebhookURL != nil {
			request.WebhookURL = *input.TelegramWebhookURL
		}
		if input.TelegramDiscussLink != nil {
			request.DiscussLink = *input.TelegramDiscussLink
		}
		save, err := s.prepareTelegramSettingsSave(request)
		if err != nil {
			writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "Telegram 配置无效")
			return
		}
		if _, err := s.store.UpdateTelegramSettings(r.Context(), session.UserID, current.Revision, save, s.now()); err != nil {
			status, message := http.StatusUnprocessableEntity, "Telegram 配置无效"
			if errors.Is(err, store.ErrConflict) {
				status, message = http.StatusConflict, "配置已被其他管理员修改，请重试"
			} else if !errors.Is(err, store.ErrInvalidInput) {
				status, message = http.StatusInternalServerError, "Telegram 配置保存失败"
			}
			writeLegacyInviteFailure(w, status, message)
			return
		}
		writeLegacySuccess(w, http.StatusOK, true)
		return
	}
	if email {
		current, err := s.store.GetMailSettings(r.Context())
		if err != nil {
			writeLegacyInviteFailure(w, http.StatusInternalServerError, "邮件配置读取失败")
			return
		}
		request := mailSettingsRequest{
			Revision: current.Revision, SMTPEnabled: current.SMTPEnabled, SMTPHost: current.SMTPHost,
			SMTPPort: current.SMTPPort, SMTPUsername: current.SMTPUsername, SMTPEncryption: current.SMTPEncryption,
			SMTPFromAddress: current.SMTPFromAddress, RemindMailEnabled: current.RemindMailEnabled,
		}
		if input.EmailHost != nil {
			request.SMTPHost = *input.EmailHost
			request.SMTPEnabled = strings.TrimSpace(*input.EmailHost) != ""
		}
		if input.EmailPort != nil {
			request.SMTPPort = int(*input.EmailPort)
		}
		if input.EmailUsername != nil {
			request.SMTPUsername = *input.EmailUsername
		}
		if input.EmailPassword != nil && *input.EmailPassword != "" {
			request.SMTPPassword = input.EmailPassword
		}
		if input.EmailEncryption != nil {
			mapped, valid := modernSMTPEncryption(*input.EmailEncryption)
			if !valid {
				writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "邮件加密方式无效")
				return
			}
			request.SMTPEncryption = mapped
		}
		if input.EmailFromAddress != nil {
			request.SMTPFromAddress = *input.EmailFromAddress
		}
		if input.RemindMailEnabled != nil {
			request.RemindMailEnabled = *input.RemindMailEnabled
		}
		save, err := s.prepareMailSettingsSave(request, current)
		if err != nil {
			writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "邮件配置无效")
			return
		}
		if _, err := s.store.UpdateMailSettings(r.Context(), session.UserID, current.Revision, save, s.now()); err != nil {
			status, message := http.StatusUnprocessableEntity, "邮件配置无效"
			if errors.Is(err, store.ErrConflict) {
				status, message = http.StatusConflict, "配置已被其他管理员修改，请重试"
			} else if !errors.Is(err, store.ErrInvalidInput) && !errors.Is(err, store.ErrRegistrationEmailVerificationNeedsMail) && !errors.Is(err, store.ErrMailLoginNeedsMail) {
				status, message = http.StatusInternalServerError, "邮件配置保存失败"
			}
			writeLegacyInviteFailure(w, status, message)
			return
		}
		writeLegacySuccess(w, http.StatusOK, true)
		return
	}
	if templates {
		current, err := s.store.GetSubscriptionSettings(r.Context())
		if err != nil {
			writeLegacyInviteFailure(w, http.StatusInternalServerError, "订阅模板读取失败")
			return
		}
		nextTemplates := make(map[string]string, len(store.SubscriptionTemplateNames))
		for name, content := range current.Templates {
			nextTemplates[name] = content
		}
		for name, content := range map[string]*string{
			"singbox": input.SubscriptionTemplateSingbox, "clash": input.SubscriptionTemplateClash,
			"clashmeta": input.SubscriptionTemplateClashMeta, "stash": input.SubscriptionTemplateStash,
			"surge": input.SubscriptionTemplateSurge, "surfboard": input.SubscriptionTemplateSurfboard,
		} {
			if content != nil {
				nextTemplates[name] = *content
			}
		}
		_, err = s.store.UpdateSubscriptionSettings(r.Context(), session.UserID, current.Revision, store.SaveSubscriptionSettingsInput{
			Path: current.Path, ShowInfo: current.ShowInfo, ShowProtocol: current.ShowProtocol, Templates: nextTemplates,
		}, s.now())
		if err != nil {
			status, message := http.StatusInternalServerError, "订阅模板保存失败"
			if errors.Is(err, store.ErrRevisionConflict) || errors.Is(err, store.ErrConflict) {
				status, message = http.StatusConflict, "配置已被其他管理员修改，请重试"
			} else if errors.Is(err, store.ErrInvalidInput) {
				status, message = http.StatusUnprocessableEntity, "订阅模板无效"
			}
			writeLegacyInviteFailure(w, status, message)
			return
		}
		writeLegacySuccess(w, http.StatusOK, true)
		return
	}
	if subscribe {
		_, err := s.store.UpdateLegacyAdminSubscriptionConfig(r.Context(), session.UserID, store.SaveLegacyAdminSubscriptionConfigInput{
			PlanChangeEnabled: input.PlanChangeEnabled, ResetTrafficMethod: legacyConfigIntPointer(input.ResetTrafficMethod),
			SurplusEnabled: input.SurplusEnabled, NewOrderEventID: legacyConfigIntPointer(input.NewOrderEventID),
			RenewOrderEventID: legacyConfigIntPointer(input.RenewOrderEventID), ChangeOrderEventID: legacyConfigIntPointer(input.ChangeOrderEventID),
			ShowInfo: input.ShowInfo, ShowProtocol: input.ShowProtocol,
			DefaultRemindExpire: input.DefaultRemindExpire, DefaultRemindTraffic: input.DefaultRemindTraffic,
			Path: input.Path,
		}, s.now())
		if err != nil {
			status, message := http.StatusInternalServerError, "订阅配置保存失败"
			if errors.Is(err, store.ErrInvalidInput) {
				status, message = http.StatusUnprocessableEntity, "订阅配置无效"
			}
			writeLegacyInviteFailure(w, status, message)
			return
		}
		writeLegacySuccess(w, http.StatusOK, true)
		return
	}
	if input.InvitationForce != nil && *input.InvitationForce && s.invitationProtector == nil {
		writeLegacyInviteFailure(w, http.StatusServiceUnavailable, "服务器未配置邀请码加密密钥")
		return
	}
	_, err := s.store.UpdateLegacyInvitationSettings(r.Context(), session.UserID, store.SaveLegacyInvitationSettingsInput{
		InvitationForce: input.InvitationForce, InviteCommission: input.InviteCommission,
		InvitationCodeLimit: input.InvitationCodeLimit, InvitationNeverExpire: input.InvitationNeverExpire,
		FirstTimeEnabled: input.FirstTimeEnabled, AutoCheckEnabled: input.AutoCheckEnabled,
		WithdrawLimit: input.WithdrawLimit, WithdrawMethods: input.WithdrawMethods, WithdrawClosed: input.WithdrawClosed,
		DistributionEnabled: input.DistributionEnabled, DistributionL1: input.DistributionL1,
		DistributionL2: input.DistributionL2, DistributionL3: input.DistributionL3,
	}, s.now())
	if err != nil {
		status := http.StatusUnprocessableEntity
		message := "邀请佣金配置无效"
		if errors.Is(err, store.ErrConflict) {
			status = http.StatusConflict
			message = "配置已被其他管理员修改，请重试"
		} else if !errors.Is(err, store.ErrInvalidInput) {
			status = http.StatusInternalServerError
			message = "邀请佣金配置保存失败"
		}
		writeLegacyInviteFailure(w, status, message)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

var errLegacySettingsEncryptionUnavailable = errors.New("legacy settings encryption unavailable")

func (s *server) saveLegacySafeConfig(r *http.Request, administratorID int64, input legacyConfigSaveRequest) error {
	current, err := s.store.GetSiteSettings(r.Context())
	if err != nil {
		return err
	}
	next := siteSettingsInputFromCurrent(current)
	if input.EmailVerificationEnabled != nil {
		next.EmailVerificationEnabled = *input.EmailVerificationEnabled
	}
	if input.SafeModeEnabled != nil {
		next.SafeModeEnabled = input.SafeModeEnabled
	}
	if input.SecurePath != nil {
		next.SecurePath = input.SecurePath
	}
	if input.EmailWhitelistEnabled != nil {
		next.EmailWhitelistEnabled = *input.EmailWhitelistEnabled
	}
	if input.EmailWhitelistSuffixes != nil {
		next.EmailWhitelistSuffixes = append([]string{}, (*input.EmailWhitelistSuffixes)...)
	}
	if input.GmailAliasLimitEnabled != nil {
		next.GmailAliasLimitEnabled = *input.GmailAliasLimitEnabled
	}
	if input.CaptchaEnabled != nil && input.RecaptchaEnabled != nil && *input.CaptchaEnabled != *input.RecaptchaEnabled {
		return errInvalidLegacyConfig
	}
	if input.CaptchaEnabled != nil {
		next.CaptchaEnabled = *input.CaptchaEnabled
	} else if input.RecaptchaEnabled != nil {
		next.CaptchaEnabled = *input.RecaptchaEnabled
	}
	if input.CaptchaType != nil {
		next.CaptchaType = *input.CaptchaType
	}
	if input.RecaptchaSiteKey != nil {
		next.RecaptchaSiteKey = *input.RecaptchaSiteKey
	}
	if input.RecaptchaV3SiteKey != nil {
		next.RecaptchaV3SiteKey = *input.RecaptchaV3SiteKey
	}
	if input.RecaptchaV3ScoreThreshold != nil {
		next.RecaptchaV3ScoreThreshold = *input.RecaptchaV3ScoreThreshold
	}
	if input.TurnstileSiteKey != nil {
		next.TurnstileSiteKey = *input.TurnstileSiteKey
	}
	if input.RegistrationIPLimitEnabled != nil {
		next.RegistrationIPLimitEnabled = *input.RegistrationIPLimitEnabled
	}
	if input.RegistrationIPLimitCount != nil {
		next.RegistrationIPLimitCount = *input.RegistrationIPLimitCount
	}
	if input.RegistrationIPLimitMinutes != nil {
		next.RegistrationIPLimitMinutes = *input.RegistrationIPLimitMinutes
	}
	if input.PasswordLimitEnabled != nil {
		next.PasswordLimitEnabled = *input.PasswordLimitEnabled
	}
	if input.PasswordLimitCount != nil {
		next.PasswordLimitCount = *input.PasswordLimitCount
	}
	if input.PasswordLimitMinutes != nil {
		next.PasswordLimitMinutes = *input.PasswordLimitMinutes
	}
	if next.EmailVerificationEnabled && s.registrationEmailProtector == nil {
		return errLegacySettingsEncryptionUnavailable
	}
	for _, secret := range []struct {
		value      *string
		purpose    appsettings.SecretPurpose
		replace    *bool
		ciphertext *[]byte
	}{
		{input.RecaptchaSecret, appsettings.RecaptchaSecretPurpose, &next.ReplaceRecaptchaSecret, &next.RecaptchaSecretCipher},
		{input.RecaptchaV3Secret, appsettings.RecaptchaV3SecretPurpose, &next.ReplaceRecaptchaV3Secret, &next.RecaptchaV3SecretCipher},
		{input.TurnstileSecret, appsettings.TurnstileSecretPurpose, &next.ReplaceTurnstileSecret, &next.TurnstileSecretCipher},
	} {
		replace, ciphertext, err := s.prepareLegacyConfigSecret(secret.value, secret.purpose)
		if err != nil {
			return err
		}
		if replace {
			*secret.replace = true
			*secret.ciphertext = ciphertext
		}
	}
	_, err = s.store.UpdateSiteSettings(r.Context(), administratorID, current.Revision, next, s.now())
	return err
}

var errInvalidLegacyConfig = fmt.Errorf("%w: conflicting legacy configuration aliases", store.ErrInvalidInput)

func (s *server) prepareLegacyConfigSecret(value *string, purpose appsettings.SecretPurpose) (bool, []byte, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return false, nil, nil
	}
	plaintext := strings.TrimSpace(*value)
	if len(plaintext) > 4<<10 || strings.IndexFunc(plaintext, unicode.IsControl) >= 0 {
		return false, nil, store.ErrInvalidInput
	}
	if s.settingsCipher == nil {
		return false, nil, errLegacySettingsEncryptionUnavailable
	}
	ciphertext, err := s.settingsCipher.EncryptFor(purpose, []byte(plaintext))
	if err != nil {
		return false, nil, fmt.Errorf("encrypt legacy configuration secret: %w", err)
	}
	return true, ciphertext, nil
}

func siteSettingsInputFromCurrent(current store.SiteSettings) store.SaveSiteSettingsInput {
	return store.SaveSiteSettingsInput{
		AppName: current.AppName, AppDescription: current.AppDescription, AppURL: current.AppURL,
		SafeModeEnabled: &current.SafeModeEnabled, SecurePath: &current.SecurePath,
		ForceHTTPS: &current.ForceHTTPS, SubscribeURL: &current.SubscribeURL,
		TOSURL: current.TOSURL, Logo: current.Logo, Currency: &current.Currency, CurrencySymbol: &current.CurrencySymbol,
		StopRegister: current.StopRegister, EmailVerificationEnabled: current.EmailVerificationEnabled,
		EmailWhitelistEnabled: current.EmailWhitelistEnabled, EmailWhitelistSuffixes: append([]string{}, current.EmailWhitelistSuffixes...),
		GmailAliasLimitEnabled:     current.GmailAliasLimitEnabled,
		RegistrationIPLimitEnabled: current.RegistrationIPLimitEnabled,
		RegistrationIPLimitCount:   current.RegistrationIPLimitCount, RegistrationIPLimitMinutes: current.RegistrationIPLimitMinutes,
		PasswordLimitEnabled: current.PasswordLimitEnabled, PasswordLimitCount: current.PasswordLimitCount,
		PasswordLimitMinutes:   current.PasswordLimitMinutes,
		InvitationForceEnabled: current.InvitationForceEnabled, InvitationCodeLimit: current.InvitationCodeLimit,
		InvitationNeverExpire: current.InvitationNeverExpire, MailLoginEnabled: current.MailLoginEnabled,
		TrialPlanID: &current.TrialPlanID, TrialHours: &current.TrialHours, TrafficResetMethod: &current.TrafficResetMethod,
		CouponEnabled: &current.CouponEnabled, CaptchaEnabled: current.CaptchaEnabled, CaptchaType: current.CaptchaType,
		RecaptchaSiteKey: current.RecaptchaSiteKey, RecaptchaV3SiteKey: current.RecaptchaV3SiteKey,
		RecaptchaV3ScoreThreshold: current.RecaptchaV3ScoreThreshold, TurnstileSiteKey: current.TurnstileSiteKey,
	}
}

func (s *server) legacyProvisionTelegramWebhook(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !s.telegramProvisionRequests.take(strconv.FormatInt(session.UserID, 10), s.now()) {
		writeLegacyInviteFailure(w, http.StatusTooManyRequests, "Webhook 设置过于频繁")
		return
	}
	settings, err := s.store.GetTelegramSettings(r.Context())
	if err != nil {
		writeLegacyInviteFailure(w, http.StatusInternalServerError, "Telegram 配置读取失败")
		return
	}
	result, err := s.provisionTelegram(r, session.UserID, settings.Revision)
	if err != nil {
		status, message := http.StatusBadGateway, "Telegram Webhook 设置失败"
		if errors.Is(err, store.ErrConflict) {
			status, message = http.StatusConflict, "配置已被其他管理员修改，请重试"
		} else if errors.Is(err, store.ErrInvalidInput) || errors.Is(err, errTelegramWebhookURLInvalid) || errors.Is(err, errTelegramNotConfigured) {
			status, message = http.StatusUnprocessableEntity, "Telegram 配置无效"
		} else if errors.Is(err, errTelegramEncryptionUnavailable) {
			status, message = http.StatusServiceUnavailable, "Telegram 配置加密不可用"
		}
		writeLegacyInviteFailure(w, status, message)
		return
	}
	writeLegacySuccess(w, http.StatusOK, map[string]any{
		"success": true, "webhook_url": result.WebhookURL, "webhook_base_url": result.WebhookBaseURL,
	})
}

func legacyTelegramSettings(settings store.TelegramSettings) map[string]any {
	return map[string]any{
		"telegram_bot_enable": settings.BotEnabled, "telegram_bot_token": "",
		"telegram_webhook_url": settings.WebhookURL, "telegram_discuss_link": settings.DiscussLink,
	}
}

func (s *server) legacyTestSendMail(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !s.smtpTestRequests.take(strconv.FormatInt(session.UserID, 10), s.now()) {
		writeLegacyInviteFailure(w, http.StatusTooManyRequests, "测试邮件发送过于频繁")
		return
	}
	if err := s.sendTestMail(r.Context(), session.Email); err != nil {
		status, message := http.StatusBadGateway, "测试邮件发送失败"
		if errors.Is(err, errSMTPTestNotConfigured) {
			status, message = http.StatusConflict, "请先保存并启用 SMTP 邮件服务"
		} else if errors.Is(err, errSMTPTestUnavailable) {
			status, message = http.StatusServiceUnavailable, "测试邮件服务暂时不可用"
		}
		writeLegacyInviteFailure(w, status, message)
		return
	}
	writeLegacySuccess(w, http.StatusOK, map[string]any{
		"email": session.Email, "subject": "This is xboard test email", "error": nil,
	})
}

func legacyCommissionSettings(settings store.CommissionSettings) map[string]any {
	return map[string]any{
		"invite_commission":              settings.InviteCommission,
		"commission_first_time_enable":   settings.FirstTimeEnabled,
		"commission_auto_check_enable":   settings.AutoCheckEnabled,
		"commission_withdraw_limit":      settings.WithdrawLimit,
		"commission_withdraw_method":     append([]string{}, settings.WithdrawMethods...),
		"withdraw_close_enable":          settings.WithdrawClosed,
		"commission_distribution_enable": settings.DistributionEnabled,
		"commission_distribution_l1":     settings.DistributionL1,
		"commission_distribution_l2":     settings.DistributionL2,
		"commission_distribution_l3":     settings.DistributionL3,
	}
}
