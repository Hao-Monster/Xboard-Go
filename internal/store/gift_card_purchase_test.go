package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func purchaseFixture(t *testing.T, period string, count int) (*Store, Plan, int64, GiftCardTemplate, []GiftCardCode, time.Time) {
	t.Helper()
	db := newTestStore(t)
	now := time.Date(2026, 1, 31, 4, 0, 0, 0, time.UTC)
	plan, user := createOrderFixture(t, db, now, PlanPrices{"onetime": 100, "monthly": 100}, nil)
	if _, err := db.db.Exec(`UPDATE plans SET transfer_enable_gib=200 WHERE id=?`, plan.ID); err != nil {
		t.Fatal(err)
	}
	plan.TransferEnableGiB = 200
	template, err := db.CreateGiftCardTemplate(t.Context(), SaveGiftCardTemplateInput{Name: "Purchased rights", Type: GiftCardTypePurchase, Status: true, Rewards: GiftCardReward{PlanID: &plan.ID, PurchasePeriod: period}}, user, now)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := db.GenerateGiftCardCodes(t.Context(), template.ID, GenerateGiftCardCodesInput{Count: count, MaxUsage: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	return db, plan, user, template, codes, now
}

func TestGiftPurchaseNewPreviewMatchesGrantedRights(t *testing.T) {
	for _, period := range []string{"onetime", "monthly"} {
		t.Run(period, func(t *testing.T) {
			db, _, user, _, codes, now := purchaseFixture(t, period, 1)
			if _, err := db.db.Exec(`UPDATE users SET plan_id=NULL,expired_at=?,traffic_u=123,traffic_d=456 WHERE id=?`, now.AddDate(1, 0, 0).Unix(), user); err != nil {
				t.Fatal(err)
			}
			p, err := db.CheckGiftCard(t.Context(), user, codes[0].Code, now)
			if err != nil || p.PurchasePreview == nil || p.PurchasePreview.Renewal || p.PurchasePreview.UsedTraffic != 0 {
				t.Fatalf("preview=%+v err=%v", p, err)
			}
			if period == "monthly" && !p.PurchasePreview.ExpiresAfter.Equal(addOrderMonths(now, 1)) {
				t.Fatalf("new subscription expiry=%v", p.PurchasePreview.ExpiresAfter)
			}
			if _, err := db.RedeemGiftCard(t.Context(), RedeemGiftCardInput{UserID: user, Code: codes[0].Code}, now); err != nil {
				t.Fatal(err)
			}
			var used, quota int64
			if err := db.db.QueryRow(`SELECT traffic_u+traffic_d,transfer_enable FROM users WHERE id=?`, user).Scan(&used, &quota); err != nil {
				t.Fatal(err)
			}
			if used != p.PurchasePreview.UsedTraffic || quota != p.PurchasePreview.TransferAfter {
				t.Fatalf("actual used/quota=%d/%d preview=%+v", used, quota, p.PurchasePreview)
			}
		})
	}
}

func TestGiftPurchaseTrafficStacksSnapshotAndRetry(t *testing.T) {
	db, plan, user, template, codes, now := purchaseFixture(t, "onetime", 2)
	ctx := t.Context()
	if _, err := db.db.Exec(`UPDATE users SET plan_id=?,expired_at=NULL,transfer_enable=?,traffic_u=?,traffic_d=0 WHERE id=?`, plan.ID, 200*bytesPerGiB, 150*bytesPerGiB, user); err != nil {
		t.Fatal(err)
	}
	// Editing a product or template must not rewrite already issued rights.
	if _, err := db.db.Exec(`UPDATE plans SET transfer_enable_gib=999,name='Changed' WHERE id=?`, plan.ID); err != nil {
		t.Fatal(err)
	}
	_, err := db.UpdateGiftCardTemplate(ctx, template.ID, template.Revision, SaveGiftCardTemplateInput{Name: "Changed template", Type: GiftCardTypePurchase, Status: true, Rewards: GiftCardReward{PlanID: &plan.ID, PurchasePeriod: "monthly"}}, user, now)
	if err != nil {
		t.Fatal(err)
	}
	p, err := db.CheckGiftCard(ctx, user, codes[0].Code, now)
	if err != nil || p.PurchasePreview.TransferAfter != 400*bytesPerGiB || p.Rewards.PurchaseSnapshot.PlanName == "Changed" || p.Rewards.PurchasePeriod != "onetime" {
		t.Fatalf("preview=%+v err=%v", p, err)
	}
	first, err := db.RedeemGiftCard(ctx, RedeemGiftCardInput{UserID: user, Code: codes[0].Code}, now)
	if err != nil {
		t.Fatal(err)
	}
	again, err := db.RedeemGiftCard(ctx, RedeemGiftCardInput{UserID: user, Code: codes[0].Code}, now.Add(time.Second))
	if err != nil || first.ID != again.ID || first.OrderTradeNo == "" || first.OrderTradeNo != again.OrderTradeNo {
		t.Fatalf("retry=%+v err=%v", again, err)
	}
	if _, err = db.RedeemGiftCard(ctx, RedeemGiftCardInput{UserID: user, Code: codes[1].Code}, now); err != nil {
		t.Fatal(err)
	}
	var total, up, down int64
	var expires sql.NullInt64
	if err = db.db.QueryRow(`SELECT transfer_enable,traffic_u,traffic_d,expired_at FROM users WHERE id=?`, user).Scan(&total, &up, &down, &expires); err != nil {
		t.Fatal(err)
	}
	if total != 600*bytesPerGiB || up != 150*bytesPerGiB || down != 0 || expires.Valid {
		t.Fatalf("rights=%d/%d/%d/%v", total, up, down, expires)
	}
	order, err := db.GetUserOrder(ctx, user, first.OrderTradeNo)
	if err != nil {
		t.Fatal(err)
	}
	if order.Source != "gift_card_purchase" || order.Status != OrderStatusCompleted || order.PaidAt != nil || order.PaymentID != nil || order.TotalAmount != 0 || order.BalanceAmount != 0 || order.CommissionBalance != 0 || order.PurchaseSnapshot.TransferEnable != 200*bytesPerGiB || order.GiftCardBatchNo != codes[0].BatchNo {
		t.Fatalf("order=%+v", order)
	}
	var orders, events int
	if err = db.db.QueryRow(`SELECT (SELECT COUNT(*) FROM orders),(SELECT COUNT(*) FROM order_entitlement_events)`).Scan(&orders, &events); err != nil {
		t.Fatal(err)
	}
	if orders != 2 || events != 2 {
		t.Fatalf("orders/events=%d/%d", orders, events)
	}
}

func TestGiftPurchaseCycleNewRenewalAndExpired(t *testing.T) {
	for _, scenario := range []string{"new", "active", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			db, plan, user, _, codes, now := purchaseFixture(t, "monthly", 2)
			base := now
			if scenario != "new" {
				expiry := now.Add(-24 * time.Hour)
				if scenario == "active" {
					expiry = now.AddDate(0, 1, 0)
					base = expiry
				}
				if _, err := db.db.Exec(`UPDATE users SET plan_id=?,expired_at=?,transfer_enable=?,traffic_u=123,traffic_d=456 WHERE id=?`, plan.ID, expiry.Unix(), 300*bytesPerGiB, user); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				if _, err := db.RedeemGiftCard(t.Context(), RedeemGiftCardInput{UserID: user, Code: codes[i].Code}, now); err != nil {
					t.Fatal(err)
				}
				base = addOrderMonths(base, 1)
			}
			var expiry, total, used int64
			if err := db.db.QueryRow(`SELECT expired_at,transfer_enable,traffic_u+traffic_d FROM users WHERE id=?`, user).Scan(&expiry, &total, &used); err != nil {
				t.Fatal(err)
			}
			wantTotal, wantUsed := int64(300*bytesPerGiB), int64(579)
			if scenario == "new" {
				wantTotal, wantUsed = 200*bytesPerGiB, 0
			}
			if expiry != base.Unix() || total != wantTotal || used != wantUsed {
				t.Fatalf("rights=%d/%d/%d want=%d/%d/%d", expiry, total, used, base.Unix(), wantTotal, wantUsed)
			}
		})
	}
}

