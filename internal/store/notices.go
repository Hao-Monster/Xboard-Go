package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxNoticeTitleRunes   = 255
	maxNoticeContentBytes = 256 << 10
	maxNoticeImageURL     = 2_048
	maxNoticeTags         = 20
	maxNoticeTagRunes     = 64
	maxNoticeOrderItems   = 10_000
)

func (s *Store) CreateNotice(ctx context.Context, input SaveNoticeInput, now time.Time) (Notice, error) {
	normalized, tagsJSON, err := normalizeNoticeInput(input)
	if err != nil {
		return Notice{}, err
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO notices (sort_position, title, content, image_url, tags_json, visible, revision, created_at, updated_at)
		VALUES (0, ?, ?, ?, ?, ?, 1, ?, ?)
	`, normalized.Title, normalized.Content, nullableString(normalized.ImageURL), tagsJSON, normalized.Visible, now.Unix(), now.Unix())
	if err != nil {
		return Notice{}, fmt.Errorf("create notice: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Notice{}, fmt.Errorf("read notice id: %w", err)
	}
	return s.GetNotice(ctx, id)
}

func (s *Store) UpdateNotice(ctx context.Context, noticeID, revision int64, input SaveNoticeInput, now time.Time) (Notice, error) {
	if revision <= 0 {
		return Notice{}, ErrInvalidInput
	}
	normalized, tagsJSON, err := normalizeNoticeInput(input)
	if err != nil {
		return Notice{}, err
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE notices
		SET title = ?, content = ?, image_url = ?, tags_json = ?, visible = ?, revision = revision + 1, updated_at = ?
		WHERE id = ? AND revision = ?
	`, normalized.Title, normalized.Content, nullableString(normalized.ImageURL), tagsJSON, normalized.Visible, now.Unix(), noticeID, revision)
	if err != nil {
		return Notice{}, fmt.Errorf("update notice: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return Notice{}, s.noticeMutationError(ctx, noticeID)
	}
	return s.GetNotice(ctx, noticeID)
}

func (s *Store) SetNoticeVisibility(ctx context.Context, noticeID, revision int64, visible bool, now time.Time) (Notice, error) {
	if revision <= 0 {
		return Notice{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE notices SET visible = ?, revision = revision + 1, updated_at = ? WHERE id = ? AND revision = ?
	`, visible, now.Unix(), noticeID, revision)
	if err != nil {
		return Notice{}, fmt.Errorf("set notice visibility: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return Notice{}, s.noticeMutationError(ctx, noticeID)
	}
	return s.GetNotice(ctx, noticeID)
}

func (s *Store) DeleteNotice(ctx context.Context, noticeID, revision int64) error {
	if revision <= 0 {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `DELETE FROM notices WHERE id = ? AND revision = ?`, noticeID, revision)
	if err != nil {
		return fmt.Errorf("delete notice: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return s.noticeMutationError(ctx, noticeID)
	}
	return nil
}

func (s *Store) ReorderNotices(ctx context.Context, ids []int64, now time.Time) error {
	if len(ids) == 0 || len(ids) > maxNoticeOrderItems {
		return ErrInvalidInput
	}
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
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
		return fmt.Errorf("begin reorder notices: %w", err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM notices`).Scan(&count); err != nil {
		return fmt.Errorf("count notices: %w", err)
	}
	if count != len(ids) {
		return ErrConflict
	}
	for position, id := range ids {
		result, err := tx.ExecContext(ctx, `UPDATE notices SET sort_position = ?, updated_at = ? WHERE id = ?`, position+1, now.Unix(), id)
		if err != nil {
			return fmt.Errorf("reorder notice: %w", err)
		}
		rows, _ := result.RowsAffected()
		if rows != 1 {
			return ErrConflict
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reorder notices: %w", err)
	}
	return nil
}

func (s *Store) GetNotice(ctx context.Context, noticeID int64) (Notice, error) {
	return scanNotice(s.db.QueryRowContext(ctx, noticeSelect+` WHERE id = ?`, noticeID))
}

func (s *Store) ListNotices(ctx context.Context) ([]Notice, error) {
	rows, err := s.db.QueryContext(ctx, noticeSelect+` ORDER BY sort_position, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list notices: %w", err)
	}
	defer rows.Close()
	return collectNotices(rows)
}

func (s *Store) ListVisibleNotices(ctx context.Context, page, pageSize int) ([]Notice, int64, error) {
	if page < 1 || pageSize < 1 || pageSize > 5 {
		return nil, 0, ErrInvalidInput
	}
	const maxInt64 = int64(^uint64(0) >> 1)
	if int64(page-1) > maxInt64/int64(pageSize) {
		return nil, 0, ErrInvalidInput
	}
	offset := int64(page-1) * int64(pageSize)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, fmt.Errorf("begin visible notice page: %w", err)
	}
	defer tx.Rollback()
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM notices WHERE visible = 1`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count visible notices: %w", err)
	}
	rows, err := tx.QueryContext(ctx, noticeSelect+`
		WHERE visible = 1 ORDER BY sort_position, id DESC LIMIT ? OFFSET ?
	`, pageSize, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list visible notices: %w", err)
	}
	notices, err := collectNotices(rows)
	closeErr := rows.Close()
	if err != nil {
		return nil, 0, err
	}
	if closeErr != nil {
		return nil, 0, fmt.Errorf("close visible notices: %w", closeErr)
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, fmt.Errorf("commit visible notice page: %w", err)
	}
	return notices, total, nil
}

const noticeSelect = `
	SELECT id, sort_position, title, content, image_url, tags_json, visible, revision, created_at, updated_at
	FROM notices`

func collectNotices(rows *sql.Rows) ([]Notice, error) {
	notices := make([]Notice, 0)
	for rows.Next() {
		notice, err := scanNotice(rows)
		if err != nil {
			return nil, err
		}
		notices = append(notices, notice)
	}
	return notices, rows.Err()
}

func scanNotice(row rowScanner) (Notice, error) {
	var notice Notice
	var imageURL sql.NullString
	var tagsJSON string
	var createdAt, updatedAt int64
	err := row.Scan(&notice.ID, &notice.SortPosition, &notice.Title, &notice.Content, &imageURL, &tagsJSON,
		&notice.Visible, &notice.Revision, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Notice{}, ErrNotFound
	}
	if err != nil {
		return Notice{}, fmt.Errorf("scan notice: %w", err)
	}
	if err := json.Unmarshal([]byte(tagsJSON), &notice.Tags); err != nil {
		return Notice{}, fmt.Errorf("decode notice tags: %w", err)
	}
	if notice.Tags == nil {
		notice.Tags = []string{}
	}
	if imageURL.Valid {
		notice.ImageURL = &imageURL.String
	}
	notice.CreatedAt = time.Unix(createdAt, 0).UTC()
	notice.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return notice, nil
}

func normalizeNoticeInput(input SaveNoticeInput) (SaveNoticeInput, string, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Content = strings.TrimSpace(input.Content)
	input.ImageURL = strings.TrimSpace(input.ImageURL)
	if input.Title == "" || !utf8.ValidString(input.Title) || utf8.RuneCountInString(input.Title) > maxNoticeTitleRunes || strings.IndexFunc(input.Title, unicode.IsControl) >= 0 {
		return SaveNoticeInput{}, "", fmt.Errorf("%w: invalid notice title", ErrInvalidInput)
	}
	if input.Content == "" || !utf8.ValidString(input.Content) || len(input.Content) > maxNoticeContentBytes {
		return SaveNoticeInput{}, "", fmt.Errorf("%w: invalid notice content", ErrInvalidInput)
	}
	if input.ImageURL != "" {
		parsed, err := url.ParseRequestURI(input.ImageURL)
		if err != nil || len(input.ImageURL) > maxNoticeImageURL || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			return SaveNoticeInput{}, "", fmt.Errorf("%w: invalid notice image URL", ErrInvalidInput)
		}
	}
	if len(input.Tags) > maxNoticeTags {
		return SaveNoticeInput{}, "", fmt.Errorf("%w: too many notice tags", ErrInvalidInput)
	}
	tags := make([]string, 0, len(input.Tags))
	seen := make(map[string]struct{}, len(input.Tags))
	for _, tag := range input.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if !utf8.ValidString(tag) || utf8.RuneCountInString(tag) > maxNoticeTagRunes || strings.IndexFunc(tag, unicode.IsControl) >= 0 {
			return SaveNoticeInput{}, "", fmt.Errorf("%w: invalid notice tag", ErrInvalidInput)
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		tags = append(tags, tag)
	}
	input.Tags = tags
	encoded, err := json.Marshal(tags)
	if err != nil {
		return SaveNoticeInput{}, "", fmt.Errorf("encode notice tags: %w", err)
	}
	return input, string(encoded), nil
}

func (s *Store) noticeMutationError(ctx context.Context, noticeID int64) error {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notices WHERE id = ?)`, noticeID).Scan(&exists); err != nil {
		return fmt.Errorf("check notice revision: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrConflict
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
