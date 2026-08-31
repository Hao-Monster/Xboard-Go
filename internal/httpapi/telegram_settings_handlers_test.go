package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/Hao-Monster/Xboard-Go/internal/telegrambot"
)

const httpTestTelegramToken = "123456789:abcdefghijklmnopqrstuvwxyzABCDE"

type recordingTelegramBot struct {
	failure            error
	setWebhookFailure  error
	getMeCalls         int
	setMyCommandsCalls int
	commands           []telegrambot.BotCommand
	setWebhookURL      string
	webhookSecret      []byte
	approvedUserID     int64
	declinedUserID     int64
	approveCalls       int
	declineCalls       int
	sendMessageCalls   int
	onSetWebhook       func()
}

func (bot *recordingTelegramBot) SetMyCommands(_ context.Context, token []byte, commands []telegrambot.BotCommand) error {
	if string(token) != httpTestTelegramToken {
		return telegrambot.ErrRejected
	}
	bot.setMyCommandsCalls++
	bot.commands = append([]telegrambot.BotCommand(nil), commands...)
	return bot.failure
}

func (bot *recordingTelegramBot) SendMessage(_ context.Context, token []byte, _ int64, _ string) error {
	if string(token) != httpTestTelegramToken {
		return telegrambot.ErrRejected
	}
	bot.sendMessageCalls++
	return bot.failure
}

func (bot *recordingTelegramBot) GetMe(_ context.Context, token []byte) (telegrambot.BotIdentity, error) {
	bot.getMeCalls++
	if string(token) != httpTestTelegramToken {
		return telegrambot.BotIdentity{}, telegrambot.ErrRejected
	}
	if bot.failure != nil {
		return telegrambot.BotIdentity{}, bot.failure
	}
	return telegrambot.BotIdentity{ID: 123, Username: "xboard_test_bot"}, nil
}

func (bot *recordingTelegramBot) SetWebhook(_ context.Context, token []byte, webhookURL string, secret []byte) error {
	if string(token) != httpTestTelegramToken {
		return telegrambot.ErrRejected
	}
	bot.setWebhookURL = webhookURL
	bot.webhookSecret = append([]byte(nil), secret...)
	if bot.onSetWebhook != nil {
		bot.onSetWebhook()
	}
	if bot.setWebhookFailure != nil {
		return bot.setWebhookFailure
	}
	return bot.failure
}

func (bot *recordingTelegramBot) ApproveChatJoinRequest(_ context.Context, token []byte, _ int64, userID int64) error {
	if string(token) != httpTestTelegramToken {
		return telegrambot.ErrRejected
	}
	bot.approveCalls++
	bot.approvedUserID = userID
	return bot.failure
}

func (bot *recordingTelegramBot) DeclineChatJoinRequest(_ context.Context, token []byte, _ int64, userID int64) error {
	if string(token) != httpTestTelegramToken {
		return telegrambot.ErrRejected
	}
	bot.declineCalls++
	bot.declinedUserID = userID
	return bot.failure
}

