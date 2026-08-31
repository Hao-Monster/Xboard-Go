package legacymigration

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReadThemeSettingsSnapshotReadsDefaultAndSafeXboardConfig(t *testing.T) {
	for name, rows := range map[string]string{
		"missing defaults": "",
		"explicit safe": `INSERT INTO v2_settings(name,value) VALUES
			('frontend_theme','Xboard'),('current_theme','Xboard'),
			('theme_xboard','{"theme_color":"blue","background_url":"","custom_html":""}');`,
		"legacy frontend fallback": `INSERT INTO v2_settings(name,value) VALUES
			('frontend_theme_color','black'),('frontend_background_url','');`,
		"matching legacy frontend": `INSERT INTO v2_settings(name,value) VALUES
			('theme_xboard','{"theme_color":"darkblue","background_url":"","custom_html":""}'),
			('frontend_theme_color','darkblue'),('frontend_background_url','');`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy-theme.db")
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);` + rows); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()
			snapshot, err := ReadThemeSettingsSnapshot(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			wantColor := "default"
			if name == "explicit safe" {
				wantColor = "blue"
			} else if name == "legacy frontend fallback" {
				wantColor = "black"
			} else if name == "matching legacy frontend" {
				wantColor = "darkblue"
			}
			if snapshot.Settings.ActiveTheme != "Xboard" || snapshot.Settings.Config.ThemeColor != wantColor ||
				snapshot.Settings.Config.BackgroundURL != "" || snapshot.Checksum == "" || snapshot.SHA256 == "" {
				t.Fatalf("snapshot=%#v", snapshot)
			}
		})
	}
}

func TestReadThemeSettingsSnapshotRejectsExecutableOrAmbiguousLegacyThemes(t *testing.T) {
	for name, rows := range map[string]string{
		"active custom theme":        `('frontend_theme','Custom'),('current_theme','Custom')`,
		"mismatched active":          `('frontend_theme','Xboard'),('current_theme','Other')`,
		"mismatched colors":          `('theme_xboard','{"theme_color":"blue","background_url":"","custom_html":""}'),('frontend_theme_color','black')`,
		"custom html":                `('theme_xboard','{"theme_color":"default","background_url":"","custom_html":"<script>alert(1)</script>"}')`,
		"remote background":          `('theme_xboard','{"theme_color":"default","background_url":"https://tracker.example/a.png","custom_html":""}')`,
		"frontend remote background": `('frontend_background_url','https://tracker.example/admin.png')`,
		"unsupported frontend color": `('frontend_theme_color','green')`,
		"unknown config":             `('theme_xboard','{"theme_color":"default","background_url":"","custom_html":"","extra":"silent-loss"}')`,
		"duplicate":                  `('frontend_theme','Xboard'),('frontend_theme','Xboard')`,
		"duplicate frontend":         `('frontend_theme_color','black'),('frontend_theme_color','black')`,
		"oversized":                  `('theme_xboard','` + strings.Repeat("a", 9000) + `')`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "unsafe-legacy-theme.db")
			database, _ := sql.Open("sqlite", "file:"+path)
			_, _ = database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
				INSERT INTO v2_settings(name,value) VALUES ` + rows)
			_ = database.Close()
			if _, err := ReadThemeSettingsSnapshot(t.Context(), path); err == nil {
				t.Fatal("unsafe legacy theme settings were accepted")
			}
		})
	}
}

func TestReadThemeSettingsSnapshotMatchingLegacyFrontendKeysKeepEffectiveChecksum(t *testing.T) {
	checksums := make([]string, 0, 2)
	for index, extraRows := range []string{
		"",
		`,('frontend_theme_color','blue'),('frontend_background_url','')`,
	} {
		path := filepath.Join(t.TempDir(), "legacy-theme-checksum.db")
		database, _ := sql.Open("sqlite", "file:"+path)
		if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
			INSERT INTO v2_settings(name,value) VALUES
			('theme_xboard','{"theme_color":"blue","background_url":"","custom_html":""}')` + extraRows); err != nil {
			t.Fatal(err)
		}
		_ = database.Close()
		snapshot, err := ReadThemeSettingsSnapshot(t.Context(), path)
		if err != nil {
			t.Fatalf("snapshot %d: %v", index, err)
		}
		checksums = append(checksums, snapshot.Checksum)
	}
	if checksums[0] != checksums[1] {
		t.Fatalf("matching compatibility keys changed effective checksum: %q != %q", checksums[0], checksums[1])
	}
}

func BenchmarkReadThemeSettingsSnapshot(b *testing.B) {
	path := filepath.Join(b.TempDir(), "legacy-theme-benchmark.db")
	database, _ := sql.Open("sqlite", "file:"+path)
	_, _ = database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('frontend_theme','Xboard'),('current_theme','Xboard'),
		('theme_xboard','{"theme_color":"darkblue","background_url":"","custom_html":""}'),
		('frontend_theme_color','darkblue'),('frontend_background_url','')`)
	_ = database.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := ReadThemeSettingsSnapshot(b.Context(), path); err != nil {
			b.Fatal(err)
		}
	}
}
