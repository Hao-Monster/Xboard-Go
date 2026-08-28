package nodecoord

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func BenchmarkRedisCoordinatorClaimMachine100Nodes(b *testing.B) {
	coordinator := newBenchmarkCoordinator(b, "claim")
	nodeIDs := make([]int64, 100)
	for index := range nodeIDs {
		nodeIDs[index] = int64(index + 1)
	}
	connectionID := mustConnectionID(b, coordinator)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := coordinator.ClaimMachine(context.Background(), 1, nodeIDs, connectionID); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRedisCoordinatorVerifyMachineNode(b *testing.B) {
	coordinator := newBenchmarkCoordinator(b, "verify")
	connectionID := mustConnectionID(b, coordinator)
	if err := coordinator.ClaimMachine(context.Background(), 1, []int64{1}, connectionID); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		owned, err := coordinator.OwnsMachineAndNodes(context.Background(), 1, []int64{1}, connectionID)
		if err != nil || !owned {
			b.Fatalf("OwnsMachineAndNodes() = (%v, %v)", owned, err)
		}
	}
}

func BenchmarkRedisCoordinatorRenew1000Machines(b *testing.B) {
	coordinator := newBenchmarkCoordinator(b, "renew")
	leases := make([]Lease, 1_000)
	for index := range leases {
		machineID := int64(index + 1)
		connectionID := mustConnectionID(b, coordinator)
		leases[index] = Lease{MachineID: machineID, NodeIDs: []int64{machineID}, ConnectionID: connectionID}
		if err := coordinator.ClaimMachine(context.Background(), machineID, []int64{machineID}, connectionID); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		renewed, err := coordinator.Renew(context.Background(), leases)
		if err != nil || len(renewed) != len(leases) {
			b.Fatalf("Renew() results=%d error=%v", len(renewed), err)
		}
		for _, owned := range renewed {
			if !owned {
				b.Fatal("Renew() lost a current lease")
			}
		}
	}
}

func newBenchmarkCoordinator(b *testing.B, revision string) *RedisCoordinator {
	b.Helper()
	redisURL := os.Getenv("XBOARD_TEST_REDIS_URL")
	if redisURL == "" {
		b.Skip("XBOARD_TEST_REDIS_URL is not configured")
	}
	coordinator, err := NewRedis(context.Background(), Options{
		URL: redisURL, Prefix: "xboard-go-benchmark:" + uuid.NewString() + ":",
		Revision: revision, LeaseDuration: 30 * time.Second,
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = coordinator.Close() })
	return coordinator
}
