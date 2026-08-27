package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var (
	migratedTestDatabaseOnce sync.Once
	migratedTestDatabase     []byte
	migratedTestDatabaseErr  error
)

// cloneMigratedTestDatabase keeps every test on an isolated SQLite file while
// avoiding hundreds of identical schema-v1-to-current migrations. newTestStore
// still calls Migrate on each clone, so the production current-schema path is
// exercised and dedicated migration tests can continue to downgrade their own
// private copy without affecting another test.
func cloneMigratedTestDatabase(t testing.TB) string {
	t.Helper()
	migratedTestDatabaseOnce.Do(func() {
		migratedTestDatabase, migratedTestDatabaseErr = createMigratedTestDatabaseTemplate()
	})
	if migratedTestDatabaseErr != nil {
		t.Fatalf("create migrated test database template: %v", migratedTestDatabaseErr)
	}
	path := filepath.Join(t.TempDir(), "xboard-test.db")
	if err := os.WriteFile(path, migratedTestDatabase, 0o600); err != nil {
		t.Fatalf("clone migrated test database template: %v", err)
	}
	return sqliteFileDSN(path)
}

func createMigratedTestDatabaseTemplate() ([]byte, error) {
	directory, err := os.MkdirTemp("", "xboard-go-store-template-")
	if err != nil {
		return nil, fmt.Errorf("create temporary directory: %w", err)
	}
	defer os.RemoveAll(directory)

	path := filepath.Join(directory, "template.db")
	database, err := OpenSQLite(sqliteFileDSN(path))
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		_ = database.Close()
		return nil, err
	}
	if _, err := database.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("checkpoint migrated database template: %w", err)
	}
	if err := database.Close(); err != nil {
		return nil, fmt.Errorf("close migrated database template: %w", err)
	}
	template, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read migrated database template: %w", err)
	}
	return template, nil
}

func sqliteFileDSN(path string) string {
	return "file:" + filepath.ToSlash(path)
}

func TestClonedTestDatabasesAreIsolated(t *testing.T) {
	left := newTestStore(t)
	right := newTestStore(t)
	ctx := context.Background()
	if _, err := left.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "isolated@example.test", PasswordHash: "hash",
	}, time.Unix(1, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	for name, testCase := range map[string]struct {
		database *Store
		expected int
	}{
		"left":  {database: left, expected: 1},
		"right": {database: right, expected: 0},
	} {
		var count int
		if err := testCase.database.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil || count != testCase.expected {
			t.Fatalf("%s cloned user count = %d, error = %v, want %d", name, count, err, testCase.expected)
		}
	}
}
