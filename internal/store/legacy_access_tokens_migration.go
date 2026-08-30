package store

import (
	"bufio"
	"context"
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
	LegacyAccessTokensSlice = "access-tokens-v1"
	maxLegacyAccessTokens   = 2_000_000
)

type LegacyAccessToken struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"user_id"`
	TokenHash  string `json:"token_hash"`
	Name       string `json:"name"`
	LastUsedAt *int64 `json:"last_used_at"`
	ExpiresAt  *int64 `json:"expires_at"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

type LegacyAccessTokensImport struct {
	Slice                string
	SourceSHA256         string
	SourceSize           int64
	Tokens               []LegacyAccessToken
	Checksum             string
	RollbackBackupPath   string
	RollbackBackupSHA256 string
}

type LegacyAccessTokensImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Tokens               LegacyDomainResult `json:"tokens"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func LegacyAccessTokensChecksum(tokens []LegacyAccessToken) string {
	order := make([]int, len(tokens))
	for index := range order {
		order[index] = index
	}
	sort.Slice(order, func(left, right int) bool { return tokens[order[left]].ID < tokens[order[right]].ID })
	encoder := newLegacyAccessTokenChecksumEncoder(len(tokens))
	for _, index := range order {
		encoder.Write(tokens[index])
	}
	return encoder.Sum()
}

type legacyAccessTokenChecksumEncoder struct {
	hash    hash.Hash
	writer  *bufio.Writer
	integer [8]byte
}

func newLegacyAccessTokenChecksumEncoder(count int) *legacyAccessTokenChecksumEncoder {
	digest := sha256.New()
	encoder := &legacyAccessTokenChecksumEncoder{hash: digest, writer: bufio.NewWriterSize(digest, 64<<10)}
	encoder.writeInteger(int64(count))
	return encoder
}

func (e *legacyAccessTokenChecksumEncoder) Write(token LegacyAccessToken) {
	e.writeInteger(token.ID)
	e.writeInteger(token.UserID)
	e.writeString(token.TokenHash)
	e.writeString(token.Name)
	e.writeOptionalInteger(token.LastUsedAt)
	e.writeOptionalInteger(token.ExpiresAt)
	e.writeInteger(token.CreatedAt)
	e.writeInteger(token.UpdatedAt)
}

func (e *legacyAccessTokenChecksumEncoder) Sum() string {
	_ = e.writer.Flush()
	return hex.EncodeToString(e.hash.Sum(nil))
}

func (e *legacyAccessTokenChecksumEncoder) writeInteger(value int64) {
	binary.BigEndian.PutUint64(e.integer[:], uint64(value))
	_, _ = e.writer.Write(e.integer[:])
}

func (e *legacyAccessTokenChecksumEncoder) writeString(value string) {
	e.writeInteger(int64(len(value)))
	_, _ = e.writer.WriteString(value)
}

func (e *legacyAccessTokenChecksumEncoder) writeOptionalInteger(value *int64) {
	if value == nil {
		_ = e.writer.WriteByte(0)
		return
	}
	_ = e.writer.WriteByte(1)
	e.writeInteger(*value)
}

func ValidateLegacyAccessTokensData(tokens []LegacyAccessToken) error {
	if len(tokens) > maxLegacyAccessTokens {
		return fmt.Errorf("%w: legacy access tokens exceed the %d-row migration limit", ErrInvalidInput, maxLegacyAccessTokens)
	}
	ids := make(map[int64]struct{}, len(tokens))
	hashes := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		if token.ID < 1 || token.UserID < 1 || !validAccessTokenHash(token.TokenHash) ||
			!validLegacyAccessTokenName(token.Name) || !validLegacyUnixTimestamp(token.CreatedAt) ||
			!validLegacyUnixTimestamp(token.UpdatedAt) || token.UpdatedAt < token.CreatedAt ||
			!validLegacyAccessTokenOptionalTime(token.LastUsedAt, token.CreatedAt, false) ||
			!validLegacyAccessTokenOptionalTime(token.ExpiresAt, token.CreatedAt, true) {
			return fmt.Errorf("%w: invalid legacy access token id %d", ErrInvalidInput, token.ID)
		}
		if _, duplicate := ids[token.ID]; duplicate {
			return fmt.Errorf("%w: duplicate legacy access token id %d", ErrConflict, token.ID)
		}
		if _, duplicate := hashes[token.TokenHash]; duplicate {
			return fmt.Errorf("%w: duplicate legacy access token hash", ErrConflict)
		}
		ids[token.ID] = struct{}{}
		hashes[token.TokenHash] = struct{}{}
	}
	return nil
}

func validLegacyAccessTokenName(value string) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= 80 &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func validLegacyAccessTokenOptionalTime(value *int64, createdAt int64, strictlyAfter bool) bool {
	if value == nil || !validLegacyUnixTimestamp(*value) {
		return value == nil
	}
	if strictlyAfter {
		return *value > createdAt
	}
	return *value >= createdAt
}

func (s *Store) LookupLegacyAccessTokensImport(ctx context.Context, sourceSHA256 string) (LegacyAccessTokensImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyAccessTokensImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacyAccessTokensImport(ctx, s.db, sourceSHA256)
}

func lookupLegacyAccessTokensImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyAccessTokensImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?`, LegacyAccessTokensSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyAccessTokensImportReport{}, false, nil
	}
	if err != nil {
		return LegacyAccessTokensImportReport{}, false, fmt.Errorf("lookup legacy access token migration: %w", err)
	}
	var report LegacyAccessTokensImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyAccessTokensImportReport{}, false, fmt.Errorf("decode legacy access token migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyAccessTokens(ctx context.Context, input LegacyAccessTokensImport, now time.Time) (LegacyAccessTokensImportReport, error) {
	if err := validateLegacyAccessTokensImport(input); err != nil {
		return LegacyAccessTokensImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyAccessTokensImportReport{}, fmt.Errorf("begin legacy access token import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return LegacyAccessTokensImportReport{}, fmt.Errorf("read legacy access token target schema: %w", err)
	}
	if version != CurrentSchemaVersion() {
		return LegacyAccessTokensImportReport{}, fmt.Errorf("legacy access token import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyAccessTokensImportReport{}, fmt.Errorf("validate legacy access token target schema: %w", err)
	}
	if existing, found, err := lookupLegacyAccessTokensImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyAccessTokensImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyAccessTokensImportReport{}, fmt.Errorf("commit idempotent legacy access token import: %w", err)
		}
		return existing, nil
	}
	var otherRuns, prerequisiteRuns, targetRows int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyAccessTokensSlice).Scan(&otherRuns); err != nil {
		return LegacyAccessTokensImportReport{}, fmt.Errorf("count legacy access token migrations: %w", err)
	}
	if otherRuns != 0 {
		return LegacyAccessTokensImportReport{}, fmt.Errorf("%w: legacy access token slice was already imported from another snapshot", ErrConflict)
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM legacy_migration_runs
		WHERE source_sha256 = ? AND slice = ?
	`, input.SourceSHA256, LegacyHumanUsersSlice).Scan(&prerequisiteRuns); err != nil {
		return LegacyAccessTokensImportReport{}, fmt.Errorf("validate legacy access token prerequisites: %w", err)
	}
	if prerequisiteRuns != 1 {
		return LegacyAccessTokensImportReport{}, fmt.Errorf("%w: import human users from the same snapshot before access tokens", ErrConflict)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_tokens`).Scan(&targetRows); err != nil {
		return LegacyAccessTokensImportReport{}, fmt.Errorf("count target access tokens: %w", err)
	}
	if targetRows != 0 {
		return LegacyAccessTokensImportReport{}, fmt.Errorf("%w: legacy access token import requires an empty access token target", ErrConflict)
	}
	if err := validateLegacyAccessTokenUsers(ctx, tx, input.Tokens); err != nil {
		return LegacyAccessTokensImportReport{}, err
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO access_tokens
		(id, user_id, token_hash, name, expires_at, last_used_at, revoked_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?)
	`)
	if err != nil {
		return LegacyAccessTokensImportReport{}, fmt.Errorf("prepare legacy access token import: %w", err)
	}
	defer statement.Close()
	for _, token := range input.Tokens {
		if _, err := statement.ExecContext(ctx, token.ID, token.UserID, token.TokenHash, token.Name,
			nullableInt64Value(token.ExpiresAt), nullableInt64Value(token.LastUsedAt), token.CreatedAt, token.UpdatedAt); err != nil {
			return LegacyAccessTokensImportReport{}, fmt.Errorf("import legacy access token id %d: %w", token.ID, err)
		}
	}
	targetRows, targetChecksum, err := summarizeLegacyTargetAccessTokens(ctx, tx)
	if err != nil {
		return LegacyAccessTokensImportReport{}, err
	}
	report := LegacyAccessTokensImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Tokens:    LegacyDomainResult{SourceRows: len(input.Tokens), TargetRows: targetRows, SourceChecksum: input.Checksum, TargetChecksum: targetChecksum},
		AppliedAt: now.UTC(),
	}
	if report.Tokens.SourceRows != report.Tokens.TargetRows || report.Tokens.SourceChecksum != report.Tokens.TargetChecksum {
		return LegacyAccessTokensImportReport{}, errors.New("legacy access token target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyAccessTokensImportReport{}, fmt.Errorf("encode legacy access token migration report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO legacy_migration_runs
		(slice, source_sha256, source_size, rollback_backup_path, rollback_backup_sha256, report_json, applied_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyAccessTokensImportReport{}, fmt.Errorf("record legacy access token migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyAccessTokensImportReport{}, fmt.Errorf("commit legacy access token import: %w", err)
	}
	return report, nil
}

func validateLegacyAccessTokensImport(input LegacyAccessTokensImport) error {
	if input.Slice != LegacyAccessTokensSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) ||
		strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 || !validLowerSHA256(input.RollbackBackupSHA256) ||
		input.Checksum != LegacyAccessTokensChecksum(input.Tokens) {
		return fmt.Errorf("%w: invalid legacy access token import", ErrInvalidInput)
	}
	return ValidateLegacyAccessTokensData(input.Tokens)
}

func validateLegacyAccessTokenUsers(ctx context.Context, tx *sql.Tx, tokens []LegacyAccessToken) error {
	referenced := make(map[int64]struct{})
	for _, token := range tokens {
		referenced[token.UserID] = struct{}{}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM users WHERE account_kind = 'human'`)
	if err != nil {
		return fmt.Errorf("list human users for legacy access tokens: %w", err)
	}
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan human user for legacy access tokens: %w", err)
		}
		delete(referenced, userID)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close human users for legacy access tokens: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate human users for legacy access tokens: %w", err)
	}
	if len(referenced) != 0 {
		var missing int64
		for userID := range referenced {
			if missing == 0 || userID < missing {
				missing = userID
			}
		}
		return fmt.Errorf("%w: legacy access tokens reference missing human user %d", ErrConflict, missing)
	}
	return nil
}

