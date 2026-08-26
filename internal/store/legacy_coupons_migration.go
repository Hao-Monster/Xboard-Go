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
	LegacyCouponsSlice = "coupons-v1"
	maxLegacyCoupons   = 1_000_000
)

type LegacyCoupon struct {
	ID               int64      `json:"id"`
	Code             string     `json:"code"`
	Name             string     `json:"name"`
	Type             CouponType `json:"type"`
	Value            int64      `json:"value"`
	Show             bool       `json:"show"`
	LimitUse         *int       `json:"limit_use"`
	LimitUseWithUser *int       `json:"limit_use_with_user"`
	LimitPlanIDs     []int64    `json:"limit_plan_ids"`
	LimitPeriods     []string   `json:"limit_period"`
	StartedAt        int64      `json:"started_at"`
	EndedAt          int64      `json:"ended_at"`
	CreatedAt        int64      `json:"created_at"`
	UpdatedAt        int64      `json:"updated_at"`
}

type LegacyCouponsImport struct {
	Slice                string
	SourceSHA256         string
	SourceSize           int64
	Coupons              []LegacyCoupon
	CouponsChecksum      string
	CouponEnabled        bool
	SettingsChecksum     string
	RollbackBackupPath   string
	RollbackBackupSHA256 string
}

type LegacyCouponsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Coupons              LegacyDomainResult `json:"coupons"`
	Settings             LegacyDomainResult `json:"settings"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func LegacyCouponsChecksum(coupons []LegacyCoupon) string {
	ordered := append([]LegacyCoupon(nil), coupons...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	if ordered == nil {
		ordered = []LegacyCoupon{}
	}
	return legacyCanonicalChecksum(ordered)
}

func LegacyCouponSettingsChecksum(enabled bool) string {
	return legacyCanonicalChecksum(struct {
		CouponEnabled bool `json:"coupon_enabled"`
	}{CouponEnabled: enabled})
}

func ValidateLegacyCouponsData(coupons []LegacyCoupon) error {
	if len(coupons) > maxLegacyCoupons {
		return fmt.Errorf("%w: legacy coupons exceed the %d-row migration limit", ErrInvalidInput, maxLegacyCoupons)
	}
	ids := make(map[int64]struct{}, len(coupons))
	codes := make(map[string]struct{}, len(coupons))
	for _, coupon := range coupons {
		if coupon.ID < 1 || !validCouponCode(coupon.Code) || coupon.Code != strings.TrimSpace(coupon.Code) ||
			!utf8.ValidString(coupon.Name) || len(coupon.Name) < 1 || len(coupon.Name) > maxCouponNameBytes ||
			strings.IndexFunc(coupon.Name, unicode.IsControl) >= 0 || coupon.Name != strings.TrimSpace(coupon.Name) ||
			coupon.Type != CouponTypeFixed && coupon.Type != CouponTypePercentage ||
			coupon.Type == CouponTypeFixed && (coupon.Value < 1 || coupon.Value > maxOrderMoneyCents) ||
			coupon.Type == CouponTypePercentage && (coupon.Value < 1 || coupon.Value > 100) ||
			coupon.LimitUse != nil && (*coupon.LimitUse < 0 || *coupon.LimitUse > maxCouponUseLimit) ||
			coupon.LimitUseWithUser != nil && (*coupon.LimitUseWithUser < 0 || *coupon.LimitUseWithUser > maxCouponUseLimit) ||
			!validLegacyUnixTimestamp(coupon.StartedAt) || !validLegacyUnixTimestamp(coupon.EndedAt) || coupon.EndedAt <= coupon.StartedAt ||
			!validLegacyUnixTimestamp(coupon.CreatedAt) || !validLegacyUnixTimestamp(coupon.UpdatedAt) || coupon.UpdatedAt < coupon.CreatedAt ||
			len(coupon.LimitPlanIDs) > maxCouponPlanIDs || len(coupon.LimitPeriods) > len(orderPeriodMonths)+2 {
			return fmt.Errorf("%w: invalid legacy coupon id %d", ErrInvalidInput, coupon.ID)
		}
		if _, exists := ids[coupon.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy coupon id %d", ErrInvalidInput, coupon.ID)
		}
		ids[coupon.ID] = struct{}{}
		if _, exists := codes[coupon.Code]; exists {
			return fmt.Errorf("%w: duplicate legacy coupon code %q", ErrConflict, coupon.Code)
		}
		codes[coupon.Code] = struct{}{}
		seenPlans := make(map[int64]struct{}, len(coupon.LimitPlanIDs))
		for _, planID := range coupon.LimitPlanIDs {
			if planID < 1 {
				return fmt.Errorf("%w: legacy coupon id %d has an invalid plan restriction", ErrInvalidInput, coupon.ID)
			}
			if _, duplicate := seenPlans[planID]; duplicate {
				return fmt.Errorf("%w: legacy coupon id %d has duplicate plan restrictions", ErrInvalidInput, coupon.ID)
			}
			seenPlans[planID] = struct{}{}
		}
		seenPeriods := make(map[string]struct{}, len(coupon.LimitPeriods))
		for _, period := range coupon.LimitPeriods {
			_, monthly := orderPeriodMonths[period]
			if !monthly && period != "onetime" && period != "reset_traffic" {
				return fmt.Errorf("%w: legacy coupon id %d has an invalid period restriction", ErrInvalidInput, coupon.ID)
			}
			if _, duplicate := seenPeriods[period]; duplicate {
				return fmt.Errorf("%w: legacy coupon id %d has duplicate period restrictions", ErrInvalidInput, coupon.ID)
			}
			seenPeriods[period] = struct{}{}
		}
	}
	return nil
}

func (s *Store) LookupLegacyCouponsImport(ctx context.Context, sourceSHA256 string) (LegacyCouponsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyCouponsImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacyCouponsImport(ctx, s.db, sourceSHA256)
}

func lookupLegacyCouponsImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyCouponsImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?`, LegacyCouponsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyCouponsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyCouponsImportReport{}, false, fmt.Errorf("lookup legacy coupon migration: %w", err)
	}
	var report LegacyCouponsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyCouponsImportReport{}, false, fmt.Errorf("decode legacy coupon migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyCoupons(ctx context.Context, input LegacyCouponsImport, now time.Time) (LegacyCouponsImportReport, error) {
	if err := validateLegacyCouponsImport(input); err != nil {
		return LegacyCouponsImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyCouponsImportReport{}, fmt.Errorf("begin legacy coupon import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return LegacyCouponsImportReport{}, fmt.Errorf("read legacy coupon target schema: %w", err)
	}
	if version != CurrentSchemaVersion() {
		return LegacyCouponsImportReport{}, fmt.Errorf("legacy coupon import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyCouponsImportReport{}, fmt.Errorf("validate legacy coupon target schema: %w", err)
	}
	if existing, found, err := lookupLegacyCouponsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyCouponsImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyCouponsImportReport{}, fmt.Errorf("commit idempotent legacy coupon import: %w", err)
		}
		return existing, nil
	}
	var otherRuns, targetCoupons int
	var currentEnabled bool
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyCouponsSlice).Scan(&otherRuns); err != nil {
		return LegacyCouponsImportReport{}, fmt.Errorf("count legacy coupon migrations: %w", err)
	}
	if otherRuns != 0 {
		return LegacyCouponsImportReport{}, fmt.Errorf("%w: legacy coupon slice was already imported from another snapshot", ErrConflict)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM coupons`).Scan(&targetCoupons); err != nil {
		return LegacyCouponsImportReport{}, fmt.Errorf("count target coupons: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT coupon_enabled FROM app_settings WHERE id = 1`).Scan(&currentEnabled); err != nil {
		return LegacyCouponsImportReport{}, fmt.Errorf("read target coupon setting: %w", err)
	}
	if targetCoupons != 0 || !currentEnabled {
		return LegacyCouponsImportReport{}, fmt.Errorf("%w: legacy coupon import requires empty coupons and the default enabled setting", ErrConflict)
	}
	if err := validateLegacyCouponPlanReferences(ctx, tx, input.Coupons); err != nil {
		return LegacyCouponsImportReport{}, err
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO coupons (id, code, name, type, value, show, limit_use, limit_use_with_user,
			limit_plan_ids_json, limit_periods_json, started_at, ended_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return LegacyCouponsImportReport{}, fmt.Errorf("prepare legacy coupon import: %w", err)
	}
	defer statement.Close()
	for _, coupon := range input.Coupons {
		plans, _ := json.Marshal(coupon.LimitPlanIDs)
		periods, _ := json.Marshal(coupon.LimitPeriods)
		if _, err := statement.ExecContext(ctx, coupon.ID, coupon.Code, coupon.Name, coupon.Type, coupon.Value, coupon.Show,
			nullableIntValue(coupon.LimitUse), nullableIntValue(coupon.LimitUseWithUser), string(plans), string(periods),
			coupon.StartedAt, coupon.EndedAt, coupon.CreatedAt, coupon.UpdatedAt); err != nil {
			return LegacyCouponsImportReport{}, fmt.Errorf("import legacy coupon id %d: %w", coupon.ID, err)
		}
	}
	if err := validateExistingOrderCouponReferences(ctx, tx); err != nil {
		return LegacyCouponsImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE app_settings SET coupon_enabled = ? WHERE id = 1`, input.CouponEnabled); err != nil {
		return LegacyCouponsImportReport{}, fmt.Errorf("import legacy coupon setting: %w", err)
	}
	target, err := readLegacyTargetCoupons(ctx, tx)
	if err != nil {
		return LegacyCouponsImportReport{}, err
	}
	var targetEnabled bool
	if err := tx.QueryRowContext(ctx, `SELECT coupon_enabled FROM app_settings WHERE id = 1`).Scan(&targetEnabled); err != nil {
		return LegacyCouponsImportReport{}, fmt.Errorf("verify legacy coupon setting: %w", err)
	}
	report := LegacyCouponsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Coupons:   LegacyDomainResult{SourceRows: len(input.Coupons), TargetRows: len(target), SourceChecksum: input.CouponsChecksum, TargetChecksum: LegacyCouponsChecksum(target)},
		Settings:  LegacyDomainResult{SourceRows: 1, TargetRows: 1, SourceChecksum: input.SettingsChecksum, TargetChecksum: LegacyCouponSettingsChecksum(targetEnabled)},
		AppliedAt: now.UTC(),
	}
	if report.Coupons.SourceRows != report.Coupons.TargetRows || report.Coupons.SourceChecksum != report.Coupons.TargetChecksum || report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		return LegacyCouponsImportReport{}, errors.New("legacy coupon target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyCouponsImportReport{}, fmt.Errorf("encode legacy coupon migration report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_migration_runs (slice, source_sha256, source_size, rollback_backup_path, rollback_backup_sha256, report_json, applied_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyCouponsImportReport{}, fmt.Errorf("record legacy coupon migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyCouponsImportReport{}, fmt.Errorf("commit legacy coupon import: %w", err)
	}
	return report, nil
}

func validateLegacyCouponsImport(input LegacyCouponsImport) error {
	if input.Slice != LegacyCouponsSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) ||
		strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 || !validLowerSHA256(input.RollbackBackupSHA256) ||
		input.CouponsChecksum != LegacyCouponsChecksum(input.Coupons) || input.SettingsChecksum != LegacyCouponSettingsChecksum(input.CouponEnabled) {
		return fmt.Errorf("%w: invalid legacy coupon import", ErrInvalidInput)
	}
	return ValidateLegacyCouponsData(input.Coupons)
}

func validateLegacyCouponPlanReferences(ctx context.Context, tx *sql.Tx, coupons []LegacyCoupon) error {
	checked := make(map[int64]struct{})
	for _, coupon := range coupons {
		for _, planID := range coupon.LimitPlanIDs {
			if _, exists := checked[planID]; exists {
				continue
			}
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plans WHERE id = ?)`, planID).Scan(&exists); err != nil {
				return fmt.Errorf("validate legacy coupon plan: %w", err)
			}
			if !exists {
				return fmt.Errorf("%w: legacy coupons reference missing plan %d", ErrConflict, planID)
			}
			checked[planID] = struct{}{}
		}
	}
	return nil
}

