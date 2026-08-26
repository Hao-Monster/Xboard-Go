package payment

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

var ErrInvalidConfig = errors.New("invalid payment configuration")

type Field struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required"`
	Secret      bool     `json:"secret"`
	Options     []string `json:"options,omitempty"`
	ValueKind   string   `json:"-"`
}

type Definition struct {
	Provider store.PaymentProvider `json:"provider"`
	Label    string                `json:"label"`
	Fields   []Field               `json:"fields"`
}

var providerDefinitions = []Definition{
	{Provider: store.PaymentProviderAlipayF2F, Label: "支付宝当面付", Fields: []Field{
		{Key: "app_id", Label: "支付宝APPID", Type: "text", Description: "支付宝开放平台应用的 APPID", Required: true},
		{Key: "private_key", Label: "支付宝私钥", Type: "textarea", Description: "应用私钥，用于签名", Required: true, Secret: true},
		{Key: "public_key", Label: "支付宝公钥", Type: "textarea", Description: "支付宝公钥，用于验签", Required: true, Secret: true},
		{Key: "product_name", Label: "自定义商品名称", Type: "text", Description: "将会体现在支付宝账单中"},
	}},
	{Provider: store.PaymentProviderBTCPay, Label: "BTCPay", Fields: []Field{
		{Key: "btcpay_url", Label: "API接口所在网址", Type: "url", Description: "BTCPay 服务的 HTTPS 地址", Required: true, ValueKind: "url"},
		{Key: "btcpay_storeId", Label: "Store ID", Type: "text", Description: "BTCPay 商店标识符", Required: true},
		{Key: "btcpay_api_key", Label: "API KEY", Type: "password", Required: true, Secret: true},
		{Key: "btcpay_webhook_key", Label: "WEBHOOK KEY", Type: "password", Required: true, Secret: true},
	}},
	{Provider: store.PaymentProviderCoinPayments, Label: "CoinPayments", Fields: []Field{
		{Key: "coinpayments_merchant_id", Label: "Merchant ID", Type: "text", Required: true},
		{Key: "coinpayments_ipn_secret", Label: "IPN Secret", Type: "password", Required: true, Secret: true},
		{Key: "coinpayments_currency", Label: "货币代码", Type: "text", Required: true, ValueKind: "currency"},
	}},
	{Provider: store.PaymentProviderCoinbase, Label: "Coinbase", Fields: []Field{
		{Key: "coinbase_url", Label: "接口地址", Type: "url", Description: "Coinbase Commerce API HTTPS 地址", Required: true, ValueKind: "url"},
		{Key: "coinbase_api_key", Label: "API KEY", Type: "password", Required: true, Secret: true},
		{Key: "coinbase_webhook_key", Label: "WEBHOOK KEY", Type: "password", Required: true, Secret: true},
	}},
	{Provider: store.PaymentProviderEPay, Label: "易支付", Fields: []Field{
		{Key: "url", Label: "支付网关地址", Type: "url", Description: "支付网关 HTTPS 地址", Required: true, ValueKind: "url"},
		{Key: "pid", Label: "商户ID", Type: "text", Required: true},
		{Key: "key", Label: "通信密钥", Type: "password", Required: true, Secret: true},
		{Key: "type", Label: "支付类型", Type: "text", Description: "例如 alipay、wxpay 或 qqpay", ValueKind: "payment-type"},
	}},
	{Provider: store.PaymentProviderMGate, Label: "MGate", Fields: []Field{
		{Key: "mgate_url", Label: "API地址", Type: "url", Description: "MGate 支付网关 HTTPS 地址", Required: true, ValueKind: "url"},
		{Key: "mgate_app_id", Label: "APP ID", Type: "text", Required: true},
		{Key: "mgate_app_secret", Label: "App Secret", Type: "password", Required: true, Secret: true},
		{Key: "mgate_source_currency", Label: "源货币", Type: "text", Description: "默认 CNY", ValueKind: "currency"},
	}},
}

type configEnvelope struct {
	Version  int                   `json:"version"`
	Provider store.PaymentProvider `json:"provider"`
	Values   map[string]string     `json:"values"`
}

func Definitions() []Definition {
	result := make([]Definition, len(providerDefinitions))
	for index, definition := range providerDefinitions {
		result[index] = definition
		result[index].Fields = append([]Field(nil), definition.Fields...)
	}
	return result
}

func DefinitionFor(provider store.PaymentProvider) (Definition, bool) {
	for _, definition := range providerDefinitions {
		if definition.Provider == provider {
			definition.Fields = append([]Field(nil), definition.Fields...)
			return definition, true
		}
	}
	return Definition{}, false
}

