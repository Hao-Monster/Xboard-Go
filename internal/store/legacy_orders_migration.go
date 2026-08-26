package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	LegacyOrdersSlice = "orders-v1"
	maxLegacyOrders   = 1_000_000
)

type LegacyOrder struct {
	ID                         int64       `json:"id"`
	UserID                     int64       `json:"user_id"`
	PlanID                     int64       `json:"plan_id"`
	PaymentID                  *int64      `json:"payment_id"`
	Period                     string      `json:"period"`
	TradeNo                    string      `json:"trade_no"`
	OriginalAmount             int64       `json:"original_amount"`
	TotalAmount                int64       `json:"total_amount"`
	HandlingAmount             *int64      `json:"handling_amount"`
	BalanceAmount              int64       `json:"balance_amount"`
	SurplusCredit              int64       `json:"surplus_credit"`
	SurplusAmount              int64       `json:"surplus_amount"`
	Type                       OrderType   `json:"type"`
	Status                     OrderStatus `json:"status"`
	SurplusOrderIDs            []int64     `json:"surplus_order_ids"`
	CouponID                   *int64      `json:"coupon_id"`
	CommissionStatus           *int        `json:"commission_status"`
	InviteUserID               *int64      `json:"invite_user_id"`
	ActualCommissionBalance    *int64      `json:"actual_commission_balance"`
	CommissionBalance          int64       `json:"commission_balance"`
	DiscountAmount             int64       `json:"discount_amount"`
	PaidAt                     *int64      `json:"paid_at"`
	CallbackNo                 *string     `json:"callback_no"`
	DistributorOrderID         *int64      `json:"distributor_order_id"`
	EntitlementExpiredAtBefore *int64      `json:"entitlement_expired_at_before"`
	EntitlementExpiredAtAfter  *int64      `json:"entitlement_expired_at_after"`
	DistributorIdempotencyKey  *string     `json:"distributor_idempotency_key"`
	DistributorSettledBy       *int64      `json:"distributor_settled_by"`
	CreatedAt                  int64       `json:"created_at"`
	UpdatedAt                  int64       `json:"updated_at"`
}

type LegacyOrdersImport struct {
	Slice                string
	SourceSHA256         string
	SourceSize           int64
	Orders               []LegacyOrder
	Checksum             string
	RollbackBackupPath   string
	RollbackBackupSHA256 string
}

type LegacyOrdersImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Orders               LegacyDomainResult `json:"orders"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func LegacyOrdersChecksum(orders []LegacyOrder) string {
	ordered := append([]LegacyOrder(nil), orders...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	if ordered == nil {
		ordered = []LegacyOrder{}
	}
	return legacyCanonicalChecksum(ordered)
}

func LegacyOrderOriginalAmount(total, balance, discount, surplus, surplusCredit int64) (int64, bool) {
	values := [...]int64{total, balance, discount, surplus, surplusCredit}
	for _, value := range values {
		if value < 0 || value > maxOrderMoneyCents {
			return 0, false
		}
	}
	amount := total
	for _, value := range [...]int64{balance, discount, surplus} {
		if amount > maxOrderMoneyCents-value {
			return 0, false
		}
		amount += value
	}
	if surplusCredit > amount {
		return 0, false
	}
	return amount - surplusCredit, true
}

func ValidateLegacyOrdersData(orders []LegacyOrder) error {
	if len(orders) > maxLegacyOrders {
		return fmt.Errorf("%w: legacy orders exceed the %d-row migration limit", ErrInvalidInput, maxLegacyOrders)
	}
	ids := make(map[int64]LegacyOrder, len(orders))
	trades := make(map[string]struct{}, len(orders))
	activeUsers := make(map[int64]struct{})
	for _, order := range orders {
		derived, derivedOK := LegacyOrderOriginalAmount(order.TotalAmount, order.BalanceAmount, order.DiscountAmount, order.SurplusAmount, order.SurplusCredit)
		_, periodOK := orderPeriodMonths[order.Period]
		periodOK = periodOK || order.Period == "onetime" || order.Period == "reset_traffic"
		if order.ID < 1 || order.UserID < 1 || order.PlanID < 1 || !periodOK || !validTradeNo(order.TradeNo) ||
			order.Type < OrderTypeNew || order.Type > OrderTypeResetTraffic || order.Status < OrderStatusPending || order.Status > OrderStatusDiscounted ||
			!derivedOK || order.OriginalAmount != derived || !validLegacyMoneyPointer(order.HandlingAmount) ||
			!validLegacyMoneyPointer(order.ActualCommissionBalance) || order.CommissionBalance < 0 || order.CommissionBalance > maxOrderMoneyCents ||
			order.CommissionStatus == nil || *order.CommissionStatus < 0 || *order.CommissionStatus > 3 ||
			!validLegacyPositivePointer(order.PaymentID) || !validLegacyPositivePointer(order.CouponID) ||
			!validLegacyPositivePointer(order.InviteUserID) || !validLegacyPositivePointer(order.DistributorOrderID) ||
			!validLegacyPositivePointer(order.DistributorSettledBy) ||
			!validLegacyOptionalTimestamp(order.PaidAt) || !validLegacyOptionalTimestamp(order.EntitlementExpiredAtBefore) ||
			!validLegacyOptionalTimestamp(order.EntitlementExpiredAtAfter) ||
			!validLegacyUnixTimestamp(order.CreatedAt) || !validLegacyUnixTimestamp(order.UpdatedAt) || order.UpdatedAt < order.CreatedAt ||
			!validLegacyOrderString(order.CallbackNo, 255) || !validLegacyOrderString(order.DistributorIdempotencyKey, 128) ||
			len(order.SurplusOrderIDs) > maxLegacyOrders {
			return fmt.Errorf("%w: invalid legacy order id %d", ErrInvalidInput, order.ID)
		}
		if _, exists := ids[order.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy order id %d", ErrInvalidInput, order.ID)
		}
		ids[order.ID] = order
		if _, exists := trades[order.TradeNo]; exists {
			return fmt.Errorf("%w: duplicate legacy order trade number", ErrInvalidInput)
		}
		trades[order.TradeNo] = struct{}{}
		if order.Status == OrderStatusPending || order.Status == OrderStatusProcessing {
			if _, exists := activeUsers[order.UserID]; exists {
				return fmt.Errorf("%w: legacy user %d has multiple active orders", ErrConflict, order.UserID)
			}
			activeUsers[order.UserID] = struct{}{}
		}
		seenSurplus := make(map[int64]struct{}, len(order.SurplusOrderIDs))
		for _, orderID := range order.SurplusOrderIDs {
			if orderID < 1 || orderID == order.ID {
				return fmt.Errorf("%w: legacy order id %d has an invalid surplus order reference", ErrInvalidInput, order.ID)
			}
			if _, duplicate := seenSurplus[orderID]; duplicate {
				return fmt.Errorf("%w: legacy order id %d has duplicate surplus order references", ErrInvalidInput, order.ID)
			}
			seenSurplus[orderID] = struct{}{}
		}
	}
	for _, order := range orders {
		for _, orderID := range order.SurplusOrderIDs {
			referenced, exists := ids[orderID]
			if !exists || referenced.UserID != order.UserID {
				return fmt.Errorf("%w: legacy order id %d references a missing or foreign surplus order", ErrConflict, order.ID)
			}
		}
	}
	return nil
}

func validLegacyMoneyPointer(value *int64) bool {
	return value == nil || *value >= 0 && *value <= maxOrderMoneyCents
}

func validLegacyPositivePointer(value *int64) bool { return value == nil || *value > 0 }

func validLegacyOptionalTimestamp(value *int64) bool {
	return value == nil || validLegacyUnixTimestamp(*value)
}

func validLegacyOrderString(value *string, maximum int) bool {
	if value == nil {
		return true
	}
	return *value != "" && len(*value) <= maximum && utf8.ValidString(*value) && strings.IndexByte(*value, 0) < 0
}

func (s *Store) LookupLegacyOrdersImport(ctx context.Context, sourceSHA256 string) (LegacyOrdersImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyOrdersImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacyOrdersImport(ctx, s.db, sourceSHA256)
}

func lookupLegacyOrdersImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyOrdersImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?`, LegacyOrdersSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyOrdersImportReport{}, false, nil
	}
	if err != nil {
		return LegacyOrdersImportReport{}, false, fmt.Errorf("lookup legacy order migration: %w", err)
	}
	var report LegacyOrdersImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyOrdersImportReport{}, false, fmt.Errorf("decode legacy order migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyOrders(ctx context.Context, input LegacyOrdersImport, now time.Time) (LegacyOrdersImportReport, error) {
	if err := validateLegacyOrdersImport(input); err != nil {
		return LegacyOrdersImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyOrdersImportReport{}, fmt.Errorf("begin legacy order import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return LegacyOrdersImportReport{}, fmt.Errorf("read legacy order target schema: %w", err)
	}
	if version != CurrentSchemaVersion() {
		return LegacyOrdersImportReport{}, fmt.Errorf("legacy order import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyOrdersImportReport{}, fmt.Errorf("validate legacy order target schema: %w", err)
	}
	if existing, found, err := lookupLegacyOrdersImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyOrdersImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyOrdersImportReport{}, fmt.Errorf("commit idempotent legacy order import: %w", err)
		}
		return existing, nil
	}
	var otherRuns, targetOrders, targetEvents int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyOrdersSlice).Scan(&otherRuns); err != nil {
		return LegacyOrdersImportReport{}, fmt.Errorf("count legacy order migrations: %w", err)
	}
	if otherRuns != 0 {
		return LegacyOrdersImportReport{}, fmt.Errorf("%w: legacy order slice was already imported from another snapshot", ErrConflict)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders`).Scan(&targetOrders); err != nil {
		return LegacyOrdersImportReport{}, fmt.Errorf("count target orders: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM order_entitlement_events`).Scan(&targetEvents); err != nil {
		return LegacyOrdersImportReport{}, fmt.Errorf("count target order events: %w", err)
	}
	if targetOrders != 0 || targetEvents != 0 {
		return LegacyOrdersImportReport{}, fmt.Errorf("%w: legacy order import requires empty order targets", ErrConflict)
	}
	if err := validateLegacyOrderReferences(ctx, tx, input.Orders); err != nil {
		return LegacyOrdersImportReport{}, err
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO orders (
			id, user_id, plan_id, payment_id, period, trade_no, original_amount, total_amount, handling_amount,
			balance_amount, surplus_credit, surplus_amount, type, status, surplus_order_ids_json, coupon_id,
			commission_status, invite_user_id, actual_commission_balance, commission_balance, discount_amount,
			paid_at, callback_no, distributor_order_id, entitlement_expired_at_before, entitlement_expired_at_after,
			distributor_idempotency_key, distributor_settled_by, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return LegacyOrdersImportReport{}, fmt.Errorf("prepare legacy order import: %w", err)
	}
	defer statement.Close()
	for _, order := range input.Orders {
		surplusIDs, err := json.Marshal(order.SurplusOrderIDs)
		if err != nil {
			return LegacyOrdersImportReport{}, fmt.Errorf("encode legacy order id %d surplus references: %w", order.ID, err)
		}
		if _, err := statement.ExecContext(ctx,
			order.ID, order.UserID, order.PlanID, nullableInt64Value(order.PaymentID), order.Period, order.TradeNo,
			order.OriginalAmount, order.TotalAmount, nullableInt64Value(order.HandlingAmount), order.BalanceAmount,
			order.SurplusCredit, order.SurplusAmount, order.Type, order.Status, string(surplusIDs), nullableInt64Value(order.CouponID),
			nullableIntValue(order.CommissionStatus), nullableInt64Value(order.InviteUserID), nullableInt64Value(order.ActualCommissionBalance),
			order.CommissionBalance, order.DiscountAmount, nullableInt64Value(order.PaidAt), nullableStringValue(order.CallbackNo),
			nullableInt64Value(order.DistributorOrderID), nullableInt64Value(order.EntitlementExpiredAtBefore),
			nullableInt64Value(order.EntitlementExpiredAtAfter), nullableStringValue(order.DistributorIdempotencyKey),
			nullableInt64Value(order.DistributorSettledBy), order.CreatedAt, order.UpdatedAt,
		); err != nil {
			return LegacyOrdersImportReport{}, fmt.Errorf("import legacy order id %d: %w", order.ID, err)
		}
	}
	target, err := readLegacyTargetOrders(ctx, tx)
	if err != nil {
		return LegacyOrdersImportReport{}, err
	}
	report := LegacyOrdersImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Orders:    LegacyDomainResult{SourceRows: len(input.Orders), TargetRows: len(target), SourceChecksum: input.Checksum, TargetChecksum: LegacyOrdersChecksum(target)},
		AppliedAt: now.UTC(),
	}
	if report.Orders.SourceRows != report.Orders.TargetRows || report.Orders.SourceChecksum != report.Orders.TargetChecksum {
		return LegacyOrdersImportReport{}, errors.New("legacy order target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyOrdersImportReport{}, fmt.Errorf("encode legacy order migration report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO legacy_migration_runs
		(slice, source_sha256, source_size, rollback_backup_path, rollback_backup_sha256, report_json, applied_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyOrdersImportReport{}, fmt.Errorf("record legacy order migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyOrdersImportReport{}, fmt.Errorf("commit legacy order import: %w", err)
	}
	return report, nil
}

func validateLegacyOrdersImport(input LegacyOrdersImport) error {
	if input.Slice != LegacyOrdersSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) ||
		strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 || !validLowerSHA256(input.RollbackBackupSHA256) ||
		input.Checksum != LegacyOrdersChecksum(input.Orders) {
		return fmt.Errorf("%w: invalid legacy order import", ErrInvalidInput)
	}
	return ValidateLegacyOrdersData(input.Orders)
}

func validateLegacyOrderReferences(ctx context.Context, tx *sql.Tx, orders []LegacyOrder) error {
	users := make(map[int64]struct{})
	plans := make(map[int64]struct{})
	for _, order := range orders {
		for _, userID := range []*int64{&order.UserID, order.InviteUserID, order.DistributorSettledBy} {
			if userID == nil {
				continue
			}
			if _, checked := users[*userID]; checked {
				continue
			}
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = ? AND account_kind = 'human')`, *userID).Scan(&exists); err != nil {
				return fmt.Errorf("validate legacy order user: %w", err)
			}
			if !exists {
				return fmt.Errorf("%w: legacy orders reference missing human user %d", ErrConflict, *userID)
			}
			users[*userID] = struct{}{}
		}
		if _, checked := plans[order.PlanID]; !checked {
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plans WHERE id = ?)`, order.PlanID).Scan(&exists); err != nil {
				return fmt.Errorf("validate legacy order plan: %w", err)
			}
			if !exists {
				return fmt.Errorf("%w: legacy orders reference missing plan %d", ErrConflict, order.PlanID)
			}
			plans[order.PlanID] = struct{}{}
		}
	}
	return nil
}

