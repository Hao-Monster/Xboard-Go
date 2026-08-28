package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func BenchmarkAdminNodeDefinitionReadAndUpdate(b *testing.B) {
	database := newTestStore(b)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	input, err := NewBasicAdminNodeDefinitionInput(CreateNodeInput{
		Name: "benchmark-vless", Type: "vless", Host: "benchmark.example.test", Port: "443",
		Show: true, Enabled: true,
	})
	if err != nil {
		b.Fatal(err)
	}
	definition, _, err := database.CreateAdminNodeDefinition(ctx, input, now)
	if err != nil {
		b.Fatal(err)
	}

	b.Run("read", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			loaded, err := database.GetAdminNodeDefinition(ctx, definition.ID)
			if err != nil || loaded.ID != definition.ID || loaded.ListenAddress != "0.0.0.0" {
				b.Fatalf("GetAdminNodeDefinition() id=%d address=%q error=%v", loaded.ID, loaded.ListenAddress, err)
			}
		}
	})

	b.Run("update", func(b *testing.B) {
		b.ReportAllocs()
		current, err := database.GetAdminNodeDefinition(ctx, definition.ID)
		if err != nil {
			b.Fatal(err)
		}
		for b.Loop() {
			input.Revision = current.Revision
			input.Show = !input.Show
			updated, _, err := database.UpdateAdminNodeDefinition(ctx, definition.ID, input, now)
			if err != nil || updated.Revision != current.Revision+1 {
				b.Fatalf("UpdateAdminNodeDefinition() revision=%d error=%v", updated.Revision, err)
			}
			current = updated
		}
	})
}

func BenchmarkSchemaV42BackfillMissingDefinitions(b *testing.B) {
	const nodesPerIteration = 10_000
	ctx := context.Background()
	b.ReportAllocs()
	b.ReportMetric(nodesPerIteration, "nodes/op")
	for iteration := 0; iteration < b.N; iteration++ {
		b.StopTimer()
		database, err := OpenSQLite(fmt.Sprintf("file:schema-v42-benchmark-%d?mode=memory&cache=shared", iteration))
		if err != nil {
			b.Fatal(err)
		}
		if err := database.Migrate(ctx); err != nil {
			b.Fatal(err)
		}
		now := time.Date(2026, 8, 28, 20, 0, 0, 0, time.UTC).Unix()
		if _, err := database.db.ExecContext(ctx, `
			WITH RECURSIVE sequence(value) AS (
				SELECT 1 UNION ALL SELECT value + 1 FROM sequence WHERE value < ?
			)
			INSERT INTO nodes (name, type, host, port, show, enabled, sort, created_at, updated_at)
			SELECT printf('Benchmark node %d', value), 'vless', printf('benchmark-%d.test', value),
			       '443', 1, 1, value, ?, ? FROM sequence
		`, nodesPerIteration, now, now); err != nil {
			b.Fatal(err)
		}
		if _, err := database.db.ExecContext(ctx, `PRAGMA user_version = 41`); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := database.Migrate(ctx); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if err := database.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
