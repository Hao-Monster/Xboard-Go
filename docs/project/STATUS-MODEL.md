# 项目状态模型

禁止使用单一“完成百分比”。每个需求必须分别记录五个维度。

| 维度 | 值 | 含义 |
| --- | --- | --- |
| `scope_status` | `decided` / `blocked` | 业务行为已确定，或仍依赖未解决决策 |
| `implementation_status` | `implemented` / `partial` / `blocked` / `not_started` | 代码实现状态 |
| `verification_status` | `current` / `historical` / `partial` / `none` | 当前验证目标提交证据、旧证据、部分证据或无证据 |
| `migration_status` | `current` / `historical` / `partial` / `not_assessed` / `not_applicable` | 数据/协议迁移证据状态 |
| `acceptance_status` | `accepted` / `pending` / `rejected` | 是否达到正式验收口径 |

## 验收不变量

`accepted` 必须同时满足：

1. 范围已决定；
2. 实现完整；
3. 验证状态为 `current`，且每条证据都指向 `requirements.json` 的 `baseline_commit`，包含稳定证据 ID、层级、环境、用例 ID、可核对产物、RFC3339 时间和命令，并实际通过；
4. 迁移状态为 `current` 或 `not_applicable`；
5. 对应工作项全部为 `done`；里程碑发布门禁在逐项验收后独立汇总，不与单项验收形成循环依赖。

历史 CI 通过、旧分支测试、无法定位提交的截图或口头结论不能提升为 `current`。生成状态页只汇总事实源，不自行推断完成度。

任何维度为 `partial` 的需求必须填写 `status_reason`，明确已完成、未完成和解除条件。可复现的本地运行记录写入 `progress_evidence`：它必须指向 `baseline_commit`、使用稳定 ID、RFC3339 时间、命令、结果和验收标准 ID，但不含远端可核对产物，因此只能解释进展，永远不能把需求提升为 `current` 或 `accepted`。

M1 等复杂需求必须在 `requirements.json.acceptance_criteria` 中保存稳定验收标准 ID；正式证据成为 `current` 前必须覆盖全部已登记标准。并行拆分保存在 `work-items.json.tracks`，每条轨道分别记录依赖、状态和完成门禁；父工作项与里程碑状态不得代替轨道状态。

`baseline_commit` 表示本轮证据验证的精确可执行源码提交，不要求等于记录这些证据的元数据提交。证据 PR 只能在该目标提交之后修改 `docs/project/` 治理/证据元数据；校验器要求目标提交真实存在、是当前 `HEAD` 的祖先，并在存在任何 `current` 需求时拒绝目标之后的产品代码、测试或工作流漂移。任何非 `docs/project/` 变化都必须先更新目标提交并重跑受影响证据，不能沿用旧目标的 `current` 状态。

证据层级限于 `unit`、`integration`、`contract`、`browser`、`differential`、`migration`、`security`、`performance`、`manual`。正式环境值为 `github-actions` 或 `bingo-dev`：前者必须引用本仓库 Actions run/job URL；后者必须引用保存在开发测试服务器证据区的脱敏日志 SHA-256（`bingo-dev:sha256:<digest>`）。仅写一条任意 shell 命令、没有稳定用例 ID 或没有可核对产物，不能成为当前证据。
