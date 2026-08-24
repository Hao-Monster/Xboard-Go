package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxTicketAppNameRunes   = 100
	maxTicketAppURLBytes    = 2_048
	maxSMTPHostBytes        = 253
	maxSMTPIdentityBytes    = 320
	maxSMTPPasswordCipher   = 8_192
	maxTicketMailClaimToken = 128
	maxTicketMailErrorRunes = 1_024
	maxTicketMailAttempts   = 3
	ticketMailThrottle      = 30 * time.Minute
)

func (s *Store) GetTicketSettings(ctx context.Context) (TicketSettings, error) {
	return s.getTicketSettings(ctx, false)
}

// GetSMTPPasswordCipher is used only at trusted process boundaries such as
// startup validation and the mail worker. HTTP responses must use
// GetTicketSettings, which always strips this value.
func (s *Store) GetSMTPPasswordCipher(ctx context.Context) ([]byte, error) {
	var ciphertext []byte
	if err := s.db.QueryRowContext(ctx, `SELECT smtp_password_cipher FROM app_settings WHERE id = 1`).Scan(&ciphertext); err != nil {
		return nil, fmt.Errorf("get SMTP password cipher: %w", err)
	}
	return append([]byte(nil), ciphertext...), nil
}

func (s *Store) getTicketSettings(ctx context.Context, includeCipher bool) (TicketSettings, error) {
	settings, err := scanTicketSettings(s.db.QueryRowContext(ctx, `
		SELECT revision, app_name, app_url, ticket_must_wait_reply, smtp_enabled, smtp_host,
		       smtp_port, smtp_username, smtp_password_cipher, smtp_encryption, smtp_from_address, updated_at
		FROM app_settings WHERE id = 1
	`))
	if err != nil {
		return TicketSettings{}, fmt.Errorf("get ticket settings: %w", err)
	}
	if !includeCipher {
		settings.SMTPPasswordCipher = nil
	}
	return settings, nil
}