func validateExistingOrderCouponReferences(ctx context.Context, tx *sql.Tx) error {
	var missing int64
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MIN(orders.coupon_id), 0)
		FROM orders LEFT JOIN coupons ON coupons.id = orders.coupon_id
		WHERE orders.coupon_id IS NOT NULL AND coupons.id IS NULL
	`).Scan(&missing)
	if err != nil {
		return fmt.Errorf("validate imported order coupon references: %w", err)
	}
	if missing != 0 {
		return fmt.Errorf("%w: existing orders reference missing coupon %d", ErrConflict, missing)
	}
	return nil
}

func readLegacyTargetCoupons(ctx context.Context, database queryer) ([]LegacyCoupon, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, code, name, type, value, show, limit_use, limit_use_with_user,
		       limit_plan_ids_json, limit_periods_json, started_at, ended_at, created_at, updated_at
		FROM coupons ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("read imported legacy coupons: %w", err)
	}
	defer rows.Close()
	coupons := make([]LegacyCoupon, 0)
	for rows.Next() {
		var coupon LegacyCoupon
		var limitUse, limitUser sql.NullInt64
		var plans, periods string
		if err := rows.Scan(&coupon.ID, &coupon.Code, &coupon.Name, &coupon.Type, &coupon.Value, &coupon.Show,
			&limitUse, &limitUser, &plans, &periods, &coupon.StartedAt, &coupon.EndedAt, &coupon.CreatedAt, &coupon.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan imported legacy coupon: %w", err)
		}
		coupon.LimitUse = nullableIntPointer(limitUse)
		coupon.LimitUseWithUser = nullableIntPointer(limitUser)
		if err := json.Unmarshal([]byte(plans), &coupon.LimitPlanIDs); err != nil {
			return nil, fmt.Errorf("decode imported legacy coupon plans: %w", err)
		}
		if err := json.Unmarshal([]byte(periods), &coupon.LimitPeriods); err != nil {
			return nil, fmt.Errorf("decode imported legacy coupon periods: %w", err)
		}
		coupons = append(coupons, coupon)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate imported legacy coupons: %w", err)
	}
	return coupons, nil
}
