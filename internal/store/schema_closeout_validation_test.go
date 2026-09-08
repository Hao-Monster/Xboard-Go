package store

import (
	"strings"
	"testing"
)

func TestCommissionWithdrawalSchemaRejectsMissingOrWeakenedFinancialProtection(t *testing.T) {
	for name, mutation := range map[string]string{
		"missing trigger": `DROP TRIGGER commission_withdrawals_transition`,
		"dummy trigger": `DROP TRIGGER commission_withdrawals_transition;
		 CREATE TRIGGER commission_withdrawals_transition BEFORE UPDATE ON commission_withdrawals BEGIN SELECT 1; END`,
		"nonunique active index": `DROP INDEX commission_withdrawals_active_user;
		 CREATE INDEX commission_withdrawals_active_user ON commission_withdrawals(user_id) WHERE status IN ('pending','approved')`,
		"wrong receipt index": `DROP INDEX commission_withdrawals_payment_reference;
		 CREATE UNIQUE INDEX commission_withdrawals_payment_reference ON commission_withdrawals(method,payment_reference) WHERE status = 'paid'`,
		"case changed active predicate": `DROP INDEX commission_withdrawals_active_user;
		 CREATE UNIQUE INDEX commission_withdrawals_active_user ON commission_withdrawals(user_id) WHERE status IN ('PENDING','APPROVED')`,
	} {
		t.Run(name, func(t *testing.T) {
			db := newTestStore(t)
			if err := validateSchemaV61Objects(t.Context(), db.db); err != nil {
				t.Fatalf("valid schema: %v", err)
			}
			if _, err := db.db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			if err := validateSchemaV61Objects(t.Context(), db.db); err == nil {
				t.Fatal("weakened financial schema passed validation")
			}
		})
	}
	t.Run("missing amount check", func(t *testing.T) {
		db := newTestStore(t)
		if _, err := db.db.Exec(`DROP TABLE commission_withdrawal_events; DROP TABLE commission_withdrawals`); err != nil {
			t.Fatal(err)
		}
		weakened := strings.Replace(schemaV61CommissionWithdrawals, "amount > 0", "amount >= 0", 1)
		if _, err := db.db.Exec(weakened); err != nil {
			t.Fatal(err)
		}
		if err := validateSchemaV61Objects(t.Context(), db.db); err == nil {
			t.Fatal("zero-value financial ledger passed validation")
		}
	})
}
