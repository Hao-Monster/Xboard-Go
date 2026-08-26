package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *Store) ListDistributorOptions(ctx context.Context) ([]AdminUser, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,email,is_admin,is_staff,is_distributor,distributor_name,banned,group_id,transfer_enable,traffic_u,traffic_d,
		       expired_at,speed_limit,device_limit,online_count,last_online_at,last_login_at,admin_revision,created_at,updated_at
		FROM users
		WHERE account_kind = 'human' AND is_distributor = 1
		ORDER BY banned ASC,COALESCE(NULLIF(distributor_name,''),email) COLLATE NOCASE,id
	`)
	if err != nil {
		return nil, fmt.Errorf("list distributor options: %w", err)
	}
	defer rows.Close()
	result := make([]AdminUser, 0)
	for rows.Next() {
		user, err := scanAdminUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan distributor option: %w", err)
		}
		result = append(result, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate distributor options: %w", err)
	}
	return result, nil
}

func (s *Store) GetDistributorOrderByID(ctx context.Context, orderID int64, now time.Time) (DistributorOrder, error) {
	if orderID < 1 || now.Unix() < 0 {
		return DistributorOrder{}, ErrInvalidInput
	}
	var distributorID int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT user_id FROM orders WHERE id = ? AND distributor_order_id IS NOT NULL
	`, orderID).Scan(&distributorID); errors.Is(err, sql.ErrNoRows) {
		return DistributorOrder{}, ErrNotFound
	} else if err != nil {
		return DistributorOrder{}, fmt.Errorf("find distributor order owner: %w", err)
	}
	value, err := readDistributorOrder(ctx, s.db, distributorID, orderID)
	if err != nil {
		return DistributorOrder{}, err
	}
	return s.enrichDistributorOrder(ctx, value, now)
}

