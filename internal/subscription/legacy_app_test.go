package subscription

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

func TestLegacyAppClashMatchesFixedProtocolAndGroupOracle(t *testing.T) {
	nodes := []PreparedNode{
		{Name: "SS Oracle", Type: "shadowsocks", Host: "ss.example.test", Port: 1443, Password: "credential", ProtocolSettings: map[string]any{"cipher": "aes-256-gcm", "plugin": "v2ray-plugin", "plugin_opts": "mode=websocket;tls=true;host=ss.example.test;path=/ss"}},
		{Name: "SS Unsupported", Type: "shadowsocks", Host: "bad.example.test", Port: 1444, Password: "credential", ProtocolSettings: map[string]any{"cipher": "2022-blake3-aes-128-gcm"}},
		{Name: "VMess Oracle", Type: "vmess", Host: "vmess.example.test", Port: 2443, Password: "credential", ProtocolSettings: map[string]any{"tls": float64(1), "network": "ws", "network_settings": map[string]any{"path": "/vm", "headers": map[string]any{"Host": "edge.example.test"}}, "tls_settings": map[string]any{"allow_insecure": true, "server_name": "sni.example.test"}}},
		{Name: "Trojan Oracle", Type: "trojan", Host: "trojan.example.test", Port: 3443, Password: "credential", ProtocolSettings: map[string]any{"network": "grpc", "network_settings": map[string]any{"serviceName": "oracle-grpc"}, "tls_settings": map[string]any{"allow_insecure": false, "server_name": "trojan-sni.example.test"}}},
		{Name: "VLESS Ignored", Type: "vless", Host: "vless.example.test", Port: 4443, Password: "credential", ProtocolSettings: map[string]any{}},
		{Name: "SOCKS Ignored", Type: "socks", Host: "socks.example.test", Port: 5443, Password: "credential", ProtocolSettings: map[string]any{}},
	}
	body, err := RenderLegacyAppClash("", nodes)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := yaml.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	proxies := config["proxies"].([]any)
	if len(proxies) != 3 {
		t.Fatalf("proxy count=%d body=%s", len(proxies), body)
	}
	wantNames := []string{"SS Oracle", "VMess Oracle", "Trojan Oracle"}
	for index, raw := range proxies {
		proxy := raw.(map[string]any)
		if proxy["name"] != wantNames[index] {
			t.Fatalf("proxy %d name=%v want=%q", index, proxy["name"], wantNames[index])
		}
	}
	ss := proxies[0].(map[string]any)
	plugin := ss["plugin-opts"].(map[string]any)
	if plugin["mode"] != "websocket" || plugin["tls"] != true || plugin["host"] != "ss.example.test" || plugin["path"] != "/ss" {
		t.Fatalf("SS plugin options=%#v", plugin)
	}
	vmess := proxies[1].(map[string]any)
	if vmess["network"] != "ws" || vmess["servername"] != "sni.example.test" || vmess["skip-cert-verify"] != true {
		t.Fatalf("VMess proxy=%#v", vmess)
	}
	trojan := proxies[2].(map[string]any)
	if trojan["network"] != "grpc" || trojan["sni"] != "trojan-sni.example.test" || trojan["skip-cert-verify"] != false {
		t.Fatalf("Trojan proxy=%#v", trojan)
	}
	groups := config["proxy-groups"].([]any)
	if len(groups) != 3 {
		t.Fatalf("group count=%d", len(groups))
	}
	for _, raw := range groups {
		group := raw.(map[string]any)
		entries := group["proxies"].([]any)
		for _, name := range wantNames {
			if !containsAnyString(entries, name) {
				t.Fatalf("group %v does not contain %q: %#v", group["name"], name, entries)
			}
		}
	}
	if rules := config["rules"].([]any); len(rules) != 513 {
		t.Fatalf("rules=%d want=513", len(rules))
	}
}

