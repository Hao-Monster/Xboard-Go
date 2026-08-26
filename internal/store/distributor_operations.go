package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	distributorHWIDPattern  = regexp.MustCompile(`^[A-Za-z0-9=-]{10,64}$`)
	distributorClaimPattern = regexp.MustCompile(`^[A-Za-z0-9]{64}$`)
)

func (s *Store) PreviewDistributorSettlement(ctx context.Context, distributorID int64) (DistributorSettlementSummary, error) {
	if distributorID < 1 {
		return DistributorSettlementSummary{}, ErrInvalidInput
	}
	if err := requireDistributorRole(ctx, s.db, distributorID); err != nil {
		return DistributorSettlementSummary{}, err
	}
	var result DistributorSettlementSummary
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(o.id),COALESCE(SUM(o.total_amount),0)
		FROM orders o
		JOIN distributor_subscriptions ds ON ds.id = o.distributor_order_id
		WHERE o.user_id = ? AND ds.distributor_user_id = ? AND o.status = ? AND o.paid_at IS NULL
	`, distributorID, distributorID, OrderStatusCompleted).Scan(&result.Count, &result.TotalAmount); err != nil {
		return DistributorSettlementSummary{}, fmt.Errorf("preview distributor settlement: %w", err)
	}
	return result, nil
}

func (s *Store) SettleDistributorOrders(ctx context.Context, distributorID, administratorID int64, now time.Time) (DistributorSettlementSummary, error) {
	if distributorID < 1 || administratorID < 1 || now.Unix() < 0 {
		return DistributorSettlementSummary{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DistributorSettlementSummary{}, fmt.Errorf("begin distributor settlement: %w", err)
	}
	defer tx.Rollback()
	if err := requireDistributorRole(ctx, tx, distributorID); err != nil {
		return DistributorSettlementSummary{}, err
	}
	var administrator bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM users WHERE id = ? AND account_kind = 'human' AND is_admin = 1 AND banned = 0)
	`, administratorID).Scan(&administrator); err != nil {
		return DistributorSettlementSummary{}, fmt.Errorf("validate settlement administrator: %w", err)
	}
	if !administrator {
		return DistributorSettlementSummary{}, ErrNotFound
	}
	var result DistributorSettlementSummary
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(o.id),COALESCE(SUM(o.total_amount),0)
		FROM orders o
		JOIN distributor_subscriptions ds ON ds.id = o.distributor_order_id
		WHERE o.user_id = ? AND ds.distributor_user_id = ? AND o.status = ? AND o.paid_at IS NULL
	`, distributorID, distributorID, OrderStatusCompleted).Scan(&result.Count, &result.TotalAmount); err != nil {
		return DistributorSettlementSummary{}, fmt.Errorf("aggregate distributor settlement: %w", err)
	}
	if result.Count == 0 {
		if err := tx.Commit(); err != nil {
			return DistributorSettlementSummary{}, fmt.Errorf("commit empty distributor settlement: %w", err)
		}
		return result, nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE distributor_subscriptions
		SET settlement_status = ?,settled_at = ?,settled_by = ?,revision = revision + 1,updated_at = ?
		WHERE distributor_user_id = ? AND original_order_id IN (
			SELECT o.id FROM orders o
			WHERE o.user_id = ? AND o.status = ? AND o.paid_at IS NULL
			  AND o.distributor_order_id = distributor_subscriptions.id
		)
	`, DistributorSettlementSettled, now.Unix(), administratorID, now.Unix(), distributorID, distributorID, OrderStatusCompleted); err != nil {
		return DistributorSettlementSummary{}, fmt.Errorf("synchronize distributor settlement: %w", err)
	}
	updated, err := tx.ExecContext(ctx, `
		UPDATE orders
		SET paid_at = ?,distributor_settled_by = ?,updated_at = ?
		WHERE user_id = ? AND status = ? AND paid_at IS NULL
		  AND distributor_order_id IN (SELECT id FROM distributor_subscriptions WHERE distributor_user_id = ?)
	`, now.Unix(), administratorID, now.Unix(), distributorID, OrderStatusCompleted, distributorID)
	if err != nil {
		return DistributorSettlementSummary{}, fmt.Errorf("settle distributor orders: %w", err)
	}
	count, err := updated.RowsAffected()
	if err != nil {
		return DistributorSettlementSummary{}, fmt.Errorf("count settled distributor orders: %w", err)
	}
	if count != result.Count {
		return DistributorSettlementSummary{}, fmt.Errorf("settle distributor orders: expected %d rows, updated %d", result.Count, count)
	}
	settledAt := now.UTC()
	result.SettledAt = &settledAt
	if err := tx.Commit(); err != nil {
		return DistributorSettlementSummary{}, fmt.Errorf("commit distributor settlement: %w", err)
	}
	return result, nil
}

