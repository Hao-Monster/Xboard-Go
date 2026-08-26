package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxCouponCodeBytes = 64
	maxCouponNameBytes = 200
	maxCouponListSize  = 200
	maxCouponPlanIDs   = 100
	maxCouponUseLimit  = 1_000_000_000
)

const couponCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func (s *Store) CreateCoupon(ctx context.Context, input SaveCouponInput, now time.Time) (Coupon, error) {
	normalized, err := normalizeCouponInput(input, now)
	if err != nil {
		return Coupon{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Coupon{}, fmt.Errorf("begin create coupon: %w", err)
	}
	defer tx.Rollback()
	if err := validateCouponPlans(ctx, tx, normalized.LimitPlanIDs); err != nil {
		return Coupon{}, err
	}
	planJSON, periodJSON, err := encodeCouponLimits(normalized)
	if err != nil {
		return Coupon{}, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO coupons (
			code, name, type, value, show, limit_use, limit_use_with_user,
			limit_plan_ids_json, limit_periods_json, started_at, ended_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, normalized.Code, normalized.Name, normalized.Type, normalized.Value, normalized.Show,
		nullableCouponLimit(normalized.LimitUse), nullableCouponLimit(normalized.LimitUseWithUser),
		planJSON, periodJSON, normalized.StartedAt.Unix(), normalized.EndedAt.Unix(), now.Unix(), now.Unix())
	if err != nil {
		if couponCodeExists(ctx, tx, normalized.Code, 0) {
			return Coupon{}, ErrConflict
		}
		return Coupon{}, fmt.Errorf("create coupon: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Coupon{}, fmt.Errorf("read coupon ID: %w", err)
	}
	coupon, err := getCoupon(ctx, tx, id)
	if err != nil {
		return Coupon{}, err
	}
	if err := tx.Commit(); err != nil {
		return Coupon{}, fmt.Errorf("commit create coupon: %w", err)
	}
	return coupon, nil
}

func (s *Store) CreateCouponBatch(ctx context.Context, input SaveCouponInput, count int, now time.Time) ([]Coupon, error) {
	if count < 1 || count > 500 || strings.TrimSpace(input.Code) != "" {
		return nil, ErrInvalidInput
	}
	code, err := newCouponCode()
	if err != nil {
		return nil, err
	}
	input.Code = code
	normalized, err := normalizeCouponInput(input, now)
	if err != nil {
		return nil, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin create coupon batch: %w", err)
	}
	defer tx.Rollback()
	if err := validateCouponPlans(ctx, tx, normalized.LimitPlanIDs); err != nil {
		return nil, err
	}
	planJSON, periodJSON, err := encodeCouponLimits(normalized)
	if err != nil {
		return nil, err
	}
	coupons := make([]Coupon, 0, count)
	for index := 0; index < count; index++ {
		inserted := false
		for attempt := 0; attempt < 8; attempt++ {
			if index > 0 || attempt > 0 {
				normalized.Code, err = newCouponCode()
				if err != nil {
					return nil, err
				}
			}
			result, insertErr := tx.ExecContext(ctx, `
				INSERT INTO coupons (
					code, name, type, value, show, limit_use, limit_use_with_user,
					limit_plan_ids_json, limit_periods_json, started_at, ended_at, created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, normalized.Code, normalized.Name, normalized.Type, normalized.Value, normalized.Show,
				nullableCouponLimit(normalized.LimitUse), nullableCouponLimit(normalized.LimitUseWithUser),
				planJSON, periodJSON, normalized.StartedAt.Unix(), normalized.EndedAt.Unix(), now.Unix(), now.Unix())
			if insertErr != nil {
				if couponCodeExists(ctx, tx, normalized.Code, 0) {
					continue
				}
				return nil, fmt.Errorf("create coupon batch: %w", insertErr)
			}
			id, idErr := result.LastInsertId()
			if idErr != nil {
				return nil, fmt.Errorf("read coupon batch ID: %w", idErr)
			}
			coupons = append(coupons, couponFromInput(id, normalized, now))
			inserted = true
			break
		}
		if !inserted {
			return nil, fmt.Errorf("%w: coupon code collision", ErrConflict)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit create coupon batch: %w", err)
	}
	return coupons, nil
}

func (s *Store) UpdateCoupon(ctx context.Context, couponID int64, input SaveCouponInput, now time.Time) (Coupon, error) {
	if couponID < 1 {
		return Coupon{}, ErrInvalidInput
	}
	normalized, err := normalizeCouponInput(input, now)
	if err != nil {
		return Coupon{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Coupon{}, fmt.Errorf("begin update coupon: %w", err)
	}
	defer tx.Rollback()
	if err := validateCouponPlans(ctx, tx, normalized.LimitPlanIDs); err != nil {
		return Coupon{}, err
	}
	if couponCodeExists(ctx, tx, normalized.Code, couponID) {
		return Coupon{}, ErrConflict
	}
	planJSON, periodJSON, err := encodeCouponLimits(normalized)
	if err != nil {
		return Coupon{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE coupons SET code = ?, name = ?, type = ?, value = ?, show = ?, limit_use = ?,
			limit_use_with_user = ?, limit_plan_ids_json = ?, limit_periods_json = ?,
			started_at = ?, ended_at = ?, updated_at = ?
		WHERE id = ?
	`, normalized.Code, normalized.Name, normalized.Type, normalized.Value, normalized.Show,
		nullableCouponLimit(normalized.LimitUse), nullableCouponLimit(normalized.LimitUseWithUser), planJSON,
		periodJSON, normalized.StartedAt.Unix(), normalized.EndedAt.Unix(), now.Unix(), couponID)
	if err != nil {
		return Coupon{}, fmt.Errorf("update coupon: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return Coupon{}, fmt.Errorf("count updated coupon: %w", err)
	}
	if updated != 1 {
		return Coupon{}, ErrNotFound
	}
	coupon, err := getCoupon(ctx, tx, couponID)
	if err != nil {
		return Coupon{}, err
	}
	if err := tx.Commit(); err != nil {
		return Coupon{}, fmt.Errorf("commit update coupon: %w", err)
	}
	return coupon, nil
}

func (s *Store) SetCouponVisibility(ctx context.Context, couponID int64, show bool, now time.Time) (Coupon, error) {
	if couponID < 1 || now.Unix() < 0 {
		return Coupon{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `UPDATE coupons SET show = ?, updated_at = ? WHERE id = ?`, show, now.Unix(), couponID)
	if err != nil {
		return Coupon{}, fmt.Errorf("set coupon visibility: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return Coupon{}, fmt.Errorf("count coupon visibility update: %w", err)
	}
	if updated != 1 {
		return Coupon{}, ErrNotFound
	}
	return s.GetCoupon(ctx, couponID)
}

func (s *Store) GetCoupon(ctx context.Context, couponID int64) (Coupon, error) {
	if couponID < 1 {
		return Coupon{}, ErrInvalidInput
	}
	return getCoupon(ctx, s.db, couponID)
}

func (s *Store) DeleteCoupon(ctx context.Context, couponID int64) error {
	if couponID < 1 {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete coupon: %w", err)
	}
	defer tx.Rollback()
	var referenced bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM orders WHERE coupon_id = ?)`, couponID).Scan(&referenced); err != nil {
		return fmt.Errorf("check coupon references: %w", err)
	}
	if referenced {
		return ErrCouponReferenced
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM coupons WHERE id = ?`, couponID)
	if err != nil {
		return fmt.Errorf("delete coupon: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count deleted coupon: %w", err)
	}
	if deleted != 1 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete coupon: %w", err)
	}
	return nil
}

func (s *Store) ListCoupons(ctx context.Context, filter CouponFilter) (CouponPage, error) {
	if filter.Page < 1 || filter.PageSize < 1 || filter.PageSize > maxCouponListSize || len(filter.Query) > maxCouponNameBytes {
		return CouponPage{}, ErrInvalidInput
	}
	if filter.Type != nil && *filter.Type != CouponTypeFixed && *filter.Type != CouponTypePercentage {
		return CouponPage{}, ErrInvalidInput
	}
	columns := map[string]string{
		"": "created_at", "id": "id", "name": "name", "type": "type", "code": "code",
		"limit_use": "limit_use", "started_at": "started_at", "ended_at": "ended_at", "created_at": "created_at",
	}
	sortColumn, valid := columns[filter.Sort]
	if !valid {
		return CouponPage{}, ErrInvalidInput
	}
	where := make([]string, 0, 3)
	arguments := make([]any, 0, 5)
	query := strings.TrimSpace(filter.Query)
	if query != "" {
		where = append(where, `(name LIKE ? ESCAPE '\' OR code LIKE ? ESCAPE '\')`)
		like := "%" + escapeLike(query) + "%"
		arguments = append(arguments, like, like)
	}
	if filter.Type != nil {
		where = append(where, `type = ?`)
		arguments = append(arguments, *filter.Type)
	}
	if filter.Show != nil {
		where = append(where, `show = ?`)
		arguments = append(arguments, *filter.Show)
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM coupons`+whereSQL, arguments...).Scan(&total); err != nil {
		return CouponPage{}, fmt.Errorf("count coupons: %w", err)
	}
	direction := "ASC"
	if filter.Desc || filter.Sort == "" {
		direction = "DESC"
	}
	listArguments := append(append([]any(nil), arguments...), filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := s.db.QueryContext(ctx, couponSelect+whereSQL+` ORDER BY `+sortColumn+` `+direction+`, id `+direction+` LIMIT ? OFFSET ?`, listArguments...)
	if err != nil {
		return CouponPage{}, fmt.Errorf("list coupons: %w", err)
	}
	defer rows.Close()
	items := make([]Coupon, 0, filter.PageSize)
	for rows.Next() {
		coupon, scanErr := scanCoupon(rows)
		if scanErr != nil {
			return CouponPage{}, scanErr
		}
		items = append(items, coupon)
	}
	if err := rows.Err(); err != nil {
		return CouponPage{}, fmt.Errorf("iterate coupons: %w", err)
	}
	return CouponPage{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (s *Store) CheckCoupon(ctx context.Context, input CouponCheckInput, now time.Time) (CouponQuote, error) {
	period, valid := normalizeOrderPeriod(input.Period)
	code := strings.TrimSpace(input.Code)
	if input.UserID < 1 || input.PlanID < 1 || !valid || !validCouponCode(code) || now.Unix() < 0 {
		return CouponQuote{}, ErrInvalidInput
	}
	user, err := readOrderUser(ctx, s.db, input.UserID)
	if err != nil {
		return CouponQuote{}, err
	}
	if user.banned || user.accountKind != AccountKindHuman {
		return CouponQuote{}, ErrCouponInvalid
	}
	plan, err := getPlan(ctx, s.db, input.PlanID, now)
	if err != nil {
		return CouponQuote{}, err
	}
	price, exists := plan.Prices[period]
	if !exists {
		return CouponQuote{}, ErrPlanUnavailable
	}
	coupon, err := validateCoupon(ctx, s.db, user.id, plan.ID, period, code, now)
	if err != nil {
		return CouponQuote{}, err
	}
	discount := couponDiscount(price, coupon)
	return CouponQuote{Coupon: coupon, OriginalAmount: price, CouponDiscountAmount: discount, TotalAfterCoupon: price - discount}, nil
}

func (s *Store) CouponEnabled(ctx context.Context) (bool, error) {
	var enabled bool
	if err := s.db.QueryRowContext(ctx, `SELECT coupon_enabled FROM app_settings WHERE id = 1`).Scan(&enabled); err != nil {
		return false, fmt.Errorf("read coupon setting: %w", err)
	}
	return enabled, nil
}

func validateCoupon(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID, planID int64, period, code string, now time.Time) (Coupon, error) {
	var enabled bool
	if err := database.QueryRowContext(ctx, `SELECT coupon_enabled FROM app_settings WHERE id = 1`).Scan(&enabled); err != nil {
		return Coupon{}, fmt.Errorf("read coupon setting: %w", err)
	}
	if !enabled {
		return Coupon{}, ErrCouponInvalid
	}
	coupon, err := scanCoupon(database.QueryRowContext(ctx, couponSelect+` WHERE code = ?`, code))
	if errors.Is(err, ErrNotFound) {
		return Coupon{}, ErrCouponInvalid
	}
	if err != nil {
		return Coupon{}, err
	}
	if !coupon.Show {
		return Coupon{}, ErrCouponInvalid
	}
	if now.Before(coupon.StartedAt) {
		return Coupon{}, ErrCouponNotStarted
	}
	if now.After(coupon.EndedAt) {
		return Coupon{}, ErrCouponExpired
	}
	if coupon.LimitUse != nil && *coupon.LimitUse <= 0 {
		return Coupon{}, ErrCouponExhausted
	}
	if len(coupon.LimitPlanIDs) > 0 && !containsInt64(coupon.LimitPlanIDs, planID) {
		return Coupon{}, ErrCouponPlanRestricted
	}
	if len(coupon.LimitPeriods) > 0 && !containsString(coupon.LimitPeriods, period) {
		return Coupon{}, ErrCouponPeriodRestricted
	}
	if coupon.LimitUseWithUser != nil {
		var used int
		if err := database.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM orders WHERE coupon_id = ? AND user_id = ? AND status NOT IN (0, 2)
		`, coupon.ID, userID).Scan(&used); err != nil {
			return Coupon{}, fmt.Errorf("count coupon user uses: %w", err)
		}
		if used >= *coupon.LimitUseWithUser {
			return Coupon{}, ErrCouponUserLimit
		}
	}
	return coupon, nil
}

func normalizeCouponInput(input SaveCouponInput, now time.Time) (SaveCouponInput, error) {
	input.Code = strings.TrimSpace(input.Code)
	input.Name = strings.TrimSpace(input.Name)
	if !validCouponCode(input.Code) || !utf8.ValidString(input.Name) || len(input.Name) < 1 || len(input.Name) > maxCouponNameBytes ||
		strings.IndexFunc(input.Name, unicode.IsControl) >= 0 || now.Unix() < 0 || input.StartedAt.Unix() < 0 ||
		!input.EndedAt.After(input.StartedAt) {
		return SaveCouponInput{}, ErrInvalidInput
	}
	if input.Type != CouponTypeFixed && input.Type != CouponTypePercentage {
		return SaveCouponInput{}, ErrInvalidInput
	}
	if input.Type == CouponTypeFixed && (input.Value < 1 || input.Value > maxOrderMoneyCents) ||
		input.Type == CouponTypePercentage && (input.Value < 1 || input.Value > 100) {
		return SaveCouponInput{}, ErrInvalidInput
	}
	if input.LimitUse != nil && (*input.LimitUse < 0 || *input.LimitUse > maxCouponUseLimit) ||
		input.LimitUseWithUser != nil && (*input.LimitUseWithUser < 0 || *input.LimitUseWithUser > maxCouponUseLimit) {
		return SaveCouponInput{}, ErrInvalidInput
	}
	input.LimitPlanIDs = normalizeCouponPlanIDs(input.LimitPlanIDs)
	if len(input.LimitPlanIDs) > maxCouponPlanIDs {
		return SaveCouponInput{}, ErrInvalidInput
	}
	periods := make([]string, 0, len(input.LimitPeriods))
	seenPeriods := make(map[string]struct{}, len(input.LimitPeriods))
	for _, value := range input.LimitPeriods {
		period, valid := normalizeOrderPeriod(value)
		if !valid {
			return SaveCouponInput{}, ErrInvalidInput
		}
		if _, exists := seenPeriods[period]; exists {
			continue
		}
		seenPeriods[period] = struct{}{}
		periods = append(periods, period)
	}
	input.LimitPeriods = periods
	return input, nil
}

func normalizeCouponPlanIDs(values []int64) []int64 {
	normalized := make([]int64, 0, len(values))
	seen := make(map[int64]struct{}, len(values))
	for _, value := range values {
		if value < 1 {
			return []int64{0}
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized
}

func validateCouponPlans(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, planIDs []int64) error {
	for _, planID := range planIDs {
		if planID < 1 {
			return ErrInvalidInput
		}
		var exists bool
		if err := database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plans WHERE id = ?)`, planID).Scan(&exists); err != nil {
			return fmt.Errorf("validate coupon plan: %w", err)
		}
		if !exists {
			return fmt.Errorf("%w: coupon plan does not exist", ErrInvalidInput)
		}
	}
	return nil
}

func encodeCouponLimits(input SaveCouponInput) (string, string, error) {
	plans, err := json.Marshal(input.LimitPlanIDs)
	if err != nil {
		return "", "", fmt.Errorf("encode coupon plans: %w", err)
	}
	periods, err := json.Marshal(input.LimitPeriods)
	if err != nil {
		return "", "", fmt.Errorf("encode coupon periods: %w", err)
	}
	return string(plans), string(periods), nil
}

func couponDiscount(amount int64, coupon Coupon) int64 {
	var discount int64
	if coupon.Type == CouponTypePercentage {
		discount = percentageCents(amount, coupon.Value)
	} else {
		discount = coupon.Value
	}
	return minInt64(amount, discount)
}

func percentageCents(amount, percent int64) int64 {
	return (amount*percent + 50) / 100
}

func validCouponCode(value string) bool {
	return utf8.ValidString(value) && len(value) >= 1 && len(value) <= maxCouponCodeBytes && strings.IndexFunc(value, unicode.IsControl) < 0
}

func getCoupon(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, couponID int64) (Coupon, error) {
	return scanCoupon(database.QueryRowContext(ctx, couponSelect+` WHERE id = ?`, couponID))
}

func scanCoupon(row rowScanner) (Coupon, error) {
	var coupon Coupon
	var limitUse, limitUseWithUser sql.NullInt64
	var planJSON, periodJSON string
	var startedAt, endedAt, createdAt, updatedAt int64
	if err := row.Scan(&coupon.ID, &coupon.Code, &coupon.Name, &coupon.Type, &coupon.Value, &coupon.Show,
		&limitUse, &limitUseWithUser, &planJSON, &periodJSON, &startedAt, &endedAt, &createdAt, &updatedAt); errors.Is(err, sql.ErrNoRows) {
		return Coupon{}, ErrNotFound
	} else if err != nil {
		return Coupon{}, fmt.Errorf("scan coupon: %w", err)
	}
	coupon.LimitUse = nullableIntPointer(limitUse)
	coupon.LimitUseWithUser = nullableIntPointer(limitUseWithUser)
	if err := json.Unmarshal([]byte(planJSON), &coupon.LimitPlanIDs); err != nil {
		return Coupon{}, fmt.Errorf("decode coupon plans: %w", err)
	}
	if err := json.Unmarshal([]byte(periodJSON), &coupon.LimitPeriods); err != nil {
		return Coupon{}, fmt.Errorf("decode coupon periods: %w", err)
	}
	if coupon.LimitPlanIDs == nil {
		coupon.LimitPlanIDs = []int64{}
	}
	if coupon.LimitPeriods == nil {
		coupon.LimitPeriods = []string{}
	}
	coupon.StartedAt = time.Unix(startedAt, 0).UTC()
	coupon.EndedAt = time.Unix(endedAt, 0).UTC()
	coupon.CreatedAt = time.Unix(createdAt, 0).UTC()
	coupon.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return coupon, nil
}

func couponCodeExists(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, code string, exceptID int64) bool {
	var exists bool
	if err := database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coupons WHERE code = ? AND id != ?)`, code, exceptID).Scan(&exists); err != nil {
		return false
	}
	return exists
}

func nullableCouponLimit(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func couponFromInput(id int64, input SaveCouponInput, now time.Time) Coupon {
	return Coupon{
		ID: id, Code: input.Code, Name: input.Name, Type: input.Type, Value: input.Value, Show: input.Show,
		LimitUse: cloneIntPointer(input.LimitUse), LimitUseWithUser: cloneIntPointer(input.LimitUseWithUser),
		LimitPlanIDs: append([]int64(nil), input.LimitPlanIDs...), LimitPeriods: append([]string(nil), input.LimitPeriods...),
		StartedAt: input.StartedAt.UTC(), EndedAt: input.EndedAt.UTC(), CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func newCouponCode() (string, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate coupon code: %w", err)
	}
	for index, value := range random {
		random[index] = couponCodeAlphabet[int(value)&(len(couponCodeAlphabet)-1)]
	}
	return string(random), nil
}

func containsInt64(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

const couponSelect = `SELECT id, code, name, type, value, show, limit_use, limit_use_with_user,
	limit_plan_ids_json, limit_periods_json, started_at, ended_at, created_at, updated_at FROM coupons`
