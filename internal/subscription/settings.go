package subscription

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func nested(settings map[string]any, path string) any {
	var current any = settings
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = object[segment]
		if !ok {
			return nil
		}
	}
	return current
}

func stringSetting(settings map[string]any, path string) string {
	value, _ := nested(settings, path).(string)
	return value
}

func boolSetting(settings map[string]any, path string) bool {
	switch value := nested(settings, path).(type) {
	case bool:
		return value
	case float64:
		return value != 0
	case string:
		parsed, _ := strconv.ParseBool(value)
		return parsed
	default:
		return false
	}
}

func stringsSetting(settings map[string]any, path string) []string {
	value := nested(settings, path)
	switch typed := value.(type) {
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
		return result
	case []string:
		return append([]string(nil), typed...)
	case string:
		if typed != "" {
			return []string{typed}
		}
	}
	return nil
}

func wrapAddress(host string) string {
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return host
	}
	if net.ParseIP(host) != nil && strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func rawURLEncode(value string) string {
	return strings.ReplaceAll(url.PathEscape(value), "+", "%2B")
}

type queryValue struct {
	key   string
	value any
}

func buildQuery(values ...queryValue) string {
	parts := make([]string, 0, len(values))
	for _, pair := range values {
		if pair.value == nil {
			continue
		}
		var value string
		switch typed := pair.value.(type) {
		case bool:
			if typed {
				value = "1"
			} else {
				value = "0"
			}
		case string:
			value = typed
		case int:
			value = strconv.Itoa(typed)
		case int64:
			value = strconv.FormatInt(typed, 10)
		case float64:
			value = strconv.FormatFloat(typed, 'f', -1, 64)
		default:
			value = fmt.Sprint(typed)
		}
		parts = append(parts, url.QueryEscape(pair.key)+"="+url.QueryEscape(value))
	}
	return strings.Join(parts, "&")
}

var fingerprints = [...]string{"chrome", "firefox", "safari", "ios", "edge", "qq"}

func randomFingerprint() string {
	index, err := rand.Int(rand.Reader, big.NewInt(int64(len(fingerprints))))
	if err != nil {
		return "chrome"
	}
	return fingerprints[index.Int64()]
}

func tlsFingerprint(settings map[string]any, selectFingerprint func() string) string {
	utls := nested(settings, "utls")
	if object, ok := utls.(map[string]any); ok {
		if !boolSetting(object, "enabled") {
			return ""
		}
		fingerprint := stringSetting(object, "fingerprint")
		if fingerprint == "" {
			fingerprint = "chrome"
		}
		if fingerprint != "random" {
			return fingerprint
		}
	}
	return selectFingerprint()
}
