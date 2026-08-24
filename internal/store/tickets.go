package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultTicketPageSize = 20
	maxTicketPageSize     = 100
	maxTicketMessageBytes = 64 << 10
	maxTicketMessages     = 1_000
	maxTicketCloseBatch   = 1_000
)

func (s *Store) CreateTicket(ctx context.Context, userID int64, input SaveTicketInput, now time.Time) (Ticket, error) {
	input.Subject = strings.TrimSpace(input.Subject)
	input.Message = strings.TrimSpace(input.Message)
	if userID < 1 || !validTicketSubject(input.Subject) || !validTicketLevel(input.Level) || !validTicketMessage(input.Message) {
		return Ticket{}, fmt.Errorf("%w: invalid ticket", ErrInvalidInput)
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Ticket{}, fmt.Errorf("begin create ticket: %w", err)
	}
	defer tx.Rollback()

	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tickets WHERE user_id = ? AND status = 0)`, userID).Scan(&exists); err != nil {
		return Ticket{}, fmt.Errorf("check open ticket: %w", err)
	}
	if exists {
		return Ticket{}, ErrOpenTicketExists
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO tickets (user_id, subject, level, status, reply_status, last_reply_user_id, created_at, updated_at)
		VALUES (?, ?, ?, 0, 0, ?, ?, ?)
	`, userID, input.Subject, input.Level, userID, now.Unix(), now.Unix())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Ticket{}, ErrOpenTicketExists
		}
		return Ticket{}, fmt.Errorf("insert ticket: %w", err)
	}
	ticketID, err := result.LastInsertId()
	if err != nil {
		return Ticket{}, fmt.Errorf("read ticket id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ticket_messages (ticket_id, user_id, message, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, ticketID, userID, input.Message, now.Unix(), now.Unix()); err != nil {
		return Ticket{}, fmt.Errorf("insert initial ticket message: %w", err)
	}
	ticket, err := getTicketTx(ctx, tx, ticketID, false)
	if err != nil {
		return Ticket{}, err
	}
	if err := tx.Commit(); err != nil {
		return Ticket{}, fmt.Errorf("commit create ticket: %w", err)
	}
	return ticket, nil
}

