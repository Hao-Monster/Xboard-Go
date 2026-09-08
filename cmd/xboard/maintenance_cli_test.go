package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/maintenance"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestRunCommandMaintenanceCleanupExpired(t *testing.T) {
	databasePath := createMaintenanceTestDatabase(t)
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+databasePath)
	now := time.Date(2026, 8, 25, 15, 0, 0, 0, time.UTC)
	var stdout, stderr bytes.Buffer

	handled, err := runCommand(context.Background(), []string{"maintenance", "cleanup-expired", "--limit", "37"}, &stdout, &stderr, func() time.Time { return now })
	if err != nil || !handled {
		t.Fatalf("runCommand(cleanup) = handled %v error %v stderr=%q", handled, err, stderr.String())
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(cleanup) stderr = %q", stderr.String())
	}
	var output maintenanceCommandOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode cleanup output %q: %v", stdout.String(), err)
	}
	if output.Status != "success" || output.Action != "maintenance.cleanup-expired" || output.Limit != 37 || output.AsOf != now || output.Result != (maintenance.CleanupResult{}) {
		t.Fatalf("cleanup output = %#v", output)
	}
}

func TestRunCommandMaintenanceNodeAuthRetirementReadiness(t *testing.T) {
	databasePath := createMaintenanceTestDatabase(t)
	ctx := context.Background()
	database, err := store.OpenSQLite("file:" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	observedSince := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	asOf := observedSince.Add(31 * 24 * time.Hour)
	lastUsedAt := observedSince.Add(30 * 24 * time.Hour)
	if err := database.EnsureNodeAuthTelemetry(ctx, observedSince); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.AddNodeAuthUsage(ctx, []store.NodeAuthUsageIncrement{
		{AuthKind: store.NodeAuthKindMachineCredential, Transport: store.NodeAuthTransportHTTP, SuccessCount: 12, LastUsedAt: lastUsedAt},
		{AuthKind: store.NodeAuthKindMachineCredential, Transport: store.NodeAuthTransportWebSocket, SuccessCount: 3, LastUsedAt: lastUsedAt.Add(-time.Minute)},
	}); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XBOARD_DATABASE_DSN", "file:"+databasePath)
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(ctx, []string{"maintenance", "node-auth-retirement-readiness", "--min-observation-days", "30"}, &stdout, &stderr, func() time.Time {
		return asOf
	})
	if err != nil || !handled {
		t.Fatalf("runCommand(node-auth-retirement-readiness) = handled %v error %v stderr=%q", handled, err, stderr.String())
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(node-auth-retirement-readiness) stderr = %q", stderr.String())
	}
	var output nodeAuthRetirementReadinessCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode node auth readiness output %q: %v", stdout.String(), err)
	}
	if output.Status != "success" || output.Action != "maintenance.node-auth-retirement-readiness" ||
		output.MinimumObservedDays != 30 || !output.Result.RetirementCandidate ||
		output.Result.LegacyGlobalToken.HTTPAuthSuccess != 0 || output.Result.MachineCredential.HTTPAuthSuccess != 12 ||
		output.Result.MachineCredential.WebSocketAuthSuccess != 3 {
		t.Fatalf("node auth readiness output = %#v", output)
	}
	if strings.Contains(stdout.String(), "server_token") || strings.Contains(stdout.String(), "machine-credential") {
		t.Fatalf("node auth readiness output exposed credential-shaped data: %s", stdout.String())
	}
}

func TestRunCommandMaintenanceOperationalRetentionReadiness(t *testing.T) {
	sourcePath := createLegacyOperationalRetentionTestSnapshot(t)
	asOf := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var stdout, stderr bytes.Buffer

	handled, err := runCommand(context.Background(), []string{"maintenance", "operational-retention-readiness", "--source", sourcePath, "--retention-days", "90"}, &stdout, &stderr, func() time.Time {
		return asOf
	})
	if err != nil || !handled {
		t.Fatalf("runCommand(operational-retention-readiness) = handled %v error %v stderr=%q", handled, err, stderr.String())
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(operational-retention-readiness) stderr = %q", stderr.String())
	}
	var output operationalRetentionReadinessCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode operational retention output %q: %v", stdout.String(), err)
	}
	if output.Status != "success" || output.Action != "maintenance.operational-retention-readiness" ||
		output.RetentionDays != 90 || output.Result.NodeTraffic.TotalRows != 2 ||
		output.Result.NodeTraffic.RetainedRows != 1 || !output.Result.FailedJobs.PayloadColumnsPresent ||
		output.Result.FailedJobs.ExecutableImportSupported || output.Result.QueuedJobs.TotalRows != 1 ||
		output.Result.MigrationSafe {
		t.Fatalf("operational retention output = %#v", output)
	}
	for _, forbidden := range []string{"secret payload", "stack trace"} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("operational retention output leaked failed_jobs data: %s", stdout.String())
		}
	}
}

