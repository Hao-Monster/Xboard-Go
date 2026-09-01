package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSchemaV59GuardsCombinedUserTrafficWithoutLosingBoundaryRows(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	maximum := int64(^uint64(0) >> 1)
	account, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "traffic-v59@example.test", PasswordHash: "opaque", TransferEnable: 1_000,
	}, time.Unix(1_800_400_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE users SET traffic_u=?,traffic_d=1 WHERE id=?;
		DROP TRIGGER users_validate_traffic_total_insert;
		DROP TRIGGER users_validate_traffic_total_update;
		PRAGMA user_version=58;
	`, maximum-1, account.ID); err != nil {
		t.Fatal(err)
	}

	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var version, upload, download int64
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT traffic_u,traffic_d FROM users WHERE id=?`, account.ID).Scan(&upload, &download); err != nil {
		t.Fatal(err)
	}
	if version != 59 || upload != maximum-1 || download != 1 {
		t.Fatalf("schema/user traffic = %d/%d/%d, want 59/%d/1", version, upload, download, maximum-1)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET traffic_u=?,traffic_d=1 WHERE id=?`, maximum, account.ID); err == nil || !strings.Contains(err.Error(), "user traffic total overflow") {
		t.Fatalf("overflowing direct update error = %v, want traffic total guard", err)
	}
	if err := ValidateSchema(ctx, database.db, 59); err != nil {
		t.Fatalf("ValidateSchema() error = %v", err)
	}
	if _, err := database.db.ExecContext(ctx, `DROP TRIGGER users_validate_traffic_total_update`); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSchema(ctx, database.db, 59); err == nil || !strings.Contains(err.Error(), "user traffic total trigger") {
		t.Fatalf("ValidateSchema() error = %v, want missing trigger rejection", err)
	}
}

func TestSchemaV59RejectsExistingCombinedUserTrafficOverflowAtomically(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	maximum := int64(^uint64(0) >> 1)
	account, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "traffic-v59-invalid@example.test", PasswordHash: "opaque", TransferEnable: 1_000,
	}, time.Unix(1_800_400_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		DROP TRIGGER users_validate_traffic_total_insert;
		DROP TRIGGER users_validate_traffic_total_update;
		UPDATE users SET traffic_u=?,traffic_d=1 WHERE id=?;
		PRAGMA user_version=58;
	`, maximum, account.ID); err != nil {
		t.Fatal(err)
	}

	err = database.Migrate(ctx)
	if err == nil || !strings.Contains(err.Error(), "combined traffic outside the int64 range") {
		t.Fatalf("Migrate() error = %v, want invalid traffic rejection", err)
	}
	var version, triggers int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_schema
		WHERE type='trigger' AND name IN ('users_validate_traffic_total_insert','users_validate_traffic_total_update')
	`).Scan(&triggers); err != nil {
		t.Fatal(err)
	}
	if version != 58 || triggers != 0 {
		t.Fatalf("rejected migration left version/triggers = %d/%d, want 58/0", version, triggers)
	}
}
