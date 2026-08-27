package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	defaultAdminUserPageSize = 50
	maxAdminUserPageSize     = 200
	maxAdminUserPage         = 1_000_000
)

func (s *Store) ListAdminUsers(ctx context.Context, filter AdminUserFilter) (AdminUserPage, error) {
	if filter.Page != 0 || filter.PageSize != 0 || filter.SortBy != "" || len(filter.Sorts) > 0 || len(filter.Rules) > 0 {
		return s.listAdminUsersPage(ctx, filter)
	}
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

	query := adminUserSelect + adminUserFrom + ` WHERE u.account_kind = 'human'`
	args := make([]any, 0, 7)
	if filter.EmailPrefix != "" {
		if cursor.Mode != "" && cursor.Mode != "email" {
			return AdminUserPage{}, fmt.Errorf("%w: cursor does not match email ordering", ErrInvalidInput)
		}
		query += ` AND u.email LIKE ? ESCAPE '\'`
		args = append(args, escapeLike(filter.EmailPrefix)+"%")
		if cursor.Email != "" {
			query += ` AND u.email > ? COLLATE NOCASE`
			args = append(args, cursor.Email)
		}
	} else {
		if cursor.Mode == "email" {
			return AdminUserPage{}, fmt.Errorf("%w: cursor requires email ordering", ErrInvalidInput)
		}
		query += ` AND u.id < ?`
		args = append(args, cursor.ID)
	}
	if filter.Banned != nil {
		query += ` AND u.banned = ?`
		args = append(args, *filter.Banned)
	}
	if filter.GroupID != nil {
		query += ` AND u.group_id = ?`
		args = append(args, *filter.GroupID)
	}
	if filter.EmailPrefix != "" {
		query += ` ORDER BY u.email COLLATE NOCASE, u.id LIMIT ?`
	} else {
		query += ` ORDER BY u.id DESC LIMIT ?`
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

const adminUserSelect = `
	SELECT u.id, u.email, u.is_admin, u.is_staff, u.is_distributor, u.distributor_name, u.banned,
	       u.group_id, g.name, u.plan_id, p.name, u.invite_user_id, inviter.email,
	       u.transfer_enable, u.traffic_u, u.traffic_d, (u.traffic_u + u.traffic_d),
	       u.expired_at, u.speed_limit, u.device_limit, u.online_count, u.last_online_at, u.last_login_at,
	       u.balance, u.commission_type, u.commission_rate, u.commission_balance, u.discount,
	       u.next_reset_at, u.last_reset_at, u.reset_count, u.telegram_id, u.remind_expire, u.remind_traffic, u.remarks,
	       u.admin_revision, u.created_at, u.updated_at`

const adminUserFrom = `
	FROM users u
	LEFT JOIN server_groups g ON g.id = u.group_id
	LEFT JOIN plans p ON p.id = u.plan_id
	LEFT JOIN users inviter ON inviter.id = u.invite_user_id AND inviter.account_kind = 'human'`

func (s *Store) listAdminUsersPage(ctx context.Context, filter AdminUserFilter) (AdminUserPage, error) {
	if filter.Cursor != "" || filter.Limit != 0 {
		return AdminUserPage{}, fmt.Errorf("%w: cursor and page pagination cannot be combined", ErrInvalidInput)
	}
	page := filter.Page
	if page == 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize == 0 {
		pageSize = defaultAdminUserPageSize
	}
	if page < 1 || page > maxAdminUserPage || pageSize < 1 || pageSize > maxAdminUserPageSize || len(filter.Sorts) > 3 ||
		int64(page-1) > math.MaxInt64/int64(pageSize) {
		return AdminUserPage{}, fmt.Errorf("%w: invalid user page", ErrInvalidInput)
	}
	where, args, err := buildAdminUserWhere(filter)
	if err != nil {
		return AdminUserPage{}, err
	}
	order, err := buildAdminUserOrder(filter)
	if err != nil {
		return AdminUserPage{}, err
	}
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) `+adminUserCountFrom(filter)+where, args...).Scan(&total); err != nil {
		return AdminUserPage{}, fmt.Errorf("count admin users: %w", err)
	}
	offset := int64(page-1) * int64(pageSize)
	listArgs := append(append([]any(nil), args...), pageSize, offset)
	rows, err := s.db.QueryContext(ctx, adminUserSelect+adminUserFrom+where+order+` LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return AdminUserPage{}, fmt.Errorf("list paged admin users: %w", err)
	}
	defer rows.Close()
	items := make([]AdminUser, 0, min(pageSize, int(total)))
	for rows.Next() {
		user, scanErr := scanAdminUser(rows)
		if scanErr != nil {
			return AdminUserPage{}, scanErr
		}
		items = append(items, user)
	}
	if err := rows.Err(); err != nil {
		return AdminUserPage{}, fmt.Errorf("iterate paged admin users: %w", err)
	}
	return AdminUserPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func adminUserCountFrom(filter AdminUserFilter) string {
	for _, rule := range filter.Rules {
		if rule.Field == AdminUserFieldInviteUserEmail {
			return `FROM users u LEFT JOIN users inviter ON inviter.id = u.invite_user_id AND inviter.account_kind = 'human'`
		}
	}
	return `FROM users u`
}

func buildAdminUserWhere(filter AdminUserFilter) (string, []any, error) {
	filter.EmailPrefix = strings.TrimSpace(filter.EmailPrefix)
	if len(filter.EmailPrefix) > 320 || filter.GroupID != nil && *filter.GroupID < 1 || len(filter.Rules) > 10 {
		return "", nil, fmt.Errorf("%w: invalid user filter", ErrInvalidInput)
	}
	clauses := []string{"u.account_kind = 'human'"}
	args := make([]any, 0, 16)
	if filter.EmailPrefix != "" {
		clauses = append(clauses, `u.email LIKE ? ESCAPE '\' COLLATE NOCASE`)
		args = append(args, escapeLike(filter.EmailPrefix)+"%")
	}
	if filter.Banned != nil {
		clauses = append(clauses, `u.banned = ?`)
		args = append(args, *filter.Banned)
	}
	if filter.GroupID != nil {
		clauses = append(clauses, `u.group_id = ?`)
		args = append(args, *filter.GroupID)
	}
	for _, rule := range filter.Rules {
		condition, conditionArgs, err := buildAdminUserRule(rule)
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, "("+condition+")")
		args = append(args, conditionArgs...)
	}
	return ` WHERE ` + strings.Join(clauses, " AND "), args, nil
}

