package legacymigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	maxLegacyHumanUsers     = 250_000
	maxLegacyHumanUserBytes = int64(256 << 20)
)

var legacyHumanUserColumns = []string{
	"id", "invite_user_id", "telegram_id", "email", "password", "password_algo", "password_salt",
	"balance", "discount", "commission_type", "commission_rate", "commission_balance", "t", "u", "d",
	"transfer_enable", "banned", "is_admin", "last_login_at", "is_staff", "last_login_ip", "uuid",
	"group_id", "plan_id", "speed_limit", "remind_expire", "remind_traffic", "token", "expired_at",
	"remarks", "created_at", "updated_at", "device_limit", "online_count", "last_online_at", "next_reset_at",
	"last_reset_at", "reset_count", "is_distributor", "distributor_name",
}

type HumanUsersSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Users    []store.LegacyHumanUser
	Checksum string
}

func ReadHumanUsersSnapshot(ctx context.Context, sourcePath string) (HumanUsersSnapshot, error) {
	users := []store.LegacyHumanUser{}
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_user", legacyHumanUserColumns); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(
				length(CAST(email AS BLOB)) + length(CAST(password AS BLOB)) +
				length(CAST(uuid AS BLOB)) + length(CAST(token AS BLOB)) +
				COALESCE(length(CAST(password_algo AS BLOB)), 0) + COALESCE(length(CAST(password_salt AS BLOB)), 0) +
				COALESCE(length(CAST(last_login_ip AS BLOB)), 0) + COALESCE(length(CAST(remarks AS BLOB)), 0) +
				COALESCE(length(CAST(distributor_name AS BLOB)), 0)
			), 0) FROM v2_user
		`, maxLegacyHumanUsers, maxLegacyHumanUserBytes, "legacy human users"); err != nil {
			return err
		}
		var readBytes int64
		var readErr error
		users, readBytes, readErr = readLegacyHumanUsers(ctx, database)
		if readErr != nil {
			return readErr
		}
		if readBytes > maxLegacyHumanUserBytes {
			return errors.New("legacy human users exceed the migration data limit")
		}
		if err := store.ValidateLegacyHumanUsersData(users); err != nil {
			return fmt.Errorf("validate legacy human users: %w", err)
		}
		return nil
	})
	if err != nil {
		return HumanUsersSnapshot{}, err
	}
	return HumanUsersSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Users: users, Checksum: store.LegacyHumanUsersChecksum(users),
	}, nil
}

func readLegacyHumanUsers(ctx context.Context, database *sql.DB) ([]store.LegacyHumanUser, int64, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, invite_user_id, telegram_id, email, password, password_algo, password_salt,
		       balance, discount, commission_type, commission_rate, commission_balance, t, u, d,
		       transfer_enable, banned, is_admin, last_login_at, is_staff, last_login_ip, uuid,
		       group_id, plan_id, speed_limit, remind_expire, remind_traffic, token, expired_at,
		       remarks, created_at, updated_at, device_limit, online_count, last_online_at, next_reset_at,
		       last_reset_at, reset_count, is_distributor, distributor_name
		FROM v2_user ORDER BY id
	`)
	if err != nil {
		return nil, 0, fmt.Errorf("read legacy human users: %w", err)
	}
	defer rows.Close()
	users := make([]store.LegacyHumanUser, 0)
	var bytesRead int64
	maxInt := int64(^uint(0) >> 1)
	for rows.Next() {
		if len(users) >= maxLegacyHumanUsers {
			return nil, 0, fmt.Errorf("legacy human users exceed the %d-row migration limit", maxLegacyHumanUsers)
		}
		var user store.LegacyHumanUser
		var inviteUserID, lastLoginAt, groupID, planID, speedLimit, expiredAt, deviceLimit, onlineCount sql.NullInt64
		var lastOnlineAt, nextResetAt, lastResetAt, resetCount sql.NullInt64
		var telegramID, passwordAlgorithm, passwordSalt, lastLoginIP, remarks, distributorName sql.NullString
		var discount, commissionRate sql.NullFloat64
		var balance, commissionBalance, legacyTime int64
		var commissionType sql.NullInt64
		var banned, isAdmin, isStaff, remindExpire, remindTraffic, isDistributor int64
		if err := rows.Scan(&user.ID, &inviteUserID, &telegramID, &user.Email, &user.PasswordHash, &passwordAlgorithm,
			&passwordSalt, &balance, &discount, &commissionType, &commissionRate, &commissionBalance, &legacyTime,
			&user.TrafficUpload, &user.TrafficDownload, &user.TransferEnable, &banned, &isAdmin, &lastLoginAt,
			&isStaff, &lastLoginIP, &user.UUID, &groupID, &planID, &speedLimit, &remindExpire, &remindTraffic,
			&user.SubscriptionToken, &expiredAt, &remarks, &user.CreatedAt, &user.UpdatedAt, &deviceLimit,
			&onlineCount, &lastOnlineAt, &nextResetAt, &lastResetAt, &resetCount, &isDistributor, &distributorName); err != nil {
			return nil, 0, fmt.Errorf("scan legacy human user: %w", err)
		}
		if err := validateUnsupportedLegacyHumanUserFields(user.ID, telegramID, passwordAlgorithm, passwordSalt,
			balance, discount, commissionType, commissionRate, commissionBalance, legacyTime, isStaff, lastLoginIP,
			planID, remindExpire, remindTraffic, remarks, onlineCount, nextResetAt, lastResetAt, resetCount,
			isDistributor, distributorName); err != nil {
			return nil, 0, err
		}
		if banned != 0 && banned != 1 || isAdmin != 0 && isAdmin != 1 {
			return nil, 0, fmt.Errorf("legacy human user id %d has an invalid boolean value", user.ID)
		}
		user.Banned = banned == 1
		user.IsAdmin = isAdmin == 1
		user.InviteUserID = positiveNullableInt64(inviteUserID)
		user.LastLoginAt = positiveNullableInt64(lastLoginAt)
		user.GroupID = positiveNullableInt64(groupID)
		user.ExpiredAt = positiveNullableInt64(expiredAt)
		user.LastOnlineAt = positiveNullableInt64(lastOnlineAt)
		if speedLimit.Valid {
			if speedLimit.Int64 < 0 || speedLimit.Int64 > maxInt {
				return nil, 0, fmt.Errorf("legacy human user id %d has an invalid speed limit", user.ID)
			}
			user.SpeedLimit = int(speedLimit.Int64)
		}
		if deviceLimit.Valid {
			if deviceLimit.Int64 < 0 || deviceLimit.Int64 > maxInt {
				return nil, 0, fmt.Errorf("legacy human user id %d has an invalid device limit", user.ID)
			}
			user.DeviceLimit = int(deviceLimit.Int64)
		}
		bytesRead += int64(len(user.Email) + len(user.PasswordHash) + len(user.UUID) + len(user.SubscriptionToken) +
			len(passwordAlgorithm.String) + len(passwordSalt.String) + len(lastLoginIP.String) + len(remarks.String) + len(distributorName.String))
		if bytesRead > maxLegacyHumanUserBytes {
			return nil, 0, errors.New("legacy human users exceed the migration data limit")
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate legacy human users: %w", err)
	}
	return users, bytesRead, nil
}

