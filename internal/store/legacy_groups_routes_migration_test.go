package store

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestImportLegacyGroupsRoutesComposesWithContentAndIsIdempotent(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	contentTime := time.Date(2026, 8, 25, 18, 0, 0, 0, time.UTC)
	if _, err := database.ImportLegacyContent(ctx, validLegacyContentImport(t), contentTime); err != nil {
		t.Fatal(err)
	}
	input := validLegacyGroupsRoutesImport()
	now := contentTime.Add(time.Hour)
	report, err := database.ImportLegacyGroupsRoutes(ctx, input, now)
	if err != nil {
		t.Fatalf("ImportLegacyGroupsRoutes() error = %v", err)
	}
	if report.AlreadyApplied || report.Slice != LegacyGroupsRoutesSlice || report.Groups.SourceRows != 2 || report.Routes.SourceRows != 2 ||
		report.Groups.SourceChecksum != report.Groups.TargetChecksum || report.Routes.SourceChecksum != report.Routes.TargetChecksum {
		t.Fatalf("report = %#v", report)
	}
	groups, err := database.ListServerGroups(ctx)
	if err != nil || len(groups) != 2 || groups[0].ID != 9 || groups[0].Name != "Premium" || groups[1].ID != 3 || !groups[1].CreatedAt.Equal(time.Unix(100, 0).UTC()) {
		t.Fatalf("groups = (%#v, %v)", groups, err)
	}
	routes, err := database.ListRoutingRules(ctx)
	if err != nil || len(routes) != 2 || routes[0].ID != 12 || routes[1].ID != 4 || routes[1].ActionValue != "" || len(routes[1].Match) != 2 {
		t.Fatalf("routes = (%#v, %v)", routes, err)
	}
	repeated, err := database.ImportLegacyGroupsRoutes(ctx, input, now.Add(time.Minute))
	if err != nil || !repeated.AlreadyApplied || repeated.AppliedAt != now {
		t.Fatalf("repeated = (%#v, %v)", repeated, err)
	}
	var runs int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs`).Scan(&runs); err != nil || runs != 2 {
		t.Fatalf("migration runs = %d, err=%v", runs, err)
	}
}

func TestImportLegacyGroupsRoutesRecordsEmptySourceDomains(t *testing.T) {
	database := newTestStore(t)
	input := validLegacyGroupsRoutesImport()
	input.Groups = []LegacyServerGroup{}
	input.Routes = []LegacyRoutingRule{}
	input.Checksums = LegacyGroupsRoutesChecksums{
		Groups: LegacyServerGroupsChecksum(input.Groups), Routes: LegacyRoutingRulesChecksum(input.Routes),
	}
	report, err := database.ImportLegacyGroupsRoutes(context.Background(), input, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	if report.Groups.SourceRows != 0 || report.Groups.TargetRows != 0 || report.Routes.SourceRows != 0 || report.Routes.TargetRows != 0 || report.AlreadyApplied {
		t.Fatalf("empty import report = %#v", report)
	}
	var runs int
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyGroupsRoutesSlice).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("empty migration runs = %d, err=%v", runs, err)
	}
}

func TestImportLegacyGroupsRoutesRejectsInvalidAndNonEmptyTargetsAtomically(t *testing.T) {
	t.Run("checksum mismatch", func(t *testing.T) {
		database := newTestStore(t)
		input := validLegacyGroupsRoutesImport()
		input.Checksums.Groups = strings.Repeat("f", 64)
		if _, err := database.ImportLegacyGroupsRoutes(context.Background(), input, time.Now()); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("error = %v, want ErrInvalidInput", err)
		}
		assertLegacyGroupsRoutesEmpty(t, database)
	})

	t.Run("non empty target", func(t *testing.T) {
		database := newTestStore(t)
		if _, err := database.CreateServerGroup(context.Background(), "existing", time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := database.ImportLegacyGroupsRoutes(context.Background(), validLegacyGroupsRoutesImport(), time.Now()); !errors.Is(err, ErrConflict) {
			t.Fatalf("error = %v, want ErrConflict", err)
		}
		var groups, routes, runs int
		_ = database.db.QueryRow(`SELECT COUNT(*) FROM server_groups`).Scan(&groups)
		_ = database.db.QueryRow(`SELECT COUNT(*) FROM routing_rules`).Scan(&routes)
		_ = database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs`).Scan(&runs)
		if groups != 1 || routes != 0 || runs != 0 {
			t.Fatalf("partial target groups=%d routes=%d runs=%d", groups, routes, runs)
		}
	})

	t.Run("different source for completed slice", func(t *testing.T) {
		database := newTestStore(t)
		input := validLegacyGroupsRoutesImport()
		if _, err := database.ImportLegacyGroupsRoutes(context.Background(), input, time.Now()); err != nil {
			t.Fatal(err)
		}
		input.SourceSHA256 = strings.Repeat("d", 64)
		if _, err := database.ImportLegacyGroupsRoutes(context.Background(), input, time.Now()); !errors.Is(err, ErrConflict) {
			t.Fatalf("error = %v, want ErrConflict", err)
		}
	})
}

