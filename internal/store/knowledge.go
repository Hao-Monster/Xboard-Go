package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxKnowledgeTitleRunes    = 255
	maxKnowledgeCategoryRunes = 255
	maxKnowledgeBodyBytes     = 1 << 20
	maxKnowledgeOrderItems    = 10_000
)

var supportedKnowledgeLanguages = map[string]struct{}{
	"en-US": {}, "ja-JP": {}, "ko-KR": {}, "vi-VN": {}, "zh-CN": {}, "zh-TW": {}, "ru-RU": {},
}

func (s *Store) CreateKnowledge(ctx context.Context, input SaveKnowledgeInput, now time.Time) (Knowledge, error) {
	normalized, err := normalizeKnowledgeInput(input)
	if err != nil {
		return Knowledge{}, err
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO knowledge (language, category, title, body, sort_position, visible, revision, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, ?, 1, ?, ?)
	`, normalized.Language, normalized.Category, normalized.Title, normalized.Body, normalized.Visible, now.Unix(), now.Unix())
	if err != nil {
		return Knowledge{}, fmt.Errorf("create knowledge: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Knowledge{}, fmt.Errorf("read created knowledge id: %w", err)
	}
	return s.GetKnowledge(ctx, id)
}

func (s *Store) UpdateKnowledge(ctx context.Context, knowledgeID, revision int64, input SaveKnowledgeInput, now time.Time) (Knowledge, error) {
	if knowledgeID < 1 || revision < 1 {
		return Knowledge{}, ErrInvalidInput
	}
	normalized, err := normalizeKnowledgeInput(input)
	if err != nil {
		return Knowledge{}, err
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE knowledge
		SET language = ?, category = ?, title = ?, body = ?, visible = ?, revision = revision + 1, updated_at = ?
		WHERE id = ? AND revision = ?
	`, normalized.Language, normalized.Category, normalized.Title, normalized.Body, normalized.Visible, now.Unix(), knowledgeID, revision)
	if err != nil {
		return Knowledge{}, fmt.Errorf("update knowledge: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return Knowledge{}, s.knowledgeMutationError(ctx, knowledgeID)
	}
	return s.GetKnowledge(ctx, knowledgeID)
}

func (s *Store) SetKnowledgeVisibility(ctx context.Context, knowledgeID, revision int64, visible bool, now time.Time) (Knowledge, error) {
	if knowledgeID < 1 || revision < 1 {
		return Knowledge{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE knowledge SET visible = ?, revision = revision + 1, updated_at = ? WHERE id = ? AND revision = ?
	`, visible, now.Unix(), knowledgeID, revision)
	if err != nil {
		return Knowledge{}, fmt.Errorf("set knowledge visibility: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return Knowledge{}, s.knowledgeMutationError(ctx, knowledgeID)
	}
	return s.GetKnowledge(ctx, knowledgeID)
}

func (s *Store) DeleteKnowledge(ctx context.Context, knowledgeID, revision int64) error {
	if knowledgeID < 1 || revision < 1 {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `DELETE FROM knowledge WHERE id = ? AND revision = ?`, knowledgeID, revision)
	if err != nil {
		return fmt.Errorf("delete knowledge: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return s.knowledgeMutationError(ctx, knowledgeID)
	}
	return nil
}

func (s *Store) ReorderKnowledge(ctx context.Context, ids []int64, now time.Time) error {
	if len(ids) == 0 || len(ids) > maxKnowledgeOrderItems {
		return ErrInvalidInput
	}
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id < 1 {
			return ErrInvalidInput
		}
		if _, exists := seen[id]; exists {
			return ErrInvalidInput
		}
		seen[id] = struct{}{}
	}

	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reorder knowledge: %w", err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge`).Scan(&count); err != nil {
		return fmt.Errorf("count knowledge: %w", err)
	}
	if count != len(ids) {
		return ErrConflict
	}
	for position, id := range ids {
		result, err := tx.ExecContext(ctx, `UPDATE knowledge SET sort_position = ?, updated_at = ? WHERE id = ?`, position+1, now.Unix(), id)
		if err != nil {
			return fmt.Errorf("reorder knowledge: %w", err)
		}
		rows, _ := result.RowsAffected()
		if rows != 1 {
			return ErrConflict
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reorder knowledge: %w", err)
	}
	return nil
}

func (s *Store) GetKnowledge(ctx context.Context, knowledgeID int64) (Knowledge, error) {
	if knowledgeID < 1 {
		return Knowledge{}, ErrInvalidInput
	}
	return scanKnowledge(s.db.QueryRowContext(ctx, knowledgeSelect+` WHERE id = ?`, knowledgeID))
}

func (s *Store) GetVisibleKnowledge(ctx context.Context, knowledgeID int64) (Knowledge, error) {
	if knowledgeID < 1 {
		return Knowledge{}, ErrInvalidInput
	}
	return scanKnowledge(s.db.QueryRowContext(ctx, knowledgeSelect+` WHERE id = ? AND visible = 1`, knowledgeID))
}

func (s *Store) ListKnowledge(ctx context.Context) ([]Knowledge, error) {
	rows, err := s.db.QueryContext(ctx, knowledgeSummarySelect+` ORDER BY sort_position, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list knowledge: %w", err)
	}
	defer rows.Close()
	return collectKnowledge(rows)
}

func (s *Store) ListVisibleKnowledge(ctx context.Context, language, keyword string) ([]Knowledge, error) {
	language = strings.TrimSpace(language)
	keyword = strings.TrimSpace(keyword)
	if _, ok := supportedKnowledgeLanguages[language]; !ok || !utf8.ValidString(keyword) || utf8.RuneCountInString(keyword) > 255 {
		return nil, ErrInvalidInput
	}
	query := knowledgeSelect + ` WHERE visible = 1 AND language = ?`
	arguments := []any{language}
	if keyword != "" {
		query += ` AND (instr(lower(title), lower(?)) > 0 OR instr(lower(body), lower(?)) > 0)`
		arguments = append(arguments, keyword, keyword)
	}
	query += ` ORDER BY sort_position, id DESC`
	rows, err := s.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list visible knowledge: %w", err)
	}
	defer rows.Close()
	return collectKnowledge(rows)
}

func (s *Store) ListVisibleKnowledgeNavigation(ctx context.Context) ([]Knowledge, error) {
	rows, err := s.db.QueryContext(ctx, knowledgeSummarySelect+` WHERE visible = 1 ORDER BY sort_position, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list public knowledge navigation: %w", err)
	}
	defer rows.Close()
	return collectKnowledge(rows)
}

func (s *Store) ListKnowledgeCategories(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT category FROM knowledge ORDER BY category COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("list knowledge categories: %w", err)
	}
	defer rows.Close()
	categories := make([]string, 0)
	for rows.Next() {
		var category string
		if err := rows.Scan(&category); err != nil {
			return nil, fmt.Errorf("scan knowledge category: %w", err)
		}
		categories = append(categories, category)
	}
	return categories, rows.Err()
}

func (s *Store) GetKnowledgeViewer(ctx context.Context, userID int64, now time.Time) (KnowledgeViewer, error) {
	if userID < 1 {
		return KnowledgeViewer{}, ErrInvalidInput
	}
	var viewer KnowledgeViewer
	var banned bool
	var transferEnable int64
	var expiredAt sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT subscription_token, banned, transfer_enable, expired_at
		FROM users WHERE id = ? AND account_kind = 'human'
	`, userID).Scan(&viewer.SubscriptionToken, &banned, &transferEnable, &expiredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeViewer{}, ErrNotFound
	}
	if err != nil {
		return KnowledgeViewer{}, fmt.Errorf("get knowledge viewer: %w", err)
	}
	viewer.SubscriptionValid = !banned && transferEnable > 0 && (!expiredAt.Valid || expiredAt.Int64 > now.Unix())
	return viewer, nil
}

const knowledgeSelect = `
	SELECT id, language, category, title, body, sort_position, visible, revision, created_at, updated_at
	FROM knowledge`

const knowledgeSummarySelect = `
	SELECT id, language, category, title, '' AS body, sort_position, visible, revision, created_at, updated_at
	FROM knowledge`

func collectKnowledge(rows *sql.Rows) ([]Knowledge, error) {
	items := make([]Knowledge, 0)
	for rows.Next() {
		item, err := scanKnowledge(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanKnowledge(row rowScanner) (Knowledge, error) {
	var item Knowledge
	var createdAt, updatedAt int64
	err := row.Scan(&item.ID, &item.Language, &item.Category, &item.Title, &item.Body, &item.SortPosition,
		&item.Visible, &item.Revision, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Knowledge{}, ErrNotFound
	}
	if err != nil {
		return Knowledge{}, fmt.Errorf("scan knowledge: %w", err)
	}
	item.CreatedAt = time.Unix(createdAt, 0).UTC()
	item.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return item, nil
}

func normalizeKnowledgeInput(input SaveKnowledgeInput) (SaveKnowledgeInput, error) {
	input.Language = strings.TrimSpace(input.Language)
	input.Category = strings.TrimSpace(input.Category)
	input.Title = strings.TrimSpace(input.Title)
	if _, ok := supportedKnowledgeLanguages[input.Language]; !ok {
		return SaveKnowledgeInput{}, fmt.Errorf("%w: invalid knowledge language", ErrInvalidInput)
	}
	if input.Category == "" || !utf8.ValidString(input.Category) || utf8.RuneCountInString(input.Category) > maxKnowledgeCategoryRunes || strings.IndexFunc(input.Category, unicode.IsControl) >= 0 {
		return SaveKnowledgeInput{}, fmt.Errorf("%w: invalid knowledge category", ErrInvalidInput)
	}
	if input.Title == "" || !utf8.ValidString(input.Title) || utf8.RuneCountInString(input.Title) > maxKnowledgeTitleRunes || strings.IndexFunc(input.Title, unicode.IsControl) >= 0 {
		return SaveKnowledgeInput{}, fmt.Errorf("%w: invalid knowledge title", ErrInvalidInput)
	}
	if strings.TrimSpace(input.Body) == "" || !utf8.ValidString(input.Body) || len(input.Body) > maxKnowledgeBodyBytes {
		return SaveKnowledgeInput{}, fmt.Errorf("%w: invalid knowledge body", ErrInvalidInput)
	}
	return input, nil
}

func (s *Store) knowledgeMutationError(ctx context.Context, knowledgeID int64) error {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge WHERE id = ?)`, knowledgeID).Scan(&exists); err != nil {
		return fmt.Errorf("check knowledge revision: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrConflict
}
