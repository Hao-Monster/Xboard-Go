package attachments

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type CleanupReport struct {
	ExpiredUploads    int
	SoftDeletedDrafts int64
	PurgedAttachments int
	OrphanFiles       int
	Failures          int
}

// Cleanup processes at most limit rows in each independent phase and never
// overlaps within a process. Files are removed before their database rows so a
// failed filesystem operation cannot silently orphan a physical object.
func (s *Service) Cleanup(ctx context.Context, now time.Time, limit int) (CleanupReport, error) {
	if limit < 1 || limit > 1000 {
		return CleanupReport{}, ErrInvalidInput
	}
	if !s.cleanupMu.TryLock() {
		return CleanupReport{}, ErrConflict
	}
	defer s.cleanupMu.Unlock()
	report := CleanupReport{}
	var cleanupErrors []error

	uploads, err := s.database.ListExpiredKnowledgeAttachmentUploads(ctx, now, limit)
	if err != nil {
		return report, err
	}
	for _, upload := range uploads {
		unlock := s.locks.lock(upload.UUID)
		path, pathErr := s.safePath(upload.TemporaryPath)
		if pathErr == nil {
			pathErr = os.RemoveAll(path)
		}
		if pathErr == nil {
			pathErr = s.database.DeleteExpiredKnowledgeAttachmentUpload(ctx, upload.ID, now)
		}
		unlock()
		if pathErr != nil {
			report.Failures++
			cleanupErrors = append(cleanupErrors, fmt.Errorf("cleanup upload %s: %w", upload.UUID, pathErr))
			continue
		}
		report.ExpiredUploads++
	}

	softDeleted, err := s.database.SoftDeleteStaleDraftKnowledgeAttachments(ctx, now.Add(-s.draftTTL), now, limit)
	if err != nil {
		cleanupErrors = append(cleanupErrors, err)
		report.Failures++
	} else {
		report.SoftDeletedDrafts = softDeleted
	}

	attachments, err := s.database.ListPurgeableKnowledgeAttachments(ctx, now.Add(-s.trashRetention), limit)
	if err != nil {
		cleanupErrors = append(cleanupErrors, err)
		report.Failures++
		return report, errors.Join(cleanupErrors...)
	}
	cutoff := now.Add(-s.trashRetention)
	for _, attachment := range attachments {
		unlock := s.locks.lock(attachment.UUID)
		path, pathErr := s.safePath(attachment.StoragePath)
		if pathErr == nil {
			pathErr = os.Remove(path)
			if errors.Is(pathErr, os.ErrNotExist) {
				pathErr = nil
			}
		}
		if pathErr == nil {
			pathErr = s.database.DeletePurgedKnowledgeAttachment(ctx, attachment.ID, cutoff)
		}
		unlock()
		if pathErr != nil {
			report.Failures++
			cleanupErrors = append(cleanupErrors, fmt.Errorf("purge attachment %s: %w", attachment.UUID, pathErr))
			continue
		}
		report.PurgedAttachments++
	}
	orphans, err := s.cleanupOrphanFiles(ctx, now.Add(-s.draftTTL), limit)
	if err != nil {
		report.Failures++
		cleanupErrors = append(cleanupErrors, err)
	}
	report.OrphanFiles = orphans
	return report, errors.Join(cleanupErrors...)
}

func (s *Service) cleanupOrphanFiles(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	const stopWalk = "attachment cleanup scan limit reached"
	removed := 0
	var cleanupErrors []error
	for _, directory := range []string{"quarantine", "temporary", "files"} {
		root, err := s.safePath(directory)
		if err != nil {
			cleanupErrors = append(cleanupErrors, err)
			continue
		}
		scanned := 0
		err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
				return nil
			}
			if scanned >= limit {
				return errors.New(stopWalk)
			}
			scanned++
			info, err := entry.Info()
			if err != nil || info.ModTime().After(cutoff) {
				return err
			}
			relative, err := filepath.Rel(s.root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			referenced := false
			switch directory {
			case "files":
				referenced, err = s.database.KnowledgeAttachmentStoragePathExists(ctx, relative)
			case "temporary":
				referenced, err = s.database.KnowledgeAttachmentTemporaryPathExists(ctx, relative)
			}
			if err != nil || referenced {
				return err
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			removed++
			return nil
		})
		if err != nil && err.Error() != stopWalk && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("scan orphan attachment %s: %w", directory, err))
		}
	}
	return removed, errors.Join(cleanupErrors...)
}
