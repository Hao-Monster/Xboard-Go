package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxAdminAuditQueryRunes = 200
	maxAdminAuditPage       = 1_000_000
	maxAdminAuditPageSize   = 100
)

func (s *Store) RecordAdminAudit(ctx context.Context, input AdminAuditInput, now time.Time) error {
	input.AdministratorEmail = strings.ToLower(strings.TrimSpace(input.AdministratorEmail))
	input.Method = strings.ToUpper(strings.TrimSpace(input.Method))
	input.Route = strings.TrimSpace(input.Route)
	if input.AdministratorID < 1 || input.AdministratorEmail == "" || len(input.AdministratorEmail) > 320 ||
		!isMutationMethod(input.Method) || !strings.HasPrefix(input.Route, "/api/v1/admin/") || len(input.Route) > 512 ||
		strings.ContainsAny(input.Route, "\r\n\x00") || input.StatusCode < 100 || input.StatusCode > 599 || now.IsZero() {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO admin_audit_logs (
			administrator_id, administrator_email, method, route, status_code, created_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`, input.AdministratorID, input.AdministratorEmail, input.Method, input.Route, input.StatusCode, now.Unix()); err != nil {
		return fmt.Errorf("record administrator audit: %w", err)
	}
	return nil
}

func (s *Store) ListAdminAuditLogs(ctx context.Context, filter AdminAuditFilter) (AdminAuditPage, error) {
	filter.Method = strings.ToUpper(strings.TrimSpace(filter.Method))
	filter.Query = strings.TrimSpace(filter.Query)
	if filter.Page < 1 || filter.Page > maxAdminAuditPage || filter.PageSize < 1 || filter.PageSize > maxAdminAuditPageSize ||
		(filter.Method != "" && !isMutationMethod(filter.Method)) || utf8.RuneCountInString(filter.Query) > maxAdminAuditQueryRunes {
		return AdminAuditPage{}, ErrInvalidInput
	}
	where := make([]string, 0, 2)
	arguments := make([]any, 0, 4)
	if filter.Method != "" {
		where = append(where, "method = ?")
		arguments = append(arguments, filter.Method)
	}
	if filter.Query != "" {
		where = append(where, "(route LIKE ? ESCAPE '\\' OR administrator_email LIKE ? ESCAPE '\\')")
		pattern := "%" + escapeLike(filter.Query) + "%"
		arguments = append(arguments, pattern, pattern)
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}
	var total int64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM admin_audit_logs"+whereSQL, arguments...).Scan(&total); err != nil {
		return AdminAuditPage{}, fmt.Errorf("count administrator audit: %w", err)
	}
	queryArguments := append(append([]any(nil), arguments...), filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, administrator_id, administrator_email, method, route, status_code, created_at
		FROM admin_audit_logs`+whereSQL+`
		ORDER BY id DESC LIMIT ? OFFSET ?
	`, queryArguments...)
	if err != nil {
		return AdminAuditPage{}, fmt.Errorf("list administrator audit: %w", err)
	}
	defer rows.Close()
	items := make([]AdminAuditLog, 0, min(filter.PageSize, int(total)))
	for rows.Next() {
		var item AdminAuditLog
		var administratorID sql.NullInt64
		var createdAt int64
		if err := rows.Scan(&item.ID, &administratorID, &item.AdministratorEmail, &item.Method, &item.Route, &item.StatusCode, &createdAt); err != nil {
			return AdminAuditPage{}, fmt.Errorf("scan administrator audit: %w", err)
		}
		if administratorID.Valid {
			value := administratorID.Int64
			item.AdministratorID = &value
		}
		item.CreatedAt = time.Unix(createdAt, 0).UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return AdminAuditPage{}, fmt.Errorf("iterate administrator audit: %w", err)
	}
	return AdminAuditPage{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (s *Store) GetSystemQueueStats(ctx context.Context) (SystemQueueStats, error) {
	var stats SystemQueueStats
	var oldestPending sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN sent_at IS NULL AND failed_at IS NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN sent_at IS NULL AND failed_at IS NULL AND claim_token IS NOT NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN sent_at IS NOT NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN failed_at IS NOT NULL THEN 1 ELSE 0 END), 0),
			MIN(CASE WHEN sent_at IS NULL AND failed_at IS NULL THEN available_at END)
		FROM ticket_mail_outbox
	`).Scan(&stats.Pending, &stats.Claimed, &stats.Sent, &stats.Failed, &oldestPending); err != nil {
		return SystemQueueStats{}, fmt.Errorf("get system queue stats: %w", err)
	}
	if oldestPending.Valid {
		value := time.Unix(oldestPending.Int64, 0).UTC()
		stats.OldestPendingAt = &value
	}
	return stats, nil
}

func (s *Store) ListTicketMailFailures(ctx context.Context, page, pageSize int) (TicketMailFailurePage, error) {
	if page < 1 || page > maxAdminAuditPage || pageSize < 1 || pageSize > maxAdminAuditPageSize {
		return TicketMailFailurePage{}, ErrInvalidInput
	}
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_mail_outbox WHERE failed_at IS NOT NULL`).Scan(&total); err != nil {
		return TicketMailFailurePage{}, fmt.Errorf("count failed ticket mail: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, recipient, ticket_subject, attempt_count, COALESCE(last_error, ''), created_at, failed_at
		FROM ticket_mail_outbox
		WHERE failed_at IS NOT NULL
		ORDER BY failed_at DESC, id DESC LIMIT ? OFFSET ?
	`, pageSize, (page-1)*pageSize)
	if err != nil {
		return TicketMailFailurePage{}, fmt.Errorf("list failed ticket mail: %w", err)
	}
	defer rows.Close()
	items := make([]TicketMailFailure, 0, min(pageSize, int(total)))
	for rows.Next() {
		var item TicketMailFailure
		var createdAt, failedAt int64
		if err := rows.Scan(&item.ID, &item.Recipient, &item.TicketSubject, &item.AttemptCount, &item.LastError, &createdAt, &failedAt); err != nil {
			return TicketMailFailurePage{}, fmt.Errorf("scan failed ticket mail: %w", err)
		}
		item.CreatedAt = time.Unix(createdAt, 0).UTC()
		item.FailedAt = time.Unix(failedAt, 0).UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return TicketMailFailurePage{}, fmt.Errorf("iterate failed ticket mail: %w", err)
	}
	return TicketMailFailurePage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func isMutationMethod(method string) bool {
	switch method {
	case "POST", "PUT", "PATCH", "DELETE":
		return true
	default:
		return false
	}
}