func TestGiftPurchaseRejectsMismatchPendingAndDisabledWithoutConsumption(t *testing.T) {
	for _, scenario := range []string{"plan", "mode", "pending", "disabled", "banned", "overflow"} {
		t.Run(scenario, func(t *testing.T) {
			db, plan, user, _, codes, now := purchaseFixture(t, "onetime", 1)
			var expected error
			switch scenario {
			case "plan":
				other, err := db.CreatePlan(t.Context(), SavePlanInput{Name: "Other", TransferEnableGiB: 1, Prices: PlanPrices{"monthly": 1}}, now)
				if err != nil {
					t.Fatal(err)
				}
				db.db.Exec(`UPDATE users SET plan_id=? WHERE id=?`, other.ID, user)
				expected = ErrGiftPurchasePlan
			case "mode":
				db.db.Exec(`UPDATE users SET plan_id=?,expired_at=? WHERE id=?`, plan.ID, now.Add(time.Hour).Unix(), user)
				expected = ErrGiftPurchaseMode
			case "pending":
				if _, err := db.CreateOrder(t.Context(), CreateOrderInput{UserID: user, PlanID: plan.ID, Period: "onetime"}, now); err != nil {
					t.Fatal(err)
				}
				expected = ErrGiftPurchasePending
			case "disabled":
				db.db.Exec(`UPDATE gift_card_codes SET status=2 WHERE id=?`, codes[0].ID)
				expected = ErrGiftCardUnavailable
			case "banned":
				db.db.Exec(`UPDATE users SET banned=1 WHERE id=?`, user)
				expected = ErrGiftCardCondition
			case "overflow":
				db.db.Exec(`UPDATE users SET plan_id=?,expired_at=NULL,transfer_enable=? WHERE id=?`, plan.ID, maxGiftCardTransfer, user)
				expected = ErrInvalidInput
			}
			_, err := db.RedeemGiftCard(t.Context(), RedeemGiftCardInput{UserID: user, Code: codes[0].Code}, now)
			if !errors.Is(err, expected) {
				t.Fatalf("err=%v want %v", err, expected)
			}
			var count int
			if err := db.db.QueryRow(`SELECT usage_count FROM gift_card_codes WHERE id=?`, codes[0].ID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatal("code consumed")
			}
		})
	}
}