func (s *Store) GetDistributorOrderByTradeNo(ctx context.Context, distributorID int64, tradeNo string, now time.Time) (DistributorOrder, error) {
	if distributorID < 1 || !validTradeNo(tradeNo) || now.Unix() < 0 {
		return DistributorOrder{}, ErrInvalidInput
	}
	var orderID int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT id FROM orders WHERE user_id = ? AND trade_no = ? AND distributor_order_id IS NOT NULL
	`, distributorID, tradeNo).Scan(&orderID); errors.Is(err, sql.ErrNoRows) {
		return DistributorOrder{}, ErrNotFound
	} else if err != nil {
		return DistributorOrder{}, fmt.Errorf("find distributor order: %w", err)
	}
	value, err := readDistributorOrder(ctx, s.db, distributorID, orderID)
	if err != nil {
		return DistributorOrder{}, err
	}
	return s.enrichDistributorOrder(ctx, value, now)
}

func (s *Store) ListDistributorOrders(ctx context.Context, filter DistributorOrderFilter, now time.Time) (DistributorOrderPage, error) {
	if now.Unix() < 0 || filter.Page < 0 || filter.PageSize < 0 || filter.DistributorUserID != nil && *filter.DistributorUserID < 1 {
		return DistributorOrderPage{}, ErrInvalidInput
	}
	if filter.Page == 0 {
		filter.Page = 1
	}
	if filter.PageSize == 0 {
		filter.PageSize = 20
	}
	if filter.PageSize > 200 {
		filter.PageSize = 200
	}
	filter.Search = strings.TrimSpace(filter.Search)
	if !utf8.ValidString(filter.Search) || utf8.RuneCountInString(filter.Search) > 512 {
		return DistributorOrderPage{}, ErrInvalidInput
	}
	where, args := distributorOrderWhere(filter)
	var total int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM orders o
		JOIN distributor_subscriptions ds ON ds.id = o.distributor_order_id
		JOIN orders root ON root.id = ds.original_order_id
		JOIN users subscriber ON subscriber.id = ds.subscriber_user_id
	`+where, args...).Scan(&total); err != nil {
		return DistributorOrderPage{}, fmt.Errorf("count distributor orders: %w", err)
	}
	page := DistributorOrderPage{Items: []DistributorOrder{}, Total: total, Page: filter.Page, PageSize: filter.PageSize}
	if total == 0 {
		return page, nil
	}
	queryArgs := append(append([]any{}, args...), filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+orderColumns+`, distributor.email, p.name
		FROM orders o
		JOIN distributor_subscriptions ds ON ds.id = o.distributor_order_id
		JOIN orders root ON root.id = ds.original_order_id
		JOIN users subscriber ON subscriber.id = ds.subscriber_user_id
		JOIN users distributor ON distributor.id = ds.distributor_user_id
		JOIN plans p ON p.id = o.plan_id
	`+where+` ORDER BY root.created_at DESC,root.id DESC,
		CASE WHEN o.id = root.id THEN 0 ELSE 1 END,o.created_at DESC,o.id DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return DistributorOrderPage{}, fmt.Errorf("list distributor orders: %w", err)
	}
	for rows.Next() {
		var distributorEmail, planName string
		order, err := scanOrderWithAdminFields(rows, &distributorEmail, &planName)
		if err != nil {
			return DistributorOrderPage{}, err
		}
		page.Items = append(page.Items, DistributorOrder{Order: order, PlanName: planName, DistributorEmail: distributorEmail})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return DistributorOrderPage{}, fmt.Errorf("iterate distributor orders: %w", err)
	}
	if err := rows.Close(); err != nil {
		return DistributorOrderPage{}, fmt.Errorf("close distributor orders: %w", err)
	}
	page.Items, err = s.enrichDistributorOrders(ctx, page.Items, now)
	if err != nil {
		return DistributorOrderPage{}, err
	}
	return page, nil
}

type distributorOrderEnrichment struct {
	subscription     DistributorSubscription
	entitlement      DistributorEntitlement
	distributorEmail string
	distributorName  string
	boundDevices     []string
	subscriberBanned bool
	planRenew        bool
	planShow         bool
}

func (s *Store) enrichDistributorOrders(ctx context.Context, items []DistributorOrder, now time.Time) ([]DistributorOrder, error) {
	if len(items) == 0 {
		return items, nil
	}
	ids := make([]int64, 0, len(items))
	seen := make(map[int64]struct{}, len(items))
	for _, item := range items {
		if item.Order.DistributorOrderID == nil {
			return nil, ErrNotFound
		}
		id := *item.Order.DistributorOrderID
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for index, id := range ids {
		args[index] = id
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT ds.id,ds.original_order_id,root.trade_no,ds.distributor_user_id,ds.subscriber_user_id,
		       ds.customer_name,ds.remark,ds.delivery_status,ds.settlement_status,ds.config_issued_at,
		       ds.connected_at,ds.connected_node_id,ds.connected_node_name,ds.claimed_at,ds.closed_at,
		       ds.hwid_enabled,ds.hwid_limit,ds.revision,ds.created_at,ds.updated_at,
		       subscriber.transfer_enable,subscriber.traffic_u,subscriber.traffic_d,subscriber.expired_at,
		       subscriber.speed_limit,subscriber.device_limit,subscriber.banned,
		       p.id,p.name,p.renew,p.show,distributor.email,distributor.distributor_name
		FROM distributor_subscriptions ds
		JOIN orders root ON root.id = ds.original_order_id
		JOIN users subscriber ON subscriber.id = ds.subscriber_user_id
		JOIN users distributor ON distributor.id = ds.distributor_user_id
		JOIN plans p ON p.id = subscriber.plan_id
		WHERE ds.id IN (`+placeholders+`)
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("batch enrich distributor orders: %w", err)
	}
	enrichments := make(map[int64]*distributorOrderEnrichment, len(ids))
	for rows.Next() {
		value := &distributorOrderEnrichment{boundDevices: []string{}}
		var customerName, remark, connectedNodeName, distributorName sql.NullString
		var configIssuedAt, connectedAt, connectedNodeID, claimedAt, closedAt, expiredAt sql.NullInt64
		var createdAt, updatedAt, upload, download int64
		if err := rows.Scan(
			&value.subscription.ID, &value.subscription.OriginalOrderID, &value.subscription.OriginalTradeNo,
			&value.subscription.DistributorUserID, &value.subscription.SubscriberUserID, &customerName, &remark,
			&value.subscription.DeliveryStatus, &value.subscription.SettlementStatus, &configIssuedAt, &connectedAt,
			&connectedNodeID, &connectedNodeName, &claimedAt, &closedAt, &value.subscription.HWIDEnabled,
			&value.subscription.HWIDLimit, &value.subscription.Revision, &createdAt, &updatedAt,
			&value.entitlement.TransferEnable, &upload, &download, &expiredAt, &value.entitlement.SpeedLimit,
			&value.entitlement.DeviceLimit, &value.subscriberBanned, &value.entitlement.PlanID,
			&value.entitlement.PlanName, &value.planRenew, &value.planShow, &value.distributorEmail, &distributorName,
		); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan batched distributor order: %w", err)
		}
		value.subscription.CustomerName = nullableStringPointer(customerName)
		value.subscription.Remark = nullableStringPointer(remark)
		value.subscription.ConfigIssuedAt = nullableUnixTime(configIssuedAt)
		value.subscription.ConnectedAt = nullableUnixTime(connectedAt)
		value.subscription.ConnectedNodeID = nullableInt64Pointer(connectedNodeID)
		value.subscription.ConnectedNodeName = nullableStringPointer(connectedNodeName)
		value.subscription.ClaimedAt = nullableUnixTime(claimedAt)
		value.subscription.ClosedAt = nullableUnixTime(closedAt)
		value.subscription.CreatedAt = time.Unix(createdAt, 0).UTC()
		value.subscription.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		value.distributorName = value.distributorEmail
		if distributorName.Valid {
			value.distributorName = distributorName.String
		}
		value.entitlement.UsedTraffic = safeTrafficTotal(upload, download)
		value.entitlement.RemainingTraffic = maxInt64(0, value.entitlement.TransferEnable-minInt64(value.entitlement.TransferEnable, value.entitlement.UsedTraffic))
		value.entitlement.ExpiredAt = nullableUnixTime(expiredAt)
		enrichments[value.subscription.ID] = value
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate batched distributor orders: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close batched distributor orders: %w", err)
	}
	if len(enrichments) != len(ids) {
		return nil, ErrNotFound
	}
	deviceRows, err := s.db.QueryContext(ctx, `
		SELECT subscription_id,hwid,device_model FROM distributor_hwid_devices
		WHERE subscription_id IN (`+placeholders+`) ORDER BY subscription_id,last_seen_at DESC,id DESC
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("batch list distributor device labels: %w", err)
	}
	for deviceRows.Next() {
		var subscriptionID int64
		var hwid string
		var model sql.NullString
		if err := deviceRows.Scan(&subscriptionID, &hwid, &model); err != nil {
			_ = deviceRows.Close()
			return nil, fmt.Errorf("scan batched distributor device label: %w", err)
		}
		value := enrichments[subscriptionID]
		if value == nil {
			continue
		}
		label := hwid
		if model.Valid && strings.TrimSpace(model.String) != "" {
			label = strings.TrimSpace(model.String) + " " + hwid
		}
		value.boundDevices = append(value.boundDevices, label)
	}
	if err := deviceRows.Err(); err != nil {
		_ = deviceRows.Close()
		return nil, fmt.Errorf("iterate batched distributor device labels: %w", err)
	}
	if err := deviceRows.Close(); err != nil {
		return nil, fmt.Errorf("close batched distributor device labels: %w", err)
	}
	for index := range items {
		value := enrichments[*items[index].Order.DistributorOrderID]
		items[index].Subscription = value.subscription
		items[index].Entitlement = value.entitlement
		items[index].PlanName = value.entitlement.PlanName
		items[index].DistributorEmail = value.distributorEmail
		items[index].DistributorName = value.distributorName
		items[index].BoundDevices = append([]string{}, value.boundDevices...)
		items[index].IsSubscriptionOrigin = items[index].Order.ID == value.subscription.OriginalOrderID
		items[index].CanViewSubscriptionQR = items[index].IsSubscriptionOrigin
		items[index].CanRenew = items[index].IsSubscriptionOrigin && value.entitlement.ExpiredAt != nil && !value.subscriberBanned &&
			value.subscription.DeliveryStatus != DistributorDeliveryClosed && value.planRenew &&
			(value.planShow || value.entitlement.ExpiredAt.After(now))
		items[index].SettlementStatus = value.subscription.SettlementStatus
		if items[index].Order.PaidAt != nil {
			items[index].SettlementStatus = DistributorSettlementSettled
		}
	}
	return items, nil
}

