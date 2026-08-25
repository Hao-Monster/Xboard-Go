package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestImportLegacyContentIsAtomicVerifiableAndIdempotent(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 25, 18, 0, 0, 0, time.UTC)
	input := validLegacyContentImport(t)

	report, err := database.ImportLegacyContent(ctx, input, now)
	if err != nil {
		t.Fatalf("ImportLegacyContent() error = %v", err)
	}
	if report.AlreadyApplied || report.Slice != LegacyContentSlice || report.SourceSHA256 != input.SourceSHA256 || report.RollbackBackupPath != input.RollbackBackupPath {
		t.Fatalf("report = %#v", report)
	}
	if report.SiteSettings.SourceRows != 5 || report.Notices.SourceRows != 2 || report.ClientCatalog.SourceRows != 2 ||
		report.SiteSettings.SourceChecksum != report.SiteSettings.TargetChecksum || report.Notices.SourceChecksum != report.Notices.TargetChecksum || report.ClientCatalog.SourceChecksum != report.ClientCatalog.TargetChecksum {
		t.Fatalf("domain verification = %#v", report)
	}
	settings, err := database.GetSiteSettings(ctx)
	if err != nil || settings.AppName != "Legacy Board" || settings.AppDescription != "Legacy description" || settings.Logo != "https://images.example.test/logo.svg" {
		t.Fatalf("settings = (%#v, %v)", settings, err)
	}
	notices, err := database.ListNotices(ctx)
	if err != nil || len(notices) != 2 || notices[0].ID != 20 || notices[1].ID != 10 || notices[1].Content != "  preserved body\n" {
		t.Fatalf("notices = (%#v, %v)", notices, err)
	}
	catalog, err := database.GetClientCatalogConfig(ctx)
	if err != nil || catalog.Revision != 2 || !reflect.DeepEqual(catalog.Links, input.ClientCatalogLinks) {
		t.Fatalf("catalog = (%#v, %v)", catalog, err)
	}

	repeated, err := database.ImportLegacyContent(ctx, input, now.Add(time.Minute))
	if err != nil || !repeated.AlreadyApplied || repeated.AppliedAt != now {
		t.Fatalf("repeated import = (%#v, %v)", repeated, err)
	}
	var runs int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs`).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("migration runs = %d, err=%v", runs, err)
	}

	different := input
	different.SourceSHA256 = strings.Repeat("b", 64)
	if _, err := database.ImportLegacyContent(ctx, different, now.Add(2*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("different source error = %v, want ErrConflict", err)
	}
}

func TestConcurrentLegacyContentImportHasOneAtomicWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	first, err := OpenSQLite("file:" + filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	second, err := OpenSQLite("file:" + filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	input := validLegacyContentImport(t)
	now := time.Date(2026, 8, 25, 18, 30, 0, 0, time.UTC)
	type outcome struct {
		report LegacyContentImportReport
		err    error
	}
	results := make(chan outcome, 2)
	var group sync.WaitGroup
	for _, database := range []*Store{first, second} {
		group.Add(1)
		go func() {
			defer group.Done()
			report, err := database.ImportLegacyContent(context.Background(), input, now)
			results <- outcome{report: report, err: err}
		}()
	}
	group.Wait()
	close(results)
	alreadyApplied := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent import error = %v", result.err)
		}
		if result.report.AlreadyApplied {
			alreadyApplied++
		}
	}
	if alreadyApplied != 1 {
		t.Fatalf("idempotent concurrent results = %d, want 1", alreadyApplied)
	}
	var runs, notices int
	if err := first.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := first.db.QueryRow(`SELECT COUNT(*) FROM notices`).Scan(&notices); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || notices != len(input.Notices) {
		t.Fatalf("concurrent target runs=%d notices=%d", runs, notices)
	}
}

func TestImportLegacyContentRejectsInvalidOrNonPristineTargetsWithoutPartialWrites(t *testing.T) {
	t.Run("invalid notice", func(t *testing.T) {
		database := newTestStore(t)
		input := validLegacyContentImport(t)
		input.Notices[1].ImageURL = "javascript:alert(1)"
		input.Checksums.Notices = LegacyNoticesChecksum(input.Notices)
		if _, err := database.ImportLegacyContent(context.Background(), input, time.Now()); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid import error = %v, want ErrInvalidInput", err)
		}
		assertLegacyImportEmpty(t, database)
	})

	t.Run("administrator content exists", func(t *testing.T) {
		database := newTestStore(t)
		if _, err := database.CreateNotice(context.Background(), SaveNoticeInput{Title: "existing", Content: "existing"}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := database.ImportLegacyContent(context.Background(), validLegacyContentImport(t), time.Now()); !errors.Is(err, ErrConflict) {
			t.Fatalf("non-pristine import error = %v, want ErrConflict", err)
		}
		var runs int
		if err := database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs`).Scan(&runs); err != nil || runs != 0 {
			t.Fatalf("migration runs = %d, err=%v", runs, err)
		}
	})
}

