package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	LegacyPlansSlice = "plans-v1"
	maxLegacyPlans   = 10_000
)

type LegacyPlan struct {
	ID                 int64      `json:"id"`
	GroupID            *int64     `json:"group_id"`
	TransferEnableGiB  int64      `json:"transfer_enable"`
	Name               string     `json:"name"`
	SpeedLimit         *int64     `json:"speed_limit"`
	Show               bool       `json:"show"`
	SortPosition       int        `json:"sort"`
	Renew              bool       `json:"renew"`
	Content            string     `json:"content"`
	ResetTrafficMethod *int64     `json:"reset_traffic_method"`
	CapacityLimit      *int64     `json:"capacity_limit"`
	Prices             PlanPrices `json:"prices"`
	Sell               bool       `json:"sell"`
	DeviceLimit        *int64     `json:"device_limit"`
	Tags               []string   `json:"tags"`
	CreatedAt          int64      `json:"created_at"`
	UpdatedAt          int64      `json:"updated_at"`
}

type LegacyPlansImport struct {
	Slice                string
	SourceSHA256         string
	SourceSize           int64
	Plans                []LegacyPlan
	Checksum             string
	TrafficResetMethod   int
	SettingsChecksum     string
	RollbackBackupPath   string
	RollbackBackupSHA256 string
}

type LegacyPlansImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Plans                LegacyDomainResult `json:"plans"`
	TrafficResetMethod   LegacyDomainResult `json:"traffic_reset_method"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func LegacyPlansChecksum(plans []LegacyPlan) string {
	ordered := append([]LegacyPlan(nil), plans...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	if ordered == nil {
		ordered = []LegacyPlan{}
	}
	return legacyCanonicalChecksum(ordered)
}

func LegacyPlanSettingsChecksum(trafficResetMethod int) string {
	return legacyCanonicalChecksum(struct {
		TrafficResetMethod int `json:"traffic_reset_method"`
	}{TrafficResetMethod: trafficResetMethod})
}

func ValidateLegacyPlansData(plans []LegacyPlan) error {
	if len(plans) > maxLegacyPlans {
		return fmt.Errorf("%w: legacy plans exceed the %d-row migration limit", ErrInvalidInput, maxLegacyPlans)
	}
	ids := make(map[int64]struct{}, len(plans))
	for _, plan := range plans {
		if plan.ID < 1 || plan.TransferEnableGiB < 1 || plan.TransferEnableGiB > maxPlanTransferGiB ||
			plan.Name == "" || !utf8.ValidString(plan.Name) || utf8.RuneCountInString(plan.Name) > maxPlanNameRunes ||
			strings.IndexFunc(plan.Name, unicode.IsControl) >= 0 || !utf8.ValidString(plan.Content) ||
			len(plan.Content) > maxPlanContentBytes || strings.IndexByte(plan.Content, 0) >= 0 || plan.SortPosition < 0 ||
			plan.GroupID != nil && *plan.GroupID < 1 || !validLegacyPlanInt(plan.SpeedLimit, 1_000_000_000) ||
			!validLegacyPlanInt(plan.DeviceLimit, 1_000) || !validLegacyPlanInt(plan.CapacityLimit, 1_000_000_000) ||
			plan.ResetTrafficMethod != nil && (*plan.ResetTrafficMethod < 0 || *plan.ResetTrafficMethod > 4) ||
			!validLegacyUnixTimestamp(plan.CreatedAt) || !validLegacyUnixTimestamp(plan.UpdatedAt) || plan.UpdatedAt < plan.CreatedAt ||
			len(plan.Tags) > maxPlanTags {
			return fmt.Errorf("%w: invalid legacy plan id %d", ErrInvalidInput, plan.ID)
		}
		if _, exists := ids[plan.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy plan id %d", ErrInvalidInput, plan.ID)
		}
		ids[plan.ID] = struct{}{}
		for period, price := range plan.Prices {
			if _, valid := planPricePeriods[period]; !valid || price <= 0 || price > maxPlanPriceCents {
				return fmt.Errorf("%w: invalid legacy plan id %d price", ErrInvalidInput, plan.ID)
			}
		}
		for _, tag := range plan.Tags {
			if tag == "" || !utf8.ValidString(tag) || utf8.RuneCountInString(tag) > maxPlanTagRunes || strings.IndexFunc(tag, unicode.IsControl) >= 0 {
				return fmt.Errorf("%w: invalid legacy plan id %d tag", ErrInvalidInput, plan.ID)
			}
		}
	}
	return nil
}

func validLegacyPlanInt(value *int64, maximum int64) bool {
	return value == nil || *value >= 0 && *value <= maximum
}

func (s *Store) LookupLegacyPlansImport(ctx context.Context, sourceSHA256 string) (LegacyPlansImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyPlansImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacyPlansImport(ctx, s.db, sourceSHA256)
}

func lookupLegacyPlansImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyPlansImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?`, LegacyPlansSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyPlansImportReport{}, false, nil
	}
	if err != nil {
		return LegacyPlansImportReport{}, false, fmt.Errorf("lookup legacy plan migration: %w", err)
	}
	var report LegacyPlansImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyPlansImportReport{}, false, fmt.Errorf("decode legacy plan migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyPlans(ctx context.Context, input LegacyPlansImport, now time.Time) (LegacyPlansImportReport, error) {
	if err := validateLegacyPlansImport(input); err != nil {
		return LegacyPlansImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyPlansImportReport{}, fmt.Errorf("begin legacy plan import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return LegacyPlansImportReport{}, fmt.Errorf("read legacy plan target schema: %w", err)
	}
	if version != CurrentSchemaVersion() {
		return LegacyPlansImportReport{}, fmt.Errorf("legacy plan import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyPlansImportReport{}, fmt.Errorf("validate legacy plan target schema: %w", err)
	}
	if existing, found, err := lookupLegacyPlansImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyPlansImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyPlansImportReport{}, fmt.Errorf("commit idempotent legacy plan import: %w", err)
		}
		return existing, nil
	}
	var otherRuns, targetPlans, targetTrafficResetMethod int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyPlansSlice).Scan(&otherRuns); err != nil {
		return LegacyPlansImportReport{}, fmt.Errorf("count legacy plan migrations: %w", err)
	}
	if otherRuns != 0 {
		return LegacyPlansImportReport{}, fmt.Errorf("%w: legacy plan slice was already imported from another snapshot", ErrConflict)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plans`).Scan(&targetPlans); err != nil {
		return LegacyPlansImportReport{}, fmt.Errorf("count target plans: %w", err)
	}
	if targetPlans != 0 {
		return LegacyPlansImportReport{}, fmt.Errorf("%w: legacy plan import requires an empty plan target", ErrConflict)
	}
	if err := tx.QueryRowContext(ctx, `SELECT traffic_reset_method FROM app_settings WHERE id = 1`).Scan(&targetTrafficResetMethod); err != nil {
		return LegacyPlansImportReport{}, fmt.Errorf("read target traffic reset method: %w", err)
	}
	if targetTrafficResetMethod != 1 {
		return LegacyPlansImportReport{}, fmt.Errorf("%w: legacy plan import requires the default target traffic reset method", ErrConflict)
	}
	if err := validateLegacyPlanGroups(ctx, tx, input.Plans); err != nil {
		return LegacyPlansImportReport{}, err
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO plans (
			id, group_id, transfer_enable_gib, name, speed_limit, show, sort_position, renew, content,
			reset_traffic_method, capacity_limit, prices_json, sell, device_limit, tags_json, revision, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	`)
	if err != nil {
		return LegacyPlansImportReport{}, fmt.Errorf("prepare legacy plan import: %w", err)
	}
	defer statement.Close()
	for _, plan := range input.Plans {
		prices, err := json.Marshal(plan.Prices)
		if err != nil {
			return LegacyPlansImportReport{}, fmt.Errorf("encode legacy plan id %d prices: %w", plan.ID, err)
		}
		tags, err := json.Marshal(plan.Tags)
		if err != nil {
			return LegacyPlansImportReport{}, fmt.Errorf("encode legacy plan id %d tags: %w", plan.ID, err)
		}
		if _, err := statement.ExecContext(ctx, plan.ID, nullableInt64Value(plan.GroupID), plan.TransferEnableGiB,
			plan.Name, nullableInt64Value(plan.SpeedLimit), plan.Show, plan.SortPosition, plan.Renew, plan.Content,
			nullableInt64Value(plan.ResetTrafficMethod), nullableInt64Value(plan.CapacityLimit), string(prices), plan.Sell,
			nullableInt64Value(plan.DeviceLimit), string(tags), plan.CreatedAt, plan.UpdatedAt); err != nil {
			return LegacyPlansImportReport{}, fmt.Errorf("import legacy plan id %d: %w", plan.ID, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE app_settings SET traffic_reset_method = ?, revision = revision + 1, updated_at = ? WHERE id = 1
	`, input.TrafficResetMethod, now.Unix()); err != nil {
		return LegacyPlansImportReport{}, fmt.Errorf("import legacy traffic reset method: %w", err)
	}
	target, err := readLegacyTargetPlans(ctx, tx)
	if err != nil {
		return LegacyPlansImportReport{}, err
	}
	report := LegacyPlansImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Plans:              LegacyDomainResult{SourceRows: len(input.Plans), TargetRows: len(target), SourceChecksum: input.Checksum, TargetChecksum: LegacyPlansChecksum(target)},
		TrafficResetMethod: LegacyDomainResult{SourceRows: 1, TargetRows: 1, SourceChecksum: input.SettingsChecksum},
		AppliedAt:          now.UTC(),
	}
	if err := tx.QueryRowContext(ctx, `SELECT traffic_reset_method FROM app_settings WHERE id = 1`).Scan(&targetTrafficResetMethod); err != nil {
		return LegacyPlansImportReport{}, fmt.Errorf("verify imported traffic reset method: %w", err)
	}
	report.TrafficResetMethod.TargetChecksum = LegacyPlanSettingsChecksum(targetTrafficResetMethod)
	if report.Plans.SourceRows != report.Plans.TargetRows || report.Plans.SourceChecksum != report.Plans.TargetChecksum ||
		report.TrafficResetMethod.SourceChecksum != report.TrafficResetMethod.TargetChecksum {
		return LegacyPlansImportReport{}, errors.New("legacy plan target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyPlansImportReport{}, fmt.Errorf("encode legacy plan migration report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO legacy_migration_runs
		(slice, source_sha256, source_size, rollback_backup_path, rollback_backup_sha256, report_json, applied_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyPlansImportReport{}, fmt.Errorf("record legacy plan migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyPlansImportReport{}, fmt.Errorf("commit legacy plan import: %w", err)
	}
	return report, nil
}

func validateLegacyPlansImport(input LegacyPlansImport) error {
	if input.Slice != LegacyPlansSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) ||
		strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 || !validLowerSHA256(input.RollbackBackupSHA256) ||
		input.Checksum != LegacyPlansChecksum(input.Plans) || input.TrafficResetMethod < 0 || input.TrafficResetMethod > 4 ||
		input.SettingsChecksum != LegacyPlanSettingsChecksum(input.TrafficResetMethod) {
		return fmt.Errorf("%w: invalid legacy plan import", ErrInvalidInput)
	}
	return ValidateLegacyPlansData(input.Plans)
}

func validateLegacyPlanGroups(ctx context.Context, tx *sql.Tx, plans []LegacyPlan) error {
	seen := make(map[int64]struct{})
	for _, plan := range plans {
		if plan.GroupID == nil {
			continue
		}
		if _, exists := seen[*plan.GroupID]; exists {
			continue
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM server_groups WHERE id = ?)`, *plan.GroupID).Scan(&exists); err != nil {
			return fmt.Errorf("validate legacy plan group: %w", err)
		}
		if !exists {
			return fmt.Errorf("%w: legacy plans reference missing group %d", ErrConflict, *plan.GroupID)
		}
		seen[*plan.GroupID] = struct{}{}
	}
	return nil
}

