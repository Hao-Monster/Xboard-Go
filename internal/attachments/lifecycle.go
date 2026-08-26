package attachments

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/google/uuid"
)

func (s *Service) ChunkSize() int64 { return s.chunkSize }

func (s *Service) List(ctx context.Context, uploaderUserID int64, knowledgeID *int64, draftToken string, page, perPage int) (store.KnowledgeAttachmentPage, error) {
	var draftDigest *string
	if draftToken != "" {
		digest, err := digestDraftToken(draftToken)
		if err != nil {
			return store.KnowledgeAttachmentPage{}, err
		}
		draftDigest = &digest
	}
	result, err := s.database.ListKnowledgeAttachments(ctx, uploaderUserID, knowledgeID, draftDigest, page, perPage)
	return result, mapStoreError(err)
}

func (s *Service) Cancel(ctx context.Context, uploaderUserID int64, uploadUUID, draftToken string, now time.Time) error {
	if _, valid := canonicalAttachmentUUID(uploadUUID); uploaderUserID < 1 || !valid {
		return ErrInvalidInput
	}
	digest, err := digestDraftToken(draftToken)
	if err != nil {
		return err
	}
	unlock := s.locks.lock(uploadUUID)
	defer unlock()
	upload, err := s.database.GetKnowledgeAttachmentUpload(ctx, uploaderUserID, uploadUUID, now)
	if err != nil {
		return mapStoreError(err)
	}
	if subtle.ConstantTimeCompare([]byte(upload.DraftTokenHash), []byte(digest)) != 1 {
		return ErrNotFound
	}
	if upload.Status == UploadCompleted || upload.Status == UploadCompleting {
		return ErrConflict
	}
	temporaryPath, err := s.safePath(upload.TemporaryPath)
	if err != nil {
		return err
	}
	staging := temporaryPath + ".cancelled-" + uuid.NewString()
	if err := os.Rename(temporaryPath, staging); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stage cancelled attachment upload: %w", err)
	}
	if err := s.database.CancelKnowledgeAttachmentUpload(ctx, uploaderUserID, uploadUUID); err != nil {
		if _, statErr := os.Stat(staging); statErr == nil {
			_ = os.Rename(staging, temporaryPath)
		}
		return mapStoreError(err)
	}
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("remove cancelled attachment upload: %w", err)
	}
	return nil
}

func (s *Service) DropDraft(ctx context.Context, uploaderUserID int64, attachmentUUID, draftToken string) error {
	if _, valid := canonicalAttachmentUUID(attachmentUUID); uploaderUserID < 1 || !valid {
		return ErrInvalidInput
	}
	digest, err := digestDraftToken(draftToken)
	if err != nil {
		return err
	}
	unlock := s.locks.lock(attachmentUUID)
	defer unlock()
	attachment, err := s.database.GetDraftKnowledgeAttachment(ctx, uploaderUserID, attachmentUUID)
	if err != nil {
		return mapStoreError(err)
	}
	if attachment.DraftTokenHash == nil || subtle.ConstantTimeCompare([]byte(*attachment.DraftTokenHash), []byte(digest)) != 1 {
		return ErrNotFound
	}
	objectPath, err := safeRelativePath(attachment.StoragePath)
	if err != nil {
		return err
	}
	staging, err := safeRelativePath(filepath.ToSlash(filepath.Join("quarantine", "delete-"+uuid.NewString())))
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return fmt.Errorf("open attachment storage root: %w", err)
	}
	defer root.Close()
	if err := verifyRootPathComponents(root, objectPath); err != nil {
		return err
	}
	if err := verifyRootPathComponents(root, staging); err != nil {
		return err
	}
	if err := root.MkdirAll(filepath.Dir(staging), 0o700); err != nil {
		return fmt.Errorf("create attachment delete staging: %w", err)
	}
	if err := root.Rename(objectPath, staging); err != nil {
		return fmt.Errorf("stage draft attachment deletion: %w", err)
	}
	if err := s.database.DeleteDraftKnowledgeAttachment(ctx, uploaderUserID, attachmentUUID, digest); err != nil {
		_ = root.Rename(staging, objectPath)
		return mapStoreError(err)
	}
	if err := root.Remove(staging); err != nil {
		return fmt.Errorf("remove discarded draft attachment: %w", err)
	}
	return nil
}
