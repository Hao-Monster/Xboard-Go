# BL-01～BL-30 修复工程书

| 项目 | 内容 |
| --- | --- |
| 依据 | 同目录 [BUG_LIST_REVIEW_2026-10-14.md](BUG_LIST_REVIEW_2026-10-14.md)（缺陷事实）、[TEST_RESULTS_2026-10-14.md](TEST_RESULTS_2026-10-14.md)（已有证据） |
| 代码基线 | `codex/ci-001-tiered-gates` @ `07476e0229efd7ed326843c67dc611c7b1366c1a` |
| 角色 | 实现：接手程序员；审查：本文作者（Code Review、验收证据审查） |
| 授权范围 | 本地实现、本地测试、本地提交。**不含**推送、PR、合并、部署和任何服务器操作 |
| 文档性质 | 本文写的是“怎么修”和“不能怎么修”。缺陷事实、证据边界以审查清单为准，本文不重复论证 |

> 下文行号以基线 SHA 为准。如果你开工时代码已经变化，先按函数名重新定位；**行号对不上不能成为跳过的理由**。

---

## 0. 开工前必读（硬性要求，违反直接打回）

### 0.1 工作区状态

1. 先运行 `git status --short --branch`、`git branch -vv`、`git worktree list`，结果贴到第一批交付报告里。
2. 基线工作区里**有上一任务留下的暂存改动**：`docs/project/frontend-parity-2026-10-01.json`、`web/src/App.test.tsx`、`web/src/App.tsx`、`web/src/styles.css`。另有未跟踪文件 `docs/HANDOVER.md` 和目录 `docs- box/`。
   - 这些都不属于本任务，**不得暂存、修改、删除或一起提交**。
   - `App.tsx` 与 BL-23、BL-28 有重叠。上一任务的暂存改动由原负责人提交或交接前，**不要修改 `App.tsx`**。先完成其他批次，到这一步来找我。
3. 只在唯一开发分支 `codex/ci-001-tiered-gates` 上工作。不新建分支，不 rebase/amend/reset --hard/clean -fd，不强推，也不执行任何 `git push`。
4. 暂存时逐个写明路径，禁止 `git add .` / `git add -A`。提交前检查 `git diff --cached`。

### 0.2 治理台账

- 本批缺陷没有 Requirement/Work Item ID（`BL-xx` 只是审查编号）。按先例 [`docs/project/bug-list-2026-10-10.json`](../docs/project/bug-list-2026-10-10.json) 新建 `docs/project/bug-list-2026-10-14.json`：每个 BL 一条，ID 用 `BUG-20261014-xx`。
- `requirement_ids` / `work_item_ids` 必须到 `docs/project/requirements.json`、`work-items.json` 里**查实**后填写。找不到就写 `N/A: 原因`，并在报告里列出来由我判断。**禁止编造 ID。**
- 改完 JSON 后运行 `go run ./cmd/projectctl generate` 和 `go run ./cmd/projectctl check`。**禁止手改生成的 `STATUS.md`**。
- 台账的 `status` 最多写到 `fixed_local_verified`。不得写 accepted/deployed。

### 0.3 修复方法纪律

1. **先红后绿**：每个 BL 先写能复现缺陷的失败测试，记录失败输出，再修复使它通过。报告里要有“修复前 FAIL / 修复后 PASS”两段输出摘要。做不到的（例如真实 Redis 双实例）写 `NOT RUN + 原因 + 替代证据`。
2. **断言验证结果和副作用，不照抄实现**：金额要算到分，同时断言余额、订单状态、日志表、通知是否发生，以及“不该发生的事确实没发生”。
3. **不弱化现有断言**。现有测试失败时先分析根因；确实因为行为有意改变（例如偏离 PHP parity），要在测试旁写一行注释说明原因并在报告中列出。
4. **store 层用真实隔离 SQLite**（`internal/store/store_test.go:563` 的 `newTestStore`），HTTP 层用 `internal/httpapi/server_test.go` 的 `newTestAPI*` 系列。金额、权限、迁移类不接受纯 Mock 证据。
5. **时间一律注入**。不在 store/业务函数里新增 `time.Now()`。
6. **保持文件原有换行和缩进**。`web/src/features/users/UsersPage.tsx` 是 CRLF 并且混用 Tab/空格。不得整文件格式化；diff 里出现无关行变化会被打回。
7. **只修本 BL**。看到相邻问题时记录到报告的“发现但未修”一栏，不要顺手改。

---

## 1. 批次划分与顺序

按风险从高到低串行交付。每批完成后自测、自审 diff、本地提交，然后交给我 Review。**上一批 Review 通过前不开始下一批**（可以先读下一批代码）。

| 批次 | 内容 | 主要文件 | 前置决策 |
| --- | --- | --- | --- |
| A 资金/权益 P1 | BL-01、BL-02（第一阶段）、BL-03 | `store/orders.go`、`payment_checkout.go`、`gift_cards.go`、`httpapi/payment_handlers.go`、`order_handlers.go`、`distributor_handlers.go`、`scheduler/worker.go` | D-1（宽限时长）、D-2 |
| B 配置下发门禁 | BL-08、BL-09、BL-10（同一重构） | `httpapi/subscription_handlers.go`、`client_app_config_handlers.go` | D-5 |
| C 订单/金额计算 | BL-04、BL-05、BL-06、BL-07、BL-30 | `store/orders.go`、`distributor_queries.go` | D-4 |
| D 后台任务韧性 | BL-16、BL-17、BL-18、BL-20、BL-21 | `store/commissions.go`、`orders.go`、`admin_user_bulk_jobs.go`、`telegram_outbox.go`、`bulkops/service.go`、`mailer/worker.go`、`telegrambot/worker.go` | **D-3（涉及迁移，未批准前 BL-17/18 不得动 schema）** |
| E 节点同步/会话 | BL-13、BL-14、BL-15 | `httpapi/runtime_handlers.go`、`websocket.go`、`auth_handlers.go` | 无 |
| F 订阅渲染 | BL-11、BL-12 | `subscription/core.go`、`render.go`、`uri_outputs.go`、`httpapi/subscription_handlers.go` | D-4 |
| G 运维/导入 | BL-19、BL-22 | `lifecycle/split_lifecycle.go`、`store/legacy_nodes_migration.go` | D-6 |
| H 前端 | BL-23～BL-29 | `web/src/components/Overlay.tsx`、`UsersPage.tsx`、`GiftCardManagementPage.tsx`、`UserOrdersPage.tsx`、`dataPrefetchCache.ts`、`PlanManagementPage.tsx`、`NodeDefinitionModal.tsx`、`ServerGroupsPage.tsx` | 等 `App.tsx` 解锁（见 0.1） |

提交粒度：一个 BL 或一组强耦合 BL 对应一个提交，测试和实现放在同一个提交里，使用 Conventional Commits，例如 `fix(orders): notify runtime nodes after entitlement changes (BL-01)`。台账 JSON 单独做一个 `docs:` 提交。

