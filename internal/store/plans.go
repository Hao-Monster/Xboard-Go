package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxPlanNameRunes       = 255
	maxPlanContentBytes    = 256 << 10
	maxPlanTags            = 20
	maxPlanTagRunes        = 64
	maxPlanPriceCents      = int64(9_000_000_000_000_000)
	maxPlanOrderItems      = 10_000
	bytesPerGiB            = int64(1 << 30)
	maxPlanTransferGiB     = int64(8_388_607)
	trafficResetLocationID = "Asia/Shanghai"
)

var planPricePeriods = map[string]struct{}{
	"monthly": {}, "quarterly": {}, "half_yearly": {}, "yearly": {},
	"two_yearly": {}, "three_yearly": {}, "onetime": {}, "reset_traffic": {},
}

func (s *Store) CreatePlan(ctx context.Context, input SavePlanInput, now time.Time) (Plan, error) {
	normalized, pricesJSON, tagsJSON, err := normalizePlanInput(input)
	if err != nil {
		return Plan{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Plan{}, fmt.Errorf("begin create plan: %w", err)
	}
	defer tx.Rollback()
	if err := validatePlanGroup(ctx, tx, normalized.GroupID); err != nil {
		return Plan{}, err
	}
	var position int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_position), -1) + 1 FROM plans`).Scan(&position); err != nil {
		return Plan{}, fmt.Errorf("read next plan position: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO plans (
			group_id, transfer_enable_gib, name, speed_limit, show, sort_position, renew, content,
			reset_traffic_method, capacity_limit, prices_json, sell, device_limit, tags_json,
			revision, created_at, updated_at
		) VALUES (?, ?, ?, ?, 0, ?, 1, ?, ?, ?, ?, 0, ?, ?, 1, ?, ?)
	`, normalized.GroupID, normalized.TransferEnableGiB, normalized.Name, normalized.SpeedLimit, position,
		normalized.Content, normalized.ResetTrafficMethod, normalized.CapacityLimit, pricesJSON,
		normalized.DeviceLimit, tagsJSON, now.Unix(), now.Unix())
	if err != nil {
		return Plan{}, fmt.Errorf("create plan: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Plan{}, fmt.Errorf("read plan id: %w", err)
	}
	plan, err := getPlan(ctx, tx, id, now)
	if err != nil {
		return Plan{}, err
	}
	if err := tx.Commit(); err != nil {
		return Plan{}, fmt.Errorf("commit create plan: %w", err)
	}
	return plan, nil
}

