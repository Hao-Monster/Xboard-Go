package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/clientcatalog"
	"github.com/Hao-Monster/Xboard-Go/internal/payment"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type savePaymentRequest struct {
	Revision               int64                 `json:"revision,omitempty"`
	Provider               store.PaymentProvider `json:"payment"`
	Name                   string                `json:"name"`
	Icon                   string                `json:"icon,omitempty"`
	NotifyDomain           string                `json:"notify_domain,omitempty"`
	HandlingFeeFixed       int64                 `json:"handling_fee_fixed"`
	HandlingFeeBasisPoints int64                 `json:"handling_fee_basis_points"`
	Enabled                bool                  `json:"enable"`
	Config                 map[string]string     `json:"config"`
	ClearConfigFields      []string              `json:"clear_config_fields,omitempty"`
}

type adminPaymentResponse struct {
	store.Payment
	Config           map[string]string `json:"config"`
	ConfiguredFields []string          `json:"configured_fields"`
	NotifyURL        string            `json:"notify_url"`
}

type userPaymentResponse struct {
	ID                     int64                 `json:"id"`
	Name                   string                `json:"name"`
	Provider               store.PaymentProvider `json:"payment"`
	Icon                   string                `json:"icon,omitempty"`
	HandlingFeeFixed       int64                 `json:"handling_fee_fixed"`
	HandlingFeeBasisPoints int64                 `json:"handling_fee_basis_points"`
}

type paymentCheckoutResponse struct {
	Type           int    `json:"type"`
	Data           string `json:"data"`
	QRCode         string `json:"qr_code,omitempty"`
	PaymentID      int64  `json:"payment_id"`
	HandlingAmount int64  `json:"handling_amount"`
	TotalAmount    int64  `json:"total_amount"`
}

func (s *server) checkoutPayment(ctx context.Context, userID int64, tradeNo string, paymentID int64) (paymentCheckoutResponse, error) {
	started, err := s.store.StartPaymentCheckout(ctx, store.StartPaymentCheckoutInput{UserID: userID, TradeNo: tradeNo, PaymentID: paymentID}, s.now())
	if err != nil {
		return paymentCheckoutResponse{}, err
	}
	handling := int64(0)
	if started.Order.HandlingAmount != nil {
		handling = *started.Order.HandlingAmount
	}
	if started.Cached {
		if started.Attempt.ResponseType == nil {
			return paymentCheckoutResponse{}, store.ErrConflict
		}
		return paymentCheckoutResponseOf(*started.Attempt.ResponseType, started.Attempt.ResponseData,
			started.Payment.ID, handling, started.Attempt.ExpectedAmount), nil
	}
	if s.settingsCipher == nil {
		_ = s.store.FailPaymentCheckout(ctx, started.Attempt.ID, started.Attempt.IdempotencyKey, "encryption_unavailable", s.now())
		return paymentCheckoutResponse{}, errPaymentEncryptionUnavailable
	}
	config, err := payment.OpenConfig(s.settingsCipher, started.Payment.Provider, started.Payment.ConfigCiphertext)
	if err != nil {
		_ = s.store.FailPaymentCheckout(ctx, started.Attempt.ID, started.Attempt.IdempotencyKey, "config_unavailable", s.now())
		return paymentCheckoutResponse{}, err
	}
	result, err := s.paymentGateway.Checkout(ctx, payment.CheckoutRequest{
		Provider: started.Payment.Provider, Config: config, TradeNo: started.Order.TradeNo,
		Amount: started.Attempt.ExpectedAmount, Currency: started.Attempt.Currency,
		NotifyURL: s.paymentNotifyURL(started.Payment), ReturnURL: s.panelURL + "/#/order/" + started.Order.TradeNo,
		IdempotencyKey: started.Attempt.IdempotencyKey,
	})
	if err != nil {
		_ = s.store.FailPaymentCheckout(ctx, started.Attempt.ID, started.Attempt.IdempotencyKey, "provider_error", s.now())
		return paymentCheckoutResponse{}, fmt.Errorf("%w: %v", errPaymentProviderFailed, err)
	}
	completed, err := s.store.CompletePaymentCheckout(ctx, started.Attempt.ID, started.Attempt.IdempotencyKey, result.Type, result.Data, result.ExternalID, s.now())
	if err != nil {
		return paymentCheckoutResponse{}, err
	}
	return paymentCheckoutResponseOf(result.Type, result.Data, started.Payment.ID, handling, completed.ExpectedAmount), nil
}

