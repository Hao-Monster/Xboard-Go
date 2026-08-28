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
	defer s.lockWrite()()
	return insertAdminAudit(ctx, s.db, input, now)
}

type adminAuditExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertAdminAudit(ctx context.Context, database adminAuditExecutor, input AdminAuditInput, now time.Time) error {
	input.AdministratorEmail = strings.ToLower(strings.TrimSpace(input.AdministratorEmail))
	input.Method = strings.ToUpper(strings.TrimSpace(input.Method))
	input.Route = strings.TrimSpace(input.Route)
	validRoute := strings.HasPrefix(input.Route, "/api/v1/admin/") || strings.HasPrefix(input.Route, "/api/v2/{secure_admin}/")
	if input.AdministratorID < 1 || input.AdministratorEmail == "" || len(input.AdministratorEmail) > 320 ||
		!isMutationMethod(input.Method) || !validRoute || len(input.Route) > 512 ||
		strings.ContainsAny(input.Route, "\r\n\x00") || input.StatusCode < 100 || input.StatusCode > 599 || now.IsZero() {
		return ErrInvalidInput
	}
	if _, err := database.ExecContext(ctx, `
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
		WITH all_mail AS (
			SELECT sent_at, failed_at, claim_token, available_at FROM ticket_mail_outbox
			UNION ALL
			SELECT sent_at, failed_at, claim_token, available_at FROM password_reset_mail_outbox
			WHERE cancelled_at IS NULL
			UNION ALL
			SELECT sent_at, failed_at, claim_token, available_at FROM registration_email_mail_outbox
			WHERE cancelled_at IS NULL
			UNION ALL
			SELECT sent_at, failed_at, claim_token, available_at FROM login_link_mail_outbox
			WHERE cancelled_at IS NULL
		)
		SELECT
			COALESCE(SUM(CASE WHEN sent_at IS NULL AND failed_at IS NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN sent_at IS NULL AND failed_at IS NULL AND claim_token IS NOT NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN sent_at IS NOT NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN failed_at IS NOT NULL THEN 1 ELSE 0 END), 0),
			MIN(CASE WHEN sent_at IS NULL AND failed_at IS NULL THEN available_at END)
		FROM all_mail
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
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM ticket_mail_outbox WHERE failed_at IS NOT NULL) +
			(SELECT COUNT(*) FROM password_reset_mail_outbox WHERE failed_at IS NOT NULL AND cancelled_at IS NULL) +
			(SELECT COUNT(*) FROM registration_email_mail_outbox WHERE failed_at IS NOT NULL AND cancelled_at IS NULL) +
			(SELECT COUNT(*) FROM login_link_mail_outbox WHERE failed_at IS NOT NULL AND cancelled_at IS NULL)
	`).Scan(&total); err != nil {
		return TicketMailFailurePage{}, fmt.Errorf("count failed mail: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, recipient, subject, attempt_count, last_error, created_at, failed_at FROM (
			SELECT id, 'ticket' AS kind, recipient, ticket_subject AS subject, attempt_count,
			       COALESCE(last_error, '') AS last_error, created_at, failed_at
			FROM ticket_mail_outbox WHERE failed_at IS NOT NULL
			UNION ALL
			SELECT -id AS id, 'password_reset' AS kind, recipient, '密码重置验证码' AS subject, attempt_count,
			       COALESCE(last_error, '') AS last_error, created_at, failed_at
			FROM password_reset_mail_outbox WHERE failed_at IS NOT NULL AND cancelled_at IS NULL
			UNION ALL
			SELECT -id AS id, 'registration_email_verification' AS kind, recipient, '注册邮箱验证码' AS subject, attempt_count,
			       COALESCE(last_error, '') AS last_error, created_at, failed_at
			FROM registration_email_mail_outbox WHERE failed_at IS NOT NULL AND cancelled_at IS NULL
			UNION ALL
			SELECT -id AS id, 'login_link' AS kind, recipient, '邮件登录链接' AS subject, attempt_count,
			       COALESCE(last_error, '') AS last_error, created_at, failed_at
			FROM login_link_mail_outbox WHERE failed_at IS NOT NULL AND cancelled_at IS NULL
		)
		ORDER BY failed_at DESC, id DESC LIMIT ? OFFSET ?
	`, pageSize, (page-1)*pageSize)
	if err != nil {
		return TicketMailFailurePage{}, fmt.Errorf("list failed mail: %w", err)
	}
	defer rows.Close()
	items := make([]TicketMailFailure, 0, min(pageSize, int(total)))
	for rows.Next() {
		var item TicketMailFailure
		var createdAt, failedAt int64
		if err := rows.Scan(&item.ID, &item.Kind, &item.Recipient, &item.TicketSubject, &item.AttemptCount, &item.LastError, &createdAt, &failedAt); err != nil {
			return TicketMailFailurePage{}, fmt.Errorf("scan failed mail: %w", err)
		}
		item.CreatedAt = time.Unix(createdAt, 0).UTC()
		item.FailedAt = time.Unix(failedAt, 0).UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return TicketMailFailurePage{}, fmt.Errorf("iterate failed mail: %w", err)
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
