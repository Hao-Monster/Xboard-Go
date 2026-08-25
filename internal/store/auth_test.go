package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestListActiveSessionsFiltersAndMarksCurrent(t *testing.T) {
	database, user, now := newAuthTestStore(t)
	ctx := context.Background()

	createTestSession(t, database, user.ID, "active-current", now.Add(time.Hour), now.Add(-2*time.Hour))
	current, err := database.AuthenticateSession(ctx, "active-current", now)
	if err != nil {
		t.Fatalf("AuthenticateSession(current) error = %v", err)
	}
	createTestSession(t, database, user.ID, "active-other", now.Add(2*time.Hour), now.Add(-time.Hour))
	createTestSession(t, database, user.ID, "expired", now.Add(-time.Minute), now.Add(-3*time.Hour))
	createTestSession(t, database, user.ID, "revoked", now.Add(time.Hour), now.Add(-4*time.Hour))
	revoked, err := database.AuthenticateSession(ctx, "revoked", now)
	if err != nil {
		t.Fatalf("AuthenticateSession(revoked) error = %v", err)
	}
	if err := database.RevokeSession(ctx, revoked.SessionID, now); err != nil {
		t.Fatalf("RevokeSession() error = %v", err)
	}

	sessions, err := database.ListActiveSessions(ctx, user.ID, current.SessionID, now)
	if err != nil {
		t.Fatalf("ListActiveSessions() error = %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("len(sessions) = %d, want 2: %#v", len(sessions), sessions)
	}
	if sessions[0].ID == current.SessionID || sessions[1].ID != current.SessionID {
		t.Fatalf("sessions are not newest-first: %#v", sessions)
	}
	if sessions[0].IsCurrent || !sessions[1].IsCurrent {
		t.Fatalf("current marker is wrong: %#v", sessions)
	}
	if sessions[1].LastUsedAt == nil || !sessions[1].LastUsedAt.Equal(now.UTC()) {
		t.Fatalf("last_used_at = %v, want %v", sessions[1].LastUsedAt, now.UTC())
	}
}

func TestRevokeUserSessionEnforcesOwnership(t *testing.T) {
	database, owner, now := newAuthTestStore(t)
	ctx := context.Background()

	result, err := database.db.ExecContext(ctx, `
		INSERT INTO users (email, password_hash, is_admin, banned, subscription_token, created_at, updated_at)
		VALUES ('other@example.test', 'other-hash', 0, 0, ?, ?, ?)
	`, testSubscriptionToken(t), now.Unix(), now.Unix())
	if err != nil {
		t.Fatalf("insert other user: %v", err)
	}
	otherID, _ := result.LastInsertId()
	createTestSession(t, database, otherID, "other-token", now.Add(time.Hour), now)
	otherSession, err := database.AuthenticateSession(ctx, "other-token", now)
	if err != nil {
		t.Fatalf("AuthenticateSession(other) error = %v", err)
	}

	if err := database.RevokeUserSession(ctx, owner.ID, otherSession.SessionID, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RevokeUserSession(other user) error = %v, want ErrNotFound", err)
	}
	if _, err := database.AuthenticateSession(ctx, "other-token", now); err != nil {
		t.Fatalf("other session was changed after denied revocation: %v", err)
	}
	if err := database.RevokeUserSession(ctx, otherID, otherSession.SessionID, now); err != nil {
		t.Fatalf("RevokeUserSession(owner) error = %v", err)
	}
	if _, err := database.AuthenticateSession(ctx, "other-token", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked session authentication error = %v, want ErrNotFound", err)
	}
}

func TestChangePasswordRevokesAllSessionsAndRejectsStaleHash(t *testing.T) {
	database, user, now := newAuthTestStore(t)
	ctx := context.Background()
	createTestSession(t, database, user.ID, "session-one", now.Add(time.Hour), now)
	createTestSession(t, database, user.ID, "session-two", now.Add(time.Hour), now)
	if _, err := database.CreateAccessToken(ctx, CreateAccessTokenInput{
		UserID: user.ID, TokenHash: strings.Repeat("e", 64), Name: "password-change-device",
	}, now); err != nil {
		t.Fatalf("CreateAccessToken() error = %v", err)
	}

	if err := database.ChangePassword(ctx, user.ID, "old-hash", "new-hash", now); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	updated, err := database.FindUserByEmail(ctx, user.Email)
	if err != nil {
		t.Fatalf("FindUserByEmail() error = %v", err)
	}
	if updated.PasswordHash != "new-hash" {
		t.Fatalf("password hash = %q, want new-hash", updated.PasswordHash)
	}
	sessions, err := database.ListActiveSessions(ctx, user.ID, 0, now)
	if err != nil || len(sessions) != 0 {
		t.Fatalf("active sessions after password change = %#v, err=%v", sessions, err)
	}
	if _, err := database.AuthenticateAccessToken(ctx, strings.Repeat("e", 64), now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("access token after password change error = %v, want ErrNotFound", err)
	}

	if err := database.ChangePassword(ctx, user.ID, "old-hash", "stale-overwrite", now); !errors.Is(err, ErrConflict) {
		t.Fatalf("ChangePassword(stale) error = %v, want ErrConflict", err)
	}
	updated, _ = database.FindUserByEmail(ctx, user.Email)
	if updated.PasswordHash != "new-hash" {
		t.Fatalf("stale change overwrote password: %q", updated.PasswordHash)
	}
}

func TestConcurrentPasswordChangesCannotOverwriteTheWinner(t *testing.T) {
	database, user, now := newAuthTestStore(t)
	ctx := context.Background()
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, newHash := range []string{"concurrent-hash-one", "concurrent-hash-two"} {
		workers.Add(1)
		go func(hash string) {
			defer workers.Done()
			<-start
			results <- database.ChangePassword(ctx, user.ID, "old-hash", hash, now)
		}(newHash)
	}
	close(start)
	workers.Wait()
	close(results)

	var succeeded, conflicted int
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrConflict):
			conflicted++
		default:
			t.Fatalf("concurrent ChangePassword() error = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent results: success=%d conflict=%d", succeeded, conflicted)
	}
	updated, err := database.FindUserByEmail(ctx, user.Email)
	if err != nil {
		t.Fatalf("FindUserByEmail() error = %v", err)
	}
	if updated.PasswordHash != "concurrent-hash-one" && updated.PasswordHash != "concurrent-hash-two" {
		t.Fatalf("unexpected winning hash %q", updated.PasswordHash)
	}
}

