package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	defaultAdminUserPageSize = 50
	maxAdminUserPageSize     = 200
)

func (s *Store) ListAdminUsers(ctx context.Context, filter AdminUserFilter) (AdminUserPage, error) {
	if filter.Limit < 0 {
		return AdminUserPage{}, fmt.Errorf("%w: limit must not be negative", ErrInvalidInput)
	}
	limit := filter.Limit
	if limit == 0 {
		limit = defaultAdminUserPageSize
	}
	if limit > maxAdminUserPageSize {
		limit = maxAdminUserPageSize
	}
	cursor, err := decodeAdminUserCursor(filter.Cursor)
	if err != nil {
		return AdminUserPage{}, err
	}
	filter.EmailPrefix = strings.ToLower(strings.TrimSpace(filter.EmailPrefix))
	if len(filter.EmailPrefix) > 320 || (filter.GroupID != nil && *filter.GroupID < 1) {
		return AdminUserPage{}, fmt.Errorf("%w: invalid user filter", ErrInvalidInput)
	}

	query := `
		SELECT id, email, is_admin, banned, group_id, transfer_enable, traffic_u, traffic_d,
		       expired_at, speed_limit, device_limit, online_count, last_online_at,
		       admin_revision, created_at, updated_at
		FROM users
		WHERE account_kind = 'human'`
	args := make([]any, 0, 7)
	if filter.EmailPrefix != "" {
		if cursor.Mode != "" && cursor.Mode != "email" {
			return AdminUserPage{}, fmt.Errorf("%w: cursor does not match email ordering", ErrInvalidInput)
		}
		query += ` AND email LIKE ? ESCAPE '\'`
		args = append(args, escapeLike(filter.EmailPrefix)+"%")
		if cursor.Email != "" {
			query += ` AND email > ? COLLATE NOCASE`
			args = append(args, cursor.Email)
		}
	} else {
		if cursor.Mode == "email" {
			return AdminUserPage{}, fmt.Errorf("%w: cursor requires email ordering", ErrInvalidInput)
		}
		query += ` AND id < ?`
		args = append(args, cursor.ID)
	}
	if filter.Banned != nil {
		query += ` AND banned = ?`
		args = append(args, *filter.Banned)
	}
	if filter.GroupID != nil {
		query += ` AND group_id = ?`
		args = append(args, *filter.GroupID)
	}
	if filter.EmailPrefix != "" {
		query += ` ORDER BY email COLLATE NOCASE, id LIMIT ?`
	} else {
		query += ` ORDER BY id DESC LIMIT ?`
	}
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return AdminUserPage{}, fmt.Errorf("list admin users: %w", err)
	}
	defer rows.Close()
	items := make([]AdminUser, 0, min(limit+1, 64))
	for rows.Next() {
		user, err := scanAdminUser(rows)
		if err != nil {
			return AdminUserPage{}, err
		}
		items = append(items, user)
	}
	if err := rows.Err(); err != nil {
		return AdminUserPage{}, fmt.Errorf("iterate admin users: %w", err)
	}
	page := AdminUserPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[len(page.Items)-1]
		if filter.EmailPrefix != "" {
			page.NextCursor = encodeAdminUserCursor(adminUserCursor{Mode: "email", ID: last.ID, Email: last.Email})
		} else {
			page.NextCursor = encodeAdminUserCursor(adminUserCursor{Mode: "id", ID: last.ID})
		}
	}
	return page, nil
}

