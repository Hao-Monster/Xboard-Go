package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestRunCommandImportsLegacyMailTemplatesWithBackupAndBodyFreeReport(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy-mail-templates.db")
	source, err := sql.Open("sqlite", "file:"+sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	secretBody := "<p>migration-body-marker {{code}}</p>"
	if _, err := source.Exec(`CREATE TABLE v2_mail_templates(id INTEGER PRIMARY KEY,name TEXT,subject TEXT,content TEXT); INSERT INTO v2_mail_templates(name,subject,content) VALUES('verify','{{name}} migrated',?)`, secretBody); err != nil {
		t.Fatal(err)
	}
	_ = source.Close()
	targetPath := filepath.Join(directory, "target.db")
	target, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = target.Close()
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	backupPath := filepath.Join(directory, "pre-mail-templates.xbbackup")
	now := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{"migration", "import-legacy-mail-templates", "--source", sourcePath, "--backup-output", backupPath, "--confirm-offline"}, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || stderr.Len() != 0 || strings.Contains(stdout.String(), secretBody) || strings.Contains(stdout.String(), "migration-body-marker") {
		t.Fatalf("command handled=%t err=%v stdout=%s stderr=%s", handled, err, stdout.String(), stderr.String())
	}
	var result legacyMailTemplatesMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Result.Templates.SourceRows != 1 || result.Result.Templates.SourceChecksum != result.Result.Templates.TargetChecksum {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	inspection, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	template, err := inspection.GetMailTemplate(t.Context(), "verify")
	_ = inspection.Close()
	if err != nil || !template.Customized || template.Subject != "{{name}} migrated" || template.Content != secretBody {
		t.Fatalf("migrated template=%#v err=%v", template, err)
	}

	stdout.Reset()
	handled, err = runCommand(t.Context(), []string{"migration", "import-legacy-mail-templates", "--source", sourcePath, "--confirm-offline"}, &stdout, &stderr, func() time.Time { return now.Add(time.Hour) })
	if !handled || err != nil || json.Unmarshal(stdout.Bytes(), &result) != nil || !result.Result.AlreadyApplied || !result.Result.AppliedAt.Equal(now) {
		t.Fatalf("idempotent command handled=%t err=%v result=%#v", handled, err, result)
	}
}
