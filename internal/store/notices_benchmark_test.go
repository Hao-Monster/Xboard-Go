package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func BenchmarkListVisibleNotices10000(b *testing.B) {
	database := openBenchmarkStore(b, "notices.db")
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC).Unix()
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	for index := 1; index <= 10_000; index++ {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO notices (sort_position, title, content, image_url, tags_json, visible, revision, created_at, updated_at)
			VALUES (?, ?, 'body', NULL, '[]', ?, 1, ?, ?)
		`, index, fmt.Sprintf("notice-%05d", index), index%2, now, now); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		notices, total, err := database.ListVisibleNotices(ctx, 1, 5)
		if err != nil || len(notices) != 5 || total != 5_000 {
			b.Fatalf("ListVisibleNotices() notices=%d total=%d err=%v", len(notices), total, err)
		}
	}
}
