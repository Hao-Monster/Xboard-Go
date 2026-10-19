# BUG-20261010-03 / 06 worker repair evidence

## Frozen plan before implementation

- Baseline: `2acd15f9f917b9c75ed39c0ac644cd9e2427fec1`.
- Scope: explicit HTTP shutdown completion (03), terminal recovery of abandoned final ticket/reminder mail attempts (06).
- Governance: 03 maps to OPS-002 / issue 117 / M2. 06 maps to TICKET-001, VER-004 / issue 122 / M3; OPS-001, VER-005 / closed issue 123 is historical only.
- Ownership: `cmd/xboard/**`, `internal/store/ticket_settings.go`, `internal/store/mail_settings.go`, independent regression tests, this report. No HTTP API production changes, push, PR, deployment, or real mail service.
- Main plan read before changes: `docs/testing/bug-fix-plan-2026-10-10.md` in the integration workspace.
- Environment: Windows amd64; Go 1.26.8 cached locally, `GOTOOLCHAIN=auto`, `GOPROXY=off`.

### 03 design

Extract the existing HTTP lifecycle coordination without changing behavior so that a real local HTTP regression can first fail. Then explicitly join `Server.Shutdown` before the coordinator returns or waits for special WebSocket/telemetry shutdown. Preserve the existing production ten-second grace period, error logging, special waiter, and listener-error behavior. Listener failure must also stop its coordination goroutine. No general background-worker lifecycle refactor.

### 06 design

Within each existing ticket/reminder claim transaction, finalize at most 100 expired final claims: attempts exhausted, unsent, not failed/cancelled, non-empty claim, lease expired. Clear the claim and record failure with an unknown-delivery-result explanation; never send a fourth time. A claim operation with no ordinary candidate must still commit recovery. Keep attempts one/two retryable and valid leases/terminal rows untouched. Recovery and subsequent claim remain atomic.

## Frozen executable cases

| Bug | Case | Layer / file | Expected result |
| --- | --- | --- | --- |
| 03 | Active slow ordinary handler; cancellation; special waiter ready | Real loopback HTTP / `cmd/xboard/http_shutdown_test.go` | Listener closes, but coordinator and dependency cleanup cannot finish until handler response completes |
| 03 | Empty server, startup cancellation, already-cancelled context | Real loopback HTTP | Bounded successful shutdown, no stranded listener or waiter |
| 03 | Handler exceeds grace period | Real loopback HTTP | Deadline error is visible, not reported as successful drain; test releases handler explicitly |
| 03 | Listener failure | Controlled listener boundary | Original error returned, waiter not called, coordination goroutine does not survive return |
| 03 | Special waiter timeout | Controlled waiter | Its deadline failure remains visible after HTTP shutdown |
| 06 | Third claim abandoned, before/exact lease expiry | Real temporary SQLite / `internal/store/mail_final_claim_recovery_test.go` | Before expiry unchanged; at expiry failed/unknown, attempts stay three, no fourth claim |
| 06 | No next ordinary candidate | Real SQLite | Recovery commits; queue counts and failed-mail listing reflect it |
| 06 | Late completion/failure and repeated recovery | Real SQLite | Old token rejected; terminal timestamps/counts unchanged |
| 06 | Attempts one/two expired | Real SQLite | Normal retries preserved |
| 06 | Sent/failed/cancelled/active final rows | Real SQLite | Existing terminal rows and live leases untouched |
| 06 | Concurrent workers plus ordinary pending work | Real SQLite | Recovery atomic; ordinary job claimed once |
| 06 | Reopen persisted database | Real SQLite | Recovery independent of process memory |
| 06 | Injected database failure in recovery or following claim | Real SQLite trigger | Error returned and whole transaction rolls back; later clean attempt succeeds |
| 06 | More than 100 abandoned claims | Real SQLite | Per-call recovery bounded; repeated calls drain backlog without fourth attempts |

## Planned commands

All Go commands use the environment above. Target regressions run before and after the fix. Compilation/environment failures do not count as Red.

```text
go test ./cmd/xboard -run '^TestHTTPShutdown' -count=1
go test ./internal/store -run '^TestMailFinalClaimRecovery' -count=1
go test ./cmd/xboard
go test ./internal/store -run 'Test(TicketMail|AdministratorTicketReplies|SubscriptionReminder|SystemQueue|MailFinalClaimRecovery)' -count=1
go test ./internal/mailer ./internal/bulkops
go vet ./cmd/xboard ./internal/store ./internal/mailer ./internal/bulkops
git diff --check
```

Full shared-module and repository validation is coordinated by the root agent after integration. Race availability will be checked; unsupported tooling is NOT RUN, not PASS. Coverage and independent review results will be recorded separately. Live SMTP, production restart, deployment and recovery exercises are NOT RUN / not authorized.

## Execution results

### Red to Green

The HTTP Red run followed a behavior-preserving extraction of the original lifecycle code, before adding the missing join. It failed for three product assertions: the coordinator returned while an ordinary handler was active; incomplete HTTP drain was not reported before return; and listener failure left the shutdown coordination goroutine alive. This was not a compilation or environment failure.

The mail Red run failed on persisted queue state: exhausted third claims remained pending/claimed with no terminal timestamp, recovery-side database-failure injection did not execute, and the bounded backlog did not converge. Earlier-lease retries and pre-existing terminal-row preservation already passed.

Counts below distinguish top-level tests from subtests rather than adding parent nodes into a misleading assertion total. All test runs used Windows amd64 / Go 1.26.8 with the offline environment above. Times are observed package times; wall times include compilation and concurrent local test workloads.

