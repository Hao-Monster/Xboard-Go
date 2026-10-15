# 2026-10-14 修复工程书：批次 A 交付报告

覆盖 BL-01、BL-02 第一阶段、BL-03。执行日期：2026-10-15（Asia/Singapore）。
本地实现、目标测试、正式源码全量回归、race 和两个修复提交已完成，等待 A 批 Review。
本报告不代表线上部署或真实业务验收。按工程书，A 批 Review 通过前不开始 B 批。

## 基线、授权与保护范围

起始 HEAD：`07476e0229efd7ed326843c67dc611c7b1366c1a`。

开工 `git status --short --branch`：

```text
## codex/ci-001-tiered-gates...origin/codex/ci-001-tiered-gates [ahead 13]
M  docs/project/frontend-parity-2026-10-01.json
M  web/src/App.test.tsx
M  web/src/App.tsx
M  web/src/styles.css
?? "docs- box/"
?? docs/HANDOVER.md
```

开工 `git branch -vv`：

```text
* codex/ci-001-tiered-gates 07476e02 [origin/codex/ci-001-tiered-gates: ahead 13]
+ main 88b01376 [origin/main: behind 30] (另一 worktree)
```

开工 `git worktree list`：

| 工作区 | HEAD | 分支 |
| --- | --- | --- |
| E:/CodeWorkstation/Xboard-Go | 07476e02 | codex/ci-001-tiered-gates |
| C:/Users/冯飏/.codex/worktrees/governance-d006-sqlite | 88b01376 | main |
| C:/Users/冯飏/.codex/worktrees/xboard-durability-fixes/Xboard-Go | 81a3b5ac | detached |
| C:/Users/冯飏/.codex/worktrees/xboard-ui-fixes/Xboard-Go | 9e2662b8 | detached |
| C:/Users/冯飏/.codex/worktrees/xboard-worker-fixes/Xboard-Go | 452dc7bf | detached |
| E:/CodeWorkstation/Xboard-Go-consumer-template | 48d0cc4d | detached |
| E:/CodeWorkstation/Xboard-Go-gift-purchase-ui | ff18b2c5 | detached |
| E:/CodeWorkstation/Xboard-Go-logging-infra | 31705135 | detached |
| E:/CodeWorkstation/Xboard-Go-logging-panel | 4d4408e1 | detached |
| E:/CodeWorkstation/Xboard-Go-node-logs-20261005 | a2fe7844 | detached |
| E:/CodeWorkstation/Xboard-Go-panel-release-task | 9bbb065a | detached |
| E:/CodeWorkstation/Xboard-Go-user-sync | eb9f403b | detached |

已 fetch 并查询当前开发分支开放 PR，结果为空。没有推送、PR、合并、部署或服务器操作。
原有四项暂存差异为 +16/-5，本任务未修改或提交它们；提交前后核对 index blob 和工作文件摘要。
执行期间另出现 `web/src/features/orders/` 购买/客服相关并发改动、purchaseSupport 文件和 purchase-support-handoff 文档，本任务未修改、暂存或提交。

## 业务决定与治理映射

- D-1：第一阶段按规格默认 30 分钟宽限、2h30m 绝对上限。迟到成功只记录结构化 Error，保持原失败响应，不恢复订单、不转余额。
- D-2：用户明确选择“允许兑换：消耗卡但天数不生效”。纯天数、混合、盲盒对永久用户都保持 NULL；实际奖励天数为 0。不引入 `ErrGiftCardNoEffect` 或拒绝分支。
- D-3/D-4：未决，未改 schema，未开始相关后续批次。
- BL-01：ORD-001、ORD-004、DIST-003、DIST-007、DIST-010、NODE-003 → VER-002/VER-003。
- BL-02：ORD-002、ORD-004、PAY-001 → VER-002。
- BL-03：GIFT-001、GIFT-002 → VER-002。
- Issue #120、#121 实时查询为 OPEN，Milestone 均为 M3 Release Candidate。本次不声称关闭这些验收 Issue。
- 台账 `docs/project/bug-list-2026-10-14.json` 登记全部 30 条；仅 A 批提升至本地验证状态，其余保持未开始。ID 均查自 requirements/work-items，未编造。

## 实现与文件

