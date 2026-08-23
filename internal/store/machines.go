package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
)

func (s *Store) CreateMachine(ctx context.Context, input CreateMachineInput, now time.Time) (Machine, EnrollmentSecret, error) {
	defer s.lockWrite()()
	input.Name = strings.TrimSpace(input.Name)
	input.Notes = strings.TrimSpace(input.Notes)
	if input.Name == "" || len(input.Name) > 255 || len(input.Notes) > 4000 {
		return Machine{}, EnrollmentSecret{}, fmt.Errorf("%w: invalid machine name or notes", ErrInvalidInput)
	}

	secret, err := security.NewOpaqueToken(36)
	if err != nil {
		return Machine{}, EnrollmentSecret{}, fmt.Errorf("generate enrollment: %w", err)
	}
	expiresAt := now.Add(15 * time.Minute)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Machine{}, EnrollmentSecret{}, fmt.Errorf("begin create machine: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO server_machines (name, notes, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, input.Name, input.Notes, input.IsActive, now.Unix(), now.Unix())
	if err != nil {
		return Machine{}, EnrollmentSecret{}, fmt.Errorf("insert machine: %w", err)
	}
	machineID, err := result.LastInsertId()
	if err != nil {
		return Machine{}, EnrollmentSecret{}, fmt.Errorf("read machine id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO server_machine_enrollments (machine_id, code_hash, revoke_existing, expires_at, created_at)
		VALUES (?, ?, 0, ?, ?)
	`, machineID, secret.Digest, expiresAt.Unix(), now.Unix()); err != nil {
		return Machine{}, EnrollmentSecret{}, fmt.Errorf("insert enrollment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Machine{}, EnrollmentSecret{}, fmt.Errorf("commit create machine: %w", err)
	}

	machine, err := s.GetMachine(ctx, machineID)
	if err != nil {
		return Machine{}, EnrollmentSecret{}, err
	}
	return machine, EnrollmentSecret{
		MachineID: machineID,
		Code:      secret.Plaintext,
		TokenType: "enrollment_code",
		ExpiresAt: expiresAt,
	}, nil
}

func (s *Store) CreateEnrollment(ctx context.Context, machineID int64, revokeExisting bool, now time.Time) (EnrollmentSecret, error) {
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EnrollmentSecret{}, fmt.Errorf("begin enrollment creation: %w", err)
	}
	defer tx.Rollback()

	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM server_machines WHERE id = ?)`, machineID).Scan(&exists); err != nil {
		return EnrollmentSecret{}, fmt.Errorf("check machine: %w", err)
	}
	if !exists {
		return EnrollmentSecret{}, ErrNotFound
	}

	secret, err := security.NewOpaqueToken(36)
	if err != nil {
		return EnrollmentSecret{}, fmt.Errorf("generate enrollment: %w", err)
	}
	expiresAt := now.Add(15 * time.Minute)
	if _, err := tx.ExecContext(ctx, `DELETE FROM server_machine_enrollments WHERE machine_id = ? AND consumed_at IS NULL`, machineID); err != nil {
		return EnrollmentSecret{}, fmt.Errorf("invalidate prior enrollments: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO server_machine_enrollments (machine_id, code_hash, revoke_existing, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, machineID, secret.Digest, revokeExisting, expiresAt.Unix(), now.Unix())
	if err != nil {
		return EnrollmentSecret{}, fmt.Errorf("create enrollment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return EnrollmentSecret{}, fmt.Errorf("commit enrollment creation: %w", err)
	}
	return EnrollmentSecret{
		MachineID:      machineID,
		Code:           secret.Plaintext,
		TokenType:      "enrollment_code",
		ExpiresAt:      expiresAt,
		RevokeExisting: revokeExisting,
	}, nil
}

func (s *Store) ExchangeEnrollment(ctx context.Context, expectedMachineID int64, code string, now time.Time) (MachineCredential, error) {
	defer s.lockWrite()()
	if expectedMachineID < 1 || code == "" {
		return MachineCredential{}, ErrInvalidEnrollment
	}
	credential, err := security.NewOpaqueToken(48)
	if err != nil {
		return MachineCredential{}, fmt.Errorf("generate machine credential: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MachineCredential{}, fmt.Errorf("begin enrollment exchange: %w", err)
	}
	defer tx.Rollback()

	var enrollmentID, machineID int64
	var revokeExisting, machineActive bool
	var expiresAt int64
	var consumedAt sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		SELECT e.id, e.machine_id, e.revoke_existing, e.expires_at, e.consumed_at, m.is_active
		FROM server_machine_enrollments e
		JOIN server_machines m ON m.id = e.machine_id
		WHERE e.code_hash = ? AND e.machine_id = ?
	`, security.DigestToken(code), expectedMachineID).Scan(&enrollmentID, &machineID, &revokeExisting, &expiresAt, &consumedAt, &machineActive)
	if errors.Is(err, sql.ErrNoRows) {
		return MachineCredential{}, ErrInvalidEnrollment
	}
	if err != nil {
		return MachineCredential{}, fmt.Errorf("load enrollment: %w", err)
	}
	if consumedAt.Valid || expiresAt <= now.Unix() || !machineActive {
		return MachineCredential{}, ErrInvalidEnrollment
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE server_machine_enrollments SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL
	`, now.Unix(), enrollmentID)
	if err != nil {
		return MachineCredential{}, fmt.Errorf("consume enrollment: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return MachineCredential{}, ErrInvalidEnrollment
	}
	if revokeExisting {
		if _, err := tx.ExecContext(ctx, `
			UPDATE server_machine_credentials SET revoked_at = ? WHERE machine_id = ? AND revoked_at IS NULL
		`, now.Unix(), machineID); err != nil {
			return MachineCredential{}, fmt.Errorf("revoke old credentials: %w", err)
		}
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO server_machine_credentials (machine_id, token_hash, token_prefix, created_at)
		VALUES (?, ?, ?, ?)
	`, machineID, credential.Digest, credential.Prefix, now.Unix())
	if err != nil {
		return MachineCredential{}, fmt.Errorf("store machine credential: %w", err)
	}
	if !revokeExisting {
		if _, err := tx.ExecContext(ctx, `
			UPDATE server_machine_credentials
			SET revoked_at = ?
			WHERE machine_id = ? AND revoked_at IS NULL AND id NOT IN (
				SELECT id FROM server_machine_credentials
				WHERE machine_id = ? AND revoked_at IS NULL
				ORDER BY created_at DESC, id DESC LIMIT 3
			)
		`, now.Unix(), machineID, machineID); err != nil {
			return MachineCredential{}, fmt.Errorf("limit active credentials: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return MachineCredential{}, fmt.Errorf("commit enrollment exchange: %w", err)
	}
	return MachineCredential{MachineID: machineID, Token: credential.Plaintext, Prefix: credential.Prefix}, nil
}

func (s *Store) AuthenticateMachine(ctx context.Context, expectedMachineID int64, token string, now time.Time) (Machine, error) {
	defer s.lockWrite()()
	var machine Machine
	var lastSeen sql.NullInt64
	var loadStatus sql.NullString
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT m.id, m.name, m.notes, m.is_active, m.last_seen_at, m.load_status, m.created_at, m.updated_at,
		       (SELECT COUNT(*) FROM nodes n WHERE n.machine_id = m.id)
		FROM server_machine_credentials c
		JOIN server_machines m ON m.id = c.machine_id
		WHERE c.token_hash = ? AND m.id = ? AND c.revoked_at IS NULL AND m.is_active = 1
	`, security.DigestToken(token), expectedMachineID).Scan(
		&machine.ID, &machine.Name, &machine.Notes, &machine.IsActive, &lastSeen, &loadStatus,
		&createdAt, &updatedAt, &machine.ServersCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Machine{}, ErrInvalidCredential
	}
	if err != nil {
		return Machine{}, fmt.Errorf("authenticate machine: %w", err)
	}
	machine.CreatedAt = time.Unix(createdAt, 0).UTC()
	machine.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	decodeMachineTimes(&machine, lastSeen)
	decodeLoadStatus(&machine, loadStatus)
	_, _ = s.db.ExecContext(ctx, `UPDATE server_machine_credentials SET last_used_at = ? WHERE token_hash = ?`, now.Unix(), security.DigestToken(token))
	_, _ = s.db.ExecContext(ctx, `UPDATE server_machines SET last_seen_at = ?, updated_at = ? WHERE id = ?`, now.Unix(), now.Unix(), machine.ID)
	return machine, nil
}

func (s *Store) RecordMachineStatus(ctx context.Context, machineID int64, input MachineStatusInput, now time.Time) error {
	defer s.lockWrite()()
	loadStatus := map[string]any{
		"cpu":        input.CPUPercent,
		"mem":        map[string]int64{"total": input.MemoryTotal, "used": input.MemoryUsed},
		"swap":       map[string]int64{"total": input.SwapTotal, "used": input.SwapUsed},
		"disk":       map[string]int64{"total": input.DiskTotal, "used": input.DiskUsed},
		"updated_at": now.Unix(),
	}
	networkIn, networkOut := 0.0, 0.0
	if input.NetworkIn != nil && input.NetworkOut != nil {
		networkIn, networkOut = *input.NetworkIn, *input.NetworkOut
		loadStatus["net"] = map[string]float64{"in_speed": networkIn, "out_speed": networkOut}
	}
	encoded, err := json.Marshal(loadStatus)
	if err != nil {
		return fmt.Errorf("encode load status: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin machine status: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE server_machines SET load_status = ?, last_seen_at = ?, updated_at = ? WHERE id = ? AND is_active = 1
	`, string(encoded), now.Unix(), now.Unix(), machineID)
	if err != nil {
		return fmt.Errorf("update machine status: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO server_machine_load_history (
			machine_id, cpu, mem_total, mem_used, disk_total, disk_used, net_in_speed, net_out_speed, recorded_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, machineID, input.CPUPercent, input.MemoryTotal, input.MemoryUsed, input.DiskTotal, input.DiskUsed, networkIn, networkOut, now.Unix()); err != nil {
		return fmt.Errorf("insert machine load history: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM server_machine_load_history WHERE machine_id = ? AND recorded_at < ?`, machineID, now.Add(-24*time.Hour).Unix()); err != nil {
		return fmt.Errorf("prune machine load history: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit machine status: %w", err)
	}
	return nil
}

func (s *Store) GetMachine(ctx context.Context, machineID int64) (Machine, error) {
	row := s.db.QueryRowContext(ctx, machineSelect+` WHERE m.id = ?`, machineID)
	return scanMachine(row)
}

func (s *Store) ListMachines(ctx context.Context) ([]Machine, error) {
	rows, err := s.db.QueryContext(ctx, machineSelect+` ORDER BY m.id`)
	if err != nil {
		return nil, fmt.Errorf("list machines: %w", err)
	}
	defer rows.Close()

	machines := make([]Machine, 0)
	for rows.Next() {
		machine, err := scanMachine(rows)
		if err != nil {
			return nil, err
		}
		machines = append(machines, machine)
	}
	return machines, rows.Err()
}

func (s *Store) UpdateMachine(ctx context.Context, machineID int64, input UpdateMachineInput, now time.Time) (Machine, error) {
	defer s.lockWrite()()
	input.Name = strings.TrimSpace(input.Name)
	input.Notes = strings.TrimSpace(input.Notes)
	if input.Name == "" || len(input.Name) > 255 || len(input.Notes) > 4000 {
		return Machine{}, fmt.Errorf("%w: invalid machine name or notes", ErrInvalidInput)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE server_machines SET name = ?, notes = ?, is_active = ?, updated_at = ? WHERE id = ?
	`, input.Name, input.Notes, input.IsActive, now.Unix(), machineID)
	if err != nil {
		return Machine{}, fmt.Errorf("update machine: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return Machine{}, ErrNotFound
	}
	return s.GetMachine(ctx, machineID)
}

func (s *Store) DeleteMachine(ctx context.Context, machineID int64, now time.Time) error {
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete machine: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT d.user_id
		FROM node_device_ips d JOIN nodes n ON n.id = d.node_id
		WHERE n.machine_id = ?
	`, machineID)
	if err != nil {
		return fmt.Errorf("list machine device users: %w", err)
	}
	var userIDs []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			rows.Close()
			return fmt.Errorf("scan machine device user: %w", err)
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close machine device users: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_device_ips WHERE node_id IN (SELECT id FROM nodes WHERE machine_id = ?)`, machineID); err != nil {
		return fmt.Errorf("clear machine devices: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_user_online WHERE node_id IN (SELECT id FROM nodes WHERE machine_id = ?)`, machineID); err != nil {
		return fmt.Errorf("clear machine online state: %w", err)
	}
	for _, userID := range userIDs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE users SET online_count = (
				SELECT COUNT(DISTINCT ip) FROM node_device_ips WHERE user_id = ? AND expires_at > ?
			) WHERE id = ?
		`, userID, now.Unix(), userID); err != nil {
			return fmt.Errorf("reconcile deleted machine devices: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM server_machines WHERE id = ?`, machineID)
	if err != nil {
		return fmt.Errorf("delete machine: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete machine: %w", err)
	}
	return nil
}

const machineSelect = `
	SELECT m.id, m.name, m.notes, m.is_active, m.last_seen_at, m.load_status, m.created_at, m.updated_at,
	       (SELECT COUNT(*) FROM nodes n WHERE n.machine_id = m.id)
	FROM server_machines m`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMachine(row rowScanner) (Machine, error) {
	var machine Machine
	var lastSeen sql.NullInt64
	var loadStatus sql.NullString
	var createdAt, updatedAt int64
	err := row.Scan(
		&machine.ID, &machine.Name, &machine.Notes, &machine.IsActive, &lastSeen, &loadStatus,
		&createdAt, &updatedAt, &machine.ServersCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Machine{}, ErrNotFound
	}
	if err != nil {
		return Machine{}, fmt.Errorf("scan machine: %w", err)
	}
	machine.CreatedAt = time.Unix(createdAt, 0).UTC()
	machine.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	decodeMachineTimes(&machine, lastSeen)
	decodeLoadStatus(&machine, loadStatus)
	return machine, nil
}

func decodeMachineTimes(machine *Machine, lastSeen sql.NullInt64) {
	if lastSeen.Valid {
		value := time.Unix(lastSeen.Int64, 0).UTC()
		machine.LastSeenAt = &value
	}
}

func decodeLoadStatus(machine *Machine, loadStatus sql.NullString) {
	if loadStatus.Valid && json.Valid([]byte(loadStatus.String)) {
		machine.LoadStatus = json.RawMessage(loadStatus.String)
	} else {
		machine.LoadStatus = json.RawMessage("null")
	}
}