func (s *Store) UpdateDistributorRemark(ctx context.Context, orderID int64, remark *string, now time.Time) (*string, error) {
	if orderID < 1 || now.Unix() < 0 {
		return nil, ErrInvalidInput
	}
	normalized, err := normalizeOptionalDistributorText(remark, 500)
	if err != nil {
		return nil, err
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE distributor_subscriptions
		SET remark = ?,revision = revision + 1,updated_at = ?
		WHERE id = (SELECT distributor_order_id FROM orders WHERE id = ?)
	`, nullableStringValue(normalized), now.Unix(), orderID)
	if err != nil {
		return nil, fmt.Errorf("update distributor remark: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("count distributor remark update: %w", err)
	}
	if changed != 1 {
		return nil, ErrNotFound
	}
	return normalized, nil
}

func (s *Store) GetDistributorEntitlement(ctx context.Context, orderID int64) (DistributorEntitlement, error) {
	if orderID < 1 {
		return DistributorEntitlement{}, ErrInvalidInput
	}
	return readDistributorEntitlement(ctx, s.db, orderID)
}

func (s *Store) UpdateDistributorEntitlement(ctx context.Context, orderID int64, input UpdateDistributorEntitlementInput, now time.Time) (DistributorEntitlement, error) {
	if orderID < 1 || input.TransferEnable < 0 || input.SpeedLimit < 0 || input.SpeedLimit > 1_000_000_000 ||
		input.DeviceLimit < 0 || input.DeviceLimit > 1_000 || now.Unix() < 0 || input.ExpiredAt != nil && input.ExpiredAt.Unix() < 0 {
		return DistributorEntitlement{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DistributorEntitlement{}, fmt.Errorf("begin distributor entitlement update: %w", err)
	}
	defer tx.Rollback()
	var subscriberID int64
	if err := tx.QueryRowContext(ctx, `
		SELECT ds.subscriber_user_id
		FROM orders o JOIN distributor_subscriptions ds ON ds.id = o.distributor_order_id
		WHERE o.id = ?
	`, orderID).Scan(&subscriberID); errors.Is(err, sql.ErrNoRows) {
		return DistributorEntitlement{}, ErrNotFound
	} else if err != nil {
		return DistributorEntitlement{}, fmt.Errorf("find distributor entitlement: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE users SET transfer_enable = ?,expired_at = ?,speed_limit = ?,device_limit = ?,
			admin_revision = admin_revision + 1,updated_at = ?
		WHERE id = ? AND account_kind = ?
	`, input.TransferEnable, nullableTimeUnix(input.ExpiredAt), input.SpeedLimit, input.DeviceLimit,
		now.Unix(), subscriberID, AccountKindInternalSubscription)
	if err != nil {
		return DistributorEntitlement{}, fmt.Errorf("update distributor entitlement: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		if err != nil {
			return DistributorEntitlement{}, fmt.Errorf("count distributor entitlement update: %w", err)
		}
		return DistributorEntitlement{}, ErrNotFound
	}
	value, err := readDistributorEntitlement(ctx, tx, orderID)
	if err != nil {
		return DistributorEntitlement{}, err
	}
	if err := tx.Commit(); err != nil {
		return DistributorEntitlement{}, fmt.Errorf("commit distributor entitlement update: %w", err)
	}
	return value, nil
}

