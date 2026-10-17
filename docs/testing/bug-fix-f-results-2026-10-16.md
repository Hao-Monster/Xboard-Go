# Bug Fix Batch F Test Results (`BL-11`, `BL-12`)

- **Date:** 2026-10-16
- **Branch:** `codex/ci-001-tiered-gates`
- **Scope:** Subscription delivery & Shadowrocket URI rendering (`internal/subscription`, `internal/httpapi`)

## Red → Green Summary

| Bug ID | Test Name | Red Result | Green Result |
| --- | --- | --- | --- |
| `BL-11` | `TestBL11IncompatibleClientReceivesEmptySubscriptionBanner` | FAIL (`Shadowrocket/1992 subscription missing synthetic info nodes when first node is Hysteria v2`) | PASS (0.65s) |
| `BL-12` | `TestBL12ShadowrocketHysteriaV1AndV2URIs` | FAIL (`shadowrocketLine(v1Full) = "hysteria2://...", want "hysteria://..."`) | PASS (0.00s) |

## Notes

- **Decision D-4 (`BL-11`)**: Legacy PHP cloned `servers[0]` before client compatibility filtering and lost synthetic info banners (`剩余流量`, `套餐到期`) when `servers[0]` was incompatible with the client. Go filters `CompatibleNodes(filtered, client)` before `PresentNodes` while keeping `RejectedByRequestFilter` strictly equal to `len(prepared) - len(filtered_by_request)`.
- **`BL-12` version normalization**: When `version` is `1`, `shadowrocketLine` emits `hysteria://` matching legacy PHP `Shadowrocket.php:355-378` parameter ordering (`auth`, `upmbps`, `downmbps`, `protocol=udp`, `fastopen=1`, `peer`, `obfs=xplus`, `obfsParam`, `insecure`, `mport`). When `version` is `2`, the `hysteria2://` output is unchanged byte-for-byte. When `version` is neither `1` nor `2`, `shadowrocketLine` returns `""` matching PHP.

## Commands Executed

- `go test -count=1 ./internal/subscription` — PASS (5.822s)
- `go test -v -count=1 -run=TestBL11 ./internal/httpapi` — PASS (6.564s)
