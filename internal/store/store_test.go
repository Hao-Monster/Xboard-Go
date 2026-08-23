package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
)

func TestMachineEnrollmentIsOneTimeAndHashed(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)

	machine, enrollment, err := store.CreateMachine(ctx, CreateMachineInput{
		Name:     "edge-sg-01",
		Notes:    "Singapore edge",
		IsActive: true,
	}, now)
	if err != nil {
		t.Fatalf("CreateMachine() error = %v", err)
	}
	if enrollment.Code == "" || enrollment.ExpiresAt.Sub(now) != 15*time.Minute {
		t.Fatalf("unexpected enrollment: %#v", enrollment)
	}

	var storedEnrollmentHash string
	if err := store.db.QueryRowContext(ctx, `SELECT code_hash FROM server_machine_enrollments WHERE machine_id = ?`, machine.ID).Scan(&storedEnrollmentHash); err != nil {
		t.Fatalf("read enrollment digest: %v", err)
	}
	if storedEnrollmentHash == enrollment.Code || storedEnrollmentHash != security.DigestToken(enrollment.Code) {
		t.Fatal("enrollment must be persisted only as a digest")
	}
	if _, err := store.ExchangeEnrollment(ctx, machine.ID+999, enrollment.Code, now.Add(30*time.Second)); !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatalf("cross-machine enrollment error = %v, want ErrInvalidEnrollment", err)
	}

	credential, err := store.ExchangeEnrollment(ctx, machine.ID, enrollment.Code, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ExchangeEnrollment() error = %v", err)
	}
	if credential.Token == "" || credential.MachineID != machine.ID {
		t.Fatalf("unexpected credential: %#v", credential)
	}
	if _, err := store.ExchangeEnrollment(ctx, machine.ID, enrollment.Code, now.Add(2*time.Minute)); !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatalf("second exchange error = %v, want ErrInvalidEnrollment", err)
	}

	authenticated, err := store.AuthenticateMachine(ctx, machine.ID, credential.Token, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("AuthenticateMachine() error = %v", err)
	}
	if authenticated.ID != machine.ID {
		t.Fatalf("authenticated machine ID = %d, want %d", authenticated.ID, machine.ID)
	}
	if _, err := store.AuthenticateMachine(ctx, machine.ID+999, credential.Token, now.Add(3*time.Minute)); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("cross-machine credential error = %v, want ErrInvalidCredential", err)
	}

	var storedCredentialHash string
	if err := store.db.QueryRowContext(ctx, `SELECT token_hash FROM server_machine_credentials WHERE machine_id = ?`, machine.ID).Scan(&storedCredentialHash); err != nil {
		t.Fatalf("read credential digest: %v", err)
	}
	if storedCredentialHash == credential.Token || storedCredentialHash != security.DigestToken(credential.Token) {
		t.Fatal("machine credential must be persisted only as a digest")
	}
}

func TestRotatingMachineCredentialRevokesOnlyAfterExchange(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)

	machine, initialEnrollment, err := store.CreateMachine(ctx, CreateMachineInput{Name: "edge-01", IsActive: true}, now)
	if err != nil {
		t.Fatalf("CreateMachine() error = %v", err)
	}
	oldCredential, err := store.ExchangeEnrollment(ctx, machine.ID, initialEnrollment.Code, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("initial ExchangeEnrollment() error = %v", err)
	}

	rotation, err := store.CreateEnrollment(ctx, machine.ID, true, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("CreateEnrollment() error = %v", err)
	}
	if _, err := store.AuthenticateMachine(ctx, machine.ID, oldCredential.Token, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("old credential was revoked before rotation exchange: %v", err)
	}

	newCredential, err := store.ExchangeEnrollment(ctx, machine.ID, rotation.Code, now.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("rotation ExchangeEnrollment() error = %v", err)
	}
	if _, err := store.AuthenticateMachine(ctx, machine.ID, oldCredential.Token, now.Add(5*time.Minute)); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("old credential error = %v, want ErrInvalidCredential", err)
	}
	if _, err := store.AuthenticateMachine(ctx, machine.ID, newCredential.Token, now.Add(5*time.Minute)); err != nil {
		t.Fatalf("new credential rejected: %v", err)
	}
}

