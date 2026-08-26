package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	KnowledgeAttachmentQuarantined = "quarantined"
	KnowledgeAttachmentReady       = "ready"
	KnowledgeAttachmentRejected    = "rejected"
	KnowledgeUploadInitialized     = "initialized"
	KnowledgeUploadUploading       = "uploading"
	KnowledgeUploadCompleting      = "completing"
	KnowledgeUploadCompleted       = "completed"
	KnowledgeUploadFailed          = "failed"
	KnowledgeUploadExpired         = "expired"
)

type KnowledgeAttachment struct {
	ID             int64
	UUID           string
	KnowledgeID    *int64
	UploaderUserID int64
	DraftTokenHash *string
	OriginalName   string
	StoragePath    string
	MIMEType       string
	Extension      *string
	Size           int64
	SHA256         string
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      *time.Time
}

type KnowledgeAttachmentUpload struct {
	ID             int64
	UUID           string
	UploaderUserID int64
	DraftTokenHash string
	OriginalName   string
	DeclaredSize   int64
	ExpectedSHA256 *string
	ChunkSize      int64
	TotalChunks    int
	ReceivedChunks int
	UploadedChunks []int
	TemporaryPath  string
	Status         string
	ExpiresAt      time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type CreateKnowledgeAttachmentUploadInput struct {
	UUID           string
	UploaderUserID int64
	DraftTokenHash string
	OriginalName   string
	DeclaredSize   int64
	ExpectedSHA256 *string
	ChunkSize      int64
	TotalChunks    int
	TemporaryPath  string
	ExpiresAt      time.Time
	TotalQuota     int64
}

type CompleteKnowledgeAttachmentInput struct {
	UUID           string
	UploaderUserID int64
	OriginalName   string
	StoragePath    string
	MIMEType       string
	Extension      *string
	Size           int64
	SHA256         string
}

type CreateClonedKnowledgeAttachmentInput struct {
	UUID           string
	UploaderUserID int64
	DraftTokenHash string
	OriginalName   string
	StoragePath    string
	MIMEType       string
	Extension      *string
	Size           int64
	SHA256         string
}

type KnowledgeAttachmentPage struct {
	Items   []KnowledgeAttachment
	Total   int64
	Page    int
	PerPage int
}

func (s *Store) KnowledgeAttachmentUsage(ctx context.Context, now time.Time) (used, reserved int64, err error) {
	if err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size), 0) FROM knowledge_attachments`).Scan(&used); err != nil {
		return 0, 0, fmt.Errorf("sum attachment usage: %w", err)
	}
	if err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(declared_size), 0) FROM knowledge_attachment_uploads
		WHERE status IN ('initialized', 'uploading', 'completing') AND expires_at > ?
	`, now.Unix()).Scan(&reserved); err != nil {
		return 0, 0, fmt.Errorf("sum attachment reservations: %w", err)
	}
	return used, reserved, nil
}

