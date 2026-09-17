# AUD18-07 SQLite v60 commission ledger 幂等迁移

状态：方案已细化；不表示实现或验收完成。

## 关联与入口

- 工作项：MIG-001；Issue：#118；里程碑：M2。
- 调查入口：`internal/store/sqlite.go`。入口来自台账，执行前核对实际文件及调用链。
- 当前发现：PR #211 describes repair when version >=60 but tables are missing; test source exists.

## 执行与验证步骤

1. 建立旧版本数据库及 version>=60 但 commission ledger 缺表的独立 fixture。
2. 执行升级、重复升级及失败重试，核对账本唯一性、原数据和版本状态。
3. 检查事务失败后的数据库可用性并验证备份恢复；不以删除真实表测试迁移。

## 验收条件

Prove old DB upgrade, version>=60 missing-table recovery, repeat migration, preserved data and rollback.

## 证据与交付

记录候选完整 SHA、实际修改文件、测试命令、退出码、通过/失败/跳过数量、环境及日志位置。本地修改未提交时记录 dirty diff，不能声称绑定纯提交证据。具体测试命令由入口和现有 CI 确认后填入执行记录；尚未执行的检查为 NOT RUN。每次更新追加台账 history，分别维护实现、验证、迁移、验收状态。

## 依赖、风险与回滚

依赖 AUD18-01 候选及证据口径；最终结果由 AUD18-18 汇总。 保留用户现有修改，使用独立任务提交作为代码回滚点。数据库与文件验证使用隔离副本。共享环境部署、推送及破坏性操作须明确授权；未授权不降低该项验收标准，记录为未执行。
