package attachments

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/google/uuid"
)

type CloneResult struct {
	SourceUUID string
	Attachment Attachment
}

func (s *Service) CloneForDraft(ctx context.Context, uploaderUserID, sourceKnowledgeID int64, sourceUUIDs []string, draftToken string, now time.Time) ([]CloneResult, error) {
	if uploaderUserID < 1 || sourceKnowledgeID < 1 || len(sourceUUIDs) == 0 || len(sourceUUIDs) > s.maxPerArticle {
		return nil, ErrInvalidInput
	}
	draftDigest, err := digestDraftToken(draftToken)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(sourceUUIDs))
	for _, sourceUUID := range sourceUUIDs {
		if _, valid := canonicalAttachmentUUID(sourceUUID); !valid {
			return nil, ErrInvalidInput
		}
		if _, exists := seen[sourceUUID]; exists {
			return nil, ErrInvalidInput
		}
		seen[sourceUUID] = struct{}{}
	}
	unlock := s.lockMany(sourceUUIDs)
	defer unlock()

	sources := make([]Attachment, len(sourceUUIDs))
	requested := int64(0)
	for index, sourceUUID := range sourceUUIDs {
		source, err := s.database.GetKnowledgeAttachmentForArticle(ctx, sourceKnowledgeID, sourceUUID)
		if err != nil {
			return nil, mapStoreError(err)
		}
		if source.Size < 1 || requested > s.totalQuota-source.Size {
			return nil, ErrQuotaExceeded
		}
		requested += source.Size
		sources[index] = source
	}
	used, reserved, err := s.database.KnowledgeAttachmentUsage(ctx, now)
	if err != nil {
		return nil, err
	}
	if requested > s.totalQuota-used-reserved {
		return nil, ErrQuotaExceeded
	}

	inputs := make([]store.CreateClonedKnowledgeAttachmentInput, 0, len(sourceUUIDs))
	finalPaths := make([]string, 0, len(sourceUUIDs))
	cleanup := func() {
		for _, path := range finalPaths {
			_ = os.Remove(path)
		}
	}
	for index := range sourceUUIDs {
		source := sources[index]
		reader, _, err := s.openAttachment(source)
		if err != nil {
			cleanup()
			return nil, err
		}
		cloneUUID := uuid.NewString()
		quarantineRelative := filepath.ToSlash(filepath.Join("quarantine", "clone-"+cloneUUID+".part"))
		quarantinePath, err := s.safePath(quarantineRelative)
		if err != nil {
			_ = reader.Close()
			cleanup()
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(quarantinePath), 0o700); err != nil {
			_ = reader.Close()
			cleanup()
			return nil, err
		}
		output, err := os.OpenFile(quarantinePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = reader.Close()
			cleanup()
			return nil, fmt.Errorf("create clone staging object: %w", err)
		}
		hasher := sha256.New()
		copied, copyErr := io.Copy(io.MultiWriter(output, hasher), io.LimitReader(reader, source.Size+1))
		copyErr = errors.Join(copyErr, output.Sync(), output.Close(), reader.Close())
		digest := hex.EncodeToString(hasher.Sum(nil))
		if copyErr != nil || copied != source.Size || subtle.ConstantTimeCompare([]byte(digest), []byte(source.SHA256)) != 1 {
			_ = os.Remove(quarantinePath)
			cleanup()
			if copyErr != nil {
				return nil, fmt.Errorf("copy cloned attachment: %w", copyErr)
			}
			return nil, ErrHashMismatch
		}
		filename := cloneUUID
		if source.Extension != nil {
			filename += "." + *source.Extension
		}
		storageRelative := filepath.ToSlash(filepath.Join("files", now.UTC().Format("2006"), now.UTC().Format("01"), filename))
		storagePath, err := s.safePath(storageRelative)
		if err != nil {
			_ = os.Remove(quarantinePath)
			cleanup()
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(storagePath), 0o700); err != nil {
			_ = os.Remove(quarantinePath)
			cleanup()
			return nil, err
		}
		if err := os.Rename(quarantinePath, storagePath); err != nil {
			_ = os.Remove(quarantinePath)
			cleanup()
			return nil, fmt.Errorf("publish cloned attachment: %w", err)
		}
		finalPaths = append(finalPaths, storagePath)
		inputs = append(inputs, store.CreateClonedKnowledgeAttachmentInput{
			UUID: cloneUUID, UploaderUserID: uploaderUserID, DraftTokenHash: draftDigest,
			OriginalName: source.OriginalName, StoragePath: storageRelative, MIMEType: source.MIMEType,
			Extension: source.Extension, Size: source.Size, SHA256: source.SHA256,
		})
	}
	created, err := s.database.CreateClonedKnowledgeAttachments(ctx, inputs, s.totalQuota, now)
	if err != nil {
		cleanup()
		return nil, mapStoreError(err)
	}
	results := make([]CloneResult, len(created))
	for index := range created {
		results[index] = CloneResult{SourceUUID: sourceUUIDs[index], Attachment: created[index]}
	}
	return results, nil
}

func (s *Service) lockMany(keys []string) func() {
	ordered := append([]string(nil), keys...)
	sort.Strings(ordered)
	unlocks := make([]func(), 0, len(ordered))
	for _, key := range ordered {
		unlocks = append(unlocks, s.locks.lock(key))
	}
	return func() {
		for index := len(unlocks) - 1; index >= 0; index-- {
			unlocks[index]()
		}
	}
}