// ReserveKnowledgeAttachmentUpload atomically applies the global quota to both
// durable objects and unfinished reservations.
func (s *Store) ReserveKnowledgeAttachmentUpload(ctx context.Context, input CreateKnowledgeAttachmentUploadInput, now time.Time) (KnowledgeAttachmentUpload, error) {
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KnowledgeAttachmentUpload{}, fmt.Errorf("begin attachment reservation: %w", err)
	}
	defer tx.Rollback()
	var used, reserved int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(size), 0) FROM knowledge_attachments`).Scan(&used); err != nil {
		return KnowledgeAttachmentUpload{}, fmt.Errorf("sum attachment usage: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(declared_size), 0) FROM knowledge_attachment_uploads
		WHERE status IN ('initialized', 'uploading', 'completing') AND expires_at > ?
	`, now.Unix()).Scan(&reserved); err != nil {
		return KnowledgeAttachmentUpload{}, fmt.Errorf("sum attachment reservations: %w", err)
	}
	if input.TotalQuota < 1 || input.DeclaredSize > input.TotalQuota-used-reserved {
		return KnowledgeAttachmentUpload{}, ErrAttachmentQuotaExceeded
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO knowledge_attachment_uploads (
			uuid, uploader_user_id, draft_token_hash, original_name, declared_size, expected_sha256,
			chunk_size, total_chunks, received_chunks, temporary_path, status, expires_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, 'initialized', ?, ?, ?)
	`, input.UUID, input.UploaderUserID, input.DraftTokenHash, input.OriginalName, input.DeclaredSize,
		input.ExpectedSHA256, input.ChunkSize, input.TotalChunks, input.TemporaryPath,
		input.ExpiresAt.Unix(), now.Unix(), now.Unix())
	if err != nil {
		return KnowledgeAttachmentUpload{}, fmt.Errorf("reserve attachment upload: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeAttachmentUpload{}, fmt.Errorf("commit attachment reservation: %w", err)
	}
	return s.GetKnowledgeAttachmentUpload(ctx, input.UploaderUserID, input.UUID, now)
}

func (s *Store) CreateClonedKnowledgeAttachments(ctx context.Context, inputs []CreateClonedKnowledgeAttachmentInput, totalQuota int64, now time.Time) ([]KnowledgeAttachment, error) {
	if len(inputs) == 0 || totalQuota < 1 {
		return nil, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin cloned attachments: %w", err)
	}
	defer tx.Rollback()
	var used, reserved int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(size), 0) FROM knowledge_attachments`).Scan(&used); err != nil {
		return nil, fmt.Errorf("sum cloned attachment usage: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(declared_size), 0) FROM knowledge_attachment_uploads
		WHERE status IN ('initialized', 'uploading', 'completing') AND expires_at > ?
	`, now.Unix()).Scan(&reserved); err != nil {
		return nil, fmt.Errorf("sum cloned attachment reservations: %w", err)
	}
	requested := int64(0)
	for _, input := range inputs {
		if input.Size < 1 || requested > totalQuota-input.Size {
			return nil, ErrAttachmentQuotaExceeded
		}
		requested += input.Size
	}
	if requested > totalQuota-used-reserved {
		return nil, ErrAttachmentQuotaExceeded
	}
	items := make([]KnowledgeAttachment, 0, len(inputs))
	for _, input := range inputs {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO knowledge_attachments (
				uuid, uploader_user_id, draft_token_hash, original_name, storage_path, mime_type, extension,
				size, sha256, status, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'ready', ?, ?)
		`, input.UUID, input.UploaderUserID, input.DraftTokenHash, input.OriginalName, input.StoragePath,
			input.MIMEType, input.Extension, input.Size, input.SHA256, now.Unix(), now.Unix())
		if err != nil {
			return nil, fmt.Errorf("create cloned attachment: %w", err)
		}
		item, err := getKnowledgeAttachmentTx(ctx, tx, input.UploaderUserID, input.UUID)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit cloned attachments: %w", err)
	}
	return items, nil
}

