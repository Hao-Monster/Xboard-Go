package store

import (
	"context"
	"strings"
	"testing"
)

func TestSchemaV35PreservesV34DataAndEnforcesAttachmentBoundaries(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	if _, err := database.db.ExecContext(ctx, `
		DROP TABLE knowledge_attachment_chunks;
		DROP TABLE knowledge_attachment_uploads;
		DROP TABLE knowledge_attachments;
		UPDATE app_settings SET app_name = 'V34 board' WHERE id = 1;
		PRAGMA user_version = 34;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var version int
	var appName string
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT app_name FROM app_settings WHERE id = 1`).Scan(&appName); err != nil {
		t.Fatal(err)
	}
	if version != CurrentSchemaVersion() || appName != "V34 board" {
		t.Fatalf("migration version=%d app=%q", version, appName)
	}
	for _, table := range []string{"knowledge_attachments", "knowledge_attachment_uploads", "knowledge_attachment_chunks"} {
		var count int
		if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count=%d error=%v", table, count, err)
		}
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		t.Fatal(err)
	}
	var queryPlan string
	if err := database.db.QueryRowContext(ctx, `
		EXPLAIN QUERY PLAN
		SELECT id, uuid, original_name FROM knowledge_attachments
		WHERE knowledge_id = 1 AND deleted_at IS NULL ORDER BY id DESC LIMIT 100 OFFSET 0
	`).Scan(new(int), new(int), new(int), &queryPlan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(queryPlan, "idx_knowledge_attachments_article_list") {
		t.Fatalf("article attachment list query plan = %q", queryPlan)
	}
}

func TestSchemaV35RejectsPreexistingAttachmentTableWithMissingColumns(t *testing.T) {
	database := newTestStore(t)
	if _, err := database.db.ExecContext(t.Context(), `
		DROP TABLE knowledge_attachment_chunks;
		DROP TABLE knowledge_attachment_uploads;
		DROP TABLE knowledge_attachments;
		CREATE TABLE knowledge_attachments (id INTEGER PRIMARY KEY);
		PRAGMA user_version = 34;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context()); err == nil || (!strings.Contains(err.Error(), "missing required column") && !strings.Contains(err.Error(), "no such column")) {
		t.Fatalf("Migrate(corrupt v35 table) error = %v", err)
	}
	var version int
	if err := database.db.QueryRowContext(t.Context(), `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 34 {
		t.Fatalf("failed migration committed version %d", version)
	}
}
