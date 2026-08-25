package legacymigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestReadNodesSnapshotPreservesOperationalNodeDomainWithoutPlaintextSecrets(t *testing.T) {
	path := createLegacyNodesSnapshot(t)
	snapshot, err := ReadNodesSnapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadNodesSnapshot() error = %v", err)
	}
	if snapshot.Path != path || len(snapshot.SHA256) != 64 || len(snapshot.Machines) != 1 || len(snapshot.Nodes) != 1 || len(snapshot.Schedules) != 1 || len(snapshot.Traffic) != 1 {
		t.Fatalf("snapshot identity/counts = %#v", snapshot)
	}
	if len(snapshot.Credentials) != 1 || snapshot.Credentials[0].TokenHash != security.DigestToken("legacy-machine-token") || snapshot.Credentials[0].TokenPrefix != "legacy-machi" {
		t.Fatalf("derived credential = %#v", snapshot.Credentials)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "legacy-machine-token") {
		t.Fatal("snapshot retained the legacy plaintext machine token")
	}
	node := snapshot.Nodes[0]
	if node.ID != 41 || node.Type != "vless" || node.RateMicros != 1_250_000 || len(node.GroupIDs) != 2 || node.GroupIDs[0] != 3 || node.GroupIDs[1] != 9 || node.ParentID != nil || node.ServerPort != 8443 || node.TransferEnable != 1_000_000 {
		t.Fatalf("node = %#v", node)
	}
	var runtime map[string]any
	if err := json.Unmarshal(node.RuntimeConfig, &runtime); err != nil {
		t.Fatal(err)
	}
	if runtime["protocol"] != "vless" || runtime["server_port"] != float64(8443) || runtime["custom_outbounds"] == nil || runtime["cert_config"] == nil {
		t.Fatalf("runtime config = %#v", runtime)
	}
	wantOutbounds := []any{map[string]any{
		"tag": "upstream", "protocol": "socks",
		"settings": map[string]any{"servers": []any{map[string]any{"address": "192.0.2.1", "port": float64(1080)}}},
	}}
	wantRoutes := []any{map[string]any{"type": "field", "domain": []any{"full:example.test"}, "outboundTag": "direct"}}
	if !reflect.DeepEqual(runtime["custom_outbounds"], wantOutbounds) || !reflect.DeepEqual(runtime["custom_routes"], wantRoutes) {
		t.Fatalf("custom runtime config aliases backing arrays: %#v", runtime)
	}
	if cert, ok := runtime["cert_config"].(map[string]any); !ok || cert["cert_mode"] != "file" || cert["cert_file"] != "/cert.pem" || cert["key_file"] != "/key.pem" || cert["mode"] != nil {
		t.Fatalf("certificate runtime config = %#v", runtime["cert_config"])
	}
	if snapshot.Traffic[0].Upload != 15 || snapshot.Traffic[0].Download != 27 {
		t.Fatalf("aggregated traffic = %#v", snapshot.Traffic)
	}
	for label, digest := range map[string]string{"machines": snapshot.Checksums.Machines, "nodes": snapshot.Checksums.Nodes, "schedules": snapshot.Checksums.Schedules, "traffic": snapshot.Checksums.Traffic} {
		if len(digest) != 64 {
			t.Fatalf("%s checksum = %q", label, digest)
		}
	}
}

