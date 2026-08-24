package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestAdministratorSystemOperationsAndAuditEndpoints(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	csrfAttempt := httptest.NewRequest(http.MethodPost, "/api/v1/admin/machines", strings.NewReader(`{"name":"blocked"}`))
	csrfAttempt.Header.Set("Content-Type", "application/json")
	admin.addCookies(csrfAttempt)
	csrfResponse := httptest.NewRecorder()
	api.ServeHTTP(csrfResponse, csrfAttempt)
	if csrfResponse.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", csrfResponse.Code)
	}

	statusResponse := admin.request(t, api, http.MethodGet, "/api/v1/admin/system/status", "")
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("system status = %d; body=%s", statusResponse.Code, statusResponse.Body)
	}
	var statusPayload struct {
		Data struct {
			SchemaVersion int                    `json:"schema_version"`
			Scheduler     map[string]any         `json:"scheduler"`
			MailWorker    map[string]any         `json:"mail_worker"`
			MailQueue     store.SystemQueueStats `json:"mail_queue"`
		} `json:"data"`
	}
	decodeResponse(t, statusResponse, &statusPayload)
	if statusPayload.Data.SchemaVersion != 17 || statusPayload.Data.Scheduler["healthy"] != true || statusPayload.Data.MailWorker["healthy"] != true {
		t.Fatalf("system status payload = %#v", statusPayload.Data)
	}

	created := admin.request(t, api, http.MethodPost, "/api/v1/admin/notices", `{
		"title":"Audited notice","content":"private audit body","image_url":"","tags":[],"show":false
	}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create notice = %d; body=%s", created.Code, created.Body)
	}
	notAllowed := admin.request(t, api, http.MethodTrace, "/api/v1/admin/notices", "")
	if notAllowed.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unsupported method = %d; body=%s", notAllowed.Code, notAllowed.Body)
	}
	auditResponse := admin.request(t, api, http.MethodGet, "/api/v1/admin/system/audit?page=1&page_size=20&query=notices", "")
	if auditResponse.Code != http.StatusOK {
		t.Fatalf("audit response = %d; body=%s", auditResponse.Code, auditResponse.Body)
	}
	if strings.Contains(auditResponse.Body.String(), "private audit body") {
		t.Fatal("administrator audit endpoint exposed the request body")
	}
	var auditPayload struct {
		Data store.AdminAuditPage `json:"data"`
	}
	decodeResponse(t, auditResponse, &auditPayload)
	if auditPayload.Data.Total != 1 || len(auditPayload.Data.Items) != 1 {
		t.Fatalf("audit page = %#v", auditPayload.Data)
	}
	item := auditPayload.Data.Items[0]
	if item.Method != http.MethodPost || item.Route != "/api/v1/admin/notices" || item.StatusCode != http.StatusCreated || item.AdministratorEmail != "admin@example.test" {
		t.Fatalf("audit item = %#v", item)
	}
	blockedAudit := admin.request(t, api, http.MethodGet, "/api/v1/admin/system/audit?page=1&page_size=20&method=POST&query=machines", "")
	var blockedAuditPayload struct {
		Data store.AdminAuditPage `json:"data"`
	}
	decodeResponse(t, blockedAudit, &blockedAuditPayload)
	if blockedAuditPayload.Data.Total != 1 || blockedAuditPayload.Data.Items[0].StatusCode != http.StatusForbidden || blockedAuditPayload.Data.Items[0].Route != "/api/v1/admin/machines" {
		t.Fatalf("blocked audit = %#v", blockedAuditPayload.Data)
	}

	failures := admin.request(t, api, http.MethodGet, "/api/v1/admin/system/mail-failures?page=1&page_size=20", "")
	if failures.Code != http.StatusOK {
		t.Fatalf("mail failures = %d; body=%s", failures.Code, failures.Body)
	}
	var failuresPayload struct {
		Data store.TicketMailFailurePage `json:"data"`
	}
	decodeResponse(t, failures, &failuresPayload)
	if failuresPayload.Data.Total != 0 || failuresPayload.Data.Items == nil {
		t.Fatalf("mail failures payload = %#v", failuresPayload.Data)
	}

	hasher := security.NewPasswordHasher(security.PasswordParams{
		MemoryKiB: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	})
	passwordHash, err := hasher.Hash("ordinary-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateAdminUser(context.Background(), store.CreateAdminUserInput{Email: "ordinary@example.test", PasswordHash: passwordHash}, fixedNow()); err != nil {
		t.Fatal(err)
	}
	ordinary := loginAs(t, api, "ordinary@example.test", "ordinary-password-123")
	for _, path := range []string{"/api/v1/admin/system/status", "/api/v1/admin/system/audit", "/api/v1/admin/system/mail-failures"} {
		response := ordinary.request(t, api, http.MethodGet, path, "")
		if response.Code != http.StatusForbidden {
			t.Fatalf("ordinary user %s = %d", path, response.Code)
		}
	}
}
