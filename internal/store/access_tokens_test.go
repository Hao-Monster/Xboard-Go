package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAccessTokenLifecycleSupportsPermanentAndExplicitExpiry(t *testing.T) {
	database, user, now := newAuthTestStore(t)
	ctx := context.Background()
	expiresAt := now.Add(time.Hour)
	if _, err := database.CreateAccessToken(ctx, CreateAccessTokenInput{
		UserID: user.ID, TokenHash: strings.Repeat("f", 64), Name: "bad\tname",
	}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("CreateAccessToken(control name) error = %v, want ErrInvalidInput", err)
	}

	permanent, err := database.CreateAccessToken(ctx, CreateAccessTokenInput{
		UserID: user.ID, TokenHash: strings.Repeat("a", 64), Name: "permanent-device",
	}, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("CreateAccessToken(permanent) error = %v", err)
	}
	expiring, err := database.CreateAccessToken(ctx, CreateAccessTokenInput{
		UserID: user.ID, TokenHash: strings.Repeat("b", 64), Name: "temporary-device", ExpiresAt: &expiresAt,
	}, now)
	if err != nil {
		t.Fatalf("CreateAccessToken(expiring) error = %v", err)
	}
	if permanent.ExpiresAt != nil || expiring.ExpiresAt == nil || !expiring.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("access token expiry = permanent:%v expiring:%v", permanent.ExpiresAt, expiring.ExpiresAt)
	}

	current, err := database.AuthenticateAccessToken(ctx, strings.Repeat("a", 64), now)
	if err != nil {
		t.Fatalf("AuthenticateAccessToken(permanent) error = %v", err)
	}
	if current.CredentialKind != CredentialKindAccessToken || current.SessionID != permanent.ID {
		t.Fatalf("authenticated access token context = %#v", current)
	}
	if _, err := database.AuthenticateAccessToken(ctx, strings.Repeat("b", 64), expiresAt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AuthenticateAccessToken(at expiry) error = %v, want ErrNotFound", err)
	}
	tokens, err := database.ListActiveAccessTokens(ctx, user.ID, permanent.ID, now)
	if err != nil {
		t.Fatalf("ListActiveAccessTokens() error = %v", err)
	}
	if len(tokens) != 2 || tokens[0].ID != expiring.ID || tokens[1].ID != permanent.ID || !tokens[1].IsCurrent {
		t.Fatalf("active access tokens = %#v", tokens)
	}
	if tokens[1].LastUsedAt == nil || !tokens[1].LastUsedAt.Equal(now.UTC()) {
		t.Fatalf("last_used_at = %v, want %v", tokens[1].LastUsedAt, now.UTC())
	}
	if _, err := database.AuthenticateAccessToken(ctx, strings.Repeat("a", 64), now.Add(365*24*time.Hour)); err != nil {
		t.Fatalf("permanent access token expired: %v", err)
	}

	if err := database.RevokeUserAccessToken(ctx, user.ID, expiring.ID, now); err != nil {
		t.Fatalf("RevokeUserAccessToken() error = %v", err)
	}
	if _, err := database.AuthenticateAccessToken(ctx, strings.Repeat("b", 64), now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked access token error = %v, want ErrNotFound", err)
	}
	if err := database.RevokeAccessToken(ctx, permanent.ID, now); err != nil {
		t.Fatalf("RevokeAccessToken(current) error = %v", err)
	}
	if _, err := database.AuthenticateAccessToken(ctx, strings.Repeat("a", 64), now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("current access token after logout error = %v, want ErrNotFound", err)
	}
}

func TestAccessTokenRevocationEnforcesOwnershipAndAllTokens(t *testing.T) {
	database, owner, now := newAuthTestStore(t)
	ctx := context.Background()
	otherResult, err := database.db.ExecContext(ctx, `
		INSERT INTO users (email, password_hash, subscription_token, created_at, updated_at)
		VALUES ('access-token-other@example.test', 'hash', ?, ?, ?)
	`, testSubscriptionToken(t), now.Unix(), now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	otherID, _ := otherResult.LastInsertId()
	other, err := database.CreateAccessToken(ctx, CreateAccessTokenInput{
		UserID: otherID, TokenHash: strings.Repeat("c", 64), Name: "other-device",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.RevokeUserAccessToken(ctx, owner.ID, other.ID, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user revocation error = %v, want ErrNotFound", err)
	}
	if _, err := database.AuthenticateAccessToken(ctx, strings.Repeat("c", 64), now); err != nil {
		t.Fatalf("denied revocation changed other token: %v", err)
	}
	if _, err := database.CreateAccessToken(ctx, CreateAccessTokenInput{
		UserID: owner.ID, TokenHash: strings.Repeat("d", 64), Name: "owner-device",
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := database.RevokeAllUserAccessTokens(ctx, owner.ID, now); err != nil {
		t.Fatalf("RevokeAllUserAccessTokens() error = %v", err)
	}
	if _, err := database.AuthenticateAccessToken(ctx, strings.Repeat("d", 64), now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner token after revoke-all error = %v, want ErrNotFound", err)
	}
	if _, err := database.AuthenticateAccessToken(ctx, strings.Repeat("c", 64), now); err != nil {
		t.Fatalf("revoke-all crossed ownership: %v", err)
	}
}

func TestSchemaV21PreservesV20SessionsAndAddsAccessTokenIndexes(t *testing.T) {
	database, user, now := newAuthTestStore(t)
	ctx := context.Background()
	if _, err := database.db.ExecContext(ctx, `DROP TABLE access_tokens`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `PRAGMA user_version = 20`); err != nil {
		t.Fatal(err)
	}
	createTestSession(t, database, user.ID, "preserved-v20-session", now.Add(time.Hour), now)
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v20 to v21) error = %v", err)
	}

	var version, sessions, tables, indexes int
	checks := []struct {
		query string
		value *int
	}{
		{`PRAGMA user_version`, &version},
		{`SELECT COUNT(*) FROM admin_sessions WHERE token_hash = 'preserved-v20-session'`, &sessions},
		{`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'access_tokens'`, &tables},
		{`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name IN ('idx_access_tokens_user_active', 'idx_access_tokens_expiry')`, &indexes},
	}
	for _, check := range checks {
		if err := database.db.QueryRowContext(ctx, check.query).Scan(check.value); err != nil {
			t.Fatalf("query %q: %v", check.query, err)
		}
	}
	if version != currentSchemaVersion || sessions != 1 || tables != 1 || indexes != 2 {
		t.Fatalf("migration result version=%d sessions=%d tables=%d indexes=%d", version, sessions, tables, indexes)
	}
}
