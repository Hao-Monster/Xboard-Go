package legacymigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	_ "modernc.org/sqlite"
)

func TestReadNodeAgentSettingsSnapshotHashesPlaintextImmediately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-node-settings.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	token := "legacy-agent-token-1234567890"
	if _, err := database.Exec(`CREATE TABLE v2_settings (id INTEGER PRIMARY KEY, name TEXT, value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('server_token',?),('server_pull_interval','31'),('server_push_interval','29'),
		('device_limit_mode','1'),('server_ws_enable','1'),('server_ws_url','wss://panel.example.test/ws')`, token); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()

	snapshot, err := ReadNodeAgentSettingsSnapshot(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Settings.ServerTokenHash != security.DigestToken(token) || snapshot.Settings.ServerTokenPrefix != token[:8] ||
		snapshot.Settings.PullInterval != 31 || snapshot.Settings.PushInterval != 29 || snapshot.Settings.DeviceLimitMode != 1 ||
		!snapshot.Settings.WebSocketEnabled || snapshot.Settings.WebSocketURL != "wss://panel.example.test/ws" {
		t.Fatalf("settings=%#v", snapshot.Settings)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), token) {
		t.Fatal("snapshot exposed the legacy plaintext server token")
	}
}

func TestReadNodeAgentSettingsSnapshotRejectsLegacyTokenOutsideSupportedBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-invalid-node-settings.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE v2_settings (id INTEGER PRIMARY KEY, name TEXT, value TEXT);
		INSERT INTO v2_settings(name,value) VALUES ('server_token','too-short')`); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	if _, err := ReadNodeAgentSettingsSnapshot(context.Background(), path); err == nil || !strings.Contains(err.Error(), "16-256") {
		t.Fatalf("ReadNodeAgentSettingsSnapshot() error=%v", err)
	}
}