type adminUserFilterKind int

const (
	adminUserFilterNumber adminUserFilterKind = iota
	adminUserFilterString
	adminUserFilterBoolean
)

type adminUserFilterDefinition struct {
	expression string
	kind       adminUserFilterKind
	nullable   bool
	exactOnly  bool
}

var adminUserFilterDefinitions = map[string]adminUserFilterDefinition{
	AdminUserFieldID:                {expression: "u.id", kind: adminUserFilterNumber},
	AdminUserFieldEmail:             {expression: "u.email", kind: adminUserFilterString},
	AdminUserFieldPlanID:            {expression: "u.plan_id", kind: adminUserFilterNumber, nullable: true},
	AdminUserFieldGroupID:           {expression: "u.group_id", kind: adminUserFilterNumber, nullable: true},
	AdminUserFieldTransferEnable:    {expression: "u.transfer_enable", kind: adminUserFilterNumber},
	AdminUserFieldTrafficUsed:       {expression: "(u.traffic_u + u.traffic_d)", kind: adminUserFilterNumber},
	AdminUserFieldOnlineCount:       {expression: "u.online_count", kind: adminUserFilterNumber},
	AdminUserFieldExpiredAt:         {expression: "u.expired_at", kind: adminUserFilterNumber, nullable: true},
	AdminUserFieldUUID:              {expression: "u.uuid", kind: adminUserFilterString, nullable: true, exactOnly: true},
	AdminUserFieldSubscriptionToken: {expression: "u.subscription_token", kind: adminUserFilterString, exactOnly: true},
	AdminUserFieldBanned:            {expression: "u.banned", kind: adminUserFilterBoolean},
	AdminUserFieldRemarks:           {expression: "u.remarks", kind: adminUserFilterString, nullable: true},
	AdminUserFieldInviteUserID:      {expression: "u.invite_user_id", kind: adminUserFilterNumber, nullable: true},
	AdminUserFieldInviteUserEmail:   {expression: "inviter.email", kind: adminUserFilterString, nullable: true},
	AdminUserFieldIsAdmin:           {expression: "u.is_admin", kind: adminUserFilterBoolean},
	AdminUserFieldIsStaff:           {expression: "u.is_staff", kind: adminUserFilterBoolean},
	AdminUserFieldIsDistributor:     {expression: "u.is_distributor", kind: adminUserFilterBoolean},
	AdminUserFieldBalance:           {expression: "u.balance", kind: adminUserFilterNumber},
	AdminUserFieldCommissionBalance: {expression: "u.commission_balance", kind: adminUserFilterNumber},
	AdminUserFieldCreatedAt:         {expression: "u.created_at", kind: adminUserFilterNumber},
}

