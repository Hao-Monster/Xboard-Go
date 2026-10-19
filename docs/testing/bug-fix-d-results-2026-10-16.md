# Bug Fix Batch D Test Results (`BL-16`, `BL-17`, `BL-18`, `BL-20`, `BL-21`)

- **Date**: 2026-10-16
- **Branch**: `codex/ci-001-tiered-gates`
- **Plan Reference**: `docs/testing/bug-fix-d-plan-2026-10-16.md`

## Red → Green Evidence

### 1. Red Run (Before Fix)

Command:
`go test -v -count=1 '-run=TestBL(16|17|18|20|21)' ./internal/store ./internal/bulkops ./internal/mailer ./internal/telegrambot`

Observed Failures (Expected Red):
- `TestBL17CommissionPoisonOrderIsolation/cyclic_inviter_quarantined_healthy_orders_paid`: `ProcessCommissions() unexpected error = conflict: cyclic invitation relationship, want poison order isolated`
- `TestBL17CommissionPoisonOrderIsolation/balance_limit_exceeded_quarantined_and_l1_rolled_back`: `ProcessCommissions() error = credit commission recipient: balance limit exceeded`
- `TestBL18StaleOrderRefundOverflowIsolation`: `ProcessStaleOrders() unexpected error = refund cancelled order balance: constraint failed: CHECK constraint failed: balance BETWEEN 0 AND 9000000000000000 (275), want poison order quarantined`
- `TestBL20TelegramExhaustedThirdClaimRecovery`: `exhausted row 1 = failedAt=sql.NullInt64{Int64:0, Valid:false} token=sql.NullString{String:"abandoned-token-3", Valid:true} ... want failed with delivery_result_unknown`
- `TestBL16BulkMailCancelAfterClaimAndStaleReap/cancelled_between_claim_and_active_check_releases_claim_and_terminates_job`: `target.Status = "processing", want "cancelled"`
- `TestBL16BulkMailCancelAfterClaimAndStaleReap/abandoned_processing_claim_on_cancelling_job_is_reaped_after_lease`: `reaped target = Status:"processing", want status=failed last_error='mail delivery result unknown after cancellation'`
- `TestBL21MailerBatchUsesAdvancingClockPerMessage`: `claimedAt count = 2 ([1792166400 1792166400]), want 3`
- `TestBL21TelegramBatchUsesAdvancingClockPerMessage`: `claimedAt count = 2 ([1792170000 1792170000]), want 3`

### 2. Green Run (After Fix)

| Command | Result | Duration |
| --- | --- | --- |
| `go test -v -count=1 '-run=TestBL(16|17|18|20|21)' ./internal/store ./internal/bulkops ./internal/mailer ./internal/telegrambot` | PASS | `store: 1.352s`, `bulkops: 1.372s`, `mailer: 2.752s`, `telegrambot: 2.723s` |
| `go vet ./internal/store ./internal/bulkops ./internal/mailer ./internal/telegrambot ./internal/scheduler` | PASS | `0s` |
| `go test -count=1 ./internal/bulkops ./internal/mailer ./internal/telegrambot ./internal/scheduler` | PASS | `bulkops: 7.198s`, `mailer: 5.602s`, `telegrambot: 2.190s`, `scheduler: 9.179s` |
| `go test -count=1 '-run=Test(AdminUserSchemaV37|DistributorSchema|GiftPurchase|PublicOrigin|Telegram|Commission|Order|AdminUserBulk|ValidateSchema)' ./internal/store` | PASS | Verified |
| `go test -race ...` | NOT RUN | Windows environment lacks CGo/race toolchain; verified via deterministic clock/savepoint/trigger interleaving tests |
