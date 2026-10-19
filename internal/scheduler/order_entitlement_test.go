package scheduler

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type committedEntitlementObserver struct {
	t       *testing.T
	db      *store.Store
	tradeNo string
	changes []store.EntitlementChange
}

func (n *committedEntitlementObserver) NotifyEntitlementChange(ctx context.Context, c *store.EntitlementChange) {
	order, err := n.db.GetAdminOrder(ctx, n.tradeNo)
	if err != nil || order.Status != store.OrderStatusCompleted {
		n.t.Fatalf("notification before commit: status=%d err=%v", order.Status, err)
	}
	n.changes = append(n.changes, *c)
}

func TestBL01SchedulerNotifiesOnlyCommittedRecovery(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[fail], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "QA-scheduler.db")
			db, err := store.OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := db.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
			user, err := db.CreateAdminUser(t.Context(), store.CreateAdminUserInput{Email: "QA-scheduler@example.test", PasswordHash: "hash"}, now)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := db.CreatePlan(t.Context(), store.SavePlanInput{Name: "QA recovery", TransferEnableGiB: 10, Prices: store.PlanPrices{"monthly": 500}}, now)
			if err != nil {
				t.Fatal(err)
			}
			order, err := db.AssignOrder(t.Context(), store.AssignOrderInput{Email: user.Email, PlanID: plan.ID, Period: "monthly", TotalAmount: 500}, now)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = raw.Close() })
			if _, err := raw.ExecContext(t.Context(), `UPDATE orders SET status=1 WHERE id=?`, order.ID); err != nil {
				t.Fatal(err)
			}
			if fail {
				if _, err := raw.ExecContext(t.Context(), `CREATE TRIGGER fail_entitlement BEFORE INSERT ON order_entitlement_events BEGIN SELECT RAISE(ABORT,'QA rollback'); END`); err != nil {
					t.Fatal(err)
				}
			}
			observer := &committedEntitlementObserver{t: t, db: db, tradeNo: order.TradeNo}
			worker := NewWorker(db, time.Second, nil)
			worker.SetEntitlementNotifier(observer)
			worker.processOrders(t.Context(), now)
			if fail {
				if len(observer.changes) != 0 {
					t.Fatal("rolled back batch notified")
				}
				return
			}
			if len(observer.changes) != 1 || observer.changes[0].UserID != user.ID || observer.changes[0].PreviousUUID == "" {
				t.Fatalf("changes=%+v", observer.changes)
			}
			worker.processOrders(t.Context(), now.Add(time.Minute))
			if len(observer.changes) != 1 {
				t.Fatal("recovery replay notified")
			}
		})
	}
}
