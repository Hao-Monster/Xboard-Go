package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestImportLegacyOrdersPreservesFinancialStateAndIsIdempotent(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "legacy-order-import@example.test", PasswordHash: "hash"}, time.Unix(50, 0))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := database.CreatePlan(ctx, SavePlanInput{Name: "Legacy order plan", TransferEnableGiB: 100, Prices: PlanPrices{"monthly": 1_000}}, time.Unix(50, 0))
	if err != nil {
		t.Fatal(err)
	}
	callback := "gateway-1"
	paidAt := int64(150)
	commissionStatus := 3
	input := LegacyOrdersImport{
		Slice: LegacyOrdersSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 8192,
		RollbackBackupPath: "/var/lib/xboard-backups/pre-orders.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
		Orders: []LegacyOrder{{
			ID: 70, UserID: user.ID, PlanID: plan.ID, Period: "monthly", TradeNo: "2026082612000000000000070",
			OriginalAmount: 1_000, TotalAmount: 700, BalanceAmount: 100, DiscountAmount: 100, SurplusAmount: 100,
			Type: OrderTypeNew, Status: OrderStatusCompleted, SurplusOrderIDs: []int64{}, CommissionStatus: &commissionStatus,
			CommissionBalance: 50, PaidAt: &paidAt, CallbackNo: &callback, CreatedAt: 100, UpdatedAt: 150,
		}},
	}
	input.Checksum = LegacyOrdersChecksum(input.Orders)
	report, err := database.ImportLegacyOrders(ctx, input, time.Unix(200, 0))
	if err != nil {
		t.Fatalf("ImportLegacyOrders() error = %v", err)
	}
	if report.AlreadyApplied || report.Orders.SourceRows != 1 || report.Orders.TargetRows != 1 || report.Orders.SourceChecksum != report.Orders.TargetChecksum {
		t.Fatalf("report = %#v", report)
	}
	order, err := database.GetAdminOrder(ctx, input.Orders[0].TradeNo)
	if err != nil || order.OriginalAmount != 1_000 || order.TotalAmount != 700 || order.BalanceAmount != 100 ||
		order.DiscountAmount != 100 || order.SurplusAmount != 100 || order.CommissionStatus == nil || *order.CommissionStatus != 3 {
		t.Fatalf("imported order = (%#v, %v)", order, err)
	}
	var events int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM order_entitlement_events`).Scan(&events); err != nil || events != 0 {
		t.Fatalf("historical entitlement events=%d err=%v", events, err)
	}
	repeated, err := database.ImportLegacyOrders(ctx, input, time.Unix(300, 0))
	if err != nil || !repeated.AlreadyApplied || !repeated.AppliedAt.Equal(time.Unix(200, 0).UTC()) {
		t.Fatalf("repeated import = (%#v, %v)", repeated, err)
	}
}

func TestImportLegacyOrdersRejectsMissingReferencesWithoutPartialWrites(t *testing.T) {
	database := newTestStore(t)
	commissionStatus := 0
	input := LegacyOrdersImport{
		Slice: LegacyOrdersSlice, SourceSHA256: strings.Repeat("c", 64), SourceSize: 4096,
		RollbackBackupPath: "/var/lib/xboard-backups/pre-orders.xbbackup", RollbackBackupSHA256: strings.Repeat("d", 64),
		Orders: []LegacyOrder{{
			ID: 1, UserID: 404, PlanID: 405, Period: "monthly", TradeNo: "2026082612000000000000001",
			OriginalAmount: 100, TotalAmount: 100, Type: OrderTypeNew, Status: OrderStatusCompleted,
			SurplusOrderIDs: []int64{}, CommissionStatus: &commissionStatus, CreatedAt: 100, UpdatedAt: 100,
		}},
	}
	input.Checksum = LegacyOrdersChecksum(input.Orders)
	if _, err := database.ImportLegacyOrders(context.Background(), input, time.Unix(200, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("ImportLegacyOrders(missing references) error = %v", err)
	}
	var orders, runs int
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&orders)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyOrdersSlice).Scan(&runs)
	if orders != 0 || runs != 0 {
		t.Fatalf("rejected import changed target: orders=%d runs=%d", orders, runs)
	}
}

func TestValidateLegacyOrdersRejectsMultipleActiveOrdersAndForeignSurplus(t *testing.T) {
	commissionStatus := 0
	base := LegacyOrder{ID: 1, UserID: 1, PlanID: 1, Period: "monthly", TradeNo: "2026082612000000000000001", OriginalAmount: 0, Type: OrderTypeNew, Status: OrderStatusPending, SurplusOrderIDs: []int64{}, CommissionStatus: &commissionStatus, CreatedAt: 1, UpdatedAt: 1}
	second := base
	second.ID = 2
	second.TradeNo = "2026082612000000000000002"
	if err := ValidateLegacyOrdersData([]LegacyOrder{base, second}); !errors.Is(err, ErrConflict) {
		t.Fatalf("multiple active orders error = %v", err)
	}
	base.Status = OrderStatusCompleted
	second.Status = OrderStatusCompleted
	second.UserID = 2
	second.SurplusOrderIDs = []int64{1}
	if err := ValidateLegacyOrdersData([]LegacyOrder{base, second}); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign surplus order error = %v", err)
	}
}
