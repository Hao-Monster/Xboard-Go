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
	rows map[string]int
}

// New creates a new Generator with the given config.
func New(cfg Config) *Generator {
	return &Generator{
		cfg:  cfg,
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
			"D-013 data (stats, failed_jobs, stat_server rows) is excluded pending decision; the required v2_stat_server schema is empty.",
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
