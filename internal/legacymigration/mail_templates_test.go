package legacymigration

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReadMailTemplatesSnapshotValidatesFixedCatalogAndBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-mail-templates.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE v2_mail_templates(id INTEGER PRIMARY KEY,name TEXT,subject TEXT,content TEXT,created_at TEXT,updated_at TEXT);
		INSERT INTO v2_mail_templates(name,subject,content) VALUES
		('verify','{{name}} legacy','<p>{{code}}</p>'),
		('notify','{{name}} notify','<p>{{content}}</p>')`); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	snapshot, err := ReadMailTemplatesSnapshot(t.Context(), path)
	if err != nil || len(snapshot.Templates) != 2 || snapshot.Templates[0].Name != "notify" || snapshot.Templates[1].Name != "verify" || snapshot.Checksum == "" || snapshot.SHA256 == "" {
		t.Fatalf("ReadMailTemplatesSnapshot()=(%#v,%v)", snapshot, err)
	}
}

func TestReadMailTemplatesSnapshotRejectsUnknownDuplicateAndMalformedTemplates(t *testing.T) {
	for name, rows := range map[string]string{
		"unknown":   `('unknown','subject','content')`,
		"duplicate": `('verify','subject','{{code}}'),('verify','subject','{{code}}')`,
		"malformed": `('verify','subject','missing code')`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid-mail-templates.db")
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`CREATE TABLE v2_mail_templates(name TEXT,subject TEXT,content TEXT); INSERT INTO v2_mail_templates(name,subject,content) VALUES ` + rows); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()
			if _, err := ReadMailTemplatesSnapshot(t.Context(), path); err == nil || !strings.Contains(err.Error(), "legacy mail template") {
				t.Fatalf("invalid snapshot error=%v", err)
			}
		})
	}
}
