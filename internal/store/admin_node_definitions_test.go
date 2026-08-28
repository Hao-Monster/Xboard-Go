package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestBasicAdminNodeDefinitionDefaultsValidateForEveryProtocol(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 16, 0, 0, 0, time.UTC)
	protocols := []string{"shadowsocks", "vmess", "trojan", "hysteria", "vless", "tuic", "socks", "naive", "http", "mieru", "anytls"}
	for index, protocol := range protocols {
		t.Run(protocol, func(t *testing.T) {
			startPort := 20_000 + index
			input, err := NewBasicAdminNodeDefinitionInput(CreateNodeInput{
				Name: "Basic " + protocol, Type: protocol, Host: protocol + ".example.test",
				Port: fmt.Sprintf("%d-%d", startPort, startPort+2), Show: true, Enabled: true, Sort: index,
			})
			if err != nil {
				t.Fatalf("NewBasicAdminNodeDefinitionInput() error = %v", err)
			}
			if input.ServerPort != startPort || input.ListenAddress != "0.0.0.0" || input.RateMicros != trafficRateScale {
				t.Fatalf("unexpected defaults=%#v", input)
			}
			created, _, err := database.CreateAdminNodeDefinition(ctx, input, now.Add(time.Duration(index)*time.Second))
			if err != nil {
				t.Fatalf("CreateAdminNodeDefinition() error = %v", err)
			}
			if created.Type != protocol || created.ServerPort != startPort || len(created.ProtocolSettings) == 0 {
				t.Fatalf("unexpected created definition=%#v", created)
			}
		})
	}
}

