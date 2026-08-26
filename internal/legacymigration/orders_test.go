package legacymigration

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReadOrdersSnapshotPreservesLegacyFinanceStateAndPeriodNames(t *testing.T) {
	path := createLegacyOrdersSnapshot(t)
	snapshot, err := ReadOrdersSnapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadOrdersSnapshot() error = %v", err)
	}
	if snapshot.Path != path || snapshot.Size < 512 || len(snapshot.SHA256) != 64 || len(snapshot.Checksum) != 64 || len(snapshot.Orders) != 3 {
		t.Fatalf("snapshot identity/count = path:%q size:%d orders:%d", snapshot.Path, snapshot.Size, len(snapshot.Orders))
	}
	first, upgraded, pending := snapshot.Orders[0], snapshot.Orders[1], snapshot.Orders[2]
	if first.OriginalAmount != 1_000 || first.TotalAmount != 800 || first.BalanceAmount != 100 || first.DiscountAmount != 100 ||
		first.Period != "monthly" || first.CallbackNo == nil || *first.CallbackNo != "gateway-1" || first.PaidAt == nil {
		t.Fatalf("first order = %#v", first)
	}
	if upgraded.OriginalAmount != 700 || len(upgraded.SurplusOrderIDs) != 1 || upgraded.SurplusOrderIDs[0] != first.ID ||
		upgraded.Status != 4 || upgraded.EntitlementExpiredAtBefore == nil || upgraded.EntitlementExpiredAtAfter == nil {
		t.Fatalf("upgrade order = %#v", upgraded)
	}
	if pending.Period != "monthly" || len(pending.TradeNo) != 32 || pending.OriginalAmount != 0 || pending.SurplusOrderIDs == nil {
		t.Fatalf("pending order = %#v", pending)
	}
}

func TestReadOrdersSnapshotRejectsInvalidOrAmbiguousLegacyState(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		statement string
		contains  string
	}{
		{name: "invalid trade", statement: `UPDATE v2_order SET trade_no = 'bad' WHERE id = 1`, contains: "invalid legacy order"},
		{name: "multiple active", statement: `UPDATE v2_order SET user_id = 2, status = 1 WHERE id = 2`, contains: "multiple active"},
		{name: "missing surplus", statement: `UPDATE v2_order SET surplus_order_ids = '[999]' WHERE id = 2`, contains: "missing or foreign"},
		{name: "invalid commission", statement: `UPDATE v2_order SET commission_status = 9 WHERE id = 1`, contains: "invalid legacy order"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path := createLegacyOrdersSnapshot(t)
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(scenario.statement); err != nil {
				_ = database.Close()
				t.Fatal(err)
			}
			_ = database.Close()
			if _, err := ReadOrdersSnapshot(context.Background(), path); err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(scenario.contains)) {
				t.Fatalf("ReadOrdersSnapshot() error = %v, want %q", err, scenario.contains)
			}
		})
	}
}

func createLegacyOrdersSnapshot(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-orders.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_order (
			id INTEGER PRIMARY KEY, invite_user_id INTEGER, user_id INTEGER NOT NULL, plan_id INTEGER NOT NULL,
			coupon_id INTEGER, payment_id INTEGER, type INTEGER NOT NULL, period TEXT NOT NULL, trade_no TEXT NOT NULL,
			callback_no TEXT, total_amount INTEGER NOT NULL, handling_amount INTEGER, discount_amount INTEGER,
			surplus_amount INTEGER, surplus_credit INTEGER, balance_amount INTEGER, surplus_order_ids TEXT,
			status INTEGER NOT NULL, commission_status INTEGER, commission_balance INTEGER,
			actual_commission_balance INTEGER, paid_at INTEGER, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
			distributor_order_id INTEGER, entitlement_expired_at_before INTEGER, entitlement_expired_at_after INTEGER,
			distributor_idempotency_key TEXT, distributor_settled_by INTEGER
		);
		INSERT INTO v2_order VALUES (
			1, NULL, 1, 10, NULL, 3, 1, 'monthly', '2026082612000000000000001', 'gateway-1',
			800, 5, 100, 0, 0, 100, '[]', 3, 2, 50, 40, 1700000200, 1700000000, 1700000200,
			NULL, NULL, 1800000000, NULL, NULL
		);
		INSERT INTO v2_order VALUES (
			2, 1, 1, 11, NULL, NULL, 3, 'yearly', '2026082612000000000000002', NULL,
			500, NULL, 0, 200, 0, 0, '[1]', 4, 0, 0, NULL, NULL, 1700000300, 1700000400,
			NULL, 1800000000, 1900000000, NULL, NULL
		);
		INSERT INTO v2_order VALUES (
			3, NULL, 2, 10, NULL, NULL, 1, 'month_price', '0123456789abcdef0123456789abcdef', NULL,
			0, NULL, 0, 0, 0, 0, NULL, 0, 0, 0, NULL, NULL, 1700000500, 1700000500,
			NULL, NULL, NULL, NULL, NULL
		);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