func safeTrafficTotal(upload, download int64) int64 {
	if upload < 0 || download < 0 || upload > int64(^uint64(0)>>1)-download {
		return int64(^uint64(0) >> 1)
	}
	return upload + download
}

func distributorOrderWhere(filter DistributorOrderFilter) (string, []any) {
	where := ` WHERE o.distributor_order_id IS NOT NULL`
	args := make([]any, 0, 8)
	if filter.DistributorUserID != nil {
		where += ` AND o.user_id = ? AND ds.distributor_user_id = ?`
		args = append(args, *filter.DistributorUserID, *filter.DistributorUserID)
	}
	if filter.Status != nil {
		where += ` AND o.status = ?`
		args = append(args, *filter.Status)
	}
	if filter.SettlementStatus != nil {
		if *filter.SettlementStatus == DistributorSettlementSettled {
			where += ` AND o.paid_at IS NOT NULL`
		} else {
			where += ` AND o.paid_at IS NULL`
		}
	}
	if filter.Search != "" {
		where += ` AND (INSTR(o.trade_no, ?) > 0 OR INSTR(COALESCE(ds.customer_name,''), ?) > 0 OR INSTR(root.trade_no, ?) > 0`
		args = append(args, filter.Search, filter.Search, filter.Search)
		if filter.IncludeTokenSearch {
			where += ` OR subscriber.subscription_token = ?`
			args = append(args, distributorSearchToken(filter.Search))
		}
		where += `)`
	}
	return where, args
}

