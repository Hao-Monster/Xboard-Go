package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestThemeModernLegacyGuestAndAssetContracts(t *testing.T) {
	api, database := newTestAPI(t)
	createHTTPTestUser(t, database, "theme-reader@example.test", "theme-reader-password-123")
	administrator := loginAdmin(t, api)
	reader := loginAs(t, api, "theme-reader@example.test", "theme-reader-password-123")

	unauthenticated := testClient{}.request(t, api, http.MethodGet, "/api/v1/admin/themes", "")
	expectAPIError(t, unauthenticated, http.StatusUnauthorized, "unauthenticated")
	forbidden := reader.request(t, api, http.MethodGet, "/api/v1/admin/themes", "")
	expectAPIError(t, forbidden, http.StatusForbidden, "forbidden")

	initial := administrator.request(t, api, http.MethodGet, "/api/v1/admin/themes", "")
	if initial.Code != http.StatusOK || !containsAll(initial.Body.String(), `"active_theme":"Xboard"`, `"revision":1`, `"sidebar_style":"light"`, `"header_style":"dark"`, `"is_system":true`, `"theme_color":"default"`, `"default":{`, `"blue":{`, `"black":{`, `"darkblue":{`, `"images":[]`, `"backgrounds":[]`) {
		t.Fatalf("initial themes status=%d body=%s", initial.Code, initial.Body)
	}
	guest := plainAPIRequest(api, http.MethodGet, "/api/v1/guest/comm/config", "")
	if guest.Code != http.StatusOK || !containsAll(guest.Body.String(), `"theme":{"name":"Xboard"`, `"background":"#0b0d12"`) {
		t.Fatalf("initial guest theme status=%d body=%s", guest.Code, guest.Body)
	}

	upload := themeUploadRequest(t, api, administrator, validThemeHTTPArchive(t, "Aurora", "1.0.0"), "aurora.zip")
	if upload.Code != http.StatusCreated || !containsAll(upload.Body.String(), `"name":"Aurora"`, `"version":"1.0.0"`, `"can_delete":true`) {
		t.Fatalf("upload theme status=%d body=%s", upload.Code, upload.Body)
	}

	updated := administrator.request(t, api, http.MethodPatch, "/api/v1/admin/themes/Aurora/config", `{
		"revision":1,"theme_color":"blue","background_url":"","font_scale":"large","radius":"pill"
	}`)
	if updated.Code != http.StatusOK || !containsAll(updated.Body.String(), `"revision":2`, `"theme_color":"blue"`, `"font_scale":"large"`) {
		t.Fatalf("update theme status=%d body=%s", updated.Code, updated.Body)
	}
	stale := administrator.request(t, api, http.MethodPatch, "/api/v1/admin/themes/Aurora/config", `{
		"revision":1,"theme_color":"default","background_url":"","font_scale":"normal","radius":"rounded"
	}`)
	expectAPIError(t, stale, http.StatusConflict, "theme_conflict")

	activated := administrator.request(t, api, http.MethodPost, "/api/v1/admin/themes/Aurora/activate", `{"revision":1}`)
	if activated.Code != http.StatusOK || !containsAll(activated.Body.String(), `"active_theme":"Aurora"`, `"revision":2`) {
		t.Fatalf("activate theme status=%d body=%s", activated.Code, activated.Body)
	}
	layout := administrator.request(t, api, http.MethodPut, "/api/v1/admin/themes/layout", `{"revision":2,"sidebar_style":"dark","header_style":"light"}`)
	if layout.Code != http.StatusOK || !containsAll(layout.Body.String(), `"active_theme":"Aurora"`, `"revision":3`, `"sidebar_style":"dark"`, `"header_style":"light"`) {
		t.Fatalf("update theme layout status=%d body=%s", layout.Code, layout.Body)
	}
	guest = plainAPIRequest(api, http.MethodGet, "/api/v1/guest/comm/config", "")
	if !containsAll(guest.Body.String(), `"name":"Aurora"`, `"theme_color":"blue"`, `"font_scale":"large"`, `"sidebar_style":"dark"`, `"header_style":"light"`) {
		t.Fatalf("updated guest theme body=%s", guest.Body)
	}

	catalog := decodeThemeCatalogEnvelope(t, layout)
	var aurora store.Theme
	for _, item := range catalog.Themes {
		if item.Name == "Aurora" {
			aurora = item
		}
	}
	assetPath := "/api/v1/theme-assets/Aurora/" + aurora.PackageSHA256 + "/assets/preview.png"
	asset := plainAPIRequest(api, http.MethodGet, assetPath, "")
	if asset.Code != http.StatusOK || asset.Header().Get("Content-Type") != "image/png" ||
		asset.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || asset.Header().Get("X-Content-Type-Options") != "nosniff" ||
		asset.Header().Get("Content-Security-Policy") != "default-src 'none'; sandbox" || asset.Header().Get("ETag") == "" {
		t.Fatalf("theme asset status=%d headers=%v body=%s", asset.Code, asset.Header(), asset.Body)
	}
	conditionalRequest := httptest.NewRequest(http.MethodGet, assetPath, nil)
	conditionalRequest.Header.Set("If-None-Match", asset.Header().Get("ETag"))
	conditional := httptest.NewRecorder()
	api.ServeHTTP(conditional, conditionalRequest)
	if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 {
		t.Fatalf("conditional theme asset status=%d body=%q", conditional.Code, conditional.Body.String())
	}
	wrongDigest := plainAPIRequest(api, http.MethodGet, "/api/v1/theme-assets/Aurora/"+strings.Repeat("0", 64)+"/assets/preview.png", "")
	if wrongDigest.Code != http.StatusNotFound {
		t.Fatalf("wrong theme digest status=%d body=%s", wrongDigest.Code, wrongDigest.Body)
	}

	legacy := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123")
	legacyList := bearerRequest(api, http.MethodGet, "/api/v2/admin/theme/getThemes", legacy.Authorization, "")
	if legacyList.Code != http.StatusOK || !containsAll(legacyList.Body.String(), `"active":"Aurora"`, `"Aurora":{`, `"name":"Aurora"`, `"can_delete":false`) {
		t.Fatalf("legacy theme list status=%d body=%s", legacyList.Code, legacyList.Body)
	}
	legacyConfig := bearerRequest(api, http.MethodPost, "/api/v2/admin/theme/getThemeConfig", legacy.Authorization, `{"name":"Aurora"}`)
	if legacyConfig.Code != http.StatusOK || !containsAll(legacyConfig.Body.String(), `"theme_color":"blue"`, `"font_scale":"large"`) {
		t.Fatalf("legacy theme config status=%d body=%s", legacyConfig.Code, legacyConfig.Body)
	}
	legacySaved := bearerRequest(api, http.MethodPost, "/api/v2/admin/theme/saveThemeConfig", legacy.Authorization,
		`{"name":"Aurora","config":{"theme_color":"default","background_url":"","custom_html":""}}`)
	if legacySaved.Code != http.StatusOK || !containsAll(legacySaved.Body.String(), `"theme_color":"default"`, `"font_scale":"large"`) {
		t.Fatalf("legacy theme save status=%d body=%s", legacySaved.Code, legacySaved.Body)
	}
	unsafeLegacy := bearerRequest(api, http.MethodPost, "/api/v2/admin/theme/saveThemeConfig", legacy.Authorization,
		`{"name":"Aurora","config":{"custom_html":"<script>alert(1)</script>"}}`)
	if unsafeLegacy.Code != http.StatusUnprocessableEntity || !strings.Contains(unsafeLegacy.Body.String(), `"status":"fail"`) {
		t.Fatalf("unsafe legacy theme save status=%d body=%s", unsafeLegacy.Code, unsafeLegacy.Body)
	}
	legacyActivate := bearerRequest(api, http.MethodPost, "/api/v2/admin/config/save", legacy.Authorization, `{"frontend_theme":"Xboard"}`)
	if legacyActivate.Code != http.StatusOK || !strings.Contains(legacyActivate.Body.String(), `"data":true`) {
		t.Fatalf("legacy theme activation status=%d body=%s", legacyActivate.Code, legacyActivate.Body)
	}

	deleted := administrator.request(t, api, http.MethodDelete, "/api/v1/admin/themes/Aurora", "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete theme status=%d body=%s", deleted.Code, deleted.Body)
	}
	missing := administrator.request(t, api, http.MethodGet, "/api/v1/admin/themes/Aurora/config", "")
	expectAPIError(t, missing, http.StatusNotFound, "theme_not_found")

	audits, err := database.ListAdminAuditLogs(t.Context(), store.AdminAuditFilter{Page: 1, PageSize: 100, Query: "theme"})
	if err != nil || audits.Total < 4 {
		t.Fatalf("theme audits=%#v err=%v", audits, err)
	}
}

