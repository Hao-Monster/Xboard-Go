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
	maxSubscriptionReminderBatch = 2_000
	maxReminderClaimToken        = 128
	maxReminderErrorRunes        = 1_024
	maxReminderAttempts          = 3
)

func (s *Store) GetMailSettings(ctx context.Context) (MailSettings, error) {
	var settings MailSettings
	var passwordCipher []byte
	var updatedAt int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT revision, smtp_enabled, smtp_host, smtp_port, smtp_username, smtp_password_cipher,
		       smtp_encryption, smtp_from_address, remind_mail_enable, updated_at
		FROM app_settings WHERE id = 1
	`).Scan(&settings.Revision, &settings.SMTPEnabled, &settings.SMTPHost, &settings.SMTPPort,
		&settings.SMTPUsername, &passwordCipher, &settings.SMTPEncryption, &settings.SMTPFromAddress,
		&settings.RemindMailEnabled, &updatedAt); err != nil {
		return MailSettings{}, fmt.Errorf("get mail settings: %w", err)
	}
	settings.SMTPPasswordSet = len(passwordCipher) > 0
	settings.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return settings, nil
}

func (s *Store) UpdateMailSettings(ctx context.Context, administratorID, revision int64, input SaveMailSettingsInput, now time.Time) (MailSettings, error) {
	if administratorID < 1 || revision < 1 {
		return MailSettings{}, ErrInvalidInput
	}
	normalized, err := normalizeMailSettings(input)
	if err != nil {
		return MailSettings{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MailSettings{}, fmt.Errorf("begin update mail settings: %w", err)
	}
	defer tx.Rollback()
	if !normalized.SMTPEnabled {
		if err := ensureSMTPCanBeDisabled(ctx, tx); err != nil {
			return MailSettings{}, err
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE app_settings
		SET smtp_enabled = ?, smtp_host = ?, smtp_port = ?, smtp_username = ?,
		    smtp_password_cipher = CASE WHEN ? THEN ? ELSE smtp_password_cipher END,
		    smtp_encryption = ?, smtp_from_address = ?, remind_mail_enable = ?,
		    updated_by = ?, updated_at = ?, revision = revision + 1
		WHERE id = 1 AND revision = ?
	`, normalized.SMTPEnabled, normalized.SMTPHost, normalized.SMTPPort, normalized.SMTPUsername,
		normalized.ReplaceSMTPPassword, nullableBytes(normalized.SMTPPasswordCipher), normalized.SMTPEncryption,
		normalized.SMTPFromAddress, normalized.RemindMailEnabled, administratorID, now.Unix(), revision)
	if err != nil {
		return MailSettings{}, fmt.Errorf("update mail settings: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return MailSettings{}, ErrConflict
	}
	if !normalized.SMTPEnabled {
		if err := cancelAllUnclaimedMailTx(ctx, tx, now); err != nil {
			return MailSettings{}, err
		}
	} else if !normalized.RemindMailEnabled {
		if err := cancelUnclaimedSubscriptionRemindersTx(ctx, tx, now, "cancelled because subscription reminders were disabled"); err != nil {
			return MailSettings{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return MailSettings{}, fmt.Errorf("commit mail settings: %w", err)
	}
	return s.GetMailSettings(ctx)
}

func normalizeMailSettings(input SaveMailSettingsInput) (SaveMailSettingsInput, error) {
	if input.RemindMailEnabled && !input.SMTPEnabled {
		return SaveMailSettingsInput{}, ErrInvalidInput
	}
	normalized, err := normalizeSMTPSettings(smtpSettingsInput{
		Enabled: input.SMTPEnabled, Host: input.SMTPHost, Port: input.SMTPPort, Username: input.SMTPUsername,
		PasswordCipher: input.SMTPPasswordCipher, Encryption: input.SMTPEncryption, FromAddress: input.SMTPFromAddress,
	})
	if err != nil {
		return SaveMailSettingsInput{}, err
	}
	input.SMTPHost, input.SMTPPort, input.SMTPUsername = normalized.Host, normalized.Port, normalized.Username
	input.SMTPEncryption, input.SMTPFromAddress = normalized.Encryption, normalized.FromAddress
	return input, nil
}

func ensureSMTPCanBeDisabled(ctx context.Context, tx *sql.Tx) error {
	var emailVerificationEnabled, mailLoginEnabled bool
	if err := tx.QueryRowContext(ctx, `
		SELECT email_verify, login_with_mail_link_enable FROM app_settings WHERE id = 1
	`).Scan(&emailVerificationEnabled, &mailLoginEnabled); err != nil {
		return fmt.Errorf("read SMTP dependent settings: %w", err)
	}
	if emailVerificationEnabled {
		return ErrRegistrationEmailVerificationNeedsMail
	}
	if mailLoginEnabled {
		return ErrMailLoginNeedsMail
	}
	return nil
}

func cancelAllUnclaimedMailTx(ctx context.Context, tx *sql.Tx, now time.Time) error {
	statements := []struct {
		query string
		name  string
	}{
		{`UPDATE ticket_mail_outbox SET failed_at = ?, last_error = 'cancelled because SMTP notifications were disabled', updated_at = ? WHERE sent_at IS NULL AND failed_at IS NULL AND claim_token IS NULL`, "ticket mail"},
		{`UPDATE password_reset_mail_outbox SET cancelled_at = ?, code_cipher = NULL, last_error = 'cancelled because SMTP notifications were disabled', updated_at = ? WHERE sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL AND claim_token IS NULL`, "password reset mail"},
		{`UPDATE registration_email_mail_outbox SET cancelled_at = ?, code_cipher = NULL, last_error = 'cancelled because SMTP notifications were disabled', updated_at = ? WHERE sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL AND claim_token IS NULL`, "registration verification mail"},
		{`UPDATE login_link_mail_outbox SET cancelled_at = ?, token_cipher = NULL, last_error = 'cancelled because SMTP notifications were disabled', updated_at = ? WHERE sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL AND claim_token IS NULL`, "login link mail"},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, now.Unix(), now.Unix()); err != nil {
			return fmt.Errorf("cancel disabled %s: %w", statement.name, err)
		}
	}
	return cancelUnclaimedSubscriptionRemindersTx(ctx, tx, now, "cancelled because SMTP notifications were disabled")
}

func cancelUnclaimedSubscriptionRemindersTx(ctx context.Context, tx *sql.Tx, now time.Time, reason string) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE subscription_reminder_outbox
		SET cancelled_at = ?, last_error = ?, updated_at = ?
		WHERE sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL AND claim_token IS NULL
	`, now.Unix(), reason, now.Unix()); err != nil {
		return fmt.Errorf("cancel subscription reminders: %w", err)
	}
	return nil
}

func cancelSubscriptionRemindersForUserChangeTx(ctx context.Context, tx *sql.Tx, userID int64, recipient string, banned, remindExpire, remindTraffic bool, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE subscription_reminder_outbox
		SET cancelled_at = ?, last_error = 'cancelled because user reminder delivery settings changed', updated_at = ?
		WHERE user_id = ? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL AND claim_token IS NULL
		  AND (recipient <> ? OR ? OR (kind = 'expire' AND NOT ?) OR (kind = 'traffic' AND NOT ?))
	`, now.Unix(), now.Unix(), userID, recipient, banned, remindExpire, remindTraffic); err != nil {
		return fmt.Errorf("cancel changed user subscription reminders: %w", err)
	}
	return nil
}