func nullableStringValue(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func readLegacyTargetOrders(ctx context.Context, database queryer) ([]LegacyOrder, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, user_id, plan_id, payment_id, period, trade_no, original_amount, total_amount, handling_amount,
		       balance_amount, surplus_credit, surplus_amount, type, status, surplus_order_ids_json, coupon_id,
		       commission_status, invite_user_id, actual_commission_balance, commission_balance, discount_amount,
		       paid_at, callback_no, distributor_order_id, entitlement_expired_at_before, entitlement_expired_at_after,
		       distributor_idempotency_key, distributor_settled_by, created_at, updated_at
		FROM orders ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("read imported legacy orders: %w", err)
	}
	defer rows.Close()
	orders := make([]LegacyOrder, 0)
	for rows.Next() {
		var order LegacyOrder
		var paymentID, handling, couponID, inviterID, actualCommission, paidAt sql.NullInt64
		var distributorOrderID, before, after, settledBy sql.NullInt64
		var commissionStatus sql.NullInt64
		var callback, idempotency sql.NullString
		var surplusJSON string
		if err := rows.Scan(&order.ID, &order.UserID, &order.PlanID, &paymentID, &order.Period, &order.TradeNo,
			&order.OriginalAmount, &order.TotalAmount, &handling, &order.BalanceAmount, &order.SurplusCredit,
			&order.SurplusAmount, &order.Type, &order.Status, &surplusJSON, &couponID, &commissionStatus,
			&inviterID, &actualCommission, &order.CommissionBalance, &order.DiscountAmount, &paidAt, &callback,
			&distributorOrderID, &before, &after, &idempotency, &settledBy, &order.CreatedAt, &order.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan imported legacy order: %w", err)
		}
		if err := json.Unmarshal([]byte(surplusJSON), &order.SurplusOrderIDs); err != nil {
			return nil, fmt.Errorf("decode imported legacy order id %d surplus references: %w", order.ID, err)
		}
		if order.SurplusOrderIDs == nil {
			order.SurplusOrderIDs = []int64{}
		}
		order.PaymentID = nullableInt64Pointer(paymentID)
		order.HandlingAmount = nullableInt64Pointer(handling)
		order.CouponID = nullableInt64Pointer(couponID)
		order.CommissionStatus = nullableIntPointer(commissionStatus)
		order.InviteUserID = nullableInt64Pointer(inviterID)
		order.ActualCommissionBalance = nullableInt64Pointer(actualCommission)
		order.PaidAt = nullableInt64Pointer(paidAt)
		order.CallbackNo = nullableStringPointer(callback)
		order.DistributorOrderID = nullableInt64Pointer(distributorOrderID)
		order.EntitlementExpiredAtBefore = nullableInt64Pointer(before)
		order.EntitlementExpiredAtAfter = nullableInt64Pointer(after)
		order.DistributorIdempotencyKey = nullableStringPointer(idempotency)
		order.DistributorSettledBy = nullableInt64Pointer(settledBy)
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate imported legacy orders: %w", err)
	}
	return orders, nil
}

func nullableStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}
