package payment

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
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
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestCheckoutBuildsAllLockedCoreProviderContracts(t *testing.T) {
	privateKey, publicKey := testRSAKeyPair(t)
	doer := HTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		switch request.URL.Host {
		case "btcpay.example.test":
			if request.URL.Path != "/api/v1/stores/store-one/invoices" || request.Header.Get("Authorization") != "token btcpay-api-secret" || !strings.Contains(string(body), `"orderId":"2026082612000000000000001"`) {
				t.Fatalf("unexpected BTCPay request: %s headers=%v body=%s", request.URL, request.Header, body)
			}
			return jsonResponse(`{"id":"invoice-one","checkoutLink":"https://checkout.example.test/invoice-one"}`), nil
		case "coinbase.example.test":
			if request.Header.Get("X-CC-Api-Key") != "coinbase-api-secret" || !strings.Contains(string(body), `"amount":"1026.23"`) {
				t.Fatalf("unexpected Coinbase request: %s headers=%v body=%s", request.URL, request.Header, body)
			}
			return jsonResponse(`{"data":{"id":"charge-one","hosted_url":"https://commerce.example.test/charges/one"}}`), nil
		case "mgate.example.test":
			values, _ := url.ParseQuery(string(body))
			if values.Get("total_amount") != "102623" || values.Get("app_id") != "mgate-app" || values.Get("sign") == "" {
				t.Fatalf("unexpected MGate body: %s", body)
			}
			return jsonResponse(`{"data":{"trade_no":"mgate-one","pay_url":"https://mgate-checkout.example.test/pay/one"}}`), nil
		case "openapi.alipay.com":
			values, _ := url.ParseQuery(string(body))
			if values.Get("app_id") != "alipay-app" || values.Get("method") != "alipay.trade.precreate" || values.Get("sign") == "" {
				t.Fatalf("unexpected Alipay body: %s", body)
			}
			raw := `{"code":"10000","msg":"Success","out_trade_no":"2026082612000000000000001","qr_code":"https://qr.example.test/alipay-one"}`
			signature := signTestRSA(t, privateKey, []byte(raw))
			return jsonResponse(`{"alipay_trade_precreate_response":` + raw + `,"sign":` + strconvQuote(signature) + `}`), nil
		default:
			t.Fatalf("unexpected provider request: %s", request.URL)
			return nil, nil
		}
	})
	service := NewService(Options{HTTPClient: doer})
	base := CheckoutRequest{
		TradeNo: "2026082612000000000000001", Amount: 102_623, Currency: "CNY",
		NotifyURL:      "https://panel.example.test/api/v1/guest/payment/notify/provider/uuid",
		ReturnURL:      "https://panel.example.test/#/order/2026082612000000000000001",
		IdempotencyKey: strings.Repeat("a", 64),
	}
	tests := []struct {
		provider store.PaymentProvider
		config   map[string]string
		typeWant int
		dataPart string
		external string
	}{
		{store.PaymentProviderAlipayF2F, map[string]string{"app_id": "alipay-app", "private_key": privateKey, "public_key": publicKey, "product_name": "订阅"}, 0, "qr.example.test", ""},
		{store.PaymentProviderBTCPay, map[string]string{"btcpay_url": "https://btcpay.example.test", "btcpay_storeId": "store-one", "btcpay_api_key": "btcpay-api-secret", "btcpay_webhook_key": "btcpay-webhook-secret"}, 1, "checkout.example.test", "invoice-one"},
		{store.PaymentProviderCoinPayments, map[string]string{"coinpayments_merchant_id": "merchant-one", "coinpayments_ipn_secret": "ipn-secret", "coinpayments_currency": "CNY"}, 1, "coinpayments.net/index.php", ""},
		{store.PaymentProviderCoinbase, map[string]string{"coinbase_url": "https://coinbase.example.test/charges", "coinbase_api_key": "coinbase-api-secret", "coinbase_webhook_key": "coinbase-webhook-secret"}, 1, "commerce.example.test", "charge-one"},
		{store.PaymentProviderEPay, map[string]string{"url": "https://epay.example.test", "pid": "1001", "key": "epay-secret", "type": "alipay"}, 1, "epay.example.test/submit.php", ""},
		{store.PaymentProviderMGate, map[string]string{"mgate_url": "https://mgate.example.test", "mgate_app_id": "mgate-app", "mgate_app_secret": "mgate-secret", "mgate_source_currency": "CNY"}, 1, "mgate-checkout.example.test", "mgate-one"},
	}
	for _, test := range tests {
		t.Run(string(test.provider), func(t *testing.T) {
			request := base
			request.Provider, request.Config = test.provider, test.config
			result, err := service.Checkout(context.Background(), request)
			if err != nil || result.Type != test.typeWant || !strings.Contains(result.Data, test.dataPart) || result.ExternalID != test.external {
				t.Fatalf("Checkout() = (%#v, %v)", result, err)
			}
		})
	}
}

