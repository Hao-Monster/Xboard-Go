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
go run ./cmd/testdatagen --output ./.local/legacy-dataset-v2/

# Run the generator tests
go test ./internal/testdata/legacy/gen/...

# Exercise all 17 CLI migration steps, reconciliation and backup restoration
go test ./internal/testdata/legacy/drill -count=1 -v
```

## D-013 Metadata and Statistics

The confirmed policy retains only the last 90 days of allowlisted log metadata
and rebuilds statistics from business facts. The fixture includes commissions,
an internal distributor subscriber, node traffic, log retention boundaries and
a synthetic failed-job sentinel that must never be imported or executed.
The drill checks the original SQL counts, both retention boundaries, replay
with the original `as-of` time, prerequisite order and restored database state.

## Determinism

The generator uses seed `20260903` and a fixed clock. The current `dataset_v2`
SHA-256 is pinned in `gen/generator_test.go`; update it only after reviewing an
intentional fixture change. Same-code repeat generation must be identical.

## Directory Structure

```
gen/
  generator.go       # Dataset builder (no real data)
  generator_test.go  # Determinism and PII absence tests
  domains.go         # Fixed domain row builders
  schema.go          # Legacy table shapes
drill/
  drill_integration_test.go # Real CLI migration, reconciliation and rollback
```
