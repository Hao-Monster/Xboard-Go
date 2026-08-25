package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var supportedNodeTypes = map[string]struct{}{
	"hysteria":    {},
	"vless":       {},
	"trojan":      {},
	"vmess":       {},
	"tuic":        {},
	"shadowsocks": {},
	"anytls":      {},
	"socks":       {},
	"naive":       {},
	"http":        {},
	"mieru":       {},
}

var nodePortPattern = regexp.MustCompile(`^(\d{1,5})(?:-(\d{1,5}))?$`)

func (s *Store) CreateNode(ctx context.Context, input CreateNodeInput, now time.Time) (Node, error) {
	defer s.lockWrite()()
	input.Name = strings.TrimSpace(input.Name)
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	input.Host = strings.TrimSpace(input.Host)
	if input.Name == "" || len(input.Name) > 255 || input.Host == "" || len(input.Host) > 255 || !validNodePort(input.Port) {
		return Node{}, fmt.Errorf("%w: invalid node fields", ErrInvalidInput)
	}
	if _, ok := supportedNodeTypes[input.Type]; !ok {
		return Node{}, fmt.Errorf("%w: unsupported node type", ErrInvalidInput)
	}

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO nodes (name, type, host, port, show, enabled, sort, machine_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.Name, input.Type, input.Host, input.Port, input.Show, input.Enabled, input.Sort, input.MachineID, now.Unix(), now.Unix())
	if err != nil {
		return Node{}, fmt.Errorf("create node: %w", err)
	}
	nodeID, err := result.LastInsertId()
	if err != nil {
		return Node{}, fmt.Errorf("read node id: %w", err)
	}
	return s.GetNode(ctx, nodeID)
}

func validNodePort(value string) bool {
	matches := nodePortPattern.FindStringSubmatch(value)
	if matches == nil {
		return false
	}
	start, _ := strconv.Atoi(matches[1])
	if start < 1 || start > 65535 {
		return false
	}
	if matches[2] == "" {
		return true
	}
	end, _ := strconv.Atoi(matches[2])
	return end >= start && end <= 65535
}

func (s *Store) GetNode(ctx context.Context, nodeID int64) (Node, error) {
	return scanNode(s.db.QueryRowContext(ctx, nodeSelect+` WHERE id = ?`, nodeID))
}

func (s *Store) ListMachineNodes(ctx context.Context, machineID int64) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx, nodeSelect+` WHERE machine_id = ? ORDER BY sort, id`, machineID)
	if err != nil {
		return nil, fmt.Errorf("list machine nodes: %w", err)
	}
	defer rows.Close()
	return scanNodes(rows)
}

func (s *Store) ListUnassignedNodes(ctx context.Context) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx, nodeSelect+` WHERE machine_id IS NULL ORDER BY sort, id`)
	if err != nil {
		return nil, fmt.Errorf("list unassigned nodes: %w", err)
	}
	defer rows.Close()
	return scanNodes(rows)
}

func (s *Store) AssignNode(ctx context.Context, machineID, nodeID int64, now time.Time) error {
	defer s.lockWrite()()
	var machineExists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM server_machines WHERE id = ?)`, machineID).Scan(&machineExists); err != nil {
		return fmt.Errorf("check machine: %w", err)
	}
	if !machineExists {
		return ErrNotFound
	}
	result, err := s.db.ExecContext(ctx, `UPDATE nodes SET machine_id = ?, updated_at = ? WHERE id = ?`, machineID, now.Unix(), nodeID)
	if err != nil {
		return fmt.Errorf("assign node: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UnassignNode(ctx context.Context, machineID, nodeID int64, now time.Time) error {
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE nodes SET machine_id = NULL, updated_at = ? WHERE id = ? AND machine_id = ?
	`, now.Unix(), nodeID, machineID)
	if err != nil {
		return fmt.Errorf("unassign node: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetNodeEnabled(ctx context.Context, machineID, nodeID int64, enabled bool, now time.Time) error {
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE nodes SET enabled = ?, updated_at = ? WHERE id = ? AND machine_id = ?
	`, enabled, now.Unix(), nodeID, machineID)
	if err != nil {
		return fmt.Errorf("set node enabled: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListLoadHistory(ctx context.Context, machineID int64, since time.Time, limit int) ([]LoadHistory, error) {
	if limit < 10 || limit > 1440 {
		return nil, fmt.Errorf("%w: history limit must be between 10 and 1440", ErrInvalidInput)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, machine_id, cpu, mem_total, mem_used, disk_total, disk_used, net_in_speed, net_out_speed, recorded_at
		FROM (
			SELECT id, machine_id, cpu, mem_total, mem_used, disk_total, disk_used, net_in_speed, net_out_speed, recorded_at
			FROM server_machine_load_history
			WHERE machine_id = ? AND recorded_at >= ?
			ORDER BY recorded_at DESC
			LIMIT ?
		) history
		ORDER BY recorded_at
	`, machineID, since.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("list load history: %w", err)
	}
	defer rows.Close()

	history := make([]LoadHistory, 0)
	for rows.Next() {
		var item LoadHistory
		var recordedAt int64
		if err := rows.Scan(
			&item.ID, &item.MachineID, &item.CPUPercent, &item.MemoryTotal, &item.MemoryUsed,
			&item.DiskTotal, &item.DiskUsed, &item.NetworkIn, &item.NetworkOut, &recordedAt,
		); err != nil {
			return nil, fmt.Errorf("scan load history: %w", err)
		}
		item.RecordedAt = time.Unix(recordedAt, 0).UTC()
		history = append(history, item)
	}
	return history, rows.Err()
}

const nodeSelect = `SELECT id, name, type, host, port, show, enabled, sort,
	COALESCE((SELECT configured_rate_micros FROM node_protocol_definitions WHERE node_id = nodes.id), rate_micros), traffic_u, traffic_d,
	runtime_config IS NOT NULL, last_check_at, last_push_at, machine_id, created_at, updated_at FROM nodes`

func scanNode(row rowScanner) (Node, error) {
	var node Node
	var machineID sql.NullInt64
	var lastCheckAt, lastPushAt sql.NullInt64
	var rateMicros int64
	var createdAt, updatedAt int64
	err := row.Scan(
		&node.ID, &node.Name, &node.Type, &node.Host, &node.Port, &node.Show, &node.Enabled,
		&node.Sort, &rateMicros, &node.TrafficUpload, &node.TrafficDownload, &node.RuntimeConfigured,
		&lastCheckAt, &lastPushAt, &machineID, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, ErrNotFound
	}
	if err != nil {
		return Node{}, fmt.Errorf("scan node: %w", err)
	}
	if machineID.Valid {
		node.MachineID = &machineID.Int64
	}
	node.Rate = float64(rateMicros) / 1_000_000
	if lastCheckAt.Valid {
		value := time.Unix(lastCheckAt.Int64, 0).UTC()
		node.LastCheckAt = &value
	}
	if lastPushAt.Valid {
		value := time.Unix(lastPushAt.Int64, 0).UTC()
		node.LastPushAt = &value
	}
	node.CreatedAt = time.Unix(createdAt, 0).UTC()
	node.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return node, nil
}

func scanNodes(rows *sql.Rows) ([]Node, error) {
	nodes := make([]Node, 0)
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}
