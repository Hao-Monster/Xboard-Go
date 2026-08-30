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

func TestRunCommandImportsLegacyConfigurationCompatibilitySettingsWithVerifiedBackup(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy-configuration-compatibility.db")
	source, _ := sql.Open("sqlite", "file:"+sourcePath)
	_, _ = source.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('commission_withdraw_limit','250.50'),('commission_withdraw_method','["USDT"]'),
		('frontend_theme_sidebar','dark'),('frontend_theme_header','light')`)
	_ = source.Close()
	targetPath := filepath.Join(directory, "target.db")
	target, _ := store.OpenSQLite("file:" + targetPath)
	if err := target.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = target.Close()
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	backupPath := filepath.Join(directory, "pre-configuration-compatibility.xbbackup")
	now := time.Date(2026, 8, 30, 15, 30, 0, 0, time.UTC)
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-configuration-compat-settings", "--source", sourcePath,
		"--backup-output", backupPath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || stderr.Len() != 0 {
		t.Fatalf("command handled=%t err=%v stdout=%s stderr=%s", handled, err, stdout.String(), stderr.String())
	}
	var result legacyConfigurationCompatibilitySettingsMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Result.Settings.SourceRows != 1 ||
		result.Result.Settings.SourceChecksum != result.Result.Settings.TargetChecksum || result.RollbackBackup.Path == "" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	inspection, _ := store.OpenSQLite("file:" + targetPath)
	invite, inviteErr := inspection.GetLegacyInvitationSettings(t.Context())
	frontend, frontendErr := inspection.GetLegacyFrontendSettings(t.Context())
	_ = inspection.Close()
	if inviteErr != nil || frontendErr != nil || invite.WithdrawLimit != 25_050 || strings.Join(invite.WithdrawMethods, ",") != "USDT" ||
		frontend.SidebarStyle != "dark" || frontend.HeaderStyle != "light" {
		t.Fatalf("migrated invite=%#v frontend=%#v inviteErr=%v frontendErr=%v", invite, frontend, inviteErr, frontendErr)
	}

	stdout.Reset()
	handled, err = runCommand(t.Context(), []string{
		"migration", "import-legacy-configuration-compat-settings", "--source", sourcePath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now.Add(time.Hour) })
	if !handled || err != nil || json.Unmarshal(stdout.Bytes(), &result) != nil || !result.Result.AlreadyApplied {
		t.Fatalf("idempotent command handled=%t err=%v result=%#v", handled, err, result)
	}
}
