# Bug Fix Batch G Test Results (`BL-19`, `BL-22`)

- **Date:** 2026-10-16
- **Branch:** `codex/ci-001-tiered-gates`
- **Scope:** Split lifecycle rollback journal fidelity (`internal/lifecycle`) & legacy node schedule minute-alignment validation (`internal/store`)

## Red → Green Summary

| Bug ID | Test Name | Red Result | Green Result |
| --- | --- | --- | --- |
| `BL-19` | `TestBL19RollbackFailurePhasesAndRestoreRetry` | FAIL (`Status() after restore-phase rollback failure = active deployment images drift`; `state.ActiveDSN = "file:/var/lib/xboard/xboard.db", want "...rollback..."`; `unknown_failure_requires_manual_recovery` failed) | PASS (0.00s) |
| `BL-22` | `TestBL22ValidateLegacyNodesRejectsNonMinuteAlignedDailySchedule` | FAIL (`ValidateLegacyNodesData(3600, 3630) error = <nil>, want ErrInvalidInput mentioning node 41 and minute-aligned`) | PASS (0.05s) |

## Notes

- **`BL-19`**: Added optional `RollbackPhase` (`"restore"` | `"activate"` | `"unknown"`) and `ObservedDeployment` to `DeploymentState` without bumping `DeploymentStateVersion`. Legacy journals without `RollbackPhase` continue to expect `Previous` on `StatusRollbackFailed`.
- **`BL-22` (Decision D-6)**: Non-minute-aligned `enable_second` / `disable_second` values (including `86399`) are now rejected in `ValidateLegacyNodesData` with `ErrInvalidInput` mentioning the node ID.

## Commands Executed

- `go test -count=1 ./internal/lifecycle` — PASS (2.269s)
- `go test -v -count=1 -run="TestBL22|TestImportLegacyNodes" ./internal/store` — PASS (2.282s)
