package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/mailtemplate"
)

func TestMailTemplatesExposeFixedLegacyCatalogAndUsePerTemplateCAS(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "mail-template-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	templates, err := database.ListMailTemplates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []mailtemplate.Name{mailtemplate.Verify, mailtemplate.Notify, mailtemplate.RemindExpire, mailtemplate.RemindTraffic, mailtemplate.MailLogin}
	if len(templates) != len(want) {
		t.Fatalf("ListMailTemplates() length=%d, want %d", len(templates), len(want))
	}
	for index, name := range want {
		if templates[index].Name != name || templates[index].Subject == "" || templates[index].Content == "" || templates[index].Customized || templates[index].Revision != 1 {
			t.Fatalf("templates[%d]=%#v", index, templates[index])
		}
	}
	planRows, err := database.db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT name,subject,content,revision,updated_at FROM mail_templates WHERE name = ?`, mailtemplate.Notify)
	if err != nil {
		t.Fatal(err)
	}
	defer planRows.Close()
	indexed := false
	for planRows.Next() {
		var id, parent, unused int
		var detail string
		if err := planRows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		indexed = indexed || strings.Contains(detail, "SEARCH mail_templates USING INDEX")
	}
	if err := planRows.Err(); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Fatal("mail template primary-key lookup did not use an index")
	}
	summaries, err := database.ListMailTemplateSummaries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != len(want) || summaries[0].Name != mailtemplate.Verify || summaries[1].Customized {
		t.Fatalf("ListMailTemplateSummaries()=%#v", summaries)
	}

	initial := templates[1]
	updated, err := database.UpdateMailTemplate(ctx, administrator.ID, initial.Name, initial.Revision, SaveMailTemplateInput{
		Subject: "{{name}} - 自定义通知", Content: "<p>{{content}}</p><p>{{url}}</p>",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Customized || updated.Revision != 2 || updated.Subject != "{{name}} - 自定义通知" || !updated.UpdatedAt.Equal(now) {
		t.Fatalf("updated template=%#v", updated)
	}
	summaries, err = database.ListMailTemplateSummaries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !summaries[1].Customized || summaries[1].Revision != 2 || !summaries[1].UpdatedAt.Equal(now) {
		t.Fatalf("updated summary=%#v", summaries[1])
	}
	if _, err := database.UpdateMailTemplate(ctx, administrator.ID, initial.Name, initial.Revision, SaveMailTemplateInput{
		Subject: "stale", Content: "{{content}}",
	}, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale UpdateMailTemplate() error=%v, want ErrConflict", err)
	}
	reset, err := database.ResetMailTemplate(ctx, administrator.ID, initial.Name, updated.Revision, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if reset.Customized || reset.Revision != 3 || reset.Subject != initial.Subject || reset.Content != initial.Content {
		t.Fatalf("reset template=%#v", reset)
	}
}

func TestMailTemplateStoreRejectsInvalidNamesContentAndMissingCatalogRows(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "mail-template-invalid@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetMailTemplate(ctx, mailtemplate.Name("unknown")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown GetMailTemplate() error=%v, want ErrNotFound", err)
	}
	if _, err := database.UpdateMailTemplate(ctx, administrator.ID, mailtemplate.Verify, 1, SaveMailTemplateInput{
		Subject: "subject", Content: "missing code",
	}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid UpdateMailTemplate() error=%v, want ErrInvalidInput", err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM mail_templates WHERE name = 'verify'`); err != nil {
		t.Fatal(err)
	}
	if err := database.ValidateCurrentSchema(ctx); err == nil {
		t.Fatal("ValidateCurrentSchema() accepted an incomplete mail template catalog")
	}
}

func TestSchemaV48CreatesExactlyFiveMailTemplateRows(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	if _, err := database.db.ExecContext(ctx, `DROP TABLE mail_templates; PRAGMA user_version = 47`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v47 to v48) error=%v", err)
	}
	var version, rows int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mail_templates`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if version != 48 || rows != 5 {
		t.Fatalf("schema version=%d mail template rows=%d", version, rows)
	}
}
