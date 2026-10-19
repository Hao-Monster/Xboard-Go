package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	commissionConfirmationDelay = 72 * time.Hour
	maxCommissionBatchSize      = 1_000
	maxCommissionPage           = 1_000_000
)

type commissionSummaryQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func populateInvitationCommissionSummary(ctx context.Context, database commissionSummaryQueryer, ownerID int64, summary *InvitationSummary) error {
	if ownerID < 1 || summary == nil {
		return ErrInvalidInput
	}
	var commissionRate sql.NullInt64
	var globalRate int
	var distributionEnabled bool
	var withdrawClosed bool
	var withdrawMethodsJSON string
	var levelOne, levelTwo, levelThree int
	if err := database.QueryRowContext(ctx, `
		SELECT u.commission_balance, u.commission_rate, s.invite_commission,
		       s.commission_distribution_enable, s.commission_distribution_l1,
		       s.commission_distribution_l2, s.commission_distribution_l3,
		       s.withdraw_close_enable, s.commission_withdraw_limit,
		       s.commission_withdraw_method
		FROM users u CROSS JOIN app_settings s
		WHERE u.id = ? AND u.account_kind = 'human' AND s.id = 1
	`, ownerID).Scan(
		&summary.AvailableCommission, &commissionRate, &globalRate, &distributionEnabled,
		&levelOne, &levelTwo, &levelThree, &withdrawClosed,
		&summary.CommissionWithdrawalLimit, &withdrawMethodsJSON,
	); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("read invitation commission settings: %w", err)
	}
	if commissionRate.Valid && commissionRate.Int64 > 0 {
		summary.CommissionRate = int(commissionRate.Int64)
	} else {
		summary.CommissionRate = globalRate
	}
	summary.CommissionDistributionEnabled = distributionEnabled
	if err := json.Unmarshal([]byte(withdrawMethodsJSON), &summary.CommissionWithdrawalMethods); err != nil {
		return fmt.Errorf("decode stored commission withdrawal methods: %w", err)
	}
	if !validCommissionWithdrawMethods(summary.CommissionWithdrawalMethods) {
		return errors.New("stored commission withdrawal methods are invalid")
	}
	summary.CommissionWithdrawalMethods = append([]string(nil), summary.CommissionWithdrawalMethods...)
	summary.CommissionWithdrawalEnabled = !withdrawClosed
	summary.CommissionDistributionRates = make([]int, 0)
	if distributionEnabled {
		summary.CommissionDistributionRates = []int{
			int(commissionShare(int64(summary.CommissionRate), levelOne)),
			int(commissionShare(int64(summary.CommissionRate), levelTwo)),
			int(commissionShare(int64(summary.CommissionRate), levelThree)),
		}
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE invite_user_id = ?`, ownerID).Scan(&summary.InvitedCount); err != nil {
		return fmt.Errorf("count invited users: %w", err)
	}
	if err := database.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(get_amount), 0) FROM commission_logs WHERE invite_user_id = ?
	`, ownerID).Scan(&summary.ValidCommission); err != nil {
		return fmt.Errorf("sum valid invitation commission: %w", err)
	}
	if err := database.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(commission_balance), 0)
		FROM orders
		WHERE invite_user_id = ? AND status = ? AND commission_status = 0
	`, ownerID, OrderStatusCompleted).Scan(&summary.PendingCommission); err != nil {
		return fmt.Errorf("sum pending invitation commission: %w", err)
	}
	if distributionEnabled {
		summary.PendingCommission = commissionShare(summary.PendingCommission, levelOne)
	}
	return nil
}

func (s *Store) ListCommissionLogs(ctx context.Context, ownerID int64, page, pageSize int) (CommissionLogPage, error) {
	if ownerID < 1 || page < 1 || page > maxCommissionPage || pageSize < 1 || pageSize > 100 {
		return CommissionLogPage{}, ErrInvalidInput
	}
	result := CommissionLogPage{Items: make([]CommissionLog, 0), Page: page, PageSize: pageSize}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM commission_logs WHERE invite_user_id = ?`, ownerID).Scan(&result.Total); err != nil {
		return CommissionLogPage{}, fmt.Errorf("count commission logs: %w", err)
	}
	offset := int64(page-1) * int64(pageSize)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, order_id, invite_user_id, user_id, trade_no, order_amount, get_amount, created_at
		FROM commission_logs
		WHERE invite_user_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ? OFFSET ?
	`, ownerID, pageSize, offset)
	if err != nil {
		return CommissionLogPage{}, fmt.Errorf("list commission logs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item CommissionLog
		var createdAt int64
		if err := rows.Scan(&item.ID, &item.OrderID, &item.InviteUserID, &item.UserID, &item.TradeNo,
			&item.OrderAmount, &item.GetAmount, &createdAt); err != nil {
			return CommissionLogPage{}, fmt.Errorf("scan commission log: %w", err)
		}
		item.CreatedAt = time.Unix(createdAt, 0).UTC()
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return CommissionLogPage{}, fmt.Errorf("iterate commission logs: %w", err)
	}
	return result, nil
}

func (s *Store) TransferCommission(ctx context.Context, userID, amount int64, now time.Time) (CommissionTransferResult, error) {
	if userID < 1 || amount < 1 || amount > maxOrderMoneyCents || now.Unix() < 0 {
		return CommissionTransferResult{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CommissionTransferResult{}, fmt.Errorf("begin commission transfer: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE users
		SET commission_balance = commission_balance - ?, balance = balance + ?, updated_at = ?
		WHERE id = ? AND account_kind = 'human' AND commission_balance >= ?
		  AND balance <= ?
	`, amount, amount, now.Unix(), userID, amount, maxOrderMoneyCents-amount)
	if err != nil {
		return CommissionTransferResult{}, fmt.Errorf("transfer commission: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return CommissionTransferResult{}, fmt.Errorf("count commission transfer: %w", err)
	}
	if updated != 1 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = ? AND account_kind = 'human')`, userID).Scan(&exists); err != nil {
			return CommissionTransferResult{}, fmt.Errorf("check commission transfer user: %w", err)
		}
		if !exists {
			return CommissionTransferResult{}, ErrNotFound
		}
		return CommissionTransferResult{}, ErrInsufficientCommission
	}
	var transferred CommissionTransferResult
	if err := tx.QueryRowContext(ctx, `SELECT commission_balance, balance FROM users WHERE id = ?`, userID).
		Scan(&transferred.CommissionBalance, &transferred.Balance); err != nil {
		return CommissionTransferResult{}, fmt.Errorf("read commission transfer result: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CommissionTransferResult{}, fmt.Errorf("commit commission transfer: %w", err)
	}
	return transferred, nil
}

var (
	errCommissionCycle        = errors.New("cyclic_invitation_relationship")
	errCommissionBalanceLimit = errors.New("commission_recipient_balance_limit_exceeded")
)

type payableCommissionOrder struct {
	id, userID, amount, commission, inviterID int64
	tradeNo                                   string
}

func settleOrderCommissionTx(
	ctx context.Context,
	tx *sql.Tx,
	order payableCommissionOrder,
	levels []int,
	withdrawClosed bool,
	now time.Time,
) error {
	inviterID := order.inviterID
	actual := int64(0)
	seen := make(map[int64]struct{}, len(levels))
	for _, percentage := range levels {
		if inviterID < 1 {
			break
		}
		if _, duplicate := seen[inviterID]; duplicate {
			return errCommissionCycle
		}
		seen[inviterID] = struct{}{}
		var parentID sql.NullInt64
		var accountKind string
		if err := tx.QueryRowContext(ctx, `SELECT invite_user_id, account_kind FROM users WHERE id = ?`, inviterID).
			Scan(&parentID, &accountKind); errors.Is(err, sql.ErrNoRows) {
			break
		} else if err != nil {
			return fmt.Errorf("read commission recipient: %w", err)
		}
		if accountKind != AccountKindHuman {
			break
		}
		share := commissionShare(order.commission, percentage)
		if share > 0 {
			column := "commission_balance"
			if withdrawClosed {
				column = "balance"
			}
			updated, err := tx.ExecContext(ctx, `UPDATE users SET `+column+` = `+column+` + ?, updated_at = ? WHERE id = ? AND `+column+` <= ?`,
				share, now.Unix(), inviterID, maxOrderMoneyCents-share)
			if err != nil {
				return fmt.Errorf("credit commission recipient: %w", err)
			}
			count, err := updated.RowsAffected()
			if err != nil {
				return fmt.Errorf("count commission recipient update: %w", err)
			}
			if count != 1 {
				return errCommissionBalanceLimit
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO commission_logs (
					order_id, invite_user_id, user_id, trade_no, order_amount, get_amount, created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			`, order.id, inviterID, order.userID, order.tradeNo, order.amount, share, now.Unix(), now.Unix()); err != nil {
				return fmt.Errorf("record paid commission: %w", err)
			}
			actual += share
		}
		if parentID.Valid {
			inviterID = parentID.Int64
		} else {
			inviterID = 0
		}
	}
	updated, err := tx.ExecContext(ctx, `
		UPDATE orders
		SET commission_status = 2, actual_commission_balance = ?, updated_at = ?
		WHERE id = ? AND commission_status = 1
	`, actual, now.Unix(), order.id)
	if err != nil {
		return fmt.Errorf("complete commission payment: %w", err)
	}
	count, err := updated.RowsAffected()
	if err != nil || count != 1 {
		return fmt.Errorf("complete commission payment: stale state")
	}
	return nil
}

