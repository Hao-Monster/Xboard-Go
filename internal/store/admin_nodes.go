package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxAdminNodePageSize = 500
	maxAdminNodeBatch    = 500
)

func (s *Store) ListAdminNodes(ctx context.Context, filter AdminNodeFilter, now time.Time) (AdminNodePage, error) {
	filter.Query = strings.TrimSpace(filter.Query)
	filter.Type = strings.ToLower(strings.TrimSpace(filter.Type))
	if filter.Page < 1 || filter.PageSize < 1 || filter.PageSize > maxAdminNodePageSize ||
		len(filter.Query) > 255 || !utf8.ValidString(filter.Query) || strings.IndexFunc(filter.Query, unicode.IsControl) >= 0 ||
		(filter.Type != "" && !isSupportedNodeType(filter.Type)) ||
		(filter.MachineID != nil && *filter.MachineID < 1) || (filter.MachineID != nil && filter.Unassigned) {
		return AdminNodePage{}, fmt.Errorf("%w: invalid administrator node filter", ErrInvalidInput)
	}
	if filter.Page-1 > int(^uint(0)>>1)/filter.PageSize {
		return AdminNodePage{}, fmt.Errorf("%w: administrator node page offset is too large", ErrInvalidInput)
	}

	where, arguments := adminNodeWhere(filter)
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes n `+where, arguments...).Scan(&total); err != nil {
		return AdminNodePage{}, fmt.Errorf("count administrator nodes: %w", err)
	}

	pageArguments := make([]any, 0, len(arguments)+3)
	pageArguments = append(pageArguments, now.Unix())
	pageArguments = append(pageArguments, arguments...)
	pageArguments = append(pageArguments, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.admin_revision, n.name, n.type, n.host, n.port, n.show, n.enabled, n.sort,
		       COALESCE(d.configured_rate_micros, n.rate_micros), n.traffic_u, n.traffic_d,
		       n.runtime_config IS NOT NULL, n.last_check_at, n.last_push_at, n.machine_id, n.created_at, n.updated_at,
		       m.name, COALESCE(online.online_count, 0)
		FROM nodes n
		LEFT JOIN node_protocol_definitions d ON d.node_id = n.id
		LEFT JOIN server_machines m ON m.id = n.machine_id
		LEFT JOIN (
			SELECT node_id, SUM(connections) AS online_count
			FROM node_user_online WHERE expires_at > ? GROUP BY node_id
		) online ON online.node_id = n.id
		`+where+`
		ORDER BY n.sort, n.id
		LIMIT ? OFFSET ?
	`, pageArguments...)
	if err != nil {
		return AdminNodePage{}, fmt.Errorf("list administrator nodes: %w", err)
	}
	defer rows.Close()
	items := make([]AdminNode, 0, filter.PageSize)
	for rows.Next() {
		item, err := scanAdminNode(rows)
		if err != nil {
			return AdminNodePage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return AdminNodePage{}, fmt.Errorf("list administrator nodes: %w", err)
	}
	if err := rows.Close(); err != nil {
		return AdminNodePage{}, fmt.Errorf("close administrator nodes: %w", err)
	}
	if err := s.loadAdminNodeGroups(ctx, items); err != nil {
		return AdminNodePage{}, err
	}
	return AdminNodePage{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func adminNodeWhere(filter AdminNodeFilter) (string, []any) {
	conditions := make([]string, 0, 6)
	arguments := make([]any, 0, 8)
	if filter.Query != "" {
		pattern := "%" + escapeSQLiteLike(strings.ToLower(filter.Query)) + "%"
		conditions = append(conditions, `(LOWER(n.name) LIKE ? ESCAPE '\' OR LOWER(n.host) LIKE ? ESCAPE '\' OR CAST(n.id AS TEXT) = ?)`)
		arguments = append(arguments, pattern, pattern, filter.Query)
	}
	if filter.Type != "" {
		conditions = append(conditions, "n.type = ?")
		arguments = append(arguments, filter.Type)
	}
	if filter.Show != nil {
		conditions = append(conditions, "n.show = ?")
		arguments = append(arguments, *filter.Show)
	}
	if filter.Enabled != nil {
		conditions = append(conditions, "n.enabled = ?")
		arguments = append(arguments, *filter.Enabled)
	}
	if filter.MachineID != nil {
		conditions = append(conditions, "n.machine_id = ?")
		arguments = append(arguments, *filter.MachineID)
	} else if filter.Unassigned {
		conditions = append(conditions, "n.machine_id IS NULL")
	}
	if len(conditions) == 0 {
		return "", arguments
	}
	return "WHERE " + strings.Join(conditions, " AND "), arguments
}

func escapeSQLiteLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func isSupportedNodeType(value string) bool {
	_, ok := supportedNodeTypes[value]
	return ok
}

func scanAdminNode(row rowScanner) (AdminNode, error) {
	var item AdminNode
	var machineID sql.NullInt64
	var machineName sql.NullString
	var lastCheckAt, lastPushAt sql.NullInt64
	var rateMicros, createdAt, updatedAt int64
	if err := row.Scan(
		&item.ID, &item.Revision, &item.Name, &item.Type, &item.Host, &item.Port, &item.Show, &item.Enabled, &item.Sort,
		&rateMicros, &item.TrafficUpload, &item.TrafficDownload, &item.RuntimeConfigured,
		&lastCheckAt, &lastPushAt, &machineID, &createdAt, &updatedAt, &machineName, &item.OnlineCount,
	); err != nil {
		return AdminNode{}, fmt.Errorf("scan administrator node: %w", err)
	}
	item.Rate = float64(rateMicros) / 1_000_000
	if machineID.Valid {
		item.MachineID = &machineID.Int64
	}
	if machineName.Valid {
		item.MachineName = &machineName.String
	}
	if lastCheckAt.Valid {
		value := time.Unix(lastCheckAt.Int64, 0).UTC()
		item.LastCheckAt = &value
	}
	if lastPushAt.Valid {
		value := time.Unix(lastPushAt.Int64, 0).UTC()
		item.LastPushAt = &value
	}
	item.CreatedAt = time.Unix(createdAt, 0).UTC()
	item.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	item.GroupIDs = []int64{}
	return item, nil
}

func (s *Store) loadAdminNodeGroups(ctx context.Context, items []AdminNode) error {
	if len(items) == 0 {
		return nil
	}
	index := make(map[int64]int, len(items))
	arguments := make([]any, len(items))
	for position := range items {
		index[items[position].ID] = position
		arguments[position] = items[position].ID
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT node_id, group_id FROM node_group_memberships
		WHERE node_id IN (`+sqlPlaceholders(len(items))+`) ORDER BY node_id, group_id
	`, arguments...)
	if err != nil {
		return fmt.Errorf("list administrator node groups: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var nodeID, groupID int64
		if err := rows.Scan(&nodeID, &groupID); err != nil {
			return fmt.Errorf("scan administrator node group: %w", err)
		}
		if position, ok := index[nodeID]; ok {
			items[position].GroupIDs = append(items[position].GroupIDs, groupID)
		}
	}
	return rows.Err()
}

func (s *Store) UpdateAdminNode(ctx context.Context, nodeID int64, input UpdateAdminNodeInput, now time.Time) (Node, AdminNodeMutation, error) {
	name, host, err := normalizeAdminNodeFields(input.Name, input.Host, input.Port, input.Sort)
	if err != nil || nodeID < 1 || input.Revision < 1 {
		if err != nil {
			return Node{}, AdminNodeMutation{}, err
		}
		return Node{}, AdminNodeMutation{}, fmt.Errorf("%w: invalid administrator node update", ErrInvalidInput)
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, AdminNodeMutation{}, fmt.Errorf("begin administrator node update: %w", err)
	}
	defer tx.Rollback()
	current, err := loadAdminNodeTarget(ctx, tx, nodeID)
	if err != nil {
		return Node{}, AdminNodeMutation{}, err
	}
	if current.Revision != input.Revision {
		return Node{}, AdminNodeMutation{}, ErrConflict
	}
	machineID := current.MachineID
	if input.MachineIDSet {
		machineID = input.MachineID
		if err := requireAdminNodeMachine(ctx, tx, machineID); err != nil {
			return Node{}, AdminNodeMutation{}, err
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE nodes SET name = ?, host = ?, port = ?, show = ?, enabled = ?, sort = ?, machine_id = ?,
			admin_revision = admin_revision + 1, updated_at = ?
		WHERE id = ? AND admin_revision = ?
	`, name, host, input.Port, input.Show, input.Enabled, input.Sort, machineID, now.Unix(), nodeID, input.Revision)
	if err != nil {
		return Node{}, AdminNodeMutation{}, fmt.Errorf("update administrator node: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return Node{}, AdminNodeMutation{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return Node{}, AdminNodeMutation{}, fmt.Errorf("commit administrator node update: %w", err)
	}
	updated, err := s.GetNode(ctx, nodeID)
	if err != nil {
		return Node{}, AdminNodeMutation{}, err
	}
	mutation := mutationForNodes([]adminNodeTarget{current}, []Node{updated})
	if (current.Enabled && !updated.Enabled) || !sameOptionalInt64(current.MachineID, updated.MachineID) {
		mutation.ClearNodeIDs = []int64{nodeID}
	}
	return updated, mutation, nil
}

func normalizeAdminNodeFields(name, host, port string, sortPosition int) (string, string, error) {
	name = strings.TrimSpace(name)
	host = strings.TrimSpace(host)
	if name == "" || len(name) > 255 || !utf8.ValidString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0 ||
		host == "" || len(host) > 255 || !utf8.ValidString(host) || strings.IndexFunc(host, unicode.IsControl) >= 0 ||
		!validNodePort(port) || sortPosition < 0 || sortPosition > 1_000_000_000 {
		return "", "", fmt.Errorf("%w: invalid administrator node fields", ErrInvalidInput)
	}
	return name, host, nil
}

func requireAdminNodeMachine(ctx context.Context, tx *sql.Tx, machineID *int64) error {
	if machineID == nil {
		return nil
	}
	if *machineID < 1 {
		return fmt.Errorf("%w: invalid machine id", ErrInvalidInput)
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM server_machines WHERE id = ?)`, *machineID).Scan(&exists); err != nil {
		return fmt.Errorf("validate administrator node machine: %w", err)
	}
	if !exists {
		return fmt.Errorf("%w: machine does not exist", ErrInvalidInput)
	}
	return nil
}

func (s *Store) CopyAdminNode(ctx context.Context, nodeID, revision int64, now time.Time) (Node, AdminNodeMutation, error) {
	if nodeID < 1 || revision < 1 {
		return Node{}, AdminNodeMutation{}, fmt.Errorf("%w: invalid administrator node copy", ErrInvalidInput)
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, AdminNodeMutation{}, fmt.Errorf("begin administrator node copy: %w", err)
	}
	defer tx.Rollback()
	current, err := loadAdminNodeTarget(ctx, tx, nodeID)
	if err != nil {
		return Node{}, AdminNodeMutation{}, err
	}
	if current.Revision != revision {
		return Node{}, AdminNodeMutation{}, ErrConflict
	}
	var name string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM nodes WHERE id = ?`, nodeID).Scan(&name); err != nil {
		return Node{}, AdminNodeMutation{}, fmt.Errorf("read administrator node copy name: %w", err)
	}
	copyName := copiedNodeName(name)
	result, err := tx.ExecContext(ctx, `
		INSERT INTO nodes (
			name, type, host, port, show, enabled, sort, machine_id, rate_micros, runtime_config,
			traffic_u, traffic_d, last_check_at, last_push_at, created_at, updated_at, admin_revision
		)
		SELECT ?, type, host, port, 0, enabled, sort + 1, machine_id, rate_micros, runtime_config,
		       0, 0, NULL, NULL, ?, ?, 1
		FROM nodes WHERE id = ? AND admin_revision = ?
	`, copyName, now.Unix(), now.Unix(), nodeID, revision)
	if err != nil {
		return Node{}, AdminNodeMutation{}, fmt.Errorf("copy administrator node: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return Node{}, AdminNodeMutation{}, ErrConflict
	}
	copyID, err := result.LastInsertId()
	if err != nil {
		return Node{}, AdminNodeMutation{}, fmt.Errorf("read administrator node copy id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO node_protocol_definitions (
			node_id, external_code, parent_id, server_port, tags_json, protocol_settings_json,
			rate_time_enabled, rate_time_ranges_json, custom_outbounds_json, custom_routes_json,
			cert_config_json, transfer_enable, configured_rate_micros
		)
		SELECT ?, NULL, parent_id, server_port, tags_json, protocol_settings_json,
		       rate_time_enabled, rate_time_ranges_json, custom_outbounds_json, custom_routes_json,
		       cert_config_json, transfer_enable, configured_rate_micros
		FROM node_protocol_definitions WHERE node_id = ?
	`, copyID, nodeID); err != nil {
		return Node{}, AdminNodeMutation{}, fmt.Errorf("copy administrator node protocol: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO node_group_memberships (node_id, group_id)
		SELECT ?, group_id FROM node_group_memberships WHERE node_id = ?
	`, copyID, nodeID); err != nil {
		return Node{}, AdminNodeMutation{}, fmt.Errorf("copy administrator node groups: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO node_route_memberships (node_id, route_id)
		SELECT ?, route_id FROM node_route_memberships WHERE node_id = ?
	`, copyID, nodeID); err != nil {
		return Node{}, AdminNodeMutation{}, fmt.Errorf("copy administrator node routes: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Node{}, AdminNodeMutation{}, fmt.Errorf("commit administrator node copy: %w", err)
	}
	created, err := s.GetNode(ctx, copyID)
	if err != nil {
		return Node{}, AdminNodeMutation{}, err
	}
	return created, mutationForNodes(nil, []Node{created}), nil
}

func copiedNodeName(name string) string {
	const suffix = " - 副本"
	for len(name)+len(suffix) > 255 {
		_, size := utf8.DecodeLastRuneInString(name)
		if size < 1 {
			break
		}
		name = name[:len(name)-size]
	}
	return strings.TrimSpace(name) + suffix
}

func (s *Store) ReorderAdminNodes(ctx context.Context, targets []AdminNodeRevision, now time.Time) (AdminNodeMutation, error) {
	if err := validateAdminNodeRevisions(targets); err != nil {
		return AdminNodeMutation{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminNodeMutation{}, fmt.Errorf("begin administrator node reorder: %w", err)
	}
	defer tx.Rollback()
	current, err := loadAdminNodeTargets(ctx, tx, targets)
	if err != nil {
		return AdminNodeMutation{}, err
	}
	sortSlots := make([]int, 0, len(targets))
	seenSortSlots := make(map[int]struct{}, len(targets))
	for _, target := range current {
		if _, exists := seenSortSlots[target.Sort]; exists {
			return AdminNodeMutation{}, fmt.Errorf("%w: reordered nodes must have distinct sort positions", ErrConflict)
		}
		seenSortSlots[target.Sort] = struct{}{}
		sortSlots = append(sortSlots, target.Sort)
	}
	sort.Ints(sortSlots)
	for position, requested := range targets {
		target := current[requested.ID]
		result, err := tx.ExecContext(ctx, `
			UPDATE nodes SET sort = ?, admin_revision = admin_revision + 1, updated_at = ?
			WHERE id = ? AND admin_revision = ?
		`, sortSlots[position], now.Unix(), target.ID, target.Revision)
		if err != nil {
			return AdminNodeMutation{}, fmt.Errorf("reorder administrator node: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return AdminNodeMutation{}, ErrConflict
		}
	}
	if err := tx.Commit(); err != nil {
		return AdminNodeMutation{}, fmt.Errorf("commit administrator node reorder: %w", err)
	}
	return mutationForTargets(current), nil
}

func (s *Store) UpdateAdminNodeStates(ctx context.Context, input AdminNodeStateInput, now time.Time) (AdminNodeMutation, error) {
	if input.Show == nil && input.Enabled == nil && !input.MachineIDSet {
		return AdminNodeMutation{}, fmt.Errorf("%w: administrator node state is required", ErrInvalidInput)
	}
	mutation, err := s.mutateAdminNodeTargetsWithSetup(ctx, input.Targets, now, "update states", func(tx *sql.Tx) error {
		if input.MachineIDSet {
			return requireAdminNodeMachine(ctx, tx, input.MachineID)
		}
		return nil
	}, func(tx *sql.Tx, target adminNodeTarget, _ int) error {
		show, enabled, machineID := target.Show, target.Enabled, target.MachineID
		if input.Show != nil {
			show = *input.Show
		}
		if input.Enabled != nil {
			enabled = *input.Enabled
		}
		if input.MachineIDSet {
			machineID = input.MachineID
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE nodes SET show = ?, enabled = ?, machine_id = ?, admin_revision = admin_revision + 1, updated_at = ?
			WHERE id = ? AND admin_revision = ?
		`, show, enabled, machineID, now.Unix(), target.ID, target.Revision)
		if err != nil {
			return fmt.Errorf("update administrator node states: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrConflict
		}
		return nil
	})
	if err != nil {
		return AdminNodeMutation{}, err
	}
	if input.MachineIDSet && input.MachineID != nil {
		mutation.MachineIDs = appendUniqueSortedInt64(mutation.MachineIDs, *input.MachineID)
	}
	if (input.Enabled != nil && !*input.Enabled) || input.MachineIDSet {
		mutation.ClearNodeIDs = append([]int64(nil), mutation.NodeIDs...)
	}
	return mutation, nil
}

func (s *Store) ResetAdminNodeTraffic(ctx context.Context, targets []AdminNodeRevision, now time.Time) (AdminNodeMutation, error) {
	return s.mutateAdminNodeTargets(ctx, targets, now, "reset traffic", func(tx *sql.Tx, target adminNodeTarget, _ int) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE nodes SET traffic_u = 0, traffic_d = 0, admin_revision = admin_revision + 1, updated_at = ?
			WHERE id = ? AND admin_revision = ?
		`, now.Unix(), target.ID, target.Revision)
		if err != nil {
			return fmt.Errorf("reset administrator node traffic: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrConflict
		}
		return nil
	})
}

func (s *Store) DeleteAdminNodes(ctx context.Context, targets []AdminNodeRevision, now time.Time) (AdminNodeMutation, error) {
	if err := validateAdminNodeRevisions(targets); err != nil {
		return AdminNodeMutation{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminNodeMutation{}, fmt.Errorf("begin delete administrator nodes: %w", err)
	}
	defer tx.Rollback()
	current, err := loadAdminNodeTargets(ctx, tx, targets)
	if err != nil {
		return AdminNodeMutation{}, err
	}
	arguments := adminNodeTargetArguments(targets)
	placeholders := sqlPlaceholders(len(targets))
	var referenced bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM node_protocol_definitions
			WHERE parent_id IN (`+placeholders+`) AND node_id NOT IN (`+placeholders+`)
		)
	`, append(arguments, arguments...)...).Scan(&referenced); err != nil {
		return AdminNodeMutation{}, fmt.Errorf("check administrator node child references: %w", err)
	}
	if referenced {
		return AdminNodeMutation{}, ErrConflict
	}
	userRows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT user_id FROM node_device_ips WHERE node_id IN (`+placeholders+`) ORDER BY user_id
	`, arguments...)
	if err != nil {
		return AdminNodeMutation{}, fmt.Errorf("list administrator node device users: %w", err)
	}
	affectedUserIDs := make([]int64, 0)
	for userRows.Next() {
		var userID int64
		if err := userRows.Scan(&userID); err != nil {
			_ = userRows.Close()
			return AdminNodeMutation{}, fmt.Errorf("scan administrator node device user: %w", err)
		}
		affectedUserIDs = append(affectedUserIDs, userID)
	}
	if err := userRows.Close(); err != nil {
		return AdminNodeMutation{}, fmt.Errorf("close administrator node device users: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_device_ips WHERE node_id IN (`+placeholders+`)`, arguments...); err != nil {
		return AdminNodeMutation{}, fmt.Errorf("clear administrator node devices: %w", err)
	}
	if len(affectedUserIDs) > 0 {
		userArguments := make([]any, 0, len(affectedUserIDs)+1)
		userArguments = append(userArguments, now.Unix())
		for _, userID := range affectedUserIDs {
			userArguments = append(userArguments, userID)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE users SET online_count = (
				SELECT COUNT(DISTINCT ip) FROM node_device_ips
				WHERE user_id = users.id AND expires_at > ?
			) WHERE id IN (`+sqlPlaceholders(len(affectedUserIDs))+`)
		`, userArguments...); err != nil {
			return AdminNodeMutation{}, fmt.Errorf("reconcile administrator node devices: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM nodes WHERE id IN (`+placeholders+`)`, arguments...)
	if err != nil {
		return AdminNodeMutation{}, fmt.Errorf("delete administrator nodes: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != int64(len(targets)) {
		return AdminNodeMutation{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return AdminNodeMutation{}, fmt.Errorf("commit delete administrator nodes: %w", err)
	}
	mutation := mutationForTargets(current)
	mutation.AffectedUserIDs = affectedUserIDs
	return mutation, nil
}

type adminNodeTarget struct {
	ID        int64
	Revision  int64
	Show      bool
	Enabled   bool
	Sort      int
	MachineID *int64
}

func (s *Store) mutateAdminNodeTargets(
	ctx context.Context,
	targets []AdminNodeRevision,
	now time.Time,
	label string,
	mutate func(*sql.Tx, adminNodeTarget, int) error,
) (AdminNodeMutation, error) {
	return s.mutateAdminNodeTargetsWithSetup(ctx, targets, now, label, nil, mutate)
}

func (s *Store) mutateAdminNodeTargetsWithSetup(
	ctx context.Context,
	targets []AdminNodeRevision,
	now time.Time,
	label string,
	setup func(*sql.Tx) error,
	mutate func(*sql.Tx, adminNodeTarget, int) error,
) (AdminNodeMutation, error) {
	if err := validateAdminNodeRevisions(targets); err != nil {
		return AdminNodeMutation{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminNodeMutation{}, fmt.Errorf("begin administrator node %s: %w", label, err)
	}
	defer tx.Rollback()
	if setup != nil {
		if err := setup(tx); err != nil {
			return AdminNodeMutation{}, err
		}
	}
	current, err := loadAdminNodeTargets(ctx, tx, targets)
	if err != nil {
		return AdminNodeMutation{}, err
	}
	for position, requested := range targets {
		if err := mutate(tx, current[requested.ID], position); err != nil {
			return AdminNodeMutation{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AdminNodeMutation{}, fmt.Errorf("commit administrator node %s: %w", label, err)
	}
	return mutationForTargets(current), nil
}

func validateAdminNodeRevisions(targets []AdminNodeRevision) error {
	if len(targets) == 0 || len(targets) > maxAdminNodeBatch {
		return fmt.Errorf("%w: administrator node targets must contain 1 to %d items", ErrInvalidInput, maxAdminNodeBatch)
	}
	seen := make(map[int64]struct{}, len(targets))
	for _, target := range targets {
		if target.ID < 1 || target.Revision < 1 {
			return fmt.Errorf("%w: invalid administrator node target", ErrInvalidInput)
		}
		if _, exists := seen[target.ID]; exists {
			return fmt.Errorf("%w: duplicate administrator node target", ErrInvalidInput)
		}
		seen[target.ID] = struct{}{}
	}
	return nil
}

func loadAdminNodeTarget(ctx context.Context, tx *sql.Tx, nodeID int64) (adminNodeTarget, error) {
	var target adminNodeTarget
	var machineID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT id, admin_revision, show, enabled, sort, machine_id FROM nodes WHERE id = ?
	`, nodeID).Scan(&target.ID, &target.Revision, &target.Show, &target.Enabled, &target.Sort, &machineID); errors.Is(err, sql.ErrNoRows) {
		return adminNodeTarget{}, ErrNotFound
	} else if err != nil {
		return adminNodeTarget{}, fmt.Errorf("read administrator node target: %w", err)
	}
	if machineID.Valid {
		target.MachineID = &machineID.Int64
	}
	return target, nil
}

func loadAdminNodeTargets(ctx context.Context, tx *sql.Tx, requested []AdminNodeRevision) (map[int64]adminNodeTarget, error) {
	arguments := adminNodeTargetArguments(requested)
	rows, err := tx.QueryContext(ctx, `
		SELECT id, admin_revision, show, enabled, sort, machine_id FROM nodes
		WHERE id IN (`+sqlPlaceholders(len(arguments))+`)
	`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list administrator node targets: %w", err)
	}
	defer rows.Close()
	targets := make(map[int64]adminNodeTarget, len(requested))
	for rows.Next() {
		var target adminNodeTarget
		var machineID sql.NullInt64
		if err := rows.Scan(&target.ID, &target.Revision, &target.Show, &target.Enabled, &target.Sort, &machineID); err != nil {
			return nil, fmt.Errorf("scan administrator node target: %w", err)
		}
		if machineID.Valid {
			target.MachineID = &machineID.Int64
		}
		targets[target.ID] = target
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list administrator node targets: %w", err)
	}
	if len(targets) != len(requested) {
		return nil, ErrNotFound
	}
	for _, target := range requested {
		if targets[target.ID].Revision != target.Revision {
			return nil, ErrConflict
		}
	}
	return targets, nil
}

func adminNodeTargetArguments(targets []AdminNodeRevision) []any {
	arguments := make([]any, len(targets))
	for index := range targets {
		arguments[index] = targets[index].ID
	}
	return arguments
}

func sqlPlaceholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func mutationForTargets(targets map[int64]adminNodeTarget) AdminNodeMutation {
	nodeIDs := make([]int64, 0, len(targets))
	machineSet := make(map[int64]struct{})
	for _, target := range targets {
		nodeIDs = append(nodeIDs, target.ID)
		if target.MachineID != nil {
			machineSet[*target.MachineID] = struct{}{}
		}
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })
	return AdminNodeMutation{NodeIDs: nodeIDs, MachineIDs: sortedInt64Keys(machineSet)}
}

func mutationForNodes(previous []adminNodeTarget, current []Node) AdminNodeMutation {
	machineSet := make(map[int64]struct{})
	nodeSet := make(map[int64]struct{})
	for _, node := range previous {
		nodeSet[node.ID] = struct{}{}
		if node.MachineID != nil {
			machineSet[*node.MachineID] = struct{}{}
		}
	}
	for _, node := range current {
		nodeSet[node.ID] = struct{}{}
		if node.MachineID != nil {
			machineSet[*node.MachineID] = struct{}{}
		}
	}
	return AdminNodeMutation{NodeIDs: sortedInt64Keys(nodeSet), MachineIDs: sortedInt64Keys(machineSet)}
}

func sortedInt64Keys(values map[int64]struct{}) []int64 {
	result := make([]int64, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func sameOptionalInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func appendUniqueSortedInt64(values []int64, added int64) []int64 {
	for _, value := range values {
		if value == added {
			return values
		}
	}
	values = append(values, added)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values
}
