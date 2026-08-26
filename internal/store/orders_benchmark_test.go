package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func BenchmarkListAdminOrders100K(b *testing.B) {
	database := newTestStore(b)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(b, database, now, PlanPrices{"monthly": 1_000}, nil)
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO orders (
			user_id, plan_id, period, trade_no, original_amount, total_amount,
			type, status, commission_status, commission_balance, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 100_000; index++ {
		createdAt := now.Unix() + int64(index)
		period, orderType, status, commissionStatus := "monthly", OrderTypeNew, OrderStatusCompleted, 0
		if index%2 == 1 {
			period, orderType, status, commissionStatus = "yearly", OrderTypeRenewal, OrderStatusDiscounted, 1
		}
		amount := int64(100 + index%10_000)
		if _, err := statement.ExecContext(ctx, userID, plan.ID, period, fmt.Sprintf("%025d", index+1), amount, amount,
			orderType, status, commissionStatus, amount/10, createdAt, createdAt); err != nil {
			b.Fatal(err)
		}
	}
	if err := statement.Close(); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	status := OrderStatusCompleted
	for name, benchmark := range map[string]struct {
		filter AdminOrderFilter
		total  int64
	}{
		"status-latest": {filter: AdminOrderFilter{Page: 1, PageSize: 20, Status: &status}, total: 50_000},
		"legacy-multi-filter-total-sort": {filter: AdminOrderFilter{
			Page: 1, PageSize: 20, Statuses: []OrderStatus{OrderStatusCompleted, OrderStatusDiscounted},
			Types: []OrderType{OrderTypeNew, OrderTypeRenewal}, Periods: []string{"monthly", "yearly"},
			CommissionStatuses: []int{0, 1}, SortBy: AdminOrderSortTotalAmount,
		}, total: 100_000},
		"commission-sort-descending": {filter: AdminOrderFilter{
			Page: 1, PageSize: 20, CommissionStatuses: []int{0, 1},
			SortBy: AdminOrderSortCommissionBalance, SortDescending: true,
		}, total: 100_000},
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				page, err := database.ListAdminOrders(ctx, benchmark.filter)
				if err != nil {
					b.Fatal(err)
				}
				if page.Total != benchmark.total || len(page.Items) != 20 {
					b.Fatalf("unexpected page: total=%d items=%d", page.Total, len(page.Items))
				}
			}
		})
	}
}
