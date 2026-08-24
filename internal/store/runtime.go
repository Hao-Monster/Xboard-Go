package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	maxRuntimeConfigBytes = 1 << 20
	trafficRateScale      = int64(1_000_000)
	deviceStateTTL        = 5 * time.Minute
)

func (s *Store) SaveNodeRuntime(ctx context.Context, nodeID int64, input SaveNodeRuntimeInput, now time.Time) (NodeRuntime, error) {
	if input.RateMicros < 1 || input.RateMicros > 1_000_000_000 || len(input.Config) == 0 || len(input.Config) > maxRuntimeConfigBytes || !json.Valid(input.Config) {
		return NodeRuntime{}, fmt.Errorf("%w: invalid node runtime", ErrInvalidInput)
	}
	var configHeader struct {
		Protocol   string `json:"protocol"`
		ServerPort int    `json:"server_port"`
	}
	if err := json.Unmarshal(input.Config, &configHeader); err != nil || configHeader.Protocol == "" || configHeader.ServerPort < 1 || configHeader.ServerPort > 65535 {
		return NodeRuntime{}, fmt.Errorf("%w: invalid node runtime config", ErrInvalidInput)
	}
	groups, err := normalizeGroupIDs(input.GroupIDs)
	if err != nil {
		return NodeRuntime{}, err
	}
	routes, err := normalizeRouteIDs(input.RouteIDs)
	if err != nil {
		return NodeRuntime{}, err
	}
	canonical := make([]byte, 0, len(input.Config))
	canonical = append(canonical, input.Config...)
	canonical = json.RawMessage(canonical)

	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NodeRuntime{}, fmt.Errorf("begin save node runtime: %w", err)
	}
	defer tx.Rollback()

	var nodeType string
	if err := tx.QueryRowContext(ctx, `SELECT type FROM nodes WHERE id = ?`, nodeID).Scan(&nodeType); errors.Is(err, sql.ErrNoRows) {
		return NodeRuntime{}, ErrNotFound
	} else if err != nil {
		return NodeRuntime{}, fmt.Errorf("read node runtime owner: %w", err)
	}
	if strings.ToLower(configHeader.Protocol) != nodeType {
		return NodeRuntime{}, fmt.Errorf("%w: runtime protocol must match node type", ErrInvalidInput)
	}
	if err := requireReferencedIDs(ctx, tx, "server_groups", groups, "server groups"); err != nil {
		return NodeRuntime{}, err
	}
	if err := requireReferencedIDs(ctx, tx, "routing_rules", routes, "routing rules"); err != nil {
		return NodeRuntime{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE nodes SET rate_micros = ?, runtime_config = ?, updated_at = ? WHERE id = ?`, input.RateMicros, string(canonical), now.Unix(), nodeID); err != nil {
		return NodeRuntime{}, fmt.Errorf("update node runtime: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_group_memberships WHERE node_id = ?`, nodeID); err != nil {
		return NodeRuntime{}, fmt.Errorf("replace node groups: %w", err)
	}
	for _, groupID := range groups {
		if _, err := tx.ExecContext(ctx, `INSERT INTO node_group_memberships (node_id, group_id) VALUES (?, ?)`, nodeID, groupID); err != nil {
			return NodeRuntime{}, fmt.Errorf("insert node group: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_route_memberships WHERE node_id = ?`, nodeID); err != nil {
		return NodeRuntime{}, fmt.Errorf("replace node routes: %w", err)
	}
	for _, routeID := range routes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO node_route_memberships (node_id, route_id) VALUES (?, ?)`, nodeID, routeID); err != nil {
			return NodeRuntime{}, fmt.Errorf("insert node route: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return NodeRuntime{}, fmt.Errorf("commit node runtime: %w", err)
	}
	return s.GetNodeRuntime(ctx, nodeID)
}

func normalizeGroupIDs(values []int64) ([]int64, error) {
	return normalizeRuntimeIDs(values, "node groups")
}

func normalizeRouteIDs(values []int64) ([]int64, error) {
	return normalizeRuntimeIDs(values, "node routes")
}

func normalizeRuntimeIDs(values []int64, label string) ([]int64, error) {
	if len(values) > 1_000 {
		return nil, fmt.Errorf("%w: too many %s", ErrInvalidInput, label)
	}
	seen := make(map[int64]struct{}, len(values))
	groups := make([]int64, 0, len(values))
	for _, value := range values {
		if value < 1 {
			return nil, fmt.Errorf("%w: %s ids must be positive", ErrInvalidInput, label)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		groups = append(groups, value)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
	return groups, nil
}

func requireReferencedIDs(ctx context.Context, tx *sql.Tx, table string, ids []int64, label string) error {
	if len(ids) == 0 {
		return nil
	}
	if table != "server_groups" && table != "routing_rules" {
		return errors.New("unsupported reference table")
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	arguments := make([]any, len(ids))
	for index, id := range ids {
		arguments[index] = id
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE id IN (`+placeholders+`)`, arguments...).Scan(&count); err != nil {
		return fmt.Errorf("validate %s: %w", label, err)
	}
	if count != len(ids) {
		return fmt.Errorf("%w: one or more %s do not exist", ErrInvalidInput, label)
	}
	return nil
}

func (s *Store) GetNodeRuntime(ctx context.Context, nodeID int64) (NodeRuntime, error) {
	var runtime NodeRuntime
	var config sql.NullString
	var updatedAt int64
	err := s.db.QueryRowContext(ctx, `SELECT id, rate_micros, runtime_config, updated_at FROM nodes WHERE id = ?`, nodeID).Scan(
		&runtime.NodeID, &runtime.RateMicros, &config, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return NodeRuntime{}, ErrNotFound
	}
	if err != nil {
		return NodeRuntime{}, fmt.Errorf("get node runtime: %w", err)
	}
	if !config.Valid || config.String == "" {
		return NodeRuntime{}, ErrRuntimeNotConfigured
	}
	rows, err := s.db.QueryContext(ctx, `SELECT group_id FROM node_group_memberships WHERE node_id = ? ORDER BY group_id`, nodeID)
	if err != nil {
		return NodeRuntime{}, fmt.Errorf("list node groups: %w", err)
	}
	if err := func() error {
		defer rows.Close()
		for rows.Next() {
			var groupID int64
			if err := rows.Scan(&groupID); err != nil {
				return fmt.Errorf("scan node group: %w", err)
			}
			runtime.GroupIDs = append(runtime.GroupIDs, groupID)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("list node groups: %w", err)
		}
		return nil
	}(); err != nil {
		return NodeRuntime{}, err
	}
	routeRows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.remarks, r.match_json, r.action, r.action_value, r.created_at, r.updated_at
		FROM node_route_memberships nr
		JOIN routing_rules r ON r.id = nr.route_id
		WHERE nr.node_id = ?
		ORDER BY nr.route_id
	`, nodeID)
	if err != nil {
		return NodeRuntime{}, fmt.Errorf("list node routes: %w", err)
	}
	defer routeRows.Close()
	for routeRows.Next() {
		rule, err := scanRoutingRule(routeRows)
		if err != nil {
			return NodeRuntime{}, err
		}
		runtime.RouteIDs = append(runtime.RouteIDs, rule.ID)
		runtime.Routes = append(runtime.Routes, rule)
	}
	if err := routeRows.Err(); err != nil {
		return NodeRuntime{}, fmt.Errorf("list node routes: %w", err)
	}
	runtime.Config = json.RawMessage(config.String)
	runtime.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return runtime, nil
}

func (s *Store) CreateRuntimeUser(ctx context.Context, input CreateRuntimeUserInput, now time.Time) (RuntimeUser, error) {
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.UUID = strings.ToLower(strings.TrimSpace(input.UUID))
	if input.AccountKind == "" {
		input.AccountKind = AccountKindHuman
	}
	if input.Email == "" || input.PasswordHash == "" || input.GroupID < 1 || input.TransferEnable < 0 || input.TrafficUpload < 0 || input.TrafficDownload < 0 ||
		input.SpeedLimit < 0 || input.DeviceLimit < 0 || input.DeviceLimit > 1_000 || uuid.Validate(input.UUID) != nil ||
		(input.AccountKind != AccountKindHuman && input.AccountKind != AccountKindInternalSubscription) {
		return RuntimeUser{}, fmt.Errorf("%w: invalid runtime user", ErrInvalidInput)
	}
	var expiredAt any
	if input.ExpiredAt != nil {
		expiredAt = input.ExpiredAt.Unix()
	}
	defer s.lockWrite()()
	var groupExists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM server_groups WHERE id = ?)`, input.GroupID).Scan(&groupExists); err != nil {
		return RuntimeUser{}, fmt.Errorf("validate runtime user group: %w", err)
	}
	if !groupExists {
		return RuntimeUser{}, fmt.Errorf("%w: runtime user group does not exist", ErrInvalidInput)
	}
	subscriptionToken, err := newSubscriptionToken()
	if err != nil {
		return RuntimeUser{}, err
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO users (
			email, password_hash, is_admin, banned, account_kind, uuid, group_id, transfer_enable, traffic_u, traffic_d,
			expired_at, speed_limit, device_limit, subscription_token, created_at, updated_at
		) VALUES (?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.Email, input.PasswordHash, input.Banned, input.AccountKind, input.UUID, input.GroupID, input.TransferEnable,
		input.TrafficUpload, input.TrafficDownload, expiredAt, input.SpeedLimit, input.DeviceLimit, subscriptionToken, now.Unix(), now.Unix())
	if err != nil {
		return RuntimeUser{}, fmt.Errorf("create runtime user: %w", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		return RuntimeUser{}, fmt.Errorf("read runtime user id: %w", err)
	}
	return RuntimeUser{ID: userID, UUID: input.UUID, SpeedLimit: input.SpeedLimit, DeviceLimit: input.DeviceLimit}, nil
}

func (s *Store) ListNodeRuntimeUsers(ctx context.Context, nodeID int64, now time.Time) ([]RuntimeUser, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.uuid, u.speed_limit, u.device_limit
		FROM users u
		JOIN node_group_memberships ng ON ng.group_id = u.group_id
		WHERE ng.node_id = ?
		  AND u.uuid IS NOT NULL
		  AND u.banned = 0
		  AND u.traffic_u + u.traffic_d < u.transfer_enable
		  AND (u.expired_at IS NULL OR u.expired_at >= ?)
		ORDER BY u.id
	`, nodeID, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("list runtime users: %w", err)
	}
	defer rows.Close()
	users := make([]RuntimeUser, 0)
	for rows.Next() {
		var user RuntimeUser
		if err := rows.Scan(&user.ID, &user.UUID, &user.SpeedLimit, &user.DeviceLimit); err != nil {
			return nil, fmt.Errorf("scan runtime user: %w", err)
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) GetNodeRuntimeUser(ctx context.Context, nodeID, userID int64, now time.Time) (RuntimeUser, error) {
	var user RuntimeUser
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.uuid, u.speed_limit, u.device_limit
		FROM users u
		JOIN node_group_memberships ng ON ng.group_id = u.group_id
		JOIN nodes n ON n.id = ng.node_id
		WHERE ng.node_id = ? AND u.id = ? AND n.enabled = 1 AND n.runtime_config IS NOT NULL
		  AND u.uuid IS NOT NULL AND u.banned = 0
		  AND u.traffic_u + u.traffic_d < u.transfer_enable
		  AND (u.expired_at IS NULL OR u.expired_at >= ?)
	`, nodeID, userID, now.Unix()).Scan(&user.ID, &user.UUID, &user.SpeedLimit, &user.DeviceLimit)
	if errors.Is(err, sql.ErrNoRows) {
		return RuntimeUser{}, ErrNotFound
	}
	if err != nil {
		return RuntimeUser{}, fmt.Errorf("get node runtime user: %w", err)
	}
	return user, nil
}

func (s *Store) ListRuntimeNodeTargetsForGroups(ctx context.Context, groupIDs []int64) ([]NodeRuntimeTarget, error) {
	if len(groupIDs) == 0 {
		return nil, nil
	}
	unique := make(map[int64]struct{}, len(groupIDs))
	for _, groupID := range groupIDs {
		if groupID < 1 {
			return nil, fmt.Errorf("%w: invalid group id", ErrInvalidInput)
		}
		unique[groupID] = struct{}{}
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(unique)), ",")
	args := make([]any, 0, len(unique))
	for groupID := range unique {
		args = append(args, groupID)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT n.id, n.machine_id
		FROM node_group_memberships ng
		JOIN nodes n ON n.id = ng.node_id
		WHERE ng.group_id IN (`+placeholders+`)
		  AND n.machine_id IS NOT NULL AND n.enabled = 1 AND n.runtime_config IS NOT NULL
		ORDER BY n.id
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("list runtime node targets for groups: %w", err)
	}
	defer rows.Close()
	targets := make([]NodeRuntimeTarget, 0)
	for rows.Next() {
		var target NodeRuntimeTarget
		if err := rows.Scan(&target.NodeID, &target.MachineID); err != nil {
			return nil, fmt.Errorf("scan runtime node target: %w", err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runtime node targets: %w", err)
	}
	return targets, nil
}

func (s *Store) ApplyNodeReport(ctx context.Context, input NodeReportInput) (NodeReportResult, error) {
	for userID, traffic := range input.Traffic {
		if userID < 1 || traffic.Upload < 0 || traffic.Download < 0 {
			return NodeReportResult{}, fmt.Errorf("%w: invalid traffic entry", ErrInvalidInput)
		}
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NodeReportResult{}, fmt.Errorf("begin node report: %w", err)
	}
	defer tx.Rollback()

	var rateMicros int64
	var allowed bool
	if err := tx.QueryRowContext(ctx, `
		SELECT rate_micros, machine_id = ? AND enabled = 1 AND runtime_config IS NOT NULL
		FROM nodes WHERE id = ?
	`, input.MachineID, input.NodeID).Scan(&rateMicros, &allowed); errors.Is(err, sql.ErrNoRows) {
		return NodeReportResult{}, ErrNotFound
	} else if err != nil {
		return NodeReportResult{}, fmt.Errorf("authorize node report: %w", err)
	}
	if !allowed {
		return NodeReportResult{}, ErrNodeNotLinked
	}

	result := NodeReportResult{}
	if input.ReportID != "" && len(input.Traffic) > 0 {
		trafficHash := reportTrafficHash(input.Traffic)
		claimed, err := tx.ExecContext(ctx, `
			INSERT INTO node_report_receipts (node_id, report_id, traffic_hash, created_at)
			VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING
		`, input.NodeID, input.ReportID, trafficHash, input.Now.Unix())
		if err != nil {
			return NodeReportResult{}, fmt.Errorf("claim node report: %w", err)
		}
		rows, _ := claimed.RowsAffected()
		result.DuplicateTraffic = rows == 0
		if result.DuplicateTraffic {
			var storedHash []byte
			if err := tx.QueryRowContext(ctx, `
				SELECT traffic_hash FROM node_report_receipts WHERE node_id = ? AND report_id = ?
			`, input.NodeID, input.ReportID).Scan(&storedHash); err != nil {
				return NodeReportResult{}, fmt.Errorf("read node report receipt: %w", err)
			}
			if !bytes.Equal(storedHash, trafficHash) {
				return NodeReportResult{}, fmt.Errorf("%w: report_id was already used for different traffic", ErrInvalidInput)
			}
			// The original transaction already applied the traffic and its device
			// snapshot. Ignore retry-time user state so a later group change cannot
			// strand the durable report, while still allowing fresh status metrics.
			input.Alive = nil
			input.Online = nil
		}
	}
	if !result.DuplicateTraffic {
		if err := authorizeReportedUsers(ctx, tx, input); err != nil {
			return NodeReportResult{}, err
		}
	}

	if len(input.Traffic) > 0 && !result.DuplicateTraffic {
		if err := applyTraffic(ctx, tx, input, rateMicros); err != nil {
			return NodeReportResult{}, err
		}
	}
	deviceUsers := make(map[int64]struct{}, len(input.Alive))
	for userID := range input.Alive {
		deviceUsers[userID] = struct{}{}
	}
	if input.ReplaceAllDevices {
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT user_id FROM node_device_ips WHERE node_id = ?`, input.NodeID)
		if err != nil {
			return NodeReportResult{}, fmt.Errorf("list replaced node device users: %w", err)
		}
		for rows.Next() {
			var userID int64
			if err := rows.Scan(&userID); err != nil {
				rows.Close()
				return NodeReportResult{}, fmt.Errorf("scan replaced node device user: %w", err)
			}
			deviceUsers[userID] = struct{}{}
		}
		if err := rows.Close(); err != nil {
			return NodeReportResult{}, fmt.Errorf("close replaced node device users: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM node_device_ips WHERE node_id = ?`, input.NodeID); err != nil {
			return NodeReportResult{}, fmt.Errorf("clear node device snapshot: %w", err)
		}
	}
	if err := applyAlive(ctx, tx, input.NodeID, input.Alive, input.Now); err != nil {
		return NodeReportResult{}, err
	}
	if err := applyOnline(ctx, tx, input.NodeID, input.Online, input.Now); err != nil {
		return NodeReportResult{}, err
	}
	if len(input.Status) > 0 || len(input.Metrics) > 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO node_runtime_state (node_id, status_json, metrics_json, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(node_id) DO UPDATE SET
				status_json = COALESCE(excluded.status_json, node_runtime_state.status_json),
				metrics_json = COALESCE(excluded.metrics_json, node_runtime_state.metrics_json),
				updated_at = excluded.updated_at
		`, input.NodeID, nullableJSON(input.Status), nullableJSON(input.Metrics), input.Now.Unix()); err != nil {
			return NodeReportResult{}, fmt.Errorf("update node runtime state: %w", err)
		}
	}
	lastPush := any(nil)
	if len(input.Traffic) > 0 {
		lastPush = input.Now.Unix()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE nodes SET last_check_at = ?, last_push_at = COALESCE(?, last_push_at) WHERE id = ?`, input.Now.Unix(), lastPush, input.NodeID); err != nil {
		return NodeReportResult{}, fmt.Errorf("touch node report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM node_report_receipts
		WHERE created_at < ? AND (node_id != ? OR report_id != ?)
	`, input.Now.Add(-7*24*time.Hour).Unix(), input.NodeID, input.ReportID); err != nil {
		return NodeReportResult{}, fmt.Errorf("expire node report receipts: %w", err)
	}
	expiredDeviceUsers, err := expireNodeDevices(ctx, tx, input.Now)
	if err != nil {
		return NodeReportResult{}, err
	}
	for _, userID := range expiredDeviceUsers {
		deviceUsers[userID] = struct{}{}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_user_online WHERE expires_at <= ?`, input.Now.Unix()); err != nil {
		return NodeReportResult{}, fmt.Errorf("expire node online state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return NodeReportResult{}, fmt.Errorf("commit node report: %w", err)
	}
	result.DeviceUserIDs = make([]int64, 0, len(deviceUsers))
	for userID := range deviceUsers {
		result.DeviceUserIDs = append(result.DeviceUserIDs, userID)
	}
	sort.Slice(result.DeviceUserIDs, func(i, j int) bool { return result.DeviceUserIDs[i] < result.DeviceUserIDs[j] })
	return result, nil
}

func authorizeReportedUsers(ctx context.Context, tx *sql.Tx, input NodeReportInput) error {
	countNonEmpty := 0
	if len(input.Traffic) > 0 {
		countNonEmpty++
	}
	if len(input.Alive) > 0 {
		countNonEmpty++
	}
	if len(input.Online) > 0 {
		countNonEmpty++
	}
	if countNonEmpty == 0 {
		return nil
	}
	userIDs := make([]int64, 0, len(input.Traffic)+len(input.Alive)+len(input.Online))
	if countNonEmpty == 1 {
		for userID := range input.Traffic {
			userIDs = append(userIDs, userID)
		}
		for userID := range input.Alive {
			userIDs = append(userIDs, userID)
		}
		for userID := range input.Online {
			userIDs = append(userIDs, userID)
		}
	} else {
		unique := make(map[int64]struct{}, cap(userIDs))
		for userID := range input.Traffic {
			unique[userID] = struct{}{}
		}
		for userID := range input.Alive {
			unique[userID] = struct{}{}
		}
		for userID := range input.Online {
			unique[userID] = struct{}{}
		}
		for userID := range unique {
			userIDs = append(userIDs, userID)
		}
	}
	for _, userID := range userIDs {
		if userID < 1 {
			return fmt.Errorf("%w: invalid reported user", ErrInvalidInput)
		}
	}
	sort.Slice(userIDs, func(i, j int) bool { return userIDs[i] < userIDs[j] })
	const queryBatchSize = 500
	for start := 0; start < len(userIDs); start += queryBatchSize {
		end := min(start+queryBatchSize, len(userIDs))
		batch := userIDs[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch)+1)
		args = append(args, input.NodeID)
		for _, userID := range batch {
			args = append(args, userID)
		}
		var authorized int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(DISTINCT u.id)
			FROM users u
			JOIN node_group_memberships ngm ON ngm.group_id = u.group_id
			WHERE ngm.node_id = ? AND u.id IN (`+placeholders+`)
		`, args...).Scan(&authorized); err != nil {
			return fmt.Errorf("authorize reported users: %w", err)
		}
		if authorized != len(batch) {
			return fmt.Errorf("%w: report contains a user outside the node groups", ErrInvalidInput)
		}
	}
	return nil
}

func reportTrafficHash(traffic map[int64]TrafficUsage) []byte {
	userIDs := make([]int64, 0, len(traffic))
	for userID := range traffic {
		userIDs = append(userIDs, userID)
	}
	sort.Slice(userIDs, func(i, j int) bool { return userIDs[i] < userIDs[j] })
	hash := sha256.New()
	var encoded [24]byte
	for _, userID := range userIDs {
		usage := traffic[userID]
		binary.BigEndian.PutUint64(encoded[0:8], uint64(userID))
		binary.BigEndian.PutUint64(encoded[8:16], uint64(usage.Upload))
		binary.BigEndian.PutUint64(encoded[16:24], uint64(usage.Download))
		_, _ = hash.Write(encoded[:])
	}
	return hash.Sum(nil)
}

func applyTraffic(ctx context.Context, tx *sql.Tx, input NodeReportInput, rateMicros int64) error {
	reportKey, err := randomReportKey()
	if err != nil {
		return err
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO node_report_traffic_stage (report_key, user_id, upload, download, weighted_upload, weighted_download)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare traffic stage: %w", err)
	}
	var totalUpload, totalDownload int64
	for userID, traffic := range input.Traffic {
		weightedUpload, err := weightedTraffic(traffic.Upload, rateMicros)
		if err != nil {
			statement.Close()
			return err
		}
		weightedDownload, err := weightedTraffic(traffic.Download, rateMicros)
		if err != nil {
			statement.Close()
			return err
		}
		if totalUpload > math.MaxInt64-traffic.Upload || totalDownload > math.MaxInt64-traffic.Download {
			statement.Close()
			return fmt.Errorf("%w: node traffic total overflows", ErrInvalidInput)
		}
		totalUpload += traffic.Upload
		totalDownload += traffic.Download
		if _, err := statement.ExecContext(ctx, reportKey, userID, traffic.Upload, traffic.Download, weightedUpload, weightedDownload); err != nil {
			statement.Close()
			return fmt.Errorf("stage node traffic: %w", err)
		}
	}
	if err := statement.Close(); err != nil {
		return fmt.Errorf("close traffic stage: %w", err)
	}
	var userOverflow bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM node_report_traffic_stage s
			JOIN users u ON u.id = s.user_id
			WHERE s.report_key = ?
			  AND (u.traffic_u > ? - s.weighted_upload OR u.traffic_d > ? - s.weighted_download)
		)
	`, reportKey, int64(math.MaxInt64), int64(math.MaxInt64)).Scan(&userOverflow); err != nil {
		return fmt.Errorf("check user traffic overflow: %w", err)
	}
	if userOverflow {
		return fmt.Errorf("%w: user traffic total overflows", ErrInvalidInput)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE users SET
			traffic_u = traffic_u + COALESCE((SELECT s.weighted_upload FROM node_report_traffic_stage s WHERE s.report_key = ? AND s.user_id = users.id), 0),
			traffic_d = traffic_d + COALESCE((SELECT s.weighted_download FROM node_report_traffic_stage s WHERE s.report_key = ? AND s.user_id = users.id), 0),
			updated_at = ?
		WHERE id IN (SELECT user_id FROM node_report_traffic_stage WHERE report_key = ?)
	`, reportKey, reportKey, input.Now.Unix(), reportKey); err != nil {
		return fmt.Errorf("increment user traffic: %w", err)
	}

	recordAt := time.Date(input.Now.UTC().Year(), input.Now.UTC().Month(), input.Now.UTC().Day(), 0, 0, 0, 0, time.UTC).Unix()
	var userStatsOverflow bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM node_report_traffic_stage s
			JOIN user_traffic_stats us
			  ON us.user_id = s.user_id AND us.rate_micros = ? AND us.record_at = ? AND us.record_type = 'd'
			WHERE s.report_key = ?
			  AND (us.upload > ? - s.weighted_upload OR us.download > ? - s.weighted_download)
		)
	`, rateMicros, recordAt, reportKey, int64(math.MaxInt64), int64(math.MaxInt64)).Scan(&userStatsOverflow); err != nil {
		return fmt.Errorf("check user traffic stats overflow: %w", err)
	}
	if userStatsOverflow {
		return fmt.Errorf("%w: user traffic statistics overflow", ErrInvalidInput)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_traffic_stats (user_id, rate_micros, record_at, record_type, upload, download, created_at, updated_at)
		SELECT s.user_id, ?, ?, 'd', s.weighted_upload, s.weighted_download, ?, ?
		FROM node_report_traffic_stage s JOIN users u ON u.id = s.user_id
		WHERE s.report_key = ?
		ON CONFLICT(user_id, rate_micros, record_at, record_type) DO UPDATE SET
			upload = upload + excluded.upload,
			download = download + excluded.download,
			updated_at = excluded.updated_at
	`, rateMicros, recordAt, input.Now.Unix(), input.Now.Unix(), reportKey); err != nil {
		return fmt.Errorf("update user traffic stats: %w", err)
	}

	var nodeUpload, nodeDownload int64
	if err := tx.QueryRowContext(ctx, `SELECT traffic_u, traffic_d FROM nodes WHERE id = ?`, input.NodeID).Scan(&nodeUpload, &nodeDownload); err != nil {
		return fmt.Errorf("read node traffic: %w", err)
	}
	if nodeUpload > math.MaxInt64-totalUpload || nodeDownload > math.MaxInt64-totalDownload {
		return fmt.Errorf("%w: node traffic total overflows", ErrInvalidInput)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE nodes SET traffic_u = traffic_u + ?, traffic_d = traffic_d + ? WHERE id = ?`, totalUpload, totalDownload, input.NodeID); err != nil {
		return fmt.Errorf("increment node traffic: %w", err)
	}
	var statsUpload, statsDownload int64
	err = tx.QueryRowContext(ctx, `
		SELECT upload, download FROM node_traffic_stats
		WHERE node_id = ? AND record_at = ? AND record_type = 'd'
	`, input.NodeID, recordAt).Scan(&statsUpload, &statsDownload)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read node traffic stats: %w", err)
	}
	if err == nil && (statsUpload > math.MaxInt64-totalUpload || statsDownload > math.MaxInt64-totalDownload) {
		return fmt.Errorf("%w: node traffic statistics overflow", ErrInvalidInput)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO node_traffic_stats (node_id, record_at, record_type, upload, download, created_at, updated_at)
		VALUES (?, ?, 'd', ?, ?, ?, ?)
		ON CONFLICT(node_id, record_at, record_type) DO UPDATE SET
			upload = upload + excluded.upload,
			download = download + excluded.download,
			updated_at = excluded.updated_at
	`, input.NodeID, recordAt, totalUpload, totalDownload, input.Now.Unix(), input.Now.Unix()); err != nil {
		return fmt.Errorf("update node traffic stats: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_report_traffic_stage WHERE report_key = ?`, reportKey); err != nil {
		return fmt.Errorf("clear traffic stage: %w", err)
	}
	return nil
}

func weightedTraffic(value, rateMicros int64) (int64, error) {
	if value < 0 || rateMicros < 1 {
		return 0, fmt.Errorf("%w: invalid weighted traffic", ErrInvalidInput)
	}
	whole := value / trafficRateScale
	remainder := value % trafficRateScale
	if whole > math.MaxInt64/rateMicros {
		return 0, fmt.Errorf("%w: weighted traffic overflows", ErrInvalidInput)
	}
	weightedWhole := whole * rateMicros
	weightedRemainder := (remainder * rateMicros) / trafficRateScale
	if weightedWhole > math.MaxInt64-weightedRemainder {
		return 0, fmt.Errorf("%w: weighted traffic overflows", ErrInvalidInput)
	}
	return weightedWhole + weightedRemainder, nil
}

func applyAlive(ctx context.Context, tx *sql.Tx, nodeID int64, alive map[int64][]string, now time.Time) error {
	for userID, ips := range alive {
		if _, err := tx.ExecContext(ctx, `DELETE FROM node_device_ips WHERE node_id = ? AND user_id = ?`, nodeID, userID); err != nil {
			return fmt.Errorf("replace node devices: %w", err)
		}
		for _, ip := range ips {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO node_device_ips (node_id, user_id, ip, expires_at)
				SELECT ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM users WHERE id = ?)
			`, nodeID, userID, ip, now.Add(deviceStateTTL).Unix(), userID); err != nil {
				return fmt.Errorf("insert node device: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE users SET
				online_count = (SELECT COUNT(DISTINCT ip) FROM node_device_ips WHERE user_id = ? AND expires_at > ?),
				last_online_at = ?
			WHERE id = ?
		`, userID, now.Unix(), now.Unix(), userID); err != nil {
			return fmt.Errorf("update user device state: %w", err)
		}
	}
	return nil
}

