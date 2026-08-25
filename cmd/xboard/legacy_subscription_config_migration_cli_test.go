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

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestRunCommandImportsLegacySubscriptionConfigWithIndependentRollback(t *testing.T) {
	directory := t.TempDir()
	sourcePath := createLegacySubscriptionConfigCLIInput(t, directory)
	targetPath := filepath.Join(directory, "target.db")
	rollbackPath := filepath.Join(directory, "pre-subscription-config.xbbackup")
	target, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Migrate(t.Context()); err != nil {
		_ = target.Close()
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	now := time.Date(2026, 8, 26, 10, 30, 0, 0, time.UTC)

	var blockedOut, blockedErr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-subscription-config", "--source", sourcePath, "--backup-output", rollbackPath,
	}, &blockedOut, &blockedErr, func() time.Time { return now })
	if !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") || blockedOut.Len() != 0 {
		t.Fatalf("missing confirmation = handled %v error %v stdout=%q", handled, err, blockedOut.String())
	}

	result := runLegacySubscriptionConfigCommand(t, []string{
		"migration", "import-legacy-subscription-config", "--source", sourcePath,
		"--backup-output", rollbackPath, "--confirm-offline",
	}, now)
	if result.Status != "success" || result.Action != "migration.import-legacy-subscription-config" || result.Result.AlreadyApplied ||
		result.Result.Config.SourceChecksum != result.Result.Config.TargetChecksum ||
		result.RollbackBackup.Manifest.SchemaVersion != store.CurrentSchemaVersion() {
		t.Fatalf("migration output = %#v", result)
	}
	if verified, err := backup.Verify(t.Context(), rollbackPath); err != nil || verified != result.RollbackBackup.Manifest {
		t.Fatalf("Verify(rollback) = (%#v, %v)", verified, err)
	}
	repeated := runLegacySubscriptionConfigCommand(t, []string{
		"migration", "import-legacy-subscription-config", "--source", sourcePath, "--confirm-offline",
	}, now.Add(time.Minute))
	if !repeated.Result.AlreadyApplied || repeated.Result.AppliedAt != now || repeated.RollbackBackup.SHA256 != result.RollbackBackup.SHA256 {
		t.Fatalf("idempotent output = %#v", repeated)
	}

	inspection, err := sql.Open("sqlite", "file:"+targetPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	var path, clash string
	var showInfo, showProtocol, revision, runs int
	if err := inspection.QueryRow(`SELECT path,show_info,show_protocol,revision FROM subscription_settings WHERE id=1`).Scan(&path, &showInfo, &showProtocol, &revision); err != nil {
		t.Fatal(err)
	}
	if err := inspection.QueryRow(`SELECT content FROM subscription_templates WHERE name='clash'`).Scan(&clash); err != nil {
		t.Fatal(err)
	}
	if err := inspection.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice='subscription-config-v1'`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if path != "legacy_feed" || showInfo != 1 || showProtocol != 0 || revision != 2 || clash == "" || runs != 1 {
		t.Fatalf("target path=%q info=%d protocol=%d revision=%d clash=%q runs=%d", path, showInfo, showProtocol, revision, clash, runs)
	}
}

func runLegacySubscriptionConfigCommand(t *testing.T, arguments []string, now time.Time) legacySubscriptionConfigMigrationCommandResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), arguments, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(%q) = handled %v error %v stderr=%q", arguments, handled, err, stderr.String())
	}
	var output legacySubscriptionConfigMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode migration output %q: %v", stdout.String(), err)
	}
	return output
}

func createLegacySubscriptionConfigCLIInput(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "legacy-subscription-config.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_settings (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, value TEXT);
		INSERT INTO v2_settings (name,value) VALUES
			('subscribe_path','legacy_feed'),('show_info_to_server_enable','1'),('show_protocol_to_server_enable','0');
		CREATE TABLE v2_subscribe_templates (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, content TEXT);
		INSERT INTO v2_subscribe_templates (name,content) VALUES
			('singbox',''),('clash','{proxies: [], proxy-groups: [], rules: []}'),('clashmeta',''),
			('stash',''),('surge',''),('surfboard','');
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
