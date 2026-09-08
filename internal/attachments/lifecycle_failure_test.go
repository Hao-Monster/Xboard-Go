package attachments

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestDropDraftRestoresObjectWhenSQLiteDeleteFails(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 16, 0, 0, 0, time.UTC)
	databasePath := filepath.Join(t.TempDir(), "attachments.db")
	dsn := "file:" + filepath.ToSlash(databasePath)
	database, err := store.OpenSQLite(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "attachment-rollback@example.test", PasswordHash: "test-hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(database, Options{
		Root: filepath.Join(t.TempDir(), "private-attachments"), SigningKey: bytes.Repeat([]byte{0x42}, 32),
		PanelURL: "https://panel.example.test", ChunkSize: 4, MaxFileSize: 64, TotalQuota: 1 << 20,
		SignedURLTTL: 2 * time.Hour, DraftTTL: 24 * time.Hour, TrashRetention: 7 * 24 * time.Hour, MaxPerArticle: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	draftToken := testDraftToken("a")
	content := []byte("keep this object")
	attachment := uploadTestAttachment(t, service, admin.ID, draftToken, "rollback.txt", content, now)
	objectPath := filepath.Join(service.root, filepath.FromSlash(attachment.StoragePath))

	injector, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = injector.Close() })
	if _, err := injector.ExecContext(ctx, `
		CREATE TRIGGER attachment_delete_failure
		BEFORE DELETE ON knowledge_attachments
		BEGIN
			SELECT RAISE(ABORT, 'injected attachment delete failure');
		END`); err != nil {
		t.Fatal(err)
	}

	if err := service.DropDraft(ctx, admin.ID, attachment.UUID, draftToken); err == nil {
		t.Fatal("DropDraft unexpectedly succeeded while SQLite delete trigger was active")
	} else if !strings.Contains(err.Error(), "injected attachment delete failure") {
		t.Fatalf("DropDraft error = %v, want injected SQLite delete failure", err)
	}
	stored, err := os.ReadFile(objectPath)
	if err != nil {
		t.Fatalf("object after failed delete: %v", err)
	}
	if !bytes.Equal(stored, content) {
		t.Fatalf("object after failed delete = %q, want %q", stored, content)
	}
	restored, err := database.GetDraftKnowledgeAttachment(ctx, admin.ID, attachment.UUID)
	if err != nil {
		t.Fatalf("metadata after failed delete: %v", err)
	}
	if restored.UUID != attachment.UUID || restored.StoragePath != attachment.StoragePath || restored.Size != int64(len(content)) {
		t.Fatalf("metadata after failed delete = %#v, want original attachment", restored)
	}

	if _, err := injector.ExecContext(ctx, `DROP TRIGGER attachment_delete_failure`); err != nil {
		t.Fatal(err)
	}
	if err := service.DropDraft(ctx, admin.ID, attachment.UUID, draftToken); err != nil {
		t.Fatalf("DropDraft retry error = %v", err)
	}
	if _, err := os.Stat(objectPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("object after successful retry stat error = %v, want not exist", err)
	}
	if _, err := database.GetDraftKnowledgeAttachment(ctx, admin.ID, attachment.UUID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("metadata after successful retry error = %v, want not found", err)
	}
}
