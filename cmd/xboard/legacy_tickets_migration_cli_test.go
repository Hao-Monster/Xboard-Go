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

func TestRunCommandImportsLegacyTicketsWithVerifiedRollbackAndRedactedOutput(t *testing.T) {
	directory := t.TempDir()
	sourcePath := createLegacyTicketsCLIInput(t, directory)
	snapshot, err := legacymigration.ReadTicketsSnapshot(t.Context(), sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(directory, "target.db")
	rollbackPath := filepath.Join(directory, "pre-tickets.xbbackup")
	target, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Migrate(t.Context()); err != nil {
		_ = target.Close()
		t.Fatal(err)
	}
	if _, err := target.CreateAdminUser(t.Context(), store.CreateAdminUserInput{Email: "ticket-admin@example.test", PasswordHash: "hash", IsAdmin: true}, time.Unix(1, 0)); err != nil {
		_ = target.Close()
		t.Fatal(err)
	}
	if _, err := target.CreateAdminUser(t.Context(), store.CreateAdminUserInput{Email: "ticket-owner@example.test", PasswordHash: "hash"}, time.Unix(1, 0)); err != nil {
		_ = target.Close()
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	insertLegacyTicketsHumanLedger(t, targetPath, snapshot.SHA256)
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

	var blockedOut, blockedErr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{"migration", "import-legacy-tickets", "--source", sourcePath, "--backup-output", rollbackPath}, &blockedOut, &blockedErr, func() time.Time { return now })
	if !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") || blockedOut.Len() != 0 {
		t.Fatalf("missing confirmation = handled %v error %v stdout=%q", handled, err, blockedOut.String())
	}

	result, raw := runLegacyTicketsCommand(t, []string{"migration", "import-legacy-tickets", "--source", sourcePath, "--backup-output", rollbackPath, "--confirm-offline"}, now)
	if result.Status != "success" || result.Action != "migration.import-legacy-tickets" || result.Result.AlreadyApplied ||
		result.Result.Tickets.SourceRows != 2 || result.Result.Messages.SourceRows != 3 || result.DurationMilliseconds < 0 ||
		result.RollbackBackup.Manifest.SchemaVersion != store.CurrentSchemaVersion() {
		t.Fatalf("result = %#v", result)
	}
	for _, secret := range []string{"Owner question", "Administrator answer", "Waiting for support"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("command output leaked message body %q: %s", secret, raw)
		}
	}
	if _, err := backup.Verify(t.Context(), rollbackPath); err != nil {
		t.Fatalf("Verify(rollback) error = %v", err)
	}
	repeated, _ := runLegacyTicketsCommand(t, []string{"migration", "import-legacy-tickets", "--source", sourcePath, "--confirm-offline"}, now.Add(time.Minute))
	if !repeated.Result.AlreadyApplied || repeated.Result.AppliedAt != now {
		t.Fatalf("repeated = %#v", repeated)
	}
}

func runLegacyTicketsCommand(t *testing.T, arguments []string, now time.Time) (legacyTicketsMigrationCommandResult, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), arguments, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(%q) = handled %v error %v stderr=%q", arguments, handled, err, stderr.String())
	}
	var output legacyTicketsMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode migration output %q: %v", stdout.String(), err)
	}
	return output, stdout.String()
}

func createLegacyTicketsCLIInput(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "legacy-tickets.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_ticket (
			id INTEGER PRIMARY KEY,user_id INTEGER NOT NULL,subject TEXT NOT NULL,level INTEGER NOT NULL,
			status INTEGER NOT NULL,reply_status INTEGER NOT NULL,created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,last_reply_user_id INTEGER
		);
		CREATE TABLE v2_ticket_message (
			id INTEGER PRIMARY KEY,user_id INTEGER NOT NULL,ticket_id INTEGER NOT NULL,
			message TEXT NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL
		);
		INSERT INTO v2_ticket VALUES (159,2,'Closed legacy ticket',2,1,1,100,120,1);
		INSERT INTO v2_ticket VALUES (160,2,'Open legacy ticket',0,0,0,200,200,NULL);
		INSERT INTO v2_ticket_message VALUES (361,2,159,'Owner question',100,100);
		INSERT INTO v2_ticket_message VALUES (362,1,159,'Administrator answer',120,120);
		INSERT INTO v2_ticket_message VALUES (363,2,160,'Waiting for support',200,200);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func insertLegacyTicketsHumanLedger(t *testing.T, targetPath, sourceSHA string) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
		INSERT INTO legacy_migration_runs
		(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at)
		VALUES (?, ?, 1, '/tmp/pre-users.xbbackup', ?, '{}', 1)
	`, store.LegacyHumanUsersSlice, sourceSHA, strings.Repeat("f", 64)); err != nil {
		t.Fatal(err)
	}
}
