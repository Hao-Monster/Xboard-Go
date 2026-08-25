package legacymigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const maxLegacyKnowledgeRows = 10_000

type KnowledgeSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Articles []store.LegacyKnowledgeArticle
	Checksum string
}

func ReadKnowledgeSnapshot(ctx context.Context, sourcePath string) (KnowledgeSnapshot, error) {
	articles := []store.LegacyKnowledgeArticle{}
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_knowledge", []string{"id", "language", "category", "title", "body", "sort", "show", "created_at", "updated_at"}); err != nil {
			return err
		}
		if err := requireRealTable(ctx, database, "v2_knowledge_attachment", []string{"id", "knowledge_id", "uuid", "status", "deleted_at"}); err != nil {
			return err
		}
		if err := requireRealTable(ctx, database, "v2_knowledge_attachment_upload", []string{"id", "uuid", "status"}); err != nil {
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
		if err := validateLegacyQueryBudget(ctx, database, `SELECT COUNT(*), 0 FROM v2_knowledge_attachment`, 0, 0, "legacy knowledge attachments"); err != nil {
			return fmt.Errorf("knowledge attachment data is not supported by this migration slice: %w", err)
		}
		if err := validateLegacyQueryBudget(ctx, database, `SELECT COUNT(*), 0 FROM v2_knowledge_attachment_upload`, 0, 0, "legacy knowledge attachment uploads"); err != nil {
			return fmt.Errorf("knowledge attachment data is not supported by this migration slice: %w", err)
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
		if err := store.ValidateLegacyKnowledgeData(articles); err != nil {
			return fmt.Errorf("validate legacy knowledge: %w", err)
		}
		return nil
	})
	if err != nil {
		return KnowledgeSnapshot{}, err
	}
	return KnowledgeSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Articles: articles, Checksum: store.LegacyKnowledgeChecksum(articles),
	}, nil
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