func TestRunCommandMaintenanceDatabaseCompatibilityReadiness(t *testing.T) {
	databasePath := createMaintenanceTestDatabase(t)
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+databasePath)
	asOf := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	var stdout, stderr bytes.Buffer

	handled, err := runCommand(context.Background(), []string{"maintenance", "database-compatibility-readiness", "--target-engine", "sqlite3", "--representative-data"}, &stdout, &stderr, func() time.Time {
		return asOf
	})
	if err != nil || !handled {
		t.Fatalf("runCommand(database-compatibility-readiness) = handled %v error %v stderr=%q", handled, err, stderr.String())
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(database-compatibility-readiness) stderr = %q", stderr.String())
	}
	var output databaseCompatibilityReadinessCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode database compatibility readiness output %q: %v", stdout.String(), err)
	}
	if output.Status != "success" || output.Action != "maintenance.database-compatibility-readiness" ||
		output.AsOf != asOf || output.TargetEngine != "sqlite" || !output.RepresentativeDataSupplied ||
		!output.Result.QueryOnly || !output.Result.SQLiteCandidate || !output.Result.ProductionDecisionReady {
		t.Fatalf("database compatibility readiness output = %#v", output)
	}
	if strings.Contains(stdout.String(), "XBOARD_DATABASE_DSN") || strings.Contains(stdout.String(), "server_token") {
		t.Fatalf("database compatibility readiness output exposed sensitive configuration: %s", stdout.String())
	}
}

func TestRunCommandMaintenanceDatabaseCompatibilityReadinessReportsDecisionGaps(t *testing.T) {
	databasePath := createMaintenanceTestDatabase(t)
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+databasePath)
	var stdout, stderr bytes.Buffer

	handled, err := runCommand(context.Background(), []string{"maintenance", "database-compatibility-readiness", "--target-engine", "postgres"}, &stdout, &stderr, func() time.Time {
		return time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	})
	if err != nil || !handled {
		t.Fatalf("runCommand(database-compatibility-readiness) = handled %v error %v stderr=%q", handled, err, stderr.String())
	}
	var output databaseCompatibilityReadinessCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode database compatibility readiness output %q: %v", stdout.String(), err)
	}
	if output.Result.ProductionDecisionReady || output.Result.SQLiteCandidate {
		t.Fatalf("database compatibility readiness incorrectly passed = %#v", output.Result)
	}
	reasons := strings.Join(output.Result.Reasons, "\n")
	for _, want := range []string{"only SQLite is implemented", "representative production-shaped database evidence has not been supplied"} {
		if !strings.Contains(reasons, want) {
			t.Fatalf("database compatibility reasons %q missing %q", reasons, want)
		}
	}
}

func TestRunCommandMaintenanceNodeAuthRetirementReadinessRejectsUnsafeInputsWithoutCreatingDatabase(t *testing.T) {
	directory := t.TempDir()
	missingPath := filepath.Join(directory, "missing.db")
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name      string
		arguments []string
		dsn       string
	}{
		{name: "unknown subcommand", arguments: []string{"maintenance", "node-auth-retirement"}, dsn: "file:" + missingPath},
		{name: "zero days", arguments: []string{"maintenance", "node-auth-retirement-readiness", "--min-observation-days", "0"}, dsn: "file:" + missingPath},
		{name: "excessive days", arguments: []string{"maintenance", "node-auth-retirement-readiness", "--min-observation-days", "366"}, dsn: "file:" + missingPath},
		{name: "positional argument", arguments: []string{"maintenance", "node-auth-retirement-readiness", "unexpected"}, dsn: "file:" + missingPath},
		{name: "memory database", arguments: []string{"maintenance", "node-auth-retirement-readiness"}, dsn: "file:readiness?mode=memory&cache=shared"},
		{name: "missing database", arguments: []string{"maintenance", "node-auth-retirement-readiness"}, dsn: "file:" + missingPath},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("XBOARD_DATABASE_DSN", testCase.dsn)
			var stdout, stderr bytes.Buffer
			handled, err := runCommand(context.Background(), testCase.arguments, &stdout, &stderr, func() time.Time { return now })
			if !handled || err == nil {
				t.Fatalf("runCommand(%q) = handled %v error %v", testCase.arguments, handled, err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("failed node auth readiness wrote success output: %q", stdout.String())
			}
		})
	}
	if _, err := os.Lstat(missingPath); !os.IsNotExist(err) {
		t.Fatalf("node auth readiness created missing database: %v", err)
	}
}