// CreateKnowledgeWithAttachments writes the article and binds all draft
// attachments in one transaction, so a rejected reference cannot leave a
// partially-created article behind.
func (s *Store) CreateKnowledgeWithAttachments(ctx context.Context, input SaveKnowledgeInput, uploaderUserID int64, draftTokenHash string, attachmentUUIDs []string, now time.Time) (Knowledge, error) {
	normalized, err := normalizeKnowledgeInput(input)
	if err != nil || uploaderUserID < 1 || draftTokenHash == "" {
		return Knowledge{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Knowledge{}, fmt.Errorf("begin knowledge attachment create: %w", err)
	}
	defer tx.Rollback()
	if err := validateKnowledgeAttachmentBindings(ctx, tx, 0, uploaderUserID, draftTokenHash, attachmentUUIDs, now); err != nil {
		return Knowledge{}, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO knowledge (language, category, title, body, sort_position, visible, revision, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, ?, 1, ?, ?)
	`, normalized.Language, normalized.Category, normalized.Title, normalized.Body, normalized.Visible, now.Unix(), now.Unix())
	if err != nil {
		return Knowledge{}, fmt.Errorf("create knowledge with attachments: %w", err)
	}
	knowledgeID, err := result.LastInsertId()
	if err != nil {
		return Knowledge{}, fmt.Errorf("read knowledge id: %w", err)
	}
	if err := applyKnowledgeAttachmentBindings(ctx, tx, knowledgeID, attachmentUUIDs, now); err != nil {
		return Knowledge{}, err
	}
	item, err := getKnowledgeTx(ctx, tx, knowledgeID)
	if err != nil {
		return Knowledge{}, err
	}
	if err := tx.Commit(); err != nil {
		return Knowledge{}, fmt.Errorf("commit knowledge attachment create: %w", err)
	}
	return item, nil
}

func (s *Store) UpdateKnowledgeWithAttachments(ctx context.Context, knowledgeID, revision int64, input SaveKnowledgeInput, uploaderUserID int64, draftTokenHash string, attachmentUUIDs []string, now time.Time) (Knowledge, error) {
	if knowledgeID < 1 || revision < 1 || uploaderUserID < 1 || draftTokenHash == "" {
		return Knowledge{}, ErrInvalidInput
	}
	normalized, err := normalizeKnowledgeInput(input)
	if err != nil {
		return Knowledge{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Knowledge{}, fmt.Errorf("begin knowledge attachment update: %w", err)
	}
	defer tx.Rollback()
	if err := validateKnowledgeAttachmentBindings(ctx, tx, knowledgeID, uploaderUserID, draftTokenHash, attachmentUUIDs, now); err != nil {
		return Knowledge{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE knowledge SET language = ?, category = ?, title = ?, body = ?, visible = ?, revision = revision + 1, updated_at = ?
		WHERE id = ? AND revision = ?
	`, normalized.Language, normalized.Category, normalized.Title, normalized.Body, normalized.Visible, now.Unix(), knowledgeID, revision)
	if err != nil {
		return Knowledge{}, fmt.Errorf("update knowledge with attachments: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		if _, findErr := getKnowledgeTx(ctx, tx, knowledgeID); errors.Is(findErr, ErrNotFound) {
			return Knowledge{}, ErrNotFound
		}
		return Knowledge{}, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE knowledge_attachments SET deleted_at = COALESCE(deleted_at, ?), updated_at = ?
		WHERE knowledge_id = ? AND status = 'ready'
	`, now.Unix(), now.Unix(), knowledgeID); err != nil {
		return Knowledge{}, fmt.Errorf("soft-delete removed knowledge attachments: %w", err)
	}
	if err := applyKnowledgeAttachmentBindings(ctx, tx, knowledgeID, attachmentUUIDs, now); err != nil {
		return Knowledge{}, err
	}
	item, err := getKnowledgeTx(ctx, tx, knowledgeID)
	if err != nil {
		return Knowledge{}, err
	}
	if err := tx.Commit(); err != nil {
		return Knowledge{}, fmt.Errorf("commit knowledge attachment update: %w", err)
	}
	return item, nil
}

func (s *Store) DeleteKnowledgeWithAttachments(ctx context.Context, knowledgeID, revision int64, now time.Time) error {
	if knowledgeID < 1 || revision < 1 {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin knowledge attachment delete: %w", err)
	}
	defer tx.Rollback()
	var currentRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM knowledge WHERE id = ?`, knowledgeID).Scan(&currentRevision); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("inspect knowledge attachment delete: %w", err)
	} else if currentRevision != revision {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE knowledge_attachments SET deleted_at = COALESCE(deleted_at, ?), updated_at = ?
		WHERE knowledge_id = ?
	`, now.Unix(), now.Unix(), knowledgeID); err != nil {
		return fmt.Errorf("soft-delete removed article attachments: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM knowledge WHERE id = ? AND revision = ?`, knowledgeID, revision)
	if err != nil {
		return fmt.Errorf("delete knowledge with attachments: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge WHERE id = ?`, knowledgeID).Scan(&exists); err != nil {
			return fmt.Errorf("inspect knowledge delete conflict: %w", err)
		}
		if exists == 0 {
			return ErrNotFound
		}
		return ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit knowledge attachment delete: %w", err)
	}
	return nil
}

