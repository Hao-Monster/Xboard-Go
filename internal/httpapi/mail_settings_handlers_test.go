package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/mailer"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type recordingMailSettingsSender struct {
	failure        error
	configurations []mailer.SMTPConfig
	messages       []mailer.Message
}

func (sender *recordingMailSettingsSender) Send(_ context.Context, configuration mailer.SMTPConfig, message mailer.Message) error {
	sender.configurations = append(sender.configurations, configuration)
	sender.messages = append(sender.messages, message)
	return sender.failure
}

func TestAdminMailSettingsModernAndLegacySurface(t *testing.T) {
	api, database := newTestAPI(t)
	createHTTPTestUser(t, database, "mail-settings-reader@example.test", "mail-settings-reader-password-123")
	administrator := loginAdmin(t, api)
	ordinary := loginAs(t, api, "mail-settings-reader@example.test", "mail-settings-reader-password-123")

	initial := administrator.request(t, api, http.MethodGet, "/api/v1/admin/admin/mail-settings", "")
	if initial.Code != http.StatusOK {
		t.Fatalf("initial mail settings status=%d body=%s", initial.Code, initial.Body)
	}
	var envelope struct {
		Data struct {
			Revision          int64  `json:"revision"`
			SMTPEnabled       bool   `json:"smtp_enabled"`
			SMTPHost          string `json:"smtp_host"`
			SMTPPasswordSet   bool   `json:"smtp_password_set"`
			RemindMailEnabled bool   `json:"remind_mail_enable"`
		} `json:"data"`
	}
	if err := json.Unmarshal(initial.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode initial mail settings: %v; body=%s", err, initial.Body)
	}
	if envelope.Data.Revision != 1 || envelope.Data.SMTPEnabled || envelope.Data.SMTPHost != "" ||
		envelope.Data.SMTPPasswordSet || envelope.Data.RemindMailEnabled {
		t.Fatalf("initial mail settings=%#v", envelope.Data)
	}
	if strings.Contains(initial.Body.String(), "smtp_password_cipher") || strings.Contains(initial.Body.String(), "smtp_password\"") {
		t.Fatalf("mail settings exposed a secret field: %s", initial.Body)
	}

	forbidden := ordinary.request(t, api, http.MethodGet, "/api/v1/admin/admin/mail-settings", "")
	expectAPIError(t, forbidden, http.StatusForbidden, "forbidden")

	legacyAuthorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	legacy := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=email", legacyAuthorization, "")
	if legacy.Code != http.StatusOK {
		t.Fatalf("legacy mail settings status=%d body=%s", legacy.Code, legacy.Body)
	}
	for _, key := range []string{
		"email_host", "email_port", "email_username", "email_password", "email_encryption",
		"email_from_address", "remind_mail_enable",
	} {
		if !strings.Contains(legacy.Body.String(), `"`+key+`"`) {
			t.Fatalf("legacy mail settings omitted %s: %s", key, legacy.Body)
		}
	}
	if strings.Contains(legacy.Body.String(), "smtp_password_cipher") {
		t.Fatalf("legacy mail settings exposed cipher: %s", legacy.Body)
	}
}

