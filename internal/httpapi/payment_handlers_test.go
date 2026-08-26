package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/payment"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type fakePaymentGateway struct {
	checkoutCalls int
	failFirst     bool
	requests      []payment.CheckoutRequest
}

func (gateway *fakePaymentGateway) Checkout(_ context.Context, request payment.CheckoutRequest) (payment.CheckoutResult, error) {
	gateway.checkoutCalls++
	gateway.requests = append(gateway.requests, request)
	if gateway.failFirst && gateway.checkoutCalls == 1 {
		return payment.CheckoutResult{}, errors.New("temporary provider failure")
	}
	return payment.CheckoutResult{Type: 1, Data: "https://checkout.example.test/pay/" + request.TradeNo, ExternalID: "provider-" + request.TradeNo}, nil
}

func (*fakePaymentGateway) VerifyWebhook(context.Context, payment.WebhookRequest) (payment.VerifiedWebhook, error) {
	return payment.VerifiedWebhook{}, payment.ErrInvalidWebhookSignature
}

func TestPaymentAdministratorAPIsEncryptMaskUpdateSortAndRestrictSecrets(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	providers := admin.request(t, api, http.MethodGet, "/api/v1/admin/payment-providers", "")
	if providers.Code != http.StatusOK || !containsAll(providers.Body.String(), `"provider":"AlipayF2F"`, `"provider":"BTCPay"`, `"provider":"MGate"`) {
		t.Fatalf("provider definitions status=%d body=%s", providers.Code, providers.Body)
	}

	createBody := `{
		"payment":"BTCPay","name":"BTCPay 主通道","icon":"https://cdn.example.test/btcpay.svg",
		"notify_domain":"https://pay.example.test","handling_fee_fixed":123,"handling_fee_basis_points":250,"enable":true,
		"config":{"btcpay_url":"https://btcpay.example.test/","btcpay_storeId":"store-one","btcpay_api_key":"api-secret-one","btcpay_webhook_key":"webhook-secret-one"}
	}`
	created := admin.request(t, api, http.MethodPost, "/api/v1/admin/payments", createBody)
	if created.Code != http.StatusCreated || !containsAll(created.Body.String(), `"name":"BTCPay 主通道"`, `"handling_fee_basis_points":250`, `"configured_fields":["btcpay_api_key","btcpay_webhook_key"]`) {
		t.Fatalf("create payment status=%d body=%s", created.Code, created.Body)
	}
	if containsAll(created.Body.String(), "api-secret-one") || containsAll(created.Body.String(), "webhook-secret-one") {
		t.Fatalf("create response leaked secret: %s", created.Body)
	}
	var createdPayload struct {
		Data struct {
			ID       int64 `json:"id"`
			Revision int64 `json:"revision"`
		} `json:"data"`
	}
	decodeResponse(t, created, &createdPayload)
	createdPayment, err := database.GetPayment(context.Background(), createdPayload.Data.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedCiphertext := createdPayment.ConfigCiphertext
	if bytes.Contains(storedCiphertext, []byte("api-secret-one")) || bytes.Contains(storedCiphertext, []byte("webhook-secret-one")) {
		t.Fatal("database payment config contains plaintext secret")
	}

	updateBody := fmt.Sprintf(`{
		"revision":%d,"payment":"BTCPay","name":"BTCPay 更新","icon":"https://cdn.example.test/btcpay.svg",
		"notify_domain":"https://pay.example.test","handling_fee_fixed":0,"handling_fee_basis_points":0,"enable":true,
		"config":{"btcpay_url":"https://new-btcpay.example.test","btcpay_storeId":"store-two","btcpay_api_key":"","btcpay_webhook_key":"webhook-secret-two"}
	}`, createdPayload.Data.Revision)
	updated := admin.request(t, api, http.MethodPut, fmt.Sprintf("/api/v1/admin/payments/%d", createdPayload.Data.ID), updateBody)
	if updated.Code != http.StatusOK || !containsAll(updated.Body.String(), `"name":"BTCPay 更新"`, `"btcpay_url":"https://new-btcpay.example.test"`) || containsAll(updated.Body.String(), "webhook-secret-two") {
		t.Fatalf("update payment status=%d body=%s", updated.Code, updated.Body)
	}
	stored, err := database.GetPayment(context.Background(), createdPayload.Data.ID)
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := appsettings.NewCipher(make([]byte, 32))
	config, err := payment.OpenConfig(cipher, stored.Provider, stored.ConfigCiphertext)
	if err != nil || config["btcpay_api_key"] != "api-secret-one" || config["btcpay_webhook_key"] != "webhook-secret-two" {
		t.Fatalf("updated encrypted config = (%#v, %v)", config, err)
	}

	second := admin.request(t, api, http.MethodPost, "/api/v1/admin/payments", `{
		"payment":"CoinPayments","name":"CoinPayments","handling_fee_fixed":0,"handling_fee_basis_points":0,"enable":false,
		"config":{"coinpayments_merchant_id":"merchant","coinpayments_ipn_secret":"ipn-secret","coinpayments_currency":"cny"}
	}`)
	if second.Code != http.StatusCreated {
		t.Fatalf("create second payment status=%d body=%s", second.Code, second.Body)
	}
	var secondPayload struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	decodeResponse(t, second, &secondPayload)
	reordered := admin.request(t, api, http.MethodPut, "/api/v1/admin/payments/order", fmt.Sprintf(`{"ids":[%d,%d]}`, secondPayload.Data.ID, createdPayload.Data.ID))
	if reordered.Code != http.StatusNoContent {
		t.Fatalf("reorder payments status=%d body=%s", reordered.Code, reordered.Body)
	}
	listed := admin.request(t, api, http.MethodGet, "/api/v1/admin/payments?page=1&page_size=20", "")
	if listed.Code != http.StatusOK || !containsAll(listed.Body.String(), `"total":2`) || bytes.Contains(listed.Body.Bytes(), []byte("ipn-secret")) {
		t.Fatalf("list payments status=%d body=%s", listed.Code, listed.Body)
	}
}

func TestPaymentLegacyAdminAndUserMethodContractsDoNotExposeSecrets(t *testing.T) {
	api, database := newTestAPI(t)
	adminAuthorization := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123").Authorization
	prefix := "/api/v2/admin/payment"
	methods := bearerRequest(api, http.MethodGet, prefix+"/getPaymentMethods", adminAuthorization, "")
	if methods.Code != http.StatusOK || !containsAll(methods.Body.String(), `"AlipayF2F"`, `"CoinPayments"`, `"MGate"`) {
		t.Fatalf("legacy methods status=%d body=%s", methods.Code, methods.Body)
	}
	form := bearerRequest(api, http.MethodPost, prefix+"/getPaymentForm", adminAuthorization, `{"payment":"CoinPayments"}`)
	if form.Code != http.StatusOK || !containsAll(form.Body.String(), "Merchant ID", "IPN Secret", "货币代码") {
		t.Fatalf("legacy form status=%d body=%s", form.Code, form.Body)
	}
	created := bearerRequest(api, http.MethodPost, prefix+"/save", adminAuthorization, `{
		"payment":"CoinPayments","name":"CoinPayments","handling_fee_fixed":123,"handling_fee_percent":2.5,"enable":true,
		"config":{"coinpayments_merchant_id":"merchant","coinpayments_ipn_secret":"legacy-ipn-secret","coinpayments_currency":"CNY"}
	}`)
	if created.Code != http.StatusOK || !containsAll(created.Body.String(), `"data":true`) {
		t.Fatalf("legacy save status=%d body=%s", created.Code, created.Body)
	}
	listed := bearerRequest(api, http.MethodGet, prefix+"/fetch", adminAuthorization, "")
	if listed.Code != http.StatusOK || !containsAll(listed.Body.String(), `"handling_fee_percent":2.5`, `"configured_fields":["coinpayments_ipn_secret"]`) || bytes.Contains(listed.Body.Bytes(), []byte("legacy-ipn-secret")) {
		t.Fatalf("legacy list status=%d body=%s", listed.Code, listed.Body)
	}
	paymentPage, err := database.ListPayments(context.Background(), store.PaymentFilter{Page: 1, PageSize: 20})
	if err != nil || len(paymentPage.Items) != 1 {
		t.Fatalf("stored legacy payment = (%#v, %v)", paymentPage, err)
	}
	toggled := bearerRequest(api, http.MethodPost, prefix+"/show", adminAuthorization, fmt.Sprintf(`{"id":%d}`, paymentPage.Items[0].ID))
	if toggled.Code != http.StatusOK || !containsAll(toggled.Body.String(), `"data":true`) {
		t.Fatalf("legacy toggle status=%d body=%s", toggled.Code, toggled.Body)
	}

	user := createKnowledgeTestUser(t, database, "payment-user@example.test", "payment-user-password-123", 1, false)
	userAuthorization := loginLegacyBearer(t, api, user.email, user.password).Authorization
	userMethods := bearerRequest(api, http.MethodGet, "/api/v1/user/order/getPaymentMethod", userAuthorization, "")
	if userMethods.Code != http.StatusOK || !containsAll(userMethods.Body.String(), `"name":"CoinPayments"`, `"handling_fee_fixed":123`, `"handling_fee_percent":2.5`) || bytes.Contains(userMethods.Body.Bytes(), []byte("legacy-ipn-secret")) {
		t.Fatalf("legacy user methods status=%d body=%s", userMethods.Code, userMethods.Body)
	}
}

func TestPaymentConfigCannotBeDecodedAsPlainJSON(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	created := admin.request(t, api, http.MethodPost, "/api/v1/admin/payments", `{
		"payment":"EPay","name":"EPay","handling_fee_fixed":0,"handling_fee_basis_points":0,"enable":true,
		"config":{"url":"https://epay.example.test","pid":"1001","key":"epay-secret","type":"alipay"}
	}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create EPay status=%d body=%s", created.Code, created.Body)
	}
	page, err := database.ListPayments(context.Background(), store.PaymentFilter{Page: 1, PageSize: 20})
	if err != nil || len(page.Items) != 1 {
		t.Fatal(err)
	}
	ciphertext := page.Items[0].ConfigCiphertext
	var decoded map[string]any
	if json.Unmarshal(ciphertext, &decoded) == nil {
		t.Fatalf("ciphertext decoded as JSON: %#v", decoded)
	}
}

func TestPaymentAPIKeepsActiveCallbackCredentialsStableWhileAllowingMetadataEdits(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	created := admin.request(t, api, http.MethodPost, "/api/v1/admin/payments", `{
		"payment":"EPay","name":"EPay","handling_fee_fixed":0,"handling_fee_basis_points":0,"enable":true,
		"config":{"url":"https://epay.example.test","pid":"1001","key":"epay-secret","type":"alipay"}
	}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create payment status=%d body=%s", created.Code, created.Body)
	}
	var createdPayload struct {
		Data struct {
			ID       int64 `json:"id"`
			Revision int64 `json:"revision"`
		} `json:"data"`
	}
	decodeResponse(t, created, &createdPayload)
	method, err := database.GetPayment(context.Background(), createdPayload.Data.ID)
	if err != nil {
		t.Fatal(err)
	}
	originalCiphertext := append([]byte(nil), method.ConfigCiphertext...)
	plan := createOrderAPIPlan(t, database, store.PlanPrices{"monthly": 1_000})
	user := createKnowledgeTestUser(t, database, "payment-config-lock@example.test", "payment-config-lock-password-123", 1, false)
	order, err := database.CreateOrder(context.Background(), store.CreateOrderInput{
		UserID: user.id, PlanID: plan.ID, Period: "monthly",
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.StartPaymentCheckout(context.Background(), store.StartPaymentCheckoutInput{
		UserID: user.id, TradeNo: order.TradeNo, PaymentID: method.ID,
	}, fixedNow()); err != nil {
		t.Fatal(err)
	}

	metadata := admin.request(t, api, http.MethodPut, fmt.Sprintf("/api/v1/admin/payments/%d", method.ID), fmt.Sprintf(`{
		"revision":%d,"payment":"EPay","name":"EPay 新名称","handling_fee_fixed":0,"handling_fee_basis_points":0,"enable":true,
		"config":{"url":"https://epay.example.test","pid":"1001","key":"","type":"alipay"}
	}`, createdPayload.Data.Revision))
	if metadata.Code != http.StatusOK || !containsAll(metadata.Body.String(), `"name":"EPay 新名称"`) {
		t.Fatalf("metadata update status=%d body=%s", metadata.Code, metadata.Body)
	}
	var metadataPayload struct {
		Data struct {
			Revision int64 `json:"revision"`
		} `json:"data"`
	}
	decodeResponse(t, metadata, &metadataPayload)
	method, err = database.GetPayment(context.Background(), method.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(method.ConfigCiphertext, originalCiphertext) {
		t.Fatal("metadata-only update unexpectedly re-encrypted provider credentials")
	}

	rotated := admin.request(t, api, http.MethodPut, fmt.Sprintf("/api/v1/admin/payments/%d", method.ID), fmt.Sprintf(`{
		"revision":%d,"payment":"EPay","name":"EPay 新名称","handling_fee_fixed":0,"handling_fee_basis_points":0,"enable":true,
		"config":{"url":"https://epay.example.test","pid":"1001","key":"replacement-secret","type":"alipay"}
	}`, metadataPayload.Data.Revision))
	if rotated.Code != http.StatusConflict || !containsAll(rotated.Body.String(), `"code":"payment_config_in_use"`) {
		t.Fatalf("credential rotation status=%d body=%s", rotated.Code, rotated.Body)
	}
}

func TestPaidOrderCheckoutCalculatesFeeCachesResponseAndKeepsLegacyEnvelope(t *testing.T) {
	gateway := &fakePaymentGateway{}
	api, database := newTestAPIWithPaymentGateway(t, gateway)
	method := createPaymentAPIMethod(t, database, store.PaymentProviderEPay, map[string]string{
		"url": "https://epay.example.test", "pid": "1001", "key": "epay-secret", "type": "alipay",
	}, 123, 250)
	plan := createOrderAPIPlan(t, database, store.PlanPrices{"monthly": 100_000})
	user := createKnowledgeTestUser(t, database, "payment-checkout@example.test", "payment-checkout-password-123", 1, false)
	client := loginAs(t, api, user.email, user.password)
	created := client.request(t, api, http.MethodPost, "/api/v1/orders", fmt.Sprintf(`{"plan_id":%d,"period":"month_price"}`, plan.ID))
	var orderPayload struct {
		Data store.Order `json:"data"`
	}
	decodeResponse(t, created, &orderPayload)
	path := "/api/v1/orders/" + orderPayload.Data.TradeNo + "/checkout"
	checkout := client.request(t, api, http.MethodPost, path, fmt.Sprintf(`{"payment_id":%d}`, method.ID))
	if checkout.Code != http.StatusOK || !containsAll(checkout.Body.String(), `"handling_amount":2623`, `"total_amount":102623`, `"type":1`) {
		t.Fatalf("modern paid checkout status=%d body=%s", checkout.Code, checkout.Body)
	}
	repeated := client.request(t, api, http.MethodPost, path, fmt.Sprintf(`{"payment_id":%d}`, method.ID))
	if repeated.Code != http.StatusOK || repeated.Body.String() != checkout.Body.String() {
		t.Fatalf("cached checkout status=%d first=%s repeated=%s", repeated.Code, checkout.Body, repeated.Body)
	}
	if gateway.checkoutCalls != 1 || len(gateway.requests) != 1 || gateway.requests[0].Amount != 102_623 || gateway.requests[0].Currency != "CNY" ||
		gateway.requests[0].Provider != store.PaymentProviderEPay || gateway.requests[0].IdempotencyKey == "" {
		t.Fatalf("gateway requests=%#v calls=%d", gateway.requests, gateway.checkoutCalls)
	}
	cancel := client.request(t, api, http.MethodPost, "/api/v1/orders/"+orderPayload.Data.TradeNo+"/cancel", `{}`)
	if cancel.Code != http.StatusConflict || !containsAll(cancel.Body.String(), `"code":"payment_in_progress"`) {
		t.Fatalf("cancel active payment checkout status=%d body=%s", cancel.Code, cancel.Body)
	}

	legacyUser := createKnowledgeTestUser(t, database, "legacy-payment-checkout@example.test", "legacy-payment-password-123", 1, false)
	authorization := loginLegacyBearer(t, api, legacyUser.email, legacyUser.password).Authorization
	legacyCreated := bearerRequest(api, http.MethodPost, "/api/v1/user/order/save", authorization,
		fmt.Sprintf(`{"plan_id":%d,"period":"month_price"}`, plan.ID))
	var legacyOrder struct {
		Data string `json:"data"`
	}
	decodeResponse(t, legacyCreated, &legacyOrder)
	legacyCheckout := bearerRequest(api, http.MethodPost, "/api/v1/user/order/checkout", authorization,
		fmt.Sprintf(`{"trade_no":%q,"method":%d}`, legacyOrder.Data, method.ID))
	if legacyCheckout.Code != http.StatusOK || !containsAll(legacyCheckout.Body.String(), `"type":1`, `"data":"https://checkout.example.test/pay/`+legacyOrder.Data) {
		t.Fatalf("legacy paid checkout status=%d body=%s", legacyCheckout.Code, legacyCheckout.Body)
	}
}

func TestPaidOrderCheckoutProviderFailureCanBeRetried(t *testing.T) {
	gateway := &fakePaymentGateway{failFirst: true}
	api, database := newTestAPIWithPaymentGateway(t, gateway)
	method := createPaymentAPIMethod(t, database, store.PaymentProviderEPay, map[string]string{
		"url": "https://epay.example.test", "pid": "1001", "key": "epay-secret", "type": "alipay",
	}, 0, 0)
	plan := createOrderAPIPlan(t, database, store.PlanPrices{"monthly": 500})
	user := createKnowledgeTestUser(t, database, "payment-retry@example.test", "payment-retry-password-123", 1, false)
	client := loginAs(t, api, user.email, user.password)
	created := client.request(t, api, http.MethodPost, "/api/v1/orders", fmt.Sprintf(`{"plan_id":%d,"period":"month_price"}`, plan.ID))
	var orderPayload struct {
		Data store.Order `json:"data"`
	}
	decodeResponse(t, created, &orderPayload)
	path := "/api/v1/orders/" + orderPayload.Data.TradeNo + "/checkout"
	first := client.request(t, api, http.MethodPost, path, fmt.Sprintf(`{"payment_id":%d}`, method.ID))
	if first.Code != http.StatusBadGateway || !containsAll(first.Body.String(), `"code":"payment_provider_failed"`) {
		t.Fatalf("failed provider checkout status=%d body=%s", first.Code, first.Body)
	}
	second := client.request(t, api, http.MethodPost, path, fmt.Sprintf(`{"payment_id":%d}`, method.ID))
	if second.Code != http.StatusOK || gateway.checkoutCalls != 2 || gateway.requests[0].IdempotencyKey != gateway.requests[1].IdempotencyKey {
		t.Fatalf("retried checkout status=%d calls=%d requests=%#v body=%s", second.Code, gateway.checkoutCalls, gateway.requests, second.Body)
	}
}

func TestCoinPaymentsWebhookRequiresSignatureAndExactOrderBindingAndIsIdempotent(t *testing.T) {
	api, database := newTestAPI(t)
	const secret = "coinpayments-webhook-secret"
	method := createPaymentAPIMethod(t, database, store.PaymentProviderCoinPayments, map[string]string{
		"coinpayments_merchant_id": "merchant-one", "coinpayments_ipn_secret": secret, "coinpayments_currency": "CNY",
	}, 123, 250)
	plan := createOrderAPIPlan(t, database, store.PlanPrices{"monthly": 100_000})
	user := createKnowledgeTestUser(t, database, "payment-webhook@example.test", "payment-webhook-password-123", 1, false)
	client := loginAs(t, api, user.email, user.password)
	created := client.request(t, api, http.MethodPost, "/api/v1/orders", fmt.Sprintf(`{"plan_id":%d,"period":"month_price"}`, plan.ID))
	var orderPayload struct {
		Data store.Order `json:"data"`
	}
	decodeResponse(t, created, &orderPayload)
	checkout := client.request(t, api, http.MethodPost, "/api/v1/orders/"+orderPayload.Data.TradeNo+"/checkout", fmt.Sprintf(`{"payment_id":%d}`, method.ID))
	if checkout.Code != http.StatusOK || !containsAll(checkout.Body.String(), `"total_amount":102623`) {
		t.Fatalf("coinpayments checkout status=%d body=%s", checkout.Code, checkout.Body)
	}
	callbackPath := "/api/v1/guest/payment/notify/CoinPayments/" + method.UUID
	goodBody := "merchant=merchant-one&status=100&amount1=1026.23&currency1=CNY&item_number=" + orderPayload.Data.TradeNo + "&txn_id=coin-txn-good"
	tampered := paymentWebhookRequest(api, callbackPath, goodBody, "invalid-signature")
	if tampered.Code != http.StatusUnprocessableEntity || tampered.Body.String() != "fail" {
		t.Fatalf("tampered callback status=%d body=%q", tampered.Code, tampered.Body.String())
	}
	wrongBody := "merchant=merchant-one&status=100&amount1=1026.24&currency1=CNY&item_number=" + orderPayload.Data.TradeNo + "&txn_id=coin-txn-wrong"
	wrong := paymentWebhookRequest(api, callbackPath, wrongBody, paymentWebhookHMAC(secret, wrongBody))
	if wrong.Code != http.StatusConflict || wrong.Body.String() != "fail" {
		t.Fatalf("wrong amount callback status=%d body=%q", wrong.Code, wrong.Body.String())
	}
	secondMethod := createPaymentAPIMethod(t, database, store.PaymentProviderEPay, map[string]string{
		"url": "https://epay.example.test", "pid": "1001", "key": "epay-secret", "type": "alipay",
	}, 50, 0)
	secondCheckout := client.request(t, api, http.MethodPost, "/api/v1/orders/"+orderPayload.Data.TradeNo+"/checkout", fmt.Sprintf(`{"payment_id":%d}`, secondMethod.ID))
	if secondCheckout.Code != http.StatusOK || !containsAll(secondCheckout.Body.String(), `"total_amount":100050`) {
		t.Fatalf("second-method checkout status=%d body=%s", secondCheckout.Code, secondCheckout.Body)
	}
	if _, err := database.SetPaymentEnabled(t.Context(), method.ID, false, fixedNow()); err != nil {
		t.Fatal(err)
	}
	good := paymentWebhookRequest(api, callbackPath, goodBody, paymentWebhookHMAC(secret, goodBody))
	if good.Code != http.StatusOK || good.Body.String() != "IPN OK" {
		t.Fatalf("disabled-after-checkout callback status=%d body=%q", good.Code, good.Body.String())
	}
	retryBody := goodBody + "&retry_metadata=provider-retry"
	duplicate := paymentWebhookRequest(api, callbackPath, retryBody, paymentWebhookHMAC(secret, retryBody))
	if duplicate.Code != http.StatusOK || duplicate.Body.String() != "IPN OK" {
		t.Fatalf("verified duplicate with changed metadata status=%d body=%q", duplicate.Code, duplicate.Body.String())
	}
	detail := client.request(t, api, http.MethodGet, "/api/v1/orders/"+orderPayload.Data.TradeNo, "")
	if detail.Code != http.StatusOK || !containsAll(detail.Body.String(), `"status":3`, `"callback_no":"coin-txn-good"`, fmt.Sprintf(`"payment_id":%d`, method.ID)) {
		t.Fatalf("completed payment order status=%d body=%s", detail.Code, detail.Body)
	}
}

func createPaymentAPIMethod(t *testing.T, database *store.Store, provider store.PaymentProvider, config map[string]string, fixedFee, basisPoints int64) store.Payment {
	t.Helper()
	cipher, err := appsettings.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := payment.MergeConfig(provider, config, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := payment.SealConfig(cipher, provider, normalized)
	if err != nil {
		t.Fatal(err)
	}
	method, err := database.CreatePayment(context.Background(), store.SavePaymentInput{
		Provider: provider, Name: string(provider), ConfigCiphertext: ciphertext,
		HandlingFeeFixed: fixedFee, HandlingFeeBasisPoints: basisPoints, Enabled: true,
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	return method
}

func paymentWebhookHMAC(secret, body string) string {
	mac := hmac.New(sha512.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

func paymentWebhookRequest(api http.Handler, path, body, signature string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HMAC", signature)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}