func buildAdminUserRule(rule AdminUserFilterRule) (string, []any, error) {
	definition, exists := adminUserFilterDefinitions[rule.Field]
	if !exists {
		return "", nil, fmt.Errorf("%w: unsupported user filter field", ErrInvalidInput)
	}
	if rule.Operator == AdminUserOperatorIsNull || rule.Operator == AdminUserOperatorNotNull {
		if !definition.nullable || len(rule.Values) != 0 {
			return "", nil, fmt.Errorf("%w: invalid null user filter", ErrInvalidInput)
		}
		if rule.Operator == AdminUserOperatorIsNull {
			return definition.expression + ` IS NULL`, nil, nil
		}
		return definition.expression + ` IS NOT NULL`, nil, nil
	}
	if len(rule.Values) < 1 || len(rule.Values) > 20 {
		return "", nil, fmt.Errorf("%w: invalid user filter values", ErrInvalidInput)
	}
	if definition.exactOnly && rule.Operator != AdminUserOperatorEqual && rule.Operator != AdminUserOperatorIn {
		return "", nil, fmt.Errorf("%w: secret filters require exact matching", ErrInvalidInput)
	}
	values := make([]any, 0, len(rule.Values))
	for _, raw := range rule.Values {
		if len(raw) > 512 || !utf8.ValidString(raw) {
			return "", nil, fmt.Errorf("%w: invalid user filter value", ErrInvalidInput)
		}
		switch definition.kind {
		case adminUserFilterNumber:
			value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
			if err != nil || value < 0 {
				return "", nil, fmt.Errorf("%w: invalid numeric user filter", ErrInvalidInput)
			}
			values = append(values, value)
		case adminUserFilterBoolean:
			value, err := strconv.ParseBool(strings.TrimSpace(raw))
			if err != nil {
				if raw == "0" {
					value = false
				} else if raw == "1" {
					value = true
				} else {
					return "", nil, fmt.Errorf("%w: invalid boolean user filter", ErrInvalidInput)
				}
			}
			values = append(values, value)
		default:
			value := strings.TrimSpace(raw)
			if value == "" {
				return "", nil, fmt.Errorf("%w: empty user filter value", ErrInvalidInput)
			}
			values = append(values, value)
		}
	}
	if rule.Operator == AdminUserOperatorIn {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
		return definition.expression + ` IN (` + placeholders + `)`, values, nil
	}
	if len(values) != 1 {
		return "", nil, fmt.Errorf("%w: user filter operator requires one value", ErrInvalidInput)
	}
	operator := map[string]string{
		AdminUserOperatorEqual: "=", AdminUserOperatorNotEqual: "!=", AdminUserOperatorGreater: ">",
		AdminUserOperatorGreaterOrEqual: ">=", AdminUserOperatorLess: "<", AdminUserOperatorLessOrEqual: "<=",
	}[rule.Operator]
	if rule.Operator == AdminUserOperatorContains {
		if definition.kind != adminUserFilterString {
			return "", nil, fmt.Errorf("%w: contains requires a string field", ErrInvalidInput)
		}
		return definition.expression + ` LIKE ? ESCAPE '\' COLLATE NOCASE`, []any{"%" + escapeLike(values[0].(string)) + "%"}, nil
	}
	if operator == "" || definition.kind == adminUserFilterBoolean && rule.Operator != AdminUserOperatorEqual && rule.Operator != AdminUserOperatorNotEqual {
		return "", nil, fmt.Errorf("%w: unsupported user filter operator", ErrInvalidInput)
	}
	return definition.expression + " " + operator + " ?", values, nil
}

