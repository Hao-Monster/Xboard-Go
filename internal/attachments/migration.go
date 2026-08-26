package attachments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func PrepareStorageRoot(root string) error {
	_, err := secureStorageRoot(root)
	return err
}

// ImportLegacyFiles copies immutable legacy attachment objects into a private
// target root. The returned rollback must be called if the matching database
// transaction does not commit.
func ImportLegacyFiles(ctx context.Context, sourceRoot, targetRoot string, entries []store.LegacyKnowledgeAttachment, totalQuota int64) (func(), error) {
	if len(entries) == 0 {
		return func() {}, nil
	}
	if totalQuota < 1 {
		return nil, ErrInvalidInput
	}
	source, err := existingPrivateRoot(sourceRoot)
	if err != nil {
		return nil, fmt.Errorf("validate legacy attachment root: %w", err)
	}
	target, err := secureStorageRoot(targetRoot)
	if err != nil {
		return nil, fmt.Errorf("validate target attachment root: %w", err)
	}
	sourceInfo, sourceErr := os.Stat(source)
	targetInfo, targetErr := os.Stat(target)
	if sourceErr != nil || targetErr != nil || os.SameFile(sourceInfo, targetInfo) {
		return nil, errors.New("legacy and target attachment roots must be different directories")
	}

	var total int64
	for _, entry := range entries {
		if entry.Size < 1 || total > totalQuota-entry.Size {
			return nil, ErrQuotaExceeded
		}
		total += entry.Size
	}
	created := make([]string, 0, len(entries))
	rollback := func() {
		for index := len(created) - 1; index >= 0; index-- {
			_ = os.Remove(created[index])
		}
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			rollback()
			return nil, err
		}
		sourcePath, err := safeMigrationPath(source, entry.StoragePath)
		if err != nil {
			rollback()
			return nil, err
		}
		targetPath, err := safeMigrationPath(target, entry.StoragePath)
		if err != nil {
			rollback()
			return nil, err
		}
		if _, err := os.Lstat(targetPath); err == nil {
			rollback()
			return nil, fmt.Errorf("target attachment path already exists: %s", entry.StoragePath)
		} else if !errors.Is(err, os.ErrNotExist) {
			rollback()
			return nil, err
		}
		info, err := os.Lstat(sourcePath)
		if err != nil || !info.Mode().IsRegular() || info.Size() != entry.Size {
			rollback()
			return nil, fmt.Errorf("legacy attachment %s is missing or has the wrong size", entry.UUID)
		}
		input, err := os.Open(sourcePath)
		if err != nil {
			rollback()
			return nil, fmt.Errorf("open legacy attachment %s: %w", entry.UUID, err)
		}
		openedInfo, statErr := input.Stat()
		if statErr != nil || !os.SameFile(info, openedInfo) {
			_ = input.Close()
			rollback()
			return nil, fmt.Errorf("legacy attachment %s changed before copying", entry.UUID)
		}
		quarantineRelative := filepath.ToSlash(filepath.Join("quarantine", "legacy-"+entry.UUID+".part"))
		quarantinePath, err := safeMigrationPath(target, quarantineRelative)
		if err != nil {
			_ = input.Close()
			rollback()
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(quarantinePath), 0o700); err != nil {
			_ = input.Close()
			rollback()
			return nil, err
		}
		output, err := os.OpenFile(quarantinePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = input.Close()
			rollback()
			return nil, err
		}
		hash := sha256.New()
		copied, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(&migrationContextReader{ctx: ctx, reader: input}, entry.Size+1))
		copyErr = errors.Join(copyErr, output.Sync(), output.Close(), input.Close())
		if copyErr != nil || copied != entry.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			_ = os.Remove(quarantinePath)
			rollback()
			if copyErr != nil {
				return nil, fmt.Errorf("copy legacy attachment %s: %w", entry.UUID, copyErr)
			}
			return nil, fmt.Errorf("legacy attachment %s failed SHA-256 verification", entry.UUID)
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
			_ = os.Remove(quarantinePath)
			rollback()
			return nil, err
		}
		if err := os.Rename(quarantinePath, targetPath); err != nil {
			_ = os.Remove(quarantinePath)
			rollback()
			return nil, fmt.Errorf("publish legacy attachment %s: %w", entry.UUID, err)
		}
		created = append(created, targetPath)
	}
	return rollback, nil
}

