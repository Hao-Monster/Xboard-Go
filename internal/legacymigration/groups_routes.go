package legacymigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const maxLegacyGroupsRoutes = 100_000

type GroupsRoutesSnapshot struct {
	Path      string
	Size      int64
	SHA256    string
	Groups    []store.LegacyServerGroup
	Routes    []store.LegacyRoutingRule
	Checksums store.LegacyGroupsRoutesChecksums
}

func ReadGroupsRoutesSnapshot(ctx context.Context, sourcePath string) (GroupsRoutesSnapshot, error) {
	groups := []store.LegacyServerGroup{}
	routes := []store.LegacyRoutingRule{}
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_server_group", []string{"id", "name", "created_at", "updated_at"}); err != nil {
			return err
		}
		if err := requireRealTable(ctx, database, "v2_server_route", []string{"id", "remarks", "match", "action", "action_value", "created_at", "updated_at"}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(length(CAST(name AS BLOB))), 0) FROM v2_server_group
		`, maxLegacyGroupsRoutes, maxLegacyRelevantDataBytes, "legacy server groups"); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(
				length(CAST(remarks AS BLOB)) + length(CAST(match AS BLOB)) + length(CAST(action AS BLOB)) +
				COALESCE(length(CAST(action_value AS BLOB)), 0)
			), 0)
			FROM v2_server_route
		`, maxLegacyGroupsRoutes, maxLegacyRelevantDataBytes, "legacy routing rules"); err != nil {
			return err
		}
		var relevantBytes int64
		var readErr error
		groups, relevantBytes, readErr = readLegacyServerGroups(ctx, database)
		if readErr != nil {
			return readErr
		}
		var routeBytes int64
		routes, routeBytes, readErr = readLegacyRoutingRules(ctx, database)
		if readErr != nil {
			return readErr
		}
		if relevantBytes+routeBytes > maxLegacyRelevantDataBytes {
			return errors.New("legacy groups/routes exceed the migration data limit")
		}
		if err := store.ValidateLegacyGroupsRoutesData(groups, routes); err != nil {
			return fmt.Errorf("validate legacy groups/routes: %w", err)
		}
		return nil
	})
	if err != nil {
		return GroupsRoutesSnapshot{}, err
	}
	return GroupsRoutesSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Groups: groups, Routes: routes,
		Checksums: store.LegacyGroupsRoutesChecksums{
			Groups: store.LegacyServerGroupsChecksum(groups), Routes: store.LegacyRoutingRulesChecksum(routes),
		},
	}, nil
}

func readLegacyServerGroups(ctx context.Context, database *sql.DB) ([]store.LegacyServerGroup, int64, error) {
	rows, err := database.QueryContext(ctx, `SELECT id, name, created_at, updated_at FROM v2_server_group ORDER BY id`)
	if err != nil {
		return nil, 0, fmt.Errorf("read legacy server groups: %w", err)
	}
	defer rows.Close()
	result := make([]store.LegacyServerGroup, 0)
	var bytesRead int64
	for rows.Next() {
		if len(result) >= maxLegacyGroupsRoutes {
			return nil, 0, fmt.Errorf("legacy server groups exceed the %d-row migration limit", maxLegacyGroupsRoutes)
		}
		var group store.LegacyServerGroup
		if err := rows.Scan(&group.ID, &group.Name, &group.CreatedAt, &group.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan legacy server group: %w", err)
		}
		bytesRead += int64(len(group.Name))
		if bytesRead > maxLegacyRelevantDataBytes {
			return nil, 0, errors.New("legacy server groups exceed the migration data limit")
		}
		result = append(result, group)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate legacy server groups: %w", err)
	}
	return result, bytesRead, nil
}

func readLegacyRoutingRules(ctx context.Context, database *sql.DB) ([]store.LegacyRoutingRule, int64, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, remarks, match, action, action_value, created_at, updated_at
		FROM v2_server_route ORDER BY id
	`)
	if err != nil {
		return nil, 0, fmt.Errorf("read legacy routing rules: %w", err)
	}
	defer rows.Close()
	result := make([]store.LegacyRoutingRule, 0)
	var bytesRead int64
	for rows.Next() {
		if len(result) >= maxLegacyGroupsRoutes {
			return nil, 0, fmt.Errorf("legacy routing rules exceed the %d-row migration limit", maxLegacyGroupsRoutes)
		}
		var route store.LegacyRoutingRule
		var encodedMatch string
		var actionValue sql.NullString
		if err := rows.Scan(&route.ID, &route.Remarks, &encodedMatch, &route.Action, &actionValue, &route.CreatedAt, &route.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan legacy routing rule: %w", err)
		}
		if actionValue.Valid {
			route.ActionValue = actionValue.String
		}
		decoder := json.NewDecoder(strings.NewReader(encodedMatch))
		if err := decoder.Decode(&route.Match); err != nil {
			return nil, 0, fmt.Errorf("decode legacy routing rule id %d match: %w", route.ID, err)
		}
		if decoder.Decode(new(any)) != io.EOF {
			return nil, 0, fmt.Errorf("decode legacy routing rule id %d match: trailing JSON data", route.ID)
		}
		if route.Match == nil {
			route.Match = []string{}
		}
		bytesRead += int64(len(route.Remarks) + len(encodedMatch) + len(route.Action) + len(route.ActionValue))
		if bytesRead > maxLegacyRelevantDataBytes {
			return nil, 0, errors.New("legacy routing rules exceed the migration data limit")
		}
		result = append(result, route)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate legacy routing rules: %w", err)
	}
	return result, bytesRead, nil
}
