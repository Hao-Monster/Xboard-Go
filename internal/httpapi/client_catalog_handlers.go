package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/clientcatalog"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) listAdminClientCatalog(w http.ResponseWriter, r *http.Request) {
	catalog, err := s.clientCatalog.AdminCatalog(r.Context())
	if err != nil {
		handleClientCatalogError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, catalog)
}

func (s *server) saveClientCatalog(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Revision int64                       `json:"revision"`
		Links    clientcatalog.OverrideInput `json:"links"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Revision <= 0 || input.Links == nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "revision 必须为正整数且 links 必须存在", nil)
		return
	}
	catalog, err := s.clientCatalog.SaveOverrides(r.Context(), input.Revision, input.Links)
	if err != nil {
		handleClientCatalogError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, catalog)
}

func (s *server) listUserClientCatalog(w http.ResponseWriter, r *http.Request) {
	catalog, err := s.clientCatalog.UserCatalog(r.Context())
	if err != nil {
		handleClientCatalogError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, catalog)
}

func (s *server) clientCatalogQR(w http.ResponseWriter, r *http.Request) {
	clientID := strings.TrimSpace(r.URL.Query().Get("client"))
	platform := strings.TrimSpace(r.URL.Query().Get("platform"))
	if clientID == "" || platform == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_query", "client 和 platform 为必填项", nil)
		return
	}
	downloadURL, qrCode, err := s.clientCatalog.QRData(clientID, platform)
	if err != nil {
		handleClientCatalogError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, map[string]string{"download_url": downloadURL, "qr_code": qrCode})
}

func (s *server) clientDownloadRedirect(w http.ResponseWriter, r *http.Request) {
	s.clientCatalogRedirect(w, r, "direct")
}

func (s *server) clientActionRedirect(w http.ResponseWriter, r *http.Request) {
	s.clientCatalogRedirect(w, r, r.PathValue("action"))
}

func (s *server) clientCatalogRedirect(w http.ResponseWriter, r *http.Request, action string) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	address, err := s.clientCatalog.Resolve(r.Context(), r.PathValue("clientID"), r.PathValue("platform"), action)
	if err != nil {
		handleClientCatalogError(w, err)
		return
	}
	http.Redirect(w, r, address, http.StatusFound)
}

func handleClientCatalogError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, clientcatalog.ErrInvalid), errors.Is(err, store.ErrInvalidInput):
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), nil)
	case errors.Is(err, store.ErrConflict):
		writeAPIError(w, http.StatusConflict, "client_catalog_conflict", "客户端配置已被其他操作修改，请刷新后重试", nil)
	case errors.Is(err, clientcatalog.ErrNotFound), errors.Is(err, store.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "client_catalog_not_found", "客户端、平台或动作不存在", nil)
	case errors.Is(err, clientcatalog.ErrUnavailable):
		w.Header().Set("Retry-After", "30")
		writeAPIError(w, http.StatusServiceUnavailable, "client_download_unavailable", "暂时无法获取客户端安装包，请稍后重试", nil)
	default:
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
	}
}
