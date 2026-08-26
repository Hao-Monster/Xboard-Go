package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestImportLegacyCommissionsPreservesLogsAndIsIdempotent(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	inviter, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "legacy-commission-inviter@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	buyer, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "legacy-commission-buyer@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := database.CreatePlan(ctx, SavePlanInput{Name: "Legacy commission plan", TransferEnableGiB: 10, Prices: PlanPrices{"monthly": 10_000}}, now)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = database.SetPlanState(ctx, plan.ID, plan.Revision, PlanState{Show: true, Sell: true, Renew: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: buyer.ID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	sourceSHA := strings.Repeat("a", 64)
	for _, slice := range []string{LegacyHumanUsersSlice, LegacyOrdersSlice} {
		if _, err := database.db.ExecContext(ctx, `
			INSERT INTO legacy_migration_runs
			(slice, source_sha256, source_size, rollback_backup_path, rollback_backup_sha256, report_json, applied_at)
			VALUES (?, ?, 4096, '/backups/prerequisite.xbbackup', ?, '{}', ?)
		`, slice, sourceSHA, strings.Repeat("b", 64), now.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	logs := []LegacyCommissionLog{{
		ID: 41, InviteUserID: inviter.ID, UserID: buyer.ID, TradeNo: order.TradeNo,
		OrderAmount: order.TotalAmount, GetAmount: 1_700, CreatedAt: now.Unix(), UpdatedAt: now.Add(time.Minute).Unix(),
	}}
	input := LegacyCommissionsImport{
		Slice: LegacyCommissionsSlice, SourceSHA256: sourceSHA, SourceSize: 4096, Logs: logs,
		RollbackBackupPath: "/backups/commissions.xbbackup", RollbackBackupSHA256: strings.Repeat("c", 64),
	}
	input.Checksum = LegacyCommissionsChecksum(input.Logs)
	report, err := database.ImportLegacyCommissions(ctx, input, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("ImportLegacyCommissions() error = %v", err)
	}
	if report.AlreadyApplied || report.Logs.SourceRows != 1 || report.Logs.TargetRows != 1 || report.Logs.SourceChecksum != report.Logs.TargetChecksum {
		t.Fatalf("commission report = %#v", report)
	}
	page, err := database.ListCommissionLogs(ctx, inviter.ID, 1, 10)
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].TradeNo != order.TradeNo || page.Items[0].GetAmount != 1_700 {
		t.Fatalf("imported commission page = (%#v, %v)", page, err)
	}
	repeated, err := database.ImportLegacyCommissions(ctx, input, now.Add(3*time.Minute))
	if err != nil || !repeated.AlreadyApplied || repeated.AppliedAt != report.AppliedAt {
		t.Fatalf("idempotent ImportLegacyCommissions() = (%#v, %v)", repeated, err)
	}
}

func TestImportLegacyCommissionsRequiresMatchingUserAndOrderSlices(t *testing.T) {
	database := newTestStore(t)
	input := LegacyCommissionsImport{
		Slice: LegacyCommissionsSlice, SourceSHA256: strings.Repeat("d", 64), SourceSize: 1,
		Logs: []LegacyCommissionLog{}, RollbackBackupPath: "/backups/commissions.xbbackup",
		RollbackBackupSHA256: strings.Repeat("e", 64),
	}
	input.Checksum = LegacyCommissionsChecksum(input.Logs)
	if _, err := database.ImportLegacyCommissions(context.Background(), input, time.Unix(100, 0)); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "human users and orders") {
		t.Fatalf("ImportLegacyCommissions(missing prerequisites) error = %v", err)
	}
	var rows, runs int
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM commission_logs`).Scan(&rows)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyCommissionsSlice).Scan(&runs)
	if rows != 0 || runs != 0 {
		t.Fatalf("failed commission import left rows=%d runs=%d", rows, runs)
	}
}
