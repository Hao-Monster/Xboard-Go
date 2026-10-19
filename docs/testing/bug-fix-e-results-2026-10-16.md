# Bug Fix Batch E Test Results (`BL-13`, `BL-14`, `BL-15`)

- **Date:** 2026-10-16
- **Branch:** `codex/ci-001-tiered-gates`
- **Scope:** Real-time node coordination & auth session/token isolation (`internal/httpapi`)

## Red → Green Summary

| Bug ID | Test Name | Red Result | Green Result |
| --- | --- | --- | --- |
| `BL-13` | `TestBL13SaveNodeRuntimePublishesNodeFullAcrossReplicas` | FAIL (`saveNodeRuntime(1) on non-owning replica published [Kind:"machine_nodes"], want EventNodeFull for node 1`) | PASS (0.64s) |
| `BL-14` | `TestBL14NotifyMachineNodesFailedReleaseRetriedOnDisconnect` | FAIL (`coordinator.ReleasedNodes() = [1], want removed node 2 released on disconnect retry`) | PASS (0.08s) |
| `BL-15` | `TestBL15BearerTokenDoesNotCollideWithCookieSessionID` | FAIL (`listAccountSessions(bearer) marked cookie session 1 as IsCurrent=true when authenticated via Bearer access token`) | PASS (0.08s) |

## Commands Executed

- `go test -v -count=1 '-run=TestBL(13|14|15)' ./internal/httpapi` — PASS (6.769s)
- `go test -count=1 '-run=Test(WebSocket|NodeRuntime|Auth|Session|AccessToken)' ./internal/httpapi` — PASS