func validateKnowledgeAttachmentBindings(ctx context.Context, tx *sql.Tx, knowledgeID, uploaderUserID int64, draftTokenHash string, attachmentUUIDs []string, now time.Time) error {
	var activeUploads int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM knowledge_attachment_uploads
		WHERE uploader_user_id = ? AND draft_token_hash = ?
		AND status IN ('initialized', 'uploading', 'completing', 'failed') AND expires_at > ?
	`, uploaderUserID, draftTokenHash, now.Unix()).Scan(&activeUploads); err != nil {
		return fmt.Errorf("inspect unfinished attachment uploads: %w", err)
	}
	if activeUploads != 0 {
		return ErrConflict
	}
	for _, attachmentUUID := range attachmentUUIDs {
		var boundKnowledgeID sql.NullInt64
		var deletedAt sql.NullInt64
		var ownerID int64
		var storedDraftHash, status string
		err := tx.QueryRowContext(ctx, `
			SELECT knowledge_id, uploader_user_id, COALESCE(draft_token_hash, ''), status, deleted_at
			FROM knowledge_attachments WHERE uuid = ?
		`, attachmentUUID).Scan(&boundKnowledgeID, &ownerID, &storedDraftHash, &status, &deletedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidInput
		}
		if err != nil {
			return fmt.Errorf("validate knowledge attachment: %w", err)
		}
		if status != KnowledgeAttachmentReady {
			return ErrInvalidInput
		}
		if boundKnowledgeID.Valid {
			if knowledgeID == 0 || boundKnowledgeID.Int64 != knowledgeID {
				return ErrInvalidInput
			}
			continue
		}
		if deletedAt.Valid || ownerID != uploaderUserID || storedDraftHash != draftTokenHash {
			return ErrInvalidInput
		}
	}
	return nil
}

func applyKnowledgeAttachmentBindings(ctx context.Context, tx *sql.Tx, knowledgeID int64, attachmentUUIDs []string, now time.Time) error {
	for _, attachmentUUID := range attachmentUUIDs {
		result, err := tx.ExecContext(ctx, `
			UPDATE knowledge_attachments
			SET knowledge_id = ?, draft_token_hash = NULL, deleted_at = NULL, updated_at = ?
			WHERE uuid = ? AND status = 'ready'
		`, knowledgeID, now.Unix(), attachmentUUID)
		if err != nil {
			return fmt.Errorf("bind knowledge attachment: %w", err)
		}
		rows, _ := result.RowsAffected()
		if rows != 1 {
			return ErrConflict
		}
	}
	return nil
}

func getKnowledgeTx(ctx context.Context, tx *sql.Tx, knowledgeID int64) (Knowledge, error) {
	return scanKnowledge(tx.QueryRowContext(ctx, knowledgeSelect+` WHERE id = ?`, knowledgeID))
}

func (s *Store) GetKnowledgeAttachmentUpload(ctx context.Context, uploaderUserID int64, uploadUUID string, now time.Time) (KnowledgeAttachmentUpload, error) {
	upload, err := scanKnowledgeAttachmentUpload(s.db.QueryRowContext(ctx, knowledgeAttachmentUploadSelect+` WHERE uploader_user_id = ? AND uuid = ?`, uploaderUserID, uploadUUID))
	if err != nil {
		return KnowledgeAttachmentUpload{}, err
	}
	chunks, err := s.listKnowledgeAttachmentChunks(ctx, upload.ID)
	if err != nil {
		return KnowledgeAttachmentUpload{}, err
	}
	upload.UploadedChunks = chunks
	_ = now // expiry mutation is deliberately left to bounded cleanup
	return upload, nil
}

func (s *Store) ExpireKnowledgeAttachmentUpload(ctx context.Context, uploaderUserID int64, uploadUUID string, now time.Time) (KnowledgeAttachmentUpload, error) {
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE knowledge_attachment_uploads SET status = 'expired', updated_at = ?
		WHERE uploader_user_id = ? AND uuid = ? AND expires_at <= ?
		AND status IN ('initialized', 'uploading', 'failed')
	`, now.Unix(), uploaderUserID, uploadUUID, now.Unix())
	if err != nil {
		return KnowledgeAttachmentUpload{}, fmt.Errorf("expire attachment upload: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return KnowledgeAttachmentUpload{}, ErrNotFound
	}
	return s.GetKnowledgeAttachmentUpload(ctx, uploaderUserID, uploadUUID, now)
}

func (s *Store) GetReadyKnowledgeAttachment(ctx context.Context, attachmentUUID string) (KnowledgeAttachment, error) {
	return scanKnowledgeAttachment(s.db.QueryRowContext(ctx, knowledgeAttachmentSelect+` WHERE uuid = ? AND status = 'ready' AND deleted_at IS NULL`, attachmentUUID))
}

func (s *Store) GetKnowledgeAttachmentForArticle(ctx context.Context, knowledgeID int64, attachmentUUID string) (KnowledgeAttachment, error) {
	return scanKnowledgeAttachment(s.db.QueryRowContext(ctx, knowledgeAttachmentSelect+`
		WHERE knowledge_id = ? AND uuid = ? AND status = 'ready' AND deleted_at IS NULL
	`, knowledgeID, attachmentUUID))
}

func (s *Store) GetKnowledgeAttachmentsForArticle(ctx context.Context, knowledgeID int64, attachmentUUIDs []string) (map[string]KnowledgeAttachment, error) {
	if knowledgeID < 1 || len(attachmentUUIDs) < 1 || len(attachmentUUIDs) > 1000 {
		return nil, ErrInvalidInput
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(attachmentUUIDs)), ",")
	arguments := make([]any, 0, len(attachmentUUIDs)+1)
	arguments = append(arguments, knowledgeID)
	for _, attachmentUUID := range attachmentUUIDs {
		arguments = append(arguments, attachmentUUID)
	}
	rows, err := s.db.QueryContext(ctx, knowledgeAttachmentSelect+`
		WHERE knowledge_id = ? AND status = 'ready' AND deleted_at IS NULL AND uuid IN (`+placeholders+`)
	`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list knowledge article attachments: %w", err)
	}
	defer rows.Close()
	items := make(map[string]KnowledgeAttachment, len(attachmentUUIDs))
	for rows.Next() {
		item, err := scanKnowledgeAttachment(rows)
		if err != nil {
			return nil, err
		}
		items[item.UUID] = item
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) GetKnowledgeAttachmentByUpload(ctx context.Context, uploaderUserID int64, uploadUUID string) (KnowledgeAttachment, error) {
	return scanKnowledgeAttachment(s.db.QueryRowContext(ctx, knowledgeAttachmentSelect+` WHERE uploader_user_id = ? AND uuid = ? AND deleted_at IS NULL`, uploaderUserID, uploadUUID))
}

