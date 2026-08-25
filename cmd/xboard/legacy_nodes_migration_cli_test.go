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

func TestRunCommandImportsLegacyNodesWithIndependentRollback(t *testing.T) {
	directory := t.TempDir()
	sourcePath := createEmptyLegacyNodesCLIInput(t, directory)
	targetPath := filepath.Join(directory, "target.db")
	rollbackPath := filepath.Join(directory, "pre-nodes.xbbackup")
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
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)

	var blockedOut, blockedErr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{"migration", "import-legacy-nodes", "--source", sourcePath, "--backup-output", rollbackPath}, &blockedOut, &blockedErr, func() time.Time { return now })
	if !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") || blockedOut.Len() != 0 {
		t.Fatalf("missing confirmation = handled %v error %v stdout=%q", handled, err, blockedOut.String())
	}

	result := runLegacyNodesCommand(t, []string{
		"migration", "import-legacy-nodes", "--source", sourcePath,
		"--backup-output", rollbackPath, "--confirm-offline",
	}, now)
	if result.Status != "success" || result.Action != "migration.import-legacy-nodes" || result.Result.AlreadyApplied ||
		result.Result.Machines.SourceRows != 0 || result.Result.Nodes.SourceRows != 0 || result.RollbackBackup.Manifest.SchemaVersion != store.CurrentSchemaVersion() {
		t.Fatalf("migration output = %#v", result)
	}
	if verified, err := backup.Verify(t.Context(), rollbackPath); err != nil || verified != result.RollbackBackup.Manifest {
		t.Fatalf("Verify(rollback) = (%#v, %v)", verified, err)
	}
	repeated := runLegacyNodesCommand(t, []string{"migration", "import-legacy-nodes", "--source", sourcePath, "--confirm-offline"}, now.Add(time.Minute))
	if !repeated.Result.AlreadyApplied || repeated.Result.AppliedAt != now || repeated.RollbackBackup.SHA256 != result.RollbackBackup.SHA256 {
		t.Fatalf("idempotent output = %#v", repeated)
	}

	inspection, err := sql.Open("sqlite", "file:"+targetPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	var version, runs int
	if err := inspection.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := inspection.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = 'nodes-v1'`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	_ = inspection.Close()
	if version != store.CurrentSchemaVersion() || runs != 1 {
		t.Fatalf("target version=%d runs=%d", version, runs)
	}
}

func runLegacyNodesCommand(t *testing.T, arguments []string, now time.Time) legacyNodesMigrationCommandResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), arguments, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(%q) = handled %v error %v stderr=%q", arguments, handled, err, stderr.String())
	}
	var output legacyNodesMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode migration output %q: %v", stdout.String(), err)
	}
	return output
}

func createEmptyLegacyNodesCLIInput(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "legacy-nodes.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_server_machine (id INTEGER PRIMARY KEY,name TEXT,token TEXT,notes TEXT,is_active INTEGER,last_seen_at INTEGER,load_status TEXT,created_at DATETIME,updated_at DATETIME);
		CREATE TABLE v2_server_machine_credential (id INTEGER PRIMARY KEY,machine_id INTEGER,token_hash TEXT,token_prefix TEXT,last_used_at INTEGER,revoked_at INTEGER,created_at DATETIME);
		CREATE TABLE v2_server_machine_enrollment (id INTEGER PRIMARY KEY,machine_id INTEGER,code_hash TEXT,revoke_existing INTEGER,expires_at INTEGER,consumed_at INTEGER,created_at DATETIME);
		CREATE TABLE v2_server_machine_load_history (id INTEGER PRIMARY KEY,machine_id INTEGER,cpu REAL,mem_total INTEGER,mem_used INTEGER,disk_total INTEGER,disk_used INTEGER,net_in_speed REAL,net_out_speed REAL,recorded_at INTEGER);
		CREATE TABLE v2_server (id INTEGER PRIMARY KEY,type TEXT,code TEXT,parent_id INTEGER,group_ids TEXT,route_ids TEXT,name TEXT,rate NUMERIC,tags TEXT,host TEXT,port TEXT,server_port INTEGER,protocol_settings TEXT,show INTEGER,sort INTEGER,created_at DATETIME,updated_at DATETIME,rate_time_enable INTEGER,rate_time_ranges TEXT,custom_outbounds TEXT,custom_routes TEXT,cert_config TEXT,transfer_enable INTEGER,u INTEGER,d INTEGER,machine_id INTEGER,enabled INTEGER);
		CREATE TABLE v2_server_activation_schedule (id INTEGER PRIMARY KEY,server_id INTEGER,schedule_type TEXT,timezone TEXT,enable_second INTEGER,disable_second INTEGER,enable_at INTEGER,disable_at INTEGER,revision TEXT,next_transition_at INTEGER,next_target_enabled INTEGER,enabled_applied_at INTEGER,disabled_applied_at INTEGER,created_at DATETIME,updated_at DATETIME);
		CREATE TABLE v2_stat_server (id INTEGER PRIMARY KEY,server_id INTEGER,server_type TEXT,u INTEGER,d INTEGER,record_type TEXT,record_at INTEGER,created_at INTEGER,updated_at INTEGER);
		CREATE TABLE v2_server_report_receipt (id INTEGER PRIMARY KEY);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
