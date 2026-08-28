package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestRunCommandImportsLegacyNodeAgentSettingsWithoutExposingToken(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy-node-agent-settings.db")
	token := "Q7vP2kLm-node-agent-token-1234567890"
	source, err := sql.Open("sqlite", "file:"+sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('server_token',?),('server_pull_interval','31'),('server_push_interval','29'),
		('device_limit_mode','1'),('server_ws_enable','1'),('server_ws_url','wss://panel.example.test/ws')`, token); err != nil {
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
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	backupPath := filepath.Join(directory, "pre-node-agent-settings.xbbackup")
	now := time.Date(2026, 8, 28, 13, 0, 0, 0, time.UTC)

	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), []string{
		"migration", "import-legacy-node-agent-settings", "--source", sourcePath,
		"--backup-output", backupPath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || stderr.Len() != 0 {
		t.Fatalf("runCommand() handled=%t err=%v stderr=%q", handled, err, stderr.String())
	}
	if strings.Contains(stdout.String(), token) || strings.Contains(stdout.String(), security.DigestToken(token)) || strings.Contains(stdout.String(), token[:8]) {
		t.Fatal("migration output exposed legacy credential material")
	}
	var result legacyNodeAgentSettingsMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.Result.AlreadyApplied || result.Result.Settings.SourceChecksum != result.Result.Settings.TargetChecksum {
		t.Fatalf("result=%#v", result)
	}
	inspection, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	valid, err := inspection.AuthenticateLegacyNodeToken(t.Context(), token)
	if err != nil || !valid {
		t.Fatalf("migrated token auth=(%t,%v)", valid, err)
	}

	stdout.Reset()
	handled, err = runCommand(context.Background(), []string{
		"migration", "import-legacy-node-agent-settings", "--source", sourcePath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now.Add(time.Minute) })
	if !handled || err != nil {
		t.Fatalf("idempotent run handled=%t err=%v", handled, err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || !result.Result.AlreadyApplied || result.Result.AppliedAt != now {
		t.Fatalf("idempotent result=%#v err=%v", result, err)
	}
}
