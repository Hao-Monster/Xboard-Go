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
	maxTelegramDeliveryAttempts = 3
	maxTelegramClaimTokenBytes  = 128
	maxTelegramDeliveryError    = 1_024
)

func (s *Store) ClaimTelegramMessage(ctx context.Context, claimToken string, now time.Time, lease time.Duration) (TelegramDeliveryJob, bool, error) {
	claimToken = strings.TrimSpace(claimToken)
	if claimToken == "" || len(claimToken) > maxTelegramClaimTokenBytes || now.Unix() < 0 || lease < time.Second || lease > time.Hour {
		return TelegramDeliveryJob{}, false, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TelegramDeliveryJob{}, false, fmt.Errorf("begin claim Telegram message: %w", err)
	}
	defer tx.Rollback()

	staleBefore := now.Add(-lease).Unix()
	var id int64
	err = tx.QueryRowContext(ctx, `
		SELECT o.id
		FROM telegram_message_outbox o
		CROSS JOIN app_settings s
		CROSS JOIN trusted_plugins p
		WHERE s.id=1 AND s.telegram_bot_enable=1 AND s.telegram_bot_token_cipher IS NOT NULL
		  AND p.code='telegram' AND p.enabled=1
		  AND o.sent_at IS NULL AND o.failed_at IS NULL AND o.cancelled_at IS NULL
		  AND o.attempt_count < ? AND o.available_at <= ?
		  AND (o.claimed_at IS NULL OR o.claimed_at <= ?)
		ORDER BY o.available_at,o.id LIMIT 1
	`, maxTelegramDeliveryAttempts, now.Unix(), staleBefore).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return TelegramDeliveryJob{}, false, nil
	}
	if err != nil {
		return TelegramDeliveryJob{}, false, fmt.Errorf("select Telegram message: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE telegram_message_outbox
		SET claim_token=?,claimed_at=?,attempt_count=attempt_count+1,updated_at=?
		WHERE id=? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
		  AND attempt_count < ? AND available_at <= ?
		  AND (claimed_at IS NULL OR claimed_at <= ?)
	`, claimToken, now.Unix(), now.Unix(), id, maxTelegramDeliveryAttempts, now.Unix(), staleBefore)
	if err != nil {
		return TelegramDeliveryJob{}, false, fmt.Errorf("claim Telegram message: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return TelegramDeliveryJob{}, false, nil
	}
	var job TelegramDeliveryJob
	if err := tx.QueryRowContext(ctx, `
		SELECT o.id,o.attempt_count,o.chat_id,o.text,s.telegram_bot_token_cipher
		FROM telegram_message_outbox o CROSS JOIN app_settings s
		WHERE o.id=? AND o.claim_token=? AND s.id=1
	`, id, claimToken).Scan(&job.ID, &job.Attempt, &job.ChatID, &job.Text, &job.BotTokenCipher); err != nil {
		return TelegramDeliveryJob{}, false, fmt.Errorf("read claimed Telegram message: %w", err)
	}
	job.BotTokenCipher = append([]byte(nil), job.BotTokenCipher...)
	if err := tx.Commit(); err != nil {
		return TelegramDeliveryJob{}, false, fmt.Errorf("commit Telegram message claim: %w", err)
	}
	return job, true, nil
}

func (s *Store) CompleteTelegramMessage(ctx context.Context, jobID int64, claimToken string, now time.Time) error {
	claimToken = strings.TrimSpace(claimToken)
	if jobID < 1 || claimToken == "" || now.Unix() < 0 {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE telegram_message_outbox
		SET sent_at=?,claim_token=NULL,claimed_at=NULL,last_error=NULL,updated_at=?
		WHERE id=? AND claim_token=? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
	`, now.Unix(), now.Unix(), jobID, claimToken)
	if err != nil {
		return fmt.Errorf("complete Telegram message: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) FailTelegramMessage(ctx context.Context, jobID int64, claimToken, failure string, retryAt, now time.Time) error {
	claimToken = strings.TrimSpace(claimToken)
	failure = truncateRunes(strings.TrimSpace(failure), maxTelegramDeliveryError)
	if failure == "" {
		failure = "Telegram delivery failed"
	}
	if jobID < 1 || claimToken == "" || retryAt.Before(now) || now.Unix() < 0 {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE telegram_message_outbox
		SET available_at=CASE WHEN attempt_count < ? THEN ? ELSE available_at END,
		    failed_at=CASE WHEN attempt_count >= ? THEN ? ELSE NULL END,
		    claim_token=NULL,claimed_at=NULL,last_error=?,updated_at=?
		WHERE id=? AND claim_token=? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
	`, maxTelegramDeliveryAttempts, retryAt.Unix(), maxTelegramDeliveryAttempts, now.Unix(), failure, now.Unix(), jobID, claimToken)
	if err != nil {
		return fmt.Errorf("fail Telegram message: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

func cancelPendingTelegramMessagesTx(ctx context.Context, tx *sql.Tx, reason string, now time.Time) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE telegram_message_outbox
		SET cancelled_at=?,claim_token=NULL,claimed_at=NULL,last_error=?,updated_at=?
		WHERE sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
	`, now.Unix(), reason, now.Unix())
	if err != nil {
		return fmt.Errorf("cancel pending Telegram messages: %w", err)
	}
	_, _ = result.RowsAffected()
	return nil
}
