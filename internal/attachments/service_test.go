package attachments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestUploadLifecycleIsVerifiedIdempotentAndPrivate(t *testing.T) {
	service, database, adminID, now := newAttachmentTestService(t, 32)
	content := []byte("abcdefgh")
	wholeHash := hexSHA256(content)
	upload, err := service.Initialize(context.Background(), adminID, InitializeInput{
		OriginalName: "客户手册.txt", Size: int64(len(content)), DraftToken: testDraftToken("a"), SHA256: wholeHash,
	}, now)
	if err != nil || upload.TotalChunks != 2 || upload.ReceivedChunks != 0 || upload.Status != UploadInitialized {
		t.Fatalf("Initialize() upload=%#v error=%v", upload, err)
	}
	if filepath.IsAbs(upload.TemporaryPath) || upload.TemporaryPath == "" {
		t.Fatalf("temporary path must be private relative storage: %q", upload.TemporaryPath)
	}

	first := content[:4]
	chunk, err := service.StoreChunk(context.Background(), adminID, upload.UUID, 0, hexSHA256(first), bytes.NewReader(first), int64(len(first)), now)
	if err != nil || chunk.Idempotent || chunk.ReceivedChunks != 1 || chunk.ReadyToComplete {
		t.Fatalf("StoreChunk(first) result=%#v error=%v", chunk, err)
	}
	chunk, err = service.StoreChunk(context.Background(), adminID, upload.UUID, 0, hexSHA256(first), bytes.NewReader(first), int64(len(first)), now)
	if err != nil || !chunk.Idempotent || chunk.ReceivedChunks != 1 {
		t.Fatalf("StoreChunk(retry) result=%#v error=%v", chunk, err)
	}
	if _, err := service.StoreChunk(context.Background(), adminID, upload.UUID, 0, hexSHA256([]byte("WXYZ")), bytes.NewReader([]byte("WXYZ")), 4, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting chunk error=%v, want ErrConflict", err)
	}
	second := content[4:]
	chunk, err = service.StoreChunk(context.Background(), adminID, upload.UUID, 1, hexSHA256(second), bytes.NewReader(second), int64(len(second)), now)
	if err != nil || !chunk.ReadyToComplete || chunk.ReceivedChunks != 2 {
		t.Fatalf("StoreChunk(second) result=%#v error=%v", chunk, err)
	}

	attachment, err := service.Complete(context.Background(), adminID, upload.UUID, now)
	if err != nil || attachment.UUID != upload.UUID || attachment.Status != AttachmentReady || attachment.Size != int64(len(content)) || attachment.SHA256 != wholeHash || attachment.MIMEType != "text/plain; charset=utf-8" {
		t.Fatalf("Complete() attachment=%#v error=%v", attachment, err)
	}
	again, err := service.Complete(context.Background(), adminID, upload.UUID, now.Add(time.Second))
	if err != nil || again.ID != attachment.ID {
		t.Fatalf("Complete(idempotent) attachment=%#v error=%v", again, err)
	}
	reader, metadata, err := service.Open(context.Background(), attachment.UUID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	stored, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(stored, content) || metadata.StoragePath == "" {
		t.Fatalf("Open() content=%q metadata=%#v error=%v", stored, metadata, err)
	}
	if _, err := os.Stat(filepath.Join(service.root, metadata.StoragePath)); err != nil {
		t.Fatalf("completed object missing: %v", err)
	}
	status, err := database.GetKnowledgeAttachmentUpload(context.Background(), adminID, upload.UUID, now)
	if err != nil || status.Status != UploadCompleted || status.ReceivedChunks != status.TotalChunks {
		t.Fatalf("stored upload status=%#v error=%v", status, err)
	}
}

func TestStorageRootRejectsFilesystemRootAndSymlinks(t *testing.T) {
	volumeRoot := filepath.Clean(filepath.VolumeName(t.TempDir()) + string(filepath.Separator))
	if _, err := secureStorageRoot(volumeRoot); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("secureStorageRoot(filesystem root) error = %v, want ErrInvalidInput", err)
	}

	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err == nil {
		if _, err := secureStorageRoot(link); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("secureStorageRoot(symlink) error = %v, want ErrInvalidInput", err)
		}
	}

	regularFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(regularFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := secureStorageRoot(regularFile); err == nil {
		t.Fatal("secureStorageRoot(regular file) unexpectedly succeeded")
	}
}

