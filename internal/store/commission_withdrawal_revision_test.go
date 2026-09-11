package store

import (
	"errors"
	"testing"
	"time"
)

func TestCommissionWithdrawalRejectsStaleAdministratorFunds(t *testing.T) {
	for _, operation := range []string{"freeze", "refund"} {
		t.Run(operation, func(t *testing.T) {
			db, user, admin, now := withdrawalFixture(t)
			ctx := t.Context()
			stale, err := db.GetAdminUser(ctx, user.ID)
			if err != nil {
				t.Fatal(err)
			}
			ticket, err := db.CreateCommissionWithdrawalTicket(ctx, user.ID, CommissionWithdrawalInput{Method: "USDT", Account: "stale-editor-wallet", RequestKey: "stale-editor-key"}, now)
			if err != nil {
				t.Fatal(err)
			}
			wantAvailable, wantFrozen := int64(0), int64(25050)
			if operation == "refund" {
				stale, err = db.GetAdminUser(ctx, user.ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.TransitionCommissionWithdrawal(ctx, admin.ID, ticket.ID, CommissionWithdrawalTransitionInput{Status: "rejected"}, now.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				wantAvailable, wantFrozen = 25050, 0
			}
			remark := "only a remark changed in the old editor"
			input := UpdateAdminUserInput{
				Revision: stale.Revision, Email: stale.Email, GroupID: stale.GroupID, TransferEnable: stale.TransferEnable,
				SpeedLimit: stale.SpeedLimit, DeviceLimit: stale.DeviceLimit, ExpiredAt: stale.ExpiredAt, Banned: stale.Banned,
				Balance: &stale.Balance, CommissionBalance: &stale.CommissionBalance, RemarksSet: true, Remarks: &remark,
			}
			if _, _, err := db.UpdateAdminUser(ctx, user.ID, input, now.Add(2*time.Second)); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale money snapshot accepted after %s: %v", operation, err)
			}
			assertWithdrawalFunds(t, db, user.ID, wantAvailable, wantFrozen, 0)
			fresh, err := db.GetAdminUser(ctx, user.ID)
			if err != nil {
				t.Fatal(err)
			}
			if fresh.Revision != stale.Revision+1 || fresh.Remarks != nil {
				t.Fatalf("financial mutation did not invalidate exactly one editor revision: %#v", fresh)
			}
			input.Revision, input.CommissionBalance = fresh.Revision, &fresh.CommissionBalance
			updated, _, err := db.UpdateAdminUser(ctx, user.ID, input, now.Add(3*time.Second))
			if err != nil || updated.Remarks == nil || *updated.Remarks != remark || updated.Revision != fresh.Revision+1 {
				t.Fatalf("fresh edit = %#v, %v", updated, err)
			}
			assertWithdrawalFunds(t, db, user.ID, wantAvailable, wantFrozen, 0)
		})
	}
}

func TestCommissionWithdrawalMoneyRevisionProtectsEveryWriter(t *testing.T) {
	db, user, _, now := withdrawalFixture(t)
	ctx := t.Context()
	current, err := db.GetAdminUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	// One trigger covers asynchronous writers, including those updating both
	// money columns. It must not depend on a particular Go service or connection.
	for _, statement := range []string{
		`UPDATE users SET balance=balance+101 WHERE id=?`,
		`UPDATE users SET commission_balance=commission_balance+202 WHERE id=?`,
		`UPDATE users SET balance=balance+303,commission_balance=commission_balance-303 WHERE id=?`,
	} {
		if _, err := db.db.ExecContext(ctx, statement, user.ID); err != nil {
			t.Fatal(err)
		}
		fresh, err := db.GetAdminUser(ctx, user.ID)
		if err != nil || fresh.Revision != current.Revision+1 {
			t.Fatalf("money writer revision=%d want=%d: %v", fresh.Revision, current.Revision+1, err)
		}
		current = fresh
	}
	if _, err := db.TransferCommission(ctx, user.ID, 123, now); err != nil {
		t.Fatal(err)
	}
	fresh, err := db.GetAdminUser(ctx, user.ID)
	if err != nil || fresh.Revision != current.Revision+1 || fresh.Balance != current.Balance+123 || fresh.CommissionBalance != current.CommissionBalance-123 {
		t.Fatalf("transfer revision or conserved money = %#v, %v", fresh, err)
	}
	current = fresh
	if _, err := db.db.ExecContext(ctx, `UPDATE users SET balance=balance,commission_balance=commission_balance,updated_at=updated_at+1 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	fresh, err = db.GetAdminUser(ctx, user.ID)
	if err != nil || fresh.Revision != current.Revision {
		t.Fatalf("no-op invalidated money revision: %#v, %v", fresh, err)
	}
	// A deliberate administrator adjustment already increments the revision;
	// the trigger must leave that increment intact rather than increment twice.
	balance, commission := current.Balance+100, current.CommissionBalance+200
	updated, _, err := db.UpdateAdminUser(ctx, user.ID, UpdateAdminUserInput{
		Revision: current.Revision, Email: current.Email, GroupID: current.GroupID, TransferEnable: current.TransferEnable,
		SpeedLimit: current.SpeedLimit, DeviceLimit: current.DeviceLimit, ExpiredAt: current.ExpiredAt, Banned: current.Banned,
		Balance: &balance, CommissionBalance: &commission,
	}, now.Add(time.Minute))
	if err != nil || updated.Revision != current.Revision+1 || updated.Balance != balance || updated.CommissionBalance != commission {
		t.Fatalf("explicit administrator adjustment = %#v, %v", updated, err)
	}
	// A failed transaction may not advance the revision independently of funds.
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET balance=balance+999 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	fresh, err = db.GetAdminUser(ctx, user.ID)
	if err != nil || fresh.Revision != updated.Revision || fresh.Balance != updated.Balance || fresh.CommissionBalance != updated.CommissionBalance {
		t.Fatalf("rollback leaked money or revision = %#v, %v", fresh, err)
	}
}

func TestCommissionWithdrawalMoneyRevisionTriggerIsRequired(t *testing.T) {
	for _, statement := range []string{
		`DROP TRIGGER users_money_admin_revision`,
		`DROP TRIGGER users_money_admin_revision; CREATE TRIGGER users_money_admin_revision AFTER UPDATE OF balance,commission_balance ON users BEGIN SELECT 1; END`,
	} {
		db := newTestStore(t)
		if _, err := db.db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
		if err := db.ValidateCurrentSchema(t.Context()); err == nil {
			t.Fatal("missing or weakened monetary revision protection passed schema validation")
		}
	}
}
