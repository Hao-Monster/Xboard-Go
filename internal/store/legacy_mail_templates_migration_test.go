package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/mailtemplate"
)

func TestImportLegacyMailTemplatesIsVerifiedBodyFreeAndIdempotent(t *testing.T) {
	database := newTestStore(t)
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	templates := []LegacyMailTemplate{
		{Name: mailtemplate.Verify, Subject: "{{name}} legacy verify", Content: "<p>secret-shaped body {{code}}</p>"},
		{Name: mailtemplate.Notify, Subject: "{{name}} legacy notify", Content: "<p>{{content}}</p>"},
	}
	input := LegacyMailTemplatesImport{
		Slice: LegacyMailTemplatesSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 4096,
		Templates: templates, Checksum: LegacyMailTemplatesChecksum(templates),
		RollbackBackupPath: "E:/backup/pre-mail-templates.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
	}
	report, err := database.ImportLegacyMailTemplates(t.Context(), input, now)
	if err != nil || report.Templates.SourceRows != 2 || report.Templates.TargetRows != 2 || report.Templates.SourceChecksum != report.Templates.TargetChecksum {
		t.Fatalf("ImportLegacyMailTemplates()=(%#v,%v)", report, err)
	}
	verify, err := database.GetMailTemplate(t.Context(), mailtemplate.Verify)
	if err != nil || !verify.Customized || verify.Revision != 2 || verify.Subject != templates[0].Subject || verify.Content != templates[0].Content {
		t.Fatalf("migrated verify=%#v err=%v", verify, err)
	}
	encoded, err := json.Marshal(report)
	if err != nil || strings.Contains(string(encoded), "secret-shaped body") || strings.Contains(string(encoded), templates[0].Subject) {
		t.Fatalf("migration report exposed template body: %s err=%v", encoded, err)
	}
	repeated, err := database.ImportLegacyMailTemplates(t.Context(), input, now.Add(time.Hour))
	if err != nil || !repeated.AlreadyApplied || !repeated.AppliedAt.Equal(now) {
		t.Fatalf("idempotent import=(%#v,%v)", repeated, err)
	}
}

func TestImportLegacyMailTemplatesRequiresValidSourceAndPristineTarget(t *testing.T) {
	database := newTestStore(t)
	now := time.Unix(100, 0).UTC()
	administrator, err := database.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "migration-template@example.test", PasswordHash: "hash", IsAdmin: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := database.GetMailTemplate(t.Context(), mailtemplate.Notify)
	if _, err := database.UpdateMailTemplate(t.Context(), administrator.ID, current.Name, current.Revision, SaveMailTemplateInput{Subject: "subject", Content: "{{content}}"}, now); err != nil {
		t.Fatal(err)
	}
	templates := []LegacyMailTemplate{{Name: mailtemplate.Verify, Subject: "subject", Content: "{{code}}"}}
	input := LegacyMailTemplatesImport{
		Slice: LegacyMailTemplatesSlice, SourceSHA256: strings.Repeat("c", 64), SourceSize: 1,
		Templates: templates, Checksum: LegacyMailTemplatesChecksum(templates),
		RollbackBackupPath: "E:/backup/pre.xbbackup", RollbackBackupSHA256: strings.Repeat("d", 64),
	}
	if _, err := database.ImportLegacyMailTemplates(t.Context(), input, now); err == nil || !strings.Contains(err.Error(), "pristine") {
		t.Fatalf("non-pristine import error=%v", err)
	}
	input.Templates[0].Content = "missing required placeholder"
	input.Checksum = LegacyMailTemplatesChecksum(input.Templates)
	if err := ValidateLegacyMailTemplatesData(input.Templates); err == nil {
		t.Fatal("ValidateLegacyMailTemplatesData() accepted missing required placeholder")
	}
}
