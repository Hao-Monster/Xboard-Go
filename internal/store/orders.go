package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	orderTimeout       = 2 * time.Hour
	maxOrderBatchSize  = 1_000
	maxOrderMoneyCents = int64(9_000_000_000_000_000)
)

var (
	legacyOrderPeriods = map[string]string{
		"month_price": "monthly", "quarter_price": "quarterly", "half_year_price": "half_yearly",
		"year_price": "yearly", "two_year_price": "two_yearly", "three_year_price": "three_yearly",
		"onetime_price": "onetime", "reset_price": "reset_traffic",
	}
	orderPeriodMonths = map[string]int{
		"monthly": 1, "quarterly": 3, "half_yearly": 6, "yearly": 12,
		"two_yearly": 24, "three_yearly": 36,
	}
)

type orderSettings struct {
	planChange              bool
	surplus                 bool
	newOrderEvent           int
	renewOrderEvent         int
	changeOrderEvent        int
	commissionFirstTime     bool
	inviteCommissionPercent int
}

type orderUserState struct {
	id                int64
	planID            sql.NullInt64
	expiredAt         sql.NullInt64
	balance           int64
	discount          sql.NullInt64
	inviteUserID      sql.NullInt64
	transferEnable    int64
	trafficUpload     int64
	trafficDownload   int64
	speedLimit        int
	deviceLimit       int
	groupID           sql.NullInt64
	nextResetAt       sql.NullInt64
	lastResetAt       sql.NullInt64
	resetCount        int64
	banned            bool
	accountKind       string
	commissionType    int
	commissionRate    sql.NullInt64
	commissionBalance int64
}

