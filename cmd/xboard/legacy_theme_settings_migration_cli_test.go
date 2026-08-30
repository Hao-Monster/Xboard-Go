package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestRunCommandImportsLegacyThemeSettingsWithVerifiedBackup(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy-theme-settings.db")
	source, _ := sql.Open("sqlite", "file:"+sourcePath)
	_, _ = source.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('frontend_theme','Xboard'),('current_theme','Xboard'),
		('theme_xboard','{"theme_color":"darkblue","background_url":"","custom_html":""}')`)
	_ = source.Close()
	targetPath := filepath.Join(directory, "target.db")
	target, _ := store.OpenSQLite("file:" + targetPath)
	if err := target.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = target.Close()
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	backupPath := filepath.Join(directory, "pre-theme-settings.xbbackup")
	now := time.Date(2026, 8, 30, 13, 30, 0, 0, time.UTC)
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-theme-settings", "--source", sourcePath,
		"--backup-output", backupPath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || stderr.Len() != 0 {
		t.Fatalf("command handled=%t err=%v stdout=%s stderr=%s", handled, err, stdout.String(), stderr.String())
	}
	var result legacyThemeSettingsMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Result.Settings.SourceRows != 1 ||
		result.Result.Settings.SourceChecksum != result.Result.Settings.TargetChecksum || result.RollbackBackup.Path == "" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	inspection, _ := store.OpenSQLite("file:" + targetPath)
	item, err := inspection.GetTheme(t.Context(), "Xboard")
	_ = inspection.Close()
	if err != nil || item.Revision != 2 || item.Config.ThemeColor != "darkblue" {
		t.Fatalf("migrated theme=%#v err=%v", item, err)
	}

	stdout.Reset()
	handled, err = runCommand(t.Context(), []string{
		"migration", "import-legacy-theme-settings", "--source", sourcePath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now.Add(time.Hour) })
	if !handled || err != nil || json.Unmarshal(stdout.Bytes(), &result) != nil || !result.Result.AlreadyApplied {
		t.Fatalf("idempotent command handled=%t err=%v result=%#v", handled, err, result)
	}
}
