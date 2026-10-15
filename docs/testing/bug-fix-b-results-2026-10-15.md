# 批次 B 修复与验证结果（2026-10-15）

A 批在 `ec4593bde9b2573da36296bf84c58cf8cd7a3d84` 的源码独立复审、store/httpapi/scheduler 重新回归及 A 定向 race 均通过后，按用户指令进入 B。A 本轮为 1291/0/9，race 45/0/0；NULL UUID 真实 SQLite 订单完成、权益与幂等，以及空 UUID 不发 remove 均核实。详见 A 结果与证据新增的再复审节。

## 实现与范围

BL-08/09/10 共用根因，采用一个代码提交 `d35449b9546481ae329426f5184ddcdb58538abb`，不复制两份门禁。源码和 golden SHA256 见本批证据 JSON。Requirement DIST-011、DIST-014、SUB-002；Work Item VER-001、VER-002；Issue #119/#120 在本日查询为 OPEN，Milestone M3 Release Candidate，本批不关闭它们。

- `subscription_admission.go`：统一账户配额、渲染槽、tracker、HWID 授权及领取标记；拒绝和数据库失败自行释放资源，成功交由 handler defer 释放。
- `subscription_handlers.go`：复用两个 helper；保留普通入口 HEAD/prefetch、响应头、错误顺序与其他行为。既有测试文件和断言未改。
- `client_app_config_handlers.go`：V1 仅可用且有组时准入；成功 Render 后、写响应前标记 config issued。V2、无效 token limiter、不可用/无组空节点配置保持原路径。
- D-5 按工程书默认：HWID 404 空体及相关头、账户限额 429/60、繁忙 503/1。有容量的新 HWID 正常自动登记。

## 矩阵结果

| 验收/风险 | 实际证据 | 结果 |
| --- | --- | --- |
| HWID 门禁 | 真实 API/SQLite：缺失/非法拒绝、首设备登记、第二设备超限、已有设备重试；未放行前不写业务标记 | PASS |
| 标记和首次连接 | V1 首次 claimed/issued 写入，重试保留首次时间；正流量 NodeReport 写 connected_at/节点，重复报告保持首次值 | PASS |
| 共享成本控制 | 普通→V1、V1→普通双向共享账户预算；独立账户不受影响；429/60；配额先于槽位；503/1 | PASS |
| 资源与失败副作用 | HWID 拒绝、HWID 数据库失败、claim、List、Prepare、Render、issued 七种失败；槽位/InFlight 归零、issued 未写；解除故障后恢复 | PASS |
| 普通 V1 golden | 固定 UUID/时间/节点，修改前捕获 20789 字节；最终 bytes.Equal；不新增普通入口专用头 | PASS |
| 排除分支及既有契约 | 不可用/无组/V2 在满槽下仍返回原结果；既有 Legacy/普通订阅/distributor 用例未改断言 | PASS |
| V1 HEAD/prefetch 专项 | 源码审查保留既有 V1 行为；未新增该分支动态用例。普通入口405/425已有用例不充作V1证据 | NOT RUN |

## 执行证据

环境 Windows/amd64、Go 1.26.8、CGO_ENABLED=1、MSYS2 GCC；SQLite 使用隔离合成数据，故障注入只在各用例临时数据库。测试条目含父/子测试，根测试数、包结果、跳过名称与原因见 JSON。时间是最大包耗时，不是 shell 墙钟。