func readDistributorEntitlement(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, orderID int64) (DistributorEntitlement, error) {
	var value DistributorEntitlement
	var expiredAt sql.NullInt64
	var upload, download int64
	err := query.QueryRowContext(ctx, `
		SELECT p.id,p.name,u.transfer_enable,u.traffic_u,u.traffic_d,u.expired_at,u.speed_limit,u.device_limit
		FROM orders o
		JOIN distributor_subscriptions ds ON ds.id = o.distributor_order_id
		JOIN users u ON u.id = ds.subscriber_user_id
		JOIN plans p ON p.id = u.plan_id
		WHERE o.id = ? AND u.account_kind = ?
	`, orderID, AccountKindInternalSubscription).Scan(&value.PlanID, &value.PlanName, &value.TransferEnable,
		&upload, &download, &expiredAt, &value.SpeedLimit, &value.DeviceLimit)
	if errors.Is(err, sql.ErrNoRows) {
		return DistributorEntitlement{}, ErrNotFound
	}
	if err != nil {
		return DistributorEntitlement{}, fmt.Errorf("read distributor entitlement: %w", err)
	}
	if upload > math.MaxInt64-download {
		value.UsedTraffic = math.MaxInt64
	} else {
		value.UsedTraffic = upload + download
	}
	value.RemainingTraffic = maxInt64(0, value.TransferEnable-minInt64(value.TransferEnable, value.UsedTraffic))
	value.ExpiredAt = nullableUnixTime(expiredAt)
	return value, nil
}

