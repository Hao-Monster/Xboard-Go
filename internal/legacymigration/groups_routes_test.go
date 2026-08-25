package legacymigration

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReadGroupsRoutesSnapshotPreservesCanonicalLegacyRows(t *testing.T) {
	path := createLegacyGroupsRoutesSnapshot(t)
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_settings (name TEXT, value TEXT);
		INSERT INTO v2_settings (name, value) VALUES ('server_token', 'must-never-be-read');
		INSERT INTO v2_server_group (id, name, created_at, updated_at) VALUES
			(3, 'Standard', 100, 110),
			(9, 'Premium', 120, 130);
		INSERT INTO v2_server_route (id, remarks, match, action, action_value, created_at, updated_at) VALUES
			(4, 'Block ads', '["domain:ads.example","regexp:^tracker\\\\."]', 'block', NULL, 200, 210),
			(12, 'Private DNS', '["geosite:private"]', 'dns', '1.1.1.1', 220, 230);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	snapshot, err := ReadGroupsRoutesSnapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadGroupsRoutesSnapshot() error = %v", err)
	}
	if snapshot.Path != path || snapshot.Size < 512 || len(snapshot.SHA256) != 64 || strings.Contains(snapshot.SHA256, "must-never") {
		t.Fatalf("snapshot identity = %#v", snapshot)
	}
	if len(snapshot.Groups) != 2 || snapshot.Groups[0].ID != 3 || snapshot.Groups[1].Name != "Premium" {
		t.Fatalf("groups = %#v", snapshot.Groups)
	}
	if len(snapshot.Routes) != 2 || snapshot.Routes[0].ID != 4 || snapshot.Routes[0].ActionValue != "" || len(snapshot.Routes[0].Match) != 2 || snapshot.Routes[1].ActionValue != "1.1.1.1" {
		t.Fatalf("routes = %#v", snapshot.Routes)
	}
	if len(snapshot.Checksums.Groups) != 64 || len(snapshot.Checksums.Routes) != 64 {
		t.Fatalf("checksums = %#v", snapshot.Checksums)
	}
}

func TestReadGroupsRoutesSnapshotRecordsARealEmptyDomain(t *testing.T) {
	path := createLegacyGroupsRoutesSnapshot(t)
	snapshot, err := ReadGroupsRoutesSnapshot(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Groups) != 0 || len(snapshot.Routes) != 0 ||
		snapshot.Checksums.Groups == "" || snapshot.Checksums.Routes == "" {
		t.Fatalf("empty snapshot = %#v", snapshot)
	}
}

func BenchmarkReadGroupsRoutesSnapshotTenThousandRows(b *testing.B) {
	path := filepath.Join(b.TempDir(), "legacy-groups-routes-benchmark.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_server_group (id INTEGER PRIMARY KEY, name TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
		CREATE TABLE v2_server_route (id INTEGER PRIMARY KEY, remarks TEXT NOT NULL, match TEXT NOT NULL, action TEXT NOT NULL, action_value TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
	`); err != nil {
		_ = database.Close()
		b.Fatal(err)
	}
	tx, err := database.Begin()
	if err != nil {
		_ = database.Close()
		b.Fatal(err)
	}
	groupStatement, err := tx.Prepare(`INSERT INTO v2_server_group VALUES (?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		_ = database.Close()
		b.Fatal(err)
	}
	routeStatement, err := tx.Prepare(`INSERT INTO v2_server_route VALUES (?, ?, ?, 'direct', NULL, ?, ?)`)
	if err != nil {
		_ = groupStatement.Close()
		_ = tx.Rollback()
		_ = database.Close()
		b.Fatal(err)
	}
	for index := 1; index <= 5_000; index++ {
		if _, err := groupStatement.Exec(index, "Group "+strconv.Itoa(index), index, index); err != nil {
			b.Fatal(err)
		}
		if _, err := routeStatement.Exec(index, "Route "+strconv.Itoa(index), `["domain:example.test"]`, index, index); err != nil {
			b.Fatal(err)
		}
	}
	_ = groupStatement.Close()
	_ = routeStatement.Close()
	if err := tx.Commit(); err != nil {
		_ = database.Close()
		b.Fatal(err)
	}
	if err := database.Close(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		snapshot, err := ReadGroupsRoutesSnapshot(context.Background(), path)
		if err != nil {
			b.Fatal(err)
		}
		if len(snapshot.Groups)+len(snapshot.Routes) != 10_000 {
			b.Fatalf("rows = %d, want 10000", len(snapshot.Groups)+len(snapshot.Routes))
		}
	}
	b.ReportMetric(10_000, "rows/op")
}

func TestReadGroupsRoutesSnapshotRejectsUnsafeOrLossyRows(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		statement string
		contains  string
	}{
		{name: "non canonical group", statement: `INSERT INTO v2_server_group VALUES (1, ' padded ', 1, 1)`, contains: "group"},
		{name: "invalid match JSON", statement: `INSERT INTO v2_server_route VALUES (1, 'bad json', '{', 'block', NULL, 1, 1)`, contains: "match"},
		{name: "duplicate match", statement: `INSERT INTO v2_server_route VALUES (1, 'duplicate', '["domain:a","domain:a"]', 'direct', NULL, 1, 1)`, contains: "normalization"},
		{name: "missing action value", statement: `INSERT INTO v2_server_route VALUES (1, 'dns', '["domain:a"]', 'dns', NULL, 1, 1)`, contains: "action"},
		{name: "invalid timestamp", statement: `INSERT INTO v2_server_group VALUES (1, 'time', 2, 1)`, contains: "timestamp"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path := createLegacyGroupsRoutesSnapshot(t)
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(scenario.statement); err != nil {
				_ = database.Close()
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadGroupsRoutesSnapshot(context.Background(), path); err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(scenario.contains)) {
				t.Fatalf("ReadGroupsRoutesSnapshot() error = %v, want %q", err, scenario.contains)
			}
		})
	}

	t.Run("view masquerades as route table", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "legacy-view.db")
		database, err := sql.Open("sqlite", "file:"+path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`
			CREATE TABLE v2_server_group (id INTEGER, name TEXT, created_at INTEGER, updated_at INTEGER);
			CREATE TABLE backing_route (id INTEGER, remarks TEXT, match TEXT, action TEXT, action_value TEXT, created_at INTEGER, updated_at INTEGER);
			CREATE VIEW v2_server_route AS SELECT * FROM backing_route;
		`); err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadGroupsRoutesSnapshot(context.Background(), path); err == nil || !strings.Contains(err.Error(), "real table") {
			t.Fatalf("ReadGroupsRoutesSnapshot() error = %v, want real-table rejection", err)
		}
	})
}

func createLegacyGroupsRoutesSnapshot(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-groups-routes.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_server_group (
			id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
			name TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE v2_server_route (
			id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
			remarks TEXT NOT NULL,
			match TEXT NOT NULL,
			action TEXT NOT NULL,
			action_value TEXT,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
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
