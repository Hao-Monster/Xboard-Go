# BL-01/02/03 批次 A 测试基线

基线：07476e0229efd7ed326843c67dc611c7b1366c1a；本地实现、测试、提交；不推送或部署。
按修复工程书串行交付，本批 Review 通过前不开始 B 批。

## 已确认决策与保护范围

- D-1 第一阶段：30 分钟宽限，2h30m 绝对上限；迟到支付仅结构化错误日志，不自动恢复订单或入账。
- D-2 用户已确认：允许永久用户兑换并消耗纯天数通用卡，天数不生效，响应和记录写实际生效值 0；混合卡和盲盒同样保持永久有效。
- 保留原有暂存的 frontend-parity JSON、App.tsx、App.test.tsx、styles.css，以及未跟踪 docs/HANDOVER.md 和 docs- box/。
- SQLite 使用 newTestStore/newTestAPI；测试时间固定注入，全部使用合成数据。

## 冻结测试矩阵

| 缺陷/风险 | 层级及测试文件 | 场景与预期 | 命令 |
| --- | --- | --- | --- |
| BL-01 完成权益变更 | store/orders_entitlement_change_test.go | 新购/续费/换组/永久/重置，正确用户 UUID 和新旧组；重复完成无变更；事务失败无已提交变更 | go test ./internal/store -run TestBL01 -count=1 |
| BL-01 支付与分销通知 | httpapi/order_entitlement_notification_test.go | 真实 hub add/remove 与正确 UUID；分销订阅用户而非财务用户；重复回调不通知；跨副本发布包含新旧组 | go test ./internal/httpapi -run TestBL01 -count=1 |
| BL-01 后台补完 | scheduler/order_entitlement_test.go | processing 订单提交成功后通知；失败不通知 | go test ./internal/scheduler -run TestBL01 -count=1 |
| BL-02 支付宽限 | store/orders_payment_grace_test.go | 已创建支付/有效租约保护且 Remaining=0；失败/过期租约取消；宽限过期和绝对上限取消；余额只退一次；超时禁止新支付；宽限内回调完成 | go test ./internal/store -run TestBL02 -count=1 |
| BL-02 迟到回调 | httpapi/order_entitlement_notification_test.go | 维持失败响应，结构化 Error 含交易标识、不含 payload；不复活、不发权益通知 | go test ./internal/httpapi -run TestBL02 -count=1 |
| BL-03 永久权益与实际奖励 | store/gift_card_permanent_reward_test.go | 纯天数、混合、盲盒保持 NULL 与 next_reset；其他奖励发放；天数响应/usage=0；普通有效/过期用户按正确基准延期；无套餐仍拒绝 | go test ./internal/store -run TestBL03 -count=1 |
| 公共路径回归 | 全部 Go 包 | 目标测试后完整回归、vet、build | go test ./...; go vet ./...; go build ./... |
| 并发与治理 | 受影响包/治理 | race（工具链支持时）；generate/check；diff whitespace | go test -race ./internal/store ./internal/httpapi ./internal/scheduler; go run ./cmd/projectctl generate; go run ./cmd/projectctl check; git diff --check |

真实 Redis 双实例、真实支付提供方、生产环境验收不以本地替身结果代替。每项执行结果、退出码、耗时和 Red/Green 摘要在批次报告记录。