func TestSchemaV41PreservesV40NodeDefinitionsAndAddsListenAddress(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC)
	node, err := database.CreateNode(ctx, CreateNodeInput{Name: "Preserved", Type: "vless", Host: "edge.test", Port: "443", Show: true, Enabled: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO node_protocol_definitions (node_id, server_port, protocol_settings_json, configured_rate_micros)
		VALUES (?, 8443, '{"tls":0}', 1250000)
	`, node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(ctx, node.ID, SaveNodeRuntimeInput{RateMicros: 1_250_000, Config: json.RawMessage(`{"protocol":"vless","server_port":8443,"marker":"v40"}`)}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `ALTER TABLE node_protocol_definitions DROP COLUMN listen_address; PRAGMA user_version = 40`); err != nil {
		t.Fatalf("prepare v40 database: %v", err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v40 to v41) error = %v", err)
	}
	var listenAddress, runtime string
	if err := database.db.QueryRowContext(ctx, `
		SELECT d.listen_address, n.runtime_config
		FROM nodes n JOIN node_protocol_definitions d ON d.node_id = n.id WHERE n.id = ?
	`, node.ID).Scan(&listenAddress, &runtime); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 41 || listenAddress != "0.0.0.0" || runtime != `{"protocol":"vless","server_port":8443,"marker":"v40"}` {
		t.Fatalf("migration result version=%d listen=%q runtime=%s", version, listenAddress, runtime)
	}
}

func TestAdminNodeDefinitionCreateRoundTripsAndBuildsAllProtocolRuntimes(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 16, 0, 0, 0, time.UTC)
	group, err := database.CreateServerGroup(ctx, "Protocol users", now)
	if err != nil {
		t.Fatal(err)
	}
	route, err := database.CreateRoutingRule(ctx, SaveRoutingRuleInput{Remarks: "Protocol route", Match: []string{"domain:example.test"}, Action: "direct"}, now)
	if err != nil {
		t.Fatal(err)
	}
	machine, _, err := database.CreateMachine(ctx, CreateMachineInput{Name: "protocol-edge", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]json.RawMessage{
		"shadowsocks": json.RawMessage(`{"cipher":"2022-blake3-aes-128-gcm","plugin":"","plugin_opts":""}`),
		"vmess":       json.RawMessage(`{"tls":1,"network":"ws","network_settings":{"path":"/vmess"},"tls_settings":{"server_name":"vmess.example.test","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}},"utls":{"enabled":true,"fingerprint":"chrome"},"multiplex":{"enabled":true,"protocol":"smux","max_connections":4,"padding":false,"brutal":{"enabled":false,"up_mbps":100,"down_mbps":100}}}`),
		"trojan":      json.RawMessage(`{"tls":2,"network":"tcp","network_settings":{},"tls_settings":{"server_name":"trojan.example.test","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}},"reality_settings":{"server_name":"reality.example.test","server_port":443,"public_key":"public","private_key":"private","short_id":"01234567","allow_insecure":false},"utls":{"enabled":true,"fingerprint":"chrome"},"multiplex":{"enabled":false,"protocol":"smux","max_connections":4,"padding":false,"brutal":{"enabled":false,"up_mbps":100,"down_mbps":100}}}`),
		"hysteria":    json.RawMessage(`{"version":2,"alpn":"h2","obfs":{"open":true,"type":"salamander","password":"obfs-secret"},"tls":{"server_name":"hy.example.test","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}},"bandwidth":{"up":100,"down":200},"hop_interval":30}`),
		"vless":       json.RawMessage(`{"tls":2,"network":"grpc","network_settings":{"serviceName":"vless"},"flow":"xtls-rprx-vision","encryption":{"enabled":true,"encryption":"client-public","decryption":"server-private"},"tls_settings":{"server_name":"vless.example.test","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}},"reality_settings":{"server_name":"reality.example.test","server_port":443,"public_key":"public","private_key":"private","short_id":"01234567","allow_insecure":false},"utls":{"enabled":true,"fingerprint":"chrome"},"multiplex":{"enabled":false,"protocol":"smux","max_connections":4,"padding":false,"brutal":{"enabled":false,"up_mbps":100,"down_mbps":100}}}`),
		"tuic":        json.RawMessage(`{"version":5,"congestion_control":"bbr","alpn":["h3"],"udp_relay_mode":"native","tls":{"server_name":"tuic.example.test","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}}}`),
		"socks":       json.RawMessage(`{"tls":0,"tls_settings":{"server_name":"","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}}}`),
		"naive":       json.RawMessage(`{"tls":1,"tls_settings":{"server_name":"naive.example.test","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}}}`),
		"http":        json.RawMessage(`{"tls":1,"tls_settings":{"server_name":"http.example.test","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}}}`),
		"mieru":       json.RawMessage(`{"transport":"TCP","traffic_pattern":"","multiplex":{"enabled":true,"protocol":"smux","max_connections":4,"padding":false,"brutal":{"enabled":false,"up_mbps":100,"down_mbps":100}}}`),
		"anytls":      json.RawMessage(`{"alpn":"h3","padding_scheme":["stop=8","0=30-30"],"tls":{"server_name":"any.example.test","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}}}`),
	}
	requiredRuntimeKeys := map[string][]string{
		"shadowsocks": {"cipher", "plugin", "plugin_opts", "server_key"},
		"vmess":       {"network", "tls", "tls_settings", "multiplex", "utls"},
		"trojan":      {"network", "host", "server_name", "tls", "tls_settings", "multiplex", "utls"},
		"hysteria":    {"version", "host", "server_name", "tls_settings", "up_mbps", "down_mbps", "obfs", "obfs-password", "hop_interval"},
		"vless":       {"network", "tls", "flow", "decryption", "tls_settings", "multiplex", "utls"},
		"tuic":        {"version", "server_name", "congestion_control", "udp_relay_mode", "tls_settings", "auth_timeout", "zero_rtt_handshake", "heartbeat"},
		"socks":       {"tls", "tls_settings"},
		"naive":       {"tls", "tls_settings"},
		"http":        {"tls", "tls_settings"},
		"mieru":       {"transport", "traffic_pattern", "multiplex"},
		"anytls":      {"server_name", "tls_settings", "padding_scheme"},
	}

	for protocol, settings := range cases {
		t.Run(protocol, func(t *testing.T) {
			input := SaveAdminNodeDefinitionInput{
				Type: protocol, ExternalCode: "code-" + protocol, Name: "Node " + protocol,
				RateMicros: 1_500_000, Tags: []string{"edge", protocol}, Host: protocol + ".example.test",
				Port: "18443-18444", ServerPort: 19443, ListenAddress: "::", ProtocolSettings: settings,
				Show: true, Enabled: true, Sort: 20, MachineID: &machine.ID,
				GroupIDs: []int64{group.ID}, RouteIDs: []int64{route.ID}, RateTimeEnabled: true,
				RateTimeRanges:    json.RawMessage(`[{"start":"00:00","end":"06:30","rate":0.5}]`),
				CustomOutbounds:   json.RawMessage(`[{"tag":"proxy-out","protocol":"socks","settings":{"server":"127.0.0.1","server_port":1080}}]`),
				CustomRoutes:      json.RawMessage(`[{"action":{"type":"route","target":"direct"}}]`),
				CertificateConfig: json.RawMessage(`{"cert_mode":"file","domain":"` + protocol + `.example.test","cert_file":"/etc/xboard/cert.pem","key_file":"/etc/xboard/key.pem"}`),
				TransferEnable:    10 << 30,
			}
			created, mutation, err := database.CreateAdminNodeDefinition(ctx, input, now)
			if err != nil {
				t.Fatalf("CreateAdminNodeDefinition() error = %v", err)
			}
			if created.Revision != 1 || created.Type != protocol || created.ListenAddress != "::" || created.ExternalCode != input.ExternalCode ||
				!reflect.DeepEqual(created.GroupIDs, input.GroupIDs) || !reflect.DeepEqual(created.RouteIDs, input.RouteIDs) ||
				string(created.ProtocolSettings) != string(settings) || !reflect.DeepEqual(mutation.MachineIDs, []int64{machine.ID}) {
				t.Fatalf("created detail=%#v mutation=%#v", created, mutation)
			}
			loaded, err := database.GetAdminNodeDefinition(ctx, created.ID)
			if err != nil || !reflect.DeepEqual(loaded, created) {
				t.Fatalf("round trip detail=%#v error=%v want=%#v", loaded, err, created)
			}
			runtime, err := database.GetNodeRuntime(ctx, created.ID)
			if err != nil {
				t.Fatalf("GetNodeRuntime() error = %v", err)
			}
			var config map[string]any
			if err := json.Unmarshal(runtime.Config, &config); err != nil {
				t.Fatal(err)
			}
			if config["protocol"] != protocol || config["listen_ip"] != "::" || config["server_port"] != float64(19443) {
				t.Fatalf("runtime base = %s", runtime.Config)
			}
			if protocol == "vmess" && config["network"] != "ws" {
				t.Fatalf("VMess runtime network = %v, want ws: %s", config["network"], runtime.Config)
			}
			for _, key := range requiredRuntimeKeys[protocol] {
				if _, ok := config[key]; !ok {
					t.Fatalf("%s runtime missing %q: %s", protocol, key, runtime.Config)
				}
			}
			if _, ok := config["custom_outbounds"]; !ok {
				t.Fatalf("runtime missing custom_outbounds: %s", runtime.Config)
			}
			if _, ok := config["custom_routes"]; !ok {
				t.Fatalf("runtime missing custom_routes: %s", runtime.Config)
			}
			if _, ok := config["cert_config"]; !ok {
				t.Fatalf("runtime missing cert_config: %s", runtime.Config)
			}
		})
	}
}

func TestAdminNodeDefinitionUpdateRejectsStaleInvalidAndCyclicWritesAtomically(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 17, 0, 0, 0, time.UTC)
	base := SaveAdminNodeDefinitionInput{
		Type: "vless", Name: "Parent", RateMicros: 1_000_000, Tags: []string{}, Host: "parent.example.test",
		Port: "443", ServerPort: 443, ListenAddress: "0.0.0.0", ProtocolSettings: json.RawMessage(`{"tls":0,"network":"tcp","network_settings":{},"flow":"","encryption":{"enabled":false,"encryption":"","decryption":""},"tls_settings":{"server_name":"","allow_insecure":false},"reality_settings":{},"utls":{"enabled":false,"fingerprint":"chrome"},"multiplex":{"enabled":false,"protocol":"smux","max_connections":4,"padding":false,"brutal":{"enabled":false,"up_mbps":100,"down_mbps":100}}}`),
		Show: true, Enabled: true, GroupIDs: []int64{}, RouteIDs: []int64{}, RateTimeRanges: json.RawMessage(`[]`),
		CustomOutbounds: json.RawMessage(`[]`), CustomRoutes: json.RawMessage(`[]`), CertificateConfig: json.RawMessage(`{}`),
	}
	parent, _, err := database.CreateAdminNodeDefinition(ctx, base, now)
	if err != nil {
		t.Fatal(err)
	}
	childInput := base
	childInput.Name = "Child"
	childInput.Host = "child.example.test"
	childInput.ParentID = &parent.ID
	child, _, err := database.CreateAdminNodeDefinition(ctx, childInput, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	stale := base
	stale.Revision = parent.Revision + 1
	stale.Name = "Stale"
	if _, _, err := database.UpdateAdminNodeDefinition(ctx, parent.ID, stale, now.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error=%v, want ErrConflict", err)
	}
	invalid := base
	invalid.Revision = parent.Revision
	invalid.ListenAddress = "127.0.0.1:443"
	if _, _, err := database.UpdateAdminNodeDefinition(ctx, parent.ID, invalid, now.Add(2*time.Minute)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid listen address error=%v, want ErrInvalidInput", err)
	}
	cyclic := base
	cyclic.Revision = parent.Revision
	cyclic.ParentID = &child.ID
	if _, _, err := database.UpdateAdminNodeDefinition(ctx, parent.ID, cyclic, now.Add(3*time.Minute)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cyclic parent error=%v, want ErrInvalidInput", err)
	}
	unknown := base
	unknown.Revision = parent.Revision
	unknown.ProtocolSettings = json.RawMessage(`{"tls":0,"network":"tcp","network_settings":{},"unexpected":true}`)
	if _, _, err := database.UpdateAdminNodeDefinition(ctx, parent.ID, unknown, now.Add(4*time.Minute)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown protocol field error=%v, want ErrInvalidInput", err)
	}
	after, err := database.GetAdminNodeDefinition(ctx, parent.ID)
	if err != nil || after.Revision != parent.Revision || after.Name != parent.Name || after.ParentID != nil || after.ListenAddress != "0.0.0.0" {
		t.Fatalf("failed writes changed parent=%#v error=%v", after, err)
	}
}

func TestAdminNodeDefinitionMutationMinimizesMachineListSynchronization(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 17, 30, 0, 0, time.UTC)
	machine, _, err := database.CreateMachine(ctx, CreateMachineInput{Name: "mutation-edge", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	input := SaveAdminNodeDefinitionInput{
		Type: "shadowsocks", Name: "Mutation node", RateMicros: 1_000_000, Tags: []string{},
		Host: "mutation.example.test", Port: "443", ServerPort: 443, ListenAddress: "0.0.0.0",
		ProtocolSettings: json.RawMessage(`{"cipher":"aes-128-gcm","plugin":"","plugin_opts":""}`),
		Show:             true, Enabled: true, MachineID: &machine.ID, GroupIDs: []int64{}, RouteIDs: []int64{},
		RateTimeRanges: json.RawMessage(`[]`), CustomOutbounds: json.RawMessage(`[]`), CustomRoutes: json.RawMessage(`[]`), CertificateConfig: json.RawMessage(`{}`),
	}
	created, _, err := database.CreateAdminNodeDefinition(ctx, input, now)
	if err != nil {
		t.Fatal(err)
	}

	input.Revision = created.Revision
	input.ProtocolSettings = json.RawMessage(`{"cipher":"aes-256-gcm","plugin":"","plugin_opts":""}`)
	updated, mutation, err := database.UpdateAdminNodeDefinition(ctx, created.ID, input, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(mutation.MachineIDs) != 0 {
		t.Fatalf("protocol-only update machine list syncs = %v, want none", mutation.MachineIDs)
	}
	if !reflect.DeepEqual(mutation.FullSyncs, []AdminNodeFullSync{{MachineID: machine.ID, NodeID: created.ID}}) {
		t.Fatalf("protocol-only update full syncs = %#v", mutation.FullSyncs)
	}

	input.Revision = updated.Revision
	input.Name = "Renamed mutation node"
	_, mutation, err = database.UpdateAdminNodeDefinition(ctx, created.ID, input, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mutation.MachineIDs, []int64{machine.ID}) {
		t.Fatalf("rename machine list syncs = %v, want [%d]", mutation.MachineIDs, machine.ID)
	}
	if !reflect.DeepEqual(mutation.FullSyncs, []AdminNodeFullSync{{MachineID: machine.ID, NodeID: created.ID}}) {
		t.Fatalf("rename full syncs = %#v", mutation.FullSyncs)
	}
}

func TestAdminNodeDefinitionRejectsCertificateModesThatCannotStart(t *testing.T) {
	base := SaveAdminNodeDefinitionInput{
		Type: "shadowsocks", Name: "Certificate validation", RateMicros: 1_000_000, Tags: []string{},
		Host: "cert.example.test", Port: "443", ServerPort: 443, ListenAddress: "0.0.0.0",
		ProtocolSettings: json.RawMessage(`{"cipher":"aes-128-gcm","plugin":"","plugin_opts":""}`),
		Show:             true, Enabled: true, GroupIDs: []int64{}, RouteIDs: []int64{}, RateTimeRanges: json.RawMessage(`[]`),
		CustomOutbounds: json.RawMessage(`[]`), CustomRoutes: json.RawMessage(`[]`),
	}
	tests := map[string]json.RawMessage{
		"http without domain":      json.RawMessage(`{"cert_mode":"http","http_port":80}`),
		"dns without provider":     json.RawMessage(`{"cert_mode":"dns","domain":"cert.example.test","dns_env":{"TOKEN":"secret"}}`),
		"dns without credentials":  json.RawMessage(`{"cert_mode":"dns","domain":"cert.example.test","dns_provider":"cloudflare","dns_env":{}}`),
		"file without private key": json.RawMessage(`{"cert_mode":"file","cert_file":"/etc/xboard/cert.pem"}`),
		"invalid certificate pair": json.RawMessage(`{"cert_mode":"content","cert_content":"not-a-certificate","key_content":"not-a-key"}`),
	}
	for name, certificate := range tests {
		t.Run(name, func(t *testing.T) {
			input := base
			input.CertificateConfig = certificate
			if _, err := normalizeAdminNodeDefinition(input, false); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("normalizeAdminNodeDefinition() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestAdminNodeDefinitionRejectsUnsupportedProtocolEnumerations(t *testing.T) {
	base := SaveAdminNodeDefinitionInput{
		Name: "Protocol validation", RateMicros: 1_000_000, Tags: []string{},
		Host: "enum.example.test", Port: "443", ServerPort: 443, ListenAddress: "0.0.0.0",
		Show: true, Enabled: true, GroupIDs: []int64{}, RouteIDs: []int64{}, RateTimeRanges: json.RawMessage(`[]`),
		CustomOutbounds: json.RawMessage(`[]`), CustomRoutes: json.RawMessage(`[]`), CertificateConfig: json.RawMessage(`{}`),
	}
	tests := map[string]struct {
		protocol string
		settings json.RawMessage
	}{
		"unknown Shadowsocks cipher": {
			protocol: "shadowsocks",
			settings: json.RawMessage(`{"cipher":"rot13","plugin":"","plugin_opts":""}`),
		},
		"unknown Shadowsocks plugin": {
			protocol: "shadowsocks",
			settings: json.RawMessage(`{"cipher":"aes-128-gcm","plugin":"shell-plugin","plugin_opts":""}`),
		},
		"unknown multiplex protocol": {
			protocol: "vmess",
			settings: json.RawMessage(`{"tls":0,"network":"tcp","network_settings":{},"tls_settings":{},"multiplex":{"enabled":true,"protocol":"unknown"},"utls":{"enabled":false,"fingerprint":"chrome"}}`),
		},
		"array network settings cannot be parsed by Xboard-Node": {
			protocol: "vmess",
			settings: json.RawMessage(`{"tls":0,"network":"tcp","network_settings":[],"tls_settings":{},"multiplex":{"enabled":false,"protocol":"smux"},"utls":{"enabled":false,"fingerprint":"chrome"}}`),
		},
		"unknown uTLS fingerprint": {
			protocol: "vmess",
			settings: json.RawMessage(`{"tls":1,"network":"tcp","network_settings":{},"tls_settings":{},"multiplex":{"enabled":false,"protocol":"smux"},"utls":{"enabled":true,"fingerprint":"crawler"}}`),
		},
		"unknown VLESS flow": {
			protocol: "vless",
			settings: json.RawMessage(`{"tls":0,"network":"tcp","network_settings":{},"flow":"unsafe-flow","encryption":{},"tls_settings":{},"reality_settings":{},"multiplex":{"enabled":false,"protocol":"smux"},"utls":{"enabled":false,"fingerprint":"chrome"}}`),
		},
		"Trojan does not support unencrypted mode": {
			protocol: "trojan",
			settings: json.RawMessage(`{"tls":0,"network":"tcp","network_settings":{},"tls_settings":{},"reality_settings":{},"multiplex":{"enabled":false,"protocol":"smux"},"utls":{"enabled":false,"fingerprint":"chrome"}}`),
		},
		"Reality requires server and key material": {
			protocol: "vless",
			settings: json.RawMessage(`{"tls":2,"network":"tcp","network_settings":{},"flow":"","encryption":{},"tls_settings":{},"reality_settings":{"server_port":443},"multiplex":{"enabled":false,"protocol":"smux"},"utls":{"enabled":false,"fingerprint":"chrome"}}`),
		},
		"uTLS requires transport security": {
			protocol: "vmess",
			settings: json.RawMessage(`{"tls":0,"network":"tcp","network_settings":{},"tls_settings":{},"multiplex":{"enabled":false,"protocol":"smux"},"utls":{"enabled":true,"fingerprint":"chrome"}}`),
		},
		"ECH requires client and server key material": {
			protocol: "vmess",
			settings: json.RawMessage(`{"tls":1,"network":"tcp","network_settings":{},"tls_settings":{"ech":{"enabled":true,"config":"","key":""}},"multiplex":{"enabled":false,"protocol":"smux"},"utls":{"enabled":false,"fingerprint":"chrome"}}`),
		},
		"unknown Hysteria ALPN": {
			protocol: "hysteria",
			settings: json.RawMessage(`{"version":1,"alpn":"ftp","bandwidth":{},"obfs":{"open":false,"type":"salamander","password":""},"tls":{}}`),
		},
		"unknown TUIC ALPN": {
			protocol: "tuic",
			settings: json.RawMessage(`{"version":5,"congestion_control":"bbr","alpn":["ftp"],"udp_relay_mode":"native","tls":{}}`),
		},
		"control character in TLS server name": {
			protocol: "trojan",
			settings: json.RawMessage("{\"tls\":1,\"network\":\"tcp\",\"network_settings\":{},\"tls_settings\":{\"server_name\":\"safe.example\\nforged\"},\"reality_settings\":{},\"multiplex\":{\"enabled\":false,\"protocol\":\"smux\"},\"utls\":{\"enabled\":false,\"fingerprint\":\"chrome\"}}"),
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			input := base
			input.Type = test.protocol
			input.ProtocolSettings = test.settings
			if _, err := normalizeAdminNodeDefinition(input, false); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("normalizeAdminNodeDefinition() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestAdminNodeDefinitionRejectsNodeIncompatibleCustomOutbounds(t *testing.T) {
	base := SaveAdminNodeDefinitionInput{
		Type: "shadowsocks", Name: "Outbound validation", RateMicros: 1_000_000, Tags: []string{},
		Host: "outbound.example.test", Port: "443", ServerPort: 443, ListenAddress: "0.0.0.0",
		ProtocolSettings: json.RawMessage(`{"cipher":"aes-128-gcm","plugin":"","plugin_opts":""}`),
		Show:             true, Enabled: true, GroupIDs: []int64{}, RouteIDs: []int64{}, RateTimeRanges: json.RawMessage(`[]`),
		CustomRoutes: json.RawMessage(`[]`), CertificateConfig: json.RawMessage(`{}`),
	}
	tests := map[string]json.RawMessage{
		"missing settings": json.RawMessage(`[{"tag":"proxy","protocol":"socks","settings":{}}]`),
		"duplicate tag": json.RawMessage(`[
			{"tag":"Proxy","protocol":"socks","settings":{"server":"127.0.0.1","server_port":1080}},
			{"tag":"proxy","protocol":"http","settings":{"server":"127.0.0.1","server_port":8080}}
		]`),
		"unknown proxy tag":   json.RawMessage(`[{"tag":"proxy","protocol":"socks","proxy_tag":"missing","settings":{"server":"127.0.0.1","server_port":1080}}]`),
		"reserved setting":    json.RawMessage(`[{"tag":"proxy","protocol":"socks","settings":{"tag":"nested","server":"127.0.0.1","server_port":1080}}]`),
		"invalid server port": json.RawMessage(`[{"tag":"proxy","protocol":"socks","settings":{"server":"127.0.0.1","server_port":65536}}]`),
	}
	for name, outbounds := range tests {
		t.Run(name, func(t *testing.T) {
			input := base
			input.CustomOutbounds = outbounds
			if _, err := normalizeAdminNodeDefinition(input, false); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("normalizeAdminNodeDefinition() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}
