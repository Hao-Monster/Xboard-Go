package legacymigration

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestReadKnowledgeSnapshotPreservesCanonicalLegacyRows(t *testing.T) {
	path := createLegacyKnowledgeSnapshot(t)
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_settings (name TEXT, value TEXT);
		INSERT INTO v2_settings VALUES ('smtp_password', 'must-never-be-read');
		INSERT INTO v2_knowledge (id, language, category, title, body, sort, show, created_at, updated_at) VALUES
			(2, 'en-US', 'General', 'Getting started', '# Hello', NULL, 1, 100, 110),
			(7, 'zh-CN', '使用指南', '客户端设置', '正文 {{siteName}}', 5, 0, 120, 130);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	snapshot, err := ReadKnowledgeSnapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadKnowledgeSnapshot() error = %v", err)
	}
	if snapshot.Path != path || snapshot.Size < 512 || len(snapshot.SHA256) != 64 || strings.Contains(snapshot.SHA256, "must-never") {
		t.Fatalf("snapshot identity = %#v", snapshot)
	}
	wantArticles := []store.LegacyKnowledgeArticle{
		{ID: 2, Language: "en-US", Category: "General", Title: "Getting started", Body: "# Hello", SortPosition: 0, Visible: true, CreatedAt: 100, UpdatedAt: 110},
		{ID: 7, Language: "zh-CN", Category: "使用指南", Title: "客户端设置", Body: "正文 {{siteName}}", SortPosition: 5, Visible: false, CreatedAt: 120, UpdatedAt: 130},
	}
	if !reflect.DeepEqual(snapshot.Articles, wantArticles) {
		t.Fatalf("articles = %#v", snapshot.Articles)
	}
	if len(snapshot.Checksum) != 64 {
		t.Fatalf("checksum = %q", snapshot.Checksum)
	}
}

func TestReadKnowledgeSnapshotRecordsARealEmptyDomain(t *testing.T) {
	snapshot, err := ReadKnowledgeSnapshot(context.Background(), createLegacyKnowledgeSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Articles) != 0 || snapshot.Checksum == "" {
		t.Fatalf("empty snapshot = %#v", snapshot)
	}
}

func TestReadKnowledgeSnapshotRejectsUnsafeLossyOrAttachmentData(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		statement string
		contains  string
	}{
		{name: "unsupported language", statement: `INSERT INTO v2_knowledge VALUES (1, 'xx-XX', 'General', 'Title', 'Body', 1, 1, 1, 1)`, contains: "language"},
		{name: "title normalization", statement: `INSERT INTO v2_knowledge VALUES (1, 'en-US', 'General', ' padded ', 'Body', 1, 1, 1, 1)`, contains: "normalization"},
		{name: "blank body", statement: `INSERT INTO v2_knowledge VALUES (1, 'en-US', 'General', 'Title', '   ', 1, 1, 1, 1)`, contains: "body"},
		{name: "negative sort", statement: `INSERT INTO v2_knowledge VALUES (1, 'en-US', 'General', 'Title', 'Body', -1, 1, 1, 1)`, contains: "sort"},
		{name: "invalid visibility", statement: `INSERT INTO v2_knowledge VALUES (1, 'en-US', 'General', 'Title', 'Body', 1, 2, 1, 1)`, contains: "visibility"},
		{name: "invalid timestamp", statement: `INSERT INTO v2_knowledge VALUES (1, 'en-US', 'General', 'Title', 'Body', 1, 1, 2, 1)`, contains: "timestamp"},
		{name: "attachment marker", statement: `INSERT INTO v2_knowledge VALUES (1, 'en-US', 'General', 'Title', '[file](knowledge-attachment://abc)', 1, 1, 1, 1)`, contains: "attachment"},
		{name: "mixed case attachment marker", statement: `INSERT INTO v2_knowledge VALUES (1, 'en-US', 'General', 'Title', '[file](Knowledge-Attachment://abc)', 1, 1, 1, 1)`, contains: "attachment"},
		{name: "bound attachment", statement: `INSERT INTO v2_knowledge_attachment VALUES (1, 1, 'uuid', 'ready', NULL)`, contains: "attachment"},
		{name: "pending upload", statement: `INSERT INTO v2_knowledge_attachment_upload VALUES (1, 'uuid', 'uploading')`, contains: "attachment"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path := createLegacyKnowledgeSnapshot(t)
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(scenario.statement); err != nil {
				_ = database.Close()
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadKnowledgeSnapshot(context.Background(), path); err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(scenario.contains)) {
				t.Fatalf("ReadKnowledgeSnapshot() error = %v, want %q", err, scenario.contains)
			}
		})
	}

	t.Run("view masquerades as knowledge table", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "legacy-knowledge-view.db")
		database, err := sql.Open("sqlite", "file:"+path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`
			CREATE TABLE backing_knowledge (id INTEGER, language TEXT, category TEXT, title TEXT, body TEXT, sort INTEGER, show INTEGER, created_at INTEGER, updated_at INTEGER);
			CREATE VIEW v2_knowledge AS SELECT * FROM backing_knowledge;
			CREATE TABLE v2_knowledge_attachment (id INTEGER, knowledge_id INTEGER, uuid TEXT, status TEXT, deleted_at INTEGER);
			CREATE TABLE v2_knowledge_attachment_upload (id INTEGER, uuid TEXT, status TEXT);
		`); err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadKnowledgeSnapshot(context.Background(), path); err == nil || !strings.Contains(err.Error(), "real table") {
			t.Fatalf("ReadKnowledgeSnapshot() error = %v, want real-table rejection", err)
		}
	})
}

func BenchmarkReadKnowledgeSnapshotTenThousandRows(b *testing.B) {
	path := filepath.Join(b.TempDir(), "legacy-knowledge-benchmark.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		b.Fatal(err)
	}
	createLegacyKnowledgeSchema(b, database)
	tx, err := database.Begin()
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.Prepare(`INSERT INTO v2_knowledge VALUES (?, 'en-US', 'General', ?, ?, ?, 1, ?, ?)`)
	if err != nil {
		b.Fatal(err)
	}
	for index := 1; index <= 10_000; index++ {
		if _, err := statement.Exec(index, "Article "+strconv.Itoa(index), "Body "+strconv.Itoa(index), index, index, index); err != nil {
			b.Fatal(err)
		}
	}
	_ = statement.Close()
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	if err := database.Close(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		snapshot, err := ReadKnowledgeSnapshot(context.Background(), path)
		if err != nil || len(snapshot.Articles) != 10_000 {
			b.Fatalf("snapshot rows=%d error=%v", len(snapshot.Articles), err)
		}
	}
	b.ReportMetric(10_000, "rows/op")
}

func createLegacyKnowledgeSnapshot(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-knowledge.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	createLegacyKnowledgeSchema(t, database)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

type fataler interface{ Fatal(...any) }

func createLegacyKnowledgeSchema(t fataler, database *sql.DB) {
	if _, err := database.Exec(`
		CREATE TABLE v2_knowledge (
			id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, language TEXT NOT NULL, category TEXT NOT NULL,
			title TEXT NOT NULL, body TEXT NOT NULL, sort INTEGER, show INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
		);
		CREATE TABLE v2_knowledge_attachment (id INTEGER PRIMARY KEY, knowledge_id INTEGER, uuid TEXT, status TEXT, deleted_at INTEGER);
		CREATE TABLE v2_knowledge_attachment_upload (id INTEGER PRIMARY KEY, uuid TEXT, status TEXT);
	`); err != nil {
		t.Fatal(err)
	}
}