func TestRunCommandMaintenanceNodeAuthRetirementReadinessRequiresExistingObservation(t *testing.T) {
	databasePath := createMaintenanceTestDatabase(t)
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+databasePath)
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), []string{"maintenance", "node-auth-retirement-readiness"}, &stdout, &stderr, func() time.Time {
		return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	})
	if !handled || err == nil || !strings.Contains(err.Error(), "read node authentication telemetry") {
		t.Fatalf("readiness without observation = handled %v error %v", handled, err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("readiness without observation wrote success output: %q", stdout.String())
	}
}

func TestRunCommandMaintenanceOperationalRetentionReadinessRejectsUnsafeInputsWithoutCreatingDatabase(t *testing.T) {
	directory := t.TempDir()
	missingPath := filepath.Join(directory, "missing.db")
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name      string
		arguments []string
	}{
		{name: "missing source", arguments: []string{"maintenance", "operational-retention-readiness"}},
		{name: "zero days", arguments: []string{"maintenance", "operational-retention-readiness", "--source", missingPath, "--retention-days", "0"}},
		{name: "excessive days", arguments: []string{"maintenance", "operational-retention-readiness", "--source", missingPath, "--retention-days", "3651"}},
		{name: "positional argument", arguments: []string{"maintenance", "operational-retention-readiness", "--source", missingPath, "unexpected"}},
		{name: "missing database", arguments: []string{"maintenance", "operational-retention-readiness", "--source", missingPath}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			handled, err := runCommand(context.Background(), testCase.arguments, &stdout, &stderr, func() time.Time { return now })
			if !handled || err == nil {
				t.Fatalf("runCommand(%q) = handled %v error %v", testCase.arguments, handled, err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("failed operational retention wrote success output: %q", stdout.String())
			}
		})
	}
	if _, err := os.Lstat(missingPath); !os.IsNotExist(err) {
		t.Fatalf("operational retention created missing database: %v", err)
	}
}

func TestRunCommandMaintenanceDatabaseCompatibilityReadinessRejectsUnsafeInputsWithoutCreatingDatabase(t *testing.T) {
	directory := t.TempDir()
	missingPath := filepath.Join(directory, "missing.db")
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name      string
		arguments []string
		dsn       string
	}{
		{name: "positional argument", arguments: []string{"maintenance", "database-compatibility-readiness", "unexpected"}, dsn: "file:" + missingPath},
		{name: "memory database", arguments: []string{"maintenance", "database-compatibility-readiness"}, dsn: "file:compatibility?mode=memory&cache=shared"},
		{name: "missing database", arguments: []string{"maintenance", "database-compatibility-readiness"}, dsn: "file:" + missingPath},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("XBOARD_DATABASE_DSN", testCase.dsn)
			var stdout, stderr bytes.Buffer
			handled, err := runCommand(context.Background(), testCase.arguments, &stdout, &stderr, func() time.Time { return now })
			if !handled || err == nil {
				t.Fatalf("runCommand(%q) = handled %v error %v", testCase.arguments, handled, err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("failed database compatibility readiness wrote success output: %q", stdout.String())
			}
		})
	}
	if _, err := os.Lstat(missingPath); !os.IsNotExist(err) {
		t.Fatalf("database compatibility readiness created missing database: %v", err)
	}
}

func TestRunCommandMaintenanceCleanupRejectsUnsafeInputsWithoutCreatingDatabase(t *testing.T) {
	directory := t.TempDir()
	missingPath := filepath.Join(directory, "missing.db")
	now := time.Date(2026, 8, 25, 15, 0, 0, 0, time.UTC)

	for _, testCase := range []struct {
		name      string
		arguments []string
		dsn       string
	}{
		{name: "missing subcommand", arguments: []string{"maintenance"}, dsn: "file:" + missingPath},
		{name: "unknown subcommand", arguments: []string{"maintenance", "unknown"}, dsn: "file:" + missingPath},
		{name: "zero limit", arguments: []string{"maintenance", "cleanup-expired", "--limit", "0"}, dsn: "file:" + missingPath},
		{name: "excessive limit", arguments: []string{"maintenance", "cleanup-expired", "--limit", "1001"}, dsn: "file:" + missingPath},
		{name: "positional argument", arguments: []string{"maintenance", "cleanup-expired", "unexpected"}, dsn: "file:" + missingPath},
		{name: "memory database", arguments: []string{"maintenance", "cleanup-expired"}, dsn: "file:cleanup?mode=memory&cache=shared"},
		{name: "missing database", arguments: []string{"maintenance", "cleanup-expired"}, dsn: "file:" + missingPath},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("XBOARD_DATABASE_DSN", testCase.dsn)
			var stdout, stderr bytes.Buffer
			handled, err := runCommand(context.Background(), testCase.arguments, &stdout, &stderr, func() time.Time { return now })
			if !handled || err == nil {
				t.Fatalf("runCommand(%q) = handled %v error %v", testCase.arguments, handled, err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("failed cleanup wrote success output: %q", stdout.String())
			}
		})
	}
	if _, err := os.Lstat(missingPath); !os.IsNotExist(err) {
		t.Fatalf("cleanup created missing database: %v", err)
	}
}