func TestCompletePasswordLoginAndCreateSessionUsesOneCASWithoutRevokingCredentials(t *testing.T) {
	database, user, now := newAuthTestStore(t)
	ctx := context.Background()
	createTestSession(t, database, user.ID, "existing-session", now.Add(time.Hour), now.Add(-time.Hour))
	if _, err := database.CreateAccessToken(ctx, CreateAccessTokenInput{
		UserID: user.ID, TokenHash: strings.Repeat("f", 64), Name: "existing-device",
	}, now); err != nil {
		t.Fatalf("CreateAccessToken() error = %v", err)
	}

	loginAt := now.Add(5 * time.Minute)
	if err := database.CompletePasswordLoginAndCreateSession(ctx, user.ID, user.Email, "old-hash", "upgraded-hash",
		"new-login-session", "new-login-csrf", loginAt.Add(time.Hour), loginAt); err != nil {
		t.Fatalf("CompletePasswordLoginAndCreateSession() error = %v", err)
	}
	updated, err := database.FindUserByEmail(ctx, user.Email)
	if err != nil {
		t.Fatalf("FindUserByEmail() error = %v", err)
	}
	if updated.PasswordHash != "upgraded-hash" {
		t.Fatalf("password hash = %q, want upgraded-hash", updated.PasswordHash)
	}
	var lastLoginAt int64
	if err := database.db.QueryRowContext(ctx, `SELECT last_login_at FROM users WHERE id = ?`, user.ID).Scan(&lastLoginAt); err != nil {
		t.Fatalf("query last_login_at: %v", err)
	}
	if lastLoginAt != loginAt.Unix() {
		t.Fatalf("last_login_at = %d, want %d", lastLoginAt, loginAt.Unix())
	}
	if sessions, err := database.ListActiveSessions(ctx, user.ID, 0, loginAt); err != nil || len(sessions) != 2 {
		t.Fatalf("sessions after transparent rehash = %#v, err=%v", sessions, err)
	}
	if _, err := database.AuthenticateAccessToken(ctx, strings.Repeat("f", 64), loginAt); err != nil {
		t.Fatalf("access token after transparent rehash error = %v", err)
	}

	if err := database.CompletePasswordLoginAndCreateSession(ctx, user.ID, user.Email, "old-hash", "stale-overwrite",
		"stale-session", "stale-csrf", loginAt.Add(2*time.Hour), loginAt.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("CompletePasswordLoginAndCreateSession(stale) error = %v, want ErrConflict", err)
	}
	updated, _ = database.FindUserByEmail(ctx, user.Email)
	if updated.PasswordHash != "upgraded-hash" {
		t.Fatalf("stale login overwrote password: %q", updated.PasswordHash)
	}

	if _, err := database.db.ExecContext(ctx, `UPDATE users SET email = 'renamed@example.test' WHERE id = ?`, user.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.CompletePasswordLoginAndCreateSession(ctx, user.ID, user.Email, "upgraded-hash", "upgraded-hash",
		"renamed-race-session", "renamed-race-csrf", loginAt.Add(2*time.Hour), loginAt.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("CompletePasswordLoginAndCreateSession(renamed) error = %v, want ErrConflict", err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET banned = 1 WHERE id = ?`, user.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.CompletePasswordLoginAndCreateSession(ctx, user.ID, "renamed@example.test", "upgraded-hash", "upgraded-hash",
		"banned-race-session", "banned-race-csrf", loginAt.Add(2*time.Hour), loginAt.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("CompletePasswordLoginAndCreateSession(banned) error = %v, want ErrConflict", err)
	}
	var forbiddenSessions int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_sessions WHERE token_hash IN ('renamed-race-session', 'banned-race-session')`).Scan(&forbiddenSessions); err != nil {
		t.Fatal(err)
	}
	if forbiddenSessions != 0 {
		t.Fatalf("stale identity or banned login created %d sessions", forbiddenSessions)
	}
}

func TestPasswordResetAndTransparentRehashCannotBothWin(t *testing.T) {
	database, user, now := newAuthTestStore(t)
	ctx := context.Background()
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		<-start
		results <- database.ChangePassword(ctx, user.ID, "old-hash", "reset-hash", now)
	}()
	go func() {
		defer workers.Done()
		<-start
		results <- database.CompletePasswordLoginAndCreateSession(ctx, user.ID, user.Email, "old-hash", "rehash",
			"concurrent-login-session", "concurrent-login-csrf", now.Add(time.Hour), now)
	}()
	close(start)
	workers.Wait()
	close(results)

	var succeeded, conflicted int
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrConflict):
			conflicted++
		default:
			t.Fatalf("concurrent password operation error = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent results: success=%d conflict=%d", succeeded, conflicted)
	}
	updated, err := database.FindUserByEmail(ctx, user.Email)
	if err != nil {
		t.Fatalf("FindUserByEmail() error = %v", err)
	}
	if updated.PasswordHash != "reset-hash" && updated.PasswordHash != "rehash" {
		t.Fatalf("unexpected winning password hash %q", updated.PasswordHash)
	}
	sessions, err := database.ListActiveSessions(ctx, user.ID, 0, now)
	if err != nil {
		t.Fatalf("ListActiveSessions() error = %v", err)
	}
	if updated.PasswordHash == "reset-hash" && len(sessions) != 0 {
		t.Fatalf("password reset won but login session was created: %#v", sessions)
	}
	if updated.PasswordHash == "rehash" && len(sessions) != 1 {
		t.Fatalf("password login won without exactly one session: %#v", sessions)
	}
}

func newAuthTestStore(t *testing.T) (*Store, User, time.Time) {
	t.Helper()
	database, err := OpenSQLite(fmt.Sprintf("file:auth-%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	if _, err := database.BootstrapAdmin(context.Background(), "admin@example.test", "old-hash", now); err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}
	user, err := database.FindUserByEmail(context.Background(), "admin@example.test")
	if err != nil {
		t.Fatalf("FindUserByEmail() error = %v", err)
	}
	return database, user, now
}

func createTestSession(t *testing.T, database *Store, userID int64, token string, expiresAt, createdAt time.Time) {
	t.Helper()
	if err := database.CreateSession(context.Background(), userID, token, "csrf-"+token, expiresAt, createdAt); err != nil {
		t.Fatalf("CreateSession(%s) error = %v", token, err)
	}
}
