package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

func TestReplicateCopiesVerifiedArchiveWithoutReplacingDestination(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "source.db")
	database, err := store.OpenSQLite("file:" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(directory, "source.xbbackup")
	created, err := Create(t.Context(), "file:"+databasePath, source, "replication-test", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(directory, "independent", "replica.xbbackup")
	replicated, err := Replicate(t.Context(), source, destination)
	if err != nil {
		t.Fatal(err)
	}
	if replicated.Manifest != created || replicated.Size <= 0 || len(replicated.SHA256) != sha256.Size*2 {
		t.Fatalf("Replicate() = %#v", replicated)
	}
	if verified, err := Verify(t.Context(), destination); err != nil || verified != created {
		t.Fatalf("Verify(replica) = (%#v, %v), want %#v", verified, err, created)
	}
	assertPrivateFile(t, destination)
	if sourceDigest, err := fileSHA256(t.Context(), source); err != nil || sourceDigest != replicated.SHA256 {
		t.Fatalf("source digest = (%q, %v), want %q", sourceDigest, err, replicated.SHA256)
	}
	before, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Replicate(t.Context(), source, destination); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Replicate(existing destination) error = %v", err)
	}
	after, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("existing replica changed: bytes_equal=%t err=%v", bytes.Equal(after, before), err)
	}
}

func TestReplicateRejectsInvalidArchiveAndCancellationWithoutPublishing(t *testing.T) {
	directory := t.TempDir()
	corrupt := filepath.Join(directory, "corrupt.xbbackup")
	if err := os.WriteFile(corrupt, []byte("not a backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(directory, "replica.xbbackup")
	if _, err := Replicate(t.Context(), corrupt, destination); err == nil {
		t.Fatal("Replicate() accepted a corrupt source archive")
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replica after corrupt source: %v", err)
	}
	if runtime.GOOS != "windows" {
		symlink := filepath.Join(directory, "source-link.xbbackup")
		if err := os.Symlink(corrupt, symlink); err != nil {
			t.Fatal(err)
		}
		if _, err := Replicate(t.Context(), symlink, destination); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("Replicate(symlink) error = %v", err)
		}
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Replicate(cancelled, corrupt, destination); !errors.Is(err, context.Canceled) {
		t.Fatalf("Replicate(cancelled) error = %v", err)
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replica after cancellation: %v", err)
	}
}

func TestAttachmentBundleCreateVerifyAndRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "source.db")
	database, err := store.OpenSQLite("file:" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 1, 2, 3, 0, time.UTC)
	admin, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "attachment-backup@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(directory, "attachment-root")
	relative := filepath.ToSlash(filepath.Join("files", "2026", "08", "11111111-1111-4111-8111-111111111111.txt"))
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	content := []byte("immutable attachment backup")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	chunkContent := []byte("resumable upload chunk")
	chunkRelative := filepath.ToSlash(filepath.Join("temporary", "1", "22222222-2222-4222-8222-222222222222", "chunks", "0.part"))
	chunkPath := filepath.Join(root, filepath.FromSlash(chunkRelative))
	if err := os.MkdirAll(filepath.Dir(chunkPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chunkPath, chunkContent, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	chunkDigest := sha256.Sum256(chunkContent)
	raw, err := sql.Open("sqlite", "file:"+databasePath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		INSERT INTO knowledge_attachments
		(uuid, uploader_user_id, original_name, storage_path, mime_type, extension, size, sha256, status, created_at, updated_at)
		VALUES (?, ?, 'manual.txt', ?, 'text/plain; charset=utf-8', 'txt', ?, ?, 'ready', ?, ?)
	`, "11111111-1111-4111-8111-111111111111", admin.ID, relative, len(content), hex.EncodeToString(digest[:]), now.Unix(), now.Unix()); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	result, err := raw.Exec(`
		INSERT INTO knowledge_attachment_uploads
		(uuid, uploader_user_id, draft_token_hash, original_name, declared_size, chunk_size, total_chunks, received_chunks, temporary_path, status, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, 'resume.bin', ?, ?, 1, 1, ?, 'uploading', ?, ?, ?)
	`, "22222222-2222-4222-8222-222222222222", admin.ID, strings.Repeat("a", 64), len(chunkContent), len(chunkContent),
		filepath.ToSlash(filepath.Dir(filepath.Dir(chunkRelative))), now.Add(time.Hour).Unix(), now.Unix(), now.Unix())
	if err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	uploadID, err := result.LastInsertId()
	if err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO knowledge_attachment_chunks (upload_id, chunk_index, size, sha256, created_at) VALUES (?, 0, ?, ?, ?)`, uploadID, len(chunkContent), hex.EncodeToString(chunkDigest[:]), now.Unix()); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(directory, "bundle.xbbackup")
	created, err := Create(ctx, "file:"+databasePath, archivePath, "bundle-test", now, root)
	if err != nil {
		t.Fatal(err)
	}
	if created.FormatVersion != bundleFormatVersion || created.AttachmentCount != 2 || created.AttachmentSize != int64(len(content)+len(chunkContent)) || len(created.AttachmentSHA256) != 64 {
		t.Fatalf("bundle manifest = %#v", created)
	}
	if verified, err := Verify(ctx, archivePath); err != nil || verified != created {
		t.Fatalf("Verify(bundle) = (%#v, %v), want %#v", verified, err, created)
	}

	missingRootDatabase := filepath.Join(directory, "missing-root.db")
	if _, err := Restore(ctx, archivePath, missingRootDatabase); err == nil || !strings.Contains(err.Error(), "attachment") {
		t.Fatalf("Restore(bundle without attachment output) error = %v", err)
	}
	if _, err := os.Lstat(missingRootDatabase); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed bundle restore published a database: %v", err)
	}

	restoredDatabase := filepath.Join(directory, "restored.db")
	restoredRoot := filepath.Join(directory, "restored-attachments")
	if restored, err := Restore(ctx, archivePath, restoredDatabase, restoredRoot); err != nil || restored != created {
		t.Fatalf("Restore(bundle) = (%#v, %v), want %#v", restored, err, created)
	}
	got, err := os.ReadFile(filepath.Join(restoredRoot, filepath.FromSlash(relative)))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("restored attachment = %q, err=%v", got, err)
	}
	gotChunk, err := os.ReadFile(filepath.Join(restoredRoot, filepath.FromSlash(chunkRelative)))
	if err != nil || !bytes.Equal(gotChunk, chunkContent) {
		t.Fatalf("restored resumable chunk = %q, err=%v", gotChunk, err)
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