func expireNodeDevices(ctx context.Context, tx *sql.Tx, now time.Time) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT user_id FROM node_device_ips WHERE expires_at <= ?`, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("list expired node device users: %w", err)
	}
	var userIDs []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan expired node device user: %w", err)
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close expired node device users: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_device_ips WHERE expires_at <= ?`, now.Unix()); err != nil {
		return nil, fmt.Errorf("expire node devices: %w", err)
	}
	const queryBatchSize = 500
	for start := 0; start < len(userIDs); start += queryBatchSize {
		end := min(start+queryBatchSize, len(userIDs))
		batch := userIDs[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch)+1)
		args = append(args, now.Unix())
		for _, userID := range batch {
			args = append(args, userID)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE users SET online_count = (
				SELECT COUNT(DISTINCT ip) FROM node_device_ips
				WHERE user_id = users.id AND expires_at > ?
			) WHERE id IN (`+placeholders+`)
		`, args...); err != nil {
			return nil, fmt.Errorf("reconcile expired node device users: %w", err)
		}
	}
	return userIDs, nil
}

func applyOnline(ctx context.Context, tx *sql.Tx, nodeID int64, online map[int64]int64, now time.Time) error {
	for userID, connections := range online {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO node_user_online (node_id, user_id, connections, expires_at)
			SELECT ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM users WHERE id = ?)
			ON CONFLICT(node_id, user_id) DO UPDATE SET connections = excluded.connections, expires_at = excluded.expires_at
		`, nodeID, userID, connections, now.Add(deviceStateTTL).Unix(), userID); err != nil {
			return fmt.Errorf("update node online state: %w", err)
		}
	}
	return nil
}

