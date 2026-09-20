# Xboard-Go 项目协作规则 (AGENTS.md)

本规范适用于所有参与本项目开发的 AI Agent（如 Antigravity、Codex、Claude 等）与人类工程师，作为仓库根目录的权威行为准则。

---

## 一、项目事实源与治理体系

- `docs/project/*.json` 是项目范围、需求（80 项功能清单）、决策、风险、工作项与发布门禁的**唯一版本化事实源**。
- `docs/project/STATUS.md` 由 `go run ./cmd/projectctl generate` 自动渲染生成，**严禁手工编辑**。
- `docs-dev/` 是本地历史取证与临时排查区，保持 Git 忽略；其中内容不代表当前权威需求，任何有效结论必须经过审查后沉淀至 `docs/project/`。
- **任务前导**：开始任何功能开发、缺陷修复、数据迁移或发布前，必须先定位对应的 `Requirement ID` 或 `Work Item ID`，并确认关联的 GitHub Issue 与 Milestone。
- 被决策（Decision）阻塞的需求，严禁实现未经确认的业务语义；允许进行只读调查、测试设计及不受阻塞的工程准备。

---

## 二、多 Agent 协作与工作区隔离

在单机或团队中存在多个 AI Agent 并行协作时，必须遵循物理隔离与范围约束：

1. **工作树物理隔离（Git Worktree）**：
   - 严禁多个 Agent 在同一个本地工作目录（Working Tree）中并发修改不同任务代码。
   - 并行任务优先使用 `git worktree add` 检出独立的工作区目录，避免脏工作区混淆与未暂存覆盖。
2. **任务认领与排他性（Single Task Ownership）**：
   - 同一时刻，同一个 Issue / 模块仅由单个 Agent 认领开发，避免重复劳动与分支冲突。
3. **爆炸半径控制（Blast Radius Control）**：
   - Agent 必须坚守单一职责，**仅修改与当前任务直接关联的文件**。
   - 严禁借开发之名顺带执行跨模块重构、无授权的代码格式化或全工程依赖升级。

---

## 三、分支管理与提交规范

1. **分支保护铁律**：
   - **严禁直接向 `main` 分支提交或推送代码**。所有变更必须通过特性分支发起 Pull Request，经门禁验证后合并。
   - 不绕过分支保护，不手工创建未经门禁验证的 Tag 或 Release。
2. **分支命名规范**：
   - 格式：`<agent-or-role>/<type>/<issue-or-task-id>-<short-description>`
   - 示例：
     - `antigravity/feat/admin-tabs`
     - `codex/fix/subscription-probe`
     - `ci/chore/rebase-baseline`
3. **提交规范（Conventional Commits & 原子提交）**：
   - 采用标准规范提交（`feat:`, `fix:`, `test:`, `refactor:`, `docs:`, `chore:`）。
   - 保持小步原子提交（Atomic Commits）：一个独立功能/修复 + 对应单元测试 = 一个提交。
   - 严禁包含无意义或混杂的批量提交（如 `fix bug`, `update files`）。
4. **主干合并策略**：
   - 推荐使用 **Squash and Merge** 将特性分支上的碎提交压缩为单一高质量提交合并入主干，保持 `main` 分支历史线性干净、易于溯源（Git Bisect）。

---

## 四、PR 规范与质量门禁

1. **PR 强制元数据模版**：
   PR 正文顶部必须包含机器可读的治理元数据块，确实不适用时明确填写 `N/A: 原因`：
   ```markdown
   ## Governance metadata
   Requirement IDs: REQ-001, REQ-002
   Work item IDs: CI-001
   Milestone: M3
   Closes: #123
   ```
2. **GitHub 字段联动**：
   - 发起 PR 时，GitHub 网页端的 **Milestone 字段必须与正文中的 Milestone 一致绑定**（否则将被 `projectctl pr-check` 门禁拦截）。
3. **状态与证据铁律**：
   - 治理状态不得只写“完成”。必须分别更新范围、实现、验证、迁移和验收状态，并附带精确的提交 SHA 与可复现的验证日志。
   - 历史测试、旧分支测试或未绑定精确提交的结果只能标记为 `historical`，严禁提升为 `current` 或 `accepted`。
4. **本地预检（Pre-Flight Checklist）**：
   在推送分支或发起 PR 前，Agent 必须在本地按顺序通过以下检查：
   - 前端：`pnpm --dir web run typecheck && pnpm --dir web run lint && pnpm --dir web test`
   - 后端：`go test -race ./...` 与静态代码检查
   - 治理：`go run ./cmd/projectctl check`（如修改了治理数据需先执行 `go run ./cmd/projectctl generate`）

---

## 五、安全与环境边界

1. **环境与代码执行权限**：
   - 当前开发仅授权在本地开发机与隔离 CI/测试环境中运行；**生产环境部署与直接操作生产数据不在默认授权范围内**。
   - Go 主进程严禁直接调用系统 Shell 执行任意 PHP 脚本或用户上传的动态脚本；受信扩展边界详见 `docs/project/compatibility-exceptions.json`。
2. **机密防泄漏准则**：
   - 严禁将数据库密码、私钥（SSH/RSA/JWT）、生产真实数据、用户邮件正文、原始请求报文或旧系统 PHP 队列载荷写入仓库、Issue 描述、日志或测试快照。
   - 单元测试与 E2E 必须使用 Mock 或加密随机生成的虚拟 Fixture 凭证。
3. **流水线依赖防投毒（Action Pinning）**：
   - 所有 GitHub Actions 步骤的 `uses` 必须锁定为 **40 位不可变 Commit SHA** 或 SHA256 镜像摘要，严禁使用漂移分支（如 `@v4`、`@main`）。

---

## 六、部署与交付规则（硬性铁律）

1. **只允许持续集成（CI）与流水线（CD）部署**：
   - **严禁从本地机器通过 SSH、SCP、SFTP 或任何手工脚本直接向服务器传输代码、静态资源或二进制构建产物**。
   - **严禁在本地手动执行服务端发布、重启或容器切换操作**。
2. **所有环境部署必须由 CI/CD 流水线统一触发**：
   - 代码必须通过 Git 提交并推送至远端，由自动化 CI/CD 流水线（如 GitHub Actions）统一进行代码检出、依赖安装、安全审计、不可变镜像构建与健康探针发布。
   - 任何本地未推送、未通过云端 CI 流水线测试的代码严禁上线。
3. **本地与 SSH 权限边界**：
   - 本地终端仅允许执行代码编写、本地单元测试/构建验证、静态检查以及对服务器的**只读巡检、日志查看与故障排查**。


## 当前交付约定（用户 2026-09-20 确认，优先于上文默认流程）

- 唯一开发分支为 `codex/ci-001-tiered-gates`，禁止新增开发分支；独立实现代理仅使用 detached worktree。
- 每项需求最多一个面向 `main` 的 PR，保留开发分支复用。
- 开发测试与生产共用应用代码、架构和容量考量，仅部署流程分开。
- 开发测试部署不运行或等待自动回归、race、浏览器、CodeQL、容量或业务验收门禁；合入 main 后自动构建发布。业务验收由用户负责。
- Codex 对具体实现做针对性的本地检查；构建、精确版本核验、启动失败报告与回滚属于部署过程。
- 原生产门禁基线保存在 `docs/operations/production-gates-backup/`；生产检查工作流只手动触发，启用正式生产发布前重新核对环境。
