package legacymigration

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReadSafeAccessSettingsSnapshotReadsOnlyFixedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-safe-access-settings.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('safe_mode_enable','true'),('secure_path',' secure-admin_01 '),('payment_secret','must-not-be-read')`); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	snapshot, err := ReadSafeAccessSettingsSnapshot(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Settings.SafeModeEnabled || snapshot.Settings.SecurePath != "secure-admin_01" || snapshot.Checksum == "" || snapshot.SHA256 == "" || snapshot.Size < 1 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if _, err := ReadSafeAccessSettingsSnapshot(t.Context(), path, "different-admin"); err == nil {
		t.Fatal("stored secure path and effective override were both accepted")
	}
}

func TestReadSafeAccessSettingsSnapshotRejectsMissingDuplicateAndUnsafeData(t *testing.T) {
	for name, rows := range map[string]string{
		"missing path": `('safe_mode_enable','0')`,
		"duplicate":    `('secure_path','secure-one'),('secure_path','secure-two')`,
		"invalid bool": `('safe_mode_enable','yes'),('secure_path','secure-admin')`,
		"short path":   `('secure_path','admin')`,
		"reserved":     `('secure_path','passport')`,
		"oversized":    `('secure_path','` + strings.Repeat("a", 65) + `')`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid-safe-access-settings.db")
			database, _ := sql.Open("sqlite", "file:"+path)
			if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT); INSERT INTO v2_settings(name,value) VALUES ` + rows); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()
			if _, err := ReadSafeAccessSettingsSnapshot(t.Context(), path); err == nil {
				t.Fatal("invalid safe access settings snapshot was accepted")
			}
		})
	}
}

func TestReadSafeAccessSettingsSnapshotRequiresExplicitEffectivePathWhenLegacyRowIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-safe-access-default.db")
	database, _ := sql.Open("sqlite", "file:"+path)
	_, _ = database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT); INSERT INTO v2_settings(name,value) VALUES ('safe_mode_enable','0')`)
	_ = database.Close()
	if _, err := ReadSafeAccessSettingsSnapshot(t.Context(), path); err == nil {
		t.Fatal("missing effective legacy secure path was accepted")
	}
	snapshot, err := ReadSafeAccessSettingsSnapshot(t.Context(), path, "crc32-path")
	if err != nil || snapshot.Settings.SecurePath != "crc32-path" {
		t.Fatalf("explicit effective path snapshot=%#v err=%v", snapshot, err)
	}
	if _, err := ReadSafeAccessSettingsSnapshot(t.Context(), path, "short"); err == nil {
		t.Fatal("unsafe effective legacy secure path was accepted")
	}
}