func randomReportKey() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate traffic stage key: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}

func (s *Store) GetRuntimeUserTraffic(ctx context.Context, userID int64) (TrafficUsage, error) {
	var traffic TrafficUsage
	err := s.db.QueryRowContext(ctx, `SELECT traffic_u, traffic_d FROM users WHERE id = ?`, userID).Scan(&traffic.Upload, &traffic.Download)
	if errors.Is(err, sql.ErrNoRows) {
		return TrafficUsage{}, ErrNotFound
	}
	if err != nil {
		return TrafficUsage{}, fmt.Errorf("get runtime user traffic: %w", err)
	}
	return traffic, nil
}

func (s *Store) GetNodeRuntimeState(ctx context.Context, nodeID int64) (NodeRuntimeState, error) {
	var state NodeRuntimeState
	var status, metrics sql.NullString
	var updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT node_id, status_json, metrics_json, updated_at
		FROM node_runtime_state WHERE node_id = ?
	`, nodeID).Scan(&state.NodeID, &status, &metrics, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return NodeRuntimeState{}, ErrNotFound
	}
	if err != nil {
		return NodeRuntimeState{}, fmt.Errorf("get node runtime state: %w", err)
	}
	if status.Valid {
		state.Status = json.RawMessage(status.String)
	}
	if metrics.Valid {
		state.Metrics = json.RawMessage(metrics.String)
	}
	state.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return state, nil
}

func (s *Store) ClearNodeDevices(ctx context.Context, nodeIDs []int64, now time.Time) ([]int64, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin clear node devices: %w", err)
	}
	defer tx.Rollback()
	affected := make(map[int64]struct{})
	for _, nodeID := range nodeIDs {
		if nodeID < 1 {
			return nil, fmt.Errorf("%w: invalid node id", ErrInvalidInput)
		}
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT user_id FROM node_device_ips WHERE node_id = ?`, nodeID)
		if err != nil {
			return nil, fmt.Errorf("list node device users: %w", err)
		}
		var userIDs []int64
		for rows.Next() {
			var userID int64
			if err := rows.Scan(&userID); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan node device user: %w", err)
			}
			userIDs = append(userIDs, userID)
			affected[userID] = struct{}{}
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close node device users: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM node_device_ips WHERE node_id = ?`, nodeID); err != nil {
			return nil, fmt.Errorf("clear node devices: %w", err)
		}
		for _, userID := range userIDs {
			if _, err := tx.ExecContext(ctx, `
				UPDATE users SET online_count = (
					SELECT COUNT(DISTINCT ip) FROM node_device_ips WHERE user_id = ? AND expires_at > ?
				) WHERE id = ?
			`, userID, now.Unix(), userID); err != nil {
				return nil, fmt.Errorf("reconcile user devices: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit clear node devices: %w", err)
	}
	userIDs := make([]int64, 0, len(affected))
	for userID := range affected {
		userIDs = append(userIDs, userID)
	}
	sort.Slice(userIDs, func(i, j int) bool { return userIDs[i] < userIDs[j] })
	return userIDs, nil
}

func (s *Store) ListRuntimeNodeIDsForUsers(ctx context.Context, userIDs []int64) ([]int64, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	targets := make(map[int64]struct{})
	const queryBatchSize = 500
	for start := 0; start < len(userIDs); start += queryBatchSize {
		end := min(start+queryBatchSize, len(userIDs))
		batch := userIDs[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch))
		for _, userID := range batch {
			if userID < 1 {
				return nil, fmt.Errorf("%w: invalid user id", ErrInvalidInput)
			}
			args = append(args, userID)
		}
		rows, err := s.db.QueryContext(ctx, `
			SELECT DISTINCT ngm.node_id
			FROM users u
			JOIN node_group_memberships ngm ON ngm.group_id = u.group_id
			JOIN nodes n ON n.id = ngm.node_id
			WHERE u.id IN (`+placeholders+`)
			  AND n.enabled = 1 AND n.runtime_config IS NOT NULL
		`, args...)
		if err != nil {
			return nil, fmt.Errorf("list runtime nodes for users: %w", err)
		}
		for rows.Next() {
			var nodeID int64
			if err := rows.Scan(&nodeID); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan runtime node for users: %w", err)
			}
			targets[nodeID] = struct{}{}
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close runtime nodes for users: %w", err)
		}
	}
	nodeIDs := make([]int64, 0, len(targets))
	for nodeID := range targets {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })
	return nodeIDs, nil
}

func (s *Store) ListUserDevices(ctx context.Context, userIDs []int64, now time.Time) (map[int64][]string, error) {
	devices := make(map[int64][]string)
	if len(userIDs) == 0 {
		return devices, nil
	}
	for _, userID := range userIDs {
		if userID < 1 {
			return nil, fmt.Errorf("%w: invalid user id", ErrInvalidInput)
		}
	}
	const queryBatchSize = 500
	for start := 0; start < len(userIDs); start += queryBatchSize {
		end := min(start+queryBatchSize, len(userIDs))
		batch := userIDs[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch)+1)
		for _, userID := range batch {
			args = append(args, userID)
		}
		args = append(args, now.Unix())
		rows, err := s.db.QueryContext(ctx, `
			SELECT user_id, ip FROM node_device_ips
			WHERE user_id IN (`+placeholders+`) AND expires_at > ?
			GROUP BY user_id, ip
			ORDER BY user_id, ip
		`, args...)
		if err != nil {
			return nil, fmt.Errorf("list user devices: %w", err)
		}
		for rows.Next() {
			var userID int64
			var ip string
			if err := rows.Scan(&userID, &ip); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan user device: %w", err)
			}
			devices[userID] = append(devices[userID], ip)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close user devices: %w", err)
		}
	}
	return devices, nil
}