func TestReadNodesSnapshotRejectsInFlightReportReceipts(t *testing.T) {
	path := createLegacyNodesSnapshot(t)
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO v2_server_report_receipt (server_id,report_id,job_type,chunk_index,created_at) VALUES (41,'report','stat_server',0,1)`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	_ = database.Close()
	if _, err := ReadNodesSnapshot(context.Background(), path); err == nil || !strings.Contains(err.Error(), "drained") {
		t.Fatalf("ReadNodesSnapshot() error = %v, want in-flight-state rejection", err)
	}
}

func TestReadNodesSnapshotRejectsLossyNodeState(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		statement string
		contains  string
	}{
		{name: "negative fractional rate", statement: `UPDATE v2_server SET rate = -0.5`, contains: "rate"},
		{name: "traffic protocol mismatch", statement: `UPDATE v2_stat_server SET server_type = 'trojan'`, contains: "inconsistent"},
		{name: "duplicate group membership", statement: `UPDATE v2_server SET group_ids = '[3,3]'`, contains: "duplicate"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path := createLegacyNodesSnapshot(t)
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(scenario.statement); err != nil {
				_ = database.Close()
				t.Fatal(err)
			}
			_ = database.Close()
			if _, err := ReadNodesSnapshot(context.Background(), path); err == nil || !strings.Contains(strings.ToLower(err.Error()), scenario.contains) {
				t.Fatalf("ReadNodesSnapshot() error = %v, want %q", err, scenario.contains)
			}
		})
	}

	t.Run("empty certificate array is canonical empty object", func(t *testing.T) {
		path := createLegacyNodesSnapshot(t)
		database, err := sql.Open("sqlite", "file:"+path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`UPDATE v2_server SET cert_config = '[]'`); err != nil {
			t.Fatal(err)
		}
		_ = database.Close()
		snapshot, err := ReadNodesSnapshot(context.Background(), path)
		if err != nil || string(snapshot.Nodes[0].CertConfig) != "{}" {
			t.Fatalf("certificate config = %s, err=%v", snapshot.Nodes[0].CertConfig, err)
		}
	})
}

func TestBuildLegacyRuntimeConfigCoversEverySupportedProtocol(t *testing.T) {
	cases := map[string]string{
		"shadowsocks": `{"cipher":"2022-blake3-aes-128-gcm","plugin":"","plugin_opts":""}`,
		"vmess":       `{"tls":1,"tls_settings":{"server_name":"vmess.example"},"multiplex":{}}`,
		"trojan":      `{"tls":2,"tls_settings":{"server_name":"trojan.example"},"reality_settings":{"server_name":"reality.example"},"multiplex":{}}`,
		"vless":       `{"tls":1,"flow":"xtls-rprx-vision","encryption":{"enabled":true,"decryption":"mlkem768x25519plus"},"tls_settings":{"server_name":"vless.example"},"multiplex":{}}`,
		"hysteria":    `{"version":2,"tls":{"server_name":"hy.example"},"bandwidth":{"up":100,"down":200},"obfs":{"open":true,"type":"salamander","password":"secret"}}`,
		"tuic":        `{"version":5,"tls":{"server_name":"tuic.example"},"congestion_control":"bbr"}`,
		"anytls":      `{"tls":{"server_name":"any.example"},"padding_scheme":["stop=8"]}`,
		"socks":       `{"tls":0,"tls_settings":{}}`,
		"naive":       `{"tls":1,"tls_settings":{"server_name":"naive.example"}}`,
		"http":        `{"tls":1,"tls_settings":{"server_name":"http.example"}}`,
		"mieru":       `{"transport":"TCP","traffic_pattern":""}`,
	}
	expected := map[string]string{
		"shadowsocks": `{"protocol":"shadowsocks","listen_ip":"0.0.0.0","server_port":443,"network":null,"networkSettings":null,"cipher":"2022-blake3-aes-128-gcm","plugin":"","plugin_opts":"","server_key":"` +
			"MjQ5MjBk" + "ZWNmODNmNjk2MA==" + `"}`,
		"vmess":    `{"protocol":"vmess","listen_ip":"0.0.0.0","server_port":443,"network":null,"networkSettings":null,"tls":1,"tls_settings":{"server_name":"vmess.example","allow_insecure":false,"ech":null},"multiplex":{"enabled":false,"protocol":"yamux","max_connections":null,"padding":false,"brutal":null}}`,
		"trojan":   `{"protocol":"trojan","listen_ip":"0.0.0.0","server_port":443,"network":null,"networkSettings":null,"host":"trojan.example.test","server_name":"trojan.example","multiplex":{"enabled":false,"protocol":"yamux","max_connections":null,"padding":false,"brutal":null},"tls":2,"tls_settings":{"server_name":"reality.example","server_port":null,"public_key":null,"private_key":null,"short_id":null,"allow_insecure":false}}`,
		"vless":    `{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443,"network":null,"networkSettings":null,"tls":1,"flow":"xtls-rprx-vision","decryption":"mlkem768x25519plus","tls_settings":{"server_name":"vless.example","allow_insecure":false,"ech":null},"multiplex":{"enabled":false,"protocol":"yamux","max_connections":null,"padding":false,"brutal":null}}`,
		"hysteria": `{"protocol":"hysteria","listen_ip":"0.0.0.0","server_port":443,"network":null,"networkSettings":null,"version":2,"host":"hysteria.example.test","server_name":"hy.example","tls_settings":{"server_name":"hy.example","allow_insecure":false,"ech":null},"up_mbps":100,"down_mbps":200,"obfs":"salamander","obfs-password":"secret"}`,
		"tuic":     `{"protocol":"tuic","listen_ip":"0.0.0.0","server_port":443,"network":null,"networkSettings":null,"version":5,"server_name":"tuic.example","congestion_control":"bbr","tls_settings":{"server_name":"tuic.example","allow_insecure":false,"ech":null},"auth_timeout":"3s","zero_rtt_handshake":false,"heartbeat":"3s"}`,
		"anytls":   `{"protocol":"anytls","listen_ip":"0.0.0.0","server_port":443,"network":null,"networkSettings":null,"server_name":"any.example","tls_settings":{"server_name":"any.example","allow_insecure":false,"ech":null},"padding_scheme":["stop=8"]}`,
		"socks":    `{"protocol":"socks","listen_ip":"0.0.0.0","server_port":443,"network":null,"networkSettings":null,"tls":0,"tls_settings":{"server_name":null,"allow_insecure":false,"ech":null}}`,
		"naive":    `{"protocol":"naive","listen_ip":"0.0.0.0","server_port":443,"network":null,"networkSettings":null,"tls":1,"tls_settings":{"server_name":"naive.example","allow_insecure":false,"ech":null}}`,
		"http":     `{"protocol":"http","listen_ip":"0.0.0.0","server_port":443,"network":null,"networkSettings":null,"tls":1,"tls_settings":{"server_name":"http.example","allow_insecure":false,"ech":null}}`,
		"mieru":    `{"protocol":"mieru","listen_ip":"0.0.0.0","server_port":443,"network":null,"networkSettings":null,"transport":"TCP","traffic_pattern":""}`,
	}
	for protocol, settings := range cases {
		t.Run(protocol, func(t *testing.T) {
			node := store.LegacyNode{
				Type: protocol, Host: protocol + ".example.test", ServerPort: 443, CreatedAt: 1_700_000_000,
				ProtocolSettings: json.RawMessage(settings), CustomOutbounds: json.RawMessage(`[]`), CustomRoutes: json.RawMessage(`[]`), CertConfig: json.RawMessage(`{}`),
			}
			encoded, err := buildLegacyRuntimeConfig(node)
			if err != nil {
				t.Fatalf("buildLegacyRuntimeConfig() error = %v", err)
			}
			var config, want map[string]any
			if err := json.Unmarshal(encoded, &config); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(expected[protocol]), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(config, want) {
				t.Fatalf("runtime config differs from Xboard oracle\n got: %s\nwant: %s", encoded, expected[protocol])
			}
		})
	}
}

