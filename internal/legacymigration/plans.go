package legacymigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	maxLegacyPlanRows      = 10_000
	maxLegacyPlanDataBytes = int64(64 << 20)
)

var legacyPlanPricePeriods = map[string]struct{}{
	"monthly": {}, "quarterly": {}, "half_yearly": {}, "yearly": {},
	"two_yearly": {}, "three_yearly": {}, "onetime": {}, "reset_traffic": {},
}

type PlansSnapshot struct {
	Path               string
	Size               int64
	SHA256             string
	Plans              []store.LegacyPlan
	Checksum           string
	TrafficResetMethod int
	SettingsChecksum   string
}

func ReadPlansSnapshot(ctx context.Context, sourcePath string) (PlansSnapshot, error) {
	var plans []store.LegacyPlan
	trafficResetMethod := 1
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_plan", []string{
			"id", "group_id", "transfer_enable", "name", "speed_limit", "show", "sort", "renew", "content",
			"reset_traffic_method", "capacity_limit", "created_at", "updated_at", "prices", "sell", "device_limit", "tags",
		}); err != nil {
			return err
		}
		if err := requireRealTable(ctx, database, "v2_settings", []string{"name", "value"}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(
				length(CAST(name AS BLOB)) + COALESCE(length(CAST(content AS BLOB)), 0) +
				COALESCE(length(CAST(prices AS BLOB)), 0) + COALESCE(length(CAST(tags AS BLOB)), 0)
			), 0) FROM v2_plan
		`, maxLegacyPlanRows, maxLegacyPlanDataBytes, "legacy plans"); err != nil {
			return err
		}
		var readErr error
		plans, readErr = readLegacyPlans(ctx, database)
		if readErr != nil {
			return readErr
		}
		trafficResetMethod, readErr = readLegacyTrafficResetMethod(ctx, database)
		return readErr
	})
	if err != nil {
		return PlansSnapshot{}, err
	}
	if plans == nil {
		plans = []store.LegacyPlan{}
	}
	return PlansSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Plans: plans, Checksum: store.LegacyPlansChecksum(plans), TrafficResetMethod: trafficResetMethod,
		SettingsChecksum: store.LegacyPlanSettingsChecksum(trafficResetMethod),
	}, nil
}

func readLegacyTrafficResetMethod(ctx context.Context, database *sql.DB) (int, error) {
	rows, err := database.QueryContext(ctx, `SELECT value FROM v2_settings WHERE name = 'reset_traffic_method'`)
	if err != nil {
		return 0, fmt.Errorf("read legacy traffic reset method: %w", err)
	}
	defer rows.Close()
	method := 1
	found := false
	for rows.Next() {
		if found {
			return 0, errors.New("legacy traffic reset method contains duplicate settings")
		}
		found = true
		var value sql.NullString
		if err := rows.Scan(&value); err != nil {
			return 0, fmt.Errorf("scan legacy traffic reset method: %w", err)
		}
		if !value.Valid {
			// admin_setting(..., 1) uses PHP's null-coalescing fallback when a
			// legacy row exists with a NULL value, so NULL and a missing row are
			// both the runtime default rather than method zero.
			method = 1
			continue
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(value.String))
		if err != nil || parsed < 0 || parsed > 4 {
			return 0, errors.New("legacy traffic reset method must be an integer between 0 and 4")
		}
		method = parsed
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate legacy traffic reset method: %w", err)
	}
	return method, nil
}

func readLegacyPlans(ctx context.Context, database *sql.DB) ([]store.LegacyPlan, error) {
	query := `
		SELECT id, group_id, transfer_enable, name, speed_limit, show, COALESCE(sort, 0), renew,
		       COALESCE(content, ''), reset_traffic_method, capacity_limit,
		       ` + legacyUnixExpression("created_at") + `, ` + legacyUnixExpression("updated_at") + `,
		       COALESCE(prices, '{}'), sell, device_limit, COALESCE(tags, '[]')
		FROM v2_plan ORDER BY id
	`
	rows, err := database.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read legacy plans: %w", err)
	}
	defer rows.Close()
	result := make([]store.LegacyPlan, 0)
	for rows.Next() {
		if len(result) >= maxLegacyPlanRows {
			return nil, fmt.Errorf("legacy plans exceed the %d-row migration limit", maxLegacyPlanRows)
		}
		var plan store.LegacyPlan
		var groupID, speedLimit, resetMethod, capacityLimit, deviceLimit sql.NullInt64
		var visible, renewable, sellable int64
		var pricesJSON, tagsJSON string
		if err := rows.Scan(&plan.ID, &groupID, &plan.TransferEnableGiB, &plan.Name, &speedLimit, &visible,
			&plan.SortPosition, &renewable, &plan.Content, &resetMethod, &capacityLimit, &plan.CreatedAt,
			&plan.UpdatedAt, &pricesJSON, &sellable, &deviceLimit, &tagsJSON); err != nil {
			return nil, fmt.Errorf("scan legacy plan: %w", err)
		}
		if visible < 0 || visible > 1 || renewable < 0 || renewable > 1 || sellable < 0 || sellable > 1 {
			return nil, fmt.Errorf("legacy plan id %d has an invalid boolean value", plan.ID)
		}
		plan.GroupID = legacyNullableInt64(groupID)
		plan.SpeedLimit = legacyNullableInt64(speedLimit)
		plan.ResetTrafficMethod = legacyNullableInt64(resetMethod)
		plan.CapacityLimit = legacyNullableInt64(capacityLimit)
		plan.DeviceLimit = legacyNullableInt64(deviceLimit)
		plan.Show = visible == 1
		plan.Renew = renewable == 1
		plan.Sell = sellable == 1
		plan.Prices, err = decodeLegacyPlanPrices(pricesJSON)
		if err != nil {
			return nil, fmt.Errorf("decode legacy plan id %d prices: %w", plan.ID, err)
		}
		plan.Tags, err = decodeLegacyPlanTags(tagsJSON)
		if err != nil {
			return nil, fmt.Errorf("decode legacy plan id %d tags: %w", plan.ID, err)
		}
		result = append(result, plan)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy plans: %w", err)
	}
	if err := store.ValidateLegacyPlansData(result); err != nil {
		return nil, fmt.Errorf("validate legacy plans: %w", err)
	}
	return result, nil
}

func decodeLegacyPlanPrices(encoded string) (store.PlanPrices, error) {
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.UseNumber()
	var values map[string]json.Number
	if err := decoder.Decode(&values); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing JSON data")
	}
	result := make(store.PlanPrices, len(values))
	for period, number := range values {
		if _, valid := legacyPlanPricePeriods[period]; !valid {
			return nil, fmt.Errorf("unknown price period %q", period)
		}
		rational, ok := new(big.Rat).SetString(number.String())
		if !ok || rational.Sign() < 0 {
			return nil, fmt.Errorf("price %q must be a non-negative decimal", period)
		}
		rational.Mul(rational, big.NewRat(100, 1))
		if !rational.IsInt() || !rational.Num().IsInt64() {
			return nil, fmt.Errorf("price %q must have at most two decimal places", period)
		}
		cents := rational.Num().Int64()
		if cents > 0 {
			result[period] = cents
		}
	}
	return result, nil
}

func decodeLegacyPlanTags(encoded string) ([]string, error) {
	decoder := json.NewDecoder(strings.NewReader(encoded))
	var result []string
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing JSON data")
	}
	if result == nil {
		return []string{}, nil
	}
	return result, nil
}

func legacyNullableInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}
