package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func BenchmarkListVisibleKnowledge10000(b *testing.B) {
	database := openBenchmarkStore(b, "knowledge.db")
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC).Unix()
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	for index := 1; index <= 10_000; index++ {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO knowledge (language, category, title, body, sort_position, visible, revision, created_at, updated_at)
			VALUES ('zh-CN', 'guide', ?, 'benchmark body', ?, ?, 1, ?, ?)
		`, fmt.Sprintf("article-%05d", index), index, index%2, now, now); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		articles, err := database.ListVisibleKnowledge(ctx, "zh-CN", "")
		if err != nil || len(articles) != 5_000 {
			b.Fatalf("ListVisibleKnowledge() articles=%d err=%v", len(articles), err)
		}
	}
}