func paymentCheckoutResponseOf(responseType int, data string, paymentID, handlingAmount, totalAmount int64) paymentCheckoutResponse {
	response := paymentCheckoutResponse{Type: responseType, Data: data, PaymentID: paymentID, HandlingAmount: handlingAmount, TotalAmount: totalAmount}
	if responseType == 0 {
		response.QRCode, _ = clientcatalog.QRDataURL(data)
	}
	return response
}

func (s *server) paymentWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.paymentWebhookRequests.take(requestIP(r), s.now()) {
		w.Header().Set("Retry-After", "60")
		writePaymentWebhookFailure(w, http.StatusTooManyRequests)
		return
	}
	provider := store.PaymentProvider(r.PathValue("method"))
	method, err := s.store.GetPaymentByUUID(r.Context(), r.PathValue("uuid"))
	if err != nil || method.Provider != provider || s.settingsCipher == nil {
		writePaymentWebhookFailure(w, http.StatusNotFound)
		return
	}
	body, form, err := readPaymentWebhook(w, r)
	if err != nil {
		return
	}
	config, err := payment.OpenConfig(s.settingsCipher, method.Provider, method.ConfigCiphertext)
	if err != nil {
		writePaymentWebhookFailure(w, http.StatusInternalServerError)
		return
	}
	verified, err := s.paymentGateway.VerifyWebhook(r.Context(), payment.WebhookRequest{
		Provider: method.Provider, Config: config, Headers: r.Header.Clone(), Body: body, Form: form,
	})
	if err != nil {
		writePaymentWebhookFailure(w, http.StatusUnprocessableEntity)
		return
	}
	payloadHash := sha256.Sum256(body)
	order, err := s.store.CompletePaymentWebhook(r.Context(), store.CompletePaymentWebhookInput{
		PaymentID: method.ID, Provider: method.Provider, ExternalID: verified.ExternalID,
		TradeNo: verified.TradeNo, Amount: verified.Amount, Currency: verified.Currency,
		PayloadSHA256: fmt.Sprintf("%x", payloadHash),
	}, s.now())
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrInvalidInput) {
			status = http.StatusUnprocessableEntity
		} else if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrNotFound) {
			status = http.StatusConflict
		} else {
			s.logger.Error("complete payment webhook", "provider", method.Provider, "payment_id", method.ID, "error", err)
		}
		writePaymentWebhookFailure(w, status)
		return
	}
	if s.hub != nil {
		s.hub.NotifyUserMutation(r.Context(), order.UserID, "", nil, nil, true)
	}
	writePaymentWebhookSuccess(w, method.Provider)
}

func readPaymentWebhook(w http.ResponseWriter, r *http.Request) ([]byte, url.Values, error) {
	if r.Method == http.MethodGet {
		body := []byte(r.URL.RawQuery)
		if len(body) > maxJSONBody {
			writePaymentWebhookFailure(w, http.StatusRequestEntityTooLarge)
			return nil, nil, errors.New("payment webhook query is too large")
		}
		return body, r.URL.Query(), nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writePaymentWebhookFailure(w, http.StatusRequestEntityTooLarge)
		} else {
			writePaymentWebhookFailure(w, http.StatusBadRequest)
		}
		return nil, nil, err
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if contentType == "application/x-www-form-urlencoded" || contentType == "" {
		form, parseErr := url.ParseQuery(string(body))
		if parseErr != nil {
			writePaymentWebhookFailure(w, http.StatusBadRequest)
			return nil, nil, parseErr
		}
		return body, form, nil
	}
	if contentType != "application/json" {
		writePaymentWebhookFailure(w, http.StatusUnsupportedMediaType)
		return nil, nil, errors.New("unsupported payment webhook content type")
	}
	return body, nil, nil
}

func writePaymentWebhookFailure(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "fail")
}

func writePaymentWebhookSuccess(w http.ResponseWriter, provider store.PaymentProvider) {
	result := "success"
	if provider == store.PaymentProviderCoinPayments {
		result = "IPN OK"
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, result)
}

func (s *server) listPaymentProviders(w http.ResponseWriter, r *http.Request) {
	definitions, err := s.enabledPaymentDefinitions(r.Context())
	if err != nil {
		handlePaymentError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, definitions)
}

