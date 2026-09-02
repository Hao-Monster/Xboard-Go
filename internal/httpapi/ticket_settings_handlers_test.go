package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestAdminTicketSettingsProtectSecretRevisionAndReplyPolicy(t *testing.T) {
	api, database := newTestAPI(t)
	createHTTPTestUser(t, database, "ticket-policy-user@example.test", "ticket-policy-password-123")
	admin := loginAdmin(t, api)
	user := loginAs(t, api, "ticket-policy-user@example.test", "ticket-policy-password-123")

	initialResponse := admin.request(t, api, http.MethodGet, "/api/v1/admin/ticket-settings", "")
	if initialResponse.Code != http.StatusOK {
		t.Fatalf("initial settings status = %d; body=%s", initialResponse.Code, initialResponse.Body)
	}
	initial := decodeTicketSettingsEnvelope(t, initialResponse.Body.Bytes())
	if initial.Revision != 1 || initial.TicketMustWaitReply || initial.SMTPPasswordSet {
		t.Fatalf("initial settings = %#v", initial)
	}

	updatedResponse := admin.request(t, api, http.MethodPut, "/api/v1/admin/ticket-settings", `{
		"revision":1,"app_name":"Xboard","app_url":"https://panel.example.test",
		"ticket_must_wait_reply":true,"smtp_enabled":true,"smtp_host":"smtp.example.test",
		"smtp_port":587,"smtp_username":"mailer","smtp_password":"smtp-secret-password",
		"smtp_encryption":"starttls","smtp_from_address":"support@example.test"
	}`)
	if updatedResponse.Code != http.StatusOK {
		t.Fatalf("update settings status = %d; body=%s", updatedResponse.Code, updatedResponse.Body)
	}
	if strings.Contains(updatedResponse.Body.String(), "smtp-secret-password") || strings.Contains(updatedResponse.Body.String(), "smtp_password_cipher") {
		t.Fatalf("settings response disclosed a credential: %s", updatedResponse.Body)
	}
	updated := decodeTicketSettingsEnvelope(t, updatedResponse.Body.Bytes())
	if updated.Revision != 2 || !updated.TicketMustWaitReply || !updated.SMTPPasswordSet {
		t.Fatalf("updated settings = %#v", updated)
	}

	stale := admin.request(t, api, http.MethodPut, "/api/v1/admin/ticket-settings", `{
		"revision":1,"app_name":"stale","app_url":"","ticket_must_wait_reply":false,
		"smtp_enabled":false,"smtp_host":"","smtp_port":587,"smtp_username":"",
		"smtp_encryption":"starttls","smtp_from_address":""
	}`)
	expectAPIError(t, stale, http.StatusConflict, "settings_conflict")

	created := user.request(t, api, http.MethodPost, "/api/v1/tickets", `{"subject":"等待回复策略","level":0,"message":"初始消息"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create ticket status = %d; body=%s", created.Code, created.Body)
	}
	ticket := decodeTicketEnvelope(t, created)
	pending := user.request(t, api, http.MethodPost, "/api/v1/tickets/"+ticketID(ticket.ID)+"/messages", `{"message":"连续回复"}`)
	expectAPIError(t, pending, http.StatusConflict, "ticket_reply_pending")

	forbidden := user.request(t, api, http.MethodGet, "/api/v1/admin/ticket-settings", "")
	expectAPIError(t, forbidden, http.StatusForbidden, "forbidden")
}

func TestTicketSettingsRejectInsecureSMTPAndUnavailableEncryption(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	insecure := admin.request(t, api, http.MethodPut, "/api/v1/admin/ticket-settings", `{
		"revision":1,"app_name":"Xboard","app_url":"","ticket_must_wait_reply":false,
		"smtp_enabled":true,"smtp_host":"mailpit","smtp_port":1025,"smtp_username":"",
		"smtp_encryption":"none","smtp_from_address":"support@example.test"
	}`)
	expectAPIError(t, insecure, http.StatusUnprocessableEntity, "insecure_smtp_disabled")

	hasher := security.NewPasswordHasher(security.PasswordParams{
		MemoryKiB: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	})
	withoutCipher := New(Dependencies{Store: database, PasswordHasher: hasher, Now: fixedNow, PanelURL: "https://panel.example.test", LegacyAdminPath: testAdminPath})
	adminWithoutCipher := loginAdmin(t, withoutCipher)
	unavailable := adminWithoutCipher.request(t, withoutCipher, http.MethodPut, "/api/v1/admin/ticket-settings", `{
		"revision":1,"app_name":"Xboard","app_url":"","ticket_must_wait_reply":false,
		"smtp_enabled":true,"smtp_host":"smtp.example.test","smtp_port":587,"smtp_username":"mailer",
		"smtp_password":"secret-password","smtp_encryption":"starttls","smtp_from_address":"support@example.test"
	}`)
	expectAPIError(t, unavailable, http.StatusServiceUnavailable, "settings_encryption_unavailable")

	settings, err := database.GetTicketSettings(context.Background())
	if err != nil || settings.Revision != 1 || settings.SMTPPasswordSet {
		t.Fatalf("rejected settings changed persistent state: settings=%#v err=%v", settings, err)
	}
}

func decodeTicketSettingsEnvelope(t *testing.T, payload []byte) store.TicketSettings {
	t.Helper()
	var envelope struct {
		Data store.TicketSettings `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode ticket settings: %v; body=%s", err, payload)
	}
	return envelope.Data
}
