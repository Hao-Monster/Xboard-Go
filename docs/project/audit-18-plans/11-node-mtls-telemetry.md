# AUD18-11 节点双向认证与运行时 telemetry

状态：方案已细化；不表示实现或验收完成。

## 关联与入口

- 工作项：VER-003；Issue：#121；里程碑：M3。
- 调查入口：`internal/httpapi/node_auth_telemetry.go`。入口来自台账，执行前核对实际文件及调用链。
- 当前发现：Dual authentication and telemetry changes exist; local work item done but GitHub issue open.

## 执行与验证步骤

1. 核对 V1/V2 节点鉴权和遥测字段，验证旧节点协议兼容。
2. 验证错误凭据、重放或过期请求、越权节点及遥测敏感字段。
3. 模拟节点重启、网络恢复和运行时状态更新，确认可观测且不泄露密钥。

## 验收条件

Verify V1/V2 compatibility, unauthorized rejection, secret-free telemetry and runtime recovery.

## 证据与交付

记录候选完整 SHA、实际修改文件、测试命令、退出码、通过/失败/跳过数量、环境及日志位置。本地修改未提交时记录 dirty diff，不能声称绑定纯提交证据。具体测试命令由入口和现有 CI 确认后填入执行记录；尚未执行的检查为 NOT RUN。每次更新追加台账 history，分别维护实现、验证、迁移、验收状态。

## 依赖、风险与回滚

依赖 AUD18-01 候选及证据口径；最终结果由 AUD18-18 汇总。 保留用户现有修改，使用独立任务提交作为代码回滚点。数据库与文件验证使用隔离副本。共享环境部署、推送及破坏性操作须明确授权；未授权不降低该项验收标准，记录为未执行。