func TestConcurrentLegacyGroupsRoutesImportHasOneAtomicWinner(t *testing.T) {
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
	input := validLegacyGroupsRoutesImport()
	now := time.Date(2026, 8, 25, 20, 0, 0, 0, time.UTC)
	type outcome struct {
		report LegacyGroupsRoutesImportReport
		err    error
	}
	results := make(chan outcome, 2)
	var wait sync.WaitGroup
	for _, database := range []*Store{first, second} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			report, err := database.ImportLegacyGroupsRoutes(context.Background(), input, now)
			results <- outcome{report: report, err: err}
		}()
	}
	wait.Wait()
	close(results)
	alreadyApplied := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent error = %v", result.err)
		}
		if result.report.AlreadyApplied {
			alreadyApplied++
		}
	}
	if alreadyApplied != 1 {
		t.Fatalf("already applied = %d, want 1", alreadyApplied)
	}
}

func BenchmarkImportLegacyGroupsRoutesTenThousandRows(b *testing.B) {
	root := b.TempDir()
	input := validLegacyGroupsRoutesImport()
	input.Groups = make([]LegacyServerGroup, 5_000)
	input.Routes = make([]LegacyRoutingRule, 5_000)
	for index := range input.Groups {
		identity := int64(index + 1)
		input.Groups[index] = LegacyServerGroup{ID: identity, Name: "Group " + strconv.Itoa(index+1), CreatedAt: identity, UpdatedAt: identity}
		input.Routes[index] = LegacyRoutingRule{
			ID: identity, Remarks: "Route " + strconv.Itoa(index+1), Match: []string{"domain:example.test"},
			Action: "direct", ActionValue: "", CreatedAt: identity, UpdatedAt: identity,
		}
	}
	input.Checksums = LegacyGroupsRoutesChecksums{
		Groups: LegacyServerGroupsChecksum(input.Groups), Routes: LegacyRoutingRulesChecksum(input.Routes),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		b.StopTimer()
		database, err := OpenSQLite("file:" + filepath.ToSlash(filepath.Join(root, strconv.Itoa(index)+".db")))
		if err != nil {
			b.Fatal(err)
		}
		if err := database.Migrate(context.Background()); err != nil {
			_ = database.Close()
			b.Fatal(err)
		}
		b.StartTimer()
		report, err := database.ImportLegacyGroupsRoutes(context.Background(), input, time.Unix(1_800_000_000, 0))
		b.StopTimer()
		if err != nil {
			_ = database.Close()
			b.Fatal(err)
		}
		if report.Groups.TargetRows+report.Routes.TargetRows != 10_000 {
			_ = database.Close()
			b.Fatalf("target rows = %d, want 10000", report.Groups.TargetRows+report.Routes.TargetRows)
		}
		if err := database.Close(); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(10_000, "rows/op")
}

func validLegacyGroupsRoutesImport() LegacyGroupsRoutesImport {
	input := LegacyGroupsRoutesImport{
		Slice: LegacyGroupsRoutesSlice, SourceSHA256: strings.Repeat("b", 64), SourceSize: 8192,
		RollbackBackupPath: "/var/lib/xboard-backups/pre-groups-routes.xbbackup", RollbackBackupSHA256: strings.Repeat("e", 64),
		Groups: []LegacyServerGroup{
			{ID: 3, Name: "Standard", CreatedAt: 100, UpdatedAt: 110},
			{ID: 9, Name: "Premium", CreatedAt: 120, UpdatedAt: 130},
		},
		Routes: []LegacyRoutingRule{
			{ID: 4, Remarks: "Block ads", Match: []string{"domain:ads.example", `regexp:^tracker\.`}, Action: "block", ActionValue: "", CreatedAt: 200, UpdatedAt: 210},
			{ID: 12, Remarks: "Private DNS", Match: []string{"geosite:private"}, Action: "dns", ActionValue: "1.1.1.1", CreatedAt: 220, UpdatedAt: 230},
		},
	}
	input.Checksums = LegacyGroupsRoutesChecksums{
		Groups: LegacyServerGroupsChecksum(input.Groups), Routes: LegacyRoutingRulesChecksum(input.Routes),
	}
	return input
}

func assertLegacyGroupsRoutesEmpty(t *testing.T, database *Store) {
	t.Helper()
	var groups, routes, runs int
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM server_groups`).Scan(&groups); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM routing_rules`).Scan(&routes); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if groups != 0 || routes != 0 || runs != 0 {
		t.Fatalf("partial import groups=%d routes=%d runs=%d", groups, routes, runs)
	}
}