func (s *Store) CreateOrder(ctx context.Context, input CreateOrderInput, now time.Time) (Order, error) {
	if input.UserID < 1 || input.PlanID < 1 || now.Unix() < 0 {
		return Order{}, ErrInvalidInput
	}
	period, valid := normalizeOrderPeriod(input.Period)
	if !valid {
		return Order{}, fmt.Errorf("%w: invalid order period", ErrInvalidInput)
	}
	tradeNo, err := newOrderTradeNo(now)
	if err != nil {
		return Order{}, err
	}

	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Order{}, fmt.Errorf("begin create order: %w", err)
	}
	defer tx.Rollback()

	user, err := readOrderUser(ctx, tx, input.UserID)
	if err != nil {
		return Order{}, err
	}
	if user.banned || user.accountKind != AccountKindHuman {
		return Order{}, ErrPlanUnavailable
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM orders WHERE user_id = ? AND status IN (0, 1))`, input.UserID).Scan(&active); err != nil {
		return Order{}, fmt.Errorf("check active order: %w", err)
	}
	if active {
		return Order{}, ErrActiveOrderExists
	}
	plan, err := getPlan(ctx, tx, input.PlanID, now)
	if err != nil {
		return Order{}, err
	}
	price, exists := plan.Prices[period]
	if !exists {
		return Order{}, fmt.Errorf("%w: unavailable order period", ErrPlanUnavailable)
	}
	if err := validateOrderPurchase(user, plan, period, now); err != nil {
		return Order{}, err
	}
	settings, err := readOrderSettings(ctx, tx)
	if err != nil {
		return Order{}, err
	}

	commissionStatus := 0
	order := Order{
		UserID: input.UserID, PlanID: input.PlanID, Period: period, TradeNo: tradeNo,
		OriginalAmount: price, TotalAmount: price, Status: OrderStatusPending, SurplusOrderIDs: []int64{}, CommissionStatus: &commissionStatus,
		CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	var appliedCoupon *Coupon
	couponCode := strings.TrimSpace(input.CouponCode)
	if couponCode != "" {
		coupon, couponErr := validateCoupon(ctx, tx, user.id, plan.ID, period, couponCode, now)
		if couponErr != nil {
			return Order{}, couponErr
		}
		order.CouponID = &coupon.ID
		order.DiscountAmount = couponDiscount(order.OriginalAmount, coupon)
		appliedCoupon = &coupon
	}
	if user.discount.Valid && user.discount.Int64 > 0 {
		order.DiscountAmount += percentageCents(order.OriginalAmount, user.discount.Int64)
	}
	if order.DiscountAmount > order.OriginalAmount {
		order.DiscountAmount = order.OriginalAmount
	}
	order.TotalAmount = order.OriginalAmount - order.DiscountAmount
	activeSubscription := !user.expiredAt.Valid || user.expiredAt.Int64 > now.Unix()
	switch {
	case period == "reset_traffic":
		order.Type = OrderTypeResetTraffic
	case user.planID.Valid && user.planID.Int64 != plan.ID && activeSubscription:
		if !settings.planChange {
			return Order{}, fmt.Errorf("%w: plan changes are disabled", ErrPlanUnavailable)
		}
		order.Type = OrderTypeUpgrade
		if settings.surplus {
			if err := calculateOrderSurplus(ctx, tx, user, &order, settings.changeOrderEvent == 1, now); err != nil {
				return Order{}, err
			}
		}
		if order.SurplusAmount >= order.TotalAmount {
			order.SurplusCredit = order.SurplusAmount - order.TotalAmount
			order.TotalAmount = 0
		} else {
			order.TotalAmount -= order.SurplusAmount
		}
	case user.planID.Valid && user.planID.Int64 == plan.ID && activeSubscription:
		order.Type = OrderTypeRenewal
	default:
		order.Type = OrderTypeNew
	}
	if err := setOrderCommission(ctx, tx, user, settings, &order); err != nil {
		return Order{}, err
	}
	if user.balance > 0 && order.TotalAmount > 0 {
		order.BalanceAmount = minInt64(user.balance, order.TotalAmount)
		order.TotalAmount -= order.BalanceAmount
		user.balance -= order.BalanceAmount
		if _, err := tx.ExecContext(ctx, `UPDATE users SET balance = ?, updated_at = ? WHERE id = ?`, user.balance, now.Unix(), user.id); err != nil {
			return Order{}, fmt.Errorf("deduct order balance: %w", err)
		}
	}
	if appliedCoupon != nil && appliedCoupon.LimitUse != nil {
		result, consumeErr := tx.ExecContext(ctx, `
			UPDATE coupons SET limit_use = limit_use - 1, updated_at = ?
			WHERE id = ? AND limit_use > 0
		`, now.Unix(), appliedCoupon.ID)
		if consumeErr != nil {
			return Order{}, fmt.Errorf("consume coupon: %w", consumeErr)
		}
		consumed, consumeErr := result.RowsAffected()
		if consumeErr != nil {
			return Order{}, fmt.Errorf("count consumed coupon: %w", consumeErr)
		}
		if consumed != 1 {
			return Order{}, ErrCouponExhausted
		}
	}
	surplusJSON, err := json.Marshal(order.SurplusOrderIDs)
	if err != nil {
		return Order{}, fmt.Errorf("encode order surplus IDs: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO orders (
			user_id, plan_id, period, trade_no, original_amount, total_amount, balance_amount,
			surplus_credit, surplus_amount, type, status, surplus_order_ids_json, invite_user_id,
			commission_rate, commission_balance, discount_amount, coupon_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?)
	`, order.UserID, order.PlanID, order.Period, order.TradeNo, order.OriginalAmount, order.TotalAmount,
		order.BalanceAmount, order.SurplusCredit, order.SurplusAmount, order.Type, string(surplusJSON),
		order.InviteUserID, order.CommissionRate, order.CommissionBalance, order.DiscountAmount, order.CouponID, now.Unix(), now.Unix())
	if err != nil {
		var exists bool
		if checkErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM orders WHERE user_id = ? AND status IN (0, 1))`, input.UserID).Scan(&exists); checkErr == nil && exists {
			return Order{}, ErrActiveOrderExists
		}
		return Order{}, fmt.Errorf("create order: %w", err)
	}
	order.ID, err = result.LastInsertId()
	if err != nil {
		return Order{}, fmt.Errorf("read order ID: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Order{}, fmt.Errorf("commit create order: %w", err)
	}
	return order, nil
}

func (s *Store) GetUserOrder(ctx context.Context, userID int64, tradeNo string) (Order, error) {
	if userID < 1 || !validTradeNo(tradeNo) {
		return Order{}, ErrInvalidInput
	}
	order, err := scanOrder(s.db.QueryRowContext(ctx, orderSelect+` WHERE o.user_id = ? AND o.trade_no = ?`, userID, tradeNo))
	if err != nil {
		return Order{}, err
	}
	plan, err := getPlanForOrder(ctx, s.db, order.PlanID)
	if err != nil {
		return Order{}, err
	}
	order.Plan = &plan
	return order, nil
}

func (s *Store) ListUserOrders(ctx context.Context, userID int64, status *OrderStatus, limit int) ([]Order, error) {
	if userID < 1 || limit < 1 || limit > 200 || status != nil && (*status < OrderStatusPending || *status > OrderStatusDiscounted) {
		return nil, ErrInvalidInput
	}
	arguments := []any{userID}
	where := ` WHERE o.user_id = ?`
	if status != nil {
		where += ` AND o.status = ?`
		arguments = append(arguments, *status)
	}
	arguments = append(arguments, limit)
	rows, err := s.db.QueryContext(ctx, orderSelect+where+` ORDER BY o.created_at DESC, o.id DESC LIMIT ?`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list user orders: %w", err)
	}
	defer rows.Close()
	orders := make([]Order, 0)
	for rows.Next() {
		order, scanErr := scanOrder(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user orders: %w", err)
	}
	if err := attachOrderPlans(ctx, s.db, orders); err != nil {
		return nil, err
	}
	return orders, nil
}

func (s *Store) GetAdminOrder(ctx context.Context, tradeNo string) (AdminOrder, error) {
	if !validTradeNo(tradeNo) {
		return AdminOrder{}, ErrInvalidInput
	}
	var result AdminOrder
	row := s.db.QueryRowContext(ctx, adminOrderSelect+` WHERE o.trade_no = ?`, tradeNo)
	order, err := scanOrderWithAdminFields(row, &result.UserEmail, &result.PlanName)
	if err != nil {
		return AdminOrder{}, err
	}
	result.Order = order
	result.Plan = &Plan{ID: order.PlanID, Name: result.PlanName}
	return result, nil
}

func (s *Store) GetAdminOrderByID(ctx context.Context, orderID int64) (AdminOrder, error) {
	if orderID < 1 {
		return AdminOrder{}, ErrInvalidInput
	}
	var result AdminOrder
	row := s.db.QueryRowContext(ctx, adminOrderSelect+` WHERE o.id = ?`, orderID)
	order, err := scanOrderWithAdminFields(row, &result.UserEmail, &result.PlanName)
	if err != nil {
		return AdminOrder{}, err
	}
	result.Order = order
	result.Plan = &Plan{ID: order.PlanID, Name: result.PlanName}
	return result, nil
}

func (s *Store) ListAdminOrders(ctx context.Context, filter AdminOrderFilter) (AdminOrderPage, error) {
	if filter.Page == 0 {
		filter.Page = 1
	}
	if filter.PageSize == 0 {
		filter.PageSize = 20
	}
	if filter.Page < 1 || filter.PageSize < 1 || filter.PageSize > 100 ||
		filter.Status != nil && (*filter.Status < OrderStatusPending || *filter.Status > OrderStatusDiscounted) ||
		filter.Type != nil && (*filter.Type < OrderTypeNew || *filter.Type > OrderTypeResetTraffic) || len(filter.Query) > 128 {
		return AdminOrderPage{}, ErrInvalidInput
	}
	filter.Query = strings.TrimSpace(filter.Query)
	filter.Period = strings.TrimSpace(filter.Period)
	where := ` WHERE 1 = 1`
	arguments := make([]any, 0, 8)
	if filter.Status != nil {
		where += ` AND o.status = ?`
		arguments = append(arguments, *filter.Status)
	}
	if filter.Type != nil {
		where += ` AND o.type = ?`
		arguments = append(arguments, *filter.Type)
	}
	if filter.Period != "" {
		period, valid := normalizeOrderPeriod(filter.Period)
		if !valid {
			return AdminOrderPage{}, ErrInvalidInput
		}
		where += ` AND o.period = ?`
		arguments = append(arguments, period)
	}
	if filter.Query != "" {
		where += ` AND (o.trade_no = ? OR u.email LIKE ? ESCAPE '\')`
		arguments = append(arguments, filter.Query, "%"+escapeOrderLike(filter.Query)+"%")
	}
	var page AdminOrderPage
	page.Page, page.PageSize = filter.Page, filter.PageSize
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM orders o JOIN users u ON u.id = o.user_id
	`+where, arguments...).Scan(&page.Total); err != nil {
		return AdminOrderPage{}, fmt.Errorf("count administrator orders: %w", err)
	}
	queryArguments := append(append([]any(nil), arguments...), filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := s.db.QueryContext(ctx, adminOrderSelect+where+` ORDER BY o.created_at DESC, o.id DESC LIMIT ? OFFSET ?`, queryArguments...)
	if err != nil {
		return AdminOrderPage{}, fmt.Errorf("list administrator orders: %w", err)
	}
	defer rows.Close()
	page.Items = make([]AdminOrder, 0, filter.PageSize)
	for rows.Next() {
		var item AdminOrder
		order, scanErr := scanOrderWithAdminFields(rows, &item.UserEmail, &item.PlanName)
		if scanErr != nil {
			return AdminOrderPage{}, scanErr
		}
		item.Order = order
		item.Plan = &Plan{ID: order.PlanID, Name: item.PlanName}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return AdminOrderPage{}, fmt.Errorf("iterate administrator orders: %w", err)
	}
	return page, nil
}

func (s *Store) AssignOrder(ctx context.Context, input AssignOrderInput, now time.Time) (Order, error) {
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	period, valid := normalizeOrderPeriod(input.Period)
	if input.Email == "" || input.PlanID < 1 || !valid || input.TotalAmount < 0 || input.TotalAmount > maxOrderMoneyCents {
		return Order{}, ErrInvalidInput
	}
	tradeNo, err := newAssignedOrderTradeNo()
	if err != nil {
		return Order{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Order{}, fmt.Errorf("begin assign order: %w", err)
	}
	defer tx.Rollback()
	var userID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ? AND account_kind = 'human'`, input.Email).Scan(&userID); errors.Is(err, sql.ErrNoRows) {
		return Order{}, ErrNotFound
	} else if err != nil {
		return Order{}, fmt.Errorf("find assigned order user: %w", err)
	}
	user, err := readOrderUser(ctx, tx, userID)
	if err != nil {
		return Order{}, err
	}
	if _, err := getPlanForOrder(ctx, tx, input.PlanID); err != nil {
		return Order{}, err
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM orders WHERE user_id = ? AND status IN (0, 1))`, userID).Scan(&active); err != nil {
		return Order{}, fmt.Errorf("check assigned active order: %w", err)
	}
	if active {
		return Order{}, ErrActiveOrderExists
	}
	commissionStatus := 0
	order := Order{
		UserID: userID, PlanID: input.PlanID, Period: period, TradeNo: tradeNo,
		OriginalAmount: input.TotalAmount, TotalAmount: input.TotalAmount, Status: OrderStatusPending,
		SurplusOrderIDs: []int64{}, CommissionStatus: &commissionStatus, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	switch {
	case period == "reset_traffic":
		order.Type = OrderTypeResetTraffic
	case user.planID.Valid && user.planID.Int64 != input.PlanID:
		order.Type = OrderTypeUpgrade
	case user.planID.Valid && user.planID.Int64 == input.PlanID && user.expiredAt.Valid && user.expiredAt.Int64 > now.Unix():
		order.Type = OrderTypeRenewal
	default:
		order.Type = OrderTypeNew
	}
	settings, err := readOrderSettings(ctx, tx)
	if err != nil {
		return Order{}, err
	}
	if err := setOrderCommission(ctx, tx, user, settings, &order); err != nil {
		return Order{}, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO orders (
			user_id, plan_id, period, trade_no, original_amount, total_amount, type, status,
			invite_user_id, commission_rate, commission_balance, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?)
	`, order.UserID, order.PlanID, order.Period, order.TradeNo, order.OriginalAmount, order.TotalAmount,
		order.Type, order.InviteUserID, order.CommissionRate, order.CommissionBalance, now.Unix(), now.Unix())
	if err != nil {
		var exists bool
		if checkErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM orders WHERE user_id = ? AND status IN (0, 1))`, userID).Scan(&exists); checkErr == nil && exists {
			return Order{}, ErrActiveOrderExists
		}
		return Order{}, fmt.Errorf("assign order: %w", err)
	}
	order.ID, err = result.LastInsertId()
	if err != nil {
		return Order{}, fmt.Errorf("read assigned order ID: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Order{}, fmt.Errorf("commit assigned order: %w", err)
	}
	return order, nil
}

func (s *Store) CancelAdminOrder(ctx context.Context, tradeNo string, now time.Time) (Order, error) {
	if !validTradeNo(tradeNo) {
		return Order{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Order{}, fmt.Errorf("begin administrator cancel order: %w", err)
	}
	defer tx.Rollback()
	order, err := scanOrder(tx.QueryRowContext(ctx, orderSelect+` WHERE o.trade_no = ?`, tradeNo))
	if err != nil {
		return Order{}, err
	}
	if order.Status != OrderStatusPending {
		return Order{}, ErrOrderState
	}
	activeCheckout, err := hasActivePaymentCheckoutTx(ctx, tx, order.ID)
	if err != nil {
		return Order{}, err
	}
	if activeCheckout {
		return Order{}, ErrPaymentInProgress
	}
	if err := cancelOrderTx(ctx, tx, &order, now); err != nil {
		return Order{}, err
	}
	if err := tx.Commit(); err != nil {
		return Order{}, fmt.Errorf("commit administrator cancel order: %w", err)
	}
	return order, nil
}

func (s *Store) CancelOrder(ctx context.Context, userID int64, tradeNo string, now time.Time) (Order, error) {
	if userID < 1 || !validTradeNo(tradeNo) || now.Unix() < 0 {
		return Order{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Order{}, fmt.Errorf("begin cancel order: %w", err)
	}
	defer tx.Rollback()
	order, err := scanOrder(tx.QueryRowContext(ctx, orderSelect+` WHERE o.user_id = ? AND o.trade_no = ?`, userID, tradeNo))
	if err != nil {
		return Order{}, err
	}
	if order.Status != OrderStatusPending {
		return Order{}, ErrOrderState
	}
	activeCheckout, err := hasActivePaymentCheckoutTx(ctx, tx, order.ID)
	if err != nil {
		return Order{}, err
	}
	if activeCheckout {
		return Order{}, ErrPaymentInProgress
	}
	if err := cancelOrderTx(ctx, tx, &order, now); err != nil {
		return Order{}, err
	}
	if err := tx.Commit(); err != nil {
		return Order{}, fmt.Errorf("commit cancel order: %w", err)
	}
	return order, nil
}

func (s *Store) CompleteOrder(ctx context.Context, tradeNo, callbackNo string, now time.Time) (Order, error) {
	if !validTradeNo(tradeNo) || len(callbackNo) < 1 || len(callbackNo) > 255 || now.Unix() < 0 {
		return Order{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Order{}, fmt.Errorf("begin complete order: %w", err)
	}
	defer tx.Rollback()
	order, err := scanOrder(tx.QueryRowContext(ctx, orderSelect+` WHERE o.trade_no = ?`, tradeNo))
	if err != nil {
		return Order{}, err
	}
	if order.Status == OrderStatusCompleted {
		return order, nil
	}
	if err := completeOrderTx(ctx, tx, &order, callbackNo, now); err != nil {
		return Order{}, err
	}
	if err := tx.Commit(); err != nil {
		return Order{}, fmt.Errorf("commit complete order: %w", err)
	}
	return order, nil
}

// CompletePendingOrder preserves the legacy administrator contract: manual
// payment is only valid while an order is pending and repeated attempts fail.
func (s *Store) CompletePendingOrder(ctx context.Context, tradeNo, callbackNo string, now time.Time) (Order, error) {
	if !validTradeNo(tradeNo) || len(callbackNo) < 1 || len(callbackNo) > 255 || now.Unix() < 0 {
		return Order{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Order{}, fmt.Errorf("begin complete pending order: %w", err)
	}
	defer tx.Rollback()
	order, err := scanOrder(tx.QueryRowContext(ctx, orderSelect+` WHERE o.trade_no = ?`, tradeNo))
	if err != nil {
		return Order{}, err
	}
	if order.Status != OrderStatusPending {
		return Order{}, ErrOrderState
	}
	if err := completeOrderTx(ctx, tx, &order, callbackNo, now); err != nil {
		return Order{}, err
	}
	if err := tx.Commit(); err != nil {
		return Order{}, fmt.Errorf("commit complete pending order: %w", err)
	}
	return order, nil
}

func (s *Store) ProcessStaleOrders(ctx context.Context, now time.Time, limit int) (StaleOrderBatchResult, error) {
	if limit < 1 || limit > maxOrderBatchSize || now.Unix() < 0 {
		return StaleOrderBatchResult{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StaleOrderBatchResult{}, fmt.Errorf("begin stale order batch: %w", err)
	}
	defer tx.Rollback()
	cutoff := now.Add(-orderTimeout).Unix()
	rows, err := tx.QueryContext(ctx, orderSelect+`
		WHERE o.status = 1 OR (o.status = 0 AND o.created_at <= ?)
		ORDER BY o.created_at, o.id LIMIT ?
	`, cutoff, limit)
	if err != nil {
		return StaleOrderBatchResult{}, fmt.Errorf("list stale orders: %w", err)
	}
	orders := make([]Order, 0, limit)
	for rows.Next() {
		order, scanErr := scanOrder(rows)
		if scanErr != nil {
			_ = rows.Close()
			return StaleOrderBatchResult{}, scanErr
		}
		orders = append(orders, order)
	}
	if err := rows.Close(); err != nil {
		return StaleOrderBatchResult{}, fmt.Errorf("close stale orders: %w", err)
	}
	if err := rows.Err(); err != nil {
		return StaleOrderBatchResult{}, fmt.Errorf("iterate stale orders: %w", err)
	}
	result := StaleOrderBatchResult{}
	for index := range orders {
		order := &orders[index]
		if order.Status == OrderStatusPending {
			if err := cancelOrderTx(ctx, tx, order, now); err != nil {
				return StaleOrderBatchResult{}, err
			}
			result.Cancelled++
			continue
		}
		callback := order.CallbackNo
		if callback == "" {
			callback = order.TradeNo
		}
		if err := completeOrderTx(ctx, tx, order, callback, now); err != nil {
			return StaleOrderBatchResult{}, err
		}
		result.Completed++
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM orders WHERE status = 1 OR (status = 0 AND created_at <= ?)
	`, cutoff).Scan(&result.Remaining); err != nil {
		return StaleOrderBatchResult{}, fmt.Errorf("count stale orders: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return StaleOrderBatchResult{}, fmt.Errorf("commit stale order batch: %w", err)
	}
	return result, nil
}

