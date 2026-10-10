# 2026-10-10 Bug 修复与本地验收总报告

## 范围与授权

用户要求的顺序为：记录 Bug List → 逐项研究方案 → 修复 → 等待部署授权。范围固定为 BUG-20261010-01 至 11，不把本次结果解释为整个项目没有其他 Bug。

结果：11 项均已修复并完成下述本地验证；独立审查提出的本次修复缺口已处理。尚未推送、部署或进行线上业务验收。

- 原始代码基线：`2acd15f9f917b9c75ed39c0ac644cd9e2427fec1`。
- 最终实现快照：`e79c62a3c281027ea7f35b287682a5cfc875646c`；后续收尾提交仅更新本报告与缺陷台账，不改变实现或测试代码。
- 开发分支：`codex/ci-001-tiered-gates`；保留原有五个未推送提交。
- [缺陷台账](../project/bug-list-2026-10-10.json)、[先行冻结的方案与测试矩阵](bug-fix-plan-2026-10-10.md)。
- 本次仅本地代码、合成测试数据、测试和提交；没有推送、PR、部署、生产操作或真实邮件投递。
- 用户原有未跟踪文件 `docs/HANDOVER.md` 和被忽略的 `docs-dev/` 历史取证材料均保持原样。

## Bug List 与验收映射

以下序号均带前缀 `BUG-20261010-`。具体源码、需求、工作项、Issue、Milestone 关联见 JSON 台账；历史已关闭的 #123 不重开，不作为本次新关闭事项。

| 编号 / 优先级 | 已确认问题 | 修复方案 | 本地验收证据 |
| --- | --- | --- | --- |
| 01 / P1 | 改密、身份/权限变更后旧登录链接和重置挑战仍有效 | 同一数据库事务撤销全部待用凭据并清理对应未完成邮件 | 真实 SQLite，9 种变更、其他用户隔离、预检查挑战再次校验、清理失败原子回滚 |
| 02 / P1 | 自动恢复旧快照会丢失快照后的已确认写入 | 失败后停止候选并保留最新数据库/WAL、配置和备份，要求显式恢复 | combined/split 状态机、隔离 SQLite、实际发布 shell 的本地替身验证 |
| 03 / P1 | 主流程先于 HTTP Shutdown 完成而退出 | 明确等待 HTTP 排空和监听结果，保留并返回失败 | 真实 loopback 慢请求、监听失败、取消、超时与错误合并 |
| 04 / P1 | 登录/改密/管理入口绕过密码计算并发上限 | 复用既有两槽池，覆盖 Verify、Hash、升级及冲突重试 | 有界低成本替身验证饱和拒绝、成功/失败释放、批量取消 |
| 05 / P2 | 可信代理后普通用户错误共享代理 IP 配额 | 入口解析受信代理链，保留 transport peer 独立限流 | 9 种客户端/代理/非法头场景与既有 node peer 回归 |
| 06 / P2 | 邮件第三次领取后 worker 失联使任务永久 pending | 租约到期有界终结为失败/结果未知，不再第四次发送 | 真实 SQLite，边界、重启、并发、迟到 token、事务回滚及 100 条批次上限 |
| 07 / P2 | 正确但不健康的目标不能显式回滚 | 校验目标身份而非健康，保留镜像/DSN/备份门禁 | unhealthy/stopped/target_failed 接受，错误身份仍拒绝 |
| 08 / P2 | journal 半条尾行与下一次追加拼接损坏 | 操作锁内验证完整历史，仅截去未完成尾行后追加并同步 | 两种 journal Load→Append→Load、损坏完整行/无完整记录/上限拒绝 |
| 09 / P2 | 套餐修改后重挂恢复旧预取缓存 | 失效实际缓存，阻止旧读取覆盖与旧 Promise 误删 | 组件交互、缓存测试、真实构建桌面/移动浏览器 |
| 10 / P2 | 订单详情乱序覆盖或关闭后重新打开 | 详情请求代次与页面/列表生命周期分离 | 乱序成功/错误、关闭/卸载、成功操作后的当前列表同步、慢初始化下套餐选项保留 |
| 11 / P2 | 工单关闭确认 Escape 同时关闭父弹窗并丢草稿 | 确认期间挂起父 Modal，复用现有焦点规则 | 管理员/用户草稿与焦点、正常关闭、真实浏览器键盘操作 |