func TestSafePathRejectsTraversalAndNestedSymlinks(t *testing.T) {
	service, _, _, _ := newAttachmentTestService(t, 32)
	for _, candidate := range []string{"../outside", "objects/../../outside", "/absolute", `objects\escape`} {
		if _, err := service.safePath(candidate); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("safePath(%q) error = %v, want ErrInvalidInput", candidate, err)
		}
	}

	inside := filepath.Join(service.root, "objects")
	if err := os.MkdirAll(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	link := filepath.Join(inside, "redirect")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("nested symlink unavailable: %v", err)
	}
	if _, err := service.safePath("objects/redirect/payload"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("safePath(nested symlink) error = %v, want ErrInvalidInput", err)
	}
}

func TestListDraftKnowledgeAttachmentsUsesDraftScopeWithoutArticleID(t *testing.T) {
	service, _, adminID, now := newAttachmentTestService(t, 32)
	draftToken := testDraftToken("d")
	attachment := uploadTestAttachment(t, service, adminID, draftToken, "draft.txt", []byte("draft"), now)

	page, err := service.List(context.Background(), adminID, nil, draftToken, 1, 100)
	if err != nil {
		t.Fatalf("List(draft) error = %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].UUID != attachment.UUID {
		t.Fatalf("List(draft) page = %#v", page)
	}
}

func TestConcurrentQuotaReservationsAndCompletionAreSerialized(t *testing.T) {
	service, _, adminID, now := newAttachmentTestService(t, 8)
	var wait sync.WaitGroup
	errorsOut := make(chan error, 2)
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := service.Initialize(context.Background(), adminID, InitializeInput{
				OriginalName: "quota.bin", Size: 8, DraftToken: testDraftToken("4"),
			}, now)
			errorsOut <- err
		}()
	}
	wait.Wait()
	close(errorsOut)
	successes, quotaFailures := 0, 0
	for err := range errorsOut {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrQuotaExceeded) {
			quotaFailures++
		} else {
			t.Fatalf("unexpected reservation error: %v", err)
		}
	}
	if successes != 1 || quotaFailures != 1 {
		t.Fatalf("reservations success=%d quota=%d", successes, quotaFailures)
	}

	service, _, adminID, now = newAttachmentTestService(t, 32)
	content := []byte("abcdefgh")
	upload, err := service.Initialize(context.Background(), adminID, InitializeInput{
		OriginalName: "concurrent.bin", Size: 8, DraftToken: testDraftToken("5"), SHA256: hexSHA256(content),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	for index, chunk := range [][]byte{content[:4], content[4:]} {
		if _, err := service.StoreChunk(context.Background(), adminID, upload.UUID, index, hexSHA256(chunk), bytes.NewReader(chunk), 4, now); err != nil {
			t.Fatal(err)
		}
	}
	identifiers := make(chan int64, 8)
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			attachment, err := service.Complete(context.Background(), adminID, upload.UUID, now)
			if err != nil {
				t.Errorf("Complete() error: %v", err)
				return
			}
			identifiers <- attachment.ID
		}()
	}
	wait.Wait()
	close(identifiers)
	var identifier int64
	for current := range identifiers {
		if identifier == 0 {
			identifier = current
		}
		if current != identifier {
			t.Fatalf("completion IDs differ: %d/%d", identifier, current)
		}
	}
}

func TestUploadRejectsUnsafeNamesQuotaAndCorruptContent(t *testing.T) {
	service, _, adminID, now := newAttachmentTestService(t, 8)
	for _, name := range []string{"", ".", "..", "../escape", "dir\\escape", "bad\r\nname"} {
		if _, err := service.Initialize(context.Background(), adminID, InitializeInput{
			OriginalName: name, Size: 1, DraftToken: testDraftToken("b"),
		}, now); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("Initialize(name=%q) error=%v, want ErrInvalidInput", name, err)
		}
	}
	for _, token := range []string{strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("z", 64)} {
		if _, err := service.Initialize(context.Background(), adminID, InitializeInput{
			OriginalName: "token.bin", Size: 1, DraftToken: token,
		}, now); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("Initialize(draft token length=%d) error=%v, want ErrInvalidInput", len(token), err)
		}
	}
	if _, err := service.Initialize(context.Background(), adminID, InitializeInput{
		OriginalName: "too-large.bin", Size: 9, DraftToken: testDraftToken("b"),
	}, now); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("quota error=%v, want ErrQuotaExceeded", err)
	}

	content := []byte("1234")
	upload, err := service.Initialize(context.Background(), adminID, InitializeInput{
		OriginalName: "hash.bin", Size: 4, DraftToken: testDraftToken("b"), SHA256: hexSHA256([]byte("different")),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StoreChunk(context.Background(), adminID, upload.UUID, 0, hexSHA256(content), bytes.NewReader(content), 4, now); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Complete(context.Background(), adminID, upload.UUID, now); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("Complete(corrupt) error=%v, want ErrHashMismatch", err)
	}
	status, err := service.Status(context.Background(), adminID, upload.UUID, now)
	if err != nil || status.Status != UploadInitialized || status.ReceivedChunks != 0 || len(status.UploadedChunks) != 0 {
		t.Fatalf("recoverable status=%#v error=%v", status, err)
	}
}

