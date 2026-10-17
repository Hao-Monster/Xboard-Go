package maintenance

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestCheckDatabaseCompatibilityReadinessAcceptsRepresentativeSQLiteCandidate(t *testing.T) {
	path := createDatabaseCompatibilityTestDatabase(t)
	asOf := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	result, err := CheckDatabaseCompatibilityReadiness(context.Background(), path, asOf, "sqlite3", true)
	if err != nil {
		t.Fatalf("CheckDatabaseCompatibilityReadiness() error = %v", err)
	}
	if result.AsOf != asOf || result.TargetEngine != "sqlite" || !result.RepresentativeDataSupplied ||
		result.SchemaVersion != store.CurrentSchemaVersion() || !result.CurrentSchema ||
		!result.SchemaValid || !result.IntegrityOK || result.ForeignKeyViolations != 0 ||
		!result.QueryOnly || !result.SQLiteCandidate || !result.ProductionDecisionReady {
		t.Fatalf("readiness result = %#v", result)
	}
	if got := strings.Join(result.Reasons, " "); !strings.Contains(got, "representative data evidence") {
		t.Fatalf("readiness reasons = %#v", result.Reasons)
	}
}

func TestCheckDatabaseCompatibilityReadinessReportsDecisionGaps(t *testing.T) {
	path := createDatabaseCompatibilityTestDatabase(t)
	setDatabaseCompatibilitySchemaVersion(t, path, store.CurrentSchemaVersion()-1)

	result, err := CheckDatabaseCompatibilityReadiness(context.Background(), path, time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC), "postgres", false)
	if err != nil {
		t.Fatalf("CheckDatabaseCompatibilityReadiness() error = %v", err)
	}
	if result.ProductionDecisionReady || result.SQLiteCandidate || result.CurrentSchema || !result.SchemaValid || !result.IntegrityOK {
		t.Fatalf("readiness incorrectly passed: %#v", result)
	}
	reasons := strings.Join(result.Reasons, "\n")
	for _, want := range []string{
		"only SQLite is implemented",
		"representative production-shaped database evidence has not been supplied",
		"database schema is not at the current Xboard-Go schema version",
	} {
		if !strings.Contains(reasons, want) {
			t.Fatalf("readiness reasons %q missing %q", reasons, want)
		}
	}
}

func TestCheckDatabaseCompatibilityReadinessRejectsInvalidSource(t *testing.T) {
	_, err := CheckDatabaseCompatibilityReadiness(context.Background(), filepath.Join(t.TempDir(), "missing.db"), time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC), "sqlite", false)
	if err == nil {
		t.Fatal("CheckDatabaseCompatibilityReadiness() accepted a missing source")
	}
}

func createDatabaseCompatibilityTestDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "compatibility.db")
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

func setDatabaseCompatibilitySchemaVersion(t *testing.T, path string, version int) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if version == 69 {
		if _, err := database.ExecContext(t.Context(), `
			DROP TABLE IF EXISTS scheduler_quarantine;
			CREATE TABLE IF NOT EXISTS scheduler_quarantine (
			    scope TEXT NOT NULL CHECK (scope IN ('commission_settlement', 'stale_order_cancel')),
			    order_id INTEGER NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
			    reason TEXT NOT NULL CHECK (length(reason) BETWEEN 1 AND 512),
			    quarantined_at INTEGER NOT NULL CHECK (quarantined_at >= 0),
			    PRIMARY KEY (scope, order_id)
			) STRICT, WITHOUT ROWID;
		`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.ExecContext(t.Context(), fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		t.Fatal(err)
	}
}
