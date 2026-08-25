package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

func TestOracleRepresentativeOutputMatrix(t *testing.T) {
	account := oracleAccount()
	nodes := oracleRepresentativeNodes(account.UUID)
	tests := []struct {
		name            string
		client          ClientInfo
		contentType     string
		wantUserInfo    bool
		wantDisposition bool
		validate        func(*testing.T, Response)
	}{
		{name: "general", client: ClientInfo{Kind: KindGeneral, Name: "v2rayn", Version: "7.12.3"}, contentType: "text/plain; charset=utf-8", wantUserInfo: true, validate: validateBase64Lines(9)},
		{name: "shadowsocks", client: ClientInfo{Kind: KindShadowsocks, Name: "shadowsocks"}, contentType: "application/json", validate: validateSIP008},
		{name: "clash", client: ClientInfo{Kind: KindClash, Name: "clash"}, contentType: "text/yaml; charset=utf-8", wantUserInfo: true, wantDisposition: true, validate: validateMihomo(KindClash, 4)},
		{name: "clashmeta", client: ClientInfo{Kind: KindClashMeta, Name: "meta"}, contentType: "text/yaml; charset=utf-8", wantUserInfo: true, wantDisposition: true, validate: validateMihomo(KindClashMeta, 6)},
		{name: "stash", client: ClientInfo{Kind: KindStash, Name: "stash"}, contentType: "text/yaml; charset=utf-8", wantUserInfo: true, wantDisposition: true, validate: validateMihomo(KindStash, 5)},
		{name: "singbox", client: ClientInfo{Kind: KindSingBox, Name: "sing-box", Version: "1.12.0"}, contentType: "application/json", wantUserInfo: true, validate: validateSingBox(8)},
		{name: "surge", client: ClientInfo{Kind: KindSurge, Name: "surge", Version: "2398"}, contentType: "application/octet-stream", wantDisposition: true, validate: validateManagedLines(7)},
		{name: "surfboard", client: ClientInfo{Kind: KindSurfboard, Name: "surfboard"}, contentType: "text/html; charset=utf-8", wantDisposition: true, validate: validateManagedLines(4)},
		{name: "shadowrocket", client: ClientInfo{Kind: KindShadowrocket, Name: "shadowrocket", Version: "2592"}, contentType: "text/plain; charset=utf-8", validate: validateShadowrocketOracle},
		{name: "quantumultx", client: ClientInfo{Kind: KindQuantumultX, Name: "quantumult-x"}, contentType: "text/plain; charset=utf-8", wantUserInfo: true, validate: validateQuantumultXOracle},
		{name: "loon", client: ClientInfo{Kind: KindLoon, Name: "loon", Version: "637"}, contentType: "text/plain; charset=utf-8", wantUserInfo: true, validate: validateLoonOracle},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := Render(RenderInput{
				Account: account, Nodes: nodes, Client: test.client, AppName: "Subscription Oracle",
				RequestHost: "127.0.0.1", SubscriptionURL: "http://127.0.0.1:7183/s/token",
				Templates: map[string]string{}, Fingerprint: func() string { return "chrome" },
			})
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if response.ContentType != test.contentType {
				t.Errorf("ContentType = %q, want %q", response.ContentType, test.contentType)
			}
			_, hasUserInfo := response.Headers["subscription-userinfo"]
			if hasUserInfo != test.wantUserInfo {
				t.Errorf("subscription-userinfo presence = %v, want %v", hasUserInfo, test.wantUserInfo)
			}
			_, hasDisposition := response.Headers["content-disposition"]
			if hasDisposition != test.wantDisposition {
				t.Errorf("content-disposition presence = %v, want %v", hasDisposition, test.wantDisposition)
			}
			test.validate(t, response)
		})
	}
}

