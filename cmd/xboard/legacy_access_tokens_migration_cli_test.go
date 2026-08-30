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
	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestRunCommandImportsLegacyAccessTokensOfflineWithoutExposingHashes(t *testing.T) {
	directory := t.TempDir()
	sourcePath := createLegacyAccessTokensCLIInput(t, directory)
	snapshot, err := legacymigration.ReadAccessTokensSnapshot(t.Context(), sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(directory, "target.db")
	rollbackPath := filepath.Join(directory, "pre-access-tokens.xbbackup")
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
	recordLegacyAccessTokensCLIPrerequisite(t, targetPath, snapshot.SHA256)
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	now := time.Date(2026, 8, 31, 4, 0, 0, 0, time.UTC)

	var blockedOut, blockedErr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-access-tokens", "--source", sourcePath, "--backup-output", rollbackPath,
	}, &blockedOut, &blockedErr, func() time.Time { return now })
	if !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") || blockedOut.Len() != 0 {
		t.Fatalf("missing confirmation = handled %v error %v stdout=%q", handled, err, blockedOut.String())
	}

	result, raw := runLegacyAccessTokensCommand(t, []string{
		"migration", "import-legacy-access-tokens", "--source", sourcePath,
		"--backup-output", rollbackPath, "--confirm-offline",
	}, now)
	if result.Status != "success" || result.Action != "migration.import-legacy-access-tokens" || result.Result.AlreadyApplied ||
		result.Result.Tokens.SourceRows != 1 || result.Result.Tokens.TargetRows != 1 ||
		result.RollbackBackup.Manifest.SchemaVersion != store.CurrentSchemaVersion() {
		t.Fatalf("migration output = %#v", result)
	}
	if strings.Contains(raw, security.DigestToken("legacy-cli-bearer")) {
		t.Fatal("migration output exposed an access token hash")
	}
	if verified, err := backup.Verify(t.Context(), rollbackPath); err != nil || verified != result.RollbackBackup.Manifest {
		t.Fatalf("Verify(rollback) = (%#v, %v)", verified, err)
	}
	imported, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	session, authenticateErr := imported.AuthenticateAccessToken(t.Context(), security.DigestToken("legacy-cli-bearer"), now.Add(time.Minute))
	closeErr := imported.Close()
	if authenticateErr != nil || closeErr != nil || session.UserID != 11 || session.SessionID != 7 {
		t.Fatalf("AuthenticateAccessToken(imported CLI token) = (%#v, %v), close = %v", session, authenticateErr, closeErr)
	}
	repeated, _ := runLegacyAccessTokensCommand(t, []string{
		"migration", "import-legacy-access-tokens", "--source", sourcePath, "--confirm-offline",
	}, now.Add(time.Minute))
	if !repeated.Result.AlreadyApplied || repeated.Result.AppliedAt != now || repeated.RollbackBackup.SHA256 != result.RollbackBackup.SHA256 {
		t.Fatalf("idempotent output = %#v", repeated)
	}
	var mismatchOut, mismatchErr bytes.Buffer
	handled, err = runCommand(t.Context(), []string{
		"migration", "import-legacy-access-tokens", "--source", sourcePath,
		"--backup-output", filepath.Join(directory, "different.xbbackup"), "--confirm-offline",
	}, &mismatchOut, &mismatchErr, func() time.Time { return now.Add(2 * time.Minute) })
	if !handled || err == nil || !strings.Contains(err.Error(), "does not match") || mismatchOut.Len() != 0 {
		t.Fatalf("mismatched rollback path = handled %v error %v stdout=%q", handled, err, mismatchOut.String())
	}
}

func runLegacyAccessTokensCommand(t *testing.T, arguments []string, now time.Time) (legacyAccessTokensMigrationCommandResult, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), arguments, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(%q) = handled %v error %v stderr=%q", arguments, handled, err, stderr.String())
	}
	var output legacyAccessTokensMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode migration output %q: %v", stdout.String(), err)
	}
	return output, stdout.String()
}

func createLegacyAccessTokensCLIInput(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "legacy-access-tokens.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE personal_access_tokens (
			id INTEGER PRIMARY KEY, tokenable_type TEXT NOT NULL, tokenable_id INTEGER NOT NULL,
			name TEXT NOT NULL, token TEXT NOT NULL, abilities TEXT, last_used_at DATETIME,
			expires_at DATETIME, created_at DATETIME, updated_at DATETIME
		);
		INSERT INTO personal_access_tokens VALUES
			(7, 'App\Models\User', 11, 'legacy-cli-device', ?, '["*"]', NULL, NULL, 1700000000, 1700000000)
	`, security.DigestToken("legacy-cli-bearer")); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func recordLegacyAccessTokensCLIPrerequisite(t *testing.T, targetPath, sourceSHA256 string) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
		INSERT INTO users (id,email,password_hash,account_kind,subscription_token,created_at,updated_at)
		VALUES (11,'legacy-cli@example.test','hash','human',?,1700000000,1700000000)
	`, strings.Repeat("1", 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO legacy_migration_runs
		(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at)
		VALUES (?, ?, 1, 'prerequisite.xbbackup', ?, '{}', 1)
	`, store.LegacyHumanUsersSlice, sourceSHA256, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
}
