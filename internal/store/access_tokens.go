package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func (s *Store) CreateAccessToken(ctx context.Context, input CreateAccessTokenInput, now time.Time) (AccountAccessToken, error) {
	input, expiresAt, err := prepareAccessTokenInput(input, now)
	if err != nil {
		return AccountAccessToken{}, err
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO access_tokens (user_id, token_hash, name, expires_at, created_at, updated_at)
		SELECT id, ?, ?, ?, ?, ? FROM users WHERE id = ? AND account_kind = ? AND banned = 0
	`, input.TokenHash, input.Name, expiresAt, now.Unix(), now.Unix(), input.UserID, AccountKindHuman)
	if err != nil {
		return AccountAccessToken{}, fmt.Errorf("create access token: %w", err)
	}
	created, err := result.RowsAffected()
	if err != nil {
		return AccountAccessToken{}, fmt.Errorf("count created access tokens: %w", err)
	}
	if created != 1 {
		return AccountAccessToken{}, ErrNotFound
	}
	id, err := result.LastInsertId()
	if err != nil {
		return AccountAccessToken{}, fmt.Errorf("read access token id: %w", err)
	}
	token := AccountAccessToken{ID: id, UserID: input.UserID, Name: input.Name, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	if input.ExpiresAt != nil {
		value := input.ExpiresAt.UTC()
		token.ExpiresAt = &value
	}
	return token, nil
}

// CompletePasswordLoginAndCreateAccessToken is the bearer-token equivalent of
// CompletePasswordLoginAndCreateSession and preserves the same reset ordering.
func (s *Store) CompletePasswordLoginAndCreateAccessToken(ctx context.Context, userID int64, expectedEmail, expectedHash, replacementHash string, input CreateAccessTokenInput, now time.Time) (AccountAccessToken, error) {
	input.UserID = userID
	input, expiresAt, err := prepareAccessTokenInput(input, now)
	if err != nil || expectedEmail == "" || normalizeEmail(expectedEmail) != expectedEmail || expectedHash == "" || replacementHash == "" {
		if err != nil {
			return AccountAccessToken{}, err
		}
		return AccountAccessToken{}, fmt.Errorf("%w: invalid password login access token", ErrInvalidInput)
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountAccessToken{}, fmt.Errorf("begin password login access token: %w", err)
	}
	defer tx.Rollback()
	if err := completePasswordLoginTx(ctx, tx, userID, expectedEmail, expectedHash, replacementHash, now); err != nil {
		return AccountAccessToken{}, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO access_tokens (user_id, token_hash, name, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, userID, input.TokenHash, input.Name, expiresAt, now.Unix(), now.Unix())
	if err != nil {
		return AccountAccessToken{}, fmt.Errorf("create password login access token: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return AccountAccessToken{}, fmt.Errorf("read password login access token id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AccountAccessToken{}, fmt.Errorf("commit password login access token: %w", err)
	}
	token := AccountAccessToken{ID: id, UserID: userID, Name: input.Name, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	if input.ExpiresAt != nil {
		value := input.ExpiresAt.UTC()
		token.ExpiresAt = &value
	}
	return token, nil
}

func prepareAccessTokenInput(input CreateAccessTokenInput, now time.Time) (CreateAccessTokenInput, any, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.UserID < 1 || !validAccessTokenHash(input.TokenHash) || input.Name == "" ||
		!utf8.ValidString(input.Name) || utf8.RuneCountInString(input.Name) > 80 ||
		strings.IndexFunc(input.Name, unicode.IsControl) >= 0 || now.IsZero() ||
		(input.ExpiresAt != nil && !input.ExpiresAt.After(now)) {
		return CreateAccessTokenInput{}, nil, fmt.Errorf("%w: invalid access token", ErrInvalidInput)
	}
	var expiresAt any
	if input.ExpiresAt != nil {
		expiresAt = input.ExpiresAt.Unix()
	}
	return input, expiresAt, nil
}

func (s *Store) AuthenticateAccessToken(ctx context.Context, tokenHash string, now time.Time) (SessionUser, error) {
	if !validAccessTokenHash(tokenHash) || now.IsZero() {
		return SessionUser{}, ErrNotFound
	}
	var session SessionUser
	var expiresAt, lastUsedAt sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT a.id, u.id, u.email, u.is_admin, u.banned, a.expires_at, a.last_used_at
		FROM access_tokens a
		JOIN users u ON u.id = a.user_id
		WHERE a.token_hash = ? AND a.revoked_at IS NULL
		  AND (a.expires_at IS NULL OR a.expires_at > ?)
		  AND u.account_kind = ? AND u.banned = 0
	`, tokenHash, now.Unix(), AccountKindHuman).Scan(
		&session.SessionID, &session.UserID, &session.Email, &session.IsAdmin, &session.Banned, &expiresAt, &lastUsedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionUser{}, ErrNotFound
	}
	if err != nil {
		return SessionUser{}, fmt.Errorf("authenticate access token: %w", err)
	}
	session.CredentialKind = CredentialKindAccessToken
	if expiresAt.Valid {
		session.ExpiresAt = time.Unix(expiresAt.Int64, 0).UTC()
	}
	if lastUsedAt.Valid {
		value := time.Unix(lastUsedAt.Int64, 0).UTC()
		session.LastUsedAt = &value
	}
	if session.LastUsedAt == nil || now.Sub(*session.LastUsedAt) >= time.Minute {
		unlock := s.lockWrite()
		_, _ = s.db.ExecContext(ctx, `
			UPDATE access_tokens SET last_used_at = ?, updated_at = ?
			WHERE id = ? AND revoked_at IS NULL AND (last_used_at IS NULL OR last_used_at <= ?)
		`, now.Unix(), now.Unix(), session.SessionID, now.Add(-time.Minute).Unix())
		unlock()
		value := now.UTC()
		session.LastUsedAt = &value
	}
	return session, nil
}

