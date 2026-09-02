package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func BenchmarkPERFNODE007ApplyTraffic1000Users(b *testing.B) {
	database := newTestStore(b)
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	machine, node := createReportingNode(b, database, now)
	traffic := make(map[int64]TrafficUsage, 1_000)
	for index := 0; index < 1_000; index++ {
		user, err := database.CreateRuntimeUser(ctx, CreateRuntimeUserInput{
			Email: fmt.Sprintf("traffic-benchmark-%04d@example.test", index), PasswordHash: "hash",
			UUID: fmt.Sprintf("00000000-0000-4000-8000-%012d", index), GroupID: 7, TransferEnable: 1 << 60,
		}, now)
		if err != nil {
			b.Fatal(err)
		}
		traffic[user.ID] = TrafficUsage{Upload: 1, Download: 2}
	}
	input := NodeReportInput{MachineID: machine.ID, NodeID: node.ID, Traffic: traffic, Now: now}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		input.Now = now.Add(time.Duration(iteration) * time.Second)
		input.ReportID = fmt.Sprintf("00000000-0000-4000-9000-%012d", iteration)
		if _, err := database.ApplyNodeReport(ctx, input); err != nil {
			b.Fatal(err)
		}
	}
}
