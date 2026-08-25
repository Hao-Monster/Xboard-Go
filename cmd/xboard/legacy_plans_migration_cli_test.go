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

func TestRunCommandImportsLegacyPlansWithIndependentRollback(t *testing.T) {
	directory := t.TempDir()
	sourcePath := createLegacyPlansCLIInput(t, directory)
	targetPath := filepath.Join(directory, "target.db")
	rollbackPath := filepath.Join(directory, "pre-plans.xbbackup")
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
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)

	var blockedOut, blockedErr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-plans", "--source", sourcePath, "--backup-output", rollbackPath,
	}, &blockedOut, &blockedErr, func() time.Time { return now })
	if !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") || blockedOut.Len() != 0 {
		t.Fatalf("missing confirmation = handled %v error %v stdout=%q", handled, err, blockedOut.String())
	}

	result := runLegacyPlansCommand(t, []string{
		"migration", "import-legacy-plans", "--source", sourcePath,
		"--backup-output", rollbackPath, "--confirm-offline",
	}, now)
	if result.Status != "success" || result.Action != "migration.import-legacy-plans" || result.Result.AlreadyApplied ||
		result.Result.Plans.SourceRows != 1 || result.Result.Plans.TargetRows != 1 ||
		result.RollbackBackup.Manifest.SchemaVersion != store.CurrentSchemaVersion() {
		t.Fatalf("migration output = %#v", result)
	}
	if verified, err := backup.Verify(t.Context(), rollbackPath); err != nil || verified != result.RollbackBackup.Manifest {
		t.Fatalf("Verify(rollback) = (%#v, %v)", verified, err)
	}
	repeated := runLegacyPlansCommand(t, []string{
		"migration", "import-legacy-plans", "--source", sourcePath, "--confirm-offline",
	}, now.Add(time.Minute))
	if !repeated.Result.AlreadyApplied || repeated.Result.AppliedAt != now || repeated.RollbackBackup.SHA256 != result.RollbackBackup.SHA256 {
		t.Fatalf("idempotent output = %#v", repeated)
	}

	inspection, err := sql.Open("sqlite", "file:"+targetPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	var priceJSON string
	var transfer, runs, resetMethod int64
	if err := inspection.QueryRow(`SELECT transfer_enable_gib, prices_json FROM plans WHERE id = 11`).Scan(&transfer, &priceJSON); err != nil {
		t.Fatal(err)
	}
	if err := inspection.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = 'plans-v1'`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := inspection.QueryRow(`SELECT traffic_reset_method FROM app_settings WHERE id = 1`).Scan(&resetMethod); err != nil {
		t.Fatal(err)
	}
	if transfer != 128 || priceJSON != `{"monthly":999}` || runs != 1 || resetMethod != 4 {
		t.Fatalf("target transfer=%d prices=%q runs=%d reset_method=%d", transfer, priceJSON, runs, resetMethod)
	}
}

func runLegacyPlansCommand(t *testing.T, arguments []string, now time.Time) legacyPlansMigrationCommandResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), arguments, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(%q) = handled %v error %v stderr=%q", arguments, handled, err, stderr.String())
	}
	var output legacyPlansMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode migration output %q: %v", stdout.String(), err)
	}
	return output
}

func createLegacyPlansCLIInput(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "legacy-plans.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_settings (name TEXT, value TEXT);
		INSERT INTO v2_settings (name, value) VALUES ('reset_traffic_method', '4');
		CREATE TABLE v2_plan (
			id INTEGER PRIMARY KEY, group_id INTEGER, transfer_enable INTEGER, name TEXT,
			speed_limit INTEGER, show INTEGER, sort INTEGER, renew INTEGER, content TEXT,
			reset_traffic_method INTEGER, capacity_limit INTEGER, created_at, updated_at,
			prices TEXT, sell INTEGER, device_limit INTEGER, tags TEXT
		);
		INSERT INTO v2_plan VALUES (
			11, NULL, 128, 'CLI plan', NULL, 1, 0, 1, 'content', NULL, NULL,
			1700000000, 1700000010, '{"monthly":9.99}', 1, NULL, '["popular"]'
		);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