func TestCheckoutUsesDeploymentCurrencyOrRejectsIncompatibleProvider(t *testing.T) {
	service := NewService(Options{HTTPClient: HTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), `"currency":"USD"`) {
			t.Fatalf("BTCPay request did not preserve deployment currency: %s", body)
		}
		return jsonResponse(`{"id":"invoice-usd","checkoutLink":"https://checkout.example.test/invoice-usd"}`), nil
	})})
	base := CheckoutRequest{
		TradeNo: "2026082612000000000000001", Amount: 102_623, Currency: "USD",
		NotifyURL: "https://panel.example.test/api/v1/guest/payment/notify/provider/uuid",
		ReturnURL: "https://panel.example.test/#/order/2026082612000000000000001", IdempotencyKey: strings.Repeat("a", 64),
	}
	btcpay := base
	btcpay.Provider = store.PaymentProviderBTCPay
	btcpay.Config = map[string]string{"btcpay_url": "https://btcpay.example.test", "btcpay_storeId": "store-one", "btcpay_api_key": "api", "btcpay_webhook_key": "webhook"}
	if result, err := service.Checkout(t.Context(), btcpay); err != nil || result.ExternalID != "invoice-usd" {
		t.Fatalf("USD BTCPay checkout = (%#v, %v)", result, err)
	}
	for _, incompatible := range []CheckoutRequest{
		{Provider: store.PaymentProviderAlipayF2F, Config: map[string]string{"app_id": "app", "private_key": "key", "public_key": "key"}},
		{Provider: store.PaymentProviderCoinPayments, Config: map[string]string{"coinpayments_merchant_id": "merchant", "coinpayments_ipn_secret": "secret", "coinpayments_currency": "CNY"}},
		{Provider: store.PaymentProviderMGate, Config: map[string]string{"mgate_url": "https://mgate.example.test", "mgate_app_id": "app", "mgate_app_secret": "secret", "mgate_source_currency": "CNY"}},
	} {
		incompatible.TradeNo, incompatible.Amount, incompatible.Currency = base.TradeNo, base.Amount, base.Currency
		incompatible.NotifyURL, incompatible.ReturnURL, incompatible.IdempotencyKey = base.NotifyURL, base.ReturnURL, base.IdempotencyKey
		if _, err := service.Checkout(t.Context(), incompatible); !errors.Is(err, ErrInvalidGatewayRequest) {
			t.Fatalf("incompatible %s checkout error = %v", incompatible.Provider, err)
		}
	}
}