func TestAdminTelegramSettingsProvisionAndLegacyCompatibility(t *testing.T) {
	telegramClient := &recordingTelegramBot{}
	api, database := newTestAPIWithTelegram(t, telegramClient)
	administrator := loginAdmin(t, api)

	initial := administrator.request(t, api, http.MethodGet, "/api/v1/admin/telegram-settings", "")
	if initial.Code != http.StatusOK || strings.Contains(initial.Body.String(), "cipher") || strings.Contains(initial.Body.String(), "telegram_bot_token\"") {
		t.Fatalf("initial Telegram settings status=%d body=%s", initial.Code, initial.Body)
	}

	saved := administrator.request(t, api, http.MethodPut, "/api/v1/admin/telegram-settings", `{
		"revision":1,"telegram_bot_enable":true,"telegram_bot_token":"`+httpTestTelegramToken+`",
		"telegram_webhook_url":"https://hooks.example.test/xboard","telegram_discuss_link":"https://t.me/xboard_group"
	}`)
	if saved.Code != http.StatusOK || strings.Contains(saved.Body.String(), httpTestTelegramToken) || strings.Contains(saved.Body.String(), "cipher") {
		t.Fatalf("save Telegram settings status=%d body=%s", saved.Code, saved.Body)
	}
	var savedEnvelope struct {
		Data struct {
			Revision    int64 `json:"revision"`
			BotTokenSet bool  `json:"telegram_bot_token_set"`
		} `json:"data"`
	}
	decodeResponse(t, saved, &savedEnvelope)
	if savedEnvelope.Data.Revision != 2 || !savedEnvelope.Data.BotTokenSet {
		t.Fatalf("saved Telegram settings = %#v", savedEnvelope.Data)
	}
	guestConfig := plainAPIRequest(api, http.MethodGet, "/api/v1/guest/comm/config", "")
	if guestConfig.Code != http.StatusOK || !strings.Contains(guestConfig.Body.String(), `"is_telegram":1`) || !strings.Contains(guestConfig.Body.String(), `"telegram_discuss_link":"https://t.me/xboard_group"`) || strings.Contains(guestConfig.Body.String(), httpTestTelegramToken) {
		t.Fatalf("guest Telegram config status=%d body=%s", guestConfig.Code, guestConfig.Body)
	}

	provisioned := administrator.request(t, api, http.MethodPost, "/api/v1/admin/telegram-settings/webhook", `{"revision":2}`)
	if provisioned.Code != http.StatusOK || telegramClient.getMeCalls != 1 || telegramClient.setMyCommandsCalls != 1 || len(telegramClient.commands) != 5 || telegramClient.setWebhookURL != "https://hooks.example.test/xboard/api/v1/guest/telegram/webhook" || len(telegramClient.webhookSecret) < 32 {
		t.Fatalf("provision status=%d getMe=%d webhook=%q secretLength=%d body=%s", provisioned.Code, telegramClient.getMeCalls, telegramClient.setWebhookURL, len(telegramClient.webhookSecret), provisioned.Body)
	}
	if strings.Contains(provisioned.Body.String(), httpTestTelegramToken) || strings.Contains(provisioned.Body.String(), string(telegramClient.webhookSecret)) || strings.Contains(provisioned.Body.String(), "cipher") {
		t.Fatalf("provision response exposed secret material: %s", provisioned.Body)
	}
	var provisionEnvelope struct {
		Data struct {
			Settings struct {
				Revision    int64  `json:"revision"`
				BotUsername string `json:"telegram_bot_username"`
			} `json:"settings"`
			WebhookURL string `json:"webhook_url"`
		} `json:"data"`
	}
	decodeResponse(t, provisioned, &provisionEnvelope)
	if provisionEnvelope.Data.Settings.Revision != 3 || provisionEnvelope.Data.Settings.BotUsername != "xboard_test_bot" || provisionEnvelope.Data.WebhookURL != telegramClient.setWebhookURL {
		t.Fatalf("provision result = %#v", provisionEnvelope.Data)
	}

	legacyAuthorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	legacy := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=telegram", legacyAuthorization, "")
	if legacy.Code != http.StatusOK || !strings.Contains(legacy.Body.String(), `"telegram_bot_enable":true`) || !strings.Contains(legacy.Body.String(), `"telegram_bot_token":""`) || strings.Contains(legacy.Body.String(), httpTestTelegramToken) {
		t.Fatalf("legacy Telegram settings status=%d body=%s", legacy.Code, legacy.Body)
	}
	botInfo := bearerRequest(api, http.MethodGet, "/api/v1/user/telegram/getBotInfo", legacyAuthorization, "")
	if botInfo.Code != http.StatusOK || !strings.Contains(botInfo.Body.String(), `"username":"xboard_test_bot"`) {
		t.Fatalf("legacy Telegram bot info status=%d body=%s", botInfo.Code, botInfo.Body)
	}
	legacySave := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/save", legacyAuthorization, `{"telegram_bot_token":"","telegram_discuss_link":"https://t.me/updated_group"}`)
	if legacySave.Code != http.StatusOK {
		t.Fatalf("legacy Telegram save status=%d body=%s", legacySave.Code, legacySave.Body)
	}
	legacyProvision := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/setTelegramWebhook", legacyAuthorization, "")
	if legacyProvision.Code != http.StatusOK || strings.Contains(legacyProvision.Body.String(), httpTestTelegramToken) || strings.Contains(legacyProvision.Body.String(), string(telegramClient.webhookSecret)) {
		t.Fatalf("legacy Telegram provision status=%d body=%s", legacyProvision.Code, legacyProvision.Body)
	}

	current, err := database.GetTelegramSettings(t.Context())
	if err != nil || current.Revision != 5 || current.DiscussLink != "https://t.me/updated_group" || telegramClient.getMeCalls != 2 {
		t.Fatalf("current Telegram settings=%#v err=%v", current, err)
	}
}

