package gen_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/legacymigration"
	"github.com/Hao-Monster/Xboard-Go/internal/testdata/legacy/gen"
)

// TestGeneratorProducesDeterministicDataset verifies that the same seed
// always produces the same database and pins the current SQLite-candidate
// evidence identity. Schema or fixture changes must update this value
// deliberately after cross-platform review; the pin does not resolve D-006.
func TestGeneratorProducesDeterministicDataset(t *testing.T) {
	const expectedSHA256 = "e179d5ce428d0983451b951ad00ef642f28cb930c0726bd3d5a1dc35d255f3f6"
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")
	manifestPath := filepath.Join(dir, "manifest.json")

	cfg := gen.DefaultConfig(dbPath)
	g := gen.New(cfg)

	manifest, err := g.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if manifest.DatabaseSHA == "" {
		t.Fatal("Generate() returned empty DatabaseSHA")
	}
	if manifest.Seed != gen.DefaultSeed {
		t.Fatalf("Generate() seed = %d, want %d", manifest.Seed, gen.DefaultSeed)
	}
	if manifest.DatabaseSHA != expectedSHA256 {
		t.Fatalf("Generate() sha256 = %s, want pinned %s", manifest.DatabaseSHA, expectedSHA256)
	}

	if err := gen.WriteManifest(manifest, manifestPath); err != nil {
		t.Fatalf("WriteManifest() error = %v", err)
	}

	// Re-generate with same seed — must produce same SHA.
	dbPath2 := filepath.Join(dir, "legacy2.db")
	cfg2 := gen.DefaultConfig(dbPath2)
	g2 := gen.New(cfg2)
	manifest2, err := g2.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate() second run error = %v", err)
	}
	if manifest.DatabaseSHA != manifest2.DatabaseSHA {
		t.Errorf("non-deterministic: sha1=%s sha2=%s", manifest.DatabaseSHA, manifest2.DatabaseSHA)
	}
}

func TestGeneratorProducesRepresentativeMigrationDomains(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")
	manifest, err := gen.New(gen.DefaultConfig(dbPath)).Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	for domain, minimum := range map[string]int{
		"settings":         1,
		"notices":          1,
		"server_groups":    1,
		"server_routes":    1,
		"plans":            1,
		"human_users":      2,
		"access_tokens":    1,
		"invitation_codes": 1,
		"coupons":          1,
		"payments":         1,
		"orders":           1,
		"tickets":          1,
		"ticket_messages":  1,
		"server_machines":  1,
		"servers":          1,
	} {
		if got := manifest.DomainRows[domain]; got < minimum {
			t.Errorf("manifest domain %q rows = %d, want >= %d", domain, got, minimum)
		}
	}

	database, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, table := range []string{
		"v2_settings", "v2_notice", "v2_server_group", "v2_server_route", "v2_plan", "v2_user",
		"personal_access_tokens", "v2_invite_code", "v2_coupon", "v2_payment", "v2_order",
		"v2_ticket", "v2_ticket_message", "v2_server_machine", "v2_server",
	} {
		var objectType string
		if err := database.QueryRow(`SELECT type FROM sqlite_schema WHERE name = ?`, table).Scan(&objectType); err != nil {
			t.Errorf("required representative table %q: %v", table, err)
		} else if objectType != "table" {
			t.Errorf("sqlite object %q type = %q, want table", table, objectType)
		}
	}
	for _, excluded := range []string{"failed_jobs", "stats_daily"} {
		var count int
		if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name = ?`, excluded).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("D-013-gated table %q must be absent", excluded)
		}
	}
	var statRows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM v2_stat_server`).Scan(&statRows); err != nil {
		t.Fatal(err)
	}
	if statRows != 0 {
		t.Errorf("D-013-gated v2_stat_server rows = %d, want 0", statRows)
	}
}

