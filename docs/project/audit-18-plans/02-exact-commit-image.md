# AUD18-02 精确提交构建镜像

状态：方案已细化；不表示实现或验收完成。

## 关联与入口

- 工作项：REL-001；Issue：#131；里程碑：M4。
- 调查入口：`.github/workflows/deploy-dev.yml`。入口来自台账，执行前核对实际文件及调用链。
- 当前发现：revision is an unrestricted string interpolated directly into shell; no CI-success authorization or artifact identity manifest.

## 执行与验证步骤

1. 验证完整小写 SHA、实际 checkout HEAD 和允许来源；在构建之前通过 GitHub API 核对候选 CI 的 workflow、SHA、仓库、结论。
2. 构建一次，保存本地镜像 ID、提交、运行 ID、归档 SHA256；无 registry digest 时明确记为不适用。
3. 验证上传前后摘要、载入后镜像身份；注入错误 SHA、失败 CI、篡改归档，必须在切换前拒绝。

## 验收条件

Validate full SHA and allowed source; verify CI for candidate; build once; record image ID/digest, archive checksum and run ID.

## 证据与交付

记录候选完整 SHA、实际修改文件、测试命令、退出码、通过/失败/跳过数量、环境及日志位置。本地修改未提交时记录 dirty diff，不能声称绑定纯提交证据。具体测试命令由入口和现有 CI 确认后填入执行记录；尚未执行的检查为 NOT RUN。每次更新追加台账 history，分别维护实现、验证、迁移、验收状态。

## 依赖、风险与回滚

依赖 AUD18-01 候选及证据口径；最终结果由 AUD18-18 汇总。 保留用户现有修改，使用独立任务提交作为代码回滚点。数据库与文件验证使用隔离副本。共享环境部署、推送及破坏性操作须明确授权；未授权不降低该项验收标准，记录为未执行。
