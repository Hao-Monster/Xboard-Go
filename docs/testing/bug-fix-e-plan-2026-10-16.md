# Bug Fix Batch E Test Plan (`BL-13`, `BL-14`, `BL-15`)

- **Date**: 2026-10-16
- **Branch**: `codex/ci-001-tiered-gates`
- **Spec Reference**: `docs- box/FIX_ENGINEERING_SPEC_2026-10-14.md` §Batch E

## Scope

1. **`BL-13` (`internal/httpapi/runtime_handlers.go`)**:
   - Always call `s.hub.NotifyNodeFull(r.Context(), machineID, nodeID)` in `saveNodeRuntime` regardless of whether the local replica's in-memory `s.hub.hasNode(nodeID)` is true, so `nodecoord.EventNodeFull` is broadcast to peer replicas owning the node's WebSocket connection.
2. **`BL-14` (`internal/httpapi/websocket.go`)**:
   - Track removed nodes whose `ReleaseNodeIfOwned` failed during `notifyMachineNodes` (`connection.addPendingRelease`), clear devices for any nodes that were already released before the failure, and retry releasing pending removed nodes + clearing their devices during `unregisterAndClear`.
3. **`BL-15` (`internal/httpapi/auth_handlers.go`)**:
   - Introduce `currentCookieSessionID(session)` so `listAccountSessions` and `revokeAccountSession` only treat `session.SessionID` as a cookie session ID when `session.CredentialKind == store.CredentialKindCookieSession`.

## Planned Verification Commands

- `go test -v -count=1 '-run=TestBL(13|14|15)' ./internal/httpapi`
- `go vet ./internal/httpapi`
- `go test -count=1 '-run=Test(R014|DIFFNODE004|RoutingRule|WebSocket|Auth|Session|AccessToken)' ./internal/httpapi`