func cancelOrderTx(ctx context.Context, tx *sql.Tx, order *Order, now time.Time) error {
	result, err := tx.ExecContext(ctx, `UPDATE orders SET status = 2, updated_at = ? WHERE id = ? AND status = 0`, now.Unix(), order.ID)
	if err != nil {
		return fmt.Errorf("cancel order: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count cancelled order: %w", err)
	}
	if changed != 1 {
		return ErrOrderState
	}
	if order.BalanceAmount > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE users SET balance = balance + ?, updated_at = ? WHERE id = ?
		`, order.BalanceAmount, now.Unix(), order.UserID); err != nil {
			return fmt.Errorf("refund cancelled order balance: %w", err)
		}
	}
	order.Status = OrderStatusCancelled
	order.UpdatedAt = now.UTC()
	return nil
}

func hasActivePaymentCheckoutTx(ctx context.Context, tx *sql.Tx, orderID int64) (bool, error) {
	var active bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM payment_checkout_attempts
			WHERE order_id = ? AND status IN (0, 1)
		)
	`, orderID).Scan(&active); err != nil {
		return false, fmt.Errorf("check active payment checkout before cancellation: %w", err)
	}
	return active, nil
}

func completeOrderTx(ctx context.Context, tx *sql.Tx, order *Order, callbackNo string, now time.Time) error {
	if order.Status != OrderStatusPending && order.Status != OrderStatusProcessing {
		return ErrOrderState
	}
	user, err := readOrderUser(ctx, tx, order.UserID)
	if err != nil {
		return err
	}
	plan, err := getPlan(ctx, tx, order.PlanID, now)
	if err != nil {
		return err
	}
	settings, err := readOrderSettings(ctx, tx)
	if err != nil {
		return err
	}
	beforeJSON, err := marshalEntitlementSnapshot(user)
	if err != nil {
		return err
	}

	paidAt := now.UTC()
	if order.PaidAt != nil {
		paidAt = *order.PaidAt
	}
	if order.CallbackNo != "" {
		callbackNo = order.CallbackNo
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE orders SET status = 1, paid_at = COALESCE(paid_at, ?),
			callback_no = COALESCE(callback_no, ?), updated_at = ?
		WHERE id = ? AND status IN (0, 1)
	`, paidAt.Unix(), callbackNo, now.Unix(), order.ID)
	if err != nil {
		return fmt.Errorf("mark order processing: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count processing order: %w", err)
	}
	if changed != 1 {
		return ErrOrderState
	}
	if order.SurplusCredit > 0 {
		user.balance += order.SurplusCredit
	}
	if len(order.SurplusOrderIDs) > 0 {
		for _, orderID := range order.SurplusOrderIDs {
			if orderID < 1 {
				return fmt.Errorf("invalid surplus order ID in order %d", order.ID)
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE orders SET status = 4, updated_at = ?
				WHERE id = ? AND user_id = ? AND status = 3 AND period <> 'reset_traffic'
			`, now.Unix(), orderID, order.UserID); err != nil {
				return fmt.Errorf("discount surplus order: %w", err)
			}
		}
	}

	resetTraffic := false
	beforeExpiry := nullableUnixTime(user.expiredAt)
	switch order.Period {
	case "reset_traffic":
		resetTraffic = true
	case "onetime":
		resetTraffic = true
		user.planID = sql.NullInt64{Int64: plan.ID, Valid: true}
		user.groupID = pointerToNullInt64(plan.GroupID)
		user.transferEnable = plan.TransferEnableGiB * bytesPerGiB
		user.expiredAt = sql.NullInt64{}
	default:
		months, valid := orderPeriodMonths[order.Period]
		if !valid {
			return fmt.Errorf("%w: invalid stored order period", ErrInvalidInput)
		}
		if order.Type == OrderTypeUpgrade {
			user.expiredAt = sql.NullInt64{Int64: now.Unix(), Valid: true}
		}
		if !user.expiredAt.Valid || order.Type == OrderTypeNew {
			resetTraffic = true
		}
		base := now
		if user.expiredAt.Valid && user.expiredAt.Int64 >= now.Unix() {
			base = time.Unix(user.expiredAt.Int64, 0)
		}
		user.planID = sql.NullInt64{Int64: plan.ID, Valid: true}
		user.groupID = pointerToNullInt64(plan.GroupID)
		user.transferEnable = plan.TransferEnableGiB * bytesPerGiB
		user.expiredAt = sql.NullInt64{Int64: addOrderMonths(base, months).Unix(), Valid: true}
	}
	user.speedLimit = optionalPlanInt(plan.SpeedLimit)
	user.deviceLimit = optionalPlanInt(plan.DeviceLimit)
	eventID := 0
	switch order.Type {
	case OrderTypeNew:
		eventID = settings.newOrderEvent
	case OrderTypeRenewal:
		eventID = settings.renewOrderEvent
	case OrderTypeUpgrade:
		eventID = settings.changeOrderEvent
	}
	if eventID == 1 {
		resetTraffic = true
	}
	if resetTraffic {
		user.trafficUpload = 0
		user.trafficDownload = 0
		user.lastResetAt = sql.NullInt64{Int64: now.Unix(), Valid: true}
		user.resetCount++
	}
	var systemResetMethod int
	if err := tx.QueryRowContext(ctx, `SELECT traffic_reset_method FROM app_settings WHERE id = 1`).Scan(&systemResetMethod); err != nil {
		return fmt.Errorf("read order traffic reset method: %w", err)
	}
	var expiry *time.Time
	if user.expiredAt.Valid {
		value := time.Unix(user.expiredAt.Int64, 0)
		expiry = &value
	}
	nextReset := CalculateNextTrafficReset(plan.ResetTrafficMethod, systemResetMethod, expiry, now)
	user.nextResetAt = nullableTime(nextReset)
	if _, err := tx.ExecContext(ctx, `
		UPDATE users SET plan_id = ?, group_id = ?, transfer_enable = ?, traffic_u = ?, traffic_d = ?,
			expired_at = ?, speed_limit = ?, device_limit = ?, next_reset_at = ?, last_reset_at = ?,
			reset_count = ?, balance = ?, admin_revision = admin_revision + 1, updated_at = ?
		WHERE id = ? AND account_kind = 'human'
	`, nullableSQLInt(user.planID), nullableSQLInt(user.groupID), user.transferEnable, user.trafficUpload,
		user.trafficDownload, nullableSQLInt(user.expiredAt), user.speedLimit, user.deviceLimit,
		nullableSQLInt(user.nextResetAt), nullableSQLInt(user.lastResetAt), user.resetCount, user.balance,
		now.Unix(), user.id); err != nil {
		return fmt.Errorf("apply order entitlement: %w", err)
	}
	afterJSON, err := marshalEntitlementSnapshot(user)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO order_entitlement_events (order_id, user_id, event_type, before_json, after_json, applied_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, order.ID, order.UserID, orderTypeEventName(order.Type), string(beforeJSON), string(afterJSON), now.Unix()); err != nil {
		return fmt.Errorf("record order entitlement: %w", err)
	}
	completed, err := tx.ExecContext(ctx, `
		UPDATE orders SET status = 3, entitlement_expired_at_before = ?, entitlement_expired_at_after = ?, updated_at = ?
		WHERE id = ? AND status = 1
	`, nullableTimeUnix(beforeExpiry), nullableSQLInt(user.expiredAt), now.Unix(), order.ID)
	if err != nil {
		return fmt.Errorf("complete order: %w", err)
	}
	completedCount, err := completed.RowsAffected()
	if err != nil {
		return fmt.Errorf("count completed order: %w", err)
	}
	if completedCount != 1 {
		return ErrOrderState
	}
	order.Status = OrderStatusCompleted
	order.PaidAt = &paidAt
	order.CallbackNo = callbackNo
	order.EntitlementExpiredAtBefore = beforeExpiry
	order.EntitlementExpiredAtAfter = nullableUnixTime(user.expiredAt)
	order.UpdatedAt = now.UTC()
	return nil
}

