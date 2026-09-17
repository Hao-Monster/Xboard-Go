# AUD18-03 开发测试服务器 SSH 发布

状态：方案已细化；不表示实现或验收完成。

## 关联与入口

- 工作项：REL-001；Issue：#131；里程碑：M4。
- 调查入口：`.github/workflows/deploy-dev.yml`。入口来自台账，执行前核对实际文件及调用链。
- 当前发现：本机 `bingo-dev` 通过 Tailscale 可达且核心服务 active，但 `ubuntu-latest` runner 原先没有 Tailscale 接入步骤，直接访问 `100.79.73.91` 无法由本机 SSH 成功推导。工作流现加入固定版本的 Tailscale Action，并在 SSH 前执行目标地址 ping；GitHub secret、tag ACL 和真实 runner 连接仍需候选运行验证。

## 执行与验证步骤

1. 按 deploy-dev-test-server 技能核对真实连接方式；区分本机 SSH 别名与 hosted runner 环境。
2. 通过固定提交的 `tailscale/github-action` 使用 `TS_OAUTH_CLIENT_ID`、`TS_OAUTH_SECRET` 和 `tag:xboard-go-ci`，先 ping `100.79.73.91`，再验证 `bingo@100.79.73.91`、known_hosts、密钥权限和最小权限；禁止以运行时 keyscan 作为信任来源。
3. 先验证错误密钥、错误指纹、连接超时与最小权限；共享服务器实测需单独授权。

## 验收条件

Verify runner connectivity, pinned host identity, least-privilege key and explicit user/host in isolated deployment.

## 证据与交付

记录候选完整 SHA、实际修改文件、测试命令、退出码、通过/失败/跳过数量、环境及日志位置。本地修改未提交时记录 dirty diff，不能声称绑定纯提交证据。具体测试命令由入口和现有 CI 确认后填入执行记录；尚未执行的检查为 NOT RUN。每次更新追加台账 history，分别维护实现、验证、迁移、验收状态。

## 依赖、风险与回滚

依赖 AUD18-01 候选及证据口径；最终结果由 AUD18-18 汇总。 保留用户现有修改，使用独立任务提交作为代码回滚点。数据库与文件验证使用隔离副本。共享环境部署、推送及破坏性操作须明确授权；未授权不降低该项验收标准，记录为未执行。
