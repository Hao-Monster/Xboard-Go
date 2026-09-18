# AUD18-01 流水线治理门禁和 PR 元数据

状态：方案已细化；不表示实现或验收完成。

## 关联与入口

- 工作项：CI-001；Issue：#127；里程碑：M3。
- 调查入口：`.github/workflows/ci.yml`。入口来自台账，执行前核对实际文件及调用链。
- 当前发现：PR #224 CI 35056520257 passed at a2b968321e22ce60aaaa95a75dc0e1ab9c129f87; PR #211 governance remains stale.

## 执行与验证步骤

1. 核对 PR #211/#224 的提交、文件范围、Issue 和里程碑，选定整合候选，不将小 PR 证据推广到大分支。
2. 追溯 baseline 之后产品变更及其影响，保留旧证据 SHA；只有重跑通过的证据才能记为 current。
3. 核对 decisions/work-items/requirements/release-gates，先解决不一致，再运行 generate 与 check。

## 验收条件

Target PR metadata and governance pass; record exact candidate and actual evidence. Do not rebind historical results without rerunning.

## 证据与交付

记录候选完整 SHA、实际修改文件、测试命令、退出码、通过/失败/跳过数量、环境及日志位置。本地修改未提交时记录 dirty diff，不能声称绑定纯提交证据。具体测试命令由入口和现有 CI 确认后填入执行记录；尚未执行的检查为 NOT RUN。每次更新追加台账 history，分别维护实现、验证、迁移、验收状态。

## 依赖、风险与回滚

与其余条目共享候选版本；不得批量替换历史证据 SHA。 保留用户现有修改，使用独立任务提交作为代码回滚点。数据库与文件验证使用隔离副本。共享环境部署、推送及破坏性操作须明确授权；未授权不降低该项验收标准，记录为未执行。