func TestUploadStatusPersistsExpiryAndCannotRevert(t *testing.T) {
	service, database, adminID, now := newAttachmentTestService(t, 32)
	upload, err := service.Initialize(context.Background(), adminID, InitializeInput{
		OriginalName: "expires.bin", Size: 4, DraftToken: testDraftToken("6"),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	expiredAt := now.Add(25 * time.Hour)
	status, err := service.Status(context.Background(), adminID, upload.UUID, expiredAt)
	if err != nil || status.Status != store.KnowledgeUploadExpired {
		t.Fatalf("expired status=%#v error=%v", status, err)
	}
	stored, err := database.GetKnowledgeAttachmentUpload(context.Background(), adminID, upload.UUID, expiredAt)
	if err != nil || stored.Status != store.KnowledgeUploadExpired {
		t.Fatalf("stored expiry=%#v error=%v", stored, err)
	}
	if _, err := service.StoreChunk(context.Background(), adminID, upload.UUID, 0, hexSHA256([]byte("data")), bytes.NewReader([]byte("data")), 4, expiredAt); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired chunk error=%v", err)
	}
}

func TestCompletionFailureResetsSessionSoVerifiedChunksCanBeResubmitted(t *testing.T) {
	service, _, adminID, now := newAttachmentTestService(t, 32)
	content := []byte("abcdefgh")
	upload, err := service.Initialize(context.Background(), adminID, InitializeInput{
		OriginalName: "recover.bin", Size: int64(len(content)), DraftToken: testDraftToken("8"), SHA256: hexSHA256(content),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	for index, chunk := range [][]byte{content[:4], content[4:]} {
		if _, err := service.StoreChunk(context.Background(), adminID, upload.UUID, index, hexSHA256(chunk), bytes.NewReader(chunk), int64(len(chunk)), now); err != nil {
			t.Fatal(err)
		}
	}
	missing, err := service.safePath(filepath.ToSlash(filepath.Join(upload.TemporaryPath, "chunks", "1.part")))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Complete(context.Background(), adminID, upload.UUID, now); err == nil {
		t.Fatal("Complete() succeeded despite a missing verified chunk")
	}
	status, err := service.Status(context.Background(), adminID, upload.UUID, now)
	if err != nil || status.Status != UploadInitialized || status.ReceivedChunks != 0 || len(status.UploadedChunks) != 0 {
		t.Fatalf("recoverable status=%#v error=%v", status, err)
	}
	for index, chunk := range [][]byte{content[:4], content[4:]} {
		if _, err := service.StoreChunk(context.Background(), adminID, upload.UUID, index, hexSHA256(chunk), bytes.NewReader(chunk), int64(len(chunk)), now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.Complete(context.Background(), adminID, upload.UUID, now); err != nil {
		t.Fatalf("Complete(retry) error = %v", err)
	}
}

func newAttachmentTestService(t testing.TB, quota int64) (*Service, *store.Store, int64, time.Time) {
	t.Helper()
	database, err := store.OpenSQLite("file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "attachments.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 26, 16, 0, 0, 0, time.UTC)
	admin, err := database.CreateAdminUser(context.Background(), store.CreateAdminUserInput{
		Email: "attachment-admin@example.test", PasswordHash: "test-hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(database, Options{
		Root: filepath.Join(t.TempDir(), "private-attachments"), SigningKey: bytes.Repeat([]byte{0x42}, 32),
		PanelURL: "https://panel.example.test", ChunkSize: 4, MaxFileSize: 64, TotalQuota: quota,
		SignedURLTTL: 2 * time.Hour, DraftTTL: 24 * time.Hour, TrashRetention: 7 * 24 * time.Hour, MaxPerArticle: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, database, admin.ID, now
}

func testDraftToken(character string) string { return string(bytes.Repeat([]byte(character), 64)) }

func hexSHA256(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
