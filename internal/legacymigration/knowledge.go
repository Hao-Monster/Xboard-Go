package legacymigration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const maxLegacyKnowledgeRows = 10_000

type KnowledgeSnapshot struct {
	Path                string
	Size                int64
	SHA256              string
	Articles            []store.LegacyKnowledgeArticle
	Checksum            string
	Attachments         []store.LegacyKnowledgeAttachment
	AttachmentsChecksum string
	Uploads             []store.LegacyKnowledgeUpload
	UploadsChecksum     string
}

func ReadKnowledgeSnapshot(ctx context.Context, sourcePath string) (KnowledgeSnapshot, error) {
	articles := []store.LegacyKnowledgeArticle{}
	attachments := []store.LegacyKnowledgeAttachment{}
	uploads := []store.LegacyKnowledgeUpload{}
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_knowledge", []string{"id", "language", "category", "title", "body", "sort", "show", "created_at", "updated_at"}); err != nil {
			return err
		}
		if err := requireRealTable(ctx, database, "v2_knowledge_attachment", []string{
			"id", "uuid", "knowledge_id", "uploader_user_id", "draft_token", "original_name", "storage_path",
			"mime_type", "extension", "size", "sha256", "status", "created_at", "updated_at", "deleted_at",
		}); err != nil {
			return err
		}
		if err := requireRealTable(ctx, database, "v2_knowledge_attachment_upload", []string{
			"id", "uuid", "uploader_user_id", "draft_token", "original_name", "declared_size", "expected_sha256",
			"chunk_size", "total_chunks", "received_chunks", "temporary_path", "status", "expires_at", "created_at", "updated_at",
		}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(
				length(CAST(language AS BLOB)) + length(CAST(category AS BLOB)) +
				length(CAST(title AS BLOB)) + length(CAST(body AS BLOB))
			), 0) FROM v2_knowledge
		`, maxLegacyKnowledgeRows, maxLegacyRelevantDataBytes, "legacy knowledge"); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `SELECT COUNT(*), COALESCE(SUM(
			length(CAST(uuid AS BLOB)) + length(CAST(original_name AS BLOB)) + length(CAST(storage_path AS BLOB)) +
			length(CAST(mime_type AS BLOB)) + length(CAST(sha256 AS BLOB)) + COALESCE(length(CAST(extension AS BLOB)), 0) +
			COALESCE(length(CAST(draft_token AS BLOB)), 0)
		), 0) FROM v2_knowledge_attachment`, maxLegacyKnowledgeRows*100, maxLegacyRelevantDataBytes, "legacy knowledge attachments"); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `SELECT COUNT(*), COALESCE(SUM(
			length(CAST(uuid AS BLOB)) + length(CAST(draft_token AS BLOB)) + length(CAST(original_name AS BLOB)) +
			length(CAST(temporary_path AS BLOB)) + COALESCE(length(CAST(expected_sha256 AS BLOB)), 0)
		), 0) FROM v2_knowledge_attachment_upload`, maxLegacyKnowledgeRows*100, maxLegacyRelevantDataBytes, "legacy knowledge attachment uploads"); err != nil {
			return err
		}
		var readBytes int64
		var readErr error
		articles, readBytes, readErr = readLegacyKnowledge(ctx, database)
		if readErr != nil {
			return readErr
		}
		if readBytes > maxLegacyRelevantDataBytes {
			return errors.New("legacy knowledge exceeds the migration data limit")
		}
		attachments, readBytes, readErr = readLegacyKnowledgeAttachments(ctx, database)
		if readErr != nil {
			return readErr
		}
		uploads, readBytes, readErr = readLegacyKnowledgeUploads(ctx, database)
		if readErr != nil {
			return readErr
		}
		if readBytes > maxLegacyRelevantDataBytes {
			return errors.New("legacy knowledge attachment uploads exceed the migration data limit")
		}
		if err := store.ValidateLegacyKnowledgeSnapshotData(articles, attachments, uploads); err != nil {
			return fmt.Errorf("validate legacy knowledge: %w", err)
		}
		return nil
	})
	if err != nil {
		return KnowledgeSnapshot{}, err
	}
	return KnowledgeSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Articles: articles, Checksum: store.LegacyKnowledgeChecksum(articles), Attachments: attachments,
		AttachmentsChecksum: store.LegacyKnowledgeAttachmentsChecksum(attachments),
		Uploads:             uploads, UploadsChecksum: store.LegacyKnowledgeUploadsChecksum(uploads),
	}, nil
}

func readLegacyKnowledgeUploads(ctx context.Context, database *sql.DB) ([]store.LegacyKnowledgeUpload, int64, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, uuid, uploader_user_id, draft_token, original_name, declared_size, expected_sha256,
		       chunk_size, total_chunks, received_chunks, temporary_path, status, expires_at, created_at, updated_at
		FROM v2_knowledge_attachment_upload ORDER BY id
	`)
	if err != nil {
		return nil, 0, fmt.Errorf("read legacy knowledge attachment uploads: %w", err)
	}
	defer rows.Close()
	result := make([]store.LegacyKnowledgeUpload, 0)
	var bytesRead int64
	for rows.Next() {
		if len(result) >= maxLegacyKnowledgeRows*100 {
			return nil, 0, errors.New("legacy knowledge attachment uploads exceed the migration row limit")
		}
		var upload store.LegacyKnowledgeUpload
		var draftToken string
		var expectedSHA256 sql.NullString
		if err := rows.Scan(&upload.ID, &upload.UUID, &upload.UploaderUserID, &draftToken, &upload.OriginalName,
			&upload.DeclaredSize, &expectedSHA256, &upload.ChunkSize, &upload.TotalChunks, &upload.ReceivedChunks,
			&upload.TemporaryPath, &upload.Status, &upload.ExpiresAt, &upload.CreatedAt, &upload.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan legacy knowledge attachment upload: %w", err)
		}
		if len(draftToken) != 64 || strings.ToLower(draftToken) != draftToken {
			return nil, 0, fmt.Errorf("legacy knowledge attachment upload id %d has an invalid draft token", upload.ID)
		}
		if _, err := hex.DecodeString(draftToken); err != nil {
			return nil, 0, fmt.Errorf("legacy knowledge attachment upload id %d has an invalid draft token", upload.ID)
		}
		digest := sha256.Sum256([]byte(draftToken))
		upload.DraftTokenHash = hex.EncodeToString(digest[:])
		if expectedSHA256.Valid {
			upload.ExpectedSHA256 = &expectedSHA256.String
		}
		upload.Chunks = []store.LegacyKnowledgeUploadChunk{}
		bytesRead += int64(len(upload.UUID) + len(draftToken) + len(upload.OriginalName) + len(upload.TemporaryPath))
		if expectedSHA256.Valid {
			bytesRead += int64(len(expectedSHA256.String))
		}
		if bytesRead > maxLegacyRelevantDataBytes {
			return nil, 0, errors.New("legacy knowledge attachment uploads exceed the migration data limit")
		}
		result = append(result, upload)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate legacy knowledge attachment uploads: %w", err)
	}
	return result, bytesRead, nil
}

