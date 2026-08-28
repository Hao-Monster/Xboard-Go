package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/schedule"
	"github.com/Hao-Monster/Xboard-Go/internal/security"
)

func (s *Store) SaveDailySchedule(ctx context.Context, nodeID int64, timezone, enableTime, disableTime string, now time.Time) (ActivationSchedule, error) {
	defer s.lockWrite()()
	if timezone != "Asia/Singapore" {
		return ActivationSchedule{}, fmt.Errorf("%w: unsupported timezone", ErrInvalidInput)
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return ActivationSchedule{}, fmt.Errorf("%w: invalid timezone", ErrInvalidInput)
	}
	window, err := schedule.NewDailyWindow(location, enableTime, disableTime)
	if err != nil {
		return ActivationSchedule{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	state := window.StateAt(now)
	revision, err := security.NewOpaqueToken(18)
	if err != nil {
		return ActivationSchedule{}, fmt.Errorf("generate schedule revision: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ActivationSchedule{}, fmt.Errorf("begin schedule save: %w", err)
	}
	defer tx.Rollback()

	var machineID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT machine_id FROM nodes WHERE id = ?`, nodeID).Scan(&machineID); errors.Is(err, sql.ErrNoRows) {
		return ActivationSchedule{}, ErrNotFound
	} else if err != nil {
		return ActivationSchedule{}, fmt.Errorf("load schedule node: %w", err)
	}
	if !machineID.Valid {
		return ActivationSchedule{}, ErrNodeNotLinked
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO server_activation_schedules (
			node_id, schedule_type, timezone, enable_second, disable_second, revision,
			next_transition_at, next_target_enabled, created_at, updated_at
		) VALUES (?, 'daily', ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			schedule_type = 'daily', timezone = excluded.timezone,
			enable_second = excluded.enable_second, disable_second = excluded.disable_second,
			enable_at = NULL, disable_at = NULL, revision = excluded.revision,
			next_transition_at = excluded.next_transition_at,
			next_target_enabled = excluded.next_target_enabled,
			enabled_applied_at = NULL, disabled_applied_at = NULL,
			updated_at = excluded.updated_at
	`, nodeID, timezone, window.EnableSecond(), window.DisableSecond(), revision.Plaintext,
		state.NextTransition.Unix(), state.NextTargetEnabled, now.Unix(), now.Unix())
	if err != nil {
		return ActivationSchedule{}, fmt.Errorf("upsert schedule: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE nodes SET enabled = ?, admin_revision = admin_revision + 1, updated_at = ? WHERE id = ?`, state.Enabled, now.Unix(), nodeID); err != nil {
		return ActivationSchedule{}, fmt.Errorf("reconcile node state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ActivationSchedule{}, fmt.Errorf("commit schedule save: %w", err)
	}
	return s.GetActivationSchedule(ctx, nodeID)
}

func (s *Store) GetActivationSchedule(ctx context.Context, nodeID int64) (ActivationSchedule, error) {
	return scanActivationSchedule(s.db.QueryRowContext(ctx, scheduleSelect+` WHERE node_id = ?`, nodeID))
}

func (s *Store) DeleteActivationSchedule(ctx context.Context, nodeID int64) error {
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `DELETE FROM server_activation_schedules WHERE node_id = ?`, nodeID)
	if err != nil {
		return fmt.Errorf("delete activation schedule: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListDueSchedules(ctx context.Context, now time.Time, limit int) ([]DueSchedule, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("%w: due schedule limit must be between 1 and 1000", ErrInvalidInput)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT node_id, revision, next_transition_at
		FROM server_activation_schedules
		WHERE next_transition_at IS NOT NULL AND next_transition_at <= ?
		ORDER BY next_transition_at, node_id
		LIMIT ?
	`, now.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("list due schedules: %w", err)
	}
	defer rows.Close()

	due := make([]DueSchedule, 0)
	for rows.Next() {
		var item DueSchedule
		var transitionAt int64
		if err := rows.Scan(&item.NodeID, &item.Revision, &transitionAt); err != nil {
			return nil, fmt.Errorf("scan due schedule: %w", err)
		}
		item.NextTransitionAt = time.Unix(transitionAt, 0).UTC()
		due = append(due, item)
	}
	return due, rows.Err()
}

func (s *Store) ApplyDueSchedule(ctx context.Context, due DueSchedule, now time.Time) (bool, error) {
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin due schedule: %w", err)
	}
	defer tx.Rollback()

	var scheduleType string
	var timezone sql.NullString
	var enableSecond, disableSecond sql.NullInt64
	var enableAt, disableAt sql.NullInt64
	var currentTransition int64
	var revision string
	err = tx.QueryRowContext(ctx, `
		SELECT schedule_type, timezone, enable_second, disable_second, enable_at, disable_at, revision, next_transition_at
		FROM server_activation_schedules
		WHERE node_id = ?
	`, due.NodeID).Scan(&scheduleType, &timezone, &enableSecond, &disableSecond, &enableAt, &disableAt, &revision, &currentTransition)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load due schedule: %w", err)
	}
	if revision != due.Revision || currentTransition != due.NextTransitionAt.Unix() || currentTransition > now.Unix() {
		return false, nil
	}

	var enabled, nextTarget bool
	var nextTransition any
	if scheduleType == "daily" {
		location, err := time.LoadLocation(timezone.String)
		if err != nil {
			return false, fmt.Errorf("load schedule timezone: %w", err)
		}
		window, err := schedule.NewDailyWindow(location, formatScheduleClock(int(enableSecond.Int64)), formatScheduleClock(int(disableSecond.Int64)))
		if err != nil {
			return false, fmt.Errorf("load daily window: %w", err)
		}
		state := window.StateAt(now)
		enabled = state.Enabled
		nextTarget = state.NextTargetEnabled
		nextTransition = state.NextTransition.Unix()
	} else if scheduleType == "once" && enableAt.Valid && disableAt.Valid {
		switch {
		case now.Unix() < enableAt.Int64:
			nextTransition = enableAt.Int64
			nextTarget = true
		case now.Unix() < disableAt.Int64:
			enabled = true
			nextTransition = disableAt.Int64
		default:
			nextTransition = nil
		}
	} else {
		return false, fmt.Errorf("%w: invalid persisted activation schedule", ErrInvalidInput)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE server_activation_schedules
		SET next_transition_at = ?, next_target_enabled = ?, updated_at = ?
		WHERE node_id = ? AND revision = ? AND next_transition_at = ?
	`, nextTransition, nextTarget, now.Unix(), due.NodeID, due.Revision, currentTransition)
	if err != nil {
		return false, fmt.Errorf("advance due schedule: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes SET enabled = ?, admin_revision = admin_revision + 1, updated_at = ? WHERE id = ? AND machine_id IS NOT NULL
	`, enabled, now.Unix(), due.NodeID); err != nil {
		return false, fmt.Errorf("apply due node state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit due schedule: %w", err)
	}
	return true, nil
}

const scheduleSelect = `
	SELECT node_id, schedule_type, timezone, enable_second, disable_second, enable_at, disable_at, revision,
	       next_transition_at, next_target_enabled, created_at, updated_at
	FROM server_activation_schedules`

func scanActivationSchedule(row rowScanner) (ActivationSchedule, error) {
	var item ActivationSchedule
	var enableSecond, disableSecond sql.NullInt64
	var enableAt, disableAt sql.NullInt64
	var timezone sql.NullString
	var nextTransition sql.NullInt64
	var nextTarget sql.NullBool
	var createdAt, updatedAt int64
	err := row.Scan(
		&item.NodeID, &item.ScheduleType, &timezone, &enableSecond, &disableSecond, &enableAt, &disableAt, &item.Revision,
		&nextTransition, &nextTarget, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ActivationSchedule{}, ErrNotFound
	}
	if err != nil {
		return ActivationSchedule{}, fmt.Errorf("scan activation schedule: %w", err)
	}
	item.Timezone = timezone.String
	if enableSecond.Valid {
		item.EnableTime = formatScheduleClock(int(enableSecond.Int64))
	}
	if disableSecond.Valid {
		item.DisableTime = formatScheduleClock(int(disableSecond.Int64))
	}
	if enableAt.Valid {
		value := time.Unix(enableAt.Int64, 0).UTC()
		item.EnableAt = &value
	}
	if disableAt.Valid {
		value := time.Unix(disableAt.Int64, 0).UTC()
		item.DisableAt = &value
	}
	if nextTransition.Valid {
		item.NextTransitionAt = time.Unix(nextTransition.Int64, 0).UTC()
	}
	item.NextTargetEnabled = nextTarget.Valid && nextTarget.Bool
	item.CreatedAt = time.Unix(createdAt, 0).UTC()
	item.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return item, nil
}

func formatScheduleClock(second int) string {
	return fmt.Sprintf("%02d:%02d", second/3600, second%3600/60)
}
