package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const clientCompatOutputEnv = "XBOARD_CLIENT_COMPAT_OUTPUT_DIR"

func TestGenerateRealClientCompatibilityFixtures(t *testing.T) {
	outputDirectory := os.Getenv(clientCompatOutputEnv)
	if outputDirectory == "" {
		t.Skip(clientCompatOutputEnv + " is not set")
	}
	if err := os.MkdirAll(outputDirectory, 0o700); err != nil {
		t.Fatalf("create client compatibility output directory: %v", err)
	}

	account := oracleAccount()
	nodes := oracleRepresentativeNodes(account.UUID)
	fixtures := []struct {
		name   string
		client ClientInfo
	}{
		{name: "sing-box-1.14.0.json", client: ClientInfo{Kind: KindSingBox, Name: "sing-box", Version: "1.14.0"}},
		{name: "sing-box-1.13.21.json", client: ClientInfo{Kind: KindSingBox, Name: "sing-box", Version: "1.13.21"}},
		{name: "clash-1.19.30.yaml", client: ClientInfo{Kind: KindClash, Name: "clash", Version: "1.19.30"}},
		{name: "clash-1.19.29.yaml", client: ClientInfo{Kind: KindClash, Name: "clash", Version: "1.19.29"}},
		{name: "clashmeta-1.19.30.yaml", client: ClientInfo{Kind: KindClashMeta, Name: "meta", Version: "1.19.30"}},
		{name: "clashmeta-1.19.29.yaml", client: ClientInfo{Kind: KindClashMeta, Name: "meta", Version: "1.19.29"}},
	}
	for _, fixture := range fixtures {
		response, err := Render(RenderInput{
			Account:     account,
			Nodes:       nodes,
			Client:      fixture.client,
			AppName:     "Xboard Client Compatibility",
			RequestHost: "panel.example.test",
			Fingerprint: func() string { return "chrome" },
		})
		if err != nil {
			t.Fatalf("render %s: %v", fixture.name, err)
		}
		path := filepath.Join(outputDirectory, fixture.name)
		if err := os.WriteFile(path, response.Body, 0o600); err != nil {
			t.Fatalf("write %s: %v", fixture.name, err)
		}
	}
}

