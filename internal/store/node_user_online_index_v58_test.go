package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSchemaV58IndexesUserScopedOnlineStateWithoutLosingRows(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_300_000, 0)
	account, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "online-index-upgrade@example.test", PasswordHash: "opaque", TransferEnable: 1_000,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	_, node := createReportingNode(t, database, now)
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO node_user_online(node_id,user_id,connections,expires_at) VALUES(?,?,2,?);
		DROP INDEX IF EXISTS idx_node_user_online_user_expiry;
		PRAGMA user_version=57;
	`, node.ID, account.ID, now.Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}

	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var version, preserved int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_user_online WHERE node_id=? AND user_id=?`, node.ID, account.ID).Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if version != 58 || preserved != 1 {
		t.Fatalf("schema version=%d preserved rows=%d, want 58/1", version, preserved)
	}
	assertQueryPlanContains(t, database, `
		EXPLAIN QUERY PLAN DELETE FROM node_user_online WHERE user_id=?
	`, "idx_node_user_online_user_expiry", account.ID)
	assertQueryPlanContains(t, database, `
		EXPLAIN QUERY PLAN DELETE FROM node_user_online
		WHERE user_id IN (SELECT user_id FROM admin_user_bulk_targets WHERE job_id=? AND status='succeeded')
	`, "idx_node_user_online_user_expiry", "00000000-0000-0000-0000-000000000000")

	if _, err := database.db.ExecContext(ctx, `DROP INDEX idx_node_user_online_user_expiry`); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSchema(ctx, database.db, 58); err == nil || !strings.Contains(err.Error(), "online user index") {
		t.Fatalf("ValidateSchema() error=%v, want online user index rejection", err)
	}
}
