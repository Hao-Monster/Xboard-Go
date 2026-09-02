package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const maxAdminUserOperationPageSize = 100

func (s *Store) ResetAdminUserTraffic(ctx context.Context, input AdminUserTrafficResetInput, now time.Time) (AdminUserTrafficResetResult, error) {
	normalized, err := normalizeAdminUserTrafficResetInput(input)
	if err != nil {
		return AdminUserTrafficResetResult{}, err
	}
	now = now.UTC()
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminUserTrafficResetResult{}, fmt.Errorf("begin administrator traffic reset: %w", err)
	}
	defer tx.Rollback()

	existing, found, err := findAdminUserTrafficReset(ctx, tx, normalized)
	if err != nil {
		return AdminUserTrafficResetResult{}, err
	}
	if found {
		existing.Idempotent = true
		if err := tx.Commit(); err != nil {
			return AdminUserTrafficResetResult{}, fmt.Errorf("commit idempotent administrator traffic reset: %w", err)
		}
		return existing, nil
	}

	var administratorEmail string
	if err := tx.QueryRowContext(ctx, `
		SELECT email FROM users WHERE id = ? AND account_kind = 'human' AND is_admin = 1
	`, normalized.AdministratorID).Scan(&administratorEmail); errors.Is(err, sql.ErrNoRows) {
		return AdminUserTrafficResetResult{}, ErrNotFound
	} else if err != nil {
		return AdminUserTrafficResetResult{}, fmt.Errorf("read traffic reset administrator: %w", err)
	}

	var result AdminUserTrafficResetResult
	var planID, groupID, expiry, nextReset, planMethod sql.NullInt64
	var banned bool
	var systemMethod int
	if err := tx.QueryRowContext(ctx, `
		SELECT u.email, u.uuid, u.group_id, u.plan_id, u.traffic_u, u.traffic_d, u.reset_count,
		       u.expired_at, u.banned, p.reset_traffic_method, settings.traffic_reset_method
		FROM users u
		LEFT JOIN plans p ON p.id = u.plan_id
		CROSS JOIN app_settings settings
		WHERE u.id = ? AND u.account_kind = 'human' AND settings.id = 1
	`, normalized.UserID).Scan(
		&result.Email, &result.UUID, &groupID, &planID, &result.UploadBefore, &result.DownloadBefore,
		&result.ResetCount, &expiry, &banned, &planMethod, &systemMethod,
	); errors.Is(err, sql.ErrNoRows) {
		return AdminUserTrafficResetResult{}, ErrNotFound
	} else if err != nil {
		return AdminUserTrafficResetResult{}, fmt.Errorf("read administrator traffic reset target: %w", err)
	}
	if banned || !planID.Valid || expiry.Valid && expiry.Int64 <= now.Unix() {
		return AdminUserTrafficResetResult{}, ErrTrafficResetUnavailable
	}
	result.UserID = normalized.UserID
	result.GroupID = nullableInt64Pointer(groupID)
	result.UploadAfter = 0
	result.DownloadAfter = 0
	result.ResetCount++
	result.ResetAt = now
	result.Reason = normalized.Reason

	var expiresAt *time.Time
	if expiry.Valid {
		value := time.Unix(expiry.Int64, 0).UTC()
		expiresAt = &value
	}
	var method *int
	result.ResetMethod = systemMethod
	if planMethod.Valid {
		value := int(planMethod.Int64)
		method = &value
		result.ResetMethod = value
	}
	result.NextResetAt = CalculateNextTrafficReset(method, systemMethod, expiresAt, now)
	if result.NextResetAt != nil {
		nextReset = sql.NullInt64{Int64: result.NextResetAt.Unix(), Valid: true}
	}

	updated, err := tx.ExecContext(ctx, `
		UPDATE users
		SET traffic_u = 0, traffic_d = 0, last_reset_at = ?, reset_count = ?, next_reset_at = ?,
		    admin_revision = admin_revision + 1, updated_at = ?
		WHERE id = ? AND account_kind = 'human' AND banned = 0 AND plan_id = ?
		  AND (expired_at IS NULL OR expired_at > ?)
	`, now.Unix(), result.ResetCount, nullableSQLInt(nextReset), now.Unix(), normalized.UserID, planID.Int64, now.Unix())
	if err != nil {
		return AdminUserTrafficResetResult{}, fmt.Errorf("reset administrator user traffic: %w", err)
	}
	rows, err := updated.RowsAffected()
	if err != nil {
		return AdminUserTrafficResetResult{}, fmt.Errorf("count administrator user traffic reset: %w", err)
	}
	if rows != 1 {
		return AdminUserTrafficResetResult{}, ErrTrafficResetUnavailable
	}
	var reason any
	if normalized.Reason != "" {
		reason = normalized.Reason
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO traffic_reset_logs (
			user_id, plan_id, scheduled_for, reset_at, upload_before, download_before,
			upload_after, download_after, reset_count, trigger_source, reason,
			administrator_id, administrator_email, idempotency_key
		) VALUES (?, ?, NULL, ?, ?, ?, 0, 0, ?, 'manual', ?, ?, ?, ?)
	`, normalized.UserID, planID.Int64, now.Unix(), result.UploadBefore, result.DownloadBefore,
		result.ResetCount, reason, normalized.AdministratorID, administratorEmail, normalized.IdempotencyKey); err != nil {
		return AdminUserTrafficResetResult{}, fmt.Errorf("record administrator user traffic reset: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AdminUserTrafficResetResult{}, fmt.Errorf("commit administrator user traffic reset: %w", err)
	}
	return result, nil
}

func findAdminUserTrafficReset(ctx context.Context, tx *sql.Tx, input AdminUserTrafficResetInput) (AdminUserTrafficResetResult, bool, error) {
	var result AdminUserTrafficResetResult
	var storedUserID int64
	var storedReason string
	var nextReset, groupID sql.NullInt64
	var resetAt int64
	err := tx.QueryRowContext(ctx, `
		SELECT l.user_id, u.email, u.uuid, u.group_id, l.upload_before, l.download_before,
		       l.upload_after, l.download_after, l.reset_count, l.reset_at, COALESCE(l.reason, ''), u.next_reset_at
		FROM traffic_reset_logs l JOIN users u ON u.id = l.user_id
		WHERE l.trigger_source = 'manual' AND l.administrator_id = ? AND l.idempotency_key = ?
	`, input.AdministratorID, input.IdempotencyKey).Scan(
		&storedUserID, &result.Email, &result.UUID, &groupID, &result.UploadBefore, &result.DownloadBefore,
		&result.UploadAfter, &result.DownloadAfter, &result.ResetCount, &resetAt, &storedReason, &nextReset,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminUserTrafficResetResult{}, false, nil
	}
	if err != nil {
		return AdminUserTrafficResetResult{}, false, fmt.Errorf("read idempotent administrator traffic reset: %w", err)
	}
	if storedUserID != input.UserID || storedReason != input.Reason {
		return AdminUserTrafficResetResult{}, false, fmt.Errorf("%w: idempotency key was already used for another traffic reset", ErrConflict)
	}
	result.UserID = storedUserID
	result.GroupID = nullableInt64Pointer(groupID)
	result.ResetAt = time.Unix(resetAt, 0).UTC()
	result.Reason = storedReason
	if nextReset.Valid {
		value := time.Unix(nextReset.Int64, 0).UTC()
		result.NextResetAt = &value
	}
	return result, true, nil
}

func normalizeAdminUserTrafficResetInput(input AdminUserTrafficResetInput) (AdminUserTrafficResetInput, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.UserID < 1 || input.AdministratorID < 1 || !utf8.ValidString(input.Reason) ||
		utf8.RuneCountInString(input.Reason) > 255 || strings.IndexByte(input.Reason, 0) >= 0 ||
		len(input.IdempotencyKey) < 8 || len(input.IdempotencyKey) > 128 {
		return AdminUserTrafficResetInput{}, ErrInvalidInput
	}
	for _, character := range input.IdempotencyKey {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("._:-", character) {
			continue
		}
		return AdminUserTrafficResetInput{}, ErrInvalidInput
	}
	return input, nil
}

func (s *Store) ListAdminUserTrafficResets(ctx context.Context, userID int64, page, pageSize int) (AdminUserTrafficResetPage, error) {
	page, pageSize, err := normalizeAdminUserOperationPage(userID, page, pageSize)
	if err != nil {
		return AdminUserTrafficResetPage{}, err
	}
	if err := s.requireAdminHumanUser(ctx, userID); err != nil {
		return AdminUserTrafficResetPage{}, err
	}
	result := AdminUserTrafficResetPage{Page: page, PageSize: pageSize}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM traffic_reset_logs WHERE user_id = ?`, userID).Scan(&result.Total); err != nil {
		return AdminUserTrafficResetPage{}, fmt.Errorf("count administrator user traffic resets: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.id, l.user_id, l.plan_id, l.scheduled_for, l.reset_at, l.upload_before, l.download_before,
		       l.upload_after, l.download_after, l.reset_count, l.trigger_source, COALESCE(l.reason, ''),
		       l.administrator_id, l.administrator_email,
		       COALESCE(p.reset_traffic_method, settings.traffic_reset_method)
		FROM traffic_reset_logs l
		LEFT JOIN plans p ON p.id = l.plan_id
		CROSS JOIN app_settings settings
		WHERE l.user_id = ? ORDER BY l.reset_at DESC, l.id DESC LIMIT ? OFFSET ?
	`, userID, pageSize, (page-1)*pageSize)
	if err != nil {
		return AdminUserTrafficResetPage{}, fmt.Errorf("list administrator user traffic resets: %w", err)
	}
	defer rows.Close()
	result.Items = make([]AdminUserTrafficResetLog, 0, min(pageSize, int(result.Total)))
	for rows.Next() {
		var item AdminUserTrafficResetLog
		var planID, scheduledFor, administratorID sql.NullInt64
		var administratorEmail sql.NullString
		var resetAt int64
		if err := rows.Scan(&item.ID, &item.UserID, &planID, &scheduledFor, &resetAt,
			&item.UploadBefore, &item.DownloadBefore, &item.UploadAfter, &item.DownloadAfter,
			&item.ResetCount, &item.TriggerSource, &item.Reason, &administratorID, &administratorEmail,
			&item.ResetMethod); err != nil {
			return AdminUserTrafficResetPage{}, fmt.Errorf("scan administrator user traffic reset: %w", err)
		}
		item.PlanID = nullableInt64Pointer(planID)
		item.AdministratorID = nullableInt64Pointer(administratorID)
		if administratorEmail.Valid {
			item.AdministratorEmail = &administratorEmail.String
		}
		item.ResetAt = time.Unix(resetAt, 0).UTC()
		if scheduledFor.Valid {
			value := time.Unix(scheduledFor.Int64, 0).UTC()
			item.ScheduledFor = &value
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return AdminUserTrafficResetPage{}, fmt.Errorf("iterate administrator user traffic resets: %w", err)
	}
	return result, nil
}

func (s *Store) ListAdminUserInvitations(ctx context.Context, userID int64, page, pageSize int) (AdminUserPage, error) {
	page, pageSize, err := normalizeAdminUserOperationPage(userID, page, pageSize)
	if err != nil {
		return AdminUserPage{}, err
	}
	if err := s.requireAdminHumanUser(ctx, userID); err != nil {
		return AdminUserPage{}, err
	}
	return s.ListAdminUsers(ctx, AdminUserFilter{
		Page: page, PageSize: pageSize, SortBy: AdminUserSortID, SortDescending: true,
		Rules: []AdminUserFilterRule{{Field: AdminUserFieldInviteUserID, Operator: AdminUserOperatorEqual, Values: []string{fmt.Sprint(userID)}}},
	})
}

func (s *Store) ListAdminUserTrafficStats(ctx context.Context, userID int64, page, pageSize int) (AdminUserTrafficStatPage, error) {
	page, pageSize, err := normalizeAdminUserOperationPage(userID, page, pageSize)
	if err != nil {
		return AdminUserTrafficStatPage{}, err
	}
	if err := s.requireAdminHumanUser(ctx, userID); err != nil {
		return AdminUserTrafficStatPage{}, err
	}
	result := AdminUserTrafficStatPage{Page: page, PageSize: pageSize}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_traffic_stats WHERE user_id = ?`, userID).Scan(&result.Total); err != nil {
		return AdminUserTrafficStatPage{}, fmt.Errorf("count administrator user traffic: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT rate_micros, record_at, record_type, upload, download
		FROM user_traffic_stats WHERE user_id = ?
		ORDER BY record_at DESC, record_type DESC, rate_micros DESC LIMIT ? OFFSET ?
	`, userID, pageSize, (page-1)*pageSize)
	if err != nil {
		return AdminUserTrafficStatPage{}, fmt.Errorf("list administrator user traffic: %w", err)
	}
	defer rows.Close()
	result.Items = make([]AdminUserTrafficStat, 0, min(pageSize, int(result.Total)))
	for rows.Next() {
		var item AdminUserTrafficStat
		var recordedAt int64
		if err := rows.Scan(&item.RateMicros, &recordedAt, &item.RecordType, &item.Upload, &item.Download); err != nil {
			return AdminUserTrafficStatPage{}, fmt.Errorf("scan administrator user traffic: %w", err)
		}
		item.RecordAt = time.Unix(recordedAt, 0).UTC()
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return AdminUserTrafficStatPage{}, fmt.Errorf("iterate administrator user traffic: %w", err)
	}
	return result, nil
}

func (s *Store) GetAdminUserSubscriptionToken(ctx context.Context, userID int64) (string, error) {
	if userID < 1 {
		return "", ErrInvalidInput
	}
	var token string
	if err := s.db.QueryRowContext(ctx, `
		SELECT subscription_token FROM users
		WHERE id = ? AND account_kind = 'human' AND lifecycle_status = 'active'
	`, userID).Scan(&token); errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", fmt.Errorf("read administrator user subscription token: %w", err)
	}
	return token, nil
}

func (s *Store) requireAdminHumanUser(ctx context.Context, userID int64) error {
	if userID < 1 {
		return ErrInvalidInput
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM users WHERE id = ? AND account_kind = 'human')
	`, userID).Scan(&exists); err != nil {
		return fmt.Errorf("read administrator user target: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func normalizeAdminUserOperationPage(userID int64, page, pageSize int) (int, int, error) {
	if page == 0 {
		page = 1
	}
	if pageSize == 0 {
		pageSize = 20
	}
	if userID < 1 || page < 1 || page > maxAdminUserPage || pageSize < 1 || pageSize > maxAdminUserOperationPageSize {
		return 0, 0, ErrInvalidInput
	}
	return page, pageSize, nil
}