func (s *Store) UpdatePlan(ctx context.Context, planID, revision int64, input SavePlanInput, forceUpdateUsers bool, now time.Time) (Plan, error) {
	if planID < 1 || revision < 1 {
		return Plan{}, ErrInvalidInput
	}
	normalized, pricesJSON, tagsJSON, err := normalizePlanInput(input)
	if err != nil {
		return Plan{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Plan{}, fmt.Errorf("begin update plan: %w", err)
	}
	defer tx.Rollback()
	if err := validatePlanGroup(ctx, tx, normalized.GroupID); err != nil {
		return Plan{}, err
	}
	var currentResetMethod sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT reset_traffic_method FROM plans WHERE id = ? AND revision = ?
	`, planID, revision).Scan(&currentResetMethod); errors.Is(err, sql.ErrNoRows) {
		return Plan{}, planMutationError(ctx, tx, planID)
	} else if err != nil {
		return Plan{}, fmt.Errorf("read current plan reset method: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE plans SET group_id = ?, transfer_enable_gib = ?, name = ?, speed_limit = ?, content = ?,
			reset_traffic_method = ?, capacity_limit = ?, prices_json = ?, device_limit = ?, tags_json = ?,
			revision = revision + 1, updated_at = ?
		WHERE id = ? AND revision = ?
	`, normalized.GroupID, normalized.TransferEnableGiB, normalized.Name, normalized.SpeedLimit, normalized.Content,
		normalized.ResetTrafficMethod, normalized.CapacityLimit, pricesJSON, normalized.DeviceLimit, tagsJSON,
		now.Unix(), planID, revision)
	if err != nil {
		return Plan{}, fmt.Errorf("update plan: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Plan{}, fmt.Errorf("count updated plan: %w", err)
	}
	if rows != 1 {
		return Plan{}, planMutationError(ctx, tx, planID)
	}
	if !sameOptionalPlanInt(currentResetMethod, normalized.ResetTrafficMethod) {
		if err := reschedulePlanUsers(ctx, tx, planID, normalized.ResetTrafficMethod, now); err != nil {
			return Plan{}, err
		}
	}
	if forceUpdateUsers {
		speedLimit := 0
		if normalized.SpeedLimit != nil {
			speedLimit = *normalized.SpeedLimit
		}
		deviceLimit := 0
		if normalized.DeviceLimit != nil {
			deviceLimit = *normalized.DeviceLimit
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE users SET group_id = ?, transfer_enable = ?, speed_limit = ?, device_limit = ?,
				admin_revision = admin_revision + 1, updated_at = ?
			WHERE plan_id = ? AND account_kind = 'human'
		`, normalized.GroupID, normalized.TransferEnableGiB*bytesPerGiB, speedLimit, deviceLimit, now.Unix(), planID); err != nil {
			return Plan{}, fmt.Errorf("force update plan users: %w", err)
		}
	}
	plan, err := getPlan(ctx, tx, planID, now)
	if err != nil {
		return Plan{}, err
	}
	if err := tx.Commit(); err != nil {
		return Plan{}, fmt.Errorf("commit update plan: %w", err)
	}
	return plan, nil
}

func (s *Store) SetPlanState(ctx context.Context, planID, revision int64, state PlanState, now time.Time) (Plan, error) {
	if planID < 1 || revision < 1 {
		return Plan{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE plans SET show = ?, sell = ?, renew = ?, revision = revision + 1, updated_at = ?
		WHERE id = ? AND revision = ?
	`, state.Show, state.Sell, state.Renew, now.Unix(), planID, revision)
	if err != nil {
		return Plan{}, fmt.Errorf("set plan state: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Plan{}, fmt.Errorf("count plan state update: %w", err)
	}
	if rows != 1 {
		return Plan{}, planMutationError(ctx, s.db, planID)
	}
	return getPlan(ctx, s.db, planID, now)
}

func (s *Store) DeletePlan(ctx context.Context, planID int64) error {
	if planID < 1 {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete plan: %w", err)
	}
	defer tx.Rollback()
	var exists, referenced bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM plans WHERE id = ?),
		       EXISTS(SELECT 1 FROM users WHERE plan_id = ? LIMIT 1)
	`, planID, planID).Scan(&exists, &referenced); err != nil {
		return fmt.Errorf("check plan references: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	if referenced {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM plans WHERE id = ?`, planID); err != nil {
		return fmt.Errorf("delete plan: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete plan: %w", err)
	}
	return nil
}

func (s *Store) ReorderPlans(ctx context.Context, ids []int64, now time.Time) ([]Plan, error) {
	if len(ids) == 0 || len(ids) > maxPlanOrderItems {
		return nil, ErrInvalidInput
	}
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id < 1 {
			return nil, ErrInvalidInput
		}
		if _, exists := seen[id]; exists {
			return nil, ErrInvalidInput
		}
		seen[id] = struct{}{}
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin reorder plans: %w", err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plans`).Scan(&count); err != nil {
		return nil, fmt.Errorf("count plans: %w", err)
	}
	if count != len(ids) {
		return nil, ErrInvalidInput
	}
	for position, id := range ids {
		result, err := tx.ExecContext(ctx, `
			UPDATE plans SET sort_position = ?, revision = revision + 1, updated_at = ? WHERE id = ?
		`, position, now.Unix(), id)
		if err != nil {
			return nil, fmt.Errorf("reorder plan: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("count reordered plan: %w", err)
		}
		if rows != 1 {
			return nil, ErrInvalidInput
		}
	}
	plans, err := listPlans(ctx, tx, now, "", nil)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit reorder plans: %w", err)
	}
	return plans, nil
}

func (s *Store) GetPlan(ctx context.Context, planID int64, now time.Time) (Plan, error) {
	return getPlan(ctx, s.db, planID, now)
}

