# Bug Fix Batch G Test Plan (`BL-19`, `BL-22`)

**Date:** 2026-10-16  
**Branch:** `codex/ci-001-tiered-gates`  
**Scope:** Split lifecycle rollback journal fidelity (`internal/lifecycle`) & legacy node schedule minute-alignment validation (`internal/store`)

## Target Bug Items

| Bug ID | Summary | Target Files | Red Test Name |
| --- | --- | --- | --- |
| `BL-19` | `split_lifecycle.go` rollback failure overwrites `ActiveDSN` / expected deployment without distinguishing `restore` vs `activate` vs `unknown` phases, causing false drift or blocking retry | `internal/lifecycle/split_lifecycle.go` | `TestBL19RollbackFailurePhasesAndRestoreRetry` |
| `BL-22` | `ValidateLegacyNodesData` accepts non-minute-aligned `enable_second`/`disable_second` in daily schedules | `internal/store/legacy_nodes_migration.go` | `TestBL22ValidateLegacyNodesRejectsNonMinuteAlignedDailySchedule` |

## Verification Commands

- `go test -v -count=1 -run=TestBL19 ./internal/lifecycle`
- `go test -v -count=1 -run=TestBL22 ./internal/store`
- `go test -count=1 ./internal/lifecycle`
