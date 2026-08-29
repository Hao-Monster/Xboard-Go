package legacymigration

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReadClientAppSettingsSnapshotReadsFixedKeysAndNormalizesValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-client-app-settings.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('windows_version',' 4.8.1 '),('windows_download_url',' https://download.example.test/windows.exe '),
		('macos_version','4.8.2'),('macos_download_url','https://download.example.test/macos.dmg'),
		('android_version','4.8.3'),('android_download_url','https://download.example.test/android.apk'),
		('unrelated_secret','must-not-be-read')`); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	snapshot, err := ReadClientAppSettingsSnapshot(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Settings.WindowsVersion != "4.8.1" || snapshot.Settings.WindowsDownloadURL != "https://download.example.test/windows.exe" ||
		snapshot.Settings.MacOSVersion != "4.8.2" || snapshot.Settings.AndroidVersion != "4.8.3" ||
		snapshot.Checksum == "" || snapshot.SHA256 == "" || snapshot.Size < 1 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestReadClientAppSettingsSnapshotRejectsDuplicatesUnsafeURLsAndOversizedData(t *testing.T) {
	for name, rows := range map[string]string{
		"duplicate":  `('windows_version','4.8.1'),('windows_version','4.8.2')`,
		"unsafe URL": `('windows_download_url','http://download.example.test/windows.exe')`,
		"oversized":  `('windows_version','` + strings.Repeat("a", 129) + `')`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid-client-app-settings.db")
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT); INSERT INTO v2_settings(name,value) VALUES ` + rows); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()
			if _, err := ReadClientAppSettingsSnapshot(t.Context(), path); err == nil {
				t.Fatal("invalid client app settings snapshot was accepted")
			}
		})
	}
}