## 分组执行记录

- [凭据、密码工作、可信代理](bug-fix-auth-2026-10-10.md)：BUG01/04/05 的 Red→Green、全模块回归、隔离与历史迁移 fixture 说明。
- [数据保留、显式恢复、journal](bug-fix-durability-2026-10-10.md)：BUG02/07/08 的矩阵、coverage、race、actionlint 及 workflow shell 验证。
- [HTTP 停机和邮件收尾](bug-fix-worker-2026-10-10.md)：BUG03/06 的真实 HTTP/SQLite、多连接并发、错误注入与目标覆盖率。
- [前端缓存、异步状态与弹窗](bug-fix-ui-2026-10-10.md)：BUG09/10/11 的组件回归、完整前端覆盖率、审查补强和真实浏览器证据。

## 集成验证

环境：Windows amd64；缓存 Go 1.26.8（`GOTOOLCHAIN=auto`、`GOPROXY=off`）；Node 24.12.0、pnpm 11.10.0、Vitest 4.1.11、Chromium。race 使用本机 MinGW GCC、`CGO_ENABLED=1`。耗时以实际工具记录为准，未单独计时的命令不虚构耗时。通过数量分别统计顶层测试与子测试，不把它们混称独立用例数量。

| 命令 / 范围 | 状态 / 退出码 | 数量 / 时间 / 说明 |
| --- | --- | --- |
| `go test -p 2 ./... -count=1 -json -coverprofile=...` | FAIL / 1 | 401.735s；1254 顶层/877 子测试通过、0 用例失败、34 顶层/2 子测试既有跳过；本机 ignored `docs-dev/gift-card-qa-20261004/supplement` 混合 httpapi/store 包，额外包配置失败 |
| `go test -p 2 ./cmd/... ./internal/... ./tools/... -count=1 -json -coverprofile=...` | PASS / 0 | 35 包；1254 顶层/877 子测试通过、0 失败，34 顶层/2 子测试既有跳过，329.211s |
| `go vet -p 2 ./cmd/... ./internal/... ./tools/...` | PASS / 0 | 33.416s；正式源码全量静态检查 |
| `go build -p 2 ./cmd/... ./internal/... ./tools/...` | PASS / 0 | 23.634s；正式源码全量构建 |
| `python -m unittest deploy/legacy_parity_deploy_test.py` | PASS / 0 | 12 通过、0 失败/跳过，15.393s；未执行实际 Docker/SSH/HTTP |
| `go run ./cmd/projectctl generate`、`go run ./cmd/projectctl check` | PASS / 0 | 首次共 11.070s；最终台账更新后复验共 7.472s；没有生成规范治理记录的无关差异 |
| `git diff 2acd15f9 HEAD --check` | PASS / 0 | 最终实现集成后的累计差异检查通过 |
| `CGO_ENABLED=1 go test -p 1 -race ./internal/store ./internal/httpapi -run '^(TestCredential\|TestPasswordWork\|TestClientIP)' -count=1 -json` | PASS / 0 | 8 顶层/32 子测试通过，0 失败/跳过，无 race 诊断；store 227.483s、httpapi 38.685s，墙钟 381.048s |
| `go test -race ./cmd/xboard ./internal/store -run 'TestHTTPShutdown\|TestMailFinalClaimRecovery' -count=1 -json`（分组） | PASS / 0 | 14 顶层/21 子测试，0 失败/跳过；cmd 8.288s、store 63.507s，墙钟 159.477s |
| `go test -race ./internal/lifecycle -count=1 -json`（分组） | PASS / 0 | 1 包通过、0 失败/跳过；48.617s，详见 durability 报告 |
| `pnpm --dir web run test:coverage`（首轮） | FAIL / 1 | 65 文件，296 通过/1 超时/0 跳过，759.33s；既有节点用例接近 5 秒阈值，未改 timeout 或断言 |
| `pnpm --dir web run test:coverage --maxWorkers=2 --coverage.reportOnFailure --reporter=dot` | PASS / 0 | 65 文件、297 通过/0 失败/0 跳过，635.51s；这是订单审查补强前的完整前端快照 |
| 最终套餐/订单/工单/cache 关联 coverage（完整 C2 命令见 UI 报告） | PASS / 0 | 12 文件、62 通过/0 失败/0 跳过，119.42s；包含两次审查补强后的最终代码 |
| `pnpm --dir web run typecheck`、`lint`、`build` | PASS / 均 0 | 最终代码分别 36.74s、78.32s、22.26s；未关闭规则或降低构建预算 |
| `pnpm --dir web exec playwright test e2e/ui-state-regressions.spec.ts --reporter=list` | PASS / 0 | 最终构建、Chromium 桌面/移动模拟，12 通过/0 失败/0 跳过/0 retry，34.6s；本地预览与合成 API |

