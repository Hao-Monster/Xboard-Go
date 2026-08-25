package legacymigration

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReadContentSnapshotValidatesAndCanonicalizesLegacyData(t *testing.T) {
	path := createLegacyContentSnapshot(t, `{
		"karing":{"android":{"direct":"https://downloads.example.test/karing.apk","tutorial":"/guide/12/karing"}},
		"koalaclash":{"windows":{"cloud":"https://cloud.example.test/clash"}}
	}`)
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO v2_settings (name, value) VALUES ('server_token', 'must-never-be-read');
		INSERT INTO v2_notice (id, sort, title, content, img_url, tags, show, created_at, updated_at)
		VALUES
			(41, NULL, 'Migration first', 'first body', NULL, '["ops","news"]', 1, 1700000000, 1700000010),
			(92, 3, 'Migration second', 'second body', 'https://images.example.test/notice.png', '[]', 0, 1700000020, 1700000030);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	snapshot, err := ReadContentSnapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadContentSnapshot() error = %v", err)
	}
	if snapshot.Path != path || snapshot.Size <= 0 || len(snapshot.SHA256) != 64 || strings.Contains(snapshot.SHA256, "must-never") {
		t.Fatalf("snapshot identity = %#v", snapshot)
	}
	if snapshot.SiteSettings.AppName == nil || *snapshot.SiteSettings.AppName != "迁移面板" || snapshot.SiteSettings.AppURL == nil || *snapshot.SiteSettings.AppURL != "https://legacy.example.test" {
		t.Fatalf("site settings = %#v", snapshot.SiteSettings)
	}
	if snapshot.SiteSettings.PresentCount() != 5 {
		t.Fatalf("site setting count = %d, want 5", snapshot.SiteSettings.PresentCount())
	}
	if len(snapshot.Notices) != 2 || snapshot.Notices[0].ID != 41 || snapshot.Notices[0].SortPosition != 0 || snapshot.Notices[1].ID != 92 || snapshot.Notices[1].SortPosition != 3 {
		t.Fatalf("notices = %#v", snapshot.Notices)
	}
	if len(snapshot.ClientCatalogLinks) != 3 || snapshot.ClientCatalogLinks[0].ClientID != "karing" || snapshot.ClientCatalogLinks[2].ClientID != "koalaclash" {
		t.Fatalf("client links = %#v", snapshot.ClientCatalogLinks)
	}
	if snapshot.Checksums.SiteSettings == "" || snapshot.Checksums.Notices == "" || snapshot.Checksums.ClientCatalog == "" {
		t.Fatalf("checksums = %#v", snapshot.Checksums)
	}
}

func TestReadContentSnapshotRejectsMutableOrUntrustedSources(t *testing.T) {
	t.Run("adjacent WAL", func(t *testing.T) {
		path := createLegacyContentSnapshot(t, `{}`)
		if err := os.WriteFile(path+"-wal", []byte("live"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadContentSnapshot(context.Background(), path); err == nil || !strings.Contains(err.Error(), "WAL") {
			t.Fatalf("ReadContentSnapshot() error = %v, want WAL rejection", err)
		}
	})

	t.Run("symbolic link", func(t *testing.T) {
		target := createLegacyContentSnapshot(t, `{}`)
		link := filepath.Join(t.TempDir(), "legacy-link.db")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		if _, err := ReadContentSnapshot(context.Background(), link); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("ReadContentSnapshot() error = %v, want symbolic-link rejection", err)
		}
	})

	t.Run("unsafe client URL", func(t *testing.T) {
		path := createLegacyContentSnapshot(t, `{"karing":{"android":{"direct":"http://downloads.example.test/app.apk"}}}`)
		if _, err := ReadContentSnapshot(context.Background(), path); err == nil || !strings.Contains(err.Error(), "client_catalog_links") {
			t.Fatalf("ReadContentSnapshot() error = %v, want client validation", err)
		}
	})

	t.Run("view masquerades as table", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "legacy-view.db")
		database, err := sql.Open("sqlite", "file:"+path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`
			CREATE TABLE backing_settings (name TEXT, value TEXT);
			CREATE TABLE backing_notices (id INTEGER, sort INTEGER, title TEXT, content TEXT, img_url TEXT, tags TEXT, show INTEGER, created_at INTEGER, updated_at INTEGER);
			CREATE VIEW v2_settings AS SELECT name, value FROM backing_settings;
			CREATE VIEW v2_notice AS SELECT * FROM backing_notices;
		`); err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadContentSnapshot(context.Background(), path); err == nil || !strings.Contains(err.Error(), "real table") {
			t.Fatalf("ReadContentSnapshot() error = %v, want table validation", err)
		}
	})

	t.Run("required column missing", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "legacy-missing-column.db")
		database, err := sql.Open("sqlite", "file:"+path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`
			CREATE TABLE v2_settings (name TEXT NOT NULL UNIQUE, value TEXT);
			CREATE TABLE v2_notice (id INTEGER PRIMARY KEY, title TEXT, content TEXT, show INTEGER, img_url TEXT, tags TEXT, created_at INTEGER, updated_at INTEGER);
		`); err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadContentSnapshot(context.Background(), path); err == nil || !strings.Contains(err.Error(), `column "sort"`) {
			t.Fatalf("ReadContentSnapshot() error = %v, want required column rejection", err)
		}
	})

	t.Run("duplicate public setting", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "legacy-duplicate-setting.db")
		database, err := sql.Open("sqlite", "file:"+path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`
			CREATE TABLE v2_settings (name TEXT NOT NULL, value TEXT);
			CREATE TABLE v2_notice (
				id INTEGER, sort INTEGER, title TEXT, content TEXT, img_url TEXT,
				tags TEXT, show INTEGER, created_at INTEGER, updated_at INTEGER
			);
			INSERT INTO v2_settings (name, value) VALUES ('app_name', 'first'), ('app_name', 'second');
		`); err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadContentSnapshot(context.Background(), path); err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("ReadContentSnapshot() error = %v, want duplicate-setting rejection", err)
		}
	})
}

