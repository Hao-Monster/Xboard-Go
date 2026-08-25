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

func TestImportLegacyKnowledgeComposesWithPriorSlicesAndIsIdempotent(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	if _, err := database.ImportLegacyContent(ctx, validLegacyContentImport(t), time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ImportLegacyGroupsRoutes(ctx, validLegacyGroupsRoutesImport(), time.Unix(200, 0)); err != nil {
		t.Fatal(err)
	}
	input := validLegacyKnowledgeImport()
	report, err := database.ImportLegacyKnowledge(ctx, input, time.Unix(300, 0))
	if err != nil {
		t.Fatalf("ImportLegacyKnowledge() error = %v", err)
	}
	if report.AlreadyApplied || report.Slice != LegacyKnowledgeSlice || report.Articles.SourceRows != 2 ||
		report.Articles.SourceChecksum != report.Articles.TargetChecksum {
		t.Fatalf("report = %#v", report)
	}
	articles, err := database.ListKnowledge(ctx)
	if err != nil || len(articles) != 2 || articles[0].ID != 2 || articles[0].SortPosition != 0 || articles[0].Revision != 1 ||
		articles[1].ID != 7 || !articles[1].CreatedAt.Equal(time.Unix(120, 0).UTC()) {
		t.Fatalf("articles = (%#v, %v)", articles, err)
	}
	detail, err := database.GetKnowledge(ctx, 2)
	if err != nil || detail.Body != "# Hello" || detail.Language != "en-US" || detail.Category != "General" || detail.Title != "Getting started" ||
		!detail.UpdatedAt.Equal(time.Unix(110, 0).UTC()) {
		t.Fatalf("knowledge detail = (%#v, %v)", detail, err)
	}
	repeated, err := database.ImportLegacyKnowledge(ctx, input, time.Unix(400, 0))
	if err != nil || !repeated.AlreadyApplied || !repeated.AppliedAt.Equal(time.Unix(300, 0).UTC()) {
		t.Fatalf("repeated = (%#v, %v)", repeated, err)
	}
	var runs int
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs`).Scan(&runs); err != nil || runs != 3 {
		t.Fatalf("migration runs = %d, error=%v", runs, err)
	}
}

func TestImportLegacyKnowledgeRecordsEmptySource(t *testing.T) {
	database := newTestStore(t)
	input := validLegacyKnowledgeImport()
	input.Articles = []LegacyKnowledgeArticle{}
	input.Checksum = LegacyKnowledgeChecksum(input.Articles)
	report, err := database.ImportLegacyKnowledge(context.Background(), input, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	if report.Articles.SourceRows != 0 || report.Articles.TargetRows != 0 || report.AlreadyApplied {
		t.Fatalf("empty report = %#v", report)
	}
}

func TestLegacyKnowledgeChecksumMatchesCanonicalArrayWithoutWholeDomainBuffering(t *testing.T) {
	articles := validLegacyKnowledgeImport().Articles
	if got, want := LegacyKnowledgeChecksum(articles), legacyCanonicalChecksum(articles); got != want {
		t.Fatalf("checksum = %q, want canonical %q", got, want)
	}
	if got, want := LegacyKnowledgeChecksum(nil), legacyCanonicalChecksum([]LegacyKnowledgeArticle{}); got != want {
		t.Fatalf("empty checksum = %q, want canonical %q", got, want)
	}
}

func TestImportLegacyKnowledgeRejectsInvalidAndNonEmptyTargetsAtomically(t *testing.T) {
	t.Run("checksum mismatch", func(t *testing.T) {
		database := newTestStore(t)
		input := validLegacyKnowledgeImport()
		input.Checksum = strings.Repeat("f", 64)
		if _, err := database.ImportLegacyKnowledge(context.Background(), input, time.Now()); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("error = %v, want ErrInvalidInput", err)
		}
		assertLegacyKnowledgeEmpty(t, database)
	})
	t.Run("non empty target", func(t *testing.T) {
		database := newTestStore(t)
		if _, err := database.CreateKnowledge(context.Background(), SaveKnowledgeInput{Language: "en-US", Category: "Existing", Title: "Existing", Body: "Existing", Visible: true}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := database.ImportLegacyKnowledge(context.Background(), validLegacyKnowledgeImport(), time.Now()); !errors.Is(err, ErrConflict) {
			t.Fatalf("error = %v, want ErrConflict", err)
		}
		var articles, runs int
		_ = database.db.QueryRow(`SELECT COUNT(*) FROM knowledge`).Scan(&articles)
		_ = database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs`).Scan(&runs)
		if articles != 1 || runs != 0 {
			t.Fatalf("partial target articles=%d runs=%d", articles, runs)
		}
	})
	t.Run("different source", func(t *testing.T) {
		database := newTestStore(t)
		input := validLegacyKnowledgeImport()
		if _, err := database.ImportLegacyKnowledge(context.Background(), input, time.Now()); err != nil {
			t.Fatal(err)
		}
		input.SourceSHA256 = strings.Repeat("d", 64)
		if _, err := database.ImportLegacyKnowledge(context.Background(), input, time.Now()); !errors.Is(err, ErrConflict) {
			t.Fatalf("error = %v, want ErrConflict", err)
		}
	})
}