func oracleRepresentativeNodes(password string) []PreparedNode {
	serverKey := "ZjlhYWE2ZTA4NjllNDA1Yw==:MTExMTExMTEtMjIyMi00Mw=="
	return []PreparedNode{
		{ID: 41, Type: "shadowsocks", Name: "Shadowsocks 2022", Host: "ss.example.test", Port: 443, Password: serverKey, ProtocolSettings: map[string]any{"cipher": "2022-blake3-aes-128-gcm", "plugin": "", "plugin_opts": ""}},
		{ID: 42, Type: "vmess", Name: "VMess TLS", Host: "vmess.example.test", Port: 443, Password: password, ProtocolSettings: map[string]any{"tls": float64(1), "tls_settings": map[string]any{"server_name": "vmess.example"}, "multiplex": []any{}}},
		{ID: 43, Type: "trojan", Name: "Trojan Reality", Host: "trojan.example.test", Port: 443, Password: password, ProtocolSettings: map[string]any{"tls": float64(2), "tls_settings": map[string]any{"server_name": "trojan.example"}, "reality_settings": map[string]any{"server_name": "reality.example"}, "multiplex": []any{}}},
		{ID: 44, Type: "vless", Name: "VLESS Vision", Host: "vless.example.test", Port: 8443, Password: password, ProtocolSettings: map[string]any{"tls": float64(1), "flow": "xtls-rprx-vision", "encryption": map[string]any{"enabled": true, "decryption": "mlkem768x25519plus"}, "tls_settings": map[string]any{"server_name": "vless.example"}, "multiplex": []any{}}},
		{ID: 45, Type: "hysteria", Name: "Hysteria 2", Host: "hysteria.example.test", Port: 443, Password: password, ProtocolSettings: map[string]any{"version": float64(2), "tls": map[string]any{"server_name": "hy.example"}, "bandwidth": map[string]any{"up": float64(100), "down": float64(200)}, "obfs": map[string]any{"open": true, "type": "salamander", "password": "secret"}}},
		{ID: 46, Type: "tuic", Name: "TUIC 5", Host: "tuic.example.test", Port: 443, Password: password, ProtocolSettings: map[string]any{"version": float64(5), "tls": map[string]any{"server_name": "tuic.example"}, "congestion_control": "bbr"}},
		{ID: 47, Type: "anytls", Name: "AnyTLS", Host: "anytls.example.test", Port: 443, Password: password, ProtocolSettings: map[string]any{"tls": map[string]any{"server_name": "any.example"}, "padding_scheme": []any{"stop=8"}}},
		{ID: 48, Type: "socks", Name: "SOCKS", Host: "socks.example.test", Port: 1080, Password: password, ProtocolSettings: map[string]any{"tls": float64(0), "tls_settings": []any{}}},
		{ID: 49, Type: "naive", Name: "Naive", Host: "naive.example.test", Port: 443, Password: password, ProtocolSettings: map[string]any{"tls": float64(1), "tls_settings": map[string]any{"server_name": "naive.example"}}},
		{ID: 50, Type: "http", Name: "HTTP TLS", Host: "http.example.test", Port: 443, Password: password, ProtocolSettings: map[string]any{"tls": float64(1), "tls_settings": map[string]any{"server_name": "http.example"}}},
	}
}

func validateBase64Lines(want int) func(*testing.T, Response) {
	return func(t *testing.T, response Response) {
		decoded := decodeBase64Body(t, response.Body)
		if got := nonEmptyLineCount(decoded); got != want {
			t.Fatalf("decoded line count = %d, want %d\n%s", got, want, decoded)
		}
	}
}

