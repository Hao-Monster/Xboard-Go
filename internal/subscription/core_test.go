package subscription

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestDetectClientMatchesLegacyFlagPrecedenceAndVersions(t *testing.T) {
	for _, test := range []struct {
		queryFlag string
		userAgent string
		wantKind  Kind
		wantName  string
		wantVer   string
	}{
		{queryFlag: "ClashMetaForAndroid/2.11.0", wantKind: KindClashMeta, wantName: "clashmetaforandroid", wantVer: "2.11.0"},
		{queryFlag: "sing-box 1.11", wantKind: KindSingBox, wantName: "sing-box", wantVer: "1.11"},
		{queryFlag: "quantumult%20x/1.4.0", wantKind: KindQuantumultX, wantName: "quantumult%20x", wantVer: "1.4.0"},
		{queryFlag: "unknown", userAgent: "clash/9.9", wantKind: KindGeneral},
		{userAgent: "Shadowrocket/2592", wantKind: KindShadowrocket, wantName: "shadowrocket", wantVer: "2592"},
		{userAgent: "v2rayN/7.12.3", wantKind: KindGeneral, wantName: "v2rayn", wantVer: "7.12.3"},
	} {
		got := DetectClient(test.queryFlag, test.userAgent)
		if got.Kind != test.wantKind || got.Name != test.wantName || got.Version != test.wantVer {
			t.Errorf("DetectClient(%q,%q) = %#v, want kind=%s name=%q version=%q", test.queryFlag, test.userAgent, got, test.wantKind, test.wantName, test.wantVer)
		}
	}
}

func TestFilterNodesPreservesLegacyTypeAndKeywordSemantics(t *testing.T) {
	nodes := []PreparedNode{
		{Type: "vless", Name: "Tokyo Edge", Tags: []string{"premium"}},
		{Type: "vmess", Name: "Singapore", Tags: []string{"Premium"}},
		{Type: "trojan", Name: "Los Angeles", Tags: []string{}},
	}
	for _, test := range []struct {
		types  string
		filter string
		want   []string
	}{
		{want: []string{"Tokyo Edge", "Singapore", "Los Angeles"}},
		{types: "all", want: []string{"Tokyo Edge", "Singapore", "Los Angeles"}},
		{types: "vless|vmess", want: []string{"Tokyo Edge", "Singapore"}},
		// The old frontend/backend contract treats an invalid-only type list as no type filter.
		{types: "invalid", want: []string{"Tokyo Edge", "Singapore", "Los Angeles"}},
		{filter: "tokyo", want: []string{"Tokyo Edge"}},
		{filter: "premium", want: []string{"Tokyo Edge"}},
		{filter: "Premium", want: []string{"Singapore"}},
		{filter: "123456789012345678901", want: []string{"Tokyo Edge", "Singapore", "Los Angeles"}},
		{filter: "missing", want: []string{}},
	} {
		got := FilterNodes(nodes, test.types, test.filter)
		if len(got) != len(test.want) {
			t.Errorf("FilterNodes(types=%q,filter=%q) names=%v, want %v", test.types, test.filter, nodeNames(got), test.want)
			continue
		}
		for index := range got {
			if got[index].Name != test.want[index] {
				t.Errorf("FilterNodes(types=%q,filter=%q) names=%v, want %v", test.types, test.filter, nodeNames(got), test.want)
				break
			}
		}
	}
}

func TestPrepareNodesGeneratesLegacyPasswordsPortsAndPresentation(t *testing.T) {
	createdAt := time.Unix(1_699_971_200, 0)
	account := store.SubscriptionAccount{
		UUID:            "11111111-2222-4333-8444-555555555555",
		TransferEnable:  10 << 30,
		TrafficUpload:   123,
		TrafficDownload: 456,
		ExpiredAt:       timePointer(time.Date(2030, 3, 17, 0, 0, 0, 0, time.UTC)),
	}
	nodes := []store.SubscriptionNode{
		{ID: 41, Type: "shadowsocks", Name: "SS", Host: "ss.example.test", Port: "443-449", ServerPort: 443, Tags: []string{}, ProtocolSettings: []byte(`{"cipher":"2022-blake3-aes-128-gcm"}`), CreatedAt: createdAt},
		{ID: 42, Type: "vless", Name: "Vision", Host: "vless.example.test", Port: "8443", ServerPort: 8443, Tags: []string{"premium"}, ProtocolSettings: []byte(`{"tls":1}`), CreatedAt: createdAt},
	}
	prepared, err := PrepareNodes(account, nodes, PrepareOptions{
		ShowInfo: true, ShowProtocol: true, RejectedByRequestFilter: 1,
		SelectPort: func(minimum, maximum int) (int, error) {
			if minimum != 443 || maximum != 449 {
				t.Fatalf("port range = %d-%d", minimum, maximum)
			}
			return 447, nil
		},
	})
	if err != nil {
		t.Fatalf("PrepareNodes() error = %v", err)
	}
	if len(prepared) != 5 {
		t.Fatalf("prepared node count = %d, want 5: %#v", len(prepared), prepared)
	}
	if prepared[0].Name != "[ss]剩余流量：10 GB" || prepared[1].Name != "[ss]套餐到期：2030-03-17" || prepared[2].Name != "[ss]过滤掉1条线路" {
		t.Fatalf("presentation nodes = %#v", nodeNames(prepared[:3]))
	}
	wantServerKey := base64.StdEncoding.EncodeToString([]byte("f9aaa6e0869e405c"))
	wantUserKey := base64.StdEncoding.EncodeToString([]byte("11111111-2222-43"))
	if prepared[3].Port != 447 || prepared[3].Ports != "443-449" || prepared[3].Password != wantServerKey+":"+wantUserKey || prepared[3].Name != "[ss]SS" {
		t.Fatalf("prepared Shadowsocks node = %#v", prepared[3])
	}
	if prepared[4].Password != account.UUID || prepared[4].Name != "[vless]Vision" {
		t.Fatalf("prepared VLESS node = %#v", prepared[4])
	}
}

func TestVersionComparisonUsesNumericComponents(t *testing.T) {
	for _, test := range []struct {
		actual, minimum string
		want            bool
	}{
		{"1.11", "1.10.7", true},
		{"1.9.9", "1.10", false},
		{"2592", "2592", true},
		{"", "1", false},
	} {
		if got := VersionAtLeast(test.actual, test.minimum); got != test.want {
			t.Errorf("VersionAtLeast(%q,%q) = %v, want %v", test.actual, test.minimum, got, test.want)
		}
	}
}

func nodeNames(nodes []PreparedNode) []string {
	names := make([]string, len(nodes))
	for index := range nodes {
		names[index] = nodes[index].Name
	}
	return names
}

func timePointer(value time.Time) *time.Time { return &value }
