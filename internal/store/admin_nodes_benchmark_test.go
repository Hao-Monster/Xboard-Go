package store

import (
	"context"
	"testing"
	"time"
)

func BenchmarkListAdminNodes100K(b *testing.B) {
	database := newTestStore(b)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	if _, err := database.db.ExecContext(ctx, `
		WITH RECURSIVE sequence(value) AS (
			SELECT 1 UNION ALL SELECT value + 1 FROM sequence WHERE value < 100000
		)
		INSERT INTO nodes (
			name, type, host, port, show, enabled, sort, machine_id, created_at, updated_at
		)
		SELECT printf('benchmark-node-%06d', value),
		       CASE value % 4 WHEN 0 THEN 'vless' WHEN 1 THEN 'trojan' WHEN 2 THEN 'shadowsocks' ELSE 'hysteria' END,
		       printf('node-%06d.example.test', value), '443', value % 3 != 0, value % 5 != 0,
		       value, NULL, ?, ?
		FROM sequence
	`, now.Unix(), now.Unix()); err != nil {
		b.Fatalf("seed 100k administrator nodes: %v", err)
	}

	for _, benchmark := range []struct {
		name   string
		filter AdminNodeFilter
	}{
		{name: "first-page-vless", filter: AdminNodeFilter{Page: 1, PageSize: 500, Type: "vless"}},
		{name: "deep-page-vless", filter: AdminNodeFilter{Page: 50, PageSize: 500, Type: "vless"}},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				page, err := database.ListAdminNodes(ctx, benchmark.filter, now)
				if err != nil || len(page.Items) != 500 || page.Total != 25000 {
					b.Fatalf("ListAdminNodes() items=%d total=%d error=%v", len(page.Items), page.Total, err)
				}
			}
		})
	}
}
