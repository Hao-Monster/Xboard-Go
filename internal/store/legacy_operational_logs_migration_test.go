package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func operationalImportFixture(t *testing.T) (*Store, LegacyOperationalLogsImport, time.Time) {
	t.Helper()
	db := newTestStore(t)
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	input := LegacyOperationalLogsImport{
		Slice: LegacyOperationalLogsSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 4096,
		AsOf: now.Unix(), CutoffAt: now.Add(-90 * 24 * time.Hour).Unix(),
		RollbackBackupPath: "/backups/operational.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
		ExcludedRows: 3, ExcludedFailedJobs: 2,
		UserFactsChecksum: LegacyOperationalUserFactsChecksum(nil),
	}
	input.Logs = []LegacyOperationalLog{{Category: "request", ID: 1, Method: "GET", CreatedAt: input.CutoffAt}, {Category: "mail", ID: 1, CreatedAt: input.AsOf}}
	input.Checksum = LegacyOperationalLogsChecksum(input.Logs)
	for _, slice := range []string{LegacyHumanUsersSlice, LegacyNodesSlice, LegacyOrdersSlice, LegacyCommissionsSlice} {
		if _, err := db.db.Exec(`INSERT INTO legacy_migration_runs(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at) VALUES (?,?,4096,'prerequisite',?,'{}',?)`, slice, input.SourceSHA256, strings.Repeat("b", 64), now.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ImportLegacyDistributors(t.Context(), LegacyDistributorsImport{Slice: LegacyDistributorsSlice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		Checksum: LegacyDistributorsChecksum(LegacyDistributorsData{}), RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256}, now); err != nil {
		t.Fatal(err)
	}
	return db, input, now
}

func TestOperationalImportReadsBackTargetAndRejectsMutatedRows(t *testing.T) {
	db, input, now := operationalImportFixture(t)
	if _, err := db.db.Exec(`CREATE TRIGGER corrupt_imported_metadata AFTER INSERT ON legacy_operational_logs BEGIN UPDATE legacy_operational_logs SET method='POST' WHERE category=NEW.category AND source_id=NEW.source_id; END`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ImportLegacyOperationalLogs(t.Context(), input, now); err == nil || !strings.Contains(err.Error(), "verification") {
		t.Fatalf("target mutation must fail actual checksum verification, got %v", err)
	}
	assertNoOperationalImport(t, db)
}

func TestOperationalImportReplaysFixedWindowAndDetectsTargetDrift(t *testing.T) {
	db, input, now := operationalImportFixture(t)
	first, err := db.ImportLegacyOperationalLogs(t.Context(), input, now)
	if err != nil || first.AlreadyApplied || first.Logs.TargetRows != 2 || first.Logs.TargetChecksum != input.Checksum {
		t.Fatalf("initial import = %#v, %v", first, err)
	}
	again, err := db.ImportLegacyOperationalLogs(t.Context(), input, now.Add(24*time.Hour))
	if err != nil || !again.AlreadyApplied || again.AsOf != input.AsOf || again.AppliedAt != first.AppliedAt {
		t.Fatalf("idempotent import = %#v, %v", again, err)
	}
	changed := input
	changed.AsOf++
	changed.CutoffAt++
	changed.Logs = input.Logs[1:]
	changed.Checksum = LegacyOperationalLogsChecksum(changed.Logs)
	if _, err := db.ImportLegacyOperationalLogs(t.Context(), changed, now.Add(time.Hour)); !errors.Is(err, ErrConflict) {
		t.Fatalf("different window must conflict: %v", err)
	}
	if _, err := db.db.Exec(`UPDATE legacy_operational_logs SET method='POST' WHERE category='request'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.LookupLegacyOperationalLogsImport(t.Context(), input.SourceSHA256); err == nil || !strings.Contains(err.Error(), "verification") {
		t.Fatalf("replay lookup must detect changed target: %v", err)
	}
}

func TestOperationalImportStatisticsUseBusinessFactsAndShanghaiDays(t *testing.T) {
	db, input, now := operationalImportFixture(t)
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	local := now.In(location)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location).Unix()
	inviter, err := db.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "op-inviter@example.test", PasswordHash: "hash"}, time.Unix(day-1, 0))
	if err != nil {
		t.Fatal(err)
	}
	buyer, err := db.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "op-buyer@example.test", PasswordHash: "hash"}, time.Unix(day, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE users SET invite_user_id=? WHERE id=?`, inviter.ID, buyer.ID); err != nil {
		t.Fatal(err)
	}
	input.UserFactsChecksum = LegacyOperationalUserFactsChecksum([]LegacyOperationalUserFact{{HourAt: (day - 1) / 3600 * 3600, Registered: 1}, {HourAt: day / 3600 * 3600, Registered: 1, Invited: 1}})
	plan, err := db.CreatePlan(t.Context(), SavePlanInput{Name: "Operational fact plan", TransferEnableGiB: 10, Prices: PlanPrices{"monthly": 100}}, now)
	if err != nil {
		t.Fatal(err)
	}
	for index, fact := range []struct{ created, paid, amount, status int64 }{{day - 1, day + 1, 100, 3}, {day + 1, day + 2, 200, 2}, {day + 2, day + 3, 300, 4}, {now.Unix() + 1, 0, 400, 0}} {
		trade := strings.Repeat("0", 24) + string(rune('1'+index))
		if _, err := db.db.Exec(`INSERT INTO orders(id,user_id,plan_id,period,trade_no,original_amount,total_amount,type,status,paid_at,created_at,updated_at) VALUES (?,?,?,'monthly',?,?,?,1,?,?,?,?)`, 101+index, buyer.ID, plan.ID, trade, fact.amount, fact.amount, fact.status, fact.paid, fact.created, fact.created); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.db.Exec(`INSERT INTO commission_logs(order_id,invite_user_id,user_id,trade_no,order_amount,get_amount,created_at,updated_at) VALUES (101,?,?,?,100,17,?,?)`, inviter.ID, buyer.ID, strings.Repeat("0", 24)+"1", day+2, day+2); err != nil {
		t.Fatal(err)
	}
	nodeInput, err := NewBasicAdminNodeDefinitionInput(CreateNodeInput{Name: "stats", Type: "vless", Host: "example.test", Port: "443", Show: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	node, _, err := db.CreateAdminNodeDefinition(t.Context(), nodeInput, time.Unix(day, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`INSERT INTO node_traffic_stats(node_id,record_at,record_type,upload,download,created_at,updated_at) VALUES (?,?,'d',5,7,?,?), (?,?,'m',90,90,?,?)`, node.ID, day, day+1, day+1, node.ID, day+1, now.Unix()+1, now.Unix()+1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`INSERT INTO operational_daily_statistics VALUES (?,'order_total',999999)`, day); err != nil {
		t.Fatal(err)
	}
	report, err := db.ImportLegacyOperationalLogs(t.Context(), input, now)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]map[string]int64{
		day - 86400: {"order_count": 1, "order_total": 100, "register_count": 1},
		day:         {"order_count": 2, "order_total": 500, "paid_count": 2, "paid_total": 400, "commission_count": 1, "commission_total": 17, "register_count": 1, "invite_count": 1, "transfer_used_total": 12},
	}
	rows, err := db.db.Query(`SELECT record_at,metric,value FROM operational_daily_statistics ORDER BY record_at,metric`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]map[string]int64{}
	for rows.Next() {
		var stamp, value int64
		var metric string
		if err := rows.Scan(&stamp, &metric, &value); err != nil {
			t.Fatal(err)
		}
		if got[stamp] == nil {
			got[stamp] = map[string]int64{}
		}
		got[stamp][metric] = value
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if !reflect.DeepEqual(got, want) || report.StatisticsRows != 12 || report.StatisticsTimezone != "Asia/Shanghai" {
		t.Fatalf("statistics = %#v; report=%#v", got, report)
	}
}

func TestOperationalImportFailureRollsBackLogsStatisticsAndReceipt(t *testing.T) {
	db, input, now := operationalImportFixture(t)
	if _, err := db.db.Exec(`INSERT INTO operational_daily_statistics VALUES (57600,'order_count',77); CREATE TRIGGER reject_stats_delete BEFORE DELETE ON operational_daily_statistics BEGIN SELECT RAISE(ABORT,'injected statistics failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ImportLegacyOperationalLogs(t.Context(), input, now); err == nil {
		t.Fatal("expected injected failure")
	}
	assertNoOperationalImport(t, db)
	var value int
	if err := db.db.QueryRow(`SELECT value FROM operational_daily_statistics WHERE record_at=57600 AND metric='order_count'`).Scan(&value); err != nil || value != 77 {
		t.Fatalf("rollback lost prior statistics: value=%d error=%v", value, err)
	}
}

func TestOperationalImportStatisticsPreserveHistoricalShanghaiMidnight(t *testing.T) {
	db, input, now := operationalImportFixture(t)
	// Shanghai observed UTC+9 in July 1990. These UTC instants bracket its
	// local midnight; a fixed current UTC+8 offset assigns the second row wrong.
	midnight := time.Date(1990, 6, 30, 15, 0, 0, 0, time.UTC).Unix()
	for index, stamp := range []int64{0, midnight - 1, midnight} {
		email := []string{"epoch", "before-midnight", "at-midnight"}[index] + "@example.test"
		if _, err := db.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: email, PasswordHash: "hash"}, time.Unix(stamp, 0)); err != nil {
			t.Fatal(err)
		}
	}
	input.UserFactsChecksum = LegacyOperationalUserFactsChecksum([]LegacyOperationalUserFact{{HourAt: 0, Registered: 1}, {HourAt: (midnight - 1) / 3600 * 3600, Registered: 1}, {HourAt: midnight, Registered: 1}})
	if _, err := db.ImportLegacyOperationalLogs(t.Context(), input, now); err != nil {
		t.Fatal(err)
	}
	statistics, err := readOperationalStatistics(t.Context(), db.db)
	if err != nil {
		t.Fatal(err)
	}
	want := []operationalStatistic{
		{RecordAt: -28800, Metric: "register_count", Value: 1},
		{RecordAt: midnight - 86400, Metric: "register_count", Value: 1},
		{RecordAt: midnight, Metric: "register_count", Value: 1},
	}
	if !reflect.DeepEqual(statistics, want) {
		t.Fatalf("Shanghai historical statistics = %#v; want %#v", statistics, want)
	}
}

func TestOperationalImportRequiresCompleteSameSourceUserDomain(t *testing.T) {
	for _, mode := range []string{"missing distributors", "distributors from another source"} {
		t.Run(mode, func(t *testing.T) {
			db, input, now := operationalImportFixture(t)
			if _, err := db.db.Exec(`DELETE FROM legacy_migration_runs WHERE slice=?`, LegacyDistributorsSlice); err != nil {
				t.Fatal(err)
			}
			if mode == "distributors from another source" {
				if _, err := db.ImportLegacyDistributors(t.Context(), LegacyDistributorsImport{
					Slice: LegacyDistributorsSlice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
					Checksum: LegacyDistributorsChecksum(LegacyDistributorsData{}), RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
				}, now); err != nil {
					t.Fatal(err)
				}
				if _, err := db.db.Exec(`UPDATE legacy_migration_runs SET source_sha256=? WHERE slice=?`, strings.Repeat("c", 64), LegacyDistributorsSlice); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.ImportLegacyOperationalLogs(t.Context(), input, now); !errors.Is(err, ErrConflict) {
				t.Fatalf("incomplete same-source user domain must conflict: %v", err)
			}
			assertNoOperationalImport(t, db)
		})
	}
}

func TestOperationalImportIncludesMigratedInternalSubscriberRegistration(t *testing.T) {
	db, input, now := operationalImportFixture(t)
	if _, err := db.db.Exec(`DELETE FROM legacy_migration_runs WHERE slice=?`, LegacyDistributorsSlice); err != nil {
		t.Fatal(err)
	}
	plan, distributor := createDistributorFixture(t, db, now)
	if _, err := db.db.Exec(`INSERT INTO orders(id,user_id,plan_id,period,trade_no,original_amount,total_amount,type,status,callback_no,entitlement_expired_at_after,created_at,updated_at)
	 VALUES (10,?,?,'monthly','2026090812000012345678901',100000,100000,1,3,'distributor_auto',1800000000,?,?)`, distributor.ID, plan.ID, now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	expires := int64(1800000000)
	data := LegacyDistributorsData{
		Subscribers:   []LegacyDistributorSubscriber{{ID: 100, Email: "legacy-subscription@internal.invalid", PasswordHash: "!internal:legacy", UUID: "11111111-2222-4333-8444-555555555555", GroupID: plan.GroupID, PlanID: plan.ID, TransferEnable: 100 * bytesPerGiB, ExpiredAt: &expires, SubscriptionToken: "12345678901234567890123456789012", CreatedAt: now.Unix(), UpdatedAt: now.Unix()}},
		Subscriptions: []LegacyDistributorSubscription{{ID: 20, OriginalOrderID: 10, DistributorUserID: distributor.ID, SubscriberUserID: 100, ClaimTokenHash: strings.Repeat("a", 64), DeliveryStatus: DistributorDeliveryPending, SettlementStatus: DistributorSettlementUnsettled, HWIDEnabled: true, HWIDLimit: 1, CreatedAt: now.Unix(), UpdatedAt: now.Unix()}},
		OrderLinks:    []LegacyDistributorOrderLink{{OrderID: 10, SubscriptionID: 20}},
	}
	report, err := db.ImportLegacyDistributors(t.Context(), LegacyDistributorsImport{Slice: LegacyDistributorsSlice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize, Data: data, Checksum: LegacyDistributorsChecksum(data), RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256}, now)
	if err != nil || report.Subscribers.SourceRows != 1 || report.Subscribers.TargetRows != 1 {
		t.Fatalf("real distributor mapping: %#v %v", report, err)
	}
	var kind string
	var created int64
	if err := db.db.QueryRow(`SELECT account_kind,created_at FROM users WHERE id=100`).Scan(&kind, &created); err != nil || kind != AccountKindInternalSubscription || created != data.Subscribers[0].CreatedAt {
		t.Fatalf("imported subscriber kind=%s created=%d: %v", kind, created, err)
	}
	input.UserFactsChecksum = LegacyOperationalUserFactsChecksum([]LegacyOperationalUserFact{{HourAt: now.Unix() / 3600 * 3600, Registered: 2}})
	// Execute the original StatisticalService User predicates against the
	// source facts, without a not-internal-subscriber scope.
	legacy, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, err := legacy.Exec(`CREATE TABLE v2_user(id INTEGER,created_at INTEGER,invite_user_id INTEGER);
	 INSERT INTO v2_user VALUES (?, ?, NULL),(100,?,NULL)`, distributor.ID, now.Unix(), data.Subscribers[0].CreatedAt); err != nil {
		t.Fatal(err)
	}
	var sourceRegistered int64
	if err := legacy.QueryRow(`SELECT COUNT(*) FROM v2_user WHERE created_at >= ? AND created_at < ?`, now.Unix(), now.Unix()+1).Scan(&sourceRegistered); err != nil || sourceRegistered != 2 {
		t.Fatalf("original source statistics count=%d: %v", sourceRegistered, err)
	}
	// The existing distributor importer cannot preserve an internal user's
	// inviter. A source carrying one must be rejected, not silently undercounted.
	unsupported := input
	unsupported.UserFactsChecksum = LegacyOperationalUserFactsChecksum([]LegacyOperationalUserFact{{HourAt: now.Unix() / 3600 * 3600, Registered: 2, Invited: 1}})
	if _, err := db.ImportLegacyOperationalLogs(t.Context(), unsupported, now); err == nil || !strings.Contains(err.Error(), "user facts verification") {
		t.Fatalf("lost internal invitation must reject migration: %v", err)
	}
	assertNoOperationalImport(t, db)
	first, err := db.ImportLegacyOperationalLogs(t.Context(), input, now)
	if err != nil {
		t.Fatal(err)
	}
	var registered int64
	if err := db.db.QueryRow(`SELECT SUM(value) FROM operational_daily_statistics WHERE metric='register_count'`).Scan(&registered); err != nil || registered != sourceRegistered {
		t.Fatalf("legacy User::count includes distributor and internal subscriber, got %d: %v", registered, err)
	}
	again, err := db.ImportLegacyOperationalLogs(t.Context(), input, now.Add(100*24*time.Hour))
	if err != nil || !again.AlreadyApplied || first.StatisticsChecksum != again.StatisticsChecksum {
		t.Fatalf("complete-domain replay changed statistics: %#v %v", again, err)
	}
}

func TestOperationalImportRejectsSourceUserFactMismatch(t *testing.T) {
	for _, mode := range []string{"missing registration", "missing invitation"} {
		t.Run(mode, func(t *testing.T) {
			db, input, now := operationalImportFixture(t)
			if _, err := db.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "facts@example.test", PasswordHash: "hash"}, now); err != nil {
				t.Fatal(err)
			}
			fact := LegacyOperationalUserFact{HourAt: now.Unix() / 3600 * 3600, Registered: 1}
			if mode == "missing registration" {
				fact.Registered = 2
			} else {
				fact.Invited = 1
			}
			input.UserFactsChecksum = LegacyOperationalUserFactsChecksum([]LegacyOperationalUserFact{fact})
			if _, err := db.ImportLegacyOperationalLogs(t.Context(), input, now); err == nil || !strings.Contains(err.Error(), "user facts verification") {
				t.Fatalf("source/target user facts mismatch must reject success: %v", err)
			}
			assertNoOperationalImport(t, db)
		})
	}
}

func TestOperationalImportRejectsMissingPrerequisiteAndDuplicateMetadata(t *testing.T) {
	for _, mode := range []string{"missing prerequisite", "duplicate metadata", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			db, input, now := operationalImportFixture(t)
			ctx := t.Context()
			switch mode {
			case "missing prerequisite":
				if _, err := db.db.Exec(`DELETE FROM legacy_migration_runs WHERE slice=?`, LegacyNodesSlice); err != nil {
					t.Fatal(err)
				}
			case "duplicate metadata":
				input.Logs = append(input.Logs, input.Logs[0])
				input.Checksum = LegacyOperationalLogsChecksum(input.Logs)
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := db.ImportLegacyOperationalLogs(ctx, input, now); err == nil {
				t.Fatal("expected rejection")
			}
			assertNoOperationalImport(t, db)
		})
	}
}

func assertNoOperationalImport(t *testing.T, db *Store) {
	t.Helper()
	var logs, runs int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM legacy_operational_logs`).Scan(&logs); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyOperationalLogsSlice).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if logs != 0 || runs != 0 {
		t.Fatalf("failed import leaked logs=%d receipts=%d", logs, runs)
	}
}

func TestOperationalImportRejectsStatisticsMutationAndOverflow(t *testing.T) {
	for _, mode := range []string{"mutated aggregate", "integer overflow", "cross-hour integer overflow", "replay drift"} {
		t.Run(mode, func(t *testing.T) {
			db, input, now := operationalImportFixture(t)
			if _, err := db.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "statistics@example.test", PasswordHash: "hash"}, now); err != nil {
				t.Fatal(err)
			}
			input.UserFactsChecksum = LegacyOperationalUserFactsChecksum([]LegacyOperationalUserFact{{HourAt: now.Unix() / 3600 * 3600, Registered: 1}})
			if mode == "mutated aggregate" {
				if _, err := db.db.Exec(`CREATE TRIGGER change_statistic AFTER INSERT ON operational_daily_statistics BEGIN UPDATE operational_daily_statistics SET value=value+1 WHERE record_at=NEW.record_at AND metric=NEW.metric; END`); err != nil {
					t.Fatal(err)
				}
			} else if mode == "integer overflow" || mode == "cross-hour integer overflow" {
				definition, err := NewBasicAdminNodeDefinitionInput(CreateNodeInput{Name: "overflow", Type: "vless", Host: "example.test", Port: "443", Enabled: true, Show: true})
				if err != nil {
					t.Fatal(err)
				}
				node, _, err := db.CreateAdminNodeDefinition(t.Context(), definition, now)
				if err != nil {
					t.Fatal(err)
				}
				download := int64(1)
				if mode == "cross-hour integer overflow" {
					download = 0
					if _, err := db.db.Exec(`INSERT INTO node_traffic_stats(node_id,record_at,record_type,upload,download,created_at,updated_at) VALUES (?,?,'d',1,0,?,?)`, node.ID, now.Unix()-3600, now.Unix()-3600, now.Unix()-3600); err != nil {
						t.Fatal(err)
					}
				}
				if _, err = db.db.Exec(`INSERT INTO node_traffic_stats(node_id,record_at,record_type,upload,download,created_at,updated_at) VALUES (?,?,'d',?,?,?,?)`, node.ID, now.Unix(), int64(math.MaxInt64), download, now.Unix(), now.Unix()); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := db.ImportLegacyOperationalLogs(t.Context(), input, now); err != nil {
					t.Fatal(err)
				}
				if _, err := db.db.Exec(`UPDATE operational_daily_statistics SET value=value+1`); err != nil {
					t.Fatal(err)
				}
				if _, _, err := db.LookupLegacyOperationalLogsImport(t.Context(), input.SourceSHA256); err == nil || !strings.Contains(err.Error(), "statistics target verification") {
					t.Fatalf("statistics drift not rejected: %v", err)
				}
				return
			}
			if _, err := db.ImportLegacyOperationalLogs(t.Context(), input, now); err == nil {
				t.Fatal("invalid aggregate accepted")
			}
			assertNoOperationalImport(t, db)
		})
	}
}

func TestOperationalImportConcurrentConnectionsCommitOneReceipt(t *testing.T) {
	db, input, now := operationalImportFixture(t)
	var sequence int
	var name, path string
	if err := db.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	other, err := OpenSQLite("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	start := make(chan struct{})
	var group sync.WaitGroup
	reports := make(chan LegacyOperationalLogsImportReport, 2)
	errs := make(chan error, 2)
	for _, connection := range []*Store{db, other} {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			report, err := connection.ImportLegacyOperationalLogs(t.Context(), input, now)
			reports <- report
			errs <- err
		}()
	}
	close(start)
	group.Wait()
	close(reports)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	applied := 0
	for report := range reports {
		if !report.AlreadyApplied {
			applied++
		}
		if report.Logs.TargetRows != 2 {
			t.Fatalf("partial report=%#v", report)
		}
	}
	var receipts int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, input.Slice).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if applied != 1 || receipts != 1 {
		t.Fatalf("new applications=%d persisted receipts=%d", applied, receipts)
	}
}
