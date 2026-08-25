package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxGroupNameRunes      = 255
	maxRoutingRemarksRunes = 255
	maxRoutingMatches      = 1_000
	maxRoutingMatchBytes   = 2_048
	maxRoutingActionRunes  = 255
)

func (s *Store) CreateServerGroup(ctx context.Context, name string, now time.Time) (ServerGroup, error) {
	name, err := normalizeServerGroupName(name)
	if err != nil {
		return ServerGroup{}, err
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `INSERT INTO server_groups (name, created_at, updated_at) VALUES (?, ?, ?)`, name, now.Unix(), now.Unix())
	if err != nil {
		return ServerGroup{}, fmt.Errorf("create server group: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return ServerGroup{}, fmt.Errorf("read server group id: %w", err)
	}
	return s.GetServerGroup(ctx, id)
}

func (s *Store) UpdateServerGroup(ctx context.Context, groupID int64, name string, now time.Time) (ServerGroup, error) {
	name, err := normalizeServerGroupName(name)
	if err != nil {
		return ServerGroup{}, err
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `UPDATE server_groups SET name = ?, updated_at = ? WHERE id = ?`, name, now.Unix(), groupID)
	if err != nil {
		return ServerGroup{}, fmt.Errorf("update server group: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ServerGroup{}, ErrNotFound
	}
	return s.GetServerGroup(ctx, groupID)
}

func normalizeServerGroupName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxGroupNameRunes || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%w: invalid server group name", ErrInvalidInput)
	}
	return name, nil
}

func (s *Store) GetServerGroup(ctx context.Context, groupID int64) (ServerGroup, error) {
	return scanServerGroup(s.db.QueryRowContext(ctx, serverGroupSelect+` WHERE g.id = ? GROUP BY g.id`, groupID))
}

