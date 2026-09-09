package legacymigration_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/legacymigration"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/Hao-Monster/Xboard-Go/internal/testdata/legacy/gen"
	_ "modernc.org/sqlite"
)

func TestLegacyCommissionImportFreezesAndRejectsWithoutMoneyDrift(t *testing.T) {
	ctx := t.Context()
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy.db")
	if _, err := gen.New(gen.DefaultConfig(sourcePath)).Generate(ctx); err != nil {
		t.Fatalf("generate synthetic legacy source: %v", err)
	}
	minimizeLegacyCommissionSource(t, sourcePath)

	plans, err := legacymigration.ReadPlansSnapshot(ctx, sourcePath)
	if err != nil {
		t.Fatalf("read plans snapshot: %v", err)
	}
	users, err := legacymigration.ReadHumanUsersSnapshot(ctx, sourcePath)
	if err != nil {
		t.Fatalf("read human users snapshot: %v", err)
	}
	orders, err := legacymigration.ReadOrdersSnapshot(ctx, sourcePath)
	if err != nil {
		t.Fatalf("read orders snapshot: %v", err)
	}
	commissions, err := legacymigration.ReadCommissionsSnapshot(ctx, sourcePath)
	if err != nil {
		t.Fatalf("read commissions snapshot: %v", err)
	}
	if plans.SHA256 != users.SHA256 || users.SHA256 != orders.SHA256 || orders.SHA256 != commissions.SHA256 {
		t.Fatalf("migration slices do not share one source: plans=%s users=%s orders=%s commissions=%s", plans.SHA256, users.SHA256, orders.SHA256, commissions.SHA256)
	}
	if len(users.Users) != 2 || users.Users[1].ID != 2 || users.Users[1].CommissionBalance != 50 {
		t.Fatalf("legacy withdrawal owner finance = %#v", users.Users)
	}
	if len(orders.Orders) != 1 || orders.Orders[0].ID != 61 || orders.Orders[0].TotalAmount != 524 {
		t.Fatalf("legacy order finance = %#v", orders.Orders)
	}
	if len(commissions.Logs) != 1 || commissions.Logs[0].InviteUserID != 2 || commissions.Logs[0].UserID != 1 ||
		commissions.Logs[0].OrderAmount != 524 || commissions.Logs[0].GetAmount != 50 {
		t.Fatalf("legacy commission finance = %#v", commissions.Logs)
	}

	targetPath := filepath.Join(directory, "target.db")
	database, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if created, err := database.BootstrapAdmin(ctx, "bootstrap@example.test", "bootstrap-hash", time.Unix(50, 0)); err != nil || !created {
		t.Fatalf("bootstrap target = %v, %v", created, err)
	}
	inspection, err := sql.Open("sqlite", "file:"+targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()

	now := time.Date(2026, 9, 9, 1, 0, 0, 0, time.UTC)
	rollbackHash := strings.Repeat("a", 64)
	if _, err := database.ImportLegacyPlans(ctx, store.LegacyPlansImport{
		Slice: store.LegacyPlansSlice, SourceSHA256: plans.SHA256, SourceSize: plans.Size,
		Plans: plans.Plans, Checksum: plans.Checksum, TrafficResetMethod: plans.TrafficResetMethod, SettingsChecksum: plans.SettingsChecksum,
		RollbackBackupPath: "/synthetic/plans.xbbackup", RollbackBackupSHA256: rollbackHash,
	}, now); err != nil {
		t.Fatalf("import legacy plans: %v", err)
	}
	if _, err := database.ImportLegacyHumanUsers(ctx, store.LegacyHumanUsersImport{
		Slice: store.LegacyHumanUsersSlice, SourceSHA256: users.SHA256, SourceSize: users.Size,
		Users: users.Users, Checksum: users.Checksum, ReplaceBootstrapAdmin: true,
		RollbackBackupPath: "/synthetic/human-users.xbbackup", RollbackBackupSHA256: rollbackHash,
	}, now.Add(time.Minute)); err != nil {
		t.Fatalf("import legacy human users: %v", err)
	}
	assertImportedCommissionState(t, inspection, 2, 50, 0, 0)
	if _, err := database.ImportLegacyOrders(ctx, store.LegacyOrdersImport{
		Slice: store.LegacyOrdersSlice, SourceSHA256: orders.SHA256, SourceSize: orders.Size,
		Orders: orders.Orders, Checksum: orders.Checksum,
		RollbackBackupPath: "/synthetic/orders.xbbackup", RollbackBackupSHA256: rollbackHash,
	}, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("import legacy orders: %v", err)
	}
	assertImportedCommissionState(t, inspection, 2, 50, 0, 0)
	assertImportedOrderState(t, inspection)
	commissionReport, err := database.ImportLegacyCommissions(ctx, store.LegacyCommissionsImport{
		Slice: store.LegacyCommissionsSlice, SourceSHA256: commissions.SHA256, SourceSize: commissions.Size,
		Logs: commissions.Logs, Checksum: commissions.Checksum,
		RollbackBackupPath: "/synthetic/commissions.xbbackup", RollbackBackupSHA256: rollbackHash,
	}, now.Add(3*time.Minute))
	if err != nil || commissionReport.Logs.SourceRows != 1 || commissionReport.Logs.TargetRows != 1 {
		t.Fatalf("import legacy commissions = %#v, %v", commissionReport, err)
	}
	assertLegacyMigrationRunsShareSource(t, inspection, commissions.SHA256)
	assertImportedCommissionState(t, inspection, 2, 50, 1, 50)
	assertImportedCommissionRelation(t, inspection)
	if _, err := inspection.Exec(`UPDATE app_settings SET withdraw_close_enable = 0, commission_withdraw_limit = 1, commission_withdraw_method = '["USDT"]' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	withdrawal, err := database.CreateCommissionWithdrawalTicket(ctx, 2, store.CommissionWithdrawalInput{
		Method: "USDT", Account: "legacy-import-wallet", RequestKey: "legacy-import-withdrawal",
	}, now.Add(4*time.Minute))
	if err != nil || withdrawal.Withdrawal == nil || withdrawal.Withdrawal.Status != "pending" || withdrawal.Withdrawal.Amount != 50 {
		t.Fatalf("freeze imported commission = %#v, %v", withdrawal, err)
	}
	assertWithdrawalState(t, inspection, withdrawal.Withdrawal.ID, 0, "pending", 1, -50, 50)

	rejected, err := database.TransitionCommissionWithdrawal(ctx, 1, withdrawal.ID, store.CommissionWithdrawalTransitionInput{Status: "rejected"}, now.Add(5*time.Minute))
	if err != nil || rejected.Withdrawal == nil || rejected.Withdrawal.Status != "rejected" || rejected.Withdrawal.Amount != 50 {
		t.Fatalf("reject imported commission withdrawal = %#v, %v", rejected, err)
	}
	assertWithdrawalState(t, inspection, withdrawal.Withdrawal.ID, 50, "rejected", 2, 0, 0)
	assertImportedCommissionState(t, inspection, 2, 50, 1, 50)
	var audits int
	if err := inspection.QueryRow(`SELECT COUNT(*) FROM admin_audit_logs WHERE route = '/api/v1/admin/tickets/{ticketID}/withdrawal'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("withdrawal audits = %d, %v", audits, err)
	}
}

func assertLegacyMigrationRunsShareSource(t *testing.T, database *sql.DB, sourceSHA256 string) {
	t.Helper()
	var runs, sources int
	var minimumSHA256, maximumSHA256 string
	if err := database.QueryRow(`
		SELECT COUNT(*), COUNT(DISTINCT source_sha256), MIN(source_sha256), MAX(source_sha256)
		FROM legacy_migration_runs
		WHERE slice IN (?, ?, ?, ?)
	`, store.LegacyPlansSlice, store.LegacyHumanUsersSlice, store.LegacyOrdersSlice, store.LegacyCommissionsSlice).
		Scan(&runs, &sources, &minimumSHA256, &maximumSHA256); err != nil {
		t.Fatal(err)
	}
	if runs != 4 || sources != 1 || minimumSHA256 != sourceSHA256 || maximumSHA256 != sourceSHA256 {
		t.Fatalf("legacy migration runs = runs %d sources %d range %s..%s, want 4/1/%s", runs, sources, minimumSHA256, maximumSHA256, sourceSHA256)
	}
}

func assertImportedOrderState(t *testing.T, database *sql.DB) {
	t.Helper()
	var userID, inviteUserID, totalAmount int64
	if err := database.QueryRow(`SELECT user_id, invite_user_id, total_amount FROM orders WHERE id = 61`).
		Scan(&userID, &inviteUserID, &totalAmount); err != nil {
		t.Fatal(err)
	}
	if userID != 1 || inviteUserID != 2 || totalAmount != 524 {
		t.Fatalf("imported order relation = user %d inviter %d total %d, want 1/2/524", userID, inviteUserID, totalAmount)
	}
}

func assertImportedCommissionRelation(t *testing.T, database *sql.DB) {
	t.Helper()
	var orderID, inviteUserID, userID, orderAmount, getAmount int64
	if err := database.QueryRow(`SELECT order_id, invite_user_id, user_id, order_amount, get_amount FROM commission_logs WHERE id = 131`).
		Scan(&orderID, &inviteUserID, &userID, &orderAmount, &getAmount); err != nil {
		t.Fatal(err)
	}
	if orderID != 61 || inviteUserID != 2 || userID != 1 || orderAmount != 524 || getAmount != 50 {
		t.Fatalf("imported commission relation = order %d inviter %d user %d amounts %d/%d, want 61/2/1/524/50", orderID, inviteUserID, userID, orderAmount, getAmount)
	}
}

func minimizeLegacyCommissionSource(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
		DELETE FROM v2_distributor_order;
		DELETE FROM v2_user WHERE id = 100;
		UPDATE v2_user SET group_id = NULL, plan_id = NULL;
		UPDATE v2_user SET commission_balance = 0 WHERE id = 1;
		UPDATE v2_user SET invite_user_id = NULL, commission_balance = 50, is_distributor = 0, distributor_name = NULL WHERE id = 2;
		DELETE FROM v2_plan WHERE id <> 11;
		UPDATE v2_plan SET group_id = NULL WHERE id = 11;
		DELETE FROM v2_order WHERE id <> 61;
		UPDATE v2_order SET user_id = 1, invite_user_id = 2, coupon_id = NULL, payment_id = NULL WHERE id = 61;
		UPDATE v2_commission_log SET invite_user_id = 2, user_id = 1 WHERE id = 131;
	`); err != nil {
		t.Fatal(err)
	}
}

func assertImportedCommissionState(t *testing.T, database *sql.DB, userID, available int64, logs int, getAmount int64) {
	t.Helper()
	var gotAvailable, gotLogs int64
	var gotAmount int64
	if err := database.QueryRow(`SELECT commission_balance FROM users WHERE id = ?`, userID).Scan(&gotAvailable); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*), COALESCE(SUM(get_amount), 0) FROM commission_logs WHERE invite_user_id = ?`, userID).Scan(&gotLogs, &gotAmount); err != nil {
		t.Fatal(err)
	}
	if gotAvailable != available || gotLogs != int64(logs) || gotAmount != getAmount {
		t.Fatalf("imported commission state = available %d logs %d amount %d, want %d/%d/%d", gotAvailable, gotLogs, gotAmount, available, logs, getAmount)
	}
}

