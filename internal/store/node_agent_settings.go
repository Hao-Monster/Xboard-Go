package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
)

const (
	minimumNodeAgentTokenLength = 16
	maximumNodeAgentTokenLength = 256
)

// EnsureNodeAgentSettings establishes deployment defaults only for a new
// database. Once created, the row belongs to the administrator and process
// restarts must not overwrite it.
func (s *Store) EnsureNodeAgentSettings(ctx context.Context, defaults NodeAgentSettingsDefaults, now time.Time) error {
	if err := validateNodeAgentSettings(defaults.PullInterval, defaults.PushInterval, defaults.DeviceLimitMode, defaults.WebSocketURL); err != nil {
		return err
	}
	defer s.lockWrite()()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO node_agent_settings (
			id, revision, server_token_hash, server_token_prefix, pull_interval, push_interval,
			device_limit_mode, websocket_enabled, websocket_url, updated_by, updated_at
		) VALUES (1, 1, NULL, '', ?, ?, ?, ?, ?, NULL, ?)
		ON CONFLICT(id) DO NOTHING
	`, defaults.PullInterval, defaults.PushInterval, defaults.DeviceLimitMode, defaults.WebSocketEnabled, defaults.WebSocketURL, now.Unix()); err != nil {
		return fmt.Errorf("ensure node agent settings: %w", err)
	}
	return nil
}

func (s *Store) GetNodeAgentSettings(ctx context.Context) (NodeAgentSettings, error) {
	return readNodeAgentSettings(ctx, s.db)
}

func (s *Store) UpdateNodeAgentSettings(ctx context.Context, input UpdateNodeAgentSettingsInput, now time.Time) (NodeAgentSettings, error) {
	if input.Revision < 1 {
		return NodeAgentSettings{}, fmt.Errorf("%w: node agent revision must be positive", ErrInvalidInput)
	}
	if err := validateNodeAgentSettings(input.PullInterval, input.PushInterval, input.DeviceLimitMode, input.WebSocketURL); err != nil {
		return NodeAgentSettings{}, err
	}
	if input.UpdatedBy == nil {
		if input.Audit != nil {
			return NodeAgentSettings{}, fmt.Errorf("%w: node agent audit requires an administrator", ErrInvalidInput)
		}
	} else if input.Audit == nil || input.Audit.AdministratorID != *input.UpdatedBy {
		return NodeAgentSettings{}, fmt.Errorf("%w: administrator update requires a matching audit", ErrInvalidInput)
	}

	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NodeAgentSettings{}, fmt.Errorf("begin update node agent settings: %w", err)
	}
	defer tx.Rollback()

	var tokenHash sql.NullString
	var tokenPrefix string
	if err := tx.QueryRowContext(ctx, `SELECT server_token_hash, server_token_prefix FROM node_agent_settings WHERE id = 1`).Scan(&tokenHash, &tokenPrefix); errors.Is(err, sql.ErrNoRows) {
		return NodeAgentSettings{}, ErrNotFound
	} else if err != nil {
		return NodeAgentSettings{}, fmt.Errorf("read node agent token metadata: %w", err)
	}
	if input.ServerToken != nil {
		if *input.ServerToken == "" {
			tokenHash, tokenPrefix = sql.NullString{}, ""
		} else {
			if !validNodeAgentToken(*input.ServerToken) {
				return NodeAgentSettings{}, fmt.Errorf("%w: server token must contain 16-256 printable ASCII characters without whitespace", ErrInvalidInput)
			}
			tokenHash = sql.NullString{String: security.DigestToken(*input.ServerToken), Valid: true}
			tokenPrefix = (*input.ServerToken)[:min(8, len(*input.ServerToken))]
		}
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE node_agent_settings SET
			revision = revision + 1, server_token_hash = ?, server_token_prefix = ?,
			pull_interval = ?, push_interval = ?, device_limit_mode = ?, websocket_enabled = ?, websocket_url = ?,
			updated_by = ?, updated_at = ?
		WHERE id = 1 AND revision = ?
	`, nullableNodeAgentTokenHash(tokenHash), tokenPrefix, input.PullInterval, input.PushInterval, input.DeviceLimitMode,
		input.WebSocketEnabled, input.WebSocketURL, input.UpdatedBy, now.Unix(), input.Revision)
	if err != nil {
		return NodeAgentSettings{}, fmt.Errorf("update node agent settings: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return NodeAgentSettings{}, fmt.Errorf("read node agent settings update: %w", err)
	}
	if updated != 1 {
		return NodeAgentSettings{}, ErrConflict
	}
	if input.Audit != nil {
		if err := insertAdminAudit(ctx, tx, *input.Audit, now); err != nil {
			return NodeAgentSettings{}, fmt.Errorf("record node agent settings audit: %w", err)
		}
	}
	settings, err := readNodeAgentSettings(ctx, tx)
	if err != nil {
		return NodeAgentSettings{}, err
	}
	if err := tx.Commit(); err != nil {
		return NodeAgentSettings{}, fmt.Errorf("commit node agent settings: %w", err)
	}
	return settings, nil
}