// ImportLegacySnapshotFiles extends immutable attachment migration with the
// resumable upload sessions whose chunk indexes are stored in the legacy file
// tree rather than in SQLite.
func ImportLegacySnapshotFiles(
	ctx context.Context,
	sourceRoot, targetRoot string,
	entries []store.LegacyKnowledgeAttachment,
	uploads []store.LegacyKnowledgeUpload,
	totalQuota int64,
) ([]store.LegacyKnowledgeUpload, func(), error) {
	if totalQuota < 1 {
		return nil, nil, ErrInvalidInput
	}
	var reserved int64
	for _, entry := range entries {
		if entry.Size < 1 || reserved > totalQuota-entry.Size {
			return nil, nil, ErrQuotaExceeded
		}
		reserved += entry.Size
	}
	for _, upload := range uploads {
		switch upload.Status {
		case store.KnowledgeUploadInitialized, store.KnowledgeUploadUploading, store.KnowledgeUploadCompleting, store.KnowledgeUploadFailed:
			if upload.DeclaredSize < 1 || reserved > totalQuota-upload.DeclaredSize {
				return nil, nil, ErrQuotaExceeded
			}
			reserved += upload.DeclaredSize
		}
	}
	attachmentRollback, err := ImportLegacyFiles(ctx, sourceRoot, targetRoot, entries, totalQuota)
	if err != nil {
		return nil, nil, err
	}
	rollback := attachmentRollback
	if len(uploads) == 0 {
		return []store.LegacyKnowledgeUpload{}, rollback, nil
	}
	source, err := existingPrivateRoot(sourceRoot)
	if err != nil {
		rollback()
		return nil, nil, fmt.Errorf("validate legacy attachment root: %w", err)
	}
	target, err := secureStorageRoot(targetRoot)
	if err != nil {
		rollback()
		return nil, nil, fmt.Errorf("validate target attachment root: %w", err)
	}
	sourceInfo, sourceErr := os.Stat(source)
	targetInfo, targetErr := os.Stat(target)
	if sourceErr != nil || targetErr != nil || os.SameFile(sourceInfo, targetInfo) {
		rollback()
		return nil, nil, errors.New("legacy and target attachment roots must be different directories")
	}
	created := make([]string, 0)
	rollbackAll := func() {
		for index := len(created) - 1; index >= 0; index-- {
			_ = os.Remove(created[index])
		}
		attachmentRollback()
	}
	enriched := append([]store.LegacyKnowledgeUpload(nil), uploads...)
	for uploadIndex := range enriched {
		upload := &enriched[uploadIndex]
		if upload.Status == store.KnowledgeUploadCompleted {
			upload.Chunks = []store.LegacyKnowledgeUploadChunk{}
			continue
		}
		chunks, paths, err := importLegacyUploadChunks(ctx, source, target, *upload)
		if err != nil {
			rollbackAll()
			return nil, nil, fmt.Errorf("copy legacy upload %s: %w", upload.UUID, err)
		}
		created = append(created, paths...)
		upload.Chunks = chunks
		upload.ReceivedChunks = len(chunks)
		if upload.Status == store.KnowledgeUploadExpired {
			for _, chunk := range chunks {
				if reserved > totalQuota-chunk.Size {
					rollbackAll()
					return nil, nil, ErrQuotaExceeded
				}
				reserved += chunk.Size
			}
		}
		if upload.Status == store.KnowledgeUploadInitialized && len(chunks) != 0 {
			upload.Status = store.KnowledgeUploadUploading
		}
	}
	return enriched, rollbackAll, nil
}

