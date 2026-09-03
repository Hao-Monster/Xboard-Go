package httpapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestAdminUserBulkBanPersistsWarningWhenRuntimeNotificationFails(t *testing.T) {
	_, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	administrator, err := database.FindUserByEmail(ctx, "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	groupID := int64(7)
	target, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "u5-runtime-warning@example.test", PasswordHash: "hash", GroupID: &groupID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	job, err := database.BanAdminUsers(ctx, store.BanAdminUsersInput{
		AdministratorID: administrator.ID, IdempotencyKey: "u5-runtime-warning-0001",
		Scope: store.AdminUserBulkScope{Scope: store.AdminUserBulkScopeSelected, UserIDs: []int64{target.ID}},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	failedHubStore := cloneHTTPAPITestDatabase(t)
	if err := failedHubStore.Close(); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	testServer := &server{
		store: database, hub: newWSHub(failedHubStore, fixedNow, logger, nil, nil), logger: logger, now: fixedNow,
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/admin/users/bulk/ban", nil)
	warned := testServer.notifyAndRecordAdminUserBulkBan(request, job)
	if warned.Status != store.AdminUserBulkStatusSucceeded || !strings.Contains(warned.LastError, "next full pull") {
		t.Fatalf("runtime notification warning = %#v", warned)
	}
	persisted, err := database.GetAdminUserBulkJob(ctx, job.ID)
	if err != nil || persisted.LastError != warned.LastError {
		t.Fatalf("persisted runtime warning = %#v, %v", persisted, err)
	}
}

func TestAdminUserBulkEndpointsValidateScopeProtectSelfAndHideContent(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	client := loginAdmin(t, api)
	administrator, err := database.FindUserByEmail(ctx, "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	target, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "u5-http-target@example.test", PasswordHash: "hash", TransferEnable: 8 << 30,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(ctx, administrator.ID, settings.Revision, store.SaveTicketSettingsInput{
		AppName: "U5 HTTP", AppURL: "https://panel.example.test", SMTPEnabled: true,
		SMTPHost: "smtp.example.test", SMTPPort: 587, SMTPEncryption: "starttls", SMTPFromAddress: "no-reply@example.test",
	}, now); err != nil {
		t.Fatal(err)
	}

	invalid := client.request(t, api, http.MethodPost, "/api/v1/admin/admin/users/bulk/csv", `{"scope":"filtered"}`)
	if invalid.Code != http.StatusUnprocessableEntity || !strings.Contains(invalid.Body.String(), `"code":"validation_failed"`) {
		t.Fatalf("empty filtered scope status=%d body=%s", invalid.Code, invalid.Body)
	}
	mail := client.request(t, api, http.MethodPost, "/api/v1/admin/admin/users/bulk/mail", fmt.Sprintf(
		`{"scope":"selected","user_ids":[%d],"subject":"通知 {{user.email}}","content":"secret body {{app.name}}"}`, target.ID))
	if mail.Code != http.StatusAccepted || !containsAll(mail.Body.String(), `"kind":"mail"`, `"status":"queued"`, `"total_count":1`) ||
		strings.Contains(mail.Body.String(), "secret body") {
		t.Fatalf("mail status=%d body=%s", mail.Code, mail.Body)
	}
	var mailPayload struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	decodeResponse(t, mail, &mailPayload)
	detail := client.request(t, api, http.MethodGet, "/api/v1/admin/admin/user-bulk-jobs/"+mailPayload.Data.ID, "")
	if detail.Code != http.StatusOK || strings.Contains(detail.Body.String(), "secret body") || strings.Contains(detail.Body.String(), "smtp.example.test") {
		t.Fatalf("job detail status=%d body=%s", detail.Code, detail.Body)
	}

	missingKey := client.request(t, api, http.MethodPost, "/api/v1/admin/admin/users/bulk/ban", `{"scope":"all"}`)
	if missingKey.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing ban key status=%d body=%s", missingKey.Code, missingKey.Body)
	}
	ban := client.request(t, api, http.MethodPost, "/api/v1/admin/admin/users/bulk/ban",
		`{"scope":"all","idempotency_key":"u5-http-ban-0001"}`)
	if ban.Code != http.StatusOK || !containsAll(ban.Body.String(), `"kind":"ban"`, `"status":"succeeded"`, `"success_count":1`, `"skipped_count":1`) {
		t.Fatalf("ban status=%d body=%s", ban.Code, ban.Body)
	}
	adminAfter, err := database.FindUserByEmail(ctx, administrator.Email)
	if err != nil || adminAfter.Banned {
		t.Fatalf("administrator after ban = %#v, %v", adminAfter, err)
	}
	targetAfter, err := database.FindUserByEmail(ctx, target.Email)
	if err != nil || !targetAfter.Banned {
		t.Fatalf("banned target state=%#v error=%v", targetAfter, err)
	}
	replay := client.request(t, api, http.MethodPost, "/api/v1/admin/admin/users/bulk/ban",
		`{"scope":"all","idempotency_key":"u5-http-ban-0001"}`)
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"success_count":1`) {
		t.Fatalf("ban replay status=%d body=%s", replay.Code, replay.Body)
	}

	listed := client.request(t, api, http.MethodGet, "/api/v1/admin/admin/user-bulk-jobs?page=1&page_size=20", "")
	if listed.Code != http.StatusOK || !containsAll(listed.Body.String(), `"total":2`, `"page":1`) || strings.Contains(listed.Body.String(), "secret body") {
		t.Fatalf("job list status=%d body=%s", listed.Code, listed.Body)
	}
}

func TestAdminUserBulkEndpointsEnforceAuthenticationAuthorizationCSRFAndUnknownJobPrivacy(t *testing.T) {
	api, _ := newTestAPI(t)
	unauthenticated := testClient{}.request(t, api, http.MethodGet, "/api/v1/admin/admin/user-bulk-jobs", "")
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list status=%d body=%s", unauthenticated.Code, unauthenticated.Body)
	}
	admin := loginAdmin(t, api)
	created := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/users", `{
		"email":"u5-bulk-reader@example.test","password":"reader-password-123","group_id":7,
		"transfer_enable":1,"speed_limit":0,"device_limit":0,"banned":false
	}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create non-admin status=%d body=%s", created.Code, created.Body)
	}
	var createdPayload struct {
		Data store.AdminUser `json:"data"`
	}
	decodeResponse(t, created, &createdPayload)
	reader := loginAccount(t, api, createdPayload.Data.Email, "reader-password-123")
	forbidden := reader.request(t, api, http.MethodPost, "/api/v1/admin/admin/users/bulk/csv", fmt.Sprintf(
		`{"scope":"selected","user_ids":[%d]}`, createdPayload.Data.ID))
	if forbidden.Code != http.StatusForbidden || strings.Contains(forbidden.Body.String(), createdPayload.Data.Email) {
		t.Fatalf("non-admin bulk status=%d body=%s", forbidden.Code, forbidden.Body)
	}
	legacyAuthorization := loginLegacyBearer(t, api, createdPayload.Data.Email, "reader-password-123").Authorization
	legacyForbidden := bearerRequest(api, http.MethodPost, "/api/v2/admin/user/dumpCSV", legacyAuthorization,
		fmt.Sprintf(`{"scope":"selected","user_ids":[%d]}`, createdPayload.Data.ID))
	if legacyForbidden.Code != http.StatusForbidden {
		t.Fatalf("non-admin legacy bulk status=%d body=%s", legacyForbidden.Code, legacyForbidden.Body)
	}
	withoutCSRF := testClient{cookies: admin.cookies}
	csrfRejected := withoutCSRF.request(t, api, http.MethodPost, "/api/v1/admin/admin/users/bulk/csv", fmt.Sprintf(
		`{"scope":"selected","user_ids":[%d]}`, createdPayload.Data.ID))
	if csrfRejected.Code != http.StatusForbidden || !strings.Contains(csrfRejected.Body.String(), `"code":"csrf_failed"`) {
		t.Fatalf("missing CSRF status=%d body=%s", csrfRejected.Code, csrfRejected.Body)
	}
	unknown := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/user-bulk-jobs/00000000-0000-4000-8000-000000000099", "")
	if unknown.Code != http.StatusNotFound || strings.Contains(unknown.Body.String(), createdPayload.Data.Email) {
		t.Fatalf("unknown job status=%d body=%s", unknown.Code, unknown.Body)
	}
}

func TestLegacyAdminUserBulkMailAndBanCompatibility(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	administrator, err := database.FindUserByEmail(ctx, "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(ctx, administrator.ID, settings.Revision, store.SaveTicketSettingsInput{
		AppName: "Legacy U5", AppURL: "https://panel.example.test", SMTPEnabled: true,
		SMTPHost: "smtp.example.test", SMTPPort: 587, SMTPEncryption: "starttls", SMTPFromAddress: "no-reply@example.test",
	}, now); err != nil {
		t.Fatal(err)
	}
	target, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "u5-legacy-bulk@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	mail := bearerRequest(api, http.MethodPost, "/api/v2/admin/user/sendMail", authorization,
		fmt.Sprintf(`{"scope":"selected","user_ids":[%d],"subject":"旧通知","content":"旧正文"}`, target.ID))
	if mail.Code != http.StatusOK || strings.TrimSpace(mail.Body.String()) != `{"data":true}` {
		t.Fatalf("legacy mail status=%d body=%s", mail.Code, mail.Body)
	}
	ban := bearerRequest(api, http.MethodPost, "/api/v2/admin/user/ban", authorization,
		fmt.Sprintf(`{"scope":"selected","user_ids":[%d]}`, target.ID))
	if ban.Code != http.StatusOK || strings.TrimSpace(ban.Body.String()) != `{"data":true}` {
		t.Fatalf("legacy ban status=%d body=%s", ban.Code, ban.Body)
	}
	page, err := database.ListAdminUserBulkJobs(ctx, 1, 20)
	if err != nil || page.Total != 2 {
		t.Fatalf("legacy jobs = %#v, %v", page, err)
	}
}

