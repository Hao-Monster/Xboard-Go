package legacymigration

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReadCouponsSnapshotPreservesRestrictionsAndGlobalSetting(t *testing.T) {
	path := createLegacyCouponsSnapshot(t)
	snapshot, err := ReadCouponsSnapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadCouponsSnapshot() error = %v", err)
	}
	if snapshot.Path != path || snapshot.Size < 512 || len(snapshot.SHA256) != 64 || len(snapshot.CouponsChecksum) != 64 ||
		len(snapshot.SettingsChecksum) != 64 || snapshot.CouponEnabled || len(snapshot.Coupons) != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	fixed, percentage := snapshot.Coupons[0], snapshot.Coupons[1]
	if fixed.Code != "FIXED123" || fixed.Value != 1_234 || fixed.LimitUse == nil || *fixed.LimitUse != 3 ||
		len(fixed.LimitPlanIDs) != 1 || fixed.LimitPlanIDs[0] != 7 || len(fixed.LimitPeriods) != 1 || fixed.LimitPeriods[0] != "monthly" {
		t.Fatalf("fixed coupon = %#v", fixed)
	}
	if percentage.Type != 2 || percentage.Value != 15 || percentage.LimitUseWithUser == nil || *percentage.LimitUseWithUser != 0 ||
		percentage.LimitPlanIDs == nil || percentage.LimitPeriods == nil {
		t.Fatalf("percentage coupon = %#v", percentage)
	}
}

func TestReadCouponsSnapshotRejectsDuplicateCodesAndInvalidRestrictions(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		statement string
		contains  string
	}{
		{name: "duplicate code", statement: `UPDATE v2_coupon SET code = 'FIXED123' WHERE id = 2`, contains: "duplicate legacy coupon code"},
		{name: "invalid plans", statement: `UPDATE v2_coupon SET limit_plan_ids = '[0]' WHERE id = 1`, contains: "invalid plan restriction"},
		{name: "invalid period JSON", statement: `UPDATE v2_coupon SET limit_period = '["month_price"] trailing' WHERE id = 1`, contains: "period restrictions"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path := createLegacyCouponsSnapshot(t)
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(scenario.statement); err != nil {
				_ = database.Close()
				t.Fatal(err)
			}
			_ = database.Close()
			if _, err := ReadCouponsSnapshot(context.Background(), path); err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(scenario.contains)) {
				t.Fatalf("ReadCouponsSnapshot() error = %v, want %q", err, scenario.contains)
			}
		})
	}
}

func createLegacyCouponsSnapshot(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-coupons.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_coupon (
			id INTEGER PRIMARY KEY, code TEXT NOT NULL, name TEXT NOT NULL, type INTEGER NOT NULL, value INTEGER NOT NULL,
			show INTEGER NOT NULL, limit_use INTEGER, limit_use_with_user INTEGER, limit_plan_ids TEXT, limit_period TEXT,
			started_at INTEGER NOT NULL, ended_at INTEGER NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
		);
		CREATE TABLE v2_settings (id INTEGER PRIMARY KEY, name TEXT NOT NULL, value TEXT);
		INSERT INTO v2_settings VALUES (1, 'app_enable_coupon_system', '0');
		INSERT INTO v2_coupon VALUES (1, 'FIXED123', '固定优惠', 1, 1234, 1, 3, 1, '["7"]', '["month_price"]', 1700000000, 1800000000, 1690000000, 1690000100);
		INSERT INTO v2_coupon VALUES (2, 'PERCENT15', '比例优惠', 2, 15, 0, NULL, 0, NULL, NULL, 1700000000, 1800000000, 1690000000, 1690000100);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
