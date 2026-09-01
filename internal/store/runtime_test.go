package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNodeRuntimeUsersPreserveAvailabilityRules(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	ensureTestServerGroups(t, database, now, 7, 8, 9)
	machine, _, err := database.CreateMachine(ctx, CreateMachineInput{Name: "runtime-users", IsActive: true}, now)
	if err != nil {
		t.Fatalf("CreateMachine() error = %v", err)
	}
	node, err := database.CreateNode(ctx, CreateNodeInput{
		Name: "users-node", Type: "vless", Host: "users.example.test", Port: "443",
		Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE nodes SET runtime_config = NULL WHERE id = ?`, node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetNodeRuntime(ctx, node.ID); !errors.Is(err, ErrRuntimeNotConfigured) {
		t.Fatalf("GetNodeRuntime(unconfigured) error = %v, want ErrRuntimeNotConfigured", err)
	}
	if _, err := database.SaveNodeRuntime(ctx, node.ID, SaveNodeRuntimeInput{
		RateMicros: 1_500_000,
		GroupIDs:   []int64{9, 7, 9},
		Config:     []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}`),
	}, now); err != nil {
		t.Fatalf("SaveNodeRuntime() error = %v", err)
	}

	available := createRuntimeUser(t, database, now, "available", 7, 100, 10, 20, nil, false)
	createRuntimeUser(t, database, now, "other-group", 8, 100, 0, 0, nil, false)
	createRuntimeUser(t, database, now, "banned", 7, 100, 0, 0, nil, true)
	expired := now.Add(-time.Second)
	createRuntimeUser(t, database, now, "expired", 7, 100, 0, 0, &expired, false)
	createRuntimeUser(t, database, now, "exhausted", 7, 30, 10, 20, nil, false)
	expiresNow := now
	boundary := createRuntimeUser(t, database, now, "boundary", 9, 100, 0, 0, &expiresNow, false)

	users, err := database.ListNodeRuntimeUsers(ctx, node.ID, now)
	if err != nil {
		t.Fatalf("ListNodeRuntimeUsers() error = %v", err)
	}
	if len(users) != 2 || users[0].ID != available.ID || users[1].ID != boundary.ID {
		t.Fatalf("runtime users = %#v, want available and inclusive expiry boundary", users)
	}
	runtime, err := database.GetNodeRuntime(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNodeRuntime() error = %v", err)
	}
	if len(runtime.GroupIDs) != 2 || runtime.GroupIDs[0] != 7 || runtime.GroupIDs[1] != 9 || runtime.RateMicros != 1_500_000 {
		t.Fatalf("runtime metadata = %#v", runtime)
	}
}

