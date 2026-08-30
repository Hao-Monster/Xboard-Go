package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type clientAppSettingsRequest struct {
	Revision           int64   `json:"revision"`
	WindowsVersion     *string `json:"windows_version"`
	WindowsDownloadURL *string `json:"windows_download_url"`
	MacOSVersion       *string `json:"macos_version"`
	MacOSDownloadURL   *string `json:"macos_download_url"`
	AndroidVersion     *string `json:"android_version"`
	AndroidDownloadURL *string `json:"android_download_url"`
}

func (input clientAppSettingsRequest) complete() bool {
	return input.Revision > 0 && input.WindowsVersion != nil && input.WindowsDownloadURL != nil &&
		input.MacOSVersion != nil && input.MacOSDownloadURL != nil &&
		input.AndroidVersion != nil && input.AndroidDownloadURL != nil
}

func (input clientAppSettingsRequest) storeInput() store.SaveClientAppSettingsInput {
	return store.SaveClientAppSettingsInput{
		WindowsVersion: *input.WindowsVersion, WindowsDownloadURL: *input.WindowsDownloadURL,
		MacOSVersion: *input.MacOSVersion, MacOSDownloadURL: *input.MacOSDownloadURL,
		AndroidVersion: *input.AndroidVersion, AndroidDownloadURL: *input.AndroidDownloadURL,
	}
}

func (s *server) getClientAppSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetClientAppSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, settings)
}

func (s *server) updateClientAppSettings(w http.ResponseWriter, r *http.Request) {
	var input clientAppSettingsRequest
	if !decodeStrictUTF8JSON(w, r, &input) {
		return
	}
	if !input.complete() {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请完整填写客户端应用设置", nil)
		return
	}
	session, _ := sessionFromContext(r.Context())
	settings, err := s.store.UpdateClientAppSettings(r.Context(), session.UserID, input.Revision, input.storeInput(), s.now())
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, http.StatusConflict, "settings_conflict", "设置已被其他管理员修改，请刷新后重试", nil)
		return
	}
	if errors.Is(err, store.ErrInvalidInput) {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请检查客户端版本和 HTTPS 下载地址", nil)
		return
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, settings)
}

func decodeStrictUTF8JSON(w http.ResponseWriter, r *http.Request, target any) bool {
	return decodeStrictUTF8JSONLimit(w, r, target, maxJSONBody)
}

func decodeStrictUTF8JSONLimit(w http.ResponseWriter, r *http.Request, target any, limit int64) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "请求必须使用 application/json", nil)
		return false
	}
	limitedBody := http.MaxBytesReader(w, r.Body, limit)
	body, err := io.ReadAll(limitedBody)
	if closeErr := limitedBody.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", fmt.Sprintf("请求不得超过 %d 字节", limit), nil)
			return false
		}
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式无效", nil)
		return false
	}
	if !utf8.Valid(body) {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式无效", nil)
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return decodeJSON(w, r, target)
}

func (s *server) legacyClientAppVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	token := r.URL.Query().Get("token")
	if token == "" {
		writeClientAppTokenError(w, "token is null")
		return
	}
	requestKey := requestIP(r)
	if !s.subscriptionFailures.allowed(requestKey, s.now()) {
		w.Header().Set("Retry-After", "900")
		writeAPIError(w, http.StatusTooManyRequests, "subscription_rate_limited", "订阅令牌错误次数过多，请稍后重试", nil)
		return
	}
	exists, err := s.store.ClientAppVersionTokenExists(r.Context(), token)
	if err == nil && !exists {
		s.subscriptionFailures.failed(requestKey, s.now())
		writeClientAppTokenError(w, "token is error")
		return
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	settings, err := s.store.GetClientAppSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	userAgent := boundedUTF8(r.UserAgent(), 512)
	if strings.Contains(userAgent, "tidalab/4.0.0") || strings.Contains(userAgent, "tunnelab/4.0.0") {
		version, downloadURL := settings.MacOSVersion, settings.MacOSDownloadURL
		if strings.Contains(userAgent, "Win64") {
			version, downloadURL = settings.WindowsVersion, settings.WindowsDownloadURL
		}
		writeLegacySuccess(w, http.StatusOK, map[string]string{"version": version, "download_url": downloadURL})
		return
	}
	writeLegacySuccess(w, http.StatusOK, legacyClientAppSettings(settings))
}

func writeClientAppTokenError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store, private")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"status":"fail","message":"` + message + `","data":null,"error":null}`))
}

func legacyClientAppSettings(settings store.ClientAppSettings) map[string]string {
	return map[string]string{
		"windows_version": settings.WindowsVersion, "windows_download_url": settings.WindowsDownloadURL,
		"macos_version": settings.MacOSVersion, "macos_download_url": settings.MacOSDownloadURL,
		"android_version": settings.AndroidVersion, "android_download_url": settings.AndroidDownloadURL,
	}
}
