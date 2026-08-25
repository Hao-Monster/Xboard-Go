package subscription

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestRenderGeneralProducesLegacySchemesAndHeaders(t *testing.T) {
	account := oracleAccount()
	nodes := []PreparedNode{
		{ID: 1, Type: "shadowsocks", Name: "SS 2022", Host: "ss.example.test", Port: 443, Password: "server:user", ProtocolSettings: map[string]any{"cipher": "2022-blake3-aes-128-gcm"}},
		{ID: 2, Type: "vmess", Name: "VMess TLS", Host: "vmess.example.test", Port: 443, Password: account.UUID, ProtocolSettings: map[string]any{"tls": float64(1), "tls_settings": map[string]any{"server_name": "vmess.example"}}},
		{ID: 3, Type: "trojan", Name: "Trojan", Host: "trojan.example.test", Port: 443, Password: account.UUID, ProtocolSettings: map[string]any{"tls": float64(1), "tls_settings": map[string]any{"server_name": "trojan.example"}}},
		{ID: 4, Type: "vless", Name: "VLESS Vision", Host: "vless.example.test", Port: 8443, Password: account.UUID, ProtocolSettings: map[string]any{"tls": float64(1), "flow": "xtls-rprx-vision", "tls_settings": map[string]any{"server_name": "vless.example"}}},
		{ID: 5, Type: "hysteria", Name: "Hysteria 2", Host: "hy.example.test", Port: 443, Password: account.UUID, ProtocolSettings: map[string]any{"version": float64(2), "tls": map[string]any{"server_name": "hy.example"}}},
		{ID: 6, Type: "tuic", Name: "TUIC 5", Host: "tuic.example.test", Port: 443, Password: account.UUID, ProtocolSettings: map[string]any{"version": float64(5), "tls": map[string]any{"server_name": "tuic.example"}}},
		{ID: 7, Type: "anytls", Name: "AnyTLS", Host: "any.example.test", Port: 443, Password: account.UUID, ProtocolSettings: map[string]any{"tls": map[string]any{"server_name": "any.example"}}},
		{ID: 8, Type: "socks", Name: "SOCKS", Host: "socks.example.test", Port: 1080, Password: account.UUID, ProtocolSettings: map[string]any{}},
		{ID: 9, Type: "http", Name: "HTTP TLS", Host: "http.example.test", Port: 443, Password: account.UUID, ProtocolSettings: map[string]any{"tls": float64(1), "tls_settings": map[string]any{"server_name": "http.example"}}},
		{ID: 10, Type: "naive", Name: "Naive", Host: "naive.example.test", Port: 443, Password: account.UUID, ProtocolSettings: map[string]any{}},
	}
	response, err := Render(RenderInput{Account: account, Nodes: nodes, Client: ClientInfo{Kind: KindGeneral, Name: "v2rayn", Version: "7.12.3"}, Fingerprint: func() string { return "chrome" }})
	if err != nil {
		t.Fatalf("Render(general) error = %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(response.Body))
	if err != nil {
		t.Fatalf("decode general body: %v", err)
	}
	body := string(decoded)
	for _, fragment := range []string{"ss://", "vmess://", "trojan://", "vless://", "hysteria2://", "tuic://", "anytls://", "socks://", "http://"} {
		if !strings.Contains(body, fragment) {
			t.Errorf("general body is missing %q: %s", fragment, body)
		}
	}
	if strings.Contains(body, "Naive") || strings.Count(body, "\r\n") != 9 {
		t.Fatalf("general protocol whitelist body = %q", body)
	}
	if response.ContentType != "text/plain; charset=utf-8" || response.Headers["subscription-userinfo"] != "upload=123456; download=654321; total=10737418240; expire=1900000000" {
		t.Fatalf("general response metadata = %#v", response)
	}
	vmessLine := findLine(body, "vmess://")
	vmessJSON, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(vmessLine, "vmess://"))
	if err != nil {
		t.Fatal(err)
	}
	var vmess map[string]any
	if err := json.Unmarshal(vmessJSON, &vmess); err != nil || vmess["id"] != account.UUID || vmess["sni"] != "vmess.example" || vmess["fp"] != "chrome" {
		t.Fatalf("VMess payload = %s, err=%v", vmessJSON, err)
	}
}

func TestRenderShadowsocksUsesSIP008AndExcludes2022Ciphers(t *testing.T) {
	account := oracleAccount()
	nodes := []PreparedNode{
		{ID: 1, Type: "shadowsocks", Name: "Legacy", Host: "legacy.example.test", Port: 8388, Password: account.UUID, ProtocolSettings: map[string]any{"cipher": "aes-256-gcm"}},
		{ID: 2, Type: "shadowsocks", Name: "2022", Host: "modern.example.test", Port: 443, Password: "server:user", ProtocolSettings: map[string]any{"cipher": "2022-blake3-aes-128-gcm"}},
		{ID: 3, Type: "vless", Name: "ignored", Host: "ignored.example.test", Port: 443, Password: account.UUID, ProtocolSettings: map[string]any{}},
	}
	response, err := Render(RenderInput{Account: account, Nodes: nodes, Client: ClientInfo{Kind: KindShadowsocks}})
	if err != nil {
		t.Fatalf("Render(shadowsocks) error = %v", err)
	}
	var payload struct {
		Servers []struct {
			ID         int64  `json:"id"`
			Remarks    string `json:"remarks"`
			Method     string `json:"method"`
			ServerPort int    `json:"server_port"`
		} `json:"servers"`
		BytesUsed      int64 `json:"bytes_used"`
		BytesRemaining int64 `json:"bytes_remaining"`
		Version        int   `json:"version"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if response.ContentType != "application/json" || len(payload.Servers) != 1 || payload.Servers[0].ID != 1 || payload.Servers[0].Remarks != "Legacy" || payload.Servers[0].Method != "aes-256-gcm" || payload.Servers[0].ServerPort != 8388 || payload.BytesUsed != 777777 || payload.BytesRemaining != 10736640463 || payload.Version != 1 {
		t.Fatalf("SIP008 response = %#v, metadata=%#v", payload, response)
	}
}

func TestGeneralVersionFilterMatchesLegacyHysteriaThresholds(t *testing.T) {
	node := PreparedNode{Type: "hysteria", Name: "Hy2", ProtocolSettings: map[string]any{"version": float64(2)}}
	for _, test := range []struct {
		client ClientInfo
		want   int
	}{
		{client: ClientInfo{Kind: KindGeneral, Name: "v2rayn", Version: "6.30"}, want: 0},
		{client: ClientInfo{Kind: KindGeneral, Name: "v2rayn", Version: "6.31"}, want: 1},
		{client: ClientInfo{Kind: KindGeneral, Name: "v2rayng", Version: "1.9.4"}, want: 0},
		{client: ClientInfo{Kind: KindGeneral, Name: "v2rayng", Version: "1.9.5"}, want: 1},
		{client: ClientInfo{Kind: KindGeneral, Name: "v2rayn"}, want: 1},
	} {
		if got := compatibleNodes([]PreparedNode{node}, test.client); len(got) != test.want {
			t.Errorf("compatibleNodes(%#v) count = %d, want %d", test.client, len(got), test.want)
		}
	}
}

func oracleAccount() store.SubscriptionAccount {
	expiry := time.Unix(1_900_000_000, 0)
	return store.SubscriptionAccount{
		UUID: "11111111-2222-4333-8444-555555555555", TransferEnable: 10 << 30,
		TrafficUpload: 123456, TrafficDownload: 654321, ExpiredAt: &expiry,
	}
}

func findLine(body, prefix string) string {
	for _, line := range strings.Split(body, "\r\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}
