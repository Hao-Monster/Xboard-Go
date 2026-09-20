# Node management parity delivery

Requirement IDs: NODE-001, NODE-002, NODE-003, NODE-004, NODE-005, NODE-006, NODE-007
Work item IDs: VER-003
Milestone: M3 Release Candidate
Related issue: #121 (broader acceptance remains open)

## Scope

Reproduce the observed original node directory, new-node modal and edit drawer while retaining the application theme. The directory supports debounced search, multi-select protocol/server/group filters, pagination, visibility, row and bulk actions, copy, traffic reset and saved/cancelled ordering. Filtering and ordering occur before database pagination.

The definition form supports the existing eleven protocols, editable protocol selection, parent lookup, tags, inline permission-group creation, server binding, traffic quota, rates, transport configuration and nested TLS/Mux/outbound/routing settings. Cancel discards nested drafts. Submissions contain only writable API fields. ECH material is generated through an authenticated no-store endpoint and is never persisted by the generator.

Duplicate legacy/default sort values are normalized transactionally when reordering requires it, preserving the positions of unselected nodes in the stable global order. This can update their sort values and revisions; stale concurrent changes still fail rather than overwrite.

## Verification

Targeted checks for this change:

- `go test ./internal/store ./internal/httpapi -run 'AdminNode|NodeDefinition|NodeECH' -count=1`
- `pnpm --dir web exec eslint src/features/nodes src/components/Overlay.tsx src/lib/api.ts src/features/servers/ServerManagementPage.test.tsx --max-warnings 0`
- `pnpm --dir web test src/features/nodes/NodeManagementPage.test.tsx src/features/nodes/NodeGroupCreator.test.tsx`
- `pnpm --dir web build` (includes TypeScript and entry budget)
- `pnpm --dir web exec playwright test e2e/nodes.spec.ts`

Browser checks use isolated local in-memory SQLite and fictional credentials/data: CRUD/bulk/sorting, remote-parent selection, and create/persist/edit/reopen for every protocol on desktop and mobile. They do not change the original panel. The PR records actual command outcomes and the deployed commit.

## Boundaries and release

No schema migration, dependency upgrade or deployment-workflow change is required. Business acceptance, live node handshakes and production-capacity acceptance are not claimed by these local checks. No full regression/race/CodeQL gate is added to development deployment.

Use the existing main CI -> workflow_run development pipeline only. Confirm the completed deployment and exact image commit plus health endpoint. Roll back through the existing pipeline using a revert PR; do not manually transfer artifacts or restart services from the local workstation. Keep #121 open for its remaining acceptance scope.

## Layout collision regression (2026-09-20)

Node directory rows and table containers use dedicated classes, avoiding global `.node-row` flex-card and `.resource-table` mobile-card rules. Narrow screens scroll the table horizontally; header and body retain native column alignment. Long addresses and permission badges stay readable. Bulk operations sit beside filters, while ordering remains at the toolbar end.

The directory browser scenario asserts native table-row display, matching header/cell positions, and full row width at 1280px and 3414px before continuing desktop/mobile interactions. This assertion failed against the previous release and passes with the isolated styles.
