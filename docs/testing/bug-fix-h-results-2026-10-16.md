# Bug Fix Batch H Test Results (`BL-23` through `BL-29`)

**Date:** 2026-10-16  
**Branch:** `codex/ci-001-tiered-gates`  
**Scope:** Frontend React/TypeScript overlay Escape stack, user editor reset, gift card pagination clamp, user order detail request versioning & filtered replacement, admin prefetch cache invalidation, and inline server group state sync (`web/src`)

## Red → Green Summary

| Bug ID | Test File & Case | Red Result | Green Result |
| --- | --- | --- | --- |
| `BL-23` | `src/features/tickets/TicketDetailDialog.test.tsx` (`opens and closes the ticket confirmation overlay without calling onClose on the parent ticket dialog when Escape is pressed (BL-23)`) | FAIL (`expected "vi.fn()" to not be called at all, but actually been called 1 times`) | PASS |
| `BL-24` | `src/features/users/UsersPage.test.tsx` (`populates groups and plans even if a user search increments requestVersion before initial metadata resolves (BL-24) and invalidates prefetch cache on mutations (BL-28)`) | FAIL (`Expected groups/plans select options to populate when search increments requestVersion`) | PASS |
| `BL-25` | `src/features/giftcards/GiftCardManagementPage.test.tsx` (`clamps to the last valid page after deleting the only record on page > 1 (BL-25)` — template & code tabs) | FAIL (`Unable to find an element with the text: 新人礼品卡`; showed `暂无礼品卡模板` / `暂无兑换码` on empty page 2) | PASS |
| `BL-26`, `BL-27` | `src/features/orders/OrdersPage.test.tsx` (`ignores stale detail responses and removes status-mismatched orders from filtered user list (BL-26, BL-27)`) | FAIL (`expected document not to contain element: <tbody>...A-001...已取消...</tbody>`) | PASS |
| `BL-28` | `src/features/users/UsersPage.test.tsx` (`populates groups and plans even if a user search increments requestVersion before initial metadata resolves (BL-24) and invalidates prefetch cache on mutations (BL-28)`), `src/features/orders/OrdersPage.test.tsx` (`invalidates both orders and users prefetch cache when assigning an order in OrderManagementPage (BL-28)`), `src/lib/dataPrefetchCache.test.ts` (`invalidates multiple adminDataCache keys via invalidateAdminData`) | FAIL (`expected cached plans/orders/users to be invalidated or refetched`) | PASS |
| `BL-29` | `src/features/plans/PlanManagementPage.test.tsx` (`retains inline-created server groups in parent state after closing and reopening the plan editor (BL-29)`), `src/features/nodes/NodeManagementPage.test.tsx` (`syncs inline-created server groups from NodeDefinitionModal to NodeManagementPage state (BL-29)`) | FAIL (`Unable to find an element with the text: 内联VIP组`) | PASS |

## Commands Executed

- `pnpm --dir web test -- src/lib/dataPrefetchCache.test.ts src/features/tickets/TicketDetailDialog.test.tsx src/features/users/UsersPage.test.tsx src/features/giftcards/GiftCardManagementPage.test.tsx src/features/orders/OrdersPage.test.tsx src/features/plans/PlanManagementPage.test.tsx src/features/nodes/NodeManagementPage.test.tsx` — PASS (7 files, 88 tests)
- `pnpm --dir web test` — PASS (66 test files, 318 tests)
- `pnpm --dir web run typecheck` — PASS
- `pnpm --dir web run lint` — PASS (0 warnings, 0 errors)
- `pnpm --dir web run build` — PASS (within entry bundle budget)
