# Xboard-Go 项目交接文档

> **历史归档（2026-10-15 纳入版本控制）**：以下保留原交接记录，其中分支、PR、工作区和部署状态不是当前事实，也不作为当前操作指令。当前规则以 `AGENTS.md` 为准；状态以实时查询、`docs/project/*.json` 和现行工作流为准。

> 生成时间：2026-09-20 · 交接人：Antigravity（高级开发专家与项目负责人）\
> 本文档为单一入口，所有细节链接到仓库内已有文档，不重复复制内容。

---

## 工作约定（优先于以下所有内容）

| 约定 | 详情 |
|---|---|
| **唯一开发分支** | `codex/ci-001-tiered-gates`，禁止随意新增开发分支 |
| **PR 策略** | 每项需求最多一个面向 `main` 的 PR；合并后保留开发分支复用 |
| **部署铁律** | 只走 CI/CD 流水线（GitHub Actions），禁止本地上传文件或手动替换服务器产物 |
| **开发测试部署** | 使用快速流程（`deploy-dev.yml`），不等待全量回归、容量或业务验收门禁 |
| **责任边界** | 开发者负责针对性检查、构建和部署版本核验；**业务验收由你（负责人）执行** |
| **代码共用** | 开发测试与生产共用应用代码和架构，仅部署流程分开 |
| **生产门禁备份** | `docs/operations/production-gates-backup/` |
| **对照来源** | 原项目（旧 PHP Xboard）是功能和交互的对照来源；复刻时保留主题颜色 |

### 完成状态定义

| 标签 | 含义 |
|---|---|
| **已实现** | 代码已存在于仓库 |
| **已验证** | 列出具体测试、环境和对应提交 SHA |
| **已部署** | 列出流水线运行 ID 和服务器实际镜像摘要 |
| **待你验收** | 开发者验证完成，但尚未获得负责人业务验收 |
| **未完成或已放弃** | 明确原因、影响和后续处理方式 |

---

## 一、项目与架构

### 1.1 定位

Xboard-Go 是旧 PHP Xboard 面板的 Go 语言重写版，目标为功能完整对照并最终切换生产。\
仓库地址：`https://github.com/Hao-Monster/Xboard-Go`（Apache-2.0，`main` 受规则集保护）

### 1.2 技术栈

| 层 | 技术 | 说明 |
|---|---|---|
| **后端** | Go 1.26.8 | 入口 `cmd/xboard`，核心逻辑在 `internal/` |
| **前端** | React 19 + TypeScript + Vite 8 | 目录 `web/`，无 Tailwind，Vanilla CSS |
| **数据库** | SQLite（`modernc.org/sqlite`）| 单机单写，文件路径由 `XBOARD_DATABASE_DSN` 指定 |
| **缓存/协调** | Redis（可选）| 多 WebSocket 副本时需要，默认 `local` 单进程模式 |
| **构建产物** | 单 Docker 镜像 | `Dockerfile` 含前后端；分离部署见 `compose.split.yaml` |
| **包管理器** | Go modules + pnpm 11.10.0 | Node >= 24.0.0 |

### 1.3 主要目录

```
Xboard-Go/
├── cmd/
│   ├── xboard/           # 主程序入口（API 服务器）
│   ├── xboard-lifecycle/ # 数据库迁移 / 升级工具
│   ├── projectctl/       # 治理门禁 CLI
│   └── testdatagen/      # 测试数据生成
├── internal/             # 30 个业务子包
│   ├── httpapi/          # HTTP 路由与处理器
│   ├── store/            # 数据库访问层（SQLite）
│   ├── subscription/     # 订阅协议输出（11 类）
│   ├── nodecoord/        # 节点协调（WebSocket + Redis）
│   ├── payment/          # 支付网关适配器
│   ├── schedule/         # 计划任务
│   ├── legacymigration/  # 旧数据迁移工具
│   └── security/         # 认证、权限、CAPTCHA
├── web/
│   └── src/
│       ├── features/     # 按业务领域拆分的 React 功能模块（22 个）
│       ├── components/   # 通用 UI 组件
│       ├── lib/api.ts    # 前端统一 API 层
│       └── App.tsx       # 路由根组件
├── docs/
│   ├── project/          # 版本化治理事实源
│   └── operations/       # 运维文档与生产门禁备份
├── .github/workflows/    # 5 个 GitHub Actions 流水线
└── AGENTS.md             # Agent 协作规范（权威行为准则）
```