func TestSchemaV2MigrationPreservesFirstSliceData(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "migration.db")
	database, err := OpenSQLite("file:" + filepath.ToSlash(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	if _, err := database.db.ExecContext(ctx, schemaV1); err != nil {
		t.Fatalf("apply legacy schema: %v", err)
	}
	if _, err := database.db.ExecContext(ctx, `PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC).Unix()
	result, err := database.db.ExecContext(ctx, `
		INSERT INTO server_machines (name, notes, is_active, created_at, updated_at)
		VALUES ('preserved-machine', '', 1, ?, ?)
	`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	machineID, _ := result.LastInsertId()
	result, err = database.db.ExecContext(ctx, `
		INSERT INTO nodes (name, type, host, port, show, enabled, sort, machine_id, created_at, updated_at)
		VALUES ('preserved-node', 'vless', 'preserved.example.test', '443', 1, 1, 0, ?, ?, ?)
	`, machineID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	nodeID, _ := result.LastInsertId()

	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v1 to current) error = %v", err)
	}
	node, err := database.GetNode(ctx, nodeID)
	if err != nil || node.Name != "preserved-node" || node.MachineID == nil || *node.MachineID != machineID || node.Rate != 1 || !node.RuntimeConfigured {
		t.Fatalf("migrated node = %#v, err=%v", node, err)
	}
	var version int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("schema version = %d, err=%v", version, err)
	}
}

func TestMigrationRejectsNewerSchemaVersion(t *testing.T) {
	database, err := OpenSQLite("file:newer-schema?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	newerVersion := currentSchemaVersion + 1
	if _, err := database.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, newerVersion)); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(context.Background()); err == nil {
		t.Fatal("Migrate() accepted a schema created by a newer application version")
	}
	var version int
	if err := database.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != newerVersion {
		t.Fatalf("schema version after rejection = %d, err=%v", version, err)
	}
}

func TestNodeReportTrafficIsAtomicAndIdempotentAcrossConcurrency(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	machine, node := createReportingNode(t, database, now)
	user := createRuntimeUser(t, database, now, "traffic", 7, 1_000_000, 0, 0, nil, false)

	input := NodeReportInput{
		MachineID: machine.ID,
		NodeID:    node.ID,
		ReportID:  "72b3e058-0f19-4cb5-83b6-c7e4d4fe00a9",
		Traffic:   map[int64]TrafficUsage{user.ID: {Upload: 10, Download: 20}},
		Alive:     map[int64][]string{user.ID: {"192.0.2.10", "2001:db8::10"}},
		Online:    map[int64]int64{user.ID: 2},
		Status:    []byte(`{"cpu":10,"mem":{"total":100,"used":20},"swap":{"total":0,"used":0},"disk":{"total":1000,"used":100}}`),
		Metrics:   []byte(`{"active_connections":2}`),
		Now:       now,
	}
	first, err := database.ApplyNodeReport(ctx, input)
	if err != nil || first.DuplicateTraffic {
		t.Fatalf("ApplyNodeReport(first) = (%#v, %v)", first, err)
	}
	second, err := database.ApplyNodeReport(ctx, input)
	if err != nil || !second.DuplicateTraffic {
		t.Fatalf("ApplyNodeReport(duplicate) = (%#v, %v)", second, err)
	}
	assertTrafficTotals(t, database, user.ID, node.ID, 15, 30, 10, 20)

	concurrent := input
	concurrent.ReportID = "1eb19642-2ba5-431f-9258-a167bb161c34"
	var applied atomic.Int32
	var wait sync.WaitGroup
	errorsFound := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := database.ApplyNodeReport(ctx, concurrent)
			if err != nil {
				errorsFound <- err
				return
			}
			if !result.DuplicateTraffic {
				applied.Add(1)
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent ApplyNodeReport() error = %v", err)
	}
	if applied.Load() != 1 {
		t.Fatalf("concurrent report applied %d times, want 1", applied.Load())
	}
	assertTrafficTotals(t, database, user.ID, node.ID, 30, 60, 20, 40)

	var receipts, userStats, nodeStats, devices int
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM node_report_receipts WHERE node_id = ?`, node.ID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM user_traffic_stats WHERE user_id = ?`, user.ID).Scan(&userStats); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM node_traffic_stats WHERE node_id = ?`, node.ID).Scan(&nodeStats); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM node_device_ips WHERE node_id = ? AND user_id = ?`, node.ID, user.ID).Scan(&devices); err != nil {
		t.Fatal(err)
	}
	if receipts != 2 || userStats != 1 || nodeStats != 1 || devices != 2 {
		t.Fatalf("receipt/stats/devices counts = %d/%d/%d/%d", receipts, userStats, nodeStats, devices)
	}
}

func TestNodeReportCombinedUserTrafficInt64BoundaryIsAtomic(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	machine, node := createReportingNode(t, database, now)
	user := createRuntimeUser(t, database, now, "combined-overflow", 7, 1_000_000, 0, 0, nil, false)
	maximum := int64(^uint64(0) >> 1)
	if _, err := database.db.ExecContext(ctx, `
		UPDATE users SET transfer_enable = ?, traffic_u = ?, traffic_d = 0 WHERE id = ?
	`, maximum, maximum-1, user.ID); err != nil {
		t.Fatal(err)
	}

	first, err := database.ApplyNodeReport(ctx, NodeReportInput{
		MachineID: machine.ID,
		NodeID:    node.ID,
		ReportID:  "2c986c88-c082-48a6-806c-7ad61da85dc5",
		Traffic:   map[int64]TrafficUsage{user.ID: {Download: 1}},
		Now:       now,
	})
	if err != nil || first.DuplicateTraffic {
		t.Fatalf("ApplyNodeReport(boundary) = (%#v, %v)", first, err)
	}

	_, err = database.ApplyNodeReport(ctx, NodeReportInput{
		MachineID: machine.ID,
		NodeID:    node.ID,
		ReportID:  "c108a66b-9c49-4a36-8c3a-e56c1d8be991",
		Traffic:   map[int64]TrafficUsage{user.ID: {Download: 1}},
		Now:       now.Add(time.Second),
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ApplyNodeReport() error = %v, want ErrInvalidInput", err)
	}

	var upload, download int64
	if err := database.db.QueryRowContext(ctx, `SELECT traffic_u, traffic_d FROM users WHERE id = ?`, user.ID).Scan(&upload, &download); err != nil {
		t.Fatal(err)
	}
	if upload != maximum-1 || download != 1 {
		t.Fatalf("user traffic after rejected report = %d/%d", upload, download)
	}
	detail, err := database.GetAdminUser(ctx, user.ID)
	if err != nil || detail.TrafficUsed != maximum {
		t.Fatalf("GetAdminUser() traffic used = %d, error = %v", detail.TrafficUsed, err)
	}
	var receipts, userStats, nodeStats int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_report_receipts WHERE node_id = ?`, node.ID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_traffic_stats WHERE user_id = ?`, user.ID).Scan(&userStats); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_traffic_stats WHERE node_id = ?`, node.ID).Scan(&nodeStats); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 || userStats != 1 || nodeStats != 1 {
		t.Fatalf("rejected report left receipt/user/node stats = %d/%d/%d", receipts, userStats, nodeStats)
	}
}

