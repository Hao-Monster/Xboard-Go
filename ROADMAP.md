# Xboard-Go Roadmap

本路线图以可验证退出条件组织，不以“代码大致完成”作为里程碑完成标准。实时状态见 [`docs/project/STATUS.md`](docs/project/STATUS.md)，机器可读事实源见 [`docs/project/`](docs/project/)。

## M0 — Project Governance Baseline

目标：建立版本化、可校验、可追溯的项目控制面。

退出条件：80 个功能需求、18 个决策、风险、兼容性例外、跨领域工作项和发布门禁进入仓库；GitHub 里程碑/Issue 建立；PR 元数据和状态漂移由 CI 检查；M0 合并后的精确 `main` 提交通过全部必需检查。

## M1 — Functional Parity

目标：关闭仍被业务决策阻塞或仅部分实现的功能范围。

退出条件：D-011 与 D-012 决策完成；`FIN-001`、`USER-001`、`USER-003` 有实现、并发/事务/权限测试和当前提交证据；无已知 Critical/High 功能完整性问题。

## M2 — Migration & Operations

目标：证明真实旧数据迁移、备份恢复、保留策略和可运维性。

退出条件：D-013 及相关保留决策完成；`OPS-002` 完整；代表性脱敏数据迁移、校验、回滚、恢复和异地副本演练有当前证据。

## M3 — Release Candidate

目标：把历史实现证据提升为当前候选提交的逐需求验收证据。

退出条件：80/80 需求达到 `accepted`；差分/协议/浏览器/安全/迁移矩阵全部映射到精确提交；性能基线和供应链门禁完成；不存在已知 Critical/High 问题。

## M4 — Production Ready

目标：在独立授权下完成生产准备和受控切换。

退出条件：SLO、容量、监控、告警、运行手册、切换/回滚、事故响应和责任人已确认；生产部署必须另行明确授权。