func distributorSearchToken(value string) string {
	value = strings.TrimSpace(value)
	if slash := strings.LastIndex(value, "/"); slash >= 0 {
		value = strings.Trim(strings.SplitN(value[slash+1:], "#", 2)[0], "/")
		if question := strings.IndexByte(value, '?'); question >= 0 {
			value = value[:question]
		}
	}
	return value
}

func (s *Store) StreamDistributorOrderExport(ctx context.Context, filter DistributorOrderFilter, emit func(DistributorOrderExportRow) error) error {
	if emit == nil || filter.DistributorUserID != nil && *filter.DistributorUserID < 1 {
		return ErrInvalidInput
	}
	filter.Search = strings.TrimSpace(filter.Search)
	if !utf8.ValidString(filter.Search) || utf8.RuneCountInString(filter.Search) > 512 {
		return ErrInvalidInput
	}
	where, args := distributorOrderWhere(filter)
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.trade_no,o.type,root.trade_no,ds.customer_name,distributor.email,
		       COALESCE(NULLIF(distributor.distributor_name,''),distributor.email),p.name,o.period,
		       o.total_amount,CASE WHEN o.paid_at IS NULL THEN 0 ELSE 1 END,ds.remark
		FROM orders o
		JOIN distributor_subscriptions ds ON ds.id = o.distributor_order_id
		JOIN orders root ON root.id = ds.original_order_id
		JOIN users subscriber ON subscriber.id = ds.subscriber_user_id
		JOIN users distributor ON distributor.id = ds.distributor_user_id
		JOIN plans p ON p.id = o.plan_id
	`+where+` ORDER BY root.created_at DESC,root.id DESC,
		CASE WHEN o.id = root.id THEN 0 ELSE 1 END,o.created_at DESC,o.id DESC
	`, args...)
	if err != nil {
		return fmt.Errorf("stream distributor order export: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var value DistributorOrderExportRow
		var customerName, remark sql.NullString
		if err := rows.Scan(&value.TradeNo, &value.Type, &value.SubscriptionTradeNo, &customerName,
			&value.DistributorEmail, &value.DistributorName, &value.PlanName, &value.Period,
			&value.TotalAmount, &value.SettlementStatus, &remark); err != nil {
			return fmt.Errorf("scan distributor order export: %w", err)
		}
		value.CustomerName = nullableStringPointer(customerName)
		value.Remark = nullableStringPointer(remark)
		if err := emit(value); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate distributor order export: %w", err)
	}
	return nil
}

func (s *Store) enrichDistributorOrder(ctx context.Context, value DistributorOrder, now time.Time) (DistributorOrder, error) {
	if value.Order.DistributorOrderID == nil {
		return DistributorOrder{}, ErrNotFound
	}
	var customerName, remark, connectedNodeName, distributorName sql.NullString
	var configIssuedAt, connectedAt, connectedNodeID, claimedAt, closedAt, expiredAt sql.NullInt64
	var createdAt, updatedAt int64
	var upload, download int64
	var subscriberBanned, planRenew, planShow bool
	err := s.db.QueryRowContext(ctx, `
		SELECT ds.id,ds.original_order_id,root.trade_no,ds.distributor_user_id,ds.subscriber_user_id,
		       ds.customer_name,ds.remark,ds.delivery_status,ds.settlement_status,ds.config_issued_at,
		       ds.connected_at,ds.connected_node_id,ds.connected_node_name,ds.claimed_at,ds.closed_at,
		       ds.hwid_enabled,ds.hwid_limit,ds.revision,ds.created_at,ds.updated_at,
		       subscriber.subscription_token,subscriber.uuid,subscriber.transfer_enable,subscriber.traffic_u,
		       subscriber.traffic_d,subscriber.expired_at,subscriber.speed_limit,subscriber.device_limit,
		       subscriber.banned,p.id,p.name,p.renew,p.show,distributor.email,distributor.distributor_name
		FROM distributor_subscriptions ds
		JOIN orders root ON root.id = ds.original_order_id
		JOIN users subscriber ON subscriber.id = ds.subscriber_user_id
		JOIN users distributor ON distributor.id = ds.distributor_user_id
		JOIN plans p ON p.id = subscriber.plan_id
		WHERE ds.id = ?
	`, *value.Order.DistributorOrderID).Scan(
		&value.Subscription.ID, &value.Subscription.OriginalOrderID, &value.Subscription.OriginalTradeNo,
		&value.Subscription.DistributorUserID, &value.Subscription.SubscriberUserID, &customerName, &remark,
		&value.Subscription.DeliveryStatus, &value.Subscription.SettlementStatus, &configIssuedAt, &connectedAt,
		&connectedNodeID, &connectedNodeName, &claimedAt, &closedAt, &value.Subscription.HWIDEnabled,
		&value.Subscription.HWIDLimit, &value.Subscription.Revision, &createdAt, &updatedAt,
		&value.Subscription.SubscriptionToken, &value.Subscription.SubscriberUUID, &value.Entitlement.TransferEnable,
		&upload, &download, &expiredAt, &value.Entitlement.SpeedLimit, &value.Entitlement.DeviceLimit,
		&subscriberBanned, &value.Entitlement.PlanID, &value.Entitlement.PlanName, &planRenew, &planShow,
		&value.DistributorEmail, &distributorName,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return DistributorOrder{}, ErrNotFound
	}
	if err != nil {
		return DistributorOrder{}, fmt.Errorf("enrich distributor order: %w", err)
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
	value.DistributorName = value.DistributorEmail
	if distributorName.Valid {
		value.DistributorName = distributorName.String
	}
	value.Entitlement.UsedTraffic = safeTrafficTotal(upload, download)
	value.Entitlement.RemainingTraffic = maxInt64(0, value.Entitlement.TransferEnable-minInt64(value.Entitlement.TransferEnable, value.Entitlement.UsedTraffic))
	value.Entitlement.ExpiredAt = nullableUnixTime(expiredAt)
	value.IsSubscriptionOrigin = value.Order.ID == value.Subscription.OriginalOrderID
	value.CanViewSubscriptionQR = value.IsSubscriptionOrigin && value.Subscription.SubscriptionToken != ""
	value.CanRenew = value.IsSubscriptionOrigin && value.Entitlement.ExpiredAt != nil && !subscriberBanned &&
		value.Subscription.DeliveryStatus != DistributorDeliveryClosed && planRenew && (planShow || value.Entitlement.ExpiredAt.After(now))
	value.BoundDevices = []string{}
	deviceRows, err := s.db.QueryContext(ctx, `
		SELECT hwid,device_model FROM distributor_hwid_devices
		WHERE subscription_id = ? ORDER BY last_seen_at DESC,id DESC
	`, value.Subscription.ID)
	if err != nil {
		return DistributorOrder{}, fmt.Errorf("list distributor device labels: %w", err)
	}
	for deviceRows.Next() {
		var hwid string
		var model sql.NullString
		if err := deviceRows.Scan(&hwid, &model); err != nil {
			_ = deviceRows.Close()
			return DistributorOrder{}, fmt.Errorf("scan distributor device label: %w", err)
		}
		label := hwid
		if model.Valid && strings.TrimSpace(model.String) != "" {
			label = strings.TrimSpace(model.String) + " " + hwid
		}
		value.BoundDevices = append(value.BoundDevices, label)
	}
	if err := deviceRows.Close(); err != nil {
		return DistributorOrder{}, fmt.Errorf("close distributor device labels: %w", err)
	}
	if err := deviceRows.Err(); err != nil {
		return DistributorOrder{}, fmt.Errorf("iterate distributor device labels: %w", err)
	}
	if value.Order.PaidAt != nil {
		value.SettlementStatus = DistributorSettlementSettled
	}
	return value, nil
}

func (s *Store) CloseDistributorDelivery(ctx context.Context, distributorID int64, tradeNo string, now time.Time) (DistributorOrder, error) {
	if distributorID < 1 || !validTradeNo(tradeNo) || now.Unix() < 0 {
		return DistributorOrder{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DistributorOrder{}, fmt.Errorf("begin close distributor delivery: %w", err)
	}
	defer tx.Rollback()
	var orderID, subscriptionID int64
	var status DistributorDeliveryStatus
	if err := tx.QueryRowContext(ctx, `
		SELECT o.id,ds.id,ds.delivery_status
		FROM orders o JOIN distributor_subscriptions ds ON ds.id = o.distributor_order_id
		WHERE o.user_id = ? AND ds.distributor_user_id = ? AND o.trade_no = ?
	`, distributorID, distributorID, tradeNo).Scan(&orderID, &subscriptionID, &status); errors.Is(err, sql.ErrNoRows) {
		return DistributorOrder{}, ErrNotFound
	} else if err != nil {
		return DistributorOrder{}, fmt.Errorf("find distributor delivery: %w", err)
	}
	if status == DistributorDeliveryPending {
		if _, err := tx.ExecContext(ctx, `
			UPDATE distributor_subscriptions SET delivery_status = ?,closed_at = ?,revision = revision + 1,updated_at = ?
			WHERE id = ? AND delivery_status = ?
		`, DistributorDeliveryClosed, now.Unix(), now.Unix(), subscriptionID, DistributorDeliveryPending); err != nil {
			return DistributorOrder{}, fmt.Errorf("close distributor delivery: %w", err)
		}
	}
	value, err := readDistributorOrder(ctx, tx, distributorID, orderID)
	if err != nil {
		return DistributorOrder{}, err
	}
	if err := tx.Commit(); err != nil {
		return DistributorOrder{}, fmt.Errorf("commit close distributor delivery: %w", err)
	}
	return s.enrichDistributorOrder(ctx, value, now)
}
