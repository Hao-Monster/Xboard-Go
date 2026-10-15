package store

import (
	"database/sql"
	"testing"
	"time"
)

func TestBL01OrderEntitlementChange(t *testing.T) {
	for _, scenario := range []string{"new", "renewal", "upgrade", "onetime", "reset_traffic", "processing", "rollback"} {
		t.Run(scenario, func(t *testing.T) {
			db := newTestStore(t)
			ctx := t.Context()
			now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
			plan, userID := createOrderFixture(t, db, now, PlanPrices{"monthly": 1000, "onetime": 1000, "reset_traffic": 100}, nil)
			oldGroup := *plan.GroupID
			if scenario == "renewal" || scenario == "upgrade" || scenario == "reset_traffic" {
				if _, err := db.db.ExecContext(ctx, `UPDATE users SET plan_id=?,expired_at=? WHERE id=?`, plan.ID, now.Add(24*time.Hour).Unix(), userID); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "upgrade" {
				group, err := db.CreateServerGroup(ctx, "New entitlement group", now)
				if err != nil {
					t.Fatal(err)
				}
				plan, err = db.CreatePlan(ctx, SavePlanInput{Name: "Upgrade", GroupID: &group.ID, TransferEnableGiB: 200, Prices: PlanPrices{"monthly": 2000}}, now)
				if err != nil {
					t.Fatal(err)
				}
				plan, err = db.SetPlanState(ctx, plan.ID, plan.Revision, PlanState{Show: true, Sell: true, Renew: true}, now)
				if err != nil {
					t.Fatal(err)
				}
			}
			period := "monthly"
			if scenario == "onetime" || scenario == "reset_traffic" {
				period = scenario
			}
			order, err := db.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: period}, now)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "rollback" {
				if _, err := db.db.ExecContext(ctx, `CREATE TRIGGER fail_entitlement BEFORE INSERT ON order_entitlement_events BEGIN SELECT RAISE(ABORT,'QA rollback'); END`); err != nil {
					t.Fatal(err)
				}
				completed, err := db.CompleteOrder(ctx, order.TradeNo, "QA", now)
				if err == nil || completed.Change != nil {
					t.Fatalf("rollback result=%+v err=%v", completed, err)
				}
				got, err := db.GetUserOrder(ctx, userID, order.TradeNo)
				if err != nil || got.Status != OrderStatusPending {
					t.Fatalf("rollback order=%+v err=%v", got, err)
				}
				var actualPlan, expiry sql.NullInt64
				var group, transfer, events int64
				if err := db.db.QueryRowContext(ctx, `SELECT plan_id, expired_at, group_id, transfer_enable, (SELECT COUNT(*) FROM order_entitlement_events WHERE order_id=?) FROM users WHERE id=?`, order.ID, userID).Scan(&actualPlan, &expiry, &group, &transfer, &events); err != nil {
					t.Fatal(err)
				}
				if actualPlan.Valid || expiry.Valid || group != oldGroup || transfer != 1 || events != 0 {
					t.Fatalf("rolled back user plan/expiry/group/transfer/events=%v/%v/%d/%d/%d", actualPlan, expiry, group, transfer, events)
				}
				return
			}
			var change *EntitlementChange
			if scenario == "processing" {
				if _, err := db.db.ExecContext(ctx, `UPDATE orders SET status=1 WHERE id=?`, order.ID); err != nil {
					t.Fatal(err)
				}
				batch, err := db.ProcessStaleOrders(ctx, now, 10)
				if err != nil {
					t.Fatal(err)
				}
				if batch.Completed != 1 || len(batch.Changes) != 1 {
					t.Fatalf("recovery result=%+v", batch)
				}
				change = &batch.Changes[0]
			} else {
				completed, err := db.CompleteOrder(ctx, order.TradeNo, "QA", now)
				if err != nil {
					t.Fatal(err)
				}
				change = completed.Change
			}
			if change == nil || change.UserID != userID || change.PreviousUUID != "020d6dd3-bc81-43c7-8011-71845ef9df88" || change.OldGroupID == nil || *change.OldGroupID != oldGroup || change.NewGroupID == nil || *change.NewGroupID != *plan.GroupID {
				t.Fatalf("change=%+v want user=%d groups=%d/%d", change, userID, oldGroup, *plan.GroupID)
			}
			repeated, err := db.CompleteOrder(ctx, order.TradeNo, "QA", now)
			if err != nil || repeated.Change != nil {
				t.Fatalf("repeat=%+v err=%v", repeated, err)
			}
		})
	}
}
