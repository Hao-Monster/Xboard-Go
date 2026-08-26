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
)

func TestRunCommandImportsLegacyGiftCardsWithIndependentRollback(t *testing.T) {
	directory := t.TempDir()
	sourcePath := createLegacyGiftCardsCLIInput(t, directory)
	targetPath := filepath.Join(directory, "target.db")
	rollbackPath := filepath.Join(directory, "pre-gift-cards.xbbackup")
	target, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Migrate(t.Context()); err != nil {
		_ = target.Close()
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 26, 16, 0, 0, 0, time.UTC)
	if _, err := target.CreateAdminUser(t.Context(), store.CreateAdminUserInput{Email: "gift-cli-admin@example.test", PasswordHash: "hash"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := target.CreateAdminUser(t.Context(), store.CreateAdminUserInput{Email: "gift-cli-user@example.test", PasswordHash: "hash"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := target.CreatePlan(t.Context(), store.SavePlanInput{Name: "Gift CLI plan", TransferEnableGiB: 100}, now); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)

	var blockedOut, blockedErr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{"migration", "import-legacy-gift-cards", "--source", sourcePath, "--backup-output", rollbackPath}, &blockedOut, &blockedErr, func() time.Time { return now })
	if !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") || blockedOut.Len() != 0 {
		t.Fatalf("missing confirmation = handled %v error %v stdout=%q", handled, err, blockedOut.String())
	}

	result := runLegacyGiftCardsCommand(t, []string{"migration", "import-legacy-gift-cards", "--source", sourcePath, "--backup-output", rollbackPath, "--confirm-offline"}, now)
	if result.Status != "success" || result.Action != "migration.import-legacy-gift-cards" || result.Result.AlreadyApplied ||
		result.Result.Templates.SourceRows != 1 || result.Result.Codes.SourceRows != 1 || result.Result.Usages.SourceRows != 1 ||
		result.Result.Usages.SourceChecksum != result.Result.Usages.TargetChecksum || result.RollbackBackup.Manifest.SchemaVersion != store.CurrentSchemaVersion() {
		t.Fatalf("migration output = %#v", result)
	}
	if verified, err := backup.Verify(t.Context(), rollbackPath); err != nil || verified != result.RollbackBackup.Manifest {
		t.Fatalf("Verify(rollback) = (%#v, %v)", verified, err)
	}
	repeated := runLegacyGiftCardsCommand(t, []string{"migration", "import-legacy-gift-cards", "--source", sourcePath, "--confirm-offline"}, now.Add(time.Minute))
	if !repeated.Result.AlreadyApplied || repeated.Result.AppliedAt != now || repeated.RollbackBackup.SHA256 != result.RollbackBackup.SHA256 {
		t.Fatalf("idempotent output = %#v", repeated)
	}
}

func runLegacyGiftCardsCommand(t *testing.T, arguments []string, now time.Time) legacyGiftCardsMigrationCommandResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), arguments, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(%q) = handled %v error %v stderr=%q", arguments, handled, err, stderr.String())
	}
	var output legacyGiftCardsMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode migration output %q: %v", stdout.String(), err)
	}
	return output
}

func createLegacyGiftCardsCLIInput(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "legacy-gift-cards.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`
		CREATE TABLE v2_gift_card_template (id INTEGER PRIMARY KEY, name TEXT, description TEXT, type INTEGER, status INTEGER, conditions TEXT, rewards TEXT, limits TEXT, special_config TEXT, icon TEXT, background_image TEXT, theme_color TEXT, sort INTEGER, admin_id INTEGER, created_at INTEGER, updated_at INTEGER);
		CREATE TABLE v2_gift_card_code (id INTEGER PRIMARY KEY, template_id INTEGER, code TEXT, batch_id TEXT, status INTEGER, user_id INTEGER, used_at INTEGER, expires_at INTEGER, actual_rewards TEXT, usage_count INTEGER, max_usage INTEGER, metadata TEXT, created_at INTEGER, updated_at INTEGER);
		CREATE TABLE v2_gift_card_usage (id INTEGER PRIMARY KEY, code_id INTEGER, template_id INTEGER, user_id INTEGER, invite_user_id INTEGER, rewards_given TEXT, invite_rewards TEXT, user_level_at_use INTEGER, plan_id_at_use INTEGER, multiplier_applied DECIMAL(3,2), ip_address TEXT, user_agent TEXT, notes TEXT, created_at INTEGER);
		INSERT INTO v2_gift_card_template VALUES (10, 'CLI gift', '', 1, 1, '{}', '{"balance":500}', '{"max_use_per_user":2}', '{}', '', '', '#1890ff', 1, 1, 1700000000, 1700000100);
		INSERT INTO v2_gift_card_code VALUES (20, 10, 'LEGACYGC00000020', 'legacy_batch_0020', 1, 2, 1700000200, NULL, '{"balance":500}', 1, 2, '{}', 1700000100, 1700000200);
		INSERT INTO v2_gift_card_usage VALUES (30, 20, 10, 2, NULL, '{"balance":500}', NULL, 9, 1, 1.0, '192.0.2.20', 'cli-test', '', 1700000200);
	`)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
