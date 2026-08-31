package store

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	LegacyInvitationCodesSlice = "invitation-codes-v1"
	maxLegacyInvitationCodes   = 2_000_000
)

type LegacyInvitationCode struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"user_id"`
	CodeDigest []byte `json:"-"`
	CodeCipher []byte `json:"-"`
	PV         int64  `json:"pv"`
	ConsumedAt *int64 `json:"consumed_at"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

type LegacyInvitationCodesImport struct {
	Slice                string
	SourceSHA256         string
	SourceSize           int64
	Codes                []LegacyInvitationCode
	Checksum             string
	RollbackBackupPath   string
	RollbackBackupSHA256 string
}

type LegacyInvitationCodesImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Codes                LegacyDomainResult `json:"codes"`
	ImmutableChecksum    string             `json:"immutable_checksum"`
	CipherFingerprint    string             `json:"cipher_fingerprint"`
	Sequence             int64              `json:"sequence"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func LegacyInvitationCodesChecksum(codes []LegacyInvitationCode) string {
	order := make([]int, len(codes))
	for index := range order {
		order[index] = index
	}
	sort.Slice(order, func(left, right int) bool { return codes[order[left]].ID < codes[order[right]].ID })
	encoder := newLegacyInvitationCodeChecksumEncoder(len(codes))
	for _, index := range order {
		encoder.Write(codes[index])
	}
	return encoder.Sum()
}

func legacyInvitationCodesImmutableChecksum(codes []LegacyInvitationCode) string {
	order := make([]int, len(codes))
	for index := range order {
		order[index] = index
	}
	sort.Slice(order, func(left, right int) bool { return codes[order[left]].ID < codes[order[right]].ID })
	encoder := newLegacyInvitationCodeImmutableChecksumEncoder(len(codes))
	for _, index := range order {
		encoder.Write(codes[index])
	}
	return encoder.Sum()
}

type legacyInvitationCodeChecksumEncoder struct {
	hash    hash.Hash
	writer  *bufio.Writer
	integer [8]byte
}

func newLegacyInvitationCodeChecksumEncoder(count int) *legacyInvitationCodeChecksumEncoder {
	digest := sha256.New()
	encoder := &legacyInvitationCodeChecksumEncoder{hash: digest, writer: bufio.NewWriterSize(digest, 64<<10)}
	encoder.writeInteger(int64(count))
	return encoder
}

func (e *legacyInvitationCodeChecksumEncoder) Write(code LegacyInvitationCode) {
	e.writeInteger(code.ID)
	e.writeInteger(code.UserID)
	e.writeBytes(code.CodeDigest)
	e.writeInteger(code.PV)
	e.writeOptionalInteger(code.ConsumedAt)
	e.writeInteger(code.CreatedAt)
	e.writeInteger(code.UpdatedAt)
}

func (e *legacyInvitationCodeChecksumEncoder) Sum() string {
	_ = e.writer.Flush()
	return hex.EncodeToString(e.hash.Sum(nil))
}

func (e *legacyInvitationCodeChecksumEncoder) writeInteger(value int64) {
	binary.BigEndian.PutUint64(e.integer[:], uint64(value))
	_, _ = e.writer.Write(e.integer[:])
}

func (e *legacyInvitationCodeChecksumEncoder) writeBytes(value []byte) {
	e.writeInteger(int64(len(value)))
	_, _ = e.writer.Write(value)
}

func (e *legacyInvitationCodeChecksumEncoder) writeOptionalInteger(value *int64) {
	if value == nil {
		_ = e.writer.WriteByte(0)
		return
	}
	_ = e.writer.WriteByte(1)
	e.writeInteger(*value)
}

type legacyInvitationCodeImmutableChecksumEncoder struct {
	*legacyInvitationCodeChecksumEncoder
}

func newLegacyInvitationCodeImmutableChecksumEncoder(count int) *legacyInvitationCodeImmutableChecksumEncoder {
	return &legacyInvitationCodeImmutableChecksumEncoder{legacyInvitationCodeChecksumEncoder: newLegacyInvitationCodeChecksumEncoder(count)}
}

func (e *legacyInvitationCodeImmutableChecksumEncoder) Write(code LegacyInvitationCode) {
	e.writeInteger(code.ID)
	e.writeInteger(code.UserID)
	e.writeBytes(code.CodeDigest)
	e.writeInteger(code.CreatedAt)
}

func ValidateLegacyInvitationCodesData(codes []LegacyInvitationCode) error {
	if len(codes) > maxLegacyInvitationCodes {
		return fmt.Errorf("%w: legacy invitation codes exceed the %d-row migration limit", ErrInvalidInput, maxLegacyInvitationCodes)
	}
	ids := make(map[int64]struct{}, len(codes))
	digests := make(map[[sha256.Size]byte]struct{}, len(codes))
	for _, code := range codes {
		if code.ID < 1 || code.UserID < 1 || len(code.CodeDigest) != invitationDigestBytes ||
			len(code.CodeCipher) < minInvitationCipherBytes || len(code.CodeCipher) > maxInvitationCipherBytes ||
			code.PV < 0 || !validLegacyUnixTimestamp(code.CreatedAt) || !validLegacyUnixTimestamp(code.UpdatedAt) ||
			code.UpdatedAt < code.CreatedAt || code.ConsumedAt != nil && *code.ConsumedAt != code.UpdatedAt {
			return fmt.Errorf("%w: invalid legacy invitation code id %d", ErrInvalidInput, code.ID)
		}
		if _, duplicate := ids[code.ID]; duplicate {
			return fmt.Errorf("%w: duplicate legacy invitation code id %d", ErrConflict, code.ID)
		}
		var digest [sha256.Size]byte
		copy(digest[:], code.CodeDigest)
		if _, duplicate := digests[digest]; duplicate {
			return fmt.Errorf("%w: duplicate legacy invitation code digest", ErrConflict)
		}
		ids[code.ID] = struct{}{}
		digests[digest] = struct{}{}
	}
	return nil
}

func (s *Store) LookupLegacyInvitationCodesImport(ctx context.Context, sourceSHA256 string) (LegacyInvitationCodesImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyInvitationCodesImportReport{}, false, ErrInvalidInput
	}
	report, found, err := lookupLegacyInvitationCodesImport(ctx, s.db, sourceSHA256)
	if err != nil || !found {
		return report, found, err
	}
	if err := verifyLegacyInvitationCodesTarget(ctx, s.db, report); err != nil {
		return LegacyInvitationCodesImportReport{}, false, err
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func lookupLegacyInvitationCodesImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyInvitationCodesImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `
		SELECT report_json FROM legacy_migration_runs WHERE slice=? AND source_sha256=?
	`, LegacyInvitationCodesSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyInvitationCodesImportReport{}, false, nil
	}
	if err != nil {
		return LegacyInvitationCodesImportReport{}, false, fmt.Errorf("lookup legacy invitation code migration: %w", err)
	}
	var report LegacyInvitationCodesImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyInvitationCodesImportReport{}, false, fmt.Errorf("decode legacy invitation code migration report: %w", err)
	}
	if err := validateLegacyInvitationCodesReport(report, sourceSHA256); err != nil {
		return LegacyInvitationCodesImportReport{}, false, err
	}
	return report, true, nil
}

func (s *Store) ImportLegacyInvitationCodes(ctx context.Context, input LegacyInvitationCodesImport, now time.Time) (LegacyInvitationCodesImportReport, error) {
	if err := validateLegacyInvitationCodesImport(input, now); err != nil {
		return LegacyInvitationCodesImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyInvitationCodesImportReport{}, fmt.Errorf("begin legacy invitation code import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return LegacyInvitationCodesImportReport{}, fmt.Errorf("read legacy invitation code target schema: %w", err)
	}
	if version != CurrentSchemaVersion() {
		return LegacyInvitationCodesImportReport{}, fmt.Errorf("legacy invitation code import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyInvitationCodesImportReport{}, fmt.Errorf("validate legacy invitation code target schema: %w", err)
	}
	if existing, found, err := lookupLegacyInvitationCodesImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyInvitationCodesImportReport{}, err
	} else if found {
		if existing.Codes.SourceChecksum != input.Checksum {
			return LegacyInvitationCodesImportReport{}, fmt.Errorf("%w: legacy invitation code source differs from its migration ledger", ErrConflict)
		}
		if err := verifyLegacyInvitationCodesTarget(ctx, tx, existing); err != nil {
			return LegacyInvitationCodesImportReport{}, err
		}
		if err := tx.Commit(); err != nil {
			return LegacyInvitationCodesImportReport{}, fmt.Errorf("commit idempotent legacy invitation code import: %w", err)
		}
		existing.AlreadyApplied = true
		return existing, nil
	}
	var otherRuns, prerequisiteRuns, targetRows int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyInvitationCodesSlice).Scan(&otherRuns); err != nil {
		return LegacyInvitationCodesImportReport{}, fmt.Errorf("count legacy invitation code migrations: %w", err)
	}
	if otherRuns != 0 {
		return LegacyInvitationCodesImportReport{}, fmt.Errorf("%w: legacy invitation code slice was already imported from another snapshot", ErrConflict)
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM legacy_migration_runs WHERE source_sha256=? AND slice=?
	`, input.SourceSHA256, LegacyHumanUsersSlice).Scan(&prerequisiteRuns); err != nil {
		return LegacyInvitationCodesImportReport{}, fmt.Errorf("validate legacy invitation code prerequisites: %w", err)
	}
	if prerequisiteRuns != 1 {
		return LegacyInvitationCodesImportReport{}, fmt.Errorf("%w: import human users from the same snapshot before invitation codes", ErrConflict)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM invitation_codes`).Scan(&targetRows); err != nil {
		return LegacyInvitationCodesImportReport{}, fmt.Errorf("count target invitation codes: %w", err)
	}
	if targetRows != 0 {
		return LegacyInvitationCodesImportReport{}, fmt.Errorf("%w: legacy invitation code import requires an empty invitation code target", ErrConflict)
	}
	if err := validateLegacyInvitationCodeUsers(ctx, tx, input.Codes); err != nil {
		return LegacyInvitationCodesImportReport{}, err
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO invitation_codes
		(id,user_id,code_digest,code_cipher,pv,consumed_at,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?)
	`)
	if err != nil {
		return LegacyInvitationCodesImportReport{}, fmt.Errorf("prepare legacy invitation code import: %w", err)
	}
	defer statement.Close()
	var maximumID int64
	for _, code := range input.Codes {
		if _, err := statement.ExecContext(ctx, code.ID, code.UserID, code.CodeDigest, code.CodeCipher, code.PV,
			nullableInt64Value(code.ConsumedAt), code.CreatedAt, code.UpdatedAt); err != nil {
			return LegacyInvitationCodesImportReport{}, fmt.Errorf("import legacy invitation code id %d: %w", code.ID, err)
		}
		if code.ID > maximumID {
			maximumID = code.ID
		}
	}
	if err := advanceLegacySequence(ctx, tx, "invitation_codes", maximumID); err != nil {
		return LegacyInvitationCodesImportReport{}, err
	}
	rows, checksum, immutableChecksum, fingerprint, err := summarizeLegacyTargetInvitationCodes(ctx, tx, input.SourceSHA256, maximumID)
	if err != nil {
		return LegacyInvitationCodesImportReport{}, err
	}
	sequence, err := readLegacySequence(ctx, tx, "invitation_codes")
	if err != nil {
		return LegacyInvitationCodesImportReport{}, err
	}
	report := LegacyInvitationCodesImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Codes:             LegacyDomainResult{SourceRows: len(input.Codes), TargetRows: rows, SourceChecksum: input.Checksum, TargetChecksum: checksum},
		ImmutableChecksum: immutableChecksum, CipherFingerprint: fingerprint, Sequence: sequence, AppliedAt: now.UTC(),
	}
	if !legacyDomainMatches(report.Codes) || report.ImmutableChecksum != legacyInvitationCodesImmutableChecksum(input.Codes) || report.Sequence != maximumID {
		return LegacyInvitationCodesImportReport{}, errors.New("legacy invitation code target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyInvitationCodesImportReport{}, fmt.Errorf("encode legacy invitation code migration report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO legacy_migration_runs
		(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at)
		VALUES (?,?,?,?,?,?,?)
	`, report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath,
		report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyInvitationCodesImportReport{}, fmt.Errorf("record legacy invitation code migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyInvitationCodesImportReport{}, fmt.Errorf("commit legacy invitation code import: %w", err)
	}
	return report, nil
}

func validateLegacyInvitationCodesImport(input LegacyInvitationCodesImport, now time.Time) error {
	if input.Slice != LegacyInvitationCodesSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) ||
		strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 || !validLowerSHA256(input.RollbackBackupSHA256) ||
		input.Checksum != LegacyInvitationCodesChecksum(input.Codes) || now.IsZero() {
		return fmt.Errorf("%w: invalid legacy invitation code import", ErrInvalidInput)
	}
	return ValidateLegacyInvitationCodesData(input.Codes)
}

func validateLegacyInvitationCodeUsers(ctx context.Context, tx *sql.Tx, codes []LegacyInvitationCode) error {
	referenced := make(map[int64]struct{})
	for _, code := range codes {
		referenced[code.UserID] = struct{}{}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM users WHERE account_kind='human'`)
	if err != nil {
		return fmt.Errorf("list human users for legacy invitation codes: %w", err)
	}
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan human user for legacy invitation codes: %w", err)
		}
		delete(referenced, userID)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close human users for legacy invitation codes: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate human users for legacy invitation codes: %w", err)
	}
	if len(referenced) != 0 {
		var missing int64
		for userID := range referenced {
			if missing == 0 || userID < missing {
				missing = userID
			}
		}
		return fmt.Errorf("%w: legacy invitation codes reference missing human user %d", ErrConflict, missing)
	}
	return nil
}

