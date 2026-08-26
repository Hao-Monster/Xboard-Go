package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestRunCommandImportsLegacyKnowledgeAfterPriorSlicesWithIndependentRollback(t *testing.T) {
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
		CREATE TABLE v2_knowledge (id INTEGER PRIMARY KEY, language TEXT NOT NULL, category TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL, sort INTEGER, show INTEGER NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
		CREATE TABLE v2_knowledge_attachment (id INTEGER PRIMARY KEY, uuid TEXT, knowledge_id INTEGER, uploader_user_id INTEGER, draft_token TEXT, original_name TEXT, storage_path TEXT, mime_type TEXT, extension TEXT, size INTEGER, sha256 TEXT, status TEXT, created_at INTEGER, updated_at INTEGER, deleted_at INTEGER);
		CREATE TABLE v2_knowledge_attachment_upload (id INTEGER PRIMARY KEY, uuid TEXT, uploader_user_id INTEGER, draft_token TEXT, original_name TEXT, declared_size INTEGER, expected_sha256 TEXT, chunk_size INTEGER, total_chunks INTEGER, received_chunks INTEGER, temporary_path TEXT, status TEXT, expires_at INTEGER, created_at INTEGER, updated_at INTEGER);
		INSERT INTO v2_knowledge VALUES
			(11, 'en-US', 'CLI General', 'CLI guide', '# CLI private body', NULL, 1, 300, 310),
			(19, 'zh-CN', 'CLI 指南', 'CLI 客户端', 'CLI 正文', 9, 0, 320, 330);
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
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	now := time.Date(2026, 8, 25, 22, 0, 0, 0, time.UTC)
	content := runLegacyMigrationCommand(t, []string{"migration", "import-legacy-content", "--source", sourcePath, "--backup-output", filepath.Join(directory, "pre-content.xbbackup"), "--confirm-offline"}, now)
	groups := runLegacyGroupsRoutesCommand(t, []string{"migration", "import-legacy-groups-routes", "--source", sourcePath, "--backup-output", filepath.Join(directory, "pre-groups.xbbackup"), "--confirm-offline"}, now.Add(time.Minute))
	if content.Source.SHA256 != groups.Source.SHA256 {
		t.Fatal("prior slices did not bind the same source")
	}

	backupPath := filepath.Join(directory, "pre-knowledge.xbbackup")
	var blockedOut, blockedErr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{"migration", "import-legacy-knowledge", "--source", sourcePath, "--backup-output", backupPath}, &blockedOut, &blockedErr, func() time.Time { return now.Add(2 * time.Minute) })
	if !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") || blockedOut.Len() != 0 {
		t.Fatalf("missing confirmation = handled %v error %v stdout=%q", handled, err, blockedOut.String())
	}

	result := runLegacyKnowledgeCommand(t, []string{"migration", "import-legacy-knowledge", "--source", sourcePath, "--backup-output", backupPath, "--confirm-offline"}, now.Add(2*time.Minute))
	if result.Status != "success" || result.Action != "migration.import-legacy-knowledge" || result.Result.AlreadyApplied || result.Result.Articles.SourceRows != 2 || result.Source.SHA256 != content.Source.SHA256 {
		t.Fatalf("migration output = %#v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"CLI guide", "CLI 客户端", "CLI private body", "CLI 正文"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("migration output leaked source value %q: %s", forbidden, encoded)
		}
	}
	if verified, err := backup.Verify(t.Context(), backupPath); err != nil || verified != result.RollbackBackup.Manifest {
		t.Fatalf("Verify(rollback) = (%#v, %v)", verified, err)
	}

	target, err = store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	articles, articleErr := target.ListKnowledge(t.Context())
	if closeErr := target.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if articleErr != nil || len(articles) != 2 || articles[0].ID != 11 || articles[0].SortPosition != 0 || articles[1].ID != 19 {
		t.Fatalf("articles=%#v error=%v", articles, articleErr)
	}

	repeated := runLegacyKnowledgeCommand(t, []string{"migration", "import-legacy-knowledge", "--source", sourcePath, "--confirm-offline"}, now.Add(3*time.Minute))
	if !repeated.Result.AlreadyApplied || repeated.Result.AppliedAt != now.Add(2*time.Minute) || repeated.RollbackBackup.SHA256 != result.RollbackBackup.SHA256 {
		t.Fatalf("idempotent output = %#v", repeated)
	}

	restoredPath := filepath.Join(directory, "restored-before-knowledge.db")
	if _, err := backup.Restore(t.Context(), backupPath, restoredPath); err != nil {
		t.Fatal(err)
	}
	restored, err := sql.Open("sqlite", "file:"+restoredPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	var restoredKnowledge, restoredGroups, restoredRoutes, restoredRuns int
	for query, destination := range map[string]*int{
		`SELECT COUNT(*) FROM knowledge`:             &restoredKnowledge,
		`SELECT COUNT(*) FROM server_groups`:         &restoredGroups,
		`SELECT COUNT(*) FROM routing_rules`:         &restoredRoutes,
		`SELECT COUNT(*) FROM legacy_migration_runs`: &restoredRuns,
	} {
		if err := restored.QueryRow(query).Scan(destination); err != nil {
			_ = restored.Close()
			t.Fatal(err)
		}
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
	if restoredKnowledge != 0 || restoredGroups != 2 || restoredRoutes != 1 || restoredRuns != 2 {
		t.Fatalf("restored knowledge=%d groups=%d routes=%d runs=%d", restoredKnowledge, restoredGroups, restoredRoutes, restoredRuns)
	}
}

func TestRunKnowledgeMigrationCopiesAndVerifiesNonEmptyAttachments(t *testing.T) {
	directory := t.TempDir()
	sourcePath := createLegacyCLIInput(t, directory)
	source, err := sql.Open("sqlite", "file:"+sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("legacy attachment bytes")
	digest := sha256.Sum256(content)
	const attachmentUUID = "22222222-2222-4222-8222-222222222222"
	const activeUploadUUID = "33333333-3333-4333-8333-333333333333"
	const expiredUploadUUID = "44444444-4444-4444-8444-444444444444"
	relative := filepath.ToSlash(filepath.Join("files", "2026", "08", attachmentUUID+".txt"))
	if _, err := source.Exec(`
		CREATE TABLE v2_knowledge (id INTEGER PRIMARY KEY, language TEXT, category TEXT, title TEXT, body TEXT, sort INTEGER, show INTEGER, created_at INTEGER, updated_at INTEGER);
		CREATE TABLE v2_knowledge_attachment (id INTEGER PRIMARY KEY, uuid TEXT, knowledge_id INTEGER, uploader_user_id INTEGER, draft_token TEXT, original_name TEXT, storage_path TEXT, mime_type TEXT, extension TEXT, size INTEGER, sha256 TEXT, status TEXT, created_at INTEGER, updated_at INTEGER, deleted_at INTEGER);
		CREATE TABLE v2_knowledge_attachment_upload (id INTEGER PRIMARY KEY, uuid TEXT, uploader_user_id INTEGER, draft_token TEXT, original_name TEXT, declared_size INTEGER, expected_sha256 TEXT, chunk_size INTEGER, total_chunks INTEGER, received_chunks INTEGER, temporary_path TEXT, status TEXT, expires_at INTEGER, created_at INTEGER, updated_at INTEGER);
	`); err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	if _, err := source.Exec(`INSERT INTO v2_knowledge VALUES (41, 'en-US', 'General', 'Attachment guide', ?, 0, 1, 100, 110)`, "[legacy](knowledge-attachment://"+attachmentUUID+")"); err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	if _, err := source.Exec(`
		INSERT INTO v2_knowledge_attachment
		(id, uuid, knowledge_id, uploader_user_id, draft_token, original_name, storage_path, mime_type, extension, size, sha256, status, created_at, updated_at, deleted_at)
		VALUES (51, ?, 41, 1, NULL, 'legacy.txt', ?, 'text/plain', 'txt', ?, ?, 'ready', 100, 110, NULL)
	`, attachmentUUID, relative, len(content), hex.EncodeToString(digest[:])); err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	activeContent := []byte("abcdefgh")
	activeDigest := sha256.Sum256(activeContent)
	if _, err := source.Exec(`
		INSERT INTO v2_knowledge_attachment_upload
		(id, uuid, uploader_user_id, draft_token, original_name, declared_size, expected_sha256, chunk_size,
		 total_chunks, received_chunks, temporary_path, status, expires_at, created_at, updated_at)
		VALUES
		(61, ?, 1, ?, 'legacy.txt', ?, ?, ?, 1, 1, ?, 'completed', 500, 100, 110),
		(62, ?, 1, ?, '多分片.bin', 8, ?, 4, 2, 2, ?, 'uploading', 500, 100, 120),
		(63, ?, 1, ?, 'expired.bin', 4, NULL, 4, 1, 1, ?, 'expired', 150, 100, 120)
	`, attachmentUUID, strings.Repeat("a", 64), len(content), hex.EncodeToString(digest[:]), len(content),
		"temporary/1/"+attachmentUUID, activeUploadUUID, strings.Repeat("b", 64), hex.EncodeToString(activeDigest[:]),
		"temporary/1/"+activeUploadUUID, expiredUploadUUID, strings.Repeat("c", 64), "temporary/1/"+expiredUploadUUID); err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	sourceRoot := filepath.Join(directory, "legacy-attachments")
	sourceFile := filepath.Join(sourceRoot, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(sourceFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceFile, content, 0o600); err != nil {
		t.Fatal(err)
	}
	for uploadUUID, chunks := range map[string][][]byte{
		activeUploadUUID:  {activeContent[:4], activeContent[4:]},
		expiredUploadUUID: {[]byte("gone")},
	} {
		for index, chunk := range chunks {
			path := filepath.Join(sourceRoot, "temporary", "1", uploadUUID, "chunks", fmt.Sprintf("%d.part", index))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, chunk, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}

	targetPath := filepath.Join(directory, "target.db")
	target, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := target.CreateAdminUser(t.Context(), store.CreateAdminUserInput{Email: "legacy-owner@example.test", PasswordHash: "hash"}, time.Unix(50, 0)); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	targetRoot := filepath.Join(directory, "target-attachments")
	result := runLegacyKnowledgeCommand(t, []string{
		"migration", "import-legacy-knowledge", "--source", sourcePath,
		"--source-attachment-root", sourceRoot, "--target-attachment-root", targetRoot,
		"--backup-output", filepath.Join(directory, "pre-knowledge.xbbackup"), "--confirm-offline",
	}, time.Unix(200, 0))
	if result.Result.Attachments.SourceRows != 1 || result.Result.Attachments.SourceChecksum != result.Result.Attachments.TargetChecksum ||
		result.Result.Uploads.SourceRows != 3 || result.Result.Uploads.SourceChecksum != result.Result.Uploads.TargetChecksum ||
		result.RollbackBackup.Manifest.FormatVersion != 2 {
		t.Fatalf("attachment migration result = %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(targetRoot, filepath.FromSlash(relative)))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("migrated attachment = %q, err=%v", got, err)
	}
	for uploadUUID, chunks := range map[string][][]byte{
		activeUploadUUID:  {activeContent[:4], activeContent[4:]},
		expiredUploadUUID: {[]byte("gone")},
	} {
		for index, want := range chunks {
			got, err := os.ReadFile(filepath.Join(targetRoot, "temporary", "1", uploadUUID, "chunks", fmt.Sprintf("%d.part", index)))
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("migrated upload %s chunk %d = %q, err=%v", uploadUUID, index, got, err)
			}
		}
	}
	raw, err := sql.Open("sqlite", "file:"+targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var uploadRows, chunkRows int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM knowledge_attachment_uploads`).Scan(&uploadRows); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM knowledge_attachment_chunks`).Scan(&chunkRows); err != nil {
		t.Fatal(err)
	}
	if uploadRows != 3 || chunkRows != 3 {
		t.Fatalf("migrated upload rows=%d chunk rows=%d", uploadRows, chunkRows)
	}
}

func TestRunKnowledgeMigrationRejectsUnsafeArgumentsWithoutWrites(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.db")
	source, err := sql.Open("sqlite", "file:"+sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(`
		CREATE TABLE v2_knowledge (id INTEGER, language TEXT, category TEXT, title TEXT, body TEXT, sort INTEGER, show INTEGER, created_at INTEGER, updated_at INTEGER);
		CREATE TABLE v2_knowledge_attachment (id INTEGER, uuid TEXT, knowledge_id INTEGER, uploader_user_id INTEGER, draft_token TEXT, original_name TEXT, storage_path TEXT, mime_type TEXT, extension TEXT, size INTEGER, sha256 TEXT, status TEXT, created_at INTEGER, updated_at INTEGER, deleted_at INTEGER);
		CREATE TABLE v2_knowledge_attachment_upload (id INTEGER, uuid TEXT, uploader_user_id INTEGER, draft_token TEXT, original_name TEXT, declared_size INTEGER, expected_sha256 TEXT, chunk_size INTEGER, total_chunks INTEGER, received_chunks INTEGER, temporary_path TEXT, status TEXT, expires_at INTEGER, created_at INTEGER, updated_at INTEGER);
	`); err != nil {
		t.Fatal(err)
	}
	_ = source.Close()
	targetPath := filepath.Join(directory, "target.db")
	target, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = target.Close()
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	for _, arguments := range [][]string{
		{"migration", "import-legacy-knowledge", "--confirm-offline"},
		{"migration", "import-legacy-knowledge", "--source", targetPath, "--confirm-offline"},
		{"migration", "import-legacy-knowledge", "--source", sourcePath, "--confirm-offline"},
		{"migration", "import-legacy-knowledge", "--source", sourcePath, "unexpected", "--confirm-offline"},
	} {
		var stdout, stderr bytes.Buffer
		handled, err := runCommand(t.Context(), arguments, &stdout, &stderr, time.Now)
		if !handled || err == nil || stdout.Len() != 0 {
			t.Fatalf("runCommand(%q) = handled %v error %v stdout=%q", arguments, handled, err, stdout.String())
		}
	}
}

type legacyKnowledgeCommandOutput struct {
	Status         string                            `json:"status"`
	Action         string                            `json:"action"`
	Source         legacyMigrationSourceResult       `json:"source"`
	RollbackBackup legacyMigrationBackupResult       `json:"rollback_backup"`
	Result         store.LegacyKnowledgeImportReport `json:"result"`
}

func runLegacyKnowledgeCommand(t *testing.T, arguments []string, now time.Time) legacyKnowledgeCommandOutput {
	t.Helper()
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), arguments, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(%q) = handled %v error %v stderr=%q", arguments, handled, err, stderr.String())
	}
	var output legacyKnowledgeCommandOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode migration output %q: %v", stdout.String(), err)
	}
	return output
}
