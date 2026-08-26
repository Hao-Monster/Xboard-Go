package attachments

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestCleanupIsBoundedAndPurgesExpiredUploadsAndRetainedDrafts(t *testing.T) {
	service, database, adminID, now := newAttachmentTestService(t, 1<<20)
	upload, err := service.Initialize(context.Background(), adminID, InitializeInput{
		OriginalName: "expired.bin", Size: 4, DraftToken: testDraftToken("2"),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	temporaryPath, _ := service.safePath(upload.TemporaryPath)
	if err := os.MkdirAll(temporaryPath, 0o700); err != nil {
		t.Fatal(err)
	}

	draft := uploadTestAttachment(t, service, adminID, testDraftToken("3"), "draft.txt", []byte("draft"), now)
	objectPath := filepath.Join(service.root, filepath.FromSlash(draft.StoragePath))
	report, err := service.Cleanup(context.Background(), now.Add(25*time.Hour), 100)
	if err != nil || report.ExpiredUploads != 1 || report.SoftDeletedDrafts != 1 {
		t.Fatalf("first Cleanup() report=%#v error=%v", report, err)
	}
	if _, err := database.GetKnowledgeAttachmentUpload(context.Background(), adminID, upload.UUID, now.Add(25*time.Hour)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired session error=%v", err)
	}
	if _, err := os.Stat(temporaryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary path remains: %v", err)
	}
	if _, err := os.Stat(objectPath); err != nil {
		t.Fatalf("retained draft object removed too early: %v", err)
	}

	report, err = service.Cleanup(context.Background(), now.Add(9*24*time.Hour), 100)
	if err != nil || report.PurgedAttachments != 1 {
		t.Fatalf("second Cleanup() report=%#v error=%v", report, err)
	}
	if _, err := os.Stat(objectPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("purged object remains: %v", err)
	}
}

func TestCleanupRemovesOldOrphansButPreservesReferencedObjects(t *testing.T) {
	service, _, adminID, now := newAttachmentTestService(t, 1<<20)
	attachment := uploadTestAttachment(t, service, adminID, testDraftToken("7"), "kept.txt", []byte("kept"), now)
	keptPath := filepath.Join(service.root, filepath.FromSlash(attachment.StoragePath))
	orphanPath := filepath.Join(service.root, "files", "2020", "01", "orphan.bin")
	quarantinePath := filepath.Join(service.root, "quarantine", "orphan.part")
	for _, path := range []string{orphanPath, quarantinePath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("orphan"), 0o600); err != nil {
			t.Fatal(err)
		}
		old := now.Add(-48 * time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}

	report, err := service.Cleanup(context.Background(), now, 100)
	if err != nil || report.OrphanFiles != 2 {
		t.Fatalf("Cleanup() report=%#v error=%v", report, err)
	}
	if _, err := os.Stat(keptPath); err != nil {
		t.Fatalf("referenced attachment was removed: %v", err)
	}
	for _, path := range []string{orphanPath, quarantinePath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("orphan remains at %s: %v", path, err)
		}
	}
}