func TestLegacyAppClashKeepsThreeEmptyGroupsAndRejectsUnsafeTemplates(t *testing.T) {
	body, err := RenderLegacyAppClash("", nil)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := yaml.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	if len(config["proxies"].([]any)) != 0 || len(config["proxy-groups"].([]any)) != 3 || len(config["rules"].([]any)) != 513 {
		t.Fatalf("empty default structure=%#v", config)
	}
	for _, input := range []string{"", "proxies: {}\nproxy-groups: []\nrules: []\n", strings.Repeat("x", 1<<20+1)} {
		if err := ValidateLegacyAppClashTemplate(input); err == nil {
			t.Fatalf("ValidateLegacyAppClashTemplate(%d bytes) accepted invalid input", len(input))
		}
	}
	custom := "mixed-port: 7891\nproxies: []\nproxy-groups:\n  - name: custom\n    type: select\n    proxies: []\nrules:\n  - MATCH,DIRECT\n"
	if err := ValidateLegacyAppClashTemplate(custom); err != nil {
		t.Fatalf("valid custom template: %v", err)
	}
}

func TestLoadLegacyAppClashTemplateFileIsBoundedAndStrictUTF8(t *testing.T) {
	directory := t.TempDir()
	validPath := filepath.Join(directory, "custom.yaml")
	valid := "proxies: []\nproxy-groups: []\nrules: []\n"
	if err := os.WriteFile(validPath, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadLegacyAppClashTemplateFile(validPath); err != nil || got != valid {
		t.Fatalf("LoadLegacyAppClashTemplateFile(valid)=(%q,%v)", got, err)
	}
	invalidPath := filepath.Join(directory, "invalid.yaml")
	if err := os.WriteFile(invalidPath, []byte{0xff}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLegacyAppClashTemplateFile(invalidPath); err == nil {
		t.Fatal("invalid UTF-8 template was accepted")
	}
	oversizedPath := filepath.Join(directory, "oversized.yaml")
	if err := os.WriteFile(oversizedPath, []byte(strings.Repeat("x", 1<<20+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLegacyAppClashTemplateFile(oversizedPath); err == nil {
		t.Fatal("oversized template was accepted")
	}
}

func TestLegacyAppClashRendererPreservesCustomTemplateWithoutCrossRequestMutation(t *testing.T) {
	custom := "mixed-port: 7891\ncustom-marker: preserved\nproxies: []\nproxy-groups:\n  - name: custom\n    type: select\n    proxies: []\nrules:\n  - MATCH,DIRECT\n"
	renderer, err := NewLegacyAppClashRenderer(custom)
	if err != nil {
		t.Fatal(err)
	}
	nodes := []PreparedNode{{Name: "Custom SS", Type: "shadowsocks", Host: "ss.example.test", Port: 443, Password: "secret", ProtocolSettings: map[string]any{"cipher": "aes-256-gcm"}}}
	first, err := renderer.Render(nodes)
	if err != nil || !bytes.Contains(first, []byte("custom-marker: preserved")) {
		t.Fatalf("first custom render err=%v body=%s", err, first)
	}
	second, err := renderer.Render(nil)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := yaml.Unmarshal(second, &config); err != nil {
		t.Fatal(err)
	}
	if len(config["proxies"].([]any)) != 0 || len(config["proxy-groups"].([]any)[0].(map[string]any)["proxies"].([]any)) != 0 {
		t.Fatalf("renderer leaked prior request state: %s", second)
	}
}

func BenchmarkLegacyAppClashRenderer(b *testing.B) {
	renderer, err := NewLegacyAppClashRenderer("")
	if err != nil {
		b.Fatal(err)
	}
	nodes := []PreparedNode{
		{Name: "SS", Type: "shadowsocks", Host: "ss.example.test", Port: 443, Password: "secret", ProtocolSettings: map[string]any{"cipher": "aes-256-gcm"}},
		{Name: "VMess", Type: "vmess", Host: "vmess.example.test", Port: 443, Password: "uuid", ProtocolSettings: map[string]any{"network": "ws"}},
		{Name: "Trojan", Type: "trojan", Host: "trojan.example.test", Port: 443, Password: "secret", ProtocolSettings: map[string]any{"network": "grpc"}},
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(DefaultLegacyAppClashTemplate())))
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := renderer.Render(nodes); err != nil {
			b.Fatal(err)
		}
	}
}

func containsAnyString(values []any, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
