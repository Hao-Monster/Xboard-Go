package legacymigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	maxLegacyAccessTokenRows      = 2_000_000
	maxLegacyAccessTokenDataBytes = int64(512 << 20)
	legacyUserTokenableType       = `App\Models\User`
	legacyWildcardAbilities       = `["*"]`
)

type AccessTokensSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Tokens   []store.LegacyAccessToken
	Checksum string
}

func ReadAccessTokensSnapshot(ctx context.Context, sourcePath string) (AccessTokensSnapshot, error) {
	tokens := []store.LegacyAccessToken{}
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "personal_access_tokens", []string{
			"id", "tokenable_type", "tokenable_id", "name", "token", "abilities", "last_used_at",
			"expires_at", "created_at", "updated_at",
		}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(
				length(CAST(tokenable_type AS BLOB)) + length(CAST(name AS BLOB)) +
				length(CAST(token AS BLOB)) + COALESCE(length(CAST(abilities AS BLOB)), 0)
			), 0)
			FROM personal_access_tokens
		`, maxLegacyAccessTokenRows, maxLegacyAccessTokenDataBytes, "legacy access tokens"); err != nil {
			return err
		}
		rows, err := database.QueryContext(ctx, `
			SELECT id, tokenable_type, tokenable_id, name, token, abilities,
			       `+legacyUnixExpression("last_used_at")+`, `+legacyUnixExpression("expires_at")+`,
			       `+legacyUnixExpression("created_at")+`, `+legacyUnixExpression("updated_at")+`
			FROM personal_access_tokens ORDER BY id
		`)
		if err != nil {
			return fmt.Errorf("read legacy access tokens: %w", err)
		}
		defer rows.Close()
		var bytesRead int64
		for rows.Next() {
			if len(tokens) >= maxLegacyAccessTokenRows {
				return fmt.Errorf("legacy access tokens exceed the %d-row migration limit", maxLegacyAccessTokenRows)
			}
			var token store.LegacyAccessToken
			var tokenableType, abilities string
			var lastUsedAt, expiresAt sql.NullInt64
			if err := rows.Scan(&token.ID, &tokenableType, &token.UserID, &token.Name, &token.TokenHash,
				&abilities, &lastUsedAt, &expiresAt, &token.CreatedAt, &token.UpdatedAt); err != nil {
				return fmt.Errorf("scan legacy access token: %w", err)
			}
			if tokenableType != legacyUserTokenableType {
				return fmt.Errorf("legacy access token id %d has unsupported tokenable type %q", token.ID, tokenableType)
			}
			if abilities != legacyWildcardAbilities {
				return fmt.Errorf("legacy access token id %d has unsupported abilities", token.ID)
			}
			token.LastUsedAt = nullableLegacyUnixPointer(lastUsedAt)
			token.ExpiresAt = nullableLegacyUnixPointer(expiresAt)
			bytesRead += int64(len(tokenableType) + len(token.Name) + len(token.TokenHash) + len(abilities))
			if bytesRead > maxLegacyAccessTokenDataBytes {
				return errors.New("legacy access tokens exceed the migration data limit")
			}
			tokens = append(tokens, token)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate legacy access tokens: %w", err)
		}
		return store.ValidateLegacyAccessTokensData(tokens)
	})
	if err != nil {
		return AccessTokensSnapshot{}, err
	}
	return AccessTokensSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Tokens: tokens, Checksum: store.LegacyAccessTokensChecksum(tokens),
	}, nil
}

func nullableLegacyUnixPointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}