func BenchmarkReadContentSnapshotTenThousandNotices(b *testing.B) {
	path := filepath.Join(b.TempDir(), "legacy-benchmark.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_settings (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, value TEXT);
		CREATE TABLE v2_notice (
			id INTEGER PRIMARY KEY, sort INTEGER, title TEXT NOT NULL, content TEXT NOT NULL,
			img_url TEXT, tags TEXT, show INTEGER NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
		);
		INSERT INTO v2_settings (name, value) VALUES ('app_name', 'Migration benchmark');
	`); err != nil {
		_ = database.Close()
		b.Fatal(err)
	}
	tx, err := database.Begin()
	if err != nil {
		_ = database.Close()
		b.Fatal(err)
	}
	statement, err := tx.Prepare(`
		INSERT INTO v2_notice (id, sort, title, content, img_url, tags, show, created_at, updated_at)
		VALUES (?, ?, ?, ?, NULL, '[]', 1, ?, ?)
	`)
	if err != nil {
		_ = tx.Rollback()
		_ = database.Close()
		b.Fatal(err)
	}
	for index := 1; index <= 10_000; index++ {
		if _, err := statement.Exec(index, index, "Notice "+strconv.Itoa(index), strings.Repeat("x", 256), index, index); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			_ = database.Close()
			b.Fatal(err)
		}
	}
	if err := statement.Close(); err != nil {
		_ = tx.Rollback()
		_ = database.Close()
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		_ = database.Close()
		b.Fatal(err)
	}
	if err := database.Close(); err != nil {
		b.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		snapshot, err := ReadContentSnapshot(context.Background(), path)
		if err != nil {
			b.Fatal(err)
		}
		if len(snapshot.Notices) != 10_000 {
			b.Fatalf("notices = %d, want 10000", len(snapshot.Notices))
		}
	}
	b.ReportMetric(float64(info.Size()), "snapshot-bytes")
	b.ReportMetric(10_000, "notices/op")
}

func createLegacyContentSnapshot(t *testing.T, clientCatalogJSON string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_settings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			"group" TEXT,
			type TEXT,
			name TEXT NOT NULL UNIQUE,
			value TEXT,
			created_at DATETIME,
			updated_at DATETIME
		);
		CREATE TABLE v2_notice (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			show INTEGER NOT NULL DEFAULT 0,
			img_url TEXT,
			tags TEXT,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			sort INTEGER
		);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	settings := map[string]*string{
		"app_name":             stringPointer("迁移面板"),
		"app_description":      stringPointer("脱敏迁移样本"),
		"app_url":              stringPointer("https://legacy.example.test"),
		"tos_url":              stringPointer("https://legacy.example.test/terms"),
		"logo":                 nil,
		"client_catalog_links": &clientCatalogJSON,
	}
	for name, value := range settings {
		if _, err := database.Exec(`INSERT INTO v2_settings (name, value) VALUES (?, ?)`, name, value); err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