### 失败与环境边界

`docs-dev/` 在 `.gitignore` 中明确忽略，项目 AGENTS 将其定义为历史取证区，不是现行验收依据。`go list -e -json ./docs-dev/gift-card-qa-20261004/supplement` 确认同目录同时声明 `httpapi` 和 `store`；`git check-ignore -v` 确认它是被忽略的历史材料。上述文件没有改名、删除、搬移或修改，字面 `./...` 的退出码 1 仍保留。与 Go 默认 `./...` 匹配的已跟踪正式源码包位于 cmd/internal/tools，使用这三个根目录再次完整执行，不按失败用例筛选排除测试。

36 条既有 skip 事件来自未配置的 Redis 集成/故障注入、未提供的真实 legacy 插件快照与 XDB、Windows 不支持的 POSIX 权限语义、未启用的 1 GiB 附件/大规模 WebSocket 容量，以及真实客户端 fixture 导出。这些项不计为通过；没有为本次修复新增 skip。所需外部环境、数据和容量验证没有被普通单元测试替代。

其他观察到的测试 fixture/覆盖率/检查工具问题及处理详见分组报告。没有降低断言、扩大超时、跳过新测试或关闭现有 coverage 门禁来制造通过结果。

收尾台账校验 PASS：11 个唯一 ID 与冻结范围一致；全部为 `fixed_local_verified`；逐项修复提交均存在且为当前分支祖先；方案与证据文件全部存在；推送、PR、部署和生产操作授权均保持 false。集成树比较确认 Go 实现及依赖在全量验证后未变，前端与执行最终验证的 `9e2662b8` 树一致。

### 覆盖率

正式 Go 源码全量 profile 为 `C:\Temp\xboard-bugfix-20261010-integrated\go-source.cover`，`go tool cover -func` PASS/exit0，总语句覆盖率 **69.9%（34867/49850）**。受影响包：store 71.1%、httpapi 69.5%、cmd/xboard 64.9%、lifecycle 69.0%、cmd/xboard-lifecycle 46.0%。部分受影响函数：HTTP 停机协调、密码工作入口、client IP middleware 均 100%；journal append 73.9%、完整记录校验 90.0%；凭据撤销 61.5%；ticket/reminder claim 为 77.4%/78.1%。凭据清理和 claim 的多项基础设施错误返回没有逐个故障注入，已用真实 SQLite trigger 验证清理/领取失败时整体事务回滚；不能把函数或包覆盖率当作所有错误路径都已覆盖。