func (s *Store) ListUserTickets(ctx context.Context, userID int64, page, pageSize int) (TicketPage, error) {
	page, pageSize, offset, err := normalizeTicketPage(page, pageSize)
	if err != nil || userID < 1 {
		return TicketPage{}, fmt.Errorf("%w: invalid ticket page", ErrInvalidInput)
	}
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets WHERE user_id = ?`, userID).Scan(&total); err != nil {
		return TicketPage{}, fmt.Errorf("count user tickets: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, '', subject, level, status, reply_status, last_reply_user_id, created_at, updated_at
		FROM tickets WHERE user_id = ? ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?
	`, userID, pageSize, offset)
	if err != nil {
		return TicketPage{}, fmt.Errorf("list user tickets: %w", err)
	}
	items, err := scanTickets(rows)
	if err != nil {
		return TicketPage{}, err
	}
	return TicketPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *Store) ListAdminTickets(ctx context.Context, filter TicketFilter) (TicketPage, error) {
	page, pageSize, offset, err := normalizeTicketPage(filter.Page, filter.PageSize)
	if err != nil || !validTicketFilter(filter) {
		return TicketPage{}, fmt.Errorf("%w: invalid ticket filter", ErrInvalidInput)
	}
	filter.Query = strings.ToLower(strings.TrimSpace(filter.Query))
	where := ` WHERE u.account_kind = 'human'`
	args := make([]any, 0, 8)
	if filter.Status != nil {
		where += ` AND t.status = ?`
		args = append(args, *filter.Status)
	}
	if filter.ReplyStatus != nil {
		where += ` AND t.reply_status = ?`
		args = append(args, *filter.ReplyStatus)
	}
	if filter.Level != nil {
		where += ` AND t.level = ?`
		args = append(args, *filter.Level)
	}
	if filter.Query != "" {
		pattern := "%" + escapeLike(filter.Query) + "%"
		where += ` AND (lower(t.subject) LIKE ? ESCAPE '\' OR lower(u.email) LIKE ? ESCAPE '\')`
		args = append(args, pattern, pattern)
	}
	from := ` FROM tickets t JOIN users u ON u.id = t.user_id`
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+from+where, args...).Scan(&total); err != nil {
		return TicketPage{}, fmt.Errorf("count admin tickets: %w", err)
	}
	listArgs := append(append([]any(nil), args...), pageSize, offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.user_id, u.email, t.subject, t.level, t.status, t.reply_status,
		       t.last_reply_user_id, t.created_at, t.updated_at
	`+from+where+` ORDER BY t.updated_at DESC, t.id DESC LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return TicketPage{}, fmt.Errorf("list admin tickets: %w", err)
	}
	items, err := scanTickets(rows)
	if err != nil {
		return TicketPage{}, err
	}
	return TicketPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *Store) GetUserTicket(ctx context.Context, userID, ticketID int64) (Ticket, error) {
	if userID < 1 || ticketID < 1 {
		return Ticket{}, fmt.Errorf("%w: invalid ticket identity", ErrInvalidInput)
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, '', subject, level, status, reply_status, last_reply_user_id, created_at, updated_at
		FROM tickets WHERE id = ? AND user_id = ?
	`, ticketID, userID)
	ticket, err := scanTicket(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	}
	if err != nil {
		return Ticket{}, err
	}
	if err := s.loadTicketMessages(ctx, &ticket, userID); err != nil {
		return Ticket{}, err
	}
	return ticket, nil
}

func (s *Store) GetAdminTicket(ctx context.Context, ticketID int64) (Ticket, error) {
	if ticketID < 1 {
		return Ticket{}, fmt.Errorf("%w: invalid ticket identity", ErrInvalidInput)
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT t.id, t.user_id, u.email, t.subject, t.level, t.status, t.reply_status,
		       t.last_reply_user_id, t.created_at, t.updated_at
		FROM tickets t JOIN users u ON u.id = t.user_id WHERE t.id = ? AND u.account_kind = 'human'
	`, ticketID)
	ticket, err := scanTicket(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	}
	if err != nil {
		return Ticket{}, err
	}
	if err := s.loadTicketMessages(ctx, &ticket, ticket.UserID); err != nil {
		return Ticket{}, err
	}
	return ticket, nil
}

func (s *Store) ReplyTicketAsUser(ctx context.Context, userID, ticketID int64, message string, now time.Time) (Ticket, error) {
	return s.replyTicket(ctx, userID, ticketID, userID, true, message, now)
}

func (s *Store) ReplyTicketAsAdmin(ctx context.Context, adminID, ticketID int64, message string, now time.Time) (Ticket, error) {
	return s.replyTicket(ctx, 0, ticketID, adminID, false, message, now)
}

