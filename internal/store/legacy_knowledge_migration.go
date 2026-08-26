package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	LegacyKnowledgeSlice      = "knowledge-v1"
	maxLegacyKnowledgeRows    = 10_000
	maxLegacyKnowledgeFiles   = 1_000_000
	maxLegacyKnowledgeBytes   = 64 << 20
	legacyAttachmentURIScheme = "knowledge-attachment://"
)

type LegacyKnowledgeArticle struct {
	ID           int64  `json:"id"`
	Language     string `json:"language"`
	Category     string `json:"category"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	SortPosition int    `json:"sort"`
	Visible      bool   `json:"show"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

type LegacyKnowledgeAttachment struct {
	ID             int64   `json:"id"`
	UUID           string  `json:"uuid"`
	KnowledgeID    *int64  `json:"knowledge_id"`
	UploaderUserID int64   `json:"uploader_user_id"`
	DraftTokenHash *string `json:"draft_token_hash"`
	OriginalName   string  `json:"original_name"`
	StoragePath    string  `json:"storage_path"`
	MIMEType       string  `json:"mime_type"`
	Extension      *string `json:"extension"`
	Size           int64   `json:"size"`
	SHA256         string  `json:"sha256"`
	Status         string  `json:"status"`
	CreatedAt      int64   `json:"created_at"`
	UpdatedAt      int64   `json:"updated_at"`
	DeletedAt      *int64  `json:"deleted_at"`
}

type LegacyKnowledgeUploadChunk struct {
	Index     int    `json:"index"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	CreatedAt int64  `json:"created_at"`
}

type LegacyKnowledgeUpload struct {
	ID             int64                        `json:"id"`
	UUID           string                       `json:"uuid"`
	UploaderUserID int64                        `json:"uploader_user_id"`
	DraftTokenHash string                       `json:"draft_token_hash"`
	OriginalName   string                       `json:"original_name"`
	DeclaredSize   int64                        `json:"declared_size"`
	ExpectedSHA256 *string                      `json:"expected_sha256"`
	ChunkSize      int64                        `json:"chunk_size"`
	TotalChunks    int                          `json:"total_chunks"`
	ReceivedChunks int                          `json:"received_chunks"`
	TemporaryPath  string                       `json:"temporary_path"`
	Status         string                       `json:"status"`
	ExpiresAt      int64                        `json:"expires_at"`
	CreatedAt      int64                        `json:"created_at"`
	UpdatedAt      int64                        `json:"updated_at"`
	Chunks         []LegacyKnowledgeUploadChunk `json:"chunks"`
}

type LegacyKnowledgeImport struct {
	Slice                string
	SourceSHA256         string
	SourceSize           int64
	Articles             []LegacyKnowledgeArticle
	Checksum             string
	Attachments          []LegacyKnowledgeAttachment
	AttachmentsChecksum  string
	Uploads              []LegacyKnowledgeUpload
	UploadsChecksum      string
	RollbackBackupPath   string
	RollbackBackupSHA256 string
}

type LegacyKnowledgeImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Articles             LegacyDomainResult `json:"articles"`
	Attachments          LegacyDomainResult `json:"attachments"`
	Uploads              LegacyDomainResult `json:"uploads"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func LegacyKnowledgeChecksum(articles []LegacyKnowledgeArticle) string {
	ordered := append([]LegacyKnowledgeArticle(nil), articles...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	digest := sha256.New()
	_, _ = digest.Write([]byte{'['})
	for index, article := range ordered {
		if index != 0 {
			_, _ = digest.Write([]byte{','})
		}
		encoded, err := json.Marshal(article)
		if err != nil {
			panic(fmt.Sprintf("marshal canonical legacy knowledge article: %v", err))
		}
		_, _ = digest.Write(encoded)
	}
	_, _ = digest.Write([]byte{']'})
	return hex.EncodeToString(digest.Sum(nil))
}

func LegacyKnowledgeAttachmentsChecksum(attachments []LegacyKnowledgeAttachment) string {
	ordered := append([]LegacyKnowledgeAttachment(nil), attachments...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	digest := sha256.New()
	_, _ = digest.Write([]byte{'['})
	for index, attachment := range ordered {
		if index != 0 {
			_, _ = digest.Write([]byte{','})
		}
		encoded, err := json.Marshal(attachment)
		if err != nil {
			panic(fmt.Sprintf("marshal canonical legacy knowledge attachment: %v", err))
		}
		_, _ = digest.Write(encoded)
	}
	_, _ = digest.Write([]byte{']'})
	return hex.EncodeToString(digest.Sum(nil))
}

func LegacyKnowledgeUploadsChecksum(uploads []LegacyKnowledgeUpload) string {
	ordered := append([]LegacyKnowledgeUpload(nil), uploads...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	digest := sha256.New()
	_, _ = digest.Write([]byte{'['})
	for index, upload := range ordered {
		if index != 0 {
			_, _ = digest.Write([]byte{','})
		}
		upload.Chunks = append([]LegacyKnowledgeUploadChunk(nil), upload.Chunks...)
		sort.Slice(upload.Chunks, func(left, right int) bool { return upload.Chunks[left].Index < upload.Chunks[right].Index })
		encoded, err := json.Marshal(upload)
		if err != nil {
			panic(fmt.Sprintf("marshal canonical legacy knowledge upload: %v", err))
		}
		_, _ = digest.Write(encoded)
	}
	_, _ = digest.Write([]byte{']'})
	return hex.EncodeToString(digest.Sum(nil))
}

func (s *Store) LookupLegacyKnowledgeImport(ctx context.Context, sourceSHA256 string) (LegacyKnowledgeImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyKnowledgeImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacyKnowledgeImport(ctx, s.db, sourceSHA256)
}

func lookupLegacyKnowledgeImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyKnowledgeImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `
		SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?
	`, LegacyKnowledgeSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyKnowledgeImportReport{}, false, nil
	}
	if err != nil {
		return LegacyKnowledgeImportReport{}, false, fmt.Errorf("lookup legacy knowledge migration: %w", err)
	}
	var report LegacyKnowledgeImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyKnowledgeImportReport{}, false, fmt.Errorf("decode legacy knowledge migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyKnowledge(ctx context.Context, input LegacyKnowledgeImport, now time.Time) (LegacyKnowledgeImportReport, error) {
	if err := validateLegacyKnowledgeImport(input); err != nil {
		return LegacyKnowledgeImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyKnowledgeImportReport{}, fmt.Errorf("begin legacy knowledge import: %w", err)
	}
	defer tx.Rollback()

	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return LegacyKnowledgeImportReport{}, fmt.Errorf("read legacy knowledge target schema: %w", err)
	}
	if version != CurrentSchemaVersion() {
		return LegacyKnowledgeImportReport{}, fmt.Errorf("legacy knowledge import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyKnowledgeImportReport{}, fmt.Errorf("validate legacy knowledge target schema: %w", err)
	}
	if existing, found, err := lookupLegacyKnowledgeImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyKnowledgeImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyKnowledgeImportReport{}, fmt.Errorf("commit idempotent legacy knowledge import: %w", err)
		}
		return existing, nil
	}
	var otherRuns int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyKnowledgeSlice).Scan(&otherRuns); err != nil {
		return LegacyKnowledgeImportReport{}, fmt.Errorf("count legacy knowledge migrations: %w", err)
	}
	if otherRuns != 0 {
		return LegacyKnowledgeImportReport{}, fmt.Errorf("%w: legacy knowledge slice was already imported from another snapshot", ErrConflict)
	}
	var existingArticles int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge`).Scan(&existingArticles); err != nil {
		return LegacyKnowledgeImportReport{}, fmt.Errorf("count target knowledge: %w", err)
	}
	if existingArticles != 0 {
		return LegacyKnowledgeImportReport{}, fmt.Errorf("%w: legacy knowledge import requires an empty target knowledge table", ErrConflict)
	}
	var existingAttachments, existingUploads int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_attachments`).Scan(&existingAttachments); err != nil {
		return LegacyKnowledgeImportReport{}, fmt.Errorf("count target knowledge attachments: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_attachment_uploads`).Scan(&existingUploads); err != nil {
		return LegacyKnowledgeImportReport{}, fmt.Errorf("count target knowledge attachment uploads: %w", err)
	}
	if existingAttachments != 0 || existingUploads != 0 {
		return LegacyKnowledgeImportReport{}, fmt.Errorf("%w: legacy knowledge import requires empty target attachment tables", ErrConflict)
	}

	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO knowledge
		(id, language, category, title, body, sort_position, visible, revision, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	`)
	if err != nil {
		return LegacyKnowledgeImportReport{}, fmt.Errorf("prepare legacy knowledge import: %w", err)
	}
	defer statement.Close()
	for _, article := range input.Articles {
		if _, err := statement.ExecContext(ctx, article.ID, article.Language, article.Category, article.Title, article.Body,
			article.SortPosition, article.Visible, article.CreatedAt, article.UpdatedAt); err != nil {
			return LegacyKnowledgeImportReport{}, fmt.Errorf("import legacy knowledge id %d: %w", article.ID, err)
		}
	}
	attachmentStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO knowledge_attachments
		(id, uuid, knowledge_id, uploader_user_id, draft_token_hash, original_name, storage_path, mime_type,
		 extension, size, sha256, status, created_at, updated_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return LegacyKnowledgeImportReport{}, fmt.Errorf("prepare legacy knowledge attachment import: %w", err)
	}
	defer attachmentStatement.Close()
	for _, attachment := range input.Attachments {
		if _, err := attachmentStatement.ExecContext(ctx, attachment.ID, attachment.UUID, attachment.KnowledgeID,
			attachment.UploaderUserID, attachment.DraftTokenHash, attachment.OriginalName, attachment.StoragePath,
			attachment.MIMEType, attachment.Extension, attachment.Size, attachment.SHA256, attachment.Status,
			attachment.CreatedAt, attachment.UpdatedAt, attachment.DeletedAt); err != nil {
			return LegacyKnowledgeImportReport{}, fmt.Errorf("import legacy knowledge attachment id %d: %w", attachment.ID, err)
		}
	}
	uploadStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO knowledge_attachment_uploads
		(id, uuid, uploader_user_id, draft_token_hash, original_name, declared_size, expected_sha256,
		 chunk_size, total_chunks, received_chunks, temporary_path, status, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return LegacyKnowledgeImportReport{}, fmt.Errorf("prepare legacy knowledge upload import: %w", err)
	}
	defer uploadStatement.Close()
	chunkStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO knowledge_attachment_chunks (upload_id, chunk_index, size, sha256, created_at)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return LegacyKnowledgeImportReport{}, fmt.Errorf("prepare legacy knowledge upload chunk import: %w", err)
	}
	defer chunkStatement.Close()
	for _, upload := range input.Uploads {
		if _, err := uploadStatement.ExecContext(ctx, upload.ID, upload.UUID, upload.UploaderUserID, upload.DraftTokenHash,
			upload.OriginalName, upload.DeclaredSize, upload.ExpectedSHA256, upload.ChunkSize, upload.TotalChunks,
			upload.ReceivedChunks, upload.TemporaryPath, upload.Status, upload.ExpiresAt, upload.CreatedAt, upload.UpdatedAt); err != nil {
			return LegacyKnowledgeImportReport{}, fmt.Errorf("import legacy knowledge upload id %d: %w", upload.ID, err)
		}
		for _, chunk := range upload.Chunks {
			if _, err := chunkStatement.ExecContext(ctx, upload.ID, chunk.Index, chunk.Size, chunk.SHA256, chunk.CreatedAt); err != nil {
				return LegacyKnowledgeImportReport{}, fmt.Errorf("import legacy knowledge upload id %d chunk %d: %w", upload.ID, chunk.Index, err)
			}
		}
	}

	targetArticles, err := readLegacyTargetKnowledge(ctx, tx)
	if err != nil {
		return LegacyKnowledgeImportReport{}, err
	}
	targetAttachments, err := readLegacyTargetKnowledgeAttachments(ctx, tx)
	if err != nil {
		return LegacyKnowledgeImportReport{}, err
	}
	targetUploads, err := readLegacyTargetKnowledgeUploads(ctx, tx)
	if err != nil {
		return LegacyKnowledgeImportReport{}, err
	}
	report := LegacyKnowledgeImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Articles: LegacyDomainResult{
			SourceRows: len(input.Articles), TargetRows: len(targetArticles),
			SourceChecksum: input.Checksum, TargetChecksum: LegacyKnowledgeChecksum(targetArticles),
		},
		Attachments: LegacyDomainResult{
			SourceRows: len(input.Attachments), TargetRows: len(targetAttachments),
			SourceChecksum: input.AttachmentsChecksum, TargetChecksum: LegacyKnowledgeAttachmentsChecksum(targetAttachments),
		},
		Uploads: LegacyDomainResult{
			SourceRows: len(input.Uploads), TargetRows: len(targetUploads),
			SourceChecksum: input.UploadsChecksum, TargetChecksum: LegacyKnowledgeUploadsChecksum(targetUploads),
		},
		AppliedAt: now.UTC(), AlreadyApplied: false,
	}
	if report.Articles.SourceRows != report.Articles.TargetRows || report.Articles.SourceChecksum != report.Articles.TargetChecksum ||
		report.Attachments.SourceRows != report.Attachments.TargetRows || report.Attachments.SourceChecksum != report.Attachments.TargetChecksum ||
		report.Uploads.SourceRows != report.Uploads.TargetRows || report.Uploads.SourceChecksum != report.Uploads.TargetChecksum {
		return LegacyKnowledgeImportReport{}, errors.New("legacy knowledge target verification does not match source")
	}
	encodedReport, err := json.Marshal(report)
	if err != nil {
		return LegacyKnowledgeImportReport{}, fmt.Errorf("encode legacy knowledge migration report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO legacy_migration_runs
		(slice, source_sha256, source_size, rollback_backup_path, rollback_backup_sha256, report_json, applied_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encodedReport), report.AppliedAt.Unix()); err != nil {
		return LegacyKnowledgeImportReport{}, fmt.Errorf("record legacy knowledge migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyKnowledgeImportReport{}, fmt.Errorf("commit legacy knowledge import: %w", err)
	}
	return report, nil
}

func validateLegacyKnowledgeImport(input LegacyKnowledgeImport) error {
	if input.Slice != LegacyKnowledgeSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		!validLowerSHA256(input.RollbackBackupSHA256) || input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 ||
		!utf8.ValidString(input.RollbackBackupPath) || strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 ||
		len(input.Articles) > maxLegacyKnowledgeRows {
		return ErrInvalidInput
	}
	if input.Checksum != LegacyKnowledgeChecksum(input.Articles) {
		return fmt.Errorf("%w: legacy knowledge source checksum mismatch", ErrInvalidInput)
	}
	if input.AttachmentsChecksum != LegacyKnowledgeAttachmentsChecksum(input.Attachments) {
		return fmt.Errorf("%w: legacy knowledge attachment source checksum mismatch", ErrInvalidInput)
	}
	if input.UploadsChecksum != LegacyKnowledgeUploadsChecksum(input.Uploads) {
		return fmt.Errorf("%w: legacy knowledge upload source checksum mismatch", ErrInvalidInput)
	}
	return ValidateLegacyKnowledgeDataWithUploads(input.Articles, input.Attachments, input.Uploads)
}

func ValidateLegacyKnowledgeData(articles []LegacyKnowledgeArticle) error {
	return ValidateLegacyKnowledgeDataWithAttachments(articles, nil)
}

func ValidateLegacyKnowledgeDataWithAttachments(articles []LegacyKnowledgeArticle, attachments []LegacyKnowledgeAttachment) error {
	return ValidateLegacyKnowledgeDataWithUploads(articles, attachments, nil)
}

func ValidateLegacyKnowledgeDataWithUploads(articles []LegacyKnowledgeArticle, attachments []LegacyKnowledgeAttachment, uploads []LegacyKnowledgeUpload) error {
	return validateLegacyKnowledgeData(articles, attachments, uploads, true)
}

func ValidateLegacyKnowledgeSnapshotData(articles []LegacyKnowledgeArticle, attachments []LegacyKnowledgeAttachment, uploads []LegacyKnowledgeUpload) error {
	return validateLegacyKnowledgeData(articles, attachments, uploads, false)
}

func validateLegacyKnowledgeData(articles []LegacyKnowledgeArticle, attachments []LegacyKnowledgeAttachment, uploads []LegacyKnowledgeUpload, requireChunkMetadata bool) error {
	if len(articles) > maxLegacyKnowledgeRows {
		return ErrInvalidInput
	}
	if len(attachments) > maxLegacyKnowledgeFiles {
		return ErrInvalidInput
	}
	seen := make(map[int64]struct{}, len(articles))
	articleByID := make(map[int64]LegacyKnowledgeArticle, len(articles))
	var totalBytes int64
	for _, article := range articles {
		if article.ID < 1 || article.SortPosition < 0 || article.CreatedAt < 0 || article.UpdatedAt < article.CreatedAt {
			return fmt.Errorf("%w: invalid legacy knowledge id, sort, or timestamp", ErrInvalidInput)
		}
		if _, exists := seen[article.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy knowledge id %d", ErrInvalidInput, article.ID)
		}
		seen[article.ID] = struct{}{}
		articleByID[article.ID] = article
		normalized, err := normalizeKnowledgeInput(SaveKnowledgeInput{
			Language: article.Language, Category: article.Category, Title: article.Title, Body: article.Body, Visible: article.Visible,
		})
		if err != nil {
			return fmt.Errorf("%w: legacy knowledge id %d: %v", ErrInvalidInput, article.ID, err)
		}
		if normalized.Language != article.Language || normalized.Category != article.Category || normalized.Title != article.Title || normalized.Body != article.Body {
			return fmt.Errorf("%w: legacy knowledge id %d requires normalization", ErrInvalidInput, article.ID)
		}
		totalBytes += int64(len(article.Language) + len(article.Category) + len(article.Title) + len(article.Body))
		if totalBytes > maxLegacyKnowledgeBytes {
			return fmt.Errorf("%w: legacy knowledge exceeds the migration data limit", ErrInvalidInput)
		}
	}
	attachmentByUUID := make(map[string]LegacyKnowledgeAttachment, len(attachments))
	seenAttachmentIDs := make(map[int64]struct{}, len(attachments))
	seenPaths := make(map[string]struct{}, len(attachments))
	for _, attachment := range attachments {
		parsed, uuidErr := uuid.Parse(attachment.UUID)
		if attachment.ID < 1 || uuidErr != nil || parsed.String() != attachment.UUID || parsed.Variant() != uuid.RFC4122 || parsed.Version() < 1 || parsed.Version() > 5 || attachment.UploaderUserID < 1 ||
			attachment.OriginalName == "" || len([]byte(attachment.OriginalName)) > 1024 || !utf8.ValidString(attachment.OriginalName) ||
			attachment.StoragePath == "" || len(attachment.StoragePath) > 512 || strings.HasPrefix(attachment.StoragePath, "/") ||
			strings.Contains(attachment.StoragePath, `\`) || strings.Contains(attachment.StoragePath, "..") ||
			attachment.MIMEType == "" || len(attachment.MIMEType) > 191 || attachment.MIMEType != strings.ToLower(attachment.MIMEType) ||
			attachment.Size < 1 || attachment.Size > 1<<40 || !validLowerSHA256(attachment.SHA256) ||
			attachment.CreatedAt < 0 || attachment.UpdatedAt < attachment.CreatedAt ||
			(attachment.DeletedAt != nil && *attachment.DeletedAt < attachment.CreatedAt) {
			return fmt.Errorf("%w: invalid legacy knowledge attachment id %d", ErrInvalidInput, attachment.ID)
		}
		for _, value := range []string{attachment.OriginalName, attachment.StoragePath, attachment.MIMEType} {
			if strings.IndexFunc(value, unicode.IsControl) >= 0 {
				return fmt.Errorf("%w: invalid legacy knowledge attachment id %d", ErrInvalidInput, attachment.ID)
			}
		}
		if attachment.Status != KnowledgeAttachmentQuarantined && attachment.Status != KnowledgeAttachmentReady && attachment.Status != KnowledgeAttachmentRejected {
			return fmt.Errorf("%w: invalid legacy knowledge attachment status", ErrInvalidInput)
		}
		if attachment.DraftTokenHash != nil && !validLowerSHA256(*attachment.DraftTokenHash) {
			return fmt.Errorf("%w: invalid legacy knowledge attachment draft token", ErrInvalidInput)
		}
		if attachment.Extension != nil {
			extension := *attachment.Extension
			if extension == "" || len(extension) > 32 || extension != strings.ToLower(extension) || strings.IndexFunc(extension, func(character rune) bool {
				return !(character >= 'a' && character <= 'z') && !(character >= '0' && character <= '9')
			}) >= 0 {
				return fmt.Errorf("%w: invalid legacy knowledge attachment extension", ErrInvalidInput)
			}
		}
		if attachment.KnowledgeID != nil {
			if _, exists := articleByID[*attachment.KnowledgeID]; !exists {
				return fmt.Errorf("%w: legacy knowledge attachment references a missing article", ErrInvalidInput)
			}
		}
		if _, exists := seenAttachmentIDs[attachment.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy knowledge attachment id %d", ErrInvalidInput, attachment.ID)
		}
		if _, exists := attachmentByUUID[attachment.UUID]; exists {
			return fmt.Errorf("%w: duplicate legacy knowledge attachment uuid", ErrInvalidInput)
		}
		if _, exists := seenPaths[attachment.StoragePath]; exists {
			return fmt.Errorf("%w: duplicate legacy knowledge attachment path", ErrInvalidInput)
		}
		seenAttachmentIDs[attachment.ID] = struct{}{}
		attachmentByUUID[attachment.UUID] = attachment
		seenPaths[attachment.StoragePath] = struct{}{}
	}
	for _, article := range articles {
		references, err := legacyKnowledgeReferences(article.Body, 100)
		if err != nil {
			return fmt.Errorf("%w: legacy knowledge id %d has an invalid attachment reference", ErrInvalidInput, article.ID)
		}
		for _, reference := range references {
			attachment, exists := attachmentByUUID[reference]
			if !exists || attachment.KnowledgeID == nil || *attachment.KnowledgeID != article.ID ||
				attachment.Status != KnowledgeAttachmentReady || attachment.DeletedAt != nil {
				return fmt.Errorf("%w: legacy knowledge id %d references an unavailable attachment", ErrInvalidInput, article.ID)
			}
		}
	}
	if len(uploads) > maxLegacyKnowledgeFiles {
		return ErrInvalidInput
	}
	seenUploadIDs := make(map[int64]struct{}, len(uploads))
	seenUploadUUIDs := make(map[string]struct{}, len(uploads))
	seenTemporaryPaths := make(map[string]struct{}, len(uploads))
	for _, upload := range uploads {
		parsed, uuidErr := uuid.Parse(upload.UUID)
		expectedChunks := 0
		if upload.ChunkSize > 0 {
			expectedChunks = int((upload.DeclaredSize + upload.ChunkSize - 1) / upload.ChunkSize)
		}
		if upload.ID < 1 || uuidErr != nil || parsed.String() != upload.UUID || parsed.Variant() != uuid.RFC4122 || parsed.Version() < 1 || parsed.Version() > 5 ||
			upload.UploaderUserID < 1 || !validLowerSHA256(upload.DraftTokenHash) || upload.OriginalName == "" ||
			len([]byte(upload.OriginalName)) > 1024 || !utf8.ValidString(upload.OriginalName) || filepath.Base(upload.OriginalName) != upload.OriginalName ||
			upload.DeclaredSize < 1 || upload.DeclaredSize > 1<<40 || upload.ChunkSize < 1 || upload.ChunkSize > 1<<30 ||
			upload.TotalChunks < 1 || upload.TotalChunks > 1_000_000 || upload.TotalChunks != expectedChunks ||
			upload.ReceivedChunks < 0 || upload.ReceivedChunks > upload.TotalChunks || upload.TemporaryPath == "" ||
			len(upload.TemporaryPath) > 512 || strings.HasPrefix(upload.TemporaryPath, "/") || strings.Contains(upload.TemporaryPath, `\`) ||
			strings.Contains(upload.TemporaryPath, "..") || upload.ExpiresAt < 0 || upload.CreatedAt < 0 || upload.UpdatedAt < upload.CreatedAt {
			return fmt.Errorf("%w: invalid legacy knowledge upload id %d", ErrInvalidInput, upload.ID)
		}
		for _, value := range []string{upload.OriginalName, upload.TemporaryPath} {
			if strings.IndexFunc(value, unicode.IsControl) >= 0 {
				return fmt.Errorf("%w: invalid legacy knowledge upload id %d", ErrInvalidInput, upload.ID)
			}
		}
		if upload.ExpectedSHA256 != nil && !validLowerSHA256(*upload.ExpectedSHA256) {
			return fmt.Errorf("%w: invalid legacy knowledge upload expected hash", ErrInvalidInput)
		}
		switch upload.Status {
		case KnowledgeUploadInitialized, KnowledgeUploadUploading, KnowledgeUploadCompleting,
			KnowledgeUploadCompleted, KnowledgeUploadFailed, KnowledgeUploadExpired:
		default:
			return fmt.Errorf("%w: invalid legacy knowledge upload status", ErrInvalidInput)
		}
		if _, exists := seenUploadIDs[upload.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy knowledge upload id %d", ErrInvalidInput, upload.ID)
		}
		if _, exists := seenUploadUUIDs[upload.UUID]; exists {
			return fmt.Errorf("%w: duplicate legacy knowledge upload uuid", ErrInvalidInput)
		}
		if _, exists := seenTemporaryPaths[upload.TemporaryPath]; exists {
			return fmt.Errorf("%w: duplicate legacy knowledge upload path", ErrInvalidInput)
		}
		seenUploadIDs[upload.ID] = struct{}{}
		seenUploadUUIDs[upload.UUID] = struct{}{}
		seenTemporaryPaths[upload.TemporaryPath] = struct{}{}
		if upload.Status == KnowledgeUploadCompleted {
			if _, exists := attachmentByUUID[upload.UUID]; !exists || upload.ReceivedChunks != upload.TotalChunks || len(upload.Chunks) != 0 {
				return fmt.Errorf("%w: completed legacy upload has no matching attachment", ErrInvalidInput)
			}
		} else if requireChunkMetadata && upload.ReceivedChunks != len(upload.Chunks) {
			return fmt.Errorf("%w: legacy upload chunk count mismatch", ErrInvalidInput)
		}
		seenChunkIndexes := make(map[int]struct{}, len(upload.Chunks))
		for _, chunk := range upload.Chunks {
			expectedSize := upload.ChunkSize
			if chunk.Index == upload.TotalChunks-1 {
				expectedSize = upload.DeclaredSize - upload.ChunkSize*int64(upload.TotalChunks-1)
			}
			if chunk.Index < 0 || chunk.Index >= upload.TotalChunks || chunk.Size != expectedSize || !validLowerSHA256(chunk.SHA256) || chunk.CreatedAt < 0 {
				return fmt.Errorf("%w: invalid legacy knowledge upload chunk", ErrInvalidInput)
			}
			if _, exists := seenChunkIndexes[chunk.Index]; exists {
				return fmt.Errorf("%w: duplicate legacy knowledge upload chunk", ErrInvalidInput)
			}
			seenChunkIndexes[chunk.Index] = struct{}{}
		}
		if requireChunkMetadata && upload.Status == KnowledgeUploadCompleting && len(upload.Chunks) != upload.TotalChunks {
			return fmt.Errorf("%w: completing legacy upload is missing chunks", ErrInvalidInput)
		}
	}
	return nil
}

func legacyKnowledgeReferences(body string, maximum int) ([]string, error) {
	result := make([]string, 0)
	seen := make(map[string]struct{})
	for cursor := 0; ; {
		relative := strings.Index(body[cursor:], legacyAttachmentURIScheme)
		if relative < 0 {
			if containsLegacyAttachmentURI(body[cursor:]) {
				return nil, ErrInvalidInput
			}
			return result, nil
		}
		start := cursor + relative + len(legacyAttachmentURIScheme)
		end := start + 36
		if end > len(body) {
			return nil, ErrInvalidInput
		}
		identifier := body[start:end]
		parsed, err := uuid.Parse(identifier)
		if err != nil || parsed.String() != identifier || parsed.Variant() != uuid.RFC4122 || parsed.Version() < 1 || parsed.Version() > 5 {
			return nil, ErrInvalidInput
		}
		if end < len(body) {
			next, _ := utf8.DecodeRuneInString(body[end:])
			if !unicode.IsSpace(next) && !strings.ContainsRune(`)]}>"'`, next) {
				return nil, ErrInvalidInput
			}
		}
		if _, exists := seen[identifier]; !exists {
			seen[identifier] = struct{}{}
			result = append(result, identifier)
			if len(result) > maximum {
				return nil, ErrInvalidInput
			}
		}
		cursor = end
	}
}

