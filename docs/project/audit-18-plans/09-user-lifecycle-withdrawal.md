# AUD18-09 用户生命周期和提现台账

状态：方案已细化；不表示实现或验收完成。

## 关联与入口

- 工作项：FUNC-001, FUNC-002；Issue：#114, #115；里程碑：M1。
- 调查入口：`internal/store/user_lifecycle.go`。入口来自台账，执行前核对实际文件及调用链。
- 当前发现：FUNC-001/#114 and FUNC-002/#115 are closed remotely but in_progress in local work-items.

## 执行与验证步骤

1. 先读取 D-011/D-012 的已确认业务语义，逐项对应生命周期和提现账本实现。
2. 使用真实临时数据库验证重复请求、并发提现、余额约束与事务失败。
3. 核对禁用、删除、恢复、匿名化后的凭据撤销和资金历史，不发明新的金额规则。

## 验收条件

Verify agreed D-011/D-012 semantics, concurrent/idempotent money changes, credential revocation, restore and anonymization.

## 证据与交付

记录候选完整 SHA、实际修改文件、测试命令、退出码、通过/失败/跳过数量、环境及日志位置。本地修改未提交时记录 dirty diff，不能声称绑定纯提交证据。具体测试命令由入口和现有 CI 确认后填入执行记录；尚未执行的检查为 NOT RUN。每次更新追加台账 history，分别维护实现、验证、迁移、验收状态。

## 依赖、风险与回滚

依赖 AUD18-01 候选及证据口径；最终结果由 AUD18-18 汇总。 保留用户现有修改，使用独立任务提交作为代码回滚点。数据库与文件验证使用隔离副本。共享环境部署、推送及破坏性操作须明确授权；未授权不降低该项验收标准，记录为未执行。