---

## 2. 待决策事项（由产品/负责人拍板，我已给出推荐默认值）

| 编号 | 问题 | 推荐默认 | 被阻塞的部分 |
| --- | --- | --- | --- |
| D-1 | BL-02：超时后给“已发起付款”订单多长宽限？迟到的成功回调落在已取消订单上怎么处理？ | 宽限 30 分钟，绝对上限 = 2h + 30min。迟到成功第一阶段只记 Error 日志，不自动复活订单；第二阶段（另行授权）把实付金额转入用户余额并通知管理员 | 宽限时长可以先按 30 分钟实现，最后改常量即可。**迟到成功的资金处理在决策前不得实现** |
| D-2 | BL-03：永久用户兑换“只有天数”的通用卡，是拒绝还是兑换后天数不生效？ | 拒绝兑换（不消耗卡），提示“永久订阅无需延长有效期”。混合奖励卡和盲盒里的天数部分视为不生效，其他奖励照常发放 | 预检拒绝分支 |
| D-3 | BL-17/18：新增 `scheduler_quarantine` 隔离表（schema 迁移）是否批准？ | 批准，方案见 BL-17 | BL-17/18 全部 |
| D-4 | BL-04、BL-07、BL-11 会偏离旧 PHP 行为（PHP 有同样缺陷）。是否接受“修正优先于 parity”？ | 接受，在台账和测试注释中记录为有意偏离 | 相关 oracle 断言修改 |
| D-5 | BL-08：Legacy V1 被 HWID 拒绝时返回什么？ | 与普通订阅入口一致：设置 HWID 响应头，返回 404 空体 | 无（按默认实现，决策不同时只改响应） |
| D-6 | BL-22：非整分钟秒数在导入时拒绝还是取整？ | 拒绝，报错信息包含节点 ID | 无（按默认实现） |

我会把这些决策提交给负责人。没有结论前，你按“推荐默认”实现未阻塞的部分；被阻塞的部分不要动。

---

## 3. 逐项修复方案

格式说明：**根因** → **修复方法** → **必须避免** → **必须新增的测试** → **验收**。

### 批次 A

#### BL-01 · P1 · 权益写入后未正确通知在线节点

**根因**
- `completeOrderTx`（`store/orders.go:927`）会修改 `plan_id/group_id/transfer_enable/expired_at`，但调用方拿不到旧组和新组。
- `payment_handlers.go:156` 传 `nil, nil`，`NotifyUserMutation` 在 `websocket.go:1550` 清完设备就返回。
- `order_handlers.go:100,271,606` 和 `distributor_handlers.go:67,104,247,436,522` 完全没有通知。
- **审查补充**：`ProcessStaleOrders`（`orders.go:870-877`，由 `scheduler/worker.go:144` 调用）会补完 `status=1` 的订单，也是权益写入入口，同样没有通知，一并修复。

**修复方法**
1. 在 `store/models.go` 新增：
   ```go
   type EntitlementChange struct {
       UserID       int64
       PreviousUUID string // 用户真实 uuid，remove 消息依赖它
       OldGroupID   *int64
       NewGroupID   *int64
   }
   ```
2. `completeOrderTx` 改为返回 `(EntitlementChange, error)`。在 `readOrderUser` 之后、任何修改之前克隆旧 `groupID`，在 UPDATE 成功后取新 `groupID`。如果 `orderUserState` 没有 uuid，就在同一事务里 `SELECT uuid FROM users WHERE id=?`。
3. `CompleteOrder` / `CompletePendingOrder` / `CompletePaymentWebhook` 的返回值增加 `*EntitlementChange`（建议包一层 `OrderCompletion{Order; Change *EntitlementChange}`）。**幂等分支（订单已完成、重复回调）返回 `nil` Change。**
4. `StaleOrderBatchResult` 增加 `Changes []EntitlementChange`。scheduler 在 `ProcessStaleOrders` 返回后逐个通知。scheduler 现在没有 hub 引用时，注入一个可为 nil 的窄接口 `EntitlementNotifier`，不要让 scheduler 依赖整个 `httpapi` 包。
5. 分销入口：逐个检查 `distributor_handlers.go` 列出的 5 处对应 store 函数，凡是写了订阅用户 `users` 行的，都按上面的方式返回 Change，**UserID 必须是 `subscriber_user_id`**。
6. httpapi 新增一个统一 helper：
   ```go
   func (s *server) notifyEntitlementChange(ctx context.Context, c *store.EntitlementChange) {
       if s.hub == nil || c == nil { return }
       s.hub.NotifyUserMutation(ctx, c.UserID, c.PreviousUUID, c.OldGroupID, c.NewGroupID, true)
   }
   ```
   所有入口都只调用这一个 helper。`devicesCleared=true` 保留支付入口现有的语义。

**必须避免**
- ❌ 在事务内或 `Commit` 之前通知。事务回滚后节点会拿到不存在的权益。
- ❌ 分销场景用 `order.UserID`（分销商自己）作为节点身份。
- ❌ 重复回调或已完成订单再次通知。每次都会清设备，把用户踢下线。
- ❌ `PreviousUUID` 传空字符串。用户换组被移出时，节点收到的 remove 没有 UUID，删不掉。
- ❌ 用 `NotifyNodeFull` 或全量刷新所有节点来“兜底”。成本是 O(节点数)，违背 delta 设计。
- ❌ 续费（新旧组相同）不通知。额度和到期时间变了，同组也必须推 add。

**必须新增的测试**
- store：New / Renewal / Upgrade / onetime / reset_traffic 五类订单完成后，Change 的 Old/New 组和 UUID 正确；重复完成返回 nil。
- store：`ProcessStaleOrders` 补完 `status=1` 订单时返回 Changes。
- httpapi（用真实 hub 和现有 websocket 测试里的连接替身）：
  - 支付回调后，新组节点收到 `add`，旧组节点收到 `remove`（UUID 正确）。
  - 分销新购、续费时，消息里是订阅用户 ID，不是分销商 ID。
  - 重复回调不产生第二次通知。
- 跨副本：断言发布了 `EventRefreshGroups`，`GroupIDs` 包含新旧两组（用 fake coordinator）。

**验收**：覆盖审查清单 BL-01 的验收条件。真实 Redis 跨副本测试如果在 Windows 上无法执行，标记 NOT RUN。

---

#### BL-02 · P1 · 超时取消未协调活跃外部支付（第一阶段）

**根因**：`ProcessStaleOrders` 的选择条件（`orders.go:838-841`）只看 `created_at`，不看 `payment_checkout_attempts`。手动取消有 `hasActivePaymentCheckoutTx` 保护（`:724-729`），超时任务没有。

