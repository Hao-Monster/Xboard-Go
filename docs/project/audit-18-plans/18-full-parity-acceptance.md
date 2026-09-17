# AUD18-18 完整 parity、浏览器、race、迁移和恢复验收

状态：方案已细化；不表示实现或验收完成。

## 关联与入口

- 工作项：CI-001；Issue：#127；里程碑：M3。
- 调查入口：`.github/workflows/legacy-parity.yml`。入口来自台账，执行前核对实际文件及调用链。
- 当前发现：PR #224 normal CI passes; not proof for PR #211, full legacy parity or real disaster recovery.
- 当前环境基线：本地 `go test ./...` 在候选 HEAD `c1e1ccaaedaf35e4de34515b01e8139a9e018973` 通过；`go test -race` 被本机缺少 gcc 阻塞；parity 所需 `LEGACY_ADMIN_URL`、`XBOARD_GO_URL`、`LEGACY_DOCKER_CONTAINER` 等变量未配置；bingo-dev 只读健康检查通过但当前运行镜像为 `xboard-go:61aa2342`，不是本地候选。

## 执行与验证步骤

1. 冻结最终整合候选，记录源码 SHA、工具版本、测试数据和依赖环境。
2. 按现有 CI 执行完整 parity、浏览器、race、数据库迁移和备份恢复矩阵。
3. 逐项核对 01-17 证据及发布门禁，失败修复后重跑受影响测试；禁止将历史结果升级为当前验收。

## 验收条件

Choose integrated candidate; run complete applicable parity/browser/race/migration/restore matrix; resolve release gates.

## 证据与交付

记录候选完整 SHA、实际修改文件、测试命令、退出码、通过/失败/跳过数量、环境及日志位置。本地修改未提交时记录 dirty diff，不能声称绑定纯提交证据。具体测试命令由入口和现有 CI 确认后填入执行记录；尚未执行的检查为 NOT RUN。每次更新追加台账 history，分别维护实现、验证、迁移、验收状态。

## 依赖、风险与回滚

与其余条目共享候选版本；不得批量替换历史证据 SHA。 保留用户现有修改，使用独立任务提交作为代码回滚点。数据库与文件验证使用隔离副本。共享环境部署、推送及破坏性操作须明确授权；未授权不降低该项验收标准，记录为未执行。
