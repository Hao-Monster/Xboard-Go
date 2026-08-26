package payment

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestDefinitionsCoverLockedLegacyProviderForms(t *testing.T) {
	want := map[store.PaymentProvider][]string{
		store.PaymentProviderAlipayF2F:    {"app_id", "private_key", "public_key", "product_name"},
		store.PaymentProviderBTCPay:       {"btcpay_url", "btcpay_storeId", "btcpay_api_key", "btcpay_webhook_key"},
		store.PaymentProviderCoinPayments: {"coinpayments_merchant_id", "coinpayments_ipn_secret", "coinpayments_currency"},
		store.PaymentProviderCoinbase:     {"coinbase_url", "coinbase_api_key", "coinbase_webhook_key"},
		store.PaymentProviderEPay:         {"url", "pid", "key", "type"},
		store.PaymentProviderMGate:        {"mgate_url", "mgate_app_id", "mgate_app_secret", "mgate_source_currency"},
	}
	definitions := Definitions()
	if len(definitions) != len(want) {
		t.Fatalf("Definitions() count = %d, want %d", len(definitions), len(want))
	}
	for provider, keys := range want {
		definition, ok := DefinitionFor(provider)
		if !ok {
			t.Fatalf("DefinitionFor(%q) is missing", provider)
		}
		got := make([]string, 0, len(definition.Fields))
		for _, field := range definition.Fields {
			got = append(got, field.Key)
		}
		if !reflect.DeepEqual(got, keys) {
			t.Fatalf("DefinitionFor(%q) fields = %#v, want %#v", provider, got, keys)
		}
	}
}

func TestMergeConfigPreservesAndExplicitlyClearsSecrets(t *testing.T) {
	existing := map[string]string{
		"btcpay_url": "https://btcpay.example.test", "btcpay_storeId": "store-one",
		"btcpay_api_key": "old-api-secret", "btcpay_webhook_key": "old-webhook-secret",
	}
	merged, err := MergeConfig(store.PaymentProviderBTCPay, map[string]string{
		"btcpay_url": "https://new.example.test/", "btcpay_storeId": "store-two", "btcpay_api_key": "", "btcpay_webhook_key": "new-webhook-secret",
	}, existing, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if merged["btcpay_url"] != "https://new.example.test" || merged["btcpay_api_key"] != "old-api-secret" || merged["btcpay_webhook_key"] != "new-webhook-secret" {
		t.Fatalf("merged config = %#v", merged)
	}
	if _, err := MergeConfig(store.PaymentProviderBTCPay, map[string]string{
		"btcpay_url": "https://new.example.test", "btcpay_storeId": "store-two",
	}, existing, []string{"btcpay_api_key"}, false); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("clear required secret error = %v, want ErrInvalidConfig", err)
	}
	if _, err := MergeConfig(store.PaymentProviderBTCPay, map[string]string{
		"btcpay_url": "https://new.example.test", "btcpay_storeId": "store-two", "attacker": "value",
	}, existing, nil, false); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("unknown field error = %v, want ErrInvalidConfig", err)
	}
}

func TestPaymentConfigEncryptionIsPurposeIsolatedAndSanitized(t *testing.T) {
	cipher, err := appsettings.NewCipher(bytes.Repeat([]byte{0x61}, 32))
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]string{"coinpayments_merchant_id": "merchant", "coinpayments_ipn_secret": "do-not-return", "coinpayments_currency": "cny"}
	normalized, err := MergeConfig(store.PaymentProviderCoinPayments, config, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := SealConfig(cipher, store.PaymentProviderCoinPayments, normalized)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("do-not-return")) {
		t.Fatal("sealed config contains plaintext secret")
	}
	opened, err := OpenConfig(cipher, store.PaymentProviderCoinPayments, sealed)
	if err != nil || !reflect.DeepEqual(opened, normalized) {
		t.Fatalf("OpenConfig() = (%#v, %v), want %#v", opened, err, normalized)
	}
	if _, err := cipher.DecryptFor(appsettings.SMTPPasswordPurpose, sealed); err == nil {
		t.Fatal("SMTP purpose decrypted payment config")
	}
	public, configured := SanitizeConfig(store.PaymentProviderCoinPayments, opened)
	if public["coinpayments_merchant_id"] != "merchant" || public["coinpayments_currency"] != "CNY" || public["coinpayments_ipn_secret"] != "" {
		t.Fatalf("public config = %#v", public)
	}
	if !reflect.DeepEqual(configured, []string{"coinpayments_ipn_secret"}) {
		t.Fatalf("configured fields = %#v", configured)
	}
}

func TestMergeConfigRejectsUnsafeEndpointsAndInvalidProviderValues(t *testing.T) {
	for name, input := range map[string]map[string]string{
		"credentials": {"url": "https://user:pass@example.test", "pid": "1", "key": "secret", "type": "alipay"},
		"query":       {"url": "https://example.test?next=http://127.0.0.1", "pid": "1", "key": "secret", "type": "alipay"},
		"insecure":    {"url": "http://example.test", "pid": "1", "key": "secret", "type": "alipay"},
		"bad type":    {"url": "https://example.test", "pid": "1", "key": "secret", "type": "bad/type"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := MergeConfig(store.PaymentProviderEPay, input, nil, nil, true); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("MergeConfig() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}