**修复方法（第一阶段，可实现）**
1. 新增常量 `paymentSettlementGrace = 30 * time.Minute`（D-1 拍板后再调）。
2. 超时选择条件改为：超过 `orderTimeout`，并且**不存在宽限期内的活跃尝试**。同时加绝对上限，超过上限无论如何都取消：
   ```sql
   WHERE o.status = 1 OR (o.status = 0 AND o.created_at <= :cutoff AND (
       o.created_at <= :hardCutoff OR NOT EXISTS (
         SELECT 1 FROM payment_checkout_attempts a
         WHERE a.order_id = o.id AND (
              (a.status = 0 AND a.updated_at > :leaseCutoff)
           OR (a.status = 1 AND a.updated_at > :graceCutoff)))))
   ```
   其中 `hardCutoff = now - orderTimeout - paymentSettlementGrace`，`leaseCutoff = now - paymentCheckoutLease`，`graceCutoff = now - paymentSettlementGrace`。
3. **同一个谓词必须同步用到 `Remaining` 计数**（`:879-881`）。否则宽限期内的订单永远计入 Remaining，调度器可能空转。建议把 WHERE 片段抽成常量或函数，两处共用。
4. `StartPaymentCheckout`：订单已超过 `orderTimeout` 时返回 `ErrOrderState`，禁止在超时后发起新尝试。否则用户可以反复重新发起付款来刷新 `updated_at`，把订单挂成无限 pending。
5. 迟到成功回调（订单已取消）：第一阶段**只加结构化 Error 日志**（trade_no、payment_id、provider、external_id；**不记录 payload**），返回值保持现状。资金处理等 D-1。

**必须避免**
- ❌ 无限延长 pending，或只要有尝试就永不取消。绝对上限必须存在。
- ❌ 在 webhook 里把已取消订单直接改回完成。取消时余额已经退回，直接复活会白送权益，或者让余额变负。
- ❌ 改动 `cancelOrderTx` 的“条件更新成功后才退款”逻辑（`:890-911`）。
- ❌ 两处 SQL 谓词不一致（见第 3 步）。
- ❌ 在 SQL 参数里使用 `time.Now()`，必须用传入的 `now`。

**必须新增的测试**（store，真实 SQLite，可控 `now`）
1. 订单 2h+1s，尝试 status=1，5 分钟前更新：不取消，Remaining 不计它。
2. 同一订单在宽限期之后：取消，余额只退一次。
3. 尝试 status=2（失败）：按原逻辑取消。
4. 尝试 status=0，已超过租约：取消。
5. 超时后、宽限期内到达成功回调：订单完成，权益正确。
6. 超过绝对上限：即使尝试刚刚更新，也会取消。
7. 超时后再发起付款：返回 `ErrOrderState`。

---

#### BL-03 · P1 · 天数奖励把永久订阅降为限时

**根因**：`applyGiftCardReward` 通用分支（`gift_cards.go:1582-1589`）对 `expired_at IS NULL` 的用户以 `now` 为基准写入有限时间。

**修复方法**
1. 在 `applyGiftCardReward` 通用分支开头加：`permanent := user.planID.Valid && !user.expiredAt.Valid`。`permanent` 为真时**跳过 expiry 计算，保持 NULL**。余额、流量、设备数、重置流量照常发放。`next_reset_at` 走现有的 `else if user.nextResetAt.Valid` 分支保持不变。
2. D-2 预检（推荐默认）：在 `gift_cards.go:1443` 附近的条件校验中，如果是通用卡、用户永久、奖励里**唯一有效项是 ExpireDays**，返回新的错误 `ErrGiftCardNoEffect`。在 handler 的错误映射里加上 422 和中文提示；前端如果有错误文案映射，同步补上。
3. 盲盒（`GiftCardTypeMystery`）抽中的子奖励也走 `applyGiftCardReward`，第 1 步自然覆盖，不需要单独处理。
4. 检查兑换记录、兑换响应里展示的“获得奖励”：如果写的是模板奖励而不是实际生效奖励，改为实际生效值（永久用户的天数记 0），避免界面显示“+30 天”。

**必须避免**
- ❌ 把永久用户改成“极远时间”（例如 2099 年）。`NULL` 在展示（长期有效）、重置计算、折抵分支（`orders.go:1123`）中有特殊含义，改成具体时间会产生连锁错误。
- ❌ 修改套餐型卡分支（`:1546-1576`），它是另一套语义。
- ❌ 只修盲盒或只修通用卡。

**必须新增的测试**：永久用户兑换通用天数卡（D-2 拒绝且不消耗卡）；永久用户兑换混合卡（天数不生效、其他奖励生效、`expired_at` 仍为 NULL）；永久用户兑换盲盒抽中天数；有效期内用户（基准为原到期时间）；已过期用户（基准为 now）；无套餐用户（保持现有拒绝）。

---

### 批次 B

#### BL-08 / BL-09 / BL-10 · Legacy V1 绕过 HWID 门禁、业务标记、成本控制

三条同根：`legacyClientAppConfigV1`（`client_app_config_handlers.go:17-47`）跳过了普通订阅入口 `subscription_handlers.go:78-116,196-200` 的整段准入逻辑。**必须用同一个共享函数修复，禁止复制粘贴。**

**修复方法**
1. 从 `subscription_handlers.go` 抽出准入函数，**保持现有顺序**：
   ```go
   // 顺序：账号配额 → 渲染槽 → tracker → HWID 授权（含响应头/拒绝）→ 标记领取
   func (s *server) admitSubscriptionDelivery(w http.ResponseWriter, r *http.Request,
       account store.SubscriptionAccount) (hwid store.DistributorHWIDAuthorization, release func(), ok bool)
   func (s *server) markSubscriptionConfigIssued(ctx context.Context, hwid store.DistributorHWIDAuthorization) error
   ```
   `ok=false` 时函数已写完响应，调用方直接 return。`release` 负责释放渲染槽并结束 tracker，调用方 `defer release()`。
2. 普通订阅 handler 改为调用这两个函数。**行为必须零变化**，现有订阅测试不改断言、全部通过。
3. V1 handler：当 `account.AvailableAt(now) && account.GroupID != nil`（即会下发节点）时，先 `admitSubscriptionDelivery`，再 Prepare 和 Render，渲染成功、写响应前调用 `markSubscriptionConfigIssued`。不下发节点的分支（账号不可用）保持现状。
4. 拒绝响应按 D-5：HWID 头 + 404；限流 429 + `Retry-After: 60`；繁忙 503 + `Retry-After: 1`，与普通入口一致。

**必须避免**
- ❌ 两份准入逻辑（以后必然漂移）。
- ❌ 渲染槽泄漏。所有 early return（HWID 拒绝、Prepare 失败、Render 失败）都必须释放；测试要断言槽位回到 0。
- ❌ 在 Render 失败时标记 `config_issued_at`。
- ❌ 把 V2 纳入。V2 只返回应用设置，审查已确认它不在范围内。
- ❌ 改动无效 token 的失败限流（`subscriptionFailures`）。
- ❌ 把“新设备在有名额时自动登记”当成绕过来拦截，那是正常行为。

