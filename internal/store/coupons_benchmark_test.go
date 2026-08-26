package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

const benchmarkCouponRows = 100_000

func BenchmarkCheckCouponAtHundredThousandRows(b *testing.B) {
	database, planID, userID, now := benchmarkCouponStore(b)
	input := CouponCheckInput{UserID: userID, PlanID: planID, Period: "monthly", Code: "BENCH099999"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := database.CheckCoupon(context.Background(), input, now); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListCouponsAtHundredThousandRows(b *testing.B) {
	database, _, _, _ := benchmarkCouponStore(b)
	filter := CouponFilter{Page: 1, PageSize: 20}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		page, err := database.ListCoupons(context.Background(), filter)
		if err != nil {
			b.Fatal(err)
		}
		if page.Total != benchmarkCouponRows || len(page.Items) != filter.PageSize {
			b.Fatalf("ListCoupons() total/items = %d/%d", page.Total, len(page.Items))
		}
	}
}

func benchmarkCouponStore(b *testing.B) (*Store, int64, int64, time.Time) {
	b.Helper()
	database := newTestStore(b)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(b, database, now, PlanPrices{"monthly": 100_000}, nil)
	tx, err := database.db.BeginTx(context.Background(), nil)
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.Prepare(`
		INSERT INTO coupons (
			code, name, type, value, show, limit_plan_ids_json, limit_periods_json,
			started_at, ended_at, created_at, updated_at
		) VALUES (?, 'benchmark coupon', 1, 100, 1, '[]', '[]', ?, ?, ?, ?)
	`)
	if err != nil {
		_ = tx.Rollback()
		b.Fatal(err)
	}
	for index := 0; index < benchmarkCouponRows; index++ {
		if _, err := statement.Exec(fmt.Sprintf("BENCH%06d", index), now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix(), now.Unix(), now.Unix()); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			b.Fatal(err)
		}
	}
	if err := statement.Close(); err != nil {
		_ = tx.Rollback()
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	return database, plan.ID, userID, now
}
