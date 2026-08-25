package subscription

import (
	"fmt"
	"strconv"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type Response struct {
	Body        []byte
	ContentType string
	Headers     map[string]string
}

type RenderInput struct {
	Account         store.SubscriptionAccount
	Nodes           []PreparedNode
	Client          ClientInfo
	AppName         string
	AppURL          string
	RequestHost     string
	SubscriptionURL string
	Templates       map[string]string
	Fingerprint     func() string
}

func Render(input RenderInput) (Response, error) {
	input.Nodes = compatibleNodes(input.Nodes, input.Client)
	if input.Fingerprint == nil {
		input.Fingerprint = randomFingerprint
	}
	switch input.Client.Kind {
	case KindShadowsocks:
		return renderShadowsocks(input)
	case KindGeneral:
		return renderGeneral(input)
	case KindClash, KindClashMeta, KindStash:
		return renderMihomo(input)
	case KindSingBox:
		return renderSingBox(input)
	case KindSurge, KindSurfboard:
		return renderManagedConfig(input)
	case KindShadowrocket, KindQuantumultX, KindLoon:
		return renderURIClient(input)
	default:
		return Response{}, fmt.Errorf("unsupported subscription output %q", input.Client.Kind)
	}
}

func subscriptionUserInfo(account store.SubscriptionAccount) string {
	expiry := ""
	if account.ExpiredAt != nil {
		expiry = strconv.FormatInt(account.ExpiredAt.Unix(), 10)
	}
	return fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%s", account.TrafficUpload, account.TrafficDownload, account.TransferEnable, expiry)
}

func compatibleNodes(nodes []PreparedNode, client ClientInfo) []PreparedNode {
	allowed := allowedProtocols(client.Kind)
	result := make([]PreparedNode, 0, len(nodes))
	for _, node := range nodes {
		if _, exists := allowed[node.Type]; !exists {
			continue
		}
		if !globallyCompatible(node, client.Kind) {
			continue
		}
		if client.Kind == KindGeneral && numberValue(node.ProtocolSettings["version"]) == 2 && client.Version != "" {
			switch client.Name {
			case "v2rayn":
				if !VersionAtLeast(client.Version, "6.31") {
					continue
				}
			case "v2rayng":
				if !VersionAtLeast(client.Version, "1.9.5") {
					continue
				}
			}
		}
		applyClientRules := client.Name != "" && (client.Version != "" || client.Kind == KindClashMeta || client.Kind == KindStash)
		if applyClientRules && !clientNodeCompatible(node, client) {
			continue
		}
		result = append(result, node)
	}
	return result
}

func globallyCompatible(node PreparedNode, kind Kind) bool {
	settings := node.ProtocolSettings
	switch kind {
	case KindClashMeta:
		network := stringSetting(settings, "network")
		switch node.Type {
		case "vmess", "vless":
			return containsString([]string{"tcp", "ws", "grpc", "http", "h2", "httpupgrade", "xhttp"}, network)
		case "trojan":
			return containsString([]string{"tcp", "ws", "grpc", "httpupgrade"}, network)
		}
	case KindStash:
		if node.Type == "trojan" && numberValue(nested(settings, "tls")) == 2 {
			return false
		}
		if node.Type == "vmess" && stringSetting(settings, "network") == "httpupgrade" {
			return false
		}
	}
	return true
}

func clientNodeCompatible(node PreparedNode, client ClientInfo) bool {
	settings := node.ProtocolSettings
	switch client.Kind {
	case KindShadowrocket:
		if node.Type == "hysteria" && numberValue(nested(settings, "version")) == 2 && !VersionAtLeast(client.Version, "1993") {
			return false
		}
		if node.Type == "trojan" {
			network := stringSetting(settings, "network")
			return network == "tcp" || network == "ws" || network == "grpc" || network == "h2" || network == "httpupgrade"
		}
	case KindLoon:
		if node.Type == "hysteria" && numberValue(nested(settings, "version")) == 2 && !VersionAtLeast(client.Version, "637") {
			return false
		}
		if node.Type == "trojan" {
			minimum := "3.2.1"
			if numberValue(nested(settings, "tls")) == 2 {
				minimum = "999.9.9"
			}
			return VersionAtLeast(client.Version, minimum)
		}
	case KindSurge:
		if node.Type == "hysteria" && numberValue(nested(settings, "version")) == 2 && !VersionAtLeast(client.Version, "2398") {
			return false
		}
	case KindSingBox:
		if client.Name != "sing-box" {
			return true
		}
		if stringSetting(settings, "network") == "xhttp" && (node.Type == "vless" || node.Type == "vmess" || node.Type == "trojan") {
			return false
		}
		if node.Type == "vless" && numberValue(nested(settings, "tls")) == 2 && !VersionAtLeast(client.Version, "1.6.0") {
			return false
		}
		if node.Type == "vless" && stringSetting(settings, "flow") == "xtls-rprx-vision" && !VersionAtLeast(client.Version, "1.5.0") {
			return false
		}
		if node.Type == "hysteria" && numberValue(nested(settings, "version")) == 2 && !VersionAtLeast(client.Version, "1.5.0") {
			return false
		}
		if subscriptionECHEnabled(node) {
			minimum := "1.5.0"
			if node.Type == "anytls" {
				minimum = "1.12.0"
			}
			if !VersionAtLeast(client.Version, minimum) {
				return false
			}
		}
	case KindClashMeta:
		if node.Type == "hysteria" && numberValue(nested(settings, "version")) == 2 {
			minimum := map[string]string{
				"nekobox": "1.2.7", "clashmetaforandroid": "2.9.0", "verge": "1.3.8", "flclash": "0.8.0",
			}[client.Name]
			if minimum != "" && !VersionAtLeast(client.Version, minimum) {
				return false
			}
		}
		if subscriptionECHEnabled(node) && containsString([]string{"meta", "verge", "flclash", "nekobox", "clashmetaforandroid"}, client.Name) && !VersionAtLeast(client.Version, "1.19.9") {
			return false
		}
	case KindStash:
		switch node.Type {
		case "shadowsocks":
			if containsString([]string{"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305"}, stringSetting(settings, "cipher")) && !VersionAtLeast(client.Version, "3.0.0") {
				return false
			}
		case "vless":
			if numberValue(nested(settings, "tls")) == 2 && !VersionAtLeast(client.Version, "3.1.0") {
				return false
			}
			if stringSetting(settings, "flow") == "xtls-rprx-vision" && !VersionAtLeast(client.Version, "3.1.0") {
				return false
			}
		case "hysteria":
			minimum := "2.0.0"
			if numberValue(nested(settings, "version")) == 2 {
				minimum = "2.5.0"
			}
			if !VersionAtLeast(client.Version, minimum) {
				return false
			}
		}
	}
	return true
}

func subscriptionECHEnabled(node PreparedNode) bool {
	path := "tls_settings.ech.enabled"
	if node.Type == "hysteria" || node.Type == "tuic" || node.Type == "anytls" {
		path = "tls.ech.enabled"
	}
	value := nested(node.ProtocolSettings, path)
	return numberValue(value) == 1
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

var protocolsByClient = map[Kind]map[string]struct{}{
	KindGeneral:      protocolSet("vmess", "vless", "shadowsocks", "trojan", "hysteria", "anytls", "socks", "tuic", "http"),
	KindShadowsocks:  protocolSet("shadowsocks"),
	KindClash:        protocolSet("shadowsocks", "vmess", "trojan", "socks", "http"),
	KindClashMeta:    protocolSet("shadowsocks", "vmess", "trojan", "vless", "hysteria", "tuic", "anytls", "socks", "http", "mieru"),
	KindStash:        protocolSet("shadowsocks", "vmess", "vless", "hysteria", "trojan", "tuic", "anytls", "socks", "http"),
	KindSingBox:      protocolSet("shadowsocks", "trojan", "vmess", "vless", "hysteria", "tuic", "anytls", "socks", "http"),
	KindSurge:        protocolSet("shadowsocks", "vmess", "trojan", "hysteria", "anytls", "socks", "http"),
	KindSurfboard:    protocolSet("shadowsocks", "vmess", "trojan", "anytls"),
	KindShadowrocket: protocolSet("shadowsocks", "vmess", "vless", "trojan", "hysteria", "tuic", "anytls", "socks"),
	KindQuantumultX:  protocolSet("shadowsocks", "vmess", "vless", "trojan", "anytls", "socks", "http"),
	KindLoon:         protocolSet("shadowsocks", "vmess", "trojan", "hysteria", "vless", "anytls"),
}

func allowedProtocols(kind Kind) map[string]struct{} { return protocolsByClient[kind] }

func protocolSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
