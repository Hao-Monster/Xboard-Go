# 批次 C 修复与验证结果（2026-10-16）

按已确认的工程规格书与 D-4 决策执行批次 C（`BL-04`、`BL-05`、`BL-06`、`BL-07`、`BL-30`）。

## 实现与范围

- `internal/store/orders.go`：
  - `BL-04`（有意偏离旧 PHP 同款缺陷）：`AssignOrder` 在跨套餐分支增加 `activeSubscription := !user.expiredAt.Valid || user.expiredAt.Int64 > now.Unix()` 判定，已过期用户跨套餐分配记为 `OrderTypeNew`，在 `completeOrderTx` 中自动清零历史已用流量。
  - `BL-06`：`calculateOrderSurplus` 流量感知周期折算使用 `*big.Int`（`mulDivFloorBig`、`fractionLessBig`）计算 `plan.TransferEnableGiB * bytesPerGiB * totalMonths` 及使用比例，消除 `int64` 乘法溢出。
  - `BL-07`（有意偏离旧 PHP 同款缺陷）：`calculateOrderSurplus` 的 `onetime` 折算查询改为读取 `total_amount + balance_amount + surplus_amount - surplus_credit`，保留链式升级中的历史套餐价值并扣除已退回余额的 `surplus_credit`。
- `internal/store/distributor_queries.go`：
  - `BL-05`：`ListDistributorOrders` 与 `enrichDistributorOrders` 的三处 `sql.Rows` 改为 `defer rows.Close()` / `defer deviceRows.Close()`，防止 `Scan` 失败提前返回导致连接泄漏。
  - `BL-30`：`enrichDistributorOrders` 顶层 `items[index].SettlementStatus` 严格按单笔订单 `items[index].Order.PaidAt != nil` 派生（与 `enrichDistributorOrder`、`StreamDistributorOrderExport`、`distributorOrderWhere` 保持一致），同时保留 `items[index].Subscription.SettlementStatus` 原值。
- `internal/store/orders_batch_c_test.go`：新增 `TestBL04`、`TestBL05`、`TestBL06`、`TestBL07`、`TestBL30` 共 5 个根测试（含子测试共 11 个测试项）。

## Red → Green 执行证据

| 阶段 | 命令 | 状态 / 退出码 | 结果摘要 |
| --- | --- | --- | --- |
| Red（修复前） | `go test -v -count=1 '-run=TestBL(04\|05\|06\|07\|30)' ./internal/store` | FAIL / 1 | 5 个根测试对应的缺陷断言全部复现失败（`BL-04` `assigned.Type = 3, want 1`；`BL-05` `context deadline exceeded (connection leaked)`；`BL-06` `months=1025 upgradeOrder.SurplusAmount = 0, want 10244448`；`BL-07` `orderC.SurplusAmount = 10000, want 20000`；`BL-30` `renewed.SettlementStatus = 1, want Unsettled`） |
| Green（修复后） | `go test -v -count=1 '-run=TestBL(04\|05\|06\|07\|30)' ./internal/store` | PASS / 0 | 5 个根测试（11 个测试条目）全部 PASS（耗时 `3.321s`） |
| 静态检查与包内回归 | `go vet ./internal/store ; go test -count=1 '-run=Test.*(Order\|Distributor\|BL(04\|05\|06\|07\|30))' ./internal/store` | PASS / 0 | `go vet` 零告警，Order / Distributor / Batch C 相关测试全部 PASS（耗时 `8.575s`） |
