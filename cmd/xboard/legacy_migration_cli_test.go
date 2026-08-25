package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestRunCommandImportsLegacyContentWithVerifiedRollbackAndIdempotency(t *testing.T) {
	directory := t.TempDir()
	sourcePath := createLegacyCLIInput(t, directory)
	targetPath := filepath.Join(directory, "target.db")
	rollbackPath := filepath.Join(directory, "pre-import.xbbackup")
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
	now := time.Date(2026, 8, 25, 19, 0, 0, 0, time.UTC)

	var blockedOut, blockedErr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{"migration", "import-legacy-content", "--source", sourcePath, "--backup-output", rollbackPath}, &blockedOut, &blockedErr, func() time.Time { return now })
	if !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") || blockedOut.Len() != 0 {
		t.Fatalf("missing confirmation = handled %v error %v stdout=%q", handled, err, blockedOut.String())
	}
	if _, err := os.Lstat(rollbackPath); !os.IsNotExist(err) {
		t.Fatalf("blocked import created backup: %v", err)
	}

	result := runLegacyMigrationCommand(t, []string{
		"migration", "import-legacy-content", "--source", sourcePath,
		"--backup-output", rollbackPath, "--confirm-offline",
	}, now)
	if result.Status != "success" || result.Action != "migration.import-legacy-content" || result.Result.AlreadyApplied || result.Result.Notices.SourceRows != 1 || result.RollbackBackup.Manifest.SchemaVersion != store.CurrentSchemaVersion() {
		t.Fatalf("migration output = %#v", result)
	}
	if result.Source.Path != sourcePath || len(result.Source.SHA256) != 64 || result.RollbackBackup.Path != rollbackPath || len(result.RollbackBackup.SHA256) != 64 {
		t.Fatalf("migration identities = %#v", result)
	}
	redacted, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"CLI Legacy", "CLI notice", "CLI body", "excluded-secret", "/guide/73/karing"} {
		if strings.Contains(string(redacted), forbidden) {
			t.Fatalf("migration output leaked source value %q: %s", forbidden, redacted)
		}
	}
	if verified, err := backup.Verify(t.Context(), rollbackPath); err != nil || verified != result.RollbackBackup.Manifest {
		t.Fatalf("Verify(rollback) = (%#v, %v)", verified, err)
	}

	target, err = store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	settings, settingsErr := target.GetSiteSettings(t.Context())
	notices, noticesErr := target.ListNotices(t.Context())
	catalog, catalogErr := target.GetClientCatalogConfig(t.Context())
	if closeErr := target.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if settingsErr != nil || noticesErr != nil || catalogErr != nil || settings.AppName != "CLI Legacy" || len(notices) != 1 || notices[0].ID != 73 || len(catalog.Links) != 1 {
		t.Fatalf("target settings=%#v notices=%#v catalog=%#v errors=(%v,%v,%v)", settings, notices, catalog, settingsErr, noticesErr, catalogErr)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(targetPath, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	repeated := runLegacyMigrationCommand(t, []string{
		"migration", "import-legacy-content", "--source", sourcePath, "--confirm-offline",
	}, now.Add(time.Minute))
	if !repeated.Result.AlreadyApplied || repeated.Result.AppliedAt != now || repeated.RollbackBackup.SHA256 != result.RollbackBackup.SHA256 {
		t.Fatalf("idempotent output = %#v", repeated)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(targetPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("idempotent rerun target permissions = %o, want 600", info.Mode().Perm())
		}
	}

	restoredPath := filepath.Join(directory, "restored-pre-import.db")
	if _, err := backup.Restore(t.Context(), rollbackPath, restoredPath); err != nil {
		t.Fatal(err)
	}
	restored, err := sql.Open("sqlite", "file:"+restoredPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	var restoredNotices, restoredRuns int
	if err := restored.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM notices`).Scan(&restoredNotices); err != nil {
		_ = restored.Close()
		t.Fatal(err)
	}
	if err := restored.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM legacy_migration_runs`).Scan(&restoredRuns); err != nil {
		_ = restored.Close()
		t.Fatal(err)
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
	if restoredNotices != 0 || restoredRuns != 0 {
		t.Fatalf("rollback snapshot notices=%d runs=%d", restoredNotices, restoredRuns)
	}
}

func TestRunCommandLegacyMigrationRejectsUnsafeArgumentsWithoutWrites(t *testing.T) {
	directory := t.TempDir()
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
	now := time.Date(2026, 8, 25, 19, 0, 0, 0, time.UTC)
	for _, arguments := range [][]string{
		{"migration"},
		{"migration", "unknown"},
		{"migration", "import-legacy-content", "--confirm-offline"},
		{"migration", "import-legacy-content", "--source", targetPath, "--confirm-offline"},
		{"migration", "import-legacy-content", "--source", "missing.db", "--confirm-offline"},
		{"migration", "import-legacy-content", "unexpected", "--confirm-offline"},
	} {
		var stdout, stderr bytes.Buffer
		handled, err := runCommand(t.Context(), arguments, &stdout, &stderr, func() time.Time { return now })
		if !handled || err == nil || stdout.Len() != 0 {
			t.Fatalf("runCommand(%q) = handled %v error %v stdout=%q", arguments, handled, err, stdout.String())
		}
	}
}

type legacyMigrationCommandOutput struct {
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
	Result store.LegacyContentImportReport `json:"result"`
}

func runLegacyMigrationCommand(t *testing.T, arguments []string, now time.Time) legacyMigrationCommandOutput {
	t.Helper()
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), arguments, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(%q) = handled %v error %v stderr=%q", arguments, handled, err, stderr.String())
	}
	var output legacyMigrationCommandOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode migration output %q: %v", stdout.String(), err)
	}
	return output
}

func createLegacyCLIInput(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "legacy.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_settings (id INTEGER PRIMARY KEY, "group" TEXT, type TEXT, name TEXT NOT NULL UNIQUE, value TEXT, created_at DATETIME, updated_at DATETIME);
		CREATE TABLE v2_notice (id INTEGER PRIMARY KEY, title TEXT NOT NULL, content TEXT NOT NULL, show INTEGER NOT NULL, img_url TEXT, tags TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, sort INTEGER);
		INSERT INTO v2_settings (name, value) VALUES
			('app_name', 'CLI Legacy'),
			('client_catalog_links', '{"karing":{"android":{"tutorial":"/guide/73/karing"}}}'),
			('server_token', 'excluded-secret');
		INSERT INTO v2_notice (id, sort, title, content, show, tags, created_at, updated_at)
		VALUES (73, 1, 'CLI notice', 'CLI body', 1, '["migration"]', 100, 101);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