func (s *server) listAdminPayments(w http.ResponseWriter, r *http.Request) {
	page, ok := positiveQueryInt(w, r, "page", 1)
	if !ok {
		return
	}
	pageSize, ok := positiveQueryInt(w, r, "page_size", 20)
	if !ok {
		return
	}
	provider := store.PaymentProvider(strings.TrimSpace(r.URL.Query().Get("payment")))
	payments, err := s.store.ListPayments(r.Context(), store.PaymentFilter{
		Page: page, PageSize: pageSize, Query: strings.TrimSpace(r.URL.Query().Get("query")), Provider: provider,
	})
	if err != nil {
		handlePaymentError(w, err)
		return
	}
	responses := make([]adminPaymentResponse, 0, len(payments.Items))
	for _, item := range payments.Items {
		response, err := s.adminPaymentResponse(item)
		if err != nil {
			handlePaymentError(w, err)
			return
		}
		responses = append(responses, response)
	}
	writeSuccess(w, http.StatusOK, paymentPageWithResponses(payments, responses))
}

func (s *server) createAdminPayment(w http.ResponseWriter, r *http.Request) {
	var input savePaymentRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	created, err := s.saveNewPayment(r, input)
	if err != nil {
		handlePaymentError(w, err)
		return
	}
	response, err := s.adminPaymentResponse(created)
	if err != nil {
		handlePaymentError(w, err)
		return
	}
	writeSuccess(w, http.StatusCreated, response)
}

func (s *server) updateAdminPayment(w http.ResponseWriter, r *http.Request) {
	paymentID, ok := pathID(w, r, "paymentID")
	if !ok {
		return
	}
	var input savePaymentRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	updated, err := s.saveExistingPayment(r, paymentID, input)
	if err != nil {
		handlePaymentError(w, err)
		return
	}
	response, err := s.adminPaymentResponse(updated)
	if err != nil {
		handlePaymentError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, response)
}

