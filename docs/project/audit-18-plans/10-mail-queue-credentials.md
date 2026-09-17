# AUD18-10 邮件队列、取消通知和凭据清理

状态：方案已细化；不表示实现或验收完成。

## 关联与入口

- 工作项：FUNC-002；Issue：#115；里程碑：M1。
- 调查入口：`internal/mailer/worker.go`。入口来自台账，执行前核对实际文件及调用链。
- 当前发现：Mail worker and lifecycle ownership/privacy tests changed in PR #211.

## 执行与验证步骤

1. 追踪入队、领取、发送前重检和清理链路，梳理用户状态变化后的旧消息。
2. 使用沙箱收件器模拟禁用及匿名化与发送并发、失败重试和取消通知。
3. 确认凭据已撤销、邮件不泄露旧信息且日志脱敏；不发送真实邮件。

## 验收条件

Prove disabled/anonymized users cannot receive stale queued notifications or retain credentials; sandbox sends only.

## 证据与交付

记录候选完整 SHA、实际修改文件、测试命令、退出码、通过/失败/跳过数量、环境及日志位置。本地修改未提交时记录 dirty diff，不能声称绑定纯提交证据。具体测试命令由入口和现有 CI 确认后填入执行记录；尚未执行的检查为 NOT RUN。每次更新追加台账 history，分别维护实现、验证、迁移、验收状态。

## 依赖、风险与回滚

依赖 AUD18-01 候选及证据口径；最终结果由 AUD18-18 汇总。 保留用户现有修改，使用独立任务提交作为代码回滚点。数据库与文件验证使用隔离副本。共享环境部署、推送及破坏性操作须明确授权；未授权不降低该项验收标准，记录为未执行。
