package legacymigration

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReadPublicOriginSettingsSnapshotReadsOnlyFixedKeysAndNormalizes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-public-origin-settings.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('force_https','1'),('subscribe_url',' https://one.example.test/, https://two.example.test/root/ '),('payment_secret','must-not-be-read')`); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	snapshot, err := ReadPublicOriginSettingsSnapshot(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Settings.ForceHTTPS || snapshot.Settings.SubscribeURL != "https://one.example.test,https://two.example.test/root" || snapshot.Checksum == "" || snapshot.SHA256 == "" || snapshot.Size < 1 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestReadPublicOriginSettingsSnapshotDefaultsMissingKeysAndRejectsUnsafeData(t *testing.T) {
	defaultPath := filepath.Join(t.TempDir(), "legacy-public-origin-defaults.db")
	defaultDB, _ := sql.Open("sqlite", "file:"+defaultPath)
	_, _ = defaultDB.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT)`)
	_ = defaultDB.Close()
	defaults, err := ReadPublicOriginSettingsSnapshot(t.Context(), defaultPath)
	if err != nil || defaults.Settings.ForceHTTPS || defaults.Settings.SubscribeURL != "" {
		t.Fatalf("defaults=%#v err=%v", defaults, err)
	}

	for name, rows := range map[string]string{
		"duplicate":    `('force_https','1'),('force_https','0')`,
		"invalid bool": `('force_https','yes')`,
		"insecure URL": `('subscribe_url','http://external.example.test')`,
		"oversized":    `('subscribe_url','https://example.test/` + strings.Repeat("a", 8_192) + `')`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid-public-origin-settings.db")
			database, _ := sql.Open("sqlite", "file:"+path)
			if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT); INSERT INTO v2_settings(name,value) VALUES ` + rows); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()
			if _, err := ReadPublicOriginSettingsSnapshot(t.Context(), path); err == nil {
				t.Fatal("invalid public origin settings snapshot was accepted")
			}
		})
	}
}