func TestSchemaV24AddsLegacyMigrationLedgerWithoutChangingBusinessData(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	if _, err := database.CreateNotice(ctx, SaveNoticeInput{Title: "before", Content: "preserved", Visible: true}, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `PRAGMA user_version = 23; DROP TABLE legacy_migration_runs`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var version, notices, ledger int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notices`).Scan(&notices); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='legacy_migration_runs'`).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if version != 24 || notices != 1 || ledger != 1 {
		t.Fatalf("migration result version=%d notices=%d ledger=%d", version, notices, ledger)
	}
}

func validLegacyContentImport(t *testing.T) LegacyContentImport {
	t.Helper()
	input := LegacyContentImport{
		Slice: LegacyContentSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 4096,
		RollbackBackupPath: "/var/lib/xboard-backups/pre-legacy.xbbackup", RollbackBackupSHA256: strings.Repeat("c", 64),
		SiteSettings: LegacySiteSettings{
			AppName: pointer("Legacy Board"), AppDescription: pointer("Legacy description"),
			AppURL: pointer("https://legacy.example.test"), TOSURL: pointer("https://legacy.example.test/terms"),
			Logo: pointer("https://images.example.test/logo.svg"),
		},
		Notices: []LegacyNotice{
			{ID: 10, SortPosition: 2, Title: "Second", Content: "  preserved body\n", Tags: []string{"ops", "ops"}, Visible: true, CreatedAt: 100, UpdatedAt: 110},
			{ID: 20, SortPosition: 1, Title: "First", Content: "first", ImageURL: "https://images.example.test/first.png", Tags: []string{}, CreatedAt: 120, UpdatedAt: 130},
		},
		ClientCatalogPresent: true,
		ClientCatalogLinks: []ClientCatalogOverride{
			{ClientID: "karing", Platform: "android", Action: "tutorial", URL: "/guide/12/karing"},
			{ClientID: "koalaclash", Platform: "windows", Action: "cloud", URL: "https://cloud.example.test/clash"},
		},
	}
	input.Checksums = LegacyContentChecksums{
		SiteSettings:  LegacySiteSettingsChecksum(input.SiteSettings),
		Notices:       LegacyNoticesChecksum(input.Notices),
		ClientCatalog: LegacyClientCatalogChecksum(input.ClientCatalogLinks),
	}
	return input
}

func assertLegacyImportEmpty(t *testing.T, database *Store) {
	t.Helper()
	var notices, links, runs int
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM notices`).Scan(&notices); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM client_catalog_links`).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetSiteSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if notices != 0 || links != 0 || runs != 0 || settings.AppName != "Xboard-Go" || settings.Revision != 1 {
		t.Fatalf("partial import notices=%d links=%d runs=%d settings=%#v", notices, links, runs, settings)
	}
}

func pointer(value string) *string { return &value }
