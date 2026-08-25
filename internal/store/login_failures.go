package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (s *Store) GetLoginFailureStatus(ctx context.Context, credentialDigest []byte, now time.Time) (LoginFailureStatus, error) {
	if len(credentialDigest) != 32 || now.IsZero() {
		return LoginFailureStatus{}, fmt.Errorf("%w: invalid login failure status", ErrInvalidInput)
	}
	var status LoginFailureStatus
	var windowMinutes int
	var expiresAt sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT a.password_limit_enable, a.password_limit_count, a.password_limit_expire,
		       COALESCE(l.failure_count, 0), l.expires_at
		FROM app_settings a
		LEFT JOIN login_failure_limits l ON l.credential_digest = ?
		WHERE a.id = 1
	`, credentialDigest).Scan(&status.Enabled, &status.Maximum, &windowMinutes, &status.Failures, &expiresAt)
	if err != nil {
		return LoginFailureStatus{}, fmt.Errorf("get login failure status: %w", err)
	}
	status.Window = time.Duration(windowMinutes) * time.Minute
	if expiresAt.Valid {
		value := time.Unix(expiresAt.Int64, 0).UTC()
		status.ResetAt = &value
		if !now.Before(value) {
			status.Failures = 0
			status.ResetAt = nil
		}
	}
	status.Limited = status.Enabled && status.Failures >= status.Maximum
	return status, nil
}

func (s *Store) RecordLoginFailure(ctx context.Context, credentialDigest []byte, now time.Time) (LoginFailureStatus, error) {
	if len(credentialDigest) != 32 || now.IsZero() {
		return LoginFailureStatus{}, fmt.Errorf("%w: invalid login failure", ErrInvalidInput)
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LoginFailureStatus{}, fmt.Errorf("begin login failure update: %w", err)
	}
	defer tx.Rollback()
	var status LoginFailureStatus
	var windowMinutes int
	if err := tx.QueryRowContext(ctx, `
		SELECT password_limit_enable, password_limit_count, password_limit_expire
		FROM app_settings WHERE id = 1
	`).Scan(&status.Enabled, &status.Maximum, &windowMinutes); err != nil {
		return LoginFailureStatus{}, fmt.Errorf("read login failure settings: %w", err)
	}
	status.Window = time.Duration(windowMinutes) * time.Minute
	if !status.Enabled {
		if err := tx.Commit(); err != nil {
			return LoginFailureStatus{}, fmt.Errorf("commit disabled login failure update: %w", err)
		}
		return status, nil
	}
	expiresAt := now.Add(status.Window)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO login_failure_limits (credential_digest, failure_count, expires_at, updated_at)
		VALUES (?, 1, ?, ?)
		ON CONFLICT(credential_digest) DO UPDATE SET
			failure_count = CASE
				WHEN login_failure_limits.expires_at <= excluded.updated_at THEN 1
				ELSE login_failure_limits.failure_count + 1
			END,
			expires_at = CASE
				WHEN login_failure_limits.expires_at <= excluded.updated_at THEN excluded.expires_at
				ELSE login_failure_limits.expires_at
			END,
			updated_at = excluded.updated_at
	`, credentialDigest, expiresAt.Unix(), now.Unix()); err != nil {
		return LoginFailureStatus{}, fmt.Errorf("record login failure: %w", err)
	}
	var storedExpiry int64
	if err := tx.QueryRowContext(ctx, `
		SELECT failure_count, expires_at FROM login_failure_limits WHERE credential_digest = ?
	`, credentialDigest).Scan(&status.Failures, &storedExpiry); err != nil {
		return LoginFailureStatus{}, fmt.Errorf("read recorded login failure: %w", err)
	}
	resetAt := time.Unix(storedExpiry, 0).UTC()
	status.ResetAt = &resetAt
	status.Limited = status.Failures >= status.Maximum
	if err := tx.Commit(); err != nil {
		return LoginFailureStatus{}, fmt.Errorf("commit login failure update: %w", err)
	}
	return status, nil
}

func (s *Store) PruneExpiredLoginFailureLimits(ctx context.Context, now time.Time, limit int) (int64, error) {
	if now.IsZero() || limit < 1 || limit > 1_000 {
		return 0, fmt.Errorf("%w: invalid login failure prune", ErrInvalidInput)
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM login_failure_limits WHERE credential_digest IN (
			SELECT credential_digest FROM login_failure_limits
			WHERE expires_at <= ? ORDER BY expires_at, credential_digest LIMIT ?
		)
	`, now.Unix(), limit)
	if err != nil {
		return 0, fmt.Errorf("prune expired login failures: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count pruned login failures: %w", err)
	}
	return removed, nil
}
