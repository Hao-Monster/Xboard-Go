# Bug Fix Batch H Test Plan (`BL-23` .. `BL-29`)

**Date:** 2026-10-16  
**Branch:** `codex/ci-001-tiered-gates`  
**Scope:** Frontend modal Escape stacking, async race conditions, pagination bounds, prefetch cache invalidation, and inline group creation (`web/src`)

## Target Bug Items

| Bug ID | Summary | Target Files | Red Test Name |
| --- | --- | --- | --- |
| `BL-23` | Nested overlays (`Modal`, `Drawer`, `NodeFilter`) handle `Escape` independently without a LIFO stack | `web/src/components/overlayStack.ts`, `web/src/components/Modal.tsx`, `web/src/components/Drawer.tsx`, `web/src/features/nodes/NodeFilter.tsx` | `Modal.test.tsx` (`BL-23` nested modal / filter / IME composing tests) |
| `BL-24` | `UsersPage` initial load shares `requestVersion` between metadata (`groups`, `plans`) and user list query, dropping metadata on fast search | `web/src/features/users/UsersPage.tsx` | `UsersPage.test.tsx` (`BL-24` metadata preserved on fast search) |
| `BL-25` | `GiftCardsPage` deleting last item on final page leaves user on empty page (`page > lastValidPage`) | `web/src/features/giftcards/GiftCardsPage.tsx` | `GiftCardsPage.test.tsx` (`BL-25` clamps page after deleting last item on final page) |
| `BL-26` / `BL-27` | `UserOrdersPage` detail modal out-of-order race and `replace(order)` keeping cancelled order in pending filter or reopening closed modal | `web/src/features/user/UserOrdersPage.tsx` | `UserOrdersPage.test.tsx` (`BL-26` / `BL-27` out-of-order detail race and pending filter removal) |
| `BL-28` | Admin mutations in `UsersPage`, `OrderManagementPage`, and `ServerGroupsPage` do not invalidate `adminDataCache` | `web/src/lib/dataPrefetchCache.ts`, `web/src/features/users/UsersPage.tsx`, `web/src/features/orders/OrderManagementPage.tsx`, `web/src/features/nodes/ServerGroupsPage.tsx` | `dataPrefetchCache.test.ts` (`BL-28` domain invalidation) |
| `BL-29` | Inline group creation in `NodeDefinitionModal` and `PlanManagementPage` does not update parent `groups` state, displaying `#ID` in table | `web/src/features/nodes/NodeDefinitionModal.tsx`, `web/src/features/nodes/NodesPage.tsx`, `web/src/features/plans/PlanManagementPage.tsx` | `PlanManagementPage.test.tsx` / `NodesPage.test.tsx` (`BL-29` inline group creation updates parent table) |

## Verification Commands

- `pnpm --dir web run typecheck`
- `pnpm --dir web run lint`
- `pnpm --dir web test`
- `pnpm --dir web run build`