**必须新增的测试**（httpapi + 真实 store，分销订阅开启 HWID，limit=1）
- V1 无 `x-hwid` → 404，响应体不含节点；非法 HWID → 404。
- 第一个合法 HWID → 200，设备已登记，`claimed_at`、`config_issued_at` 已写入。
- 第二个新 HWID（名额已满）→ 404；已登记的 HWID 再次请求 → 200，幂等，时间戳不回退。
- 普通用户走 V1：输出与修改前逐字节一致（golden）。
- 连续请求超过配额 → 429；测试依赖里把槽位设小，占满 → 503；请求结束后槽位全部释放。
- BL-09：分销订阅只走 V1 的完整生命周期，之后节点上报连接，能写入 `connected_at`（`runtime.go:802-806`）。

---

### 批次 C

#### BL-04 · P2 · 过期用户跨套餐派单仍按升级处理

**修复方法**：`AssignOrder`（`orders.go:622-631`）的 Upgrade 分支增加有效期条件，判断方式与 `CreateOrder:146` 完全一致：
```go
activeSubscription := !user.expiredAt.Valid || user.expiredAt.Int64 > now.Unix()
case user.planID.Valid && user.planID.Int64 != input.PlanID && activeSubscription:
    order.Type = OrderTypeUpgrade
```
**必须避免**
- ❌ 顺手统一 Renewal 分支。`AssignOrder` 把“永久用户 + 同套餐”判为 New，`CreateOrder` 判为 Renewal。这是另一个行为差异，不在本 BL 范围内，只在报告中记录。
- ❌ 修改 `completeOrderTx`。
- 偏离 PHP 的派单逻辑（D-4），在台账里注明。

**测试**：过期用户 A 套餐有旧用量，派 B 套餐月付 → New，付款后用量清零，到期时间 = now+1 月；有效用户 A→B 仍为 Upgrade，`change_order_event_id=0` 时保留用量；永久用户 A→B 为 Upgrade；同套餐过期派单为 New（不变）。

#### BL-05 · P3 · 扫描失败未关闭 Rows

**修复方法**：`distributor_queries.go:109` 的 `QueryContext` 成功后立即 `defer rows.Close()`。保留末尾带错误检查的显式 `Close`（重复 Close 是安全的）。同时检查同文件其他循环有没有同样的 early return。

**测试**：测试库设 `SetMaxOpenConns(1)`，插入 `surplus_order_ids = '["x"]'` 的异常订单，用 `context.Background()` 调用列表，确认返回错误；然后用 2 秒超时的 context 执行另一个查询，必须成功（修复前会阻塞超时）。

#### BL-06 · P2 · 流量折抵累计月份乘法溢出

**修复方法**：`orders.go:1192-1203` 的 traffic-aware 分支整体改用 `math/big`：
- `totalTraffic = GiB × monthSum × bytesPerGiB`（big.Int）
- `used = min(totalTraffic, upload + download)`（加法也用 big，或复用 `safeTrafficTotal` 的饱和思路）
- 比较用 big 版 `fractionLess`，最终用新增的 `mulDivFloorBig(value int64, num, den *big.Int) int64`，饱和语义与 `mulDivFloor` 相同（超过 `maxOrderMoneyCents` 返回上限）。
- 小数值情况下结果必须与现在逐分一致。

**必须避免**
- ❌ 用 float64（金额会出现分差）。
- ❌ 把 `totalTraffic` 饱和到 MaxInt64（会改变比例，折抵金额错误）。
- ❌ 修改 `TransferEnableGiB == 0` 时“折抵为 0”的现有行为，那不在本 BL 范围。

**测试**：GiB=8,388,607，累计 1024 个月（乘积 9,223,370,937,343,148,032，不溢出）和 1025 个月（溢出）两组，直接在测试库插入订单行构造。期望值在测试里用 `big.Rat` **独立计算**，不要调用生产 helper。再加一组普通套餐的回归，结果与修改前一致。

#### BL-07 · P2 · 永久套餐链式升级遗漏上次折抵价值

**修复方法**：`orders.go:1126` 的 onetime 查询改为 `total_amount + balance_amount + surplus_amount - surplus_credit`，与周期分支 `:1167` 口径一致。先确认这两列 NOT NULL（周期分支直接 Scan 成 int64，应该是）；可能为空就用 COALESCE。结果小于等于 0 时沿用现有 `return nil`。

**必须避免**
- ❌ 累加所有历史 onetime 订单。上一笔的价值已经通过 surplus 链转入最新一笔，累加会重复计算。
- ❌ 动 `:1114-1120` 的礼品卡购买禁止分支和 `listSurplusOrderIDs`。

**测试**（精确到分）：A 剩余 10000 抵扣升级到 B 永久 20000，再付 10000；B 零用量再升级 → 折抵 20000；B 用量 50% → 10000；B 升级时折抵超过价格产生 `surplus_credit` → 下一跳只计净值。另外断言总剩余价值守恒。

#### BL-30 · P2 · 新续费行继承“已结算”展示

**修复方法**：`distributor_queries.go:287-290` 改为每笔订单的展示状态只由自己的 `paid_at` 决定：
```go
items[index].SettlementStatus = store.DistributorSettlementUnsettled
if items[index].Order.PaidAt != nil { items[index].SettlementStatus = store.DistributorSettlementSettled }
```
**开工前必须先核实两件事并写进报告**：
1. `DistributorSettlementStatus`（`models.go:1824`）除已结算/未结算外有没有其他取值。有的话，要说明那些取值如何映射。
2. 旧数据迁移是否保证“已结算的订单一定有 `paid_at`”。如果存在“订阅级已结算、订单级 `paid_at` 为空”的历史数据，这次修改会让它们显示成未结算。**这种情况先停下来找我**，不要自行决定。

**必须避免**：修改订阅级 `settlement_status` 的写入；修改过滤 SQL（`:318-323`，它本来就是正确口径）；修改结算金额逻辑。

**测试**：结算原单 → 创建续费 → 列表中原单已结算、续费未结算；按“未结算”过滤能看到续费；结算预览与列表一致。

---

### 批次 D

#### BL-17 / BL-18 · 坏单导致整批回滚（共享隔离机制，需 D-3）

**根因**：佣金批次（`commissions.go:249-310`）和超时订单批次（`orders.go:861-878`）都是“一个事务处理整批，任何一笔出错就整体返回”。坏单按排序总在队首，于是每轮都被重新选中、整批反复回滚。

**修复方法**
1. **迁移（D-3 批准后）**：按 `sqlite.go` 现有的迁移机制新增表：
   ```sql
   CREATE TABLE scheduler_quarantine (
     kind TEXT NOT NULL,            -- 'commission' | 'stale_order'
     entity_id INTEGER NOT NULL,
     reason TEXT NOT NULL,          -- 机器可读的原因码，不写个人信息
     attempts INTEGER NOT NULL,
     first_failed_at INTEGER NOT NULL,
     last_failed_at INTEGER NOT NULL,
     retry_after INTEGER NOT NULL,
     PRIMARY KEY (kind, entity_id)
   );
   ```
