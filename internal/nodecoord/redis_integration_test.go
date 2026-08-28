package nodecoord

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestRedisCoordinatorClaimsFencesRenewsAndConditionallyReleases(t *testing.T) {
	prefix := testPrefix()
	first := newTestCoordinatorAt(t, "first", prefix)
	second := newTestCoordinatorAt(t, "second", prefix)
	ctx := context.Background()
	firstID := mustConnectionID(t, first)
	secondID := mustConnectionID(t, second)

	if err := first.ClaimMachine(ctx, 7, []int64{17, 18, 18}, firstID); err != nil {
		t.Fatalf("first ClaimMachine() error = %v", err)
	}
	if owned, err := first.OwnsMachineAndNodes(ctx, 7, []int64{17, 18}, firstID); err != nil || !owned {
		t.Fatalf("first OwnsMachineAndNodes() = (%v, %v), want (true, nil)", owned, err)
	}

	if err := second.ClaimMachine(ctx, 7, []int64{17, 18}, secondID); err != nil {
		t.Fatalf("second ClaimMachine() error = %v", err)
	}
	if owned, err := first.OwnsMachineAndNodes(ctx, 7, []int64{17}, firstID); err != nil || owned {
		t.Fatalf("stale OwnsMachineAndNodes() = (%v, %v), want (false, nil)", owned, err)
	}
	if released, err := first.ReleaseNodeIfOwned(ctx, 17, firstID); err != nil || released {
		t.Fatalf("stale ReleaseNodeIfOwned() = (%v, %v), want (false, nil)", released, err)
	}
	if owned, err := second.OwnsMachineAndNodes(ctx, 7, []int64{17, 18}, secondID); err != nil || !owned {
		t.Fatalf("current OwnsMachineAndNodes() = (%v, %v), want (true, nil)", owned, err)
	}

	renewed, err := second.Renew(ctx, []Lease{
		{MachineID: 7, NodeIDs: []int64{17, 18}, ConnectionID: secondID},
		{MachineID: 7, NodeIDs: []int64{17}, ConnectionID: firstID},
	})
	if err != nil {
		t.Fatalf("Renew() error = %v", err)
	}
	if len(renewed) != 2 || !renewed[0] || renewed[1] {
		t.Fatalf("Renew() = %v, want [true false]", renewed)
	}

	if released, err := second.ReleaseMachineIfOwned(ctx, 7, secondID); err != nil || !released {
		t.Fatalf("ReleaseMachineIfOwned() = (%v, %v), want (true, nil)", released, err)
	}
	for _, nodeID := range []int64{17, 18} {
		if released, err := second.ReleaseNodeIfOwned(ctx, nodeID, secondID); err != nil || !released {
			t.Fatalf("ReleaseNodeIfOwned(%d) = (%v, %v), want (true, nil)", nodeID, released, err)
		}
	}
}

func TestRedisCoordinatorFencesMachineMembershipChanges(t *testing.T) {
	prefix := testPrefix()
	first := newTestCoordinatorAt(t, "membership-first", prefix)
	second := newTestCoordinatorAt(t, "membership-second", prefix)
	ctx := context.Background()
	firstID := mustConnectionID(t, first)
	secondID := mustConnectionID(t, second)

	if err := first.ClaimMachine(ctx, 9, []int64{21}, firstID); err != nil {
		t.Fatal(err)
	}
	claimed, err := first.ClaimMachineNodesIfOwned(ctx, 9, []int64{21, 22}, firstID)
	if err != nil || !claimed {
		t.Fatalf("current ClaimMachineNodesIfOwned() = (%v, %v), want (true, nil)", claimed, err)
	}
	if err := second.ClaimMachine(ctx, 9, []int64{21, 22}, secondID); err != nil {
		t.Fatal(err)
	}
	claimed, err = first.ClaimMachineNodesIfOwned(ctx, 9, []int64{21, 23}, firstID)
	if err != nil || claimed {
		t.Fatalf("stale ClaimMachineNodesIfOwned() = (%v, %v), want (false, nil)", claimed, err)
	}
	if owned, err := first.OwnsMachineAndNodes(ctx, 9, []int64{23}, firstID); err != nil || owned {
		t.Fatalf("stale connection unexpectedly claimed node 23: owned=%v err=%v", owned, err)
	}
}