func TestNewEnrollmentInvalidatesPriorUnusedEnrollment(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)

	machine, initial, err := store.CreateMachine(ctx, CreateMachineInput{Name: "edge-01", IsActive: true}, now)
	if err != nil {
		t.Fatalf("CreateMachine() error = %v", err)
	}
	replacement, err := store.CreateEnrollment(ctx, machine.ID, false, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("CreateEnrollment() error = %v", err)
	}

	if _, err := store.ExchangeEnrollment(ctx, machine.ID, initial.Code, now.Add(2*time.Minute)); !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatalf("superseded enrollment error = %v, want ErrInvalidEnrollment", err)
	}
	if _, err := store.ExchangeEnrollment(ctx, machine.ID, replacement.Code, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("replacement enrollment rejected: %v", err)
	}
}

func TestDailyScheduleReconcilesImmediatelyAndRejectsStaleRevision(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	location, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}
	now := time.Date(2026, 8, 20, 20, 0, 0, 0, location)

	machine, _, err := store.CreateMachine(ctx, CreateMachineInput{Name: "edge-01", IsActive: true}, now)
	if err != nil {
		t.Fatalf("CreateMachine() error = %v", err)
	}
	node, err := store.CreateNode(ctx, CreateNodeInput{
		Name:      "SG VLESS",
		Type:      "vless",
		Host:      "sg.example.test",
		Port:      "443",
		Show:      true,
		Enabled:   false,
		MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	first, err := store.SaveDailySchedule(ctx, node.ID, "Asia/Singapore", "19:00", "01:00", now)
	if err != nil {
		t.Fatalf("SaveDailySchedule() error = %v", err)
	}
	gotNode, err := store.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if !gotNode.Enabled {
		t.Fatal("saving an active schedule window must enable the node immediately")
	}

	replacement, err := store.SaveDailySchedule(ctx, node.ID, "Asia/Singapore", "08:00", "09:00", now)
	if err != nil {
		t.Fatalf("replacement SaveDailySchedule() error = %v", err)
	}
	if replacement.Revision == first.Revision {
		t.Fatal("replacing a schedule must change its revision")
	}
	gotNode, _ = store.GetNode(ctx, node.ID)
	if gotNode.Enabled {
		t.Fatal("replacement outside its active window must disable the node immediately")
	}

	applied, err := store.ApplyDueSchedule(ctx, DueSchedule{
		NodeID:           first.NodeID,
		Revision:         first.Revision,
		NextTransitionAt: first.NextTransitionAt,
	}, now.Add(6*time.Hour))
	if err != nil {
		t.Fatalf("ApplyDueSchedule(stale) error = %v", err)
	}
	if applied {
		t.Fatal("stale schedule revision must be a no-op")
	}
	gotNode, _ = store.GetNode(ctx, node.ID)
	if gotNode.Enabled {
		t.Fatal("stale schedule revision changed the node")
	}
}

func TestScheduleRequiresLinkedNodeAndManualOverrideLastsUntilBoundary(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	location, _ := time.LoadLocation("Asia/Singapore")
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, location)

	node, err := store.CreateNode(ctx, CreateNodeInput{
		Name: "orphan", Type: "hysteria", Host: "orphan.example.test", Port: "443", Show: true, Enabled: true,
	}, now)
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if _, err := store.SaveDailySchedule(ctx, node.ID, "Asia/Singapore", "19:00", "01:00", now); !errors.Is(err, ErrNodeNotLinked) {
		t.Fatalf("SaveDailySchedule(unlinked) error = %v, want ErrNodeNotLinked", err)
	}

	machine, _, err := store.CreateMachine(ctx, CreateMachineInput{Name: "edge", IsActive: true}, now)
	if err != nil {
		t.Fatalf("CreateMachine() error = %v", err)
	}
	if err := store.AssignNode(ctx, machine.ID, node.ID, now); err != nil {
		t.Fatalf("AssignNode() error = %v", err)
	}
	saved, err := store.SaveDailySchedule(ctx, node.ID, "Asia/Singapore", "19:00", "01:00", now)
	if err != nil {
		t.Fatalf("SaveDailySchedule() error = %v", err)
	}
	if err := store.SetNodeEnabled(ctx, machine.ID, node.ID, true, now.Add(time.Minute)); err != nil {
		t.Fatalf("manual SetNodeEnabled() error = %v", err)
	}
	gotNode, _ := store.GetNode(ctx, node.ID)
	if !gotNode.Enabled {
		t.Fatal("manual override should apply immediately")
	}

	applied, err := store.ApplyDueSchedule(ctx, DueSchedule{
		NodeID:           saved.NodeID,
		Revision:         saved.Revision,
		NextTransitionAt: saved.NextTransitionAt,
	}, saved.NextTransitionAt)
	if err != nil || !applied {
		t.Fatalf("ApplyDueSchedule() = (%v, %v), want (true, nil)", applied, err)
	}
	gotNode, _ = store.GetNode(ctx, node.ID)
	if !gotNode.Enabled {
		t.Fatal("the 19:00 boundary should enable the node")
	}

	due, err := store.ListDueSchedules(ctx, saved.NextTransitionAt.Add(24*time.Hour), 10)
	if err != nil {
		t.Fatalf("ListDueSchedules() error = %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("due schedule count = %d, want 1", len(due))
	}
}

func TestDueScheduleIsAppliedOnceUnderConcurrency(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	location, _ := time.LoadLocation("Asia/Singapore")
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, location)
	machine, _, err := store.CreateMachine(ctx, CreateMachineInput{Name: "concurrent-edge", IsActive: true}, now)
	if err != nil {
		t.Fatalf("CreateMachine() error = %v", err)
	}
	node, err := store.CreateNode(ctx, CreateNodeInput{Name: "concurrent-node", Type: "vless", Host: "concurrent.example.test", Port: "443", Show: true, Enabled: false, MachineID: &machine.ID}, now)
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	saved, err := store.SaveDailySchedule(ctx, node.ID, "Asia/Singapore", "19:00", "01:00", now)
	if err != nil {
		t.Fatalf("SaveDailySchedule() error = %v", err)
	}
	due := DueSchedule{NodeID: node.ID, Revision: saved.Revision, NextTransitionAt: saved.NextTransitionAt}

	var appliedCount atomic.Int32
	var wait sync.WaitGroup
	errorsFound := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			applied, err := store.ApplyDueSchedule(ctx, due, saved.NextTransitionAt)
			if err != nil {
				errorsFound <- err
				return
			}
			if applied {
				appliedCount.Add(1)
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("ApplyDueSchedule() error = %v", err)
	}
	if got := appliedCount.Load(); got != 1 {
		t.Fatalf("applied count = %d, want 1", got)
	}
}

