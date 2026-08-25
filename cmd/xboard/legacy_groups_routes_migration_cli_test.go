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

func TestRunCommandImportsLegacyGroupsRoutesAfterContentWithIndependentRollback(t *testing.T) {
	directory := t.TempDir()
	sourcePath := createLegacyCLIInput(t, directory)
	source, err := sql.Open("sqlite", "file:"+sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(`
		CREATE TABLE v2_server_group (id INTEGER PRIMARY KEY, name TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
		CREATE TABLE v2_server_route (id INTEGER PRIMARY KEY, remarks TEXT NOT NULL, match TEXT NOT NULL, action TEXT NOT NULL, action_value TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
		INSERT INTO v2_server_group VALUES (5, 'CLI Standard', 100, 110), (8, 'CLI Premium', 120, 130);
		INSERT INTO v2_server_route VALUES (7, 'CLI direct', '["domain:example.test"]', 'direct', NULL, 200, 210);
	`); err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}

	targetPath := filepath.Join(directory, "target.db")
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
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	now := time.Date(2026, 8, 25, 21, 0, 0, 0, time.UTC)
	contentBackup := filepath.Join(directory, "pre-content.xbbackup")
	contentResult := runLegacyMigrationCommand(t, []string{
		"migration", "import-legacy-content", "--source", sourcePath,
		"--backup-output", contentBackup, "--confirm-offline",
	}, now)

	groupsBackup := filepath.Join(directory, "pre-groups-routes.xbbackup")
	var blockedOut, blockedErr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-groups-routes", "--source", sourcePath, "--backup-output", groupsBackup,
	}, &blockedOut, &blockedErr, func() time.Time { return now.Add(time.Minute) })
	if !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") || blockedOut.Len() != 0 {
		t.Fatalf("missing confirmation = handled %v error %v stdout=%q", handled, err, blockedOut.String())
	}

	result := runLegacyGroupsRoutesCommand(t, []string{
		"migration", "import-legacy-groups-routes", "--source", sourcePath,
		"--backup-output", groupsBackup, "--confirm-offline",
	}, now.Add(time.Minute))
	if result.Status != "success" || result.Action != "migration.import-legacy-groups-routes" || result.Result.AlreadyApplied ||
		result.Result.Groups.SourceRows != 2 || result.Result.Routes.SourceRows != 1 || result.Source.SHA256 != contentResult.Source.SHA256 {
		t.Fatalf("migration output = %#v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"CLI Standard", "CLI Premium", "CLI direct", "domain:example.test"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("migration output leaked source value %q: %s", forbidden, encoded)
		}
	}
	if verified, err := backup.Verify(t.Context(), groupsBackup); err != nil || verified != result.RollbackBackup.Manifest {
		t.Fatalf("Verify(rollback) = (%#v, %v)", verified, err)
	}

	target, err = store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	groups, groupsErr := target.ListServerGroups(t.Context())
	routes, routesErr := target.ListRoutingRules(t.Context())
	if closeErr := target.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	inspection, err := sql.Open("sqlite", "file:"+targetPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	var runs int
	runsErr := inspection.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM legacy_migration_runs`).Scan(&runs)
	if closeErr := inspection.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if groupsErr != nil || routesErr != nil || runsErr != nil || len(groups) != 2 || groups[0].ID != 8 || len(routes) != 1 || routes[0].ID != 7 || runs != 2 {
		t.Fatalf("groups=%#v routes=%#v runs=%d errors=(%v,%v,%v)", groups, routes, runs, groupsErr, routesErr, runsErr)
	}

	repeated := runLegacyGroupsRoutesCommand(t, []string{
		"migration", "import-legacy-groups-routes", "--source", sourcePath, "--confirm-offline",
	}, now.Add(2*time.Minute))
	if !repeated.Result.AlreadyApplied || repeated.Result.AppliedAt != now.Add(time.Minute) || repeated.RollbackBackup.SHA256 != result.RollbackBackup.SHA256 {
		t.Fatalf("idempotent output = %#v", repeated)
	}

	restoredPath := filepath.Join(directory, "restored-before-groups.db")
	if _, err := backup.Restore(t.Context(), groupsBackup, restoredPath); err != nil {
		t.Fatal(err)
	}
	restored, err := sql.Open("sqlite", "file:"+restoredPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	var restoredGroups, restoredRoutes, restoredRuns, restoredNotices int
	for query, destination := range map[string]*int{
		`SELECT COUNT(*) FROM server_groups`:         &restoredGroups,
		`SELECT COUNT(*) FROM routing_rules`:         &restoredRoutes,
		`SELECT COUNT(*) FROM legacy_migration_runs`: &restoredRuns,
		`SELECT COUNT(*) FROM notices`:               &restoredNotices,
	} {
		if err := restored.QueryRowContext(t.Context(), query).Scan(destination); err != nil {
			_ = restored.Close()
			t.Fatal(err)
		}
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
	if restoredGroups != 0 || restoredRoutes != 0 || restoredRuns != 1 || restoredNotices != 1 {
		t.Fatalf("restored groups=%d routes=%d runs=%d notices=%d", restoredGroups, restoredRoutes, restoredRuns, restoredNotices)
	}
}

func TestRunGroupsRoutesMigrationRejectsUnsafeArgumentsWithoutWrites(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.db")
	source, err := sql.Open("sqlite", "file:"+sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(`
		CREATE TABLE v2_server_group (id INTEGER, name TEXT, created_at INTEGER, updated_at INTEGER);
		CREATE TABLE v2_server_route (id INTEGER, remarks TEXT, match TEXT, action TEXT, action_value TEXT, created_at INTEGER, updated_at INTEGER);
	`); err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(directory, "target.db")
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
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	for _, arguments := range [][]string{
		{"migration", "import-legacy-groups-routes", "--confirm-offline"},
		{"migration", "import-legacy-groups-routes", "--source", targetPath, "--confirm-offline"},
		{"migration", "import-legacy-groups-routes", "--source", sourcePath, "--confirm-offline"},
		{"migration", "import-legacy-groups-routes", "--source", sourcePath, "unexpected", "--confirm-offline"},
	} {
		var stdout, stderr bytes.Buffer
		handled, err := runCommand(t.Context(), arguments, &stdout, &stderr, time.Now)
		if !handled || err == nil || stdout.Len() != 0 {
			t.Fatalf("runCommand(%q) = handled %v error %v stdout=%q", arguments, handled, err, stdout.String())
		}
	}
	inspection, err := sql.Open("sqlite", "file:"+targetPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	var runs, groups, routes int
	for query, destination := range map[string]*int{
		`SELECT COUNT(*) FROM legacy_migration_runs`: &runs,
		`SELECT COUNT(*) FROM server_groups`:         &groups,
		`SELECT COUNT(*) FROM routing_rules`:         &routes,
	} {
		if err := inspection.QueryRowContext(t.Context(), query).Scan(destination); err != nil {
			_ = inspection.Close()
			t.Fatal(err)
		}
	}
	if err := inspection.Close(); err != nil {
		t.Fatal(err)
	}
	if runs != 0 || groups != 0 || routes != 0 {
		t.Fatalf("unsafe arguments wrote runs=%d groups=%d routes=%d", runs, groups, routes)
	}
}

type legacyGroupsRoutesCommandOutput struct {
	Status string `json:"status"`
	Action string `json:"action"`
	Source struct {
		Path   string `json:"path"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	} `json:"source"`
	RollbackBackup struct {
		Path     string          `json:"path"`
		SHA256   string          `json:"sha256"`
		Manifest backup.Manifest `json:"manifest"`
	} `json:"rollback_backup"`
	Result store.LegacyGroupsRoutesImportReport `json:"result"`
}

func runLegacyGroupsRoutesCommand(t *testing.T, arguments []string, now time.Time) legacyGroupsRoutesCommandOutput {
	t.Helper()
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), arguments, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(%q) = handled %v error %v stderr=%q", arguments, handled, err, stderr.String())
	}
	var output legacyGroupsRoutesCommandOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode migration output %q: %v", stdout.String(), err)
	}
	return output
}
