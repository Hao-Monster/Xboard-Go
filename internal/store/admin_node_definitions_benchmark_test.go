package store

import (
	"context"
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
