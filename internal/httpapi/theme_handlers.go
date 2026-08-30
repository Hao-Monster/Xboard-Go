package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/Hao-Monster/Xboard-Go/internal/theme"
)

const maxThemeUploadBody = theme.MaxArchiveBytes + 64<<10

var themeArchiveFilenamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}\.[zZ][iI][pP]$`)

type themeConfigRequest struct {
	Revision      int64  `json:"revision"`
	ThemeColor    string `json:"theme_color"`
	BackgroundURL string `json:"background_url"`
	FontScale     string `json:"font_scale"`
	Radius        string `json:"radius"`
}

func (s *server) listThemes(w http.ResponseWriter, r *http.Request) {
	catalog, err := s.store.ListThemes(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, catalog)
}

func (s *server) updateThemeLayout(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Revision     int64  `json:"revision"`
		SidebarStyle string `json:"sidebar_style"`
		HeaderStyle  string `json:"header_style"`
	}
	if !decodeStrictUTF8JSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	catalog, err := s.store.UpdateThemeLayoutSettings(r.Context(), session.UserID, input.Revision, input.SidebarStyle, input.HeaderStyle, s.now())
	if writeThemeStoreError(w, err) {
		return
	}
	writeSuccess(w, http.StatusOK, catalog)
}

func (s *server) uploadTheme(w http.ResponseWriter, r *http.Request) {
	archive, err := readThemeUpload(w, r)
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_theme_package", err.Error(), nil)
		return
	}
	packaged, err := theme.ParseArchive(archive)
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_theme_package", "主题包不符合安全的声明式主题格式", nil)
		return
	}
	session, _ := sessionFromContext(r.Context())
	installed, err := s.store.InstallTheme(r.Context(), session.UserID, packaged, s.now())
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, http.StatusConflict, "theme_conflict", "同名主题已存在，升级包版本必须更高", nil)
		return
	}
	if errors.Is(err, store.ErrInvalidInput) {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_theme_package", "主题包不符合安全的声明式主题格式", nil)
		return
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusCreated, installed)
}

func (s *server) getThemeConfig(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetTheme(r.Context(), r.PathValue("name"))
	if writeThemeStoreError(w, err) {
		return
	}
	writeSuccess(w, http.StatusOK, item)
}

func (s *server) updateThemeConfig(w http.ResponseWriter, r *http.Request) {
	var input themeConfigRequest
	if !decodeStrictUTF8JSON(w, r, &input) {
		return
	}
	if input.Revision < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "主题配置版本无效", nil)
		return
	}
	session, _ := sessionFromContext(r.Context())
	updated, err := s.store.UpdateThemeConfig(r.Context(), session.UserID, r.PathValue("name"), input.Revision, theme.Config{
		ThemeColor: input.ThemeColor, BackgroundURL: input.BackgroundURL, FontScale: input.FontScale, Radius: input.Radius,
	}, s.now())
	if writeThemeStoreError(w, err) {
		return
	}
	writeSuccess(w, http.StatusOK, updated)
}

func (s *server) activateTheme(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Revision int64 `json:"revision"`
	}
	if !decodeStrictUTF8JSON(w, r, &input) {
		return
	}
	if input.Revision < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "活动主题版本无效", nil)
		return
	}
	session, _ := sessionFromContext(r.Context())
	catalog, err := s.store.ActivateTheme(r.Context(), session.UserID, r.PathValue("name"), input.Revision, s.now())
	if writeThemeStoreError(w, err) {
		return
	}
	writeSuccess(w, http.StatusOK, catalog)
}

func (s *server) deleteTheme(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if err := s.store.DeleteTheme(r.Context(), session.UserID, r.PathValue("name"), s.now()); writeThemeStoreError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) getThemeAsset(w http.ResponseWriter, r *http.Request) {
	asset, err := s.store.GetThemeAsset(r.Context(), r.PathValue("name"), r.PathValue("digest"), r.PathValue("path"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	etag := `"` + asset.SHA256 + `"`
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Type", asset.MIME)
	w.Header().Set("Content-Length", strconv.Itoa(len(asset.Data)))
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(asset.Data)
}

func (s *server) legacyListThemes(w http.ResponseWriter, r *http.Request) {
	catalog, err := s.store.ListThemes(r.Context())
	if err != nil {
		writeLegacyThemeError(w, err)
		return
	}
	themes := make(map[string]any, len(catalog.Themes))
	for _, item := range catalog.Themes {
		themes[item.Name] = legacyThemeDefinition(item)
	}
	writeLegacySuccess(w, http.StatusOK, map[string]any{"themes": themes, "active": catalog.ActiveTheme})
}

func (s *server) legacyUploadTheme(w http.ResponseWriter, r *http.Request) {
	archive, err := readThemeUpload(w, r)
	if err != nil {
		writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "主题包格式无效")
		return
	}
	packaged, err := theme.ParseArchive(archive)
	if err != nil {
		writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "主题包不符合安全的声明式主题格式")
		return
	}
	session, _ := sessionFromContext(r.Context())
	if _, err := s.store.InstallTheme(r.Context(), session.UserID, packaged, s.now()); err != nil {
		writeLegacyThemeError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func (s *server) legacyDeleteTheme(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !decodeStrictUTF8JSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if err := s.store.DeleteTheme(r.Context(), session.UserID, input.Name, s.now()); err != nil {
		writeLegacyThemeError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func (s *server) legacyGetThemeConfig(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !decodeStrictUTF8JSON(w, r, &input) {
		return
	}
	item, err := s.store.GetTheme(r.Context(), input.Name)
	if err != nil {
		writeLegacyThemeError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, item.Config)
}

func (s *server) legacySaveThemeConfig(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name   string `json:"name"`
		Config struct {
			ThemeColor    *string `json:"theme_color"`
			BackgroundURL *string `json:"background_url"`
			FontScale     *string `json:"font_scale"`
			Radius        *string `json:"radius"`
			CustomHTML    *string `json:"custom_html"`
		} `json:"config"`
	}
	if !decodeStrictUTF8JSON(w, r, &input) {
		return
	}
	if input.Config.CustomHTML != nil && strings.TrimSpace(*input.Config.CustomHTML) != "" {
		writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "出于安全原因不支持自定义 HTML 或脚本")
		return
	}
	current, err := s.store.GetTheme(r.Context(), input.Name)
	if err != nil {
		writeLegacyThemeError(w, err)
		return
	}
	config := current.Config
	if input.Config.ThemeColor != nil {
		config.ThemeColor = *input.Config.ThemeColor
	}
	if input.Config.BackgroundURL != nil {
		config.BackgroundURL = *input.Config.BackgroundURL
	}
	if input.Config.FontScale != nil {
		config.FontScale = *input.Config.FontScale
	}
	if input.Config.Radius != nil {
		config.Radius = *input.Config.Radius
	}
	session, _ := sessionFromContext(r.Context())
	updated, err := s.store.UpdateThemeConfig(r.Context(), session.UserID, current.Name, current.Revision, config, s.now())
	if err != nil {
		writeLegacyThemeError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, updated.Config)
}

func legacyThemeDefinition(item store.Theme) map[string]any {
	paletteOptions := make(map[string]string, len(item.Palettes))
	for key := range item.Palettes {
		paletteOptions[key] = key
	}
	return map[string]any{
		"name": item.Name, "description": item.Description, "version": item.Version,
		"images": item.Images, "can_delete": item.CanDelete, "is_system": item.IsSystem,
		"configs": []map[string]any{
			{"label": "主题色", "field_name": "theme_color", "field_type": "select", "select_options": paletteOptions, "default_value": item.Config.ThemeColor},
			{"label": "背景", "field_name": "background_url", "field_type": "select", "select_options": item.Backgrounds, "default_value": item.Config.BackgroundURL},
			{"label": "字号", "field_name": "font_scale", "field_type": "select", "select_options": []string{"small", "normal", "large"}, "default_value": item.Config.FontScale},
			{"label": "圆角", "field_name": "radius", "field_type": "select", "select_options": []string{"compact", "rounded", "pill"}, "default_value": item.Config.Radius},
		},
	}
}

func readThemeUpload(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxThemeUploadBody)
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, errors.New("请求必须上传 ZIP 主题包")
	}
	var archive []byte
	parts := 0
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.New("主题包上传格式无效")
		}
		parts++
		if parts > 4 {
			_ = part.Close()
			return nil, errors.New("主题包上传字段过多")
		}
		if part.FormName() != "file" || archive != nil || !themeArchiveFilenamePattern.MatchString(filepath.Base(part.FileName())) || filepath.Base(part.FileName()) != part.FileName() {
			_ = part.Close()
			return nil, errors.New("请选择名称安全的 ZIP 主题包")
		}
		body, readErr := io.ReadAll(io.LimitReader(part, theme.MaxArchiveBytes+1))
		closeErr := part.Close()
		if readErr != nil || closeErr != nil || len(body) == 0 || len(body) > theme.MaxArchiveBytes {
			return nil, fmt.Errorf("主题包大小不得超过 %d MiB", theme.MaxArchiveBytes>>20)
		}
		archive = body
	}
	if archive == nil {
		return nil, errors.New("请选择 ZIP 主题包")
	}
	return archive, nil
}

func writeThemeStoreError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "theme_not_found", "主题不存在", nil)
	case errors.Is(err, store.ErrConflict):
		writeAPIError(w, http.StatusConflict, "theme_conflict", "主题已被其他管理员修改，或当前状态不允许该操作", nil)
	case errors.Is(err, store.ErrInvalidInput):
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "主题配置无效", nil)
	default:
		handleStoreError(w, err)
	}
	return true
}

func writeLegacyThemeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeLegacyInviteFailure(w, http.StatusNotFound, "主题不存在")
	case errors.Is(err, store.ErrConflict):
		writeLegacyInviteFailure(w, http.StatusConflict, "主题已被其他管理员修改，或当前状态不允许该操作")
	case errors.Is(err, store.ErrInvalidInput):
		writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "主题配置无效")
	default:
		writeLegacyInviteFailure(w, http.StatusInternalServerError, "主题操作失败")
	}
}

func themeAssetURL(name, digest, assetPath string) string {
	if assetPath == "" {
		return ""
	}
	segments := strings.Split(assetPath, "/")
	for index := range segments {
		segments[index] = urlPathEscape(segments[index])
	}
	return "/api/v1/theme-assets/" + urlPathEscape(name) + "/" + digest + "/" + strings.Join(segments, "/")
}

func urlPathEscape(value string) string {
	return url.PathEscape(value)
}
