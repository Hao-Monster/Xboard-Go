package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const distributorTokenAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func (s *Store) CreateDistributorOrder(ctx context.Context, input CreateDistributorOrderInput, now time.Time) (DistributorOrder, error) {
	if input.DistributorUserID < 1 || input.PlanID < 1 || now.Unix() < 0 {
		return DistributorOrder{}, ErrInvalidInput
	}
	period, valid := normalizeOrderPeriod(input.Period)
	if !valid || period == "reset_traffic" {
		return DistributorOrder{}, fmt.Errorf("%w: invalid distributor order period", ErrInvalidInput)
	}
	customerName, err := normalizeOptionalDistributorText(input.CustomerName, 64)
	if err != nil {
		return DistributorOrder{}, err
	}
	tradeNo, err := newOrderTradeNo(now)
	if err != nil {
		return DistributorOrder{}, err
	}
	subscriptionToken, err := newSubscriptionToken()
	if err != nil {
		return DistributorOrder{}, err
	}
	claimToken, err := randomDistributorToken(64)
	if err != nil {
		return DistributorOrder{}, err
	}
	passwordMaterial := make([]byte, 32)
	if _, err := rand.Read(passwordMaterial); err != nil {
		return DistributorOrder{}, fmt.Errorf("generate internal subscription credential: %w", err)
	}
	claimHash := sha256.Sum256([]byte(claimToken))

	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DistributorOrder{}, fmt.Errorf("begin distributor order: %w", err)
	}
	defer tx.Rollback()
	if err := requireAvailableDistributor(ctx, tx, input.DistributorUserID); err != nil {
		return DistributorOrder{}, err
	}
	plan, err := getPlan(ctx, tx, input.PlanID, now)
	if err != nil {
		return DistributorOrder{}, err
	}
	price, exists := plan.Prices[period]
	if !exists || price <= 0 || !plan.Show || !plan.Sell {
		return DistributorOrder{}, ErrPlanUnavailable
	}
	if plan.CapacityLimit != nil && *plan.CapacityLimit > 0 {
		var active int64
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM users
			WHERE plan_id = ? AND (expired_at IS NULL OR expired_at >= ?)
		`, plan.ID, now.Unix()).Scan(&active); err != nil {
			return DistributorOrder{}, fmt.Errorf("count distributor plan capacity: %w", err)
		}
		if active >= int64(*plan.CapacityLimit) {
			return DistributorOrder{}, ErrPlanUnavailable
		}
	}

	var expiresAt *time.Time
	if period != "onetime" {
		months := orderPeriodMonths[period]
		value := addOrderMonths(now, months).UTC()
		expiresAt = &value
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO orders (
			user_id,plan_id,period,trade_no,original_amount,total_amount,balance_amount,surplus_credit,
			surplus_amount,type,status,surplus_order_ids_json,commission_status,commission_balance,
			discount_amount,callback_no,created_at,updated_at
		) VALUES (?, ?, ?, ?, ?, ?, 0, 0, 0, ?, ?, '[]', 0, 0, 0, 'distributor_auto', ?, ?)
	`, input.DistributorUserID, plan.ID, period, tradeNo, price, price, OrderTypeNew, OrderStatusCompleted, now.Unix(), now.Unix())
	if err != nil {
		return DistributorOrder{}, fmt.Errorf("create distributor financial order: %w", err)
	}
	orderID, err := result.LastInsertId()
	if err != nil {
		return DistributorOrder{}, fmt.Errorf("read distributor financial order id: %w", err)
	}

	systemResetMethod, err := readSystemTrafficResetMethod(ctx, tx)
	if err != nil {
		return DistributorOrder{}, err
	}
	nextReset := CalculateNextTrafficReset(plan.ResetTrafficMethod, systemResetMethod, expiresAt, now)
	result, err = tx.ExecContext(ctx, `
		INSERT INTO users (
			email,password_hash,is_admin,is_staff,is_distributor,distributor_name,banned,account_kind,
			uuid,group_id,plan_id,transfer_enable,traffic_u,traffic_d,expired_at,next_reset_at,
			speed_limit,device_limit,subscription_token,created_at,updated_at
		) VALUES (?, ?, 0, 0, 0, NULL, 0, ?, ?, ?, ?, ?, 0, 0, ?, ?, ?, ?, ?, ?, ?)
	`, "dist-"+tradeNo+"@internal.invalid", "!internal:"+hex.EncodeToString(passwordMaterial),
		AccountKindInternalSubscription, uuid.NewString(), nullableInt64Value(plan.GroupID), plan.ID,
		plan.TransferEnableGiB*bytesPerGiB, nullableTimeUnix(expiresAt), nullableTimeUnix(nextReset),
		optionalPlanInt(plan.SpeedLimit), optionalPlanInt(plan.DeviceLimit), subscriptionToken, now.Unix(), now.Unix())
	if err != nil {
		return DistributorOrder{}, fmt.Errorf("create distributor internal subscription: %w", err)
	}
	subscriberID, err := result.LastInsertId()
	if err != nil {
		return DistributorOrder{}, fmt.Errorf("read distributor subscriber id: %w", err)
	}
	result, err = tx.ExecContext(ctx, `
		INSERT INTO distributor_subscriptions (
			original_order_id,distributor_user_id,subscriber_user_id,customer_name,claim_token_hash,
			delivery_status,settlement_status,hwid_enabled,hwid_limit,created_at,updated_at
		) VALUES (?, ?, ?, ?, ?, 0, 0, 1, 1, ?, ?)
	`, orderID, input.DistributorUserID, subscriberID, nullableStringValue(customerName), hex.EncodeToString(claimHash[:]), now.Unix(), now.Unix())
	if err != nil {
		return DistributorOrder{}, fmt.Errorf("create distributor subscription: %w", err)
	}
	subscriptionID, err := result.LastInsertId()
	if err != nil {
		return DistributorOrder{}, fmt.Errorf("read distributor subscription id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE orders
		SET distributor_order_id = ?, entitlement_expired_at_after = ?, updated_at = ?
		WHERE id = ? AND user_id = ?
	`, subscriptionID, nullableTimeUnix(expiresAt), now.Unix(), orderID, input.DistributorUserID); err != nil {
		return DistributorOrder{}, fmt.Errorf("link distributor financial order: %w", err)
	}
	created, err := readDistributorOrder(ctx, tx, input.DistributorUserID, orderID)
	if err != nil {
		return DistributorOrder{}, err
	}
	created.Subscription.ClaimToken = claimToken
	if err := tx.Commit(); err != nil {
		return DistributorOrder{}, fmt.Errorf("commit distributor order: %w", err)
	}
	return created, nil
}

func (s *Store) RenewDistributorOrder(ctx context.Context, input RenewDistributorOrderInput, now time.Time) (DistributorOrder, error) {
	period, valid := normalizeOrderPeriod(input.Period)
	if input.DistributorUserID < 1 || !validTradeNo(input.TradeNo) || !valid || period == "onetime" || period == "reset_traffic" ||
		uuid.Validate(strings.TrimSpace(input.IdempotencyKey)) != nil || now.Unix() < 0 {
		return DistributorOrder{}, ErrInvalidInput
	}
	input.IdempotencyKey = strings.ToLower(strings.TrimSpace(input.IdempotencyKey))
	tradeNo, err := newOrderTradeNo(now)
	if err != nil {
		return DistributorOrder{}, err
	}

	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DistributorOrder{}, fmt.Errorf("begin distributor renewal: %w", err)
	}
	defer tx.Rollback()
	if err := requireAvailableDistributor(ctx, tx, input.DistributorUserID); err != nil {
		return DistributorOrder{}, err
	}
	var subscriptionID int64
	if err := tx.QueryRowContext(ctx, `
		SELECT distributor_order_id FROM orders
		WHERE user_id = ? AND trade_no = ? AND distributor_order_id IS NOT NULL
	`, input.DistributorUserID, input.TradeNo).Scan(&subscriptionID); errors.Is(err, sql.ErrNoRows) {
		return DistributorOrder{}, ErrNotFound
	} else if err != nil {
		return DistributorOrder{}, fmt.Errorf("find distributor subscription for renewal: %w", err)
	}

	var existingOrderID, existingSubscriptionID int64
	var existingPeriod string
	err = tx.QueryRowContext(ctx, `
		SELECT id,distributor_order_id,period FROM orders
		WHERE user_id = ? AND distributor_idempotency_key = ?
	`, input.DistributorUserID, input.IdempotencyKey).Scan(&existingOrderID, &existingSubscriptionID, &existingPeriod)
	if err == nil {
		if existingSubscriptionID != subscriptionID || existingPeriod != period {
			return DistributorOrder{}, ErrDistributorRenewalMismatch
		}
		return readDistributorOrder(ctx, tx, input.DistributorUserID, existingOrderID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DistributorOrder{}, fmt.Errorf("check distributor renewal idempotency: %w", err)
	}

	var deliveryStatus DistributorDeliveryStatus
	var subscriberID, planID int64
	var subscriberBanned bool
	var expiredAt int64
	if err := tx.QueryRowContext(ctx, `
		SELECT ds.delivery_status,u.id,u.plan_id,u.expired_at,u.banned
		FROM distributor_subscriptions ds
		JOIN users u ON u.id = ds.subscriber_user_id
		WHERE ds.id = ? AND ds.distributor_user_id = ? AND u.account_kind = ?
	`, subscriptionID, input.DistributorUserID, AccountKindInternalSubscription).Scan(
		&deliveryStatus, &subscriberID, &planID, &expiredAt, &subscriberBanned,
	); errors.Is(err, sql.ErrNoRows) {
		return DistributorOrder{}, ErrNotFound
	} else if err != nil {
		return DistributorOrder{}, fmt.Errorf("read distributor renewal subscription: %w", err)
	}
	if deliveryStatus == DistributorDeliveryClosed {
		return DistributorOrder{}, ErrDistributorSubscriptionClosed
	}
	if subscriberBanned {
		return DistributorOrder{}, ErrDistributorRenewalUnavailable
	}
	plan, err := getPlan(ctx, tx, planID, now)
	if err != nil {
		return DistributorOrder{}, err
	}
	price, exists := plan.Prices[period]
	if !exists || price <= 0 || !plan.Renew || !plan.Show && expiredAt <= now.Unix() {
		return DistributorOrder{}, ErrDistributorRenewalUnavailable
	}
	months := orderPeriodMonths[period]
	base := time.Unix(expiredAt, 0)
	wasExpired := expiredAt <= now.Unix()
	if wasExpired {
		base = now
	}
	after := addDistributorMonthsNoOverflow(base, months).UTC()
	before := time.Unix(expiredAt, 0).UTC()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO orders (
			user_id,plan_id,period,trade_no,original_amount,total_amount,balance_amount,surplus_credit,
			surplus_amount,type,status,surplus_order_ids_json,commission_status,commission_balance,
			discount_amount,callback_no,distributor_order_id,entitlement_expired_at_before,
			entitlement_expired_at_after,distributor_idempotency_key,created_at,updated_at
		) VALUES (?, ?, ?, ?, ?, ?, 0, 0, 0, ?, ?, '[]', 0, 0, 0, 'distributor_auto', ?, ?, ?, ?, ?, ?)
	`, input.DistributorUserID, plan.ID, period, tradeNo, price, price, OrderTypeRenewal, OrderStatusCompleted,
		subscriptionID, before.Unix(), after.Unix(), input.IdempotencyKey, now.Unix(), now.Unix())
	if err != nil {
		return DistributorOrder{}, fmt.Errorf("create distributor renewal order: %w", err)
	}
	orderID, err := result.LastInsertId()
	if err != nil {
		return DistributorOrder{}, fmt.Errorf("read distributor renewal order id: %w", err)
	}
	if wasExpired {
		systemMethod, err := readSystemTrafficResetMethod(ctx, tx)
		if err != nil {
			return DistributorOrder{}, err
		}
		nextReset := CalculateNextTrafficReset(plan.ResetTrafficMethod, systemMethod, &after, now)
		if _, err := tx.ExecContext(ctx, `
			UPDATE users SET expired_at = ?,traffic_u = 0,traffic_d = 0,last_reset_at = ?,next_reset_at = ?,
				reset_count = reset_count + 1,admin_revision = admin_revision + 1,updated_at = ?
			WHERE id = ? AND account_kind = ? AND banned = 0
		`, after.Unix(), now.Unix(), nullableTimeUnix(nextReset), now.Unix(), subscriberID, AccountKindInternalSubscription); err != nil {
			return DistributorOrder{}, fmt.Errorf("renew expired distributor entitlement: %w", err)
		}
	} else if _, err := tx.ExecContext(ctx, `
		UPDATE users SET expired_at = ?,admin_revision = admin_revision + 1,updated_at = ?
		WHERE id = ? AND account_kind = ? AND banned = 0
	`, after.Unix(), now.Unix(), subscriberID, AccountKindInternalSubscription); err != nil {
		return DistributorOrder{}, fmt.Errorf("renew distributor entitlement: %w", err)
	}
	renewed, err := readDistributorOrder(ctx, tx, input.DistributorUserID, orderID)
	if err != nil {
		return DistributorOrder{}, err
	}
	if err := tx.Commit(); err != nil {
		return DistributorOrder{}, fmt.Errorf("commit distributor renewal: %w", err)
	}
	return renewed, nil
}