func (s *Store) ProcessCommissions(ctx context.Context, now time.Time, limit int) (CommissionProcessingResult, error) {
	if now.Unix() < 0 || limit < 1 || limit > maxCommissionBatchSize {
		return CommissionProcessingResult{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CommissionProcessingResult{}, fmt.Errorf("begin commission processing: %w", err)
	}
	defer tx.Rollback()

	var autoCheck, withdrawClosed, distributionEnabled bool
	var levelOne, levelTwo, levelThree int
	if err := tx.QueryRowContext(ctx, `
		SELECT commission_auto_check_enable, withdraw_close_enable, commission_distribution_enable,
		       commission_distribution_l1, commission_distribution_l2, commission_distribution_l3
		FROM app_settings WHERE id = 1
	`).Scan(&autoCheck, &withdrawClosed, &distributionEnabled, &levelOne, &levelTwo, &levelThree); err != nil {
		return CommissionProcessingResult{}, fmt.Errorf("read commission processing settings: %w", err)
	}
	if levelOne < 0 || levelTwo < 0 || levelThree < 0 || levelOne+levelTwo+levelThree > 100 {
		return CommissionProcessingResult{}, fmt.Errorf("%w: invalid commission distribution", ErrInvalidInput)
	}
	levels := []int{100}
	if distributionEnabled {
		levels = []int{levelOne, levelTwo, levelThree}
	}

	var processing CommissionProcessingResult
	if autoCheck {
		checked, err := tx.ExecContext(ctx, `
			UPDATE orders SET commission_status = 1, updated_at = ?
			WHERE id IN (
				SELECT id FROM orders
				WHERE status = ? AND commission_status = 0 AND invite_user_id IS NOT NULL
				  AND updated_at <= ?
				ORDER BY updated_at, id LIMIT ?
			)
		`, now.Unix(), OrderStatusCompleted, now.Add(-commissionConfirmationDelay).Unix(), limit)
		if err != nil {
			return CommissionProcessingResult{}, fmt.Errorf("confirm due commissions: %w", err)
		}
		processing.Checked, err = checked.RowsAffected()
		if err != nil {
			return CommissionProcessingResult{}, fmt.Errorf("count confirmed commissions: %w", err)
		}
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT o.id, o.user_id, o.trade_no, o.total_amount, o.commission_balance, o.invite_user_id
		FROM orders o
		WHERE o.status = ? AND o.commission_status = 1 AND o.invite_user_id IS NOT NULL
		  AND NOT EXISTS (
			  SELECT 1 FROM scheduler_quarantine q
			  WHERE q.kind = 'commission' AND q.entity_id = o.id AND q.retry_after > ?
		  )
		ORDER BY o.updated_at, o.id LIMIT ?
	`, OrderStatusCompleted, now.Unix(), limit)
	if err != nil {
		return CommissionProcessingResult{}, fmt.Errorf("list payable commissions: %w", err)
	}
	payable := make([]payableCommissionOrder, 0, limit)
	for rows.Next() {
		var item payableCommissionOrder
		if err := rows.Scan(&item.id, &item.userID, &item.tradeNo, &item.amount, &item.commission, &item.inviterID); err != nil {
			_ = rows.Close()
			return CommissionProcessingResult{}, fmt.Errorf("scan payable commission: %w", err)
		}
		payable = append(payable, item)
	}
	if err := rows.Close(); err != nil {
		return CommissionProcessingResult{}, fmt.Errorf("close payable commission rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return CommissionProcessingResult{}, fmt.Errorf("iterate payable commissions: %w", err)
	}

	retryAfterUnix := now.Add(schedulerQuarantineBackoff).Unix()
	for _, order := range payable {
		if _, err := tx.ExecContext(ctx, `SAVEPOINT sp_item`); err != nil {
			return CommissionProcessingResult{}, fmt.Errorf("create commission savepoint: %w", err)
		}
		err := settleOrderCommissionTx(ctx, tx, order, levels, withdrawClosed, now)
		if err != nil {
			var quarantineReason string
			switch {
			case errors.Is(err, errCommissionCycle):
				quarantineReason = "cyclic_invitation_relationship"
			case errors.Is(err, errCommissionBalanceLimit):
				quarantineReason = "commission_balance_limit_exceeded"
			}
			if quarantineReason == "" {
				return CommissionProcessingResult{}, err
			}
			if _, rbErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT sp_item`); rbErr != nil {
				return CommissionProcessingResult{}, fmt.Errorf("rollback commission savepoint: %w", rbErr)
			}
			if _, relErr := tx.ExecContext(ctx, `RELEASE SAVEPOINT sp_item`); relErr != nil {
				return CommissionProcessingResult{}, fmt.Errorf("release commission savepoint after rollback: %w", relErr)
			}
			if _, qErr := tx.ExecContext(ctx, `
				INSERT INTO scheduler_quarantine (kind, entity_id, reason, attempts, first_failed_at, last_failed_at, retry_after)
				VALUES ('commission', ?, ?, 1, ?, ?, ?)
				ON CONFLICT (kind, entity_id) DO UPDATE SET
					reason = excluded.reason,
					attempts = scheduler_quarantine.attempts + 1,
					last_failed_at = excluded.last_failed_at,
					retry_after = excluded.retry_after
			`, order.id, quarantineReason, now.Unix(), now.Unix(), retryAfterUnix); qErr != nil {
				return CommissionProcessingResult{}, fmt.Errorf("quarantine commission order %d: %w", order.id, qErr)
			}
			processing.Quarantined++
			processing.QuarantinedOrders = append(processing.QuarantinedOrders, SchedulerQuarantineEvent{
				OrderID: order.id,
				Reason:  quarantineReason,
			})
			continue
		}
		if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT sp_item`); err != nil {
			return CommissionProcessingResult{}, fmt.Errorf("release commission savepoint: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM scheduler_quarantine WHERE kind = 'commission' AND entity_id = ?`, order.id); err != nil {
			return CommissionProcessingResult{}, fmt.Errorf("clear commission quarantine %d: %w", order.id, err)
		}
		processing.Paid++
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM orders o
		WHERE o.status = ? AND o.commission_status = 1 AND o.invite_user_id IS NOT NULL
		  AND NOT EXISTS (
			  SELECT 1 FROM scheduler_quarantine q
			  WHERE q.kind = 'commission' AND q.entity_id = o.id AND q.retry_after > ?
		  )
	`, OrderStatusCompleted, now.Unix()).Scan(&processing.Remaining); err != nil {
		return CommissionProcessingResult{}, fmt.Errorf("count remaining commissions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CommissionProcessingResult{}, fmt.Errorf("commit commission processing: %w", err)
	}
	return processing, nil
}

func commissionShare(amount int64, percentage int) int64 {
	if amount <= 0 || percentage <= 0 {
		return 0
	}
	return amount * int64(percentage) / 100
}