func (s *Store) ListServerGroups(ctx context.Context) ([]ServerGroup, error) {
	rows, err := s.db.QueryContext(ctx, serverGroupSelect+` GROUP BY g.id ORDER BY g.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list server groups: %w", err)
	}
	defer rows.Close()
	groups := make([]ServerGroup, 0)
	for rows.Next() {
		group, err := scanServerGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

const serverGroupSelect = `
	SELECT g.id, g.name, COALESCE(uc.users_count, 0), COALESCE(nc.servers_count, 0), g.created_at, g.updated_at
	FROM server_groups g
	LEFT JOIN (SELECT group_id, COUNT(*) users_count FROM users WHERE account_kind = 'human' AND group_id IS NOT NULL GROUP BY group_id) uc ON uc.group_id = g.id
	LEFT JOIN (SELECT group_id, COUNT(*) servers_count FROM node_group_memberships GROUP BY group_id) nc ON nc.group_id = g.id`

func scanServerGroup(row rowScanner) (ServerGroup, error) {
	var group ServerGroup
	var createdAt, updatedAt int64
	err := row.Scan(&group.ID, &group.Name, &group.UsersCount, &group.ServersCount, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ServerGroup{}, ErrNotFound
	}
	if err != nil {
		return ServerGroup{}, fmt.Errorf("scan server group: %w", err)
	}
	group.CreatedAt = time.Unix(createdAt, 0).UTC()
	group.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return group, nil
}

func (s *Store) DeleteServerGroup(ctx context.Context, groupID int64) error {
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete server group: %w", err)
	}
	defer tx.Rollback()
	var exists, referenced bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM server_groups WHERE id = ?),
		       EXISTS(
		           SELECT 1 FROM users WHERE group_id = ?
		           UNION ALL SELECT 1 FROM node_group_memberships WHERE group_id = ?
		           UNION ALL SELECT 1 FROM plans WHERE group_id = ?
		           LIMIT 1
		       )
	`, groupID, groupID, groupID, groupID).Scan(&exists, &referenced); err != nil {
		return fmt.Errorf("check server group references: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	if referenced {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM server_groups WHERE id = ?`, groupID); err != nil {
		return fmt.Errorf("delete server group: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete server group: %w", err)
	}
	return nil
}

func (s *Store) CreateRoutingRule(ctx context.Context, input SaveRoutingRuleInput, now time.Time) (RoutingRule, error) {
	normalized, encoded, err := normalizeRoutingRuleInput(input)
	if err != nil {
		return RoutingRule{}, err
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO routing_rules (remarks, match_json, action, action_value, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, normalized.Remarks, encoded, normalized.Action, normalized.ActionValue, now.Unix(), now.Unix())
	if err != nil {
		return RoutingRule{}, fmt.Errorf("create routing rule: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return RoutingRule{}, fmt.Errorf("read routing rule id: %w", err)
	}
	return s.GetRoutingRule(ctx, id)
}

func (s *Store) UpdateRoutingRule(ctx context.Context, routeID int64, input SaveRoutingRuleInput, now time.Time) (RoutingRule, error) {
	normalized, encoded, err := normalizeRoutingRuleInput(input)
	if err != nil {
		return RoutingRule{}, err
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE routing_rules SET remarks = ?, match_json = ?, action = ?, action_value = ?, updated_at = ? WHERE id = ?
	`, normalized.Remarks, encoded, normalized.Action, normalized.ActionValue, now.Unix(), routeID)
	if err != nil {
		return RoutingRule{}, fmt.Errorf("update routing rule: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return RoutingRule{}, ErrNotFound
	}
	return s.GetRoutingRule(ctx, routeID)
}

func normalizeRoutingRuleInput(input SaveRoutingRuleInput) (SaveRoutingRuleInput, string, error) {
	input.Remarks = strings.TrimSpace(input.Remarks)
	input.Action = strings.ToLower(strings.TrimSpace(input.Action))
	input.ActionValue = strings.TrimSpace(input.ActionValue)
	if input.Remarks == "" || !utf8.ValidString(input.Remarks) || utf8.RuneCountInString(input.Remarks) > maxRoutingRemarksRunes || strings.IndexFunc(input.Remarks, unicode.IsControl) >= 0 {
		return SaveRoutingRuleInput{}, "", fmt.Errorf("%w: invalid routing rule remarks", ErrInvalidInput)
	}
	if len(input.Match) > maxRoutingMatches {
		return SaveRoutingRuleInput{}, "", fmt.Errorf("%w: too many routing rule matches", ErrInvalidInput)
	}
	seen := make(map[string]struct{}, len(input.Match))
	matches := make([]string, 0, len(input.Match))
	for _, value := range input.Match {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !utf8.ValidString(value) || len(value) > maxRoutingMatchBytes || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return SaveRoutingRuleInput{}, "", fmt.Errorf("%w: invalid routing rule match", ErrInvalidInput)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		matches = append(matches, value)
	}
	if len(matches) == 0 {
		return SaveRoutingRuleInput{}, "", fmt.Errorf("%w: routing rule match is required", ErrInvalidInput)
	}
	switch input.Action {
	case "block", "direct":
		input.ActionValue = ""
	case "dns", "proxy":
		if input.ActionValue == "" || !utf8.ValidString(input.ActionValue) || utf8.RuneCountInString(input.ActionValue) > maxRoutingActionRunes || strings.IndexFunc(input.ActionValue, unicode.IsControl) >= 0 {
			return SaveRoutingRuleInput{}, "", fmt.Errorf("%w: routing action value is required", ErrInvalidInput)
		}
	default:
		return SaveRoutingRuleInput{}, "", fmt.Errorf("%w: unsupported routing action", ErrInvalidInput)
	}
	input.Match = matches
	encoded, err := json.Marshal(matches)
	if err != nil {
		return SaveRoutingRuleInput{}, "", fmt.Errorf("encode routing rule matches: %w", err)
	}
	return input, string(encoded), nil
}

func (s *Store) GetRoutingRule(ctx context.Context, routeID int64) (RoutingRule, error) {
	return scanRoutingRule(s.db.QueryRowContext(ctx, routingRuleSelect+` WHERE id = ?`, routeID))
}

func (s *Store) ListRoutingRules(ctx context.Context) ([]RoutingRule, error) {
	rows, err := s.db.QueryContext(ctx, routingRuleSelect+` ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list routing rules: %w", err)
	}
	defer rows.Close()
	rules := make([]RoutingRule, 0)
	for rows.Next() {
		rule, err := scanRoutingRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

const routingRuleSelect = `SELECT id, remarks, match_json, action, action_value, created_at, updated_at FROM routing_rules`

func scanRoutingRule(row rowScanner) (RoutingRule, error) {
	var rule RoutingRule
	var encoded string
	var createdAt, updatedAt int64
	err := row.Scan(&rule.ID, &rule.Remarks, &encoded, &rule.Action, &rule.ActionValue, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RoutingRule{}, ErrNotFound
	}
	if err != nil {
		return RoutingRule{}, fmt.Errorf("scan routing rule: %w", err)
	}
	if err := json.Unmarshal([]byte(encoded), &rule.Match); err != nil {
		return RoutingRule{}, fmt.Errorf("decode routing rule matches: %w", err)
	}
	rule.CreatedAt = time.Unix(createdAt, 0).UTC()
	rule.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return rule, nil
}

func (s *Store) DeleteRoutingRule(ctx context.Context, routeID int64) error {
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete routing rule: %w", err)
	}
	defer tx.Rollback()
	var exists, referenced bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM routing_rules WHERE id = ?),
		       EXISTS(SELECT 1 FROM node_route_memberships WHERE route_id = ?)
	`, routeID, routeID).Scan(&exists, &referenced); err != nil {
		return fmt.Errorf("check routing rule references: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	if referenced {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM routing_rules WHERE id = ?`, routeID); err != nil {
		return fmt.Errorf("delete routing rule: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete routing rule: %w", err)
	}
	return nil
}

func (s *Store) ListRoutingRuleTargets(ctx context.Context, routeID int64) ([]NodeRuntimeTarget, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.machine_id
		FROM node_route_memberships nr
		JOIN nodes n ON n.id = nr.node_id
		WHERE nr.route_id = ? AND n.machine_id IS NOT NULL AND n.enabled = 1
		ORDER BY n.id
	`, routeID)
	if err != nil {
		return nil, fmt.Errorf("list routing rule targets: %w", err)
	}
	defer rows.Close()
	targets := make([]NodeRuntimeTarget, 0)
	for rows.Next() {
		var target NodeRuntimeTarget
		if err := rows.Scan(&target.NodeID, &target.MachineID); err != nil {
			return nil, fmt.Errorf("scan routing rule target: %w", err)
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}
