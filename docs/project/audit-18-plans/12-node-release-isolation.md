# AUD18-12 节点 release 包校验、隔离和可见性控制

状态：方案已细化；不表示实现或验收完成。

## 关联与入口

- 工作项：VER-003；Issue：#121；里程碑：M3。
- 调查入口：`internal/httpapi/node_release_handlers.go`。入口来自台账，执行前核对实际文件及调用链。
- 当前发现：Release handler and tests changed in PR #211.

## 执行与验证步骤

1. 核对 release 获取权限、可见性过滤和节点隔离，枚举跨用户访问路径。
2. 测试摘要或签名策略、包大小、归档路径穿越和解压资源限制。
3. 使用隔离下载与解压目录验证错误包拒绝、有效包接受及失败清理。

## 验收条件

Verify checksum/signature policy, archive path limits, authorization, visibility and isolation.

## 证据与交付

记录候选完整 SHA、实际修改文件、测试命令、退出码、通过/失败/跳过数量、环境及日志位置。本地修改未提交时记录 dirty diff，不能声称绑定纯提交证据。具体测试命令由入口和现有 CI 确认后填入执行记录；尚未执行的检查为 NOT RUN。每次更新追加台账 history，分别维护实现、验证、迁移、验收状态。

## 依赖、风险与回滚

依赖 AUD18-01 候选及证据口径；最终结果由 AUD18-18 汇总。 保留用户现有修改，使用独立任务提交作为代码回滚点。数据库与文件验证使用隔离副本。共享环境部署、推送及破坏性操作须明确授权；未授权不降低该项验收标准，记录为未执行。
