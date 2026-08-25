package httpapi

import (
	"net/http"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const maxSubscriptionSettingsBody = 16 << 20

func (s *server) getSubscriptionSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetSubscriptionSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, settings)
}

func (s *server) updateSubscriptionSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Revision     int64             `json:"revision"`
		Path         string            `json:"path"`
		ShowInfo     bool              `json:"show_info"`
		ShowProtocol bool              `json:"show_protocol"`
		Templates    map[string]string `json:"templates"`
	}
	if !decodeJSONLimit(w, r, &input, maxSubscriptionSettingsBody) {
		return
	}
	session, ok := sessionFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "请先登录", nil)
		return
	}
	settings, err := s.store.UpdateSubscriptionSettings(r.Context(), session.UserID, input.Revision, store.SaveSubscriptionSettingsInput{
		Path: input.Path, ShowInfo: input.ShowInfo, ShowProtocol: input.ShowProtocol, Templates: input.Templates,
	}, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, settings)
}
