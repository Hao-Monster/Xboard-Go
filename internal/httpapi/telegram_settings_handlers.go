package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/Hao-Monster/Xboard-Go/internal/telegrambot"
)

const telegramWebhookPath = "/api/v1/guest/telegram/webhook"

type telegramSettingsRequest struct {
	Revision      int64   `json:"revision"`
	BotEnabled    bool    `json:"telegram_bot_enable"`
	BotToken      *string `json:"telegram_bot_token"`
	ClearBotToken bool    `json:"clear_telegram_bot_token"`
	WebhookURL    string  `json:"telegram_webhook_url"`
	DiscussLink   string  `json:"telegram_discuss_link"`
}

type telegramProvisionRequest struct {
	Revision int64 `json:"revision"`
}

type telegramProvisionResponse struct {
	Settings       store.TelegramSettings `json:"settings"`
	WebhookURL     string                 `json:"webhook_url"`
	WebhookBaseURL string                 `json:"webhook_base_url"`
}

func (s *server) getTelegramSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetTelegramSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, settings)
}

func (s *server) updateTelegramSettings(w http.ResponseWriter, r *http.Request) {
	var input telegramSettingsRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	save, err := s.prepareTelegramSettingsSave(input)
	if err != nil {
		s.writeTelegramSettingsError(w, err)
		return
	}
	session, _ := sessionFromContext(r.Context())
	updated, err := s.store.UpdateTelegramSettings(r.Context(), session.UserID, input.Revision, save, s.now())
	if err != nil {
		s.writeTelegramSettingsError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, updated)
}

func (s *server) prepareTelegramSettingsSave(input telegramSettingsRequest) (store.SaveTelegramSettingsInput, error) {
	save := store.SaveTelegramSettingsInput{
		BotEnabled: input.BotEnabled, WebhookURL: input.WebhookURL, DiscussLink: input.DiscussLink,
	}
	if input.ClearBotToken {
		if input.BotToken != nil && strings.TrimSpace(*input.BotToken) != "" {
			return store.SaveTelegramSettingsInput{}, store.ErrInvalidInput
		}
		save.BotEnabled = false
		save.ReplaceBotToken = true
		return save, nil
	}
	if input.BotToken == nil || *input.BotToken == "" {
		return save, nil
	}
	token := []byte(strings.TrimSpace(*input.BotToken))
	defer zeroBytes(token)
	if !utf8.Valid(token) || !telegrambot.ValidBotToken(token) {
		return store.SaveTelegramSettingsInput{}, store.ErrInvalidInput
	}
	if s.settingsCipher == nil {
		return store.SaveTelegramSettingsInput{}, errTelegramEncryptionUnavailable
	}
	ciphertext, err := s.settingsCipher.EncryptFor(appsettings.TelegramBotTokenPurpose, token)
	if err != nil {
		return store.SaveTelegramSettingsInput{}, errTelegramEncryptionUnavailable
	}
	save.ReplaceBotToken = true
	save.BotTokenCipher = ciphertext
	return save, nil
}

var (
	errTelegramEncryptionUnavailable = errors.New("Telegram settings encryption is unavailable")
	errTelegramNotConfigured         = errors.New("Telegram bot is not configured")
	errTelegramWebhookURLInvalid     = errors.New("Telegram webhook URL is invalid")
	errTelegramUpstream              = errors.New("Telegram upstream operation failed")
)

func (s *server) writeTelegramSettingsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrConflict):
		writeAPIError(w, http.StatusConflict, "settings_conflict", "设置已被其他管理员修改，请刷新后重试", nil)
	case errors.Is(err, errTelegramEncryptionUnavailable):
		writeAPIError(w, http.StatusServiceUnavailable, "settings_encryption_unavailable", "服务器未配置 Telegram 设置加密密钥", nil)
	case errors.Is(err, errTelegramNotConfigured):
		writeAPIError(w, http.StatusConflict, "telegram_not_configured", "请先保存并启用 Telegram Bot", nil)
	case errors.Is(err, errTelegramWebhookURLInvalid):
		writeAPIError(w, http.StatusUnprocessableEntity, "telegram_webhook_url_invalid", "Webhook Base URL 必须是无凭据、查询参数和片段的 HTTPS 地址", nil)
	case errors.Is(err, errTelegramUpstream), errors.Is(err, telegrambot.ErrRejected), errors.Is(err, telegrambot.ErrUnavailable):
		writeAPIError(w, http.StatusBadGateway, "telegram_webhook_failed", "Telegram Webhook 设置失败，请检查 Bot Token 与网络连接", nil)
	case errors.Is(err, store.ErrInvalidInput), errors.Is(err, telegrambot.ErrInvalid):
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "Telegram 设置无效", nil)
	default:
		handleStoreError(w, err)
	}
}

