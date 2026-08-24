package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	passwordResetCodeTTL       = 5 * time.Minute
	passwordResetSendCooldown  = time.Minute
	passwordResetFailureWindow = 5 * time.Minute
	maxPasswordResetCipher     = 512
)

// RequestPasswordReset creates the same persistent cooldown record for known
// and unknown email addresses. Only a human account receives an outbox job.
func (s *Store) RequestPasswordReset(ctx context.Context, input PasswordResetRequestInput, now time.Time) (bool, error) {
	input.Email = normalizeEmail(input.Email)
	if input.Email == "" || len(input.Email) > 320 || len(input.EmailDigest) != 32 || len(input.CodeDigest) != 32 ||
		len(input.CodeCipher) < 32 || len(input.CodeCipher) > maxPasswordResetCipher || now.IsZero() {
		return false, fmt.Errorf("%w: invalid password reset request", ErrInvalidInput)
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin password reset request: %w", err)
	}
	defer tx.Rollback()

	var smtpEnabled bool
	var appName, appURL string
	if err := tx.QueryRowContext(ctx, `SELECT smtp_enabled, app_name, app_url FROM app_settings WHERE id = 1`).Scan(&smtpEnabled, &appName, &appURL); err != nil {
		return false, fmt.Errorf("read password reset mail settings: %w", err)
	}
	if !smtpEnabled {
		return false, ErrMailUnavailable
	}
	var resendAfter int64
	err = tx.QueryRowContext(ctx, `SELECT resend_after FROM password_reset_challenges WHERE email_digest = ?`, input.EmailDigest).Scan(&resendAfter)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("read password reset cooldown: %w", err)
	}
	if err == nil && resendAfter > now.Unix() {
		return false, &PasswordResetLimitError{RetryAfterSeconds: resendAfter - now.Unix()}
	}

	var userID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM users WHERE email = ? COLLATE NOCASE AND account_kind = ?
	`, input.Email, AccountKindHuman).Scan(&userID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("find password reset account: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO password_reset_challenges (
			email_digest, user_id, code_digest, expires_at, resend_after,
			failed_attempts, failure_reset_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 0, NULL, ?, ?)
		ON CONFLICT(email_digest) DO UPDATE SET
			user_id = excluded.user_id,
			code_digest = excluded.code_digest,
			expires_at = excluded.expires_at,
			resend_after = excluded.resend_after,
			failed_attempts = CASE
				WHEN password_reset_challenges.failure_reset_at IS NULL OR password_reset_challenges.failure_reset_at <= excluded.updated_at THEN 0
				ELSE password_reset_challenges.failed_attempts
			END,
			failure_reset_at = CASE
				WHEN password_reset_challenges.failure_reset_at IS NULL OR password_reset_challenges.failure_reset_at <= excluded.updated_at THEN NULL
				ELSE password_reset_challenges.failure_reset_at
			END,
			updated_at = excluded.updated_at
	`, input.EmailDigest, nullableInt64(userID), input.CodeDigest, now.Add(passwordResetCodeTTL).Unix(),
		now.Add(passwordResetSendCooldown).Unix(), now.Unix(), now.Unix()); err != nil {
		return false, fmt.Errorf("save password reset challenge: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE password_reset_mail_outbox
		SET cancelled_at = ?, code_cipher = NULL, claim_token = NULL, claimed_at = NULL,
			last_error = 'superseded by a newer password reset code', updated_at = ?
		WHERE email_digest = ? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
	`, now.Unix(), now.Unix(), input.EmailDigest); err != nil {
		return false, fmt.Errorf("cancel superseded password reset mail: %w", err)
	}
	queued := userID.Valid
	if queued {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO password_reset_mail_outbox (
				email_digest, recipient, code_cipher, app_name, app_url,
				available_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, input.EmailDigest, input.Email, input.CodeCipher, appName, appURL, now.Unix(), now.Unix(), now.Unix()); err != nil {
			return false, fmt.Errorf("enqueue password reset mail: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit password reset request: %w", err)
	}
	return queued, nil
}

// CheckPasswordResetChallenge performs the cheap verifier check before
// Argon2id. Invalid attempts are persisted so process restarts cannot reset the
// three-attempt lockout.
func (s *Store) CheckPasswordResetChallenge(ctx context.Context, emailDigest, codeDigest []byte, now time.Time) (PasswordResetChallenge, error) {
	if len(emailDigest) != 32 || len(codeDigest) != 32 || now.IsZero() {
		return PasswordResetChallenge{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PasswordResetChallenge{}, fmt.Errorf("begin password reset check: %w", err)
	}
	defer tx.Rollback()

	challenge, storedDigest, expiresAt, failedAttempts, failureResetAt, err := readPasswordResetChallenge(ctx, tx, emailDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return PasswordResetChallenge{}, ErrPasswordResetInvalid
	}
	if err != nil {
		return PasswordResetChallenge{}, fmt.Errorf("read password reset challenge: %w", err)
	}
	if failureResetAt.Valid && failureResetAt.Int64 <= now.Unix() {
		failedAttempts = 0
		failureResetAt.Valid = false
	}
	if failedAttempts >= 3 && failureResetAt.Valid && failureResetAt.Int64 > now.Unix() {
		return PasswordResetChallenge{}, &PasswordResetLockedError{RetryAfterSeconds: failureResetAt.Int64 - now.Unix()}
	}
	if expiresAt <= now.Unix() {
		return PasswordResetChallenge{}, ErrPasswordResetInvalid
	}
	if subtle.ConstantTimeCompare(storedDigest, codeDigest) != 1 {
		resetAt := failureResetAt.Int64
		if !failureResetAt.Valid {
			resetAt = now.Add(passwordResetFailureWindow).Unix()
		}
		nextAttempts := failedAttempts + 1
		if nextAttempts > 3 {
			nextAttempts = 3
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE password_reset_challenges
			SET failed_attempts = ?, failure_reset_at = ?, updated_at = ?
			WHERE email_digest = ?
		`, nextAttempts, resetAt, now.Unix(), emailDigest); err != nil {
			return PasswordResetChallenge{}, fmt.Errorf("record password reset failure: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return PasswordResetChallenge{}, fmt.Errorf("commit password reset failure: %w", err)
		}
		return PasswordResetChallenge{}, ErrPasswordResetInvalid
	}
	if challenge.UserID < 1 || challenge.PasswordHash == "" {
		return PasswordResetChallenge{}, ErrPasswordResetInvalid
	}
	if err := tx.Commit(); err != nil {
		return PasswordResetChallenge{}, fmt.Errorf("commit password reset check: %w", err)
	}
	return challenge, nil
}