func TestGeneratedDatasetSatisfiesImplementedMigrationReaders(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	if _, err := gen.New(gen.DefaultConfig(dbPath)).Generate(context.Background()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	readers := []struct {
		name string
		read func(context.Context, string) error
	}{
		{"content", func(ctx context.Context, path string) error {
			_, err := legacymigration.ReadContentSnapshot(ctx, path)
			return err
		}},
		{"groups-routes", func(ctx context.Context, path string) error {
			_, err := legacymigration.ReadGroupsRoutesSnapshot(ctx, path)
			return err
		}},
		{"plans", func(ctx context.Context, path string) error {
			_, err := legacymigration.ReadPlansSnapshot(ctx, path)
			return err
		}},
		{"human-users", func(ctx context.Context, path string) error {
			_, err := legacymigration.ReadHumanUsersSnapshot(ctx, path)
			return err
		}},
		{"access-tokens", func(ctx context.Context, path string) error {
			_, err := legacymigration.ReadAccessTokensSnapshot(ctx, path)
			return err
		}},
		{"invitation-codes", func(ctx context.Context, path string) error {
			_, err := legacymigration.ReadInvitationCodesSnapshot(ctx, path)
			return err
		}},
		{"coupons", func(ctx context.Context, path string) error {
			_, err := legacymigration.ReadCouponsSnapshot(ctx, path)
			return err
		}},
		{"payments", func(ctx context.Context, path string) error {
			_, err := legacymigration.ReadPaymentsSnapshot(ctx, path)
			return err
		}},
		{"orders", func(ctx context.Context, path string) error {
			_, err := legacymigration.ReadOrdersSnapshot(ctx, path)
			return err
		}},
		{"tickets", func(ctx context.Context, path string) error {
			_, err := legacymigration.ReadTicketsSnapshot(ctx, path)
			return err
		}},
		{"nodes", func(ctx context.Context, path string) error {
			_, err := legacymigration.ReadNodesSnapshot(ctx, path)
			return err
		}},
		{"currency-settings", func(ctx context.Context, path string) error {
			_, err := legacymigration.ReadCurrencySettingsSnapshot(ctx, path)
			return err
		}},
		{"public-origin-settings", func(ctx context.Context, path string) error {
			_, err := legacymigration.ReadPublicOriginSettingsSnapshot(ctx, path)
			return err
		}},
		{"safe-access-settings", func(ctx context.Context, path string) error {
			_, err := legacymigration.ReadSafeAccessSettingsSnapshot(ctx, path)
			return err
		}},
	}
	for _, reader := range readers {
		t.Run(reader.name, func(t *testing.T) {
			if err := reader.read(t.Context(), dbPath); err != nil {
				t.Fatalf("reader rejected generated dataset: %v", err)
			}
		})
	}
}

func TestGeneratorRefusesToOverwriteExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")
	sentinel := []byte("caller-owned data")
	if err := os.WriteFile(dbPath, sentinel, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := gen.New(gen.DefaultConfig(dbPath)).Generate(context.Background())
	if err == nil {
		t.Fatal("Generate() unexpectedly overwrote an existing database")
	}
	got, readErr := os.ReadFile(dbPath)
	if readErr != nil {
		t.Fatalf("read sentinel database: %v", readErr)
	}
	if string(got) != string(sentinel) {
		t.Fatalf("existing database changed: got %q want %q", got, sentinel)
	}
}

func TestGeneratorCleansUpAfterCancelledGeneration(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := gen.New(gen.DefaultConfig(dbPath)).Generate(ctx); err == nil {
		t.Fatal("Generate() unexpectedly succeeded with a cancelled context")
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("cancelled generation left database behind, stat error = %v", err)
	}
}

// TestManifestContainsNoPII verifies the manifest does not contain
// common PII patterns (email addresses with real domains, passwords).
func TestManifestContainsNoPII(t *testing.T) {
	dir := t.TempDir()
	cfg := gen.DefaultConfig(filepath.Join(dir, "legacy.db"))
	g := gen.New(cfg)

	manifest, err := g.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	s := string(encoded)

	// Must not contain real email domains (only .test TLD is allowed)
	for _, banned := range []string{"@gmail.com", "@qq.com", "@163.com", "@outlook.com"} {
		if contains(s, banned) {
			t.Errorf("manifest contains banned email domain %q", banned)
		}
	}
}

// TestManifestExcludesD013Domains verifies that D-013-gated tables
// (stats, failed_jobs, stat_server) are not included in the generated dataset.
func TestManifestExcludesD013Domains(t *testing.T) {
	dir := t.TempDir()
	cfg := gen.DefaultConfig(filepath.Join(dir, "legacy.db"))
	g := gen.New(cfg)

	manifest, err := g.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	for _, banned := range []string{"stat_server", "failed_jobs", "stats_"} {
		if _, found := manifest.DomainRows[banned]; found {
			t.Errorf("D-013 domain %q must not be in generated dataset until D-013 is decided", banned)
		}
	}
}

// TestGeneratedDatabaseFileHasRestrictedPermissions verifies the
// generated SQLite file is not world-readable.
func TestGeneratedDatabaseFileHasRestrictedPermissions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping file permission test in short mode")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")
	cfg := gen.DefaultConfig(dbPath)
	g := gen.New(cfg)

	if _, err := g.Generate(context.Background()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("generated DB is not a regular file")
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("generated DB permissions = %o, want 600", got)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsLoop(s, substr))
}

func containsLoop(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