func containsLegacyAttachmentURI(value string) bool {
	for index := 0; index+len(legacyAttachmentURIScheme) <= len(value); index++ {
		if value[index] != 'k' && value[index] != 'K' {
			continue
		}
		if strings.EqualFold(value[index:index+len(legacyAttachmentURIScheme)], legacyAttachmentURIScheme) {
			return true
		}
	}
	return false
}

func readLegacyTargetKnowledge(ctx context.Context, database queryer) ([]LegacyKnowledgeArticle, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, language, category, title, body, sort_position, visible, revision, created_at, updated_at
		FROM knowledge ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("read imported knowledge: %w", err)
	}
	defer rows.Close()
	result := make([]LegacyKnowledgeArticle, 0)
	for rows.Next() {
		var article LegacyKnowledgeArticle
		var revision int64
		if err := rows.Scan(&article.ID, &article.Language, &article.Category, &article.Title, &article.Body, &article.SortPosition,
			&article.Visible, &revision, &article.CreatedAt, &article.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan imported knowledge: %w", err)
		}
		if revision != 1 {
			return nil, fmt.Errorf("imported legacy knowledge id %d has unexpected revision %d", article.ID, revision)
		}
		result = append(result, article)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate imported knowledge: %w", err)
	}
	return result, nil
}