func summarizeLegacyTargetInvitationCodes(ctx context.Context, database queryer, sourceSHA256 string, maximumID int64) (int, string, string, string, error) {
	var count int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM invitation_codes WHERE id<=?`, maximumID).Scan(&count); err != nil {
		return 0, "", "", "", fmt.Errorf("count imported legacy invitation codes: %w", err)
	}
	rows, err := database.QueryContext(ctx, `
		SELECT id,user_id,code_digest,code_cipher,pv,consumed_at,created_at,updated_at
		FROM invitation_codes WHERE id<=? ORDER BY id
	`, maximumID)
	if err != nil {
		return 0, "", "", "", fmt.Errorf("read imported legacy invitation codes: %w", err)
	}
	defer rows.Close()
	checksum := newLegacyInvitationCodeChecksumEncoder(count)
	immutableChecksum := newLegacyInvitationCodeImmutableChecksumEncoder(count)
	fingerprint := newLegacyInvitationCipherFingerprint(sourceSHA256, count)
	seen := 0
	for rows.Next() {
		var code LegacyInvitationCode
		var consumedAt sql.NullInt64
		if err := rows.Scan(&code.ID, &code.UserID, &code.CodeDigest, &code.CodeCipher, &code.PV,
			&consumedAt, &code.CreatedAt, &code.UpdatedAt); err != nil {
			return 0, "", "", "", fmt.Errorf("scan imported legacy invitation code: %w", err)
		}
		code.ConsumedAt = nullableInt64Pointer(consumedAt)
		checksum.Write(code)
		immutableChecksum.Write(code)
		fingerprint.Write(code.ID, code.UserID, code.CodeCipher)
		seen++
	}
	if err := rows.Err(); err != nil {
		return 0, "", "", "", fmt.Errorf("iterate imported legacy invitation codes: %w", err)
	}
	if seen != count {
		return 0, "", "", "", errors.New("imported legacy invitation code count changed during verification")
	}
	return count, checksum.Sum(), immutableChecksum.Sum(), fingerprint.Sum(), nil
}

type legacyInvitationCipherFingerprint struct {
	hash    hash.Hash
	integer [8]byte
}

func newLegacyInvitationCipherFingerprint(sourceSHA256 string, count int) *legacyInvitationCipherFingerprint {
	mac := hmac.New(sha256.New, []byte("xboard-go/legacy-invitation-codes/cipher-drift/v1/"+sourceSHA256))
	fingerprint := &legacyInvitationCipherFingerprint{hash: mac}
	fingerprint.writeInteger(int64(count))
	return fingerprint
}

func (f *legacyInvitationCipherFingerprint) Write(id, ownerID int64, cipher []byte) {
	f.writeInteger(id)
	f.writeInteger(ownerID)
	f.writeInteger(int64(len(cipher)))
	_, _ = f.hash.Write(cipher)
}

func (f *legacyInvitationCipherFingerprint) writeInteger(value int64) {
	binary.BigEndian.PutUint64(f.integer[:], uint64(value))
	_, _ = f.hash.Write(f.integer[:])
}

func (f *legacyInvitationCipherFingerprint) Sum() string {
	return hex.EncodeToString(f.hash.Sum(nil))
}

func validateLegacyInvitationCodesReport(report LegacyInvitationCodesImportReport, sourceSHA256 string) error {
	if report.Slice != LegacyInvitationCodesSlice || report.SourceSHA256 != sourceSHA256 || report.SourceSize < 1 ||
		report.RollbackBackupPath == "" || len(report.RollbackBackupPath) > 4096 || !utf8.ValidString(report.RollbackBackupPath) ||
		strings.IndexFunc(report.RollbackBackupPath, unicode.IsControl) >= 0 || !validLowerSHA256(report.RollbackBackupSHA256) ||
		report.Codes.SourceRows < 0 || report.Codes.SourceRows > maxLegacyInvitationCodes ||
		report.Codes.TargetRows != report.Codes.SourceRows || !validLowerSHA256(report.Codes.SourceChecksum) ||
		report.Codes.TargetChecksum != report.Codes.SourceChecksum || !validLowerSHA256(report.ImmutableChecksum) ||
		!validLowerSHA256(report.CipherFingerprint) ||
		report.Sequence < 0 || report.AppliedAt.IsZero() {
		return errors.New("stored legacy invitation code migration report is invalid")
	}
	return nil
}

func verifyLegacyInvitationCodesTarget(ctx context.Context, database queryer, report LegacyInvitationCodesImportReport) error {
	// Rows created after the import use IDs above the recorded sequence. PV,
	// consumed_at, and updated_at are intentionally excluded here because the
	// normal invitation workflow mutates them. Identity, ownership, lookup
	// digest, ciphertext, and creation time remain ledger-locked.
	rows, _, immutableChecksum, fingerprint, err := summarizeLegacyTargetInvitationCodes(ctx, database, report.SourceSHA256, report.Sequence)
	if err != nil {
		return err
	}
	if rows != report.Codes.TargetRows || immutableChecksum != report.ImmutableChecksum || fingerprint != report.CipherFingerprint {
		return fmt.Errorf("%w: imported legacy invitation codes no longer match their migration ledger", ErrConflict)
	}
	sequence, err := readLegacySequence(ctx, database, "invitation_codes")
	if err != nil {
		return err
	}
	if sequence < report.Sequence {
		return fmt.Errorf("%w: imported legacy invitation code sequence no longer matches its migration ledger", ErrConflict)
	}
	return nil
}