func (s *Store) ListPlans(ctx context.Context, now time.Time) ([]Plan, error) {
	return listPlans(ctx, s.db, now, "", nil)
}

func (s *Store) ListGuestPlanOffers(ctx context.Context, now time.Time) ([]PlanOffer, error) {
	plans, err := listPlans(ctx, s.db, now, `
		WHERE p.show = 1 AND p.sell = 1
		  AND (p.capacity_limit IS NULL OR p.capacity_limit <= 0 OR COALESCE(uc.capacity_users, 0) < p.capacity_limit)
	`, nil)
	if err != nil {
		return nil, err
	}
	systemMethod, err := readSystemTrafficResetMethod(ctx, s.db)
	if err != nil {
		return nil, err
	}
	return planOffers(plans, nil, systemMethod), nil
}

func (s *Store) ListUserPlanOffers(ctx context.Context, userID int64, now time.Time) ([]PlanOffer, error) {
	if userID < 1 {
		return nil, ErrInvalidInput
	}
	var currentPlanID sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT plan_id FROM users WHERE id = ? AND account_kind = 'human'`, userID).Scan(&currentPlanID); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("get user plan: %w", err)
	}
	var current *int64
	if currentPlanID.Valid {
		current = &currentPlanID.Int64
	}
	plans, err := listPlans(ctx, s.db, now, `
		WHERE (p.show = 1 AND p.sell = 1
		       AND (p.capacity_limit IS NULL OR p.capacity_limit <= 0 OR COALESCE(uc.capacity_users, 0) < p.capacity_limit))
		   OR (p.id = ? AND p.renew = 1)
	`, []any{currentPlanID})
	if err != nil {
		return nil, err
	}
	systemMethod, err := readSystemTrafficResetMethod(ctx, s.db)
	if err != nil {
		return nil, err
	}
	return planOffers(plans, current, systemMethod), nil
}

type planQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const planSelect = `
	SELECT p.id, p.group_id, p.transfer_enable_gib, p.name, p.speed_limit, p.show, p.sort_position,
	       p.renew, p.content, p.reset_traffic_method, p.capacity_limit, p.prices_json, p.sell,
	       p.device_limit, p.tags_json, COALESCE(uc.users_count, 0), COALESCE(uc.active_users_count, 0),
	       COALESCE(uc.capacity_users, 0), p.revision, p.created_at, p.updated_at
	FROM plans p
	LEFT JOIN (
		SELECT plan_id, COUNT(*) AS users_count,
		       SUM(CASE WHEN expired_at IS NULL OR expired_at > ? THEN 1 ELSE 0 END) AS active_users_count,
		       SUM(CASE WHEN expired_at IS NULL OR expired_at >= ? THEN 1 ELSE 0 END) AS capacity_users
		FROM users
		WHERE account_kind = 'human' AND plan_id IS NOT NULL
		GROUP BY plan_id
	) uc ON uc.plan_id = p.id
`

func getPlan(ctx context.Context, database planQueryer, planID int64, now time.Time) (Plan, error) {
	return scanPlan(database.QueryRowContext(ctx, planSelect+` WHERE p.id = ?`, now.Unix(), now.Unix(), planID))
}

func listPlans(ctx context.Context, database planQueryer, now time.Time, where string, arguments []any) ([]Plan, error) {
	queryArguments := make([]any, 0, len(arguments)+2)
	queryArguments = append(queryArguments, now.Unix(), now.Unix())
	queryArguments = append(queryArguments, arguments...)
	rows, err := database.QueryContext(ctx, planSelect+where+` ORDER BY p.sort_position, p.id`, queryArguments...)
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	defer rows.Close()
	plans := make([]Plan, 0)
	for rows.Next() {
		plan, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate plans: %w", err)
	}
	return plans, nil
}

func scanPlan(row rowScanner) (Plan, error) {
	var plan Plan
	var groupID, speedLimit, resetMethod, capacityLimit, deviceLimit sql.NullInt64
	var pricesJSON, tagsJSON string
	var createdAt, updatedAt int64
	if err := row.Scan(&plan.ID, &groupID, &plan.TransferEnableGiB, &plan.Name, &speedLimit, &plan.Show,
		&plan.SortPosition, &plan.Renew, &plan.Content, &resetMethod, &capacityLimit, &pricesJSON,
		&plan.Sell, &deviceLimit, &tagsJSON, &plan.UsersCount, &plan.ActiveUsersCount, &plan.CapacityUsersCount,
		&plan.Revision, &createdAt, &updatedAt); errors.Is(err, sql.ErrNoRows) {
		return Plan{}, ErrNotFound
	} else if err != nil {
		return Plan{}, fmt.Errorf("scan plan: %w", err)
	}
	plan.GroupID = nullableInt64Pointer(groupID)
	plan.SpeedLimit = nullableIntPointer(speedLimit)
	plan.ResetTrafficMethod = nullableIntPointer(resetMethod)
	plan.CapacityLimit = nullableIntPointer(capacityLimit)
	plan.DeviceLimit = nullableIntPointer(deviceLimit)
	if err := json.Unmarshal([]byte(pricesJSON), &plan.Prices); err != nil {
		return Plan{}, fmt.Errorf("decode plan prices: %w", err)
	}
	if err := json.Unmarshal([]byte(tagsJSON), &plan.Tags); err != nil {
		return Plan{}, fmt.Errorf("decode plan tags: %w", err)
	}
	if plan.Prices == nil {
		plan.Prices = PlanPrices{}
	}
	if plan.Tags == nil {
		plan.Tags = []string{}
	}
	plan.CreatedAt = time.Unix(createdAt, 0).UTC()
	plan.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return plan, nil
}

func planOffers(plans []Plan, currentPlanID *int64, systemResetMethod int) []PlanOffer {
	offers := make([]PlanOffer, 0, len(plans))
	for _, plan := range plans {
		var remaining *int64
		if plan.CapacityLimit != nil && *plan.CapacityLimit > 0 {
			value := int64(*plan.CapacityLimit) - plan.CapacityUsersCount
			if value < 0 {
				value = 0
			}
			remaining = &value
		}
		isCurrent := currentPlanID != nil && plan.ID == *currentPlanID
		plan.Content = formatPlanContent(plan, systemResetMethod)
		offers = append(offers, PlanOffer{
			Plan: plan, CapacityRemaining: remaining,
			CanPurchase: plan.Show && plan.Sell && (remaining == nil || *remaining > 0),
			CanRenew:    isCurrent && plan.Renew,
		})
	}
	return offers
}

func readSystemTrafficResetMethod(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int, error) {
	var method int
	if err := database.QueryRowContext(ctx, `SELECT traffic_reset_method FROM app_settings WHERE id = 1`).Scan(&method); err != nil {
		return 0, fmt.Errorf("read system traffic reset method: %w", err)
	}
	return method, nil
}

func formatPlanContent(plan Plan, systemResetMethod int) string {
	method := systemResetMethod
	if plan.ResetTrafficMethod != nil {
		method = *plan.ResetTrafficMethod
	}
	resetLabels := [...]string{"每月1号", "按月", "不重置", "每年1月1日", "按年"}
	resetLabel := "按月"
	if method >= 0 && method < len(resetLabels) {
		resetLabel = resetLabels[method]
	}
	return strings.NewReplacer(
		"{{transfer}}", strconv.FormatInt(plan.TransferEnableGiB, 10),
		"{{speed}}", planContentLimit(plan.SpeedLimit),
		"{{devices}}", planContentLimit(plan.DeviceLimit),
		"{{reset_method}}", resetLabel,
	).Replace(plan.Content)
}

func planContentLimit(value *int) string {
	if value == nil {
		return "不限制"
	}
	return strconv.Itoa(*value)
}

func normalizePlanInput(input SavePlanInput) (SavePlanInput, string, string, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Content = strings.TrimSpace(input.Content)
	if input.Name == "" || !utf8.ValidString(input.Name) || utf8.RuneCountInString(input.Name) > maxPlanNameRunes ||
		strings.IndexFunc(input.Name, unicode.IsControl) >= 0 || !utf8.ValidString(input.Content) ||
		len(input.Content) > maxPlanContentBytes || strings.IndexByte(input.Content, 0) >= 0 ||
		input.TransferEnableGiB < 1 || input.TransferEnableGiB > maxPlanTransferGiB ||
		input.GroupID != nil && *input.GroupID < 1 || !validOptionalPlanInt(input.SpeedLimit, 1_000_000_000) ||
		!validOptionalPlanInt(input.DeviceLimit, 1_000) || !validOptionalPlanInt(input.CapacityLimit, 1_000_000_000) ||
		input.ResetTrafficMethod != nil && (*input.ResetTrafficMethod < 0 || *input.ResetTrafficMethod > 4) {
		return SavePlanInput{}, "", "", fmt.Errorf("%w: invalid plan", ErrInvalidInput)
	}
	prices := make(PlanPrices, len(input.Prices))
	for period, price := range input.Prices {
		period = strings.TrimSpace(period)
		if _, valid := planPricePeriods[period]; !valid || price < 0 || price > maxPlanPriceCents {
			return SavePlanInput{}, "", "", fmt.Errorf("%w: invalid plan price", ErrInvalidInput)
		}
		if price > 0 {
			prices[period] = price
		}
	}
	input.Prices = prices
	input.Tags = normalizePlanTags(input.Tags)
	if len(input.Tags) > maxPlanTags {
		return SavePlanInput{}, "", "", fmt.Errorf("%w: invalid plan tags", ErrInvalidInput)
	}
	for _, tag := range input.Tags {
		if !utf8.ValidString(tag) || utf8.RuneCountInString(tag) > maxPlanTagRunes || strings.IndexFunc(tag, unicode.IsControl) >= 0 {
			return SavePlanInput{}, "", "", fmt.Errorf("%w: invalid plan tag", ErrInvalidInput)
		}
	}
	pricesJSON, err := json.Marshal(input.Prices)
	if err != nil {
		return SavePlanInput{}, "", "", fmt.Errorf("encode plan prices: %w", err)
	}
	tagsJSON, err := json.Marshal(input.Tags)
	if err != nil {
		return SavePlanInput{}, "", "", fmt.Errorf("encode plan tags: %w", err)
	}
	return input, string(pricesJSON), string(tagsJSON), nil
}

func normalizePlanTags(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func validOptionalPlanInt(value *int, maximum int) bool {
	return value == nil || *value >= 0 && *value <= maximum
}

func sameOptionalPlanInt(current sql.NullInt64, next *int) bool {
	if !current.Valid || next == nil {
		return !current.Valid && next == nil
	}
	return current.Int64 == int64(*next)
}

func reschedulePlanUsers(ctx context.Context, tx *sql.Tx, planID int64, planMethod *int, now time.Time) error {
	const batchSize = 500
	systemMethod := 1
	if planMethod == nil {
		if err := tx.QueryRowContext(ctx, `SELECT traffic_reset_method FROM app_settings WHERE id = 1`).Scan(&systemMethod); err != nil {
			return fmt.Errorf("read system traffic reset method: %w", err)
		}
	}
	statement, err := tx.PrepareContext(ctx, `UPDATE users SET next_reset_at = ?, updated_at = ? WHERE id = ? AND plan_id = ?`)
	if err != nil {
		return fmt.Errorf("prepare plan user reset reschedule: %w", err)
	}
	defer statement.Close()
	var lastID int64
	for {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, expired_at FROM users
			WHERE account_kind = 'human' AND plan_id = ? AND banned = 0 AND id > ?
			  AND (expired_at IS NULL OR expired_at > ?)
			ORDER BY id LIMIT ?
		`, planID, lastID, now.Unix(), batchSize)
		if err != nil {
			return fmt.Errorf("list plan users for reset reschedule: %w", err)
		}
		type planUser struct {
			id        int64
			expiresAt sql.NullInt64
		}
		users := make([]planUser, 0, batchSize)
		for rows.Next() {
			var user planUser
			if err := rows.Scan(&user.id, &user.expiresAt); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan plan user for reset reschedule: %w", err)
			}
			users = append(users, user)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close plan users for reset reschedule: %w", err)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate plan users for reset reschedule: %w", err)
		}
		for _, user := range users {
			var expiresAt *time.Time
			if user.expiresAt.Valid {
				value := time.Unix(user.expiresAt.Int64, 0)
				expiresAt = &value
			}
			next := CalculateNextTrafficReset(planMethod, systemMethod, expiresAt, now)
			var nextUnix any
			if next != nil {
				nextUnix = next.Unix()
			}
			if _, err := statement.ExecContext(ctx, nextUnix, now.Unix(), user.id, planID); err != nil {
				return fmt.Errorf("reschedule plan user traffic reset: %w", err)
			}
			lastID = user.id
		}
		if len(users) < batchSize {
			return nil
		}
	}
}

