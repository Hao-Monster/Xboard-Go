package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBL17CommissionPoisonOrderIsolation(t *testing.T) {
	t.Run("cyclic_inviter_quarantined_healthy_orders_paid", func(t *testing.T) {
		database := newTestStore(t)
		ctx := t.Context()
		now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)

		plan, buyer1 := createOrderFixture(t, database, now, PlanPrices{"monthly": 10_000}, nil)
		buyerPoison, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "bl17-buyer-poison@example.test", PasswordHash: "hash"}, now)
		if err != nil {
			t.Fatal(err)
		}
		buyer2, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "bl17-buyer2@example.test", PasswordHash: "hash"}, now)
		if err != nil {
			t.Fatal(err)
		}

		inviterHealthy1, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "bl17-inv1@example.test", PasswordHash: "hash"}, now)
		if err != nil {
			t.Fatal(err)
		}
		inviterHealthy2, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "bl17-inv2@example.test", PasswordHash: "hash"}, now)
		if err != nil {
			t.Fatal(err)
		}
		cycleA, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "bl17-cycle-a@example.test", PasswordHash: "hash"}, now)
		if err != nil {
			t.Fatal(err)
		}
		cycleB, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "bl17-cycle-b@example.test", PasswordHash: "hash"}, now)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := database.db.ExecContext(ctx, `
			UPDATE app_settings SET commission_distribution_enable = 1,
				commission_distribution_l1 = 50, commission_distribution_l2 = 30, commission_distribution_l3 = 20,
				withdraw_close_enable = 0 WHERE id = 1
		`); err != nil {
			t.Fatal(err)
		}
		for _, pair := range []struct{ child, parent int64 }{
			{buyer1, inviterHealthy1.ID},
			{buyerPoison.ID, cycleA.ID},
			{cycleA.ID, cycleB.ID},
			{cycleB.ID, cycleA.ID},
			{buyer2.ID, inviterHealthy2.ID},
		} {
			if _, err := database.db.ExecContext(ctx, `UPDATE users SET invite_user_id = ? WHERE id = ?`, pair.parent, pair.child); err != nil {
				t.Fatal(err)
			}
		}
		for _, invID := range []int64{inviterHealthy1.ID, inviterHealthy2.ID, cycleA.ID, cycleB.ID} {
			if _, err := database.db.ExecContext(ctx, `UPDATE users SET commission_type = 1, commission_rate = 20 WHERE id = ?`, invID); err != nil {
				t.Fatal(err)
			}
		}

		createCompleted := func(userID int64, offset time.Duration, cb string) Order {
			at := now.Add(offset)
			ord, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, at)
			if err != nil {
				t.Fatal(err)
			}
			completed, err := database.CompleteOrder(ctx, ord.TradeNo, cb, at)
			if err != nil {
				t.Fatal(err)
			}
			return completed
		}

		order1 := createCompleted(buyer1, 0, "bl17-cb-1")
		orderPoison := createCompleted(buyerPoison.ID, time.Second, "bl17-cb-poison")
		order2 := createCompleted(buyer2.ID, 2*time.Second, "bl17-cb-2")

		maturedAt := now.Add(2*time.Second + commissionConfirmationDelay)
		res, err := database.ProcessCommissions(ctx, maturedAt, 100)
		if err != nil {
			t.Fatalf("ProcessCommissions() unexpected error = %v, want poison order isolated", err)
		}
		if res.Checked != 3 || res.Paid != 2 || res.Quarantined != 1 || res.Remaining != 0 {
			t.Fatalf("ProcessCommissions() = %#v, want Checked=3 Paid=2 Quarantined=1 Remaining=0", res)
		}

		assertCommissionState := func(inviterID, orderID, wantBalance int64, wantStatus, wantLogs int) {
			t.Helper()
			var bal int64
			var status, logs int
			if err := database.db.QueryRowContext(ctx, `
				SELECT u.commission_balance, o.commission_status,
				       (SELECT COUNT(*) FROM commission_logs WHERE order_id = o.id)
				FROM users u CROSS JOIN orders o WHERE u.id = ? AND o.id = ?
			`, inviterID, orderID).Scan(&bal, &status, &logs); err != nil {
				t.Fatal(err)
			}
			if bal != wantBalance || status != wantStatus || logs != wantLogs {
				t.Fatalf("inviter=%d order=%d: balance=%d status=%d logs=%d, want %d/%d/%d",
					inviterID, orderID, bal, status, logs, wantBalance, wantStatus, wantLogs)
			}
		}

		// Order 1 and Order 2 commission is 20% of 10000 = 2000; L1 gets 50% = 1000.
		assertCommissionState(inviterHealthy1.ID, order1.ID, 1_000, 2, 1)
		assertCommissionState(inviterHealthy2.ID, order2.ID, 1_000, 2, 1)
		// Poison order must have rolled back L1 and L2 partial credits via savepoint!
		assertCommissionState(cycleA.ID, orderPoison.ID, 0, 1, 0)
		assertCommissionState(cycleB.ID, orderPoison.ID, 0, 1, 0)

		var reason string
		var attempts int
		var firstFailedAt, lastFailedAt, retryAfter int64
		if err := database.db.QueryRowContext(ctx, `
			SELECT reason, attempts, first_failed_at, last_failed_at, retry_after
			FROM scheduler_quarantine
			WHERE kind = 'commission' AND entity_id = ?
		`, orderPoison.ID).Scan(&reason, &attempts, &firstFailedAt, &lastFailedAt, &retryAfter); err != nil {
			t.Fatalf("read quarantine row: %v", err)
		}
		if reason != "cyclic_invitation_relationship" || attempts != 1 || firstFailedAt != maturedAt.Unix() || lastFailedAt != maturedAt.Unix() || retryAfter != maturedAt.Add(time.Hour).Unix() {
			t.Fatalf("quarantine = (%q, attempts=%d, first=%d, last=%d, retry=%d), want (cyclic_invitation_relationship, 1, %d, %d, %d)",
				reason, attempts, firstFailedAt, lastFailedAt, retryAfter, maturedAt.Unix(), maturedAt.Unix(), maturedAt.Add(time.Hour).Unix())
		}

		// Re-run while quarantined: skipped, no re-payout, Remaining=0.
		again, err := database.ProcessCommissions(ctx, maturedAt.Add(10*time.Minute), 100)
		if err != nil || again.Paid != 0 || again.Quarantined != 0 || again.Remaining != 0 {
			t.Fatalf("ProcessCommissions(within quarantine) = (%#v, %v)", again, err)
		}

		// Re-run after retry_after while still broken: attempts increments to 2, first_failed_at preserved.
		secondFailAt := maturedAt.Add(time.Hour + time.Second)
		secondTry, err := database.ProcessCommissions(ctx, secondFailAt, 100)
		if err != nil || secondTry.Paid != 0 || secondTry.Quarantined != 1 || secondTry.Remaining != 0 {
			t.Fatalf("ProcessCommissions(second failure after retry_after) = (%#v, %v)", secondTry, err)
		}
		if err := database.db.QueryRowContext(ctx, `
			SELECT attempts, first_failed_at, last_failed_at, retry_after
			FROM scheduler_quarantine
			WHERE kind = 'commission' AND entity_id = ?
		`, orderPoison.ID).Scan(&attempts, &firstFailedAt, &lastFailedAt, &retryAfter); err != nil {
			t.Fatalf("read quarantine row after second attempt: %v", err)
		}
		if attempts != 2 || firstFailedAt != maturedAt.Unix() || lastFailedAt != secondFailAt.Unix() || retryAfter != secondFailAt.Add(time.Hour).Unix() {
			t.Fatalf("second quarantine = (attempts=%d, first=%d, last=%d, retry=%d)", attempts, firstFailedAt, lastFailedAt, retryAfter)
		}

		// Break the cycle and advance clock past retry_after (NO manual DELETE from scheduler_quarantine).
		if _, err := database.db.ExecContext(ctx, `UPDATE users SET invite_user_id = NULL WHERE id = ?`, cycleB.ID); err != nil {
			t.Fatal(err)
		}
		recovered, err := database.ProcessCommissions(ctx, secondFailAt.Add(time.Hour+time.Second), 100)
		if err != nil || recovered.Paid != 1 || recovered.Quarantined != 0 || recovered.Remaining != 0 {
			t.Fatalf("ProcessCommissions(after fix) = (%#v, %v), want Paid=1 Quarantined=0 Remaining=0", recovered, err)
		}
		assertCommissionState(cycleA.ID, orderPoison.ID, 1_000, 2, 2)
		assertCommissionState(cycleB.ID, orderPoison.ID, 600, 2, 2)

		var quarantineCount int
		if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduler_quarantine WHERE kind = 'commission' AND entity_id = ?`, orderPoison.ID).Scan(&quarantineCount); err != nil || quarantineCount != 0 {
			t.Fatalf("quarantine count after recovery = %d, err=%v, want 0", quarantineCount, err)
		}
	})

	t.Run("balance_limit_exceeded_quarantined_and_l1_rolled_back", func(t *testing.T) {
		database := newTestStore(t)
		ctx := t.Context()
		now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

		plan, buyerPoison := createOrderFixture(t, database, now, PlanPrices{"monthly": 10_000}, nil)
		buyerHealthy, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "bl17-limit-healthy@example.test", PasswordHash: "hash"}, now)
		if err != nil {
			t.Fatal(err)
		}
		l1, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "bl17-limit-l1@example.test", PasswordHash: "hash"}, now)
		if err != nil {
			t.Fatal(err)
		}
		l2, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "bl17-limit-l2@example.test", PasswordHash: "hash"}, now)
		if err != nil {
			t.Fatal(err)
		}
		healthyInviter, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "bl17-limit-inv@example.test", PasswordHash: "hash"}, now)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := database.db.ExecContext(ctx, `
			UPDATE app_settings SET commission_distribution_enable = 1,
				commission_distribution_l1 = 50, commission_distribution_l2 = 50, commission_distribution_l3 = 0,
				withdraw_close_enable = 0 WHERE id = 1
		`); err != nil {
			t.Fatal(err)
		}
		for _, pair := range []struct{ child, parent int64 }{
			{buyerPoison, l1.ID},
			{l1.ID, l2.ID},
			{buyerHealthy.ID, healthyInviter.ID},
		} {
			if _, err := database.db.ExecContext(ctx, `UPDATE users SET invite_user_id = ? WHERE id = ?`, pair.parent, pair.child); err != nil {
				t.Fatal(err)
			}
		}
		for _, invID := range []int64{l1.ID, l2.ID, healthyInviter.ID} {
			if _, err := database.db.ExecContext(ctx, `UPDATE users SET commission_type = 1, commission_rate = 20 WHERE id = ?`, invID); err != nil {
				t.Fatal(err)
			}
		}
		// Set L2 commission_balance at maxOrderMoneyCents so L1 succeeds inside savepoint and L2 fails.
		if _, err := database.db.ExecContext(ctx, `UPDATE users SET commission_balance = ? WHERE id = ?`, maxOrderMoneyCents, l2.ID); err != nil {
			t.Fatal(err)
		}

		ordPoison, err := database.CreateOrder(ctx, CreateOrderInput{UserID: buyerPoison, PlanID: plan.ID, Period: "monthly"}, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.CompleteOrder(ctx, ordPoison.TradeNo, "bl17-limit-poison", now); err != nil {
			t.Fatal(err)
		}
		ordHealthy, err := database.CreateOrder(ctx, CreateOrderInput{UserID: buyerHealthy.ID, PlanID: plan.ID, Period: "monthly"}, now.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.CompleteOrder(ctx, ordHealthy.TradeNo, "bl17-limit-healthy", now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}

		maturedAt := now.Add(time.Second + commissionConfirmationDelay)
		res, err := database.ProcessCommissions(ctx, maturedAt, 100)
		if err != nil {
			t.Fatalf("ProcessCommissions() error = %v", err)
		}
		if res.Paid != 1 || res.Quarantined != 1 || res.Remaining != 0 {
			t.Fatalf("ProcessCommissions() = %#v, want Paid=1 Quarantined=1 Remaining=0", res)
		}

		// L1 must have 0 balance (rolled back by savepoint when L2 failed).
		var l1Bal, healthyBal int64
		if err := database.db.QueryRowContext(ctx, `SELECT commission_balance FROM users WHERE id = ?`, l1.ID).Scan(&l1Bal); err != nil || l1Bal != 0 {
			t.Fatalf("l1 commission_balance = %d, err=%v, want 0 (rolled back)", l1Bal, err)
		}
		if err := database.db.QueryRowContext(ctx, `SELECT commission_balance FROM users WHERE id = ?`, healthyInviter.ID).Scan(&healthyBal); err != nil || healthyBal != 1_000 {
			t.Fatalf("healthyInviter commission_balance = %d, err=%v, want 1000", healthyBal, err)
		}
	})

	t.Run("infrastructure_error_rolls_back_entire_batch", func(t *testing.T) {
		database := newTestStore(t)
		ctx := t.Context()
		now := time.Date(2026, 10, 16, 13, 0, 0, 0, time.UTC)

		plan, buyer := createOrderFixture(t, database, now, PlanPrices{"monthly": 10_000}, nil)
		inviter, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "bl17-infra-inv@example.test", PasswordHash: "hash"}, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(ctx, `UPDATE users SET invite_user_id = ? WHERE id = ?`, inviter.ID, buyer); err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(ctx, `UPDATE users SET commission_type = 1, commission_rate = 20 WHERE id = ?`, inviter.ID); err != nil {
			t.Fatal(err)
		}
		ord, err := database.CreateOrder(ctx, CreateOrderInput{UserID: buyer, PlanID: plan.ID, Period: "monthly"}, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.CompleteOrder(ctx, ord.TradeNo, "bl17-infra-cb", now); err != nil {
			t.Fatal(err)
		}

		canceledCtx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := database.ProcessCommissions(canceledCtx, now.Add(commissionConfirmationDelay+time.Minute), 100); err == nil {
			t.Fatal("ProcessCommissions(canceledCtx) succeeded, want error")
		}
		var status int
		if err := database.db.QueryRowContext(ctx, `SELECT commission_status FROM orders WHERE id = ?`, ord.ID).Scan(&status); err != nil || status != 0 {
			t.Fatalf("commission_status after canceled context = %d, err=%v, want 0 (unmodified)", status, err)
		}
	})
}

func TestBL18StaleOrderRefundOverflowIsolation(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 10, 16, 14, 0, 0, 0, time.UTC)

	plan, user1 := createOrderFixture(t, database, now, PlanPrices{"monthly": 10_000}, nil)
	userPoison, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "bl18-poison@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	user2, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "bl18-user2@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []int64{user1, userPoison.ID, user2.ID} {
		if _, err := database.db.ExecContext(ctx, `UPDATE users SET balance = 2000 WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}

	ord1, err := database.CreateOrder(ctx, CreateOrderInput{UserID: user1, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	ordPoison, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userPoison.ID, PlanID: plan.ID, Period: "monthly"}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	ord2, err := database.CreateOrder(ctx, CreateOrderInput{UserID: user2.ID, PlanID: plan.ID, Period: "monthly"}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}

	// Top up userPoison's balance so refunding ordPoison.BalanceAmount (2000) would exceed maxOrderMoneyCents.
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET balance = ? WHERE id = ?`, maxOrderMoneyCents-500, userPoison.ID); err != nil {
		t.Fatal(err)
	}

	staleAt := now.Add(2*time.Second + orderTimeout + time.Minute)
	res, err := database.ProcessStaleOrders(ctx, staleAt, 100)
	if err != nil {
		t.Fatalf("ProcessStaleOrders() unexpected error = %v, want poison order quarantined", err)
	}
	if res.Cancelled != 2 || res.Quarantined != 1 || res.Remaining != 0 {
		t.Fatalf("ProcessStaleOrders() = %#v, want Cancelled=2 Quarantined=1 Remaining=0", res)
	}

	checkUserOrder := func(uid, oid, wantBal int64, wantStatus OrderStatus) {
		t.Helper()
		var bal int64
		var st OrderStatus
		if err := database.db.QueryRowContext(ctx, `
			SELECT u.balance, o.status FROM users u CROSS JOIN orders o WHERE u.id = ? AND o.id = ?
		`, uid, oid).Scan(&bal, &st); err != nil {
			t.Fatal(err)
		}
		if bal != wantBal || st != wantStatus {
			t.Fatalf("user=%d order=%d: balance=%d status=%d, want %d/%d", uid, oid, bal, st, wantBal, wantStatus)
		}
	}

	checkUserOrder(user1, ord1.ID, 2_000, OrderStatusCancelled)
	checkUserOrder(user2.ID, ord2.ID, 2_000, OrderStatusCancelled)
	// Poison order must remain Pending (0), NOT cancelled without refund!
	checkUserOrder(userPoison.ID, ordPoison.ID, maxOrderMoneyCents-500, OrderStatusPending)

	// Re-run within quarantine window: Remaining=0, Cancelled=0, Quarantined=0.
	again, err := database.ProcessStaleOrders(ctx, staleAt.Add(15*time.Minute), 100)
	if err != nil || again.Cancelled != 0 || again.Quarantined != 0 || again.Remaining != 0 {
		t.Fatalf("ProcessStaleOrders(within quarantine) = (%#v, %v)", again, err)
	}

	// Adjust userPoison balance and advance clock past retry_after (NO manual DELETE from scheduler_quarantine).
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET balance = 1000 WHERE id = ?`, userPoison.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := database.ProcessStaleOrders(ctx, staleAt.Add(time.Hour+time.Second), 100)
	if err != nil || recovered.Cancelled != 1 || recovered.Quarantined != 0 || recovered.Remaining != 0 {
		t.Fatalf("ProcessStaleOrders(after fix) = (%#v, %v), want Cancelled=1 Quarantined=0 Remaining=0", recovered, err)
	}
	checkUserOrder(userPoison.ID, ordPoison.ID, 3_000, OrderStatusCancelled)

	var quarantineCount int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduler_quarantine WHERE kind = 'stale_order' AND entity_id = ?`, ordPoison.ID).Scan(&quarantineCount); err != nil || quarantineCount != 0 {
		t.Fatalf("quarantine count after recovery = %d, err=%v, want 0", quarantineCount, err)
	}
}

func TestBL20TelegramExhaustedThirdClaimRecovery(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 10, 16, 15, 0, 0, 0, time.UTC)
	const lease = 30 * time.Second

	admin, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "bl20-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTelegramSettings(ctx, admin.ID, 1, SaveTelegramSettingsInput{
		BotEnabled: true, ReplaceBotToken: true, BotTokenCipher: bytesRepeat(0x42, 48),
	}, now); err != nil {
		t.Fatal(err)
	}

	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO telegram_message_outbox (
			id, source_kind, source_id, chat_id, text, attempt_count, available_at,
			claim_token, claimed_at, created_at, updated_at
		) VALUES
			(1, 'command', 101, 10001, 'exhausted-third-claim', 3, ?, 'abandoned-token-3', ?, ?, ?),
			(2, 'command', 102, 10002, 'retryable-second-claim', 2, ?, 'abandoned-token-2', ?, ?, ?)
	`, now.Unix(), now.Unix(), now.Unix(), now.Unix(), now.Unix(), now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}

	// At t0 + lease - 1s: neither claim has expired yet.
	if _, claimed, err := database.ClaimTelegramMessage(ctx, "probe-before-expiry", now.Add(lease-time.Second), lease); err != nil || claimed {
		t.Fatalf("ClaimTelegramMessage(before expiry) = (%v, %v), want false, nil", claimed, err)
	}
	statsBefore, err := database.GetTelegramQueueStats(ctx)
	if err != nil || statsBefore.Pending != 2 || statsBefore.Claimed != 2 || statsBefore.Failed != 0 {
		t.Fatalf("stats before expiry = %#v, %v, want Pending=2 Claimed=2 Failed=0", statsBefore, err)
	}

	// At t0 + lease + 1s: id=1 (attempt=3) must become failed with delivery_result_unknown,
	// while id=2 (attempt=2) must be reclaimed for attempt 3.
	afterExpiry := now.Add(lease + time.Second)
	job, claimed, err := database.ClaimTelegramMessage(ctx, "reclaim-second", afterExpiry, lease)
	if err != nil || !claimed || job.ID != 2 || job.Attempt != 3 {
		t.Fatalf("ClaimTelegramMessage(after expiry) = (%#v, %v, %v), want ID=2 Attempt=3", job, claimed, err)
	}

	var failedAt, claimedAt sql.NullInt64
	var claimToken, lastError sql.NullString
	if err := database.db.QueryRowContext(ctx, `
		SELECT failed_at, claim_token, claimed_at, last_error
		FROM telegram_message_outbox WHERE id = 1
	`).Scan(&failedAt, &claimToken, &claimedAt, &lastError); err != nil {
		t.Fatal(err)
	}
	if !failedAt.Valid || failedAt.Int64 != afterExpiry.Unix() || claimToken.Valid || claimedAt.Valid || lastError.String != "delivery_result_unknown" {
		t.Fatalf("exhausted row 1 = failedAt=%#v token=%#v claimedAt=%#v lastError=%q, want failed with delivery_result_unknown",
			failedAt, claimToken, claimedAt, lastError.String)
	}

	statsAfter, err := database.GetTelegramQueueStats(ctx)
	if err != nil || statsAfter.Pending != 1 || statsAfter.Claimed != 1 || statsAfter.Failed != 1 {
		t.Fatalf("stats after expiry = %#v, %v, want Pending=1 Claimed=1 Failed=1", statsAfter, err)
	}
}

func bytesRepeat(b byte, n int) []byte {
	return []byte(strings.Repeat(string([]byte{b}), n))
}

var _ = errors.Is
var _ = context.Background