func (s *Store) ScheduleSubscriptionReminders(ctx context.Context, now time.Time, reminderDay string, batchSize int) (SubscriptionReminderScheduleResult, error) {
	if batchSize < 1 || batchSize > maxSubscriptionReminderBatch {
		return SubscriptionReminderScheduleResult{}, ErrInvalidInput
	}
	parsedDay, err := time.Parse("2006-01-02", reminderDay)
	if err != nil || parsedDay.Format("2006-01-02") != reminderDay {
		return SubscriptionReminderScheduleResult{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubscriptionReminderScheduleResult{}, fmt.Errorf("begin schedule subscription reminders: %w", err)
	}
	defer tx.Rollback()
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT smtp_enabled AND remind_mail_enable FROM app_settings WHERE id = 1`).Scan(&enabled); err != nil {
		return SubscriptionReminderScheduleResult{}, fmt.Errorf("read subscription reminder setting: %w", err)
	}
	if !enabled {
		return SubscriptionReminderScheduleResult{}, nil
	}
	createdAt := now.Unix()
	expireResult, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO subscription_reminder_outbox (
			user_id, kind, reminder_day, recipient, app_name, app_url, available_at, created_at, updated_at
		)
		SELECT u.id, 'expire', ?, u.email, s.app_name, s.app_url, ?, ?, ?
		FROM users u CROSS JOIN app_settings s
		WHERE s.id = 1 AND s.smtp_enabled = 1 AND s.remind_mail_enable = 1
		  AND u.banned = 0 AND u.remind_expire = 1 AND u.email <> ''
		  AND u.expired_at IS NOT NULL AND u.expired_at > ? AND u.expired_at < ?
		  AND NOT EXISTS (
			SELECT 1 FROM subscription_reminder_outbox queued
			WHERE queued.user_id = u.id AND queued.kind = 'expire' AND queued.reminder_day = ?
		  )
		ORDER BY u.id LIMIT ?
	`, reminderDay, createdAt, createdAt, createdAt, createdAt, now.Add(24*time.Hour).Unix(), reminderDay, batchSize)
	if err != nil {
		return SubscriptionReminderScheduleResult{}, fmt.Errorf("schedule expiry reminders: %w", err)
	}
	trafficResult, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO subscription_reminder_outbox (
			user_id, kind, reminder_day, recipient, app_name, app_url, available_at, created_at, updated_at
		)
		SELECT u.id, 'traffic', ?, u.email, s.app_name, s.app_url, ?, ?, ?
		FROM users u CROSS JOIN app_settings s
		WHERE s.id = 1 AND s.smtp_enabled = 1 AND s.remind_mail_enable = 1
		  AND u.banned = 0 AND u.remind_traffic = 1 AND u.email <> '' AND u.transfer_enable > 0
		  AND (u.traffic_u + u.traffic_d) >= u.transfer_enable - u.transfer_enable / 5
		  AND (u.traffic_u + u.traffic_d) < u.transfer_enable
		  AND NOT EXISTS (
			SELECT 1 FROM subscription_reminder_outbox queued
			WHERE queued.user_id = u.id AND queued.kind = 'traffic' AND queued.reminder_day = ?
		  )
		ORDER BY u.id LIMIT ?
	`, reminderDay, createdAt, createdAt, createdAt, reminderDay, batchSize)
	if err != nil {
		return SubscriptionReminderScheduleResult{}, fmt.Errorf("schedule traffic reminders: %w", err)
	}
	expireQueued, _ := expireResult.RowsAffected()
	trafficQueued, _ := trafficResult.RowsAffected()
	if err := tx.Commit(); err != nil {
		return SubscriptionReminderScheduleResult{}, fmt.Errorf("commit subscription reminders: %w", err)
	}
	return SubscriptionReminderScheduleResult{ExpireQueued: expireQueued, TrafficQueued: trafficQueued}, nil
}

func (s *Store) ClaimSubscriptionReminder(ctx context.Context, claimToken string, now time.Time, lease time.Duration) (SubscriptionReminderJob, bool, error) {
	claimToken = strings.TrimSpace(claimToken)
	if claimToken == "" || len(claimToken) > maxReminderClaimToken || lease < time.Second || lease > time.Hour {
		return SubscriptionReminderJob{}, false, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubscriptionReminderJob{}, false, fmt.Errorf("begin claim subscription reminder: %w", err)
	}
	defer tx.Rollback()
	var id int64
	staleBefore := now.Add(-lease).Unix()
	err = tx.QueryRowContext(ctx, `
		SELECT o.id FROM subscription_reminder_outbox o CROSS JOIN app_settings s
		WHERE s.id = 1 AND s.smtp_enabled = 1 AND s.remind_mail_enable = 1
		  AND o.sent_at IS NULL AND o.failed_at IS NULL AND o.cancelled_at IS NULL
		  AND o.attempt_count < 3 AND o.available_at <= ?
		  AND (o.claimed_at IS NULL OR o.claimed_at <= ?)
		ORDER BY o.available_at, o.id LIMIT 1
	`, now.Unix(), staleBefore).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return SubscriptionReminderJob{}, false, nil
	}
	if err != nil {
		return SubscriptionReminderJob{}, false, fmt.Errorf("select subscription reminder: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE subscription_reminder_outbox
		SET claim_token = ?, claimed_at = ?, attempt_count = attempt_count + 1, updated_at = ?
		WHERE id = ? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
		  AND attempt_count < 3 AND available_at <= ? AND (claimed_at IS NULL OR claimed_at <= ?)
	`, claimToken, now.Unix(), now.Unix(), id, now.Unix(), staleBefore)
	if err != nil {
		return SubscriptionReminderJob{}, false, fmt.Errorf("claim subscription reminder: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return SubscriptionReminderJob{}, false, nil
	}
	var job SubscriptionReminderJob
	if err := tx.QueryRowContext(ctx, `
		SELECT o.id, o.attempt_count, o.kind, o.recipient, o.app_name, o.app_url,
		       s.smtp_host, s.smtp_port, s.smtp_username, s.smtp_password_cipher,
		       s.smtp_encryption, s.smtp_from_address
		FROM subscription_reminder_outbox o CROSS JOIN app_settings s
		WHERE o.id = ? AND o.claim_token = ? AND s.id = 1
	`, id, claimToken).Scan(&job.ID, &job.Attempt, &job.Kind, &job.Recipient, &job.AppName, &job.AppURL,
		&job.SMTPHost, &job.SMTPPort, &job.SMTPUsername, &job.SMTPPasswordCipher,
		&job.SMTPEncryption, &job.SMTPFromAddress); err != nil {
		return SubscriptionReminderJob{}, false, fmt.Errorf("read claimed subscription reminder: %w", err)
	}
	job.SMTPPasswordCipher = append([]byte(nil), job.SMTPPasswordCipher...)
	if err := tx.Commit(); err != nil {
		return SubscriptionReminderJob{}, false, fmt.Errorf("commit subscription reminder claim: %w", err)
	}
	return job, true, nil
}

