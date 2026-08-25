package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestCreateVerifyAndRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.db")
	database, err := store.OpenSQLite("file:" + sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 12, 30, 0, 0, time.UTC)
	first, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "backup-first@example.test", PasswordHash: "first-hash",
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(directory, "backups", "xboard.xbbackup")
	created, err := Create(ctx, "file:"+sourcePath, archivePath, "test-revision", now)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.FormatVersion != 1 || created.AppRevision != "test-revision" || created.SchemaVersion <= 0 ||
		created.DatabaseSize <= 0 || len(created.DatabaseSHA256) != 64 || !created.CreatedAt.Equal(now) {
		t.Fatalf("Create() manifest = %#v", created)
	}
	assertPrivateFile(t, archivePath)

	verified, err := Verify(ctx, archivePath)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if verified != created {
		t.Fatalf("Verify() manifest = %#v, want %#v", verified, created)
	}

	second, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "backup-second@example.test", PasswordHash: "second-hash",
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	restoredPath := filepath.Join(directory, "restored", "xboard.db")
	restored, err := Restore(ctx, archivePath, restoredPath)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if restored != created {
		t.Fatalf("Restore() manifest = %#v, want %#v", restored, created)
	}
	assertPrivateFile(t, restoredPath)

	restoredDatabase, err := store.OpenSQLite("file:" + restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restoredDatabase.Close() })
	if got, err := restoredDatabase.GetAdminUser(ctx, first.ID); err != nil || got.Email != first.Email {
		t.Fatalf("restored first user = %#v, err=%v", got, err)
	}
	if _, err := restoredDatabase.GetAdminUser(ctx, second.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("post-backup user is present or returned wrong error: %v", err)
	}
}

func TestRestoreNeverOverwritesExistingDestination(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	archivePath := createTestBackup(t, directory)
	destination := filepath.Join(directory, "existing.db")
	const sentinel = "do-not-overwrite"
	if err := os.WriteFile(destination, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Restore(ctx, archivePath, destination); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Restore(existing) error = %v", err)
	}
	content, err := os.ReadFile(destination)
	if err != nil || string(content) != sentinel {
		t.Fatalf("existing destination changed: content=%q err=%v", content, err)
	}
}

func TestCreateRestrictsExistingOutputDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX permission bits")
	}
	directory := t.TempDir()
	outputDirectory := filepath.Join(directory, "permissive")
	if err := os.Mkdir(outputDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	archivePath := createTestBackup(t, outputDirectory)
	info, err := os.Stat(filepath.Dir(archivePath))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("backup directory permissions = %o, want 700", info.Mode().Perm())
	}
}

func TestVerifyRejectsCorruptionAndUnsafeArchiveEntries(t *testing.T) {
	directory := t.TempDir()
	validPath := createTestBackup(t, directory)
	content, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatal(err)
	}
	content[len(content)/2] ^= 0xff
	corruptPath := filepath.Join(directory, "corrupt.xbbackup")
	if err := os.WriteFile(corruptPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(context.Background(), corruptPath); err == nil {
		t.Fatal("Verify() accepted a corrupted archive")
	}

	for _, name := range []string{"../database.sqlite", "/database.sqlite", "extra.txt", "manifest.json/child"} {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "unsafe.xbbackup")
			writeSingleEntryArchive(t, path, name, []byte("unsafe"))
			if _, err := Verify(context.Background(), path); err == nil {
				t.Fatalf("Verify() accepted archive entry %q", name)
			}
		})
	}
}

func TestCreateRejectsFutureSchemaAndNonFileDSN(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "future.db")
	database, err := sql.Open("sqlite", "file:"+databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE future_data (id INTEGER PRIMARY KEY); PRAGMA user_version = 999;`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), "file:"+databasePath, filepath.Join(directory, "future.xbbackup"), "test", time.Now()); err == nil || !strings.Contains(err.Error(), "unsupported schema") {
		t.Fatalf("Create(future schema) error = %v", err)
	}
	if _, err := Create(context.Background(), "https://example.test/database", filepath.Join(directory, "remote.xbbackup"), "test", time.Now()); err == nil {
		t.Fatal("Create() accepted a non-SQLite file DSN")
	}
}

func TestValidateSQLiteRejectsNonXboardSchema(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "not-xboard.db")
	database, err := sql.Open("sqlite", "file:"+databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE unrelated (id INTEGER PRIMARY KEY); PRAGMA user_version = 23;`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	if _, _, err := validateSQLite(context.Background(), databasePath); err == nil || !strings.Contains(err.Error(), "Xboard schema") {
		t.Fatalf("validateSQLite(non-Xboard schema) error = %v", err)
	}
}

func TestCreateRejectsForeignKeyViolationsAndCancellationLeavesNoOutput(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "invalid.db")
	database, err := store.OpenSQLite("file:" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+databasePath+"?_pragma=foreign_keys(0)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		INSERT INTO admin_sessions (user_id, token_hash, csrf_hash, expires_at, created_at)
		VALUES (999999, 'orphan-token', 'orphan-csrf', 9999999999, 1)
	`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	invalidOutput := filepath.Join(directory, "invalid.xbbackup")
	if _, err := Create(context.Background(), "file:"+databasePath, invalidOutput, "test", time.Now()); err == nil || !strings.Contains(err.Error(), "foreign key") {
		t.Fatalf("Create(foreign key violation) error = %v", err)
	}
	if _, err := os.Lstat(invalidOutput); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed backup left output behind: %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cancelledOutput := filepath.Join(directory, "cancelled.xbbackup")
	if _, err := Create(cancelled, "file:"+databasePath, cancelledOutput, "test", time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Create(cancelled) error = %v", err)
	}
	if _, err := os.Lstat(cancelledOutput); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled backup left output behind: %v", err)
	}
}

func TestRestoreCorruptionLeavesNoDatabaseOrTemporaryFile(t *testing.T) {
	directory := t.TempDir()
	validPath := createTestBackup(t, directory)
	content, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatal(err)
	}
	content[len(content)-1] ^= 0xff
	corruptPath := filepath.Join(directory, "corrupt-restore.xbbackup")
	if err := os.WriteFile(corruptPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	restoreDirectory := filepath.Join(directory, "restore")
	destination := filepath.Join(restoreDirectory, "xboard.db")
	if _, err := Restore(context.Background(), corruptPath, destination); err == nil {
		t.Fatal("Restore() accepted a corrupted archive")
	}
	entries, err := os.ReadDir(restoreDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed restore left files behind: %#v", entries)
	}
}

func createTestBackup(t *testing.T, directory string) string {
	t.Helper()
	databasePath := filepath.Join(directory, "source.db")
	database, err := store.OpenSQLite("file:" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(directory, "valid.xbbackup")
	if _, err := Create(context.Background(), "file:"+databasePath, archivePath, "test", time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	return archivePath
}

func writeSingleEntryArchive(t *testing.T, path, name string, content []byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertPrivateFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("%s permissions = %o, want 600", path, info.Mode().Perm())
	}
}