func adminUserSortExpression(sortBy AdminUserSort) (string, error) {
	if sortBy == "" {
		return "u.id", nil
	}
	expressions := map[AdminUserSort]string{
		AdminUserSortID: "u.id", AdminUserSortOnlineCount: "u.online_count", AdminUserSortBanned: "u.banned",
		AdminUserSortTrafficUsed: "(u.traffic_u + u.traffic_d)", AdminUserSortTransferEnable: "u.transfer_enable",
		AdminUserSortExpiredAt: "u.expired_at", AdminUserSortBalance: "u.balance",
		AdminUserSortCommissionBalance: "u.commission_balance", AdminUserSortCreatedAt: "u.created_at",
	}
	expression, exists := expressions[sortBy]
	if !exists {
		return "", fmt.Errorf("%w: unsupported user sort", ErrInvalidInput)
	}
	return expression, nil
}

func buildAdminUserOrder(filter AdminUserFilter) (string, error) {
	sorts := append([]AdminUserSortRule(nil), filter.Sorts...)
	if len(sorts) == 0 {
		descending := filter.SortDescending || filter.SortBy == ""
		sorts = append(sorts, AdminUserSortRule{Field: filter.SortBy, Descending: descending})
	}
	parts := make([]string, 0, len(sorts)+1)
	seen := make(map[AdminUserSort]struct{}, len(sorts))
	hasID := false
	for _, sortRule := range sorts {
		if _, exists := seen[sortRule.Field]; exists {
			return "", fmt.Errorf("%w: duplicate user sort", ErrInvalidInput)
		}
		seen[sortRule.Field] = struct{}{}
		expression, err := adminUserSortExpression(sortRule.Field)
		if err != nil {
			return "", err
		}
		direction := "ASC"
		if sortRule.Descending {
			direction = "DESC"
		}
		parts = append(parts, expression+" "+direction)
		hasID = hasID || expression == "u.id"
	}
	if !hasID {
		parts = append(parts, "u.id DESC")
	}
	return ` ORDER BY ` + strings.Join(parts, ", "), nil
}

