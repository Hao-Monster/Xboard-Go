# AUD18-06 备份创建与备份完整性验证

状态：方案已细化；不表示实现或验收完成。

## 关联与入口

- 工作项：OPS-002；Issue：#117；里程碑：M2。
- 调查入口：`cmd/xboard/backup_cli_test.go`。入口来自台账，执行前核对实际文件及调用链。
- 当前发现：Backup and HTTP replica implementation/tests are in PR #211; no fresh recovery drill was run in this audit.

## 执行与验证步骤

1. 阅读备份 CLI 与已有测试，明确数据库、附件、配置的备份范围及敏感数据处理。
2. 在临时数据库和目录生成备份，校验完整性、摘要、缺文件及篡改拒绝。
3. 恢复到另一隔离目录，核对记录数量、关键业务状态、附件和恢复耗时；不覆盖源库。

## 验收条件

Create isolated backup, verify hashes and integrity, restore database/files, measure recovery and validate business state.

## 证据与交付

记录候选完整 SHA、实际修改文件、测试命令、退出码、通过/失败/跳过数量、环境及日志位置。本地修改未提交时记录 dirty diff，不能声称绑定纯提交证据。具体测试命令由入口和现有 CI 确认后填入执行记录；尚未执行的检查为 NOT RUN。每次更新追加台账 history，分别维护实现、验证、迁移、验收状态。

## 依赖、风险与回滚

依赖 AUD18-01 候选及证据口径；最终结果由 AUD18-18 汇总。 保留用户现有修改，使用独立任务提交作为代码回滚点。数据库与文件验证使用隔离副本。共享环境部署、推送及破坏性操作须明确授权；未授权不降低该项验收标准，记录为未执行。