func (s *Store) replyTicket(ctx context.Context, ownerID, ticketID, authorID int64, requireOpen bool, message string, now time.Time) (Ticket, error) {
	message = strings.TrimSpace(message)
	if ticketID < 1 || authorID < 1 || (ownerID < 1 && requireOpen) || !validTicketMessage(message) {
		return Ticket{}, fmt.Errorf("%w: invalid ticket reply", ErrInvalidInput)
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Ticket{}, fmt.Errorf("begin ticket reply: %w", err)
	}
	defer tx.Rollback()
	query := `SELECT id, user_id, '', subject, level, status, reply_status, last_reply_user_id, created_at, updated_at FROM tickets WHERE id = ?`
	args := []any{ticketID}
	if ownerID > 0 {
		query += ` AND user_id = ?`
		args = append(args, ownerID)
	}
	ticket, err := scanTicket(tx.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	}
	if err != nil {
		return Ticket{}, err
	}
	if requireOpen && ticket.Status == TicketStatusClosed {
		return Ticket{}, ErrTicketClosed
	}
	var messageCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_messages WHERE ticket_id = ?`, ticketID).Scan(&messageCount); err != nil {
		return Ticket{}, fmt.Errorf("count ticket messages: %w", err)
	}
	if messageCount >= maxTicketMessages {
		return Ticket{}, ErrTicketMessageLimit
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ticket_messages (ticket_id, user_id, message, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
	`, ticketID, authorID, message, now.Unix(), now.Unix()); err != nil {
		return Ticket{}, fmt.Errorf("insert ticket reply: %w", err)
	}
	replyStatus := TicketReplyWaiting
	if authorID != ticket.UserID {
		replyStatus = TicketReplyAnswered
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE tickets SET reply_status = ?, last_reply_user_id = ?, updated_at = ? WHERE id = ?
	`, replyStatus, authorID, now.Unix(), ticketID); err != nil {
		return Ticket{}, fmt.Errorf("update ticket reply state: %w", err)
	}
	updated, err := getTicketTx(ctx, tx, ticketID, false)
	if err != nil {
		return Ticket{}, err
	}
	if err := tx.Commit(); err != nil {
		return Ticket{}, fmt.Errorf("commit ticket reply: %w", err)
	}
	return updated, nil
}

func (s *Store) CloseTicketAsUser(ctx context.Context, userID, ticketID int64, now time.Time) (Ticket, error) {
	return s.closeTicket(ctx, ticketID, userID, now)
}

func (s *Store) CloseTicketAsAdmin(ctx context.Context, ticketID int64, now time.Time) (Ticket, error) {
	return s.closeTicket(ctx, ticketID, 0, now)
}

func (s *Store) closeTicket(ctx context.Context, ticketID, ownerID int64, now time.Time) (Ticket, error) {
	if ticketID < 1 || ownerID < 0 {
		return Ticket{}, fmt.Errorf("%w: invalid ticket identity", ErrInvalidInput)
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Ticket{}, fmt.Errorf("begin close ticket: %w", err)
	}
	defer tx.Rollback()
	query := `SELECT id, user_id, '', subject, level, status, reply_status, last_reply_user_id, created_at, updated_at FROM tickets WHERE id = ?`
	args := []any{ticketID}
	if ownerID > 0 {
		query += ` AND user_id = ?`
		args = append(args, ownerID)
	}
	ticket, err := scanTicket(tx.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	}
	if err != nil {
		return Ticket{}, err
	}
	if ticket.Status == TicketStatusOpen {
		if _, err := tx.ExecContext(ctx, `UPDATE tickets SET status = 1, updated_at = ? WHERE id = ? AND status = 0`, now.Unix(), ticketID); err != nil {
			return Ticket{}, fmt.Errorf("close ticket: %w", err)
		}
	}
	updated, err := getTicketTx(ctx, tx, ticketID, false)
	if err != nil {
		return Ticket{}, err
	}
	if err := tx.Commit(); err != nil {
		return Ticket{}, fmt.Errorf("commit close ticket: %w", err)
	}
	return updated, nil
}

func (s *Store) CloseStaleAnsweredTickets(ctx context.Context, cutoff, now time.Time, limit int) (int64, error) {
	if limit < 1 || limit > maxTicketCloseBatch {
		return 0, fmt.Errorf("%w: invalid ticket close batch", ErrInvalidInput)
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE tickets SET status = 1, updated_at = ?
		WHERE id IN (
			SELECT id FROM tickets
			WHERE status = 0 AND reply_status = 1 AND last_reply_user_id <> user_id AND updated_at <= ?
			ORDER BY updated_at, id LIMIT ?
		) AND status = 0 AND reply_status = 1 AND last_reply_user_id <> user_id AND updated_at <= ?
	`, now.Unix(), cutoff.Unix(), limit, cutoff.Unix())
	if err != nil {
		return 0, fmt.Errorf("close stale answered tickets: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count closed stale tickets: %w", err)
	}
	return count, nil
}