func validateSIP008(t *testing.T, response Response) {
	var payload struct {
		Servers []any `json:"servers"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		t.Fatalf("decode SIP008: %v", err)
	}
	if len(payload.Servers) != 0 {
		t.Fatalf("SIP008 servers = %d, want legacy SS-2022 exclusion", len(payload.Servers))
	}
}

func validateMihomo(kind Kind, want int) func(*testing.T, Response) {
	return func(t *testing.T, response Response) {
		var payload map[string]any
		if err := yaml.Unmarshal(response.Body, &payload); err != nil {
			t.Fatalf("decode YAML: %v", err)
		}
		proxies, ok := payload["proxies"].([]any)
		if !ok || len(proxies) != want {
			t.Fatalf("YAML proxies = %#v, want %d", payload["proxies"], want)
		}
		byName := namedObjects(t, proxies, "name")
		switch kind {
		case KindClash:
			assertObjectFields(t, byName["VMess TLS"], map[string]any{"tls": true, "skip-cert-verify": false, "servername": "vmess.example"}, "client-fingerprint")
			assertObjectFields(t, byName["Trojan Reality"], map[string]any{"sni": "trojan.example", "skip-cert-verify": false, "network": "tcp"}, "tls", "servername", "reality-opts", "client-fingerprint")
		case KindClashMeta:
			assertObjectFields(t, byName["Hysteria 2"], map[string]any{"type": "hysteria2", "up": 100, "down": 200, "obfs": "salamander"}, "up-speed", "down-speed")
			assertObjectFields(t, byName["AnyTLS"], map[string]any{"type": "anytls", "sni": "any.example", "udp": true}, "skip-cert-verify")
		case KindStash:
			assertObjectFields(t, byName["VMess TLS"], map[string]any{"tls": true, "servername": "vmess.example"}, "client-fingerprint")
			assertObjectFields(t, byName["TUIC 5"], map[string]any{"reduce-rtt": true, "fast-open": true, "heartbeat-interval": 10000, "request-timeout": 8000, "max-udp-relay-packet-size": 1500, "version": 5})
			assertObjectFields(t, byName["HTTP TLS"], map[string]any{"tls": true, "sni": "http.example", "skip-cert-verify": false})
		}
	}
}

func validateSingBox(want int) func(*testing.T, Response) {
	return func(t *testing.T, response Response) {
		var payload map[string]any
		if err := json.Unmarshal(response.Body, &payload); err != nil {
			t.Fatalf("decode sing-box JSON: %v", err)
		}
		outbounds, ok := payload["outbounds"].([]any)
		if !ok || len(outbounds) < want {
			t.Fatalf("sing-box outbounds = %#v, want at least %d", payload["outbounds"], want)
		}
		count := 0
		for _, value := range outbounds {
			outbound, _ := value.(map[string]any)
			if outbound["server"] != nil {
				count++
			}
		}
		if count != want {
			t.Fatalf("sing-box server outbounds = %d, want %d", count, want)
		}
		byTag := namedObjects(t, outbounds, "tag")
		trojanTLS, _ := byTag["Trojan Reality"]["tls"].(map[string]any)
		reality, _ := trojanTLS["reality"].(map[string]any)
		if reality["public_key"] != nil || reality["short_id"] != nil {
			t.Fatalf("sing-box Reality null fields = %#v", reality)
		}
		anyTLS, _ := byTag["AnyTLS"]["tls"].(map[string]any)
		if !reflect.DeepEqual(anyTLS["alpn"], []any{"h3"}) {
			t.Fatalf("sing-box AnyTLS alpn = %#v, want [h3]", anyTLS["alpn"])
		}
	}
}

func namedObjects(t *testing.T, values []any, key string) map[string]map[string]any {
	t.Helper()
	result := make(map[string]map[string]any, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			continue
		}
		name, _ := object[key].(string)
		if name != "" {
			result[name] = object
		}
	}
	return result
}

func assertObjectFields(t *testing.T, object map[string]any, fields map[string]any, forbidden ...string) {
	t.Helper()
	if object == nil {
		t.Fatal("expected object is missing")
	}
	for key, wanted := range fields {
		if !reflect.DeepEqual(object[key], wanted) {
			t.Errorf("field %s = %#v, want %#v in %#v", key, object[key], wanted, object)
		}
	}
	for _, key := range forbidden {
		if _, exists := object[key]; exists {
			t.Errorf("forbidden field %s exists in %#v", key, object)
		}
	}
}

func validateManagedLines(want int) func(*testing.T, Response) {
	return func(t *testing.T, response Response) {
		body := string(response.Body)
		section := between(body, "[Proxy]", "[Proxy Group]")
		if got := nonCommentAssignmentCount(section); got != want {
			t.Fatalf("managed proxy count = %d, want %d\n%s", got, want, section)
		}
	}
}

func validateShadowrocketOracle(t *testing.T, response Response) {
	body := decodeBase64Body(t, response.Body)
	if !strings.HasPrefix(body, "STATUS=🚀↑:0GB,↓:0GB,TOT:10GB💡Expires:2030-03-18\r\n") {
		t.Fatalf("Shadowrocket status mismatch: %q", strings.SplitN(body, "\r\n", 2)[0])
	}
	if got := nonEmptyLineCount(body) - 1; got != 7 {
		t.Fatalf("Shadowrocket node lines = %d, want 7\n%s", got, body)
	}
	for _, fragment := range []string{"ss://", "vmess://", "vless://", "hysteria2://", "tuic://", "anytls://", "socks://"} {
		if !strings.Contains(body, fragment) {
			t.Errorf("Shadowrocket output missing %q", fragment)
		}
	}
	if strings.Contains(body, "trojan://") {
		t.Error("Shadowrocket 2592 must exclude Trojan with missing strict network")
	}
}

func validateQuantumultXOracle(t *testing.T, response Response) {
	body := decodeBase64Body(t, response.Body)
	wantPrefixes := []string{"shadowsocks=", "vmess=", "trojan=", "vless=", "anytls=", "socks5=", "http="}
	lines := nonEmptyLines(body)
	if len(lines) != len(wantPrefixes) {
		t.Fatalf("Quantumult X line count = %d, want %d\n%s", len(lines), len(wantPrefixes), body)
	}
	for index, prefix := range wantPrefixes {
		if !strings.HasPrefix(lines[index], prefix) {
			t.Errorf("Quantumult X line %d = %q, want prefix %q", index, lines[index], prefix)
		}
	}
	for _, fragment := range []string{
		"trojan=trojan.example.test:443,password=11111111-2222-4333-8444-555555555555,tls-host=reality.example",
		"vless=vless.example.test:8443,method=none,password=11111111-2222-4333-8444-555555555555,tls-verification=true,obfs-host=vless.example,vless-flow=xtls-rprx-vision",
		"anytls=anytls.example.test:443,password=11111111-2222-4333-8444-555555555555,udp-relay=true,tag=AnyTLS,over-tls=true,tls-verification=true,tls-host=any.example",
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("Quantumult X output missing legacy fragment %q\n%s", fragment, body)
		}
	}
}

func validateLoonOracle(t *testing.T, response Response) {
	want := strings.Join([]string{
		"Shadowsocks 2022=Shadowsocks,ss.example.test,443,2022-blake3-aes-128-gcm,ZjlhYWE2ZTA4NjllNDA1Yw==:MTExMTExMTEtMjIyMi00Mw==,fast-open=false,udp=true",
		"VMess TLS=vmess,vmess.example.test,443,auto,11111111-2222-4333-8444-555555555555,fast-open=false,udp=true,alterId=0,over-tls=true,skip-cert-verify=false,tls-name=vmess.example",
		"VLESS Vision=VLESS,vless.example.test,8443,11111111-2222-4333-8444-555555555555,alterId=0,udp=true,flow=xtls-rprx-vision,over-tls=true,skip-cert-verify=false,sni=vless.example,transport=tcp",
		"Hysteria 2=Hysteria2,hysteria.example.test,443,11111111-2222-4333-8444-555555555555,sni=hy.example,download-bandwidth=200,udp=true",
		"AnyTLS=anytls,anytls.example.test,443,11111111-2222-4333-8444-555555555555,udp=true,sni=any.example",
	}, "\r\n") + "\r\n"
	if string(response.Body) != want {
		t.Fatalf("Loon output differs from Oracle\n--- got ---\n%s--- want ---\n%s", response.Body, want)
	}
}

func decodeBase64Body(t *testing.T, body []byte) string {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(string(body))
	if err != nil {
		t.Fatalf("decode base64 body: %v", err)
	}
	return string(decoded)
}

func nonEmptyLineCount(value string) int { return len(nonEmptyLines(value)) }

func nonEmptyLines(value string) []string {
	lines := strings.FieldsFunc(value, func(character rune) bool { return character == '\r' || character == '\n' })
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		}
	}
	return result
}

func between(value, start, end string) string {
	startIndex := strings.Index(value, start)
	if startIndex < 0 {
		return ""
	}
	value = value[startIndex+len(start):]
	endIndex := strings.Index(value, end)
	if endIndex < 0 {
		return value
	}
	return value[:endIndex]
}

func nonCommentAssignmentCount(value string) int {
	count := 0
	for _, line := range nonEmptyLines(value) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#") && strings.Contains(line, "=") {
			count++
		}
	}
	return count
}

func TestOracleClientCompatibilityBoundaries(t *testing.T) {
	nodes := oracleRepresentativeNodes(oracleAccount().UUID)
	tests := []struct {
		client ClientInfo
		want   int
	}{
		{client: ClientInfo{Kind: KindShadowrocket, Name: "shadowrocket", Version: "1992"}, want: 6},
		{client: ClientInfo{Kind: KindShadowrocket, Name: "shadowrocket", Version: "1993"}, want: 7},
		{client: ClientInfo{Kind: KindLoon, Name: "loon", Version: "636"}, want: 4},
		{client: ClientInfo{Kind: KindLoon, Name: "loon", Version: "637"}, want: 5},
		{client: ClientInfo{Kind: KindSurge, Name: "surge", Version: "2397"}, want: 6},
		{client: ClientInfo{Kind: KindSurge, Name: "surge", Version: "2398"}, want: 7},
	}
	for _, test := range tests {
		t.Run(fmt.Sprintf("%s-%s", test.client.Kind, test.client.Version), func(t *testing.T) {
			if got := len(compatibleNodes(nodes, test.client)); got != test.want {
				t.Fatalf("compatibleNodes() = %d, want %d", got, test.want)
			}
		})
	}
}
