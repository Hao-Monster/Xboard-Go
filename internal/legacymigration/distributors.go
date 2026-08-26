package legacymigration

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type DistributorsSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Data     store.LegacyDistributorsData
	Checksum string
}

func ReadDistributorsSnapshot(ctx context.Context, sourcePath string) (DistributorsSnapshot, error) {
	data := store.LegacyDistributorsData{}
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_distributor_order", []string{
			"id", "order_id", "distributor_user_id", "subscriber_user_id", "customer_name", "remark",
			"claim_token_hash", "delivery_status", "settlement_status", "config_issued_at", "connected_at",
			"connected_node_id", "connected_node_name", "claimed_at", "closed_at", "settled_at", "settled_by",
			"claim_ip", "claim_ua", "hwid_enabled", "hwid_limit", "created_at", "updated_at",
		}); err != nil {
			return err
		}
		if err := requireRealTable(ctx, database, "v2_distributor_hwid_device", []string{
			"id", "distributor_order_id", "hwid", "device_os", "os_version", "device_model", "user_agent", "ip",
			"first_seen_at", "last_seen_at",
		}); err != nil {
			return err
		}
		if err := requireRealTable(ctx, database, "v2_order", []string{"id", "distributor_order_id"}); err != nil {
			return err
		}
		if err := requireRealTable(ctx, database, "v2_user", legacyHumanUserColumns); err != nil {
			return err
		}
		var readErr error
		data.Subscribers, readErr = readLegacyDistributorSubscribers(ctx, database)
		if readErr != nil {
			return readErr
		}
		data.Subscriptions, readErr = readLegacyDistributorSubscriptions(ctx, database)
		if readErr != nil {
			return readErr
		}
		data.OrderLinks, readErr = readLegacyDistributorOrderLinks(ctx, database)
		if readErr != nil {
			return readErr
		}
		data.HWIDDevices, readErr = readLegacyDistributorHWIDDevices(ctx, database)
		if readErr != nil {
			return readErr
		}
		return store.ValidateLegacyDistributorsData(data)
	})
	if err != nil {
		return DistributorsSnapshot{}, err
	}
	return DistributorsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Data: data, Checksum: store.LegacyDistributorsChecksum(data),
	}, nil
}

