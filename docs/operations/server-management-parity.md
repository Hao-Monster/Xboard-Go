# Server management parity implementation

Requirement IDs: MACH-001, MACH-002, MACH-003, MACH-004
Work item IDs: VER-003, CI-001
Milestone: M3
Related issues: #121, #127

## Scope

Match the legacy server management table, overview metrics, search, filters, sorting,
pagination, create/edit forms and row actions while retaining the current theme.
The detail drawer exposes load history ranges and metrics, linked node management,
and navigation to filtered node management or preselected node creation.
Existing enrollment, node revision checking, activation schedules and failure handling remain functional.

## Explicit remaining compatibility boundary

The legacy page can read back a long-lived server Token. This application stores
credential hashes and issues one-time enrollment codes. No plaintext credential
storage or fake Token viewing control is introduced. A separate user decision is
required before changing this authentication contract. This delivery therefore does
not claim complete 100% parity or business acceptance.

## Development release

The only development branch remains `codex/ci-001-tiered-gates`; deliver through one PR.
A main push triggers the lightweight CI dispatch, then the existing authorized
`workflow_run` runner builds and deploys the exact source SHA to bingo-dev.
Automated regression and CodeQL are not deployment prerequisites. Container health,
release SHA and public reachability are checked to detect an unsuccessful deployment.
The previous image/configuration are retained for rollback. User performs business acceptance.
Original gates are preserved under `production-gates-backup/`; Production checks and
CodeQL are manual only, not an automatic production release policy.

## Local validation

Targeted component tests, machine lifecycle browser tests on desktop and mobile,
TypeScript, lint, frontend build and the existing deployment-source authorization tests.
No full repository regression, capacity test or business acceptance is claimed.