func TestParseLegacyRateRejectsNegativeZeroAndLossyPrecision(t *testing.T) {
	for _, value := range []string{"-0.5", "+1", "1.0000001", "1000.1", "NaN"} {
		if _, err := parseLegacyRate(value); err == nil {
			t.Fatalf("parseLegacyRate(%q) accepted unsafe value", value)
		}
	}
	if got, err := parseLegacyRate("0"); err != nil || got != 0 {
		t.Fatalf("parseLegacyRate(0) = %d, %v", got, err)
	}
}

func BenchmarkReadNodesSnapshotTenThousandNodes(b *testing.B) {
	path := createLegacyNodesSnapshot(b)
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		b.Fatal(err)
	}
	tx, err := database.Begin()
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.Prepare(`INSERT INTO v2_server (id,type,group_ids,route_ids,name,rate,tags,host,port,server_port,protocol_settings,show,sort,created_at,updated_at,rate_time_enable,rate_time_ranges,custom_outbounds,custom_routes,cert_config,transfer_enable,u,d,machine_id,enabled) VALUES (?,'vless','[]','[]',?,1,'[]','edge.example.test','443',443,'{"tls":0}',1,?,1700000000,1700000000,0,'[]','[]','[]','{}',0,0,0,7,1)`)
	if err != nil {
		b.Fatal(err)
	}
	for index := 42; index <= 10_040; index++ {
		if _, err := statement.Exec(index, "Node "+strconv.Itoa(index), index); err != nil {
			b.Fatal(err)
		}
	}
	_ = statement.Close()
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	if err := database.Close(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		snapshot, err := ReadNodesSnapshot(context.Background(), path)
		if err != nil {
			b.Fatal(err)
		}
		if len(snapshot.Nodes) != 10_000 {
			b.Fatalf("nodes = %d, want 10000", len(snapshot.Nodes))
		}
	}
	b.ReportMetric(10_000, "nodes/op")
}