func assertWithdrawalState(t *testing.T, database *sql.DB, withdrawalID, available int64, status string, events int, availableDelta, frozenDelta int64) {
	t.Helper()
	var gotAvailable, amount int64
	var gotStatus string
	if err := database.QueryRow(`SELECT commission_balance FROM users WHERE id = 2`).Scan(&gotAvailable); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT amount, status FROM commission_withdrawals WHERE id = ?`, withdrawalID).Scan(&amount, &gotStatus); err != nil {
		t.Fatal(err)
	}
	var gotEvents int
	var gotAvailableDelta, gotFrozenDelta, gotPaidDelta int64
	if err := database.QueryRow(`SELECT COUNT(*), COALESCE(SUM(available_delta), 0), COALESCE(SUM(frozen_delta), 0), COALESCE(SUM(paid_delta), 0) FROM commission_withdrawal_events WHERE withdrawal_id = ?`, withdrawalID).
		Scan(&gotEvents, &gotAvailableDelta, &gotFrozenDelta, &gotPaidDelta); err != nil {
		t.Fatal(err)
	}
	if gotAvailable != available || amount != 50 || gotStatus != status || gotEvents != events ||
		gotAvailableDelta != availableDelta || gotFrozenDelta != frozenDelta || gotPaidDelta != 0 {
		t.Fatalf("withdrawal state = available %d amount %d status %s events %d deltas %d/%d/%d", gotAvailable, amount, gotStatus, gotEvents, gotAvailableDelta, gotFrozenDelta, gotPaidDelta)
	}
}
