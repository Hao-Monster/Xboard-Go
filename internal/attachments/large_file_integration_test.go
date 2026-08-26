package attachments

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestOneGiBAttachmentCompletesWithChunkBoundedMemory(t *testing.T) {
	if os.Getenv("XBOARD_RUN_LARGE_ATTACHMENT_TEST") != "1" {
		t.Skip("set XBOARD_RUN_LARGE_ATTACHMENT_TEST=1 for the 1 GiB streaming integration test")
	}
	const (
		fileSize  = int64(1 << 30)
		chunkSize = int64(5 << 20)
	)
	database, err := store.OpenSQLite("file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "large-attachment.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	admin, err := database.CreateAdminUser(context.Background(), store.CreateAdminUserInput{Email: "large-attachment@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(database, Options{
		Root: filepath.Join(t.TempDir(), "objects"), SigningKey: bytes.Repeat([]byte{0x52}, 32), PanelURL: "https://panel.example.test",
		ChunkSize: chunkSize, MaxFileSize: fileSize, TotalQuota: 2 * fileSize, SignedURLTTL: 2 * time.Hour,
		DraftTTL: 24 * time.Hour, TrashRetention: 7 * 24 * time.Hour, MaxPerArticle: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	upload, err := service.Initialize(context.Background(), admin.ID, InitializeInput{
		OriginalName: "one-gib.bin", Size: fileSize, DraftToken: testDraftToken("8"),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, chunkSize)
	fullDigest := hexSHA256(buffer)
	for index := 0; index < upload.TotalChunks; index++ {
		size := chunkSize
		if index == upload.TotalChunks-1 {
			size = fileSize - chunkSize*int64(upload.TotalChunks-1)
		}
		content := buffer[:size]
		digest := fullDigest
		if size != chunkSize {
			digest = hexSHA256(content)
		}
		if _, err := service.StoreChunk(context.Background(), admin.ID, upload.UUID, index, digest, bytes.NewReader(content), size, now); err != nil {
			t.Fatalf("store chunk %d/%d: %v", index+1, upload.TotalChunks, err)
		}
	}
	attachment, err := service.Complete(context.Background(), admin.ID, upload.UUID, now)
	if err != nil {
		t.Fatal(err)
	}
	if attachment.Size != fileSize || attachment.Status != AttachmentReady {
		t.Fatalf("completed attachment = %#v", attachment)
	}
	file, _, err := service.Open(context.Background(), attachment.UUID)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != fileSize {
		t.Fatalf("completed file size=%d", info.Size())
	}
}
