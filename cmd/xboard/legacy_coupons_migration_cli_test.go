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

func TestRunCommandImportsLegacyCouponsWithIndependentRollback(t *testing.T) {
	directory := t.TempDir()
	sourcePath := createLegacyCouponsCLIInput(t, directory)
	targetPath := filepath.Join(directory, "target.db")
	rollbackPath := filepath.Join(directory, "pre-coupons.xbbackup")
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
	now := time.Date(2026, 8, 26, 13, 0, 0, 0, time.UTC)

	var blockedOut, blockedErr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{"migration", "import-legacy-coupons", "--source", sourcePath, "--backup-output", rollbackPath}, &blockedOut, &blockedErr, func() time.Time { return now })
	if !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") || blockedOut.Len() != 0 {
		t.Fatalf("missing confirmation = handled %v error %v stdout=%q", handled, err, blockedOut.String())
	}

	result := runLegacyCouponsCommand(t, []string{"migration", "import-legacy-coupons", "--source", sourcePath, "--backup-output", rollbackPath, "--confirm-offline"}, now)
	if result.Status != "success" || result.Action != "migration.import-legacy-coupons" || result.Result.AlreadyApplied ||
		result.Result.Coupons.SourceRows != 1 || result.Result.Settings.SourceChecksum != result.Result.Settings.TargetChecksum ||
		result.RollbackBackup.Manifest.SchemaVersion != store.CurrentSchemaVersion() {
		t.Fatalf("migration output = %#v", result)
	}
	if verified, err := backup.Verify(t.Context(), rollbackPath); err != nil || verified != result.RollbackBackup.Manifest {
		t.Fatalf("Verify(rollback) = (%#v, %v)", verified, err)
	}
	repeated := runLegacyCouponsCommand(t, []string{"migration", "import-legacy-coupons", "--source", sourcePath, "--confirm-offline"}, now.Add(time.Minute))
	if !repeated.Result.AlreadyApplied || repeated.Result.AppliedAt != now || repeated.RollbackBackup.SHA256 != result.RollbackBackup.SHA256 {
		t.Fatalf("idempotent output = %#v", repeated)
	}

	inspection, err := sql.Open("sqlite", "file:"+targetPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	var code string
	var enabled, runs int
	if err := inspection.QueryRow(`SELECT code FROM coupons WHERE id = 11`).Scan(&code); err != nil {
		t.Fatal(err)
	}
	if err := inspection.QueryRow(`SELECT coupon_enabled FROM app_settings WHERE id = 1`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if err := inspection.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = 'coupons-v1'`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if code != "CLI500" || enabled != 0 || runs != 1 {
		t.Fatalf("target code=%q enabled=%d runs=%d", code, enabled, runs)
	}
}

func runLegacyCouponsCommand(t *testing.T, arguments []string, now time.Time) legacyCouponsMigrationCommandResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), arguments, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(%q) = handled %v error %v stderr=%q", arguments, handled, err, stderr.String())
	}
	var output legacyCouponsMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode migration output %q: %v", stdout.String(), err)
	}
	return output
}

func createLegacyCouponsCLIInput(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "legacy-coupons.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_settings (name TEXT, value TEXT);
		INSERT INTO v2_settings VALUES ('app_enable_coupon_system', '0');
		CREATE TABLE v2_coupon (
			id INTEGER PRIMARY KEY, code TEXT, name TEXT, type INTEGER, value INTEGER, show INTEGER,
			limit_use INTEGER, limit_use_with_user INTEGER, limit_plan_ids TEXT, limit_period TEXT,
			started_at INTEGER, ended_at INTEGER, created_at INTEGER, updated_at INTEGER
		);
		INSERT INTO v2_coupon VALUES (11, 'CLI500', 'CLI coupon', 1, 500, 1, 2, 1, NULL, NULL, 1700000000, 1800000000, 1690000000, 1690000010);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