前端全量复验在订单独立审查补强之前取得 297/297 PASS：语句 78.19%、分支 71.37%、函数 74.34%、行 83.70%，原有 70/55/70/70 门禁通过。最终补强后重跑受影响的 62 项：四个修改文件语句 86.97%、分支 78.69%、函数 81.21%、行 90.48%，同样通过原门禁。不把前一版全量证据冒充最后一版全量重跑，不将前后两轮有重叠的用例数量相加。

## 独立审查

- 主负责者复核全部生产差异、范围、调用方和文档；各实现组使用独享 detached worktree，集成前逐个审查。
- worker 组只读复核 BUG01/04/05，未发现阻塞性生产问题；补齐其他用户邮件与 bearer 隔离、distributor 角色场景后，凭据目标 2 个顶层/9 个子场景 PASS（11.400s）。该组再次只读核对 `8a28ad32`，确认所指出的测试缺口已关闭。
- durability 组只读复核 BUG03/06，确认 Shutdown 有界等待、失败传播、邮件事务与上限，未发现本次新增阻塞。
- 前端独立审查发现关闭详情后成功操作仍应刷新列表，保留不重开详情断言并补充列表/缓存同步的 Red→Green；二次复核发现旧订单读取不能连同独立套餐初始化一起丢弃，补充真实异步顺序的失败用例并最小修正。最终相关 62 项与浏览器 12 项 PASS，独立审查确认所指出的 P2 均已关闭，没有发现本次限定差异的新阻塞。
- 收尾独立只读审查对照全部分组报告与本报告，确认通过数量、版本范围、失败记录、NOT RUN 和授权边界一致；没有把本地结果提升为线上验收。

## 重要操作取舍与未运行项

1. **保数据优先于自动恢复可用性。** 候选可能写库后失败，将停止候选并保留最新数据，服务可能需要人工恢复。显式旧快照 rollback 仍可能丢弃快照后写入，必须单独授权和对账，详见 [恢复约定](../operations/lifecycle-recovery.md)。
2. 无法追回已经投递的旧邮件；本次确保凭据兑换失效。失联第三次邮件尝试标为“结果未知”，不声称远端 SMTP 未接收。
3. BUG03 验证普通 HTTP handler 的有界排空；非信号监听故障仍保留原有 WebSocket/telemetry 退出约定，不声称该异常路径也已完成整进程优雅退出。
4. BUG05 修复 `requestIP` 消费者的配额；未把礼品卡审计和工单位置的独立 IP 逻辑纳入全项目统一改造。
5. NOT RUN：真实 SMTP、生产数据、实际容器切换/CI 发布、远端恢复、断电/磁盘满演练、真实用户业务验收。浏览器证据使用本地真实构建与合成 API 边界，不能替代线上验收。
6. Go 原生 coverage 是语句口径，不提供本轮行/分支指标；分组和最终覆盖率按各自实际范围披露，不以局部数字冒充全仓指标。

## 本地提交与交付边界

| 本地集成提交 | 内容 |
| --- | --- |
| `d2c56506b76b1aad6d03e0c2edf8dcb5014e6833` | 先行 Bug List 和逐项方案/测试矩阵 |
| `8a28ad326f73c4207bc115d583e4bfe58f2b2723` | BUG01/04/05 |
| `a9423fbff556490674f43d959fc01239833938a3` | BUG03/06 |
| `af691d3775ed9cb98831e77c2ef1df415504f857` | BUG02/07/08 |
| `fd2faf6d4e593e3fc0a8731c7529021b20295c0e` | 现行恢复文档同步 |
| `65857535149a2f2c0077db10c6644d6f3db8267b` | 恢复验证记录的复现细节 |
| `e79c62a3c281027ea7f35b287682a5cfc875646c` | BUG09/10/11，包含两次订单独立审查补强及最终验证记录 |

全部提交均仅本地保留，精确实现 SHA 同步绑定到缺陷台账。推送、PR、部署仍全部未授权且未执行；等待用户部署授权后再核对目标环境、版本和对应 CI/CD 步骤。