func TestAdminMailSettingsSaveTestAndLegacyCompatibility(t *testing.T) {
	sender := &recordingMailSettingsSender{}
	var logs bytes.Buffer
	api, database := newMailSettingsTestAPI(t, sender, slog.New(slog.NewTextHandler(&logs, nil)))
	administrator := loginAdmin(t, api)

	saved := administrator.request(t, api, http.MethodPut, "/api/v1/admin/admin/mail-settings", `{
		"revision":1,"smtp_enabled":true,"smtp_host":"smtp.example.test","smtp_port":587,
		"smtp_username":"mailer","smtp_password":"secret-smtp-password","smtp_encryption":"starttls",
		"smtp_from_address":"support@example.test","remind_mail_enable":true
	}`)
	if saved.Code != http.StatusOK {
		t.Fatalf("save mail settings status=%d body=%s", saved.Code, saved.Body)
	}
	if strings.Contains(saved.Body.String(), "secret-smtp-password") || strings.Contains(saved.Body.String(), "smtp_password_cipher") {
		t.Fatalf("save response exposed SMTP password: %s", saved.Body)
	}
	var savedEnvelope struct {
		Data store.MailSettings `json:"data"`
	}
	decodeResponse(t, saved, &savedEnvelope)
	if savedEnvelope.Data.Revision != 2 || !savedEnvelope.Data.SMTPPasswordSet || !savedEnvelope.Data.RemindMailEnabled {
		t.Fatalf("saved mail settings=%#v", savedEnvelope.Data)
	}

	stale := administrator.request(t, api, http.MethodPut, "/api/v1/admin/admin/mail-settings", `{
		"revision":1,"smtp_enabled":true,"smtp_host":"stale.example.test","smtp_port":587,
		"smtp_username":"mailer","smtp_encryption":"starttls","smtp_from_address":"support@example.test",
		"remind_mail_enable":true
	}`)
	expectAPIError(t, stale, http.StatusConflict, "settings_conflict")
	current, err := database.GetMailSettings(t.Context())
	if err != nil || current.SMTPHost != "smtp.example.test" || current.Revision != 2 {
		t.Fatalf("stale update changed settings=%#v err=%v", current, err)
	}

	unknown := administrator.request(t, api, http.MethodPut, "/api/v1/admin/admin/mail-settings", `{
		"revision":2,"smtp_enabled":true,"smtp_host":"smtp.example.test","smtp_port":587,
		"smtp_username":"mailer","smtp_encryption":"starttls","smtp_from_address":"support@example.test",
		"remind_mail_enable":true,"unexpected":true
	}`)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown mail setting status=%d body=%s", unknown.Code, unknown.Body)
	}
	missingCSRFRequest := httptest.NewRequest(http.MethodPost, "/api/v1/admin/admin/mail-settings/test", nil)
	administrator.addCookies(missingCSRFRequest)
	missingCSRF := httptest.NewRecorder()
	api.ServeHTTP(missingCSRF, missingCSRFRequest)
	if missingCSRF.Code != http.StatusForbidden || len(sender.messages) != 0 {
		t.Fatalf("missing CSRF status=%d sends=%d", missingCSRF.Code, len(sender.messages))
	}

	testResponse := administrator.request(t, api, http.MethodPost, "/api/v1/admin/admin/mail-settings/test", "")
	if testResponse.Code != http.StatusOK || len(sender.messages) != 1 {
		t.Fatalf("test mail status=%d sends=%d body=%s", testResponse.Code, len(sender.messages), testResponse.Body)
	}
	configuration, message := sender.configurations[0], sender.messages[0]
	if configuration.Password != "secret-smtp-password" || configuration.Encryption != mailer.EncryptionStartTLS ||
		message.To != "admin@example.test" || message.Subject != "This is xboard test email" ||
		!strings.Contains(message.Text, "Site: Xboard-Go") {
		t.Fatalf("test mail configuration=%#v message=%#v", configuration, message)
	}

	legacyAuthorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	legacySave := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/save", legacyAuthorization,
		`{"email_encryption":"ssl","email_password":""}`)
	if legacySave.Code != http.StatusOK {
		t.Fatalf("legacy mail save status=%d body=%s", legacySave.Code, legacySave.Body)
	}
	current, err = database.GetMailSettings(t.Context())
	if err != nil || current.SMTPEncryption != mailer.EncryptionTLS || !current.SMTPPasswordSet || current.Revision != 3 {
		t.Fatalf("legacy mail settings=%#v err=%v", current, err)
	}
	legacyFetch := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=email", legacyAuthorization, "")
	if legacyFetch.Code != http.StatusOK || !strings.Contains(legacyFetch.Body.String(), `"email_encryption":"ssl"`) ||
		!strings.Contains(legacyFetch.Body.String(), `"email_password":""`) || strings.Contains(legacyFetch.Body.String(), "secret-smtp-password") {
		t.Fatalf("legacy mail fetch status=%d body=%s", legacyFetch.Code, legacyFetch.Body)
	}
	mixed := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/save", legacyAuthorization,
		`{"email_host":"mixed.example.test","plan_change_enable":true}`)
	if mixed.Code != http.StatusUnprocessableEntity {
		t.Fatalf("mixed legacy groups status=%d body=%s", mixed.Code, mixed.Body)
	}
	legacyTest := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/testSendMail", legacyAuthorization, "")
	if legacyTest.Code != http.StatusOK || len(sender.messages) != 2 {
		t.Fatalf("legacy test mail status=%d sends=%d body=%s", legacyTest.Code, len(sender.messages), legacyTest.Body)
	}

	sender.failure = errors.New("dial smtp.example.test: credential secret must not leak")
	failed := administrator.request(t, api, http.MethodPost, "/api/v1/admin/admin/mail-settings/test", "")
	expectAPIError(t, failed, http.StatusBadGateway, "smtp_test_failed")
	if strings.Contains(failed.Body.String(), "smtp.example.test") || strings.Contains(failed.Body.String(), "credential") {
		t.Fatalf("SMTP failure response leaked internal details: %s", failed.Body)
	}
	if strings.Contains(logs.String(), "smtp.example.test") || strings.Contains(logs.String(), "credential") || strings.Contains(logs.String(), "admin@example.test") {
		t.Fatalf("SMTP failure log leaked recipient or untrusted server details: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "SMTP test delivery failed") {
		t.Fatalf("SMTP failure log omitted the sanitized operational event: %s", logs.String())
	}
	limited := administrator.request(t, api, http.MethodPost, "/api/v1/admin/admin/mail-settings/test", "")
	expectAPIError(t, limited, http.StatusTooManyRequests, "rate_limited")
}

func TestAdminMailSettingsRejectCleartextSMTPByDefault(t *testing.T) {
	api, _ := newMailSettingsTestAPI(t, &recordingMailSettingsSender{})
	administrator := loginAdmin(t, api)
	response := administrator.request(t, api, http.MethodPut, "/api/v1/admin/admin/mail-settings", `{
		"revision":1,"smtp_enabled":true,"smtp_host":"mailpit","smtp_port":1025,
		"smtp_username":"","smtp_encryption":"none","smtp_from_address":"support@example.test",
		"remind_mail_enable":true
	}`)
	expectAPIError(t, response, http.StatusUnprocessableEntity, "insecure_smtp_disabled")
}

func newMailSettingsTestAPI(t *testing.T, sender mailer.Sender, loggers ...*slog.Logger) (http.Handler, *store.Store) {
	t.Helper()
	database := cloneHTTPAPITestDatabase(t)
	cipherBox, err := appsettings.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	var logger *slog.Logger
	if len(loggers) > 0 {
		logger = loggers[0]
	}
	return New(Dependencies{
		Store: database, PasswordHasher: newHTTPAPITestPasswordHasher(), Now: fixedNow,
		PanelURL: "https://panel.example.test", SettingsCipher: cipherBox, MailSender: sender, Logger: logger,
	}), database
}