func MergeConfig(provider store.PaymentProvider, incoming, existing map[string]string, clearFields []string, creating bool) (map[string]string, error) {
	definition, exists := DefinitionFor(provider)
	if !exists || len(incoming) > 32 || len(existing) > 32 || len(clearFields) > 32 {
		return nil, ErrInvalidConfig
	}
	fields := make(map[string]Field, len(definition.Fields))
	for _, field := range definition.Fields {
		fields[field.Key] = field
	}
	for key := range incoming {
		if _, known := fields[key]; !known {
			return nil, ErrInvalidConfig
		}
	}
	cleared := make(map[string]struct{}, len(clearFields))
	for _, key := range clearFields {
		field, known := fields[key]
		if !known || !field.Secret {
			return nil, ErrInvalidConfig
		}
		if _, duplicate := cleared[key]; duplicate {
			return nil, ErrInvalidConfig
		}
		cleared[key] = struct{}{}
	}
	result := make(map[string]string, len(definition.Fields))
	for _, field := range definition.Fields {
		value, supplied := incoming[field.Key]
		value = strings.TrimSpace(value)
		if field.Secret && value == "" {
			if _, clear := cleared[field.Key]; clear {
				value = ""
			} else if existingValue := existing[field.Key]; existingValue != "" {
				value = existingValue
			}
		} else if !supplied && !creating {
			value = existing[field.Key]
		}
		normalized, err := normalizeFieldValue(field, value)
		if err != nil || field.Required && normalized == "" {
			return nil, ErrInvalidConfig
		}
		if normalized != "" {
			result[field.Key] = normalized
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 4096 {
		return nil, ErrInvalidConfig
	}
	return result, nil
}

func SealConfig(cipher *appsettings.Cipher, provider store.PaymentProvider, config map[string]string) ([]byte, error) {
	normalized, err := MergeConfig(provider, config, nil, nil, true)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(configEnvelope{Version: 1, Provider: provider, Values: normalized})
	if err != nil {
		return nil, fmt.Errorf("encode payment configuration: %w", err)
	}
	sealed, err := cipher.EncryptFor(appsettings.PaymentConfigPurpose, encoded)
	if err != nil {
		return nil, fmt.Errorf("encrypt payment configuration: %w", err)
	}
	return sealed, nil
}

func OpenConfig(cipher *appsettings.Cipher, provider store.PaymentProvider, ciphertext []byte) (map[string]string, error) {
	plaintext, err := cipher.DecryptFor(appsettings.PaymentConfigPurpose, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt payment configuration: %w", err)
	}
	defer clearBytes(plaintext)
	var envelope configEnvelope
	if err := json.Unmarshal(plaintext, &envelope); err != nil || envelope.Version != 1 || envelope.Provider != provider {
		return nil, ErrInvalidConfig
	}
	normalized, err := MergeConfig(provider, envelope.Values, nil, nil, true)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

func SanitizeConfig(provider store.PaymentProvider, config map[string]string) (map[string]string, []string) {
	definition, exists := DefinitionFor(provider)
	if !exists {
		return map[string]string{}, []string{}
	}
	public := make(map[string]string)
	configured := make([]string, 0)
	for _, field := range definition.Fields {
		value := config[field.Key]
		if field.Secret {
			if value != "" {
				configured = append(configured, field.Key)
			}
			continue
		}
		if value != "" {
			public[field.Key] = value
		}
	}
	sort.Strings(configured)
	return public, configured
}

func normalizeFieldValue(field Field, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if !utf8.ValidString(value) || len(value) > 4096 || containsUnsafeControl(value) {
		return "", ErrInvalidConfig
	}
	switch field.ValueKind {
	case "url":
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", ErrInvalidConfig
		}
		return strings.TrimRight(value, "/"), nil
	case "currency":
		value = strings.ToUpper(value)
		if len(value) != 3 {
			return "", ErrInvalidConfig
		}
		for _, character := range value {
			if character < 'A' || character > 'Z' {
				return "", ErrInvalidConfig
			}
		}
	case "payment-type":
		if len(value) > 32 {
			return "", ErrInvalidConfig
		}
		for _, character := range value {
			if character < 'a' || character > 'z' {
				if character < '0' || character > '9' {
					if character != '_' && character != '-' {
						return "", ErrInvalidConfig
					}
				}
			}
		}
	}
	return value, nil
}

func containsUnsafeControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\r' && character != '\t' {
			return true
		}
	}
	return false
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
