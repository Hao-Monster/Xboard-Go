package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
	"github.com/Hao-Monster/Xboard-Go/internal/legacymigration"
	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestRunCommandImportsLegacyInvitationCodesEncryptedAndIdempotent(t *testing.T) {
	directory := t.TempDir()
	sourcePath := createLegacyInvitationCodesCLIInput(t, directory)
	snapshot, err := legacymigration.ReadInvitationCodesSnapshot(t.Context(), sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.ClearSecrets()
	targetPath := filepath.Join(directory, "target.db")
	rollbackPath := filepath.Join(directory, "pre-invitation-codes.xbbackup")
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
	recordLegacyInvitationCodesCLIPrerequisite(t, targetPath, snapshot.SHA256)
	key := bytes.Repeat([]byte{0x72}, 32)
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	t.Setenv("XBOARD_SETTINGS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("XBOARD_SETTINGS_ENCRYPTION_KEY_FILE", "")
	now := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)

	var blockedOut, blockedErr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-invitation-codes", "--source", sourcePath, "--backup-output", rollbackPath,
	}, &blockedOut, &blockedErr, func() time.Time { return now })
	if !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") || blockedOut.Len() != 0 {
		t.Fatalf("missing confirmation = handled %v error %v stdout=%q", handled, err, blockedOut.String())
	}

	result, raw := runLegacyInvitationCodesCommand(t, []string{
		"migration", "import-legacy-invitation-codes", "--source", sourcePath,
		"--backup-output", rollbackPath, "--confirm-offline",
	}, now)
	if result.Status != "success" || result.Action != "migration.import-legacy-invitation-codes" || result.Result.AlreadyApplied ||
		result.Result.Codes.SourceRows != 2 || result.Result.Codes.SourceChecksum != result.Result.Codes.TargetChecksum ||
		result.RollbackBackup.Manifest.SchemaVersion != store.CurrentSchemaVersion() {
		t.Fatalf("migration output = %#v", result)
	}
	for _, code := range []string{"CliA1234", "CliB5678"} {
		if strings.Contains(raw, code) {
			t.Fatalf("migration output exposed invitation code %q: %s", code, raw)
		}
	}
	if verified, err := backup.Verify(t.Context(), rollbackPath); err != nil || verified != result.RollbackBackup.Manifest {
		t.Fatalf("Verify(rollback) = (%#v, %v)", verified, err)
	}
	protector, _ := security.NewInvitationProtector(key)
	inspection, err := sql.Open("sqlite", "file:"+targetPath)
	if err != nil {
		t.Fatal(err)
	}
	var ownerID int64
	var cipher []byte
	var consumedAt sql.NullInt64
	if err := inspection.QueryRow(`SELECT user_id,code_cipher,consumed_at FROM invitation_codes WHERE id=7`).Scan(&ownerID, &cipher, &consumedAt); err != nil {
		_ = inspection.Close()
		t.Fatal(err)
	}
	plaintext, err := protector.DecryptCode(ownerID, cipher)
	if err != nil || string(plaintext) != "CliA1234" || consumedAt.Valid {
		t.Fatalf("imported active invitation = owner %d plaintext %q consumed %#v error %v", ownerID, plaintext, consumedAt, err)
	}
	for index := range plaintext {
		plaintext[index] = 0
	}
	_ = inspection.Close()
	repeated, _ := runLegacyInvitationCodesCommand(t, []string{
		"migration", "import-legacy-invitation-codes", "--source", sourcePath, "--confirm-offline",
	}, now.Add(time.Minute))
	if !repeated.Result.AlreadyApplied || repeated.Result.AppliedAt != now || repeated.RollbackBackup.SHA256 != result.RollbackBackup.SHA256 {
		t.Fatalf("idempotent output = %#v", repeated)
	}
	t.Setenv("XBOARD_SETTINGS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x31}, 32)))
	var wrongKeyOut, wrongKeyErr bytes.Buffer
	handled, err = runCommand(t.Context(), []string{
		"migration", "import-legacy-invitation-codes", "--source", sourcePath, "--confirm-offline",
	}, &wrongKeyOut, &wrongKeyErr, func() time.Time { return now.Add(2 * time.Minute) })
	if !handled || err == nil || !strings.Contains(err.Error(), "cannot decrypt") || wrongKeyOut.Len() != 0 ||
		strings.Contains(err.Error(), "CliA1234") || strings.Contains(err.Error(), "CliB5678") {
		t.Fatalf("wrong key = handled %v error %v stdout=%q", handled, err, wrongKeyOut.String())
	}
}

func TestRunCommandRejectsLegacyInvitationCodesWithoutKeyBeforeBackup(t *testing.T) {
	directory := t.TempDir()
	sourcePath := createLegacyInvitationCodesCLIInput(t, directory)
	targetPath := filepath.Join(directory, "target.db")
	rollbackPath := filepath.Join(directory, "must-not-exist.xbbackup")
	target, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Migrate(t.Context()); err != nil {
		_ = target.Close()
		t.Fatal(err)
	}
	_ = target.Close()
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	t.Setenv("XBOARD_SETTINGS_ENCRYPTION_KEY", "")
	t.Setenv("XBOARD_SETTINGS_ENCRYPTION_KEY_FILE", "")
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-invitation-codes", "--source", sourcePath,
		"--backup-output", rollbackPath, "--confirm-offline",
	}, &stdout, &stderr, time.Now)
	if !handled || err == nil || !strings.Contains(err.Error(), "XBOARD_SETTINGS_ENCRYPTION_KEY") || stdout.Len() != 0 {
		t.Fatalf("missing key = handled %v error %v stdout=%q", handled, err, stdout.String())
	}
	if _, statErr := os.Stat(rollbackPath); !os.IsNotExist(statErr) {
		t.Fatalf("backup was created before encryption validation: %v", statErr)
	}
}

func runLegacyInvitationCodesCommand(t *testing.T, arguments []string, now time.Time) (legacyInvitationCodesMigrationCommandResult, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), arguments, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(%q) = handled %v error %v stderr=%q", arguments, handled, err, stderr.String())
	}
	var output legacyInvitationCodesMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode migration output %q: %v", stdout.String(), err)
	}
	return output, stdout.String()
}

func createLegacyInvitationCodesCLIInput(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "legacy-invitation-codes.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_invite_code (
			id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL,code VARCHAR NOT NULL,
			status INTEGER NOT NULL DEFAULT 0,pv INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL
		);
		INSERT INTO v2_invite_code VALUES
			(7,11,'CliA1234',0,2,1700000000,1700000100),
			(9,11,'CliB5678',1,4,1700000200,1700000300);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func recordLegacyInvitationCodesCLIPrerequisite(t *testing.T, targetPath, sourceSHA256 string) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
		INSERT INTO users (id,email,password_hash,account_kind,subscription_token,created_at,updated_at)
		VALUES (11,'legacy-inviter-cli@example.test','hash','human',?,1700000000,1700000000)
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
