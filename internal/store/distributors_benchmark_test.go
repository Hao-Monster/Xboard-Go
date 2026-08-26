package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func BenchmarkDistributorOrders100K(b *testing.B) {
	database := newTestStore(b)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, distributor := createDistributorFixture(b, database, now)
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	users, err := tx.PrepareContext(ctx, `
		INSERT INTO users (
			id,email,password_hash,account_kind,uuid,group_id,plan_id,transfer_enable,expired_at,
			speed_limit,device_limit,subscription_token,created_at,updated_at
		) VALUES (?, ?, '!internal:benchmark', ?, ?, ?, ?, ?, ?, 200, 3, ?, ?, ?)
	`)
	if err != nil {
		b.Fatal(err)
	}
	orders, err := tx.PrepareContext(ctx, `
		INSERT INTO orders (
			id,user_id,plan_id,period,trade_no,original_amount,total_amount,type,status,
			callback_no,entitlement_expired_at_after,created_at,updated_at
		) VALUES (?, ?, ?, 'monthly', ?, 100000, 100000, 1, 3, 'distributor_auto', ?, ?, ?)
	`)
	if err != nil {
		b.Fatal(err)
	}
	subscriptions, err := tx.PrepareContext(ctx, `
		INSERT INTO distributor_subscriptions (
			id,original_order_id,distributor_user_id,subscriber_user_id,claim_token_hash,
			delivery_status,settlement_status,hwid_enabled,hwid_limit,created_at,updated_at
		) VALUES (?, ?, ?, ?, ?, 0, 0, 1, 1, ?, ?)
	`)
	if err != nil {
		b.Fatal(err)
	}
	links, err := tx.PrepareContext(ctx, `UPDATE orders SET distributor_order_id = ? WHERE id = ?`)
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 100_000; index++ {
		subscriberID := int64(1_000_000 + index)
		orderID := int64(2_000_000 + index)
		subscriptionID := int64(3_000_000 + index)
		createdAt := now.Unix() + int64(index)
		if _, err := users.ExecContext(ctx, subscriberID, fmt.Sprintf("dist-bench-%06d@internal.invalid", index),
			AccountKindInternalSubscription, fmt.Sprintf("00000000-0000-4000-8000-%012x", subscriberID),
			nullableInt64Value(plan.GroupID), plan.ID, 100*bytesPerGiB, now.AddDate(0, 1, 0).Unix(),
			fmt.Sprintf("%032x", subscriberID), createdAt, createdAt); err != nil {
			b.Fatal(err)
		}
		if _, err := orders.ExecContext(ctx, orderID, distributor.ID, plan.ID, fmt.Sprintf("%025d", orderID),
			now.AddDate(0, 1, 0).Unix(), createdAt, createdAt); err != nil {
			b.Fatal(err)
		}
		if _, err := subscriptions.ExecContext(ctx, subscriptionID, orderID, distributor.ID, subscriberID,
			fmt.Sprintf("%064x", subscriptionID), createdAt, createdAt); err != nil {
			b.Fatal(err)
		}
		if _, err := links.ExecContext(ctx, subscriptionID, orderID); err != nil {
			b.Fatal(err)
		}
	}
	if err := users.Close(); err != nil {
		b.Fatal(err)
	}
	if err := orders.Close(); err != nil {
		b.Fatal(err)
	}
	if err := subscriptions.Close(); err != nil {
		b.Fatal(err)
	}
	if err := links.Close(); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}

	distributorID := distributor.ID
	b.Run("page-200", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			page, err := database.ListDistributorOrders(ctx, DistributorOrderFilter{
				DistributorUserID: &distributorID, Page: 1, PageSize: 200,
			}, now.AddDate(0, 0, 1))
			if err != nil || page.Total != 100_000 || len(page.Items) != 200 {
				b.Fatalf("ListDistributorOrders() total=%d items=%d error=%v", page.Total, len(page.Items), err)
			}
		}
	})
	b.Run("stream-export", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rows := 0
			if err := database.StreamDistributorOrderExport(ctx, DistributorOrderFilter{
				DistributorUserID: &distributorID,
			}, func(DistributorOrderExportRow) error {
				rows++
				return nil
			}); err != nil || rows != 100_000 {
				b.Fatalf("StreamDistributorOrderExport() rows=%d error=%v", rows, err)
			}
		}
	})
}
