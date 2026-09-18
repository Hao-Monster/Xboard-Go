# AUD18-17 所有后台列表页宽度、自适应和窄屏行为

状态：方案已细化；不表示实现或验收完成。

## 关联与入口

- 工作项：CI-001；Issue：#127；里程碑：M3。
- 调查入口：`web/src/styles.css`。入口来自台账，执行前核对实际文件及调用链。
- 当前发现：PR #224 width change passed CI; local styles.css also dirty; no per-page legacy visual acceptance this audit.

## 执行与验证步骤

1. 枚举全部后台列表页面和共用容器，形成逐页验收矩阵。
2. 在桌面、平板、手机视口检查表格内滚动、筛选、分页和操作列，无页面横向溢出。
3. 逐页对照旧系统截图和交互；只有有实测证据的页面才能标记通过。

## 验收条件

List every affected page; compare desktop/mobile with legacy; ensure internal table scrolling and no controls clipping.

## 证据与交付

记录候选完整 SHA、实际修改文件、测试命令、退出码、通过/失败/跳过数量、环境及日志位置。本地修改未提交时记录 dirty diff，不能声称绑定纯提交证据。具体测试命令由入口和现有 CI 确认后填入执行记录；尚未执行的检查为 NOT RUN。每次更新追加台账 history，分别维护实现、验证、迁移、验收状态。

## 依赖、风险与回滚

依赖 AUD18-01 候选及证据口径；最终结果由 AUD18-18 汇总。 保留用户现有修改，使用独立任务提交作为代码回滚点。数据库与文件验证使用隔离副本。共享环境部署、推送及破坏性操作须明确授权；未授权不降低该项验收标准，记录为未执行。