func readLegacyTargetKnowledgeAttachments(ctx context.Context, database queryer) ([]LegacyKnowledgeAttachment, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, uuid, knowledge_id, uploader_user_id, draft_token_hash, original_name, storage_path,
		       mime_type, extension, size, sha256, status, created_at, updated_at, deleted_at
		FROM knowledge_attachments ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("read imported knowledge attachments: %w", err)
	}
	defer rows.Close()
	result := make([]LegacyKnowledgeAttachment, 0)
	for rows.Next() {
		var attachment LegacyKnowledgeAttachment
		var knowledgeID, deletedAt sql.NullInt64
		var draftHash, extension sql.NullString
		if err := rows.Scan(&attachment.ID, &attachment.UUID, &knowledgeID, &attachment.UploaderUserID, &draftHash,
			&attachment.OriginalName, &attachment.StoragePath, &attachment.MIMEType, &extension, &attachment.Size,
			&attachment.SHA256, &attachment.Status, &attachment.CreatedAt, &attachment.UpdatedAt, &deletedAt); err != nil {
			return nil, fmt.Errorf("scan imported knowledge attachment: %w", err)
		}
		if knowledgeID.Valid {
			attachment.KnowledgeID = &knowledgeID.Int64
		}
		if draftHash.Valid {
			attachment.DraftTokenHash = &draftHash.String
		}
		if extension.Valid {
			attachment.Extension = &extension.String
		}
		if deletedAt.Valid {
			attachment.DeletedAt = &deletedAt.Int64
		}
		result = append(result, attachment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate imported knowledge attachments: %w", err)
	}
	return result, nil
}