| 命令 | 状态 / 退出码 | 通过 / 失败 / 跳过 | 最大包耗时 |
| --- | --- | --- | --- |
| `go test -json -count=1 -run=TestBL10LegacyV1Golden ./internal/httpapi` | FAIL / 1 | 0 / 1 / 0 | 6.881s |
| `go test -json -count=1 -run=TestBL10LegacyV1Golden ./internal/httpapi` | PASS / 0 | 1 / 0 / 0 | 1.045s |
| `go test -json -count=1 '-run=TestBL(08\|09\|10)' ./internal/httpapi` | FAIL / 1 | 1 / 13 / 0 | 11.32s |
| `go test -json -count=1 '-run=TestBL(08\|09\|10)' -coverprofile=tmp/bugfix-b/target-coverage.out ./internal/httpapi` | FAIL / 1 | 14 / 0 / 0 | 11.735s |
| `go test -json -count=1 '-run=TestBL(08\|09\|10)' '-coverprofile=tmp/bugfix-b/target-coverage.out' ./internal/httpapi` | PASS / 0 | 14 / 0 / 0 | 9.286s |
| `go test -json -race -count=1 '-run=Test(BL(08\|09\|10)\|LegacyClientAppConfig\|ClientSubscription\|ValidSubscription\|SubscriptionRenderConcurrency\|DistributorDirectSubscriptionHWID)' ./internal/httpapi` | PASS / 0 | 34 / 0 / 0 | 188.134s |
| `go test -json -count=1 ./cmd/... ./internal/...` | PASS / 0 | 2190 / 0 / 36 | 150.257s |
| `go vet ./cmd/... ./internal/...` | PASS / 0 | 不适用 | 未单独计时 |
| `go build ./cmd/... ./internal/...` | PASS / 0 | 不适用 | 未单独计时 |

Red 的 13 个失败条目来自目标缺陷，golden 同时通过。首次 golden 捕获因 testdata 目录不存在失败，建立目录后成功，属于 fixture 准备错误，不冒充产品 Red。首次带覆盖率的 Green 因 PowerShell 未引用参数拆出 `.out` 包而整体 FAIL（真实 httpapi 14 项 PASS）；引用完整参数后的 Green 为 PASS。`go tool cover` 的一次未引用参数调用同样报 too many arguments，纠正后成功；这些调用错误不改写为通过。

本批目标语句覆盖率：httpapi 7.1%，准入 helper 100%，标记 helper 100%，V1 97%，普通 handler 45%。这是目标用例对大包的局部覆盖，非全仓指标；没有单独测行覆盖率，Go 原生工具不提供分支覆盖率。V1 鉴权提前返回未被本批目标覆盖，但既有 token 契约在 full/race 执行。

根 `go test/vet/build ./...` 本轮 **NOT RUN**：ignored 历史 docs-dev/gift-card-qa-20261004/supplement 的 httpapi/store 混包仍阻塞，沿用用户接受的 `./cmd/... ./internal/...`。A 首次实际运行失败仍保留，不篡改历史。全源码既有 skip 见 JSON；未新增 skip/only，未弱化断言。

## 审查与交付边界

独立只读审查未发现 Critical/High 或需要打回的 Medium；核对执行顺序、释放责任、标记时机、真实数据库故障和输出兼容性。源码、既有断言、最终 diff 已复核；未引入依赖或迁移。

V1 的 GET/HEAD、预取处理沿用旧契约；会生成节点配置的 V1 请求现经过准入及标记，没有移植普通入口的405/425策略。受控满槽测试和 race 不代表压力/容量认证。真实客户端/设备、生产、支付提供方与部署验收未运行；前端无变更，本批未重跑前端。

A 原历史台账中待复审/前端尚未集成等描述已明确被后续状态替代。前端任务已由此前授权推送中的 `746dd513`、`07c129f6` 集成；本批未改前端，H 尚未开始。礼品卡权益未通知节点仍是发现未修项，未扩大 B 范围。

当前分支 `codex/ci-001-tiered-gates`；B 代码已本地提交，证据和 A 复审结论另作 docs 提交，不 amend。B 未推送、未创建/更新 PR、未合并、未部署；C–H 未开始。回退使用对本批提交的普通 revert，不重置共享历史。

`go run ./cmd/projectctl generate`、`go run ./cmd/projectctl check`、差异空白检查均 PASS / 0；治理命令输出 `project governance check passed`，未单独计时。未手改生成的 STATUS。