func validatePlanGroup(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, groupID *int64) error {
	if groupID == nil {
		return nil
	}
	var exists bool
	if err := database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM server_groups WHERE id = ?)`, *groupID).Scan(&exists); err != nil {
		return fmt.Errorf("validate plan group: %w", err)
	}
	if !exists {
		return fmt.Errorf("%w: plan group does not exist", ErrInvalidInput)
	}
	return nil
}

func planMutationError(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, planID int64) error {
	var exists bool
	if err := database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plans WHERE id = ?)`, planID).Scan(&exists); err != nil {
		return fmt.Errorf("check plan mutation: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrRevisionConflict
}

func nullableIntPointer(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int64)
	return &result
}

// CalculateNextTrafficReset mirrors Xboard's Asia/Shanghai calendar rules.
// A nil plan method follows the system method. Permanent users never receive a
// scheduled reset because the expiration timestamp supplies the anchor.
func CalculateNextTrafficReset(planMethod *int, systemMethod int, expiresAt *time.Time, now time.Time) *time.Time {
	if expiresAt == nil {
		return nil
	}
	method := systemMethod
	if planMethod != nil {
		method = *planMethod
	}
	if method < 0 || method > 4 || method == 2 {
		return nil
	}
	location, err := time.LoadLocation(trafficResetLocationID)
	if err != nil {
		return nil
	}
	localNow := now.In(location)
	localExpiry := expiresAt.In(location)
	var next time.Time
	switch method {
	case 0:
		year, month, _ := localNow.Date()
		next = time.Date(year, month+1, 1, 0, 0, 0, 0, location)
	case 1:
		next = calendarAnchor(localNow.Year(), localNow.Month(), localExpiry, location)
		if !next.After(localNow) {
			nextMonth := time.Date(localNow.Year(), localNow.Month()+1, 1, 0, 0, 0, 0, location)
			next = calendarAnchor(nextMonth.Year(), nextMonth.Month(), localExpiry, location)
		}
	case 3:
		next = time.Date(localNow.Year()+1, time.January, 1, 0, 0, 0, 0, location)
	case 4:
		next = annualAnchor(localNow.Year(), localExpiry, location)
		if !next.After(localNow) {
			next = annualAnchor(localNow.Year()+1, localExpiry, location)
		}
	}
	result := next.UTC()
	return &result
}