2. **单笔 SAVEPOINT**：每笔订单处理前执行 `SAVEPOINT sp_item`。
   - 成功：`RELEASE sp_item`，并删除该订单的隔离记录（如果有）。
   - **已知业务错误**：`ROLLBACK TO sp_item; RELEASE sp_item;` 然后 upsert 隔离记录（`retry_after = now + 1h`，attempts 加 1），继续处理下一笔。
   - **其他错误**（数据库错误、context 取消等基础设施错误）：保持现有行为，整个事务回滚并返回错误。
3. **业务错误要显式定义**，不要靠解析错误字符串：
   - 佣金：`errCommissionCycle`（`:258`）、`errCommissionBalanceLimit`（`:284-286`），两者都 wrap `ErrConflict` 以保持兼容。
   - 超时退款：在 `cancelOrderTx` 加余额之前**预检** `balance <= maxBalance - order.BalanceAmount`，不满足返回 `errRefundBalanceLimit`。不要依赖数据库 CHECK 约束报错再去识别。
4. 两个选择查询和对应的 Remaining 计数都加上 `NOT EXISTS (SELECT 1 FROM scheduler_quarantine q WHERE q.kind=? AND q.entity_id=o.id AND q.retry_after > ?)`。
5. 结果结构增加 `Quarantined int`。调度器对每笔隔离打 Warn 日志（订单 ID 和原因码）。
6. 预防项（**单独提交，需要我确认后再做**）：`admin_users.go:654` 设置邀请人时沿链向上检查是否成环，有界遍历。

**必须避免**
- ❌ 吞掉错误继续结算。一笔订单的多级入账必须在同一个 savepoint 内全部成功或全部回滚，不允许“L1 已入账、L2 失败”。
- ❌ 把坏单 `commission_status` 改成 3 或 0，或把坏的超时订单“取消但不退款”。这会改变业务状态，造成资金损失。
- ❌ 靠更新 `updated_at` 把坏单排到队尾，这会污染业务时间字段。
- ❌ savepoint 未释放就进入下一轮。每条退出路径都必须 RELEASE。
- ❌ 破坏现有“故障后重试 exactly-once”测试，它必须继续 PASS。

**测试**
- 佣金批次 `[健康1, 成环坏单, 健康2]`：健康单各入账一次；坏单 `commission_status` 仍为 1，无 `commission_logs`，余额不变，有隔离记录。重跑不重复入账，坏单被跳过。修正邀请关系并把时钟推过 `retry_after` 后，坏单入账恰好一次，隔离记录被删除。
- 余额上限坏单同上。
- 超时批次 `[健康pending, 退款越界坏单, 健康pending2]`：健康单取消并退款一次；坏单仍为 pending，已隔离；Remaining 不计入隔离期内的坏单。
- 注入基础设施错误（例如取消 context）：整批回滚，没有任何部分写入。

#### BL-16 · P2 · 批量邮件 claim 后取消没有终结路径

**修复方法**
1. 新增 store 方法 `ReleaseCancelledAdminUserBulkMailClaim(ctx, jobID, seq, claimToken, now) (bool, error)`。在事务里执行：
   ```sql
   UPDATE admin_user_bulk_targets SET status='cancelled', processed_at=?, claim_token=NULL, claimed_at=NULL
   WHERE job_id=? AND sequence=? AND status='processing' AND claim_token=?
     AND EXISTS (SELECT 1 FROM admin_user_bulk_jobs j WHERE j.id=job_id AND j.status='cancelling')
   ```
   影响 1 行时，在同一事务里调用 `refreshAdminUserBulkJobTx`。
2. `bulkops/service.go:247` 的 `!active` 分支调用上面的方法（返回 false 时忽略；出错时返回错误）。
3. **失联 worker 回收**：新增 `ReapCancellingAdminUserBulkMail(ctx, now, lease)`，在 `RunMailOnce` 开头调用。把 `cancelling` 任务中 `status='processing' AND claimed_at <= now-lease` 的目标标为 `failed`，`last_error` 写 `mail delivery result unknown after cancellation`（与 `:391` 现有口径一致），然后逐个刷新受影响的任务。

**必须避免**
- ❌ 对 cancelling 任务重新发送邮件。
- ❌ 在 `CancelAdminUserBulkJob` 里直接把 processing 改成 cancelled。worker 可能正在发送，之后 Complete 会因状态冲突失败，结果变成“已发送但记为取消”。
- ❌ 回收租约未过期的 processing 目标。
- ❌ 去掉 `claim_token` fencing。

**测试**：claim → cancel → 在 active 检查前触发（用 store 包装替身让 `AdminUserBulkMailClaimActive` 返回 false，或直接调用新方法）→ 目标 cancelled，任务 cancelled，sender 调用 0 次。失联回收：t0 claim，cancel，`t0+lease-1` 回收无变化，`t0+lease+1` 回收后目标 failed（结果未知），任务 cancelled。

#### BL-20 · P2 · Telegram 第三次认领失联后没有终结

**修复方法**：在 `ClaimTelegramMessage` 事务里，紧接着现有的无效接收人取消语句，增加一条有界回收（沿用 `:45` 的 `LIMIT 100` 子查询写法）：
```sql
UPDATE telegram_message_outbox SET failed_at=?, claim_token=NULL, claimed_at=NULL,
       last_error='delivery_result_unknown', updated_at=?
WHERE id IN (SELECT id FROM telegram_message_outbox
  WHERE sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL
    AND attempt_count >= ? AND claimed_at IS NOT NULL AND claimed_at <= ? LIMIT 100)
```
同时确认 `system_operations.go:145` 的统计口径：处理后这些记录应计入失败，不再计入 Pending/Claimed。

**必须避免**：提高最大尝试次数或重置 `attempt_count` 来重发（可能重复推送）；标记为 sent；回收租约未过期的记录。

**测试**：attempt=3、t0 claim 的消息，`t0+lease-1` 不变，`t0+lease+1` 变为 failed，last_error 为结果未知，统计相应变化；attempt=2 的过期消息按原逻辑被重新认领，不进入失败。

#### BL-21 · P2 · 批处理复用起始时间

**修复方法**：`mailer.Worker`、`telegrambot.Worker` 增加字段 `now func() time.Time`，构造时默认 `time.Now`。`runBatch` 不再接收 `now` 参数，循环内每次 `worker.RunOnce(ctx, worker.now())`。发送完成或失败时的时间戳（Complete/Fail/retryAt）在当时重新取 `worker.now()`。`RunOnce(ctx, now)` 签名保留，供确定性测试使用。

**必须避免**：在 store 内部改用 `time.Now()`；通过延长租约来掩盖问题；只修其中一个 worker。

**测试**：注入假时钟，每次 Send 前进 31 秒，批次 3 条，断言每条的 `claimed_at` 等于认领时刻的时钟值；两个 worker 共享同一 store 交替运行，每条消息只发送一次。

---

### 批次 E

#### BL-13 · P2 · runtime 更新以本地连接为条件

