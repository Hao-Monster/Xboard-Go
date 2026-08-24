package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) BootstrapAdmin(ctx context.Context, email, passwordHash string, now time.Time) (bool, error) {
	defer s.lockWrite()()
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || passwordHash == "" {
		return false, fmt.Errorf("%w: email and password hash are required", ErrInvalidInput)
	}

	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE account_kind = 'human'`).Scan(&count); err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return false, nil
	}
	subscriptionToken, err := newSubscriptionToken()
	if err != nil {
		return false, err
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO users (email, password_hash, is_admin, banned, subscription_token, created_at, updated_at)
		VALUES (?, ?, 1, 0, ?, ?, ?)
	`, email, passwordHash, subscriptionToken, now.Unix(), now.Unix())
	if err != nil {
		return false, fmt.Errorf("bootstrap admin: %w", err)
	}
	return true, nil
}

func (s *Store) FindUserByEmail(ctx context.Context, email string) (User, error) {
	var user User
	err := s.db.QueryRowContext(ctx, `
		SELECT id, email, password_hash, is_admin, banned, account_kind
		FROM users
		WHERE email = ? COLLATE NOCASE
	`, strings.TrimSpace(email)).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.IsAdmin, &user.Banned, &user.AccountKind)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("find user: %w", err)
	}
	return user, nil
}

func (s *Store) FindUserByID(ctx context.Context, userID int64) (User, error) {
	var user User
	err := s.db.QueryRowContext(ctx, `
		SELECT id, email, password_hash, is_admin, banned, account_kind
		FROM users
		WHERE id = ?
	`, userID).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.IsAdmin, &user.Banned, &user.AccountKind)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("find user by ID: %w", err)
	}
	return user, nil
}

func (s *Store) CreateSession(ctx context.Context, userID int64, tokenHash, csrfHash string, expiresAt, now time.Time) error {
	defer s.lockWrite()()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO admin_sessions (user_id, token_hash, csrf_hash, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, userID, tokenHash, csrfHash, expiresAt.Unix(), now.Unix())
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *Store) AuthenticateSession(ctx context.Context, tokenHash string, now time.Time) (SessionUser, error) {
	var session SessionUser
	var expiresAt int64
	var lastUsed sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, u.id, u.email, u.is_admin, u.banned, s.csrf_hash, s.expires_at, s.last_used_at
		FROM admin_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.revoked_at IS NULL AND s.expires_at > ? AND u.account_kind = 'human'
	`, tokenHash, now.Unix()).Scan(
		&session.SessionID,
		&session.UserID,
		&session.Email,
		&session.IsAdmin,
		&session.Banned,
		&session.CSRFHash,
		&expiresAt,
		&lastUsed,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionUser{}, ErrNotFound
	}
	if err != nil {
		return SessionUser{}, fmt.Errorf("authenticate session: %w", err)
	}
	if session.Banned {
		return SessionUser{}, ErrNotFound
	}
	session.ExpiresAt = time.Unix(expiresAt, 0).UTC()
	if lastUsed.Valid {
		value := time.Unix(lastUsed.Int64, 0).UTC()
		session.LastUsedAt = &value
	}
	if session.LastUsedAt == nil || now.Sub(*session.LastUsedAt) >= time.Minute {
		unlock := s.lockWrite()
		_, _ = s.db.ExecContext(ctx, `UPDATE admin_sessions SET last_used_at = ? WHERE id = ?`, now.Unix(), session.SessionID)
		unlock()
	}
	return session, nil
}

func (s *Store) RevokeSession(ctx context.Context, sessionID int64, now time.Time) error {
	defer s.lockWrite()()
	_, err := s.db.ExecContext(ctx, `UPDATE admin_sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now.Unix(), sessionID)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func (s *Store) ListActiveSessions(ctx context.Context, userID, currentSessionID int64, now time.Time) ([]AccountSession, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, created_at, last_used_at, expires_at
		FROM admin_sessions
		WHERE user_id = ? AND revoked_at IS NULL AND expires_at > ?
		ORDER BY created_at DESC, id DESC
	`, userID, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("list active sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]AccountSession, 0)
	for rows.Next() {
		var session AccountSession
		var createdAt, expiresAt int64
		var lastUsedAt sql.NullInt64
		if err := rows.Scan(&session.ID, &createdAt, &lastUsedAt, &expiresAt); err != nil {
			return nil, fmt.Errorf("scan active session: %w", err)
		}
		session.IsCurrent = session.ID == currentSessionID
		session.CreatedAt = time.Unix(createdAt, 0).UTC()
		session.ExpiresAt = time.Unix(expiresAt, 0).UTC()
		if lastUsedAt.Valid {
			value := time.Unix(lastUsedAt.Int64, 0).UTC()
			session.LastUsedAt = &value
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active sessions: %w", err)
	}
	return sessions, nil
}

func (s *Store) RevokeUserSession(ctx context.Context, userID, sessionID int64, now time.Time) error {
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE admin_sessions
		SET revoked_at = ?
		WHERE id = ? AND user_id = ? AND revoked_at IS NULL AND expires_at > ?
	`, now.Unix(), sessionID, userID, now.Unix())
	if err != nil {
		return fmt.Errorf("revoke user session: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count revoked user sessions: %w", err)
	}
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ChangePassword(ctx context.Context, userID int64, expectedHash, newHash string, now time.Time) error {
	if userID < 1 || expectedHash == "" || newHash == "" {
		return fmt.Errorf("%w: user and password hashes are required", ErrInvalidInput)
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password change: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE users SET password_hash = ?, updated_at = ?
		WHERE id = ? AND password_hash = ?
	`, newHash, now.Unix(), userID, expectedHash)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated passwords: %w", err)
	}
	if changed == 0 {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_sessions SET revoked_at = ?
		WHERE user_id = ? AND revoked_at IS NULL
	`, now.Unix(), userID); err != nil {
		return fmt.Errorf("revoke sessions after password change: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password change: %w", err)
	}
	return nil
}