func TestTelegramAdministratorRoutesEnforceRoleCSRFOriginAndBoundedStrictJSON(t *testing.T) {
	api, database := newTestAPIWithTelegram(t, &recordingTelegramBot{})
	createHTTPTestUser(t, database, "telegram-reader@example.test", "reader-password-123")
	reader := loginAccount(t, api, "telegram-reader@example.test", "reader-password-123")
	expectAPIError(t, reader.request(t, api, http.MethodGet, "/api/v1/admin/telegram-settings", ""), http.StatusForbidden, "forbidden")

	visitor := plainAPIRequest(api, http.MethodGet, "/api/v1/admin/telegram-settings", "")
	expectAPIError(t, visitor, http.StatusUnauthorized, "unauthenticated")
	administrator := loginAdmin(t, api)
	before, err := database.GetTelegramSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	missingCSRFRequest := httptest.NewRequest(http.MethodPut, "/api/v1/admin/telegram-settings", strings.NewReader(`{"revision":1}`))
	missingCSRFRequest.Header.Set("Content-Type", "application/json")
	administrator.addCookies(missingCSRFRequest)
	missingCSRF := httptest.NewRecorder()
	api.ServeHTTP(missingCSRF, missingCSRFRequest)
	expectAPIError(t, missingCSRF, http.StatusForbidden, "csrf_failed")

	crossOriginRequest := httptest.NewRequest(http.MethodPut, "/api/v1/admin/telegram-settings", strings.NewReader(`{"revision":1}`))
	crossOriginRequest.Header.Set("Content-Type", "application/json")
	crossOriginRequest.Header.Set("Origin", "https://attacker.example.test")
	crossOriginRequest.Header.Set("X-CSRF-Token", administrator.csrf)
	administrator.addCookies(crossOriginRequest)
	crossOrigin := httptest.NewRecorder()
	api.ServeHTTP(crossOrigin, crossOriginRequest)
	expectAPIError(t, crossOrigin, http.StatusForbidden, "invalid_origin")

	unknown := administrator.request(t, api, http.MethodPut, "/api/v1/admin/telegram-settings", `{"revision":1,"unknown":true}`)
	expectAPIError(t, unknown, http.StatusBadRequest, "invalid_json")
	oversized := administrator.request(t, api, http.MethodPut, "/api/v1/admin/telegram-settings", `{"revision":1,"telegram_bot_token":"`+strings.Repeat("x", int(maxJSONBody))+`"}`)
	expectAPIError(t, oversized, http.StatusRequestEntityTooLarge, "request_too_large")

	legacyAuthorization := loginLegacyBearer(t, api, "telegram-reader@example.test", "reader-password-123").Authorization
	if response := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=telegram", legacyAuthorization, ""); response.Code != http.StatusForbidden {
		t.Fatalf("ordinary user legacy Telegram fetch status=%d body=%s", response.Code, response.Body)
	}
	if response := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/setTelegramWebhook", legacyAuthorization, ""); response.Code != http.StatusForbidden {
		t.Fatalf("ordinary user legacy Telegram provision status=%d body=%s", response.Code, response.Body)
	}
	after, err := database.GetTelegramSettings(t.Context())
	if err != nil || after != before {
		t.Fatalf("rejected Telegram administrator requests mutated settings: before=%#v after=%#v err=%v", before, after, err)
	}
}

