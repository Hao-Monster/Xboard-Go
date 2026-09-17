# AUD18-16 用户门户和礼品卡页面

状态：方案已细化；不表示实现或验收完成。

## 关联与入口

- 工作项：VER-002；Issue：#120；里程碑：M3。
- 调查入口：`web/src/features/giftcards/UserGiftCardPage.tsx`。入口来自台账，执行前核对实际文件及调用链。
- 当前发现：Portal/giftcard changes exist; user-owned GiftCardManagementPage.tsx is dirty.

## 执行与验证步骤

1. 对照用户门户与礼品卡页面，追踪权限、兑换入口和账务结果。
2. 隔离数据库验证跨用户访问、重复兑换、并发兑换及无效/过期卡。
3. 浏览器核对请求、响应、余额和列表刷新，并保留已有礼品卡修改。

## 验收条件

Verify user isolation, giftcard redemption/idempotency, actual API flow and legacy parity.

## 证据与交付

记录候选完整 SHA、实际修改文件、测试命令、退出码、通过/失败/跳过数量、环境及日志位置。本地修改未提交时记录 dirty diff，不能声称绑定纯提交证据。具体测试命令由入口和现有 CI 确认后填入执行记录；尚未执行的检查为 NOT RUN。每次更新追加台账 history，分别维护实现、验证、迁移、验收状态。

## 依赖、风险与回滚

依赖 AUD18-01 候选及证据口径；最终结果由 AUD18-18 汇总。 保留用户现有修改，使用独立任务提交作为代码回滚点。数据库与文件验证使用隔离副本。共享环境部署、推送及破坏性操作须明确授权；未授权不降低该项验收标准，记录为未执行。