func readLegacyTargetPlans(ctx context.Context, database queryer) ([]LegacyPlan, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, group_id, transfer_enable_gib, name, speed_limit, show, sort_position, renew, content,
		       reset_traffic_method, capacity_limit, prices_json, sell, device_limit, tags_json, revision, created_at, updated_at
		FROM plans ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("read imported legacy plans: %w", err)
	}
	defer rows.Close()
	plans := make([]LegacyPlan, 0)
	for rows.Next() {
		var plan LegacyPlan
		var groupID, speedLimit, resetMethod, capacityLimit, deviceLimit sql.NullInt64
		var prices, tags string
		var revision int64
		if err := rows.Scan(&plan.ID, &groupID, &plan.TransferEnableGiB, &plan.Name, &speedLimit, &plan.Show,
			&plan.SortPosition, &plan.Renew, &plan.Content, &resetMethod, &capacityLimit, &prices, &plan.Sell,
			&deviceLimit, &tags, &revision, &plan.CreatedAt, &plan.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan imported legacy plan: %w", err)
		}
		if revision != 1 {
			return nil, fmt.Errorf("imported legacy plan id %d has unexpected revision", plan.ID)
		}
		plan.GroupID = nullableInt64Pointer(groupID)
		plan.SpeedLimit = nullableInt64Pointer(speedLimit)
		plan.ResetTrafficMethod = nullableInt64Pointer(resetMethod)
		plan.CapacityLimit = nullableInt64Pointer(capacityLimit)
		plan.DeviceLimit = nullableInt64Pointer(deviceLimit)
		if err := json.Unmarshal([]byte(prices), &plan.Prices); err != nil {
			return nil, fmt.Errorf("decode imported legacy plan id %d prices: %w", plan.ID, err)
		}
		if err := json.Unmarshal([]byte(tags), &plan.Tags); err != nil {
			return nil, fmt.Errorf("decode imported legacy plan id %d tags: %w", plan.ID, err)
		}
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate imported legacy plans: %w", err)
	}
	return plans, nil
}
