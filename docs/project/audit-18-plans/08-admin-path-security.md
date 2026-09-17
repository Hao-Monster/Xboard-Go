# AUD18-08 管理员路径持久化与安全校验

状态：方案已细化；不表示实现或验收完成。

## 关联与入口

- 工作项：FUNC-003；Issue：#145；里程碑：M1。
- 调查入口：`internal/config/config.go`。入口来自台账，执行前核对实际文件及调用链。
- 当前发现：Existing work item is done locally; current-candidate acceptance not established by this audit.

## 执行与验证步骤

1. 检查管理员路径读取、验证、持久化及路由注册的完整链路。
2. 覆盖保留路径、编码变体、非法字符、未授权请求与旧路径行为。
3. 在隔离实例更新路径并重启，确认新路径生效且公共路由及登录权限保持正确。

## 验收条件

Verify restart persistence, invalid/reserved paths, unauthorized access, old-path behavior and public routing.

## 证据与交付

记录候选完整 SHA、实际修改文件、测试命令、退出码、通过/失败/跳过数量、环境及日志位置。本地修改未提交时记录 dirty diff，不能声称绑定纯提交证据。具体测试命令由入口和现有 CI 确认后填入执行记录；尚未执行的检查为 NOT RUN。每次更新追加台账 history，分别维护实现、验证、迁移、验收状态。

## 依赖、风险与回滚

依赖 AUD18-01 候选及证据口径；最终结果由 AUD18-18 汇总。 保留用户现有修改，使用独立任务提交作为代码回滚点。数据库与文件验证使用隔离副本。共享环境部署、推送及破坏性操作须明确授权；未授权不降低该项验收标准，记录为未执行。