| 缺陷 | 文件与结果 |
| --- | --- |
| BL-01 | store/models.go、orders.go、payment_checkout.go：事务内捕获用户 UUID、新旧组，提交后才返回 Change；重复完成/支付重放返回 nil；scheduler 补完返回 Changes。 |
| BL-01 | store/distributors.go、distributor_operations.go：创建、续费、权益编辑返回订阅用户的 Change，分销续费重放为空。 |
| BL-01 | httpapi/entitlement_notifications.go、order_handlers.go、payment_handlers.go、distributor_handlers.go：11 个实际 HTTP 写入调用点统一通知，包括工程书未列出的 legacy 新购/免费结算。 |
| BL-01 | scheduler/worker.go、httpapi/server.go、cmd/xboard/main.go：窄接口注入，API 初始化后、worker 启动前绑定；提交失败不通知。 |
| BL-02 | store/orders.go、payment_checkout.go：selection/Remaining 共用谓词；lease/grace/绝对截止边界；2h 起拒绝 checkout；条件取消退款原逻辑保持。 |
| BL-02 | httpapi/payment_handlers.go：记录 trade_no/payment_id/provider/external_id，不记录 payload；取消后的回调仍为 409/fail。 |
| BL-03 | store/gift_cards.go：永久状态跳过天数计算；预览/兑换先形成实际奖励，卡记录、usage、响应一致。套餐型卡保持原行为。 |

工程选择：在既有 Order/DistributorOrder/DistributorEntitlement 返回对象增加 `json:"-"` 的临时 Change 字段，避免批量改变既有签名和 HTTP JSON；`completeOrderTx` 返回 `(EntitlementChange, error)`。StaleOrderBatchResult.Changes 同样不序列化。实际奖励为零时沿用现有 `omitempty` 编码，解码语义为 0，不写虚假的 +30 天。

## Red → Green

先冻结 [测试矩阵](bug-fix-a-plan-2026-10-14.md)，再新增回归测试。BL-01 为编译新契约先添加零值 Change 字段，尚未接入业务实现时执行失败测试，失败原因是缺失变更信息而非编译错误。

修复前 FAIL：

```text
TestBL01OrderEntitlementChange/new,renewal,upgrade,onetime,reset_traffic:
  change=<nil> want user=1 groups=1/1 (upgrade 1/2)
TestBL01OrderEntitlementChange/processing:
  recovery result={Changes:[] Cancelled:0 Completed:1 Remaining:0}
TestBL02PaymentGrace/created,creating:
  batch={Cancelled:1 Completed:0 Remaining:0}, want cancelled=0 remaining=0
TestBL02CheckoutDeadlineAndGraceSettlement:
  expired checkout error=<nil>, want ErrOrderState
TestBL03ActualDayRewards/permanent_days,permanent_mixed,permanent_mystery:
  permanent expiry={1794571200 true}, want NULL
  reward days response/usage/code=30/30/30 want 0, uses=1
```

中间夹具修正如实记录：checkout.updated_at 不得早于 created_at；零天数 JSON 的 omitempty 按解码 0 校验；分销列表不读取 UUID，改从 GetDistributorOrderByID 获取；现代/legacy 严格 JSON 契约分别构造请求；手工 server 补充有效 panelURL。未删除或弱化既有测试断言。

修复后 PASS 场景：

| 验收项 | 测试证据 |
| --- | --- |
| 新购、续费、升级、永久、重置、后台补完 | TestBL01OrderEntitlementChange；正确 ID/UUID/Old/New，重复无 Change。 |
| 写入中失败回滚 | 同测试 rollback；订单 Pending、用户 plan/expiry/group/transfer 不变、事件 0、Change nil。 |
| 支付组增删与协调事件 | TestBL01PaymentWebhookNotifiesGroupsExactlyOnce；真实 hub/SQLite、真实 HMAC 验签；旧组 remove、新组 add、两组 EventRefreshGroups。 |
| 支付/分销幂等 | 同回调两次、不同 receipt、分销续费重放都不再通知。 |
| 分销订阅身份 | TestBL01DistributorHandlersNotifySubscriber；ID 与分销商不同；创建/续费同组 add。 |
| 管理员完成/免费结账 | TestBL01ManualAndFreeCompletionHandlers；现代和 legacy 四入口；重复不通知。 |
| 分销权益修改 | TestBL01DistributorEntitlementUpdateHandlers；现代/legacy 两入口，实际 speed/device、数据库流量/expiry 正确。 |
| scheduler | TestBL01SchedulerNotifiesOnlyCommittedRecovery；通知中重新查库确认已提交；失败不通知；重复不通知。 |
| 支付超时 | TestBL02PaymentGrace；创建支付/有效 lease 保护且 Remaining=0；失败/过期 lease/grace 取消；绝对上限取消；余额退回 300 分且仅一次。 |
| 成功/迟到支付 | TestBL02CheckoutDeadlineAndGraceSettlement、TestBL02LatePaymentLogsWithoutRevivingOrder；宽限内开通，超时禁止新 checkout；取消后回调 409，退款不再增加、无权益/receipt，无通知，日志不含 payload。 |
| 实际奖励 | TestBL03ActualDayRewards；永久纯天数/混合/盲盒，普通有效/过期，无套餐对照；三处奖励一致、卡消耗一次，永久 expiry/next_reset 不变。 |

## 执行结果

环境：Windows/amd64，Go 1.26.8，CGO_ENABLED=1，GCC（C:/msys64/ucrt64/bin/gcc.exe）。