func TestLegacyAndModernAdminUserBulkCSVDownload(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	target, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "u5-csv-download@example.test", PasswordHash: "hash", TransferEnable: 3 << 30,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	exported := bearerRequest(api, http.MethodPost, "/api/v2/admin/user/dumpCSV", authorization,
		fmt.Sprintf(`{"scope":"selected","user_ids":[%d]}`, target.ID))
	if exported.Code != http.StatusOK || exported.Header().Get("Cache-Control") != "no-store" ||
		!strings.HasPrefix(exported.Body.String(), "\ufeff邮箱,余额,推广佣金") ||
		!strings.Contains(exported.Body.String(), target.Email+",0.00,0.00,3 GB,3 GB") {
		t.Fatalf("legacy CSV status=%d headers=%v body=%q", exported.Code, exported.Header(), exported.Body.String())
	}
	jobs, err := database.ListAdminUserBulkJobs(ctx, 1, 20)
	if err != nil || jobs.Total != 1 || jobs.Items[0].Status != store.AdminUserBulkStatusSucceeded {
		t.Fatalf("CSV jobs=%#v error=%v", jobs, err)
	}
	unauthenticated := httptest.NewRecorder()
	api.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v1/admin/admin/user-bulk-jobs/"+jobs.Items[0].ID+"/download", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated download status=%d body=%s", unauthenticated.Code, unauthenticated.Body)
	}
	client := loginAdmin(t, api)
	download := client.request(t, api, http.MethodGet, "/api/v1/admin/admin/user-bulk-jobs/"+jobs.Items[0].ID+"/download", "")
	if download.Code != http.StatusOK || download.Body.String() != exported.Body.String() ||
		download.Header().Get("Content-Disposition") == "" {
		t.Fatalf("modern download status=%d headers=%v body=%q", download.Code, download.Header(), download.Body.String())
	}
	if cleared, err := database.ClearExpiredAdminUserBulkOutput(ctx, jobs.Items[0].ID, jobs.Items[0].OutputRelativePath, now.Add(25*time.Hour)); err != nil || !cleared {
		t.Fatalf("ClearExpiredAdminUserBulkOutput() = %v, %v", cleared, err)
	}
	expired := client.request(t, api, http.MethodGet, "/api/v1/admin/admin/user-bulk-jobs/"+jobs.Items[0].ID+"/download", "")
	if expired.Code != http.StatusGone || !strings.Contains(expired.Body.String(), `"code":"bulk_export_expired"`) || expired.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expired download status=%d headers=%v body=%s", expired.Code, expired.Header(), expired.Body)
	}
}
