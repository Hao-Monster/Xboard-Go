package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestImportLegacyPlansIsVerifiedIdempotentAndPreservesRelations(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	group, err := database.CreateServerGroup(ctx, "Legacy premium", now)
	if err != nil {
		t.Fatal(err)
	}
	method, speed, capacity, devices := int64(1), int64(200), int64(0), int64(3)
	plans := []LegacyPlan{{
		ID: 41, GroupID: &group.ID, TransferEnableGiB: 100, Name: "Legacy Pro", SpeedLimit: &speed,
		Show: true, SortPosition: 7, Renew: false, Content: "legacy content", ResetTrafficMethod: &method,
		CapacityLimit: &capacity, Prices: PlanPrices{"monthly": 123, "quarterly": 345}, Sell: true,
		DeviceLimit: &devices, Tags: []string{"推荐", "稳定"}, CreatedAt: now.Add(-time.Hour).Unix(), UpdatedAt: now.Unix(),
	}}
	input := LegacyPlansImport{
		Slice: LegacyPlansSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 4096,
		Plans: plans, Checksum: LegacyPlansChecksum(plans), RollbackBackupPath: "plans-backup.tar",
		RollbackBackupSHA256: strings.Repeat("b", 64), TrafficResetMethod: 4,
		SettingsChecksum: LegacyPlanSettingsChecksum(4),
	}
	report, err := database.ImportLegacyPlans(ctx, input, now)
	if err != nil {
		t.Fatalf("ImportLegacyPlans() error = %v", err)
	}
	if report.Plans.SourceRows != 1 || report.Plans.TargetRows != 1 || report.Plans.SourceChecksum != report.Plans.TargetChecksum ||
		report.TrafficResetMethod.SourceChecksum != report.TrafficResetMethod.TargetChecksum || report.AlreadyApplied {
		t.Fatalf("report = %#v", report)
	}
	settings, err := database.GetSiteSettings(ctx)
	if err != nil || settings.TrafficResetMethod != 4 {
		t.Fatalf("traffic reset setting = %#v, err=%v", settings, err)
	}
	imported, err := database.GetPlan(ctx, 41, now)
	if err != nil || imported.Name != "Legacy Pro" || imported.Prices["monthly"] != 123 || imported.GroupID == nil || *imported.GroupID != group.ID {
		t.Fatalf("imported plan = %#v, err=%v", imported, err)
	}
	second, err := database.ImportLegacyPlans(ctx, input, now.Add(time.Minute))
	if err != nil || !second.AlreadyApplied || second.AppliedAt != report.AppliedAt {
		t.Fatalf("idempotent import = %#v, err=%v", second, err)
	}
}

func TestImportLegacyPlansRejectsDirtyTargetAndMissingGroupAtomically(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	base := LegacyPlansImport{Slice: LegacyPlansSlice, SourceSHA256: strings.Repeat("c", 64), SourceSize: 1,
		RollbackBackupPath: "backup", RollbackBackupSHA256: strings.Repeat("d", 64), TrafficResetMethod: 1,
		SettingsChecksum: LegacyPlanSettingsChecksum(1)}
	t.Run("missing group", func(t *testing.T) {
		database := newTestStore(t)
		missing := int64(999)
		base.Plans = []LegacyPlan{{ID: 1, GroupID: &missing, TransferEnableGiB: 1, Name: "Missing", Prices: PlanPrices{}, Tags: []string{}, CreatedAt: 1, UpdatedAt: 1}}
		base.Checksum = LegacyPlansChecksum(base.Plans)
		if _, err := database.ImportLegacyPlans(ctx, base, now); !errors.Is(err, ErrConflict) {
			t.Fatalf("missing group error = %v, want ErrConflict", err)
		}
		if plans, _ := database.ListPlans(ctx, now); len(plans) != 0 {
			t.Fatalf("partial plans = %#v", plans)
		}
	})
	t.Run("dirty target", func(t *testing.T) {
		database := newTestStore(t)
		if _, err := database.CreatePlan(ctx, SavePlanInput{Name: "existing", TransferEnableGiB: 1}, now); err != nil {
			t.Fatal(err)
		}
		base.Plans = []LegacyPlan{}
		base.Checksum = LegacyPlansChecksum(base.Plans)
		if _, err := database.ImportLegacyPlans(ctx, base, now); !errors.Is(err, ErrConflict) {
			t.Fatalf("dirty target error = %v, want ErrConflict", err)
		}
	})
	t.Run("dirty traffic reset setting", func(t *testing.T) {
		database := newTestStore(t)
		if _, err := database.db.ExecContext(ctx, `UPDATE app_settings SET traffic_reset_method = 2 WHERE id = 1`); err != nil {
			t.Fatal(err)
		}
		base.Plans = []LegacyPlan{}
		base.Checksum = LegacyPlansChecksum(base.Plans)
		if _, err := database.ImportLegacyPlans(ctx, base, now); !errors.Is(err, ErrConflict) {
			t.Fatalf("dirty traffic reset setting error = %v, want ErrConflict", err)
		}
	})
}
