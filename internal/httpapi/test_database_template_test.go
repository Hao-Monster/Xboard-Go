package httpapi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

var (
	httpAPITestDatabaseOnce sync.Once
	httpAPITestDatabase     []byte
	httpAPITestDatabaseErr  error
)

func cloneHTTPAPITestDatabase(t testing.TB) *store.Store {
	t.Helper()
	httpAPITestDatabaseOnce.Do(func() {
		httpAPITestDatabase, httpAPITestDatabaseErr = createHTTPAPITestDatabaseTemplate()
	})
	if httpAPITestDatabaseErr != nil {
		t.Fatalf("create HTTP API test database template: %v", httpAPITestDatabaseErr)
	}
	directory, err := os.MkdirTemp("", "xboard-go-httpapi-test-")
	if err != nil {
		t.Fatalf("create HTTP API test database directory: %v", err)
	}
	path := filepath.Join(directory, "xboard-httpapi-test.db")
	if err := os.WriteFile(path, httpAPITestDatabase, 0o600); err != nil {
		_ = os.RemoveAll(directory)
		t.Fatalf("clone HTTP API test database template: %v", err)
	}
	database, err := store.OpenSQLite(sqliteHTTPAPITestDSN(path))
	if err != nil {
		_ = os.RemoveAll(directory)
		t.Fatalf("open HTTP API test database clone: %v", err)
	}
	if err := database.Migrate(context.Background()); err != nil {
		_ = database.Close()
		_ = os.RemoveAll(directory)
		t.Fatalf("validate HTTP API test database clone: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close HTTP API test database clone: %v", err)
		}
		removeErr := os.RemoveAll(directory)
		if errors.Is(removeErr, syscall.ENOTEMPTY) {
			// SQLite may finish unlinking a transient journal between directory
			// enumeration and removal. Retry once, but keep persistent writers visible.
			removeErr = os.RemoveAll(directory)
		}
		if removeErr != nil {
			t.Errorf("remove HTTP API test database directory: %v", removeErr)
		}
	})
	return database
}

func createHTTPAPITestDatabaseTemplate() ([]byte, error) {
	directory, err := os.MkdirTemp("", "xboard-go-httpapi-template-")
	if err != nil {
		return nil, fmt.Errorf("create temporary directory: %w", err)
	}
	defer os.RemoveAll(directory)

	path := filepath.Join(directory, "template.db")
	database, err := store.OpenSQLite(sqliteHTTPAPITestDSN(path))
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		_ = database.Close()
		return nil, err
	}
	for index := 1; index <= 9; index++ {
		if _, err := database.CreateServerGroup(ctx, fmt.Sprintf("Test group %d", index), fixedNow()); err != nil {
			_ = database.Close()
			return nil, fmt.Errorf("create template server group %d: %w", index, err)
		}
	}
	hasher := newHTTPAPITestPasswordHasher()
	passwordHash, err := hasher.Hash("admin-password-123")
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("hash template administrator password: %w", err)
	}
	if _, err := database.BootstrapAdmin(ctx, "admin@example.test", passwordHash, fixedNow()); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("bootstrap template administrator: %w", err)
	}
	if err := database.Close(); err != nil {
		return nil, fmt.Errorf("close HTTP API test database template: %w", err)
	}
	template, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read HTTP API test database template: %w", err)
	}
	return template, nil
}

func newHTTPAPITestPasswordHasher() security.PasswordHasher {
	return security.NewPasswordHasher(security.PasswordParams{
		MemoryKiB: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	})
}

func sqliteHTTPAPITestDSN(path string) string {
	return "file:" + filepath.ToSlash(path)
}

func TestHTTPAPITestDatabaseClonesAreIsolated(t *testing.T) {
	left := cloneHTTPAPITestDatabase(t)
	right := cloneHTTPAPITestDatabase(t)
	ctx := context.Background()
	if _, err := left.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "http-isolated@example.test", PasswordHash: "hash",
	}, fixedNow()); err != nil {
		t.Fatal(err)
	}
	if _, err := right.FindUserByEmail(ctx, "http-isolated@example.test"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("right clone lookup error = %v, want ErrNotFound", err)
	}
}