func summarizeLegacyTargetAccessTokens(ctx context.Context, database queryer) (int, string, error) {
	var count int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_tokens`).Scan(&count); err != nil {
		return 0, "", fmt.Errorf("count imported legacy access tokens: %w", err)
	}
	rows, err := database.QueryContext(ctx, `
		SELECT id, user_id, token_hash, name, last_used_at, expires_at, created_at, updated_at
		FROM access_tokens ORDER BY id
	`)
	if err != nil {
		return 0, "", fmt.Errorf("read imported legacy access tokens: %w", err)
	}
	defer rows.Close()
	encoder := newLegacyAccessTokenChecksumEncoder(count)
	seen := 0
	for rows.Next() {
		var token LegacyAccessToken
		var lastUsedAt, expiresAt sql.NullInt64
		if err := rows.Scan(&token.ID, &token.UserID, &token.TokenHash, &token.Name, &lastUsedAt, &expiresAt, &token.CreatedAt, &token.UpdatedAt); err != nil {
			return 0, "", fmt.Errorf("scan imported legacy access token: %w", err)
		}
		token.LastUsedAt = nullableInt64Pointer(lastUsedAt)
		token.ExpiresAt = nullableInt64Pointer(expiresAt)
		encoder.Write(token)
		seen++
	}
	if err := rows.Err(); err != nil {
		return 0, "", fmt.Errorf("iterate imported legacy access tokens: %w", err)
	}
	if seen != count {
		return 0, "", errors.New("imported legacy access token count changed during verification")
	}
	return count, encoder.Sum(), nil
}
