package store

import (
	"context"
	"testing"
)

func BenchmarkListTrustedPlugins(b *testing.B) {
	database := newTestStore(b)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		plugins, err := database.ListTrustedPlugins(ctx)
		if err != nil || len(plugins) != 7 {
			b.Fatalf("ListTrustedPlugins() count=%d error=%v", len(plugins), err)
		}
	}
}
