# Bug Fix Batch D Test Plan (`BL-16`, `BL-17`, `BL-18`, `BL-20`, `BL-21`)

- **Date**: 2026-10-16
- **Branch**: `codex/ci-001-tiered-gates`
- **Spec Reference**: `docs- box/FIX_ENGINEERING_SPEC_2026-10-14.md` §Batch D

## Scope

1. **`BL-16` (`internal/store/admin_user_bulk_jobs.go`, `internal/bulkops/service.go`)**:
   - Release claimed recipients when a bulk mail job transitions to `cancelling`/`cancelled` during SMTP delivery (`ReleaseCancelledAdminUserBulkMailClaim`) instead of persisting `status='sent'`.
   - Reap stale claimed recipients (`claimed_at + lease < now`) and settle `cancelling` bulk mail jobs into `cancelled` at the beginning of `RunMailOnce` (`ReapCancellingAdminUserBulkMail`).
2. **`BL-17` (`internal/store/sqlite.go`, `internal/store/schema_validation.go`, `internal/store/commissions.go`, `internal/scheduler/worker.go`)**:
   - Add `scheduler_quarantine` table (`schemaV69SchedulerQuarantine`, schema version `69`).
   - Isolate each order in `ProcessPendingCommissions` inside `SAVEPOINT sp_item`, detect inviter-chain cycles (`errCommissionCycle`, max 3 levels) and commission balance overflow (`errCommissionBalanceLimit`), roll back only the poison order to `sp_item`, record it in `scheduler_quarantine`, and allow valid orders in the same batch to commit.
3. **`BL-18` (`internal/store/orders.go`, `internal/scheduler/worker.go`)**:
   - Isolate each stale order cancellation in `ProcessStaleOrders` inside `SAVEPOINT sp_item`, check `users.balance + balance_amount > MaxUserCreditCents` (`errRefundBalanceLimit`) before `cancelOrderTx` credit refund, roll back only the overflowing order to `sp_item`, record it in `scheduler_quarantine`, and allow subsequent stale orders in the batch to cancel.
4. **`BL-20` (`internal/store/telegram_outbox.go`)**:
   - Recover `telegram_outbox` rows stuck in `status='processing' AND attempt_count >= 3 AND claimed_at <= ?` in `ClaimTelegramMessage` by marking them `status='failed', last_error='delivery_result_unknown'` (bounded `LIMIT 100`).
5. **`BL-21` (`internal/mailer/worker.go`, `internal/telegrambot/worker.go`)**:
   - Advance the worker clock (`worker.currentNow()`) per message claim and completion/failure inside `runBatch` and `RunOnce` instead of reusing a single batch-start timestamp across all messages.

## Planned Verification Commands

- `go test -v -count=1 '-run=TestBL(16|17|18|20|21)' ./internal/store ./internal/bulkops ./internal/mailer ./internal/telegrambot`
- `go test -count=1 ./internal/store ./internal/bulkops ./internal/mailer ./internal/telegrambot ./internal/scheduler`
- `go vet ./internal/store ./internal/bulkops ./internal/mailer ./internal/telegrambot ./internal/scheduler`