func readLegacyDistributorSubscribers(ctx context.Context, database *sql.DB) ([]store.LegacyDistributorSubscriber, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT u.id,u.email,u.password,u.uuid,u.group_id,u.plan_id,u.transfer_enable,u.u,u.d,u.banned,
		       u.expired_at,u.speed_limit,u.device_limit,u.online_count,u.last_online_at,u.next_reset_at,u.last_reset_at,
		       u.reset_count,u.token,`+legacyUnixExpression("u.created_at")+`,`+legacyUnixExpression("u.updated_at")+`
		FROM v2_user u JOIN v2_distributor_order d ON d.subscriber_user_id = u.id
		ORDER BY u.id
	`)
	if err != nil {
		return nil, fmt.Errorf("read legacy distributor subscribers: %w", err)
	}
	defer rows.Close()
	result := make([]store.LegacyDistributorSubscriber, 0)
	for rows.Next() {
		var value store.LegacyDistributorSubscriber
		var groupID, planID, expiredAt, speedLimit, deviceLimit, onlineCount sql.NullInt64
		var lastOnlineAt, nextResetAt, lastResetAt, resetCount sql.NullInt64
		var banned int64
		if err := rows.Scan(&value.ID, &value.Email, &value.PasswordHash, &value.UUID, &groupID, &planID,
			&value.TransferEnable, &value.TrafficUpload, &value.TrafficDownload, &banned, &expiredAt,
			&speedLimit, &deviceLimit, &onlineCount, &lastOnlineAt, &nextResetAt, &lastResetAt, &resetCount,
			&value.SubscriptionToken, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan legacy distributor subscriber: %w", err)
		}
		if banned < 0 || banned > 1 || !planID.Valid || planID.Int64 < 1 || speedLimit.Int64 < 0 ||
			deviceLimit.Int64 < 0 || onlineCount.Int64 < 0 || resetCount.Int64 < 0 {
			return nil, fmt.Errorf("legacy distributor subscriber id %d has invalid numeric state", value.ID)
		}
		value.Banned = banned == 1
		value.GroupID = positiveNullableInt64(groupID)
		value.PlanID = planID.Int64
		value.ExpiredAt = positiveNullableInt64(expiredAt)
		value.SpeedLimit = int(speedLimit.Int64)
		value.DeviceLimit = int(deviceLimit.Int64)
		value.OnlineCount = int(onlineCount.Int64)
		value.LastOnlineAt = positiveNullableInt64(lastOnlineAt)
		value.NextResetAt = positiveNullableInt64(nextResetAt)
		value.LastResetAt = positiveNullableInt64(lastResetAt)
		value.ResetCount = resetCount.Int64
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy distributor subscribers: %w", err)
	}
	return result, nil
}

func readLegacyDistributorSubscriptions(ctx context.Context, database *sql.DB) ([]store.LegacyDistributorSubscription, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id,order_id,distributor_user_id,subscriber_user_id,customer_name,remark,claim_token_hash,
		       delivery_status,settlement_status,config_issued_at,connected_at,connected_node_id,connected_node_name,
		       claimed_at,closed_at,settled_at,settled_by,claim_ip,claim_ua,hwid_enabled,hwid_limit,
		       `+legacyUnixExpression("created_at")+`,`+legacyUnixExpression("updated_at")+`
		FROM v2_distributor_order ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("read legacy distributor subscriptions: %w", err)
	}
	defer rows.Close()
	result := make([]store.LegacyDistributorSubscription, 0)
	for rows.Next() {
		var value store.LegacyDistributorSubscription
		var customer, remark, nodeName, claimIP, claimUA sql.NullString
		var configAt, connectedAt, nodeID, claimedAt, closedAt, settledAt, settledBy sql.NullInt64
		var hwidEnabled int64
		if err := rows.Scan(&value.ID, &value.OriginalOrderID, &value.DistributorUserID, &value.SubscriberUserID,
			&customer, &remark, &value.ClaimTokenHash, &value.DeliveryStatus, &value.SettlementStatus,
			&configAt, &connectedAt, &nodeID, &nodeName, &claimedAt, &closedAt, &settledAt, &settledBy,
			&claimIP, &claimUA, &hwidEnabled, &value.HWIDLimit, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan legacy distributor subscription: %w", err)
		}
		if hwidEnabled < 0 || hwidEnabled > 1 {
			return nil, fmt.Errorf("legacy distributor subscription id %d has invalid HWID state", value.ID)
		}
		value.HWIDEnabled = hwidEnabled == 1
		value.CustomerName, value.Remark = nullableString(customer), nullableString(remark)
		value.ConnectedNodeName, value.ClaimIP, value.ClaimUserAgent = nullableString(nodeName), nullableString(claimIP), nullableString(claimUA)
		value.ConfigIssuedAt, value.ConnectedAt, value.ConnectedNodeID = positiveNullableInt64(configAt), positiveNullableInt64(connectedAt), positiveNullableInt64(nodeID)
		value.ClaimedAt, value.ClosedAt, value.SettledAt, value.SettledBy = positiveNullableInt64(claimedAt), positiveNullableInt64(closedAt), positiveNullableInt64(settledAt), positiveNullableInt64(settledBy)
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy distributor subscriptions: %w", err)
	}
	return result, nil
}

func readLegacyDistributorOrderLinks(ctx context.Context, database *sql.DB) ([]store.LegacyDistributorOrderLink, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id,distributor_order_id FROM v2_order WHERE distributor_order_id IS NOT NULL ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("read legacy distributor order links: %w", err)
	}
	defer rows.Close()
	result := make([]store.LegacyDistributorOrderLink, 0)
	for rows.Next() {
		var value store.LegacyDistributorOrderLink
		if err := rows.Scan(&value.OrderID, &value.SubscriptionID); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func readLegacyDistributorHWIDDevices(ctx context.Context, database *sql.DB) ([]store.LegacyDistributorHWIDDevice, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id,distributor_order_id,hwid,device_os,os_version,device_model,user_agent,ip,first_seen_at,last_seen_at
		FROM v2_distributor_hwid_device ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("read legacy distributor HWIDs: %w", err)
	}
	defer rows.Close()
	result := make([]store.LegacyDistributorHWIDDevice, 0)
	for rows.Next() {
		var value store.LegacyDistributorHWIDDevice
		var deviceOS, osVersion, deviceModel, userAgent, ip sql.NullString
		if err := rows.Scan(&value.ID, &value.SubscriptionID, &value.HWID, &deviceOS, &osVersion,
			&deviceModel, &userAgent, &ip, &value.FirstSeenAt, &value.LastSeenAt); err != nil {
			return nil, err
		}
		value.DeviceOS, value.OSVersion, value.DeviceModel = nullableString(deviceOS), nullableString(osVersion), nullableString(deviceModel)
		value.UserAgent, value.IPAddress = nullableString(userAgent), nullableString(ip)
		result = append(result, value)
	}
	return result, rows.Err()
}

func nullableString(value sql.NullString) *string {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	result := value.String
	return &result
}
