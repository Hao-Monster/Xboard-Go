package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestRunCommandImportsLegacySafeAccessSettingsWithVerifiedBackup(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy-safe-access-settings.db")
	source, err := sql.Open("sqlite", "file:"+sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES ('safe_mode_enable','1')`); err != nil {
		t.Fatal(err)
	}
	_ = source.Close()
	targetPath := filepath.Join(directory, "target.db")
	target, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = target.Close()
	targetSQL, err := sql.Open("sqlite", "file:"+targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := targetSQL.Exec(`UPDATE app_settings SET app_url='https://panel.example.test' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	_ = targetSQL.Close()
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	backupPath := filepath.Join(directory, "pre-safe-access-settings.xbbackup")
	now := time.Date(2026, 8, 30, 13, 15, 0, 0, time.UTC)
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-safe-access-settings", "--source", sourcePath,
		"--source-effective-secure-path", "secure-admin_01",
		"--backup-output", backupPath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || stderr.Len() != 0 {
		t.Fatalf("command handled=%t err=%v stdout=%s stderr=%s", handled, err, stdout.String(), stderr.String())
	}
	var result legacySafeAccessSettingsMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Result.Settings.SourceRows != 1 ||
		result.Result.Settings.SourceChecksum != result.Result.Settings.TargetChecksum || result.RollbackBackup.Path == "" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	inspection, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := inspection.GetSiteSettings(t.Context())
	_ = inspection.Close()
	if err != nil || !settings.SafeModeEnabled || settings.SecurePath != "secure-admin_01" {
		t.Fatalf("migrated safe access settings=%#v err=%v", settings, err)
	}

	stdout.Reset()
	handled, err = runCommand(t.Context(), []string{
		"migration", "import-legacy-safe-access-settings", "--source", sourcePath,
		"--source-effective-secure-path", "secure-admin_01", "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now.Add(time.Hour) })
	if !handled || err != nil || json.Unmarshal(stdout.Bytes(), &result) != nil || !result.Result.AlreadyApplied || !result.Result.AppliedAt.Equal(now) {
		t.Fatalf("idempotent command handled=%t err=%v result=%#v", handled, err, result)
	}
}

func TestRunCommandRequiresOfflineConfirmationAndBackupForNewSafeAccessSettingsImport(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy-safe-access-settings.db")
	source, _ := sql.Open("sqlite", "file:"+sourcePath)
	_, _ = source.Exec(`CREATE TABLE v2_settings(name TEXT,value TEXT); INSERT INTO v2_settings(name,value) VALUES ('secure_path','secure-admin')`)
	_ = source.Close()
	targetPath := filepath.Join(directory, "target.db")
	target, _ := store.OpenSQLite("file:" + targetPath)
	_ = target.Migrate(t.Context())
	_ = target.Close()
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	var stdout, stderr bytes.Buffer
	if handled, err := runCommand(t.Context(), []string{"migration", "import-legacy-safe-access-settings", "--source", sourcePath}, &stdout, &stderr, time.Now); !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") {
		t.Fatalf("offline gate handled=%t err=%v", handled, err)
	}
	if handled, err := runCommand(t.Context(), []string{"migration", "import-legacy-safe-access-settings", "--source", sourcePath, "--confirm-offline"}, &stdout, &stderr, time.Now); !handled || err == nil || !strings.Contains(err.Error(), "backup-output") {
		t.Fatalf("backup gate handled=%t err=%v", handled, err)
	}
}