func requireAvailableDistributor(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID int64) error {
	var available bool
	if err := query.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM users
			WHERE id = ? AND account_kind = 'human' AND is_distributor = 1 AND banned = 0
		)
	`, userID).Scan(&available); err != nil {
		return fmt.Errorf("validate distributor account: %w", err)
	}
	if !available {
		return ErrDistributorUnavailable
	}
	return nil
}

func readDistributorOrder(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, distributorID, orderID int64) (DistributorOrder, error) {
	order, err := scanOrder(query.QueryRowContext(ctx, orderSelect+`
		WHERE o.id = ? AND o.user_id = ? AND o.distributor_order_id IS NOT NULL
	`, orderID, distributorID))
	if err != nil {
		return DistributorOrder{}, err
	}
	var value DistributorOrder
	value.Order = order
	var customerName, remark, connectedNodeName sql.NullString
	var configIssuedAt, connectedAt, connectedNodeID, claimedAt, closedAt sql.NullInt64
	var createdAt, updatedAt int64
	if err := query.QueryRowContext(ctx, `
		SELECT ds.id,ds.original_order_id,original.trade_no,ds.distributor_user_id,ds.subscriber_user_id,
		       ds.customer_name,ds.remark,ds.delivery_status,ds.settlement_status,ds.config_issued_at,
		       ds.connected_at,ds.connected_node_id,ds.connected_node_name,ds.claimed_at,ds.closed_at,
		       ds.hwid_enabled,ds.hwid_limit,ds.revision,ds.created_at,ds.updated_at,
		       subscriber.subscription_token,subscriber.uuid,p.name
		FROM distributor_subscriptions ds
		JOIN orders original ON original.id = ds.original_order_id
		JOIN users subscriber ON subscriber.id = ds.subscriber_user_id
		JOIN plans p ON p.id = original.plan_id
		WHERE ds.id = ? AND ds.distributor_user_id = ?
	`, *order.DistributorOrderID, distributorID).Scan(
		&value.Subscription.ID, &value.Subscription.OriginalOrderID, &value.Subscription.OriginalTradeNo,
		&value.Subscription.DistributorUserID, &value.Subscription.SubscriberUserID, &customerName, &remark,
		&value.Subscription.DeliveryStatus, &value.Subscription.SettlementStatus, &configIssuedAt, &connectedAt,
		&connectedNodeID, &connectedNodeName, &claimedAt, &closedAt, &value.Subscription.HWIDEnabled,
		&value.Subscription.HWIDLimit, &value.Subscription.Revision, &createdAt, &updatedAt,
		&value.Subscription.SubscriptionToken, &value.Subscription.SubscriberUUID, &value.PlanName,
	); errors.Is(err, sql.ErrNoRows) {
		return DistributorOrder{}, ErrNotFound
	} else if err != nil {
		return DistributorOrder{}, fmt.Errorf("read distributor order: %w", err)
	}
	value.Subscription.CustomerName = nullableStringPointer(customerName)
	value.Subscription.Remark = nullableStringPointer(remark)
	value.Subscription.ConfigIssuedAt = nullableUnixTime(configIssuedAt)
	value.Subscription.ConnectedAt = nullableUnixTime(connectedAt)
	value.Subscription.ConnectedNodeID = nullableInt64Pointer(connectedNodeID)
	value.Subscription.ConnectedNodeName = nullableStringPointer(connectedNodeName)
	value.Subscription.ClaimedAt = nullableUnixTime(claimedAt)
	value.Subscription.ClosedAt = nullableUnixTime(closedAt)
	value.Subscription.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.Subscription.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	if order.PaidAt != nil {
		value.SettlementStatus = DistributorSettlementSettled
	}
	return value, nil
}

func normalizeOptionalDistributorText(value *string, maximum int) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized := strings.TrimSpace(*value)
	if normalized == "" {
		return nil, nil
	}
	if !utf8.ValidString(normalized) || utf8.RuneCountInString(normalized) > maximum {
		return nil, fmt.Errorf("%w: distributor text exceeds %d characters", ErrInvalidInput, maximum)
	}
	return &normalized, nil
}

func randomDistributorToken(length int) (string, error) {
	if length < 1 {
		return "", ErrInvalidInput
	}
	result := make([]byte, length)
	maximum := big.NewInt(int64(len(distributorTokenAlphabet)))
	for index := range result {
		value, err := rand.Int(rand.Reader, maximum)
		if err != nil {
			return "", fmt.Errorf("generate distributor token: %w", err)
		}
		result[index] = distributorTokenAlphabet[value.Int64()]
	}
	return string(result), nil
}

func addDistributorMonthsNoOverflow(value time.Time, months int) time.Time {
	location, err := time.LoadLocation(trafficResetLocationID)
	if err != nil {
		location = value.Location()
	}
	local := value.In(location)
	targetFirst := time.Date(local.Year(), local.Month()+time.Month(months), 1, local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), location)
	lastDay := time.Date(targetFirst.Year(), targetFirst.Month()+1, 0, local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), location).Day()
	day := local.Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(targetFirst.Year(), targetFirst.Month(), day, local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), location)
}