### 1.4 接口与权限关系

- **用户端**：`/api/v1/` 前缀，需登录 Cookie/Session
- **管理员端**：`/<secure_path>/api/v1/` 前缀（`XBOARD_LEGACY_ADMIN_PATH` 配置），需管理员权限
- **节点上报**：WebSocket（或 HTTP 轮询），机器凭据验证
- **分离部署**：网关（Caddy/Nginx）→ 前端容器 + 后端容器，`compose.split.yaml` 定义

---

## 二、当前代码状态（交接时实时查询）

### 2.1 分支 SHA

| 分支 | 最新提交 SHA | 说明 |
|---|---|---|
| `main` | `d23fdda8` | `docs(governance): resolve SQLite production boundary (#256)` |
| `codex/ci-001-tiered-gates` | `244271b8` | Merge remote-tracking branch 'origin/main' |
| 开发分支领先 main | 44 个提交 | 含 #260–#269 所有已合并 PR 的开发历史 |

> 开发分支是持续性复用分支，所有功能通过 Squash Merge 进 main 后，开发分支再同步 main。

### 2.2 工作区状态

```
On branch codex/ci-001-tiered-gates
nothing to commit, working tree clean
```

✅ **工作区干净，无未提交改动。**

### 2.3 开放 PR

**当前无开放 PR。**

### 2.4 最近合并的 PR（前 10 条）

| PR | 标题 | 合并时间 (UTC) |
|---|---|---|
| #269 | fix: prevent node action menus from being clipped | 2026-09-20 12:45 |
| #268 | fix: make administrator menu navigation instant | 2026-09-20 11:40 |
| #267 | fix: keep administrator page on browser refresh | 2026-09-20 11:08 |
| #266 | fix: restore full-width aligned node management table | 2026-09-20 10:57 |
| #265 | feat: align node management page and node dialogs | 2026-09-20 10:43 |
| #264 | fix(ci): prevent backup from consuming deployment script | 2026-09-20 09:52 |
| #263 | feat: align server edit and detail dialogs with legacy | 2026-09-20 09:49 |
| #262 | feat: match legacy new server dialog | 2026-09-20 09:25 |
| #261 | fix: restore server management release build | 2026-09-20 09:12 |
| #260 | feat: align server management and enable fast development deployment | 2026-09-20 09:04 |

### 2.5 流水线状态

最近 10 次流水线（`CI` 和 `Legacy parity`）全部 `success`，最后一次绑定提交 `3fadec94`。

---

## 三、本地开发

### 3.1 环境要求

| 工具 | 版本 |
|---|---|
| Go | 1.26.8 |
| Node.js | >= 24.0.0（本机：v24.12.0）|
| pnpm | 11.10.0 |
| Docker | 需要（本地镜像构建和集成测试）|

### 3.2 首次启动

```bash
# 1. 复制并填写配置
cp .env.example .env
# 必填（仅首次）：
#   XBOARD_BOOTSTRAP_ADMIN_EMAIL=<管理员邮箱>
#   XBOARD_BOOTSTRAP_ADMIN_PASSWORD=<管理员密码>

# 2. 安装前端依赖
cd web && pnpm install && cd ..

# 3. 启动后端（监听 :8080）
go run ./cmd/xboard

# 4. 启动前端（另开终端，监听 :5173）
cd web && pnpm dev
```

### 3.3 关键环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `XBOARD_ADDRESS` | `127.0.0.1:8080` | 后端监听地址 |
| `XBOARD_DATABASE_DSN` | `file:./data/xboard.db` | SQLite 文件路径 |
| `XBOARD_PANEL_URL` | `http://127.0.0.1:5173` | 前端面板 URL |
| `XBOARD_LEGACY_ADMIN_PATH` | `admin` | 管理员安全路径 |
| `XBOARD_ALLOWED_ORIGINS` | `http://127.0.0.1:5173` | CORS 允许来源 |
| `XBOARD_WEBSOCKET_ENABLED` | `true` | 是否启用 WebSocket |
| `XBOARD_NODE_COORDINATION_MODE` | `local` | `local`（单进程）或 `redis`（多副本）|
| `XBOARD_BOOTSTRAP_ADMIN_EMAIL` | — | **仅首次启动**填写 |
| `XBOARD_BOOTSTRAP_ADMIN_PASSWORD` | — | **仅首次启动**填写 |

