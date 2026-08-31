package legacymigration

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	maxLegacyInvitationCodeRows      = 2_000_000
	maxLegacyInvitationCodeDataBytes = int64(128 << 20)
)

type InvitationCodeSource struct {
	ID        int64
	UserID    int64
	Code      []byte
	Status    int64
	PV        int64
	CreatedAt int64
	UpdatedAt int64
}

type InvitationCodesSnapshot struct {
	Path   string
	Size   int64
	SHA256 string
	Codes  []InvitationCodeSource
}

func ReadInvitationCodesSnapshot(ctx context.Context, sourcePath string) (InvitationCodesSnapshot, error) {
	codes := []InvitationCodeSource{}
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_invite_code", []string{
			"id", "user_id", "code", "status", "pv", "created_at", "updated_at",
		}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*),COALESCE(SUM(length(CAST(code AS BLOB))),0) FROM v2_invite_code
		`, maxLegacyInvitationCodeRows, maxLegacyInvitationCodeDataBytes, "legacy invitation codes"); err != nil {
			return err
		}
		rows, err := database.QueryContext(ctx, `
			SELECT id,user_id,CAST(code AS BLOB),status,pv,created_at,updated_at
			FROM v2_invite_code ORDER BY id
		`)
		if err != nil {
			return fmt.Errorf("read legacy invitation codes: %w", err)
		}
		defer rows.Close()
		var bytesRead int64
		seenIDs := make(map[int64]struct{})
		seenCodes := make(map[[sha256.Size]byte]struct{})
		for rows.Next() {
			if len(codes) >= maxLegacyInvitationCodeRows {
				return fmt.Errorf("legacy invitation codes exceed the %d-row migration limit", maxLegacyInvitationCodeRows)
			}
			var code InvitationCodeSource
			if err := rows.Scan(&code.ID, &code.UserID, &code.Code, &code.Status, &code.PV, &code.CreatedAt, &code.UpdatedAt); err != nil {
				return fmt.Errorf("scan legacy invitation code: %w", err)
			}
			bytesRead += int64(len(code.Code))
			if bytesRead > maxLegacyInvitationCodeDataBytes {
				return errors.New("legacy invitation codes exceed the migration data limit")
			}
			if code.ID < 1 || code.UserID < 1 || !validLegacyInvitationCodeBytes(code.Code) ||
				(code.Status != 0 && code.Status != 1) || code.PV < 0 ||
				code.CreatedAt < 0 || code.UpdatedAt < code.CreatedAt {
				return fmt.Errorf("legacy invitation code id %d is invalid", code.ID)
			}
			if _, duplicate := seenIDs[code.ID]; duplicate {
				return fmt.Errorf("legacy invitation code id %d is duplicated", code.ID)
			}
			codeFingerprint := sha256.Sum256(code.Code)
			if _, duplicate := seenCodes[codeFingerprint]; duplicate {
				return fmt.Errorf("legacy invitation codes contain a duplicate code at id %d", code.ID)
			}
			seenIDs[code.ID] = struct{}{}
			seenCodes[codeFingerprint] = struct{}{}
			codes = append(codes, code)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate legacy invitation codes: %w", err)
		}
		return nil
	})
	if err != nil {
		clearInvitationCodeSources(codes)
		return InvitationCodesSnapshot{}, err
	}
	return InvitationCodesSnapshot{Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256, Codes: codes}, nil
}

func (snapshot InvitationCodesSnapshot) Prepare(protector *security.InvitationProtector) ([]store.LegacyInvitationCode, string, error) {
	if len(snapshot.Codes) != 0 && protector == nil {
		return nil, "", errors.New("invitation protection is unavailable")
	}
	prepared := make([]store.LegacyInvitationCode, 0, len(snapshot.Codes))
	for _, source := range snapshot.Codes {
		digest, err := protector.CodeDigest(string(source.Code))
		if err != nil {
			clearPreparedInvitationCodes(prepared)
			return nil, "", fmt.Errorf("protect legacy invitation code id %d", source.ID)
		}
		ciphertext, err := protector.EncryptCode(source.UserID, string(source.Code))
		if err != nil {
			clearBytes(digest)
			clearPreparedInvitationCodes(prepared)
			return nil, "", fmt.Errorf("protect legacy invitation code id %d", source.ID)
		}
		verification, err := protector.DecryptCode(source.UserID, ciphertext)
		verified := err == nil && subtle.ConstantTimeCompare(verification, source.Code) == 1
		clearBytes(verification)
		if !verified {
			clearBytes(digest)
			clearBytes(ciphertext)
			clearPreparedInvitationCodes(prepared)
			return nil, "", fmt.Errorf("verify protected legacy invitation code id %d", source.ID)
		}
		var consumedAt *int64
		if source.Status == 1 {
			// The legacy model only retained a boolean status. Eloquent refreshed
			// updated_at when consumption saved that status, making it the sole
			// source-backed consumption timestamp available for migration.
			value := source.UpdatedAt
			consumedAt = &value
		}
		prepared = append(prepared, store.LegacyInvitationCode{
			ID: source.ID, UserID: source.UserID, CodeDigest: digest, CodeCipher: ciphertext,
			PV: source.PV, ConsumedAt: consumedAt, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt,
		})
	}
	return prepared, store.LegacyInvitationCodesChecksum(prepared), nil
}

func (snapshot *InvitationCodesSnapshot) ClearSecrets() {
	if snapshot == nil {
		return
	}
	clearInvitationCodeSources(snapshot.Codes)
}

func ClearPreparedInvitationCodes(codes []store.LegacyInvitationCode) {
	clearPreparedInvitationCodes(codes)
}

func clearPreparedInvitationCodes(codes []store.LegacyInvitationCode) {
	for index := range codes {
		clearBytes(codes[index].CodeDigest)
		clearBytes(codes[index].CodeCipher)
	}
}

func clearInvitationCodeSources(codes []InvitationCodeSource) {
	for index := range codes {
		clearBytes(codes[index].Code)
	}
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func validLegacyInvitationCodeBytes(code []byte) bool {
	if len(code) != 8 {
		return false
	}
	for _, character := range code {
		if (character < '0' || character > '9') && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') {
			return false
		}
	}
	return true
}
