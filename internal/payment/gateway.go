package payment

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	maxProviderResponseBytes = 1 << 20
	maxProviderMoneyCents    = int64(9_000_000_000_000_000)
)

var (
	ErrInvalidGatewayRequest   = errors.New("invalid payment gateway request")
	ErrInvalidGatewayResponse  = errors.New("invalid payment gateway response")
	ErrInvalidWebhookSignature = errors.New("invalid payment webhook signature")
	ErrUnsuccessfulWebhook     = errors.New("payment webhook is not a successful settlement")
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Options struct {
	HTTPClient HTTPDoer
}

type Service struct {
	httpClient HTTPDoer
}

type CheckoutRequest struct {
	Provider       store.PaymentProvider
	Config         map[string]string
	TradeNo        string
	Amount         int64
	Currency       string
	NotifyURL      string
	ReturnURL      string
	IdempotencyKey string
}

type CheckoutResult struct {
	Type       int    `json:"type"`
	Data       string `json:"data"`
	ExternalID string `json:"external_id,omitempty"`
}

type WebhookRequest struct {
	Provider store.PaymentProvider
	Config   map[string]string
	Headers  http.Header
	Body     []byte
	Form     url.Values
}

type VerifiedWebhook struct {
	ExternalID string
	TradeNo    string
	Amount     int64
	Currency   string
}

func NewService(options Options) *Service {
	client := options.HTTPClient
	if client == nil {
		client = newSecureHTTPClient()
	}
	return &Service{httpClient: client}
}

func (service *Service) Checkout(ctx context.Context, request CheckoutRequest) (CheckoutResult, error) {
	config, err := MergeConfig(request.Provider, request.Config, nil, nil, true)
	if err != nil || !validGatewayTradeNo(request.TradeNo) || request.Amount < 1 || request.Amount > maxProviderMoneyCents ||
		request.Currency != "CNY" || !validGatewayCallbackURL(request.NotifyURL) || !validGatewayCallbackURL(request.ReturnURL) || !validIdempotencyKey(request.IdempotencyKey) {
		return CheckoutResult{}, ErrInvalidGatewayRequest
	}
	request.Config = config
	switch request.Provider {
	case store.PaymentProviderAlipayF2F:
		return service.checkoutAlipay(ctx, request)
	case store.PaymentProviderBTCPay:
		return service.checkoutBTCPay(ctx, request)
	case store.PaymentProviderCoinPayments:
		return checkoutCoinPayments(request)
	case store.PaymentProviderCoinbase:
		return service.checkoutCoinbase(ctx, request)
	case store.PaymentProviderEPay:
		return checkoutEPay(request)
	case store.PaymentProviderMGate:
		return service.checkoutMGate(ctx, request)
	default:
		return CheckoutResult{}, ErrInvalidGatewayRequest
	}
}

func (service *Service) VerifyWebhook(ctx context.Context, request WebhookRequest) (VerifiedWebhook, error) {
	config, err := MergeConfig(request.Provider, request.Config, nil, nil, true)
	if err != nil || len(request.Body) > maxProviderResponseBytes {
		return VerifiedWebhook{}, ErrInvalidGatewayRequest
	}
	request.Config = config
	if request.Headers == nil {
		request.Headers = make(http.Header)
	}
	switch request.Provider {
	case store.PaymentProviderAlipayF2F:
		return verifyAlipayWebhook(request)
	case store.PaymentProviderBTCPay:
		return service.verifyBTCPayWebhook(ctx, request)
	case store.PaymentProviderCoinPayments:
		return verifyCoinPaymentsWebhook(request)
	case store.PaymentProviderCoinbase:
		return verifyCoinbaseWebhook(request)
	case store.PaymentProviderEPay:
		return verifyEPayWebhook(request)
	case store.PaymentProviderMGate:
		return verifyMGateWebhook(request)
	default:
		return VerifiedWebhook{}, ErrInvalidGatewayRequest
	}
}

func (service *Service) checkoutAlipay(ctx context.Context, request CheckoutRequest) (CheckoutResult, error) {
	productName := strings.TrimSpace(request.Config["product_name"])
	if productName == "" {
		productName = "XBoard 订阅"
	}
	business, _ := json.Marshal(map[string]any{
		"subject": productName, "out_trade_no": request.TradeNo, "total_amount": formatCents(request.Amount),
	})
	values := url.Values{
		"app_id": {request.Config["app_id"]}, "method": {"alipay.trade.precreate"}, "format": {"JSON"},
		"charset": {"utf-8"}, "sign_type": {"RSA2"}, "timestamp": {time.Now().Format("2006-01-02 15:04:05")},
		"version": {"1.0"}, "notify_url": {request.NotifyURL}, "biz_content": {string(business)},
	}
	privateKey, err := parseRSAPrivateKey(request.Config["private_key"])
	if err != nil {
		return CheckoutResult{}, ErrInvalidGatewayRequest
	}
	signature, err := signRSA2(privateKey, []byte(canonicalValues(values, map[string]struct{}{"sign": {}})))
	if err != nil {
		return CheckoutResult{}, ErrInvalidGatewayRequest
	}
	values.Set("sign", signature)
	responseBody, err := service.do(ctx, http.MethodPost, "https://openapi.alipay.com/gateway.do", strings.NewReader(values.Encode()), map[string]string{
		"Content-Type": "application/x-www-form-urlencoded", "Idempotency-Key": request.IdempotencyKey,
	})
	if err != nil {
		return CheckoutResult{}, err
	}
	var envelope struct {
		Response json.RawMessage `json:"alipay_trade_precreate_response"`
		Sign     string          `json:"sign"`
	}
	if json.Unmarshal(responseBody, &envelope) != nil || len(envelope.Response) == 0 || envelope.Sign == "" {
		return CheckoutResult{}, ErrInvalidGatewayResponse
	}
	publicKey, err := parseRSAPublicKey(request.Config["public_key"])
	if err != nil || verifyRSA2(publicKey, envelope.Response, envelope.Sign) != nil {
		return CheckoutResult{}, ErrInvalidGatewayResponse
	}
	var result struct {
		Code    string `json:"code"`
		TradeNo string `json:"out_trade_no"`
		QRCode  string `json:"qr_code"`
	}
	if json.Unmarshal(envelope.Response, &result) != nil || result.Code != "10000" || result.TradeNo != request.TradeNo || !validHTTPSURL(result.QRCode) {
		return CheckoutResult{}, ErrInvalidGatewayResponse
	}
	return CheckoutResult{Type: 0, Data: result.QRCode}, nil
}

func (service *Service) checkoutBTCPay(ctx context.Context, request CheckoutRequest) (CheckoutResult, error) {
	payload, _ := json.Marshal(map[string]any{
		"amount": formatCents(request.Amount), "currency": request.Currency,
		"metadata": map[string]string{"orderId": request.TradeNo}, "checkout": map[string]string{"redirectURL": request.ReturnURL},
	})
	endpoint := request.Config["btcpay_url"] + "/api/v1/stores/" + url.PathEscape(request.Config["btcpay_storeId"]) + "/invoices"
	body, err := service.do(ctx, http.MethodPost, endpoint, bytes.NewReader(payload), map[string]string{
		"Authorization": "token " + request.Config["btcpay_api_key"], "Content-Type": "application/json",
		"Idempotency-Key": request.IdempotencyKey,
	})
	if err != nil {
		return CheckoutResult{}, err
	}
	var response struct {
		ID           string `json:"id"`
		CheckoutLink string `json:"checkoutLink"`
	}
	if json.Unmarshal(body, &response) != nil || !validExternalID(response.ID) || !validHTTPSURL(response.CheckoutLink) {
		return CheckoutResult{}, ErrInvalidGatewayResponse
	}
	return CheckoutResult{Type: 1, Data: response.CheckoutLink, ExternalID: response.ID}, nil
}

func checkoutCoinPayments(request CheckoutRequest) (CheckoutResult, error) {
	returnURL, _ := url.Parse(request.ReturnURL)
	successURL := returnURL.Scheme + "://" + returnURL.Host
	values := url.Values{
		"cmd": {"_pay_simple"}, "reset": {"1"}, "merchant": {request.Config["coinpayments_merchant_id"]},
		"item_name": {request.TradeNo}, "item_number": {request.TradeNo}, "want_shipping": {"0"},
		"currency": {request.Config["coinpayments_currency"]}, "amountf": {formatCents(request.Amount)},
		"success_url": {successURL}, "cancel_url": {request.ReturnURL}, "ipn_url": {request.NotifyURL},
	}
	return CheckoutResult{Type: 1, Data: "https://www.coinpayments.net/index.php?" + values.Encode()}, nil
}

func (service *Service) checkoutCoinbase(ctx context.Context, request CheckoutRequest) (CheckoutResult, error) {
	payload, _ := json.Marshal(map[string]any{
		"name": "订阅套餐", "description": "订单号 " + request.TradeNo, "pricing_type": "fixed_price",
		"local_price": map[string]string{"amount": formatCents(request.Amount), "currency": request.Currency},
		"metadata":    map[string]string{"outTradeNo": request.TradeNo}, "redirect_url": request.ReturnURL, "cancel_url": request.ReturnURL,
	})
	body, err := service.do(ctx, http.MethodPost, request.Config["coinbase_url"], bytes.NewReader(payload), map[string]string{
		"X-CC-Api-Key": request.Config["coinbase_api_key"], "X-CC-Version": "2018-03-22",
		"Content-Type": "application/json", "Idempotency-Key": request.IdempotencyKey,
	})
	if err != nil {
		return CheckoutResult{}, err
	}
	var response struct {
		Data struct {
			ID        string `json:"id"`
			HostedURL string `json:"hosted_url"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &response) != nil || !validExternalID(response.Data.ID) || !validHTTPSURL(response.Data.HostedURL) {
		return CheckoutResult{}, ErrInvalidGatewayResponse
	}
	return CheckoutResult{Type: 1, Data: response.Data.HostedURL, ExternalID: response.Data.ID}, nil
}

func checkoutEPay(request CheckoutRequest) (CheckoutResult, error) {
	values := url.Values{
		"money": {formatCents(request.Amount)}, "name": {request.TradeNo}, "notify_url": {request.NotifyURL},
		"return_url": {request.ReturnURL}, "out_trade_no": {request.TradeNo}, "pid": {request.Config["pid"]},
	}
	if paymentType := request.Config["type"]; paymentType != "" {
		values.Set("type", paymentType)
	}
	digest := md5.Sum([]byte(canonicalRawValues(values) + request.Config["key"]))
	values.Set("sign", hex.EncodeToString(digest[:]))
	values.Set("sign_type", "MD5")
	return CheckoutResult{Type: 1, Data: request.Config["url"] + "/submit.php?" + values.Encode()}, nil
}

func (service *Service) checkoutMGate(ctx context.Context, request CheckoutRequest) (CheckoutResult, error) {
	values := url.Values{
		"out_trade_no": {request.TradeNo}, "total_amount": {strconv.FormatInt(request.Amount, 10)},
		"notify_url": {request.NotifyURL}, "return_url": {request.ReturnURL}, "app_id": {request.Config["mgate_app_id"]},
	}
	if currency := request.Config["mgate_source_currency"]; currency != "" {
		values.Set("source_currency", currency)
	}
	digest := md5.Sum([]byte(values.Encode() + request.Config["mgate_app_secret"]))
	values.Set("sign", hex.EncodeToString(digest[:]))
	body, err := service.do(ctx, http.MethodPost, request.Config["mgate_url"]+"/v1/gateway/fetch", strings.NewReader(values.Encode()), map[string]string{
		"Content-Type": "application/x-www-form-urlencoded", "User-Agent": "Xboard-Go MGate", "Idempotency-Key": request.IdempotencyKey,
	})
	if err != nil {
		return CheckoutResult{}, err
	}
	var response struct {
		Data struct {
			TradeNo string `json:"trade_no"`
			PayURL  string `json:"pay_url"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &response) != nil || !validExternalID(response.Data.TradeNo) || !validHTTPSURL(response.Data.PayURL) {
		return CheckoutResult{}, ErrInvalidGatewayResponse
	}
	return CheckoutResult{Type: 1, Data: response.Data.PayURL, ExternalID: response.Data.TradeNo}, nil
}

func verifyAlipayWebhook(request WebhookRequest) (VerifiedWebhook, error) {
	form, err := webhookForm(request)
	if err != nil || form.Get("trade_status") != "TRADE_SUCCESS" || form.Get("app_id") != request.Config["app_id"] {
		return VerifiedWebhook{}, ErrUnsuccessfulWebhook
	}
	publicKey, err := parseRSAPublicKey(request.Config["public_key"])
	if err != nil || verifyRSA2(publicKey, []byte(canonicalAlipayValues(form)), form.Get("sign")) != nil {
		return VerifiedWebhook{}, ErrInvalidWebhookSignature
	}
	amount, err := parseCents(form.Get("total_amount"))
	if err != nil || !validGatewayTradeNo(form.Get("out_trade_no")) || !validExternalID(form.Get("trade_no")) {
		return VerifiedWebhook{}, ErrInvalidGatewayResponse
	}
	return VerifiedWebhook{ExternalID: form.Get("trade_no"), TradeNo: form.Get("out_trade_no"), Amount: amount, Currency: "CNY"}, nil
}

func (service *Service) verifyBTCPayWebhook(ctx context.Context, request WebhookRequest) (VerifiedWebhook, error) {
	provided := request.Headers.Get("Btcpay-Sig")
	mac := hmac.New(sha256.New, []byte(request.Config["btcpay_webhook_key"]))
	_, _ = mac.Write(request.Body)
	if !constantTimeText(provided, "sha256="+hex.EncodeToString(mac.Sum(nil))) {
		return VerifiedWebhook{}, ErrInvalidWebhookSignature
	}
	var event struct {
		Type      string `json:"type"`
		InvoiceID string `json:"invoiceId"`
		StoreID   string `json:"storeId"`
	}
	if json.Unmarshal(request.Body, &event) != nil || event.Type != "InvoiceSettled" || !validExternalID(event.InvoiceID) || event.StoreID != request.Config["btcpay_storeId"] {
		return VerifiedWebhook{}, ErrUnsuccessfulWebhook
	}
	endpoint := request.Config["btcpay_url"] + "/api/v1/stores/" + url.PathEscape(request.Config["btcpay_storeId"]) + "/invoices/" + url.PathEscape(event.InvoiceID)
	body, err := service.do(ctx, http.MethodGet, endpoint, nil, map[string]string{"Authorization": "token " + request.Config["btcpay_api_key"]})
	if err != nil {
		return VerifiedWebhook{}, err
	}
	var invoice struct {
		ID       string      `json:"id"`
		StoreID  string      `json:"storeId"`
		Status   string      `json:"status"`
		Amount   json.Number `json:"amount"`
		Currency string      `json:"currency"`
		Metadata struct {
			OrderID string `json:"orderId"`
		} `json:"metadata"`
	}
	if json.Unmarshal(body, &invoice) != nil || invoice.ID != event.InvoiceID || invoice.StoreID != request.Config["btcpay_storeId"] || invoice.Status != "Settled" || !validGatewayTradeNo(invoice.Metadata.OrderID) {
		return VerifiedWebhook{}, ErrInvalidGatewayResponse
	}
	amount, err := parseCents(invoice.Amount.String())
	if err != nil || !validCurrency(invoice.Currency) {
		return VerifiedWebhook{}, ErrInvalidGatewayResponse
	}
	return VerifiedWebhook{ExternalID: event.InvoiceID, TradeNo: invoice.Metadata.OrderID, Amount: amount, Currency: invoice.Currency}, nil
}

func verifyCoinPaymentsWebhook(request WebhookRequest) (VerifiedWebhook, error) {
	mac := hmac.New(sha512.New, []byte(request.Config["coinpayments_ipn_secret"]))
	_, _ = mac.Write(request.Body)
	if !constantTimeText(strings.ToLower(request.Headers.Get("HMAC")), hex.EncodeToString(mac.Sum(nil))) {
		return VerifiedWebhook{}, ErrInvalidWebhookSignature
	}
	form, err := url.ParseQuery(string(request.Body))
	if err != nil || form.Get("merchant") != request.Config["coinpayments_merchant_id"] {
		return VerifiedWebhook{}, ErrInvalidGatewayResponse
	}
	status, err := strconv.Atoi(form.Get("status"))
	if err != nil || status != 2 && status < 100 {
		return VerifiedWebhook{}, ErrUnsuccessfulWebhook
	}
	amount, err := parseCents(form.Get("amount1"))
	currency := strings.ToUpper(form.Get("currency1"))
	if err != nil || currency != request.Config["coinpayments_currency"] || !validGatewayTradeNo(form.Get("item_number")) || !validExternalID(form.Get("txn_id")) {
		return VerifiedWebhook{}, ErrInvalidGatewayResponse
	}
	return VerifiedWebhook{ExternalID: form.Get("txn_id"), TradeNo: form.Get("item_number"), Amount: amount, Currency: currency}, nil
}

func verifyCoinbaseWebhook(request WebhookRequest) (VerifiedWebhook, error) {
	mac := hmac.New(sha256.New, []byte(request.Config["coinbase_webhook_key"]))
	_, _ = mac.Write(request.Body)
	if !constantTimeText(strings.ToLower(request.Headers.Get("X-CC-Webhook-Signature")), hex.EncodeToString(mac.Sum(nil))) {
		return VerifiedWebhook{}, ErrInvalidWebhookSignature
	}
	var payload struct {
		Event struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Data struct {
				Metadata struct {
					TradeNo string `json:"outTradeNo"`
				} `json:"metadata"`
				Pricing struct {
					Local struct {
						Amount   string `json:"amount"`
						Currency string `json:"currency"`
					} `json:"local"`
				} `json:"pricing"`
			} `json:"data"`
		} `json:"event"`
	}
	if json.Unmarshal(request.Body, &payload) != nil || payload.Event.Type != "charge:confirmed" || !validExternalID(payload.Event.ID) || !validGatewayTradeNo(payload.Event.Data.Metadata.TradeNo) {
		return VerifiedWebhook{}, ErrUnsuccessfulWebhook
	}
	amount, err := parseCents(payload.Event.Data.Pricing.Local.Amount)
	currency := strings.ToUpper(payload.Event.Data.Pricing.Local.Currency)
	if err != nil || !validCurrency(currency) {
		return VerifiedWebhook{}, ErrInvalidGatewayResponse
	}
	return VerifiedWebhook{ExternalID: payload.Event.ID, TradeNo: payload.Event.Data.Metadata.TradeNo, Amount: amount, Currency: currency}, nil
}

func verifyEPayWebhook(request WebhookRequest) (VerifiedWebhook, error) {
	form, err := webhookForm(request)
	if err != nil || form.Get("pid") != request.Config["pid"] || form.Get("trade_status") != "TRADE_SUCCESS" {
		return VerifiedWebhook{}, ErrUnsuccessfulWebhook
	}
	provided := strings.ToLower(form.Get("sign"))
	unsigned := cloneURLValues(form)
	unsigned.Del("sign")
	unsigned.Del("sign_type")
	digest := md5.Sum([]byte(canonicalRawValues(unsigned) + request.Config["key"]))
	if !constantTimeText(provided, hex.EncodeToString(digest[:])) {
		return VerifiedWebhook{}, ErrInvalidWebhookSignature
	}
	amount, err := parseCents(form.Get("money"))
	if err != nil || !validGatewayTradeNo(form.Get("out_trade_no")) || !validExternalID(form.Get("trade_no")) {
		return VerifiedWebhook{}, ErrInvalidGatewayResponse
	}
	return VerifiedWebhook{ExternalID: form.Get("trade_no"), TradeNo: form.Get("out_trade_no"), Amount: amount, Currency: "CNY"}, nil
}

func verifyMGateWebhook(request WebhookRequest) (VerifiedWebhook, error) {
	form, err := webhookForm(request)
	if err != nil || form.Get("app_id") != request.Config["mgate_app_id"] || !successfulMGateStatus(form.Get("status")) {
		return VerifiedWebhook{}, ErrUnsuccessfulWebhook
	}
	provided := strings.ToLower(form.Get("sign"))
	unsigned := cloneURLValues(form)
	unsigned.Del("sign")
	digest := md5.Sum([]byte(unsigned.Encode() + request.Config["mgate_app_secret"]))
	if !constantTimeText(provided, hex.EncodeToString(digest[:])) {
		return VerifiedWebhook{}, ErrInvalidWebhookSignature
	}
	amount, err := strconv.ParseInt(form.Get("total_amount"), 10, 64)
	currency := strings.ToUpper(form.Get("source_currency"))
	if currency == "" {
		currency = "CNY"
	}
	if err != nil || amount < 1 || amount > maxProviderMoneyCents || currency != defaultCurrency(request.Config["mgate_source_currency"]) ||
		!validGatewayTradeNo(form.Get("out_trade_no")) || !validExternalID(form.Get("trade_no")) {
		return VerifiedWebhook{}, ErrInvalidGatewayResponse
	}
	return VerifiedWebhook{ExternalID: form.Get("trade_no"), TradeNo: form.Get("out_trade_no"), Amount: amount, Currency: currency}, nil
}

func (service *Service) do(ctx context.Context, method, endpoint string, body io.Reader, headers map[string]string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, ErrInvalidGatewayRequest
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := service.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("payment provider request: %w", err)
	}
	if response == nil || response.Body == nil {
		return nil, ErrInvalidGatewayResponse
	}
	defer response.Body.Close()
	reader := io.LimitReader(response.Body, maxProviderResponseBytes+1)
	result, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read payment provider response: %w", err)
	}
	if len(result) > maxProviderResponseBytes || response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, ErrInvalidGatewayResponse
	}
	return result, nil
}

func newSecureHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: nil, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second, IdleConnTimeout: 60 * time.Second, MaxIdleConns: 32, MaxIdleConnsPerHost: 4,
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(addresses) == 0 {
			return nil, fmt.Errorf("resolve payment provider host: %w", err)
		}
		for _, resolved := range addresses {
			if unsafeProviderIP(resolved.IP) {
				return nil, errors.New("payment provider host resolves to a non-public address")
			}
		}
		var lastErr error
		for _, resolved := range addresses {
			connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			lastErr = dialErr
		}
		return nil, lastErr
	}
	return &http.Client{
		Transport: transport, Timeout: 15 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("payment provider redirects are disabled")
		},
	}
}

func unsafeProviderIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsMulticast() || address.IsUnspecified() {
		return true
	}
	for _, prefix := range nonPublicProviderPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

var nonPublicProviderPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("3fff::/20"),
}

func webhookForm(request WebhookRequest) (url.Values, error) {
	if request.Form != nil {
		return cloneURLValues(request.Form), nil
	}
	return url.ParseQuery(string(request.Body))
}

func canonicalRawValues(values url.Values) string { return canonicalValues(values, nil) }

func canonicalAlipayValues(values url.Values) string {
	return canonicalValues(values, map[string]struct{}{"sign": {}, "sign_type": {}})
}