func (s *Store) UpdateTicketSettings(ctx context.Context, administratorID, revision int64, input SaveTicketSettingsInput, now time.Time) (TicketSettings, error) {
	if administratorID < 1 || revision < 1 {
		return TicketSettings{}, ErrInvalidInput
	}
	normalized, err := normalizeTicketSettings(input)
	if err != nil {
		return TicketSettings{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TicketSettings{}, fmt.Errorf("begin update ticket settings: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE app_settings
		SET app_name = ?, app_url = ?, ticket_must_wait_reply = ?, smtp_enabled = ?, smtp_host = ?,
		    smtp_port = ?, smtp_username = ?,
		    smtp_password_cipher = CASE WHEN ? THEN ? ELSE smtp_password_cipher END,
		    smtp_encryption = ?, smtp_from_address = ?, updated_by = ?, updated_at = ?, revision = revision + 1
		WHERE id = 1 AND revision = ?
	`, normalized.AppName, normalized.AppURL, normalized.TicketMustWaitReply, normalized.SMTPEnabled,
		normalized.SMTPHost, normalized.SMTPPort, normalized.SMTPUsername,
		normalized.ReplaceSMTPPassword, nullableBytes(normalized.SMTPPasswordCipher), normalized.SMTPEncryption,
		normalized.SMTPFromAddress, administratorID, now.Unix(), revision)
	if err != nil {
		return TicketSettings{}, fmt.Errorf("update ticket settings: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return TicketSettings{}, ErrConflict
	}
	if !normalized.SMTPEnabled {
		if _, err := tx.ExecContext(ctx, `
			UPDATE ticket_mail_outbox
			SET failed_at = ?, last_error = 'cancelled because SMTP notifications were disabled', updated_at = ?
			WHERE sent_at IS NULL AND failed_at IS NULL AND claim_token IS NULL
		`, now.Unix(), now.Unix()); err != nil {
			return TicketSettings{}, fmt.Errorf("cancel disabled ticket mail: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE password_reset_mail_outbox
			SET cancelled_at = ?, code_cipher = NULL,
			    last_error = 'cancelled because SMTP notifications were disabled', updated_at = ?
			WHERE sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL AND claim_token IS NULL
		`, now.Unix(), now.Unix()); err != nil {
			return TicketSettings{}, fmt.Errorf("cancel disabled password reset mail: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return TicketSettings{}, fmt.Errorf("commit ticket settings: %w", err)
	}
	return s.GetTicketSettings(ctx)
}

func (s *Store) ClaimTicketMail(ctx context.Context, claimToken string, now time.Time, lease time.Duration) (TicketMailJob, bool, error) {
	claimToken = strings.TrimSpace(claimToken)
	if claimToken == "" || len(claimToken) > maxTicketMailClaimToken || lease < time.Second || lease > time.Hour {
		return TicketMailJob{}, false, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TicketMailJob{}, false, fmt.Errorf("begin claim ticket mail: %w", err)
	}
	defer tx.Rollback()

	var id int64
	staleBefore := now.Add(-lease).Unix()
	err = tx.QueryRowContext(ctx, `
		SELECT o.id
		FROM ticket_mail_outbox o CROSS JOIN app_settings s
		WHERE s.id = 1 AND s.smtp_enabled = 1
		  AND o.sent_at IS NULL AND o.failed_at IS NULL AND o.attempt_count < 3
		  AND o.available_at <= ? AND (o.claimed_at IS NULL OR o.claimed_at <= ?)
		ORDER BY o.available_at, o.id LIMIT 1
	`, now.Unix(), staleBefore).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return TicketMailJob{}, false, nil
	}
	if err != nil {
		return TicketMailJob{}, false, fmt.Errorf("select ticket mail: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE ticket_mail_outbox
		SET claim_token = ?, claimed_at = ?, attempt_count = attempt_count + 1, updated_at = ?
		WHERE id = ? AND sent_at IS NULL AND failed_at IS NULL AND attempt_count < 3
		  AND available_at <= ? AND (claimed_at IS NULL OR claimed_at <= ?)
	`, claimToken, now.Unix(), now.Unix(), id, now.Unix(), staleBefore)
	if err != nil {
		return TicketMailJob{}, false, fmt.Errorf("claim ticket mail: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return TicketMailJob{}, false, nil
	}
	job, err := scanTicketMailJob(tx.QueryRowContext(ctx, `
		SELECT o.id, o.attempt_count, o.recipient, o.ticket_subject, o.reply_message,
		       o.app_name, o.app_url, s.smtp_host, s.smtp_port, s.smtp_username,
		       s.smtp_password_cipher, s.smtp_encryption, s.smtp_from_address
		FROM ticket_mail_outbox o
		CROSS JOIN app_settings s
		WHERE o.id = ? AND o.claim_token = ? AND s.id = 1
	`, id, claimToken))
	if err != nil {
		return TicketMailJob{}, false, fmt.Errorf("read claimed ticket mail: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return TicketMailJob{}, false, fmt.Errorf("commit ticket mail claim: %w", err)
	}
	return job, true, nil
}

func (s *Store) CompleteTicketMail(ctx context.Context, jobID int64, claimToken string, now time.Time) error {
	if jobID < 1 || strings.TrimSpace(claimToken) == "" {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE ticket_mail_outbox
		SET sent_at = ?, claim_token = NULL, claimed_at = NULL, last_error = NULL, updated_at = ?
		WHERE id = ? AND claim_token = ? AND sent_at IS NULL AND failed_at IS NULL
	`, now.Unix(), now.Unix(), jobID, claimToken)
	if err != nil {
		return fmt.Errorf("complete ticket mail: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) FailTicketMail(ctx context.Context, jobID int64, claimToken, failure string, retryAt, now time.Time) error {
	if jobID < 1 || strings.TrimSpace(claimToken) == "" || retryAt.Before(now) {
		return ErrInvalidInput
	}
	failure = truncateRunes(strings.TrimSpace(failure), maxTicketMailErrorRunes)
	if failure == "" {
		failure = "mail delivery failed"
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE ticket_mail_outbox
		SET available_at = CASE WHEN attempt_count < ? THEN ? ELSE available_at END,
		    failed_at = CASE WHEN attempt_count >= ? OR NOT (SELECT smtp_enabled FROM app_settings WHERE id = 1) THEN ? ELSE NULL END,
		    claim_token = NULL, claimed_at = NULL, last_error = ?, updated_at = ?
		WHERE id = ? AND claim_token = ? AND sent_at IS NULL AND failed_at IS NULL
	`, maxTicketMailAttempts, retryAt.Unix(), maxTicketMailAttempts, now.Unix(), failure, now.Unix(), jobID, claimToken)
	if err != nil {
		return fmt.Errorf("fail ticket mail: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

func enqueueTicketMailTx(ctx context.Context, tx *sql.Tx, userID, ticketMessageID int64, now time.Time) error {
	var enabled bool
	var recipient, subject, message, appName, appURL string
	if err := tx.QueryRowContext(ctx, `
		SELECT s.smtp_enabled, u.email, t.subject, m.message, s.app_name, s.app_url
		FROM ticket_messages m
		JOIN tickets t ON t.id = m.ticket_id
		JOIN users u ON u.id = t.user_id
		CROSS JOIN app_settings s
		WHERE m.id = ? AND t.user_id = ? AND s.id = 1
	`, ticketMessageID, userID).Scan(&enabled, &recipient, &subject, &message, &appName, &appURL); err != nil {
		return fmt.Errorf("read ticket email setting: %w", err)
	}
	if !enabled {
		return nil
	}
	var lastEnqueued sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT last_enqueued_at FROM ticket_mail_throttle WHERE user_id = ?`, userID).Scan(&lastEnqueued)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read ticket mail throttle: %w", err)
	}
	if lastEnqueued.Valid && now.Unix()-lastEnqueued.Int64 < int64(ticketMailThrottle/time.Second) {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ticket_mail_outbox (
			ticket_message_id, recipient, ticket_subject, reply_message, app_name, app_url,
			available_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, ticketMessageID, recipient, subject, message, appName, appURL, now.Unix(), now.Unix(), now.Unix()); err != nil {
		return fmt.Errorf("enqueue ticket mail: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ticket_mail_throttle (user_id, last_enqueued_at) VALUES (?, ?)
		ON CONFLICT(user_id) DO UPDATE SET last_enqueued_at = excluded.last_enqueued_at
	`, userID, now.Unix()); err != nil {
		return fmt.Errorf("update ticket mail throttle: %w", err)
	}
	return nil
}

func normalizeTicketSettings(input SaveTicketSettingsInput) (SaveTicketSettingsInput, error) {
	input.AppName = strings.TrimSpace(input.AppName)
	input.AppURL = strings.TrimRight(strings.TrimSpace(input.AppURL), "/")
	input.SMTPHost = strings.TrimSpace(input.SMTPHost)
	input.SMTPUsername = strings.TrimSpace(input.SMTPUsername)
	input.SMTPEncryption = strings.ToLower(strings.TrimSpace(input.SMTPEncryption))
	input.SMTPFromAddress = strings.TrimSpace(input.SMTPFromAddress)
	if input.SMTPPort == 0 {
		input.SMTPPort = 587
	}
	if input.SMTPEncryption == "" {
		input.SMTPEncryption = "starttls"
	}
	if utf8.RuneCountInString(input.AppName) < 1 || utf8.RuneCountInString(input.AppName) > maxTicketAppNameRunes ||
		containsUnsafeTicketControl(input.AppName, false) || len(input.AppURL) > maxTicketAppURLBytes ||
		(input.AppURL != "" && !validHTTPURL(input.AppURL)) || input.SMTPPort < 1 || input.SMTPPort > 65_535 ||
		len(input.SMTPHost) > maxSMTPHostBytes || len(input.SMTPUsername) > maxSMTPIdentityBytes ||
		len(input.SMTPFromAddress) > maxSMTPIdentityBytes || len(input.SMTPPasswordCipher) > maxSMTPPasswordCipher ||
		(input.SMTPHost != "" && !validSMTPHost(input.SMTPHost)) || containsUnsafeTicketControl(input.SMTPUsername, false) ||
		(input.SMTPFromAddress != "" && !validMailAddress(input.SMTPFromAddress)) ||
		(input.SMTPEncryption != "starttls" && input.SMTPEncryption != "tls" && input.SMTPEncryption != "none") {
		return SaveTicketSettingsInput{}, ErrInvalidInput
	}
	if input.SMTPEnabled && (input.SMTPHost == "" || input.SMTPFromAddress == "") {
		return SaveTicketSettingsInput{}, ErrInvalidInput
	}
	return input, nil
}

func validHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.User == nil
}

func validSMTPHost(value string) bool {
	if value == "" || strings.ContainsAny(value, "[]\r\n\t ") {
		return false
	}
	if net.ParseIP(value) != nil {
		return true
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) < 1 || len(label) > 63 || !asciiLetterOrDigit(label[0]) || !asciiLetterOrDigit(label[len(label)-1]) {
			return false
		}
		for index := 1; index < len(label)-1; index++ {
			if !asciiLetterOrDigit(label[index]) && label[index] != '-' {
				return false
			}
		}
	}
	return true
}

func asciiLetterOrDigit(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func validMailAddress(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address != "" && !strings.ContainsAny(address.Address, "\r\n")
}

func scanTicketSettings(row rowScanner) (TicketSettings, error) {
	var settings TicketSettings
	var cipher []byte
	var updatedAt int64
	if err := row.Scan(&settings.Revision, &settings.AppName, &settings.AppURL, &settings.TicketMustWaitReply,
		&settings.SMTPEnabled, &settings.SMTPHost, &settings.SMTPPort, &settings.SMTPUsername,
		&cipher, &settings.SMTPEncryption, &settings.SMTPFromAddress, &updatedAt); err != nil {
		return TicketSettings{}, err
	}
	settings.SMTPPasswordSet = len(cipher) > 0
	settings.SMTPPasswordCipher = append([]byte(nil), cipher...)
	settings.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return settings, nil
}

func scanTicketMailJob(row rowScanner) (TicketMailJob, error) {
	var job TicketMailJob
	if err := row.Scan(&job.ID, &job.Attempt, &job.Recipient, &job.Subject, &job.Message,
		&job.AppName, &job.AppURL, &job.SMTPHost, &job.SMTPPort, &job.SMTPUsername,
		&job.SMTPPasswordCipher, &job.SMTPEncryption, &job.SMTPFromAddress); err != nil {
		return TicketMailJob{}, err
	}
	job.SMTPPasswordCipher = append([]byte(nil), job.SMTPPasswordCipher...)
	return job, nil
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func truncateRunes(value string, maximum int) string {
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	return string([]rune(value)[:maximum])
}
