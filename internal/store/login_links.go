package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	quickLoginLinkTTL               = time.Minute
	mailLoginLinkTTL                = 5 * time.Minute
	mailLoginSendCooldown           = time.Minute
	maxActiveQuickLoginLinksPerUser = 32
	maxLoginLinkCipher              = 512
)

func (s *Store) CreateQuickLoginLink(ctx context.Context, userID int64, tokenDigest []byte, redirect string, now time.Time) error {
	if userID < 1 || len(tokenDigest) != 32 || !validLoginLinkRedirect(redirect) || now.IsZero() {
		return fmt.Errorf("%w: invalid quick login link", ErrInvalidInput)
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin quick login link: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM login_link_tokens
		WHERE user_id = ? AND purpose = 'quick' AND expires_at <= ?
	`, userID, now.Unix()); err != nil {
		return fmt.Errorf("prune expired quick login links: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM login_link_tokens
		WHERE token_digest IN (
			SELECT token_digest FROM login_link_tokens
			WHERE user_id = ? AND purpose = 'quick'
			ORDER BY created_at DESC, rowid DESC LIMIT -1 OFFSET ?
		)
	`, userID, maxActiveQuickLoginLinksPerUser-1); err != nil {
		return fmt.Errorf("bound active quick login links: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO login_link_tokens (token_digest, user_id, purpose, redirect_path, expires_at, created_at)
		SELECT ?, id, 'quick', ?, ?, ? FROM users
		WHERE id = ? AND account_kind = ? AND banned = 0
	`, tokenDigest, redirect, now.Add(quickLoginLinkTTL).Unix(), now.Unix(), userID, AccountKindHuman)
	if err != nil {
		return fmt.Errorf("create quick login link: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count created quick login links: %w", err)
	}
	if rows != 1 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit quick login link: %w", err)
	}
	return nil
}

// RequestMailLoginLink persists the same cooldown for every normalized email.
// Only an active human account receives a token and outbox job.
func (s *Store) RequestMailLoginLink(ctx context.Context, input MailLoginLinkRequestInput, now time.Time) (bool, error) {
	input.Email = normalizeEmail(input.Email)
	input.LinkBaseURL = strings.TrimRight(strings.TrimSpace(input.LinkBaseURL), "/")
	if input.Email == "" || len(input.Email) > 320 || input.ExpectedUserID < 0 || len(input.EmailDigest) != 32 || len(input.TokenDigest) != 32 ||
		len(input.TokenCipher) < 32 || len(input.TokenCipher) > maxLoginLinkCipher ||
		!validLoginLinkRedirect(input.Redirect) || input.LinkBaseURL == "" || len(input.LinkBaseURL) > maxTicketAppURLBytes ||
		!validHTTPURL(input.LinkBaseURL) || now.IsZero() {
		return false, fmt.Errorf("%w: invalid mail login link request", ErrInvalidInput)
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin mail login link request: %w", err)
	}
	defer tx.Rollback()

	var enabled, smtpEnabled bool
	var appName string
	if err := tx.QueryRowContext(ctx, `
		SELECT login_with_mail_link_enable, smtp_enabled, app_name FROM app_settings WHERE id = 1
	`).Scan(&enabled, &smtpEnabled, &appName); err != nil {
		return false, fmt.Errorf("read mail login settings: %w", err)
	}
	if !enabled {
		return false, ErrMailLoginDisabled
	}
	if !smtpEnabled {
		return false, ErrMailUnavailable
	}
	var resendAfter int64
	err = tx.QueryRowContext(ctx, `SELECT resend_after FROM mail_login_request_limits WHERE email_digest = ?`, input.EmailDigest).Scan(&resendAfter)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("read mail login cooldown: %w", err)
	}
	if err == nil && resendAfter > now.Unix() {
		return false, &MailLoginLimitError{RetryAfterSeconds: resendAfter - now.Unix()}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO mail_login_request_limits (email_digest, resend_after, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(email_digest) DO UPDATE SET resend_after = excluded.resend_after, updated_at = excluded.updated_at
	`, input.EmailDigest, now.Add(mailLoginSendCooldown).Unix(), now.Unix()); err != nil {
		return false, fmt.Errorf("save mail login cooldown: %w", err)
	}

	var userID int64
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM users WHERE email = ? COLLATE NOCASE AND account_kind = ? AND banned = 0
	`, input.Email, AccountKindHuman).Scan(&userID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("find mail login account: %w", err)
	}
	queued := err == nil && input.ExpectedUserID == userID
	if queued {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO login_link_tokens (token_digest, user_id, purpose, redirect_path, expires_at, created_at)
			VALUES (?, ?, 'email', ?, ?, ?)
		`, input.TokenDigest, userID, input.Redirect, now.Add(mailLoginLinkTTL).Unix(), now.Unix()); err != nil {
			return false, fmt.Errorf("create mail login token: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO login_link_mail_outbox (
				token_digest, user_id, recipient, token_cipher, redirect_path, app_name, app_url,
				available_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, input.TokenDigest, userID, input.Email, input.TokenCipher, input.Redirect, appName, input.LinkBaseURL,
			now.Unix(), now.Unix(), now.Unix()); err != nil {
			return false, fmt.Errorf("enqueue mail login link: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit mail login link request: %w", err)
	}
	return queued, nil
}

func (s *Store) ExchangeLoginLink(ctx context.Context, input LoginLinkExchangeInput, now time.Time) (LoginLinkExchange, error) {
	accessTokenRequested := input.AccessTokenHash != "" || input.AccessTokenName != ""
	if len(input.TokenDigest) != 32 || (len(input.AlternateTokenDigest) != 0 && len(input.AlternateTokenDigest) != 32) ||
		strings.TrimSpace(input.SessionTokenHash) == "" || strings.TrimSpace(input.CSRFHash) == "" ||
		!input.SessionExpiresAt.After(now) || now.IsZero() ||
		(accessTokenRequested && (!validAccessTokenHash(input.AccessTokenHash) || strings.TrimSpace(input.AccessTokenName) == "" || len([]rune(input.AccessTokenName)) > 80)) {
		return LoginLinkExchange{}, fmt.Errorf("%w: invalid login link exchange", ErrInvalidInput)
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LoginLinkExchange{}, fmt.Errorf("begin login link exchange: %w", err)
	}
	defer tx.Rollback()

	alternateDigest := input.AlternateTokenDigest
	if len(alternateDigest) == 0 {
		alternateDigest = input.TokenDigest
	}
	var result LoginLinkExchange
	var consumedDigest []byte
	err = tx.QueryRowContext(ctx, `
		SELECT u.id, u.email, u.password_hash, u.is_admin, u.banned, u.account_kind, u.subscription_token,
		       l.redirect_path, l.token_digest
		FROM login_link_tokens l JOIN users u ON u.id = l.user_id
		WHERE l.token_digest IN (?, ?) AND l.expires_at > ? AND u.account_kind = ? AND u.banned = 0
		ORDER BY CASE l.purpose WHEN 'quick' THEN 0 ELSE 1 END LIMIT 1
	`, input.TokenDigest, alternateDigest, now.Unix(), AccountKindHuman).Scan(
		&result.User.ID, &result.User.Email, &result.User.PasswordHash, &result.User.IsAdmin,
		&result.User.Banned, &result.User.AccountKind, &result.User.SubscriptionToken, &result.Redirect, &consumedDigest,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return LoginLinkExchange{}, ErrLoginLinkInvalid
	}
	if err != nil {
		return LoginLinkExchange{}, fmt.Errorf("read login link exchange: %w", err)
	}
	deleted, err := tx.ExecContext(ctx, `DELETE FROM login_link_tokens WHERE token_digest = ? AND expires_at > ?`, consumedDigest, now.Unix())
	if err != nil {
		return LoginLinkExchange{}, fmt.Errorf("consume login link: %w", err)
	}
	rows, _ := deleted.RowsAffected()
	if rows != 1 {
		return LoginLinkExchange{}, ErrLoginLinkInvalid
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE login_link_mail_outbox
		SET cancelled_at = CASE WHEN sent_at IS NULL AND failed_at IS NULL THEN ? ELSE cancelled_at END,
		    token_cipher = NULL, claim_token = NULL, claimed_at = NULL, updated_at = ?
		WHERE token_digest = ?
	`, now.Unix(), now.Unix(), consumedDigest); err != nil {
		return LoginLinkExchange{}, fmt.Errorf("erase exchanged mail login token: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO admin_sessions (user_id, token_hash, csrf_hash, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, result.User.ID, input.SessionTokenHash, input.CSRFHash, input.SessionExpiresAt.Unix(), now.Unix()); err != nil {
		return LoginLinkExchange{}, fmt.Errorf("create login link session: %w", err)
	}
	if accessTokenRequested {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO access_tokens (user_id, token_hash, name, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?)
		`, result.User.ID, input.AccessTokenHash, strings.TrimSpace(input.AccessTokenName), now.Unix(), now.Unix()); err != nil {
			return LoginLinkExchange{}, fmt.Errorf("create login link access token: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return LoginLinkExchange{}, fmt.Errorf("commit login link exchange: %w", err)
	}
	return result, nil
}

func (s *Store) ClaimLoginLinkMail(ctx context.Context, claimToken string, now time.Time, lease time.Duration) (LoginLinkMailJob, bool, error) {
	claimToken = strings.TrimSpace(claimToken)
	if claimToken == "" || len(claimToken) > maxTicketMailClaimToken || lease < time.Second || lease > time.Hour {
		return LoginLinkMailJob{}, false, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LoginLinkMailJob{}, false, fmt.Errorf("begin claim login link mail: %w", err)
	}
	defer tx.Rollback()
	staleBefore := now.Add(-lease).Unix()
	var id int64
	err = tx.QueryRowContext(ctx, `
		SELECT o.id FROM login_link_mail_outbox o
		JOIN login_link_tokens l ON l.token_digest = o.token_digest
		CROSS JOIN app_settings s
		WHERE s.id = 1 AND s.smtp_enabled = 1 AND s.login_with_mail_link_enable = 1
		  AND l.purpose = 'email' AND l.expires_at > ? AND o.token_cipher IS NOT NULL
		  AND o.sent_at IS NULL AND o.failed_at IS NULL AND o.cancelled_at IS NULL AND o.attempt_count < 3
		  AND o.available_at <= ? AND (o.claimed_at IS NULL OR o.claimed_at <= ?)
		ORDER BY o.available_at, o.id LIMIT 1
	`, now.Add(30*time.Second).Unix(), now.Unix(), staleBefore).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return LoginLinkMailJob{}, false, nil
	}
	if err != nil {
		return LoginLinkMailJob{}, false, fmt.Errorf("select login link mail: %w", err)
	}
	updated, err := tx.ExecContext(ctx, `
		UPDATE login_link_mail_outbox
		SET claim_token = ?, claimed_at = ?, attempt_count = attempt_count + 1, updated_at = ?
		WHERE id = ? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL AND attempt_count < 3
		  AND available_at <= ? AND (claimed_at IS NULL OR claimed_at <= ?)
	`, claimToken, now.Unix(), now.Unix(), id, now.Unix(), staleBefore)
	if err != nil {
		return LoginLinkMailJob{}, false, fmt.Errorf("claim login link mail: %w", err)
	}
	rows, _ := updated.RowsAffected()
	if rows != 1 {
		return LoginLinkMailJob{}, false, nil
	}
	job, err := scanLoginLinkMailJob(tx.QueryRowContext(ctx, `
		SELECT o.id, o.attempt_count, o.user_id, o.token_digest, o.recipient, o.token_cipher, o.redirect_path,
		       o.app_name, o.app_url, s.smtp_host, s.smtp_port, s.smtp_username,
		       s.smtp_password_cipher, s.smtp_encryption, s.smtp_from_address
		FROM login_link_mail_outbox o CROSS JOIN app_settings s
		WHERE o.id = ? AND o.claim_token = ? AND s.id = 1
	`, id, claimToken))
	if err != nil {
		return LoginLinkMailJob{}, false, fmt.Errorf("read claimed login link mail: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LoginLinkMailJob{}, false, fmt.Errorf("commit login link mail claim: %w", err)
	}
	return job, true, nil
}

func (s *Store) CompleteLoginLinkMail(ctx context.Context, jobID int64, claimToken string, now time.Time) error {
	if jobID < 1 || strings.TrimSpace(claimToken) == "" {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE login_link_mail_outbox
		SET sent_at = ?, token_cipher = NULL, claim_token = NULL, claimed_at = NULL, last_error = NULL, updated_at = ?
		WHERE id = ? AND claim_token = ? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
	`, now.Unix(), now.Unix(), jobID, claimToken)
	if err != nil {
		return fmt.Errorf("complete login link mail: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) FailLoginLinkMail(ctx context.Context, jobID int64, claimToken, failure string, retryAt, now time.Time) error {
	if jobID < 1 || strings.TrimSpace(claimToken) == "" || retryAt.Before(now) {
		return ErrInvalidInput
	}
	failure = truncateRunes(strings.TrimSpace(failure), maxTicketMailErrorRunes)
	if failure == "" {
		failure = "mail delivery failed"
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE login_link_mail_outbox
		SET available_at = CASE WHEN attempt_count < ? THEN ? ELSE available_at END,
		    failed_at = CASE WHEN attempt_count >= ? OR NOT (SELECT smtp_enabled AND login_with_mail_link_enable FROM app_settings WHERE id = 1) THEN ? ELSE NULL END,
		    token_cipher = CASE WHEN attempt_count >= ? OR NOT (SELECT smtp_enabled AND login_with_mail_link_enable FROM app_settings WHERE id = 1) THEN NULL ELSE token_cipher END,
		    claim_token = NULL, claimed_at = NULL, last_error = ?, updated_at = ?
		WHERE id = ? AND claim_token = ? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
	`, maxTicketMailAttempts, retryAt.Unix(), maxTicketMailAttempts, now.Unix(), maxTicketMailAttempts,
		failure, now.Unix(), jobID, claimToken)
	if err != nil {
		return fmt.Errorf("fail login link mail: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) PruneExpiredLoginLinks(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit < 1 || limit > 1_000 || now.IsZero() {
		return 0, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin prune login links: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE login_link_mail_outbox
		SET cancelled_at = COALESCE(cancelled_at, ?), token_cipher = NULL,
		    claim_token = NULL, claimed_at = NULL, last_error = 'login link expired', updated_at = ?
		WHERE sent_at IS NULL AND failed_at IS NULL AND token_digest IN (
			SELECT token_digest FROM login_link_tokens WHERE expires_at <= ? ORDER BY expires_at LIMIT ?
		)
	`, now.Unix(), now.Unix(), now.Unix(), limit); err != nil {
		return 0, fmt.Errorf("cancel expired login link mail: %w", err)
	}
	removed, err := tx.ExecContext(ctx, `
		DELETE FROM login_link_tokens WHERE token_digest IN (
			SELECT token_digest FROM login_link_tokens WHERE expires_at <= ? ORDER BY expires_at LIMIT ?
		)
	`, now.Unix(), limit)
	if err != nil {
		return 0, fmt.Errorf("delete expired login links: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM mail_login_request_limits WHERE resend_after <= ?`, now.Unix()); err != nil {
		return 0, fmt.Errorf("delete expired mail login cooldowns: %w", err)
	}
	count, err := removed.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count expired login links: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit login link pruning: %w", err)
	}
	return count, nil
}

func (s *Store) LoginLinkProtectionRequired(ctx context.Context) (bool, error) {
	var required bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT login_with_mail_link_enable OR EXISTS (
			SELECT 1 FROM login_link_mail_outbox WHERE token_cipher IS NOT NULL LIMIT 1
		) FROM app_settings WHERE id = 1
	`).Scan(&required); err != nil {
		return false, fmt.Errorf("read login link protection requirement: %w", err)
	}
	return required, nil
}

func (s *Store) LoginLinkProtectionProbe(ctx context.Context) (int64, []byte, bool, error) {
	var userID int64
	var ciphertext []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, token_cipher FROM login_link_mail_outbox
		WHERE token_cipher IS NOT NULL ORDER BY id LIMIT 1
	`).Scan(&userID, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, false, nil
	}
	if err != nil {
		return 0, nil, false, fmt.Errorf("read login link protection probe: %w", err)
	}
	return userID, append([]byte(nil), ciphertext...), true, nil
}

func validLoginLinkRedirect(redirect string) bool {
	switch redirect {
	case "dashboard", "invite", "knowledge", "ticket", "subscribe":
		return true
	default:
		return false
	}
}

func scanLoginLinkMailJob(row rowScanner) (LoginLinkMailJob, error) {
	var job LoginLinkMailJob
	if err := row.Scan(
		&job.ID, &job.Attempt, &job.UserID, &job.TokenDigest, &job.Recipient, &job.TokenCipher, &job.Redirect,
		&job.AppName, &job.AppURL, &job.SMTPHost, &job.SMTPPort, &job.SMTPUsername,
		&job.SMTPPasswordCipher, &job.SMTPEncryption, &job.SMTPFromAddress,
	); err != nil {
		return LoginLinkMailJob{}, err
	}
	job.TokenDigest = append([]byte(nil), job.TokenDigest...)
	job.TokenCipher = append([]byte(nil), job.TokenCipher...)
	job.SMTPPasswordCipher = append([]byte(nil), job.SMTPPasswordCipher...)
	return job, nil
}