func validateOrderPurchase(user orderUserState, plan Plan, period string, now time.Time) error {
	active := !user.expiredAt.Valid || user.expiredAt.Int64 > now.Unix()
	if period == "reset_traffic" {
		if !active || !user.planID.Valid || user.planID.Int64 != plan.ID || user.transferEnable <= 0 {
			return ErrPlanUnavailable
		}
		return nil
	}
	if user.planID.Valid && user.planID.Int64 == plan.ID {
		if !plan.Renew || !active && !plan.Show {
			return ErrPlanUnavailable
		}
		return nil
	}
	if !plan.Show || !plan.Sell || plan.CapacityLimit != nil && *plan.CapacityLimit > 0 && plan.CapacityUsersCount >= int64(*plan.CapacityLimit) {
		return ErrPlanUnavailable
	}
	return nil
}

func calculateOrderSurplus(ctx context.Context, tx *sql.Tx, user orderUserState, order *Order, trafficAware bool, now time.Time) error {
	if !user.expiredAt.Valid {
		var id, paid int64
		err := tx.QueryRowContext(ctx, `
			SELECT id, total_amount + balance_amount FROM orders
			WHERE user_id = ? AND period = 'onetime' AND status = 3 ORDER BY id DESC LIMIT 1
		`, user.id).Scan(&id, &paid)
		if errors.Is(err, sql.ErrNoRows) || paid <= 0 || user.transferEnable <= 0 {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read permanent surplus order: %w", err)
		}
		remaining := user.transferEnable - minInt64(user.transferEnable, user.trafficUpload+user.trafficDownload)
		order.SurplusAmount = mulDivFloor(paid, remaining, user.transferEnable)
		ids, err := listSurplusOrderIDs(ctx, tx, user.id)
		if err != nil {
			return err
		}
		order.SurplusOrderIDs = ids
		return nil
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT id, total_amount, balance_amount, surplus_amount, surplus_credit, period, created_at
		FROM orders WHERE user_id = ? AND period NOT IN ('reset_traffic', 'onetime') AND status = 3
		ORDER BY id
	`, user.id)
	if err != nil {
		return fmt.Errorf("list recurring surplus orders: %w", err)
	}
	defer rows.Close()
	var amountSum, firstCreated int64
	monthSum := 0
	ids := make([]int64, 0)
	for rows.Next() {
		var id, total, balance, surplus, credit, created int64
		var period string
		if err := rows.Scan(&id, &total, &balance, &surplus, &credit, &period, &created); err != nil {
			return fmt.Errorf("scan recurring surplus order: %w", err)
		}
		months := orderPeriodMonths[period]
		if months == 0 {
			continue
		}
		amount := total + balance + surplus - credit
		if amount < 0 || amountSum > maxOrderMoneyCents-amount {
			return fmt.Errorf("order surplus amount overflow")
		}
		amountSum += amount
		monthSum += months
		if firstCreated == 0 || created < firstCreated {
			firstCreated = created
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate recurring surplus orders: %w", err)
	}
	if len(ids) == 0 || amountSum == 0 || monthSum == 0 {
		return nil
	}
	calculatedExpiry := addOrderMonths(time.Unix(firstCreated, 0), monthSum).Unix()
	totalSeconds := calculatedExpiry - firstCreated
	remainingSeconds := maxInt64(0, calculatedExpiry-now.Unix())
	if totalSeconds <= 0 || remainingSeconds == 0 {
		order.SurplusOrderIDs = ids
		return nil
	}
	numerator, denominator := remainingSeconds, totalSeconds
	if trafficAware {
		currentPlan, err := getPlan(ctx, tx, user.planID.Int64, now)
		if err != nil {
			return err
		}
		totalTraffic := currentPlan.TransferEnableGiB * int64(monthSum) * bytesPerGiB
		used := minInt64(totalTraffic, user.trafficUpload+user.trafficDownload)
		trafficRemaining := maxInt64(0, totalTraffic-used)
		if fractionLess(trafficRemaining, totalTraffic, numerator, denominator) {
			numerator, denominator = trafficRemaining, totalTraffic
		}
	}
	order.SurplusAmount = mulDivFloor(amountSum, numerator, denominator)
	order.SurplusOrderIDs = ids
	return nil
}

func listSurplusOrderIDs(ctx context.Context, tx *sql.Tx, userID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM orders WHERE user_id = ? AND period <> 'reset_traffic' AND status = 3 ORDER BY id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list surplus order IDs: %w", err)
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan surplus order ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate surplus order IDs: %w", err)
	}
	return ids, nil
}

func setOrderCommission(ctx context.Context, tx *sql.Tx, user orderUserState, settings orderSettings, order *Order) error {
	if !user.inviteUserID.Valid || order.TotalAmount <= 0 {
		return nil
	}
	inviteUserID := user.inviteUserID.Int64
	// Legacy Xboard keeps the inviter relationship on every positive order,
	// even when a one-time commission is no longer eligible.
	order.InviteUserID = &inviteUserID
	var commissionType int
	var commissionRate sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT commission_type, commission_rate FROM users WHERE id = ? AND account_kind = 'human'
	`, user.inviteUserID.Int64).Scan(&commissionType, &commissionRate); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return fmt.Errorf("read order inviter: %w", err)
	}
	if commissionType == 0 {
		if settings.commissionFirstTime {
			commissionType = 2
		} else {
			commissionType = 1
		}
	}
	eligible := commissionType == 1
	if commissionType == 2 {
		var prior bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM orders WHERE user_id = ? AND status NOT IN (0, 2))
		`, user.id).Scan(&prior); err != nil {
			return fmt.Errorf("check prior commission order: %w", err)
		}
		eligible = !prior
	}
	if !eligible {
		return nil
	}
	rate := settings.inviteCommissionPercent
	if commissionRate.Valid && commissionRate.Int64 > 0 {
		rate = int(commissionRate.Int64)
	}
	order.CommissionBalance = order.TotalAmount * int64(rate) / 100
	return nil
}

func readOrderUser(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID int64) (orderUserState, error) {
	var user orderUserState
	err := database.QueryRowContext(ctx, `
		SELECT id, plan_id, expired_at, balance, discount, invite_user_id, transfer_enable, traffic_u,
			traffic_d, speed_limit, device_limit, group_id, next_reset_at, last_reset_at, reset_count,
			banned, account_kind, commission_type, commission_rate, commission_balance
		FROM users WHERE id = ?
	`, userID).Scan(&user.id, &user.planID, &user.expiredAt, &user.balance, &user.discount,
		&user.inviteUserID, &user.transferEnable, &user.trafficUpload, &user.trafficDownload,
		&user.speedLimit, &user.deviceLimit, &user.groupID, &user.nextResetAt, &user.lastResetAt,
		&user.resetCount, &user.banned, &user.accountKind, &user.commissionType,
		&user.commissionRate, &user.commissionBalance)
	if errors.Is(err, sql.ErrNoRows) {
		return orderUserState{}, ErrNotFound
	}
	if err != nil {
		return orderUserState{}, fmt.Errorf("read order user: %w", err)
	}
	return user, nil
}

func readOrderSettings(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (orderSettings, error) {
	var settings orderSettings
	err := database.QueryRowContext(ctx, `
		SELECT plan_change_enable, surplus_enable, new_order_event_id, renew_order_event_id,
			change_order_event_id, commission_first_time_enable, invite_commission
		FROM app_settings WHERE id = 1
	`).Scan(&settings.planChange, &settings.surplus, &settings.newOrderEvent, &settings.renewOrderEvent,
		&settings.changeOrderEvent, &settings.commissionFirstTime, &settings.inviteCommissionPercent)
	if err != nil {
		return orderSettings{}, fmt.Errorf("read order settings: %w", err)
	}
	return settings, nil
}

const orderColumns = `o.id, o.user_id, o.plan_id, o.payment_id, o.period, o.trade_no, o.original_amount,
	       o.total_amount, o.handling_amount, o.balance_amount, o.surplus_credit, o.surplus_amount,
	       o.type, o.status, o.surplus_order_ids_json, o.coupon_id, o.commission_status,
	       o.invite_user_id, o.actual_commission_balance, o.commission_rate, o.commission_auto_check,
	       o.commission_balance, o.discount_amount, o.paid_at, o.callback_no, o.distributor_order_id,
	       o.entitlement_expired_at_before, o.entitlement_expired_at_after, o.created_at, o.updated_at`

const orderSelect = `SELECT ` + orderColumns + ` FROM orders o`

const adminOrderSelect = `SELECT ` + orderColumns + `, u.email, p.name
	FROM orders o JOIN users u ON u.id = o.user_id JOIN plans p ON p.id = o.plan_id`

func scanOrder(row rowScanner) (Order, error) {
	return scanOrderFields(row, nil, nil)
}

func scanOrderWithAdminFields(row rowScanner, userEmail, planName *string) (Order, error) {
	return scanOrderFields(row, userEmail, planName)
}

func scanOrderFields(row rowScanner, userEmail, planName *string) (Order, error) {
	var order Order
	var paymentID, handling, couponID, commissionStatus, inviteUserID, actualCommission, commissionRate sql.NullInt64
	var commissionAuto, paidAt, distributorOrderID, entitlementBefore, entitlementAfter sql.NullInt64
	var callbackNo sql.NullString
	var surplusJSON string
	var createdAt, updatedAt int64
	arguments := []any{&order.ID, &order.UserID, &order.PlanID, &paymentID, &order.Period, &order.TradeNo,
		&order.OriginalAmount, &order.TotalAmount, &handling, &order.BalanceAmount, &order.SurplusCredit,
		&order.SurplusAmount, &order.Type, &order.Status, &surplusJSON, &couponID, &commissionStatus,
		&inviteUserID, &actualCommission, &commissionRate, &commissionAuto, &order.CommissionBalance,
		&order.DiscountAmount, &paidAt, &callbackNo, &distributorOrderID, &entitlementBefore,
		&entitlementAfter, &createdAt, &updatedAt}
	if userEmail != nil && planName != nil {
		arguments = append(arguments, userEmail, planName)
	}
	err := row.Scan(arguments...)
	if errors.Is(err, sql.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("scan order: %w", err)
	}
	if err := json.Unmarshal([]byte(surplusJSON), &order.SurplusOrderIDs); err != nil {
		return Order{}, fmt.Errorf("decode order surplus IDs: %w", err)
	}
	if order.SurplusOrderIDs == nil {
		order.SurplusOrderIDs = []int64{}
	}
	order.PaymentID = nullableInt64Pointer(paymentID)
	order.HandlingAmount = nullableInt64Pointer(handling)
	order.CouponID = nullableInt64Pointer(couponID)
	order.CommissionStatus = nullableIntPointer(commissionStatus)
	order.InviteUserID = nullableInt64Pointer(inviteUserID)
	order.ActualCommissionBalance = nullableInt64Pointer(actualCommission)
	order.CommissionRate = nullableIntPointer(commissionRate)
	if commissionAuto.Valid {
		value := commissionAuto.Int64 != 0
		order.CommissionAutoCheck = &value
	}
	order.PaidAt = nullableUnixTime(paidAt)
	order.CallbackNo = callbackNo.String
	order.DistributorOrderID = nullableInt64Pointer(distributorOrderID)
	order.EntitlementExpiredAtBefore = nullableUnixTime(entitlementBefore)
	order.EntitlementExpiredAtAfter = nullableUnixTime(entitlementAfter)
	order.CreatedAt = time.Unix(createdAt, 0).UTC()
	order.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return order, nil
}

func normalizeOrderPeriod(period string) (string, bool) {
	period = strings.TrimSpace(period)
	if converted, exists := legacyOrderPeriods[period]; exists {
		period = converted
	}
	_, valid := planPricePeriods[period]
	return period, valid
}

func newOrderTradeNo(now time.Time) (string, error) {
	location, err := time.LoadLocation(trafficResetLocationID)
	if err != nil {
		return "", fmt.Errorf("load order timezone: %w", err)
	}
	maximum := new(big.Int).Exp(big.NewInt(10), big.NewInt(11), nil)
	random, err := rand.Int(rand.Reader, maximum)
	if err != nil {
		return "", fmt.Errorf("generate order number: %w", err)
	}
	return fmt.Sprintf("%s%011d", now.In(location).Format("20060102150405"), random.Int64()), nil
}

func validTradeNo(value string) bool {
	if len(value) == 25 {
		for _, character := range value {
			if character < '0' || character > '9' {
				return false
			}
		}
		return true
	}
	if len(value) == 32 {
		for _, character := range value {
			if character < '0' || character > '9' && (character < 'a' || character > 'f') {
				return false
			}
		}
		return true
	}
	return false
}

func newAssignedOrderTradeNo() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate assigned order number: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func addOrderMonths(value time.Time, months int) time.Time {
	location, err := time.LoadLocation(trafficResetLocationID)
	if err != nil {
		return value.AddDate(0, months, 0)
	}
	return value.In(location).AddDate(0, months, 0)
}

func marshalEntitlementSnapshot(user orderUserState) ([]byte, error) {
	return json.Marshal(map[string]any{
		"plan_id": nullableSQLInt(user.planID), "group_id": nullableSQLInt(user.groupID),
		"transfer_enable": user.transferEnable, "traffic_u": user.trafficUpload, "traffic_d": user.trafficDownload,
		"expired_at": nullableSQLInt(user.expiredAt), "speed_limit": user.speedLimit, "device_limit": user.deviceLimit,
		"next_reset_at": nullableSQLInt(user.nextResetAt), "last_reset_at": nullableSQLInt(user.lastResetAt),
		"reset_count": user.resetCount, "balance": user.balance,
	})
}

func orderTypeEventName(value OrderType) string {
	switch value {
	case OrderTypeRenewal:
		return "renewal"
	case OrderTypeUpgrade:
		return "upgrade"
	case OrderTypeResetTraffic:
		return "reset_traffic"
	default:
		return "new"
	}
}

func optionalPlanInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func pointerToNullInt64(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func nullableTime(value *time.Time) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: value.Unix(), Valid: true}
}

func nullableSQLInt(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func nullableUnixTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	result := time.Unix(value.Int64, 0).UTC()
	return &result
}

func nullableTimeUnix(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.Unix()
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func mulDivFloor(value, numerator, denominator int64) int64 {
	if value <= 0 || numerator <= 0 || denominator <= 0 {
		return 0
	}
	product := new(big.Int).Mul(big.NewInt(value), big.NewInt(numerator))
	product.Quo(product, big.NewInt(denominator))
	if !product.IsInt64() || product.Int64() > maxOrderMoneyCents {
		return maxOrderMoneyCents
	}
	return product.Int64()
}

func fractionLess(leftNumerator, leftDenominator, rightNumerator, rightDenominator int64) bool {
	if leftDenominator <= 0 {
		return true
	}
	left := new(big.Int).Mul(big.NewInt(leftNumerator), big.NewInt(rightDenominator))
	right := new(big.Int).Mul(big.NewInt(rightNumerator), big.NewInt(leftDenominator))
	return left.Cmp(right) < 0
}

func getPlanForOrder(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, planID int64) (Plan, error) {
	return scanPlan(database.QueryRowContext(ctx, `
		SELECT p.id, p.group_id, p.transfer_enable_gib, p.name, p.speed_limit, p.show, p.sort_position,
		       p.renew, p.content, p.reset_traffic_method, p.capacity_limit, p.prices_json, p.sell,
		       p.device_limit, p.tags_json, 0, 0, 0, p.revision, p.created_at, p.updated_at
		FROM plans p WHERE p.id = ?
	`, planID))
}

func attachOrderPlans(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, orders []Order) error {
	plans := make(map[int64]*Plan)
	for index := range orders {
		plan, exists := plans[orders[index].PlanID]
		if !exists {
			value, err := getPlanForOrder(ctx, database, orders[index].PlanID)
			if err != nil {
				return err
			}
			plan = &value
			plans[value.ID] = plan
		}
		orders[index].Plan = plan
	}
	return nil
}

func escapeOrderLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}
