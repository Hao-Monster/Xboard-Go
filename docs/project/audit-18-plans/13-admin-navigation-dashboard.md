# AUD18-13 管理员导航和 Dashboard

状态：方案已细化；不表示实现或验收完成。

## 关联与入口

- 工作项：VER-005；Issue：#123；里程碑：M3。
- 调查入口：`web/src/features/system/AdminDashboardPage.tsx`。入口来自台账，执行前核对实际文件及调用链。
- 当前发现：Admin dashboard and App changes exist in the branch diff; no live UI acceptance this audit.

## 执行与验证步骤

1. 核对管理员导航、角色权限和 Dashboard 数据接口，建立页面与 API 对照。
2. 验证加载、空、失败和权限不足状态，不以模拟成功掩盖接口错误。
3. 真实浏览器检查导航、统计数据、键盘焦点及桌面/移动布局。

## 验收条件

Verify role-based navigation, actual API data, error/empty states and responsive rendering.

## 证据与交付

记录候选完整 SHA、实际修改文件、测试命令、退出码、通过/失败/跳过数量、环境及日志位置。本地修改未提交时记录 dirty diff，不能声称绑定纯提交证据。具体测试命令由入口和现有 CI 确认后填入执行记录；尚未执行的检查为 NOT RUN。每次更新追加台账 history，分别维护实现、验证、迁移、验收状态。

## 依赖、风险与回滚

依赖 AUD18-01 候选及证据口径；最终结果由 AUD18-18 汇总。 保留用户现有修改，使用独立任务提交作为代码回滚点。数据库与文件验证使用隔离副本。共享环境部署、推送及破坏性操作须明确授权；未授权不降低该项验收标准，记录为未执行。
