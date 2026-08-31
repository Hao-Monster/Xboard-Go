package store

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
)

func TestImportLegacyInvitationCodesPreservesStateAndDetectsDrift(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_001_000, 0).UTC()
	for _, user := range []struct {
		id    int64
		email string
		token string
	}{{11, "legacy-inviter-one@example.test", strings.Repeat("1", 32)}, {12, "legacy-inviter-two@example.test", strings.Repeat("2", 32)}} {
		if _, err := database.db.ExecContext(ctx, `
			INSERT INTO users (id,email,password_hash,account_kind,subscription_token,created_at,updated_at)
			VALUES (?,?,'hash','human',?,?,?)
		`, user.id, user.email, user.token, now.Unix(), now.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	sourceSHA := strings.Repeat("a", 64)
	recordLegacyInvitationCodesPrerequisite(t, database, sourceSHA, now)
	protector, _ := security.NewInvitationProtector(bytes.Repeat([]byte{0x4a}, 32))
	usedAt := int64(1_700_000_300)
	codes := []LegacyInvitationCode{
		newLegacyInvitationCode(t, protector, 7, 11, "AbCd1234", 3, nil, 1_700_000_000, 1_700_000_100),
		newLegacyInvitationCode(t, protector, 9, 12, "ZyXw9876", 5, &usedAt, 1_700_000_200, usedAt),
	}
	input := LegacyInvitationCodesImport{
		Slice: LegacyInvitationCodesSlice, SourceSHA256: sourceSHA, SourceSize: 4096, Codes: codes,
		RollbackBackupPath: "/backups/invitation-codes.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
	}
	input.Checksum = LegacyInvitationCodesChecksum(input.Codes)
	report, err := database.ImportLegacyInvitationCodes(ctx, input, now)
	if err != nil {
		t.Fatalf("ImportLegacyInvitationCodes() error = %v", err)
	}
	if report.AlreadyApplied || report.Codes.SourceRows != 2 || report.Codes.TargetRows != 2 ||
		report.Codes.SourceChecksum != report.Codes.TargetChecksum || len(report.CipherFingerprint) != 64 {
		t.Fatalf("invitation code report = %#v", report)
	}
	var sequence int64
	if err := database.db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name='invitation_codes'`).Scan(&sequence); err != nil || sequence != 9 {
		t.Fatalf("invitation code sequence = %d, error = %v", sequence, err)
	}
	activeDigest, _ := protector.CodeDigest("AbCd1234")
	if err := database.CheckInvitationCode(ctx, activeDigest); err != nil {
		t.Fatalf("CheckInvitationCode(active) error = %v", err)
	}
	usedDigest, _ := protector.CodeDigest("ZyXw9876")
	if err := database.CheckInvitationCode(ctx, usedDigest); !errors.Is(err, ErrInvitationCodeInvalid) {
		t.Fatalf("CheckInvitationCode(used) error = %v", err)
	}
	if err := database.IncrementInvitationCodeView(ctx, usedDigest, now.Add(time.Minute)); err != nil {
		t.Fatalf("IncrementInvitationCodeView(used) error = %v", err)
	}
	var pv int64
	if err := database.db.QueryRow(`SELECT pv FROM invitation_codes WHERE id=9`).Scan(&pv); err != nil || pv != 6 {
		t.Fatalf("used invitation PV = %d, error = %v", pv, err)
	}
	var digest, cipher []byte
	if err := database.db.QueryRow(`SELECT code_digest,code_cipher FROM invitation_codes WHERE id=7`).Scan(&digest, &cipher); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(digest, []byte("AbCd1234")) || bytes.Contains(cipher, []byte("AbCd1234")) {
		t.Fatal("target database exposed a plaintext invitation code")
	}
	if _, err := database.db.Exec(`UPDATE invitation_codes SET consumed_at=?,updated_at=? WHERE id=7`, now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	newDigest, _ := protector.CodeDigest("NewC9012")
	newCipher, _ := protector.EncryptCode(12, "NewC9012")
	if _, err := database.CreateInvitationCode(ctx, 12, CreateInvitationCodeInput{CodeDigest: newDigest, CodeCipher: newCipher}, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("CreateInvitationCode(after import) error = %v", err)
	}
	repeated, err := database.ImportLegacyInvitationCodes(ctx, input, now.Add(2*time.Minute))
	if err != nil || !repeated.AlreadyApplied || repeated.AppliedAt != report.AppliedAt {
		t.Fatalf("idempotent ImportLegacyInvitationCodes() = (%#v, %v)", repeated, err)
	}
	if _, err := database.db.Exec(`UPDATE invitation_codes SET code_cipher=zeroblob(length(code_cipher)) WHERE id=7`); err != nil {
		t.Fatal(err)
	}
	if _, found, err := database.LookupLegacyInvitationCodesImport(ctx, sourceSHA); !errors.Is(err, ErrConflict) || found {
		t.Fatalf("LookupLegacyInvitationCodesImport(drift) = found %v, error %v", found, err)
	}
}

func TestImportLegacyInvitationCodesRequiresSameSourceUsersAndRollsBack(t *testing.T) {
	database := newTestStore(t)
	protector, _ := security.NewInvitationProtector(bytes.Repeat([]byte{0x3c}, 32))
	code := newLegacyInvitationCode(t, protector, 1, 99, "NoUser99", 0, nil, 100, 100)
	input := LegacyInvitationCodesImport{
		Slice: LegacyInvitationCodesSlice, SourceSHA256: strings.Repeat("c", 64), SourceSize: 1024,
		Codes: []LegacyInvitationCode{code}, RollbackBackupPath: "/backups/invitation-codes.xbbackup",
		RollbackBackupSHA256: strings.Repeat("d", 64),
	}
	input.Checksum = LegacyInvitationCodesChecksum(input.Codes)
	if _, err := database.ImportLegacyInvitationCodes(context.Background(), input, time.Unix(200, 0)); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "human users") {
		t.Fatalf("ImportLegacyInvitationCodes(missing prerequisite) error = %v", err)
	}
	var rows, runs int
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM invitation_codes`).Scan(&rows)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyInvitationCodesSlice).Scan(&runs)
	if rows != 0 || runs != 0 {
		t.Fatalf("failed invitation code import left rows=%d runs=%d", rows, runs)
	}
}