func (s *Store) AuthenticateLegacyNodeToken(ctx context.Context, token string) (bool, error) {
	if !validNodeAgentToken(token) {
		return false, nil
	}
	var expected sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT server_token_hash FROM node_agent_settings WHERE id = 1`).Scan(&expected); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("read legacy node token digest: %w", err)
	}
	if !expected.Valid {
		return false, nil
	}
	actual := security.DigestToken(token)
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected.String)) == 1, nil
}

type nodeAgentSettingsQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readNodeAgentSettings(ctx context.Context, database nodeAgentSettingsQueryer) (NodeAgentSettings, error) {
	var settings NodeAgentSettings
	var configured, websocketEnabled bool
	var updatedAt int64
	var updatedBy sql.NullInt64
	err := database.QueryRowContext(ctx, `
		SELECT revision, server_token_hash IS NOT NULL, server_token_prefix, pull_interval, push_interval,
		       device_limit_mode, websocket_enabled, websocket_url, updated_by, updated_at
		FROM node_agent_settings WHERE id = 1
	`).Scan(&settings.Revision, &configured, &settings.ServerTokenPrefix, &settings.PullInterval, &settings.PushInterval,
		&settings.DeviceLimitMode, &websocketEnabled, &settings.WebSocketURL, &updatedBy, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return NodeAgentSettings{}, ErrNotFound
	}
	if err != nil {
		return NodeAgentSettings{}, fmt.Errorf("get node agent settings: %w", err)
	}
	settings.ServerTokenConfigured = configured
	settings.WebSocketEnabled = websocketEnabled
	if updatedBy.Valid {
		settings.UpdatedBy = &updatedBy.Int64
	}
	settings.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return settings, nil
}

func validateNodeAgentSettings(pullInterval, pushInterval, deviceLimitMode int, websocketURL string) error {
	if pullInterval < 1 || pullInterval > 3600 || pushInterval < 1 || pushInterval > 3600 {
		return fmt.Errorf("%w: node agent intervals must be between 1 and 3600 seconds", ErrInvalidInput)
	}
	if deviceLimitMode != 0 && deviceLimitMode != 1 {
		return fmt.Errorf("%w: device limit mode must be 0 or 1", ErrInvalidInput)
	}
	if websocketURL == "" {
		return nil
	}
	if len([]byte(websocketURL)) > 2048 {
		return fmt.Errorf("%w: websocket URL is too long", ErrInvalidInput)
	}
	parsed, err := url.Parse(websocketURL)
	if err != nil || (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return fmt.Errorf("%w: websocket URL must be an absolute ws/wss URL without credentials, query, or fragment", ErrInvalidInput)
	}
	return nil
}

func validNodeAgentToken(token string) bool {
	if len(token) < minimumNodeAgentTokenLength || len(token) > maximumNodeAgentTokenLength {
		return false
	}
	for _, character := range []byte(token) {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return !strings.ContainsAny(token, " \t\r\n")
}

func nullableNodeAgentTokenHash(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}
