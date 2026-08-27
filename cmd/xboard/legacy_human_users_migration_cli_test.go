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
	"golang.org/x/crypto/bcrypt"
)

func TestRunCommandImportsLegacyHumanUsersWithExplicitReplacementAndIndependentRollback(t *testing.T) {
	directory := t.TempDir()
	sourcePath, legacyHash := createLegacyHumanUsersCLIInput(t, directory)
	targetPath := filepath.Join(directory, "target.db")
	rollbackPath := filepath.Join(directory, "pre-human-users.xbbackup")
	target, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := target.BootstrapAdmin(t.Context(), "bootstrap@example.test", "bootstrap-hash", time.Unix(50, 0)); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	now := time.Date(2026, 8, 25, 23, 0, 0, 0, time.UTC)

	for _, missingFlagArguments := range [][]string{
		{"migration", "import-legacy-human-users", "--source", sourcePath, "--backup-output", rollbackPath, "--replace-bootstrap-admin"},
		{"migration", "import-legacy-human-users", "--source", sourcePath, "--backup-output", rollbackPath, "--confirm-offline"},
	} {
		var stdout, stderr bytes.Buffer
		handled, err := runCommand(t.Context(), missingFlagArguments, &stdout, &stderr, func() time.Time { return now })
		if !handled || err == nil || stdout.Len() != 0 {
			t.Fatalf("runCommand(%q) = handled %v error %v stdout=%q", missingFlagArguments, handled, err, stdout.String())
		}
	}

	result := runLegacyHumanUsersCommand(t, []string{
		"migration", "import-legacy-human-users", "--source", sourcePath, "--backup-output", rollbackPath,
		"--confirm-offline", "--replace-bootstrap-admin",
	}, now)
	if result.Status != "success" || result.Action != "migration.import-legacy-human-users" || result.Result.AlreadyApplied ||
		result.Result.Users.SourceRows != 1 || result.Result.Users.TargetRows != 1 || result.RollbackBackup.Manifest.SchemaVersion != store.CurrentSchemaVersion() {
		t.Fatalf("migration output = %#v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"legacy-admin@example.test", legacyHash, "11111111111111111111111111111111"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("migration output leaked source credential or identity data: %s", encoded)
		}
	}
	if verified, err := backup.Verify(t.Context(), rollbackPath); err != nil || verified != result.RollbackBackup.Manifest {
		t.Fatalf("Verify(rollback) = (%#v, %v)", verified, err)
	}

	target, err = store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	user, findErr := target.FindUserByEmail(t.Context(), "legacy-admin@example.test")
	detail, detailErr := target.GetAdminUser(t.Context(), 41)
	if closeErr := target.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if findErr != nil || detailErr != nil || user.ID != 41 || user.PasswordHash != legacyHash || !user.IsAdmin ||
		detail.LastLoginAt == nil || !detail.LastLoginAt.Equal(time.Unix(1_700_000_100, 0).UTC()) ||
		detail.TelegramID == nil || *detail.TelegramID != 6_000_000_041 || detail.RemindExpire || !detail.RemindTraffic ||
		detail.Remarks == nil || *detail.Remarks != "迁移保留备注" {
		t.Fatalf("target user=(%#v,%#v) errors=(%v,%v)", user, detail, findErr, detailErr)
	}

	repeated := runLegacyHumanUsersCommand(t, []string{
		"migration", "import-legacy-human-users", "--source", sourcePath, "--confirm-offline", "--replace-bootstrap-admin",
	}, now.Add(time.Minute))
	if !repeated.Result.AlreadyApplied || repeated.Result.AppliedAt != now || repeated.RollbackBackup.SHA256 != result.RollbackBackup.SHA256 {
		t.Fatalf("idempotent output = %#v", repeated)
	}

	restoredPath := filepath.Join(directory, "restored-before-human-users.db")
	if _, err := backup.Restore(t.Context(), rollbackPath, restoredPath); err != nil {
		t.Fatal(err)
	}
	restored, err := sql.Open("sqlite", "file:"+restoredPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	var restoredEmail string
	var restoredRuns int
	if err := restored.QueryRow(`SELECT email FROM users`).Scan(&restoredEmail); err != nil {
		t.Fatal(err)
	}
	if err := restored.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs`).Scan(&restoredRuns); err != nil {
		t.Fatal(err)
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
	if restoredEmail != "bootstrap@example.test" || restoredRuns != 0 {
		t.Fatalf("rollback restored email=%q runs=%d", restoredEmail, restoredRuns)
	}
}

type legacyHumanUsersCommandOutput struct {
	Status         string                             `json:"status"`
	Action         string                             `json:"action"`
	Source         legacyMigrationSourceResult        `json:"source"`
	RollbackBackup legacyMigrationBackupResult        `json:"rollback_backup"`
	Result         store.LegacyHumanUsersImportReport `json:"result"`
}

func runLegacyHumanUsersCommand(t *testing.T, arguments []string, now time.Time) legacyHumanUsersCommandOutput {
	t.Helper()
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), arguments, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(%q) = handled %v error %v stderr=%q", arguments, handled, err, stderr.String())
	}
	var output legacyHumanUsersCommandOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode migration output %q: %v", stdout.String(), err)
	}
	return output
}

func createLegacyHumanUsersCLIInput(t *testing.T, directory string) (string, string) {
	t.Helper()
	path := filepath.Join(directory, "legacy-human-users.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_user (
			id INTEGER PRIMARY KEY, invite_user_id INTEGER, telegram_id INTEGER, email TEXT NOT NULL,
			password TEXT NOT NULL, password_algo TEXT, password_salt TEXT, balance INTEGER NOT NULL DEFAULT 0,
			discount REAL, commission_type INTEGER NOT NULL DEFAULT 0, commission_rate REAL,
			commission_balance INTEGER NOT NULL DEFAULT 0, t INTEGER NOT NULL DEFAULT 0, u INTEGER NOT NULL DEFAULT 0,
			d INTEGER NOT NULL DEFAULT 0, transfer_enable INTEGER NOT NULL DEFAULT 0, banned INTEGER NOT NULL DEFAULT 0,
			is_admin INTEGER NOT NULL DEFAULT 0, last_login_at INTEGER, is_staff INTEGER NOT NULL DEFAULT 0,
			last_login_ip TEXT, uuid TEXT NOT NULL, group_id INTEGER, plan_id INTEGER, speed_limit INTEGER,
			remind_expire INTEGER NOT NULL DEFAULT 1, remind_traffic INTEGER NOT NULL DEFAULT 1, token TEXT NOT NULL,
			expired_at INTEGER, remarks TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
			device_limit INTEGER, online_count INTEGER, last_online_at INTEGER, next_reset_at INTEGER,
			last_reset_at INTEGER, reset_count INTEGER NOT NULL DEFAULT 0, is_distributor INTEGER NOT NULL DEFAULT 0,
			distributor_name TEXT
		);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("legacy-password-123"), 10)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	phpHash := "$2y$" + string(hash[4:])
	if _, err := database.Exec(`
		INSERT INTO v2_user
		(id, email, password, is_admin, last_login_at, uuid, token, expired_at, created_at, updated_at,
		 telegram_id, remind_expire, remarks)
		VALUES (41, 'legacy-admin@example.test', ?, 1, 1700000100, '11111111-1111-4111-8111-111111111111',
		        '11111111111111111111111111111111', 0, 1700000000, 1700000200, 6000000041, 0, '迁移保留备注')
	`, phpHash); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path, phpHash
}
