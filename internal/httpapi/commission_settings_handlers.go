package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

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

func legacyConfigIntPointer(value *legacyConfigInt) *int {
	if value == nil {
		return nil
	}
	result := int(*value)
	return &result
}

type commissionSettingsRequest struct {
	Revision            *int64 `json:"revision,omitempty"`
	InviteCommission    *int   `json:"invite_commission"`
	FirstTimeEnabled    *bool  `json:"commission_first_time_enable"`
	AutoCheckEnabled    *bool  `json:"commission_auto_check_enable"`
	WithdrawClosed      *bool  `json:"withdraw_close_enable"`
	DistributionEnabled *bool  `json:"commission_distribution_enable"`
	DistributionL1      *int   `json:"commission_distribution_l1"`
	DistributionL2      *int   `json:"commission_distribution_l2"`
	DistributionL3      *int   `json:"commission_distribution_l3"`
}

type legacyConfigSaveRequest struct {
	Currency       *string `json:"currency"`
	CurrencySymbol *string `json:"currency_symbol"`
	FrontendTheme  *string `json:"frontend_theme"`

	InviteCommission    *int  `json:"invite_commission"`
	FirstTimeEnabled    *bool `json:"commission_first_time_enable"`
	AutoCheckEnabled    *bool `json:"commission_auto_check_enable"`
	WithdrawClosed      *bool `json:"withdraw_close_enable"`
	DistributionEnabled *bool `json:"commission_distribution_enable"`
	DistributionL1      *int  `json:"commission_distribution_l1"`
	DistributionL2      *int  `json:"commission_distribution_l2"`
	DistributionL3      *int  `json:"commission_distribution_l3"`

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
}

func (input legacyConfigSaveRequest) hasInvite() bool {
	return input.InviteCommission != nil || input.FirstTimeEnabled != nil || input.AutoCheckEnabled != nil ||
		input.WithdrawClosed != nil || input.DistributionEnabled != nil || input.DistributionL1 != nil ||
		input.DistributionL2 != nil || input.DistributionL3 != nil
}

func (input legacyConfigSaveRequest) hasSite() bool {
	return input.Currency != nil || input.CurrencySymbol != nil
}

func (input legacyConfigSaveRequest) hasTheme() bool {
	return input.FrontendTheme != nil
}

func (input legacyConfigSaveRequest) completeInvite() bool {
	return input.InviteCommission != nil && input.FirstTimeEnabled != nil && input.AutoCheckEnabled != nil &&
		input.WithdrawClosed != nil && input.DistributionEnabled != nil && input.DistributionL1 != nil &&
		input.DistributionL2 != nil && input.DistributionL3 != nil
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
	switch strings.TrimSpace(r.URL.Query().Get("key")) {
	case "invite":
		settings, err := s.store.GetCommissionSettings(r.Context())
		if err != nil {
			writeLegacyInviteFailure(w, http.StatusInternalServerError, "邀请佣金配置读取失败")
			return
		}
		writeLegacySuccess(w, http.StatusOK, map[string]any{"invite": legacyCommissionSettings(settings)})
	case "site":
		settings, err := s.store.GetSiteSettings(r.Context())
		if err != nil {
			writeLegacyInviteFailure(w, http.StatusInternalServerError, "站点配置读取失败")
			return
		}
		writeLegacySuccess(w, http.StatusOK, map[string]any{"site": map[string]string{
			"currency": settings.Currency, "currency_symbol": settings.CurrencySymbol,
		}})
	case "subscribe":
		settings, err := s.store.GetLegacyAdminSubscriptionConfig(r.Context())
		if err != nil {
			writeLegacyInviteFailure(w, http.StatusInternalServerError, "订阅配置读取失败")
			return
		}
		writeLegacySuccess(w, http.StatusOK, map[string]any{"subscribe": settings})
	case "email":
		settings, err := s.store.GetMailSettings(r.Context())
		if err != nil {
			writeLegacyInviteFailure(w, http.StatusInternalServerError, "邮件配置读取失败")
			return
		}
		writeLegacySuccess(w, http.StatusOK, map[string]any{"email": legacyMailSettings(settings)})
	case "telegram":
		settings, err := s.store.GetTelegramSettings(r.Context())
		if err != nil {
			writeLegacyInviteFailure(w, http.StatusInternalServerError, "Telegram 配置读取失败")
			return
		}
		writeLegacySuccess(w, http.StatusOK, map[string]any{"telegram": legacyTelegramSettings(settings)})
	case "app":
		settings, err := s.store.GetClientAppSettings(r.Context())
		if err != nil {
			writeLegacyInviteFailure(w, http.StatusInternalServerError, "客户端应用配置读取失败")
			return
		}
		writeLegacySuccess(w, http.StatusOK, map[string]any{"app": legacyClientAppSettings(settings)})
	default:
		writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "不支持的配置组")
	}
}

func (s *server) legacySaveConfigSettings(w http.ResponseWriter, r *http.Request) {
	var input legacyConfigSaveRequest
	if !decodeStrictUTF8JSON(w, r, &input) {
		return
	}
	site, invite, subscribe, email, telegram, clientApp, themeConfig := input.hasSite(), input.hasInvite(), input.hasSubscribe(), input.hasEmail(), input.hasTelegram(), input.hasClientApp(), input.hasTheme()
	groupCount := 0
	for _, present := range []bool{site, invite, subscribe, email, telegram, clientApp, themeConfig} {
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
		catalog, err := s.store.ListThemes(r.Context())
		if err == nil {
			_, err = s.store.ActivateTheme(r.Context(), session.UserID, *input.FrontendTheme, catalog.Revision, s.now())
		}
		if err != nil {
			status, message := http.StatusInternalServerError, "主题激活失败"
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
		_, err := s.store.UpdateLegacySiteSettings(r.Context(), session.UserID, store.SaveLegacySiteSettingsInput{
			Currency: input.Currency, CurrencySymbol: input.CurrencySymbol,
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
	if !input.completeInvite() {
		writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "请完整填写邀请佣金配置")
		return
	}
	commissionInput := commissionSettingsRequest{
		InviteCommission: input.InviteCommission, FirstTimeEnabled: input.FirstTimeEnabled,
		AutoCheckEnabled: input.AutoCheckEnabled, WithdrawClosed: input.WithdrawClosed,
		DistributionEnabled: input.DistributionEnabled, DistributionL1: input.DistributionL1,
		DistributionL2: input.DistributionL2, DistributionL3: input.DistributionL3,
	}
	current, err := s.store.GetCommissionSettings(r.Context())
	if err != nil {
		writeLegacyInviteFailure(w, http.StatusInternalServerError, "邀请佣金配置读取失败")
		return
	}
	if _, err := s.store.UpdateCommissionSettings(r.Context(), session.UserID, current.Revision, commissionInput.storeInput(), s.now()); err != nil {
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
		"withdraw_close_enable":          settings.WithdrawClosed,
		"commission_distribution_enable": settings.DistributionEnabled,
		"commission_distribution_l1":     settings.DistributionL1,
		"commission_distribution_l2":     settings.DistributionL2,
		"commission_distribution_l3":     settings.DistributionL3,
	}
}