func (s *Store) CompleteSubscriptionReminder(ctx context.Context, jobID int64, claimToken string, now time.Time) error {
	if jobID < 1 || strings.TrimSpace(claimToken) == "" {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE subscription_reminder_outbox
		SET sent_at = ?, claim_token = NULL, claimed_at = NULL, last_error = NULL, updated_at = ?
		WHERE id = ? AND claim_token = ? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
	`, now.Unix(), now.Unix(), jobID, claimToken)
	if err != nil {
		return fmt.Errorf("complete subscription reminder: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) FailSubscriptionReminder(ctx context.Context, jobID int64, claimToken, failure string, retryAt, now time.Time) error {
	if jobID < 1 || strings.TrimSpace(claimToken) == "" || retryAt.Before(now) {
		return ErrInvalidInput
	}
	failure = truncateRunes(strings.TrimSpace(failure), maxReminderErrorRunes)
	if failure == "" {
		failure = "mail delivery failed"
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE subscription_reminder_outbox
		SET available_at = CASE WHEN attempt_count < ? THEN ? ELSE available_at END,
		    failed_at = CASE WHEN attempt_count >= ? OR NOT (SELECT smtp_enabled AND remind_mail_enable FROM app_settings WHERE id = 1) THEN ? ELSE NULL END,
		    claim_token = NULL, claimed_at = NULL, last_error = ?, updated_at = ?
		WHERE id = ? AND claim_token = ? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
	`, maxReminderAttempts, retryAt.Unix(), maxReminderAttempts, now.Unix(), failure, now.Unix(), jobID, claimToken)
	if err != nil {
		return fmt.Errorf("fail subscription reminder: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrConflict
	}
	return nil
}
