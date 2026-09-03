# Legacy Test Data

This directory contains the deterministic, anonymized legacy dataset generator
for MIG-001 migration verification.

## Safety Notice

**NO REAL DATA.** All data in this directory and its subdirectories is
synthetically generated using a fixed random seed. It does not contain
and must never contain:
- Real user email addresses, passwords, or tokens
- Production database files or dumps
- Any personally identifiable information (PII)

## Usage

```bash
# Generate the legacy test dataset
go run ./cmd/testdatagen --output ./internal/testdata/legacy/dataset_v1/

# Run the generator tests
go test ./internal/testdata/legacy/gen/...
```

## D-013 Exclusion

Until D-013 (statistics/log migration window) is decided by the integrator,
the following legacy tables are **excluded** from the generated dataset:
- `stat_server`
- `failed_jobs`
- Any `stats_*` tables

## Determinism

The generator uses seed `20260903`. The same seed always produces the same
database SHA-256. If you change the seed or schema, update `dataset_sha256.txt`.

## Directory Structure

```
gen/
  generator.go       # Dataset builder (no real data)
  generator_test.go  # Determinism and PII absence tests
  seed.go            # (reserved) Seed constants
  domains/           # Per-domain row builders (to be implemented)
dataset_v1/          # Generated output (git-ignored, .gitignore excludes *.db)
  legacy.db          # Generated legacy SQLite (git-ignored)
  manifest.json      # Row counts and SHA-256 (committed after review)
  dataset_sha256.txt # Pinned SHA-256 for CI verification
```