func (s *server) provisionTelegramWebhook(w http.ResponseWriter, r *http.Request) {
	var input telegramProvisionRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if !s.telegramProvisionRequests.take(strconv.FormatInt(session.UserID, 10), s.now()) {
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "Webhook 设置过于频繁，请稍后重试", nil)
		return
	}
	result, err := s.provisionTelegram(r, session.UserID, input.Revision)
	if err != nil {
		s.writeTelegramSettingsError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

func (s *server) provisionTelegram(r *http.Request, administratorID, revision int64) (telegramProvisionResponse, error) {
	settings, err := s.store.GetTelegramSettings(r.Context())
	if err != nil {
		return telegramProvisionResponse{}, err
	}
	if settings.Revision != revision {
		return telegramProvisionResponse{}, store.ErrConflict
	}
	if !settings.BotEnabled || !settings.BotTokenSet || s.settingsCipher == nil || s.telegramBot == nil {
		return telegramProvisionResponse{}, errTelegramNotConfigured
	}
	baseURL := settings.WebhookURL
	if baseURL == "" {
		siteSettings, err := s.store.GetSiteSettings(r.Context())
		if err != nil {
			return telegramProvisionResponse{}, err
		}
		baseURL = siteSettings.AppURL
	}
	baseURL, ok := normalizeTelegramWebhookBaseURL(baseURL)
	if !ok {
		return telegramProvisionResponse{}, errTelegramWebhookURLInvalid
	}
	secrets, err := s.store.GetTelegramSecretCiphers(r.Context())
	if err != nil {
		return telegramProvisionResponse{}, err
	}
	botToken, err := s.settingsCipher.DecryptFor(appsettings.TelegramBotTokenPurpose, secrets.BotToken)
	if err != nil || !telegrambot.ValidBotToken(botToken) {
		zeroBytes(botToken)
		return telegramProvisionResponse{}, errTelegramEncryptionUnavailable
	}
	defer zeroBytes(botToken)
	identity, err := s.telegramBot.GetMe(r.Context(), botToken)
	if err != nil {
		return telegramProvisionResponse{}, err
	}
	provisionBytes := make([]byte, 16)
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(provisionBytes); err != nil {
		return telegramProvisionResponse{}, errTelegramUpstream
	}
	if _, err := rand.Read(secretBytes); err != nil {
		zeroBytes(provisionBytes)
		return telegramProvisionResponse{}, errTelegramUpstream
	}
	provisionID := hex.EncodeToString(provisionBytes)
	zeroBytes(provisionBytes)
	webhookSecret := []byte(base64.RawURLEncoding.EncodeToString(secretBytes))
	zeroBytes(secretBytes)
	defer zeroBytes(webhookSecret)
	secretCipher, err := s.settingsCipher.EncryptFor(appsettings.TelegramWebhookPurpose, webhookSecret)
	if err != nil {
		return telegramProvisionResponse{}, errTelegramEncryptionUnavailable
	}
	provisionSecrets, err := s.store.BeginTelegramWebhookProvision(r.Context(), administratorID, revision, provisionID, secretCipher, s.now())
	if err != nil {
		return telegramProvisionResponse{}, err
	}
	provisionSecret, err := s.settingsCipher.DecryptFor(appsettings.TelegramWebhookPurpose, provisionSecrets.PendingWebhookSecret)
	if err != nil {
		zeroBytes(provisionSecret)
		return telegramProvisionResponse{}, errTelegramEncryptionUnavailable
	}
	defer zeroBytes(provisionSecret)
	webhookURL := baseURL + telegramWebhookPath
	if err := s.telegramBot.SetWebhook(r.Context(), botToken, webhookURL, provisionSecret); err != nil {
		return telegramProvisionResponse{}, err
	}
	completionContext, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	completed, err := s.store.CompleteTelegramWebhookProvision(completionContext, provisionSecrets.ProvisionID, identity.Username, s.now())
	if err != nil {
		return telegramProvisionResponse{}, err
	}
	return telegramProvisionResponse{Settings: completed, WebhookURL: webhookURL, WebhookBaseURL: baseURL}, nil
}

func normalizeTelegramWebhookBaseURL(value string) (string, bool) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	parsed, err := url.Parse(value)
	if err != nil || len(value) == 0 || len(value) > 2_048 || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", false
	}
	return value, true
}