func (s *Store) ListActiveAccessTokens(ctx context.Context, userID, currentTokenID int64, now time.Time) ([]AccountAccessToken, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, created_at, updated_at, last_used_at, expires_at
		FROM access_tokens
		WHERE user_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)
		ORDER BY created_at DESC, id DESC
	`, userID, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("list active access tokens: %w", err)
	}
	defer rows.Close()
	tokens := make([]AccountAccessToken, 0)
	for rows.Next() {
		var token AccountAccessToken
		var createdAt, updatedAt int64
		var lastUsedAt, expiresAt sql.NullInt64
		if err := rows.Scan(&token.ID, &token.Name, &createdAt, &updatedAt, &lastUsedAt, &expiresAt); err != nil {
			return nil, fmt.Errorf("scan active access token: %w", err)
		}
		token.UserID = userID
		token.IsCurrent = token.ID == currentTokenID
		token.CreatedAt = time.Unix(createdAt, 0).UTC()
		token.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		if lastUsedAt.Valid {
			value := time.Unix(lastUsedAt.Int64, 0).UTC()
			token.LastUsedAt = &value
		}
		if expiresAt.Valid {
			value := time.Unix(expiresAt.Int64, 0).UTC()
			token.ExpiresAt = &value
		}
		tokens = append(tokens, token)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active access tokens: %w", err)
	}
	return tokens, nil
}

func (s *Store) RevokeAccessToken(ctx context.Context, tokenID int64, now time.Time) error {
	defer s.lockWrite()()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE access_tokens SET revoked_at = ?, updated_at = ? WHERE id = ? AND revoked_at IS NULL
	`, now.Unix(), now.Unix(), tokenID); err != nil {
		return fmt.Errorf("revoke access token: %w", err)
	}
	return nil
}

func (s *Store) RevokeUserAccessToken(ctx context.Context, userID, tokenID int64, now time.Time) error {
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE access_tokens SET revoked_at = ?, updated_at = ?
		WHERE id = ? AND user_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)
	`, now.Unix(), now.Unix(), tokenID, userID, now.Unix())
	if err != nil {
		return fmt.Errorf("revoke user access token: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count revoked user access tokens: %w", err)
	}
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RevokeAllUserAccessTokens(ctx context.Context, userID int64, now time.Time) error {
	defer s.lockWrite()()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE access_tokens SET revoked_at = ?, updated_at = ? WHERE user_id = ? AND revoked_at IS NULL
	`, now.Unix(), now.Unix(), userID); err != nil {
		return fmt.Errorf("revoke all user access tokens: %w", err)
	}
	return nil
}

func revokeAllCredentialsTx(ctx context.Context, tx *sql.Tx, userID int64, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL
	`, now.Unix(), userID); err != nil {
		return fmt.Errorf("revoke cookie sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE access_tokens SET revoked_at = ?, updated_at = ? WHERE user_id = ? AND revoked_at IS NULL
	`, now.Unix(), now.Unix(), userID); err != nil {
		return fmt.Errorf("revoke access tokens: %w", err)
	}
	return nil
}

func validAccessTokenHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
