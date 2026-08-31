package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestAdministratorNamespacesAreAuthorizationBoundaries(t *testing.T) {
	api, _ := newTestAPI(t)
	administrator := loginAdmin(t, api)
	const password = "authorization-boundary-password-123"

	accounts := []struct {
		name       string
		email      string
		roleFields string
	}{
		{name: "ordinary", email: "authorization-ordinary@example.test", roleFields: `"is_admin":false,"is_staff":false,"is_distributor":false`},
		{name: "staff", email: "authorization-staff@example.test", roleFields: `"is_admin":false,"is_staff":true,"is_distributor":false`},
		{name: "distributor", email: "authorization-distributor@example.test", roleFields: `"is_admin":false,"is_staff":false,"is_distributor":true,"distributor_name":"授权矩阵分销商"`},
	}
	bearers := make(map[string]string, len(accounts))
	for _, account := range accounts {
		created := administrator.request(t, api, http.MethodPost, "/api/v1/admin/users", fmt.Sprintf(`{
			"email":%q,"password":%q,"group_id":null,"transfer_enable":0,"expired_at":null,
			"speed_limit":0,"device_limit":0,"banned":false,%s
		}`, account.email, password, account.roleFields))
		if created.Code != http.StatusCreated {
			t.Fatalf("create %s status=%d body=%s", account.name, created.Code, created.Body)
		}
		bearers[account.name] = loginLegacyBearer(t, api, account.email, password).Authorization
	}
	adminBearer := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization

	modernUnknown := "/api/v1/admin/not-a-registered-route"
	visitor := httptest.NewRecorder()
	api.ServeHTTP(visitor, httptest.NewRequest(http.MethodGet, modernUnknown, nil))
	if visitor.Code != http.StatusUnauthorized {
		t.Fatalf("visitor modern admin namespace status=%d want=%d body=%s", visitor.Code, http.StatusUnauthorized, visitor.Body)
	}
	for _, account := range accounts {
		response := bearerRequest(api, http.MethodGet, modernUnknown, bearers[account.name], "")
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s modern admin namespace status=%d want=%d body=%s", account.name, response.Code, http.StatusForbidden, response.Body)
		}
	}
	if response := bearerRequest(api, http.MethodGet, modernUnknown, adminBearer, ""); response.Code != http.StatusNotFound {
		t.Fatalf("administrator modern unknown route status=%d want=%d body=%s", response.Code, http.StatusNotFound, response.Body)
	}
	missingCSRFRequest := httptest.NewRequest(http.MethodPost, modernUnknown, nil)
	administrator.addCookies(missingCSRFRequest)
	missingCSRFResponse := httptest.NewRecorder()
	api.ServeHTTP(missingCSRFResponse, missingCSRFRequest)
	if missingCSRFResponse.Code != http.StatusForbidden {
		t.Fatalf("administrator modern unknown write without CSRF status=%d want=%d body=%s", missingCSRFResponse.Code, http.StatusForbidden, missingCSRFResponse.Body)
	}
	if response := administrator.request(t, api, http.MethodPost, modernUnknown, `{}`); response.Code != http.StatusNotFound {
		t.Fatalf("administrator modern unknown write status=%d want=%d body=%s", response.Code, http.StatusNotFound, response.Body)
	}

	legacyUnknown := "/api/v2/admin/not-a-registered-route"
	if response := bearerRequest(api, http.MethodGet, legacyUnknown, "", ""); response.Code != http.StatusForbidden {
		t.Fatalf("visitor legacy admin namespace status=%d want=%d body=%s", response.Code, http.StatusForbidden, response.Body)
	}
	for _, account := range accounts {
		response := bearerRequest(api, http.MethodGet, legacyUnknown, bearers[account.name], "")
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s legacy admin namespace status=%d want=%d body=%s", account.name, response.Code, http.StatusForbidden, response.Body)
		}
	}
	if response := bearerRequest(api, http.MethodGet, legacyUnknown, adminBearer, ""); response.Code != http.StatusNotFound {
		t.Fatalf("administrator legacy unknown route status=%d want=%d body=%s", response.Code, http.StatusNotFound, response.Body)
	}

	legacyFamilies := []string{
		"/api/v2/admin/order/fetch",
		"/api/v2/admin/user/fetch",
		"/api/v2/admin/user/resetSecret",
		"/api/v2/admin/traffic-reset/user/1/history",
		"/api/v2/admin/stat/getStatUser",
		"/api/v2/admin/coupon/fetch",
		"/api/v2/admin/payment/fetch",
		"/api/v2/admin/plugin/getPlugins",
		"/api/v2/admin/gift-card/types",
		"/api/v2/admin/config/fetch?key=invite",
		"/api/v2/admin/knowledge/attachment/fetch",
	}
	for _, path := range legacyFamilies {
		for _, account := range accounts {
			response := bearerRequest(api, http.MethodGet, path, bearers[account.name], "")
			if response.Code != http.StatusForbidden {
				t.Fatalf("%s legacy family %s status=%d want=%d body=%s", account.name, path, response.Code, http.StatusForbidden, response.Body)
			}
		}
	}
}