func (s *server) telegramWebhook(w http.ResponseWriter, r *http.Request) {
	secrets, err := s.store.GetTelegramSecretCiphers(r.Context())
	if err != nil || !secrets.BotEnabled || (len(secrets.WebhookSecret) == 0 && len(secrets.PendingWebhookSecret) == 0) || len(secrets.BotToken) == 0 || s.settingsCipher == nil || s.telegramBot == nil {
		writeAPIError(w, http.StatusUnauthorized, "telegram_webhook_unauthorized", "Webhook 凭据无效", nil)
		return
	}
	providedSecret := []byte(r.Header.Get("X-Telegram-Bot-Api-Secret-Token"))
	if len(providedSecret) < 1 || len(providedSecret) > 256 {
		writeAPIError(w, http.StatusUnauthorized, "telegram_webhook_unauthorized", "Webhook 凭据无效", nil)
		return
	}
	matched := 0
	for _, ciphertext := range [][]byte{secrets.WebhookSecret, secrets.PendingWebhookSecret} {
		if len(ciphertext) == 0 {
			continue
		}
		expectedSecret, decryptErr := s.settingsCipher.DecryptFor(appsettings.TelegramWebhookPurpose, ciphertext)
		if decryptErr == nil && len(expectedSecret) == len(providedSecret) {
			matched |= subtle.ConstantTimeCompare(expectedSecret, providedSecret)
		}
		zeroBytes(expectedSecret)
	}
	zeroBytes(providedSecret)
	if matched != 1 {
		writeAPIError(w, http.StatusUnauthorized, "telegram_webhook_unauthorized", "Webhook 凭据无效", nil)
		return
	}
	var update struct {
		UpdateID        int64 `json:"update_id"`
		ChatJoinRequest *struct {
			Chat struct {
				ID int64 `json:"id"`
			} `json:"chat"`
			From struct {
				ID int64 `json:"id"`
			} `json:"from"`
		} `json:"chat_join_request"`
	}
	if !decodeJSONLimitAllowUnknown(w, r, &update, maxJSONBody) {
		return
	}
	if update.ChatJoinRequest == nil {
		writeSuccess(w, http.StatusOK, true)
		return
	}
	chatID, userID := update.ChatJoinRequest.Chat.ID, update.ChatJoinRequest.From.ID
	if update.UpdateID < 1 || chatID == 0 || userID < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "Telegram 更新格式无效", nil)
		return
	}
	claimBytes := make([]byte, 16)
	if _, err := rand.Read(claimBytes); err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "telegram_unavailable", "Telegram 服务暂时不可用", nil)
		return
	}
	claimID := hex.EncodeToString(claimBytes)
	zeroBytes(claimBytes)
	claimState, err := s.store.ClaimTelegramWebhookUpdate(r.Context(), update.UpdateID, claimID, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if claimState == store.TelegramWebhookClaimCompleted {
		writeSuccess(w, http.StatusOK, true)
		return
	}
	if claimState == store.TelegramWebhookClaimInProgress {
		writeAPIError(w, http.StatusServiceUnavailable, "telegram_update_in_progress", "Telegram 更新正在处理", nil)
		return
	}
	releaseClaim := true
	defer func() {
		if releaseClaim {
			cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
			defer cancel()
			_ = s.store.ReleaseTelegramWebhookUpdate(cleanupContext, update.UpdateID, claimID)
		}
	}()
	available, err := s.store.TelegramUserAvailable(r.Context(), userID, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	botToken, err := s.settingsCipher.DecryptFor(appsettings.TelegramBotTokenPurpose, secrets.BotToken)
	if err != nil || !telegrambot.ValidBotToken(botToken) {
		zeroBytes(botToken)
		writeAPIError(w, http.StatusServiceUnavailable, "telegram_unavailable", "Telegram 服务暂时不可用", nil)
		return
	}
	defer zeroBytes(botToken)
	if available {
		err = s.telegramBot.ApproveChatJoinRequest(r.Context(), botToken, chatID, userID)
	} else {
		err = s.telegramBot.DeclineChatJoinRequest(r.Context(), botToken, chatID, userID)
	}
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "telegram_unavailable", "Telegram 服务暂时不可用", nil)
		return
	}
	releaseClaim = false
	completionContext, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	if err := s.store.CompleteTelegramWebhookUpdate(completionContext, update.UpdateID, claimID, s.now()); err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, true)
}

func (s *server) legacyTelegramBotInfo(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetTelegramSettings(r.Context())
	if err != nil {
		writeLegacyInviteFailure(w, http.StatusInternalServerError, "Telegram Bot 信息读取失败")
		return
	}
	if !settings.BotEnabled || settings.BotUsername == "" {
		writeLegacyInviteFailure(w, http.StatusConflict, "Telegram Bot 尚未完成配置")
		return
	}
	writeLegacySuccess(w, http.StatusOK, map[string]string{"username": settings.BotUsername})
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
