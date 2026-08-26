package httpapi

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/Hao-Monster/Xboard-Go/internal/subscription"
)

const maxSubscriptionResponseBytes = 16 << 20

func (s *server) clientSubscription(w http.ResponseWriter, r *http.Request) {
	client := subscription.DetectClient(boundedUTF8(r.URL.Query().Get("flag"), 256), boundedUTF8(r.UserAgent(), 512))
	s.serveClientSubscription(w, r, r.URL.Query().Get("token"), nil, client)
}

func (s *server) dynamicClientSubscription(w http.ResponseWriter, r *http.Request) {
	client := subscription.DetectClient(boundedUTF8(r.URL.Query().Get("flag"), 256), boundedUTF8(r.UserAgent(), 512))
	config, err := s.store.GetSubscriptionRenderConfig(r.Context(), subscriptionRenderTemplate(client.Kind))
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if r.PathValue("subscriptionPath") != config.Path {
		w.Header().Set("Cache-Control", "no-store, private")
		http.NotFound(w, r)
		return
	}
	s.serveClientSubscription(w, r, r.PathValue("subscriptionToken"), &config, client)
}

func (s *server) serveClientSubscription(w http.ResponseWriter, r *http.Request, token string, loadedConfig *store.SubscriptionRenderConfig, client subscription.ClientInfo) {
	w.Header().Set("Cache-Control", "no-cache, private")
	if token == "" {
		writeSubscriptionTokenError(w, http.StatusForbidden, "token is null")
		return
	}
	requestKey := requestIP(r)
	if !s.subscriptionFailures.allowed(requestKey, s.now()) {
		w.Header().Set("Cache-Control", "no-store, private")
		w.Header().Set("Retry-After", "900")
		writeAPIError(w, http.StatusTooManyRequests, "subscription_rate_limited", "订阅令牌错误次数过多，请稍后重试", nil)
		return
	}
	account, err := s.store.FindSubscriptionAccount(r.Context(), token)
	if errors.Is(err, store.ErrNotFound) {
		s.subscriptionFailures.failed(requestKey, s.now())
		writeSubscriptionTokenError(w, http.StatusForbidden, "token is error")
		return
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Allow", http.MethodGet)
		w.Header().Set("Cache-Control", "no-store, private")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if subscriptionPrefetch(r) {
		w.Header().Set("Cache-Control", "no-store, private")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusTooEarly)
		return
	}
	if !account.AvailableAt(s.now()) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		return
	}
	hwid, err := s.store.AuthorizeDistributorHWID(r.Context(), store.AuthorizeDistributorHWIDInput{
		SubscriberUserID: account.ID, HWID: r.Header.Get("x-hwid"), DeviceOS: r.Header.Get("x-device-os"),
		OSVersion: r.Header.Get("x-ver-os"), DeviceModel: r.Header.Get("x-device-model"),
		UserAgent: r.UserAgent(), IPAddress: requestIP(r),
	}, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	setDistributorHWIDHeaders(w, hwid)
	if !hwid.Allowed {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if hwid.SubscriptionID > 0 {
		if err := s.store.MarkDistributorSubscriptionClaimed(r.Context(), hwid.SubscriptionID, requestIP(r), r.UserAgent(), s.now()); err != nil {
			handleStoreError(w, err)
			return
		}
	}

	config := store.SubscriptionRenderConfig{}
	if loadedConfig != nil {
		config = *loadedConfig
	} else {
		config, err = s.store.GetSubscriptionRenderConfig(r.Context(), subscriptionRenderTemplate(client.Kind))
		if err != nil {
			handleStoreError(w, err)
			return
		}
	}

	var source []store.SubscriptionNode
	if account.GroupID != nil {
		source, err = s.store.ListSubscriptionNodes(r.Context(), *account.GroupID)
		if err != nil {
			handleStoreError(w, err)
			return
		}
	}
	prepared, err := subscription.PrepareNodes(account, source, subscription.PrepareOptions{Now: s.now()})
	if err != nil {
		s.logger.Error("prepare subscription", "user_id", account.ID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	types := r.URL.Query().Get("types")
	if len(types) > 1_024 {
		types = ""
	}
	filtered := subscription.FilterNodes(prepared, types, r.URL.Query().Get("filter"))
	filtered = subscription.PresentNodes(account, filtered, subscription.PrepareOptions{
		ShowInfo: config.ShowInfo, ShowProtocol: config.ShowProtocol,
		RejectedByRequestFilter: len(prepared) - len(filtered), NextResetAt: account.NextResetAt, Now: s.now(),
	})
	appURL := strings.TrimRight(config.AppURL, "/")
	if appURL == "" {
		appURL = s.panelURL
	}
	subscriptionURL := appURL + "/" + config.Path + "/" + token
	response, err := subscription.Render(subscription.RenderInput{
		Account: account, Nodes: filtered, Client: client, AppName: config.AppName, AppURL: appURL,
		RequestHost: r.Host, SubscriptionURL: subscriptionURL, Templates: config.Templates,
	})
	if err != nil {
		s.logger.Error("render subscription", "user_id", account.ID, "client", client.Kind.String(), "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	if len(response.Body) > maxSubscriptionResponseBytes {
		s.logger.Warn("subscription response exceeds limit", "user_id", account.ID, "client", client.Kind.String(), "bytes", len(response.Body))
		writeAPIError(w, http.StatusServiceUnavailable, "subscription_too_large", "订阅内容过大，请联系管理员", nil)
		return
	}
	switch response.ContentType {
	case "application/json":
		w.Header().Set("Content-Type", "application/json")
	case "text/plain; charset=utf-8":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	case "text/yaml; charset=utf-8":
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	case "application/octet-stream":
		w.Header().Set("Content-Type", "application/octet-stream")
	default:
		s.logger.Error("reject unsafe subscription media type", "user_id", account.ID, "client", client.Kind.String(), "content_type", response.ContentType)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	// Subscription templates are administrator-controlled downloads. A sandbox
	// remains defense in depth in addition to non-HTML media types and nosniff.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'; sandbox")
	for name, value := range response.Headers {
		w.Header().Set(name, value)
	}
	if hwid.SubscriptionID > 0 {
		if err := s.store.MarkDistributorConfigIssued(r.Context(), hwid.SubscriptionID, s.now()); err != nil {
			handleStoreError(w, err)
			return
		}
		if hwid.OriginalTradeNo != "" {
			title := "订单号：" + hwid.OriginalTradeNo
			w.Header().Set("profile-title", "base64:"+base64.StdEncoding.EncodeToString([]byte(title)))
			w.Header().Set("x-order-no", hwid.OriginalTradeNo)
			w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+hwid.OriginalTradeNo)
		}
	}
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(response.Body); err != nil {
		s.logger.Debug("write subscription response", "user_id", account.ID, "client", client.Kind.String(), "error", err)
	}
}

func setDistributorHWIDHeaders(w http.ResponseWriter, result store.DistributorHWIDAuthorization) {
	if !result.Enabled {
		return
	}
	w.Header().Set("x-hwid-active", "true")
	if result.NotSupported {
		w.Header().Set("x-hwid-not-supported", "true")
	}
	if result.LimitReached {
		w.Header().Set("x-hwid-max-devices-reached", "true")
		w.Header().Set("x-hwid-limit", "true")
	}
}

func subscriptionRenderTemplate(kind subscription.Kind) string {
	switch kind {
	case subscription.KindClash, subscription.KindClashMeta, subscription.KindSingBox, subscription.KindSurge,
		subscription.KindStash, subscription.KindSurfboard:
		return kind.String()
	default:
		return ""
	}
}

func subscriptionPrefetch(r *http.Request) bool {
	purpose := r.Header.Get("Sec-Purpose")
	if purpose == "" {
		purpose = r.Header.Get("Purpose")
	}
	return strings.Contains(strings.ToLower(purpose), "prefetch")
}

func writeSubscriptionTokenError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, private")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"status":"fail","message":"` + message + `","data":null,"error":null}`))
}

func boundedUTF8(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
