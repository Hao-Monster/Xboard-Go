package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/Hao-Monster/Xboard-Go/internal/security"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"time"
)

// Reads never rotate existing hash-only credentials or interrupt installed agents.
func (s *Store) MachineToken(ctx context.Context, machineID int64, box *appsettings.Cipher) (string, error) {
	if _, err := s.GetMachine(ctx, machineID); err != nil {
		return "", err
	}
	var payload []byte
	var digest string
	err := s.db.QueryRowContext(ctx, `SELECT token_cipher, token_hash FROM server_machine_credentials WHERE machine_id = ? AND revoked_at IS NULL ORDER BY created_at DESC, id DESC LIMIT 1`, machineID).Scan(&payload, &digest)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && len(payload) == 0) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read machine token: %w", err)
	}
	plaintext, err := box.DecryptFor(appsettings.MachineTokenPurpose, payload)
	if err != nil {
		return "", err
	}
	// Bind ciphertext to the credential record as well as its encryption purpose.
	if security.DigestToken(string(plaintext)) != digest {
		return "", errors.New("machine token digest mismatch")
	}
	return string(plaintext), nil
}

func (s *Store) ResetMachineToken(ctx context.Context, machineID int64, box *appsettings.Cipher, now time.Time) (string, error) {
	defer s.lockWrite()()
	token, err := security.NewOpaqueToken(48)
	if err != nil {
		return "", err
	}
	encrypted, err := box.EncryptFor(appsettings.MachineTokenPurpose, []byte(token.Plaintext))
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT id FROM server_machines WHERE id = ?`, machineID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE server_machine_credentials SET revoked_at = ? WHERE machine_id = ? AND revoked_at IS NULL`, now.Unix(), machineID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM server_machine_enrollments WHERE machine_id = ? AND consumed_at IS NULL`, machineID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO server_machine_credentials(machine_id, token_hash, token_prefix, token_cipher, created_at) VALUES(?,?,?,?,?)`, machineID, token.Digest, token.Prefix, encrypted, now.Unix()); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return token.Plaintext, nil
}
