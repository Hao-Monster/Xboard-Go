package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/knowledge"
	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestKnowledgeAdminLifecycleAndUserSubscriptionRendering(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	siteResponse := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", `{
		"revision":1,"app_name":"Tenant Knowledge","app_description":"","app_url":"https://panel.example.test",
		"subscribe_url":"https://knowledge-subscriptions.example.test/root/",
		"tos_url":"","logo":"https://images.example.test/tenant.png"
	}`)
	if siteResponse.Code != http.StatusOK {
		t.Fatalf("update site identity status=%d body=%s", siteResponse.Code, siteResponse.Body)
	}

	createdResponse := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/knowledge", `{
		"language":"zh-CN","category":"入门","title":"连接指南",
		"body":"# {{siteName}}\n\n{{subscribeUrl}}\n\n<!--access start-->订阅专属<!--access end-->","show":true
	}`)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create knowledge status = %d; body=%s", createdResponse.Code, createdResponse.Body)
	}
	var created struct {
		Data store.Knowledge `json:"data"`
	}
	decodeResponse(t, createdResponse, &created)
	if created.Data.ID < 1 || created.Data.Revision != 1 || !created.Data.Visible {
		t.Fatalf("created knowledge = %#v", created.Data)
	}

	listResponse := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/knowledge", "")
	if listResponse.Code != http.StatusOK || strings.Contains(listResponse.Body.String(), "subscribeUrl") {
		t.Fatalf("admin list status=%d leaked full body=%s", listResponse.Code, listResponse.Body)
	}
	detailResponse := admin.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/admin/admin/knowledge/%d", created.Data.ID), "")
	if detailResponse.Code != http.StatusOK || !strings.Contains(detailResponse.Body.String(), "subscribeUrl") {
		t.Fatalf("admin detail status=%d body=%s", detailResponse.Code, detailResponse.Body)
	}
	categoriesResponse := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/knowledge/categories", "")
	if categoriesResponse.Code != http.StatusOK || !strings.Contains(categoriesResponse.Body.String(), "入门") {
		t.Fatalf("categories status=%d body=%s", categoriesResponse.Code, categoriesResponse.Body)
	}

	active := createKnowledgeTestUser(t, database, "active@example.test", "active-password-123", 1_000, false)
	activeClient := loginAs(t, api, active.email, active.password)
	activeResponse := activeClient.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/knowledge/%d", created.Data.ID), "")
	if activeResponse.Code != http.StatusOK {
		t.Fatalf("active user status=%d body=%s", activeResponse.Code, activeResponse.Body)
	}
	if !strings.Contains(activeResponse.Body.String(), "订阅专属") || !strings.Contains(activeResponse.Body.String(), "Tenant Knowledge") ||
		!strings.Contains(activeResponse.Body.String(), "https://knowledge-subscriptions.example.test/root/s/") || strings.Contains(activeResponse.Body.String(), "{{siteName}}") {
		t.Fatalf("active user content = %s", activeResponse.Body)
	}

	inactive := createKnowledgeTestUser(t, database, "inactive@example.test", "inactive-password-123", 0, false)
	inactiveClient := loginAs(t, api, inactive.email, inactive.password)
	inactiveResponse := inactiveClient.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/knowledge/%d", created.Data.ID), "")
	if inactiveResponse.Code != http.StatusOK || strings.Contains(inactiveResponse.Body.String(), "订阅专属") || !strings.Contains(inactiveResponse.Body.String(), knowledge.NoSubscriptionMessage) {
		t.Fatalf("inactive user content status=%d body=%s", inactiveResponse.Code, inactiveResponse.Body)
	}

	filteredResponse := activeClient.request(t, api, http.MethodGet, "/api/v1/knowledge?language=zh-CN&keyword="+url.QueryEscape("连接"), "")
	if filteredResponse.Code != http.StatusOK || !strings.Contains(filteredResponse.Body.String(), "连接指南") {
		t.Fatalf("filtered knowledge status=%d body=%s", filteredResponse.Code, filteredResponse.Body)
	}

	visibilityResponse := admin.request(t, api, http.MethodPatch, fmt.Sprintf("/api/v1/admin/admin/knowledge/%d/visibility", created.Data.ID), fmt.Sprintf(`{"revision":%d,"show":false}`, created.Data.Revision))
	if visibilityResponse.Code != http.StatusOK {
		t.Fatalf("hide knowledge status=%d body=%s", visibilityResponse.Code, visibilityResponse.Body)
	}
	hiddenResponse := activeClient.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/knowledge/%d", created.Data.ID), "")
	if hiddenResponse.Code != http.StatusNotFound {
		t.Fatalf("hidden user knowledge status=%d body=%s", hiddenResponse.Code, hiddenResponse.Body)
	}
}

func TestPublicKnowledgeUsesCanonicalSafeSharePageWithoutUserSecrets(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	siteResponse := admin.request(t, api, http.MethodPut, "/api/v1/admin/admin/site-settings", `{
		"revision":1,"app_name":"Public Board","app_description":"","app_url":"https://panel.example.test",
		"tos_url":"","logo":"https://images.example.test/public.svg?version=1"
	}`)
	if siteResponse.Code != http.StatusOK {
		t.Fatalf("update site identity status=%d body=%s", siteResponse.Code, siteResponse.Body)
	}
	article, err := database.CreateKnowledge(context.Background(), store.SaveKnowledgeInput{
		Language: "zh-CN", Category: "安全", Title: "Public Security Guide", Visible: true,
		Body: "# Security\n\n{{subscribeUrl}}\n\n<script>alert(1)</script>\n\n<img src=\"https://images.example.test/a.png\" onerror=\"alert(2)\">",
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	viewer := createKnowledgeTestUser(t, database, "secret@example.test", "secret-password-123", 1_000, false)
	viewerState, err := database.GetKnowledgeViewer(context.Background(), viewer.id, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	privateToken := viewerState.SubscriptionToken

	bare := httptest.NewRecorder()
	api.ServeHTTP(bare, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/guide/%d", article.ID), nil))
	wantCanonical := fmt.Sprintf("https://panel.example.test/guide/%d/public-security-guide", article.ID)
	if bare.Code != http.StatusFound || bare.Header().Get("Location") != wantCanonical {
		t.Fatalf("bare share = status %d location %q", bare.Code, bare.Header().Get("Location"))
	}

	wrong := httptest.NewRecorder()
	api.ServeHTTP(wrong, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/guide/%d/wrong", article.ID), nil))
	if wrong.Code != http.StatusFound || wrong.Header().Get("Location") != wantCanonical {
		t.Fatalf("wrong slug = status %d location %q", wrong.Code, wrong.Header().Get("Location"))
	}

	page := httptest.NewRecorder()
	api.ServeHTTP(page, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/guide/%d/public-security-guide", article.ID), nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("public page status=%d type=%q body=%s", page.Code, page.Header().Get("Content-Type"), page.Body)
	}
	for _, forbidden := range []string{privateToken, "{{subscribeUrl}}", "<script", "onerror", "javascript:"} {
		if strings.Contains(strings.ToLower(page.Body.String()), strings.ToLower(forbidden)) {
			t.Fatalf("public page retained %q: %s", forbidden, page.Body)
		}
	}
	if !strings.Contains(page.Body.String(), "https://panel.example.test/#/login") || !strings.Contains(page.Body.String(), "/public-knowledge.css") || !strings.Contains(page.Body.String(), `aria-current="page"`) {
		t.Fatalf("public page missing login fallback, stylesheet, or current navigation state: %s", page.Body)
	}
	for _, branding := range []string{
		"Public Security Guide - Public Board", `aria-label="Public Board"`,
		`src="https://images.example.test/public.svg?version=1"`, `alt="Public Board LOGO"`, `referrerpolicy="no-referrer"`,
	} {
		if !strings.Contains(page.Body.String(), branding) {
			t.Fatalf("public page missing branding %q: %s", branding, page.Body)
		}
	}
	if page.Header().Get("Cache-Control") != "no-store" || !strings.Contains(page.Header().Get("Content-Security-Policy"), "style-src 'self'") {
		t.Fatalf("public page security headers cache=%q csp=%q", page.Header().Get("Cache-Control"), page.Header().Get("Content-Security-Policy"))
	}

	content := httptest.NewRecorder()
	api.ServeHTTP(content, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/guide/%d/content", article.ID), nil))
	if content.Code != http.StatusOK || !strings.Contains(content.Body.String(), `"share_url":"`+wantCanonical+`"`) ||
		!strings.Contains(content.Body.String(), `"page_title":"Public Security Guide - Public Board"`) || strings.Contains(content.Body.String(), privateToken) {
		t.Fatalf("public content status=%d body=%s", content.Code, content.Body)
	}
}

type knowledgeTestUser struct {
	id       int64
	email    string
	password string
}

func createKnowledgeTestUser(t *testing.T, database *store.Store, email, password string, transferEnable int64, banned bool) knowledgeTestUser {
	t.Helper()
	hasher := security.NewPasswordHasher(security.PasswordParams{MemoryKiB: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	hash, err := hasher.Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	created, err := database.CreateAdminUser(context.Background(), store.CreateAdminUserInput{
		Email: email, PasswordHash: hash, GroupID: pointerToKnowledgeGroup(7), TransferEnable: transferEnable, Banned: banned,
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	return knowledgeTestUser{id: created.ID, email: email, password: password}
}

func pointerToKnowledgeGroup(value int64) *int64 { return &value }