**修复方法**：`runtime_handlers.go:91-95` 去掉 `hasNode` 本地判断，改为：
```go
if machineID > 0 { s.hub.NotifyMachineNodes(ctx, machineID) } // 先更新成员关系，广播
s.hub.NotifyNodeFull(ctx, machineID, nodeID)                   // 再推全量，广播；持有连接的副本负责推送
```
先确认远端处理 `EventNodeFull` 时调用的是 `notifyNodeFull(..., broadcast=false)`（防止回环）。新节点可能收到两次全量同步，这是幂等的，在报告中说明即可。

**必须避免**：去掉 `broadcast=false` 的防回环；为了查连接位置做点对点 Redis 路由（过度设计）；用断开机器的方式强制节点重连（会中断用户流量）。

**测试**：两个 hub 共享 fake coordinator，连接在 B，请求打到 A，B 的连接收到 config/users/devices。没有多副本测试工具时，至少断言 A 发布了 `EventNodeFull`（machineID>0 时还有 `EventMachineNodes`），并单独测试 B 处理该事件后推送全量。覆盖 machineID=0 和非 0、新旧节点。真实 Redis 双实例：NOT RUN 并说明原因。

#### BL-14 · P2 · 解绑释放失败后节点不再进入清理列表

**修复方法**
1. `websocket.go:1168-1186`：释放失败时**不要立即 return**。收集 `failed []int64`，继续释放其他节点，对已确认释放的节点执行 `clearNodeDevices`。
2. 有失败时，把失败的 nodeID 记到连接上（`connection.addPendingRelease(ids)`，加锁保护），然后按现在的方式关闭连接（TryAgainLater）。
3. `unregisterAndClear` 额外遍历 `connection.pendingReleaseList()`，逐个调用 `ReleaseNodeIfOwned(ctx, nodeID, connection.id)`（只有拥有者才能释放，所以重复调用也安全），释放成功的节点清理设备。仍然失败就打日志，由 180 秒 TTL 兜底，并在注释里写明。

**必须避免**：释放不属于自己的 node key（必须是 owner-conditional）；持有 `h.mu` 时调用 Redis；回滚已经发给节点的新列表或 `h.nodes` 映射；清理未确认释放节点的设备。

**测试**：fake coordinator 对节点 X 的释放第一次返回错误。断言：其他被移除的节点已释放、设备已清理，连接已关闭；unregister 后 X 被重试释放并清理设备；第二次 unregister 幂等、无错误。

#### BL-15 · P3 · Access Token ID 与 Cookie Session ID 混用

**修复方法**：照搬 `listAccessTokens`（`auth_handlers.go:383-388`）已有的正确模式，新增 helper：
```go
func currentCookieSessionID(s store.SessionUser) int64 {
    if s.CredentialKind == store.CredentialKindCookieSession { return s.SessionID }
    return 0
}
```
`listAccountSessions`（:318）传入这个值；`revokeAccountSession`（:336）只在 `CredentialKind==Cookie && sessionID==session.SessionID` 时清 Cookie。

同时核实 `legacyListAccessTokens`（:421）、`legacyLogout`（:457）只能通过 `requireLegacyBearer` 访问（`server.go:429` 已确认 logout；list 的路由也要查），并把结论写进报告。如果存在 Cookie 可达的路径，按同样模式修复。

**必须避免**：在 store 层给 ID 加偏移或改 ID 空间。

**测试**：同一用户构造 Cookie 会话 ID=N 和 Access Token ID=N。Bearer 列表：没有会话被标为当前；Bearer 撤销会话 N：204，且没有清 Cookie 的 Set-Cookie；Cookie 列表：会话 N 为当前；Cookie 撤销自己：清 Cookie。

---

### 批次 F

#### BL-11 · P3 · 首节点兼容过滤连带移除虚拟提示

**修复方法**：在 `subscription` 包导出 `CompatibleNodes(nodes, client)`（包装现有的 `compatibleNodes`）。在 `subscription_handlers.go:147-151`，`FilterNodes` 之后、`PresentNodes` 之前先做兼容过滤，让虚拟节点克隆兼容的首节点。`Render` 内部的再次过滤保持不变（幂等）。`RejectedByRequestFilter` 仍然只统计请求过滤，**不改变“过滤掉 N 条线路”的计数语义**。

**必须避免**：在 `PresentNodes` 里写死某种协议作为虚拟节点（有的客户端不支持）；改变计数语义；修改 V1 的 `PrepareNodes` 路径。全部节点都不兼容时不输出提示节点，这是可接受的，在测试里固定这个行为。

**测试**：节点 `[Shadowrocket 旧版本不兼容的 hysteria v2, vmess]`，ShowInfo 开启 → 输出包含“剩余流量/套餐到期”，协议为 vmess；全部兼容时输出与修改前逐字节一致。偏离 PHP（D-4）的地方在测试注释中注明。

#### BL-12 · P2 · Shadowrocket 把 Hysteria1 输出成 Hysteria2

**修复方法**：`uri_outputs.go:78-88` 按 `version` 分支。**v2 输出保持逐字节不变**。v1 以旧 PHP `E:\CodeWorkstation\Xboardme\app\Protocols\Shadowrocket.php:355-378` 为 oracle：
- 格式：`hysteria://{wrapIPv6(host)}:{port}?{query}#{name}\r\n`
- 参数顺序：`auth`、`upmbps`、`downmbps`、`protocol=udp`、`fastopen=1`、`peer`（有 sni 时）、`obfs=xplus` + `obfsParam`（`obfs.open` 时）、`insecure`、`mport`（有 ports 时）
- PHP `http_build_query` 会**省略 null**，`false` 编码为 `0`、`true` 编码为 `1`。核对 Go 的 `buildQuery` 是否有同样语义，不一致时用 `nilIfEmpty` 等现有工具对齐。

开工前先看 `core.go:393` 对 hysteria version 的规范化：version 既不是 1 也不是 2 时，PHP 不输出这一行。Go 要么保证规范化后只有 1/2，要么同样返回空串，结论写进报告。

**必须避免**：顺手修改 v2 输出。Go v2 与 PHP v2 在 `obfs=none`、`keepalive`、`mport` 上有差异，不在本 BL 范围，发现了只记录。名称编码（第 4 节的边界风险）不在本 BL 范围。不要影响 General/Loon/Surge 的输出。

**测试**：v1 的 golden 用例（有无 obfs、有无 ports、有无 sni）；v2 golden 不变。项目里有 `oracle_matrix_test.go` 时优先在其中加用例。

---

### 批次 G

#### BL-19 · P2 · backend 回滚失败的 journal 与实际部署不符

**调用点**（共 3 处）：`split_lifecycle.go:213`（升级失败后的自动恢复）、`:281`（restore 失败）、`:286`（激活或健康检查失败）。

**修复方法**
1. `DeploymentState` 增加**可选字段**，不升级 `DeploymentStateVersion`：
   ```go
   RollbackPhase      string      `json:"rollback_phase,omitempty"`      // "restore" | "activate" | "unknown"
   ObservedDeployment *Deployment `json:"observed_deployment,omitempty"`
   ```
   `validateDeploymentState` 增加这两个字段的校验。
