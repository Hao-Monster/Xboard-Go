# AUD18-05 容器健康检查和公开 HTTPS Smoke Test

状态：方案已细化；不表示实现或验收完成。

## 关联与入口

- 工作项：REL-001；Issue：#131；里程碑：M4。
- 调查入口：`.github/workflows/deploy-dev.yml`。入口来自台账，执行前核对实际文件及调用链。
- 当前发现：Health and image-tag checks exist; public check verifies HTTP success only, not revision or functional readiness.

## 执行与验证步骤

1. 核对应用健康接口是否验证必要依赖，并将就绪与进程存活区分。
2. 核对运行镜像 ID、版本接口、公开关键只读接口；HTTP 200 不足以证明候选版本运行。
3. 模拟超时、错误版本、错误响应内容和不健康容器，验证停止及回滚路径。

## 验收条件

Verify loaded image identity, application readiness, public critical endpoint and expected version; fail safely.

## 证据与交付

记录候选完整 SHA、实际修改文件、测试命令、退出码、通过/失败/跳过数量、环境及日志位置。本地修改未提交时记录 dirty diff，不能声称绑定纯提交证据。具体测试命令由入口和现有 CI 确认后填入执行记录；尚未执行的检查为 NOT RUN。每次更新追加台账 history，分别维护实现、验证、迁移、验收状态。

## 依赖、风险与回滚

依赖 AUD18-01 候选及证据口径；最终结果由 AUD18-18 汇总。 保留用户现有修改，使用独立任务提交作为代码回滚点。数据库与文件验证使用隔离副本。共享环境部署、推送及破坏性操作须明确授权；未授权不降低该项验收标准，记录为未执行。
