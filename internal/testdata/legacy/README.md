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

# Run the representative migration and rollback drill
go test ./internal/testdata/legacy/drill -count=1 -v
```

## D-013 Exclusion

Until D-013 (statistics/log migration window) is decided by the integrator,
the following legacy tables are **excluded** from the generated dataset:
- `stat_server`
- `failed_jobs`
- Any `stats_*` tables

## Determinism

The generator uses seed `20260903`. The same seed always produces the same
database SHA-256. If you deliberately change the seed or schema, update the
pinned SHA-256 in `gen/generator_test.go` after reviewing the resulting
migration evidence.

## Directory Structure

```
gen/
  generator.go       # Dataset lifecycle and manifest writer
  schema.go          # Representative pre-migration SQLite schema
  domains.go         # Deterministic synthetic domain rows
  generator_test.go  # Determinism, reader compatibility, and PII tests
drill/
  drill_integration_test.go  # Full slice, reconciliation, replay, and restore drill
dataset_v1/          # Generated output (git-ignored, .gitignore excludes *.db)
  legacy.db          # Generated legacy SQLite (git-ignored)
  manifest.json      # Row counts and SHA-256 (committed after review)
```

The generated fixture currently exercises the migration readers and CLI slices
that are implemented on the current branch, including one synthetic knowledge
article and gift-card templates, codes, and usage history. The knowledge
attachment tables are present but empty; no private attachment file tree is
generated yet. It deliberately excludes statistics, logs, and failed jobs
while D-013 remains undecided. The fixture uses SQLite as a reproducible test
candidate; production database compatibility and V1 token retirement remain
subject to D-006 and D-009.