func (s *Store) UpdateDistributorHWIDSettings(ctx context.Context, orderID int64, enabled bool, limit int, now time.Time) (DistributorHWIDSettings, error) {
	if orderID < 1 || limit < 1 || limit > 100 || now.Unix() < 0 {
		return DistributorHWIDSettings{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE distributor_subscriptions
		SET hwid_enabled = ?,hwid_limit = ?,revision = revision + 1,updated_at = ?
		WHERE id = (SELECT distributor_order_id FROM orders WHERE id = ?)
	`, enabled, limit, now.Unix(), orderID)
	if err != nil {
		return DistributorHWIDSettings{}, fmt.Errorf("update distributor HWID settings: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		if err != nil {
			return DistributorHWIDSettings{}, fmt.Errorf("count distributor HWID settings update: %w", err)
		}
		return DistributorHWIDSettings{}, ErrNotFound
	}
	return s.getDistributorHWIDSettings(ctx, orderID)
}

func (s *Store) GetDistributorHWIDSettings(ctx context.Context, orderID int64) (DistributorHWIDSettings, error) {
	if orderID < 1 {
		return DistributorHWIDSettings{}, ErrInvalidInput
	}
	return s.getDistributorHWIDSettings(ctx, orderID)
}

func (s *Store) getDistributorHWIDSettings(ctx context.Context, orderID int64) (DistributorHWIDSettings, error) {
	var value DistributorHWIDSettings
	err := s.db.QueryRowContext(ctx, `
		SELECT ds.hwid_enabled,ds.hwid_limit,COUNT(d.id)
		FROM orders o
		JOIN distributor_subscriptions ds ON ds.id = o.distributor_order_id
		LEFT JOIN distributor_hwid_devices d ON d.subscription_id = ds.id
		WHERE o.id = ? GROUP BY ds.id
	`, orderID).Scan(&value.Enabled, &value.Limit, &value.RegisteredCount)
	if errors.Is(err, sql.ErrNoRows) {
		return DistributorHWIDSettings{}, ErrNotFound
	}
	if err != nil {
		return DistributorHWIDSettings{}, fmt.Errorf("read distributor HWID settings: %w", err)
	}
	return value, nil
}

func (s *Store) AuthorizeDistributorHWID(ctx context.Context, input AuthorizeDistributorHWIDInput, now time.Time) (DistributorHWIDAuthorization, error) {
	if input.SubscriberUserID < 1 || now.Unix() < 0 {
		return DistributorHWIDAuthorization{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DistributorHWIDAuthorization{}, fmt.Errorf("begin distributor HWID authorization: %w", err)
	}
	defer tx.Rollback()
	var result DistributorHWIDAuthorization
	var limit int
	err = tx.QueryRowContext(ctx, `
		SELECT ds.id,root.trade_no,ds.hwid_enabled,ds.hwid_limit
		FROM distributor_subscriptions ds JOIN orders root ON root.id = ds.original_order_id
		WHERE ds.subscriber_user_id = ?
	`, input.SubscriberUserID).Scan(&result.SubscriptionID, &result.OriginalTradeNo, &result.Enabled, &limit)
	if errors.Is(err, sql.ErrNoRows) {
		return DistributorHWIDAuthorization{Allowed: true}, nil
	}
	if err != nil {
		return DistributorHWIDAuthorization{}, fmt.Errorf("find distributor HWID subscription: %w", err)
	}
	if !result.Enabled {
		result.Allowed = true
		return result, nil
	}
	input.HWID = strings.TrimSpace(input.HWID)
	if !distributorHWIDPattern.MatchString(input.HWID) {
		result.NotSupported = true
		return result, nil
	}
	deviceOS := optionalTruncatedText(input.DeviceOS, 100)
	osVersion := optionalTruncatedText(input.OSVersion, 100)
	deviceModel := optionalTruncatedText(input.DeviceModel, 150)
	userAgent := optionalTruncatedText(input.UserAgent, 255)
	ipAddress := optionalTruncatedText(input.IPAddress, 45)
	var deviceID int64
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM distributor_hwid_devices WHERE subscription_id = ? AND hwid = ?
	`, result.SubscriptionID, input.HWID).Scan(&deviceID)
	if err == nil {
		if _, err := tx.ExecContext(ctx, `
			UPDATE distributor_hwid_devices
			SET device_os = COALESCE(?,device_os),os_version = COALESCE(?,os_version),
				device_model = COALESCE(?,device_model),user_agent = COALESCE(?,user_agent),
				ip_address = COALESCE(?,ip_address),last_seen_at = ? WHERE id = ?
		`, nullableStringValue(deviceOS), nullableStringValue(osVersion), nullableStringValue(deviceModel),
			nullableStringValue(userAgent), nullableStringValue(ipAddress), now.Unix(), deviceID); err != nil {
			return DistributorHWIDAuthorization{}, fmt.Errorf("refresh distributor HWID: %w", err)
		}
		result.Allowed = true
		if err := tx.Commit(); err != nil {
			return DistributorHWIDAuthorization{}, fmt.Errorf("commit distributor HWID refresh: %w", err)
		}
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DistributorHWIDAuthorization{}, fmt.Errorf("read distributor HWID: %w", err)
	}
	var registered int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM distributor_hwid_devices WHERE subscription_id = ?`, result.SubscriptionID).Scan(&registered); err != nil {
		return DistributorHWIDAuthorization{}, fmt.Errorf("count distributor HWIDs: %w", err)
	}
	if registered >= limit {
		result.LimitReached = true
		return result, nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO distributor_hwid_devices (
			subscription_id,hwid,device_os,os_version,device_model,user_agent,ip_address,first_seen_at,last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, result.SubscriptionID, input.HWID, nullableStringValue(deviceOS), nullableStringValue(osVersion),
		nullableStringValue(deviceModel), nullableStringValue(userAgent), nullableStringValue(ipAddress), now.Unix(), now.Unix()); err != nil {
		return DistributorHWIDAuthorization{}, fmt.Errorf("register distributor HWID: %w", err)
	}
	result.Allowed = true
	if err := tx.Commit(); err != nil {
		return DistributorHWIDAuthorization{}, fmt.Errorf("commit distributor HWID registration: %w", err)
	}
	return result, nil
}

func (s *Store) ListDistributorHWIDDevices(ctx context.Context, orderID int64, search string) ([]DistributorHWIDDevice, error) {
	search = strings.TrimSpace(search)
	if orderID < 1 || utf8.RuneCountInString(search) > 64 {
		return nil, ErrInvalidInput
	}
	query := `
		SELECT d.id,d.hwid,d.device_os,d.os_version,d.device_model,d.user_agent,d.ip_address,d.first_seen_at,d.last_seen_at
		FROM distributor_hwid_devices d
		JOIN distributor_subscriptions ds ON ds.id = d.subscription_id
		JOIN orders o ON o.distributor_order_id = ds.id
		WHERE o.id = ?`
	args := []any{orderID}
	if search != "" {
		query += ` AND d.hwid LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(search)+"%")
	}
	query += ` ORDER BY d.last_seen_at DESC,d.id DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list distributor HWIDs: %w", err)
	}
	defer rows.Close()
	devices := make([]DistributorHWIDDevice, 0)
	for rows.Next() {
		var value DistributorHWIDDevice
		var deviceOS, osVersion, deviceModel, userAgent, ipAddress sql.NullString
		var firstSeenAt, lastSeenAt int64
		if err := rows.Scan(&value.ID, &value.HWID, &deviceOS, &osVersion, &deviceModel, &userAgent, &ipAddress, &firstSeenAt, &lastSeenAt); err != nil {
			return nil, fmt.Errorf("scan distributor HWID: %w", err)
		}
		value.DeviceOS = nullableStringPointer(deviceOS)
		value.OSVersion = nullableStringPointer(osVersion)
		value.DeviceModel = nullableStringPointer(deviceModel)
		value.UserAgent = nullableStringPointer(userAgent)
		value.IPAddress = nullableStringPointer(ipAddress)
		value.FirstSeenAt = time.Unix(firstSeenAt, 0).UTC()
		value.LastSeenAt = time.Unix(lastSeenAt, 0).UTC()
		devices = append(devices, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate distributor HWIDs: %w", err)
	}
	return devices, nil
}

func (s *Store) DeleteDistributorHWIDDevice(ctx context.Context, orderID, deviceID int64) (bool, error) {
	if orderID < 1 || deviceID < 1 {
		return false, ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM distributor_hwid_devices
		WHERE id = ? AND subscription_id = (
			SELECT distributor_order_id FROM orders WHERE id = ?
		)
	`, deviceID, orderID)
	if err != nil {
		return false, fmt.Errorf("delete distributor HWID: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count deleted distributor HWID: %w", err)
	}
	return changed == 1, nil
}

