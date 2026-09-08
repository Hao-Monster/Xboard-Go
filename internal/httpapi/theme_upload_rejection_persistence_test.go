package httpapi

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestThemeUploadRejectsInvalidUpgradeWithoutReplacingExistingTheme(t *testing.T) {
	api, database := newTestAPI(t)
	administrator := loginAdmin(t, api)

	installed := themeUploadRequest(t, api, administrator, validThemeHTTPArchive(t, "Aurora", "1.0.0"), "aurora.zip")
	if installed.Code != http.StatusCreated || !containsAll(installed.Body.String(), `"name":"Aurora"`, `"version":"1.0.0"`) {
		t.Fatalf("initial theme upload status=%d body_len=%d", installed.Code, installed.Body.Len())
	}
	configured := administrator.request(t, api, http.MethodPatch, "/api/v1/admin/admin/themes/Aurora/config", `{
		"revision":1,"theme_color":"blue","background_url":"","font_scale":"large","radius":"pill"
	}`)
	if configured.Code != http.StatusOK || !containsAll(configured.Body.String(), `"revision":2`, `"theme_color":"blue"`, `"font_scale":"large"`, `"radius":"pill"`) {
		t.Fatalf("theme config status=%d body_len=%d", configured.Code, configured.Body.Len())
	}

	beforeSQLite, err := database.GetTheme(t.Context(), "Aurora")
	if err != nil {
		t.Fatalf("read SQLite theme before invalid upgrade: %v", err)
	}
	beforeAPI := administrator.request(t, api, http.MethodGet, "/api/v1/admin/admin/themes/Aurora/config", "")
	if beforeAPI.Code != http.StatusOK {
		t.Fatalf("read API theme before invalid upgrade status=%d body_len=%d", beforeAPI.Code, beforeAPI.Body.Len())
	}
	assertThemeEqual(t, beforeSQLite, decodeThemeEnvelope(t, beforeAPI), "before API and SQLite")

	assetPath := "/api/v1/theme-assets/Aurora/" + beforeSQLite.PackageSHA256 + "/assets/preview.png"
	beforeAsset := plainAPIRequest(api, http.MethodGet, assetPath, "")
	assertThemeAssetResponse(t, beforeAsset, "before invalid upgrade")
	beforeAssetBytes := append([]byte(nil), beforeAsset.Body.Bytes()...)
	beforeAssetSHA256 := sha256Hex(beforeAssetBytes)
	if beforeAsset.Header().Get("ETag") != `"`+beforeAssetSHA256+`"` {
		t.Fatalf("before invalid upgrade asset ETag=%q, want %q", beforeAsset.Header().Get("ETag"), `"`+beforeAssetSHA256+`"`)
	}

	invalidUpgrade := themeUploadRequest(t, api, administrator, normalizedThemeDirectoryFileCollisionArchive(t), "aurora-upgrade.zip")
	if invalidUpgrade.Code != http.StatusUnprocessableEntity || !containsAll(invalidUpgrade.Body.String(), `"code":"invalid_theme_package"`) {
		t.Fatalf("invalid upgrade status=%d body_len=%d has_invalid_theme_package=%t", invalidUpgrade.Code, invalidUpgrade.Body.Len(), bytes.Contains(invalidUpgrade.Body.Bytes(), []byte(`"code":"invalid_theme_package"`)))
	}

	afterSQLite, err := database.GetTheme(t.Context(), "Aurora")
	if err != nil {
		t.Fatalf("read SQLite theme after invalid upgrade: %v", err)
	}
	assertThemeEqual(t, beforeSQLite, afterSQLite, "SQLite after invalid upgrade")
	if afterSQLite.Version != "1.0.0" || afterSQLite.PackageSHA256 != beforeSQLite.PackageSHA256 || afterSQLite.Config != beforeSQLite.Config {
		t.Fatalf("invalid upgrade changed version/digest/config: before={version=%q digest=%q revision=%d config=%+v} after={version=%q digest=%q revision=%d config=%+v}", beforeSQLite.Version, beforeSQLite.PackageSHA256, beforeSQLite.Revision, beforeSQLite.Config, afterSQLite.Version, afterSQLite.PackageSHA256, afterSQLite.Revision, afterSQLite.Config)
	}

	afterAPI := administrator.request(t, api, http.MethodGet, "/api/v1/admin/admin/themes/Aurora/config", "")
	if afterAPI.Code != http.StatusOK {
		t.Fatalf("read API theme after invalid upgrade status=%d body_len=%d", afterAPI.Code, afterAPI.Body.Len())
	}
	assertThemeEqual(t, beforeSQLite, decodeThemeEnvelope(t, afterAPI), "API after invalid upgrade")

	afterAsset := plainAPIRequest(api, http.MethodGet, assetPath, "")
	assertThemeAssetResponse(t, afterAsset, "after invalid upgrade")
	afterAssetBytes := afterAsset.Body.Bytes()
	if !bytes.Equal(beforeAssetBytes, afterAssetBytes) || sha256Hex(afterAssetBytes) != beforeAssetSHA256 || afterAsset.Header().Get("ETag") != beforeAsset.Header().Get("ETag") {
		t.Fatalf("invalid upgrade changed asset bytes or digest: before_sha=%s after_sha=%s before_etag=%q after_etag=%q", beforeAssetSHA256, sha256Hex(afterAssetBytes), beforeAsset.Header().Get("ETag"), afterAsset.Header().Get("ETag"))
	}
}

func decodeThemeEnvelope(t *testing.T, response *httptest.ResponseRecorder) store.Theme {
	t.Helper()
	var envelope struct {
		Data store.Theme `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode theme response: %v", err)
	}
	return envelope.Data
}

func assertThemeEqual(t *testing.T, want, got store.Theme, label string) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("%s theme changed: want=%s got=%s", label, themeSummary(want), themeSummary(got))
	}
}

func assertThemeAssetResponse(t *testing.T, response *httptest.ResponseRecorder, label string) {
	t.Helper()
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" || response.Header().Get("ETag") == "" {
		t.Fatalf("%s asset response status=%d content_type=%q etag=%q body_len=%d", label, response.Code, response.Header().Get("Content-Type"), response.Header().Get("ETag"), response.Body.Len())
	}
}

func themeSummary(item store.Theme) string {
	return fmt.Sprintf("{name=%q version=%q digest=%q revision=%d config=%+v images=%d backgrounds=%d palettes=%d active=%t system=%t can_delete=%t}", item.Name, item.Version, item.PackageSHA256, item.Revision, item.Config, len(item.Images), len(item.Backgrounds), len(item.Palettes), item.IsActive, item.IsSystem, item.CanDelete)
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func normalizedThemeDirectoryFileCollisionArchive(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	manifest, err := writer.Create("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Write([]byte(validThemeHTTPManifest("Aurora", "2.0.0"))); err != nil {
		t.Fatal(err)
	}
	directory := &zip.FileHeader{Name: "assets/preview.png/", Method: zip.Store}
	directory.SetMode(os.ModeDir | 0o755)
	if _, err := writer.CreateHeader(directory); err != nil {
		t.Fatal(err)
	}
	preview, err := writer.Create("assets/preview.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := preview.Write(testThemeHTTPPNG(t)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