func importLegacyUploadChunks(ctx context.Context, sourceRoot, targetRoot string, upload store.LegacyKnowledgeUpload) ([]store.LegacyKnowledgeUploadChunk, []string, error) {
	relativeDirectory := filepath.ToSlash(filepath.Join(upload.TemporaryPath, "chunks"))
	sourceDirectory, err := safeMigrationPath(sourceRoot, relativeDirectory)
	if err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(sourceDirectory)
	if errors.Is(err, os.ErrNotExist) {
		if upload.ReceivedChunks != 0 {
			return nil, nil, errors.New("recorded chunks are missing")
		}
		return []store.LegacyKnowledgeUploadChunk{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Lstat(sourceDirectory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, ErrInvalidInput
	}
	chunks := make([]store.LegacyKnowledgeUploadChunk, 0, len(entries))
	created := make([]string, 0, len(entries))
	rollback := func() {
		for index := len(created) - 1; index >= 0; index-- {
			_ = os.Remove(created[index])
		}
	}
	seen := make(map[int]struct{}, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			rollback()
			return nil, nil, err
		}
		name := entry.Name()
		indexText, found := strings.CutSuffix(name, ".part")
		index, parseErr := strconv.Atoi(indexText)
		if !found || parseErr != nil || index < 0 || index >= upload.TotalChunks || name != strconv.Itoa(index)+".part" ||
			entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			rollback()
			return nil, nil, fmt.Errorf("unexpected legacy upload chunk %q", name)
		}
		if _, exists := seen[index]; exists {
			rollback()
			return nil, nil, fmt.Errorf("duplicate legacy upload chunk %d", index)
		}
		seen[index] = struct{}{}
		expectedSize := upload.ChunkSize
		if index == upload.TotalChunks-1 {
			expectedSize = upload.DeclaredSize - upload.ChunkSize*int64(upload.TotalChunks-1)
		}
		relative := filepath.ToSlash(filepath.Join(upload.TemporaryPath, "chunks", name))
		sourcePath, err := safeMigrationPath(sourceRoot, relative)
		if err != nil {
			rollback()
			return nil, nil, err
		}
		targetPath, err := safeMigrationPath(targetRoot, relative)
		if err != nil {
			rollback()
			return nil, nil, err
		}
		digest, err := copyLegacyChunk(ctx, sourcePath, targetPath, expectedSize)
		if err != nil {
			rollback()
			return nil, nil, err
		}
		created = append(created, targetPath)
		chunks = append(chunks, store.LegacyKnowledgeUploadChunk{Index: index, Size: expectedSize, SHA256: digest, CreatedAt: upload.UpdatedAt})
	}
	if upload.Status == store.KnowledgeUploadCompleting && len(chunks) != upload.TotalChunks {
		rollback()
		return nil, nil, errors.New("completing upload is missing chunks")
	}
	return chunks, created, nil
}

func copyLegacyChunk(ctx context.Context, sourcePath, targetPath string, expectedSize int64) (string, error) {
	info, err := os.Lstat(sourcePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() != expectedSize {
		return "", errors.New("legacy upload chunk is missing or has the wrong size")
	}
	input, err := os.Open(sourcePath)
	if err != nil {
		return "", err
	}
	openedInfo, statErr := input.Stat()
	if statErr != nil || !os.SameFile(info, openedInfo) {
		_ = input.Close()
		return "", errors.New("legacy upload chunk changed before copying")
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		_ = input.Close()
		return "", err
	}
	output, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = input.Close()
		return "", err
	}
	hash := sha256.New()
	copied, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(&migrationContextReader{ctx: ctx, reader: input}, expectedSize+1))
	copyErr = errors.Join(copyErr, output.Sync(), output.Close(), input.Close())
	if copyErr != nil || copied != expectedSize {
		_ = os.Remove(targetPath)
		if copyErr != nil {
			return "", copyErr
		}
		return "", errors.New("legacy upload chunk changed while copying")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type migrationContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *migrationContextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func existingPrivateRoot(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", ErrInvalidInput
	}
	absolute := filepath.Clean(path)
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrInvalidInput
	}
	real, err := filepath.EvalSymlinks(absolute)
	if err != nil || filepath.Clean(real) != absolute {
		return "", ErrInvalidInput
	}
	return absolute, nil
}

func safeMigrationPath(root, relative string) (string, error) {
	if relative == "" || strings.HasPrefix(relative, "/") || strings.Contains(relative, `\`) || strings.Contains(relative, "..") {
		return "", ErrInvalidInput
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative)))
	if clean != relative || clean == "." {
		return "", ErrInvalidInput
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(root)+string(filepath.Separator)) {
		return "", ErrInvalidInput
	}
	for current := filepath.Dir(path); current != root; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", ErrInvalidInput
		}
	}
	return path, nil
}
