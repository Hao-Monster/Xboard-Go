package legacymigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	maxLegacyCouponRows      = 1_000_000
	maxLegacyCouponDataBytes = int64(256 << 20)
)

type CouponsSnapshot struct {
	Path             string
	Size             int64
	SHA256           string
	Coupons          []store.LegacyCoupon
	CouponsChecksum  string
	CouponEnabled    bool
	SettingsChecksum string
}

func ReadCouponsSnapshot(ctx context.Context, sourcePath string) (CouponsSnapshot, error) {
	coupons := []store.LegacyCoupon{}
	enabled := true
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_coupon", []string{
			"id", "code", "name", "type", "value", "show", "limit_use", "limit_use_with_user",
			"limit_plan_ids", "limit_period", "started_at", "ended_at", "created_at", "updated_at",
		}); err != nil {
			return err
		}
		if err := requireRealTable(ctx, database, "v2_settings", []string{"name", "value"}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(
				length(CAST(code AS BLOB)) + length(CAST(name AS BLOB)) +
				COALESCE(length(CAST(limit_plan_ids AS BLOB)), 0) + COALESCE(length(CAST(limit_period AS BLOB)), 0)
			), 0) FROM v2_coupon
		`, maxLegacyCouponRows, maxLegacyCouponDataBytes, "legacy coupons"); err != nil {
			return err
		}
		var bytesRead int64
		var readErr error
		coupons, bytesRead, readErr = readLegacyCoupons(ctx, database)
		if readErr != nil {
			return readErr
		}
		enabled, readErr = readLegacyCouponSetting(ctx, database)
		if readErr != nil {
			return readErr
		}
		if bytesRead > maxLegacyCouponDataBytes {
			return errors.New("legacy coupons exceed the migration data limit")
		}
		return nil
	})
	if err != nil {
		return CouponsSnapshot{}, err
	}
	return CouponsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256, Coupons: coupons,
		CouponsChecksum: store.LegacyCouponsChecksum(coupons), CouponEnabled: enabled,
		SettingsChecksum: store.LegacyCouponSettingsChecksum(enabled),
	}, nil
}

func readLegacyCoupons(ctx context.Context, database *sql.DB) ([]store.LegacyCoupon, int64, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, code, name, type, value, show, limit_use, limit_use_with_user,
		       limit_plan_ids, limit_period, started_at, ended_at,
		       `+legacyUnixExpression("created_at")+`, `+legacyUnixExpression("updated_at")+`
		FROM v2_coupon ORDER BY id
	`)
	if err != nil {
		return nil, 0, fmt.Errorf("read legacy coupons: %w", err)
	}
	defer rows.Close()
	coupons := make([]store.LegacyCoupon, 0)
	var bytesRead int64
	for rows.Next() {
		if len(coupons) >= maxLegacyCouponRows {
			return nil, 0, fmt.Errorf("legacy coupons exceed the %d-row migration limit", maxLegacyCouponRows)
		}
		var coupon store.LegacyCoupon
		var limitUse, limitUser sql.NullInt64
		var planJSON, periodJSON sql.NullString
		if err := rows.Scan(&coupon.ID, &coupon.Code, &coupon.Name, &coupon.Type, &coupon.Value, &coupon.Show,
			&limitUse, &limitUser, &planJSON, &periodJSON, &coupon.StartedAt, &coupon.EndedAt, &coupon.CreatedAt, &coupon.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan legacy coupon: %w", err)
		}
		coupon.LimitUse, err = legacyCouponLimitPointer(limitUse)
		if err != nil {
			return nil, 0, fmt.Errorf("legacy coupon id %d limit_use: %w", coupon.ID, err)
		}
		coupon.LimitUseWithUser, err = legacyCouponLimitPointer(limitUser)
		if err != nil {
			return nil, 0, fmt.Errorf("legacy coupon id %d limit_use_with_user: %w", coupon.ID, err)
		}
		coupon.LimitPlanIDs, err = decodeLegacyCouponPlanIDs(planJSON)
		if err != nil {
			return nil, 0, fmt.Errorf("decode legacy coupon id %d plan restrictions: %w", coupon.ID, err)
		}
		coupon.LimitPeriods, err = decodeLegacyCouponPeriods(periodJSON)
		if err != nil {
			return nil, 0, fmt.Errorf("decode legacy coupon id %d period restrictions: %w", coupon.ID, err)
		}
		bytesRead += int64(len(coupon.Code) + len(coupon.Name) + len(planJSON.String) + len(periodJSON.String))
		if bytesRead > maxLegacyCouponDataBytes {
			return nil, 0, errors.New("legacy coupons exceed the migration data limit")
		}
		coupons = append(coupons, coupon)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate legacy coupons: %w", err)
	}
	if err := store.ValidateLegacyCouponsData(coupons); err != nil {
		return nil, 0, fmt.Errorf("validate legacy coupons: %w", err)
	}
	return coupons, bytesRead, nil
}

func readLegacyCouponSetting(ctx context.Context, database *sql.DB) (bool, error) {
	rows, err := database.QueryContext(ctx, `SELECT value FROM v2_settings WHERE name = 'app_enable_coupon_system'`)
	if err != nil {
		return false, fmt.Errorf("read legacy coupon setting: %w", err)
	}
	defer rows.Close()
	enabled := true
	found := false
	for rows.Next() {
		if found {
			return false, errors.New("legacy coupon setting contains duplicate rows")
		}
		found = true
		var value sql.NullString
		if err := rows.Scan(&value); err != nil {
			return false, fmt.Errorf("scan legacy coupon setting: %w", err)
		}
		if !value.Valid {
			enabled = true
			continue
		}
		switch strings.ToLower(strings.TrimSpace(value.String)) {
		case "0", "false", "":
			enabled = false
		case "1", "true":
			enabled = true
		default:
			return false, errors.New("legacy coupon setting has an ambiguous boolean value")
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate legacy coupon setting: %w", err)
	}
	return enabled, nil
}

func legacyCouponLimitPointer(value sql.NullInt64) (*int, error) {
	if !value.Valid {
		return nil, nil
	}
	result := int(value.Int64)
	if int64(result) != value.Int64 {
		return nil, errors.New("integer exceeds platform range")
	}
	return &result, nil
}

func decodeLegacyCouponPlanIDs(value sql.NullString) ([]int64, error) {
	if !value.Valid || strings.TrimSpace(value.String) == "" || strings.TrimSpace(value.String) == "null" {
		return []int64{}, nil
	}
	decoder := json.NewDecoder(strings.NewReader(value.String))
	decoder.UseNumber()
	var raw []any
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing JSON data")
	}
	result := make([]int64, 0, len(raw))
	for _, item := range raw {
		var text string
		switch typed := item.(type) {
		case json.Number:
			text = typed.String()
		case string:
			text = typed
		default:
			return nil, errors.New("plan restriction must contain integer IDs")
		}
		id, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, errors.New("plan restriction must contain integer IDs")
		}
		result = append(result, id)
	}
	return result, nil
}

func decodeLegacyCouponPeriods(value sql.NullString) ([]string, error) {
	if !value.Valid || strings.TrimSpace(value.String) == "" || strings.TrimSpace(value.String) == "null" {
		return []string{}, nil
	}
	decoder := json.NewDecoder(strings.NewReader(value.String))
	var raw []string
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing JSON data")
	}
	for index, period := range raw {
		if converted, exists := legacyOrderPeriodNames[period]; exists {
			raw[index] = converted
		}
	}
	return raw, nil
}
