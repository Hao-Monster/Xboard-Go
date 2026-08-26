package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	invitationDigestBytes    = 32
	minInvitationCipherBytes = 32
	maxInvitationCipherBytes = 128
)

type activeInvitation struct {
	id      int64
	ownerID int64
}

func (s *Store) InvitationProtectionRequired(ctx context.Context) (bool, error) {
	var required bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT invite_force = 1 OR EXISTS(SELECT 1 FROM invitation_codes LIMIT 1)
		FROM app_settings WHERE id = 1
	`).Scan(&required); err != nil {
		return false, fmt.Errorf("read invitation protection requirement: %w", err)
	}
	return required, nil
}

// InvitationProtectionProbe returns one owner-bound ciphertext so startup can
// authenticate the configured key before serving requests. It never exposes a
// searchable digest or plaintext code.
func (s *Store) InvitationProtectionProbe(ctx context.Context) (int64, []byte, bool, error) {
	var ownerID int64
	var ciphertext []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, code_cipher FROM invitation_codes ORDER BY id LIMIT 1
	`).Scan(&ownerID, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, false, nil
	}
	if err != nil {
		return 0, nil, false, fmt.Errorf("read invitation protection probe: %w", err)
	}
	return ownerID, ciphertext, true, nil
}

func (s *Store) CreateInvitationCode(ctx context.Context, ownerID int64, input CreateInvitationCodeInput, now time.Time) (InvitationCode, error) {
	if ownerID < 1 || len(input.CodeDigest) != invitationDigestBytes ||
		len(input.CodeCipher) < minInvitationCipherBytes || len(input.CodeCipher) > maxInvitationCipherBytes || now.IsZero() {
		return InvitationCode{}, fmt.Errorf("%w: invalid invitation code", ErrInvalidInput)
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InvitationCode{}, fmt.Errorf("begin invitation code creation: %w", err)
	}
	defer tx.Rollback()
	var limit, activeCount int
	if err := tx.QueryRowContext(ctx, `SELECT invite_gen_limit FROM app_settings WHERE id = 1`).Scan(&limit); err != nil {
		return InvitationCode{}, fmt.Errorf("read invitation code limit: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM invitation_codes WHERE user_id = ? AND consumed_at IS NULL`, ownerID).Scan(&activeCount); err != nil {
		return InvitationCode{}, fmt.Errorf("count active invitation codes: %w", err)
	}
	if activeCount >= limit {
		return InvitationCode{}, ErrInvitationCodeLimit
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO invitation_codes (user_id, code_digest, code_cipher, pv, created_at, updated_at)
		VALUES (?, ?, ?, 0, ?, ?)
	`, ownerID, input.CodeDigest, input.CodeCipher, now.Unix(), now.Unix())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return InvitationCode{}, ErrInvitationCodeCollision
		}
		return InvitationCode{}, fmt.Errorf("insert invitation code: %w", err)
	}
	codeID, err := result.LastInsertId()
	if err != nil {
		return InvitationCode{}, fmt.Errorf("read invitation code id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return InvitationCode{}, fmt.Errorf("commit invitation code creation: %w", err)
	}
	return InvitationCode{
		ID: codeID, OwnerID: ownerID, CodeCipher: append([]byte(nil), input.CodeCipher...),
		CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}, nil
}

func (s *Store) GetInvitationSummary(ctx context.Context, ownerID int64) (InvitationSummary, error) {
	if ownerID < 1 {
		return InvitationSummary{}, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, code_cipher, pv, created_at, updated_at
		FROM invitation_codes
		WHERE user_id = ? AND consumed_at IS NULL
		ORDER BY created_at DESC, id DESC
	`, ownerID)
	if err != nil {
		return InvitationSummary{}, fmt.Errorf("list invitation codes: %w", err)
	}
	codes := make([]InvitationCode, 0)
	for rows.Next() {
		var code InvitationCode
		var createdAt, updatedAt int64
		if err := rows.Scan(&code.ID, &code.OwnerID, &code.CodeCipher, &code.PV, &createdAt, &updatedAt); err != nil {
			_ = rows.Close()
			return InvitationSummary{}, fmt.Errorf("scan invitation code: %w", err)
		}
		code.CreatedAt = time.Unix(createdAt, 0).UTC()
		code.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		codes = append(codes, code)
	}
	if err := rows.Close(); err != nil {
		return InvitationSummary{}, fmt.Errorf("close invitation code rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return InvitationSummary{}, fmt.Errorf("iterate invitation codes: %w", err)
	}
	summary := InvitationSummary{Codes: codes}
	if err := populateInvitationCommissionSummary(ctx, s.db, ownerID, &summary); err != nil {
		return InvitationSummary{}, err
	}
	return summary, nil
}

func (s *Store) CheckInvitationCode(ctx context.Context, codeDigest []byte) error {
	if len(codeDigest) != invitationDigestBytes {
		return ErrInvitationCodeInvalid
	}
	if _, err := readActiveInvitation(ctx, s.db, codeDigest); errors.Is(err, sql.ErrNoRows) {
		return ErrInvitationCodeInvalid
	} else if err != nil {
		return fmt.Errorf("check invitation code: %w", err)
	}
	return nil
}

// IncrementInvitationCodeView deliberately does not report whether the code
// exists, preserving the public endpoint's non-enumerating response contract.
func (s *Store) IncrementInvitationCodeView(ctx context.Context, codeDigest []byte, now time.Time) error {
	if len(codeDigest) != invitationDigestBytes || now.IsZero() {
		return fmt.Errorf("%w: invalid invitation view", ErrInvalidInput)
	}
	defer s.lockWrite()()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE invitation_codes
		SET pv = CASE WHEN pv < ? THEN pv + 1 ELSE pv END, updated_at = ?
		WHERE code_digest = ?
	`, int64(math.MaxInt64), now.Unix(), codeDigest); err != nil {
		return fmt.Errorf("increment invitation code view: %w", err)
	}
	return nil
}

type invitationQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readActiveInvitation(ctx context.Context, query invitationQuery, codeDigest []byte) (activeInvitation, error) {
	var code activeInvitation
	err := query.QueryRowContext(ctx, `
		SELECT id, user_id FROM invitation_codes WHERE code_digest = ? AND consumed_at IS NULL
	`, codeDigest).Scan(&code.id, &code.ownerID)
	return code, err
}
