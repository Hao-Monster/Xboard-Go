package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	LegacyGroupsRoutesSlice = "groups-routes-v1"
	maxLegacyGroupsRoutes   = 100_000
)

type LegacyServerGroup struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type LegacyRoutingRule struct {
	ID          int64    `json:"id"`
	Remarks     string   `json:"remarks"`
	Match       []string `json:"match"`
	Action      string   `json:"action"`
	ActionValue string   `json:"action_value"`
	CreatedAt   int64    `json:"created_at"`
	UpdatedAt   int64    `json:"updated_at"`
}

type LegacyGroupsRoutesChecksums struct {
	Groups string `json:"groups"`
	Routes string `json:"routes"`
}

type LegacyGroupsRoutesImport struct {
	Slice                string
	SourceSHA256         string
	SourceSize           int64
	Groups               []LegacyServerGroup
	Routes               []LegacyRoutingRule
	Checksums            LegacyGroupsRoutesChecksums
	RollbackBackupPath   string
	RollbackBackupSHA256 string
}

type LegacyGroupsRoutesImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Groups               LegacyDomainResult `json:"groups"`
	Routes               LegacyDomainResult `json:"routes"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func LegacyServerGroupsChecksum(groups []LegacyServerGroup) string {
	ordered := append([]LegacyServerGroup(nil), groups...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	if ordered == nil {
		ordered = []LegacyServerGroup{}
	}
	return legacyCanonicalChecksum(ordered)
}

func LegacyRoutingRulesChecksum(routes []LegacyRoutingRule) string {
	ordered := append([]LegacyRoutingRule(nil), routes...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	if ordered == nil {
		ordered = []LegacyRoutingRule{}
	}
	return legacyCanonicalChecksum(ordered)
}

func (s *Store) LookupLegacyGroupsRoutesImport(ctx context.Context, sourceSHA256 string) (LegacyGroupsRoutesImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyGroupsRoutesImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacyGroupsRoutesImport(ctx, s.db, sourceSHA256)
}

func lookupLegacyGroupsRoutesImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyGroupsRoutesImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `
		SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?
	`, LegacyGroupsRoutesSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyGroupsRoutesImportReport{}, false, nil
	}
	if err != nil {
		return LegacyGroupsRoutesImportReport{}, false, fmt.Errorf("lookup legacy groups/routes migration: %w", err)
	}
	var report LegacyGroupsRoutesImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyGroupsRoutesImportReport{}, false, fmt.Errorf("decode legacy groups/routes migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyGroupsRoutes(ctx context.Context, input LegacyGroupsRoutesImport, now time.Time) (LegacyGroupsRoutesImportReport, error) {
	if err := validateLegacyGroupsRoutesImport(input); err != nil {
		return LegacyGroupsRoutesImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyGroupsRoutesImportReport{}, fmt.Errorf("begin legacy groups/routes import: %w", err)
	}
	defer tx.Rollback()

	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return LegacyGroupsRoutesImportReport{}, fmt.Errorf("read legacy groups/routes target schema: %w", err)
	}
	if version != CurrentSchemaVersion() {
		return LegacyGroupsRoutesImportReport{}, fmt.Errorf("legacy groups/routes import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyGroupsRoutesImportReport{}, fmt.Errorf("validate legacy groups/routes target schema: %w", err)
	}
	if existing, found, err := lookupLegacyGroupsRoutesImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyGroupsRoutesImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyGroupsRoutesImportReport{}, fmt.Errorf("commit idempotent legacy groups/routes import: %w", err)
		}
		return existing, nil
	}
	var otherRuns int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyGroupsRoutesSlice).Scan(&otherRuns); err != nil {
		return LegacyGroupsRoutesImportReport{}, fmt.Errorf("count legacy groups/routes migrations: %w", err)
	}
	if otherRuns != 0 {
		return LegacyGroupsRoutesImportReport{}, fmt.Errorf("%w: legacy groups/routes slice was already imported from another snapshot", ErrConflict)
	}
	var existingGroups, existingRoutes int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM server_groups`).Scan(&existingGroups); err != nil {
		return LegacyGroupsRoutesImportReport{}, fmt.Errorf("count target server groups: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM routing_rules`).Scan(&existingRoutes); err != nil {
		return LegacyGroupsRoutesImportReport{}, fmt.Errorf("count target routing rules: %w", err)
	}
	if existingGroups != 0 || existingRoutes != 0 {
		return LegacyGroupsRoutesImportReport{}, fmt.Errorf("%w: legacy groups/routes import requires empty target groups and routes", ErrConflict)
	}

	groupStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO server_groups (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return LegacyGroupsRoutesImportReport{}, fmt.Errorf("prepare legacy server group import: %w", err)
	}
	defer groupStatement.Close()
	for _, group := range input.Groups {
		if _, err := groupStatement.ExecContext(ctx, group.ID, group.Name, group.CreatedAt, group.UpdatedAt); err != nil {
			return LegacyGroupsRoutesImportReport{}, fmt.Errorf("import legacy server group id %d: %w", group.ID, err)
		}
	}
	routeStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO routing_rules (id, remarks, match_json, action, action_value, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return LegacyGroupsRoutesImportReport{}, fmt.Errorf("prepare legacy routing rule import: %w", err)
	}
	defer routeStatement.Close()
	for _, route := range input.Routes {
		encodedMatch, err := json.Marshal(route.Match)
		if err != nil {
			return LegacyGroupsRoutesImportReport{}, fmt.Errorf("encode legacy routing rule id %d: %w", route.ID, err)
		}
		if _, err := routeStatement.ExecContext(ctx, route.ID, route.Remarks, string(encodedMatch), route.Action, route.ActionValue, route.CreatedAt, route.UpdatedAt); err != nil {
			return LegacyGroupsRoutesImportReport{}, fmt.Errorf("import legacy routing rule id %d: %w", route.ID, err)
		}
	}

	targetGroups, err := readLegacyTargetServerGroups(ctx, tx)
	if err != nil {
		return LegacyGroupsRoutesImportReport{}, err
	}
	targetRoutes, err := readLegacyTargetRoutingRules(ctx, tx)
	if err != nil {
		return LegacyGroupsRoutesImportReport{}, err
	}
	report := LegacyGroupsRoutesImportReport{
		Slice: LegacyGroupsRoutesSlice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Groups:    LegacyDomainResult{SourceRows: len(input.Groups), TargetRows: len(targetGroups), SourceChecksum: input.Checksums.Groups, TargetChecksum: LegacyServerGroupsChecksum(targetGroups)},
		Routes:    LegacyDomainResult{SourceRows: len(input.Routes), TargetRows: len(targetRoutes), SourceChecksum: input.Checksums.Routes, TargetChecksum: LegacyRoutingRulesChecksum(targetRoutes)},
		AppliedAt: now.UTC(), AlreadyApplied: false,
	}
	if report.Groups.SourceRows != report.Groups.TargetRows || report.Groups.SourceChecksum != report.Groups.TargetChecksum ||
		report.Routes.SourceRows != report.Routes.TargetRows || report.Routes.SourceChecksum != report.Routes.TargetChecksum {
		return LegacyGroupsRoutesImportReport{}, errors.New("legacy groups/routes target verification does not match source")
	}
	encodedReport, err := json.Marshal(report)
	if err != nil {
		return LegacyGroupsRoutesImportReport{}, fmt.Errorf("encode legacy groups/routes migration report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO legacy_migration_runs
		(slice, source_sha256, source_size, rollback_backup_path, rollback_backup_sha256, report_json, applied_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encodedReport), report.AppliedAt.Unix()); err != nil {
		return LegacyGroupsRoutesImportReport{}, fmt.Errorf("record legacy groups/routes migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyGroupsRoutesImportReport{}, fmt.Errorf("commit legacy groups/routes import: %w", err)
	}
	return report, nil
}

func validateLegacyGroupsRoutesImport(input LegacyGroupsRoutesImport) error {
	if input.Slice != LegacyGroupsRoutesSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		!validLowerSHA256(input.RollbackBackupSHA256) || input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 ||
		!utf8.ValidString(input.RollbackBackupPath) || strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 ||
		len(input.Groups) > maxLegacyGroupsRoutes || len(input.Routes) > maxLegacyGroupsRoutes {
		return ErrInvalidInput
	}
	if input.Checksums.Groups != LegacyServerGroupsChecksum(input.Groups) || input.Checksums.Routes != LegacyRoutingRulesChecksum(input.Routes) {
		return fmt.Errorf("%w: legacy groups/routes source checksum mismatch", ErrInvalidInput)
	}
	return ValidateLegacyGroupsRoutesData(input.Groups, input.Routes)
}

func ValidateLegacyGroupsRoutesData(groups []LegacyServerGroup, routes []LegacyRoutingRule) error {
	if len(groups) > maxLegacyGroupsRoutes || len(routes) > maxLegacyGroupsRoutes {
		return ErrInvalidInput
	}
	seenGroups := make(map[int64]struct{}, len(groups))
	for _, group := range groups {
		if group.ID < 1 || group.CreatedAt < 0 || group.UpdatedAt < group.CreatedAt {
			return fmt.Errorf("%w: invalid legacy server group id or timestamp", ErrInvalidInput)
		}
		if _, exists := seenGroups[group.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy server group id %d", ErrInvalidInput, group.ID)
		}
		seenGroups[group.ID] = struct{}{}
		normalized, err := normalizeServerGroupName(group.Name)
		if err != nil {
			return fmt.Errorf("%w: legacy server group id %d: %v", ErrInvalidInput, group.ID, err)
		}
		if normalized != group.Name {
			return fmt.Errorf("%w: legacy server group id %d requires normalization", ErrInvalidInput, group.ID)
		}
	}
	seenRoutes := make(map[int64]struct{}, len(routes))
	for _, route := range routes {
		if route.ID < 1 || route.CreatedAt < 0 || route.UpdatedAt < route.CreatedAt {
			return fmt.Errorf("%w: invalid legacy routing rule id or timestamp", ErrInvalidInput)
		}
		if _, exists := seenRoutes[route.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy routing rule id %d", ErrInvalidInput, route.ID)
		}
		seenRoutes[route.ID] = struct{}{}
		normalized, _, err := normalizeRoutingRuleInput(SaveRoutingRuleInput{
			Remarks: route.Remarks, Match: route.Match, Action: route.Action, ActionValue: route.ActionValue,
		})
		if err != nil {
			return fmt.Errorf("%w: legacy routing rule id %d: %v", ErrInvalidInput, route.ID, err)
		}
		if normalized.Remarks != route.Remarks || !slices.Equal(normalized.Match, route.Match) ||
			normalized.Action != route.Action || normalized.ActionValue != route.ActionValue {
			return fmt.Errorf("%w: legacy routing rule id %d requires normalization", ErrInvalidInput, route.ID)
		}
	}
	return nil
}

func readLegacyTargetServerGroups(ctx context.Context, database queryer) ([]LegacyServerGroup, error) {
	rows, err := database.QueryContext(ctx, `SELECT id, name, created_at, updated_at FROM server_groups ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read imported server groups: %w", err)
	}
	defer rows.Close()
	result := make([]LegacyServerGroup, 0)
	for rows.Next() {
		var group LegacyServerGroup
		if err := rows.Scan(&group.ID, &group.Name, &group.CreatedAt, &group.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan imported server group: %w", err)
		}
		result = append(result, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate imported server groups: %w", err)
	}
	return result, nil
}

func readLegacyTargetRoutingRules(ctx context.Context, database queryer) ([]LegacyRoutingRule, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, remarks, match_json, action, action_value, created_at, updated_at FROM routing_rules ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("read imported routing rules: %w", err)
	}
	defer rows.Close()
	result := make([]LegacyRoutingRule, 0)
	for rows.Next() {
		var route LegacyRoutingRule
		var encodedMatch string
		if err := rows.Scan(&route.ID, &route.Remarks, &encodedMatch, &route.Action, &route.ActionValue, &route.CreatedAt, &route.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan imported routing rule: %w", err)
		}
		if err := json.Unmarshal([]byte(encodedMatch), &route.Match); err != nil {
			return nil, fmt.Errorf("decode imported routing rule id %d: %w", route.ID, err)
		}
		if route.Match == nil {
			route.Match = []string{}
		}
		result = append(result, route)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate imported routing rules: %w", err)
	}
	return result, nil
}