func createLegacyNodesSnapshot(t testing.TB) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-nodes.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_server_machine (id INTEGER PRIMARY KEY,name TEXT NOT NULL,token TEXT NOT NULL,notes TEXT,is_active INTEGER NOT NULL,last_seen_at INTEGER,load_status TEXT,created_at DATETIME,updated_at DATETIME);
		CREATE TABLE v2_server_machine_credential (id INTEGER PRIMARY KEY,machine_id INTEGER NOT NULL,token_hash TEXT NOT NULL,token_prefix TEXT NOT NULL,last_used_at INTEGER,revoked_at INTEGER,created_at DATETIME,updated_at DATETIME);
		CREATE TABLE v2_server_machine_enrollment (id INTEGER PRIMARY KEY,machine_id INTEGER NOT NULL,code_hash TEXT NOT NULL,revoke_existing INTEGER NOT NULL,expires_at INTEGER NOT NULL,consumed_at INTEGER,created_at DATETIME,updated_at DATETIME);
		CREATE TABLE v2_server_machine_load_history (id INTEGER PRIMARY KEY,machine_id INTEGER NOT NULL,cpu REAL NOT NULL,mem_total INTEGER NOT NULL,mem_used INTEGER NOT NULL,disk_total INTEGER NOT NULL,disk_used INTEGER NOT NULL,recorded_at INTEGER NOT NULL,created_at DATETIME,updated_at DATETIME,net_in_speed REAL,net_out_speed REAL);
		CREATE TABLE v2_server (id INTEGER PRIMARY KEY,type TEXT NOT NULL,code TEXT,parent_id INTEGER,group_ids TEXT,route_ids TEXT,name TEXT NOT NULL,rate NUMERIC NOT NULL,tags TEXT,host TEXT NOT NULL,port TEXT NOT NULL,server_port INTEGER NOT NULL,protocol_settings TEXT,show INTEGER NOT NULL,sort INTEGER,created_at DATETIME,updated_at DATETIME,rate_time_enable INTEGER NOT NULL,rate_time_ranges TEXT,custom_outbounds TEXT,custom_routes TEXT,cert_config TEXT,transfer_enable INTEGER,u INTEGER NOT NULL,d INTEGER NOT NULL,machine_id INTEGER,enabled INTEGER);
		CREATE TABLE v2_server_activation_schedule (id INTEGER PRIMARY KEY,server_id INTEGER NOT NULL,enable_at INTEGER NOT NULL,disable_at INTEGER NOT NULL,revision TEXT NOT NULL,enabled_applied_at INTEGER,disabled_applied_at INTEGER,created_at DATETIME,updated_at DATETIME,schedule_type TEXT NOT NULL,timezone TEXT,enable_second INTEGER,disable_second INTEGER,next_transition_at INTEGER,next_target_enabled INTEGER);
		CREATE TABLE v2_server_report_receipt (id INTEGER PRIMARY KEY,server_id INTEGER NOT NULL,report_id TEXT NOT NULL,job_type TEXT NOT NULL,chunk_index INTEGER NOT NULL,created_at INTEGER NOT NULL);
		CREATE TABLE v2_stat_server (id INTEGER PRIMARY KEY,server_id INTEGER NOT NULL,server_type TEXT NOT NULL,u INTEGER NOT NULL,d INTEGER NOT NULL,record_type TEXT NOT NULL,record_at INTEGER NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL);
		INSERT INTO v2_server_machine VALUES (7,'machine-a','legacy-machine-token','edge',1,1700000200,'{"cpu":12}', '2023-11-14 22:13:20','2023-11-14 22:15:00');
		INSERT INTO v2_server VALUES (41,'vless','edge-code',NULL,'["9","3"]','[12]','VLESS edge',1.25,'["premium"]','edge.example.test','443-449',8443,'{"tls":1,"flow":"xtls-rprx-vision","encryption":{"enabled":false},"tls_settings":{"server_name":"edge.example.test"}}',1,2,'2023-11-14 22:13:20','2023-11-14 22:15:00',1,'[{"start":"08:00","end":"09:00","rate":2}]','[{"tag":"upstream","protocol":"socks","settings":{"servers":[{"address":"192.0.2.1","port":1080}]}}]','[{"type":"field","domain":["full:example.test"],"outboundTag":"direct"}]','{"mode":"file","cert_file":"/cert.pem","key_file":"/key.pem"}',1000000,5,7,7,1);
		INSERT INTO v2_server_activation_schedule VALUES (1,41,0,0,'schedule-revision',1700000000,NULL,'2023-11-14 22:13:20','2023-11-14 22:15:00','daily','Asia/Singapore',28800,72000,1700003600,0);
		INSERT INTO v2_stat_server VALUES (1,41,'vless',10,20,'d',1699920000,1700000000,1700000100);
		INSERT INTO v2_stat_server VALUES (2,41,'vless',5,7,'d',1699920000,1700000200,1700000300);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
