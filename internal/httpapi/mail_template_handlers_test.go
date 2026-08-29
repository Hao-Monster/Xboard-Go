package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/mailer"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestAdminMailTemplateModernLegacyAndDeliverySurface(t *testing.T) {
	sender := &recordingMailSettingsSender{}
	api, database := newMailSettingsTestAPI(t, sender)
	administrator := loginAdmin(t, api)

	list := administrator.request(t, api, http.MethodGet, "/api/v1/admin/mail-templates", "")
	if list.Code != http.StatusOK || strings.Count(list.Body.String(), `"customized":false`) != 5 || !strings.Contains(list.Body.String(), `"name":"verify"`) || !strings.Contains(list.Body.String(), `"name":"mailLogin"`) {
		t.Fatalf("modern template list status=%d body=%s", list.Code, list.Body)
	}
	if strings.Contains(list.Body.String(), `"subject"`) || strings.Contains(list.Body.String(), `"content"`) {
		t.Fatalf("modern template list exposed template bodies: %s", list.Body)
	}
	updated := administrator.request(t, api, http.MethodPut, "/api/v1/admin/mail-templates/notify", `{
		"revision":1,"subject":"{{name}} - 自定义通知","content":"<p>{{content}}</p><script>alert(1)</script><p>{{url}}</p>"
	}`)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"customized":true`) || !strings.Contains(updated.Body.String(), `"revision":2`) {
		t.Fatalf("modern template update status=%d body=%s", updated.Code, updated.Body)
	}
	preview := administrator.request(t, api, http.MethodPost, "/api/v1/admin/mail-templates/notify/preview", `{
		"subject":"{{name}} - 预览","content":"<p onclick=\"bad()\">{{content}}</p><script>alert(2)</script>"
	}`)
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), `"subject":"Xboard-Go - 预览"`) || strings.Contains(strings.ToLower(preview.Body.String()), "<script") || strings.Contains(strings.ToLower(preview.Body.String()), "onclick") {
		t.Fatalf("template preview status=%d body=%s", preview.Code, preview.Body)
	}

	mailSettings := administrator.request(t, api, http.MethodPut, "/api/v1/admin/mail-settings", `{
		"revision":1,"smtp_enabled":true,"smtp_host":"smtp.example.test","smtp_port":587,
		"smtp_username":"mailer","smtp_password":"template-test-password","smtp_encryption":"starttls",
		"smtp_from_address":"support@example.test","remind_mail_enable":true
	}`)
	if mailSettings.Code != http.StatusOK {
		t.Fatalf("save SMTP status=%d body=%s", mailSettings.Code, mailSettings.Body)
	}
	testSend := administrator.request(t, api, http.MethodPost, "/api/v1/admin/mail-templates/notify/test", `{"email":"recipient@example.test"}`)
	if testSend.Code != http.StatusOK || len(sender.messages) != 1 {
		t.Fatalf("template test status=%d sends=%d body=%s", testSend.Code, len(sender.messages), testSend.Body)
	}
	message := sender.messages[0]
	if message.To != "recipient@example.test" || message.Subject != "Xboard-Go - 自定义通知" || message.HTML == "" || message.Text == "" || strings.Contains(strings.ToLower(message.HTML), "<script") {
		t.Fatalf("template test message=%#v", message)
	}
	if sender.configurations[0].Password != "template-test-password" || sender.configurations[0].Encryption != mailer.EncryptionStartTLS {
		t.Fatalf("template SMTP configuration=%#v", sender.configurations[0])
	}
	fallbackSend := administrator.request(t, api, http.MethodPost, "/api/v1/admin/mail-templates/verify/test", `{}`)
	if fallbackSend.Code != http.StatusOK || len(sender.messages) != 2 || sender.messages[1].To != "admin@example.test" {
		t.Fatalf("template administrator fallback status=%d messages=%#v body=%s", fallbackSend.Code, sender.messages, fallbackSend.Body)
	}

	stale := administrator.request(t, api, http.MethodPut, "/api/v1/admin/mail-templates/notify", `{"revision":1,"subject":"stale","content":"{{content}}"}`)
	expectAPIError(t, stale, http.StatusConflict, "conflict")
	invalid := administrator.request(t, api, http.MethodPut, "/api/v1/admin/mail-templates/verify", `{"revision":1,"subject":"bad","content":"missing code"}`)
	expectAPIError(t, invalid, http.StatusUnprocessableEntity, "validation_failed")

	legacyAuthorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	legacyList := bearerRequest(api, http.MethodGet, "/api/v2/admin/mail/template/list", legacyAuthorization, "")
	if legacyList.Code != http.StatusOK || !strings.Contains(legacyList.Body.String(), `"customized":true`) {
		t.Fatalf("legacy template list status=%d body=%s", legacyList.Code, legacyList.Body)
	}
	legacyGet := bearerRequest(api, http.MethodGet, "/api/v2/admin/mail/template/get?name=notify", legacyAuthorization, "")
	if legacyGet.Code != http.StatusOK || !strings.Contains(legacyGet.Body.String(), `"required_vars":["content"]`) {
		t.Fatalf("legacy template get status=%d body=%s", legacyGet.Code, legacyGet.Body)
	}
	legacySave := bearerRequest(api, http.MethodPost, "/api/v2/admin/mail/template/save", legacyAuthorization, `{"name":"notify","subject":"{{name}} - 旧版","content":"<p>{{content}}</p>"}`)
	if legacySave.Code != http.StatusOK {
		t.Fatalf("legacy template save status=%d body=%s", legacySave.Code, legacySave.Body)
	}
	legacyReset := bearerRequest(api, http.MethodPost, "/api/v2/admin/mail/template/reset", legacyAuthorization, `{"name":"notify"}`)
	if legacyReset.Code != http.StatusOK {
		t.Fatalf("legacy template reset status=%d body=%s", legacyReset.Code, legacyReset.Body)
	}
	reset, err := database.GetMailTemplate(t.Context(), "notify")
	if err != nil || reset.Customized || reset.Revision != 4 {
		t.Fatalf("legacy reset template=%#v err=%v", reset, err)
	}
	modernAudits, err := database.ListAdminAuditLogs(t.Context(), store.AdminAuditFilter{Page: 1, PageSize: 20, Query: "mail-templates"})
	if err != nil || modernAudits.Total == 0 {
		t.Fatalf("modern mail template audits=%#v err=%v", modernAudits, err)
	}
	legacyAudits, err := database.ListAdminAuditLogs(t.Context(), store.AdminAuditFilter{Page: 1, PageSize: 20, Query: "/mail/template/"})
	if err != nil || legacyAudits.Total != 2 {
		t.Fatalf("legacy mail template audits=%#v err=%v", legacyAudits, err)
	}
	for _, audit := range append(modernAudits.Items, legacyAudits.Items...) {
		if strings.Contains(audit.Route, "自定义通知") || strings.Contains(audit.Route, "recipient@example.test") {
			t.Fatalf("mail template audit exposed request data: %#v", audit)
		}
	}
}

func TestAdminMailTemplateRoutesEnforceAuthorizationCSRFAndInputBounds(t *testing.T) {
	api, database := newMailSettingsTestAPI(t, &recordingMailSettingsSender{})
	createHTTPTestUser(t, database, "template-reader@example.test", "reader-password-123")
	reader := loginAccount(t, api, "template-reader@example.test", "reader-password-123")
	expectAPIError(t, reader.request(t, api, http.MethodGet, "/api/v1/admin/mail-templates", ""), http.StatusForbidden, "forbidden")

	administrator := loginAdmin(t, api)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/mail-templates/notify", strings.NewReader(`{"revision":1,"subject":"s","content":"{{content}}"}`))
	request.Header.Set("Content-Type", "application/json")
	administrator.addCookies(request)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	expectAPIError(t, response, http.StatusForbidden, "csrf_failed")

	unknown := administrator.request(t, api, http.MethodPut, "/api/v1/admin/mail-templates/notify", `{"revision":1,"subject":"s","content":"{{content}}","unknown":true}`)
	expectAPIError(t, unknown, http.StatusBadRequest, "invalid_json")
	badRecipient := administrator.request(t, api, http.MethodPost, "/api/v1/admin/mail-templates/notify/test", `{"email":"victim@example.test\r\nBcc: attacker@example.test"}`)
	expectAPIError(t, badRecipient, http.StatusUnprocessableEntity, "validation_failed")

	legacyReader := loginLegacyBearer(t, api, "template-reader@example.test", "reader-password-123").Authorization
	if response := bearerRequest(api, http.MethodGet, "/api/v2/admin/mail/template/list", legacyReader, ""); response.Code != http.StatusForbidden {
		t.Fatalf("legacy reader status=%d body=%s", response.Code, response.Body)
	}
}