func canonicalValues(values url.Values, excluded map[string]struct{}) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if _, skip := excluded[key]; !skip {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+values.Get(key))
	}
	return strings.Join(parts, "&")
}

func cloneURLValues(values url.Values) url.Values {
	result := make(url.Values, len(values))
	for key, entries := range values {
		result[key] = append([]string(nil), entries...)
	}
	return result
}

func parseCents(value string) (int64, error) {
	value = strings.TrimSpace(value)
	parts := strings.Split(value, ".")
	if len(parts) > 2 || len(parts) == 0 || parts[0] == "" || strings.HasPrefix(parts[0], "-") {
		return 0, ErrInvalidGatewayResponse
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > maxProviderMoneyCents/100 {
		return 0, ErrInvalidGatewayResponse
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 2 {
		return 0, ErrInvalidGatewayResponse
	}
	for len(fraction) < 2 {
		fraction += "0"
	}
	fractionValue := int64(0)
	if fraction != "" {
		fractionValue, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, ErrInvalidGatewayResponse
		}
	}
	if whole > (maxProviderMoneyCents-fractionValue)/100 {
		return 0, ErrInvalidGatewayResponse
	}
	return whole*100 + fractionValue, nil
}

func formatCents(value int64) string { return fmt.Sprintf("%d.%02d", value/100, value%100) }

func signRSA2(privateKey *rsa.PrivateKey, payload []byte) (string, error) {
	digest := sha256.Sum256(payload)
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

func verifyRSA2(publicKey *rsa.PublicKey, payload []byte, encodedSignature string) error {
	signature, err := base64.StdEncoding.DecodeString(encodedSignature)
	if err != nil {
		return ErrInvalidWebhookSignature
	}
	digest := sha256.Sum256(payload)
	if rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature) != nil {
		return ErrInvalidWebhookSignature
	}
	return nil
}

func parseRSAPrivateKey(value string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.TrimSpace(value), "\n", ""))
		if err != nil {
			return nil, err
		}
		block = &pem.Block{Bytes: decoded}
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok && rsaKey.N.BitLen() >= 2048 {
			return rsaKey, nil
		}
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil || key.N.BitLen() < 2048 {
		return nil, ErrInvalidConfig
	}
	return key, nil
}

