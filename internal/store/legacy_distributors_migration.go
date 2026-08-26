package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	LegacyDistributorsSlice = "distributors-v1"
	maxLegacyDistributors   = 250_000
)

type LegacyDistributorSubscriber struct {
	ID                int64  `json:"id"`
	Email             string `json:"email"`
	PasswordHash      string `json:"password_hash"`
	UUID              string `json:"uuid"`
	GroupID           *int64 `json:"group_id"`
	PlanID            int64  `json:"plan_id"`
	TransferEnable    int64  `json:"transfer_enable"`
	TrafficUpload     int64  `json:"traffic_upload"`
	TrafficDownload   int64  `json:"traffic_download"`
	Banned            bool   `json:"banned"`
	ExpiredAt         *int64 `json:"expired_at"`
	SpeedLimit        int    `json:"speed_limit"`
	DeviceLimit       int    `json:"device_limit"`
	OnlineCount       int    `json:"online_count"`
	LastOnlineAt      *int64 `json:"last_online_at"`
	NextResetAt       *int64 `json:"next_reset_at"`
	LastResetAt       *int64 `json:"last_reset_at"`
	ResetCount        int64  `json:"reset_count"`
	SubscriptionToken string `json:"subscription_token"`
	CreatedAt         int64  `json:"created_at"`
	UpdatedAt         int64  `json:"updated_at"`
}

type LegacyDistributorSubscription struct {
	ID                int64                       `json:"id"`
	OriginalOrderID   int64                       `json:"original_order_id"`
	DistributorUserID int64                       `json:"distributor_user_id"`
	SubscriberUserID  int64                       `json:"subscriber_user_id"`
	CustomerName      *string                     `json:"customer_name"`
	Remark            *string                     `json:"remark"`
	ClaimTokenHash    string                      `json:"claim_token_hash"`
	DeliveryStatus    DistributorDeliveryStatus   `json:"delivery_status"`
	SettlementStatus  DistributorSettlementStatus `json:"settlement_status"`
	ConfigIssuedAt    *int64                      `json:"config_issued_at"`
	ConnectedAt       *int64                      `json:"connected_at"`
	ConnectedNodeID   *int64                      `json:"connected_node_id"`
	ConnectedNodeName *string                     `json:"connected_node_name"`
	ClaimedAt         *int64                      `json:"claimed_at"`
	ClosedAt          *int64                      `json:"closed_at"`
	SettledAt         *int64                      `json:"settled_at"`
	SettledBy         *int64                      `json:"settled_by"`
	ClaimIP           *string                     `json:"claim_ip"`
	ClaimUserAgent    *string                     `json:"claim_user_agent"`
	HWIDEnabled       bool                        `json:"hwid_enabled"`
	HWIDLimit         int                         `json:"hwid_limit"`
	CreatedAt         int64                       `json:"created_at"`
	UpdatedAt         int64                       `json:"updated_at"`
}

type LegacyDistributorOrderLink struct {
	OrderID        int64 `json:"order_id"`
	SubscriptionID int64 `json:"subscription_id"`
}

type LegacyDistributorHWIDDevice struct {
	ID             int64   `json:"id"`
	SubscriptionID int64   `json:"subscription_id"`
	HWID           string  `json:"hwid"`
	DeviceOS       *string `json:"device_os"`
	OSVersion      *string `json:"os_version"`
	DeviceModel    *string `json:"device_model"`
	UserAgent      *string `json:"user_agent"`
	IPAddress      *string `json:"ip_address"`
	FirstSeenAt    int64   `json:"first_seen_at"`
	LastSeenAt     int64   `json:"last_seen_at"`
}

type LegacyDistributorsData struct {
	Subscribers   []LegacyDistributorSubscriber   `json:"subscribers"`
	Subscriptions []LegacyDistributorSubscription `json:"subscriptions"`
	OrderLinks    []LegacyDistributorOrderLink    `json:"order_links"`
	HWIDDevices   []LegacyDistributorHWIDDevice   `json:"hwid_devices"`
}

type LegacyDistributorsImport struct {
	Slice                string
	SourceSHA256         string
	SourceSize           int64
	Data                 LegacyDistributorsData
	Checksum             string
	RollbackBackupPath   string
	RollbackBackupSHA256 string
}

type LegacyDistributorsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Subscribers          LegacyDomainResult `json:"subscribers"`
	Subscriptions        LegacyDomainResult `json:"subscriptions"`
	OrderLinks           LegacyDomainResult `json:"order_links"`
	HWIDDevices          LegacyDomainResult `json:"hwid_devices"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func LegacyDistributorsChecksum(data LegacyDistributorsData) string {
	normalized := data
	if !sort.SliceIsSorted(normalized.Subscribers, func(i, j int) bool { return normalized.Subscribers[i].ID < normalized.Subscribers[j].ID }) {
		normalized.Subscribers = append([]LegacyDistributorSubscriber(nil), data.Subscribers...)
		sort.Slice(normalized.Subscribers, func(i, j int) bool { return normalized.Subscribers[i].ID < normalized.Subscribers[j].ID })
	}
	if !sort.SliceIsSorted(normalized.Subscriptions, func(i, j int) bool { return normalized.Subscriptions[i].ID < normalized.Subscriptions[j].ID }) {
		normalized.Subscriptions = append([]LegacyDistributorSubscription(nil), data.Subscriptions...)
		sort.Slice(normalized.Subscriptions, func(i, j int) bool { return normalized.Subscriptions[i].ID < normalized.Subscriptions[j].ID })
	}
	if !sort.SliceIsSorted(normalized.OrderLinks, func(i, j int) bool { return normalized.OrderLinks[i].OrderID < normalized.OrderLinks[j].OrderID }) {
		normalized.OrderLinks = append([]LegacyDistributorOrderLink(nil), data.OrderLinks...)
		sort.Slice(normalized.OrderLinks, func(i, j int) bool { return normalized.OrderLinks[i].OrderID < normalized.OrderLinks[j].OrderID })
	}
	if !sort.SliceIsSorted(normalized.HWIDDevices, func(i, j int) bool { return normalized.HWIDDevices[i].ID < normalized.HWIDDevices[j].ID }) {
		normalized.HWIDDevices = append([]LegacyDistributorHWIDDevice(nil), data.HWIDDevices...)
		sort.Slice(normalized.HWIDDevices, func(i, j int) bool { return normalized.HWIDDevices[i].ID < normalized.HWIDDevices[j].ID })
	}
	digest := sha256.New()
	encoder := json.NewEncoder(digest)
	writeLegacyDistributorChecksumDomain(digest, encoder, "subscribers", normalized.Subscribers)
	writeLegacyDistributorChecksumDomain(digest, encoder, "subscriptions", normalized.Subscriptions)
	writeLegacyDistributorChecksumDomain(digest, encoder, "order-links", normalized.OrderLinks)
	writeLegacyDistributorChecksumDomain(digest, encoder, "hwid-devices", normalized.HWIDDevices)
	return hex.EncodeToString(digest.Sum(nil))
}

func writeLegacyDistributorChecksumDomain[T any](digest interface{ Write([]byte) (int, error) }, encoder *json.Encoder, name string, values []T) {
	writeLegacyDistributorChecksumMarker(digest, name)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			panic(fmt.Sprintf("hash legacy distributor %s: %v", name, err))
		}
	}
}

func writeLegacyDistributorChecksumMarker(digest interface{ Write([]byte) (int, error) }, name string) {
	if _, err := digest.Write([]byte("\x00" + name + "\n")); err != nil {
		panic(fmt.Sprintf("hash legacy distributor %s marker: %v", name, err))
	}
}

func ValidateLegacyDistributorsData(data LegacyDistributorsData) error {
	if len(data.Subscriptions) > maxLegacyDistributors || len(data.Subscribers) != len(data.Subscriptions) ||
		len(data.OrderLinks) > maxLegacyOrders || len(data.HWIDDevices) > maxLegacyOrders {
		return fmt.Errorf("%w: invalid legacy distributor row counts", ErrInvalidInput)
	}
	subscribers := make(map[int64]struct{}, len(data.Subscribers))
	tokens := make(map[string]struct{}, len(data.Subscribers))
	uuids := make(map[string]struct{}, len(data.Subscribers))
	for _, value := range data.Subscribers {
		parsedUUID, uuidErr := uuid.Parse(value.UUID)
		if value.ID < 1 || value.Email == "" || len(value.Email) > 320 || value.PasswordHash == "" || value.PlanID < 1 ||
			value.TransferEnable < 0 || value.TrafficUpload < 0 || value.TrafficDownload < 0 || value.SpeedLimit < 0 ||
			value.DeviceLimit < 0 || value.DeviceLimit > 1_000 || value.OnlineCount < 0 || value.ResetCount < 0 ||
			parsedUUID == uuid.Nil || uuidErr != nil || parsedUUID.String() != value.UUID || !validLegacySubscriptionToken(value.SubscriptionToken) ||
			value.GroupID != nil && *value.GroupID < 1 || !validLegacyOptionalTimestamp(value.ExpiredAt) ||
			!validLegacyOptionalTimestamp(value.LastOnlineAt) || !validLegacyOptionalTimestamp(value.NextResetAt) ||
			!validLegacyOptionalTimestamp(value.LastResetAt) || !validLegacyUnixTimestamp(value.CreatedAt) ||
			!validLegacyUnixTimestamp(value.UpdatedAt) || value.UpdatedAt < value.CreatedAt {
			return fmt.Errorf("%w: invalid legacy distributor subscriber id %d", ErrInvalidInput, value.ID)
		}
		if _, exists := subscribers[value.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy distributor subscriber", ErrInvalidInput)
		}
		if _, exists := tokens[value.SubscriptionToken]; exists {
			return fmt.Errorf("%w: duplicate legacy distributor token", ErrInvalidInput)
		}
		if _, exists := uuids[value.UUID]; exists {
			return fmt.Errorf("%w: duplicate legacy distributor UUID", ErrInvalidInput)
		}
		subscribers[value.ID], tokens[value.SubscriptionToken], uuids[value.UUID] = struct{}{}, struct{}{}, struct{}{}
	}
	type subscriptionReference struct{ originalOrderID int64 }
	subscriptions := make(map[int64]subscriptionReference, len(data.Subscriptions))
	originalOrders := make(map[int64]struct{}, len(data.Subscriptions))
	for _, value := range data.Subscriptions {
		customerName, customerErr := normalizeOptionalDistributorText(value.CustomerName, 64)
		remark, remarkErr := normalizeOptionalDistributorText(value.Remark, 500)
		if value.ID < 1 || value.OriginalOrderID < 1 || value.DistributorUserID < 1 || value.SubscriberUserID < 1 ||
			customerErr != nil || remarkErr != nil || !equalOptionalStrings(customerName, value.CustomerName) ||
			!equalOptionalStrings(remark, value.Remark) || !validLowerSHA256(value.ClaimTokenHash) ||
			value.DeliveryStatus < DistributorDeliveryPending || value.DeliveryStatus > DistributorDeliveryClosed ||
			value.SettlementStatus < DistributorSettlementUnsettled || value.SettlementStatus > DistributorSettlementSettled ||
			value.HWIDLimit < 1 || value.HWIDLimit > 100 || !validLegacyOptionalTimestamp(value.ConfigIssuedAt) ||
			!validLegacyOptionalTimestamp(value.ConnectedAt) || !validLegacyOptionalTimestamp(value.ClaimedAt) ||
			!validLegacyOptionalTimestamp(value.ClosedAt) || !validLegacyOptionalTimestamp(value.SettledAt) ||
			!validLegacyPositivePointer(value.ConnectedNodeID) || !validLegacyPositivePointer(value.SettledBy) ||
			!validLegacyTextPointer(value.ConnectedNodeName, 255) || !validLegacyTextPointer(value.ClaimIP, 45) ||
			!validLegacyTextPointer(value.ClaimUserAgent, 255) || !validLegacyUnixTimestamp(value.CreatedAt) ||
			!validLegacyUnixTimestamp(value.UpdatedAt) || value.UpdatedAt < value.CreatedAt {
			return fmt.Errorf("%w: invalid legacy distributor subscription id %d", ErrInvalidInput, value.ID)
		}
		if _, exists := subscribers[value.SubscriberUserID]; !exists {
			return fmt.Errorf("%w: distributor subscription references missing subscriber", ErrConflict)
		}
		if _, exists := subscriptions[value.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy distributor subscription", ErrInvalidInput)
		}
		if _, exists := originalOrders[value.OriginalOrderID]; exists {
			return fmt.Errorf("%w: duplicate legacy distributor original order", ErrInvalidInput)
		}
		subscriptions[value.ID], originalOrders[value.OriginalOrderID] = subscriptionReference{originalOrderID: value.OriginalOrderID}, struct{}{}
	}
	linkedOrders := make(map[int64]struct{}, len(data.OrderLinks))
	linkedOriginals := make(map[int64]struct{}, len(data.Subscriptions))
	for _, value := range data.OrderLinks {
		subscription, exists := subscriptions[value.SubscriptionID]
		if value.OrderID < 1 || !exists {
			return fmt.Errorf("%w: invalid legacy distributor order link", ErrInvalidInput)
		}
		if _, duplicate := linkedOrders[value.OrderID]; duplicate {
			return fmt.Errorf("%w: duplicate legacy distributor order link", ErrInvalidInput)
		}
		if value.OrderID == subscription.originalOrderID {
			linkedOriginals[value.SubscriptionID] = struct{}{}
		}
		linkedOrders[value.OrderID] = struct{}{}
	}
	if len(linkedOriginals) != len(subscriptions) {
		return fmt.Errorf("%w: each distributor subscription requires its original order link", ErrConflict)
	}
	deviceIDs := make(map[int64]struct{}, len(data.HWIDDevices))
	deviceKeys := make(map[string]struct{}, len(data.HWIDDevices))
	for _, value := range data.HWIDDevices {
		_, subscriptionExists := subscriptions[value.SubscriptionID]
		if value.ID < 1 || !distributorHWIDPattern.MatchString(value.HWID) || !subscriptionExists ||
			!validLegacyTextPointer(value.DeviceOS, 100) || !validLegacyTextPointer(value.OSVersion, 100) ||
			!validLegacyTextPointer(value.DeviceModel, 150) || !validLegacyTextPointer(value.UserAgent, 255) ||
			!validLegacyTextPointer(value.IPAddress, 45) || !validLegacyUnixTimestamp(value.FirstSeenAt) ||
			!validLegacyUnixTimestamp(value.LastSeenAt) || value.LastSeenAt < value.FirstSeenAt {
			return fmt.Errorf("%w: invalid legacy distributor HWID id %d", ErrInvalidInput, value.ID)
		}
		key := fmt.Sprintf("%d\x00%s", value.SubscriptionID, value.HWID)
		if _, exists := deviceIDs[value.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy distributor HWID id", ErrInvalidInput)
		}
		if _, exists := deviceKeys[key]; exists {
			return fmt.Errorf("%w: duplicate legacy distributor HWID", ErrInvalidInput)
		}
		deviceIDs[value.ID], deviceKeys[key] = struct{}{}, struct{}{}
	}
	return nil
}

func validLegacyTextPointer(value *string, maximum int) bool {
	return value == nil || *value != "" && utf8.ValidString(*value) && utf8.RuneCountInString(*value) <= maximum
}

func (s *Store) ImportLegacyDistributors(ctx context.Context, input LegacyDistributorsImport, now time.Time) (LegacyDistributorsImportReport, error) {
	if err := validateLegacyDistributorsImport(input); err != nil {
		return LegacyDistributorsImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyDistributorsImportReport{}, fmt.Errorf("begin legacy distributor import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return LegacyDistributorsImportReport{}, fmt.Errorf("read legacy distributor target schema: %w", err)
	}
	if version != CurrentSchemaVersion() {
		return LegacyDistributorsImportReport{}, fmt.Errorf("legacy distributor import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyDistributorsImportReport{}, fmt.Errorf("validate legacy distributor target schema: %w", err)
	}
	if existing, found, err := lookupLegacyDistributorsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyDistributorsImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyDistributorsImportReport{}, err
		}
		return existing, nil
	}
	var otherRuns, targetSubscribers, targetSubscriptions, targetDevices int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyDistributorsSlice).Scan(&otherRuns); err != nil {
		return LegacyDistributorsImportReport{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE account_kind = ?`, AccountKindInternalSubscription).Scan(&targetSubscribers); err != nil {
		return LegacyDistributorsImportReport{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM distributor_subscriptions`).Scan(&targetSubscriptions); err != nil {
		return LegacyDistributorsImportReport{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM distributor_hwid_devices`).Scan(&targetDevices); err != nil {
		return LegacyDistributorsImportReport{}, err
	}
	if otherRuns != 0 || targetSubscribers != 0 || targetSubscriptions != 0 || targetDevices != 0 {
		return LegacyDistributorsImportReport{}, fmt.Errorf("%w: legacy distributor import requires empty distributor targets", ErrConflict)
	}
	if err := requireLegacyDistributorPrerequisites(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyDistributorsImportReport{}, err
	}
	if err := validateLegacyDistributorTargetReferences(ctx, tx, input.Data); err != nil {
		return LegacyDistributorsImportReport{}, err
	}
	subscriberStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO users (
			id,email,password_hash,is_admin,is_staff,is_distributor,distributor_name,banned,account_kind,uuid,
			group_id,plan_id,transfer_enable,traffic_u,traffic_d,expired_at,next_reset_at,last_reset_at,reset_count,
			speed_limit,device_limit,online_count,last_online_at,subscription_token,admin_revision,created_at,updated_at
		) VALUES (?, ?, ?, 0, 0, 0, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	`)
	if err != nil {
		return LegacyDistributorsImportReport{}, fmt.Errorf("prepare distributor subscriber import: %w", err)
	}
	defer subscriberStatement.Close()
	for _, value := range input.Data.Subscribers {
		if _, err := subscriberStatement.ExecContext(ctx, value.ID, value.Email, value.PasswordHash, value.Banned, AccountKindInternalSubscription, value.UUID,
			nullableInt64Value(value.GroupID), value.PlanID, value.TransferEnable, value.TrafficUpload, value.TrafficDownload,
			nullableInt64Value(value.ExpiredAt), nullableInt64Value(value.NextResetAt), nullableInt64Value(value.LastResetAt),
			value.ResetCount, value.SpeedLimit, value.DeviceLimit, value.OnlineCount, nullableInt64Value(value.LastOnlineAt),
			value.SubscriptionToken, value.CreatedAt, value.UpdatedAt); err != nil {
			return LegacyDistributorsImportReport{}, fmt.Errorf("import distributor subscriber id %d: %w", value.ID, err)
		}
	}
	subscriptionStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO distributor_subscriptions (
			id,original_order_id,distributor_user_id,subscriber_user_id,customer_name,remark,claim_token_hash,
			delivery_status,settlement_status,config_issued_at,connected_at,connected_node_id,connected_node_name,
			claimed_at,closed_at,settled_at,settled_by,claim_ip,claim_user_agent,hwid_enabled,hwid_limit,revision,created_at,updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	`)
	if err != nil {
		return LegacyDistributorsImportReport{}, fmt.Errorf("prepare distributor subscription import: %w", err)
	}
	defer subscriptionStatement.Close()
	for _, value := range input.Data.Subscriptions {
		if _, err := subscriptionStatement.ExecContext(ctx, value.ID, value.OriginalOrderID, value.DistributorUserID, value.SubscriberUserID,
			nullableStringValue(value.CustomerName), nullableStringValue(value.Remark), value.ClaimTokenHash,
			value.DeliveryStatus, value.SettlementStatus, nullableInt64Value(value.ConfigIssuedAt), nullableInt64Value(value.ConnectedAt),
			nullableInt64Value(value.ConnectedNodeID), nullableStringValue(value.ConnectedNodeName), nullableInt64Value(value.ClaimedAt),
			nullableInt64Value(value.ClosedAt), nullableInt64Value(value.SettledAt), nullableInt64Value(value.SettledBy),
			nullableStringValue(value.ClaimIP), nullableStringValue(value.ClaimUserAgent), value.HWIDEnabled, value.HWIDLimit,
			value.CreatedAt, value.UpdatedAt); err != nil {
			return LegacyDistributorsImportReport{}, fmt.Errorf("import distributor subscription id %d: %w", value.ID, err)
		}
	}
	linkStatement, err := tx.PrepareContext(ctx, `UPDATE orders SET distributor_order_id = ? WHERE id = ? AND distributor_order_id IS NULL`)
	if err != nil {
		return LegacyDistributorsImportReport{}, fmt.Errorf("prepare distributor order links: %w", err)
	}
	defer linkStatement.Close()
	for _, value := range input.Data.OrderLinks {
		result, err := linkStatement.ExecContext(ctx, value.SubscriptionID, value.OrderID)
		if err != nil {
			return LegacyDistributorsImportReport{}, fmt.Errorf("link distributor order id %d: %w", value.OrderID, err)
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return LegacyDistributorsImportReport{}, fmt.Errorf("%w: distributor order id %d was not linkable", ErrConflict, value.OrderID)
		}
	}
	deviceStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO distributor_hwid_devices (
			id,subscription_id,hwid,device_os,os_version,device_model,user_agent,ip_address,first_seen_at,last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return LegacyDistributorsImportReport{}, fmt.Errorf("prepare distributor HWID import: %w", err)
	}
	defer deviceStatement.Close()
	for _, value := range input.Data.HWIDDevices {
		if _, err := deviceStatement.ExecContext(ctx, value.ID, value.SubscriptionID, value.HWID, nullableStringValue(value.DeviceOS), nullableStringValue(value.OSVersion),
			nullableStringValue(value.DeviceModel), nullableStringValue(value.UserAgent), nullableStringValue(value.IPAddress),
			value.FirstSeenAt, value.LastSeenAt); err != nil {
			return LegacyDistributorsImportReport{}, fmt.Errorf("import distributor HWID id %d: %w", value.ID, err)
		}
	}
	target, err := verifyLegacyTargetDistributors(ctx, tx)
	if err != nil {
		return LegacyDistributorsImportReport{}, err
	}
	report := LegacyDistributorsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Subscribers:   LegacyDomainResult{SourceRows: len(input.Data.Subscribers), TargetRows: target.Subscribers, SourceChecksum: input.Checksum, TargetChecksum: target.Checksum},
		Subscriptions: LegacyDomainResult{SourceRows: len(input.Data.Subscriptions), TargetRows: target.Subscriptions, SourceChecksum: input.Checksum, TargetChecksum: target.Checksum},
		OrderLinks:    LegacyDomainResult{SourceRows: len(input.Data.OrderLinks), TargetRows: target.OrderLinks, SourceChecksum: input.Checksum, TargetChecksum: target.Checksum},
		HWIDDevices:   LegacyDomainResult{SourceRows: len(input.Data.HWIDDevices), TargetRows: target.HWIDDevices, SourceChecksum: input.Checksum, TargetChecksum: target.Checksum},
		AppliedAt:     now.UTC(),
	}
	if len(input.Data.Subscribers) != target.Subscribers || len(input.Data.Subscriptions) != target.Subscriptions ||
		len(input.Data.OrderLinks) != target.OrderLinks || len(input.Data.HWIDDevices) != target.HWIDDevices || input.Checksum != target.Checksum {
		return LegacyDistributorsImportReport{}, errors.New("legacy distributor target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyDistributorsImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO legacy_migration_runs
		(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyDistributorsImportReport{}, err
	}
	if err := tx.Commit(); err != nil {
		return LegacyDistributorsImportReport{}, err
	}
	return report, nil
}

func validateLegacyDistributorsImport(input LegacyDistributorsImport) error {
	if input.Slice != LegacyDistributorsSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) ||
		strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 || !validLowerSHA256(input.RollbackBackupSHA256) ||
		input.Checksum != LegacyDistributorsChecksum(input.Data) {
		return fmt.Errorf("%w: invalid legacy distributor import", ErrInvalidInput)
	}
	return ValidateLegacyDistributorsData(input.Data)
}