func (s *Store) ResetPasswordWithChallenge(ctx context.Context, emailDigest, codeDigest []byte, challenge PasswordResetChallenge, newHash string, now time.Time) error {
	if len(emailDigest) != 32 || len(codeDigest) != 32 || challenge.UserID < 1 || challenge.PasswordHash == "" || newHash == "" || now.IsZero() {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password reset: %w", err)
	}
	defer tx.Rollback()

	current, storedDigest, expiresAt, failedAttempts, failureResetAt, err := readPasswordResetChallenge(ctx, tx, emailDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPasswordResetInvalid
	}
	if err != nil {
		return fmt.Errorf("recheck password reset challenge: %w", err)
	}
	if failedAttempts >= 3 && failureResetAt.Valid && failureResetAt.Int64 > now.Unix() {
		return &PasswordResetLockedError{RetryAfterSeconds: failureResetAt.Int64 - now.Unix()}
	}
	if expiresAt <= now.Unix() || current.UserID != challenge.UserID || current.PasswordHash != challenge.PasswordHash ||
		subtle.ConstantTimeCompare(storedDigest, codeDigest) != 1 {
		return ErrPasswordResetInvalid
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE users SET password_hash = ?, updated_at = ?
		WHERE id = ? AND password_hash = ? AND account_kind = ?
	`, newHash, now.Unix(), challenge.UserID, challenge.PasswordHash, AccountKindHuman)
	if err != nil {
		return fmt.Errorf("update password from reset challenge: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count password reset update: %w", err)
	}
	if changed != 1 {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL
	`, now.Unix(), challenge.UserID); err != nil {
		return fmt.Errorf("revoke sessions after password reset: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM password_reset_challenges WHERE email_digest = ?`, emailDigest); err != nil {
		return fmt.Errorf("consume password reset challenge: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE password_reset_mail_outbox
		SET cancelled_at = COALESCE(cancelled_at, ?), code_cipher = NULL,
			claim_token = NULL, claimed_at = NULL, updated_at = ?
		WHERE email_digest = ? AND sent_at IS NULL AND failed_at IS NULL
	`, now.Unix(), now.Unix(), emailDigest); err != nil {
		return fmt.Errorf("cancel consumed password reset mail: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password reset: %w", err)
	}
	return nil
}

func (s *Store) ClaimPasswordResetMail(ctx context.Context, claimToken string, now time.Time, lease time.Duration) (PasswordResetMailJob, bool, error) {
	claimToken = strings.TrimSpace(claimToken)
	if claimToken == "" || len(claimToken) > maxTicketMailClaimToken || lease < time.Second || lease > time.Hour {
		return PasswordResetMailJob{}, false, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PasswordResetMailJob{}, false, fmt.Errorf("begin claim password reset mail: %w", err)
	}
	defer tx.Rollback()
	var id int64
	staleBefore := now.Add(-lease).Unix()
	err = tx.QueryRowContext(ctx, `
		SELECT o.id FROM password_reset_mail_outbox o CROSS JOIN app_settings s
		WHERE s.id = 1 AND s.smtp_enabled = 1 AND o.code_cipher IS NOT NULL
		  AND o.sent_at IS NULL AND o.failed_at IS NULL AND o.cancelled_at IS NULL AND o.attempt_count < 3
		  AND EXISTS (
			SELECT 1 FROM password_reset_challenges c
			WHERE c.email_digest = o.email_digest AND c.expires_at > ?
		  )
		  AND o.available_at <= ? AND (o.claimed_at IS NULL OR o.claimed_at <= ?)
		ORDER BY o.available_at, o.id LIMIT 1
	`, now.Add(30*time.Second).Unix(), now.Unix(), staleBefore).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return PasswordResetMailJob{}, false, nil
	}
	if err != nil {
		return PasswordResetMailJob{}, false, fmt.Errorf("select password reset mail: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE password_reset_mail_outbox
		SET claim_token = ?, claimed_at = ?, attempt_count = attempt_count + 1, updated_at = ?
		WHERE id = ? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL AND attempt_count < 3
		  AND available_at <= ? AND (claimed_at IS NULL OR claimed_at <= ?)
		  AND EXISTS (
			SELECT 1 FROM password_reset_challenges c
			WHERE c.email_digest = password_reset_mail_outbox.email_digest AND c.expires_at > ?
		  )
	`, claimToken, now.Unix(), now.Unix(), id, now.Unix(), staleBefore, now.Add(30*time.Second).Unix())
	if err != nil {
		return PasswordResetMailJob{}, false, fmt.Errorf("claim password reset mail: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return PasswordResetMailJob{}, false, nil
	}
	job, err := scanPasswordResetMailJob(tx.QueryRowContext(ctx, `
		SELECT o.id, o.attempt_count, o.email_digest, o.recipient, o.code_cipher, o.app_name, o.app_url,
		       s.smtp_host, s.smtp_port, s.smtp_username, s.smtp_password_cipher, s.smtp_encryption, s.smtp_from_address
		FROM password_reset_mail_outbox o CROSS JOIN app_settings s
		WHERE o.id = ? AND o.claim_token = ? AND s.id = 1
	`, id, claimToken))
	if err != nil {
		return PasswordResetMailJob{}, false, fmt.Errorf("read claimed password reset mail: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PasswordResetMailJob{}, false, fmt.Errorf("commit password reset mail claim: %w", err)
	}
	return job, true, nil
}

func (s *Store) CompletePasswordResetMail(ctx context.Context, jobID int64, claimToken string, now time.Time) error {
	if jobID < 1 || strings.TrimSpace(claimToken) == "" {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE password_reset_mail_outbox
		SET sent_at = ?, code_cipher = NULL, claim_token = NULL, claimed_at = NULL, last_error = NULL, updated_at = ?
		WHERE id = ? AND claim_token = ? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
	`, now.Unix(), now.Unix(), jobID, claimToken)
	if err != nil {
		return fmt.Errorf("complete password reset mail: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) FailPasswordResetMail(ctx context.Context, jobID int64, claimToken, failure string, retryAt, now time.Time) error {
	if jobID < 1 || strings.TrimSpace(claimToken) == "" || retryAt.Before(now) {
		return ErrInvalidInput
	}
	failure = truncateRunes(strings.TrimSpace(failure), maxTicketMailErrorRunes)
	if failure == "" {
		failure = "mail delivery failed"
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE password_reset_mail_outbox
		SET available_at = CASE WHEN attempt_count < ? THEN ? ELSE available_at END,
		    failed_at = CASE WHEN attempt_count >= ? OR NOT (SELECT smtp_enabled FROM app_settings WHERE id = 1) THEN ? ELSE NULL END,
		    code_cipher = CASE WHEN attempt_count >= ? OR NOT (SELECT smtp_enabled FROM app_settings WHERE id = 1) THEN NULL ELSE code_cipher END,
		    claim_token = NULL, claimed_at = NULL, last_error = ?, updated_at = ?
		WHERE id = ? AND claim_token = ? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
	`, maxTicketMailAttempts, retryAt.Unix(), maxTicketMailAttempts, now.Unix(), maxTicketMailAttempts,
		failure, now.Unix(), jobID, claimToken)
	if err != nil {
		return fmt.Errorf("fail password reset mail: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) PruneExpiredPasswordResets(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit < 1 || limit > 1_000 {
		return 0, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin prune password resets: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE password_reset_mail_outbox
		SET cancelled_at = ?, code_cipher = NULL, claim_token = NULL, claimed_at = NULL,
			last_error = 'password reset code expired', updated_at = ?
		WHERE sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL AND email_digest IN (
			SELECT email_digest FROM password_reset_challenges WHERE expires_at <= ? ORDER BY expires_at LIMIT ?
		)
	`, now.Unix(), now.Unix(), now.Unix(), limit); err != nil {
		return 0, fmt.Errorf("cancel expired password reset mail: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM password_reset_challenges WHERE email_digest IN (
			SELECT email_digest FROM password_reset_challenges WHERE expires_at <= ? ORDER BY expires_at LIMIT ?
		)
	`, now.Unix(), limit)
	if err != nil {
		return 0, fmt.Errorf("delete expired password reset challenges: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count expired password reset challenges: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit password reset pruning: %w", err)
	}
	return removed, nil
}

type passwordResetChallengeRow interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readPasswordResetChallenge(ctx context.Context, query passwordResetChallengeRow, emailDigest []byte) (PasswordResetChallenge, []byte, int64, int, sql.NullInt64, error) {
	var challenge PasswordResetChallenge
	var storedDigest []byte
	var expiresAt int64
	var failedAttempts int
	var failureResetAt sql.NullInt64
	var userID sql.NullInt64
	var passwordHash sql.NullString
	err := query.QueryRowContext(ctx, `
		SELECT c.user_id, c.code_digest, c.expires_at, c.failed_attempts, c.failure_reset_at, u.password_hash
		FROM password_reset_challenges c
		LEFT JOIN users u ON u.id = c.user_id AND u.account_kind = ?
		WHERE c.email_digest = ?
	`, AccountKindHuman, emailDigest).Scan(&userID, &storedDigest, &expiresAt, &failedAttempts, &failureResetAt, &passwordHash)
	if userID.Valid {
		challenge.UserID = userID.Int64
	}
	if passwordHash.Valid {
		challenge.PasswordHash = passwordHash.String
	}
	return challenge, storedDigest, expiresAt, failedAttempts, failureResetAt, err
}

func scanPasswordResetMailJob(row rowScanner) (PasswordResetMailJob, error) {
	var job PasswordResetMailJob
	if err := row.Scan(&job.ID, &job.Attempt, &job.EmailDigest, &job.Recipient, &job.CodeCipher,
		&job.AppName, &job.AppURL, &job.SMTPHost, &job.SMTPPort, &job.SMTPUsername,
		&job.SMTPPasswordCipher, &job.SMTPEncryption, &job.SMTPFromAddress); err != nil {
		return PasswordResetMailJob{}, err
	}
	job.EmailDigest = append([]byte(nil), job.EmailDigest...)
	job.CodeCipher = append([]byte(nil), job.CodeCipher...)
	job.SMTPPasswordCipher = append([]byte(nil), job.SMTPPasswordCipher...)
	return job, nil
}

func nullableInt64(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}
