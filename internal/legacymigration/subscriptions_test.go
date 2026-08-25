package legacymigration

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestReadSubscriptionConfigSnapshotPreservesEffectiveLegacySettingsAndTemplates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-subscriptions.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_settings (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, value TEXT);
		CREATE TABLE v2_subscribe_templates (
			id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, content TEXT, created_at INTEGER, updated_at INTEGER
		);
		INSERT INTO v2_settings (name,value) VALUES
			('subscribe_path','legacy_feed'),
			('show_info_to_server_enable','1'),
			('show_protocol_to_server_enable','0'),
			('server_token','must-not-be-read');
		INSERT INTO v2_subscribe_templates (name,content) VALUES
			('singbox',''),('clash','{proxies: [], proxy-groups: [], rules: []}'),('clashmeta',''),
			('stash',''),('surge',''),('surfboard','');
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	snapshot, err := ReadSubscriptionConfigSnapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadSubscriptionConfigSnapshot() error = %v", err)
	}
	if snapshot.Path != path || snapshot.Size < 512 || len(snapshot.SHA256) != 64 || snapshot.Config.Path != "legacy_feed" ||
		!snapshot.Config.ShowInfo || snapshot.Config.ShowProtocol || snapshot.Config.Templates["clash"] == "" ||
		snapshot.Checksum != store.LegacySubscriptionConfigChecksum(snapshot.Config) || strings.Contains(snapshot.Checksum, "must-not-be-read") {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestReadSubscriptionConfigSnapshotUsesXboardDefaultsWithoutTemplateTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-subscriptions-default.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_settings (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, value TEXT);
		INSERT INTO v2_settings (name,value) VALUES ('unrelated', zeroblob(1024));
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadSubscriptionConfigSnapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadSubscriptionConfigSnapshot() error = %v", err)
	}
	if snapshot.Config.Path != "s" || snapshot.Config.ShowInfo || snapshot.Config.ShowProtocol || len(snapshot.Config.Templates) != 6 {
		t.Fatalf("default config = %#v", snapshot.Config)
	}
	for name, content := range snapshot.Config.Templates {
		if content != "" {
			t.Fatalf("default template %s = %q", name, content)
		}
	}
}
