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
		"active custom theme": `('frontend_theme','Custom'),('current_theme','Custom')`,
		"mismatched active":   `('frontend_theme','Xboard'),('current_theme','Other')`,
		"custom html":         `('theme_xboard','{"theme_color":"default","background_url":"","custom_html":"<script>alert(1)</script>"}')`,
		"remote background":   `('theme_xboard','{"theme_color":"default","background_url":"https://tracker.example/a.png","custom_html":""}')`,
		"unknown config":      `('theme_xboard','{"theme_color":"default","background_url":"","custom_html":"","extra":"silent-loss"}')`,
		"duplicate":           `('frontend_theme','Xboard'),('frontend_theme','Xboard')`,
		"oversized":           `('theme_xboard','` + strings.Repeat("a", 9000) + `')`,
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