func (s *Store) GetDraftKnowledgeAttachment(ctx context.Context, uploaderUserID int64, attachmentUUID string) (KnowledgeAttachment, error) {
	return scanKnowledgeAttachment(s.db.QueryRowContext(ctx, knowledgeAttachmentSelect+`
		WHERE uploader_user_id = ? AND uuid = ? AND knowledge_id IS NULL AND deleted_at IS NULL
	`, uploaderUserID, attachmentUUID))
}

func (s *Store) ListKnowledgeAttachments(ctx context.Context, uploaderUserID int64, knowledgeID *int64, draftTokenHash *string, page, perPage int) (KnowledgeAttachmentPage, error) {
	if uploaderUserID < 1 || page < 1 || perPage < 1 || perPage > 100 {
		return KnowledgeAttachmentPage{}, ErrInvalidInput
	}
	where := `deleted_at IS NULL`
	arguments := make([]any, 0, 3)
	if knowledgeID != nil {
		where += ` AND knowledge_id = ?`
		arguments = append(arguments, *knowledgeID)
	}
	if draftTokenHash != nil {
		where += ` AND uploader_user_id = ? AND draft_token_hash = ?`
		arguments = append(arguments, uploaderUserID, *draftTokenHash)
	}
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_attachments WHERE `+where, arguments...).Scan(&total); err != nil {
		return KnowledgeAttachmentPage{}, fmt.Errorf("count knowledge attachments: %w", err)
	}
	queryArguments := append(append([]any(nil), arguments...), perPage, (page-1)*perPage)
	rows, err := s.db.QueryContext(ctx, knowledgeAttachmentSelect+` WHERE `+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, queryArguments...)
	if err != nil {
		return KnowledgeAttachmentPage{}, fmt.Errorf("list knowledge attachments: %w", err)
	}
	defer rows.Close()
	items := make([]KnowledgeAttachment, 0, min(perPage, int(total)))
	for rows.Next() {
		item, err := scanKnowledgeAttachment(rows)
		if err != nil {
			return KnowledgeAttachmentPage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return KnowledgeAttachmentPage{}, err
	}
	return KnowledgeAttachmentPage{Items: items, Total: total, Page: page, PerPage: perPage}, nil
}

func (s *Store) GetPublicKnowledgeAttachment(ctx context.Context, attachmentUUID string) (KnowledgeAttachment, error) {
	return scanKnowledgeAttachment(s.db.QueryRowContext(ctx, `
		SELECT a.id, a.uuid, a.knowledge_id, a.uploader_user_id, a.draft_token_hash, a.original_name,
			a.storage_path, a.mime_type, a.extension, a.size, a.sha256, a.status, a.created_at, a.updated_at, a.deleted_at
		FROM knowledge_attachments a JOIN knowledge k ON k.id = a.knowledge_id
		WHERE a.uuid = ? AND a.status = 'ready'
		AND a.deleted_at IS NULL AND k.visible = 1
		AND instr(k.body, 'knowledge-attachment://' || a.uuid) > 0
	`, attachmentUUID))
}

func (s *Store) CancelKnowledgeAttachmentUpload(ctx context.Context, uploaderUserID int64, uploadUUID string) error {
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM knowledge_attachment_uploads
		WHERE uploader_user_id = ? AND uuid = ? AND status IN ('initialized', 'uploading', 'failed', 'expired')
	`, uploaderUserID, uploadUUID)
	if err != nil {
		return fmt.Errorf("cancel attachment upload: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteDraftKnowledgeAttachment(ctx context.Context, uploaderUserID int64, attachmentUUID, draftTokenHash string) error {
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM knowledge_attachments
		WHERE uploader_user_id = ? AND uuid = ? AND knowledge_id IS NULL
		AND draft_token_hash = ? AND deleted_at IS NULL
	`, uploaderUserID, attachmentUUID, draftTokenHash)
	if err != nil {
		return fmt.Errorf("delete draft attachment: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListExpiredKnowledgeAttachmentUploads(ctx context.Context, now time.Time, limit int) ([]KnowledgeAttachmentUpload, error) {
	if limit < 1 || limit > 1000 {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, knowledgeAttachmentUploadSelect+`
		WHERE expires_at <= ? AND status IN ('initialized', 'uploading', 'completing', 'failed', 'expired')
		ORDER BY expires_at, id LIMIT ?
	`, now.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("list expired attachment uploads: %w", err)
	}
	defer rows.Close()
	items := make([]KnowledgeAttachmentUpload, 0, limit)
	for rows.Next() {
		item, err := scanKnowledgeAttachmentUpload(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) DeleteExpiredKnowledgeAttachmentUpload(ctx context.Context, uploadID int64, now time.Time) error {
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM knowledge_attachment_uploads WHERE id = ? AND expires_at <= ?
		AND status IN ('initialized', 'uploading', 'completing', 'failed', 'expired')
	`, uploadID, now.Unix())
	if err != nil {
		return fmt.Errorf("delete expired attachment upload: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) SoftDeleteStaleDraftKnowledgeAttachments(ctx context.Context, cutoff, now time.Time, limit int) (int64, error) {
	if limit < 1 || limit > 1000 {
		return 0, ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE knowledge_attachments SET deleted_at = ?, updated_at = ?
		WHERE id IN (
			SELECT id FROM knowledge_attachments WHERE knowledge_id IS NULL AND draft_token_hash IS NOT NULL
			AND deleted_at IS NULL AND created_at <= ? ORDER BY created_at, id LIMIT ?
		)
	`, now.Unix(), now.Unix(), cutoff.Unix(), limit)
	if err != nil {
		return 0, fmt.Errorf("soft-delete stale draft attachments: %w", err)
	}
	rows, _ := result.RowsAffected()
	return rows, nil
}

func (s *Store) ListPurgeableKnowledgeAttachments(ctx context.Context, cutoff time.Time, limit int) ([]KnowledgeAttachment, error) {
	if limit < 1 || limit > 1000 {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, knowledgeAttachmentSelect+`
		WHERE deleted_at IS NOT NULL AND deleted_at <= ? ORDER BY deleted_at, id LIMIT ?
	`, cutoff.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("list purgeable attachments: %w", err)
	}
	defer rows.Close()
	items := make([]KnowledgeAttachment, 0, limit)
	for rows.Next() {
		item, err := scanKnowledgeAttachment(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) KnowledgeAttachmentStoragePathExists(ctx context.Context, storagePath string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_attachments WHERE storage_path = ?)`, storagePath).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check attachment storage path: %w", err)
	}
	return exists == 1, nil
}

func (s *Store) KnowledgeAttachmentTemporaryPathExists(ctx context.Context, relativeFilePath string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM knowledge_attachment_uploads
			WHERE ? = temporary_path OR ? LIKE temporary_path || '/%'
		)
	`, relativeFilePath, relativeFilePath).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check attachment temporary path: %w", err)
	}
	return exists == 1, nil
}

func (s *Store) DeletePurgedKnowledgeAttachment(ctx context.Context, attachmentID int64, cutoff time.Time) error {
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `DELETE FROM knowledge_attachments WHERE id = ? AND deleted_at IS NOT NULL AND deleted_at <= ?`, attachmentID, cutoff.Unix())
	if err != nil {
		return fmt.Errorf("delete purged attachment: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

// RecordKnowledgeAttachmentChunk returns idempotent only when size and digest
// are identical to an existing verified chunk row.
func (s *Store) RecordKnowledgeAttachmentChunk(ctx context.Context, uploaderUserID int64, uploadUUID string, index int, size int64, digest string, now time.Time) (upload KnowledgeAttachmentUpload, idempotent bool, err error) {
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return upload, false, fmt.Errorf("begin attachment chunk record: %w", err)
	}
	defer tx.Rollback()
	upload, err = getKnowledgeAttachmentUploadTx(ctx, tx, uploaderUserID, uploadUUID)
	if err != nil {
		return upload, false, err
	}
	if upload.Status != KnowledgeUploadInitialized && upload.Status != KnowledgeUploadUploading && upload.Status != KnowledgeUploadFailed {
		return upload, false, ErrConflict
	}
	if index < 0 || index >= upload.TotalChunks {
		return upload, false, ErrInvalidInput
	}
	var existingSize int64
	var existingDigest string
	err = tx.QueryRowContext(ctx, `SELECT size, sha256 FROM knowledge_attachment_chunks WHERE upload_id = ? AND chunk_index = ?`, upload.ID, index).Scan(&existingSize, &existingDigest)
	if err == nil {
		if existingSize != size || existingDigest != digest {
			return upload, false, ErrConflict
		}
		idempotent = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return upload, false, fmt.Errorf("read attachment chunk: %w", err)
	} else {
		if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_attachment_chunks (upload_id, chunk_index, size, sha256, created_at) VALUES (?, ?, ?, ?, ?)`, upload.ID, index, size, digest, now.Unix()); err != nil {
			return upload, false, fmt.Errorf("record attachment chunk: %w", err)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE knowledge_attachment_uploads SET received_chunks = received_chunks + 1, status = 'uploading', updated_at = ? WHERE id = ?`, now.Unix(), upload.ID); err != nil {
			return upload, false, fmt.Errorf("advance attachment upload: %w", err)
		}
	}
	if idempotent && upload.Status == KnowledgeUploadFailed {
		if _, err = tx.ExecContext(ctx, `UPDATE knowledge_attachment_uploads SET status = 'uploading', updated_at = ? WHERE id = ?`, now.Unix(), upload.ID); err != nil {
			return upload, false, fmt.Errorf("resume failed attachment upload: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return upload, false, fmt.Errorf("commit attachment chunk: %w", err)
	}
	upload, err = s.GetKnowledgeAttachmentUpload(ctx, uploaderUserID, uploadUUID, now)
	return upload, idempotent, err
}

func (s *Store) BeginKnowledgeAttachmentCompletion(ctx context.Context, uploaderUserID int64, uploadUUID string, now time.Time) (KnowledgeAttachmentUpload, error) {
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KnowledgeAttachmentUpload{}, fmt.Errorf("begin attachment completion: %w", err)
	}
	defer tx.Rollback()
	upload, err := getKnowledgeAttachmentUploadTx(ctx, tx, uploaderUserID, uploadUUID)
	if err != nil {
		return KnowledgeAttachmentUpload{}, err
	}
	if upload.Status == KnowledgeUploadCompleted {
		return upload, nil
	}
	if upload.Status != KnowledgeUploadUploading || upload.ReceivedChunks != upload.TotalChunks {
		return KnowledgeAttachmentUpload{}, ErrConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE knowledge_attachment_uploads SET status = 'completing', updated_at = ? WHERE id = ? AND status = 'uploading'`, now.Unix(), upload.ID)
	if err != nil {
		return KnowledgeAttachmentUpload{}, fmt.Errorf("mark attachment completing: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return KnowledgeAttachmentUpload{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeAttachmentUpload{}, fmt.Errorf("commit attachment completion start: %w", err)
	}
	return s.GetKnowledgeAttachmentUpload(ctx, uploaderUserID, uploadUUID, now)
}

