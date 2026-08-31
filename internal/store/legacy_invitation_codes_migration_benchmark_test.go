package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func BenchmarkImportLegacyInvitationCodes100k(b *testing.B) {
	const count = 100_000
	codes := make([]LegacyInvitationCode, count)
	for index := range codes {
		id := int64(index + 1)
		var input [8]byte
		binary.BigEndian.PutUint64(input[:], uint64(id))
		digest := sha256.Sum256(input[:])
		codes[index] = LegacyInvitationCode{
			ID: id, UserID: 1, CodeDigest: append([]byte(nil), digest[:]...),
			CodeCipher: make([]byte, 40), PV: id % 17, CreatedAt: 1, UpdatedAt: 1,
		}
	}
	checksum := LegacyInvitationCodesChecksum(codes)
	sourceSHA := strings.Repeat("a", 64)
	b.ReportAllocs()
	b.SetBytes(count * 80)
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		b.StopTimer()
		path := filepath.Join(b.TempDir(), "invitation-codes.db")
		database, err := OpenSQLite("file:" + path)
		if err != nil {
			b.Fatal(err)
		}
		if err := database.Migrate(context.Background()); err != nil {
			_ = database.Close()
			b.Fatal(err)
		}
		if _, err := database.db.Exec(`
			INSERT INTO users (id,email,password_hash,account_kind,subscription_token,created_at,updated_at)
			VALUES (1,'invitation-benchmark@example.test','hash','human',?,1,1)
		`, strings.Repeat("1", 32)); err != nil {
			_ = database.Close()
			b.Fatal(err)
		}
		if _, err := database.db.Exec(`
			INSERT INTO legacy_migration_runs
			(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at)
			VALUES (?, ?, 1, 'pre-benchmark.xbbackup', ?, '{}', 1)
		`, LegacyHumanUsersSlice, sourceSHA, strings.Repeat("0", 64)); err != nil {
			_ = database.Close()
			b.Fatal(err)
		}
		input := LegacyInvitationCodesImport{
			Slice: LegacyInvitationCodesSlice, SourceSHA256: sourceSHA, SourceSize: 1,
			Codes: codes, Checksum: checksum, RollbackBackupPath: "pre-benchmark.xbbackup",
			RollbackBackupSHA256: strings.Repeat("b", 64),
		}
		b.StartTimer()
		report, err := database.ImportLegacyInvitationCodes(context.Background(), input, time.Unix(2_000_000_000, 0))
		b.StopTimer()
		if closeErr := database.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			b.Fatal(err)
		}
		if report.Codes.TargetRows != count {
			b.Fatalf("target rows = %d", report.Codes.TargetRows)
		}
	}
}