func TestDistributorAccessIsAnExplicitServerSideAllowlist(t *testing.T) {
	api, _ := newTestAPI(t)
	administrator := loginAdmin(t, api)
	const (
		email    = "authorization-allowlist-distributor@example.test"
		password = "authorization-allowlist-password-123"
	)
	created := administrator.request(t, api, http.MethodPost, "/api/v1/admin/users", fmt.Sprintf(`{
		"email":%q,"password":%q,"group_id":null,"transfer_enable":0,"expired_at":null,
		"speed_limit":0,"device_limit":0,"banned":false,"is_admin":false,"is_staff":false,
		"is_distributor":true,"distributor_name":"允许清单分销商"
	}`, email, password))
	if created.Code != http.StatusCreated {
		t.Fatalf("create distributor status=%d body=%s", created.Code, created.Body)
	}
	distributor := loginAs(t, api, email, password)
	bearer := loginLegacyBearer(t, api, email, password).Authorization

	for _, path := range []string{
		"/api/v1/auth/session",
		"/api/v1/plans",
		"/api/v1/distributor/orders",
		"/api/v1/invitations",
		"/api/v1/knowledge?language=zh-CN",
		"/api/v1/client-catalog",
		"/api/v1/notices",
	} {
		response := distributor.request(t, api, http.MethodGet, path, "")
		if response.Code != http.StatusOK {
			t.Fatalf("allowed distributor route %s status=%d want=%d body=%s", path, response.Code, http.StatusOK, response.Body)
		}
	}
	for _, path := range []string{
		"/api/v1/user/order/fetch",
		"/api/v1/user/order/getPaymentMethod",
		"/api/v1/user/invite/fetch",
	} {
		response := bearerRequest(api, http.MethodGet, path, bearer, "")
		if response.Code != http.StatusOK {
			t.Fatalf("allowed legacy distributor route %s status=%d want=%d body=%s", path, response.Code, http.StatusOK, response.Body)
		}
	}

	for _, request := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/v1/orders"},
		{method: http.MethodGet, path: "/api/v1/payments"},
		{method: http.MethodGet, path: "/api/v1/subscription"},
		{method: http.MethodGet, path: "/api/v1/tickets"},
		{method: http.MethodGet, path: "/api/v1/user/gift-card/history"},
		{method: http.MethodPost, path: "/api/v1/user/coupons/check", body: `{}`},
		{method: http.MethodPost, path: "/api/v1/user/gift-card/check", body: `{}`},
	} {
		response := distributor.request(t, api, request.method, request.path, request.body)
		if response.Code != http.StatusForbidden || response.Body.String() == "" {
			t.Fatalf("denied distributor route %s %s status=%d want=%d body=%s", request.method, request.path, response.Code, http.StatusForbidden, response.Body)
		}
	}
	for _, request := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/v1/user/getSubscribe"},
		{method: http.MethodPost, path: "/api/v1/user/coupon/check", body: `{}`},
	} {
		response := bearerRequest(api, request.method, request.path, bearer, request.body)
		if response.Code != http.StatusForbidden || response.Body.String() == "" {
			t.Fatalf("denied legacy distributor route %s %s status=%d want=%d body=%s", request.method, request.path, response.Code, http.StatusForbidden, response.Body)
		}
	}
}

