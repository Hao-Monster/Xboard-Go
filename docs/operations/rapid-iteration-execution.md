# 快速迭代阶段：分支保护与 CI/CD 工程执行单

更新日期：2026-10-03（America/Los_Angeles）。状态：方案已获用户接受；本文件将快速迭代要求纳入执行基线，配置与云端验收尚未完成。

## 1. 目标、范围与事实源

目标：频繁开发、短反馈、频繁流水线预览部署；同时保证 main 只能接收经验证的变更，部署版本可追踪、失败可恢复。

- 保留 main 和 codex/ci-001-tiered-gates 两个长期分支；机器人依赖分支为临时分支。
- 不新增长期开发分支，不通过强推整理历史，不关闭 Dependabot 安全更新。
- 保留现有 main → CI → Legacy parity 开发预览发布链路；本阶段不另建每个 PR 一套服务器或数据库。
- PR 检查与部署分离。普通改动不等待全量回归、race、浏览器、CodeQL、容量或业务验收；高风险变更不因赶进度免除对应验证。
- 本文件是执行步骤与证据索引，不替代 docs/project/*.json 的治理事实源。下表 ITER-* 仅为本执行单的步骤编号，不可直接填入 PR Work item IDs。
- 历史 CI-001 / #127、DEP-001 / #129 已完成，不重开或声称覆盖此次新实施。正式 CI 改造前执行 T01，登记真实 Work Item、Issue 和 Milestone。
- 已接受方案允许继续工程准备；远端规则、安全配置、推送、合并和发布按 AGENTS.md 的明确授权边界执行。只记录授权范围，不把“接受建议”扩展为正式生产切换授权。

## 2. 已核验基线与保护对象

2026-10-03 实时读取的基线（执行前必须重新读取）：

| 对象 | 当前状态 | 目标 |
| --- | --- | --- |
| Protect main（Ruleset 21237842） | 禁删、禁强推、要求 PR、解决讨论、无 bypass；0 个批准；无必需检查 | 保留原保护，增量增加可靠的 pr-gate |
| 开发分支 | 无对应保护规则 | 禁删、禁强推；允许普通推送和 merge 同步 |
| Actions 默认权限 | read；不允许 Actions 批准 PR | 保持 |
| CI | 仅 main push，发布请求入口 | 保留职责和名称，避免误触 workflow_run |
| Legacy parity | 受信 main 成功 CI 后发布；development；环境串行，不取消执行中部署 | 保留并核验精确 SHA、失败回滚 |
| Production checks / CodeQL | 手动触发 | 本阶段不恢复为每次预览必跑的全量门禁 |
| development / production-internal-test | 无 Environment 来源限制或审批规则 | 核对实际 ref、用途和依赖后收紧 |
| AGENTS.md | 已本地提交 280a69c2，尚未推送 | 与本执行单作为同一规则交付范围 |

受保护的用户工作：docs/HANDOVER.md 是已有未跟踪文件，不纳入本任务。已有 worktree 不删除、不重置。远端、Ruleset 和 Environment 变更前保存脱敏配置快照；不保存 Secrets 值。

## 3. 三条路径与反馈预算

| 路径 | 触发与用途 | 必须做什么 | 不等待什么 |
| --- | --- | --- | --- |
| PR 快速检查 | pull_request 指向 main；更新代码时重跑 | 全局轻量检查 + 按范围的测试/静态检查/必要构建 + pr-gate | 与变更无关的回归、容量及完整生产验收 |
| 开发预览 | 经授权合并 main 后自动运行 | 固定 SHA 构建、串行发布、版本/健康核验、失败报告与既有回滚 | 重型扫描和用户业务验收 |
| 深度验证 | 高风险变更的相关检查或明确手动触发 | 相关权限/事务/迁移/并发/安全/浏览器验证 | 不把整套生产检查强加到每个文案 PR |

性能预算是待测目标，不是当前承诺：热缓存纯文档 PR ≤2 分钟，常规代码 PR ≤8 分钟；预览先测最近可用成功运行的排队、构建、传输、切换时间，再制定基线目标。冷缓存、依赖升级、高风险测试单独统计。超预算先分析，不删除验证或放宽断言。

频繁迭代策略：

1. PR 工作流按 PR 编号设置 concurrency，新提交取消旧检查；不得因此显示旧提交已验收。
2. 检查按 Go/前端/治理/工作流分流，独立任务并行；Go、pnpm 缓存键包含操作系统、工具链和锁文件，不跨信任边界复用可执行产物。
3. 不在 PR 检查和预览部署中各跑一遍同样的全量测试。预览必须验证最终合并 SHA；Squash 前的产物不能冒充 main 产物。
4. 共享预览使用每环境互斥，禁止取消已进入迁移/切换的部署。GitHub concurrency 不作为严格 FIFO 队列；排队版本被新版本替代时明确标 superseded，不标 deployed。
5. 防止旧构建晚完成后覆盖新版本。部署切换前验证候选版本资格；手动回滚必须显式指定版本和原因，不能被“只允许最新版本”的优化误拦。
6. 不逐次申请预览部署审批；在已明确授权的开发环境交付范围内连续执行。正式生产审批单独保留。
7. 不开启新的定时任务或通知轰炸。未来若增加定时深度扫描，另行确认频率、运行成本及失败负责人。

## 4. PR 检查设计与分支规则

### 4.1 检查合同

- 新增独立 PR 工作流，不把现有 CI 改成同时处理 PR 的发布入口。
- 工作流在所有面向 main 的 PR 上触发，不用顶层 paths 跳过；任务内部按范围执行。
- 处理 opened、synchronize、reopened、ready_for_review；元数据更新需要支持 edited。Milestone 变化应明确重跑治理检查，不能假设一定产生代码事件。
- 最终必需检查固定命名 pr-gate，使用 always 汇总。适用任务必须 success；failure、cancelled、未知结果及意外 skipped 均拒绝；只有经分类器明确判为不适用的任务允许 skipped。
- 汇总同时验证分类结果有效；分类器失败或未识别路径不得降级为纯文档成功。未知路径走保守检查并显式说明。
- 编译和测试验证 PR 与目标 main 的合并结果；保存 head/base/test merge SHA，失败日志绑定本次运行。
- 普通业务 PR 执行项目治理元数据校验。Dependabot 默认正文不满足本项目模板：在合并前由负责人补真实元数据和 Milestone，不关闭治理检查、不捏造关联 Issue，不使用高权限机器人运行 PR 代码来修改正文。
- 不直接使用旧 detect-changes.mjs 的 fullRegressionResult 作为快速门禁策略；先增加分类合同测试，再复用其中适用的路径判断，保留手动生产流程行为。

### 4.2 修改范围矩阵

| 输入 | 必须检查 | 升级条件 |
| --- | --- | --- |
| 文档/Agent 规则 | diff 格式、治理及引用核对 | 工作流配置或业务配置不能归为纯文档 |
| 前端 | typecheck、lint、相关测试、构建 | 公共入口/依赖扩大测试；关键交互增加浏览器验证 |
| Go | 相关测试、vet、构建 | 共享包/依赖扩大回归，不能仅测修改包而遗漏调用方 |
| Go/前端依赖 | 对应生态完整适用测试、构建及升级说明审查 | 漏洞或兼容问题阻止合并 |
| CI/部署脚本 | 语法、触发范围、权限、固定 Action SHA、脚本测试 | 修改信任边界增加负面授权测试 |
| 权限/金额/数据库迁移 | 对应集成、失败、越权、事务/幂等或回滚测试 | 必要检查完成前不合并，不使用“快速”豁免 |

### 4.3 Ruleset 目标

- main：保留现有规则与无 bypass，增加来源为实际 GitHub Actions 的 pr-gate，要求与 main 同步；只允许 squash 合并。
- 单维护者阶段保留 0 个必需批准，不虚构独立审查人；有实际独立维护者后再配置 1 人批准、旧批准失效和 CODEOWNERS。
- 开发分支：独立规则只禁删与强推，不要求 PR、不要求线性历史。普通 merge 同步 main 仍可进行。
- 不开启全局合并后删源分支；Dependabot 分支按明确清单清理。
- 先让检查真实产生并验证，再绑定 required check。检查名、来源 App 和实际规则必须一致。

## 5. 可执行任务台账

状态只能使用 TODO / IN_PROGRESS / BLOCKED / VERIFIED。VERIFIED 必须有精确提交或远端配置、执行日志和验收结果；文档写完不代表实施完成。

| 步骤 | 工作内容 | 前置 | 完成条件 | 状态 |
| --- | --- | --- | --- | --- |
| ITER-T00 | 编写执行单，AGENTS 增加入口 | 无 | 文档审查、diff check、projectctl check、本地提交 | VERIFIED |
| ITER-T01 | 建立本次治理记录与真实 Issue/Milestone；核对授权范围 | T00 | 新 Work Item 与 Issue 一致，generate/check 通过；记录远端操作授权 | VERIFIED |
| ITER-T02 | 保存规则、合并设置、Environment 与工作流基线，确认当前无冲突发布 | T01 | 脱敏快照、回滚命令设计、两个保留分支 SHA | TODO |
| ITER-T03 | 实现快速范围分类及合同测试 | T01 | 文档/Go/前端/依赖/工作流/高风险/未知路径用例通过，旧手动流程不回归 | VERIFIED |
| ITER-T04 | 实现 PR jobs 和 pr-gate、缓存与取消策略 | T03 | 本地测试与工作流语法检查通过；无 PR Secrets 或发布权限 | VERIFIED |
| ITER-T05 | 云端试运行，观察成功与失败情形 | T02,T04 | 第 6 节必需场景有真实证据；尚不启用强制规则 | TODO |
| ITER-T06 | 增量启用 main 检查及开发分支保护 | T05 | 合格 PR 可合并，失败被阻止；唯一开发分支可正常推送 | TODO |
| ITER-T07 | 核对 Environment 来源及部署链路 | T02 | development 合法链路可用，非受信来源被拒；内部测试环境用途明确 | TODO |
| ITER-T08 | 验证连续预览发布和回滚，记录时间基线 | T06,T07 | 新旧版本不会倒灌、版本健康核验正确、失败可恢复 | TODO |
| ITER-T09 | 逐项处理现有依赖 PR | T06 | 每个 PR 有实际验证与合并/延期/关闭理由，仅清理已处理分支 | TODO |
| ITER-T10 | 更新治理状态、运维说明与交接证据 | T08,T09 | 遗留项真实列出，不把延期或阻塞标完成 | TODO |

负责者：当前主执行 Agent；云端配置与合并由获得用户对应授权的执行者负责。后续 Agent 从第一个非 VERIFIED 步骤继续，先刷新事实，不重新创建重复 Issue/PR。

## 6. 验收矩阵

| 场景 | 预期 | 证据要求 |
| --- | --- | --- |
| 纯文档 PR | 快速通过，不启动应用全量测试 | 分类输出、job 结果、耗时 |
| 正常 Go / 前端改动 | 对应检查通过后可合并 | head/base SHA、构建和测试日志 |
| 测试失败/取消/分类器失败 | pr-gate 失败，main 拒绝合并 | 本地负面测试 + 云端代表性失败运行 |
| 不适用任务正常跳过 | 汇总通过；适用任务跳过则失败 | 合同测试和云端任务图 |
| main 在检查后更新 | 旧合并结果不能直接放行 | 分支规则与重新运行证据 |
| Dependabot PR | 不接触 Secrets，元数据完整，按生态检查 | PR、检查、Milestone |
| 连续推送同一 PR | 旧检查取消，新提交有独立完整结果 | 两次运行 SHA 与状态 |
| 连续预览版本 A/B | 共享环境不并发切换，最终版本可解释，不发生旧版本倒灌 | 发布日志、运行 SHA、排队/替代状态 |
| 非受信 PR/失败上游 | 不能触发有凭据的发布 | 授权逻辑测试；不在生产故意试错 |
| 环境限制修改 | 合法预览发布仍可用，错误来源被拒 | 实际 ref、Environment 规则和运行结果 |
| 发布启动失败 | 准确报告失败，按既有机制恢复；不标验收成功 | 隔离测试或已授权开发演练证据 |
| 分支清理 | main/开发分支及其 SHA 保留 | 前后 refs、PR 状态、必要 bundle 校验 |

云端负面验证优先复用现有任务 PR 的可恢复测试提交或隔离测试工具；不得绕过唯一开发分支约定创建测试分支，不向生产注入故障。测试后恢复正常提交并重新通过检查。

## 7. 回滚与停止条件

- 必需检查名称错误导致锁仓：恢复本次修改前的 required-check 配置，保留原有 PR、禁删、禁强推规则；修复检查后再启用。不清空整个 Ruleset。
- PR 工作流错误：通过普通修复提交恢复；不强推或关闭全部检查伪造成功。
- Environment 限制误拦：仅恢复本次分支策略/审批差异，不扩散 Secrets 权限，不影响其他环境。
- 预览失败：使用现有 CI/CD 的备份与回滚流程；禁止本地 SSH 手工切换。生产切换不是本执行单的默认范围。
- 发现未知并发提交、无法确定环境用途、不可恢复数据风险或缺失关键验证：暂停受影响步骤，记录 BLOCKED 与依赖；其他独立任务继续。
- 仅配置更新不宣称云端验收通过；只有观察到实际拦截、放行及部署证据后才将相关步骤设 VERIFIED。

## 8. 证据记录与进度

每个执行步骤更新本表，不另建相互竞争的任务清单。正式需求/工作项状态仍更新 docs/project/*.json。

| 步骤 | 提交/配置标识 | 命令或运行链接 | 结果与限制 |
| --- | --- | --- | --- |
| T00 | 883fee99c2e503d7c0afa7802111926769b9709c（docs: record rapid iteration delivery execution plan） | git diff --check；go run ./cmd/projectctl check | PASS（退出码 0）；规则冲突与引用人工复核通过；未修改云端规则和工作流 |
| T01 | Work Item `CI-002`（`docs/project/work-items.json` / `docs/project/STATUS.md`），绑定 GitHub Issue #341（Milestone `M3 Release Candidate`） | go run ./cmd/projectctl generate；go run ./cmd/projectctl check；git diff --check | PASS（退出码 0）；已登记独立工作项 `CI-002` 与真实 Issue #341；当前授权范围为本地实现、验证与本地提交（远端 Ruleset/Environment 变更、推送最新提交、创建/合并 PR 待单独明确授权） |
| T03 | 685412fe27392e9f14ffbe3373ef608f0284c2f9、fbcc1b1615c89e2bc8ef46ef93d2ba6ec90fc939、ce46a242f7d88d030e9b3b70d692cc1ab2cba1fc、b3deb666706263eb41bd0af6e12b55f99fb5cfef、8fd49fefdbd1c3e712879237c9a0f8d0e7011300、3ef61ddba167057b2a0611de5465d4ba1cf7797a（`.github/scripts/detect-changes.mjs`、`.github/scripts/resolve-affected-go-pkgs.mjs`、`.github/scripts/go-test-groups.mjs` 及对应测试） | node --test .github/scripts/*.test.mjs | PASS（48/48 用例通过）；覆盖纯文档、Go 反向依赖图、未映射 Go 路径 fail-closed、`--check-geoip` CLI 探针、E2E 常规/打包环境分类与零通过拦截、前端、依赖、CI/部署脚本、`deploy/compose.yaml` 打包冒烟、`.docker/` 与 `Dockerfile` 拆分运行时、迁移演练及未知路径保守升级，且保留手动生产门禁 `fullRegressionResult` 兼容 |
| T04 | 685412fe27392e9f14ffbe3373ef608f0284c2f9、fbcc1b1615c89e2bc8ef46ef93d2ba6ec90fc939、ce46a242f7d88d030e9b3b70d692cc1ab2cba1fc、b3deb666706263eb41bd0af6e12b55f99fb5cfef、c63aab689b5827387d03e0f8c48b461f8d8124c4、8fd49fefdbd1c3e712879237c9a0f8d0e7011300、3ef61ddba167057b2a0611de5465d4ba1cf7797a（`.github/workflows/pr-gate.yml`） | go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 -shellcheck= .github/workflows/pr-gate.yml；node --test .github/scripts/*.test.mjs | PASS（退出码 0）；实现 L0-L3 分级 PR 任务、提交范围 `git diff --check`、`go-fast` GeoIP 测试固件按需拉取、`browser-smoke` 增量 E2E 用例执行（含 Mailpit/打包环境按需编排与全跳过拒绝）、`split-runtime`（验证 gateway/frontend/backend 拆分镜像与路由）、`packaging-smoke`、`migration-drill`、同 PR 并发取消及 fail-closed `pr-gate` 汇总；工作流为 `contents: read` 且不接触发布凭据；云端试运行（T05）与强制规则绑定（T06）保持 TODO |

执行完成时记录：精确 SHA、运行 URL、Ruleset ID、实际检查名、环境策略、预览运行版本、耗时、回滚证据及未完成项。不得记录敏感凭据或原始生产数据。