func (s *Store) ClaimDistributorSubscription(ctx context.Context, token, ipAddress, userAgent string, now time.Time) (DistributorClaim, error) {
	if !distributorClaimPattern.MatchString(token) || now.Unix() < 0 {
		return DistributorClaim{}, ErrNotFound
	}
	digest := sha256.Sum256([]byte(token))
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DistributorClaim{}, fmt.Errorf("begin distributor claim: %w", err)
	}
	defer tx.Rollback()
	var result DistributorClaim
	var status DistributorDeliveryStatus
	err = tx.QueryRowContext(ctx, `
		SELECT ds.id,ds.delivery_status,u.subscription_token,o.trade_no
		FROM distributor_subscriptions ds
		JOIN users u ON u.id = ds.subscriber_user_id
		JOIN orders o ON o.id = ds.original_order_id
		WHERE ds.claim_token_hash = ?
	`, hex.EncodeToString(digest[:])).Scan(&result.SubscriptionID, &status, &result.SubscriptionToken, &result.OriginalTradeNo)
	if errors.Is(err, sql.ErrNoRows) {
		return DistributorClaim{}, ErrNotFound
	}
	if err != nil {
		return DistributorClaim{}, fmt.Errorf("read distributor claim: %w", err)
	}
	if status != DistributorDeliveryPending {
		return DistributorClaim{}, ErrDistributorClaimConsumed
	}
	ip := optionalTruncatedText(ipAddress, 45)
	ua := optionalTruncatedText(userAgent, 255)
	updated, err := tx.ExecContext(ctx, `
		UPDATE distributor_subscriptions
		SET delivery_status = ?,claimed_at = ?,claim_ip = ?,claim_user_agent = ?,
			revision = revision + 1,updated_at = ?
		WHERE id = ? AND delivery_status = ?
	`, DistributorDeliveryClaimed, now.Unix(), nullableStringValue(ip), nullableStringValue(ua),
		now.Unix(), result.SubscriptionID, DistributorDeliveryPending)
	if err != nil {
		return DistributorClaim{}, fmt.Errorf("consume distributor claim: %w", err)
	}
	if changed, err := updated.RowsAffected(); err != nil || changed != 1 {
		if err != nil {
			return DistributorClaim{}, fmt.Errorf("count consumed distributor claim: %w", err)
		}
		return DistributorClaim{}, ErrDistributorClaimConsumed
	}
	if err := tx.Commit(); err != nil {
		return DistributorClaim{}, fmt.Errorf("commit distributor claim: %w", err)
	}
	return result, nil
}