func readLegacyKnowledgeAttachments(ctx context.Context, database *sql.DB) ([]store.LegacyKnowledgeAttachment, int64, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, uuid, knowledge_id, uploader_user_id, draft_token, original_name, storage_path,
		       mime_type, extension, size, sha256, status, created_at, updated_at, deleted_at
		FROM v2_knowledge_attachment ORDER BY id
	`)
	if err != nil {
		return nil, 0, fmt.Errorf("read legacy knowledge attachments: %w", err)
	}
	defer rows.Close()
	result := make([]store.LegacyKnowledgeAttachment, 0)
	var bytesRead int64
	for rows.Next() {
		if len(result) >= maxLegacyKnowledgeRows*100 {
			return nil, 0, errors.New("legacy knowledge attachments exceed the migration row limit")
		}
		var attachment store.LegacyKnowledgeAttachment
		var knowledgeID, deletedAt sql.NullInt64
		var draftToken, extension sql.NullString
		if err := rows.Scan(&attachment.ID, &attachment.UUID, &knowledgeID, &attachment.UploaderUserID, &draftToken,
			&attachment.OriginalName, &attachment.StoragePath, &attachment.MIMEType, &extension, &attachment.Size,
			&attachment.SHA256, &attachment.Status, &attachment.CreatedAt, &attachment.UpdatedAt, &deletedAt); err != nil {
			return nil, 0, fmt.Errorf("scan legacy knowledge attachment: %w", err)
		}
		if knowledgeID.Valid {
			attachment.KnowledgeID = &knowledgeID.Int64
		}
		if draftToken.Valid {
			if len(draftToken.String) != 64 || strings.ToLower(draftToken.String) != draftToken.String {
				return nil, 0, fmt.Errorf("legacy knowledge attachment id %d has an invalid draft token", attachment.ID)
			}
			if _, err := hex.DecodeString(draftToken.String); err != nil {
				return nil, 0, fmt.Errorf("legacy knowledge attachment id %d has an invalid draft token", attachment.ID)
			}
			digest := sha256.Sum256([]byte(draftToken.String))
			value := hex.EncodeToString(digest[:])
			attachment.DraftTokenHash = &value
		}
		if extension.Valid {
			attachment.Extension = &extension.String
		}
		if deletedAt.Valid {
			attachment.DeletedAt = &deletedAt.Int64
		}
		bytesRead += int64(len(attachment.UUID) + len(attachment.OriginalName) + len(attachment.StoragePath) + len(attachment.MIMEType) + len(attachment.SHA256))
		if draftToken.Valid {
			bytesRead += int64(len(draftToken.String))
		}
		if extension.Valid {
			bytesRead += int64(len(extension.String))
		}
		if bytesRead > maxLegacyRelevantDataBytes {
			return nil, 0, errors.New("legacy knowledge attachments exceed the migration data limit")
		}
		result = append(result, attachment)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate legacy knowledge attachments: %w", err)
	}
	return result, bytesRead, nil
}

func readLegacyKnowledge(ctx context.Context, database *sql.DB) ([]store.LegacyKnowledgeArticle, int64, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, language, category, title, body, sort, show, created_at, updated_at
		FROM v2_knowledge ORDER BY id
	`)
	if err != nil {
		return nil, 0, fmt.Errorf("read legacy knowledge: %w", err)
	}
	defer rows.Close()
	result := make([]store.LegacyKnowledgeArticle, 0)
	var bytesRead int64
	maxInt := int64(^uint(0) >> 1)
	for rows.Next() {
		if len(result) >= maxLegacyKnowledgeRows {
			return nil, 0, fmt.Errorf("legacy knowledge exceeds the %d-row migration limit", maxLegacyKnowledgeRows)
		}
		var article store.LegacyKnowledgeArticle
		var legacySort sql.NullInt64
		var legacyVisible int64
		if err := rows.Scan(&article.ID, &article.Language, &article.Category, &article.Title, &article.Body, &legacySort,
			&legacyVisible, &article.CreatedAt, &article.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan legacy knowledge: %w", err)
		}
		if legacySort.Valid {
			if legacySort.Int64 < 0 || legacySort.Int64 > maxInt {
				return nil, 0, fmt.Errorf("legacy knowledge id %d has an invalid sort value", article.ID)
			}
			article.SortPosition = int(legacySort.Int64)
		}
		switch legacyVisible {
		case 0:
			article.Visible = false
		case 1:
			article.Visible = true
		default:
			return nil, 0, fmt.Errorf("legacy knowledge id %d has an invalid visibility value", article.ID)
		}
		bytesRead += int64(len(article.Language) + len(article.Category) + len(article.Title) + len(article.Body))
		if bytesRead > maxLegacyRelevantDataBytes {
			return nil, 0, errors.New("legacy knowledge exceeds the migration data limit")
		}
		result = append(result, article)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate legacy knowledge: %w", err)
	}
	return result, bytesRead, nil
}
