package legacymigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

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
	var hasDistributorOrders bool
	if err := database.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type = 'table' AND name = 'v2_distributor_order')
	`).Scan(&hasDistributorOrders); err != nil {
		return nil, 0, fmt.Errorf("inspect legacy distributor order table: %w", err)
	}
	where := ""
	if hasDistributorOrders {
		where = ` WHERE NOT EXISTS (
			SELECT 1 FROM v2_distributor_order AS distributor_order
			WHERE distributor_order.subscriber_user_id = legacy_user.id
		)`
	}
	rows, err := database.QueryContext(ctx, `
		SELECT id, invite_user_id, telegram_id, email, password, password_algo, password_salt,
		       balance, discount, commission_type, commission_rate, commission_balance, t, u, d,
		       transfer_enable, banned, is_admin, last_login_at, is_staff, last_login_ip, uuid,
		       group_id, plan_id, speed_limit, remind_expire, remind_traffic, token, expired_at,
		       remarks, created_at, updated_at, device_limit, online_count, last_online_at, next_reset_at,
		       last_reset_at, reset_count, is_distributor, distributor_name
		FROM v2_user AS legacy_user
	`+where+` ORDER BY id`)
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
		var telegramID sql.NullInt64
		var passwordAlgorithm, passwordSalt, lastLoginIP, remarks, distributorName sql.NullString
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
		if err := validateUnsupportedLegacyHumanUserFields(user.ID, passwordAlgorithm, passwordSalt,
			legacyTime, lastLoginIP, onlineCount); err != nil {
			return nil, 0, err
		}
		if banned != 0 && banned != 1 || isAdmin != 0 && isAdmin != 1 || isStaff != 0 && isStaff != 1 ||
			isDistributor != 0 && isDistributor != 1 || remindExpire != 0 && remindExpire != 1 || remindTraffic != 0 && remindTraffic != 1 {
			return nil, 0, fmt.Errorf("legacy human user id %d has an invalid boolean value", user.ID)
		}
		user.Banned = banned == 1
		user.IsAdmin = isAdmin == 1
		user.IsStaff = isStaff == 1
		user.IsDistributor = isDistributor == 1
		if telegramID.Valid {
			if telegramID.Int64 < 1 {
				return nil, 0, fmt.Errorf("legacy human user id %d has an invalid telegram id", user.ID)
			}
			value := telegramID.Int64
			user.TelegramID = &value
		}
		if remindExpire == 0 {
			value := false
			user.RemindExpire = &value
		}
		if remindTraffic == 0 {
			value := false
			user.RemindTraffic = &value
		}
		if remarks.Valid && remarks.String != "" {
			value := remarks.String
			user.Remarks = &value
		}
		if user.IsDistributor {
			value := strings.TrimSpace(distributorName.String)
			if !distributorName.Valid || value == "" || value != distributorName.String || utf8.RuneCountInString(value) > 100 {
				return nil, 0, fmt.Errorf("legacy human user id %d has an invalid distributor name", user.ID)
			}
			user.DistributorName = &value
		} else if distributorName.Valid && strings.TrimSpace(distributorName.String) != "" {
			return nil, 0, fmt.Errorf("legacy human user id %d has a distributor name without the role", user.ID)
		}
		if balance < 0 || commissionBalance < 0 {
			return nil, 0, fmt.Errorf("legacy human user id %d has invalid finance balances", user.ID)
		}
		user.Balance = balance
		user.CommissionBalance = commissionBalance
		if discount.Valid {
			value, ok := legacyPercent(discount.Float64)
			if !ok {
				return nil, 0, fmt.Errorf("legacy human user id %d has an invalid discount", user.ID)
			}
			user.Discount = &value
		}
		if commissionType.Valid {
			if commissionType.Int64 < 0 || commissionType.Int64 > 2 {
				return nil, 0, fmt.Errorf("legacy human user id %d has an invalid commission type", user.ID)
			}
			user.CommissionType = int(commissionType.Int64)
		}
		if commissionRate.Valid {
			value, ok := legacyPercent(commissionRate.Float64)
			if !ok {
				return nil, 0, fmt.Errorf("legacy human user id %d has an invalid commission rate", user.ID)
			}
			user.CommissionRate = &value
		}
		user.InviteUserID = positiveNullableInt64(inviteUserID)
		user.LastLoginAt = positiveNullableInt64(lastLoginAt)
		user.GroupID = positiveNullableInt64(groupID)
		user.PlanID = positiveNullableInt64(planID)
		user.ExpiredAt = positiveNullableInt64(expiredAt)
		user.LastOnlineAt = positiveNullableInt64(lastOnlineAt)
		user.NextResetAt = positiveNullableInt64(nextResetAt)
		user.LastResetAt = positiveNullableInt64(lastResetAt)
		if resetCount.Valid {
			if resetCount.Int64 < 0 {
				return nil, 0, fmt.Errorf("legacy human user id %d has an invalid reset count", user.ID)
			}
			user.ResetCount = resetCount.Int64
		}
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

func validateUnsupportedLegacyHumanUserFields(id int64, passwordAlgorithm, passwordSalt sql.NullString,
	legacyTime int64, lastLoginIP sql.NullString, onlineCount sql.NullInt64,
) error {
	unsupported := passwordAlgorithm.String != "" || passwordSalt.String != "" || legacyTime != 0 ||
		lastLoginIP.String != "" || onlineCount.Valid && onlineCount.Int64 != 0
	if unsupported {
		return fmt.Errorf("legacy human user id %d contains unsupported account, finance, reminder, or audit state", id)
	}
	return nil
}

func legacyPercent(value float64) (int, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 || math.Trunc(value) != value {
		return 0, false
	}
	return int(value), true
}

func positiveNullableInt64(value sql.NullInt64) *int64 {
	if !value.Valid || value.Int64 == 0 {
		return nil
	}
	result := value.Int64
	return &result
}
