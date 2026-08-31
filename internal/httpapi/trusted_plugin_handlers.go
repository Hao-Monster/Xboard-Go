package httpapi

import (
	"errors"
	"net/http"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

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
	if !decodeJSON(w, r, &input) {
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
