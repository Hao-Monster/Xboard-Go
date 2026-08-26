package attachments

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestImportLegacySnapshotFilesPreservesResumableChunksAndRollsBack(t *testing.T) {
	source := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	const uploadUUID = "55555555-5555-4555-8555-555555555555"
	temporaryPath := filepath.ToSlash(filepath.Join("temporary", "1", uploadUUID))
	for index, content := range [][]byte{[]byte("abcd"), []byte("efgh")} {
		path := filepath.Join(source, filepath.FromSlash(temporaryPath), "chunks", strconv.Itoa(index)+".part")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	uploads, rollback, err := ImportLegacySnapshotFiles(context.Background(), source, target, nil, []store.LegacyKnowledgeUpload{{
		ID: 1, UUID: uploadUUID, UploaderUserID: 1, DraftTokenHash: strings.Repeat("a", 64), OriginalName: "分片.bin",
		DeclaredSize: 8, ChunkSize: 4, TotalChunks: 2, ReceivedChunks: 2, TemporaryPath: temporaryPath,
		Status: store.KnowledgeUploadInitialized, ExpiresAt: 200, CreatedAt: 100, UpdatedAt: 110,
	}}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(uploads) != 1 || uploads[0].Status != store.KnowledgeUploadUploading || uploads[0].ReceivedChunks != 2 ||
		len(uploads[0].Chunks) != 2 || uploads[0].Chunks[0].SHA256 != hexSHA256([]byte("abcd")) {
		t.Fatalf("uploads = %#v", uploads)
	}
	for index := 0; index < 2; index++ {
		path := filepath.Join(target, filepath.FromSlash(temporaryPath), "chunks", strconv.Itoa(index)+".part")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("migrated chunk %d: %v", index, err)
		}
	}
	rollback()
	for index := 0; index < 2; index++ {
		path := filepath.Join(target, filepath.FromSlash(temporaryPath), "chunks", strconv.Itoa(index)+".part")
		if !errors.Is(statError(path), os.ErrNotExist) {
			t.Fatalf("rollback left chunk %d", index)
		}
	}
}

func TestImportLegacySnapshotFilesRejectsUnexpectedChunkWithoutPartialObjects(t *testing.T) {
	source := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	const uploadUUID = "66666666-6666-4666-8666-666666666666"
	temporaryPath := filepath.ToSlash(filepath.Join("temporary", "1", uploadUUID))
	directory := filepath.Join(source, filepath.FromSlash(temporaryPath), "chunks")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "debug.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := ImportLegacySnapshotFiles(context.Background(), source, target, nil, []store.LegacyKnowledgeUpload{{
		ID: 1, UUID: uploadUUID, UploaderUserID: 1, DraftTokenHash: strings.Repeat("a", 64), OriginalName: "upload.bin",
		DeclaredSize: 4, ChunkSize: 4, TotalChunks: 1, TemporaryPath: temporaryPath,
		Status: store.KnowledgeUploadUploading, ExpiresAt: 200, CreatedAt: 100, UpdatedAt: 110,
	}}, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("ImportLegacySnapshotFiles() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(temporaryPath), "chunks", "debug.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target unexpected object error = %v", err)
	}
}

func statError(path string) error {
	_, err := os.Stat(path)
	return err
}