func (s *Store) FinishKnowledgeAttachmentUpload(ctx context.Context, input CompleteKnowledgeAttachmentInput, now time.Time) (KnowledgeAttachment, error) {
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KnowledgeAttachment{}, fmt.Errorf("begin finish attachment: %w", err)
	}
	defer tx.Rollback()
	upload, err := getKnowledgeAttachmentUploadTx(ctx, tx, input.UploaderUserID, input.UUID)
	if err != nil {
		return KnowledgeAttachment{}, err
	}
	if upload.Status == KnowledgeUploadCompleted {
		return getKnowledgeAttachmentTx(ctx, tx, input.UploaderUserID, input.UUID)
	}
	if upload.Status != KnowledgeUploadCompleting {
		return KnowledgeAttachment{}, ErrConflict
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO knowledge_attachments (
			uuid, uploader_user_id, draft_token_hash, original_name, storage_path, mime_type, extension,
			size, sha256, status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'ready', ?, ?)
	`, input.UUID, input.UploaderUserID, upload.DraftTokenHash, input.OriginalName, input.StoragePath,
		input.MIMEType, input.Extension, input.Size, input.SHA256, now.Unix(), now.Unix())
	if err != nil {
		return KnowledgeAttachment{}, fmt.Errorf("create verified attachment: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_attachment_uploads SET status = 'completed', updated_at = ? WHERE id = ?`, now.Unix(), upload.ID); err != nil {
		return KnowledgeAttachment{}, fmt.Errorf("complete attachment upload: %w", err)
	}
	attachment, err := getKnowledgeAttachmentTx(ctx, tx, input.UploaderUserID, input.UUID)
	if err != nil {
		return KnowledgeAttachment{}, err
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeAttachment{}, fmt.Errorf("commit verified attachment: %w", err)
	}
	return attachment, nil
}

