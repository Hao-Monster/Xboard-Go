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
	registrationEmailCodeTTL       = 5 * time.Minute
	registrationEmailSendCooldown  = time.Minute
	registrationEmailFailureWindow = 5 * time.Minute
	maxRegistrationEmailCipher     = 512
)

// RequestRegistrationEmailVerification persists the same challenge and
// cooldown for existing and available addresses. Only an address that can
// actually register receives mail, so the public response cannot enumerate
// accounts while internal accounts are never exposed through SMTP.
func (s *Store) RequestRegistrationEmailVerification(ctx context.Context, input RegistrationEmailVerificationRequestInput, now time.Time) (bool, error) {
	input.Email = normalizeEmail(input.Email)
	if input.Email == "" || len(input.Email) > 320 || !validRegistrationSourceIP(input.SourceIP) ||
		len(input.EmailDigest) != 32 || len(input.CodeDigest) != 32 ||
		len(input.CodeCipher) < 32 || len(input.CodeCipher) > maxRegistrationEmailCipher || now.IsZero() {
		return false, fmt.Errorf("%w: invalid registration email verification request", ErrInvalidInput)
	}

	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin registration email verification request: %w", err)
	}
	defer tx.Rollback()

	policy, err := readRegistrationPolicy(ctx, tx)
	if err != nil {
		return false, fmt.Errorf("read registration policy: %w", err)
	}
	if !policy.emailVerificationEnabled {
		return false, ErrRegistrationEmailVerificationDisabled
	}
	if policy.registrationIPLimitEnabled {
		count, resetAt, err := readRegistrationIPCounter(ctx, tx, input.SourceIP)
		if err != nil {
			return false, err
		}
		if err := registrationIPLimitError(policy, count, resetAt, now); err != nil {
			return false, err
		}
	}
	if err := checkRegistrationEmailPolicy(policy, input.Email); err != nil {
		return false, err
	}
	if policy.stopRegister {
		return false, ErrRegistrationClosed
	}

	var smtpEnabled bool
	var appName, appURL string
	if err := tx.QueryRowContext(ctx, `
		SELECT smtp_enabled, app_name, app_url FROM app_settings WHERE id = 1
	`).Scan(&smtpEnabled, &appName, &appURL); err != nil {
		return false, fmt.Errorf("read registration verification mail settings: %w", err)
	}
	if !smtpEnabled {
		return false, ErrMailUnavailable
	}

	var resendAfter int64
	err = tx.QueryRowContext(ctx, `
		SELECT resend_after FROM registration_email_challenges WHERE email_digest = ?
	`, input.EmailDigest).Scan(&resendAfter)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("read registration verification cooldown: %w", err)
	}
	if err == nil && resendAfter > now.Unix() {
		return false, &RegistrationEmailVerificationLimitError{RetryAfterSeconds: resendAfter - now.Unix()}
	}

	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email = ? COLLATE NOCASE`, input.Email).Scan(&existing); err != nil {
		return false, fmt.Errorf("find registration verification account: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO registration_email_challenges (
			email_digest, code_digest, expires_at, resend_after,
			failed_attempts, failure_reset_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, 0, NULL, ?, ?)
		ON CONFLICT(email_digest) DO UPDATE SET
			code_digest = excluded.code_digest,
			expires_at = excluded.expires_at,
			resend_after = excluded.resend_after,
			failed_attempts = CASE
				WHEN registration_email_challenges.failure_reset_at IS NULL OR registration_email_challenges.failure_reset_at <= excluded.updated_at THEN 0
				ELSE registration_email_challenges.failed_attempts
			END,
			failure_reset_at = CASE
				WHEN registration_email_challenges.failure_reset_at IS NULL OR registration_email_challenges.failure_reset_at <= excluded.updated_at THEN NULL
				ELSE registration_email_challenges.failure_reset_at
			END,
			updated_at = excluded.updated_at
	`, input.EmailDigest, input.CodeDigest, now.Add(registrationEmailCodeTTL).Unix(),
		now.Add(registrationEmailSendCooldown).Unix(), now.Unix(), now.Unix()); err != nil {
		return false, fmt.Errorf("save registration verification challenge: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE registration_email_mail_outbox
		SET cancelled_at = ?, code_cipher = NULL, claim_token = NULL, claimed_at = NULL,
			last_error = 'superseded by a newer registration email code', updated_at = ?
		WHERE email_digest = ? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
	`, now.Unix(), now.Unix(), input.EmailDigest); err != nil {
		return false, fmt.Errorf("cancel superseded registration verification mail: %w", err)
	}
	queued := existing == 0
	if queued {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO registration_email_mail_outbox (
				email_digest, recipient, code_cipher, app_name, app_url,
				available_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, input.EmailDigest, input.Email, input.CodeCipher, appName, appURL,
			now.Unix(), now.Unix(), now.Unix()); err != nil {
			return false, fmt.Errorf("enqueue registration verification mail: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit registration verification request: %w", err)
	}
	return queued, nil
}

// CheckRegistrationEmailVerification performs the inexpensive verifier check
// before Argon2id. Failures and lockout survive process restarts.
func (s *Store) CheckRegistrationEmailVerification(ctx context.Context, emailDigest, codeDigest []byte, now time.Time) error {
	if len(emailDigest) != 32 || len(codeDigest) != 32 || now.IsZero() {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin registration verification check: %w", err)
	}
	defer tx.Rollback()

	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT email_verify FROM app_settings WHERE id = 1`).Scan(&enabled); err != nil {
		return fmt.Errorf("read registration verification setting: %w", err)
	}
	if !enabled {
		return ErrRegistrationEmailVerificationDisabled
	}
	storedDigest, expiresAt, failedAttempts, failureResetAt, err := readRegistrationEmailChallenge(ctx, tx, emailDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRegistrationEmailVerificationInvalid
	}
	if err != nil {
		return fmt.Errorf("read registration verification challenge: %w", err)
	}
	if failureResetAt.Valid && failureResetAt.Int64 <= now.Unix() {
		failedAttempts = 0
		failureResetAt.Valid = false
	}
	if failedAttempts >= 3 && failureResetAt.Valid && failureResetAt.Int64 > now.Unix() {
		return &RegistrationEmailVerificationLockedError{RetryAfterSeconds: failureResetAt.Int64 - now.Unix()}
	}
	if expiresAt <= now.Unix() {
		return ErrRegistrationEmailVerificationInvalid
	}
	if subtle.ConstantTimeCompare(storedDigest, codeDigest) != 1 {
		resetAt := failureResetAt.Int64
		if !failureResetAt.Valid {
			resetAt = now.Add(registrationEmailFailureWindow).Unix()
		}
		nextAttempts := failedAttempts + 1
		if nextAttempts > 3 {
			nextAttempts = 3
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE registration_email_challenges
			SET failed_attempts = ?, failure_reset_at = ?, updated_at = ?
			WHERE email_digest = ?
		`, nextAttempts, resetAt, now.Unix(), emailDigest); err != nil {
			return fmt.Errorf("record registration verification failure: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit registration verification failure: %w", err)
		}
		return ErrRegistrationEmailVerificationInvalid
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit registration verification check: %w", err)
	}
	return nil
}

func (s *Store) ClaimRegistrationEmailVerificationMail(ctx context.Context, claimToken string, now time.Time, lease time.Duration) (RegistrationEmailVerificationMailJob, bool, error) {
	claimToken = strings.TrimSpace(claimToken)
	if claimToken == "" || len(claimToken) > maxTicketMailClaimToken || lease < time.Second || lease > time.Hour {
		return RegistrationEmailVerificationMailJob{}, false, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegistrationEmailVerificationMailJob{}, false, fmt.Errorf("begin claim registration verification mail: %w", err)
	}
	defer tx.Rollback()
	var id int64
	staleBefore := now.Add(-lease).Unix()
	err = tx.QueryRowContext(ctx, `
		SELECT o.id FROM registration_email_mail_outbox o CROSS JOIN app_settings s
		WHERE s.id = 1 AND s.smtp_enabled = 1 AND s.email_verify = 1 AND o.code_cipher IS NOT NULL
		  AND o.sent_at IS NULL AND o.failed_at IS NULL AND o.cancelled_at IS NULL AND o.attempt_count < 3
		  AND EXISTS (
			SELECT 1 FROM registration_email_challenges c
			WHERE c.email_digest = o.email_digest AND c.expires_at > ?
		  )
		  AND o.available_at <= ? AND (o.claimed_at IS NULL OR o.claimed_at <= ?)
		ORDER BY o.available_at, o.id LIMIT 1
	`, now.Add(30*time.Second).Unix(), now.Unix(), staleBefore).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return RegistrationEmailVerificationMailJob{}, false, nil
	}
	if err != nil {
		return RegistrationEmailVerificationMailJob{}, false, fmt.Errorf("select registration verification mail: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE registration_email_mail_outbox
		SET claim_token = ?, claimed_at = ?, attempt_count = attempt_count + 1, updated_at = ?
		WHERE id = ? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL AND attempt_count < 3
		  AND available_at <= ? AND (claimed_at IS NULL OR claimed_at <= ?)
		  AND EXISTS (
			SELECT 1 FROM registration_email_challenges c
			WHERE c.email_digest = registration_email_mail_outbox.email_digest AND c.expires_at > ?
		  )
	`, claimToken, now.Unix(), now.Unix(), id, now.Unix(), staleBefore, now.Add(30*time.Second).Unix())
	if err != nil {
		return RegistrationEmailVerificationMailJob{}, false, fmt.Errorf("claim registration verification mail: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return RegistrationEmailVerificationMailJob{}, false, nil
	}
	job, err := scanRegistrationEmailVerificationMailJob(tx.QueryRowContext(ctx, `
		SELECT o.id, o.attempt_count, o.email_digest, o.recipient, o.code_cipher, o.app_name, o.app_url,
		       s.smtp_host, s.smtp_port, s.smtp_username, s.smtp_password_cipher, s.smtp_encryption, s.smtp_from_address
		FROM registration_email_mail_outbox o CROSS JOIN app_settings s
		WHERE o.id = ? AND o.claim_token = ? AND s.id = 1
	`, id, claimToken))
	if err != nil {
		return RegistrationEmailVerificationMailJob{}, false, fmt.Errorf("read claimed registration verification mail: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return RegistrationEmailVerificationMailJob{}, false, fmt.Errorf("commit registration verification mail claim: %w", err)
	}
	return job, true, nil
}

func (s *Store) CompleteRegistrationEmailVerificationMail(ctx context.Context, jobID int64, claimToken string, now time.Time) error {
	if jobID < 1 || strings.TrimSpace(claimToken) == "" {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE registration_email_mail_outbox
		SET sent_at = ?, code_cipher = NULL, claim_token = NULL, claimed_at = NULL, last_error = NULL, updated_at = ?
		WHERE id = ? AND claim_token = ? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
	`, now.Unix(), now.Unix(), jobID, claimToken)
	if err != nil {
		return fmt.Errorf("complete registration verification mail: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) FailRegistrationEmailVerificationMail(ctx context.Context, jobID int64, claimToken, failure string, retryAt, now time.Time) error {
	if jobID < 1 || strings.TrimSpace(claimToken) == "" || retryAt.Before(now) {
		return ErrInvalidInput
	}
	failure = truncateRunes(strings.TrimSpace(failure), maxTicketMailErrorRunes)
	if failure == "" {
		failure = "mail delivery failed"
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE registration_email_mail_outbox
		SET available_at = CASE WHEN attempt_count < ? THEN ? ELSE available_at END,
		    failed_at = CASE WHEN attempt_count >= ? OR NOT (SELECT smtp_enabled AND email_verify FROM app_settings WHERE id = 1) THEN ? ELSE NULL END,
		    code_cipher = CASE WHEN attempt_count >= ? OR NOT (SELECT smtp_enabled AND email_verify FROM app_settings WHERE id = 1) THEN NULL ELSE code_cipher END,
		    claim_token = NULL, claimed_at = NULL, last_error = ?, updated_at = ?
		WHERE id = ? AND claim_token = ? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
	`, maxTicketMailAttempts, retryAt.Unix(), maxTicketMailAttempts, now.Unix(), maxTicketMailAttempts,
		failure, now.Unix(), jobID, claimToken)
	if err != nil {
		return fmt.Errorf("fail registration verification mail: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) PruneExpiredRegistrationEmailVerifications(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit < 1 || limit > 1_000 {
		return 0, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin prune registration verification challenges: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE registration_email_mail_outbox
		SET cancelled_at = ?, code_cipher = NULL, claim_token = NULL, claimed_at = NULL,
			last_error = 'registration email code expired', updated_at = ?
		WHERE sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL AND email_digest IN (
			SELECT email_digest FROM registration_email_challenges WHERE expires_at <= ? ORDER BY expires_at LIMIT ?
		)
	`, now.Unix(), now.Unix(), now.Unix(), limit); err != nil {
		return 0, fmt.Errorf("cancel expired registration verification mail: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM registration_email_challenges WHERE email_digest IN (
			SELECT email_digest FROM registration_email_challenges WHERE expires_at <= ? ORDER BY expires_at LIMIT ?
		)
	`, now.Unix(), limit)
	if err != nil {
		return 0, fmt.Errorf("delete expired registration verification challenges: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count expired registration verification challenges: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit registration verification pruning: %w", err)
	}
	return removed, nil
}

func readRegistrationEmailChallenge(ctx context.Context, query registrationPolicyRow, emailDigest []byte) ([]byte, int64, int, sql.NullInt64, error) {
	var storedDigest []byte
	var expiresAt int64
	var failedAttempts int
	var failureResetAt sql.NullInt64
	err := query.QueryRowContext(ctx, `
		SELECT code_digest, expires_at, failed_attempts, failure_reset_at
		FROM registration_email_challenges WHERE email_digest = ?
	`, emailDigest).Scan(&storedDigest, &expiresAt, &failedAttempts, &failureResetAt)
	return storedDigest, expiresAt, failedAttempts, failureResetAt, err
}

func validateRegistrationEmailChallengeTx(ctx context.Context, tx *sql.Tx, emailDigest, codeDigest []byte, now time.Time) error {
	if len(emailDigest) != 32 || len(codeDigest) != 32 {
		return ErrRegistrationEmailVerificationInvalid
	}
	storedDigest, expiresAt, failedAttempts, failureResetAt, err := readRegistrationEmailChallenge(ctx, tx, emailDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRegistrationEmailVerificationInvalid
	}
	if err != nil {
		return fmt.Errorf("recheck registration verification challenge: %w", err)
	}
	if failedAttempts >= 3 && failureResetAt.Valid && failureResetAt.Int64 > now.Unix() {
		return &RegistrationEmailVerificationLockedError{RetryAfterSeconds: failureResetAt.Int64 - now.Unix()}
	}
	if expiresAt <= now.Unix() || subtle.ConstantTimeCompare(storedDigest, codeDigest) != 1 {
		return ErrRegistrationEmailVerificationInvalid
	}
	return nil
}

func consumeRegistrationEmailChallengeTx(ctx context.Context, tx *sql.Tx, emailDigest []byte, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM registration_email_challenges WHERE email_digest = ?`, emailDigest); err != nil {
		return fmt.Errorf("consume registration verification challenge: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE registration_email_mail_outbox
		SET cancelled_at = COALESCE(cancelled_at, ?), code_cipher = NULL,
			claim_token = NULL, claimed_at = NULL, updated_at = ?
		WHERE email_digest = ? AND sent_at IS NULL AND failed_at IS NULL
	`, now.Unix(), now.Unix(), emailDigest); err != nil {
		return fmt.Errorf("cancel consumed registration verification mail: %w", err)
	}
	return nil
}

func scanRegistrationEmailVerificationMailJob(row rowScanner) (RegistrationEmailVerificationMailJob, error) {
	var job RegistrationEmailVerificationMailJob
	if err := row.Scan(&job.ID, &job.Attempt, &job.EmailDigest, &job.Recipient, &job.CodeCipher,
		&job.AppName, &job.AppURL, &job.SMTPHost, &job.SMTPPort, &job.SMTPUsername,
		&job.SMTPPasswordCipher, &job.SMTPEncryption, &job.SMTPFromAddress); err != nil {
		return RegistrationEmailVerificationMailJob{}, err
	}
	job.EmailDigest = append([]byte(nil), job.EmailDigest...)
	job.CodeCipher = append([]byte(nil), job.CodeCipher...)
	job.SMTPPasswordCipher = append([]byte(nil), job.SMTPPasswordCipher...)
	return job, nil
}
