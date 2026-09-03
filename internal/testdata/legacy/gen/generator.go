// Package gen provides a deterministic, anonymized legacy dataset generator
// for MIG-001 migration verification. It does NOT connect to production and
// does NOT contain or process any real user data.
//
// All generated data uses a fixed seed (DefaultSeed) so that the same code
// version always produces the same SQLite file with the same SHA-256 hash,
// enabling reproducible migration reconciliation.
package gen

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DefaultSeed is the fixed seed used by all dataset generators.
// Changing this constant produces a different dataset with a different SHA-256.
const DefaultSeed uint64 = 20260903

// DatasetManifest records metadata for a generated legacy dataset.
// It is written to manifest.json alongside the generated SQLite file.
type DatasetManifest struct {
	Version     string         `json:"version"`
	GeneratedAt time.Time      `json:"generated_at"`
	Seed        uint64         `json:"seed"`
	DatabaseSHA string         `json:"database_sha256"`
	DomainRows  map[string]int `json:"domain_rows"`
	Notes       []string       `json:"notes"`
}

// Config holds generation parameters.
type Config struct {
	// Seed for deterministic data generation.
	Seed uint64
	// OutputPath is the file path for the generated legacy.db.
	OutputPath string
	// Now is the reference timestamp for all generated timestamps.
	Now time.Time
}

// DefaultConfig returns a Config with DefaultSeed and current time.
func DefaultConfig(outputPath string) Config {
	return Config{
		Seed:       DefaultSeed,
		OutputPath: outputPath,
		Now:        time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC),
	}
}

// Generator builds a representative, anonymized legacy Xboard dataset.
type Generator struct {
	cfg  Config
	rng  *rand.Rand
	rows map[string]int
}

// New creates a new Generator with the given config.
func New(cfg Config) *Generator {
	src := rand.NewPCG(cfg.Seed, cfg.Seed^0xdeadbeef)
	return &Generator{
		cfg:  cfg,
		rng:  rand.New(src),
		rows: make(map[string]int),
	}
}

// Generate creates the legacy SQLite database and returns the manifest.
//
// SAFETY: This function only writes to cfg.OutputPath. It does not read from
// any external system, does not connect to production, and does not process
// real user data.
func (g *Generator) Generate(ctx context.Context) (DatasetManifest, error) {
	if err := reserveFreshOutput(g.cfg.OutputPath); err != nil {
		return DatasetManifest{}, err
	}
	// Open fresh SQLite — legacy schema (pre-migration target)
	db, err := sql.Open("sqlite", "file:"+g.cfg.OutputPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		removeGeneratedDatabase(g.cfg.OutputPath)
		return DatasetManifest{}, fmt.Errorf("open legacy db: %w", err)
	}
	defer db.Close()
	completed := false
	defer func() {
		if !completed {
			removeGeneratedDatabase(g.cfg.OutputPath)
		}
	}()

	if err := g.buildSchema(ctx, db); err != nil {
		return DatasetManifest{}, fmt.Errorf("build legacy schema: %w", err)
	}
	if err := g.populateDomains(ctx, db); err != nil {
		return DatasetManifest{}, fmt.Errorf("populate domains: %w", err)
	}
	if err := db.Close(); err != nil {
		return DatasetManifest{}, fmt.Errorf("close legacy db: %w", err)
	}
	if err := os.Chmod(g.cfg.OutputPath, 0o600); err != nil {
		return DatasetManifest{}, fmt.Errorf("restrict legacy db permissions: %w", err)
	}

	sha, err := fileSHA256(g.cfg.OutputPath)
	if err != nil {
		return DatasetManifest{}, fmt.Errorf("hash legacy db: %w", err)
	}

	manifest := DatasetManifest{
		Version:     "dataset_v1",
		GeneratedAt: g.cfg.Now,
		Seed:        g.cfg.Seed,
		DatabaseSHA: sha,
		DomainRows:  g.rows,
		Notes: []string{
			"All data is synthetically generated. No real users, emails, passwords, or tokens.",
			"D-013 domains (stats, failed_jobs, stat_server) are excluded pending decision.",
		},
	}
	completed = true
	return manifest, nil
}

func reserveFreshOutput(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("open legacy db: output path is required")
	}
	reserved, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open legacy db: reserve output path: %w", err)
	}
	if err := reserved.Close(); err != nil {
		removeGeneratedDatabase(path)
		return fmt.Errorf("open legacy db: close reserved output: %w", err)
	}
	return nil
}

func removeGeneratedDatabase(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
	_ = os.Remove(filepath.Clean(path) + "-journal")
}

// WriteManifest writes the manifest to manifestPath as pretty-printed JSON.
func WriteManifest(manifest DatasetManifest, manifestPath string) error {
	f, err := os.OpenFile(manifestPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create manifest: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// buildSchema creates the legacy Xboard table structure.
// This mirrors the pre-migration source schema (not the current Go schema).
//
// NOTE: The exact schema version and table set to simulate depends on D-006
// (target production database type). Until D-006 is resolved, we use SQLite
// with a representative pre-migration schema (user_version = 22, the last
// version before Go migration baseline).
func (g *Generator) buildSchema(ctx context.Context, db *sql.DB) error {
	// Minimal legacy schema placeholder — full schema TBD after D-006 decision.
	// Each domain's buildSchema* function will be added in domains/*.go files.
	_, err := db.ExecContext(ctx, `PRAGMA user_version = 22`)
	return err
}

// populateDomains writes representative rows for each covered domain.
// D-013-gated domains (stats, failed_jobs) are excluded.
func (g *Generator) populateDomains(_ context.Context, _ *sql.DB) error {
	// Placeholder: actual domain generators will be implemented in domains/*.go
	// after the schema is finalized (D-006) and this design is approved.
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