func (s *Store) ResetKnowledgeAttachmentUpload(ctx context.Context, uploaderUserID int64, uploadUUID string, now time.Time) error {
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reset attachment upload: %w", err)
	}
	defer tx.Rollback()
	upload, err := getKnowledgeAttachmentUploadTx(ctx, tx, uploaderUserID, uploadUUID)
	if err != nil {
		return err
	}
	if upload.Status == KnowledgeUploadCompleted {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_attachment_chunks WHERE upload_id = ?`, upload.ID); err != nil {
		return fmt.Errorf("clear attachment chunks: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_attachment_uploads SET received_chunks = 0, status = 'initialized', updated_at = ? WHERE id = ?`, now.Unix(), upload.ID); err != nil {
		return fmt.Errorf("reset attachment session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit attachment reset: %w", err)
	}
	return nil
}

func (s *Store) listKnowledgeAttachmentChunks(ctx context.Context, uploadID int64) ([]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT chunk_index FROM knowledge_attachment_chunks WHERE upload_id = ? ORDER BY chunk_index`, uploadID)
	if err != nil {
		return nil, fmt.Errorf("list attachment chunks: %w", err)
	}
	defer rows.Close()
	chunks := make([]int, 0)
	for rows.Next() {
		var index int
		if err := rows.Scan(&index); err != nil {
			return nil, fmt.Errorf("scan attachment chunk: %w", err)
		}
		chunks = append(chunks, index)
	}
	return chunks, rows.Err()
}

func getKnowledgeAttachmentUploadTx(ctx context.Context, tx *sql.Tx, uploaderUserID int64, uploadUUID string) (KnowledgeAttachmentUpload, error) {
	return scanKnowledgeAttachmentUpload(tx.QueryRowContext(ctx, knowledgeAttachmentUploadSelect+` WHERE uploader_user_id = ? AND uuid = ?`, uploaderUserID, uploadUUID))
}

func getKnowledgeAttachmentTx(ctx context.Context, tx *sql.Tx, uploaderUserID int64, attachmentUUID string) (KnowledgeAttachment, error) {
	return scanKnowledgeAttachment(tx.QueryRowContext(ctx, knowledgeAttachmentSelect+` WHERE uploader_user_id = ? AND uuid = ? AND deleted_at IS NULL`, uploaderUserID, attachmentUUID))
}

func scanKnowledgeAttachmentUpload(row rowScanner) (KnowledgeAttachmentUpload, error) {
	var item KnowledgeAttachmentUpload
	var expectedDigest sql.NullString
	var expiresAt, createdAt, updatedAt int64
	err := row.Scan(&item.ID, &item.UUID, &item.UploaderUserID, &item.DraftTokenHash, &item.OriginalName,
		&item.DeclaredSize, &expectedDigest, &item.ChunkSize, &item.TotalChunks, &item.ReceivedChunks,
		&item.TemporaryPath, &item.Status, &expiresAt, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeAttachmentUpload{}, ErrNotFound
	}
	if err != nil {
		return KnowledgeAttachmentUpload{}, fmt.Errorf("scan attachment upload: %w", err)
	}
	if expectedDigest.Valid {
		item.ExpectedSHA256 = &expectedDigest.String
	}
	item.ExpiresAt = time.Unix(expiresAt, 0).UTC()
	item.CreatedAt = time.Unix(createdAt, 0).UTC()
	item.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return item, nil
}

func scanKnowledgeAttachment(row rowScanner) (KnowledgeAttachment, error) {
	var item KnowledgeAttachment
	var knowledgeID sql.NullInt64
	var draftHash, extension sql.NullString
	var createdAt, updatedAt int64
	var deletedAt sql.NullInt64
	err := row.Scan(&item.ID, &item.UUID, &knowledgeID, &item.UploaderUserID, &draftHash,
		&item.OriginalName, &item.StoragePath, &item.MIMEType, &extension, &item.Size,
		&item.SHA256, &item.Status, &createdAt, &updatedAt, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeAttachment{}, ErrNotFound
	}
	if err != nil {
		return KnowledgeAttachment{}, fmt.Errorf("scan attachment: %w", err)
	}
	if knowledgeID.Valid {
		item.KnowledgeID = &knowledgeID.Int64
	}
	if draftHash.Valid {
		item.DraftTokenHash = &draftHash.String
	}
	if extension.Valid {
		item.Extension = &extension.String
	}
	item.CreatedAt = time.Unix(createdAt, 0).UTC()
	item.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	if deletedAt.Valid {
		value := time.Unix(deletedAt.Int64, 0).UTC()
		item.DeletedAt = &value
	}
	return item, nil
}

const knowledgeAttachmentUploadSelect = `
	SELECT id, uuid, uploader_user_id, draft_token_hash, original_name, declared_size, expected_sha256,
		chunk_size, total_chunks, received_chunks, temporary_path, status, expires_at, created_at, updated_at
	FROM knowledge_attachment_uploads`

const knowledgeAttachmentSelect = `
	SELECT id, uuid, knowledge_id, uploader_user_id, draft_token_hash, original_name, storage_path,
		mime_type, extension, size, sha256, status, created_at, updated_at, deleted_at
	FROM knowledge_attachments`
