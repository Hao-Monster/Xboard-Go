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
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	maxAdminUserBulkTargets     = 10_000
	maxAdminUserBulkSelected    = 500
	maxAdminUserBulkTargetPage  = 500
	maxAdminUserBulkSubjectRune = 255
	maxAdminUserBulkContentByte = 65_536
	maxAdminUserBulkErrorByte   = 2_048
	maxAdminUserBulkAttempts    = 3
)

func (s *Store) CreateAdminUserBulkJob(ctx context.Context, input CreateAdminUserBulkJobInput, now time.Time) (AdminUserBulkJob, error) {
	if input.Kind != AdminUserBulkKindMail && input.Kind != AdminUserBulkKindCSV {
		return AdminUserBulkJob{}, fmt.Errorf("%w: unsupported administrator user bulk job kind", ErrInvalidInput)
	}
	normalizedScope, where, arguments, digest, err := normalizeAdminUserBulkScope(input.Scope)
	if err != nil {
		return AdminUserBulkJob{}, err
	}
	input.Subject = strings.TrimSpace(input.Subject)
	if input.Kind == AdminUserBulkKindMail && (!utf8.ValidString(input.Subject) || utf8.RuneCountInString(input.Subject) < 1 ||
		utf8.RuneCountInString(input.Subject) > maxAdminUserBulkSubjectRune || !validAdminUserBulkText(input.Content, maxAdminUserBulkContentByte)) {
		return AdminUserBulkJob{}, fmt.Errorf("%w: invalid administrator bulk mail template", ErrInvalidInput)
	}
	now = now.UTC()
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("begin administrator user bulk job: %w", err)
	}
	defer tx.Rollback()
	job, err := createAdminUserBulkJobTx(ctx, tx, input, normalizedScope, where, arguments, digest, now)
	if err != nil {
		return AdminUserBulkJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("commit administrator user bulk job: %w", err)
	}
	return job, nil
}

func createAdminUserBulkJobTx(ctx context.Context, tx *sql.Tx, input CreateAdminUserBulkJobInput, scope AdminUserBulkScope, where string, arguments []any, digest string, now time.Time) (AdminUserBulkJob, error) {
	var administratorEmail string
	if err := tx.QueryRowContext(ctx, `
		SELECT email FROM users
		WHERE id = ? AND account_kind = 'human' AND lifecycle_status = 'active' AND is_admin = 1 AND banned = 0
	`, input.AdministratorID).Scan(&administratorEmail); errors.Is(err, sql.ErrNoRows) {
		return AdminUserBulkJob{}, ErrNotFound
	} else if err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("read administrator user bulk operator: %w", err)
	}

	from := adminUserBulkSnapshotFrom
	if input.Kind == AdminUserBulkKindMail {
		where += ` AND u.lifecycle_status = 'active'`
	}
	countArguments := append([]any(nil), arguments...)
	countArguments = append(countArguments, maxAdminUserBulkTargets+1)
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT 1 `+from+where+` LIMIT ?)`, countArguments...).Scan(&total); err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("count administrator user bulk targets: %w", err)
	}
	if total < 1 {
		return AdminUserBulkJob{}, fmt.Errorf("%w: administrator user bulk scope has no targets", ErrInvalidInput)
	}
	if total > maxAdminUserBulkTargets {
		return AdminUserBulkJob{}, ErrAdminUserBulkLimit
	}

	jobID := uuid.NewString()
	var appName, appURL, smtpHost, smtpUsername, smtpEncryption, smtpFromAddress string
	var smtpPort int
	var smtpPasswordCipher []byte
	if input.Kind == AdminUserBulkKindMail || input.Kind == AdminUserBulkKindCSV {
		var smtpEnabled bool
		if err := tx.QueryRowContext(ctx, `
			SELECT app_name, app_url, smtp_enabled, smtp_host, smtp_port, smtp_username,
			       smtp_password_cipher, smtp_encryption, smtp_from_address
			FROM app_settings WHERE id = 1
		`).Scan(&appName, &appURL, &smtpEnabled, &smtpHost, &smtpPort, &smtpUsername,
			&smtpPasswordCipher, &smtpEncryption, &smtpFromAddress); err != nil {
			return AdminUserBulkJob{}, fmt.Errorf("read administrator user bulk settings: %w", err)
		}
		if input.Kind == AdminUserBulkKindMail && (!smtpEnabled || strings.TrimSpace(smtpHost) == "" || strings.TrimSpace(smtpFromAddress) == "") {
			return AdminUserBulkJob{}, ErrMailUnavailable
		}
	}

	var subject, content, mailAppName, mailAppURL, storedSMTPHost, storedSMTPUsername, storedSMTPEncryption, storedSMTPFromAddress any
	var storedSMTPPort, storedSMTPPassword any
	if input.Kind == AdminUserBulkKindMail {
		subject, content, mailAppName, mailAppURL = input.Subject, input.Content, appName, appURL
		storedSMTPHost, storedSMTPPort, storedSMTPUsername = smtpHost, smtpPort, smtpUsername
		if len(smtpPasswordCipher) > 0 {
			storedSMTPPassword = smtpPasswordCipher
		}
		storedSMTPEncryption, storedSMTPFromAddress = smtpEncryption, smtpFromAddress
	} else if input.Kind == AdminUserBulkKindCSV {
		mailAppURL = appURL
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO admin_user_bulk_jobs (
			id, kind, scope, administrator_id, administrator_email, status, request_digest,
			subject, content, app_name, app_url, smtp_host, smtp_port, smtp_username,
			smtp_password_cipher, smtp_encryption, smtp_from_address, total_count, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 'queued', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, jobID, input.Kind, scope.Scope, input.AdministratorID, administratorEmail, digest,
		subject, content, mailAppName, mailAppURL, storedSMTPHost, storedSMTPPort, storedSMTPUsername,
		storedSMTPPassword, storedSMTPEncryption, storedSMTPFromAddress, total, now.Unix(), now.Unix()); err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("create administrator user bulk job: %w", err)
	}
	insertArguments := make([]any, 0, len(arguments)+2)
	insertArguments = append(insertArguments, jobID, now.Unix())
	insertArguments = append(insertArguments, arguments...)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO admin_user_bulk_targets (
			job_id, sequence, user_id, email, uuid, plan_name, group_id, expired_at, transfer_enable,
			transfer_used, balance, commission_balance, subscription_token, available_at
		)
		SELECT ?, u.id, u.id, u.email, COALESCE(u.uuid, ''), COALESCE(p.name, ''), u.group_id, u.expired_at,
		       u.transfer_enable, u.traffic_u + u.traffic_d, u.balance, u.commission_balance,
		       u.subscription_token, ?
		`+from+where+` ORDER BY u.id ASC
	`, insertArguments...); err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("snapshot administrator user bulk targets: %w", err)
	}
	job, err := getAdminUserBulkJobTx(ctx, tx, jobID)
	if err != nil {
		return AdminUserBulkJob{}, err
	}
	return job, nil
}