2. `rollbackFailure` 增加 phase 参数：
   - `:281` restore 失败：phase=`restore`，实际运行的仍是 Target，ActiveDSN 不变。
   - `:213` / `:286` 激活失败：调用 `platform.CurrentDeployment(ctx)` 观测实际状态（先看 `ActivateDeployment` 返回的 application 是否通过 `validateDeploymentApplicationIdentity`，通过就直接用）。观测成功时记录 `ObservedDeployment` 和 `ActiveDSN = 观测到的 DSN`；观测失败时 phase=`unknown`。
3. `expectedDeploymentForState` 处理 RollbackFailed：
   - 有 `ObservedDeployment` → 用它；
   - phase=`restore` → Target；
   - phase=`unknown` → 返回 nil，status/upgrade 报“需要人工恢复，见 docs/operations/lifecycle-recovery.md”；
   - **字段缺失的旧 journal → Previous**（兼容现有行为）。
4. `Rollback` 守卫（:258）允许 `StatusRollbackFailed && RollbackPhase=="restore"` 重试，作为受控恢复入口。Target 一致性检查和 manifest 校验必须保留。

**必须避免**
- ❌ 关闭或放宽 drift 检查。
- ❌ 往 journal 写没有观测到的猜测值。
- ❌ 升级 state version，导致旧 journal 无法读取。
- ❌ 恢复入口绕过备份 manifest 校验。
- ❌ 在任何真实环境执行验证，只能用 fake platform。

**测试**（fake platform）：A) restore 失败 → phase=restore，当前为 Target 时 status 不报 drift，第二次 restore 成功后 Rollback 重试成功；B) 激活后健康检查失败 → ActiveDSN 为恢复 DSN、Observed 为 Previous，status 不报 drift；C) `CurrentDeployment` 出错 → phase=unknown，status 拒绝并给出人工恢复提示；D) 旧 journal → 原有行为；现有 7 个 fake-platform 用例全部 PASS。

#### BL-22 · P3 · 导入接受秒级日窗

**修复方法**：`legacy_nodes_migration.go:503` 的 daily 校验增加 `*item.EnableSecond%60 != 0 || *item.DisableSecond%60 != 0`，返回 `fmt.Errorf("%w: daily activation schedule for node %d must be minute-aligned", ErrInvalidInput, item.NodeID)`（D-6）。

**必须避免**：静默截断或四舍五入（会改变开关时刻）；把 parser 改成 HH:MM:SS。

**测试**：3600/3630 → 导入失败，错误含节点 ID，事务无部分写入；3600/7200 → 成功；0/86340 → 成功；86399 → 现在会被拒绝（行为变化，在报告中注明）。

---

### 批次 H（前端）

通用要求：每个 BL 都要有 vitest + testing-library 自动化测试。涉及交互的，再用真实浏览器跑一遍关键流程（桌面和移动各一次），截图附在报告里。

#### BL-23 · P2 · 多层弹窗 Escape 先关闭底层

**修复方法**
1. 新建 `web/src/components/overlayStack.ts`，模块级栈 + **单一** document keydown（capture）监听，监听器按引用计数安装和卸载：
   ```ts
   export function useOverlayLayer(options: { active: boolean; onEscape: () => void; trapRef?: RefObject<HTMLElement | null> }): void
   ```
   - 挂载时 push 一个 `Symbol` token，卸载时**按 token 定位删除（splice）**。
   - 用 ref 保存最新的 `active` 和 `onEscape`。**这两个值变化时不要重新注册**，否则父层会被移到栈顶。
   - 键盘事件只交给栈顶 token。栈顶 `active=false`（suspended）时什么也不做，**不能落到下一层**。
   - Tab 焦点陷阱同样只对栈顶生效。
   - `event.isComposing`（输入法组字）时忽略 Escape。
2. `Modal`、`Drawer` 用 `useOverlayLayer` 替换 `useDialogFocus` 里的 keydown effect。焦点进入和恢复逻辑（含 `restoreFocusRef`）保持不变。`suspended` prop 保留（继续控制 inert 和 aria-modal）。
3. `NodeFilter` 弹出层和 Ctrl+K 搜索（`App.tsx:246-251,616-621`，**等解锁**）各自注册一层，`onEscape` 只关闭自己。
4. `TicketDetailDialog` 里的提现确认是 Modal，修复后会自动成为栈顶，不需要再手工挂起父层。现有的手工 suspended 逻辑保留，不要删。

**必须避免**：全局禁用 Escape；用 z-index 或 DOM 顺序判断栈顶；用 `pop()`（StrictMode 双挂载和非 LIFO 卸载都会出错）；回调变化导致重新注册。

**测试**：嵌套 Modal 时 Escape 只关闭子层，父层草稿保留；再按一次关闭父层。Modal + NodeFilter：只关闭 NodeFilter。编辑弹窗 + Ctrl+K：只关闭搜索。输入法组字时 Escape 被忽略。在 `<StrictMode>` 下正常。现有“父层已挂起时的关闭确认”回归用例 PASS。浏览器验证工单内提现确认流程（桌面和移动）。

#### BL-24 · P2 · 用户列表快搜丢弃首次权限组/套餐结果

**修复方法**：把 `UsersPage.tsx:88-119` 的 effect 拆成两个：
- **元数据 effect**：groups 和 plans 分别请求，只用 `let active`（卸载）守卫，**不参与 `requestVersion`**。
- **首屏用户 effect**：`const version = ++requestVersion.current`，只有 `version === requestVersion.current` 时才写入用户列表和 loading。保留 `consume("users")`。

**必须避免**：元数据和用户查询共用版本号；`allSettled` 整体 return；元数据失败时清空已有数据。

**测试**：延迟 groups 响应，在它返回前输入邮箱（触发 runQuery），groups 返回后选项出现。首屏用户响应晚于一次新搜索返回时，列表显示搜索结果而不是旧的首屏结果。

#### BL-25 · P2 · 礼品卡末页删除后停在空页

**修复方法**：新增 `lastValidPage(total, pageSize) = Math.max(1, Math.ceil(total / pageSize))`。在 effect 的 then 和 `load()` 中，如果 `items.length === 0 && total > 0 && page > lastValidPage`，把对应 tab 的 page 设为 `lastValidPage`（由 effect 触发重新请求）。模板和兑换码两个 tab 都要处理。**不改后端分页契约。**

**必须避免**：total=0 时进入死循环（停在第 1 页）；前端自己算 `total-1`（会和并发删除冲突）。

**测试**：模板共 21 条、第 2 页 1 条，删除后自动回到第 1 页，分页栏正常显示；兑换码同上。

#### BL-26 / BL-27 · P2 · 订单详情竞态、待支付筛选残留

