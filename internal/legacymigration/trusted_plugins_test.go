package legacymigration

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

const legacyTelegramPluginConfigFixture = `{"enable_ticket_notify":false,"enable_payment_notify":true,"start_welcome_title":"欢迎","start_bot_description":"助手","start_bind_guide":"绑定","start_unbind_guide":"解绑","start_bind_commands":"命令","start_footer":"页脚","help_text":"帮助"}`

func TestReadTrustedPluginsSnapshotNormalizesFixedInventory(t *testing.T) {
	path := createLegacyTrustedPluginsSnapshot(t)
	snapshot, err := ReadTrustedPluginsSnapshot(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Path == "" || snapshot.Size < 512 || len(snapshot.SHA256) != 64 || len(snapshot.Checksum) != 64 || len(snapshot.Plugins) != 7 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	for _, plugin := range snapshot.Plugins {
		switch plugin.Code {
		case store.TrustedPluginTelegram:
			if plugin.Type != "feature" || plugin.Version != "1.0.1" || plugin.Enabled || len(plugin.Config) != 9 || plugin.Config["help_text"] != "帮助" {
				t.Fatalf("Telegram plugin=%#v", plugin)
			}
		default:
			if plugin.Type != "payment" || plugin.Version != "1.0.0" || !plugin.Enabled || len(plugin.Config) != 0 {
				t.Fatalf("payment plugin=%#v", plugin)
			}
		}
	}
}

func TestReadTrustedPluginsSnapshotRejectsUntrustedOrAmbiguousInventory(t *testing.T) {
	for name, mutate := range map[string]func(*sql.DB){
		"unknown plugin": func(database *sql.DB) {
			_, _ = database.Exec(`INSERT INTO v2_plugins(name,code,version,is_enabled,config,type) VALUES('Shell','shell','1.0.0',1,'[]','feature')`)
		},
		"missing plugin": func(database *sql.DB) {
			_, _ = database.Exec(`DELETE FROM v2_plugins WHERE code='mgate'`)
		},
		"duplicate code": func(database *sql.DB) {
			_, _ = database.Exec(`DELETE FROM v2_plugins WHERE code='mgate'; INSERT INTO v2_plugins(name,code,version,is_enabled,config,type) VALUES('EPay','epay','1.0.0',1,'[]','payment')`)
		},
		"wrong name": func(database *sql.DB) {
			_, _ = database.Exec(`UPDATE v2_plugins SET name='Telegram Bot' WHERE code='telegram'`)
		},
		"wrong version": func(database *sql.DB) {
			_, _ = database.Exec(`UPDATE v2_plugins SET version='9.9.9' WHERE code='epay'`)
		},
		"wrong type": func(database *sql.DB) {
			_, _ = database.Exec(`UPDATE v2_plugins SET type='feature' WHERE code='epay'`)
		},
		"invalid enabled": func(database *sql.DB) {
			_, _ = database.Exec(`UPDATE v2_plugins SET is_enabled=2 WHERE code='epay'`)
		},
		"nonempty payment config": func(database *sql.DB) {
			_, _ = database.Exec(`UPDATE v2_plugins SET config='{"secret":"must-not-migrate"}' WHERE code='epay'`)
		},
		"unknown Telegram field": func(database *sql.DB) {
			_, _ = database.Exec(`UPDATE v2_plugins SET config=json_set(config,'$.command','unsafe') WHERE code='telegram'`)
		},
		"duplicate Telegram field": func(database *sql.DB) {
			duplicated := strings.Replace(legacyTelegramPluginConfigFixture, `"enable_ticket_notify":false`, `"enable_ticket_notify":true,"enable_ticket_notify":false`, 1)
			_, _ = database.Exec(`UPDATE v2_plugins SET config=? WHERE code='telegram'`, duplicated)
		},
		"invalid UTF-8 Telegram config": func(database *sql.DB) {
			invalid := strings.Replace(legacyTelegramPluginConfigFixture, "欢迎", string([]byte{0xff}), 1)
			_, _ = database.Exec(`UPDATE v2_plugins SET config=? WHERE code='telegram'`, invalid)
		},
		"malformed config": func(database *sql.DB) {
			_, _ = database.Exec(`UPDATE v2_plugins SET config='{' WHERE code='telegram'`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := createLegacyTrustedPluginsSnapshot(t)
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			mutate(database)
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadTrustedPluginsSnapshot(t.Context(), path); err == nil {
				t.Fatal("untrusted plugin inventory was accepted")
			}
		})
	}
}

func TestReadTrustedPluginsSnapshotMatchesCapturedRealSnapshot(t *testing.T) {
	path := strings.TrimSpace(os.Getenv("XBOARD_TEST_LEGACY_TRUSTED_PLUGINS_SNAPSHOT"))
	if path == "" {
		t.Skip("set XBOARD_TEST_LEGACY_TRUSTED_PLUGINS_SNAPSHOT to the captured local legacy snapshot")
	}
	snapshot, err := ReadTrustedPluginsSnapshot(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SHA256 != "a23d9a90866b4aec783a617afe766c227221cf7fbde272ae1f261ddb5f02ac9b" || len(snapshot.Plugins) != 7 {
		t.Fatalf("real snapshot identity=%s plugins=%d", snapshot.SHA256, len(snapshot.Plugins))
	}
	for _, plugin := range snapshot.Plugins {
		if !plugin.Enabled {
			t.Fatalf("real snapshot plugin %q is unexpectedly disabled", plugin.Code)
		}
	}
}

func createLegacyTrustedPluginsSnapshot(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-trusted-plugins.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_plugins(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			code TEXT NOT NULL,
			version TEXT NOT NULL,
			is_enabled INTEGER NOT NULL,
			config TEXT NOT NULL,
			installed_at INTEGER,
			created_at INTEGER,
			updated_at INTEGER,
			type TEXT NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		name, code, version, config, pluginType string
		enabled                                 int
	}{
		{name: "AlipayF2F", code: "alipay_f2f", version: "1.0.0", enabled: 1, config: "[]", pluginType: "payment"},
		{name: "BTCPay", code: "btcpay", version: "1.0.0", enabled: 1, config: "[]", pluginType: "payment"},
		{name: "CoinPayments", code: "coin_payments", version: "1.0.0", enabled: 1, config: "[]", pluginType: "payment"},
		{name: "Coinbase", code: "coinbase", version: "1.0.0", enabled: 1, config: "[]", pluginType: "payment"},
		{name: "EPay", code: "epay", version: "1.0.0", enabled: 1, config: "[]", pluginType: "payment"},
		{name: "MGate", code: "mgate", version: "1.0.0", enabled: 1, config: "[]", pluginType: "payment"},
		{name: "Telegram Bot 集成", code: "telegram", version: "1.0.1", enabled: 0, config: legacyTelegramPluginConfigFixture, pluginType: "feature"},
	}
	for _, row := range rows {
		if _, err := database.Exec(`INSERT INTO v2_plugins(name,code,version,is_enabled,config,type) VALUES(?,?,?,?,?,?)`, row.name, row.code, row.version, row.enabled, row.config, row.pluginType); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