func parseRSAPublicKey(value string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.TrimSpace(value), "\n", ""))
		if err != nil {
			return nil, err
		}
		block = &pem.Block{Bytes: decoded}
	}
	if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PublicKey); ok && rsaKey.N.BitLen() >= 2048 {
			return rsaKey, nil
		}
	}
	if key, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil && key.N.BitLen() >= 2048 {
		return key, nil
	}
	return nil, ErrInvalidConfig
}

func constantTimeText(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func validHTTPSURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

func validGatewayCallbackURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	host := strings.ToLower(parsed.Hostname())
	address := net.ParseIP(host)
	return parsed.Scheme == "http" && (host == "localhost" || address != nil && address.IsLoopback())
}

func validGatewayTradeNo(value string) bool {
	if len(value) != 25 && len(value) != 32 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if len(value) != 32 || character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func validIdempotencyKey(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func validExternalID(value string) bool {
	return len(value) >= 1 && len(value) <= 255 && !strings.ContainsAny(value, "\r\n\x00")
}

func validCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

func successfulMGateStatus(value string) bool {
	switch value {
	case "paid", "success", "TRADE_SUCCESS", "2":
		return true
	default:
		return false
	}
}

func defaultCurrency(value string) string {
	if value == "" {
		return "CNY"
	}
	return value
}