func requireLegacyDistributorPrerequisites(ctx context.Context, tx *sql.Tx, sourceSHA256 string) error {
	var matched int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM legacy_migration_runs
		WHERE source_sha256 = ? AND slice IN (?, ?)
	`, sourceSHA256, LegacyHumanUsersSlice, LegacyOrdersSlice).Scan(&matched); err != nil {
		return fmt.Errorf("check legacy distributor migration prerequisites: %w", err)
	}
	if matched != 2 {
		return fmt.Errorf("%w: import human users and orders from the same legacy snapshot before distributors", ErrConflict)
	}
	return nil
}

type legacyDistributorTargetSubscriptionReference struct {
	originalOrderID   int64
	distributorUserID int64
	subscriberUserID  int64
}

func validateLegacyDistributorTargetReferences(ctx context.Context, tx *sql.Tx, data LegacyDistributorsData) error {
	distributorIDs := make(map[int64]struct{}, len(data.Subscriptions))
	planIDs := make(map[int64]struct{}, len(data.Subscribers))
	groupIDs := make(map[int64]struct{}, len(data.Subscribers))
	subscriberIDs := make(map[int64]struct{}, len(data.Subscribers))
	humanReferenceIDs := make(map[int64]struct{}, len(data.Subscriptions))
	nodeIDs := make(map[int64]struct{}, len(data.Subscriptions))
	subscriptions := make(map[int64]legacyDistributorTargetSubscriptionReference, len(data.Subscriptions))
	subscriberPlans := make(map[int64]int64, len(data.Subscribers))
	for _, value := range data.Subscribers {
		planIDs[value.PlanID] = struct{}{}
		subscriberIDs[value.ID] = struct{}{}
		subscriberPlans[value.ID] = value.PlanID
		if value.GroupID != nil {
			groupIDs[*value.GroupID] = struct{}{}
		}
	}
	for _, value := range data.Subscriptions {
		distributorIDs[value.DistributorUserID] = struct{}{}
		subscriptions[value.ID] = legacyDistributorTargetSubscriptionReference{
			originalOrderID: value.OriginalOrderID, distributorUserID: value.DistributorUserID, subscriberUserID: value.SubscriberUserID,
		}
		if value.SettledBy != nil {
			humanReferenceIDs[*value.SettledBy] = struct{}{}
		}
		if value.ConnectedNodeID != nil {
			nodeIDs[*value.ConnectedNodeID] = struct{}{}
		}
	}
	if err := requireLegacyIDSet(ctx, tx, distributorIDs, `SELECT id FROM users WHERE account_kind = 'human' AND is_distributor = 1 AND id IN (`, "human distributors"); err != nil {
		return err
	}
	if err := requireLegacyIDSet(ctx, tx, humanReferenceIDs, `SELECT id FROM users WHERE account_kind = 'human' AND id IN (`, "settlement users"); err != nil {
		return err
	}
	if err := requireLegacyIDSet(ctx, tx, planIDs, `SELECT id FROM plans WHERE id IN (`, "plans"); err != nil {
		return err
	}
	if err := requireLegacyIDSet(ctx, tx, groupIDs, `SELECT id FROM server_groups WHERE id IN (`, "server groups"); err != nil {
		return err
	}
	if err := requireLegacyIDSet(ctx, tx, nodeIDs, `SELECT id FROM nodes WHERE id IN (`, "connected nodes"); err != nil {
		return err
	}
	occupied, err := readLegacyIDSet(ctx, tx, subscriberIDs, `SELECT id FROM users WHERE id IN (`)
	if err != nil {
		return fmt.Errorf("validate legacy distributor subscriber ids: %w", err)
	}
	if len(occupied) != 0 {
		return fmt.Errorf("%w: distributor subscriber user id is occupied", ErrConflict)
	}

	orderLinks := make(map[int64]int64, len(data.OrderLinks))
	for _, value := range data.OrderLinks {
		orderLinks[value.OrderID] = value.SubscriptionID
	}
	if err := validateLegacyDistributorOrders(ctx, tx, orderLinks, subscriptions, subscriberPlans); err != nil {
		return err
	}
	return nil
}

const legacyDistributorReferenceBatchSize = 500

func requireLegacyIDSet(ctx context.Context, tx *sql.Tx, expected map[int64]struct{}, queryPrefix, description string) error {
	found, err := readLegacyIDSet(ctx, tx, expected, queryPrefix)
	if err != nil {
		return fmt.Errorf("validate legacy distributor %s: %w", description, err)
	}
	if len(found) != len(expected) {
		return fmt.Errorf("%w: legacy distributor references missing %s", ErrConflict, description)
	}
	return nil
}

func readLegacyIDSet(ctx context.Context, tx *sql.Tx, expected map[int64]struct{}, queryPrefix string) (map[int64]struct{}, error) {
	found := make(map[int64]struct{}, len(expected))
	ids := sortedLegacyDistributorIDs(expected)
	for start := 0; start < len(ids); start += legacyDistributorReferenceBatchSize {
		end := min(start+legacyDistributorReferenceBatchSize, len(ids))
		batch := ids[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		arguments := make([]any, len(batch))
		for index, id := range batch {
			arguments[index] = id
		}
		rows, err := tx.QueryContext(ctx, queryPrefix+placeholders+")", arguments...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, err
			}
			found[id] = struct{}{}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return found, nil
}

func sortedLegacyDistributorIDs(values map[int64]struct{}) []int64 {
	result := make([]int64, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func validateLegacyDistributorOrders(
	ctx context.Context,
	tx *sql.Tx,
	orderLinks map[int64]int64,
	subscriptions map[int64]legacyDistributorTargetSubscriptionReference,
	subscriberPlans map[int64]int64,
) error {
	orderIDs := make(map[int64]struct{}, len(orderLinks))
	for orderID := range orderLinks {
		orderIDs[orderID] = struct{}{}
	}
	found := 0
	ids := sortedLegacyDistributorIDs(orderIDs)
	for start := 0; start < len(ids); start += legacyDistributorReferenceBatchSize {
		end := min(start+legacyDistributorReferenceBatchSize, len(ids))
		batch := ids[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		arguments := make([]any, len(batch))
		for index, id := range batch {
			arguments[index] = id
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT id,user_id,plan_id,type,status,distributor_order_id
			FROM orders WHERE id IN (`+placeholders+`)
		`, arguments...)
		if err != nil {
			return fmt.Errorf("validate legacy distributor financial orders: %w", err)
		}
		for rows.Next() {
			var orderID, userID, planID int64
			var orderType OrderType
			var status OrderStatus
			var linked sql.NullInt64
			if err := rows.Scan(&orderID, &userID, &planID, &orderType, &status, &linked); err != nil {
				_ = rows.Close()
				return err
			}
			subscription := subscriptions[orderLinks[orderID]]
			plan := subscriberPlans[subscription.subscriberUserID]
			wantType := OrderTypeRenewal
			if orderID == subscription.originalOrderID {
				wantType = OrderTypeNew
			}
			if userID != subscription.distributorUserID || planID != plan || orderType != wantType ||
				status != OrderStatusCompleted || linked.Valid {
				_ = rows.Close()
				return fmt.Errorf("%w: legacy distributor financial order %d is incompatible", ErrConflict, orderID)
			}
			found++
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}
	if found != len(orderLinks) {
		return fmt.Errorf("%w: legacy distributor financial order is missing", ErrConflict)
	}
	return nil
}

type legacyDistributorsTargetVerification struct {
	Subscribers   int
	Subscriptions int
	OrderLinks    int
	HWIDDevices   int
	Checksum      string
}

func verifyLegacyTargetDistributors(ctx context.Context, database queryer) (legacyDistributorsTargetVerification, error) {
	var result legacyDistributorsTargetVerification
	digest := sha256.New()
	encoder := json.NewEncoder(digest)
	writeLegacyDistributorChecksumMarker(digest, "subscribers")
	rows, err := database.QueryContext(ctx, `
		SELECT u.id,u.email,u.password_hash,u.uuid,u.group_id,u.plan_id,u.transfer_enable,u.traffic_u,u.traffic_d,u.banned,
		       u.expired_at,u.speed_limit,u.device_limit,u.online_count,u.last_online_at,u.next_reset_at,u.last_reset_at,u.reset_count,
		       u.subscription_token,u.created_at,u.updated_at
		FROM users u JOIN distributor_subscriptions ds ON ds.subscriber_user_id = u.id ORDER BY u.id
	`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var value LegacyDistributorSubscriber
		var groupID, expiredAt, lastOnlineAt, nextResetAt, lastResetAt sql.NullInt64
		if err := rows.Scan(&value.ID, &value.Email, &value.PasswordHash, &value.UUID, &groupID, &value.PlanID,
			&value.TransferEnable, &value.TrafficUpload, &value.TrafficDownload, &value.Banned, &expiredAt,
			&value.SpeedLimit, &value.DeviceLimit, &value.OnlineCount, &lastOnlineAt, &nextResetAt, &lastResetAt,
			&value.ResetCount, &value.SubscriptionToken, &value.CreatedAt, &value.UpdatedAt); err != nil {
			_ = rows.Close()
			return result, err
		}
		value.GroupID, value.ExpiredAt, value.LastOnlineAt = nullableInt64Pointer(groupID), nullableInt64Pointer(expiredAt), nullableInt64Pointer(lastOnlineAt)
		value.NextResetAt, value.LastResetAt = nullableInt64Pointer(nextResetAt), nullableInt64Pointer(lastResetAt)
		if err := encoder.Encode(value); err != nil {
			_ = rows.Close()
			return result, err
		}
		result.Subscribers++
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	writeLegacyDistributorChecksumMarker(digest, "subscriptions")
	rows, err = database.QueryContext(ctx, `
		SELECT id,original_order_id,distributor_user_id,subscriber_user_id,customer_name,remark,claim_token_hash,
		       delivery_status,settlement_status,config_issued_at,connected_at,connected_node_id,connected_node_name,
		       claimed_at,closed_at,settled_at,settled_by,claim_ip,claim_user_agent,hwid_enabled,hwid_limit,created_at,updated_at
		FROM distributor_subscriptions ORDER BY id
	`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var value LegacyDistributorSubscription
		var customer, remark, nodeName, claimIP, claimUA sql.NullString
		var configAt, connectedAt, nodeID, claimedAt, closedAt, settledAt, settledBy sql.NullInt64
		if err := rows.Scan(&value.ID, &value.OriginalOrderID, &value.DistributorUserID, &value.SubscriberUserID,
			&customer, &remark, &value.ClaimTokenHash, &value.DeliveryStatus, &value.SettlementStatus, &configAt,
			&connectedAt, &nodeID, &nodeName, &claimedAt, &closedAt, &settledAt, &settledBy, &claimIP, &claimUA,
			&value.HWIDEnabled, &value.HWIDLimit, &value.CreatedAt, &value.UpdatedAt); err != nil {
			_ = rows.Close()
			return result, err
		}
		value.CustomerName, value.Remark, value.ConnectedNodeName = nullableStringPointer(customer), nullableStringPointer(remark), nullableStringPointer(nodeName)
		value.ClaimIP, value.ClaimUserAgent = nullableStringPointer(claimIP), nullableStringPointer(claimUA)
		value.ConfigIssuedAt, value.ConnectedAt, value.ConnectedNodeID = nullableInt64Pointer(configAt), nullableInt64Pointer(connectedAt), nullableInt64Pointer(nodeID)
		value.ClaimedAt, value.ClosedAt, value.SettledAt, value.SettledBy = nullableInt64Pointer(claimedAt), nullableInt64Pointer(closedAt), nullableInt64Pointer(settledAt), nullableInt64Pointer(settledBy)
		if err := encoder.Encode(value); err != nil {
			_ = rows.Close()
			return result, err
		}
		result.Subscriptions++
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	writeLegacyDistributorChecksumMarker(digest, "order-links")
	rows, err = database.QueryContext(ctx, `SELECT id,distributor_order_id FROM orders WHERE distributor_order_id IS NOT NULL ORDER BY id`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var value LegacyDistributorOrderLink
		if err := rows.Scan(&value.OrderID, &value.SubscriptionID); err != nil {
			_ = rows.Close()
			return result, err
		}
		if err := encoder.Encode(value); err != nil {
			_ = rows.Close()
			return result, err
		}
		result.OrderLinks++
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	writeLegacyDistributorChecksumMarker(digest, "hwid-devices")
	rows, err = database.QueryContext(ctx, `
		SELECT id,subscription_id,hwid,device_os,os_version,device_model,user_agent,ip_address,first_seen_at,last_seen_at
		FROM distributor_hwid_devices ORDER BY id
	`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var value LegacyDistributorHWIDDevice
		var deviceOS, osVersion, deviceModel, userAgent, ipAddress sql.NullString
		if err := rows.Scan(&value.ID, &value.SubscriptionID, &value.HWID, &deviceOS, &osVersion, &deviceModel,
			&userAgent, &ipAddress, &value.FirstSeenAt, &value.LastSeenAt); err != nil {
			return result, err
		}
		value.DeviceOS, value.OSVersion, value.DeviceModel = nullableStringPointer(deviceOS), nullableStringPointer(osVersion), nullableStringPointer(deviceModel)
		value.UserAgent, value.IPAddress = nullableStringPointer(userAgent), nullableStringPointer(ipAddress)
		if err := encoder.Encode(value); err != nil {
			_ = rows.Close()
			return result, err
		}
		result.HWIDDevices++
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return result, err
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	result.Checksum = hex.EncodeToString(digest.Sum(nil))
	return result, nil
}

func lookupLegacyDistributorsImport(ctx context.Context, tx *sql.Tx, sourceSHA string) (LegacyDistributorsImportReport, bool, error) {
	var encoded string
	err := tx.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?`, LegacyDistributorsSlice, sourceSHA).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyDistributorsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyDistributorsImportReport{}, false, err
	}
	var report LegacyDistributorsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyDistributorsImportReport{}, false, err
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) LookupLegacyDistributorsImport(ctx context.Context, sourceSHA string) (LegacyDistributorsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA) {
		return LegacyDistributorsImportReport{}, false, ErrInvalidInput
	}
	var encoded string
	err := s.db.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?`, LegacyDistributorsSlice, sourceSHA).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyDistributorsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyDistributorsImportReport{}, false, err
	}
	var report LegacyDistributorsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyDistributorsImportReport{}, false, err
	}
	report.AlreadyApplied = true
	return report, true, nil
}
