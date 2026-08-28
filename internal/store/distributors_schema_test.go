package store

import (
	"context"
	"testing"
)

func TestDistributorSchemaEnforcesRolesRelationshipsAndHotIndexes(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()

	if CurrentSchemaVersion() != 41 {
		t.Fatalf("CurrentSchemaVersion() = %d, want 41", CurrentSchemaVersion())
	}
	var version int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != CurrentSchemaVersion() {
		t.Fatalf("schema version = %d, err=%v, want %d", version, err, CurrentSchemaVersion())
	}

	for _, table := range []string{"distributor_subscriptions", "distributor_hwid_devices"} {
		var found int
		if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name = ?`, table).Scan(&found); err != nil {
			t.Fatalf("inspect table %s: %v", table, err)
		}
		if found != 1 {
			t.Fatalf("table %s count = %d, want 1", table, found)
		}
	}

	for _, index := range []string{
		"idx_users_distributor",
		"idx_distributor_subscriptions_owner_settlement",
		"idx_distributor_subscriptions_subscriber",
		"idx_distributor_hwid_last_seen",
		"idx_orders_distributor_idempotency",
		"idx_orders_distributor_settlement",
	} {
		var found int
		if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type = 'index' AND name = ?`, index).Scan(&found); err != nil {
			t.Fatalf("inspect index %s: %v", index, err)
		}
		if found != 1 {
			t.Fatalf("index %s count = %d, want 1", index, found)
		}
	}

	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO users (email,password_hash,is_admin,is_staff,is_distributor,distributor_name,banned,created_at,updated_at)
		VALUES ('invalid-distributor@example.test','hash',0,0,1,NULL,0,1,1)
	`); err == nil {
		t.Fatal("distributor role without a name must be rejected by the database")
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO users (email,password_hash,is_admin,is_staff,is_distributor,distributor_name,banned,created_at,updated_at)
		VALUES ('stale-distributor-name@example.test','hash',0,0,0,'stale',0,1,1)
	`); err == nil {
		t.Fatal("non-distributor with a stale distributor name must be rejected by the database")
	}
}