func (s *server) setAdminPaymentEnabled(w http.ResponseWriter, r *http.Request) {
	paymentID, ok := pathID(w, r, "paymentID")
	if !ok {
		return
	}
	var input struct {
		Enabled bool `json:"enable"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Enabled {
		item, err := s.store.GetPayment(r.Context(), paymentID)
		if err != nil {
			handlePaymentError(w, err)
			return
		}
		if err := s.requirePaymentPluginEnabled(r.Context(), item.Provider); err != nil {
			handlePaymentError(w, err)
			return
		}
	}
	updated, err := s.store.SetPaymentEnabled(r.Context(), paymentID, input.Enabled, s.now())
	if err != nil {
		handlePaymentError(w, err)
		return
	}
	response, err := s.adminPaymentResponse(updated)
	if err != nil {
		handlePaymentError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, response)
}

func (s *server) reorderAdminPayments(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []int64 `json:"ids"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.store.ReorderPayments(r.Context(), input.IDs, s.now()); err != nil {
		handlePaymentError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) deleteAdminPayment(w http.ResponseWriter, r *http.Request) {
	paymentID, ok := pathID(w, r, "paymentID")
	if !ok {
		return
	}
	if err := s.store.DeletePayment(r.Context(), paymentID); err != nil {
		handlePaymentError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listUserPayments(w http.ResponseWriter, r *http.Request) {
	payments, err := s.store.ListEnabledPayments(r.Context())
	if err != nil {
		handlePaymentError(w, err)
		return
	}
	responses := make([]userPaymentResponse, 0, len(payments))
	for _, item := range payments {
		responses = append(responses, userPaymentResponseOf(item))
	}
	writeSuccess(w, http.StatusOK, responses)
}

func (s *server) saveNewPayment(r *http.Request, input savePaymentRequest) (store.Payment, error) {
	if s.settingsCipher == nil {
		return store.Payment{}, errPaymentEncryptionUnavailable
	}
	if err := s.requirePaymentPluginEnabled(r.Context(), input.Provider); err != nil {
		return store.Payment{}, err
	}
	config, err := payment.MergeConfig(input.Provider, input.Config, nil, input.ClearConfigFields, true)
	if err != nil {
		return store.Payment{}, err
	}
	ciphertext, err := payment.SealConfig(s.settingsCipher, input.Provider, config)
	if err != nil {
		return store.Payment{}, err
	}
	return s.store.CreatePayment(r.Context(), store.SavePaymentInput{
		Provider: input.Provider, Name: input.Name, Icon: input.Icon, ConfigCiphertext: ciphertext,
		NotifyDomain: input.NotifyDomain, HandlingFeeFixed: input.HandlingFeeFixed,
		HandlingFeeBasisPoints: input.HandlingFeeBasisPoints, Enabled: input.Enabled,
	}, s.now())
}

func (s *server) saveExistingPayment(r *http.Request, paymentID int64, input savePaymentRequest) (store.Payment, error) {
	if s.settingsCipher == nil {
		return store.Payment{}, errPaymentEncryptionUnavailable
	}
	if err := s.requirePaymentPluginEnabled(r.Context(), input.Provider); err != nil {
		return store.Payment{}, err
	}
	existing, err := s.store.GetPayment(r.Context(), paymentID)
	if err != nil {
		return store.Payment{}, err
	}
	var existingConfig map[string]string
	creatingConfig := existing.Provider != input.Provider
	if !creatingConfig {
		existingConfig, err = payment.OpenConfig(s.settingsCipher, existing.Provider, existing.ConfigCiphertext)
		if err != nil {
			return store.Payment{}, err
		}
	}
	config, err := payment.MergeConfig(input.Provider, input.Config, existingConfig, input.ClearConfigFields, creatingConfig)
	if err != nil {
		return store.Payment{}, err
	}
	ciphertext := existing.ConfigCiphertext
	if creatingConfig || !maps.Equal(config, existingConfig) {
		ciphertext, err = payment.SealConfig(s.settingsCipher, input.Provider, config)
		if err != nil {
			return store.Payment{}, err
		}
	}
	return s.store.UpdatePayment(r.Context(), paymentID, input.Revision, store.SavePaymentInput{
		Provider: input.Provider, Name: input.Name, Icon: input.Icon, ConfigCiphertext: ciphertext,
		NotifyDomain: input.NotifyDomain, HandlingFeeFixed: input.HandlingFeeFixed,
		HandlingFeeBasisPoints: input.HandlingFeeBasisPoints, Enabled: input.Enabled,
	}, s.now())
}

func (s *server) adminPaymentResponse(item store.Payment) (adminPaymentResponse, error) {
	if s.settingsCipher == nil {
		return adminPaymentResponse{}, errPaymentEncryptionUnavailable
	}
	config, err := payment.OpenConfig(s.settingsCipher, item.Provider, item.ConfigCiphertext)
	if err != nil {
		return adminPaymentResponse{}, err
	}
	public, configured := payment.SanitizeConfig(item.Provider, config)
	return adminPaymentResponse{Payment: item, Config: public, ConfiguredFields: configured, NotifyURL: s.paymentNotifyURL(item)}, nil
}

func (s *server) paymentNotifyURL(item store.Payment) string {
	base := s.panelURL
	if item.NotifyDomain != "" {
		base = strings.TrimRight(item.NotifyDomain, "/")
	}
	return base + "/api/v1/guest/payment/notify/" + string(item.Provider) + "/" + item.UUID
}

func userPaymentResponseOf(item store.Payment) userPaymentResponse {
	return userPaymentResponse{
		ID: item.ID, Name: item.Name, Provider: item.Provider, Icon: item.Icon,
		HandlingFeeFixed: item.HandlingFeeFixed, HandlingFeeBasisPoints: item.HandlingFeeBasisPoints,
	}
}

func (s *server) legacyPaymentMethods(w http.ResponseWriter, r *http.Request) {
	if session, ok := sessionFromContext(r.Context()); ok && session.IsDistributor {
		writeLegacySuccess(w, http.StatusOK, []map[string]any{})
		return
	}
	payments, err := s.store.ListEnabledPayments(r.Context())
	if err != nil {
		writeLegacyAdminPaymentError(w, err)
		return
	}
	responses := make([]map[string]any, 0, len(payments))
	for _, item := range payments {
		responses = append(responses, map[string]any{
			"id": item.ID, "name": item.Name, "payment": item.Provider, "icon": nullableLegacyString(item.Icon),
			"handling_fee_fixed":   nullableLegacyFee(item.HandlingFeeFixed),
			"handling_fee_percent": float64(item.HandlingFeeBasisPoints) / 100,
		})
	}
	writeLegacySuccess(w, http.StatusOK, responses)
}

func (s *server) legacyListPaymentProviders(w http.ResponseWriter, r *http.Request) {
	definitions, err := s.enabledPaymentDefinitions(r.Context())
	if err != nil {
		writeLegacyAdminPaymentError(w, err)
		return
	}
	providers := make([]store.PaymentProvider, 0, len(definitions))
	for _, definition := range definitions {
		providers = append(providers, definition.Provider)
	}
	writeLegacySuccess(w, http.StatusOK, providers)
}

func (s *server) legacyPaymentForm(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Provider store.PaymentProvider `json:"payment"`
		ID       *int64                `json:"id,omitempty"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	definition, ok := payment.DefinitionFor(input.Provider)
	if !ok || s.requirePaymentPluginEnabled(r.Context(), input.Provider) != nil {
		writeLegacyOrderFail(w, http.StatusBadRequest, "支付方式不存在或未启用")
		return
	}
	config := map[string]string{}
	configured := map[string]struct{}{}
	if input.ID != nil {
		item, err := s.store.GetPayment(r.Context(), *input.ID)
		if err != nil || item.Provider != input.Provider || s.settingsCipher == nil {
			writeLegacyAdminPaymentError(w, coalescePaymentError(err, errPaymentEncryptionUnavailable))
			return
		}
		opened, err := payment.OpenConfig(s.settingsCipher, item.Provider, item.ConfigCiphertext)
		if err != nil {
			writeLegacyAdminPaymentError(w, err)
			return
		}
		var configuredFields []string
		config, configuredFields = payment.SanitizeConfig(item.Provider, opened)
		for _, field := range configuredFields {
			configured[field] = struct{}{}
		}
	}
	fields := make(map[string]map[string]any, len(definition.Fields))
	for _, field := range definition.Fields {
		_, isConfigured := configured[field.Key]
		fields[field.Key] = map[string]any{
			"type": field.Type, "label": field.Label, "description": field.Description,
			"value": config[field.Key], "options": field.Options, "required": field.Required,
			"secret": field.Secret, "configured": isConfigured,
		}
	}
	writeLegacySuccess(w, http.StatusOK, fields)
}

func (s *server) legacyFetchPayments(w http.ResponseWriter, r *http.Request) {
	page, err := s.store.ListPayments(r.Context(), store.PaymentFilter{Page: 1, PageSize: 200})
	if err != nil {
		writeLegacyAdminPaymentError(w, err)
		return
	}
	responses := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		response, err := s.adminPaymentResponse(item)
		if err != nil {
			writeLegacyAdminPaymentError(w, err)
			return
		}
		responses = append(responses, map[string]any{
			"id": item.ID, "uuid": item.UUID, "payment": item.Provider, "name": item.Name,
			"icon": nullableLegacyString(item.Icon), "config": response.Config, "configured_fields": response.ConfiguredFields,
			"notify_domain": nullableLegacyString(item.NotifyDomain), "notify_url": response.NotifyURL,
			"handling_fee_fixed":   nullableLegacyFee(item.HandlingFeeFixed),
			"handling_fee_percent": float64(item.HandlingFeeBasisPoints) / 100,
			"enable":               item.Enabled, "sort": item.SortPosition, "revision": item.Revision,
			"created_at": item.CreatedAt.Unix(), "updated_at": item.UpdatedAt.Unix(),
		})
	}
	writeLegacySuccess(w, http.StatusOK, responses)
}

func (s *server) legacySavePayment(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID                 *int64                `json:"id,omitempty"`
		Revision           int64                 `json:"revision,omitempty"`
		Provider           store.PaymentProvider `json:"payment"`
		Name               string                `json:"name"`
		Icon               string                `json:"icon,omitempty"`
		Config             map[string]string     `json:"config"`
		ClearConfigFields  []string              `json:"clear_config_fields,omitempty"`
		NotifyDomain       string                `json:"notify_domain,omitempty"`
		HandlingFeeFixed   *int64                `json:"handling_fee_fixed,omitempty"`
		HandlingFeePercent json.Number           `json:"handling_fee_percent,omitempty"`
		Enabled            *bool                 `json:"enable,omitempty"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	basisPoints, err := legacyPercentBasisPoints(input.HandlingFeePercent)
	if err != nil {
		writeLegacyAdminPaymentError(w, err)
		return
	}
	fixed := int64(0)
	if input.HandlingFeeFixed != nil {
		fixed = *input.HandlingFeeFixed
	}
	request := savePaymentRequest{
		Revision: input.Revision, Provider: input.Provider, Name: input.Name, Icon: input.Icon,
		NotifyDomain: input.NotifyDomain, HandlingFeeFixed: fixed, HandlingFeeBasisPoints: basisPoints,
		Config: input.Config, ClearConfigFields: input.ClearConfigFields,
	}
	if input.ID == nil {
		// The locked legacy create path always starts disabled and requires an
		// explicit show/toggle action before users can select the method.
		request.Enabled = false
		if _, err := s.saveNewPayment(r, request); err != nil {
			writeLegacyAdminPaymentError(w, err)
			return
		}
	} else {
		existing, err := s.store.GetPayment(r.Context(), *input.ID)
		if err != nil {
			writeLegacyAdminPaymentError(w, err)
			return
		}
		request.Enabled = existing.Enabled
		if request.Revision == 0 {
			request.Revision = existing.Revision
		}
		if _, err := s.saveExistingPayment(r, *input.ID, request); err != nil {
			writeLegacyAdminPaymentError(w, err)
			return
		}
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func (s *server) legacyTogglePayment(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID int64 `json:"id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.store.GetPayment(r.Context(), input.ID)
	if err == nil {
		if !item.Enabled {
			err = s.requirePaymentPluginEnabled(r.Context(), item.Provider)
		}
	}
	if err == nil {
		_, err = s.store.SetPaymentEnabled(r.Context(), item.ID, !item.Enabled, s.now())
	}
	if err != nil {
		writeLegacyAdminPaymentError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func (s *server) legacyDeletePayment(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID int64 `json:"id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.store.DeletePayment(r.Context(), input.ID); err != nil {
		writeLegacyAdminPaymentError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func (s *server) legacyReorderPayments(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []int64 `json:"ids"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.store.ReorderPayments(r.Context(), input.IDs, s.now()); err != nil {
		writeLegacyAdminPaymentError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func legacyPercentBasisPoints(value json.Number) (int64, error) {
	text := strings.TrimSpace(value.String())
	if text == "" {
		return 0, nil
	}
	parts := strings.Split(text, ".")
	if len(parts) > 2 || parts[0] == "" || strings.HasPrefix(parts[0], "-") || len(parts[0]) > 3 {
		return 0, store.ErrInvalidInput
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > 100 {
		return 0, store.ErrInvalidInput
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 2 {
		return 0, store.ErrInvalidInput
	}
	for len(fraction) < 2 {
		fraction += "0"
	}
	fractionValue := int64(0)
	if fraction != "" {
		fractionValue, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, store.ErrInvalidInput
		}
	}
	result := whole*100 + fractionValue
	if result > 10_000 {
		return 0, store.ErrInvalidInput
	}
	return result, nil
}

func handlePaymentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errTrustedPluginDisabled):
		writeAPIError(w, http.StatusConflict, "plugin_disabled", "对应的可信支付插件未启用", nil)
	case errors.Is(err, payment.ErrInvalidConfig), errors.Is(err, store.ErrInvalidInput):
		writeAPIError(w, http.StatusUnprocessableEntity, "payment_validation_failed", "支付配置参数无效", nil)
	case errors.Is(err, store.ErrPaymentConfigInUse):
		writeAPIError(w, http.StatusConflict, "payment_config_in_use", "支付方式已创建过渠道订单，不能修改接口或密钥；请禁用后新建支付方式", nil)
	case errors.Is(err, store.ErrPaymentReferenced):
		writeAPIError(w, http.StatusConflict, "payment_referenced", "支付方式已被订单或回调引用，只能禁用，不能删除", nil)
	case errors.Is(err, errPaymentEncryptionUnavailable):
		writeAPIError(w, http.StatusServiceUnavailable, "payment_encryption_unavailable", "支付密钥加密服务不可用", nil)
	default:
		handleStoreError(w, err)
	}
}

func handlePaymentCheckoutError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrPaymentUnavailable), errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrInvalidInput):
		writeAPIError(w, http.StatusUnprocessableEntity, "payment_unavailable", "支付方式不可用", nil)
	case errors.Is(err, store.ErrPaymentInProgress):
		writeAPIError(w, http.StatusConflict, "payment_in_progress", "支付请求正在创建，请稍后重试", nil)
	case errors.Is(err, errPaymentEncryptionUnavailable):
		writeAPIError(w, http.StatusServiceUnavailable, "payment_encryption_unavailable", "支付密钥加密服务不可用", nil)
	case errors.Is(err, errPaymentProviderFailed):
		writeAPIError(w, http.StatusBadGateway, "payment_provider_failed", "支付渠道暂时不可用，请稍后重试", nil)
	default:
		handleOrderError(w, err)
	}
}

func writeLegacyPaymentCheckoutError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrPaymentInProgress):
		writeLegacyOrderFail(w, http.StatusConflict, "支付请求正在创建，请稍后重试")
	case errors.Is(err, errPaymentProviderFailed), errors.Is(err, errPaymentEncryptionUnavailable):
		writeLegacyOrderFail(w, http.StatusBadGateway, "支付渠道暂时不可用，请稍后重试")
	default:
		writeLegacyOrderFail(w, http.StatusBadRequest, "支付方式不可用")
	}
}

func writeLegacyAdminPaymentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errTrustedPluginDisabled):
		writeLegacyOrderFail(w, http.StatusConflict, "对应的可信支付插件未启用")
	case errors.Is(err, store.ErrNotFound):
		writeLegacyOrderFail(w, http.StatusBadRequest, "支付方式不存在")
	case errors.Is(err, store.ErrPaymentConfigInUse):
		writeLegacyOrderFail(w, http.StatusConflict, "支付方式已创建过渠道订单，不能修改接口或密钥，请禁用后新建支付方式")
	case errors.Is(err, store.ErrPaymentReferenced):
		writeLegacyOrderFail(w, http.StatusConflict, "支付方式已被订单使用，只能禁用")
	case errors.Is(err, payment.ErrInvalidConfig), errors.Is(err, store.ErrInvalidInput):
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "支付配置参数无效")
	default:
		writeLegacyOrderFail(w, http.StatusInternalServerError, "支付配置操作失败")
	}
}

func nullableLegacyString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableLegacyFee(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func coalescePaymentError(primary, fallback error) error {
	if primary != nil {
		return primary
	}
	return fallback
}

var errPaymentEncryptionUnavailable = errors.New("payment encryption unavailable")
var errPaymentProviderFailed = errors.New("payment provider failed")
var errTrustedPluginDisabled = errors.New("trusted plugin disabled")

func (s *server) enabledPaymentDefinitions(ctx context.Context) ([]payment.Definition, error) {
	plugins, err := s.store.ListTrustedPlugins(ctx)
	if err != nil {
		return nil, err
	}
	enabled := make(map[string]struct{}, len(plugins))
	for _, plugin := range plugins {
		if plugin.Type == "payment" && plugin.Enabled {
			enabled[plugin.Code] = struct{}{}
		}
	}
	definitions := payment.Definitions()
	result := make([]payment.Definition, 0, len(definitions))
	for _, definition := range definitions {
		code, ok := store.TrustedPluginCodeForPaymentProvider(definition.Provider)
		if !ok {
			continue
		}
		if _, ok := enabled[code]; ok {
			result = append(result, definition)
		}
	}
	return result, nil
}

func (s *server) requirePaymentPluginEnabled(ctx context.Context, provider store.PaymentProvider) error {
	code, ok := store.TrustedPluginCodeForPaymentProvider(provider)
	if !ok {
		return store.ErrInvalidInput
	}
	enabled, err := s.store.TrustedPluginEnabled(ctx, code)
	if err != nil {
		return err
	}
	if !enabled {
		return errTrustedPluginDisabled
	}
	return nil
}

type paymentPageResponse struct {
	Items    []adminPaymentResponse `json:"items"`
	Total    int64                  `json:"total"`
	Page     int                    `json:"page"`
	PageSize int                    `json:"page_size"`
}

func paymentPageWithResponses(page store.PaymentPage, items []adminPaymentResponse) paymentPageResponse {
	return paymentPageResponse{Items: items, Total: page.Total, Page: page.Page, PageSize: page.PageSize}
}
