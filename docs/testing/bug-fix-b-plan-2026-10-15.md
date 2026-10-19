# 批次 B 冻结测试矩阵（2026-10-15）

采用明确缺陷快速执行模式。范围为 BL-08/09/10，基线 `ec4593bde9b2573da36296bf84c58cf8cd7a3d84`；A 批独立复审及动态回归已通过，才进入本批。Requirement：DIST-011、DIST-014、SUB-002；Work Item：VER-001、VER-002；现有 Issue #119/#120 于本日查询均 OPEN，Milestone 为 M3 Release Candidate，不声称关闭需求或完成业务验收。

## 已确认行为与边界

- 按工程书 D-5 默认：HWID 拒绝返回相关响应头与 404 空体；账户配额 429/Retry-After 60；槽位忙 503/Retry-After 1。
- 抽出同一个准入函数：账户配额 → 槽位 → tracker → HWID → claimed；所有失败和正常返回均释放资源。Render 成功且写响应前才标记 config issued。
- 普通订阅现有顺序、响应和测试断言不变。V1 仅对可用且有组的账户准入；不可用/无组仍返回原空节点配置。V2 与无效 token 失败限流不改。
- V1 保留既有 GET/HEAD 与预取处理契约；不移植普通入口的 HEAD 405、prefetch 425 前置策略。因此 V1 中会生成节点配置的请求（包括合法 HWID 的 HEAD/预取）也经过准入和业务标记。普通入口的既有前置拦截不移动。
- 只本地实现、测试和提交；不开始 C–H，不推送、合并或部署。D-3/D-4 不在本批决策范围。

## 可执行矩阵（生产代码修改前冻结）

新增用例放在 `internal/httpapi/legacy_subscription_admission_test.go`；使用隔离真实 SQLite，合成 `example.test` 账号与节点。失败注入仅操作本用例临时数据库，无生产接口或 schema 变化。

| 验收/风险 | 层级 | 用例与预期 | 命令 |
| --- | --- | --- | --- |
| BL-08 HWID | 真实 API + SQLite | V1 缺失/非法 HWID 404 空体；首设备自动登记；第二设备拒绝；已登记设备重试成功；容量 1 | `go test -json -count=1 -run=TestBL0[89] ./internal/httpapi` |
| BL-09 标记与生命周期 | 真实 API + SQLite/node report | 首次 claimed/issued 写入；重试保留时间；只走 V1 后正流量报告写 connected_at 与节点；重复报告保留首次连接 | 同上 |
| BL-10 共享限额 | handler + SQLite | 普通/V1 双向共享账户配额；其他账户独立；429/60；配额先于槽位 | `go test -json -count=1 -run=TestBL10 ./internal/httpapi` |
| BL-10 资源回收 | handler + SQLite | 满槽 503/1；HWID 拒绝、数据库/Prepare/Render/标记失败与成功后槽位和 InFlight 均回零；失败不得 issued | 同上 |
| 普通 V1 兼容 | API + 固定 fixture | 修改前冻结输出 golden，修改后逐字节比较；既有响应头保持 | `go test -json -count=1 -run=TestBL10LegacyV1Golden ./internal/httpapi` |
| 排除分支兼容 | API/handler + SQLite | 不可用/无组 V1、V2 不受新成本/HWID 门禁影响；普通入口 HEAD/prefetch 保持原测试 | B 目标与现有 Legacy/Subscription/Distributor 测试 |
| 回归、竞争 | 包/正式源码 | httpapi/store/subscription/operations 相关用例及 race；正式源码 test/vet/build；不改旧断言 | `go test ./cmd/... ./internal/...`；相应 `go vet` / `go build`；目标 `go test -race` |

先记录 golden 基线与新增用例 Red，再修改实现并执行 Green。数量含父/子测试，报告另列根测试数。Go 原生覆盖率报告语句覆盖率，不伪称行/分支覆盖率。根 `./...` 本轮 NOT RUN：ignored 历史 `docs-dev/gift-card-qa-20261004/supplement` 存在 httpapi/store 包冲突，沿用用户已接受的正式源码范围替代。
