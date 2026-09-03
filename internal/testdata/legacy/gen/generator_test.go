package gen_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/testdata/legacy/gen"
)

// TestGeneratorProducesDeterministicDataset verifies that the same seed
// always produces the same manifest (same domain row counts).
//
// The database SHA-256 is NOT yet pinned here because the schema is a
// placeholder pending D-006 decision. Once the schema is finalized, a
// TestPinDatasetSHA256 test should be added that fails if the SHA changes.
func TestGeneratorProducesDeterministicDataset(t *testing.T) {
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
