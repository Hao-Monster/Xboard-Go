# Bug Fix Batch F Test Plan (`BL-11`, `BL-12`)

- **Date:** 2026-10-16
- **Branch:** `codex/ci-001-tiered-gates`
- **Scope:** Subscription delivery & Shadowrocket URI rendering (`internal/subscription`, `internal/httpapi`)

## Target Bug Items

| Bug ID | Summary | Target Files | Red Test Name |
| --- | --- | --- | --- |
| `BL-11` | `serveClientSubscription` evaluates `PresentNodes` before `CompatibleNodes(filtered, client)`, suppressing empty-subscription synthetic info node when all nodes are incompatible with the client format | `internal/subscription/render.go`, `internal/httpapi/subscription_handlers.go` | `TestBL11IncompatibleClientReceivesEmptySubscriptionBanner` |
| `BL-12` | `shadowrocketLine` always emits `hysteria2://` URI for all `hysteria` nodes even when `version == 1` | `internal/subscription/uri_outputs.go` | `TestBL12ShadowrocketHysteriaV1AndV2URIs` |

## Verification Commands

- Red / Green targeted tests:
  - `go test -v -count=1 -run=TestBL12 ./internal/subscription`
  - `go test -v -count=1 -run=TestBL11 ./internal/httpapi`
- Package regression:
  - `go test -count=1 ./internal/subscription`