func TestDeletingMachineUnlinksNodesWithoutDeletingThem(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	ensureTestServerGroups(t, store, now, 7)
	machine, enrollment, err := store.CreateMachine(ctx, CreateMachineInput{Name: "delete-edge", IsActive: true}, now)
	if err != nil {
		t.Fatalf("CreateMachine() error = %v", err)
	}
	credential, err := store.ExchangeEnrollment(ctx, machine.ID, enrollment.Code, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ExchangeEnrollment() error = %v", err)
	}
	node, err := store.CreateNode(ctx, CreateNodeInput{Name: "preserved-node", Type: "vless", Host: "preserved.example.test", Port: "443", Show: true, Enabled: true, MachineID: &machine.ID}, now)
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if _, err := store.SaveNodeRuntime(ctx, node.ID, SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{7},
		Config: []byte(`{"protocol":"vless","server_port":443}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateRuntimeUser(ctx, CreateRuntimeUserInput{
		Email: "machine-delete-user@example.test", PasswordHash: "test-password-hash",
		UUID: "b48f942f-5d32-41e3-a0ba-a854b16cc7dd", GroupID: 7, TransferEnable: 1_000_000,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyNodeReport(ctx, NodeReportInput{
		MachineID: machine.ID, NodeID: node.ID, Alive: map[int64][]string{user.ID: {"192.0.2.90"}}, Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteMachine(ctx, machine.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("DeleteMachine() error = %v", err)
	}
	preserved, err := store.GetNode(ctx, node.ID)
	if err != nil || preserved.MachineID != nil {
		t.Fatalf("node was not preserved and unlinked: node=%#v err=%v", preserved, err)
	}
	if _, err := store.AuthenticateMachine(ctx, machine.ID, credential.Token, now.Add(2*time.Minute)); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("deleted machine credential error = %v, want ErrInvalidCredential", err)
	}
	devices, err := store.ListUserDevices(ctx, []int64{user.ID}, now.Add(time.Minute))
	if err != nil || len(devices[user.ID]) != 0 {
		t.Fatalf("deleted machine devices = %#v, err=%v", devices, err)
	}
	var onlineCount int
	if err := store.db.QueryRow(`SELECT online_count FROM users WHERE id = ?`, user.ID).Scan(&onlineCount); err != nil || onlineCount != 0 {
		t.Fatalf("deleted machine online_count = %d, err=%v", onlineCount, err)
	}
}

func TestNodePortPreservesDynamicRanges(t *testing.T) {
	store := newTestStore(t)
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	node, err := store.CreateNode(context.Background(), CreateNodeInput{
		Name: "dynamic-port", Type: "hysteria", Host: "dynamic.example.test", Port: "20000-30000", Show: true, Enabled: true,
	}, now)
	if err != nil {
		t.Fatalf("CreateNode(dynamic range) error = %v", err)
	}
	if node.Port != "20000-30000" {
		t.Fatalf("node port = %q, want preserved range", node.Port)
	}
	if _, err := store.CreateNode(context.Background(), CreateNodeInput{
		Name: "invalid-range", Type: "hysteria", Host: "invalid.example.test", Port: "30000-20000", Show: true, Enabled: true,
	}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("descending range error = %v, want ErrInvalidInput", err)
	}
}

func TestLegacyOnceScheduleLateExecutionCannotReenableExpiredNode(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	machine, _, err := store.CreateMachine(ctx, CreateMachineInput{Name: "legacy-edge", IsActive: true}, now)
	if err != nil {
		t.Fatalf("CreateMachine() error = %v", err)
	}
	node, err := store.CreateNode(ctx, CreateNodeInput{Name: "legacy-node", Type: "vless", Host: "legacy.example.test", Port: "443", Show: true, Enabled: true, MachineID: &machine.ID}, now)
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	enableAt, disableAt := now.Add(time.Hour), now.Add(2*time.Hour)
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO server_activation_schedules (
			node_id, schedule_type, enable_at, disable_at, revision, next_transition_at, next_target_enabled, created_at, updated_at
		) VALUES (?, 'once', ?, ?, 'legacy-revision', ?, 1, ?, ?)
	`, node.ID, enableAt.Unix(), disableAt.Unix(), enableAt.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatalf("insert legacy schedule: %v", err)
	}

	applied, err := store.ApplyDueSchedule(ctx, DueSchedule{NodeID: node.ID, Revision: "legacy-revision", NextTransitionAt: enableAt}, now.Add(3*time.Hour))
	if err != nil || !applied {
		t.Fatalf("ApplyDueSchedule(legacy late) = (%v, %v), want (true, nil)", applied, err)
	}
	updated, err := store.GetNode(ctx, node.ID)
	if err != nil || updated.Enabled {
		t.Fatalf("expired legacy schedule re-enabled node: node=%#v err=%v", updated, err)
	}
	var nextTransition sql.NullInt64
	if err := store.db.QueryRowContext(ctx, `SELECT next_transition_at FROM server_activation_schedules WHERE node_id = ?`, node.ID).Scan(&nextTransition); err != nil {
		t.Fatalf("read legacy next transition: %v", err)
	}
	if nextTransition.Valid {
		t.Fatalf("completed legacy schedule still has next transition %d", nextTransition.Int64)
	}
}

func TestDeletingSchedulePreservesCurrentManualNodeState(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	location, _ := time.LoadLocation("Asia/Singapore")
	now := time.Date(2026, 8, 20, 20, 0, 0, 0, location)
	machine, _, err := store.CreateMachine(ctx, CreateMachineInput{Name: "cancel-edge", IsActive: true}, now)
	if err != nil {
		t.Fatalf("CreateMachine() error = %v", err)
	}
	node, err := store.CreateNode(ctx, CreateNodeInput{Name: "cancel-node", Type: "vless", Host: "cancel.example.test", Port: "443", Show: true, Enabled: false, MachineID: &machine.ID}, now)
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if _, err := store.SaveDailySchedule(ctx, node.ID, "Asia/Singapore", "19:00", "01:00", now); err != nil {
		t.Fatalf("SaveDailySchedule() error = %v", err)
	}
	if err := store.SetNodeEnabled(ctx, machine.ID, node.ID, false, now.Add(time.Minute)); err != nil {
		t.Fatalf("SetNodeEnabled() error = %v", err)
	}
	if err := store.DeleteActivationSchedule(ctx, node.ID); err != nil {
		t.Fatalf("DeleteActivationSchedule() error = %v", err)
	}
	updated, err := store.GetNode(ctx, node.ID)
	if err != nil || updated.Enabled {
		t.Fatalf("deleting schedule changed manual state: node=%#v err=%v", updated, err)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	store, err := OpenSQLite(dsn)
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return store
}
