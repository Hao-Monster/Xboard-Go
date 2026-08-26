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
	"github.com/Hao-Monster/Xboard-Go/internal/legacymigration"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestRunCommandImportsLegacyCommissionsOfflineWithVerifiedRollback(t *testing.T) {
	directory := t.TempDir()
	sourcePath := createEmptyLegacyCommissionsCLIInput(t, directory)
	snapshot, err := legacymigration.ReadCommissionsSnapshot(t.Context(), sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(directory, "target.db")
	rollbackPath := filepath.Join(directory, "pre-commissions.xbbackup")
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
	recordLegacyCommissionsCLIPrerequisites(t, targetPath, snapshot.SHA256)
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	now := time.Date(2026, 8, 26, 14, 0, 0, 0, time.UTC)

	var blockedOut, blockedErr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-commissions", "--source", sourcePath, "--backup-output", rollbackPath,
	}, &blockedOut, &blockedErr, func() time.Time { return now })
	if !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") || blockedOut.Len() != 0 {
		t.Fatalf("missing confirmation = handled %v error %v stdout=%q", handled, err, blockedOut.String())
	}

	result := runLegacyCommissionsCommand(t, []string{
		"migration", "import-legacy-commissions", "--source", sourcePath,
		"--backup-output", rollbackPath, "--confirm-offline",
	}, now)
	if result.Status != "success" || result.Action != "migration.import-legacy-commissions" || result.Result.AlreadyApplied ||
		result.Result.Logs.SourceRows != 0 || result.Result.Logs.TargetRows != 0 ||
		result.RollbackBackup.Manifest.SchemaVersion != store.CurrentSchemaVersion() {
		t.Fatalf("migration output = %#v", result)
	}
	if verified, err := backup.Verify(t.Context(), rollbackPath); err != nil || verified != result.RollbackBackup.Manifest {
		t.Fatalf("Verify(rollback) = (%#v, %v)", verified, err)
	}
	repeated := runLegacyCommissionsCommand(t, []string{
		"migration", "import-legacy-commissions", "--source", sourcePath, "--confirm-offline",
	}, now.Add(time.Minute))
	if !repeated.Result.AlreadyApplied || repeated.Result.AppliedAt != now || repeated.RollbackBackup.SHA256 != result.RollbackBackup.SHA256 {
		t.Fatalf("idempotent output = %#v", repeated)
	}
}

func runLegacyCommissionsCommand(t *testing.T, arguments []string, now time.Time) legacyCommissionsMigrationCommandResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), arguments, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(%q) = handled %v error %v stderr=%q", arguments, handled, err, stderr.String())
	}
	var output legacyCommissionsMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode migration output %q: %v", stdout.String(), err)
	}
	return output
}

func createEmptyLegacyCommissionsCLIInput(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "legacy-commissions.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_commission_log (
			id INTEGER PRIMARY KEY, invite_user_id INTEGER NOT NULL, user_id INTEGER NOT NULL,
			trade_no TEXT NOT NULL, order_amount INTEGER NOT NULL, get_amount INTEGER NOT NULL,
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
		)
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func recordLegacyCommissionsCLIPrerequisites(t *testing.T, targetPath, sourceSHA256 string) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, slice := range []string{store.LegacyHumanUsersSlice, store.LegacyOrdersSlice} {
		if _, err := database.Exec(`
			INSERT INTO legacy_migration_runs
			(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at)
			VALUES (?, ?, 1, 'prerequisite.xbbackup', ?, '{}', 1)
		`, slice, sourceSHA256, strings.Repeat("0", 64)); err != nil {
			t.Fatal(err)
		}
	}
}