func readLegacyTargetKnowledgeUploads(ctx context.Context, database queryer) ([]LegacyKnowledgeUpload, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, uuid, uploader_user_id, draft_token_hash, original_name, declared_size, expected_sha256,
		       chunk_size, total_chunks, received_chunks, temporary_path, status, expires_at, created_at, updated_at
		FROM knowledge_attachment_uploads ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("read imported knowledge uploads: %w", err)
	}
	result := make([]LegacyKnowledgeUpload, 0)
	indexByID := make(map[int64]int)
	for rows.Next() {
		var upload LegacyKnowledgeUpload
		var expectedSHA256 sql.NullString
		if err := rows.Scan(&upload.ID, &upload.UUID, &upload.UploaderUserID, &upload.DraftTokenHash, &upload.OriginalName,
			&upload.DeclaredSize, &expectedSHA256, &upload.ChunkSize, &upload.TotalChunks, &upload.ReceivedChunks,
			&upload.TemporaryPath, &upload.Status, &upload.ExpiresAt, &upload.CreatedAt, &upload.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan imported knowledge upload: %w", err)
		}
		if expectedSHA256.Valid {
			upload.ExpectedSHA256 = &expectedSHA256.String
		}
		upload.Chunks = make([]LegacyKnowledgeUploadChunk, 0)
		indexByID[upload.ID] = len(result)
		result = append(result, upload)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate imported knowledge uploads: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close imported knowledge uploads: %w", err)
	}
	chunkRows, err := database.QueryContext(ctx, `
		SELECT upload_id, chunk_index, size, sha256, created_at
		FROM knowledge_attachment_chunks ORDER BY upload_id, chunk_index
	`)
	if err != nil {
		return nil, fmt.Errorf("read imported knowledge upload chunks: %w", err)
	}
	defer chunkRows.Close()
	for chunkRows.Next() {
		var uploadID int64
		var chunk LegacyKnowledgeUploadChunk
		if err := chunkRows.Scan(&uploadID, &chunk.Index, &chunk.Size, &chunk.SHA256, &chunk.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan imported knowledge upload chunk: %w", err)
		}
		index, exists := indexByID[uploadID]
		if !exists {
			return nil, fmt.Errorf("imported knowledge upload chunk references missing upload %d", uploadID)
		}
		result[index].Chunks = append(result[index].Chunks, chunk)
	}
	if err := chunkRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate imported knowledge upload chunks: %w", err)
	}
	return result, nil
}