func TestImportLegacyInvitationCodesRollsBackAWriteFailure(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO users (id,email,password_hash,account_kind,subscription_token,created_at,updated_at)
		VALUES (11,'legacy-invitation-rollback@example.test','hash','human',?,?,?)
	`, strings.Repeat("1", 32), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	sourceSHA := strings.Repeat("e", 64)
	recordLegacyInvitationCodesPrerequisite(t, database, sourceSHA, now)
	if _, err := database.db.Exec(`
		CREATE TRIGGER reject_second_legacy_invitation_code
		BEFORE INSERT ON invitation_codes WHEN NEW.id=9
		BEGIN SELECT RAISE(ABORT,'injected invitation code failure'); END
	`); err != nil {
		t.Fatal(err)
	}
	protector, _ := security.NewInvitationProtector(bytes.Repeat([]byte{0x5b}, 32))
	codes := []LegacyInvitationCode{
		newLegacyInvitationCode(t, protector, 7, 11, "Roll1234", 0, nil, now.Unix(), now.Unix()),
		newLegacyInvitationCode(t, protector, 9, 11, "Back5678", 0, nil, now.Unix(), now.Unix()),
	}
	input := LegacyInvitationCodesImport{
		Slice: LegacyInvitationCodesSlice, SourceSHA256: sourceSHA, SourceSize: 1024, Codes: codes,
		RollbackBackupPath: "/backups/invitation-codes.xbbackup", RollbackBackupSHA256: strings.Repeat("f", 64),
	}
	input.Checksum = LegacyInvitationCodesChecksum(input.Codes)
	if _, err := database.ImportLegacyInvitationCodes(ctx, input, now.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "injected invitation code failure") {
		t.Fatalf("ImportLegacyInvitationCodes(injected failure) error = %v", err)
	}
	var rows, runs, sequence int
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM invitation_codes`).Scan(&rows)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyInvitationCodesSlice).Scan(&runs)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM sqlite_sequence WHERE name='invitation_codes'`).Scan(&sequence)
	if rows != 0 || runs != 0 || sequence != 0 {
		t.Fatalf("failed invitation code write left rows=%d runs=%d sequence=%d", rows, runs, sequence)
	}
}

func newLegacyInvitationCode(t *testing.T, protector *security.InvitationProtector, id, ownerID int64, code string, pv int64, consumedAt *int64, createdAt, updatedAt int64) LegacyInvitationCode {
	t.Helper()
	digest, err := protector.CodeDigest(code)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := protector.EncryptCode(ownerID, code)
	if err != nil {
		t.Fatal(err)
	}
	return LegacyInvitationCode{ID: id, UserID: ownerID, CodeDigest: digest, CodeCipher: cipher, PV: pv, ConsumedAt: consumedAt, CreatedAt: createdAt, UpdatedAt: updatedAt}
}

func recordLegacyInvitationCodesPrerequisite(t *testing.T, database *Store, sourceSHA string, now time.Time) {
	t.Helper()
	if _, err := database.db.Exec(`
		INSERT INTO legacy_migration_runs
		(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at)
		VALUES (?, ?, 1, 'prerequisite.xbbackup', ?, '{}', ?)
	`, LegacyHumanUsersSlice, sourceSHA, strings.Repeat("0", 64), now.Unix()); err != nil {
		t.Fatal(err)
	}
}