func validateUnsupportedLegacyHumanUserFields(id int64, telegramID, passwordAlgorithm, passwordSalt sql.NullString,
	balance int64, discount sql.NullFloat64, commissionType sql.NullInt64, commissionRate sql.NullFloat64,
	commissionBalance, legacyTime, isStaff int64, lastLoginIP sql.NullString, planID sql.NullInt64,
	remindExpire, remindTraffic int64, remarks sql.NullString, onlineCount, nextResetAt, lastResetAt, resetCount sql.NullInt64,
	isDistributor int64, distributorName sql.NullString,
) error {
	unsupported := telegramID.String != "" || passwordAlgorithm.String != "" || passwordSalt.String != "" ||
		balance != 0 || discount.Valid && discount.Float64 != 0 || commissionType.Valid && commissionType.Int64 != 0 ||
		commissionRate.Valid && commissionRate.Float64 != 0 || commissionBalance != 0 || legacyTime != 0 || isStaff != 0 ||
		lastLoginIP.String != "" || planID.Valid && planID.Int64 != 0 || remindExpire != 1 || remindTraffic != 1 ||
		strings.TrimSpace(remarks.String) != "" || onlineCount.Valid && onlineCount.Int64 != 0 ||
		nextResetAt.Valid && nextResetAt.Int64 != 0 || lastResetAt.Valid && lastResetAt.Int64 != 0 ||
		resetCount.Valid && resetCount.Int64 != 0 || isDistributor != 0 || strings.TrimSpace(distributorName.String) != ""
	if unsupported {
		return fmt.Errorf("legacy human user id %d contains unsupported account, finance, reminder, reset, or audit state", id)
	}
	return nil
}

func positiveNullableInt64(value sql.NullInt64) *int64 {
	if !value.Valid || value.Int64 == 0 {
		return nil
	}
	result := value.Int64
	return &result
}