func TestThemeUploadRequiresCSRFBeforeParsingOrPersistence(t *testing.T) {
	api, database := newTestAPI(t)
	administrator := loginAdmin(t, api)
	administrator.csrf = ""
	response := themeUploadRequest(t, api, administrator, validThemeHTTPArchive(t, "Blocked", "1.0.0"), "blocked.zip")
	expectAPIError(t, response, http.StatusForbidden, "csrf_failed")
	catalog, err := database.ListThemes(t.Context())
	if err != nil || len(catalog.Themes) != 1 || catalog.ActiveTheme != "Xboard" {
		t.Fatalf("CSRF-rejected upload changed catalog=%#v err=%v", catalog, err)
	}
}

func TestThemeUploadRejectsUnsafeArchiveBeforePersistence(t *testing.T) {
	api, database := newTestAPI(t)
	administrator := loginAdmin(t, api)
	archive := themeHTTPArchive(t, validThemeHTTPManifest("Unsafe", "1.0.0"), map[string][]byte{
		"assets/preview.png": testThemeHTTPPNG(t),
		"../payload.js":      []byte("alert(1)"),
	})
	response := themeUploadRequest(t, api, administrator, archive, "unsafe.zip")
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `"code":"invalid_theme_package"`) {
		t.Fatalf("unsafe upload status=%d body=%s", response.Code, response.Body)
	}
	catalog, err := database.ListThemes(t.Context())
	if err != nil || len(catalog.Themes) != 1 {
		t.Fatalf("unsafe upload persisted catalog=%#v err=%v", catalog, err)
	}
}