func (s *Store) MarkDistributorSubscriptionClaimed(ctx context.Context, subscriptionID int64, ipAddress, userAgent string, now time.Time) error {
	if subscriptionID < 1 || now.Unix() < 0 {
		return ErrInvalidInput
	}
	ip := optionalTruncatedText(ipAddress, 45)
	ua := optionalTruncatedText(userAgent, 255)
	defer s.lockWrite()()
	_, err := s.db.ExecContext(ctx, `
		UPDATE distributor_subscriptions
		SET delivery_status = ?,claimed_at = COALESCE(claimed_at,?),claim_ip = ?,claim_user_agent = ?,
			revision = revision + 1,updated_at = ?
		WHERE id = ? AND delivery_status = ?
	`, DistributorDeliveryClaimed, now.Unix(), nullableStringValue(ip), nullableStringValue(ua),
		now.Unix(), subscriptionID, DistributorDeliveryPending)
	if err != nil {
		return fmt.Errorf("mark distributor subscription claimed: %w", err)
	}
	return nil
}

func (s *Store) MarkDistributorConfigIssued(ctx context.Context, subscriptionID int64, now time.Time) error {
	if subscriptionID < 1 || now.Unix() < 0 {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	_, err := s.db.ExecContext(ctx, `
		UPDATE distributor_subscriptions
		SET config_issued_at = COALESCE(config_issued_at,?),revision = revision + 1,updated_at = ?
		WHERE id = ? AND delivery_status = ?
	`, now.Unix(), now.Unix(), subscriptionID, DistributorDeliveryClaimed)
	if err != nil {
		return fmt.Errorf("mark distributor config issued: %w", err)
	}
	return nil
}

func requireDistributorRole(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID int64) error {
	var exists bool
	if err := query.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM users WHERE id = ? AND account_kind = 'human' AND is_distributor = 1)
	`, userID).Scan(&exists); err != nil {
		return fmt.Errorf("validate distributor role: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func optionalTruncatedText(value string, maximum int) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	runes := []rune(value)
	if len(runes) > maximum {
		runes = runes[:maximum]
	}
	result := string(runes)
	return &result
}