func (s *Store) GetAdminUser(ctx context.Context, userID int64) (AdminUser, error) {
	if userID < 1 {
		return AdminUser{}, fmt.Errorf("%w: user id must be positive", ErrInvalidInput)
	}
	row := s.db.QueryRowContext(ctx, adminUserSelect+adminUserFrom+` WHERE u.id = ? AND u.account_kind = 'human'`, userID)
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
	distributorName, err := normalizedDistributorName(input.IsDistributor, &input.DistributorName)
	if err != nil {
		return AdminUser{}, err
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
			email, password_hash, is_admin, is_staff, is_distributor, distributor_name, banned, account_kind, uuid, group_id, transfer_enable,
			expired_at, speed_limit, device_limit, subscription_token, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 'human', ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.Email, input.PasswordHash, input.IsAdmin, input.IsStaff, input.IsDistributor, nullableStringValue(distributorName), input.Banned, uuid.NewString(), groupID, input.TransferEnable,
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
	isAdmin := existing.IsAdmin
	if input.IsAdmin != nil {
		isAdmin = *input.IsAdmin
	}
	isStaff := existing.IsStaff
	if input.IsStaff != nil {
		isStaff = *input.IsStaff
	}
	isDistributor := existing.IsDistributor
	if input.IsDistributor != nil {
		isDistributor = *input.IsDistributor
	}
	distributorNameInput := input.DistributorName
	if distributorNameInput == nil {
		distributorNameInput = existing.DistributorName
	}
	distributorName, err := normalizedDistributorName(isDistributor, distributorNameInput)
	if err != nil {
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
			device_limit = ?, banned = ?, is_admin = ?, is_staff = ?, is_distributor = ?, distributor_name = ?,
			admin_revision = admin_revision + 1, updated_at = ?
		WHERE id = ? AND account_kind = 'human' AND admin_revision = ?
	`, input.Email, groupID, input.TransferEnable, expiredAt, input.SpeedLimit, input.DeviceLimit,
		input.Banned, isAdmin, isStaff, isDistributor, nullableStringValue(distributorName), now.Unix(), userID, input.Revision)
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
	credentialsChanged := existing.Email != input.Email || (!existing.Banned && input.Banned) ||
		existing.IsAdmin != isAdmin || existing.IsStaff != isStaff || existing.IsDistributor != isDistributor
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
	row := tx.QueryRowContext(ctx, adminUserSelect+adminUserFrom+` WHERE u.id = ? AND u.account_kind = 'human'`, userID)
	user, err := scanAdminUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminUser{}, ErrNotFound
	}
	return user, err
}

func scanAdminUser(row rowScanner) (AdminUser, error) {
	var user AdminUser
	var distributorName, groupName, planName, inviteUserEmail, remarks sql.NullString
	var groupID, planID, inviteUserID, expiredAt, lastOnlineAt, lastLoginAt sql.NullInt64
	var commissionRate, discount, nextResetAt, lastResetAt, telegramID sql.NullInt64
	var createdAt, updatedAt int64
	if err := row.Scan(
		&user.ID, &user.Email, &user.IsAdmin, &user.IsStaff, &user.IsDistributor, &distributorName, &user.Banned,
		&groupID, &groupName, &planID, &planName, &inviteUserID, &inviteUserEmail,
		&user.TransferEnable, &user.TrafficUpload, &user.TrafficDownload, &user.TrafficUsed,
		&expiredAt, &user.SpeedLimit, &user.DeviceLimit, &user.OnlineCount, &lastOnlineAt, &lastLoginAt,
		&user.Balance, &user.CommissionType, &commissionRate, &user.CommissionBalance, &discount,
		&nextResetAt, &lastResetAt, &user.ResetCount, &telegramID, &user.RemindExpire, &user.RemindTraffic, &remarks,
		&user.Revision, &createdAt, &updatedAt,
	); err != nil {
		return AdminUser{}, err
	}
	if distributorName.Valid {
		user.DistributorName = &distributorName.String
	}
	if groupID.Valid {
		user.GroupID = &groupID.Int64
	}
	if groupName.Valid {
		user.GroupName = &groupName.String
	}
	if planID.Valid {
		user.PlanID = &planID.Int64
	}
	if planName.Valid {
		user.PlanName = &planName.String
	}
	if inviteUserID.Valid {
		user.InviteUserID = &inviteUserID.Int64
	}
	if inviteUserEmail.Valid {
		user.InviteUserEmail = &inviteUserEmail.String
	}
	if expiredAt.Valid {
		value := time.Unix(expiredAt.Int64, 0).UTC()
		user.ExpiredAt = &value
	}
	if lastOnlineAt.Valid {
		value := time.Unix(lastOnlineAt.Int64, 0).UTC()
		user.LastOnlineAt = &value
	}
	if lastLoginAt.Valid {
		value := time.Unix(lastLoginAt.Int64, 0).UTC()
		user.LastLoginAt = &value
	}
	if commissionRate.Valid {
		value := int(commissionRate.Int64)
		user.CommissionRate = &value
	}
	if discount.Valid {
		value := int(discount.Int64)
		user.Discount = &value
	}
	if nextResetAt.Valid {
		value := time.Unix(nextResetAt.Int64, 0).UTC()
		user.NextResetAt = &value
	}
	if lastResetAt.Valid {
		value := time.Unix(lastResetAt.Int64, 0).UTC()
		user.LastResetAt = &value
	}
	if telegramID.Valid {
		user.TelegramID = &telegramID.Int64
	}
	if remarks.Valid {
		user.Remarks = &remarks.String
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

func normalizedDistributorName(enabled bool, value *string) (*string, error) {
	if !enabled {
		return nil, nil
	}
	if value == nil {
		return nil, fmt.Errorf("%w: distributor name is required", ErrInvalidInput)
	}
	name := strings.TrimSpace(*value)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 100 {
		return nil, fmt.Errorf("%w: distributor name must contain 1 to 100 characters", ErrInvalidInput)
	}
	return &name, nil
}

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