**修复方法**（`UserOrdersPage.tsx`）
1. `open()`：`const id = ++detailRequest.current`，只有 id 仍是最新时才 `setSelected` / `setDetailLoading`；关闭弹窗时执行 `detailRequest.current++` 使在途请求失效。
2. `load()`：用同样的方式加 `listRequest` 版本号，解决快速切换状态 tab 时旧响应覆盖新结果的问题。
3. `replace(order)`：
   ```ts
   setOrders((cur) => statusRef.current !== "" && order.status !== statusRef.current
     ? cur.filter((i) => i.id !== order.id)
     : cur.map((i) => (i.id === order.id ? { ...i, ...order } : i)));
   setSelected((cur) => (cur?.trade_no === order.trade_no ? order : cur));
   ```
   用 `statusRef` 避免闭包里拿到旧的 status；`setSelected` 用函数式更新，**关闭弹窗后晚到的取消或轮询结果不会重新打开弹窗**。

**测试**（受控 Promise）：先打开 A 再打开 B，B 先返回、A 后返回，弹窗显示 B；关闭弹窗后响应返回，弹窗保持关闭；“待支付”下取消订单，该行被移除；“全部”下取消，该行保留并显示已取消；旧请求 A 的错误不显示。

#### BL-28 · P3 · 写操作未失效预取缓存

**修复方法**：在 `dataPrefetchCache.ts` 增加按领域的失效函数：
```ts
export function invalidateAdminData(...domains: Array<"users" | "orders" | "plans" | "groups">): void
// users→["users"]  orders→["orders"]  plans→["plans","orders"]  groups→["plans"]（"plans" 缓存条目里包含权限组）
```
在**成功回调**里调用：
- `UsersPage` 的创建、编辑、派单、生命周期操作、封禁、重置 → `users`（派单再加 `orders`）。
- `OrderManagementPage` 派单（约 :167） → `orders`、`users`。
- `ServerGroupsPage` 增、改、删 → `groups`。

可选（需要我确认）：把 `consume()` 改为取用后即删除（单次使用）。

**必须避免**：在业务代码里调用 `invalidatePrefix("")` 清空全部缓存；`App.tsx` 解锁前修改它。

**测试**：预取 users 后创建用户，`adminDataCache.consume("users")` 返回 undefined；在 ServerGroups 新建组后 `consume("plans")` 返回 undefined。

#### BL-29 · P3 · 弹窗内新建权限组后父表显示 #ID

**修复方法**：`NodeDefinitionModal`（`:65,509`）和套餐编辑器（`PlanManagementPage.tsx:366-369`）增加 `onGroupCreated?: (group: ServerGroup) => void`，在建组 API 成功后调用。父页面合并时按 id 去重：`setGroups((cur) => cur.some((g) => g.id === group.id) ? cur : [...cur, group])`，并调用 `invalidateAdminData("groups")`。

**必须避免**：整页重新加载；产生重复项；排序规则与列表现有规则不一致。

**测试**：两个编辑器里新建组并保存后，表格显示组名而不是 `#id`；重新打开编辑器时有该选项。

---

## 4. 跨批次通用避坑清单

1. **副作用在提交之后**：所有通知、日志、外部调用都放在 `tx.Commit()` 成功之后。
2. **幂等**：重复回调、重复点击、重试任务都不能产生第二次资金变动或通知。
3. **fencing**：所有 claim 类更新都带 `claim_token` 条件，不能只看 status。
4. **两处谓词一致**：选择查询改了，对应的计数或统计查询必须同步修改。
5. **时间注入**：不新增隐式 `time.Now()`；测试用可控时钟。
6. **不记录敏感数据**：日志不写支付 payload、token、邮件正文、SMTP 凭证。
7. **兼容旧数据**：新增 JSON 或 DB 字段一律可选或有默认值，不升级会导致旧数据不可读的版本号。
8. **前端竞态模板**：异步结果写入 state 前，先确认“我还是最新请求、组件还挂载着、目标对象没变”。
9. **不扩大范围**：发现的相邻问题记入报告，由我决定是否立项。
10. **Windows 限制**：`go test -race` 需要 cgo，跑不了就写 NOT RUN 和原因，不要假装跑过。

---

## 5. 每批交付报告格式（交给我 Review 时必须附带）

```text
批次：X    覆盖：BL-xx, BL-yy
提交：<SHA> <subject>（每个提交一行）
改动文件：…
修复前复现：<测试名> FAIL（关键输出 3-5 行）
修复后：<测试名> PASS
验证命令及结果：
  go test ./internal/store -run 'TestXxx|TestYyy' -count=1        PASS
  go vet ./internal/store ./internal/httpapi                       PASS
  go test ./... / go build ./...（公共路径改动时）                 PASS/FAIL
  pnpm --dir web run typecheck / lint / test                       PASS/FAIL
  go test -race …                                                  NOT RUN（原因）
偏离 PHP parity 的点：…（对应 D-4）
发现但未修：…
需要我决策的问题：…
```

验证范围按 `AGENTS.md` 第 5 节：先跑目标测试，再按影响面扩大。批次 A、B、D 改动了公共路径，交付前必须跑一次 `go test ./...`、`go vet ./...`、`go build ./...`。批次 H 交付前跑 `pnpm --dir web test` 和 `pnpm --dir web run build`。

---

## 6. 我的 Code Review 检查单（提前公开，按此打回）

**一票否决（Critical/High，直接打回）**
- [ ] 资金：会出现多退、漏退、重复入账、部分入账，或吞错继续结算。
- [ ] 通知或副作用发生在事务提交之前，或重复回调会重复触发。
- [ ] 分销通知使用了分销商 ID。
- [ ] V1 修复复制了一份准入逻辑，或存在渲染槽泄漏路径。
- [ ] 关闭或放宽了 drift 检查或安全检查；弱化或删除了现有断言。
- [ ] 未经 D-3 批准修改 schema；未经授权推送、建 PR 或部署。
- [ ] 提交中混入上一任务的暂存文件、`docs- box/` 或 `docs/HANDOVER.md`。

**必须满足**
- [ ] 每个 BL 都有先红后绿的证据；测试断言结果和副作用（包括“不该发生的事没有发生”）。
- [ ] 第 3 节列出的测试场景全部覆盖；未覆盖的写明 NOT RUN 和原因。
- [ ] 选择谓词与计数谓词一致；所有 claim 更新带 token。
- [ ] 新字段向后兼容；没有新增隐式 `time.Now()`。
- [ ] diff 只包含本 BL；没有无关的格式化或换行变化。
- [ ] 台账 JSON 字段查实无编造，`projectctl check` PASS。
- [ ] 提交信息符合 Conventional Commits，并带 BL 编号。

**我会额外做的验证**
- 逐行审查金额路径，独立复算测试期望值。
- 在本地复跑你报告中的目标测试。前端交互我会用浏览器亲自走一遍 BL-23/26/27 的关键流程。
- 检查调用方：用 `grep` 确认所有 `CompleteOrder*`、`NotifyUserMutation`、`rollbackFailure`、`compatibleNodes` 的调用点都已经适配。