func TestVerifyWebhookChecksSignatureSuccessOrderAmountCurrencyAndProviderIdentity(t *testing.T) {
	privateKey, publicKey := testRSAKeyPair(t)
	service := NewService(Options{HTTPClient: HTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/v1/stores/store-one/invoices/invoice-one" {
			t.Fatalf("unexpected BTCPay verification request: %s %s", request.Method, request.URL)
		}
		return jsonResponse(`{"id":"invoice-one","storeId":"store-one","status":"Settled","amount":"1026.23","currency":"CNY","metadata":{"orderId":"2026082612000000000000001"}}`), nil
	})})
	tradeNo := "2026082612000000000000001"

	coinPaymentsBody := `merchant=merchant-one&status=100&item_number=` + tradeNo + `&amount1=1026.23&currency1=CNY&txn_id=coin-txn-one`
	coinPaymentsMAC := hmac.New(sha512.New, []byte("ipn-secret"))
	_, _ = coinPaymentsMAC.Write([]byte(coinPaymentsBody))

	coinbaseBody := `{"event":{"id":"event-one","type":"charge:confirmed","data":{"id":"charge-one","metadata":{"outTradeNo":"` + tradeNo + `"},"pricing":{"local":{"amount":"1026.23","currency":"CNY"}}}}}`
	coinbaseMAC := hmac.New(sha256.New, []byte("coinbase-webhook-secret"))
	_, _ = coinbaseMAC.Write([]byte(coinbaseBody))

	btcpayBody := `{"type":"InvoiceSettled","invoiceId":"invoice-one","storeId":"store-one"}`
	btcpayMAC := hmac.New(sha256.New, []byte("btcpay-webhook-secret"))
	_, _ = btcpayMAC.Write([]byte(btcpayBody))

	epayValues := url.Values{"pid": {"1001"}, "trade_status": {"TRADE_SUCCESS"}, "out_trade_no": {tradeNo}, "trade_no": {"epay-one"}, "money": {"1026.23"}}
	epayDigest := md5.Sum([]byte(canonicalRawValues(epayValues) + "epay-secret"))
	epayValues.Set("sign", hex.EncodeToString(epayDigest[:]))
	epayValues.Set("sign_type", "MD5")

	mgateValues := url.Values{"app_id": {"mgate-app"}, "status": {"paid"}, "out_trade_no": {tradeNo}, "trade_no": {"mgate-one"}, "total_amount": {"102623"}, "source_currency": {"CNY"}}
	mgateDigest := md5.Sum([]byte(mgateValues.Encode() + "mgate-secret"))
	mgateValues.Set("sign", hex.EncodeToString(mgateDigest[:]))

	alipayValues := url.Values{"app_id": {"alipay-app"}, "trade_status": {"TRADE_SUCCESS"}, "out_trade_no": {tradeNo}, "trade_no": {"alipay-one"}, "total_amount": {"1026.23"}}
	alipayValues.Set("sign_type", "RSA2")
	alipayValues.Set("sign", signTestRSA(t, privateKey, []byte(canonicalAlipayValues(alipayValues))))

	tests := []struct {
		name     string
		request  WebhookRequest
		external string
	}{
		{"AlipayF2F", WebhookRequest{Provider: store.PaymentProviderAlipayF2F, Config: map[string]string{"app_id": "alipay-app", "private_key": privateKey, "public_key": publicKey}, Form: alipayValues}, "alipay-one"},
		{"BTCPay", WebhookRequest{Provider: store.PaymentProviderBTCPay, Config: map[string]string{"btcpay_url": "https://btcpay.example.test", "btcpay_storeId": "store-one", "btcpay_api_key": "api", "btcpay_webhook_key": "btcpay-webhook-secret"}, Body: []byte(btcpayBody), Headers: http.Header{"Btcpay-Sig": {"sha256=" + hex.EncodeToString(btcpayMAC.Sum(nil))}}}, "invoice-one"},
		{"CoinPayments", WebhookRequest{Provider: store.PaymentProviderCoinPayments, Config: map[string]string{"coinpayments_merchant_id": "merchant-one", "coinpayments_ipn_secret": "ipn-secret", "coinpayments_currency": "CNY"}, Body: []byte(coinPaymentsBody), Headers: http.Header{"Hmac": {hex.EncodeToString(coinPaymentsMAC.Sum(nil))}}}, "coin-txn-one"},
		{"Coinbase", WebhookRequest{Provider: store.PaymentProviderCoinbase, Config: map[string]string{"coinbase_url": "https://coinbase.example.test", "coinbase_api_key": "api", "coinbase_webhook_key": "coinbase-webhook-secret"}, Body: []byte(coinbaseBody), Headers: http.Header{"X-Cc-Webhook-Signature": {hex.EncodeToString(coinbaseMAC.Sum(nil))}}}, "event-one"},
		{"EPay", WebhookRequest{Provider: store.PaymentProviderEPay, Config: map[string]string{"url": "https://epay.example.test", "pid": "1001", "key": "epay-secret"}, Form: epayValues}, "epay-one"},
		{"MGate", WebhookRequest{Provider: store.PaymentProviderMGate, Config: map[string]string{"mgate_url": "https://mgate.example.test", "mgate_app_id": "mgate-app", "mgate_app_secret": "mgate-secret", "mgate_source_currency": "CNY"}, Form: mgateValues}, "mgate-one"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verified, err := service.VerifyWebhook(context.Background(), test.request)
			if err != nil || verified.TradeNo != tradeNo || verified.ExternalID != test.external || verified.Amount != 102_623 || verified.Currency != "CNY" {
				t.Fatalf("VerifyWebhook() = (%#v, %v)", verified, err)
			}
			bad := test.request
			bad.Body = append([]byte(nil), test.request.Body...)
			bad.Form = cloneValues(test.request.Form)
			if len(bad.Body) > 0 {
				bad.Body = append(bad.Body, ' ')
			} else {
				bad.Form.Set("out_trade_no", strings.Repeat("9", 25))
			}
			if _, err := service.VerifyWebhook(context.Background(), bad); err == nil {
				t.Fatal("VerifyWebhook() accepted a tampered callback")
			}
		})
	}
}