> 完整配置说明见 `.env.example`。**永远不要把真实凭据提交到 Git。**

### 3.4 测试命令

```bash
# 后端：带 race 检测的全量单元测试
go test -race ./...

# 后端：针对某个包（快速反馈）
go test -race ./internal/httpapi/...

# 前端：Vitest 单元测试
cd web && pnpm test

# 前端：类型检查
cd web && pnpm typecheck

# 前端：Lint（零警告）
cd web && pnpm lint

# 治理门禁检查（修改治理数据后必须执行）
go run ./cmd/projectctl check

# 前端 E2E（需要运行中的服务）
cd web && pnpm test:e2e
```

### 3.5 本地 Docker 构建验证

```bash
docker build -t xboard-go:local .
```

### 3.6 数据库迁移

```bash
go run ./cmd/xboard-lifecycle migrate up      # 升级到最新 schema
go run ./cmd/xboard-lifecycle migrate status  # 查看当前版本
```

---

## 四、部署与回滚

### 4.1 开发测试服务器

| 项目 | 值 |
|---|---|
| **服务器昵称** | `bingo-dev` / 开发测试服务器 |
| **公网域名** | `https://freedom.openal.uk` |
| **健康检查（公网）** | `https://freedom.openal.uk/healthz` |
| **健康检查（本地）** | `http://127.0.0.1:18082/healthz` |
| **容器名** | `xboard-go-freedom-app-1` |
| **应用目录** | `/home/bingo/apps/xboard-go-freedom` |
| **SSH 用户** | `bingo`（凭据通过安全渠道交接）|

### 4.2 CI/CD 流水线触发关系

```
push 到 main
  └── ci.yml（当前：打印触发日志）
  └── legacy-parity.yml（自动触发：对照旧 Xboard 差异测试）

手动 workflow_dispatch
  └── deploy-dev.yml（指定 commit SHA → 构建镜像 → bingo-dev → 健康检查 → 公网冒烟）
      ├── 健康检查失败 → 自动回滚到上一镜像
      └── 成功 → freedom.openal.uk 可访问
```

**注意**：当前 `ci.yml` 简化版仅打印日志，完整生产门禁备份位于 `docs/operations/production-gates-backup/`。

### 4.3 部署步骤（开发测试）

1. 打开 GitHub Actions → **Deploy development server**
2. 点击 **Run workflow**，可选填入精确 40 位 SHA（留空则部署当前触发时的 HEAD）
3. 等待约 2–5 分钟，确认 `Verify public endpoint` 步骤绿色通过

### 4.4 版本核验

```bash
# 公网健康探针（200 = 正常）
curl -sf https://freedom.openal.uk/healthz

# 在 bingo-dev 服务器上确认具体 SHA（只读巡检）
docker inspect xboard-go-freedom-app-1 --format '{{.Config.Image}}'
# 期望：xboard-go:<40-char-sha>

docker inspect xboard-go-freedom-app-1 --format '{{.State.Health.Status}}'
# 期望：healthy
```

**当前已部署版本**：`3fadec94`（PR #269 合并后，CI 和 legacy-parity 全绿）

### 4.5 日志查看

```bash
# 容器日志（bingo-dev，只读巡检）
docker logs xboard-go-freedom-app-1 --tail 100 -f
```

### 4.6 回滚步骤

**自动回滚**：部署脚本在 `docker compose up` 失败时自动将上一镜像重新拉起。

**手动回滚（紧急）**：在 GitHub Actions 重新触发 `deploy-dev.yml`，在 `revision` 输入框填入上一个已知好的 40 位 SHA，流水线自动部署并验证。

> ⚠️ 生产环境回滚操作必须经过授权；本地 SSH 仅用于只读巡检、日志查看与故障排查。

---

## 五、需求与剩余工作

> 权威数据源：`docs/project/`（JSON 文件），由 `go run ./cmd/projectctl generate` 自动渲染 `STATUS.md`。**严禁手工编辑 STATUS.md。**

### 5.1 里程碑概览