func TestTelegramWebhookAuthenticatesAndApprovesOnlyAvailableUsers(t *testing.T) {
	telegramClient := &recordingTelegramBot{}
	api, database := newTestAPIWithTelegram(t, telegramClient)
	administrator := loginAdmin(t, api)
	save := administrator.request(t, api, http.MethodPut, "/api/v1/admin/telegram-settings", `{
		"revision":1,"telegram_bot_enable":true,"telegram_bot_token":"`+httpTestTelegramToken+`",
		"telegram_webhook_url":"https://panel.example.test","telegram_discuss_link":""
	}`)
	if save.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", save.Code, save.Body)
	}
	if response := administrator.request(t, api, http.MethodPost, "/api/v1/admin/telegram-settings/webhook", `{"revision":2}`); response.Code != http.StatusOK {
		t.Fatalf("provision status=%d body=%s", response.Code, response.Body)
	}

	expiresAt := fixedNow().AddDate(0, 1, 0)
	active, err := database.CreateAdminUser(t.Context(), store.CreateAdminUserInput{
		Email: "telegram-member@example.test", PasswordHash: "hash", TransferEnable: 1024, ExpiredAt: &expiresAt,
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	telegramID := int64(556677)
	if _, _, err := database.UpdateAdminUser(t.Context(), active.ID, store.UpdateAdminUserInput{
		Revision: active.Revision, Email: active.Email, GroupID: active.GroupID, TransferEnable: active.TransferEnable,
		ExpiredAt: active.ExpiredAt, SpeedLimit: active.SpeedLimit, DeviceLimit: active.DeviceLimit, Banned: active.Banned,
		TelegramIDSet: true, TelegramID: &telegramID,
	}, fixedNow()); err != nil {
		t.Fatal(err)
	}

	unauthorized := telegramWebhookRequest(api, "wrong-secret", `{"update_id":1,"chat_join_request":{"chat":{"id":-1001},"from":{"id":556677}}}`)
	if unauthorized.Code != http.StatusUnauthorized || telegramClient.approvedUserID != 0 {
		t.Fatalf("unauthorized webhook status=%d approved=%d body=%s", unauthorized.Code, telegramClient.approvedUserID, unauthorized.Body)
	}
	ordinary := telegramWebhookRequest(api, string(telegramClient.webhookSecret), `{"update_id":2,"message":{"message_id":10,"chat":{"id":556677,"type":"private"},"text":"/start"},"future_update_field":{"version":1}}`)
	if ordinary.Code != http.StatusOK || telegramClient.approvedUserID != 0 || telegramClient.declinedUserID != 0 || telegramClient.sendMessageCalls != 0 {
		t.Fatalf("ordinary webhook status=%d approved=%d declined=%d body=%s", ordinary.Code, telegramClient.approvedUserID, telegramClient.declinedUserID, ordinary.Body)
	}
	queued, claimed, err := database.ClaimTelegramMessage(t.Context(), "http-message-claim", fixedNow(), 30*time.Second)
	if err != nil || !claimed || queued.ChatID != telegramID || !strings.Contains(queued.Text, active.Email) {
		t.Fatalf("queued command response=(%#v,%t,%v)", queued, claimed, err)
	}
	if err := database.CompleteTelegramMessage(t.Context(), queued.ID, "http-message-claim", fixedNow()); err != nil {
		t.Fatal(err)
	}
	duplicateOrdinary := telegramWebhookRequest(api, string(telegramClient.webhookSecret), `{"update_id":2,"message":{"message_id":10,"chat":{"id":556677,"type":"private"},"text":"/unbind"}}`)
	if duplicateOrdinary.Code != http.StatusOK {
		t.Fatalf("duplicate ordinary webhook status=%d body=%s", duplicateOrdinary.Code, duplicateOrdinary.Body)
	}
	if _, claimed, err := database.ClaimTelegramMessage(t.Context(), "http-message-duplicate", fixedNow(), 30*time.Second); err != nil || claimed {
		t.Fatalf("duplicate ordinary response claim=(%t,%v)", claimed, err)
	}
	approved := telegramWebhookRequest(api, string(telegramClient.webhookSecret), `{"update_id":3,"chat_join_request":{"chat":{"id":-1001,"title":"Subscribers","type":"supergroup"},"from":{"id":556677,"is_bot":false,"first_name":"Member"},"date":1700000000,"user_chat_id":556677}}`)
	if approved.Code != http.StatusOK || telegramClient.approvedUserID != 556677 {
		t.Fatalf("approved webhook status=%d approved=%d body=%s", approved.Code, telegramClient.approvedUserID, approved.Body)
	}
	duplicateApproved := telegramWebhookRequest(api, string(telegramClient.webhookSecret), `{"update_id":3,"chat_join_request":{"chat":{"id":-1001},"from":{"id":556677}}}`)
	if duplicateApproved.Code != http.StatusOK || telegramClient.approveCalls != 1 {
		t.Fatalf("duplicate approved webhook status=%d calls=%d body=%s", duplicateApproved.Code, telegramClient.approveCalls, duplicateApproved.Body)
	}
	declined := telegramWebhookRequest(api, string(telegramClient.webhookSecret), `{"update_id":4,"chat_join_request":{"chat":{"id":-1001},"from":{"id":998877}}}`)
	if declined.Code != http.StatusOK || telegramClient.declinedUserID != 998877 {
		t.Fatalf("declined webhook status=%d declined=%d body=%s", declined.Code, telegramClient.declinedUserID, declined.Body)
	}
	telegramClient.failure = telegrambot.ErrUnavailable
	failedDecline := telegramWebhookRequest(api, string(telegramClient.webhookSecret), `{"update_id":5,"chat_join_request":{"chat":{"id":-1001},"from":{"id":887766}}}`)
	if failedDecline.Code != http.StatusBadGateway {
		t.Fatalf("failed decline status=%d body=%s", failedDecline.Code, failedDecline.Body)
	}
	telegramClient.failure = nil
	retriedDecline := telegramWebhookRequest(api, string(telegramClient.webhookSecret), `{"update_id":5,"chat_join_request":{"chat":{"id":-1001},"from":{"id":887766}}}`)
	if retriedDecline.Code != http.StatusOK || telegramClient.declineCalls != 3 {
		t.Fatalf("retried decline status=%d calls=%d body=%s", retriedDecline.Code, telegramClient.declineCalls, retriedDecline.Body)
	}
	duplicateDecline := telegramWebhookRequest(api, string(telegramClient.webhookSecret), `{"update_id":5,"chat_join_request":{"chat":{"id":-1001},"from":{"id":887766}}}`)
	if duplicateDecline.Code != http.StatusOK || telegramClient.declineCalls != 3 {
		t.Fatalf("duplicate decline status=%d calls=%d body=%s", duplicateDecline.Code, telegramClient.declineCalls, duplicateDecline.Body)
	}
	disabled := administrator.request(t, api, http.MethodPut, "/api/v1/admin/telegram-settings", `{
		"revision":3,"telegram_bot_enable":false,"telegram_webhook_url":"https://panel.example.test","telegram_discuss_link":""
	}`)
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable Telegram status=%d body=%s", disabled.Code, disabled.Body)
	}
	disabledWebhook := telegramWebhookRequest(api, string(telegramClient.webhookSecret), `{"update_id":6,"chat_join_request":{"chat":{"id":-1001},"from":{"id":556677}}}`)
	if disabledWebhook.Code != http.StatusUnauthorized {
		t.Fatalf("disabled Telegram webhook status=%d body=%s", disabledWebhook.Code, disabledWebhook.Body)
	}
}

