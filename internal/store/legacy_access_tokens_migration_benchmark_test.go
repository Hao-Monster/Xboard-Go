package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func BenchmarkValidateLegacyAccessTokens10K(b *testing.B) {
	tokens := benchmarkLegacyAccessTokens(10_000)
	b.ReportAllocs()
	b.SetBytes(int64(len(tokens)))
	for b.Loop() {
		if err := ValidateLegacyAccessTokensData(tokens); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLegacyAccessTokensChecksum10K(b *testing.B) {
	tokens := benchmarkLegacyAccessTokens(10_000)
	b.ReportAllocs()
	b.SetBytes(int64(len(tokens)))
	for b.Loop() {
		if checksum := LegacyAccessTokensChecksum(tokens); len(checksum) != 64 {
			b.Fatalf("checksum length = %d", len(checksum))
		}
	}
}

func BenchmarkImportLegacyAccessTokens10K(b *testing.B) {
	tokens := benchmarkLegacyAccessTokens(10_000)
	input := LegacyAccessTokensImport{
		Slice: LegacyAccessTokensSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 1,
		Tokens: tokens, RollbackBackupPath: "/backups/access-tokens.xbbackup",
		RollbackBackupSHA256: strings.Repeat("b", 64),
	}
	input.Checksum = LegacyAccessTokensChecksum(tokens)
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		database := newTestStore(b)
		seedBenchmarkLegacyAccessTokenUsers(b, database, len(tokens), input.SourceSHA256)
		b.StartTimer()
		if _, err := database.ImportLegacyAccessTokens(context.Background(), input, time.Unix(1_700_000_100, 0)); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(tokens)), "tokens/op")
}

func benchmarkLegacyAccessTokens(count int) []LegacyAccessToken {
	tokens := make([]LegacyAccessToken, count)
	for index := range tokens {
		value := int64(index + 1)
		tokens[index] = LegacyAccessToken{
			ID: value, UserID: value, TokenHash: fmt.Sprintf("%064x", value), Name: fmt.Sprintf("legacy-device-%d", value),
			CreatedAt: 1_700_000_000, UpdatedAt: 1_700_000_000,
		}
	}
	return tokens
}

func seedBenchmarkLegacyAccessTokenUsers(b *testing.B, database *Store, count int, sourceSHA string) {
	b.Helper()
	tx, err := database.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.Prepare(`
		INSERT INTO users (id,email,password_hash,account_kind,subscription_token,created_at,updated_at)
		VALUES (?,?,'hash','human',?,1700000000,1700000000)
	`)
	if err != nil {
		_ = tx.Rollback()
		b.Fatal(err)
	}
	for index := 1; index <= count; index++ {
		if _, err := statement.Exec(index, fmt.Sprintf("legacy-%d@example.test", index), fmt.Sprintf("%032x", index)); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			b.Fatal(err)
		}
	}
	if err := statement.Close(); err != nil {
		_ = tx.Rollback()
		b.Fatal(err)
	}
	if _, err := tx.Exec(`
		INSERT INTO legacy_migration_runs
		(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at)
		VALUES (?, ?, 1, 'prerequisite.xbbackup', ?, '{}', 1)
	`, LegacyHumanUsersSlice, sourceSHA, strings.Repeat("0", 64)); err != nil {
		_ = tx.Rollback()
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
}