func TestConcurrentLegacyKnowledgeImportHasOneAtomicWinner(t *testing.T) {
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
	type outcome struct {
		report LegacyKnowledgeImportReport
		err    error
	}
	results := make(chan outcome, 2)
	var wait sync.WaitGroup
	for _, database := range []*Store{first, second} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			report, err := database.ImportLegacyKnowledge(context.Background(), validLegacyKnowledgeImport(), time.Unix(300, 0))
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

func BenchmarkImportLegacyKnowledgeTenThousandRows(b *testing.B) {
	root := b.TempDir()
	input := validLegacyKnowledgeImport()
	input.Articles = make([]LegacyKnowledgeArticle, 10_000)
	for index := range input.Articles {
		identity := int64(index + 1)
		input.Articles[index] = LegacyKnowledgeArticle{ID: identity, Language: "en-US", Category: "General", Title: "Article " + strconv.Itoa(index+1), Body: "Body", SortPosition: index + 1, Visible: true, CreatedAt: identity, UpdatedAt: identity}
	}
	input.Checksum = LegacyKnowledgeChecksum(input.Articles)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		b.StopTimer()
		database, err := OpenSQLite("file:" + filepath.ToSlash(filepath.Join(root, strconv.Itoa(index)+".db")))
		if err != nil {
			b.Fatal(err)
		}
		if err := database.Migrate(context.Background()); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		report, err := database.ImportLegacyKnowledge(context.Background(), input, time.Unix(300, 0))
		b.StopTimer()
		if err != nil || report.Articles.TargetRows != 10_000 {
			b.Fatalf("report=%#v error=%v", report, err)
		}
		if err := database.Close(); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(10_000, "rows/op")
}

func validLegacyKnowledgeImport() LegacyKnowledgeImport {
	input := LegacyKnowledgeImport{
		Slice: LegacyKnowledgeSlice, SourceSHA256: strings.Repeat("c", 64), SourceSize: 16_384,
		RollbackBackupPath: "/var/lib/xboard-backups/pre-knowledge.xbbackup", RollbackBackupSHA256: strings.Repeat("e", 64),
		Articles: []LegacyKnowledgeArticle{
			{ID: 2, Language: "en-US", Category: "General", Title: "Getting started", Body: "# Hello", SortPosition: 0, Visible: true, CreatedAt: 100, UpdatedAt: 110},
			{ID: 7, Language: "zh-CN", Category: "使用指南", Title: "客户端设置", Body: "正文 {{siteName}}", SortPosition: 5, Visible: false, CreatedAt: 120, UpdatedAt: 130},
		},
	}
	input.Checksum = LegacyKnowledgeChecksum(input.Articles)
	return input
}

func assertLegacyKnowledgeEmpty(t *testing.T, database *Store) {
	t.Helper()
	var articles, runs int
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM knowledge`).Scan(&articles); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if articles != 0 || runs != 0 {
		t.Fatalf("partial import articles=%d runs=%d", articles, runs)
	}
}
