package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	LegacyKnowledgeSlice      = "knowledge-v1"
	maxLegacyKnowledgeRows    = 10_000
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

type LegacyKnowledgeImport struct {
	Slice                string
	SourceSHA256         string
	SourceSize           int64
	Articles             []LegacyKnowledgeArticle
	Checksum             string
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

	targetArticles, err := readLegacyTargetKnowledge(ctx, tx)
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
		AppliedAt: now.UTC(), AlreadyApplied: false,
	}
	if report.Articles.SourceRows != report.Articles.TargetRows || report.Articles.SourceChecksum != report.Articles.TargetChecksum {
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
	return ValidateLegacyKnowledgeData(input.Articles)
}

func ValidateLegacyKnowledgeData(articles []LegacyKnowledgeArticle) error {
	if len(articles) > maxLegacyKnowledgeRows {
		return ErrInvalidInput
	}
	seen := make(map[int64]struct{}, len(articles))
	var totalBytes int64
	for _, article := range articles {
		if article.ID < 1 || article.SortPosition < 0 || article.CreatedAt < 0 || article.UpdatedAt < article.CreatedAt {
			return fmt.Errorf("%w: invalid legacy knowledge id, sort, or timestamp", ErrInvalidInput)
		}
		if _, exists := seen[article.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy knowledge id %d", ErrInvalidInput, article.ID)
		}
		seen[article.ID] = struct{}{}
		normalized, err := normalizeKnowledgeInput(SaveKnowledgeInput{
			Language: article.Language, Category: article.Category, Title: article.Title, Body: article.Body, Visible: article.Visible,
		})
		if err != nil {
			return fmt.Errorf("%w: legacy knowledge id %d: %v", ErrInvalidInput, article.ID, err)
		}
		if normalized.Language != article.Language || normalized.Category != article.Category || normalized.Title != article.Title || normalized.Body != article.Body {
			return fmt.Errorf("%w: legacy knowledge id %d requires normalization", ErrInvalidInput, article.ID)
		}
		if containsLegacyAttachmentURI(article.Body) {
			return fmt.Errorf("%w: legacy knowledge id %d references an unsupported attachment", ErrInvalidInput, article.ID)
		}
		totalBytes += int64(len(article.Language) + len(article.Category) + len(article.Title) + len(article.Body))
		if totalBytes > maxLegacyKnowledgeBytes {
			return fmt.Errorf("%w: legacy knowledge exceeds the migration data limit", ErrInvalidInput)
		}
	}
	return nil
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