| Milestone | 状态 | 说明 |
|---|---|---|
| **M0 — 项目治理基线** | ✅ complete | 治理门禁、分支保护、供应链安全 |
| **M1 — 功能对照** | 🔄 in_progress | 核心功能已实现，1 项门禁待验证 |
| **M2 — 迁移与运维** | 🚫 blocked | OPS-002（备份演练）、MIG-001（旧数据迁移）待完成 |
| **M3 — 候选发布** | 🚫 blocked | 80 项需求均待你业务验收（acceptance: pending）|
| **M4 — 生产就绪** | 🚫 blocked | REL-001 被 D-016/D-018 阻塞 |

### 5.2 需求状态摘要（基线 2026-09-08）

| 维度 | 状态 | 数量 |
|---|---|---|
| 范围 | decided | 80 |
| 实现 | implemented | 77 |
| 实现 | partial | 3 |
| 验证 | partial | 79 |
| **验收** | **pending（待你验收）** | **80** |
| **验收** | **accepted** | **0** |

**关键结论**：77/80 项已实现，0/80 项获得业务验收。当前阶段核心任务是推进 M3 业务验收。

### 5.3 部分实现的需求（3 项）

| ID | 说明 | 阻塞原因 |
|---|---|---|
| `USER-001` | 用户停用与恢复 | D-012 已决议，执行中 |
| `FIN-001` | 原子佣金提现账本 | D-011 已决议，执行中 |
| `OPS-002` | 备份、异地副本、恢复演练 | D-012/D-013 已决议，执行中 |

### 5.4 进行中的工作项

| ID | 标题 | Issue |
|---|---|---|
| `OPS-002` | 完成备份、异地副本和恢复演练 | #117 |
| `MIG-001` | 完成代表性旧数据迁移、对账和回滚证据 | #118 |
| `PERF-001` | 建立代表性容量基线并确认 SLO | #130 |

### 5.5 阻塞工作项

| ID | 标题 | Issue | 阻塞决策 |
|---|---|---|---|
| `REL-001` | 完成生产切换、回滚和事故响应运行手册 | #131 | D-016（切换策略）、D-018（MFA 策略）|

### 5.6 待解决决策（6 项 pending）

| ID | 标题 | 里程碑 | 阻塞工作项 |
|---|---|---|---|
| `D-007` | 浏览器支持范围与可访问性等级 | M3 | — |
| `D-009` | V1 节点全局 token 退役窗口 | M2 | — |
| `D-014` | 支付、邮件、Telegram 与存储沙箱 | M3 | — |
| `D-016` | 新旧系统并行与切换策略 | M4 | REL-001 |
| `D-017` | 视觉兼容程度 | M3 | — |
| `D-018` | 管理员和员工 MFA 策略 | M4 | REL-001 |

### 5.7 M3 业务验收入口（待你执行）

| 工作项 | Issue | 覆盖需求 |
|---|---|---|
| `VER-001` | #119 | 身份（AUTH-001~009）、用户、套餐、订阅 |
| `VER-002` | #120 | 订单、支付、营销、分销 |
| `VER-003` | #121 | 机器、节点、实时上报、计划 |
| `VER-004` | #122 | 内容、附件、客户端、支持 |
| `VER-005` | #123 | 配置、扩展、主题、运维 |

### 5.8 开放风险摘要（高优先级）

| ID | 严重度 | 标题 |
|---|---|---|
| R-005 | Critical | 支付/结算/余额/佣金/续费发生重复或丢失 |
| R-010 | High | 双系统并行期间数据漂移或双写不一致 |
| R-022 | High | 无正式发布、回滚和事故响应门禁导致不可控切换 |
| R-024 | High | 前端/后端/网关独立发布造成契约或版本漂移 |

完整风险列表：`docs/project/risks.json`

---

## 六、凭据与权限

> ⚠️ **凭据通过安全渠道交接，不写入本文档或 Git。**

### 6.1 权限清单

| 服务 | 所需权限 | 交接方式 |
|---|---|---|
| GitHub `Hao-Monster/Xboard-Go` | Admin（分支保护、Actions Secrets、Environments）| 仓库 Settings → 添加协作者或转移所有权 |
| GitHub Actions Secrets | `BINGO_DEV_SSH_KEY`（部署 SSH 私钥）| Actions → Secrets → environment `development` |
| bingo-dev 服务器 | `bingo` 用户 SSH（Tailscale 内网）| 安全渠道传递私钥或注册公钥 |
| 域名 `freedom.openal.uk` | DNS 管理权 | 域名注册商控制台 |
| Tailscale | bingo-dev 节点访问 | Tailscale Admin Console |

