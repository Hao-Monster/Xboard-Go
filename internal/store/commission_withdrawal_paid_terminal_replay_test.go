package store

import (
	"testing"
	"time"
)

func TestCommissionWithdrawalPaidTerminalReplayWinsOverDifferentActiveRequest(t *testing.T) {
	db, user, admin, now := withdrawalFixture(t)
	ctx := t.Context()
	terminalInput := CommissionWithdrawalInput{Method: "USDT", Account: "paid-terminal-wallet", RequestKey: "paid-terminal-request-key"}
	terminal, err := db.CreateCommissionWithdrawalTicket(ctx, user.ID, terminalInput, now)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err = db.TransitionCommissionWithdrawal(ctx, admin.ID, terminal.ID, CommissionWithdrawalTransitionInput{Status: "approved"}, now.Add(time.Minute))
	if err != nil || terminal.Withdrawal == nil || terminal.Withdrawal.Status != "approved" {
		t.Fatalf("approve terminal withdrawal = %#v, %v", terminal, err)
	}
	terminal, err = db.TransitionCommissionWithdrawal(ctx, admin.ID, terminal.ID, CommissionWithdrawalTransitionInput{
		Status: "paid", PaymentReference: "paid-terminal-receipt",
	}, now.Add(2*time.Minute))
	if err != nil || terminal.Withdrawal == nil || terminal.Withdrawal.Status != "paid" {
		t.Fatalf("pay terminal withdrawal = %#v, %v", terminal, err)
	}
	if _, err := db.CloseTicketAsUser(ctx, user.ID, terminal.ID, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("close paid terminal withdrawal ticket: %v", err)
	}

	accrualAt := now.Add(4 * time.Minute)
	plan, buyerID := createOrderFixture(t, db, accrualAt, PlanPrices{"monthly": 100_000}, nil)
	if _, err := db.db.ExecContext(ctx, `UPDATE users SET invite_user_id = ? WHERE id = ?`, user.ID, buyerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE users SET commission_type = 1, commission_rate = 20 WHERE id = ?`, user.ID); err != nil {
		t.Fatal(err)
	}
	order, err := db.CreateOrder(ctx, CreateOrderInput{UserID: buyerID, PlanID: plan.ID, Period: "monthly"}, accrualAt)
	if err != nil || order.CommissionBalance != 20_000 {
		t.Fatalf("create commission accrual order = %#v, %v", order, err)
	}
	if _, err := db.CompleteOrder(ctx, order.TradeNo, "paid-terminal-accrual", accrualAt); err != nil {
		t.Fatal(err)
	}
	processedAt := accrualAt.Add(72 * time.Hour)
	processed, err := db.ProcessCommissions(ctx, processedAt, 100)
	if err != nil || processed.Checked != 1 || processed.Paid != 1 || processed.Remaining != 0 {
		t.Fatalf("process subsequent commission = %#v, %v", processed, err)
	}
	assertWithdrawalFunds(t, db, user.ID, 20_000, 0, 25050)

	active, err := db.CreateCommissionWithdrawalTicket(ctx, user.ID, CommissionWithdrawalInput{
		Method: "USDT", Account: "paid-active-wallet", RequestKey: "paid-active-request-key",
	}, processedAt.Add(time.Minute))
	if err != nil || active.Withdrawal == nil || active.Withdrawal.Status != "pending" || active.Withdrawal.Amount != 20_000 {
		t.Fatalf("create active withdrawal after paid terminal = %#v, %v", active, err)
	}
	activeBeforeReplay := *active.Withdrawal
	assertWithdrawalFunds(t, db, user.ID, 0, 20_000, 25050)

	var eventsBefore, auditsBefore int
	if err := db.db.QueryRow(`SELECT
	 (SELECT COUNT(*) FROM commission_withdrawal_events),
	 (SELECT COUNT(*) FROM admin_audit_logs WHERE route = '/api/v1/admin/tickets/{ticketID}/withdrawal')`).Scan(&eventsBefore, &auditsBefore); err != nil {
		t.Fatal(err)
	}
	if eventsBefore != 4 || auditsBefore != 2 {
		t.Fatalf("events/audits before paid replay = %d/%d, want 4/2", eventsBefore, auditsBefore)
	}

	replayed, err := db.CreateCommissionWithdrawalTicket(ctx, user.ID, terminalInput, processedAt.Add(2*time.Minute))
	if err != nil || replayed.ID != terminal.ID || replayed.Status != TicketStatusClosed || replayed.Withdrawal == nil ||
		replayed.Withdrawal.Status != "paid" || replayed.Withdrawal.PaymentReference != "paid-terminal-receipt" {
		t.Fatalf("paid terminal replay = %#v, %v; want paid ticket %d", replayed, err, terminal.ID)
	}
	activeAfterReplay, err := db.GetAdminTicket(ctx, active.ID)
	if err != nil || activeAfterReplay.Withdrawal == nil || activeAfterReplay.Withdrawal.Status != "pending" {
		t.Fatalf("active withdrawal after paid terminal replay = %#v, %v", activeAfterReplay, err)
	}
	activeWithdrawal := activeAfterReplay.Withdrawal
	if activeWithdrawal.ID != activeBeforeReplay.ID || activeWithdrawal.TicketID != activeBeforeReplay.TicketID ||
		activeWithdrawal.Amount != activeBeforeReplay.Amount || activeWithdrawal.Method != activeBeforeReplay.Method ||
		activeWithdrawal.Account != activeBeforeReplay.Account {
		t.Fatalf("active withdrawal mutated by paid terminal replay: before=%#v after=%#v", activeBeforeReplay, *activeWithdrawal)
	}
	assertWithdrawalFunds(t, db, user.ID, 0, 20_000, 25050)

	var eventsAfter, auditsAfter int
	if err := db.db.QueryRow(`SELECT
	 (SELECT COUNT(*) FROM commission_withdrawal_events),
	 (SELECT COUNT(*) FROM admin_audit_logs WHERE route = '/api/v1/admin/tickets/{ticketID}/withdrawal')`).Scan(&eventsAfter, &auditsAfter); err != nil {
		t.Fatal(err)
	}
	if eventsAfter != eventsBefore || auditsAfter != auditsBefore {
		t.Fatalf("paid terminal replay added events/audits = %d/%d, want %d/%d", eventsAfter, auditsAfter, eventsBefore, auditsBefore)
	}
}