func (s *Store) loadTicketMessages(ctx context.Context, ticket *Ticket, viewerUserID int64) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, ticket_id, user_id, message, created_at, updated_at
		FROM ticket_messages WHERE ticket_id = ? ORDER BY id
	`, ticket.ID)
	if err != nil {
		return fmt.Errorf("list ticket messages: %w", err)
	}
	defer rows.Close()
	ticket.Messages = make([]TicketMessage, 0, 8)
	for rows.Next() {
		var message TicketMessage
		var createdAt, updatedAt int64
		if err := rows.Scan(&message.ID, &message.TicketID, &message.UserID, &message.Message, &createdAt, &updatedAt); err != nil {
			return fmt.Errorf("scan ticket message: %w", err)
		}
		message.IsMe = message.UserID == viewerUserID
		message.CreatedAt = time.Unix(createdAt, 0).UTC()
		message.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		ticket.Messages = append(ticket.Messages, message)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate ticket messages: %w", err)
	}
	return nil
}

func getTicketTx(ctx context.Context, tx *sql.Tx, ticketID int64, includeEmail bool) (Ticket, error) {
	if includeEmail {
		return scanTicket(tx.QueryRowContext(ctx, `
			SELECT t.id, t.user_id, u.email, t.subject, t.level, t.status, t.reply_status,
			       t.last_reply_user_id, t.created_at, t.updated_at
			FROM tickets t JOIN users u ON u.id = t.user_id WHERE t.id = ?
		`, ticketID))
	}
	return scanTicket(tx.QueryRowContext(ctx, `
		SELECT id, user_id, '', subject, level, status, reply_status, last_reply_user_id, created_at, updated_at
		FROM tickets WHERE id = ?
	`, ticketID))
}

func scanTickets(rows *sql.Rows) ([]Ticket, error) {
	defer rows.Close()
	items := make([]Ticket, 0, 32)
	for rows.Next() {
		ticket, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, ticket)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tickets: %w", err)
	}
	return items, nil
}

func scanTicket(row rowScanner) (Ticket, error) {
	var ticket Ticket
	var createdAt, updatedAt int64
	if err := row.Scan(&ticket.ID, &ticket.UserID, &ticket.UserEmail, &ticket.Subject, &ticket.Level,
		&ticket.Status, &ticket.ReplyStatus, &ticket.LastReplyUserID, &createdAt, &updatedAt); err != nil {
		return Ticket{}, err
	}
	ticket.CreatedAt = time.Unix(createdAt, 0).UTC()
	ticket.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return ticket, nil
}

func normalizeTicketPage(page, pageSize int) (int, int, int64, error) {
	if page == 0 {
		page = 1
	}
	if pageSize == 0 {
		pageSize = defaultTicketPageSize
	}
	if page < 1 || pageSize < 1 || pageSize > maxTicketPageSize || int64(page-1) > math.MaxInt64/int64(pageSize) {
		return 0, 0, 0, ErrInvalidInput
	}
	return page, pageSize, int64(page-1) * int64(pageSize), nil
}

func validTicketFilter(filter TicketFilter) bool {
	return len(filter.Query) <= 320 &&
		(filter.Status == nil || validTicketStatus(*filter.Status)) &&
		(filter.ReplyStatus == nil || validTicketReplyStatus(*filter.ReplyStatus)) &&
		(filter.Level == nil || validTicketLevel(*filter.Level))
}

func validTicketSubject(value string) bool {
	count := utf8.RuneCountInString(value)
	return count > 0 && count <= 255 && !containsUnsafeTicketControl(value, false)
}

func validTicketMessage(value string) bool {
	return value != "" && len(value) <= maxTicketMessageBytes && !containsUnsafeTicketControl(value, true)
}

func containsUnsafeTicketControl(value string, allowWhitespace bool) bool {
	for _, character := range value {
		if character == '\t' || character == '\n' || character == '\r' {
			if allowWhitespace {
				continue
			}
			return true
		}
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func validTicketLevel(value TicketLevel) bool {
	return value >= TicketLevelLow && value <= TicketLevelHigh
}

func validTicketStatus(value TicketStatus) bool {
	return value >= TicketStatusOpen && value <= TicketStatusClosed
}

func validTicketReplyStatus(value TicketReplyStatus) bool {
	return value >= TicketReplyWaiting && value <= TicketReplyAnswered
}
