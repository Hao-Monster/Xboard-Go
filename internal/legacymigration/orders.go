package legacymigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	maxLegacyOrderRows      = 1_000_000
	maxLegacyOrderDataBytes = int64(512 << 20)
)

var legacyOrderPeriodNames = map[string]string{
	"month_price": "monthly", "quarter_price": "quarterly", "half_year_price": "half_yearly",
	"year_price": "yearly", "two_year_price": "two_yearly", "three_year_price": "three_yearly",
	"onetime_price": "onetime", "reset_price": "reset_traffic",
}

type OrdersSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Orders   []store.LegacyOrder
	Checksum string
}

func ReadOrdersSnapshot(ctx context.Context, sourcePath string) (OrdersSnapshot, error) {
	orders := []store.LegacyOrder{}
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_order", []string{
			"id", "invite_user_id", "user_id", "plan_id", "coupon_id", "payment_id", "type", "period", "trade_no",
			"callback_no", "total_amount", "handling_amount", "discount_amount", "surplus_amount", "surplus_credit",
			"balance_amount", "surplus_order_ids", "status", "commission_status", "commission_balance",
			"actual_commission_balance", "paid_at", "created_at", "updated_at", "distributor_order_id",
			"entitlement_expired_at_before", "entitlement_expired_at_after", "distributor_idempotency_key", "distributor_settled_by",
		}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(
				length(CAST(trade_no AS BLOB)) + length(CAST(period AS BLOB)) +
				COALESCE(length(CAST(callback_no AS BLOB)), 0) +
				COALESCE(length(CAST(surplus_order_ids AS BLOB)), 0) +
				COALESCE(length(CAST(distributor_idempotency_key AS BLOB)), 0)
			), 0) FROM v2_order
		`, maxLegacyOrderRows, maxLegacyOrderDataBytes, "legacy orders"); err != nil {
			return err
		}
		var readBytes int64
		var readErr error
		orders, readBytes, readErr = readLegacyOrders(ctx, database)
		if readErr != nil {
			return readErr
		}
		if readBytes > maxLegacyOrderDataBytes {
			return errors.New("legacy orders exceed the migration data limit")
		}
		return nil
	})
	if err != nil {
		return OrdersSnapshot{}, err
	}
	return OrdersSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Orders: orders, Checksum: store.LegacyOrdersChecksum(orders),
	}, nil
}

func readLegacyOrders(ctx context.Context, database *sql.DB) ([]store.LegacyOrder, int64, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, user_id, plan_id, payment_id, period, trade_no, total_amount, handling_amount,
		       COALESCE(balance_amount, 0), COALESCE(surplus_credit, 0), COALESCE(surplus_amount, 0),
		       type, status, COALESCE(surplus_order_ids, '[]'), coupon_id, commission_status,
		       invite_user_id, actual_commission_balance, COALESCE(commission_balance, 0), COALESCE(discount_amount, 0),
		       paid_at, callback_no, distributor_order_id, entitlement_expired_at_before, entitlement_expired_at_after,
		       distributor_idempotency_key, distributor_settled_by,
		       `+legacyUnixExpression("created_at")+`, `+legacyUnixExpression("updated_at")+`
		FROM v2_order ORDER BY id
	`)
	if err != nil {
		return nil, 0, fmt.Errorf("read legacy orders: %w", err)
	}
	defer rows.Close()
	orders := make([]store.LegacyOrder, 0)
	var bytesRead int64
	for rows.Next() {
		if len(orders) >= maxLegacyOrderRows {
			return nil, 0, fmt.Errorf("legacy orders exceed the %d-row migration limit", maxLegacyOrderRows)
		}
		var order store.LegacyOrder
		var paymentID, handling, couponID, inviterID, actualCommission, paidAt sql.NullInt64
		var distributorOrderID, before, after, settledBy sql.NullInt64
		var commissionStatus sql.NullInt64
		var callback, idempotency sql.NullString
		var surplusJSON string
		if err := rows.Scan(&order.ID, &order.UserID, &order.PlanID, &paymentID, &order.Period, &order.TradeNo,
			&order.TotalAmount, &handling, &order.BalanceAmount, &order.SurplusCredit, &order.SurplusAmount,
			&order.Type, &order.Status, &surplusJSON, &couponID, &commissionStatus, &inviterID, &actualCommission,
			&order.CommissionBalance, &order.DiscountAmount, &paidAt, &callback, &distributorOrderID, &before, &after,
			&idempotency, &settledBy, &order.CreatedAt, &order.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan legacy order: %w", err)
		}
		if converted, exists := legacyOrderPeriodNames[order.Period]; exists {
			order.Period = converted
		}
		if err := decodeLegacyOrderIDs(surplusJSON, &order.SurplusOrderIDs); err != nil {
			return nil, 0, fmt.Errorf("decode legacy order id %d surplus references: %w", order.ID, err)
		}
		original, ok := store.LegacyOrderOriginalAmount(order.TotalAmount, order.BalanceAmount, order.DiscountAmount, order.SurplusAmount, order.SurplusCredit)
		if !ok {
			return nil, 0, fmt.Errorf("legacy order id %d has invalid financial amounts", order.ID)
		}
		order.OriginalAmount = original
		order.PaymentID = legacyPositivePointer(paymentID)
		order.HandlingAmount = legacyNullablePointer(handling)
		order.CouponID = legacyPositivePointer(couponID)
		order.CommissionStatus = legacyNullableIntPointer(commissionStatus)
		order.InviteUserID = legacyPositivePointer(inviterID)
		order.ActualCommissionBalance = legacyNullablePointer(actualCommission)
		order.PaidAt = legacyNullablePointer(paidAt)
		order.CallbackNo = legacyNullableStringPointer(callback)
		order.DistributorOrderID = legacyPositivePointer(distributorOrderID)
		order.EntitlementExpiredAtBefore = legacyNullablePointer(before)
		order.EntitlementExpiredAtAfter = legacyNullablePointer(after)
		order.DistributorIdempotencyKey = legacyNullableStringPointer(idempotency)
		order.DistributorSettledBy = legacyPositivePointer(settledBy)
		bytesRead += int64(len(order.TradeNo) + len(order.Period) + len(surplusJSON) + len(callback.String) + len(idempotency.String))
		if bytesRead > maxLegacyOrderDataBytes {
			return nil, 0, errors.New("legacy orders exceed the migration data limit")
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate legacy orders: %w", err)
	}
	if err := store.ValidateLegacyOrdersData(orders); err != nil {
		return nil, 0, fmt.Errorf("validate legacy orders: %w", err)
	}
	return orders, bytesRead, nil
}

func decodeLegacyOrderIDs(encoded string, target *[]int64) error {
	decoder := json.NewDecoder(strings.NewReader(encoded))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON data")
	}
	if *target == nil {
		*target = []int64{}
	}
	return nil
}

func legacyPositivePointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func legacyNullablePointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func legacyNullableIntPointer(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int64)
	return &result
}

func legacyNullableStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}
