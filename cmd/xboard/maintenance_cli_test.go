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

type maintenanceCommandOutput struct {
	Status string                    `json:"status"`
	Action string                    `json:"action"`
	AsOf   time.Time                 `json:"as_of"`
	Limit  int                       `json:"limit"`
	Result maintenance.CleanupResult `json:"result"`
}
