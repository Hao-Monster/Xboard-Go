package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
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

func TestCreateNodeMaintainsSchemaV42DefinitionInvariant(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	node, err := database.CreateNode(ctx, CreateNodeInput{
		Name: "Compact create", Type: "vless", Host: "compact.test", Port: "443", Show: true, Enabled: true,
	}, time.Date(2026, 8, 28, 14, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	detail, err := database.GetAdminNodeDefinition(ctx, node.ID)
	if err != nil || detail.ServerPort != 443 || detail.ListenAddress != "0.0.0.0" || !detail.RuntimeConfigured {
		t.Fatalf("compact node detail=%#v error=%v", detail, err)
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		t.Fatalf("ValidateCurrentSchema() error = %v", err)
	}
}

func TestSchemaV42PreservesV40NodeDefinitionsAndAddsListenAddress(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC)
	node, err := database.CreateNode(ctx, CreateNodeInput{Name: "Preserved", Type: "vless", Host: "edge.test", Port: "443", Show: true, Enabled: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE node_protocol_definitions
		SET server_port = 8443, protocol_settings_json = '{"tls":0}', configured_rate_micros = 1250000
		WHERE node_id = ?
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
		t.Fatalf("Migrate(v40 to v42) error = %v", err)
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
	if version != 42 || listenAddress != "0.0.0.0" || runtime != `{"protocol":"vless","server_port":8443,"marker":"v40"}` {
		t.Fatalf("migration result version=%d listen=%q runtime=%s", version, listenAddress, runtime)
	}
}

func TestSchemaV42BackfillsV40NodesMissingProtocolDefinitions(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 18, 0, 0, 0, time.UTC)
	protocols := []string{"shadowsocks", "vmess", "trojan", "hysteria", "vless", "tuic", "socks", "naive", "http", "mieru", "anytls"}
	created := make([]Node, 0, len(protocols))
	for index, protocol := range protocols {
		startPort := 30_000 + index
		node, err := database.CreateNode(ctx, CreateNodeInput{
			Name: "Historical " + protocol, Type: protocol, Host: protocol + ".upgrade.test",
			Port: fmt.Sprintf("%d-%d", startPort, startPort+1), Show: index%2 == 0, Enabled: true, Sort: index,
		}, now.Add(time.Duration(index)*time.Second))
		if err != nil {
			t.Fatalf("CreateNode(%s) error = %v", protocol, err)
		}
		created = append(created, node)
	}
	legacyRuntime := `{"protocol":"shadowsocks","listen_ip":"::","server_port":18443,"network":null,"networkSettings":null,"cipher":"aes-256-gcm","plugin":"obfs","plugin_opts":"obfs=http"}`
	if _, err := database.db.ExecContext(ctx, `
		DELETE FROM node_protocol_definitions;
		UPDATE nodes SET runtime_config = NULL;
		UPDATE nodes SET traffic_u = 123, traffic_d = 456, admin_revision = 7,
			rate_micros = 1250000, runtime_config = ? WHERE id = ?;
		ALTER TABLE node_protocol_definitions DROP COLUMN listen_address;
		PRAGMA user_version = 40;
	`, legacyRuntime, created[0].ID); err != nil {
		t.Fatalf("prepare v40 database: %v", err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v40 to v42) error = %v", err)
	}

	for index, node := range created {
		detail, err := database.GetAdminNodeDefinition(ctx, node.ID)
		if err != nil {
			t.Fatalf("GetAdminNodeDefinition(%s) error = %v", protocols[index], err)
		}
		startPort := 30_000 + index
		wantRevision := node.Revision
		if index == 0 {
			wantRevision = 7
		}
		wantServerPort, wantListenAddress := startPort, "0.0.0.0"
		if index == 0 {
			wantServerPort, wantListenAddress = 18443, "::"
		}
		if detail.Type != protocols[index] || detail.ServerPort != wantServerPort || detail.ListenAddress != wantListenAddress ||
			len(detail.ProtocolSettings) == 0 || detail.Revision != wantRevision {
			t.Fatalf("backfilled %s definition = %#v", protocols[index], detail)
		}
		var runtimeJSON string
		if err := database.db.QueryRowContext(ctx, `SELECT runtime_config FROM nodes WHERE id = ?`, node.ID).Scan(&runtimeJSON); err != nil {
			t.Fatalf("read %s runtime: %v", protocols[index], err)
		}
		if index == 0 && runtimeJSON != legacyRuntime {
			t.Fatalf("historical runtime changed: got %s want %s", runtimeJSON, legacyRuntime)
		}
		var runtime map[string]any
		if err := json.Unmarshal([]byte(runtimeJSON), &runtime); err != nil {
			t.Fatalf("decode %s runtime: %v", protocols[index], err)
		}
		if runtime["protocol"] != protocols[index] || runtime["listen_ip"] != wantListenAddress || runtime["server_port"] != float64(wantServerPort) {
			t.Fatalf("backfilled %s runtime = %#v", protocols[index], runtime)
		}
		if index == 0 {
			var settings map[string]any
			if err := json.Unmarshal(detail.ProtocolSettings, &settings); err != nil {
				t.Fatal(err)
			}
			if settings["cipher"] != "aes-256-gcm" || settings["plugin"] != "obfs" || detail.Rate != 1.25 {
				t.Fatalf("historical runtime fields were not recovered: detail=%#v settings=%#v", detail, settings)
			}
		}
	}
	first, err := database.GetNode(ctx, created[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 7 || first.TrafficUpload != 123 || first.TrafficDownload != 456 {
		t.Fatalf("historical counters changed: %#v", first)
	}
	var version int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 42 {
		t.Fatalf("schema version = %d, want 42", version)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	var definitions int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_protocol_definitions`).Scan(&definitions); err != nil {
		t.Fatal(err)
	}
	if definitions != len(protocols) {
		t.Fatalf("node definitions after idempotent migration = %d, want %d", definitions, len(protocols))
	}
}

func TestSchemaV42BackfillRollsBackUnsupportedHistoricalNode(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 18, 30, 0, 0, time.UTC).Unix()
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO nodes (name, type, host, port, show, enabled, sort, created_at, updated_at)
		VALUES ('Unsupported historical node', 'unknown', 'unknown.test', '443', 1, 1, 0, ?, ?)
	`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `PRAGMA user_version = 41`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err == nil || !strings.Contains(err.Error(), "prepare protocol definition") {
		t.Fatalf("Migrate() error = %v, want unsupported historical node", err)
	}
	var version, definitions int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_protocol_definitions`).Scan(&definitions); err != nil {
		t.Fatal(err)
	}
	if version != 41 || definitions != 0 {
		t.Fatalf("failed migration was not atomic: version=%d definitions=%d", version, definitions)
	}
}

func TestSchemaV42BackfillCrossesBoundedBatchBoundary(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 18, 45, 0, 0, time.UTC).Unix()
	if _, err := database.db.ExecContext(ctx, `
		WITH RECURSIVE sequence(value) AS (
			SELECT 1 UNION ALL SELECT value + 1 FROM sequence WHERE value < 257
		)
		INSERT INTO nodes (name, type, host, port, show, enabled, sort, created_at, updated_at)
		SELECT printf('Batch node %d', value), 'vless', printf('batch-%d.test', value), '443', 1, 1, value, ?, ?
		FROM sequence
	`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `PRAGMA user_version = 41`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(257 nodes) error = %v", err)
	}
	var definitions, runtimes int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_protocol_definitions`).Scan(&definitions); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE runtime_config IS NOT NULL`).Scan(&runtimes); err != nil {
		t.Fatal(err)
	}
	if definitions != 257 || runtimes != 257 {
		t.Fatalf("bounded backfill definitions=%d runtimes=%d, want 257/257", definitions, runtimes)
	}
}

func TestSchemaV42BackfillPreservesNonCanonicalLegacyRuntime(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 18, 50, 0, 0, time.UTC)
	node, err := database.CreateNode(ctx, CreateNodeInput{
		Name: "Hand-written runtime", Type: "vless", Host: "handwritten.test", Port: "443", Show: true, Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	runtime := `{"protocol":"vless","listen_ip":"not-an-ip","server_port":9443,"extension":{"preserve":true}}`
	if _, err := database.db.ExecContext(ctx, `DELETE FROM node_protocol_definitions WHERE node_id = ?`, node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE nodes SET runtime_config = ? WHERE id = ?`, runtime, node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `PRAGMA user_version = 41`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(non-canonical runtime) error = %v", err)
	}
	detail, err := database.GetAdminNodeDefinition(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	var preserved string
	if err := database.db.QueryRowContext(ctx, `SELECT runtime_config FROM nodes WHERE id = ?`, node.ID).Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if detail.ServerPort != 443 || detail.ListenAddress != "0.0.0.0" || preserved != runtime {
		t.Fatalf("fallback detail=%#v runtime=%s", detail, preserved)
	}
}

func TestValidateSchemaV42RejectsNodeWithoutProtocolDefinition(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	input, err := NewBasicAdminNodeDefinitionInput(CreateNodeInput{
		Name: "Schema invariant", Type: "vless", Host: "schema.test", Port: "443", Show: true, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	created, _, err := database.CreateAdminNodeDefinition(ctx, input, time.Date(2026, 8, 28, 19, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM node_protocol_definitions WHERE node_id = ?`, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSchema(ctx, database.db, CurrentSchemaVersion()); err == nil || !strings.Contains(err.Error(), "without a protocol definition") {
		t.Fatalf("ValidateSchema() error = %v, want missing protocol definition", err)
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

	cases := adminNodeProtocolSettingsFixtures()
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

func TestHydrateLegacyNodeDefinitionPreservesEveryGeneratedProtocolRuntime(t *testing.T) {
	createdAt := time.Date(2026, 8, 28, 16, 30, 0, 0, time.UTC)
	for protocol, settings := range adminNodeProtocolSettingsFixtures() {
		t.Run(protocol, func(t *testing.T) {
			base, err := NewBasicAdminNodeDefinitionInput(CreateNodeInput{
				Name: "Legacy " + protocol, Type: protocol, Host: protocol + ".legacy.test",
				Port: "18443-18444", Show: true, Enabled: true, Sort: 4,
			})
			if err != nil {
				t.Fatal(err)
			}
			original := base
			original.RateMicros = 1_250_000
			original.ServerPort = 19443
			original.ListenAddress = "::"
			original.ProtocolSettings = settings
			original.CustomOutbounds = json.RawMessage(`[{"tag":"proxy-out","protocol":"socks","settings":{"server":"127.0.0.1","server_port":1080}}]`)
			original.CustomRoutes = json.RawMessage(`[{"action":{"type":"route","target":"direct"}}]`)
			original.CertificateConfig = json.RawMessage(`{"cert_mode":"file","domain":"legacy.test","cert_file":"/etc/xboard/cert.pem","key_file":"/etc/xboard/key.pem"}`)
			normalizedOriginal, err := normalizeAdminNodeDefinition(original, false)
			if err != nil {
				t.Fatalf("normalize original definition: %v", err)
			}
			runtime, err := buildAdminNodeRuntime(normalizedOriginal, createdAt, nil)
			if err != nil {
				t.Fatalf("build original runtime: %v", err)
			}

			base.RateMicros = original.RateMicros
			hydrated := hydrateLegacyNodeDefinition(base, runtime)
			normalizedHydrated, err := normalizeAdminNodeDefinition(hydrated, false)
			if err != nil {
				t.Fatalf("normalize hydrated definition: %v\nruntime=%s", err, runtime)
			}
			rebuilt, err := buildAdminNodeRuntime(normalizedHydrated, createdAt, nil)
			if err != nil {
				t.Fatalf("build hydrated runtime: %v", err)
			}
			var want, got any
			if err := json.Unmarshal(runtime, &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(rebuilt, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("hydrated runtime changed\n got: %s\nwant: %s", rebuilt, runtime)
			}
		})
	}
}

func adminNodeProtocolSettingsFixtures() map[string]json.RawMessage {
	return map[string]json.RawMessage{
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