func TestTelegramWebhookProvisionFailureKeepsActiveSecretAndRetriesPendingSecret(t *testing.T) {
	telegramClient := &recordingTelegramBot{}
	api, database := newTestAPIWithTelegram(t, telegramClient)
	administrator := loginAdmin(t, api)
	saved := administrator.request(t, api, http.MethodPut, "/api/v1/admin/telegram-settings", `{
		"revision":1,"telegram_bot_enable":true,"telegram_bot_token":"`+httpTestTelegramToken+`",
		"telegram_webhook_url":"https://panel.example.test","telegram_discuss_link":""
	}`)
	if saved.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", saved.Code, saved.Body)
	}
	first := administrator.request(t, api, http.MethodPost, "/api/v1/admin/telegram-settings/webhook", `{"revision":2}`)
	if first.Code != http.StatusOK {
		t.Fatalf("initial provision status=%d body=%s", first.Code, first.Body)
	}
	activeSecret := append([]byte(nil), telegramClient.webhookSecret...)

	telegramClient.setWebhookFailure = telegrambot.ErrUnavailable
	failed := administrator.request(t, api, http.MethodPost, "/api/v1/admin/telegram-settings/webhook", `{"revision":3}`)
	if failed.Code != http.StatusBadGateway || strings.Contains(failed.Body.String(), string(telegramClient.webhookSecret)) {
		t.Fatalf("failed rotation status=%d body=%s", failed.Code, failed.Body)
	}
	pendingSecret := append([]byte(nil), telegramClient.webhookSecret...)
	if string(pendingSecret) == string(activeSecret) {
		t.Fatal("failed rotation did not create an independent pending secret")
	}
	current, err := database.GetTelegramSettings(t.Context())
	if err != nil || current.Revision != 3 || current.BotUsername != "xboard_test_bot" || current.WebhookConfiguredAt == nil {
		t.Fatalf("failed rotation changed active settings: %#v err=%v", current, err)
	}
	for label, secret := range map[string][]byte{"active": activeSecret, "pending": pendingSecret} {
		response := telegramWebhookRequest(api, string(secret), `{"update_id":7001,"message":{"message_id":1,"chat":{"id":7001,"type":"private"},"text":"/start"}}`)
		if response.Code != http.StatusOK {
			t.Fatalf("%s secret during pending rotation status=%d body=%s", label, response.Code, response.Body)
		}
	}

	telegramClient.setWebhookFailure = nil
	retried := administrator.request(t, api, http.MethodPost, "/api/v1/admin/telegram-settings/webhook", `{"revision":3}`)
	if retried.Code != http.StatusOK || string(telegramClient.webhookSecret) != string(pendingSecret) {
		t.Fatalf("retried rotation status=%d reused=%t body=%s", retried.Code, string(telegramClient.webhookSecret) == string(pendingSecret), retried.Body)
	}
	current, err = database.GetTelegramSettings(t.Context())
	if err != nil || current.Revision != 4 || current.BotUsername != "xboard_test_bot" || current.WebhookConfiguredAt == nil {
		t.Fatalf("retried rotation did not complete: %#v err=%v", current, err)
	}
	if response := telegramWebhookRequest(api, string(activeSecret), `{"update_id":7002,"message":{"message_id":2,"chat":{"id":7002,"type":"private"},"text":"/start"}}`); response.Code != http.StatusUnauthorized {
		t.Fatalf("retired active secret status=%d body=%s", response.Code, response.Body)
	}
	if response := telegramWebhookRequest(api, string(pendingSecret), `{"update_id":7003,"message":{"message_id":3,"chat":{"id":7003,"type":"private"},"text":"/start"}}`); response.Code != http.StatusOK {
		t.Fatalf("promoted pending secret status=%d body=%s", response.Code, response.Body)
	}
}