func TestNodeReportRecordsFirstDistributorConnectionAfterConfigIssued(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 19, 0, 0, 0, time.UTC)
	plan, distributor := createDistributorFixture(t, database, now)
	created, err := database.CreateDistributorOrder(ctx, CreateDistributorOrderInput{
		DistributorUserID: distributor.ID, PlanID: plan.ID, Period: "monthly",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	withoutConfig, err := database.CreateDistributorOrder(ctx, CreateDistributorOrderInput{
		DistributorUserID: distributor.ID, PlanID: plan.ID, Period: "monthly",
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.MarkDistributorSubscriptionClaimed(ctx, created.Subscription.ID, "192.0.2.1", "test-client", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkDistributorConfigIssued(ctx, created.Subscription.ID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	machine, _, err := database.CreateMachine(ctx, CreateMachineInput{Name: "distributor-connection-machine", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	createNode := func(name string) Node {
		node, createErr := database.CreateNode(ctx, CreateNodeInput{
			Name: name, Type: "vless", Host: name + ".example.test", Port: "443", Show: true, Enabled: true, MachineID: &machine.ID,
		}, now)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, saveErr := database.SaveNodeRuntime(ctx, node.ID, SaveNodeRuntimeInput{
			RateMicros: 1_000_000, GroupIDs: []int64{*plan.GroupID},
			Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}`),
		}, now); saveErr != nil {
			t.Fatal(saveErr)
		}
		return node
	}
	firstNode := createNode("first-distributor-edge")
	firstReportAt := now.Add(3 * time.Second)
	if _, err := database.ApplyNodeReport(ctx, NodeReportInput{
		MachineID: machine.ID, NodeID: firstNode.ID, ReportID: "18c9ce5d-15c6-49c1-8e65-fe2d8ccaf732",
		Traffic: map[int64]TrafficUsage{
			created.Subscription.SubscriberUserID:       {Download: 1},
			withoutConfig.Subscription.SubscriberUserID: {Upload: 1},
		}, Now: firstReportAt,
	}); err != nil {
		t.Fatal(err)
	}
	connected, err := database.GetDistributorOrderByTradeNo(ctx, distributor.ID, created.Order.TradeNo, firstReportAt)
	if err != nil || connected.Subscription.ConnectedAt == nil || !connected.Subscription.ConnectedAt.Equal(firstReportAt) ||
		connected.Subscription.ConnectedNodeID == nil || *connected.Subscription.ConnectedNodeID != firstNode.ID ||
		connected.Subscription.ConnectedNodeName == nil || *connected.Subscription.ConnectedNodeName != firstNode.Name {
		t.Fatalf("first connection = %#v err=%v", connected.Subscription, err)
	}
	pending, err := database.GetDistributorOrderByTradeNo(ctx, distributor.ID, withoutConfig.Order.TradeNo, firstReportAt)
	if err != nil || pending.Subscription.ConnectedAt != nil {
		t.Fatalf("connection before config issued = %#v err=%v", pending.Subscription, err)
	}

	secondNode := createNode("second-distributor-edge")
	if _, err := database.ApplyNodeReport(ctx, NodeReportInput{
		MachineID: machine.ID, NodeID: secondNode.ID, ReportID: "8a4645ab-106d-45b2-a441-0d6b8b7cd845",
		Traffic: map[int64]TrafficUsage{created.Subscription.SubscriberUserID: {Upload: 2}}, Now: now.Add(4 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	unchanged, err := database.GetDistributorOrderByTradeNo(ctx, distributor.ID, created.Order.TradeNo, now.Add(4*time.Second))
	if err != nil || unchanged.Subscription.ConnectedNodeID == nil || *unchanged.Subscription.ConnectedNodeID != firstNode.ID ||
		unchanged.Subscription.ConnectedAt == nil || !unchanged.Subscription.ConnectedAt.Equal(firstReportAt) {
		t.Fatalf("first connection was overwritten = %#v err=%v", unchanged.Subscription, err)
	}
}

func TestNodeReportRejectsUsersOutsideNodeGroups(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	machine, node := createReportingNode(t, database, now)
	foreignUser := createRuntimeUser(t, database, now, "foreign-group", 8, 1_000_000, 0, 0, nil, false)

	_, err := database.ApplyNodeReport(ctx, NodeReportInput{
		MachineID: machine.ID,
		NodeID:    node.ID,
		ReportID:  "06a98744-6297-4590-87f6-0146b7be4559",
		Traffic:   map[int64]TrafficUsage{foreignUser.ID: {Upload: 10, Download: 20}},
		Alive:     map[int64][]string{foreignUser.ID: {"192.0.2.80"}},
		Online:    map[int64]int64{foreignUser.ID: 1},
		Now:       now,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ApplyNodeReport(cross-group) error = %v, want ErrInvalidInput", err)
	}
	assertTrafficTotals(t, database, foreignUser.ID, node.ID, 0, 0, 0, 0)
	var receipts, devices int
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM node_report_receipts WHERE node_id = ?`, node.ID).Scan(&receipts)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM node_device_ips WHERE node_id = ?`, node.ID).Scan(&devices)
	if receipts != 0 || devices != 0 {
		t.Fatalf("rejected report left receipts/devices = %d/%d", receipts, devices)
	}
}

func TestNodeReportBindsReportIDToTrafficAndIgnoresRetryUserState(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	machine, node := createReportingNode(t, database, now)
	user := createRuntimeUser(t, database, now, "bound-report", 7, 1_000_000, 0, 0, nil, false)
	original := NodeReportInput{
		MachineID: machine.ID,
		NodeID:    node.ID,
		ReportID:  "c127a5bf-14dc-4cc4-bdb8-66fe57378c73",
		Traffic:   map[int64]TrafficUsage{user.ID: {Upload: 10, Download: 20}},
		Alive:     map[int64][]string{user.ID: {"192.0.2.10"}},
		Online:    map[int64]int64{user.ID: 1},
		Now:       now,
	}
	if result, err := database.ApplyNodeReport(ctx, original); err != nil || result.DuplicateTraffic {
		t.Fatalf("ApplyNodeReport(original) = (%#v, %v)", result, err)
	}

	retry := original
	retry.Now = now.Add(time.Minute)
	retry.Alive = map[int64][]string{user.ID: {"192.0.2.11"}}
	retry.Online = map[int64]int64{user.ID: 2}
	if result, err := database.ApplyNodeReport(ctx, retry); err != nil || !result.DuplicateTraffic {
		t.Fatalf("ApplyNodeReport(retry) = (%#v, %v)", result, err)
	}
	devices, err := database.ListUserDevices(ctx, []int64{user.ID}, retry.Now)
	if err != nil || len(devices[user.ID]) != 1 || devices[user.ID][0] != "192.0.2.10" {
		t.Fatalf("retry devices = %#v, err=%v; want original snapshot", devices, err)
	}

	tampered := original
	tampered.Traffic = map[int64]TrafficUsage{user.ID: {Upload: 11, Download: 20}}
	if _, err := database.ApplyNodeReport(ctx, tampered); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ApplyNodeReport(tampered) error = %v, want ErrInvalidInput", err)
	}
	assertTrafficTotals(t, database, user.ID, node.ID, 15, 30, 10, 20)
}

func TestNodeReportExpiryReconcilesPersistedOnlineCount(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	machine, node := createReportingNode(t, database, now)
	user := createRuntimeUser(t, database, now, "expiring-device", 7, 1_000_000, 0, 0, nil, false)
	if _, err := database.ApplyNodeReport(ctx, NodeReportInput{
		MachineID: machine.ID, NodeID: node.ID,
		Alive: map[int64][]string{user.ID: {"192.0.2.50"}}, Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	var online int
	if err := database.db.QueryRow(`SELECT online_count FROM users WHERE id = ?`, user.ID).Scan(&online); err != nil || online != 1 {
		t.Fatalf("online_count after report = %d, err=%v", online, err)
	}
	result, err := database.ApplyNodeReport(ctx, NodeReportInput{
		MachineID: machine.ID, NodeID: node.ID, Now: now.Add(deviceStateTTL),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.DeviceUserIDs) != 1 || result.DeviceUserIDs[0] != user.ID {
		t.Fatalf("expired device users = %#v, want [%d]", result.DeviceUserIDs, user.ID)
	}
	if err := database.db.QueryRow(`SELECT online_count FROM users WHERE id = ?`, user.ID).Scan(&online); err != nil || online != 0 {
		t.Fatalf("online_count after expiry = %d, err=%v; want 0", online, err)
	}
}

func TestNodeReportAndDeviceLookupCrossQueryBatchBoundary(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	machine, node := createReportingNode(t, database, now)
	const userCount = 501
	userIDs := make([]int64, 0, userCount)
	alive := make(map[int64][]string, userCount)
	for index := range userCount {
		user := createRuntimeUser(t, database, now, fmt.Sprintf("batch-device-%03d", index), 7, 1_000_000, 0, 0, nil, false)
		userIDs = append(userIDs, user.ID)
		alive[user.ID] = []string{"192.0.2.90"}
	}
	if _, err := database.ApplyNodeReport(ctx, NodeReportInput{
		MachineID: machine.ID, NodeID: node.ID, Alive: alive, Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	devices, err := database.ListUserDevices(ctx, userIDs, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != userCount {
		t.Fatalf("device users = %d, want %d", len(devices), userCount)
	}
	for _, userID := range userIDs {
		if len(devices[userID]) != 1 || devices[userID][0] != "192.0.2.90" {
			t.Fatalf("user %d devices = %#v", userID, devices[userID])
		}
	}
}

func TestListRuntimeNodeIDsForUsersTargetsEnabledMatchingGroups(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	machine, matching := createReportingNode(t, database, now)
	ensureTestServerGroups(t, database, now, 8)
	other, err := database.CreateNode(ctx, CreateNodeInput{
		Name: "other-group-node", Type: "vless", Host: "other-group.example.test", Port: "8443",
		Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(ctx, other.ID, SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{8},
		Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":8443}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	disabled, err := database.CreateNode(ctx, CreateNodeInput{
		Name: "disabled-matching-node", Type: "vless", Host: "disabled-matching.example.test", Port: "9443",
		Show: true, Enabled: false, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(ctx, disabled.ID, SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{7},
		Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":9443}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	user := createRuntimeUser(t, database, now, "device-target", 7, 1_000_000, 0, 0, nil, false)
	nodeIDs, err := database.ListRuntimeNodeIDsForUsers(ctx, []int64{user.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodeIDs) != 1 || nodeIDs[0] != matching.ID {
		t.Fatalf("target node ids = %#v, want [%d]", nodeIDs, matching.ID)
	}
}

func TestListRuntimeNodeGroupTargetsForGroupsPreservesMembershipWithoutDuplicates(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	machine, first := createReportingNode(t, database, now)
	ensureTestServerGroups(t, database, now, 8)
	second, err := database.CreateNode(ctx, CreateNodeInput{
		Name: "multi-group-node", Type: "vless", Host: "multi-group.example.test", Port: "8443",
		Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(ctx, second.ID, SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{7, 8},
		Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":8443}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	targets, err := database.ListRuntimeNodeGroupTargetsForGroups(ctx, []int64{8, 7, 7})
	if err != nil {
		t.Fatal(err)
	}
	want := []NodeRuntimeGroupTarget{
		{NodeID: first.ID, MachineID: machine.ID, GroupID: 7},
		{NodeID: second.ID, MachineID: machine.ID, GroupID: 7},
		{NodeID: second.ID, MachineID: machine.ID, GroupID: 8},
	}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("group targets = %#v, want %#v", targets, want)
	}
	if _, err := database.ListRuntimeNodeGroupTargetsForGroups(ctx, []int64{0}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid group error = %v", err)
	}
}

func TestNodeReportRollsBackReceiptAndPriorWritesOnFailure(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	machine, node := createReportingNode(t, database, now)
	user := createRuntimeUser(t, database, now, "rollback", 7, 1_000_000, 0, 0, nil, false)
	if _, err := database.db.Exec(`
		CREATE TRIGGER reject_node_traffic_update
		BEFORE UPDATE OF traffic_u, traffic_d ON nodes
		BEGIN
			SELECT RAISE(ABORT, 'forced node traffic failure');
		END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	_, err := database.ApplyNodeReport(ctx, NodeReportInput{
		MachineID: machine.ID,
		NodeID:    node.ID,
		ReportID:  "fffc3f89-26bb-44c3-a838-8ea27318d677",
		Traffic:   map[int64]TrafficUsage{user.ID: {Upload: 10, Download: 20}},
		Now:       now,
	})
	if err == nil {
		t.Fatal("ApplyNodeReport() unexpectedly succeeded through failure trigger")
	}
	assertTrafficTotals(t, database, user.ID, node.ID, 0, 0, 0, 0)
	var receipts, stageRows int
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM node_report_receipts`).Scan(&receipts)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM node_report_traffic_stage`).Scan(&stageRows)
	if receipts != 0 || stageRows != 0 {
		t.Fatalf("rollback left receipts/stage rows = %d/%d", receipts, stageRows)
	}
}

func TestNodeReportReceiptSurvivesStoreRestart(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "runtime.db")
	open := func() *Store {
		database, err := OpenSQLite("file:" + filepath.ToSlash(databasePath))
		if err != nil {
			t.Fatalf("OpenSQLite() error = %v", err)
		}
		if err := database.Migrate(context.Background()); err != nil {
			database.Close()
			t.Fatalf("Migrate() error = %v", err)
		}
		return database
	}
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	database := open()
	machine, node := createReportingNode(t, database, now)
	user := createRuntimeUser(t, database, now, "restart", 7, 1_000_000, 0, 0, nil, false)
	input := NodeReportInput{
		MachineID: machine.ID, NodeID: node.ID, ReportID: "efb9393d-54cc-4f78-91e1-f2cab1af0883",
		Traffic: map[int64]TrafficUsage{user.ID: {Upload: 10, Download: 20}}, Now: now,
	}
	if _, err := database.ApplyNodeReport(context.Background(), input); err != nil {
		database.Close()
		t.Fatalf("ApplyNodeReport(first) error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	database = open()
	defer database.Close()
	result, err := database.ApplyNodeReport(context.Background(), input)
	if err != nil || !result.DuplicateTraffic {
		t.Fatalf("ApplyNodeReport(after restart) = (%#v, %v)", result, err)
	}
	assertTrafficTotals(t, database, user.ID, node.ID, 15, 30, 10, 20)
}

func TestOldDuplicateReceiptSurvivesAnotherLostResponse(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	machine, node := createReportingNode(t, database, now)
	user := createRuntimeUser(t, database, now, "old-retry", 7, 1_000_000, 0, 0, nil, false)
	input := NodeReportInput{
		MachineID: machine.ID, NodeID: node.ID, ReportID: "2cb273f0-ab30-4383-ac17-51eef02550a7",
		Traffic: map[int64]TrafficUsage{user.ID: {Upload: 10, Download: 20}}, Now: now,
	}
	if _, err := database.ApplyNodeReport(ctx, input); err != nil {
		t.Fatal(err)
	}
	for _, retryAt := range []time.Time{now.Add(8 * 24 * time.Hour), now.Add(8*24*time.Hour + time.Minute)} {
		input.Now = retryAt
		result, err := database.ApplyNodeReport(ctx, input)
		if err != nil || !result.DuplicateTraffic {
			t.Fatalf("ApplyNodeReport(retry at %s) = (%#v, %v)", retryAt, result, err)
		}
	}
	assertTrafficTotals(t, database, user.ID, node.ID, 15, 30, 10, 20)
}

func createReportingNode(t testing.TB, database *Store, now time.Time) (Machine, Node) {
	t.Helper()
	ensureTestServerGroups(t, database, now, 7)
	machine, _, err := database.CreateMachine(context.Background(), CreateMachineInput{Name: "report-machine", IsActive: true}, now)
	if err != nil {
		t.Fatalf("CreateMachine() error = %v", err)
	}
	node, err := database.CreateNode(context.Background(), CreateNodeInput{
		Name: "report-node", Type: "vless", Host: "report.example.test", Port: "443",
		Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if _, err := database.SaveNodeRuntime(context.Background(), node.ID, SaveNodeRuntimeInput{
		RateMicros: 1_500_000,
		GroupIDs:   []int64{7},
		Config:     []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}`),
	}, now); err != nil {
		t.Fatalf("SaveNodeRuntime() error = %v", err)
	}
	return machine, node
}

func createRuntimeUser(t *testing.T, database *Store, now time.Time, name string, groupID, transferEnable, upload, download int64, expiredAt *time.Time, banned bool) RuntimeUser {
	t.Helper()
	ensureTestServerGroups(t, database, now, groupID)
	user, err := database.CreateRuntimeUser(context.Background(), CreateRuntimeUserInput{
		Email: name + "@example.test", PasswordHash: "test-password-hash", UUID: uuid.NewString(),
		GroupID: groupID, TransferEnable: transferEnable, TrafficUpload: upload, TrafficDownload: download,
		ExpiredAt: expiredAt, SpeedLimit: 100, DeviceLimit: 2, Banned: banned,
	}, now)
	if err != nil {
		t.Fatalf("CreateRuntimeUser(%s) error = %v", name, err)
	}
	return user
}

func ensureTestServerGroups(t testing.TB, database *Store, now time.Time, groupIDs ...int64) {
	t.Helper()
	for _, groupID := range groupIDs {
		if _, err := database.db.ExecContext(context.Background(), `
			INSERT OR IGNORE INTO server_groups (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)
		`, groupID, fmt.Sprintf("Test group %d", groupID), now.Unix(), now.Unix()); err != nil {
			t.Fatalf("ensure test server group %d: %v", groupID, err)
		}
	}
}

func assertTrafficTotals(t *testing.T, database *Store, userID, nodeID, userUpload, userDownload, nodeUpload, nodeDownload int64) {
	t.Helper()
	userTraffic, err := database.GetRuntimeUserTraffic(context.Background(), userID)
	if err != nil || userTraffic.Upload != userUpload || userTraffic.Download != userDownload {
		t.Fatalf("user traffic = %#v, err=%v; want %d/%d", userTraffic, err, userUpload, userDownload)
	}
	node, err := database.GetNode(context.Background(), nodeID)
	if err != nil || node.TrafficUpload != nodeUpload || node.TrafficDownload != nodeDownload {
		t.Fatalf("node traffic = %#v, err=%v; want %d/%d", node, err, nodeUpload, nodeDownload)
	}
}

func BenchmarkApplyNodeReport(b *testing.B) {
	for _, users := range []int{1_000, 10_000} {
		b.Run(fmt.Sprintf("users-%d", users), func(b *testing.B) {
			database, err := OpenSQLite("file:" + filepath.ToSlash(filepath.Join(b.TempDir(), "benchmark.db")))
			if err != nil {
				b.Fatal(err)
			}
			defer database.Close()
			ctx := context.Background()
			if err := database.Migrate(ctx); err != nil {
				b.Fatal(err)
			}
			now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
			machine, node := createReportingNode(b, database, now)
			tx, err := database.db.BeginTx(ctx, nil)
			if err != nil {
				b.Fatal(err)
			}
			statement, err := tx.PrepareContext(ctx, `
				INSERT INTO users (email, password_hash, banned, uuid, group_id, transfer_enable, subscription_token, created_at, updated_at)
				VALUES (?, 'benchmark-password-hash', 0, ?, 7, 9223372036854775807, ?, ?, ?)
			`)
			if err != nil {
				b.Fatal(err)
			}
			traffic := make(map[int64]TrafficUsage, users)
			for index := 1; index <= users; index++ {
				result, err := statement.ExecContext(ctx,
					fmt.Sprintf("benchmark-%d@example.test", index),
					fmt.Sprintf("%08x-0000-4000-8000-%012x", index, index), testSubscriptionToken(b), now.Unix(), now.Unix())
				if err != nil {
					b.Fatal(err)
				}
				userID, _ := result.LastInsertId()
				traffic[userID] = TrafficUsage{Upload: 1, Download: 2}
			}
			if err := statement.Close(); err != nil {
				b.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				if _, err := database.ApplyNodeReport(ctx, NodeReportInput{
					MachineID: machine.ID, NodeID: node.ID,
					ReportID: fmt.Sprintf("benchmark-%d", iteration), Traffic: traffic, Now: now,
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
