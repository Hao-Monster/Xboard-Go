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
		"missing path":       `('safe_mode_enable','0')`,
		"duplicate":          `('secure_path','secure-one'),('secure_path','secure-two')`,
		"duplicate fallback": `('frontend_admin_path','secure-one'),('frontend_admin_path','secure-two')`,
		"invalid bool":       `('safe_mode_enable','yes'),('secure_path','secure-admin')`,
		"short path":         `('secure_path','admin')`,
		"reserved":           `('secure_path','passport')`,
		"oversized":          `('secure_path','` + strings.Repeat("a", 65) + `')`,
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

func TestReadSafeAccessSettingsSnapshotLegacyFallbackKeepsEffectiveChecksum(t *testing.T) {
	paths := []string{
		filepath.Join(t.TempDir(), "legacy-safe-fallback.db"),
		filepath.Join(t.TempDir(), "legacy-safe-derived.db"),
	}
	rows := []string{
		`('safe_mode_enable','1'),('frontend_admin_path','legacy-admin_01')`,
		`('safe_mode_enable','1')`,
	}
	checksums := make([]string, 0, 2)
	for index, path := range paths {
		database, _ := sql.Open("sqlite", "file:"+path)
		if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
			INSERT INTO v2_settings(name,value) VALUES ` + rows[index]); err != nil {
			t.Fatal(err)
		}
		_ = database.Close()
		var snapshot SafeAccessSettingsSnapshot
		var err error
		if index == 0 {
			snapshot, err = ReadSafeAccessSettingsSnapshot(t.Context(), path)
		} else {
			snapshot, err = ReadSafeAccessSettingsSnapshot(t.Context(), path, "legacy-admin_01")
		}
		if err != nil {
			t.Fatalf("snapshot %d: %v", index, err)
		}
		checksums = append(checksums, snapshot.Checksum)
	}
	if checksums[0] != checksums[1] {
		t.Fatalf("fallback source changed effective checksum: %q != %q", checksums[0], checksums[1])
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

func TestReadSafeAccessSettingsSnapshotUsesLegacyFrontendAdminPathFallback(t *testing.T) {
	for name, test := range map[string]struct {
		rows string
		want string
	}{
		"fallback only": {
			rows: `('safe_mode_enable','0'),('frontend_admin_path','legacy-admin_01')`,
			want: "legacy-admin_01",
		},
		"secure path wins": {
			rows: `('frontend_admin_path','legacy-admin_01'),('secure_path','current-admin_02')`,
			want: "current-admin_02",
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy-safe-access-fallback.db")
			database, _ := sql.Open("sqlite", "file:"+path)
			if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
				INSERT INTO v2_settings(name,value) VALUES ` + test.rows); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()
			snapshot, err := ReadSafeAccessSettingsSnapshot(t.Context(), path)
			if err != nil || snapshot.Settings.SecurePath != test.want {
				t.Fatalf("snapshot=%#v err=%v want path=%q", snapshot, err, test.want)
			}
			if _, err := ReadSafeAccessSettingsSnapshot(t.Context(), path, "manual-admin"); err == nil {
				t.Fatal("stored effective legacy path and manual override were both accepted")
			}
		})
	}
}

func TestReadSafeAccessSettingsSnapshotRejectsUnsafeLegacyFrontendAdminPath(t *testing.T) {
	for name, value := range map[string]string{
		"empty":    "",
		"short":    "admin",
		"reserved": "passport",
		"unicode":  "管理后台路径",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "unsafe-legacy-admin-path.db")
			database, _ := sql.Open("sqlite", "file:"+path)
			if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
				INSERT INTO v2_settings(name,value) VALUES ('frontend_admin_path',?)`, value); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()
			if _, err := ReadSafeAccessSettingsSnapshot(t.Context(), path); err == nil {
				t.Fatal("unsafe frontend_admin_path was accepted")
			}
		})
	}
}

func BenchmarkReadSafeAccessSettingsSnapshot(b *testing.B) {
	path := filepath.Join(b.TempDir(), "legacy-safe-access-benchmark.db")
	database, _ := sql.Open("sqlite", "file:"+path)
	_, _ = database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('safe_mode_enable','1'),('frontend_admin_path','legacy-admin_01')`)
	_ = database.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := ReadSafeAccessSettingsSnapshot(b.Context(), path); err != nil {
			b.Fatal(err)
		}
	}
}