func TestGiftPurchaseFailureRollsBackAllWrites(t *testing.T) {
	for _, table := range []string{"orders", "gift_card_usages", "order_entitlement_events"} {
		t.Run(table, func(t *testing.T) {
			db, _, user, _, codes, now := purchaseFixture(t, "onetime", 1)
			if _, err := db.db.Exec(`CREATE TRIGGER fail_purchase BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.RedeemGiftCard(t.Context(), RedeemGiftCardInput{UserID: user, Code: codes[0].Code}, now); err == nil {
				t.Fatal("expected failure")
			}
			var used, orders, usages, events int
			var plan sql.NullInt64
			if err := db.db.QueryRow(`SELECT (SELECT usage_count FROM gift_card_codes WHERE id=?),(SELECT COUNT(*) FROM orders),(SELECT COUNT(*) FROM gift_card_usages),(SELECT COUNT(*) FROM order_entitlement_events),plan_id FROM users WHERE id=?`, codes[0].ID, user).Scan(&used, &orders, &usages, &events, &plan); err != nil {
				t.Fatal(err)
			}
			if used+orders+usages+events != 0 || plan.Valid {
				t.Fatalf("partial commit %d/%d/%d/%d/%v", used, orders, usages, events, plan)
			}
			if _, err := db.db.Exec(`DROP TRIGGER fail_purchase`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.RedeemGiftCard(t.Context(), RedeemGiftCardInput{UserID: user, Code: codes[0].Code}, now); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGiftPurchaseConcurrentUsersAndSameUserRetries(t *testing.T) {
	db, _, user, _, codes, now := purchaseFixture(t, "onetime", 1)
	other, err := db.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "other-purchase@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	var seq int
	var name, path string
	if err := db.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	second, err := OpenSQLite("file:" + filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var wg sync.WaitGroup
	results := make(chan int64, 8)
	for i := 0; i < 8; i++ {
		id := user
		writer := db
		if i%2 == 1 {
			writer = second
			id = other.ID
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := writer.RedeemGiftCard(context.Background(), RedeemGiftCardInput{UserID: id, Code: codes[0].Code}, now)
			if err == nil {
				results <- id
			} else if !errors.Is(err, ErrGiftCardExhausted) {
				t.Errorf("redeem: %v", err)
			}
		}()
	}
	wg.Wait()
	close(results)
	var winner int64
	for id := range results {
		if winner != 0 && winner != id {
			t.Fatal("two users won")
		}
		winner = id
	}
	if winner == 0 {
		t.Fatal("no winner")
	}
	var count int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM orders WHERE gift_card_code_id=?`, codes[0].ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("orders=%d err=%v", count, err)
	}
}

func TestGiftPurchaseMigrationPreservesLegacyParentChildData(t *testing.T) {
	db := newTestStore(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, user := createOrderFixture(t, db, now, PlanPrices{"monthly": 1}, nil)
	template, err := db.CreateGiftCardTemplate(t.Context(), SaveGiftCardTemplateInput{Name: "Legacy", Type: GiftCardTypeGeneral, Status: true, Rewards: GiftCardReward{Balance: 100}}, user, now)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := db.GenerateGiftCardCodes(t.Context(), template.ID, GenerateGiftCardCodesInput{Count: 1, MaxUsage: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := db.RedeemGiftCard(t.Context(), RedeemGiftCardInput{UserID: user, Code: codes[0].Code}, now)
	if err != nil {
		t.Fatal(err)
	}
	// Restore the actual v67 constrained template column and orders shape.
	_, err = db.db.Exec(`DROP TRIGGER gift_purchase_order_guard; DROP TRIGGER gift_template_type_guard; DROP TRIGGER gift_purchase_snapshot_guard;
DROP INDEX idx_orders_gift_purchase; ALTER TABLE orders DROP COLUMN source; ALTER TABLE orders DROP COLUMN gift_card_code_id; ALTER TABLE orders DROP COLUMN purchase_snapshot_json;
DROP INDEX idx_gift_templates_active; ALTER TABLE gift_card_templates RENAME COLUMN type TO new_type;
ALTER TABLE gift_card_templates ADD COLUMN type INTEGER NOT NULL DEFAULT 1 CHECK(type BETWEEN 1 AND 3);
UPDATE gift_card_templates SET type=new_type; ALTER TABLE gift_card_templates DROP COLUMN new_type;
CREATE INDEX idx_gift_templates_active ON gift_card_templates(status,type,sort_position,id);
PRAGMA user_version=67;`)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetGiftCardUsage(t.Context(), usage.ID, user)
	if err != nil || got.Rewards.Balance != 100 || got.CodeID != codes[0].ID || got.TemplateID != template.ID {
		t.Fatalf("preserved=%+v err=%v", got, err)
	}
	rows, err := db.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key damage")
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	var version int
	if err = db.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 68 {
		t.Fatalf("version=%d err=%v", version, err)
	}
}

func TestGiftPurchaseImmutableRightsAndSingleUseGuards(t *testing.T) {
	db, plan, user, template, codes, now := purchaseFixture(t, "onetime", 1)
	if _, err := db.GenerateGiftCardCodes(t.Context(), template.ID, GenerateGiftCardCodesInput{Count: 1, MaxUsage: 2}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("multiuse: %v", err)
	}
	if _, err := db.UpdateGiftCardTemplate(t.Context(), template.ID, template.Revision, SaveGiftCardTemplateInput{Name: "Changed type", Type: GiftCardTypePlan, Status: true, Rewards: GiftCardReward{PlanID: &plan.ID}}, user, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("change type: %v", err)
	}
	for _, query := range []string{`UPDATE gift_card_codes SET max_usage=2`, `UPDATE gift_card_codes SET metadata_json='{}'`, `UPDATE gift_card_codes SET code='REPLACEMENTCODE'`} {
		if _, err := db.db.Exec(query+` WHERE id=?`, codes[0].ID); err == nil {
			t.Fatalf("accepted %s", query)
		}
	}
	code, err := db.GetGiftCardCode(t.Context(), codes[0].ID)
	if err != nil || code.PurchaseSnapshot == nil {
		t.Fatalf("code snapshot %v", err)
	}
	if _, err := db.UpdateGiftCardCode(t.Context(), code.ID, SaveGiftCardCodeInput{Code: code.Code, Status: GiftCardCodeDisabled, MaxUsage: 1}, now); err != nil {
		t.Fatal(err)
	}
}

func TestGiftPurchaseDoesNotConsumeCommissionFirstOrderOrSurplus(t *testing.T) {
	db, _, user, _, codes, now := purchaseFixture(t, "onetime", 1)
	if _, err := db.RedeemGiftCard(t.Context(), RedeemGiftCardInput{UserID: user, Code: codes[0].Code}, now); err != nil {
		t.Fatal(err)
	}
	inviter, err := db.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "purchase-inviter@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.Exec(`UPDATE users SET invite_user_id=? WHERE id=?`, inviter.ID, user); err != nil {
		t.Fatal(err)
	}
	tx, err := db.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	u, err := readOrderUser(t.Context(), tx, user)
	if err != nil {
		t.Fatal(err)
	}
	order := Order{TotalAmount: 1000}
	if err = setOrderCommission(t.Context(), tx, u, orderSettings{commissionFirstTime: true, inviteCommissionPercent: 10}, &order); err != nil {
		t.Fatal(err)
	}
	if order.CommissionBalance != 100 {
		t.Fatalf("first commission=%d", order.CommissionBalance)
	}
	ids, err := listSurplusOrderIDs(t.Context(), tx, user)
	if err != nil || len(ids) != 0 {
		t.Fatalf("surplus IDs=%v err=%v", ids, err)
	}
	if err = calculateOrderSurplus(t.Context(), tx, u, &order, false, now); !errors.Is(err, ErrGiftPurchaseConversion) || order.SurplusAmount != 0 {
		t.Fatalf("surplus=%d err=%v", order.SurplusAmount, err)
	}
}

func TestGiftPurchaseMixedTrafficCannotMintUpgradeCredit(t *testing.T) {
	db, plan, user, _, codes, now := purchaseFixture(t, "onetime", 1)
	paid, err := db.CreateOrder(t.Context(), CreateOrderInput{UserID: user, PlanID: plan.ID, Period: "onetime"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.CompleteOrder(t.Context(), paid.TradeNo, "fixture-payment", now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.Exec(`UPDATE users SET traffic_u=? WHERE id=?`, 150*bytesPerGiB, user); err != nil {
		t.Fatal(err)
	}
	if _, err = db.RedeemGiftCard(t.Context(), RedeemGiftCardInput{UserID: user, Code: codes[0].Code}, now); err != nil {
		t.Fatal(err)
	}
	tx, err := db.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	u, err := readOrderUser(t.Context(), tx, user)
	if err != nil {
		t.Fatal(err)
	}
	order := Order{}
	if err = calculateOrderSurplus(t.Context(), tx, u, &order, false, now); !errors.Is(err, ErrGiftPurchaseConversion) || order.SurplusAmount != 0 {
		t.Fatalf("converted external rights: %v %+v", err, order)
	}
}

func TestGiftPurchaseCycleRetainsOrRestoresResetSchedule(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		t.Run(fmt.Sprint(scheduled), func(t *testing.T) {
			db, plan, user, _, codes, now := purchaseFixture(t, "monthly", 1)
			var old any
			if scheduled {
				old = now.Add(time.Hour).Unix()
			}
			if _, err := db.db.Exec(`UPDATE users SET plan_id=?,expired_at=?,next_reset_at=?,traffic_u=123 WHERE id=?`, plan.ID, now.Add(-time.Hour).Unix(), old, user); err != nil {
				t.Fatal(err)
			}
			if _, err := db.RedeemGiftCard(t.Context(), RedeemGiftCardInput{UserID: user, Code: codes[0].Code}, now); err != nil {
				t.Fatal(err)
			}
			var next sql.NullInt64
			var used int64
			if err := db.db.QueryRow(`SELECT next_reset_at,traffic_u FROM users WHERE id=?`, user).Scan(&next, &used); err != nil {
				t.Fatal(err)
			}
			if !next.Valid || used != 123 || scheduled && next.Int64 != now.Add(time.Hour).Unix() {
				t.Fatalf("reset=%v used=%d", next, used)
			}
		})
	}
}

func TestGiftPurchaseIssuedSnapshotProtectsReferencedPlanAndGroup(t *testing.T) {
	db, plan, user, _, _, _ := purchaseFixture(t, "onetime", 1)
	if err := db.DeletePlan(t.Context(), plan.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted issued plan: %v", err)
	}
	if _, err := db.db.Exec(`UPDATE users SET group_id=NULL WHERE id=?`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE plans SET group_id=NULL WHERE id=?`, plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteServerGroup(t.Context(), *plan.GroupID); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted issued group: %v", err)
	}
}