func TestRunCommandMaintenanceCleanupRequiresCurrentSchema(t *testing.T) {
	for _, version := range []int{store.CurrentSchemaVersion() - 1, store.CurrentSchemaVersion() + 1} {
		t.Run(fmt.Sprintf("schema-%d", version), func(t *testing.T) {
			databasePath := createMaintenanceTestDatabase(t)
			setSQLiteUserVersion(t, databasePath, version)
			t.Setenv("XBOARD_DATABASE_DSN", "file:"+databasePath)
			var stdout, stderr bytes.Buffer
			handled, err := runCommand(context.Background(), []string{"maintenance", "cleanup-expired"}, &stdout, &stderr, time.Now)
			if !handled || err == nil || !strings.Contains(err.Error(), "current schema") {
				t.Fatalf("cleanup schema %d = handled %v error %v", version, handled, err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("schema %d cleanup wrote success output: %q", version, stdout.String())
			}
		})
	}
}

func TestRunCommandMaintenanceCleanupRejectsSpoofedCurrentSchemaWithoutWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spoofed.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(t.Context(), `
		CREATE TABLE tickets (
			id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL,
			status INTEGER NOT NULL,
			reply_status INTEGER NOT NULL,
			last_reply_user_id INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		INSERT INTO tickets VALUES (1, 1, 0, 1, 2, 1);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if _, err := database.ExecContext(t.Context(), fmt.Sprintf("PRAGMA user_version = %d", store.CurrentSchemaVersion())); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XBOARD_DATABASE_DSN", "file:"+path)
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), []string{"maintenance", "cleanup-expired"}, &stdout, &stderr, func() time.Time {
		return time.Unix(100_000, 0)
	})
	if !handled || err == nil || !strings.Contains(err.Error(), "missing required table") {
		t.Fatalf("cleanup spoofed schema = handled %v error %v", handled, err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("spoofed schema cleanup wrote success output: %q", stdout.String())
	}

	database, err = sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var status int
	if err := database.QueryRowContext(t.Context(), `SELECT status FROM tickets WHERE id = 1`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != 0 {
		t.Fatalf("spoofed schema was modified before validation: ticket status = %d", status)
	}
}

func createMaintenanceTestDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "maintenance.db")
	database, err := store.OpenSQLite("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context()); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func setSQLiteUserVersion(t *testing.T, path string, version int) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(t.Context(), fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		t.Fatal(err)
	}
}

func createLegacyOperationalRetentionTestSnapshot(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
		CREATE TABLE v2_stat_server (
			id INTEGER PRIMARY KEY,
			server_id INTEGER,
			server_type TEXT,
			u INTEGER,
			d INTEGER,
			record_type TEXT,
			record_at INTEGER,
			created_at INTEGER,
			updated_at INTEGER
		);
		CREATE TABLE failed_jobs (
			id INTEGER PRIMARY KEY,
			uuid TEXT,
			connection TEXT,
			queue TEXT,
			payload TEXT,
			exception TEXT,
			failed_at TEXT
		);
		CREATE TABLE jobs (
			id INTEGER PRIMARY KEY,
			queue TEXT,
			payload TEXT,
			attempts INTEGER,
			reserved_at INTEGER,
			available_at INTEGER,
			created_at INTEGER
		);
		INSERT INTO v2_stat_server VALUES
			(1, 42, 'vless', 1024, 2048, 'd', 1787702400, 1787702400, 1787702410),
			(2, 42, 'vless', 512, 256, 'd', 1777670400, 1777670400, 1777670410);
		INSERT INTO failed_jobs VALUES (1, 'job-one', 'database', 'default', 'secret payload', 'stack trace', '2026-08-15 00:00:00');
		INSERT INTO jobs VALUES (1, 'default', 'queued secret payload', 0, NULL, 1787702400, 1787702400);
	`); err != nil {
		t.Fatal(err)
	}
	return path
}

type maintenanceCommandOutput struct {
	Status string                    `json:"status"`
	Action string                    `json:"action"`
	AsOf   time.Time                 `json:"as_of"`
	Limit  int                       `json:"limit"`
	Result maintenance.CleanupResult `json:"result"`
}
