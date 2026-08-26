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

func TestRunCommandImportsLegacyOrdersWithIndependentRollback(t *testing.T) {
	directory := t.TempDir()
	sourcePath := createLegacyOrdersCLIInput(t, directory)
	targetPath := filepath.Join(directory, "target.db")
	rollbackPath := filepath.Join(directory, "pre-orders.xbbackup")
	target, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Migrate(t.Context()); err != nil {
		_ = target.Close()
		t.Fatal(err)
	}
	user, err := target.CreateAdminUser(t.Context(), store.CreateAdminUserInput{Email: "order-cli@example.test", PasswordHash: "hash"}, time.Unix(50, 0))
	if err != nil || user.ID != 1 {
		_ = target.Close()
		t.Fatalf("CreateAdminUser() = (%#v, %v)", user, err)
	}
	plan, err := target.CreatePlan(t.Context(), store.SavePlanInput{Name: "Order CLI plan", TransferEnableGiB: 100, Prices: store.PlanPrices{"monthly": 999}}, time.Unix(50, 0))
	if err != nil || plan.ID != 1 {
		_ = target.Close()
		t.Fatalf("CreatePlan() = (%#v, %v)", plan, err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

	var blockedOut, blockedErr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-orders", "--source", sourcePath, "--backup-output", rollbackPath,
	}, &blockedOut, &blockedErr, func() time.Time { return now })
	if !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") || blockedOut.Len() != 0 {
		t.Fatalf("missing confirmation = handled %v error %v stdout=%q", handled, err, blockedOut.String())
	}

	result := runLegacyOrdersCommand(t, []string{
		"migration", "import-legacy-orders", "--source", sourcePath,
		"--backup-output", rollbackPath, "--confirm-offline",
	}, now)
	if result.Status != "success" || result.Action != "migration.import-legacy-orders" || result.Result.AlreadyApplied ||
		result.Result.Orders.SourceRows != 1 || result.Result.Orders.TargetRows != 1 ||
		result.RollbackBackup.Manifest.SchemaVersion != store.CurrentSchemaVersion() {
		t.Fatalf("migration output = %#v", result)
	}
	if verified, err := backup.Verify(t.Context(), rollbackPath); err != nil || verified != result.RollbackBackup.Manifest {
		t.Fatalf("Verify(rollback) = (%#v, %v)", verified, err)
	}
	repeated := runLegacyOrdersCommand(t, []string{
		"migration", "import-legacy-orders", "--source", sourcePath, "--confirm-offline",
	}, now.Add(time.Minute))
	if !repeated.Result.AlreadyApplied || repeated.Result.AppliedAt != now || repeated.RollbackBackup.SHA256 != result.RollbackBackup.SHA256 {
		t.Fatalf("idempotent output = %#v", repeated)
	}

	inspection, err := sql.Open("sqlite", "file:"+targetPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	var original, total, balance, discount, runs int64
	if err := inspection.QueryRow(`SELECT original_amount, total_amount, balance_amount, discount_amount FROM orders WHERE id = 11`).Scan(&original, &total, &balance, &discount); err != nil {
		t.Fatal(err)
	}
	if err := inspection.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = 'orders-v1'`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if original != 999 || total != 799 || balance != 100 || discount != 100 || runs != 1 {
		t.Fatalf("target finance=%d/%d/%d/%d runs=%d", original, total, balance, discount, runs)
	}
}

func runLegacyOrdersCommand(t *testing.T, arguments []string, now time.Time) legacyOrdersMigrationCommandResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), arguments, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(%q) = handled %v error %v stderr=%q", arguments, handled, err, stderr.String())
	}
	var output legacyOrdersMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode migration output %q: %v", stdout.String(), err)
	}
	return output
}

func createLegacyOrdersCLIInput(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "legacy-orders.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_order (
			id INTEGER PRIMARY KEY, invite_user_id INTEGER, user_id INTEGER NOT NULL, plan_id INTEGER NOT NULL,
			coupon_id INTEGER, payment_id INTEGER, type INTEGER NOT NULL, period TEXT NOT NULL, trade_no TEXT NOT NULL,
			callback_no TEXT, total_amount INTEGER NOT NULL, handling_amount INTEGER, discount_amount INTEGER,
			surplus_amount INTEGER, surplus_credit INTEGER, balance_amount INTEGER, surplus_order_ids TEXT,
			status INTEGER NOT NULL, commission_status INTEGER, commission_balance INTEGER,
			actual_commission_balance INTEGER, paid_at INTEGER, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
			distributor_order_id INTEGER, entitlement_expired_at_before INTEGER, entitlement_expired_at_after INTEGER,
			distributor_idempotency_key TEXT, distributor_settled_by INTEGER
		);
		INSERT INTO v2_order VALUES (
			11, NULL, 1, 1, NULL, NULL, 1, 'monthly', '2026082612000000000000011', 'gateway-cli',
			799, NULL, 100, 0, 0, 100, '[]', 3, 0, 0, NULL, 1700000200, 1700000000, 1700000200,
			NULL, NULL, 1800000000, NULL, NULL
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
