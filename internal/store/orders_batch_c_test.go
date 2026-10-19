package store

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"testing"
	"time"
)

func TestBL04AssignOrderExpiredCrossPlanResetsTraffic(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	setupFixtures := func(t *testing.T) (*Store, Plan, Plan, int64) {
		t.Helper()
		db := newTestStore(t)
		ctx := t.Context()
		planA, userID := createOrderFixture(t, db, now, PlanPrices{"monthly": 1000, "onetime": 3000}, nil)
		groupB, err := db.CreateServerGroup(ctx, "Group B", now)
		if err != nil {
			t.Fatal(err)
		}
		planB, err := db.CreatePlan(ctx, SavePlanInput{
			Name:              "Plan B",
			GroupID:           &groupB.ID,
			TransferEnableGiB: 50,
			Prices:            PlanPrices{"monthly": 2000, "onetime": 4000},
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		planB, err = db.SetPlanState(ctx, planB.ID, planB.Revision, PlanState{Show: true, Sell: true, Renew: true}, now)
		if err != nil {
			t.Fatal(err)
		}
		return db, planA, planB, userID
	}

	t.Run("expired_cross_plan_becomes_new_and_resets_traffic", func(t *testing.T) {
		db, planA, planB, userID := setupFixtures(t)
		ctx := t.Context()
		expiredAt := now.Add(-24 * time.Hour).Unix()
		if _, err := db.db.ExecContext(ctx, `
			UPDATE users SET plan_id=?, expired_at=?, transfer_enable=?, traffic_u=?, traffic_d=? WHERE id=?
		`, planA.ID, expiredAt, 100*bytesPerGiB, 45*bytesPerGiB, 35*bytesPerGiB, userID); err != nil {
			t.Fatal(err)
		}
		assigned, err := db.AssignOrder(ctx, AssignOrderInput{
			UserID:      &userID,
			PlanID:      planB.ID,
			Period:      "monthly",
			TotalAmount: 2000,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		// Deliberately deviates from legacy PHP parity (D-4): assigning a different plan
		// to an expired user must be classified as OrderTypeNew so old exhausted usage resets.
		if assigned.Type != OrderTypeNew {
			t.Fatalf("assigned.Type = %v, want %v (OrderTypeNew)", assigned.Type, OrderTypeNew)
		}
		if _, err := db.CompleteOrder(ctx, assigned.TradeNo, "BL04-EXP-CROSS", now); err != nil {
			t.Fatal(err)
		}
		var u, d, transfer int64
		var gotExpiry sql.NullInt64
		if err := db.db.QueryRowContext(ctx, `SELECT traffic_u, traffic_d, transfer_enable, expired_at FROM users WHERE id=?`, userID).Scan(&u, &d, &transfer, &gotExpiry); err != nil {
			t.Fatal(err)
		}
		wantExpiry := addOrderMonths(now, 1).Unix()
		if u != 0 || d != 0 || transfer != 50*bytesPerGiB || !gotExpiry.Valid || gotExpiry.Int64 != wantExpiry {
			t.Fatalf("after completion u=%d d=%d transfer=%d expiry=%v, want 0/0/%d/%d", u, d, transfer, gotExpiry, 50*bytesPerGiB, wantExpiry)
		}
	})

	t.Run("active_cross_plan_remains_upgrade_and_preserves_usage_when_event_0", func(t *testing.T) {
		db, planA, planB, userID := setupFixtures(t)
		ctx := t.Context()
		activeExpiry := now.Add(10 * 24 * time.Hour).Unix()
		if _, err := db.db.ExecContext(ctx, `
			UPDATE users SET plan_id=?, expired_at=?, transfer_enable=?, traffic_u=?, traffic_d=? WHERE id=?
		`, planA.ID, activeExpiry, 100*bytesPerGiB, 10*bytesPerGiB, 15*bytesPerGiB, userID); err != nil {
			t.Fatal(err)
		}
		assigned, err := db.AssignOrder(ctx, AssignOrderInput{
			UserID:      &userID,
			PlanID:      planB.ID,
			Period:      "monthly",
			TotalAmount: 2000,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		if assigned.Type != OrderTypeUpgrade {
			t.Fatalf("assigned.Type = %v, want %v (OrderTypeUpgrade)", assigned.Type, OrderTypeUpgrade)
		}
		if _, err := db.CompleteOrder(ctx, assigned.TradeNo, "BL04-ACT-CROSS", now); err != nil {
			t.Fatal(err)
		}
		var u, d int64
		if err := db.db.QueryRowContext(ctx, `SELECT traffic_u, traffic_d FROM users WHERE id=?`, userID).Scan(&u, &d); err != nil {
			t.Fatal(err)
		}
		if u != 10*bytesPerGiB || d != 15*bytesPerGiB {
			t.Fatalf("after upgrade completion u=%d d=%d, want %d/%d", u, d, 10*bytesPerGiB, 15*bytesPerGiB)
		}
	})

	t.Run("permanent_cross_plan_is_upgrade_and_expired_same_plan_is_new", func(t *testing.T) {
		db, planA, planB, userID := setupFixtures(t)
		ctx := t.Context()
		if _, err := db.db.ExecContext(ctx, `
			UPDATE users SET plan_id=?, expired_at=NULL, transfer_enable=?, traffic_u=?, traffic_d=? WHERE id=?
		`, planA.ID, 100*bytesPerGiB, 5*bytesPerGiB, 5*bytesPerGiB, userID); err != nil {
			t.Fatal(err)
		}
		permAssigned, err := db.AssignOrder(ctx, AssignOrderInput{
			UserID:      &userID,
			PlanID:      planB.ID,
			Period:      "monthly",
			TotalAmount: 2000,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		if permAssigned.Type != OrderTypeUpgrade {
			t.Fatalf("permanent cross-plan assigned.Type = %v, want OrderTypeUpgrade", permAssigned.Type)
		}
		if _, err := db.CancelAdminOrder(ctx, permAssigned.TradeNo, now); err != nil {
			t.Fatal(err)
		}

		expiredAt := now.Add(-24 * time.Hour).Unix()
		if _, err := db.db.ExecContext(ctx, `UPDATE users SET plan_id=?, expired_at=? WHERE id=?`, planA.ID, expiredAt, userID); err != nil {
			t.Fatal(err)
		}
		samePlanAssigned, err := db.AssignOrder(ctx, AssignOrderInput{
			UserID:      &userID,
			PlanID:      planA.ID,
			Period:      "monthly",
			TotalAmount: 1000,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		if samePlanAssigned.Type != OrderTypeNew {
			t.Fatalf("expired same-plan assigned.Type = %v, want OrderTypeNew", samePlanAssigned.Type)
		}
	})
}

func TestBL05ListDistributorOrdersClosesRowsOnScanError(t *testing.T) {
	db := newTestStore(t)
	db.db.SetMaxOpenConns(1)
	ctx := context.Background()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	plan, dealer := createDistributorFixture(t, db, now)
	created, err := db.CreateDistributorOrder(ctx, CreateDistributorOrderInput{
		DistributorUserID: dealer.ID,
		PlanID:            plan.ID,
		Period:            "monthly",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE orders SET surplus_order_ids_json = '["x"]' WHERE id = ?`, created.Order.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ListDistributorOrders(ctx, DistributorOrderFilter{
		DistributorUserID: &dealer.ID,
		Page:              1,
		PageSize:          20,
	}, now); err == nil {
		t.Fatal("ListDistributorOrders() succeeded on malformed surplus_order_ids_json, want error")
	}

	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var one int
	if err := db.db.QueryRowContext(probeCtx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("subsequent query after scan failure: one=%d err=%v (connection leaked)", one, err)
	}
}

func TestBL06TrafficAwareSurplusMonthAccumulationOverflow(t *testing.T) {
	ctx := t.Context()
	baseNow := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name        string
		gib         int64
		totalMonths int
		upload      int64
		download    int64
	}{
		{
			name:        "standard_plan_regression",
			gib:         100,
			totalMonths: 12,
			upload:      200 * bytesPerGiB,
			download:    100 * bytesPerGiB,
		},
		{
			name:        "max_gib_months_1024_no_overflow",
			gib:         8_388_607,
			totalMonths: 1024,
			upload:      3_000_000_000_000_000,
			download:    2_000_000_000_000_000,
		},
		{
			name:        "max_gib_months_1025_overflow_boundary",
			gib:         8_388_607,
			totalMonths: 1025,
			upload:      3_000_000_000_000_000,
			download:    2_000_000_000_000_000,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestStore(t)
			enableOrderSurplusSettings(t, db, baseNow, true)

			group, err := db.CreateServerGroup(ctx, "MaxQuotaGroup", baseNow)
			if err != nil {
				t.Fatal(err)
			}
			planA, err := db.CreatePlan(ctx, SavePlanInput{
				Name:              "PlanA",
				GroupID:           &group.ID,
				TransferEnableGiB: tc.gib,
				Prices:            PlanPrices{"monthly": 10_000, "yearly": 120_000, "three_yearly": 360_000},
			}, baseNow)
			if err != nil {
				t.Fatal(err)
			}
			planA, err = db.SetPlanState(ctx, planA.ID, planA.Revision, PlanState{Show: true, Sell: true, Renew: true}, baseNow)
			if err != nil {
				t.Fatal(err)
			}
			planB, err := db.CreatePlan(ctx, SavePlanInput{
				Name:              "TargetPlanB",
				GroupID:           &group.ID,
				TransferEnableGiB: 100,
				Prices:            PlanPrices{"monthly": 50_000_000},
			}, baseNow)
			if err != nil {
				t.Fatal(err)
			}
			planB, err = db.SetPlanState(ctx, planB.ID, planB.Revision, PlanState{Show: true, Sell: true, Renew: true}, baseNow)
			if err != nil {
				t.Fatal(err)
			}

			_, userID := createOrderFixture(t, db, baseNow, PlanPrices{"monthly": 1000}, nil)
			firstCreated := baseNow.Add(-24 * time.Hour).Unix()
			expiry := addOrderMonths(time.Unix(firstCreated, 0), tc.totalMonths).Unix()

			var amountSum int64
			orderSeq := 0
			insertPaidRecurring := func(period string, amount int64) {
				orderSeq++
				tradeNo := fmt.Sprintf("20261016120000999%04d%04d", tc.totalMonths, orderSeq)
				if _, err := db.db.ExecContext(ctx, `
					INSERT INTO orders (
						user_id, plan_id, period, trade_no, original_amount, total_amount,
						type, status, source, commission_status, created_at, updated_at
					) VALUES (?, ?, ?, ?, ?, ?, 2, 3, 'online', 0, ?, ?)
				`, userID, planA.ID, period, tradeNo, amount, amount, firstCreated, firstCreated); err != nil {
					t.Fatal(err)
				}
				amountSum += amount
			}
			remainingMonths := tc.totalMonths
			for remainingMonths >= 36 {
				insertPaidRecurring("three_yearly", 360_000)
				remainingMonths -= 36
			}
			for remainingMonths >= 12 {
				insertPaidRecurring("yearly", 120_000)
				remainingMonths -= 12
			}
			for remainingMonths > 0 {
				insertPaidRecurring("monthly", 10_000)
				remainingMonths--
			}

			if _, err := db.db.ExecContext(ctx, `
				UPDATE users SET plan_id=?, group_id=?, expired_at=?, transfer_enable=?, traffic_u=?, traffic_d=? WHERE id=?
			`, planA.ID, group.ID, expiry, tc.gib*bytesPerGiB, tc.upload, tc.download, userID); err != nil {
				t.Fatal(err)
			}

			upgradeOrder, err := db.CreateOrder(ctx, CreateOrderInput{
				UserID: userID,
				PlanID: planB.ID,
				Period: "monthly",
			}, baseNow)
			if err != nil {
				t.Fatal(err)
			}

			// Independently compute expected surplus using math/big.Rat without calling production helpers.
			totalTrafficBig := new(big.Int).Mul(big.NewInt(tc.gib), big.NewInt(int64(tc.totalMonths)))
			totalTrafficBig.Mul(totalTrafficBig, big.NewInt(bytesPerGiB))
			usedBig := new(big.Int).Add(big.NewInt(tc.upload), big.NewInt(tc.download))
			remTrafficBig := new(big.Int).Sub(totalTrafficBig, usedBig)

			rat := new(big.Rat).SetFrac(remTrafficBig, totalTrafficBig)
			rat.Mul(rat, new(big.Rat).SetInt64(amountSum))
			wantSurplus := new(big.Int).Quo(rat.Num(), rat.Denom()).Int64()

			if upgradeOrder.SurplusAmount != wantSurplus {
				t.Fatalf("months=%d upgradeOrder.SurplusAmount = %d, want %d", tc.totalMonths, upgradeOrder.SurplusAmount, wantSurplus)
			}
		})
	}
}

func TestBL07PermanentOnetimeChainedUpgradePreservesSurplusValue(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name               string
		planBPrice         int64
		planBUsedGiB       int64
		wantOrderBTotal    int64
		wantOrderBCredit   int64
		wantNextSurplus    int64
		wantConservedTotal int64
	}{
		{
			name:               "zero_usage_preserves_full_20000_value",
			planBPrice:         20_000,
			planBUsedGiB:       0,
			wantOrderBTotal:    10_000,
			wantOrderBCredit:   0,
			wantNextSurplus:    20_000,
			wantConservedTotal: 20_000, // 10,000 (A) + 10,000 (B cash) = 20,000 total paid, 0% consumed -> 20,000 remaining
		},
		{
			name:               "half_usage_preserves_10000_value",
			planBPrice:         20_000,
			planBUsedGiB:       100, // 100 GiB of 200 GiB = 50%
			wantOrderBTotal:    10_000,
			wantOrderBCredit:   0,
			wantNextSurplus:    10_000,
			wantConservedTotal: 10_000, // 20,000 total paid, 50% consumed -> 10,000 remaining
		},
		{
			name:               "surplus_exceeds_plan_b_price_produces_surplus_credit_and_next_hop_uses_net_value",
			planBPrice:         6_000,
			planBUsedGiB:       0,
			wantOrderBTotal:    0,
			wantOrderBCredit:   4_000,
			wantNextSurplus:    6_000,  // 0 paid + 10_000 surplus_amount - 4_000 surplus_credit = 6_000 net value
			wantConservedTotal: 10_000, // 4,000 refunded to user.balance + 6,000 orderC.SurplusAmount = 10,000 initial value conserved
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestStore(t)
			enableOrderSurplusSettings(t, db, now, true)

			planA, userID := createOrderFixture(t, db, now, PlanPrices{"onetime": 10_000}, nil)
			if _, err := db.db.ExecContext(ctx, `UPDATE plans SET transfer_enable_gib = 100 WHERE id = ?`, planA.ID); err != nil {
				t.Fatal(err)
			}
			planB, err := db.CreatePlan(ctx, SavePlanInput{
				Name:              "Permanent B",
				GroupID:           planA.GroupID,
				TransferEnableGiB: 200,
				Prices:            PlanPrices{"onetime": tc.planBPrice},
			}, now)
			if err != nil {
				t.Fatal(err)
			}
			planB, err = db.SetPlanState(ctx, planB.ID, planB.Revision, PlanState{Show: true, Sell: true, Renew: true}, now)
			if err != nil {
				t.Fatal(err)
			}
			planC, err := db.CreatePlan(ctx, SavePlanInput{
				Name:              "Permanent C",
				GroupID:           planA.GroupID,
				TransferEnableGiB: 400,
				Prices:            PlanPrices{"onetime": 50_000},
			}, now)
			if err != nil {
				t.Fatal(err)
			}
			planC, err = db.SetPlanState(ctx, planC.ID, planC.Revision, PlanState{Show: true, Sell: true, Renew: true}, now)
			if err != nil {
				t.Fatal(err)
			}

			// Step 1: Buy Plan A onetime (10,000 cents), 0 usage -> remaining value = 10,000 cents.
			orderA, err := db.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: planA.ID, Period: "onetime"}, now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.CompleteOrder(ctx, orderA.TradeNo, "BL07-A", now); err != nil {
				t.Fatal(err)
			}

			// Step 2: Upgrade from Plan A to Plan B onetime.
			orderB, err := db.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: planB.ID, Period: "onetime"}, now.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if orderB.SurplusAmount != 10_000 || orderB.TotalAmount != tc.wantOrderBTotal || orderB.SurplusCredit != tc.wantOrderBCredit {
				t.Fatalf("orderB surplus=%d total=%d credit=%d, want 10000/%d/%d",
					orderB.SurplusAmount, orderB.TotalAmount, orderB.SurplusCredit, tc.wantOrderBTotal, tc.wantOrderBCredit)
			}
			if _, err := db.CompleteOrder(ctx, orderB.TradeNo, "BL07-B", now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}

			var balanceBeforeC int64
			if err := db.db.QueryRowContext(ctx, `SELECT balance FROM users WHERE id = ?`, userID).Scan(&balanceBeforeC); err != nil {
				t.Fatal(err)
			}
			if balanceBeforeC != tc.wantOrderBCredit {
				t.Fatalf("balanceBeforeC = %d, want %d", balanceBeforeC, tc.wantOrderBCredit)
			}

			if tc.planBUsedGiB > 0 {
				if _, err := db.db.ExecContext(ctx, `UPDATE users SET traffic_u = ? WHERE id = ?`, tc.planBUsedGiB*bytesPerGiB, userID); err != nil {
					t.Fatal(err)
				}
			}

			// Step 3: Upgrade from Plan B to Plan C onetime.
			// Deliberately deviates from legacy PHP parity (D-4): chained onetime upgrades
			// must include net surplus (surplus_amount - surplus_credit) from the prior upgrade.
			orderC, err := db.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: planC.ID, Period: "onetime"}, now.Add(2*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if orderC.SurplusAmount != tc.wantNextSurplus {
				t.Fatalf("orderC.SurplusAmount = %d, want %d", orderC.SurplusAmount, tc.wantNextSurplus)
			}
			if conserved := balanceBeforeC + orderC.SurplusAmount; conserved != tc.wantConservedTotal {
				t.Fatalf("conserved value (balanceBeforeC + orderC.SurplusAmount) = %d, want %d", conserved, tc.wantConservedTotal)
			}
		})
	}
}

func TestBL30DistributorRenewalDoesNotInheritSettledDisplay(t *testing.T) {
	db := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	plan, dealer := createDistributorFixture(t, db, now)
	admin, err := db.CreateAdminUser(ctx, CreateAdminUserInput{
		Email:        "bl30-admin@example.test",
		PasswordHash: "hash",
		IsAdmin:      true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	root, err := db.CreateDistributorOrder(ctx, CreateDistributorOrderInput{
		DistributorUserID: dealer.ID,
		PlanID:            plan.ID,
		Period:            "monthly",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	settled, err := db.SettleDistributorOrders(ctx, dealer.ID, admin.ID, now.Add(time.Minute))
	if err != nil || settled.Count != 1 {
		t.Fatalf("SettleDistributorOrders() = %+v, err = %v", settled, err)
	}

	renewed, err := db.RenewDistributorOrder(ctx, RenewDistributorOrderInput{
		DistributorUserID: dealer.ID,
		TradeNo:           root.Order.TradeNo,
		Period:            "monthly",
		IdempotencyKey:    "11111111-2222-4333-8444-555555555555", // gitleaks:allow -- deterministic test UUID
	}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	page, err := db.ListDistributorOrders(ctx, DistributorOrderFilter{
		DistributorUserID: &dealer.ID,
		Page:              1,
		PageSize:          20,
	}, now.Add(3*time.Minute))
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("ListDistributorOrders() len=%d err=%v", len(page.Items), err)
	}

	var gotRoot, gotRenewed *DistributorOrder
	for i := range page.Items {
		switch page.Items[i].Order.ID {
		case root.Order.ID:
			gotRoot = &page.Items[i]
		case renewed.Order.ID:
			gotRenewed = &page.Items[i]
		}
	}
	if gotRoot == nil || gotRenewed == nil {
		t.Fatalf("missing root or renewed order in page: %+v", page.Items)
	}
	if gotRoot.SettlementStatus != DistributorSettlementSettled {
		t.Fatalf("root.SettlementStatus = %v, want Settled", gotRoot.SettlementStatus)
	}
	if gotRenewed.SettlementStatus != DistributorSettlementUnsettled {
		t.Fatalf("renewed.SettlementStatus = %v, want Unsettled (Order.PaidAt=%v)", gotRenewed.SettlementStatus, gotRenewed.Order.PaidAt)
	}

	unsettledStatus := DistributorSettlementUnsettled
	unsettledPage, err := db.ListDistributorOrders(ctx, DistributorOrderFilter{
		DistributorUserID: &dealer.ID,
		SettlementStatus:  &unsettledStatus,
		Page:              1,
		PageSize:          20,
	}, now.Add(3*time.Minute))
	if err != nil || len(unsettledPage.Items) != 1 || unsettledPage.Items[0].Order.ID != renewed.Order.ID || unsettledPage.Items[0].SettlementStatus != DistributorSettlementUnsettled {
		t.Fatalf("unsettledPage = %+v, err = %v", unsettledPage, err)
	}

	preview, err := db.PreviewDistributorSettlement(ctx, dealer.ID)
	if err != nil || preview.Count != 1 || preview.TotalAmount != renewed.Order.TotalAmount {
		t.Fatalf("PreviewDistributorSettlement() = %+v, err = %v", preview, err)
	}
}

func enableOrderSurplusSettings(t *testing.T, db *Store, now time.Time, trafficAware bool) {
	t.Helper()
	changeEvent := 0
	if trafficAware {
		changeEvent = 1
	}
	if _, err := db.db.Exec(`
		UPDATE app_settings
		SET plan_change_enable = 1, surplus_enable = 1, change_order_event_id = ?, updated_at = ?
		WHERE id = 1
	`, changeEvent, now.Unix()); err != nil {
		t.Fatal(err)
	}
}
