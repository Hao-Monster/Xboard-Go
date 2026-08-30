package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
)

func TestImportLegacyAccessTokensPreservesCredentialsAndIsIdempotent(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	createdAt := time.Unix(1_700_000_000, 0).UTC()
	for _, user := range []struct {
		id    int64
		email string
		token string
	}{{11, "legacy-token-one@example.test", strings.Repeat("1", 32)}, {12, "legacy-token-two@example.test", strings.Repeat("2", 32)}} {
		if _, err := database.db.ExecContext(ctx, `
			INSERT INTO users (id,email,password_hash,account_kind,subscription_token,created_at,updated_at)
			VALUES (?,?,'hash','human',?,?,?)
		`, user.id, user.email, user.token, createdAt.Unix(), createdAt.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	sourceSHA := strings.Repeat("a", 64)
	recordLegacyAccessTokenPrerequisite(t, database, sourceSHA, createdAt)
	lastUsed := createdAt.Add(time.Minute).Unix()
	expires := createdAt.Add(365 * 24 * time.Hour).Unix()
	tokens := []LegacyAccessToken{
		{ID: 7, UserID: 11, TokenHash: security.DigestToken("legacy-bearer-one"), Name: "legacy-device-one", LastUsedAt: &lastUsed, CreatedAt: createdAt.Unix(), UpdatedAt: lastUsed},
		{ID: 9, UserID: 12, TokenHash: security.DigestToken("legacy-bearer-two"), Name: "legacy-device-two", ExpiresAt: &expires, CreatedAt: createdAt.Unix(), UpdatedAt: createdAt.Unix()},
	}
	input := LegacyAccessTokensImport{
		Slice: LegacyAccessTokensSlice, SourceSHA256: sourceSHA, SourceSize: 4096, Tokens: tokens,
		RollbackBackupPath: "/backups/access-tokens.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
	}
	input.Checksum = LegacyAccessTokensChecksum(input.Tokens)
	report, err := database.ImportLegacyAccessTokens(ctx, input, createdAt.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("ImportLegacyAccessTokens() error = %v", err)
	}
	if report.AlreadyApplied || report.Tokens.SourceRows != 2 || report.Tokens.TargetRows != 2 || report.Tokens.SourceChecksum != report.Tokens.TargetChecksum {
		t.Fatalf("access token report = %#v", report)
	}
	var sequence int64
	if err := database.db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name = 'access_tokens'`).Scan(&sequence); err != nil || sequence != 9 {
		t.Fatalf("access token sequence = %d, error = %v", sequence, err)
	}
	authenticated, err := database.AuthenticateAccessToken(ctx, security.DigestToken("legacy-bearer-one"), createdAt.Add(2*time.Minute))
	if err != nil || authenticated.UserID != 11 || authenticated.SessionID != 7 || authenticated.CredentialKind != CredentialKindAccessToken {
		t.Fatalf("AuthenticateAccessToken(imported) = (%#v, %v)", authenticated, err)
	}
	if authenticated, err := database.AuthenticateAccessToken(ctx, security.DigestToken("legacy-bearer-two"), createdAt.Add(2*time.Minute)); err != nil || authenticated.UserID != 12 || authenticated.ExpiresAt.Unix() != expires {
		t.Fatalf("AuthenticateAccessToken(imported expiring) = (%#v, %v)", authenticated, err)
	}
	if _, err := database.AuthenticateAccessToken(ctx, security.DigestToken("legacy-bearer-two"), time.Unix(expires, 0)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AuthenticateAccessToken(imported expired) error = %v", err)
	}
	repeated, err := database.ImportLegacyAccessTokens(ctx, input, createdAt.Add(3*time.Minute))
	if err != nil || !repeated.AlreadyApplied || repeated.AppliedAt != report.AppliedAt {
		t.Fatalf("idempotent ImportLegacyAccessTokens() = (%#v, %v)", repeated, err)
	}
	different := input
	different.SourceSHA256 = strings.Repeat("9", 64)
	if _, err := database.ImportLegacyAccessTokens(ctx, different, createdAt.Add(4*time.Minute)); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "another snapshot") {
		t.Fatalf("ImportLegacyAccessTokens(different source) error = %v", err)
	}
}

func TestImportLegacyAccessTokensRequiresMatchingUsersAndRollsBack(t *testing.T) {
	database := newTestStore(t)
	input := LegacyAccessTokensImport{
		Slice: LegacyAccessTokensSlice, SourceSHA256: strings.Repeat("c", 64), SourceSize: 1,
		Tokens: []LegacyAccessToken{{
			ID: 1, UserID: 99, TokenHash: security.DigestToken("missing-user-token"), Name: "missing-user",
			CreatedAt: 100, UpdatedAt: 100,
		}},
		RollbackBackupPath: "/backups/access-tokens.xbbackup", RollbackBackupSHA256: strings.Repeat("d", 64),
	}
	input.Checksum = LegacyAccessTokensChecksum(input.Tokens)
	if _, err := database.ImportLegacyAccessTokens(context.Background(), input, time.Unix(200, 0)); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "human users") {
		t.Fatalf("ImportLegacyAccessTokens(missing prerequisite) error = %v", err)
	}
	var rows, runs int
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM access_tokens`).Scan(&rows)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyAccessTokensSlice).Scan(&runs)
	if rows != 0 || runs != 0 {
		t.Fatalf("failed access token import left rows=%d runs=%d", rows, runs)
	}
}