func (s *Store) GetAdminUser(ctx context.Context, userID int64) (AdminUser, error) {
	if userID < 1 {
		return AdminUser{}, fmt.Errorf("%w: user id must be positive", ErrInvalidInput)
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, email, is_admin, banned, group_id, transfer_enable, traffic_u, traffic_d,
		       expired_at, speed_limit, device_limit, online_count, last_online_at,
		       admin_revision, created_at, updated_at
		FROM users WHERE id = ? AND account_kind = 'human'
	`, userID)
	user, err := scanAdminUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminUser{}, ErrNotFound
	}
	return user, err
}

func (s *Store) CreateAdminUser(ctx context.Context, input CreateAdminUserInput, now time.Time) (AdminUser, error) {
	input.Email = normalizeEmail(input.Email)
	if input.Email == "" || len(input.Email) > 320 || input.PasswordHash == "" || input.TransferEnable < 0 || input.SpeedLimit < 0 || input.DeviceLimit < 0 || input.DeviceLimit > 1_000 || (input.GroupID != nil && *input.GroupID < 1) {
		return AdminUser{}, fmt.Errorf("%w: invalid user", ErrInvalidInput)
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminUser{}, fmt.Errorf("begin create user: %w", err)
	}
	defer tx.Rollback()
	if err := validateAdminUserGroup(ctx, tx, input.GroupID); err != nil {
		return AdminUser{}, err
	}
	var groupID, expiredAt any
	if input.GroupID != nil {
		groupID = *input.GroupID
	}
	if input.ExpiredAt != nil {
		expiredAt = input.ExpiredAt.Unix()
	}
	subscriptionToken, err := newSubscriptionToken()
	if err != nil {
		return AdminUser{}, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO users (
			email, password_hash, is_admin, banned, account_kind, uuid, group_id, transfer_enable,
			expired_at, speed_limit, device_limit, subscription_token, created_at, updated_at
		) VALUES (?, ?, 0, ?, 'human', ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.Email, input.PasswordHash, input.Banned, uuid.NewString(), groupID, input.TransferEnable,
		expiredAt, input.SpeedLimit, input.DeviceLimit, subscriptionToken, now.Unix(), now.Unix())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return AdminUser{}, ErrEmailInUse
		}
		return AdminUser{}, fmt.Errorf("create user: %w", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		return AdminUser{}, fmt.Errorf("read created user id: %w", err)
	}
	user, err := getAdminUserTx(ctx, tx, userID)
	if err != nil {
		return AdminUser{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminUser{}, fmt.Errorf("commit create user: %w", err)
	}
	return user, nil
}

func (s *Store) UpdateAdminUser(ctx context.Context, userID int64, input UpdateAdminUserInput, now time.Time) (AdminUser, AdminUserMutation, error) {
	input.Email = normalizeEmail(input.Email)
	if userID < 1 || input.Revision < 1 || input.Email == "" || len(input.Email) > 320 || input.TransferEnable < 0 || input.SpeedLimit < 0 || input.DeviceLimit < 0 || input.DeviceLimit > 1_000 || (input.GroupID != nil && *input.GroupID < 1) {
		return AdminUser{}, AdminUserMutation{}, fmt.Errorf("%w: invalid user update", ErrInvalidInput)
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminUser{}, AdminUserMutation{}, fmt.Errorf("begin update user: %w", err)
	}
	defer tx.Rollback()
	existing, err := getAdminUserTx(ctx, tx, userID)
	if err != nil {
		return AdminUser{}, AdminUserMutation{}, err
	}
	if existing.Revision != input.Revision {
		return AdminUser{}, AdminUserMutation{}, ErrConflict
	}
	if err := validateAdminUserGroup(ctx, tx, input.GroupID); err != nil {
		return AdminUser{}, AdminUserMutation{}, err
	}
	var groupID, expiredAt any
	if input.GroupID != nil {
		groupID = *input.GroupID
	}
	if input.ExpiredAt != nil {
		expiredAt = input.ExpiredAt.Unix()
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE users SET email = ?, group_id = ?, transfer_enable = ?, expired_at = ?, speed_limit = ?,
			device_limit = ?, banned = ?, admin_revision = admin_revision + 1, updated_at = ?
		WHERE id = ? AND account_kind = 'human' AND admin_revision = ?
	`, input.Email, groupID, input.TransferEnable, expiredAt, input.SpeedLimit, input.DeviceLimit,
		input.Banned, now.Unix(), userID, input.Revision)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return AdminUser{}, AdminUserMutation{}, ErrEmailInUse
		}
		return AdminUser{}, AdminUserMutation{}, fmt.Errorf("update user: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return AdminUser{}, AdminUserMutation{}, fmt.Errorf("count updated users: %w", err)
	}
	if changed != 1 {
		return AdminUser{}, AdminUserMutation{}, ErrConflict
	}
	credentialsChanged := existing.Email != input.Email || (!existing.Banned && input.Banned)
	if credentialsChanged {
		if err := revokeAllCredentialsTx(ctx, tx, userID, now); err != nil {
			return AdminUser{}, AdminUserMutation{}, fmt.Errorf("revoke user sessions: %w", err)
		}
	}
	groupChanged := !sameNullableInt64(existing.GroupID, input.GroupID)
	oldRuntimeEligible := adminUserRuntimeEligible(existing.GroupID, existing.Banned, existing.TransferEnable, existing.TrafficUpload+existing.TrafficDownload, existing.ExpiredAt, now)
	newRuntimeEligible := adminUserRuntimeEligible(input.GroupID, input.Banned, input.TransferEnable, existing.TrafficUpload+existing.TrafficDownload, input.ExpiredAt, now)
	accessStateCleared := groupChanged || (oldRuntimeEligible && !newRuntimeEligible)
	if accessStateCleared {
		if _, err := tx.ExecContext(ctx, `DELETE FROM node_device_ips WHERE user_id = ?`, userID); err != nil {
			return AdminUser{}, AdminUserMutation{}, fmt.Errorf("clear user devices: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM node_user_online WHERE user_id = ?`, userID); err != nil {
			return AdminUser{}, AdminUserMutation{}, fmt.Errorf("clear user online state: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET online_count = 0 WHERE id = ?`, userID); err != nil {
			return AdminUser{}, AdminUserMutation{}, fmt.Errorf("reset user online count: %w", err)
		}
	}
	updated, err := getAdminUserTx(ctx, tx, userID)
	if err != nil {
		return AdminUser{}, AdminUserMutation{}, err
	}
	var runtimeUUID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT uuid FROM users WHERE id = ?`, userID).Scan(&runtimeUUID); err != nil {
		return AdminUser{}, AdminUserMutation{}, fmt.Errorf("read user runtime identity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AdminUser{}, AdminUserMutation{}, fmt.Errorf("commit update user: %w", err)
	}
	runtimeChanged := groupChanged || oldRuntimeEligible != newRuntimeEligible || existing.SpeedLimit != input.SpeedLimit ||
		existing.DeviceLimit != input.DeviceLimit || existing.Banned != input.Banned
	return updated, AdminUserMutation{OldGroupID: cloneInt64(existing.GroupID), NewGroupID: cloneInt64(input.GroupID), UUID: runtimeUUID.String, RuntimeChanged: runtimeChanged, AccessStateCleared: accessStateCleared}, nil
}

func (s *Store) ResetAdminUserPassword(ctx context.Context, userID, revision int64, passwordHash string, now time.Time) (AdminUser, error) {
	if userID < 1 || revision < 1 || passwordHash == "" {
		return AdminUser{}, fmt.Errorf("%w: invalid password reset", ErrInvalidInput)
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminUser{}, fmt.Errorf("begin password reset: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE users SET password_hash = ?, admin_revision = admin_revision + 1, updated_at = ?
		WHERE id = ? AND account_kind = 'human' AND admin_revision = ?
	`, passwordHash, now.Unix(), userID, revision)
	if err != nil {
		return AdminUser{}, fmt.Errorf("reset user password: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return AdminUser{}, fmt.Errorf("count reset passwords: %w", err)
	}
	if changed == 0 {
		if _, findErr := getAdminUserTx(ctx, tx, userID); errors.Is(findErr, ErrNotFound) {
			return AdminUser{}, ErrNotFound
		}
		return AdminUser{}, ErrConflict
	}
	if err := revokeAllCredentialsTx(ctx, tx, userID, now); err != nil {
		return AdminUser{}, fmt.Errorf("revoke sessions after password reset: %w", err)
	}
	updated, err := getAdminUserTx(ctx, tx, userID)
	if err != nil {
		return AdminUser{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminUser{}, fmt.Errorf("commit password reset: %w", err)
	}
	return updated, nil
}

func getAdminUserTx(ctx context.Context, tx *sql.Tx, userID int64) (AdminUser, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, email, is_admin, banned, group_id, transfer_enable, traffic_u, traffic_d,
		       expired_at, speed_limit, device_limit, online_count, last_online_at,
		       admin_revision, created_at, updated_at
		FROM users WHERE id = ? AND account_kind = 'human'
	`, userID)
	user, err := scanAdminUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminUser{}, ErrNotFound
	}
	return user, err
}

func scanAdminUser(row rowScanner) (AdminUser, error) {
	var user AdminUser
	var groupID, expiredAt, lastOnlineAt sql.NullInt64
	var createdAt, updatedAt int64
	if err := row.Scan(&user.ID, &user.Email, &user.IsAdmin, &user.Banned, &groupID, &user.TransferEnable,
		&user.TrafficUpload, &user.TrafficDownload, &expiredAt, &user.SpeedLimit, &user.DeviceLimit,
		&user.OnlineCount, &lastOnlineAt, &user.Revision, &createdAt, &updatedAt); err != nil {
		return AdminUser{}, err
	}
	if groupID.Valid {
		user.GroupID = &groupID.Int64
	}
	if expiredAt.Valid {
		value := time.Unix(expiredAt.Int64, 0).UTC()
		user.ExpiredAt = &value
	}
	if lastOnlineAt.Valid {
		value := time.Unix(lastOnlineAt.Int64, 0).UTC()
		user.LastOnlineAt = &value
	}
	user.CreatedAt = time.Unix(createdAt, 0).UTC()
	user.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return user, nil
}

func validateAdminUserGroup(ctx context.Context, tx *sql.Tx, groupID *int64) error {
	if groupID == nil {
		return nil
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM server_groups WHERE id = ?)`, *groupID).Scan(&exists); err != nil {
		return fmt.Errorf("validate user group: %w", err)
	}
	if !exists {
		return fmt.Errorf("%w: user group does not exist", ErrInvalidInput)
	}
	return nil
}

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

type adminUserCursor struct {
	Version int    `json:"v"`
	Mode    string `json:"m"`
	ID      int64  `json:"i"`
	Email   string `json:"e,omitempty"`
}

func encodeAdminUserCursor(cursor adminUserCursor) string {
	cursor.Version = 1
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeAdminUserCursor(cursor string) (adminUserCursor, error) {
	if cursor == "" {
		return adminUserCursor{ID: int64(^uint64(0) >> 1)}, nil
	}
	if len(cursor) > 1024 {
		return adminUserCursor{}, fmt.Errorf("%w: invalid user cursor", ErrInvalidInput)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return adminUserCursor{}, fmt.Errorf("%w: invalid user cursor", ErrInvalidInput)
	}
	var parsed adminUserCursor
	if err := json.Unmarshal(decoded, &parsed); err != nil || parsed.Version != 1 || parsed.ID < 1 ||
		(parsed.Mode != "id" && parsed.Mode != "email") || (parsed.Mode == "email" && (parsed.Email == "" || len(parsed.Email) > 320)) ||
		(parsed.Mode == "id" && parsed.Email != "") {
		return adminUserCursor{}, fmt.Errorf("%w: invalid user cursor", ErrInvalidInput)
	}
	return parsed, nil
}

func sameNullableInt64(left, right *int64) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func adminUserRuntimeEligible(groupID *int64, banned bool, transferEnable, used int64, expiredAt *time.Time, now time.Time) bool {
	return groupID != nil && !banned && used < transferEnable && (expiredAt == nil || !expiredAt.Before(now))
}