| 命令 | 状态/退出码 | 通过/失败/跳过（含子用例） | 最大包耗时 |
| --- | --- | --- | --- |
| `go test -json -count=1 -run=TestBL0[123] -coverprofile=tmp/bugfix-a/final-target-coverage.out ./internal/store ./internal/httpapi ./internal/scheduler` | PASS / 0 | 37/0/0 | 5.086s |
| `go test -json -race -count=1 -run=TestBL0[123] ./internal/store ./internal/httpapi ./internal/scheduler` | PASS / 0 | 37/0/0 | 49.712s |
| `go test -json ./cmd/... ./internal/...` | PASS / 0 | 2168/0/36 | 147.250s |
| `go vet ./cmd/... ./internal/...` | PASS / 0 | 静态检查，不适用计数 | 未单独采集 |
| `go build ./cmd/... ./internal/...` | PASS / 0 | 构建，不适用计数 | 未单独采集 |
| `go run ./cmd/projectctl generate` / `check` | PASS / 0 | 治理检查 | 未单独采集 |
| `git diff --check`（本批路径） | PASS / 0 | 差异检查 | 未单独采集 |
| `go test ./...` | FAIL / 1 | 初始运行，见限制及夹具修正说明 | store 包 191.832s |
| `go vet ./...` / `go build ./...` | FAIL / 1 | ignored 历史目录 package 冲突 | 未单独采集 |

计数包含父测试及子测试，不将它们混称独立端到端用例。最终目标及 race 均为 10 个顶层测试、37 个测试条目，0 跳过。正式源码回归的包级结果、顶层计数、36 个既有 skip 名称/条件，以及原始日志 SHA256 见 [机器可读证据](bug-fix-a-evidence-2026-10-14.json)。没有新增 skip/only。

目标测试语句覆盖率：store 8.3%、httpapi 7.1%、scheduler 12.7%（仅此批目标测试对整个大包的覆盖，不是全量回归覆盖率）。统一 notifyEntitlementChange 为 100%，processOrders 为 92.3%，completeOrderTx 为 76.6%，readEntitlementChangeTx 为 85.7%。未覆盖错误分支包括部分数据库底层失败、非法参数及未运行的真实 Redis 环境；跨包执行不自动计入被调用包覆盖率。未独立采集行覆盖率；Go 原生工具不报告分支覆盖率。

## 审查、限制与未运行项

独立只读审查未发现明确生产代码缺陷或 Critical/High；要求补充的事务回滚用户行断言、管理员完成和分销权益编辑的真实 hub 测试均已补齐并复核。静态核对全部 CompleteOrder/CompletePendingOrder/CompletePaymentWebhook/分销写入调用面。

- 真实 Redis 双实例：NOT RUN。测试使用真实 hub 加协调发布替身，证明事件内容，不能代替跨进程投递验收。
- 真实支付提供方、生产数据库、服务器操作：NOT RUN，本次只用合成数据和本地验签。
- 浏览器/设备/前端回归：NOT RUN，本批没有前端实现；原有及并发前端任务不作为本批证据。
- `go test/vet/build ./...` 首次执行 FAIL：既有 ignored `docs-dev/gift-card-qa-20261004/supplement` 混合 httpapi/store package。首次 test 还包含本次中间夹具失败；最后的正式源码回归单独报告，不把早期失败藏为成功。历史文件原样保留。
- Go 原生 coverage 报语句覆盖率，不提供分支覆盖率；未独立计算行覆盖率。局部 target coverage 不表示整个项目的覆盖水平；跨包调用默认不计入被调用包的覆盖率。
- 未引入生产依赖、schema 迁移或新的隐式时钟。未改 PHP oracle 断言，D-4 涉及的后续修复尚未开始。
- 不新增资金补偿策略；迟到支付人工处理/第二阶段规则仍需后续授权。
- 发现但未修：上述历史目录 package 冲突；规格已列出的 B–H 缺陷；没有扩大到其他功能。

## 提交与交付边界

- `ca77e35a2cdd9c44ce493f44b32e38decb7cbbb8` — fix(orders): synchronize entitlements and bound payment grace (BL-01, BL-02)
- `9dfae293cfc81dd2f49490fb54ffbe14033d68a3` — fix(gift-cards): preserve permanent subscriptions on day rewards (BL-03)
- 台账、冻结矩阵、此报告与证据 JSON 单独 `docs:` 提交；其自身 SHA 通过 Git log 查询，避免文件内自引用。

BL-01/02 共同修改订单事务/支付结算入口，作为强耦合修复合并一个提交；BL-03 单独提交。验证绑定最终实现 SHA `9dfae293cfc81dd2f49490fb54ffbe14033d68a3`，文档提交不改变被测源码。

本地回退点为起始 HEAD；如需回退本批，可按提交反向 revert，仅回退本批提交并保留其他任务修改。不执行 reset/clean。未推送、未创建 PR、未合并、未部署。
