package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKnowledgeLifecycleVisibilityLanguageSearchAndOrdering(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 16, 0, 0, 0, time.UTC)

	first, err := database.CreateKnowledge(ctx, SaveKnowledgeInput{
		Language: "zh-CN", Category: "入门", Title: "连接指南",
		Body: "站点 {{siteName}}\n\n<!--access start-->私有 {{subscribeUrl}}<!--access end-->", Visible: true,
	}, now)
	if err != nil {
		t.Fatalf("CreateKnowledge(first) error = %v", err)
	}
	second, err := database.CreateKnowledge(ctx, SaveKnowledgeInput{
		Language: "en-US", Category: "Guides", Title: "Desktop setup", Body: "Install desktop client", Visible: true,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("CreateKnowledge(second) error = %v", err)
	}
	hidden, err := database.CreateKnowledge(ctx, SaveKnowledgeInput{
		Language: "zh-CN", Category: "入门", Title: "隐藏草稿", Body: "secret draft", Visible: false,
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("CreateKnowledge(hidden) error = %v", err)
	}

	admin, err := database.ListKnowledge(ctx)
	if err != nil || len(admin) != 3 {
		t.Fatalf("ListKnowledge() = (%#v, %v), want 3 articles", admin, err)
	}
	if got := admin[0].ID; got != hidden.ID {
		t.Fatalf("new articles must sort first before an explicit reorder: first id=%d want %d", got, hidden.ID)
	}

	visibleZH, err := database.ListVisibleKnowledge(ctx, "zh-CN", "连接")
	if err != nil || len(visibleZH) != 1 || visibleZH[0].ID != first.ID || visibleZH[0].Body == "" {
		t.Fatalf("ListVisibleKnowledge(zh search) = (%#v, %v)", visibleZH, err)
	}
	visibleEN, err := database.ListVisibleKnowledge(ctx, "en-US", "desktop")
	if err != nil || len(visibleEN) != 1 || visibleEN[0].ID != second.ID {
		t.Fatalf("ListVisibleKnowledge(en search) = (%#v, %v)", visibleEN, err)
	}
	if _, err := database.GetVisibleKnowledge(ctx, hidden.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetVisibleKnowledge(hidden) error = %v, want ErrNotFound", err)
	}

	updated, err := database.UpdateKnowledge(ctx, first.ID, first.Revision, SaveKnowledgeInput{
		Language: "zh-CN", Category: "高级", Title: "连接指南 2", Body: "updated", Visible: false,
	}, now.Add(3*time.Second))
	if err != nil || updated.Revision != first.Revision+1 || updated.Visible {
		t.Fatalf("UpdateKnowledge() = (%#v, %v)", updated, err)
	}
	if _, err := database.UpdateKnowledge(ctx, first.ID, first.Revision, SaveKnowledgeInput{
		Language: "zh-CN", Category: "高级", Title: "stale", Body: "stale", Visible: true,
	}, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale UpdateKnowledge() error = %v, want ErrConflict", err)
	}

	if err := database.ReorderKnowledge(ctx, []int64{second.ID, hidden.ID, first.ID}, now.Add(4*time.Second)); err != nil {
		t.Fatalf("ReorderKnowledge() error = %v", err)
	}
	ordered, _ := database.ListKnowledge(ctx)
	if len(ordered) != 3 || ordered[0].ID != second.ID || ordered[1].ID != hidden.ID || ordered[2].ID != first.ID {
		t.Fatalf("reordered articles = %#v", ordered)
	}
	if err := database.ReorderKnowledge(ctx, []int64{second.ID, hidden.ID}, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("partial ReorderKnowledge() error = %v, want ErrConflict", err)
	}

	categories, err := database.ListKnowledgeCategories(ctx)
	if err != nil || len(categories) != 3 || categories[0] != "Guides" || categories[1] != "入门" || categories[2] != "高级" {
		t.Fatalf("ListKnowledgeCategories() = (%#v, %v)", categories, err)
	}

	if err := database.DeleteKnowledge(ctx, second.ID, second.Revision); err != nil {
		t.Fatalf("DeleteKnowledge() error = %v", err)
	}
	if err := database.DeleteKnowledge(ctx, second.ID, second.Revision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second DeleteKnowledge() error = %v, want ErrNotFound", err)
	}
}

func TestKnowledgeValidationBoundsAndDatabaseMigration(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 16, 0, 0, 0, time.UTC)

	for name, input := range map[string]SaveKnowledgeInput{
		"missing language":     {Category: "Guide", Title: "Title", Body: "Body"},
		"unsupported language": {Language: "../../", Category: "Guide", Title: "Title", Body: "Body"},
		"control title":        {Language: "zh-CN", Category: "Guide", Title: "bad\x00title", Body: "Body"},
		"empty category":       {Language: "zh-CN", Title: "Title", Body: "Body"},
		"empty body":           {Language: "zh-CN", Category: "Guide", Title: "Title"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := database.CreateKnowledge(ctx, input, now); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("CreateKnowledge() error = %v, want ErrInvalidInput", err)
			}
		})
	}

	var version int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, currentSchemaVersion)
	}
	var missingTokens int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE subscription_token IS NULL OR length(subscription_token) <> 32`).Scan(&missingTokens); err != nil {
		t.Fatal(err)
	}
	if missingTokens != 0 {
		t.Fatalf("users without a 32-character subscription token = %d", missingTokens)
	}

	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO users (email, password_hash, created_at, updated_at)
		VALUES ('raw-insert@example.test', 'test-hash', ?, ?)
	`, now.Unix(), now.Unix()); err == nil {
		t.Fatal("database accepted a user without a subscription token")
	}
	token := testSubscriptionToken(t)
	result, err := database.db.ExecContext(ctx, `
		INSERT INTO users (email, password_hash, subscription_token, created_at, updated_at)
		VALUES ('raw-insert@example.test', 'test-hash', ?, ?, ?)
	`, token, now.Unix(), now.Unix())
	if err != nil {
		t.Fatalf("raw user insert with token error = %v", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	var storedToken string
	if err := database.db.QueryRowContext(ctx, `SELECT subscription_token FROM users WHERE id = ?`, userID).Scan(&storedToken); err != nil {
		t.Fatal(err)
	}
	if storedToken != token {
		t.Fatalf("stored subscription token did not match the supplied CSPRNG token")
	}
}

func TestSchemaV7BackfillsExistingUsersWithUniqueTokens(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "knowledge-v6.db")
	database, err := OpenSQLite("file:" + filepath.ToSlash(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	for version, schema := range []string{schemaV1, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6} {
		if _, err := database.db.ExecContext(ctx, schema); err != nil {
			t.Fatalf("apply schema v%d: %v", version+1, err)
		}
	}
	now := time.Date(2026, 8, 24, 16, 0, 0, 0, time.UTC).Unix()
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO users (email, password_hash, created_at, updated_at)
		VALUES ('legacy-one@example.test', 'hash', ?, ?), ('legacy-two@example.test', 'hash', ?, ?)
	`, now, now, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `PRAGMA user_version = 6`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v6 to current) error = %v", err)
	}
	rows, err := database.db.QueryContext(ctx, `SELECT subscription_token FROM users ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	tokens := make([]string, 0, 2)
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 2 || len(tokens[0]) != 32 || len(tokens[1]) != 32 || tokens[0] == tokens[1] {
		t.Fatalf("migrated subscription token formats or uniqueness are invalid")
	}
}

func TestKnowledgeReadPathsUseOrderingIndexes(t *testing.T) {
	database := newTestStore(t)
	for name, query := range map[string]string{
		"admin":  `SELECT id FROM knowledge ORDER BY sort_position, id DESC`,
		"user":   `SELECT id FROM knowledge WHERE visible = 1 AND language = 'zh-CN' ORDER BY sort_position, id DESC`,
		"public": `SELECT id FROM knowledge WHERE visible = 1 ORDER BY sort_position, id DESC`,
	} {
		t.Run(name, func(t *testing.T) {
			rows, err := database.db.Query(`EXPLAIN QUERY PLAN ` + query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var plan strings.Builder
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plan.WriteString(detail)
			}
			if !strings.Contains(plan.String(), "idx_knowledge_") || strings.Contains(plan.String(), "USE TEMP B-TREE FOR ORDER BY") {
				t.Fatalf("query plan does not use a knowledge ordering index: %s", plan.String())
			}
		})
	}
}