func TestGeneralOutputPassesIndependentURIParser(t *testing.T) {
	account := oracleAccount()
	nodes := []PreparedNode{
		{ID: 1, Type: "shadowsocks", Name: "SS IPv6", Host: "2001:db8::10", Port: 443, Password: "example-password", ProtocolSettings: map[string]any{"cipher": "aes-256-gcm"}},
		{ID: 2, Type: "vmess", Name: "VMess WS TLS", Host: "vmess.example.test", Port: 443, Password: account.UUID, ProtocolSettings: map[string]any{"network": "ws", "network_settings": map[string]any{"path": "/socket", "headers": map[string]any{"Host": "edge.example.test"}}, "tls": float64(1), "tls_settings": map[string]any{"server_name": "vmess.example.test"}}},
		{ID: 3, Type: "vless", Name: "VLESS Reality", Host: "2001:db8::20", Port: 8443, Password: account.UUID, ProtocolSettings: map[string]any{"network": "grpc", "network_settings": map[string]any{"serviceName": "compat"}, "tls": float64(2), "flow": "xtls-rprx-vision", "reality_settings": map[string]any{"public_key": "example-public-key", "short_id": "0123456789abcdef", "server_name": "reality.example.test"}}},
		{ID: 4, Type: "trojan", Name: "Trojan TLS", Host: "trojan.example.test", Port: 443, Password: account.UUID, ProtocolSettings: map[string]any{"network": "tcp", "tls": float64(1), "tls_settings": map[string]any{"server_name": "trojan.example.test"}}},
		{ID: 5, Type: "hysteria", Name: "Hysteria 2", Host: "hy.example.test", Port: 443, Password: account.UUID, ProtocolSettings: map[string]any{"version": float64(2), "tls": map[string]any{"server_name": "hy.example.test"}}},
		{ID: 6, Type: "tuic", Name: "TUIC 5", Host: "tuic.example.test", Port: 443, Password: account.UUID, ProtocolSettings: map[string]any{"version": float64(5), "tls": map[string]any{"server_name": "tuic.example.test"}}},
		{ID: 7, Type: "anytls", Name: "AnyTLS", Host: "any.example.test", Port: 443, Password: account.UUID, ProtocolSettings: map[string]any{"tls": map[string]any{"server_name": "any.example.test"}}},
		{ID: 8, Type: "socks", Name: "SOCKS", Host: "socks.example.test", Port: 1080, Password: account.UUID, ProtocolSettings: map[string]any{}},
		{ID: 9, Type: "http", Name: "HTTP TLS", Host: "http.example.test", Port: 443, Password: account.UUID, ProtocolSettings: map[string]any{"tls": float64(1), "tls_settings": map[string]any{"server_name": "http.example.test"}}},
	}
	response, err := Render(RenderInput{
		Account:     account,
		Nodes:       nodes,
		Client:      ClientInfo{Kind: KindGeneral, Name: "v2rayn", Version: "7.12.3"},
		Fingerprint: func() string { return "chrome" },
	})
	if err != nil {
		t.Fatalf("render general compatibility fixture: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(response.Body))
	if err != nil {
		t.Fatalf("decode general subscription: %v", err)
	}

	seen := make(map[string]int)
	for _, line := range nonEmptyLines(string(decoded)) {
		scheme, err := independentlyParseShareURI(line)
		if err != nil {
			t.Errorf("independent parser rejected %q: %v", line, err)
			continue
		}
		seen[scheme]++
	}
	for _, scheme := range []string{"ss", "vmess", "vless", "trojan", "hysteria2", "tuic", "anytls", "socks", "http"} {
		if seen[scheme] != 1 {
			t.Errorf("independent parser saw %s %d times, want 1", scheme, seen[scheme])
		}
	}

	invalid := []string{
		"vless://missing-port@example.test#invalid",
		"vless://uuid@[2001:db8::1:443?security=reality",
		"vmess://not-base64!",
		"ss://bm8tY29sb24@example.test:443#invalid",
		"unknown://value@example.test:443",
	}
	for _, input := range invalid {
		if _, err := independentlyParseShareURI(input); err == nil {
			t.Errorf("independent parser accepted invalid input %q", input)
		}
	}
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func independentlyParseShareURI(input string) (string, error) {
	if strings.HasPrefix(input, "vmess://") {
		payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(input, "vmess://"))
		if err != nil {
			return "", fmt.Errorf("decode vmess payload: %w", err)
		}
		var node struct {
			Address string `json:"add"`
			Port    string `json:"port"`
			ID      string `json:"id"`
			Network string `json:"net"`
			TLS     string `json:"tls"`
			SNI     string `json:"sni"`
		}
		if err := json.Unmarshal(payload, &node); err != nil {
			return "", fmt.Errorf("decode vmess JSON: %w", err)
		}
		if node.Address == "" || !validPort(node.Port) || !uuidPattern.MatchString(node.ID) || node.Network != "ws" || node.TLS != "tls" || node.SNI == "" {
			return "", fmt.Errorf("invalid vmess fields")
		}
		return "vmess", nil
	}

	parsed, err := url.Parse(input)
	if err != nil {
		return "", fmt.Errorf("parse URI: %w", err)
	}
	allowed := map[string]struct{}{"ss": {}, "vless": {}, "trojan": {}, "hysteria2": {}, "tuic": {}, "anytls": {}, "socks": {}, "http": {}}
	if _, ok := allowed[parsed.Scheme]; !ok {
		return "", fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	if parsed.Hostname() == "" || !validPort(parsed.Port()) || parsed.User == nil {
		return "", fmt.Errorf("missing host, port, or credentials")
	}
	username := parsed.User.Username()
	if username == "" {
		return "", fmt.Errorf("empty username")
	}
	switch parsed.Scheme {
	case "ss":
		credentials, err := base64.RawURLEncoding.DecodeString(username)
		if err != nil || !strings.Contains(string(credentials), ":") {
			return "", fmt.Errorf("invalid shadowsocks credentials")
		}
	case "vless":
		query := parsed.Query()
		if !uuidPattern.MatchString(username) || query.Get("security") != "reality" || query.Get("pbk") == "" || query.Get("sid") == "" || query.Get("sni") == "" || query.Get("type") != "grpc" {
			return "", fmt.Errorf("invalid vless Reality fields")
		}
	case "trojan":
		if !uuidPattern.MatchString(username) || parsed.Query().Get("sni") == "" {
			return "", fmt.Errorf("invalid trojan TLS fields")
		}
	case "hysteria2", "anytls":
		if !uuidPattern.MatchString(username) || parsed.Query().Get("sni") == "" {
			return "", fmt.Errorf("invalid %s TLS fields", parsed.Scheme)
		}
	case "tuic":
		password, present := parsed.User.Password()
		if !present || username != password || !uuidPattern.MatchString(username) || parsed.Query().Get("sni") == "" {
			return "", fmt.Errorf("invalid tuic credentials or TLS fields")
		}
	case "socks", "http":
		credentials, err := base64.StdEncoding.DecodeString(username)
		parts := strings.Split(string(credentials), ":")
		if err != nil || len(parts) != 2 || parts[0] != parts[1] || !uuidPattern.MatchString(parts[0]) {
			return "", fmt.Errorf("invalid %s credentials", parsed.Scheme)
		}
	}
	if strings.Contains(parsed.Host, ":") && net.ParseIP(parsed.Hostname()) != nil && !strings.HasPrefix(parsed.Host, "[") {
		return "", fmt.Errorf("IPv6 host is not bracketed")
	}
	return parsed.Scheme, nil
}

func validPort(value string) bool {
	port, err := strconv.Atoi(value)
	return err == nil && port >= 1 && port <= 65535
}
