package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const trustedPluginRequestLimit = 64 * 1024

type updateTrustedPluginRequest struct {
	Revision int64          `json:"revision"`
	Enabled  bool           `json:"enabled"`
	Config   map[string]any `json:"config"`
}

func (s *server) listTrustedPlugins(w http.ResponseWriter, r *http.Request) {
	plugins, err := s.store.ListTrustedPlugins(r.Context())
	if err != nil {
		handleTrustedPluginError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, plugins)
}

func (s *server) updateTrustedPlugin(w http.ResponseWriter, r *http.Request) {
	var input updateTrustedPluginRequest
	if !decodeTrustedPluginJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	plugin, err := s.store.UpdateTrustedPlugin(r.Context(), session.UserID, r.PathValue("code"), input.Revision, store.SaveTrustedPluginInput{
		Enabled: input.Enabled,
		Config:  input.Config,
	}, s.now())
	if err != nil {
		handleTrustedPluginError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, plugin)
}

func decodeTrustedPluginJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "请求必须使用 application/json", nil)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, trustedPluginRequestLimit)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", "插件请求不得超过 65536 字节", nil)
			return false
		}
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式无效", nil)
		return false
	}
	if !utf8.Valid(payload) {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求必须使用有效 UTF-8", nil)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式无效", nil)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求只能包含一个 JSON 对象", nil)
		return false
	}
	return true
}

func handleTrustedPluginError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "plugin_not_found", "插件不存在", nil)
	case errors.Is(err, store.ErrRevisionConflict):
		writeAPIError(w, http.StatusConflict, "plugin_revision_conflict", "插件配置已被其他管理员修改，请刷新后重试", nil)
	case errors.Is(err, store.ErrInvalidInput):
		writeAPIError(w, http.StatusUnprocessableEntity, "plugin_validation_failed", "插件配置参数无效", nil)
	default:
		handleStoreError(w, err)
	}
}
