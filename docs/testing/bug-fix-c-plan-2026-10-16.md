# 批次 C 冻结测试矩阵（2026-10-16）

采用明确缺陷快速执行模式。范围为 BL-04、BL-05、BL-06、BL-07、BL-30，基线 `5d771d22`。Requirement：ORD-001、ORD-002、ORD-003、DIST-001；Work Item：VER-001、VER-002；Milestone：M3 Release Candidate。

## 已确认行为与边界

- **D-4 已获用户确认**：接受 `BL-04` 与 `BL-07` 对旧 PHP 已知算术/状态缺陷的有意偏离（修复旧版同款 Bug），并在测试与台账中明确标注。
- **BL-04**：`AssignOrder` 在用户已有 `planID != plan.ID` 时检查订阅是否仍有效（`!user.expiredAt.Valid || user.expiredAt.Int64 > now.Unix()`）；已过期跨套餐分配按 `OrderTypeNew` 处理，完成时重置流量（`traffic_u = 0, traffic_d = 0`）。未过期跨套餐保持 `OrderTypeUpgrade`，永久套餐跨套餐保持 `OrderTypeUpgrade`，同套餐已过期保持 `OrderTypeNew`。
- **BL-05**：`ListDistributorOrders` 与 `enrichDistributorOrders` 的三处 `QueryContext` 游标统一使用 `defer rows.Close()` / `defer deviceRows.Close()`，保证任何 `rows.Scan` 错误提前返回时及时归还数据库连接。
- **BL-06**：`calculateOrderSurplus` 流量感知周期折算中，`totalMonths` 跨笔累加与 `plan.TransferEnableGiB * bytesPerGiB * totalMonths` 乘法以及比例比较改用 `math/big.Int`（`mulDivFloorBig`、`fractionLessBig`），消除 `months >= 1025` 且 `TransferEnableGiB == maxPlanTransferEnableGiB`（`8_388_607 GiB`）时的 `int64` 溢出。
- **BL-07**：`calculateOrderSurplus` 的 `onetime` 折算查询改为读取 `total_amount + balance_amount + surplus_amount - surplus_credit`，保留链式升级中的历史套餐价值并扣除已退回余额的 `surplus_credit`。
- **BL-30**：前置核查确认 `DistributorSettlementStatus` 仅 `0 (Unsettled)` / `1 (Settled)` 两态，且 `enrichDistributorOrder`（单条）、`StreamDistributorOrderExport`、`distributorOrderWhere`、`PreviewDistributorSettlement`、`SettleDistributorOrders` 均按单笔订单 `o.paid_at != nil` 判定结算状态。`enrichDistributorOrders` 顶层 `items[index].SettlementStatus` 改为严格按 `items[index].Order.PaidAt != nil` 派生，保留 `items[index].Subscription.SettlementStatus` 原值不变。

## 可执行矩阵（生产代码修改前冻结）

新增用例放在 `internal/store/orders_batch_c_test.go`，使用隔离 SQLite 数据库。

| 验收/风险 | 层级 | 用例与预期 | 命令 |
| --- | --- | --- | --- |
| BL-04 过期跨套餐分配 | store + SQLite | 过期跨套餐 `AssignOrder` 为 `OrderTypeNew` 且完成后 `traffic_u/d=0`；未过期跨套餐与永久跨套餐保持 `OrderTypeUpgrade`；同套餐过期为 `OrderTypeNew` | `go test -v -count=1 -run=TestBL04 ./internal/store` |
| BL-05 游标异常关闭 | store + SQLite (`MaxOpenConns=1`) | 注入损坏 `paid_at` 触发 `rows.Scan` 报错后，连接池不泄漏，后续查询在超时内正常返回 | `go test -v -count=1 -run=TestBL05 ./internal/store` |
| BL-06 大流量多月折算防溢出 | store + SQLite | 标准套餐结果不变；`8_388_607 GiB` 在 `1024` 与 `1025` 个月边界均不发生 `int64` 溢出，折算额单调正值 | `go test -v -count=1 -run=TestBL06 ./internal/store` |
| BL-07 永久套餐链式升级净值 | store + SQLite | `A -> B -> C` 零流量与半流量链式升级保留历史抵扣价值；产生 `surplus_credit` 时下一跳按扣除 `surplus_credit` 后的净值折算 | `go test -v -count=1 -run=TestBL07 ./internal/store` |
| BL-30 分销续费列表结算展示 | store + SQLite | 首次订单已结算后创建的未结算续费单，在 `ListDistributorOrders` 与 `GetDistributorOrder` 中顶层 `SettlementStatus` 均为 `Unsettled`，`Subscription.SettlementStatus` 仍为 `Settled` | `go test -v -count=1 -run=TestBL30 ./internal/store` |
| 包内订单与分销回归 | store | 既有 Order / Distributor 测试与 `go vet ./internal/store` 全部通过 | `go vet ./internal/store`；`go test -count=1 '-run=Test.*(Order\|Distributor\|BL(04\|05\|06\|07\|30))' ./internal/store` |
