package store

import (
	"strings"
	"testing"
	"time"
)

func TestFinancialEventsV62MigratesV61AndValidatesImmutableSchema(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 9, 2, 16, 0, 0, 0, time.UTC)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "v62-preserved@example.test", PasswordHash: "hash",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		DROP TABLE commission_transfer_events;
		DROP TABLE admin_balance_adjustment_events;
		PRAGMA user_version=61;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(V61 to V62) error = %v", err)
	}
	var version, eventTables, preserved int64
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_schema
		WHERE type='table' AND name IN ('commission_transfer_events','admin_balance_adjustment_events')
	`).Scan(&eventTables); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND email=?`, user.ID, user.Email).Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if version != 62 || eventTables != 2 || preserved != 1 {
		t.Fatalf("V62 migration = version %d tables %d preserved users %d", version, eventTables, preserved)
	}
	if _, err := database.db.ExecContext(ctx, `
		DROP TRIGGER commission_transfer_events_no_update;
		CREATE TRIGGER commission_transfer_events_no_update
		BEFORE UPDATE ON commission_transfer_events BEGIN SELECT 1; END;
	`); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSchema(ctx, database.db, 62); err == nil || !strings.Contains(err.Error(), "commission_transfer_events_no_update") {
		t.Fatalf("ValidateSchema(tampered V62 trigger) error = %v", err)
	}
}

func TestFinancialEventsV62RejectsWeakPreexistingTable(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	if _, err := database.db.ExecContext(ctx, `
		DROP TABLE commission_transfer_events;
		DROP TABLE admin_balance_adjustment_events;
		CREATE TABLE commission_transfer_events (
			id INTEGER PRIMARY KEY,
			user_id INTEGER,
			idempotency_key TEXT,
			amount INTEGER,
			currency TEXT,
			commission_balance_before INTEGER,
			commission_balance_after INTEGER,
			balance_before INTEGER,
			balance_after INTEGER,
			created_at INTEGER
		);
		PRAGMA user_version=61;
	`); err != nil {
		t.Fatal(err)
	}
	err := database.Migrate(ctx)
	if err == nil || !strings.Contains(err.Error(), `commission_transfer_events`) {
		t.Fatalf("Migrate(weak preexisting V62 table) error = %v", err)
	}
	var version int
	if scanErr := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); scanErr != nil {
		t.Fatal(scanErr)
	}
	if version != 61 {
		t.Fatalf("failed V62 migration committed version %d, want 61", version)
	}
}
