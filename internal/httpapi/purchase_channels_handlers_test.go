package httpapi

import (
	"encoding/json"
	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"net/http"
	"testing"
	"time"
)

func TestPurchaseChannelsHTTPPermissionsValidationAndAudit(t *testing.T) {
	api, db := newTestAPI(t)
	createHTTPTestUser(t, db, "channels-reader@example.test", "channels-reader-password-123")
	admin := loginAdmin(t, api)
	reader := loginAs(t, api, "channels-reader@example.test", "channels-reader-password-123")
	dealer, err := db.CreateAdminUser(t.Context(), store.CreateAdminUserInput{Email: "channels-dealer@example.test", PasswordHash: "hash", IsDistributor: true, DistributorName: "Channels dealer"}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	sessionToken, err := security.NewOpaqueToken(32)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(t.Context(), dealer.ID, sessionToken.Digest, "test-csrf-digest", fixedNow().Add(time.Hour), fixedNow()); err != nil {
		t.Fatal(err)
	}
	distributor := testClient{cookies: []*http.Cookie{{Name: SessionCookieName, Value: sessionToken.Plaintext}}}
	expectAPIError(t, distributor.request(t, api, http.MethodGet, "/api/v1/user/purchase-channels", ""), http.StatusForbidden, "distributor_route_forbidden")
	const adminPath = "/api/v1/admin/admin/purchase-channels"
	const userPath = "/api/v1/user/purchase-channels"
	const valid = `{"revision":1,"card_store_url":"https://cards.example.test/p/1","chatwoot_base_url":"https://chat.example.test","chatwoot_website_token":"public-widget"}`
	for _, path := range []string{adminPath, userPath} {
		expectAPIError(t, testClient{}.request(t, api, http.MethodGet, path, ""), http.StatusUnauthorized, "unauthenticated")
	}
	expectAPIError(t, reader.request(t, api, http.MethodGet, adminPath, ""), http.StatusForbidden, "forbidden")
	expectAPIError(t, reader.request(t, api, http.MethodPut, adminPath, valid), http.StatusForbidden, "forbidden")
	noCSRF := admin
	noCSRF.csrf = ""
	expectAPIError(t, noCSRF.request(t, api, http.MethodPut, adminPath, valid), http.StatusForbidden, "csrf_failed")
	initial := reader.request(t, api, http.MethodGet, userPath, "")
	if initial.Code != http.StatusOK || !containsAll(initial.Body.String(), `"revision":1`, `"card_store_url":""`, `"chatwoot_base_url":""`, `"chatwoot_website_token":""`) {
		t.Fatalf("initial: %d %s", initial.Code, initial.Body)
	}
	for _, body := range []string{
		`{"revision":1}`,
		`{"revision":1,"card_store_url":"http://cards.example.test","chatwoot_base_url":"","chatwoot_website_token":""}`,
		`{"revision":1,"card_store_url":"","chatwoot_base_url":"https://chat.example.test/path","chatwoot_website_token":"public-widget"}`,
		`{"revision":1,"card_store_url":"","chatwoot_base_url":"https://chat.example.test","chatwoot_website_token":""}`,
	} {
		expectAPIError(t, admin.request(t, api, http.MethodPut, adminPath, body), http.StatusUnprocessableEntity, "validation_failed")
	}
	response := admin.request(t, api, http.MethodPut, adminPath, valid)
	if response.Code != http.StatusOK {
		t.Fatalf("save: %d %s", response.Code, response.Body)
	}
	expectAPIError(t, admin.request(t, api, http.MethodPut, adminPath, valid), http.StatusConflict, "settings_conflict")
	response = reader.request(t, api, http.MethodGet, userPath, "")
	var envelope struct {
		Data store.PurchaseChannels `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || envelope.Data.Revision != 2 || envelope.Data.CardStoreURL != "https://cards.example.test/p/1" || envelope.Data.ChatwootWebsiteToken != "public-widget" || response.Header().Get("Cache-Control") != "no-store, private" {
		t.Fatalf("read: %d %+v", response.Code, envelope)
	}
	audits, err := db.ListAdminAuditLogs(t.Context(), store.AdminAuditFilter{Page: 1, PageSize: 20, Query: "purchase-channels"})
	foundSuccess := false
	for _, entry := range audits.Items {
		if entry.Method == http.MethodPut && entry.Route == "/api/v1/admin/purchase-channels" && entry.StatusCode == http.StatusOK && entry.AdministratorID != nil {
			foundSuccess = true
		}
	}
	if err != nil || !foundSuccess {
		t.Fatalf("audit missing: %+v %v", audits, err)
	}
}