### 6.2 Actions Environments

- **`development`**：部署到 bingo-dev，包含 `BINGO_DEV_SSH_KEY`
- **生产环境**：尚未建立（M4 阶段创建，参见 REL-001）

---

## 七、快速操作速查

### 开始一个新功能

```bash
# 1. 切换到开发分支并确认干净
git checkout codex/ci-001-tiered-gates
git status

# 2. 同步最新 main
git fetch origin && git merge origin/main

# 3. 开发并本地测试
go test -race ./...           # 后端
cd web && pnpm test           # 前端
go run ./cmd/projectctl check # 治理门禁

# 4. 推送并在 GitHub 发起 PR（目标 main）
git push origin codex/ci-001-tiered-gates
# PR 正文填写治理元数据块，格式见 AGENTS.md
```

### 触发开发测试部署

1. 打开 GitHub Actions → **Deploy development server** → **Run workflow**
2. 可选填入精确 40 位 SHA（留空部署当前 HEAD）
3. 约 2–5 分钟后确认 `Verify public endpoint` 步骤绿色

### 确认部署版本

```bash
# 公网健康探针
curl -sf https://freedom.openal.uk/healthz

# 服务器上确认精确 SHA（只读）
docker inspect xboard-go-freedom-app-1 --format '{{.Config.Image}}'
```

### 回滚（紧急）

在 GitHub Actions 重触 `deploy-dev.yml`，`revision` 填入上一个已知好的 SHA，流水线自动部署并验证。

---

## 八、参考文档链接

| 文档 | 路径 |
|---|---|
| Agent 协作规范（权威规则）| `AGENTS.md` |
| 多 Agent CI/CD 实践指南 | `MULTI-AGENT-CI-CD-GUIDE.md` |
| 贡献者指南 | `CONTRIBUTING.md` |
| 项目状态（自动生成）| `docs/project/STATUS.md` |
| 需求清单（80 项）| `docs/project/requirements.json` |
| 工作项清单 | `docs/project/work-items.json` |
| 决策记录 | `docs/project/decisions.json` |
| 风险登记 | `docs/project/risks.json` |
| 兼容性例外 | `docs/project/compatibility-exceptions.json` |
| 发布门禁 | `docs/project/release-gates.json` |
| 归档策略 | `docs/project/ARCHIVE-POLICY.md` |
| 生产观测策略 | `docs/project/production-observation-policy.md` |
| 生产门禁备份 | `docs/operations/production-gates-backup/` |
| 管理员导航文档 | `docs/operations/admin-navigation.md` |
| 节点管理对照文档 | `docs/operations/node-management-parity.md` |
| 服务器管理对照文档 | `docs/operations/server-management-parity.md` |
| 环境变量示例 | `.env.example` |
| 部署流水线（开发）| `.github/workflows/deploy-dev.yml` |
| CI 流水线 | `.github/workflows/ci.yml` |
| Legacy Parity 流水线 | `.github/workflows/legacy-parity.yml` |
| PR 模板 | `.github/PULL_REQUEST_TEMPLATE.md` |

---

## 九、交接验收标准

| 能力 | 参考章节 |
|---|---|
| ✅ 独立启动项目 | 第三节"本地开发" |
| ✅ 定位一个功能 | `internal/` 按业务领域拆包，`web/src/features/` 同样边界，需求 ID 在 `requirements.json` |
| ✅ 提交一个 PR | 第七节"快速操作速查"，遵循 `AGENTS.md` PR 模板 |
| ✅ 说明如何部署 | 第四节"部署与回滚" |
| ✅ 确认部署版本 | `docker inspect` + `curl /healthz` |
| ✅ 失败后如何回滚 | 自动回滚 + 手动重触流水线指定旧 SHA |

---

*本文档由 Antigravity 在 2026-09-20 21:03 交接时实时生成，数据均来自当时查询结果，非历史快照。*\
*后续状态变更请通过 `go run ./cmd/projectctl generate` 刷新 `STATUS.md`，并在下一次重大里程碑完成时更新本文档。*