func TestImportLegacyAccessTokensRollsBackAWriteFailure(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	for _, id := range []int64{11, 12} {
		if _, err := database.db.ExecContext(ctx, `
			INSERT INTO users (id,email,password_hash,account_kind,subscription_token,created_at,updated_at)
			VALUES (?,?,'hash','human',?,?,?)
		`, id, fmt.Sprintf("legacy-token-%d@example.test", id), fmt.Sprintf("%032x", id), now.Unix(), now.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	sourceSHA := strings.Repeat("e", 64)
	recordLegacyAccessTokenPrerequisite(t, database, sourceSHA, now)
	if _, err := database.db.Exec(`
		CREATE TRIGGER reject_second_legacy_access_token
		BEFORE INSERT ON access_tokens WHEN NEW.id = 9
		BEGIN SELECT RAISE(ABORT, 'injected access token failure'); END
	`); err != nil {
		t.Fatal(err)
	}
	input := LegacyAccessTokensImport{
		Slice: LegacyAccessTokensSlice, SourceSHA256: sourceSHA, SourceSize: 1,
		Tokens: []LegacyAccessToken{
			{ID: 7, UserID: 11, TokenHash: security.DigestToken("legacy-one"), Name: "legacy-one", CreatedAt: now.Unix(), UpdatedAt: now.Unix()},
			{ID: 9, UserID: 12, TokenHash: security.DigestToken("legacy-two"), Name: "legacy-two", CreatedAt: now.Unix(), UpdatedAt: now.Unix()},
		},
		RollbackBackupPath: "/backups/access-tokens.xbbackup", RollbackBackupSHA256: strings.Repeat("f", 64),
	}
	input.Checksum = LegacyAccessTokensChecksum(input.Tokens)
	if _, err := database.ImportLegacyAccessTokens(ctx, input, now.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "injected access token failure") {
		t.Fatalf("ImportLegacyAccessTokens(injected failure) error = %v", err)
	}
	var rows, runs int
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM access_tokens`).Scan(&rows)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyAccessTokensSlice).Scan(&runs)
	if rows != 0 || runs != 0 {
		t.Fatalf("failed access token write left rows=%d runs=%d", rows, runs)
	}
}

func TestImportLegacyAccessTokensRejectsANonEmptyTarget(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "existing-token@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	sourceSHA := strings.Repeat("8", 64)
	recordLegacyAccessTokenPrerequisite(t, database, sourceSHA, now)
	if _, err := database.CreateAccessToken(ctx, CreateAccessTokenInput{
		UserID: user.ID, TokenHash: security.DigestToken("existing-target-token"), Name: "existing-target",
	}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	input := LegacyAccessTokensImport{
		Slice: LegacyAccessTokensSlice, SourceSHA256: sourceSHA, SourceSize: 1, Tokens: []LegacyAccessToken{},
		RollbackBackupPath: "/backups/access-tokens.xbbackup", RollbackBackupSHA256: strings.Repeat("7", 64),
	}
	input.Checksum = LegacyAccessTokensChecksum(input.Tokens)
	if _, err := database.ImportLegacyAccessTokens(ctx, input, now.Add(2*time.Minute)); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "empty access token target") {
		t.Fatalf("ImportLegacyAccessTokens(non-empty target) error = %v", err)
	}
	var rows, runs int
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM access_tokens`).Scan(&rows)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyAccessTokensSlice).Scan(&runs)
	if rows != 1 || runs != 0 {
		t.Fatalf("rejected access token import left rows=%d runs=%d", rows, runs)
	}
}

func recordLegacyAccessTokenPrerequisite(t *testing.T, database *Store, sourceSHA string, now time.Time) {
	t.Helper()
	if _, err := database.db.Exec(`
		INSERT INTO legacy_migration_runs
		(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at)
		VALUES (?, ?, 1, 'prerequisite.xbbackup', ?, '{}', ?)
	`, LegacyHumanUsersSlice, sourceSHA, strings.Repeat("0", 64), now.Unix()); err != nil {
		t.Fatal(err)
	}
}
