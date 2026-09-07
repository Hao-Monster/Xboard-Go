package legacymigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestReadOperationalRetentionSnapshotSummarizesWindowWithoutSensitivePayloads(t *testing.T) {
	path := createOperationalRetentionSnapshot(t)
	asOf := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	report, err := ReadOperationalRetentionSnapshot(context.Background(), path, asOf, DefaultOperationalRetentionDays)
	if err != nil {
		t.Fatalf("ReadOperationalRetentionSnapshot() error = %v", err)
	}
	if report.RetentionDays != 90 || report.CutoffAt != asOf.Add(-90*24*time.Hour).Unix() || report.SHA256 == "" || report.Size < 512 {
		t.Fatalf("report identity/window = %#v", report)
	}
	if !report.NodeTraffic.Present || report.NodeTraffic.TotalRows != 4 || report.NodeTraffic.RetainedRows != 2 ||
		report.NodeTraffic.OutOfWindowRows != 1 || report.NodeTraffic.FutureRows != 1 || report.NodeTraffic.InvalidRows != 0 {
		t.Fatalf("node traffic retention = %#v", report.NodeTraffic)
	}
	if !report.FailedJobs.Present || report.FailedJobs.TotalRows != 2 || report.FailedJobs.RetainedRows != 1 ||
		report.FailedJobs.OutOfWindowRows != 1 || report.FailedJobs.InvalidRows != 0 ||
		!report.FailedJobs.PayloadColumnsPresent || report.FailedJobs.ExecutableImportSupported {
		t.Fatalf("failed_jobs retention = %#v", report.FailedJobs)
	}
	if !report.QueuedJobs.Present || report.QueuedJobs.TotalRows != 1 {
		t.Fatalf("queued jobs = %#v", report.QueuedJobs)
	}
	if !report.StatsDaily.Present || report.StatsDaily.TotalRows != 1 {
		t.Fatalf("stats_daily = %#v", report.StatsDaily)
	}
	if report.MigrationSafe || !report.DecisionRequired {
		t.Fatalf("safety flags = safe %t decision %t reasons %#v", report.MigrationSafe, report.DecisionRequired, report.Reasons)
	}
	encoded := mustJSON(t, report)
	for _, forbidden := range []string{"secret payload", "stack trace", "sensitive payload", "older trace"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("operational retention report leaked sensitive failed_jobs data: %s", encoded)
		}
	}
	for _, want := range []string{"future-dated", "failed_jobs payloads", "drained", "stats_daily"} {
		if !strings.Contains(strings.Join(report.Reasons, "\n"), want) {
			t.Fatalf("reasons %#v do not contain %q", report.Reasons, want)
		}
	}
}

func TestReadOperationalRetentionSnapshotReportsSafeWindowWhenOnlyRecentFactsExist(t *testing.T) {
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
		INSERT INTO v2_stat_server VALUES (1, 42, 'vless', 1024, 2048, 'd', 1787702400, 1787702400, 1787702410);
	`); err != nil {
		t.Fatal(err)
	}

	report, err := ReadOperationalRetentionSnapshot(context.Background(), path, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), 90)
	if err != nil {
		t.Fatalf("ReadOperationalRetentionSnapshot() error = %v", err)
	}
	if !report.MigrationSafe || report.DecisionRequired || report.NodeTraffic.RetainedRows != 1 ||
		report.FailedJobs.Present || report.QueuedJobs.Present || report.StatsDaily.Present {
		t.Fatalf("safe retention report = %#v", report)
	}
}

func TestReadOperationalRetentionSnapshotRejectsUnsafeOrInvalidSources(t *testing.T) {
	asOf := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if _, err := ReadOperationalRetentionSnapshot(context.Background(), "", asOf, 90); err == nil {
		t.Fatal("empty path was accepted")
	}
	if _, err := ReadOperationalRetentionSnapshot(context.Background(), "missing.db", asOf, 0); err == nil ||
		!strings.Contains(err.Error(), "retention window") {
		t.Fatalf("invalid window error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "legacy.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
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
		INSERT INTO v2_stat_server VALUES (1, 42, 'vless', -1, 2048, 'd', 1787702400, 1787702400, 1787702410);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := ReadOperationalRetentionSnapshot(context.Background(), path, asOf, 90)
	if err != nil {
		t.Fatalf("invalid row should be reported, not crash: %v", err)
	}
	if report.MigrationSafe || report.NodeTraffic.InvalidRows != 1 {
		t.Fatalf("invalid row report = %#v", report)
	}
}

func createOperationalRetentionSnapshot(t *testing.T) string {
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
		CREATE TABLE stats_daily (id INTEGER PRIMARY KEY, record_at INTEGER, payload TEXT);
		INSERT INTO v2_stat_server VALUES
			(1, 42, 'vless', 1024, 2048, 'd', 1787702400, 1787702400, 1787702410),
			(2, 42, 'vless', 2048, 4096, 'm', 1785542400, 1785542400, 1785542410),
			(3, 42, 'vless', 512, 256, 'd', 1777670400, 1777670400, 1777670410),
			(4, 42, 'vless', 1, 2, 'd', 1790812800, 1790812800, 1790812810);
		INSERT INTO failed_jobs VALUES
			(1, 'job-one', 'database', 'default', 'secret payload', 'stack trace', '2026-08-15 00:00:00'),
			(2, 'job-two', 'database', 'default', 'sensitive payload', 'older trace', '2026-04-01 00:00:00');
		INSERT INTO jobs VALUES (1, 'default', 'queued secret payload', 0, NULL, 1787702400, 1787702400);
		INSERT INTO stats_daily VALUES (1, 1787702400, 'aggregate blob');
	`); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
