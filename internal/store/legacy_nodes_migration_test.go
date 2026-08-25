package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/google/uuid"
)

func TestImportLegacyNodesPreservesMachineNodeScheduleAndDynamicTrafficBehavior(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 25, 8, 30, 0, 0, time.UTC)
	ensureTestServerGroups(t, database, now, 3, 9)
	if _, err := database.db.ExecContext(ctx, `INSERT INTO routing_rules (id,remarks,match_json,action,action_value,created_at,updated_at) VALUES (12,'direct','["domain:example.test"]','direct','',?,?)`, now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	input := validLegacyNodesImport()
	report, err := database.ImportLegacyNodes(ctx, input, now)
	if err != nil {
		t.Fatalf("ImportLegacyNodes() error = %v", err)
	}
	if report.AlreadyApplied || report.Machines.SourceRows != 1 || report.Machines.TargetRows != 1 || report.Nodes.SourceRows != 1 || report.Nodes.TargetRows != 1 || report.Schedules.TargetRows != 1 || report.Traffic.TargetRows != 1 {
		t.Fatalf("report = %#v", report)
	}
	machine, err := database.AuthenticateMachine(ctx, 7, "known-machine-token", now)
	if err != nil || machine.ID != 7 || machine.Name != "legacy-machine" {
		t.Fatalf("AuthenticateMachine() = %#v, %v", machine, err)
	}
	node, err := database.GetNode(ctx, 41)
	if err != nil || node.MachineID == nil || *node.MachineID != 7 || !node.RuntimeConfigured || node.Rate != 1.25 || node.TrafficUpload != 5 || node.TrafficDownload != 7 {
		t.Fatalf("node = %#v, err=%v", node, err)
	}
	runtime, err := database.GetNodeRuntime(ctx, 41)
	if err != nil || len(runtime.GroupIDs) != 2 || len(runtime.RouteIDs) != 1 || runtime.Config == nil {
		t.Fatalf("runtime = %#v, err=%v", runtime, err)
	}
	schedule, err := database.GetActivationSchedule(ctx, 41)
	if err != nil || schedule.ScheduleType != "daily" || schedule.EnableTime != "08:00" || schedule.DisableTime != "20:00" {
		t.Fatalf("schedule = %#v, err=%v", schedule, err)
	}

	user, err := database.CreateRuntimeUser(ctx, CreateRuntimeUserInput{
		Email: "dynamic-rate@example.test", PasswordHash: "test-password-hash", UUID: uuid.NewString(),
		GroupID: 3, TransferEnable: 1_000_000,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.ApplyNodeReport(ctx, NodeReportInput{
		MachineID: 7, NodeID: 41, ReportID: uuid.NewString(), Now: now,
		Traffic: map[int64]TrafficUsage{user.ID: {Upload: 10, Download: 20}},
	})
	if err != nil {
		t.Fatalf("ApplyNodeReport() error = %v", err)
	}
	usage, err := database.GetRuntimeUserTraffic(ctx, user.ID)
	if err != nil || usage.Upload != 20 || usage.Download != 40 {
		t.Fatalf("dynamic-rate traffic = %#v, err=%v; want 20/40", usage, err)
	}
	var recordAt int64
	if err := database.db.QueryRowContext(ctx, `SELECT record_at FROM node_traffic_stats WHERE node_id = 41 AND upload = 10 AND download = 20`).Scan(&recordAt); err != nil {
		t.Fatal(err)
	}
	wantRecordAt := time.Date(2026, 8, 25, 0, 0, 0, 0, nodeRateLocation).Unix()
	if recordAt != wantRecordAt {
		t.Fatalf("daily traffic record_at = %d, want Asia/Shanghai midnight %d", recordAt, wantRecordAt)
	}

	repeated, err := database.ImportLegacyNodes(ctx, input, now.Add(time.Hour))
	if err != nil || !repeated.AlreadyApplied || repeated.AppliedAt != report.AppliedAt {
		t.Fatalf("idempotent import = %#v, err=%v", repeated, err)
	}
}

func TestImportLegacyNodesRejectsNonEmptyTargetAtomically(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	ensureTestServerGroups(t, database, now, 3, 9)
	if _, err := database.db.ExecContext(ctx, `INSERT INTO routing_rules (id,remarks,match_json,action,action_value,created_at,updated_at) VALUES (12,'direct','["domain:example.test"]','direct','',?,?)`, now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.CreateMachine(ctx, CreateMachineInput{Name: "existing", IsActive: true}, now); err != nil {
		t.Fatal(err)
	}
	_, err := database.ImportLegacyNodes(ctx, validLegacyNodesImport(), now)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("ImportLegacyNodes() error = %v, want ErrConflict", err)
	}
	var imported int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE id = 41`).Scan(&imported); err != nil || imported != 0 {
		t.Fatalf("partial imported nodes = %d, err=%v", imported, err)
	}
}

func TestNodeRateMicrosAtPreservesLegacyInclusiveMinuteWindows(t *testing.T) {
	ranges := `[{"start":"08:00","end":"09:00","rate":2},{"start":"23:00","end":"01:00","rate":3},{"start":"10:00","end":"10:30","rate":0}]`
	for _, scenario := range []struct {
		name       string
		now        time.Time
		enabled    bool
		wantMicros int64
	}{
		{name: "disabled", now: time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC), enabled: false, wantMicros: 1_250_000},
		{name: "inclusive start", now: time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC), enabled: true, wantMicros: 2_000_000},
		{name: "inclusive end", now: time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC), enabled: true, wantMicros: 2_000_000},
		{name: "zero rate", now: time.Date(2026, 8, 25, 2, 15, 0, 0, time.UTC), enabled: true, wantMicros: 0},
		{name: "cross midnight does not match", now: time.Date(2026, 8, 25, 16, 30, 0, 0, time.UTC), enabled: true, wantMicros: 1_250_000},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			got, err := nodeRateMicrosAt(1_250_000, scenario.enabled, ranges, scenario.now)
			if err != nil || got != scenario.wantMicros {
				t.Fatalf("nodeRateMicrosAt() = %d, %v; want %d", got, err, scenario.wantMicros)
			}
		})
	}
}

func TestValidClockMinuteRequiresCanonicalLegacyHourMinute(t *testing.T) {
	for _, value := range []string{"00:00", "08:30", "23:59"} {
		if !validClockMinute(value) {
			t.Fatalf("validClockMinute(%q) = false", value)
		}
	}
	for _, value := range []string{"+1:00", " 1:00", "1:00", "24:00", "12:60", "12-30", "ab:cd"} {
		if validClockMinute(value) {
			t.Fatalf("validClockMinute(%q) = true", value)
		}
	}
}

func BenchmarkImportLegacyNodesTenThousandNodes(b *testing.B) {
	nodes := make([]LegacyNode, 10_000)
	for index := range nodes {
		nodes[index] = LegacyNode{
			ID: int64(index + 1), Type: "vless", Name: "Node " + strconv.Itoa(index+1), RateMicros: 1_000_000,
			GroupIDs: []int64{}, RouteIDs: []int64{},
			Tags: []string{}, Host: "edge.example.test", Port: "443", ServerPort: 443,
			ProtocolSettings: json.RawMessage(`{"tls":0}`), Show: true, Sort: index,
			CreatedAt: 1_700_000_000, UpdatedAt: 1_700_000_000,
			RateTimeRanges: json.RawMessage(`[]`), CustomOutbounds: json.RawMessage(`[]`),
			CustomRoutes: json.RawMessage(`[]`), CertConfig: json.RawMessage(`{}`), Enabled: true,
			RuntimeConfig: json.RawMessage(`{"protocol":"vless","server_port":443}`),
		}
	}
	base := LegacyNodesImport{
		Slice: LegacyNodesSlice, SourceSHA256: strings.Repeat("c", 64), SourceSize: 8 << 20,
		RollbackBackupPath: "/backups/benchmark.xbbackup", RollbackBackupSHA256: strings.Repeat("d", 64), Nodes: nodes,
	}
	base.Checksums = LegacyNodesChecksums{
		Machines: LegacyMachinesChecksum(nil, nil, nil, nil), Nodes: LegacyNodesChecksum(nodes),
		Schedules: LegacySchedulesChecksum(nil), Traffic: LegacyNodeTrafficChecksum(nil),
	}
	directory := b.TempDir()
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		b.StopTimer()
		database, err := OpenSQLite("file:" + filepath.ToSlash(filepath.Join(directory, fmt.Sprintf("target-%d.db", iteration))))
		if err != nil {
			b.Fatal(err)
		}
		if err := database.Migrate(context.Background()); err != nil {
			_ = database.Close()
			b.Fatal(err)
		}
		b.StartTimer()
		report, err := database.ImportLegacyNodes(context.Background(), base, time.Unix(1_800_000_000, 0))
		b.StopTimer()
		if err != nil || report.Nodes.TargetRows != 10_000 {
			_ = database.Close()
			b.Fatalf("ImportLegacyNodes() report=%#v err=%v", report, err)
		}
		if err := database.Close(); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
	b.ReportMetric(10_000, "nodes/op")
}

func validLegacyNodesImport() LegacyNodesImport {
	created := int64(1_700_000_000)
	credentialHash := security.DigestToken("known-machine-token")
	enableSecond, disableSecond := int64(8*3600), int64(20*3600)
	nextTransition, nextTarget := int64(1_800_000_100), false
	input := LegacyNodesImport{
		Slice: LegacyNodesSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 8192,
		RollbackBackupPath: "/backups/pre-nodes.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
		Machines:    []LegacyMachine{{ID: 7, Name: "legacy-machine", Notes: "edge", IsActive: true, LoadStatus: json.RawMessage(`{"cpu":12}`), CreatedAt: created, UpdatedAt: created + 10}},
		Credentials: []LegacyMachineCredential{{ID: 1, MachineID: 7, TokenHash: credentialHash, TokenPrefix: "known-machin", CreatedAt: created}},
		Nodes: []LegacyNode{{
			ID: 41, Type: "vless", ExternalCode: "edge-code", GroupIDs: []int64{3, 9}, RouteIDs: []int64{12},
			Name: "legacy-node", RateMicros: 1_250_000, Tags: []string{"premium"}, Host: "edge.example.test", Port: "443-449", ServerPort: 8443,
			ProtocolSettings: json.RawMessage(`{"tls":1}`), Show: true, Sort: 2, CreatedAt: created, UpdatedAt: created + 10,
			RateTimeEnabled: true, RateTimeRanges: json.RawMessage(`[{"start":"00:00","end":"23:59","rate":2}]`),
			CustomOutbounds: json.RawMessage(`[]`), CustomRoutes: json.RawMessage(`[]`), CertConfig: json.RawMessage(`{}`),
			TransferEnable: 1_000_000, TrafficUpload: 5, TrafficDownload: 7, MachineID: int64Pointer(7), Enabled: true,
			RuntimeConfig: json.RawMessage(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":8443,"tls":1}`),
		}},
		Schedules: []LegacyActivationSchedule{{
			NodeID: 41, ScheduleType: "daily", Timezone: "Asia/Singapore", EnableSecond: &enableSecond, DisableSecond: &disableSecond,
			Revision: "schedule-revision", NextTransitionAt: &nextTransition, NextTargetEnabled: &nextTarget, CreatedAt: created, UpdatedAt: created + 10,
		}},
		Traffic: []LegacyNodeTrafficStat{{NodeID: 41, RecordAt: 1_699_920_000, RecordType: "d", Upload: 5, Download: 7, CreatedAt: created, UpdatedAt: created + 10}},
	}
	input.Checksums = LegacyNodesChecksums{
		Machines: LegacyMachinesChecksum(input.Machines, input.Credentials, input.Enrollments, input.LoadHistory),
		Nodes:    LegacyNodesChecksum(input.Nodes), Schedules: LegacySchedulesChecksum(input.Schedules), Traffic: LegacyNodeTrafficChecksum(input.Traffic),
	}
	return input
}

func int64Pointer(value int64) *int64 { return &value }
