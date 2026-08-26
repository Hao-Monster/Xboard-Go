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
			type, status, commission_status, created_at, updated_at
		) VALUES (?, ?, 'monthly', ?, 1000, 1000, 1, 3, 0, ?, ?)
	`)
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 100_000; index++ {
		createdAt := now.Unix() + int64(index)
		if _, err := statement.ExecContext(ctx, userID, plan.ID, fmt.Sprintf("%025d", index+1), createdAt, createdAt); err != nil {
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
	b.ResetTimer()
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		page, err := database.ListAdminOrders(ctx, AdminOrderFilter{Page: 1, PageSize: 20, Status: &status})
		if err != nil {
			b.Fatal(err)
		}
		if page.Total != 100_000 || len(page.Items) != 20 {
			b.Fatalf("unexpected page: total=%d items=%d", page.Total, len(page.Items))
		}
	}
}