func testRSAKeyPair(t *testing.T) (string, string) {
	t.Helper()
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&private.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})), string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
}

func signTestRSA(t *testing.T, privatePEM string, payload []byte) string {
	t.Helper()
	private, err := parseRSAPrivateKey(privatePEM)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	signature, err := rsa.SignPKCS1v15(rand.Reader, private, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(signature)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func cloneValues(values url.Values) url.Values {
	if values == nil {
		return nil
	}
	result := make(url.Values, len(values))
	for key, entries := range values {
		result[key] = append([]string(nil), entries...)
	}
	return result
}

type HTTPDoerFunc func(*http.Request) (*http.Response, error)

func (function HTTPDoerFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestParseProviderAmountRejectsFloatAmbiguityAndOverflow(t *testing.T) {
	for _, value := range []string{"", "-1.00", "1.001", "NaN", "90000000000000.01"} {
		if _, err := parseCents(value); err == nil {
			t.Errorf("parseCents(%q) accepted invalid amount", value)
		}
	}
	for value, want := range map[string]int64{"0.01": 1, "1026.23": 102_623, "1": 100} {
		if got, err := parseCents(value); err != nil || got != want {
			t.Errorf("parseCents(%q) = (%d, %v), want %d", value, got, err, want)
		}
	}
}

func TestProviderHTTPBoundaryEnforcesTimeoutRedirectResponseLimitAndPublicAddresses(t *testing.T) {
	client := newSecureHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || client.Timeout != 15*time.Second || transport.Proxy != nil || transport.TLSHandshakeTimeout != 5*time.Second ||
		transport.ResponseHeaderTimeout != 10*time.Second || client.CheckRedirect == nil {
		t.Fatalf("unsafe provider HTTP client: %#v transport=%#v", client, transport)
	}
	if err := client.CheckRedirect(&http.Request{}, nil); err == nil {
		t.Fatal("provider HTTP client accepted a redirect")
	}
	for _, address := range []string{
		"127.0.0.1", "10.0.0.1", "100.64.0.1", "169.254.169.254", "192.0.2.1", "198.18.0.1",
		"198.51.100.1", "203.0.113.1", "240.0.0.1", "::1", "64:ff9b::7f00:1", "64:ff9b:1::1",
		"100::1", "2001:db8::1", "2002:7f00:1::", "3fff::1", "fc00::1", "fe80::1", "ff02::1",
	} {
		if !unsafeProviderIP(net.ParseIP(address)) {
			t.Errorf("unsafeProviderIP(%s) = false", address)
		}
	}
	for _, address := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		if unsafeProviderIP(net.ParseIP(address)) {
			t.Errorf("unsafeProviderIP(%s) = true", address)
		}
	}

	service := NewService(Options{HTTPClient: HTTPDoerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxProviderResponseBytes+1)))}, nil
	})})
	if _, err := service.do(context.Background(), http.MethodGet, "https://provider.example.test", nil, nil); err != ErrInvalidGatewayResponse {
		t.Fatalf("oversized provider response error = %v, want ErrInvalidGatewayResponse", err)
	}
}

func TestGatewayCallbackURLAllowsHTTPSAndLocalDevelopmentOnly(t *testing.T) {
	for _, value := range []string{"https://panel.example.test/callback", "http://127.0.0.1:4173/callback", "http://[::1]:4173/callback", "http://localhost:4173/callback"} {
		if !validGatewayCallbackURL(value) {
			t.Errorf("validGatewayCallbackURL(%q) = false", value)
		}
	}
	for _, value := range []string{"http://panel.example.test/callback", "http://10.0.0.1/callback", "ftp://localhost/callback", "https://user:password@panel.example.test/callback"} {
		if validGatewayCallbackURL(value) {
			t.Errorf("validGatewayCallbackURL(%q) = true", value)
		}
	}
}

func ExampleCheckoutResult() {
	result := CheckoutResult{Type: 1, Data: "https://checkout.example.test/order", ExternalID: "invoice-one"}
	fmt.Println(result.Type, result.ExternalID)
	// Output: 1 invoice-one
}
