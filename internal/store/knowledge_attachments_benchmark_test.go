package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func BenchmarkListKnowledgeAttachments10000Rows(b *testing.B) {
	database := newTestStore(b)
	now := time.Unix(1_800_000_000, 0).UTC()
	admin, err := database.CreateAdminUser(context.Background(), CreateAdminUserInput{Email: "attachment-benchmark@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		b.Fatal(err)
	}
	article, err := database.CreateKnowledge(context.Background(), SaveKnowledgeInput{Language: "en-US", Category: "General", Title: "Benchmark", Body: "Body", Visible: true}, now)
	if err != nil {
		b.Fatal(err)
	}
	tx, err := database.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.Prepare(`
		INSERT INTO knowledge_attachments
		(uuid, knowledge_id, uploader_user_id, original_name, storage_path, mime_type, extension, size, sha256, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'text/plain', 'txt', 1, ?, 'ready', ?, ?)
	`)
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 10_000; index++ {
		identifier := fmt.Sprintf("00000000-0000-4000-8000-%012x", index+1)
		if _, err := statement.Exec(identifier, article.ID, admin.ID, fmt.Sprintf("file-%05d.txt", index),
			fmt.Sprintf("files/benchmark/%05d.txt", index), strings.Repeat("a", 64), now.Unix(), now.Unix()); err != nil {
			b.Fatal(err)
		}
	}
	if err := statement.Close(); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		rows := 0
		for page := 1; page <= 100; page++ {
			result, err := database.ListKnowledgeAttachments(context.Background(), admin.ID, &article.ID, nil, page, 100)
			if err != nil || result.Total != 10_000 {
				b.Fatalf("page %d result=%#v error=%v", page, result, err)
			}
			rows += len(result.Items)
		}
		if rows != 10_000 {
			b.Fatalf("listed rows = %d", rows)
		}
	}
	b.ReportMetric(10_000, "rows/op")
}

func BenchmarkListPurgeableKnowledgeAttachments10000Rows(b *testing.B) {
	database := newTestStore(b)
	now := time.Unix(1_800_000_000, 0).UTC()
	admin, err := database.CreateAdminUser(context.Background(), CreateAdminUserInput{Email: "attachment-cleanup-benchmark@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		b.Fatal(err)
	}
	article, err := database.CreateKnowledge(context.Background(), SaveKnowledgeInput{Language: "en-US", Category: "General", Title: "Cleanup benchmark", Body: "Body", Visible: true}, now)
	if err != nil {
		b.Fatal(err)
	}
	tx, err := database.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.Prepare(`
		INSERT INTO knowledge_attachments
		(uuid, knowledge_id, uploader_user_id, original_name, storage_path, mime_type, extension, size, sha256, status, created_at, updated_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, 'text/plain', 'txt', 1, ?, 'ready', ?, ?, ?)
	`)
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 10_000; index++ {
		identifier := fmt.Sprintf("10000000-0000-4000-8000-%012x", index+1)
		if _, err := statement.Exec(identifier, article.ID, admin.ID, fmt.Sprintf("deleted-%05d.txt", index),
			fmt.Sprintf("files/deleted/%05d.txt", index), strings.Repeat("b", 64), now.Unix(), now.Unix(), now.Unix()); err != nil {
			b.Fatal(err)
		}
	}
	if err := statement.Close(); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		items, err := database.ListPurgeableKnowledgeAttachments(context.Background(), now.Add(time.Hour), 1000)
		if err != nil || len(items) != 1000 {
			b.Fatalf("purgeable rows=%d error=%v", len(items), err)
		}
	}
	b.ReportMetric(10_000, "source_rows")
	b.ReportMetric(1000, "bounded_rows/op")
}