const adminUserBulkSnapshotFrom = `
	FROM users u
	LEFT JOIN users inviter ON inviter.id = u.invite_user_id AND inviter.account_kind = 'human'
	LEFT JOIN plans p ON p.id = u.plan_id`

func normalizeAdminUserBulkScope(input AdminUserBulkScope) (AdminUserBulkScope, string, []any, string, error) {
	input.Scope = strings.ToLower(strings.TrimSpace(input.Scope))
	var where string
	var arguments []any
	switch input.Scope {
	case AdminUserBulkScopeSelected:
		if len(input.UserIDs) < 1 || len(input.UserIDs) > maxAdminUserBulkSelected {
			return AdminUserBulkScope{}, "", nil, "", fmt.Errorf("%w: selected user ids must contain 1 to %d values", ErrInvalidInput, maxAdminUserBulkSelected)
		}
		seen := make(map[int64]struct{}, len(input.UserIDs))
		ids := make([]int64, 0, len(input.UserIDs))
		for _, userID := range input.UserIDs {
			if userID < 1 {
				return AdminUserBulkScope{}, "", nil, "", fmt.Errorf("%w: selected user ids must be positive", ErrInvalidInput)
			}
			if _, exists := seen[userID]; exists {
				continue
			}
			seen[userID] = struct{}{}
			ids = append(ids, userID)
		}
		sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
		input.UserIDs = ids
		input.Filter = AdminUserFilter{}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		where = ` WHERE u.account_kind = 'human' AND u.id IN (` + placeholders + `)`
		arguments = make([]any, len(ids))
		for index, userID := range ids {
			arguments[index] = userID
		}
	case AdminUserBulkScopeFiltered:
		if !adminUserBulkFilterPresent(input.Filter) || input.Filter.Cursor != "" || input.Filter.Limit != 0 ||
			input.Filter.Page != 0 || input.Filter.PageSize != 0 {
			return AdminUserBulkScope{}, "", nil, "", fmt.Errorf("%w: filtered scope requires an unpaginated filter", ErrInvalidInput)
		}
		var err error
		where, arguments, err = buildAdminUserWhere(input.Filter)
		if err != nil {
			return AdminUserBulkScope{}, "", nil, "", err
		}
		input.UserIDs = nil
	case AdminUserBulkScopeAll:
		input.UserIDs = nil
		input.Filter = AdminUserFilter{}
		where = ` WHERE u.account_kind = 'human'`
	default:
		return AdminUserBulkScope{}, "", nil, "", fmt.Errorf("%w: invalid administrator user bulk scope", ErrInvalidInput)
	}
	canonical, err := json.Marshal(input)
	if err != nil {
		return AdminUserBulkScope{}, "", nil, "", fmt.Errorf("encode administrator user bulk scope: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return input, where, arguments, hex.EncodeToString(digest[:]), nil
}

func adminUserBulkFilterPresent(filter AdminUserFilter) bool {
	return strings.TrimSpace(filter.EmailPrefix) != "" || filter.Banned != nil || filter.GroupID != nil || len(filter.Rules) > 0
}

func validAdminUserBulkText(value string, maximumBytes int) bool {
	return utf8.ValidString(value) && len(value) >= 1 && len(value) <= maximumBytes && strings.IndexByte(value, 0) < 0
}

func (s *Store) GetAdminUserBulkJob(ctx context.Context, jobID string) (AdminUserBulkJob, error) {
	if _, err := uuid.Parse(jobID); err != nil {
		return AdminUserBulkJob{}, ErrInvalidInput
	}
	job, err := scanAdminUserBulkJob(s.db.QueryRowContext(ctx, adminUserBulkJobSelect+` WHERE id = ?`, jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return AdminUserBulkJob{}, ErrNotFound
	}
	return job, err
}

func (s *Store) ListAdminUserBulkJobs(ctx context.Context, page, pageSize int) (AdminUserBulkJobPage, error) {
	if page == 0 {
		page = 1
	}
	if pageSize == 0 {
		pageSize = 20
	}
	if page < 1 || pageSize < 1 || pageSize > 100 {
		return AdminUserBulkJobPage{}, ErrInvalidInput
	}
	result := AdminUserBulkJobPage{Page: page, PageSize: pageSize}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_user_bulk_jobs`).Scan(&result.Total); err != nil {
		return AdminUserBulkJobPage{}, fmt.Errorf("count administrator user bulk jobs: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, adminUserBulkJobSelect+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, pageSize, (page-1)*pageSize)
	if err != nil {
		return AdminUserBulkJobPage{}, fmt.Errorf("list administrator user bulk jobs: %w", err)
	}
	defer rows.Close()
	result.Items = make([]AdminUserBulkJob, 0, min(pageSize, int(result.Total)))
	for rows.Next() {
		job, err := scanAdminUserBulkJob(rows)
		if err != nil {
			return AdminUserBulkJobPage{}, err
		}
		result.Items = append(result.Items, job)
	}
	if err := rows.Err(); err != nil {
		return AdminUserBulkJobPage{}, fmt.Errorf("iterate administrator user bulk jobs: %w", err)
	}
	return result, nil
}

const adminUserBulkJobSelect = `
	SELECT id, kind, scope, administrator_id, administrator_email, status, subject, content,
	       app_name, app_url, total_count, processed_count, success_count, failure_count, skipped_count,
	       cancelled_count, output_filename, output_relative_path, output_size, output_sha256,
	       output_expires_at, last_error, started_at, completed_at, cancelled_at, created_at, updated_at
	FROM admin_user_bulk_jobs`

func scanAdminUserBulkJob(row rowScanner) (AdminUserBulkJob, error) {
	var result AdminUserBulkJob
	var administratorID, outputSize, outputExpires, startedAt, completedAt, cancelledAt sql.NullInt64
	var subject, content, appName, appURL, outputFilename, outputPath, outputSHA, lastError sql.NullString
	var createdAt, updatedAt int64
	if err := row.Scan(
		&result.ID, &result.Kind, &result.Scope, &administratorID, &result.AdministratorEmail, &result.Status,
		&subject, &content, &appName, &appURL, &result.TotalCount, &result.ProcessedCount, &result.SuccessCount,
		&result.FailureCount, &result.SkippedCount, &result.CancelledCount, &outputFilename, &outputPath,
		&outputSize, &outputSHA, &outputExpires, &lastError, &startedAt, &completedAt, &cancelledAt,
		&createdAt, &updatedAt,
	); err != nil {
		return AdminUserBulkJob{}, err
	}
	result.AdministratorID = nullableInt64Pointer(administratorID)
	result.Subject, result.Content, result.AppName, result.AppURL = subject.String, content.String, appName.String, appURL.String
	result.OutputFilename, result.OutputRelativePath, result.OutputSHA256, result.LastError = outputFilename.String, outputPath.String, outputSHA.String, lastError.String
	if outputSize.Valid {
		result.OutputSize = &outputSize.Int64
	}
	result.OutputExpiresAt = nullableUnixTime(outputExpires)
	result.StartedAt, result.CompletedAt, result.CancelledAt = nullableUnixTime(startedAt), nullableUnixTime(completedAt), nullableUnixTime(cancelledAt)
	result.CreatedAt, result.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	return result, nil
}

func (s *Store) ListAdminUserBulkTargets(ctx context.Context, jobID string, afterSequence int64, limit int) ([]AdminUserBulkTarget, error) {
	if _, err := uuid.Parse(jobID); err != nil || afterSequence < 0 || limit < 1 || limit > maxAdminUserBulkTargetPage {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, adminUserBulkTargetSelect+`
		WHERE job_id = ? AND sequence > ? ORDER BY sequence LIMIT ?`, jobID, afterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("list administrator user bulk targets: %w", err)
	}
	defer rows.Close()
	result := make([]AdminUserBulkTarget, 0, min(limit, 64))
	for rows.Next() {
		target, err := scanAdminUserBulkTarget(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate administrator user bulk targets: %w", err)
	}
	return result, nil
}

const adminUserBulkTargetSelect = `
	SELECT job_id, sequence, user_id, email, uuid, plan_name, group_id, expired_at, transfer_enable,
	       transfer_used, balance, commission_balance, subscription_token, status, attempt_count,
	       last_error, processed_at
	FROM admin_user_bulk_targets `

func scanAdminUserBulkTarget(row rowScanner) (AdminUserBulkTarget, error) {
	var result AdminUserBulkTarget
	var userID, groupID, expiredAt, processedAt sql.NullInt64
	var lastError sql.NullString
	if err := row.Scan(&result.JobID, &result.Sequence, &userID, &result.Email, &result.UUID, &result.PlanName,
		&groupID, &expiredAt, &result.TransferEnable, &result.TransferUsed, &result.Balance, &result.CommissionBalance,
		&result.SubscriptionToken, &result.Status, &result.AttemptCount, &lastError, &processedAt); err != nil {
		return AdminUserBulkTarget{}, err
	}
	if userID.Valid {
		result.UserID = userID.Int64
	}
	result.GroupID = nullableInt64Pointer(groupID)
	result.ExpiredAt, result.ProcessedAt = nullableUnixTime(expiredAt), nullableUnixTime(processedAt)
	result.LastError = lastError.String
	return result, nil
}

func (s *Store) ClaimAdminUserBulkMail(ctx context.Context, claimToken string, now time.Time, lease time.Duration) (AdminUserBulkMail, bool, error) {
	claimToken = strings.TrimSpace(claimToken)
	if len(claimToken) < 8 || len(claimToken) > 128 || lease <= 0 {
		return AdminUserBulkMail{}, false, ErrInvalidInput
	}
	now = now.UTC()
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminUserBulkMail{}, false, fmt.Errorf("begin claim administrator bulk mail: %w", err)
	}
	defer tx.Rollback()
	var result AdminUserBulkMail
	var userID sql.NullInt64
	var groupID, expiredAt sql.NullInt64
	var smtpPassword []byte
	var createdAt int64
	var targetStatus string
	err = tx.QueryRowContext(ctx, `
		SELECT t.job_id, t.sequence, t.user_id, t.email, t.uuid, t.plan_name, t.group_id, t.expired_at,
		       t.transfer_enable, t.transfer_used, t.balance, t.commission_balance, t.subscription_token,
		       t.status, t.attempt_count, j.subject, j.content, j.app_name, j.app_url, j.created_at,
		       j.smtp_host, j.smtp_port, COALESCE(j.smtp_username, ''), j.smtp_password_cipher,
		       j.smtp_encryption, j.smtp_from_address
		FROM admin_user_bulk_targets t JOIN admin_user_bulk_jobs j ON j.id = t.job_id
		WHERE j.kind = 'mail' AND j.status IN ('queued', 'running') AND (
		    (t.status = 'pending' AND t.available_at <= ?)
		    OR (t.status = 'processing' AND t.claimed_at <= ?)
		)
		ORDER BY j.created_at, t.sequence LIMIT 1
	`, now.Unix(), now.Add(-lease).Unix()).Scan(
		&result.JobID, &result.Sequence, &userID, &result.Email, &result.UUID, &result.PlanName, &groupID, &expiredAt,
		&result.TransferEnable, &result.TransferUsed, &result.Balance, &result.CommissionBalance, &result.SubscriptionToken,
		&targetStatus, &result.Attempt, &result.Subject, &result.Content, &result.AppName, &result.AppURL, &createdAt,
		&result.SMTPHost, &result.SMTPPort, &result.SMTPUsername, &smtpPassword,
		&result.SMTPEncryption, &result.SMTPFromAddress,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminUserBulkMail{}, false, nil
	}
	if err != nil {
		return AdminUserBulkMail{}, false, fmt.Errorf("select administrator bulk mail claim: %w", err)
	}
	if result.Attempt >= maxAdminUserBulkAttempts {
		updated, err := tx.ExecContext(ctx, `
			UPDATE admin_user_bulk_targets
			SET status='failed', claim_token=NULL, claimed_at=NULL,
			    last_error='mail delivery result unknown after worker restart', processed_at=?
			WHERE job_id=? AND sequence=? AND status=? AND attempt_count>=?
		`, now.Unix(), result.JobID, result.Sequence, targetStatus, maxAdminUserBulkAttempts)
		if err != nil {
			return AdminUserBulkMail{}, false, fmt.Errorf("expire exhausted administrator bulk mail claim: %w", err)
		}
		changed, _ := updated.RowsAffected()
		if changed != 1 {
			return AdminUserBulkMail{}, false, ErrConflict
		}
		if err := refreshAdminUserBulkJobTx(ctx, tx, result.JobID, now); err != nil {
			return AdminUserBulkMail{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return AdminUserBulkMail{}, false, fmt.Errorf("commit exhausted administrator bulk mail claim: %w", err)
		}
		return AdminUserBulkMail{}, false, nil
	}
	result.Attempt++
	updated, err := tx.ExecContext(ctx, `
		UPDATE admin_user_bulk_targets
		SET status='processing', attempt_count=?, claim_token=?, claimed_at=?, last_error=NULL
		WHERE job_id=? AND sequence=? AND (
		    (status='pending' AND available_at<=?) OR (status='processing' AND claimed_at<=?)
		)
	`, result.Attempt, claimToken, now.Unix(), result.JobID, result.Sequence, now.Unix(), now.Add(-lease).Unix())
	if err != nil {
		return AdminUserBulkMail{}, false, fmt.Errorf("claim administrator bulk mail target: %w", err)
	}
	changed, _ := updated.RowsAffected()
	if changed != 1 {
		return AdminUserBulkMail{}, false, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_user_bulk_jobs SET status='running', started_at=COALESCE(started_at,?), updated_at=?
		WHERE id=? AND status IN ('queued','running')
	`, now.Unix(), now.Unix(), result.JobID); err != nil {
		return AdminUserBulkMail{}, false, fmt.Errorf("start administrator bulk mail job: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AdminUserBulkMail{}, false, fmt.Errorf("commit administrator bulk mail claim: %w", err)
	}
	result.UserID = userID.Int64
	result.GroupID = nullableInt64Pointer(groupID)
	result.ExpiredAt = nullableUnixTime(expiredAt)
	result.CreatedAt = time.Unix(createdAt, 0).UTC()
	result.SMTPPasswordCipher = append([]byte(nil), smtpPassword...)
	return result, true, nil
}

// ClaimAdminUserBulkCSV leases one queued export. A non-empty jobID is used by
// the legacy synchronous compatibility endpoint; the background worker passes
// an empty ID to claim the oldest available export.
func (s *Store) ClaimAdminUserBulkCSV(ctx context.Context, jobID, claimToken string, now time.Time, lease time.Duration) (AdminUserBulkJob, bool, error) {
	claimToken = strings.TrimSpace(claimToken)
	if jobID != "" {
		if _, err := uuid.Parse(jobID); err != nil {
			return AdminUserBulkJob{}, false, ErrInvalidInput
		}
	}
	if len(claimToken) < 8 || len(claimToken) > 128 || lease <= 0 {
		return AdminUserBulkJob{}, false, ErrInvalidInput
	}
	now = now.UTC()
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminUserBulkJob{}, false, fmt.Errorf("begin claim administrator bulk CSV: %w", err)
	}
	defer tx.Rollback()
	query := `
		SELECT id FROM admin_user_bulk_jobs
		WHERE kind='csv' AND (
		    (status='queued' AND claim_token IS NULL)
		    OR (status='running' AND claimed_at<=?)
		)`
	arguments := []any{now.Add(-lease).Unix()}
	if jobID != "" {
		query += ` AND id=?`
		arguments = append(arguments, jobID)
	}
	query += ` ORDER BY created_at,id LIMIT 1`
	var claimedID string
	if err := tx.QueryRowContext(ctx, query, arguments...).Scan(&claimedID); errors.Is(err, sql.ErrNoRows) {
		return AdminUserBulkJob{}, false, nil
	} else if err != nil {
		return AdminUserBulkJob{}, false, fmt.Errorf("select administrator bulk CSV claim: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE admin_user_bulk_jobs
		SET status='running', claim_token=?, claimed_at=?, started_at=COALESCE(started_at,?), updated_at=?
		WHERE id=? AND kind='csv' AND (
		    (status='queued' AND claim_token IS NULL)
		    OR (status='running' AND claimed_at<=?)
		)
	`, claimToken, now.Unix(), now.Unix(), now.Unix(), claimedID, now.Add(-lease).Unix())
	if err != nil {
		return AdminUserBulkJob{}, false, fmt.Errorf("claim administrator bulk CSV: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return AdminUserBulkJob{}, false, ErrConflict
	}
	job, err := getAdminUserBulkJobTx(ctx, tx, claimedID)
	if err != nil {
		return AdminUserBulkJob{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return AdminUserBulkJob{}, false, fmt.Errorf("commit administrator bulk CSV claim: %w", err)
	}
	return job, true, nil
}

func (s *Store) RefreshAdminUserBulkCSVClaim(ctx context.Context, jobID, claimToken string, now time.Time) (bool, error) {
	if _, err := uuid.Parse(jobID); err != nil || len(strings.TrimSpace(claimToken)) < 8 {
		return false, ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE admin_user_bulk_jobs SET claimed_at=?, updated_at=?
		WHERE id=? AND kind='csv' AND status='running' AND claim_token=?
	`, now.Unix(), now.Unix(), jobID, claimToken)
	if err != nil {
		return false, fmt.Errorf("refresh administrator bulk CSV claim: %w", err)
	}
	changed, _ := result.RowsAffected()
	return changed == 1, nil
}

func (s *Store) CompleteAdminUserBulkCSV(ctx context.Context, jobID, claimToken, filename, relativePath string, size int64, digest string, expiresAt, now time.Time) error {
	filename = strings.TrimSpace(filename)
	relativePath = strings.TrimSpace(relativePath)
	digest = strings.ToLower(strings.TrimSpace(digest))
	if _, err := uuid.Parse(jobID); err != nil || len(strings.TrimSpace(claimToken)) < 8 ||
		filename == "" || len(filename) > 255 || !safeAdminUserBulkRelativePath(relativePath) ||
		size < 0 || size > 32<<20 || len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" ||
		!expiresAt.After(now) {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin complete administrator bulk CSV: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE admin_user_bulk_jobs
		SET status='succeeded', processed_count=total_count, success_count=total_count,
		    failure_count=0, skipped_count=0, cancelled_count=0,
		    output_filename=?, output_relative_path=?, output_size=?, output_sha256=?, output_expires_at=?,
		    claim_token=NULL, claimed_at=NULL, completed_at=?, updated_at=?
		WHERE id=? AND kind='csv' AND status='running' AND claim_token=?
	`, filename, relativePath, size, digest, expiresAt.Unix(), now.Unix(), now.Unix(), jobID, claimToken)
	if err != nil {
		return fmt.Errorf("complete administrator bulk CSV: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_user_bulk_targets SET status='succeeded', processed_at=?
		WHERE job_id=? AND status='pending'
	`, now.Unix(), jobID); err != nil {
		return fmt.Errorf("complete administrator bulk CSV targets: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit administrator bulk CSV completion: %w", err)
	}
	return nil
}

func (s *Store) FailAdminUserBulkCSV(ctx context.Context, jobID, claimToken, failure string, now time.Time) error {
	if _, err := uuid.Parse(jobID); err != nil || len(strings.TrimSpace(claimToken)) < 8 {
		return ErrInvalidInput
	}
	failure = truncateUTF8Bytes(strings.TrimSpace(failure), maxAdminUserBulkErrorByte)
	if failure == "" {
		failure = "CSV export failed"
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin fail administrator bulk CSV: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE admin_user_bulk_jobs
		SET status='failed', processed_count=total_count, success_count=0,
		    failure_count=total_count, skipped_count=0, cancelled_count=0,
		    claim_token=NULL, claimed_at=NULL, last_error=?, completed_at=?, updated_at=?
		WHERE id=? AND kind='csv' AND status='running' AND claim_token=?
	`, failure, now.Unix(), now.Unix(), jobID, claimToken)
	if err != nil {
		return fmt.Errorf("fail administrator bulk CSV: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_user_bulk_targets SET status='failed', last_error=?, processed_at=?
		WHERE job_id=? AND status='pending'
	`, failure, now.Unix(), jobID); err != nil {
		return fmt.Errorf("fail administrator bulk CSV targets: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit administrator bulk CSV failure: %w", err)
	}
	return nil
}

func safeAdminUserBulkRelativePath(value string) bool {
	return value != "" && len(value) <= 255 && !strings.HasPrefix(value, "/") &&
		!strings.Contains(value, `\`) && !strings.Contains(value, "..")
}

func (s *Store) ListExpiredAdminUserBulkOutputs(ctx context.Context, now time.Time, limit int) ([]AdminUserBulkExpiredOutput, error) {
	if limit < 1 || limit > 500 {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, output_relative_path FROM admin_user_bulk_jobs
		WHERE kind='csv' AND output_relative_path IS NOT NULL AND output_expires_at<=?
		ORDER BY output_expires_at,id LIMIT ?
	`, now.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("list expired administrator bulk outputs: %w", err)
	}
	defer rows.Close()
	result := make([]AdminUserBulkExpiredOutput, 0, limit)
	for rows.Next() {
		var output AdminUserBulkExpiredOutput
		if err := rows.Scan(&output.JobID, &output.RelativePath); err != nil {
			return nil, fmt.Errorf("scan expired administrator bulk output: %w", err)
		}
		result = append(result, output)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired administrator bulk outputs: %w", err)
	}
	return result, nil
}

func (s *Store) ClearExpiredAdminUserBulkOutput(ctx context.Context, jobID, relativePath string, now time.Time) (bool, error) {
	if _, err := uuid.Parse(jobID); err != nil || !safeAdminUserBulkRelativePath(relativePath) {
		return false, ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE admin_user_bulk_jobs
		SET output_relative_path=NULL, output_size=NULL, output_sha256=NULL, updated_at=?
		WHERE id=? AND kind='csv' AND output_relative_path=? AND output_expires_at<=?
	`, now.Unix(), jobID, relativePath, now.Unix())
	if err != nil {
		return false, fmt.Errorf("clear expired administrator bulk output: %w", err)
	}
	changed, _ := result.RowsAffected()
	return changed == 1, nil
}

func (s *Store) CompleteAdminUserBulkMail(ctx context.Context, jobID string, sequence int64, claimToken string, now time.Time) error {
	return s.finishAdminUserBulkMail(ctx, jobID, sequence, claimToken, "", now, now, true)
}

func (s *Store) FailAdminUserBulkMail(ctx context.Context, jobID string, sequence int64, claimToken, failure string, retryAt, now time.Time) error {
	if retryAt.Before(now) {
		return ErrInvalidInput
	}
	return s.finishAdminUserBulkMail(ctx, jobID, sequence, claimToken, failure, retryAt, now, false)
}

func (s *Store) finishAdminUserBulkMail(ctx context.Context, jobID string, sequence int64, claimToken, failure string, retryAt, now time.Time, succeeded bool) error {
	if _, err := uuid.Parse(jobID); err != nil || sequence < 1 || len(strings.TrimSpace(claimToken)) < 8 {
		return ErrInvalidInput
	}
	failure = truncateUTF8Bytes(strings.TrimSpace(failure), maxAdminUserBulkErrorByte)
	now = now.UTC()
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin finish administrator bulk mail: %w", err)
	}
	defer tx.Rollback()
	var attempt int
	var jobStatus string
	if err := tx.QueryRowContext(ctx, `
		SELECT t.attempt_count, j.status FROM admin_user_bulk_targets t
		JOIN admin_user_bulk_jobs j ON j.id=t.job_id
		WHERE t.job_id=? AND t.sequence=? AND t.status='processing' AND t.claim_token=?
	`, jobID, sequence, claimToken).Scan(&attempt, &jobStatus); errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	} else if err != nil {
		return fmt.Errorf("read administrator bulk mail completion: %w", err)
	}
	status := AdminUserBulkTargetSucceeded
	processedAt := any(now.Unix())
	availableAt := now.Unix()
	if !succeeded {
		if failure == "" {
			failure = "mail delivery failed"
		}
		if jobStatus == AdminUserBulkStatusCancelling {
			status = AdminUserBulkTargetCancelled
		} else if attempt < maxAdminUserBulkAttempts {
			status = AdminUserBulkTargetPending
			processedAt = nil
			availableAt = retryAt.Unix()
		} else {
			status = AdminUserBulkTargetFailed
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_user_bulk_targets
		SET status=?, available_at=?, claim_token=NULL, claimed_at=NULL, last_error=?, processed_at=?
		WHERE job_id=? AND sequence=? AND status='processing' AND claim_token=?
	`, status, availableAt, nullableText(failure), processedAt, jobID, sequence, claimToken); err != nil {
		return fmt.Errorf("finish administrator bulk mail target: %w", err)
	}
	if err := refreshAdminUserBulkJobTx(ctx, tx, jobID, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit administrator bulk mail completion: %w", err)
	}
	return nil
}

func (s *Store) CancelAdminUserBulkJob(ctx context.Context, jobID string, administratorID int64, now time.Time) (AdminUserBulkJob, error) {
	if _, err := uuid.Parse(jobID); err != nil || administratorID < 1 {
		return AdminUserBulkJob{}, ErrInvalidInput
	}
	now = now.UTC()
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("begin cancel administrator user bulk job: %w", err)
	}
	defer tx.Rollback()
	var isAdministrator bool
	if err := tx.QueryRowContext(ctx, `SELECT is_admin AND account_kind='human' AND banned=0 FROM users WHERE id=?`, administratorID).Scan(&isAdministrator); err != nil || !isAdministrator {
		if errors.Is(err, sql.ErrNoRows) || !isAdministrator {
			return AdminUserBulkJob{}, ErrNotFound
		}
		return AdminUserBulkJob{}, err
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM admin_user_bulk_jobs WHERE id=?`, jobID).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return AdminUserBulkJob{}, ErrNotFound
	} else if err != nil {
		return AdminUserBulkJob{}, err
	}
	if status == AdminUserBulkStatusSucceeded || status == AdminUserBulkStatusFailed || status == AdminUserBulkStatusCancelled {
		job, err := getAdminUserBulkJobTx(ctx, tx, jobID)
		if err == nil {
			err = tx.Commit()
		}
		return job, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_user_bulk_targets SET status='cancelled', processed_at=?, claim_token=NULL, claimed_at=NULL
		WHERE job_id=? AND status='pending'
	`, now.Unix(), jobID); err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("cancel administrator user bulk pending targets: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_user_bulk_jobs SET status='cancelling', cancelled_at=COALESCE(cancelled_at,?), updated_at=? WHERE id=?
	`, now.Unix(), now.Unix(), jobID); err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("cancel administrator user bulk job: %w", err)
	}
	if err := refreshAdminUserBulkJobTx(ctx, tx, jobID, now); err != nil {
		return AdminUserBulkJob{}, err
	}
	job, err := getAdminUserBulkJobTx(ctx, tx, jobID)
	if err != nil {
		return AdminUserBulkJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("commit cancel administrator user bulk job: %w", err)
	}
	return job, nil
}

func refreshAdminUserBulkJobTx(ctx context.Context, tx *sql.Tx, jobID string, now time.Time) error {
	var pending, processing, succeeded, failed, skipped, cancelled int
	if err := tx.QueryRowContext(ctx, `
		SELECT
		  SUM(status='pending'), SUM(status='processing'), SUM(status='succeeded'),
		  SUM(status='failed'), SUM(status='skipped'), SUM(status='cancelled')
		FROM admin_user_bulk_targets WHERE job_id=?
	`, jobID).Scan(&pending, &processing, &succeeded, &failed, &skipped, &cancelled); err != nil {
		return fmt.Errorf("summarize administrator user bulk job: %w", err)
	}
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM admin_user_bulk_jobs WHERE id=?`, jobID).Scan(&current); err != nil {
		return err
	}
	processed := succeeded + failed + skipped + cancelled
	status := current
	var completedAt any
	var lastError sql.NullString
	if failed > 0 {
		if err := tx.QueryRowContext(ctx, `
			SELECT last_error FROM admin_user_bulk_targets
			WHERE job_id=? AND status='failed' AND last_error IS NOT NULL
			ORDER BY sequence LIMIT 1
		`, jobID).Scan(&lastError); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read administrator user bulk job failure: %w", err)
		}
	}
	if pending == 0 && processing == 0 {
		completedAt = now.Unix()
		if current == AdminUserBulkStatusCancelling || cancelled > 0 {
			status = AdminUserBulkStatusCancelled
		} else if failed > 0 {
			status = AdminUserBulkStatusFailed
		} else {
			status = AdminUserBulkStatusSucceeded
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_user_bulk_jobs
		SET status=?, processed_count=?, success_count=?, failure_count=?, skipped_count=?, cancelled_count=?,
		    last_error=?, completed_at=COALESCE(?, completed_at), updated_at=? WHERE id=?
	`, status, processed, succeeded, failed, skipped, cancelled, nullableText(lastError.String), completedAt, now.Unix(), jobID); err != nil {
		return fmt.Errorf("refresh administrator user bulk job: %w", err)
	}
	return nil
}

func (s *Store) BanAdminUsers(ctx context.Context, input BanAdminUsersInput, now time.Time) (AdminUserBulkJob, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.AdministratorID < 1 || !validAdminUserBulkIdempotencyKey(input.IdempotencyKey) {
		return AdminUserBulkJob{}, ErrInvalidInput
	}
	scope, where, arguments, scopeDigest, err := normalizeAdminUserBulkScope(input.Scope)
	if err != nil {
		return AdminUserBulkJob{}, err
	}
	requestHash := sha256.Sum256([]byte(AdminUserBulkKindBan + "\x00" + scopeDigest))
	requestDigest := hex.EncodeToString(requestHash[:])
	now = now.UTC()
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("begin administrator user bulk ban: %w", err)
	}
	defer tx.Rollback()
	var existingID, existingDigest string
	err = tx.QueryRowContext(ctx, `
		SELECT id, request_digest FROM admin_user_bulk_jobs
		WHERE kind='ban' AND administrator_id=? AND idempotency_key=?
	`, input.AdministratorID, input.IdempotencyKey).Scan(&existingID, &existingDigest)
	if err == nil {
		if existingDigest != requestDigest {
			return AdminUserBulkJob{}, fmt.Errorf("%w: administrator user bulk idempotency key was reused", ErrConflict)
		}
		job, readErr := getAdminUserBulkJobTx(ctx, tx, existingID)
		if readErr != nil {
			return AdminUserBulkJob{}, readErr
		}
		if err := tx.Commit(); err != nil {
			return AdminUserBulkJob{}, err
		}
		return job, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AdminUserBulkJob{}, err
	}
	job, err := createAdminUserBulkJobTx(ctx, tx, CreateAdminUserBulkJobInput{
		Kind: AdminUserBulkKindBan, AdministratorID: input.AdministratorID, Scope: scope,
	}, scope, where, arguments, requestDigest, now)
	if err != nil {
		return AdminUserBulkJob{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_user_bulk_jobs SET idempotency_key=?, status='running', started_at=?, updated_at=? WHERE id=?
	`, input.IdempotencyKey, now.Unix(), now.Unix(), job.ID); err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("start administrator user bulk ban: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_user_bulk_targets
		SET status='skipped', last_error=CASE WHEN user_id=? THEN 'current administrator is protected' ELSE 'user is already banned' END,
		    processed_at=?
		WHERE job_id=? AND (user_id=? OR EXISTS(SELECT 1 FROM users u WHERE u.id=user_id AND u.banned=1))
	`, input.AdministratorID, now.Unix(), job.ID, input.AdministratorID); err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("mark administrator user bulk ban skips: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_user_bulk_targets SET status='succeeded', processed_at=? WHERE job_id=? AND status='pending'
	`, now.Unix(), job.ID); err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("mark administrator user bulk ban targets: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE users SET banned=1, online_count=0, admin_revision=admin_revision+1, updated_at=?
		WHERE id IN (SELECT user_id FROM admin_user_bulk_targets WHERE job_id=? AND status='succeeded')
		  AND account_kind='human' AND banned=0
	`, now.Unix(), job.ID); err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("ban administrator user bulk targets: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE subscription_reminder_outbox
		SET cancelled_at=?, last_error='cancelled because user was banned', updated_at=?
		WHERE user_id IN (SELECT user_id FROM admin_user_bulk_targets WHERE job_id=? AND status='succeeded')
		  AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL AND claim_token IS NULL
	`, now.Unix(), now.Unix(), job.ID); err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("cancel administrator user bulk reminder mail: %w", err)
	}
	for _, statement := range []string{
		`UPDATE admin_sessions SET revoked_at=? WHERE revoked_at IS NULL AND user_id IN (SELECT user_id FROM admin_user_bulk_targets WHERE job_id=? AND status='succeeded')`,
		`UPDATE access_tokens SET revoked_at=? WHERE revoked_at IS NULL AND user_id IN (SELECT user_id FROM admin_user_bulk_targets WHERE job_id=? AND status='succeeded')`,
		`DELETE FROM node_device_ips WHERE user_id IN (SELECT user_id FROM admin_user_bulk_targets WHERE job_id=? AND status='succeeded')`,
		`DELETE FROM node_user_online WHERE user_id IN (SELECT user_id FROM admin_user_bulk_targets WHERE job_id=? AND status='succeeded')`,
	} {
		var execErr error
		if strings.HasPrefix(statement, "UPDATE") {
			_, execErr = tx.ExecContext(ctx, statement, now.Unix(), job.ID)
		} else {
			_, execErr = tx.ExecContext(ctx, statement, job.ID)
		}
		if execErr != nil {
			return AdminUserBulkJob{}, fmt.Errorf("clear administrator user bulk ban access: %w", execErr)
		}
	}
	if err := refreshAdminUserBulkJobTx(ctx, tx, job.ID, now); err != nil {
		return AdminUserBulkJob{}, err
	}
	job, err = getAdminUserBulkJobTx(ctx, tx, job.ID)
	if err != nil {
		return AdminUserBulkJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("commit administrator user bulk ban: %w", err)
	}
	return job, nil
}

// MarkAdminUserBulkRuntimeWarning records the bounded, user-visible warning for
// the only non-transactional part of a bulk ban. The durable ban remains
// successful; disconnected nodes converge on their next full pull.
func (s *Store) MarkAdminUserBulkRuntimeWarning(ctx context.Context, jobID string, now time.Time) (AdminUserBulkJob, error) {
	if _, err := uuid.Parse(jobID); err != nil {
		return AdminUserBulkJob{}, ErrInvalidInput
	}
	now = now.UTC()
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("begin administrator user bulk runtime warning: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE admin_user_bulk_jobs
		SET last_error='node runtime notification failed; state will reconcile on the next full pull', updated_at=?
		WHERE id=? AND kind='ban' AND status='succeeded'
	`, now.Unix(), jobID)
	if err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("record administrator user bulk runtime warning: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("read administrator user bulk runtime warning result: %w", err)
	}
	if changed == 0 {
		var kind, status string
		if err := tx.QueryRowContext(ctx, `SELECT kind,status FROM admin_user_bulk_jobs WHERE id=?`, jobID).Scan(&kind, &status); errors.Is(err, sql.ErrNoRows) {
			return AdminUserBulkJob{}, ErrNotFound
		} else if err != nil {
			return AdminUserBulkJob{}, fmt.Errorf("read administrator user bulk runtime warning target: %w", err)
		}
		return AdminUserBulkJob{}, ErrConflict
	}
	job, err := getAdminUserBulkJobTx(ctx, tx, jobID)
	if err != nil {
		return AdminUserBulkJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminUserBulkJob{}, fmt.Errorf("commit administrator user bulk runtime warning: %w", err)
	}
	return job, nil
}

func validAdminUserBulkIdempotencyKey(value string) bool {
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("._:-", character) {
			continue
		}
		return false
	}
	return true
}

func getAdminUserBulkJobTx(ctx context.Context, tx *sql.Tx, jobID string) (AdminUserBulkJob, error) {
	job, err := scanAdminUserBulkJob(tx.QueryRowContext(ctx, adminUserBulkJobSelect+` WHERE id=?`, jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return AdminUserBulkJob{}, ErrNotFound
	}
	return job, err
}

func truncateUTF8Bytes(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