func TestRedisCoordinatorPublishesReplacementAndBoundedCommands(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prefix := testPrefix()
	first := newTestCoordinatorAt(t, "publisher", prefix)
	second := newTestCoordinatorAt(t, "subscriber", prefix)
	events := make(chan Event, 4)
	if err := second.Start(ctx, func(event Event) { events <- event }); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	connectionID := mustConnectionID(t, first)
	if err := first.ClaimMachine(ctx, 11, []int64{31, 32}, connectionID); err != nil {
		t.Fatalf("ClaimMachine() error = %v", err)
	}
	replacement := waitEvent(t, events)
	if replacement.Kind != EventReplacement || replacement.MachineID != 11 || replacement.ConnectionID != connectionID ||
		len(replacement.NodeIDs) != 2 || replacement.NodeIDs[0] != 31 || replacement.NodeIDs[1] != 32 {
		t.Fatalf("replacement = %#v", replacement)
	}

	command := Event{Kind: EventNodeConfig, MachineID: 11, NodeID: 31}
	if err := first.Publish(ctx, command); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	received := waitEvent(t, events)
	if received.Kind != command.Kind || received.MachineID != 11 || received.NodeID != 31 || received.Source != first.InstanceID() {
		t.Fatalf("command = %#v", received)
	}

	if err := first.Publish(ctx, Event{Kind: EventDeviceUsers, UserIDs: make([]int64, maxEventIDs+1)}); err == nil {
		t.Fatal("Publish() accepted an oversized ID list")
	}
}

func TestRedisCoordinatorRevokesMachineBeforePublishingDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prefix := testPrefix()
	owner := newTestCoordinatorAt(t, "revoke-owner", prefix)
	revoker := newTestCoordinatorAt(t, "revoke-caller", prefix)
	events := make(chan Event, 4)
	if err := owner.Start(ctx, func(event Event) { events <- event }); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	connectionID := mustConnectionID(t, owner)
	if err := owner.ClaimMachine(ctx, 19, []int64{41}, connectionID); err != nil {
		t.Fatal(err)
	}
	_ = waitEvent(t, events)
	if err := revoker.RevokeMachine(ctx, 19, "machine disabled"); err != nil {
		t.Fatalf("RevokeMachine() error = %v", err)
	}
	event := waitEvent(t, events)
	if event.Kind != EventDisconnectMachine || event.MachineID != 19 || event.Reason != "machine disabled" {
		t.Fatalf("disconnect event = %#v", event)
	}
	if owned, err := owner.OwnsMachineAndNodes(ctx, 19, []int64{41}, connectionID); err != nil || owned {
		t.Fatalf("ownership after revoke = (%v, %v), want (false, nil)", owned, err)
	}
}

func TestRedisCoordinatorRejectsInvalidOptionsAndInputs(t *testing.T) {
	ctx := context.Background()
	if _, err := NewRedis(ctx, Options{URL: "http://127.0.0.1:6379", Prefix: "test:", Revision: "revision"}); err == nil {
		t.Fatal("NewRedis() accepted a non-Redis URL")
	}
	if _, err := NewRedis(ctx, Options{URL: testRedisURL(t), Prefix: "bad prefix", Revision: "revision"}); err == nil {
		t.Fatal("NewRedis() accepted an unsafe key prefix")
	}
	coordinator := newTestCoordinator(t, "validation")
	if err := coordinator.ClaimMachine(ctx, 0, []int64{1}, mustConnectionID(t, coordinator)); err == nil {
		t.Fatal("ClaimMachine() accepted machine ID zero")
	}
	if err := coordinator.ClaimMachine(ctx, 1, []int64{0}, mustConnectionID(t, coordinator)); err == nil {
		t.Fatal("ClaimMachine() accepted node ID zero")
	}
	if owned, err := coordinator.OwnsMachineAndNodes(ctx, 1, nil, mustConnectionID(t, coordinator)); err != nil || owned {
		t.Fatalf("OwnsMachineAndNodes() empty nodes = (%v, %v), want (false, nil)", owned, err)
	}
}

func newTestCoordinator(t *testing.T, revision string) *RedisCoordinator {
	t.Helper()
	return newTestCoordinatorAt(t, revision, testPrefix())
}

func newTestCoordinatorAt(t *testing.T, revision, prefix string) *RedisCoordinator {
	t.Helper()
	coordinator, err := NewRedis(context.Background(), Options{
		URL: testRedisURL(t), Prefix: prefix, Revision: revision, LeaseDuration: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewRedis() error = %v", err)
	}
	t.Cleanup(func() {
		if err := coordinator.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return coordinator
}

func testPrefix() string {
	return "xboard-go-test:" + uuid.NewString() + ":"
}

func testRedisURL(t *testing.T) string {
	t.Helper()
	value := os.Getenv("XBOARD_TEST_REDIS_URL")
	if value == "" {
		t.Skip("XBOARD_TEST_REDIS_URL is not configured")
	}
	return value
}

func mustConnectionID(t testing.TB, coordinator *RedisCoordinator) string {
	t.Helper()
	connectionID, err := coordinator.NewConnectionID()
	if err != nil {
		t.Fatalf("generate connection ID: %v", err)
	}
	return connectionID
}

func waitEvent(t *testing.T, events <-chan Event) Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Redis event")
		return Event{}
	}
}

func TestRedisURLIsIsolated(t *testing.T) {
	value := testRedisURL(t)
	options, err := redis.ParseURL(value)
	if err != nil {
		t.Fatalf("redis.ParseURL(%q) error = %v", value, err)
	}
	if options.DB == 0 {
		t.Logf("Redis integration uses database 0 with isolated key prefixes: %s", fmt.Sprintf("%s:%d", options.Addr, options.DB))
	}
}
