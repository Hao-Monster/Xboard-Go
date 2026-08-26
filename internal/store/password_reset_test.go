package store

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
)

func TestSchemaMigrationPreservesV16UsersSessionsAndSettings(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "schema-v16.db")
	database, err := OpenSQLite("file:" + filepath.ToSlash(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := t.Context()
	for step, schema := range []string{
		schemaV1, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6, schemaV7, schemaV7Constraints,
		schemaV8, schemaV9, schemaV10, schemaV11, schemaV12, schemaV13, schemaV14, schemaV15, schemaV16,
	} {
		if _, err := database.db.ExecContext(ctx, schema); err != nil {
			t.Fatalf("apply pre-v17 schema step %d: %v", step+1, err)
		}
	}
	if _, err := database.db.ExecContext(ctx, `PRAGMA user_version = 16`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, schemaV25); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	user := createPreV33HumanUserFixture(t, database, "v16-reset@example.test", "preserved-hash", now)
	if _, err := database.db.ExecContext(ctx, `ALTER TABLE users DROP COLUMN last_login_at`); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateSession(ctx, user.ID, strings.Repeat("a", 64), strings.Repeat("b", 64), now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(ctx, user.ID, settings.Revision, SaveTicketSettingsInput{
		AppName: "Preserved V16", AppURL: "https://v16.example.test", SMTPEnabled: true,
		SMTPHost: "mailpit", SMTPPort: 1025, SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, now); err != nil {
		t.Fatal(err)
	}

	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v16 to v17) error = %v", err)
	}
	var version, users, sessions, challengeTables, outboxTables, challengeRows, outboxRows int
	queries := []struct {
		query string
		value *int
	}{
		{`PRAGMA user_version`, &version},
		{`SELECT COUNT(*) FROM users WHERE id = ` + fmt.Sprint(user.ID) + ` AND password_hash = 'preserved-hash'`, &users},
		{`SELECT COUNT(*) FROM admin_sessions WHERE user_id = ` + fmt.Sprint(user.ID), &sessions},
		{`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='password_reset_challenges'`, &challengeTables},
		{`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='password_reset_mail_outbox'`, &outboxTables},
		{`SELECT COUNT(*) FROM password_reset_challenges`, &challengeRows},
		{`SELECT COUNT(*) FROM password_reset_mail_outbox`, &outboxRows},
	}
	for _, item := range queries {
		if err := database.db.QueryRowContext(ctx, item.query).Scan(item.value); err != nil {
			t.Fatal(err)
		}
	}
	updatedSettings, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var integrity string
	if err := database.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	rows, err := database.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	foreignKeyViolation := rows.Next()
	if version != currentSchemaVersion || users != 1 || sessions != 1 || challengeTables != 1 || outboxTables != 1 || challengeRows != 0 || outboxRows != 0 ||
		updatedSettings.AppName != "Preserved V16" || integrity != "ok" || foreignKeyViolation {
		t.Fatalf("v16 to v17 migration: version=%d users=%d sessions=%d tables=(%d,%d) rows=(%d,%d) settings=%#v integrity=%q foreign_keys=%t",
			version, users, sessions, challengeTables, outboxTables, challengeRows, outboxRows, updatedSettings, integrity, foreignKeyViolation)
	}
}

func TestPasswordResetChallengeHidesUnknownAccountsLocksAndRevokesSessions(t *testing.T) {
	database := newPasswordResetStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)
	protector, err := security.NewPasswordResetProtector(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "reset-user@example.test", PasswordHash: "old-hash"}, now)
	if err != nil {
		t.Fatal(err)
	}

	unknown := passwordResetInput(t, protector, "unknown@example.test", "123456")
	queued, err := database.RequestPasswordReset(ctx, unknown, now)
	if err != nil || queued {
		t.Fatalf("unknown RequestPasswordReset() = (%v, %v), want accepted without mail", queued, err)
	}
	if _, err := database.RequestPasswordReset(ctx, unknown, now.Add(time.Second)); !errors.Is(err, ErrPasswordResetLimited) {
		t.Fatalf("unknown cooldown error = %v", err)
	}
	var outboxCount int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM password_reset_mail_outbox`).Scan(&outboxCount); err != nil || outboxCount != 0 {
		t.Fatalf("unknown outbox count = %d, error = %v", outboxCount, err)
	}
	var unknownDigestLength int
	if err := database.db.QueryRowContext(ctx, `SELECT length(code_digest) FROM password_reset_challenges WHERE email_digest = ?`, unknown.EmailDigest).Scan(&unknownDigestLength); err != nil || unknownDigestLength != 32 {
		t.Fatalf("unknown challenge digest length = %d, error = %v", unknownDigestLength, err)
	}

	input := passwordResetInput(t, protector, user.Email, "234567")
	queued, err = database.RequestPasswordReset(ctx, input, now)
	if err != nil || !queued {
		t.Fatalf("known RequestPasswordReset() = (%v, %v)", queued, err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		wrong, _ := protector.CodeDigest(user.Email, fmt.Sprintf("%06d", attempt))
		if _, err := database.CheckPasswordResetChallenge(ctx, input.EmailDigest, wrong, now.Add(time.Duration(attempt+1)*time.Second)); !errors.Is(err, ErrPasswordResetInvalid) {
			t.Fatalf("wrong challenge attempt %d error = %v", attempt+1, err)
		}
	}
	if _, err := database.CheckPasswordResetChallenge(ctx, input.EmailDigest, input.CodeDigest, now.Add(4*time.Second)); !errors.Is(err, ErrPasswordResetLocked) {
		t.Fatalf("locked correct challenge error = %v", err)
	}

	secondNow := now.Add(6 * time.Minute)
	second := passwordResetInput(t, protector, user.Email, "345678")
	if queued, err := database.RequestPasswordReset(ctx, second, secondNow); err != nil || !queued {
		t.Fatalf("replacement RequestPasswordReset() = (%v, %v)", queued, err)
	}
	if _, err := database.CheckPasswordResetChallenge(ctx, second.EmailDigest, input.CodeDigest, secondNow); !errors.Is(err, ErrPasswordResetInvalid) {
		t.Fatalf("superseded challenge error = %v", err)
	}
	challenge, err := database.CheckPasswordResetChallenge(ctx, second.EmailDigest, second.CodeDigest, secondNow)
	if err != nil || challenge.UserID != user.ID || challenge.PasswordHash != "old-hash" {
		t.Fatalf("CheckPasswordResetChallenge() = (%#v, %v)", challenge, err)
	}
	if err := database.CreateSession(ctx, user.ID, strings.Repeat("a", 64), strings.Repeat("b", 64), secondNow.Add(time.Hour), secondNow); err != nil {
		t.Fatal(err)
	}
	if err := database.ResetPasswordWithChallenge(ctx, second.EmailDigest, second.CodeDigest, challenge, "new-hash", secondNow); err != nil {
		t.Fatal(err)
	}
	found, err := database.FindUserByID(ctx, user.ID)
	if err != nil || found.PasswordHash != "new-hash" {
		t.Fatalf("password after reset = %q, error = %v", found.PasswordHash, err)
	}
	if _, err := database.AuthenticateSession(ctx, strings.Repeat("a", 64), secondNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("password reset left session active: %v", err)
	}
	if _, err := database.CheckPasswordResetChallenge(ctx, second.EmailDigest, second.CodeDigest, secondNow); !errors.Is(err, ErrPasswordResetInvalid) {
		t.Fatalf("consumed challenge error = %v", err)
	}
}

func TestPasswordResetRequestConcurrencyQueuesOneCurrentMailAndExpiryRejectsCode(t *testing.T) {
	database := newPasswordResetStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 25, 2, 30, 0, 0, time.UTC)
	protector, _ := security.NewPasswordResetProtector(make([]byte, 32))
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "concurrent-request@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	input := passwordResetInput(t, protector, user.Email, "512347")
	type result struct {
		queued bool
		err    error
	}
	results := make(chan result, 8)
	var start sync.WaitGroup
	start.Add(1)
	for range cap(results) {
		go func() {
			start.Wait()
			queued, err := database.RequestPasswordReset(ctx, input, now)
			results <- result{queued: queued, err: err}
		}()
	}
	start.Done()
	queued := 0
	limited := 0
	for range cap(results) {
		item := <-results
		switch {
		case item.err == nil && item.queued:
			queued++
		case errors.Is(item.err, ErrPasswordResetLimited):
			limited++
		default:
			t.Fatalf("concurrent request result = %#v", item)
		}
	}
	var challenges, pendingMail int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM password_reset_challenges`).Scan(&challenges); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM password_reset_mail_outbox
		WHERE sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
	`).Scan(&pendingMail); err != nil {
		t.Fatal(err)
	}
	if queued != 1 || limited != 7 || challenges != 1 || pendingMail != 1 {
		t.Fatalf("concurrent request queued=%d limited=%d challenges=%d pending_mail=%d", queued, limited, challenges, pendingMail)
	}
	if _, err := database.CheckPasswordResetChallenge(ctx, input.EmailDigest, input.CodeDigest, now.Add(5*time.Minute)); !errors.Is(err, ErrPasswordResetInvalid) {
		t.Fatalf("expired challenge error = %v", err)
	}
	if _, claimed, err := database.ClaimPasswordResetMail(ctx, "near-expiry", now.Add(4*time.Minute+30*time.Second), time.Minute); err != nil || claimed {
		t.Fatalf("near-expiry mail claim = (%v, %v), want no delivery", claimed, err)
	}
}

func TestPasswordResetChallengeAllowsOnlyOneConcurrentResetAndExcludesInternalAccounts(t *testing.T) {
	database := newPasswordResetStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 25, 2, 0, 0, 0, time.UTC)
	protector, _ := security.NewPasswordResetProtector(make([]byte, 32))
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "concurrent-reset@example.test", PasswordHash: "old-hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	input := passwordResetInput(t, protector, user.Email, "456789")
	if queued, err := database.RequestPasswordReset(ctx, input, now); err != nil || !queued {
		t.Fatalf("RequestPasswordReset() = (%v, %v)", queued, err)
	}
	challenge, err := database.CheckPasswordResetChallenge(ctx, input.EmailDigest, input.CodeDigest, now)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 8)
	var start sync.WaitGroup
	start.Add(1)
	for index := 0; index < cap(results); index++ {
		go func(index int) {
			start.Wait()
			results <- database.ResetPasswordWithChallenge(ctx, input.EmailDigest, input.CodeDigest, challenge, fmt.Sprintf("new-hash-%d", index), now)
		}(index)
	}
	start.Done()
	successes := 0
	for index := 0; index < cap(results); index++ {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, ErrPasswordResetInvalid) && !errors.Is(err, ErrConflict) {
			t.Fatalf("concurrent reset error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent successful resets = %d, want 1", successes)
	}

	internal, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "internal-reset@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET account_kind = ? WHERE id = ?`, AccountKindInternalSubscription, internal.ID); err != nil {
		t.Fatal(err)
	}
	internalInput := passwordResetInput(t, protector, internal.Email, "567890")
	queued, err := database.RequestPasswordReset(ctx, internalInput, now)
	if err != nil || queued {
		t.Fatalf("internal RequestPasswordReset() = (%v, %v), want no mail", queued, err)
	}
}

