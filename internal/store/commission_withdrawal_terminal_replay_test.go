package store

import (
	"testing"
	"time"
)

func TestCommissionWithdrawalTerminalReplayWinsOverDifferentActiveRequest(t *testing.T) {
	db, user, admin, now := withdrawalFixture(t)
	ctx := t.Context()
	terminalInput := CommissionWithdrawalInput{
		Method:     "USDT",
		Account:    "terminal-wallet",
		RequestKey: "terminal-request-key",
	}
	terminal, err := db.CreateCommissionWithdrawalTicket(ctx, user.ID, terminalInput, now)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err = db.TransitionCommissionWithdrawal(ctx, admin.ID, terminal.ID, CommissionWithdrawalTransitionInput{Status: "rejected"}, now.Add(time.Minute))
	if err != nil || terminal.Withdrawal == nil || terminal.Withdrawal.Status != "rejected" {
		t.Fatalf("reject terminal withdrawal = %#v, %v", terminal, err)
	}
	if _, err := db.CloseTicketAsUser(ctx, user.ID, terminal.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("close terminal withdrawal ticket: %v", err)
	}

	active, err := db.CreateCommissionWithdrawalTicket(ctx, user.ID, CommissionWithdrawalInput{
		Method:     "USDT",
		Account:    "active-wallet",
		RequestKey: "active-request-key",
	}, now.Add(3*time.Minute))
	if err != nil || active.Withdrawal == nil || active.Withdrawal.Status != "pending" {
		t.Fatalf("create active withdrawal = %#v, %v", active, err)
	}
	if active.ID == terminal.ID {
		t.Fatalf("active withdrawal reused terminal ticket %d", terminal.ID)
	}
	assertWithdrawalFunds(t, db, user.ID, 0, 25050, 0)

	var eventsBefore, auditsBefore int
	if err := db.db.QueryRow(`SELECT
	 (SELECT COUNT(*) FROM commission_withdrawal_events),
	 (SELECT COUNT(*) FROM admin_audit_logs WHERE route = '/api/v1/admin/tickets/{ticketID}/withdrawal')`).Scan(&eventsBefore, &auditsBefore); err != nil {
		t.Fatal(err)
	}
	if eventsBefore != 3 || auditsBefore != 1 {
		t.Fatalf("events/audits before replay = %d/%d, want 3/1", eventsBefore, auditsBefore)
	}

	replayed, err := db.CreateCommissionWithdrawalTicket(ctx, user.ID, terminalInput, now.Add(4*time.Minute))
	if err != nil || replayed.ID != terminal.ID || replayed.Status != TicketStatusClosed || replayed.Withdrawal == nil || replayed.Withdrawal.Status != "rejected" {
		t.Fatalf("terminal replay = %#v, %v; want rejected ticket %d", replayed, err, terminal.ID)
	}
	activeAfterReplay, err := db.GetAdminTicket(ctx, active.ID)
	if err != nil || activeAfterReplay.Withdrawal == nil || activeAfterReplay.Withdrawal.Status != "pending" {
		t.Fatalf("active withdrawal after terminal replay = %#v, %v", activeAfterReplay, err)
	}
	assertWithdrawalFunds(t, db, user.ID, 0, 25050, 0)

	var eventsAfter, auditsAfter int
	if err := db.db.QueryRow(`SELECT
	 (SELECT COUNT(*) FROM commission_withdrawal_events),
	 (SELECT COUNT(*) FROM admin_audit_logs WHERE route = '/api/v1/admin/tickets/{ticketID}/withdrawal')`).Scan(&eventsAfter, &auditsAfter); err != nil {
		t.Fatal(err)
	}
	if eventsAfter != eventsBefore || auditsAfter != auditsBefore {
		t.Fatalf("terminal replay added events/audits = %d/%d, want %d/%d", eventsAfter, auditsAfter, eventsBefore, auditsBefore)
	}
}