func calendarAnchor(year int, month time.Month, expiry time.Time, location *time.Location) time.Time {
	day := expiry.Day()
	maximum := daysInMonth(year, month, location)
	if day > maximum {
		day = maximum
	}
	return time.Date(year, month, day, expiry.Hour(), expiry.Minute(), expiry.Second(), 0, location)
}

func annualAnchor(year int, expiry time.Time, location *time.Location) time.Time {
	day := expiry.Day()
	maximum := daysInMonth(year, expiry.Month(), location)
	if day > maximum {
		day = maximum
	}
	return time.Date(year, expiry.Month(), day, expiry.Hour(), expiry.Minute(), expiry.Second(), 0, location)
}

func daysInMonth(year int, month time.Month, location *time.Location) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, location).Day()
}

func (s *Store) ProcessDueTrafficResets(ctx context.Context, now time.Time, limit int) (TrafficResetBatchResult, error) {
	if limit < 1 || limit > 1_000 {
		return TrafficResetBatchResult{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TrafficResetBatchResult{}, fmt.Errorf("begin traffic reset batch: %w", err)
	}
	defer tx.Rollback()
	var systemMethod int
	if err := tx.QueryRowContext(ctx, `SELECT traffic_reset_method FROM app_settings WHERE id = 1`).Scan(&systemMethod); err != nil {
		return TrafficResetBatchResult{}, fmt.Errorf("read traffic reset method: %w", err)
	}
	type dueReset struct {
		userID       int64
		planID       int64
		scheduledFor int64
		expiredAt    int64
		upload       int64
		download     int64
		resetCount   int64
		planMethod   sql.NullInt64
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT u.id, u.plan_id, u.next_reset_at, u.expired_at, u.traffic_u, u.traffic_d, u.reset_count,
		       p.reset_traffic_method
		FROM users u JOIN plans p ON p.id = u.plan_id
		WHERE u.account_kind = 'human' AND u.banned = 0 AND u.expired_at IS NOT NULL
		  AND u.expired_at > ? AND u.next_reset_at IS NOT NULL AND u.next_reset_at <= ?
		ORDER BY u.next_reset_at, u.id LIMIT ?
	`, now.Unix(), now.Unix(), limit)
	if err != nil {
		return TrafficResetBatchResult{}, fmt.Errorf("list due traffic resets: %w", err)
	}
	due := make([]dueReset, 0, limit)
	for rows.Next() {
		var item dueReset
		if err := rows.Scan(&item.userID, &item.planID, &item.scheduledFor, &item.expiredAt,
			&item.upload, &item.download, &item.resetCount, &item.planMethod); err != nil {
			_ = rows.Close()
			return TrafficResetBatchResult{}, fmt.Errorf("scan due traffic reset: %w", err)
		}
		due = append(due, item)
	}
	if err := rows.Close(); err != nil {
		return TrafficResetBatchResult{}, fmt.Errorf("close due traffic resets: %w", err)
	}
	if err := rows.Err(); err != nil {
		return TrafficResetBatchResult{}, fmt.Errorf("iterate due traffic resets: %w", err)
	}
	processed := 0
	for _, item := range due {
		var method *int
		if item.planMethod.Valid {
			value := int(item.planMethod.Int64)
			method = &value
		}
		expires := time.Unix(item.expiredAt, 0)
		next := CalculateNextTrafficReset(method, systemMethod, &expires, now)
		var nextUnix any
		if next != nil {
			nextUnix = next.Unix()
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE users SET traffic_u = 0, traffic_d = 0, last_reset_at = ?, reset_count = reset_count + 1,
				next_reset_at = ?, updated_at = ?
			WHERE id = ? AND plan_id = ? AND next_reset_at = ? AND next_reset_at <= ?
			  AND account_kind = 'human' AND banned = 0 AND expired_at IS NOT NULL AND expired_at > ?
		`, now.Unix(), nextUnix, now.Unix(), item.userID, item.planID, item.scheduledFor, now.Unix(), now.Unix())
		if err != nil {
			return TrafficResetBatchResult{}, fmt.Errorf("reset user traffic: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return TrafficResetBatchResult{}, fmt.Errorf("count reset user traffic: %w", err)
		}
		if updated == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO traffic_reset_logs (
				user_id, plan_id, scheduled_for, reset_at, upload_before, download_before, reset_count
			) VALUES (?, ?, ?, ?, ?, ?, ?)
		`, item.userID, item.planID, item.scheduledFor, now.Unix(), item.upload, item.download, item.resetCount+1); err != nil {
			return TrafficResetBatchResult{}, fmt.Errorf("record traffic reset: %w", err)
		}
		processed++
	}
	var remaining int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM users
		WHERE account_kind = 'human' AND banned = 0 AND expired_at IS NOT NULL AND expired_at > ?
		  AND plan_id IS NOT NULL AND next_reset_at IS NOT NULL AND next_reset_at <= ?
	`, now.Unix(), now.Unix()).Scan(&remaining); err != nil {
		return TrafficResetBatchResult{}, fmt.Errorf("count remaining traffic resets: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return TrafficResetBatchResult{}, fmt.Errorf("commit traffic reset batch: %w", err)
	}
	return TrafficResetBatchResult{Processed: processed, Remaining: remaining}, nil
}
