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

## Edit and detail dialogs

Edit uses the same compact form as creation, with existing values, a switch,
blank-name protection and preserved input after failed saves. The detail drawer
contains the status summary, four history ranges, five independently toggleable
load series, resource bars, Token controls, inline installation command and linked
node actions. Unbound nodes can be selected and linked in a separate dialog.
Details refresh every 30 seconds; API and clipboard failures are visible.

New machine credentials retain a hash for authentication and an authenticated
encrypted copy for administrator-only readback. Schema 64 adds nullable
`server_machine_credentials.token_cipher`; the existing settings encryption key
is reused with a separate machine-token encryption purpose. The key must remain
available independently of database backups. Secrets are returned with no-store
and are not included in normal machine responses.

Existing hash-only credentials cannot be recovered. Viewing them reports that
limitation and does not rotate or invalidate them. An explicit confirmed reset
revokes previous credentials and pending enrollments, disconnects the agent, and
requires the operator to update its credential or re-enroll. Newly exchanged
credentials are encrypted as well. Installation commands continue to use expiring,
single-use enrollment codes; simply opening details does not revoke active tokens.
Token and enrollment credentials remain separate. Business acceptance belongs to
the user; this document does not claim visual pixel-diff or production acceptance.

## Development release

The only development branch remains `codex/ci-001-tiered-gates`; deliver through one PR.
A main push triggers the lightweight CI dispatch, then the existing authorized
`workflow_run` runner builds and deploys the exact source SHA to bingo-dev.
Automated regression and CodeQL are not deployment prerequisites. Container health,
release SHA and public reachability are checked to detect an unsuccessful deployment.
The previous image/configuration and a database snapshot are retained for explicit
recovery. Once candidate activation has been attempted, any deployment check
failure stops the candidate and preserves the current database/WAL and configuration;
it does not restore the old snapshot or restart the old image automatically.
The failure marker prevents another automatic deployment before recovery is resolved.
An older binary may reject the upgraded schema (including the schema 64 transition),
so neither an image-only fallback nor a snapshot restore is an automatic safe option.
Explicit snapshot recovery can discard accepted post-snapshot writes and requires
authorization plus a data-reconciliation decision; see
[lifecycle failure recovery](lifecycle-recovery.md). User performs business acceptance.
Original gates are preserved under `production-gates-backup/`; Production checks and
CodeQL are manual only, not an automatic production release policy.

## Local validation

Targeted component tests, machine lifecycle browser tests on desktop and mobile,
TypeScript, lint, frontend build and the existing deployment-source authorization tests.
No full repository regression, capacity test or business acceptance is claimed.
