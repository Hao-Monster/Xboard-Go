package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestCommissionSettingsModernAndLegacyContractsAreStrictAndAudited(t *testing.T) {
	api, database := newTestAPI(t)
	createHTTPTestUser(t, database, "commission-reader@example.test", "commission-reader-password-123")
	administrator := loginAdmin(t, api)
	reader := loginAs(t, api, "commission-reader@example.test", "commission-reader-password-123")

	unauthenticated := testClient{}.request(t, api, http.MethodGet, "/api/v1/admin/commission-settings", "")
	expectAPIError(t, unauthenticated, http.StatusUnauthorized, "unauthenticated")
	forbidden := reader.request(t, api, http.MethodGet, "/api/v1/admin/commission-settings", "")
	expectAPIError(t, forbidden, http.StatusForbidden, "forbidden")

	initialResponse := administrator.request(t, api, http.MethodGet, "/api/v1/admin/commission-settings", "")
	if initialResponse.Code != http.StatusOK {
		t.Fatalf("initial commission settings status=%d body=%s", initialResponse.Code, initialResponse.Body)
	}
	initial := decodeCommissionSettingsEnvelope(t, initialResponse)
	if initial.Revision != 1 || initial.InviteCommission != 10 || !initial.FirstTimeEnabled ||
		!initial.AutoCheckEnabled || initial.WithdrawClosed || initial.DistributionEnabled ||
		initial.DistributionL1 != 100 || initial.DistributionL2 != 0 || initial.DistributionL3 != 0 {
		t.Fatalf("initial commission settings=%#v", initial)
	}

	updatedResponse := administrator.request(t, api, http.MethodPut, "/api/v1/admin/commission-settings", `{
		"revision":1,"invite_commission":25,"commission_first_time_enable":false,
		"commission_auto_check_enable":false,"withdraw_close_enable":true,
		"commission_distribution_enable":true,"commission_distribution_l1":50,
		"commission_distribution_l2":30,"commission_distribution_l3":20
	}`)
	if updatedResponse.Code != http.StatusOK {
		t.Fatalf("update commission settings status=%d body=%s", updatedResponse.Code, updatedResponse.Body)
	}
	updated := decodeCommissionSettingsEnvelope(t, updatedResponse)
	if updated.Revision != 2 || updated.InviteCommission != 25 || updated.FirstTimeEnabled || updated.AutoCheckEnabled ||
		!updated.WithdrawClosed || !updated.DistributionEnabled || updated.DistributionL1 != 50 ||
		updated.DistributionL2 != 30 || updated.DistributionL3 != 20 {
		t.Fatalf("updated commission settings=%#v", updated)
	}

	stale := administrator.request(t, api, http.MethodPut, "/api/v1/admin/commission-settings", `{
		"revision":1,"invite_commission":10,"commission_first_time_enable":true,
		"commission_auto_check_enable":true,"withdraw_close_enable":false,
		"commission_distribution_enable":false,"commission_distribution_l1":100,
		"commission_distribution_l2":0,"commission_distribution_l3":0
	}`)
	expectAPIError(t, stale, http.StatusConflict, "settings_conflict")
	missing := administrator.request(t, api, http.MethodPut, "/api/v1/admin/commission-settings", `{
		"revision":2,"invite_commission":10,"commission_first_time_enable":true
	}`)
	expectAPIError(t, missing, http.StatusUnprocessableEntity, "validation_failed")
	invalidTotal := administrator.request(t, api, http.MethodPut, "/api/v1/admin/commission-settings", `{
		"revision":2,"invite_commission":10,"commission_first_time_enable":true,
		"commission_auto_check_enable":true,"withdraw_close_enable":false,
		"commission_distribution_enable":false,"commission_distribution_l1":51,
		"commission_distribution_l2":30,"commission_distribution_l3":20
	}`)
	expectAPIError(t, invalidTotal, http.StatusUnprocessableEntity, "validation_failed")
	unknown := administrator.request(t, api, http.MethodPut, "/api/v1/admin/commission-settings", `{
		"revision":2,"invite_commission":10,"commission_first_time_enable":true,
		"commission_auto_check_enable":true,"withdraw_close_enable":false,
		"commission_distribution_enable":false,"commission_distribution_l1":100,
		"commission_distribution_l2":0,"commission_distribution_l3":0,"commission_withdraw_limit":100
	}`)
	expectAPIError(t, unknown, http.StatusBadRequest, "invalid_json")
	withoutCSRF := administrator
	withoutCSRF.csrf = ""
	csrfRejected := withoutCSRF.request(t, api, http.MethodPut, "/api/v1/admin/commission-settings", `{
		"revision":2,"invite_commission":10,"commission_first_time_enable":true,
		"commission_auto_check_enable":true,"withdraw_close_enable":false,
		"commission_distribution_enable":false,"commission_distribution_l1":100,
		"commission_distribution_l2":0,"commission_distribution_l3":0
	}`)
	expectAPIError(t, csrfRejected, http.StatusForbidden, "csrf_failed")
	wrongMediaTypeRequest := httptest.NewRequest(http.MethodPut, "/api/v1/admin/commission-settings", strings.NewReader(`{}`))
	administrator.addCookies(wrongMediaTypeRequest)
	wrongMediaTypeRequest.Header.Set("X-CSRF-Token", administrator.csrf)
	wrongMediaTypeRequest.Header.Set("Content-Type", "text/plain")
	wrongMediaType := httptest.NewRecorder()
	api.ServeHTTP(wrongMediaType, wrongMediaTypeRequest)
	expectAPIError(t, wrongMediaType, http.StatusUnsupportedMediaType, "unsupported_media_type")

	legacyAuthorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	legacyFetch := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=invite", legacyAuthorization, "")
	if legacyFetch.Code != http.StatusOK || !containsAll(legacyFetch.Body.String(),
		`"invite_commission":25`, `"commission_first_time_enable":false`, `"commission_auto_check_enable":false`,
		`"withdraw_close_enable":true`, `"commission_distribution_enable":true`,
		`"commission_distribution_l1":50`, `"commission_distribution_l2":30`, `"commission_distribution_l3":20`) {
		t.Fatalf("legacy invite fetch status=%d body=%s", legacyFetch.Code, legacyFetch.Body)
	}
	if strings.Contains(legacyFetch.Body.String(), "commission_withdraw_limit") || strings.Contains(legacyFetch.Body.String(), "revision") {
		t.Fatalf("legacy invite fetch disclosed unsupported/internal fields: %s", legacyFetch.Body)
	}
	legacySite := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=site", legacyAuthorization, "")
	if legacySite.Code != http.StatusOK || !containsAll(legacySite.Body.String(), `"currency":"CNY"`, `"currency_symbol":"¥"`) {
		t.Fatalf("legacy site config status=%d body=%s", legacySite.Code, legacySite.Body)
	}
	legacySiteSaved := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/save", legacyAuthorization, `{
		"currency":"usd","currency_symbol":" $ "
	}`)
	if legacySiteSaved.Code != http.StatusOK || !containsAll(legacySiteSaved.Body.String(), `"status":"success"`, `"data":true`) {
		t.Fatalf("legacy site config save status=%d body=%s", legacySiteSaved.Code, legacySiteSaved.Body)
	}
	updatedSite, err := database.GetSiteSettings(t.Context())
	if err != nil || updatedSite.Currency != "USD" || updatedSite.CurrencySymbol != "$" {
		t.Fatalf("legacy site config persisted=%#v err=%v", updatedSite, err)
	}
	invalidLegacySite := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/save", legacyAuthorization, `{
		"currency":"US","currency_symbol":"$"
	}`)
	if invalidLegacySite.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid legacy site status=%d body=%s", invalidLegacySite.Code, invalidLegacySite.Body)
	}
	legacySaved := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/save", legacyAuthorization, `{
		"invite_commission":20,"commission_first_time_enable":true,
		"commission_auto_check_enable":true,"withdraw_close_enable":false,
		"commission_distribution_enable":true,"commission_distribution_l1":50,
		"commission_distribution_l2":30,"commission_distribution_l3":20
	}`)
	if legacySaved.Code != http.StatusOK || !containsAll(legacySaved.Body.String(), `"status":"success"`, `"data":true`) {
		t.Fatalf("legacy config save status=%d body=%s", legacySaved.Code, legacySaved.Body)
	}
	legacyUnknown := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/save", legacyAuthorization, `{
		"invite_commission":20,"commission_first_time_enable":true,
		"commission_auto_check_enable":true,"withdraw_close_enable":false,
		"commission_distribution_enable":true,"commission_distribution_l1":50,
		"commission_distribution_l2":30,"commission_distribution_l3":20,"commission_withdraw_limit":100
	}`)
	if legacyUnknown.Code != http.StatusBadRequest || !strings.Contains(legacyUnknown.Body.String(), `"status":"fail"`) {
		t.Fatalf("legacy unknown config status=%d body=%s", legacyUnknown.Code, legacyUnknown.Body)
	}
	legacyReaderAuthorization := loginLegacyBearer(t, api, "commission-reader@example.test", "commission-reader-password-123").Authorization
	legacyForbidden := bearerRequest(api, http.MethodGet, "/api/v2/admin/config/fetch?key=invite", legacyReaderAuthorization, "")
	if legacyForbidden.Code != http.StatusForbidden {
		t.Fatalf("legacy non-admin fetch status=%d body=%s", legacyForbidden.Code, legacyForbidden.Body)
	}

	summary := administrator.request(t, api, http.MethodGet, "/api/v1/invitations", "")
	if summary.Code != http.StatusOK || !containsAll(summary.Body.String(),
		`"commission_rate":20`, `"commission_distribution_enabled":true`, `"commission_distribution_rates":[10,6,4]`) {
		t.Fatalf("distribution summary status=%d body=%s", summary.Code, summary.Body)
	}
	preserved, err := database.GetCommissionSettings(t.Context())
	if err != nil || preserved.Revision != 4 || preserved.InviteCommission != 20 || !preserved.DistributionEnabled {
		t.Fatalf("rejected requests changed settings: settings=%#v err=%v", preserved, err)
	}
	audits, err := database.ListAdminAuditLogs(t.Context(), store.AdminAuditFilter{Page: 1, PageSize: 20, Query: "commission-settings"})
	if err != nil || audits.Total == 0 || audits.Items[0].Route != "/api/v1/admin/commission-settings" {
		t.Fatalf("modern commission audit=%#v err=%v", audits, err)
	}
	legacyAudits, err := database.ListAdminAuditLogs(t.Context(), store.AdminAuditFilter{Page: 1, PageSize: 20, Query: "/config/save"})
	if err != nil || legacyAudits.Total == 0 || legacyAudits.Items[0].Route != "/api/v2/{secure_admin}/config/save" {
		t.Fatalf("legacy commission audit=%#v err=%v", legacyAudits, err)
	}
}

func decodeCommissionSettingsEnvelope(t *testing.T, response *httptest.ResponseRecorder) store.CommissionSettings {
	t.Helper()
	var envelope struct {
		Data store.CommissionSettings `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode commission settings: %v; body=%s", err, response.Body)
	}
	return envelope.Data
}