func TestTelegramWebhookProvisionPersistsSuccessAfterRequestCancellation(t *testing.T) {
	telegramClient := &recordingTelegramBot{}
	api, database := newTestAPIWithTelegram(t, telegramClient)
	administrator := loginAdmin(t, api)
	saved := administrator.request(t, api, http.MethodPut, "/api/v1/admin/telegram-settings", `{
		"revision":1,"telegram_bot_enable":true,"telegram_bot_token":"`+httpTestTelegramToken+`",
		"telegram_webhook_url":"https://panel.example.test","telegram_discuss_link":""
	}`)
	if saved.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", saved.Code, saved.Body)
	}
	requestContext, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	telegramClient.onSetWebhook = cancelRequest
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/telegram-settings/webhook", strings.NewReader(`{"revision":2}`)).WithContext(requestContext)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", administrator.csrf)
	administrator.addCookies(request)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("canceled request provision status=%d body=%s", response.Code, response.Body)
	}
	settings, err := database.GetTelegramSettings(t.Context())
	if err != nil || settings.Revision != 3 || settings.BotUsername != "xboard_test_bot" || settings.WebhookConfiguredAt == nil {
		t.Fatalf("canceled request did not persist upstream success: %#v err=%v", settings, err)
	}
}

func TestAdminTelegramSettingsRejectsInvalidSecretsConflictsAndUpstreamDetails(t *testing.T) {
	telegramClient := &recordingTelegramBot{}
	api, _ := newTestAPIWithTelegram(t, telegramClient)
	administrator := loginAdmin(t, api)
	invalid := administrator.request(t, api, http.MethodPut, "/api/v1/admin/telegram-settings", `{
		"revision":1,"telegram_bot_enable":true,"telegram_bot_token":"bad token",
		"telegram_webhook_url":"http://panel.example.test","telegram_discuss_link":"https://example.test/group"
	}`)
	expectAPIError(t, invalid, http.StatusUnprocessableEntity, "validation_failed")
	if telegramClient.getMeCalls != 0 {
		t.Fatalf("invalid save called Telegram %d times", telegramClient.getMeCalls)
	}

	saved := administrator.request(t, api, http.MethodPut, "/api/v1/admin/telegram-settings", `{
		"revision":1,"telegram_bot_enable":true,"telegram_bot_token":"`+httpTestTelegramToken+`",
		"telegram_webhook_url":"https://panel.example.test","telegram_discuss_link":""
	}`)
	if saved.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", saved.Code, saved.Body)
	}
	stale := administrator.request(t, api, http.MethodPost, "/api/v1/admin/telegram-settings/webhook", `{"revision":1}`)
	expectAPIError(t, stale, http.StatusConflict, "settings_conflict")
	if telegramClient.getMeCalls != 0 {
		t.Fatalf("stale provision called Telegram %d times", telegramClient.getMeCalls)
	}
	telegramClient.failure = errors.Join(telegrambot.ErrUnavailable, errors.New("upstream secret must not leak"))
	failed := administrator.request(t, api, http.MethodPost, "/api/v1/admin/telegram-settings/webhook", `{"revision":2}`)
	expectAPIError(t, failed, http.StatusBadGateway, "telegram_webhook_failed")
	if strings.Contains(failed.Body.String(), "upstream secret") {
		t.Fatalf("upstream details leaked: %s", failed.Body)
	}
}

func telegramWebhookRequest(api http.Handler, secret, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, telegramWebhookPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}