func decodeThemeCatalogEnvelope(t *testing.T, response *httptest.ResponseRecorder) store.ThemeCatalog {
	t.Helper()
	var envelope struct {
		Data store.ThemeCatalog `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode theme catalog: %v; body=%s", err, response.Body)
	}
	return envelope.Data
}

func themeUploadRequest(t *testing.T, api http.Handler, client testClient, archive []byte, filename string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(archive); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/themes", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-CSRF-Token", client.csrf)
	client.addCookies(request)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

func validThemeHTTPArchive(t *testing.T, name, version string) []byte {
	return themeHTTPArchive(t, validThemeHTTPManifest(name, version), map[string][]byte{"assets/preview.png": testThemeHTTPPNG(t)})
}

func themeHTTPArchive(t *testing.T, manifest string, files map[string][]byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	manifestFile, _ := writer.Create("manifest.json")
	_, _ = manifestFile.Write([]byte(manifest))
	for name, body := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write(body)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func testThemeHTTPPNG(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 2))
	canvas.Set(0, 0, color.White)
	if err := png.Encode(&output, canvas); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func validThemeHTTPManifest(name, version string) string {
	return `{"format_version":1,"name":"` + name + `","description":"Safe theme","version":"` + version + `",` +
		`"images":["assets/preview.png"],"backgrounds":[],"palettes":{` +
		`"default":{"background":"#111111","surface":"#18181b","text":"#f4f4f5","muted":"#a1a1aa","primary":"#a5b4fc","primary_text":"#111111","border":"#3f3f46"},` +
		`"blue":{"background":"#101827","surface":"#172033","text":"#f4f4f5","muted":"#a1a1aa","primary":"#93c5fd","primary_text":"#111111","border":"#334155"}},` +
		`"default_config":{"theme_color":"default","background_url":"","font_scale":"normal","radius":"rounded"}}`
}