| Command / phase | Status / exit | Top-level pass / fail | Subtest pass / fail | Skipped | Package time; wall time |
| --- | --- | --- | --- | --- | --- |
| `go test ./cmd/xboard -run '^TestHTTPShutdown' -count=1 -json` before fix | FAIL / 1 | 2 / 3 | 2 / 0 | 0 | 6.704s; not separately recorded |
| `go test ./internal/store -run '^TestMailFinalClaimRecovery' -count=1 -json` before fix | FAIL / 1 | 2 / 5 | 7 / 12 | 0 | 8.917s; not separately recorded |
| HTTP command above after review refinements | PASS / 0 | 7 / 0 | 2 / 0 | 0 | 6.955s; 47.349s |
| Mail command above after fix | PASS / 0 | 7 / 0 | 19 / 0 | 0 | 7.432s; 20.337s |
| `go test ./cmd/xboard ./internal/mailer ./internal/bulkops -count=1 -json '-coverprofile=$coveragePath'` | PASS / 0 | 97 / 0 | 29 / 0 | 1 existing | cmd 128.877s, mailer 9.624s, bulkops 12.020s; 170.226s |
| `go test ./internal/store -run 'Test(TicketMail\|AdministratorTicketReplies\|SubscriptionReminder\|SystemQueue\|MailFinalClaimRecovery)' -count=1 -json '-coverprofile=$coveragePath'` | PASS / 0 | 15 / 0 | 19 / 0 | 0 | 21.357s; 90.036s |
| `go test -race ./cmd/xboard ./internal/store -run 'TestHTTPShutdown\|TestMailFinalClaimRecovery' -count=1 -json` | PASS / 0 | 14 / 0 | 21 / 0 | 0 | cmd 8.288s, store 63.507s; 159.477s |
| `go test ./cmd/xboard ./internal/store -run 'TestHTTPShutdown\|TestMailFinalClaimRecovery' -count=1 -json '-coverprofile=C:\Temp\xboard-worker-target-20261010.cover'` | PASS / 0 | 14 / 0 | 21 / 0 | 0 | cmd 0.879s, store 6.320s; 20.730s |
| `go vet ./cmd/xboard ./internal/store ./internal/mailer ./internal/bulkops` | PASS / 0 | N/A | N/A | N/A | 29.353s wall |
| `git diff --check` | PASS / 0 | N/A | N/A | N/A | Less than 2s |

The two expanded suite commands initially wrote a literal `$coveragePath` file because the native PowerShell argument was not quoted for interpolation; their test results remain valid, but those overlapping profiles were not used as coverage evidence. That generated artifact was moved outside the repository, and coverage was regenerated with the explicit quoted path shown above.

The sole skipped module test is the existing `TestRunCommandImportsCapturedRealTrustedPluginsSnapshot`: `XBOARD_TEST_LEGACY_TRUSTED_PLUGINS_SNAPSHOT` is unset, and this local task does not acquire real legacy production data. A targeted rerun confirmed this skip and confirmed `TestServiceRejectsASymbolicLinkExportRoot` passes on this Windows host. No new tests were skipped and no existing skip/only rules were changed.

### Independent review refinements

The root reviewer identified that merely logging shutdown/drain errors while returning `http.ErrServerClosed` would still make main treat a timeout as success. The final helper normalizes only the ordinary server-closed result, returns `errors.Join(serveErr, shutdownErr, drainErr)`, and main treats every non-nil result as an unsuccessful exit.

The reviewer also required cleanup when `Serve` fails unexpectedly while handlers are still active. The helper now always performs bounded `Shutdown`, whether cancellation or listener completion wins the select. Two added review cases pass:

- A real loopback listener accepts a request and then returns a permanent Accept error; the active handler still produces its entire response before the coordinator returns the original listener error.
- Simultaneous startup cancellation and distinct serve/shutdown/special-drain failures preserve all three errors through `errors.Is`.

The final production helper was re-reviewed by the root agent with no remaining blocking finding. For 06, the review confirmed the transaction commits even without another job, recovery is capped at 100 rows, existing due-index order is reused, and both independent Store connections and injected database-write failures are covered. All frozen cases and the two added HTTP review cases are PASS.

### Coverage and remaining proof gaps

`go tool cover -func C:\Temp\xboard-worker-target-20261010.cover` exited 0 and reports statement coverage for the focused regression run:

| Function | Statement coverage |
| --- | --- |
| `runHTTPServer` | 100.0% |
| `ClaimTicketMail` | 77.4% |
| `ClaimSubscriptionReminder` | 78.1% |

Combined package statement coverage is 4.6%, because only the focused regressions ran against the large cmd/store packages; this is not a whole-project coverage result. Standard Go coverage here does not report line or branch coverage, so neither is claimed. The newly added recovery update, its write-error return, and successful no-candidate commit are covered. The defensive commit-error returns are not directly fault-injected; recovery-write and subsequent-claim failures do verify complete transaction rollback. Existing uncovered claim branches include invalid input, transaction/query/commit infrastructure failures and impossible-under-lock row-count races. No coverage gate was changed.

NOT RUN: full-process OS signal/restart acceptance, actual SMTP delivery/restart reconciliation, remote deployment, production recovery exercises and whole-repository checks in this detached worktree. Real loopback HTTP and real isolated SQLite are local evidence only. The root agent owns full integration checks after cherry-picking. An unknown third delivery is deliberately recorded as failed/unknown rather than retried; this does not claim whether a remote SMTP server accepted it.

### Scope and handoff

Only the owned HTTP lifecycle, two claim methods, their independent regression files and this report changed. The ten-second production grace periods are unchanged. No schema, mail transport, background-worker lifecycle, production data, remote ref, PR or deployment was changed. The local commit is handed to the root agent for review and integration; exact commit identity is recorded in the parent handoff rather than self-referenced inside its own content.
