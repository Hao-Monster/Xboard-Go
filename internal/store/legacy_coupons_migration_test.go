package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestImportLegacyCouponsPreservesIDsSettingAndIsIdempotent(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	plan, err := database.CreatePlan(ctx, SavePlanInput{Name: "Legacy coupon plan", TransferEnableGiB: 100, Prices: PlanPrices{"monthly": 1_000}}, time.Unix(50, 0))
	if err != nil {
		t.Fatal(err)
	}
	limit, userLimit := 3, 0
	input := LegacyCouponsImport{
		Slice: LegacyCouponsSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 8192,
		RollbackBackupPath: "/var/lib/xboard-backups/pre-coupons.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
		CouponEnabled: false,
		Coupons: []LegacyCoupon{{
			ID: 70, Code: "FIXED123", Name: "固定优惠", Type: CouponTypeFixed, Value: 1_234, Show: true,
			LimitUse: &limit, LimitUseWithUser: &userLimit, LimitPlanIDs: []int64{plan.ID}, LimitPeriods: []string{"monthly"},
			StartedAt: 100, EndedAt: 1_000, CreatedAt: 50, UpdatedAt: 60,
		}},
	}
	input.CouponsChecksum = LegacyCouponsChecksum(input.Coupons)
	input.SettingsChecksum = LegacyCouponSettingsChecksum(input.CouponEnabled)
	report, err := database.ImportLegacyCoupons(ctx, input, time.Unix(200, 0))
	if err != nil {
		t.Fatalf("ImportLegacyCoupons() error = %v", err)
	}
	if report.AlreadyApplied || report.Coupons.SourceRows != 1 || report.Coupons.SourceChecksum != report.Coupons.TargetChecksum || report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		t.Fatalf("report = %#v", report)
	}
	coupon, err := database.GetCoupon(ctx, 70)
	if err != nil || coupon.Code != "FIXED123" || coupon.LimitUseWithUser == nil || *coupon.LimitUseWithUser != 0 || len(coupon.LimitPlanIDs) != 1 {
		t.Fatalf("imported coupon = (%#v, %v)", coupon, err)
	}
	if enabled, err := database.CouponEnabled(ctx); err != nil || enabled {
		t.Fatalf("CouponEnabled() = (%v, %v), want false", enabled, err)
	}
	repeated, err := database.ImportLegacyCoupons(ctx, input, time.Unix(300, 0))
	if err != nil || !repeated.AlreadyApplied || !repeated.AppliedAt.Equal(time.Unix(200, 0).UTC()) {
		t.Fatalf("repeated import = (%#v, %v)", repeated, err)
	}
}

func TestImportLegacyCouponsRejectsMissingPlanWithoutPartialWrites(t *testing.T) {
	database := newTestStore(t)
	input := LegacyCouponsImport{
		Slice: LegacyCouponsSlice, SourceSHA256: strings.Repeat("c", 64), SourceSize: 4096,
		RollbackBackupPath: "/var/lib/xboard-backups/pre-coupons.xbbackup", RollbackBackupSHA256: strings.Repeat("d", 64),
		CouponEnabled: true,
		Coupons: []LegacyCoupon{{ID: 1, Code: "MISSING", Name: "missing", Type: CouponTypeFixed, Value: 1, Show: true,
			LimitPlanIDs: []int64{404}, LimitPeriods: []string{}, StartedAt: 1, EndedAt: 2, CreatedAt: 1, UpdatedAt: 1}},
	}
	input.CouponsChecksum = LegacyCouponsChecksum(input.Coupons)
	input.SettingsChecksum = LegacyCouponSettingsChecksum(input.CouponEnabled)
	if _, err := database.ImportLegacyCoupons(context.Background(), input, time.Unix(200, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("ImportLegacyCoupons(missing plan) error = %v", err)
	}
	var coupons, runs int
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM coupons`).Scan(&coupons)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyCouponsSlice).Scan(&runs)
	if coupons != 0 || runs != 0 {
		t.Fatalf("rejected import changed target: coupons=%d runs=%d", coupons, runs)
	}
}
