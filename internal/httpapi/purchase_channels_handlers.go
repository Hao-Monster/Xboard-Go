package httpapi

import (
	"errors"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"net/http"
)

type purchaseChannelsRequest struct {
	Revision             int64   `json:"revision"`
	CardStoreURL         *string `json:"card_store_url"`
	ChatwootBaseURL      *string `json:"chatwoot_base_url"`
	ChatwootWebsiteToken *string `json:"chatwoot_website_token"`
}

func (s *server) getPurchaseChannels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	result, err := s.store.GetPurchaseChannels(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

func (s *server) updatePurchaseChannels(w http.ResponseWriter, r *http.Request) {
	var input purchaseChannelsRequest
	if !decodeStrictUTF8JSON(w, r, &input) {
		return
	}
	if input.Revision < 1 || input.CardStoreURL == nil || input.ChatwootBaseURL == nil || input.ChatwootWebsiteToken == nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请完整填写购买渠道设置", nil)
		return
	}
	session, _ := sessionFromContext(r.Context())
	result, err := s.store.UpdatePurchaseChannels(r.Context(), session.UserID, store.PurchaseChannels{Revision: input.Revision, CardStoreURL: *input.CardStoreURL, ChatwootBaseURL: *input.ChatwootBaseURL, ChatwootWebsiteToken: *input.ChatwootWebsiteToken}, s.now())
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, http.StatusConflict, "settings_conflict", "设置已被其他管理员修改，请刷新后重试", nil)
		return
	}
	if errors.Is(err, store.ErrInvalidInput) {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "卡网地址须为 HTTPS；客服须同时填写 HTTPS 站点源地址和有效的网站令牌", nil)
		return
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}