func TestLegacyAdministratorDistributorOrderMutationsAreAudited(t *testing.T) {
	api, database := newTestAPI(t)
	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	wantRoutes := map[string]struct{}{
		"/api/v2/{secure_admin}/order/remark/update":      {},
		"/api/v2/{secure_admin}/order/entitlement/update": {},
		"/api/v2/{secure_admin}/order/hwid/update":        {},
		"/api/v2/{secure_admin}/order/hwid/device/delete": {},
		"/api/v2/{secure_admin}/order/settlement/settle":  {},
	}
	for route := range wantRoutes {
		actual := route
		actual = "/api/v2/admin/" + actual[len("/api/v2/{secure_admin}/"):]
		response := bearerRequest(api, http.MethodPost, actual, authorization, `{}`)
		if response.Code == http.StatusUnauthorized || response.Code == http.StatusForbidden || response.Code >= 500 {
			t.Fatalf("legacy mutation %s status=%d body=%s", actual, response.Code, response.Body)
		}
	}

	audits, err := database.ListAdminAuditLogs(t.Context(), store.AdminAuditFilter{
		Page: 1, PageSize: 20, Method: http.MethodPost, Query: "/order/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if audits.Total != int64(len(wantRoutes)) || len(audits.Items) != len(wantRoutes) {
		t.Fatalf("legacy mutation audits=%#v want routes=%#v", audits, wantRoutes)
	}
	for _, audit := range audits.Items {
		if _, ok := wantRoutes[audit.Route]; !ok {
			t.Fatalf("unexpected legacy mutation audit=%#v", audit)
		}
		if audit.AdministratorEmail != "admin@example.test" || audit.StatusCode < 400 || audit.StatusCode >= 500 {
			t.Fatalf("incomplete legacy mutation audit=%#v", audit)
		}
		delete(wantRoutes, audit.Route)
	}
	if len(wantRoutes) != 0 {
		t.Fatalf("missing legacy mutation audits=%#v", wantRoutes)
	}
}

func TestJSONWritesRequireAnExactJSONMediaType(t *testing.T) {
	api, _ := newTestAPI(t)
	authorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	const path = "/api/v1/admin/commission-settings"

	for _, contentType := range []string{
		"application/jsonp",
		"application/json garbage",
		"application/json, text/plain",
	} {
		request := httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{}`))
		request.Header.Set("Authorization", authorization)
		request.Header.Set("Content-Type", contentType)
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		if response.Code != http.StatusUnsupportedMediaType || !strings.Contains(response.Body.String(), `"code":"unsupported_media_type"`) {
			t.Fatalf("content type %q status=%d want=%d body=%s", contentType, response.Code, http.StatusUnsupportedMediaType, response.Body)
		}
	}

	valid := httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{}`))
	valid.Header.Set("Authorization", authorization)
	valid.Header.Set("Content-Type", "application/json; charset=utf-8")
	validResponse := httptest.NewRecorder()
	api.ServeHTTP(validResponse, valid)
	if validResponse.Code == http.StatusUnsupportedMediaType {
		t.Fatalf("standards-compliant JSON media type was rejected: body=%s", validResponse.Body)
	}
}

func TestAPISecurityHeadersCoverSuccessfulAndRejectedRequests(t *testing.T) {
	api, _ := newTestAPI(t)
	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{name: "successful public API", path: "/api/v1/guest/plans", wantStatus: http.StatusOK},
		{name: "rejected administrator API", path: "/api/v1/admin/not-a-registered-route", wantStatus: http.StatusUnauthorized},
	}
	wantHeaders := map[string]string{
		"Cache-Control":           "no-store",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "same-origin",
		"Permissions-Policy":      "camera=(), microphone=(), geolocation=()",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.wantStatus, response.Body)
			}
			for name, want := range wantHeaders {
				if got := response.Header().Get(name); got != want {
					t.Errorf("%s=%q want=%q", name, got, want)
				}
			}
		})
	}
}