func TestDisablingSMTPPermanentlyCancelsQueuedPasswordResetMail(t *testing.T) {
	database := newPasswordResetStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	protector, _ := security.NewPasswordResetProtector(make([]byte, 32))
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "cancel-reset@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	input := passwordResetInput(t, protector, user.Email, "678901")
	if queued, err := database.RequestPasswordReset(ctx, input, now); err != nil || !queued {
		t.Fatalf("RequestPasswordReset() = (%v, %v)", queued, err)
	}
	settings, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings, err = database.UpdateTicketSettings(ctx, user.ID, settings.Revision, SaveTicketSettingsInput{
		AppName: "Reset Test", SMTPPort: 1025, SMTPEncryption: "none",
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(ctx, user.ID, settings.Revision, SaveTicketSettingsInput{
		AppName: "Reset Test", SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025,
		SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := database.ClaimPasswordResetMail(ctx, "after-reenable", now.Add(3*time.Second), time.Minute); err != nil || claimed {
		t.Fatalf("ClaimPasswordResetMail(after re-enable) = (%v, %v), want cancelled queue", claimed, err)
	}
	var cancelled int
	var cipher []byte
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(MAX(code_cipher), X'') FROM password_reset_mail_outbox WHERE cancelled_at IS NOT NULL
	`).Scan(&cancelled, &cipher); err != nil {
		t.Fatal(err)
	}
	if cancelled != 1 || len(cipher) != 0 {
		t.Fatalf("cancelled reset mail count=%d cipher_bytes=%d", cancelled, len(cipher))
	}
}

func newPasswordResetStore(t *testing.T) *Store {
	t.Helper()
	database, err := OpenSQLite(fmt.Sprintf("file:password-reset-%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	if _, err := database.BootstrapAdmin(t.Context(), "reset-admin@example.test", "admin-hash", now); err != nil {
		t.Fatal(err)
	}
	admin, err := database.FindUserByEmail(t.Context(), "reset-admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetTicketSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(t.Context(), admin.ID, settings.Revision, SaveTicketSettingsInput{
		AppName: "Reset Test", AppURL: "https://panel.example.test", SMTPEnabled: true,
		SMTPHost: "mailpit", SMTPPort: 1025, SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, now); err != nil {
		t.Fatal(err)
	}
	return database
}

func passwordResetInput(t *testing.T, protector *security.PasswordResetProtector, email, code string) PasswordResetRequestInput {
	t.Helper()
	emailDigest, err := protector.EmailDigest(email)
	if err != nil {
		t.Fatal(err)
	}
	codeDigest, err := protector.CodeDigest(email, code)
	if err != nil {
		t.Fatal(err)
	}
	codeCipher, err := protector.EncryptCode(email, code)
	if err != nil {
		t.Fatal(err)
	}
	return PasswordResetRequestInput{Email: email, EmailDigest: emailDigest, CodeDigest: codeDigest, CodeCipher: codeCipher}
}

func BenchmarkPasswordResetChallengeCheck(b *testing.B) {
	database, err := OpenSQLite("file:benchmark-password-reset?mode=memory&cache=shared")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	ctx := b.Context()
	if err := database.Migrate(ctx); err != nil {
		b.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 6, 0, 0, 0, time.UTC)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "benchmark-reset@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		b.Fatal(err)
	}
	settings, err := database.GetTicketSettings(ctx)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(ctx, user.ID, settings.Revision, SaveTicketSettingsInput{
		AppName: "Benchmark", SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025,
		SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, now); err != nil {
		b.Fatal(err)
	}
	protector, _ := security.NewPasswordResetProtector(make([]byte, 32))
	emailDigest, _ := protector.EmailDigest(user.Email)
	codeDigest, _ := protector.CodeDigest(user.Email, "483729")
	codeCipher, _ := protector.EncryptCode(user.Email, "483729")
	if queued, err := database.RequestPasswordReset(ctx, PasswordResetRequestInput{
		Email: user.Email, EmailDigest: emailDigest, CodeDigest: codeDigest, CodeCipher: codeCipher,
	}, now); err != nil || !queued {
		b.Fatalf("RequestPasswordReset() = (%v, %v)", queued, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := database.CheckPasswordResetChallenge(ctx, emailDigest, codeDigest, now); err != nil {
			b.Fatal(err)
		}
	}
}
