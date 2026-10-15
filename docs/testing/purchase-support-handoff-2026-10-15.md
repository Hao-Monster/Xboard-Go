# Xboard 客服购买订单交付

## 已确认范围

2026-10-15：客户点击“打开客服购买”后直接进入会话，自动发送当前订单信息；每个新订单自动回复一次现有微信二维码，同一订单重试或刷新不重复。订单消息只是客服咨询上下文，不是付款凭证，不修改订单、余额、兑换码或支付状态。

关联现有 PAY-001 / GIFT-001、VER-002、Issue #120、M3；本次只补全购买渠道接入，不宣称上述大工作项完成。

当前另一任务正在修改 Xboard 订单、支付和服务路由。用户明确要求先完成客服端和独立前端文件，交叉文件等待另一任务结束。保留所有已有暂存和未暂存修改，不推送、不部署。

## 验收与测试矩阵（实现前冻结）

| ID | 场景 / 风险 | 可执行验证 | 预期 |
| --- | --- | --- | --- |
| S01 | 未点击按钮 | PurchaseAlternatives 组件测试 | 不加载客服、不传订单 |
| S02 | 单击入口 | SupportChatFrame 组件及浏览器 | 启用直达会话，等待身份和消息路由就绪 |
| S03 | 订单上下文 | 消息构造及入口测试 | 当前服务端返回订单的单号、套餐、周期、待付金额；不携带登录凭证 |
| S04 | 重复、刷新、并发 | 客服 PostgreSQL 集成 | 稳定订单键只保存一条订单消息和一条二维码回复 |
| S05 | 已有会话中新订单 | 客服集成 | 保留历史，新订单各得到一次二维码 |
| S06 | 首个订单与旧欢迎规则 | 客服集成 | 首单只出现一次二维码；普通访客欢迎行为保持 |
| S07 | 响应丢失后重试 | 前端及客服集成 | 原键恢复原回执，不产生新消息 |
| S08 | 错误来源、窗口、请求或身份 | 协议组件及客服集成 | 拒绝伪造事件、错身份与内容冲突，不泄露旧消息 |
| S09 | 不兼容客服、二维码缺失、超时 | 负面测试 | 明确失败，不显示已发送，不降级成无法去重的普通发送 |
| S10 | 收起、卸载、迟到响应 | 组件测试 | 取消等待，不迟到重开，不丢失已保存回执 |
| S11 | 发送后显示确认 | 客服组件及真实浏览器 | 当前会话可见真实订单消息和二维码才报成功 |
| S12 | 回归与质量 | 相关测试、typecheck、lint、构建、补丁校验 | 保留普通人工消息、FreedomPost 原流程及支付状态 |

测试使用合成 QA 订单与隔离客服数据库。生产消息和发布不包含在本次本地验证中。实际命令、Red/Green、计数和未运行项在执行后记录，不能把测试文件存在当成通过。

## 2026-10-15 执行结果

| 命令 / 环境 | 状态与退出码 | 通过 / 失败 / 跳过 | 耗时 |
| --- | --- | --- | --- |
| `pnpm --dir web test -- src/features/orders/purchaseSupport.test.ts src/features/orders/SupportChatFrame.test.tsx src/features/orders/PurchaseAlternatives.test.tsx src/features/orders/OrdersPage.test.tsx`；Windows / Vitest 4.1.11 | PASS / 0 | 23 / 0 / 0 | 40.86 秒 |
| `pnpm --dir web typecheck` | PASS / 0 | 不适用 | 未单独计时 |
| `pnpm --dir web lint` | PASS / 0 | 不适用 | 未单独计时 |
| `pnpm --dir web build`；Vite 8.3.0 | PASS / 0 | 360 模块；入口 gzip 89,999 字节 | Vite 724 毫秒，另含类型检查 |
| 客服 `pnpm exec vitest run app/javascript/widget/specs/DirectConversation.spec.js app/javascript/sdk/specs/FreedompostHandoff.spec.js --maxWorkers=2 --minWorkers=1` | PASS / 0 | 40 / 0 / 0 | 20.82 秒 |
| 客服 4 个改动 JS/Vue 文件 `pnpm exec eslint ... --max-warnings 0` | PASS / 0 | 不适用 | 未单独计时 |
| 客服 `VITE_CW_FRONTEND=widget pnpm exec vite build` | PASS / 0 | 5,075 模块 | 2 分 13 秒 |
| `wsl -d Debian -- bash /mnt/c/Temp/xboard-support-20261015/rails-test.sh green fp_chatwoot_order_qa_xboard20261015green3`；Ruby 3.4.4 / Rails 7.2.3.1 / PostgreSQL 17 / 隔离 Redis | PASS / 0 | 26 / 0 / 0，178 个断言 | 用例 10.84 秒，另含 Rails 启动 |
| 客服 `python -m unittest discover -s deploy/order-handoff -p 'test_*.py'` | PASS / 0 | 10 / 0 / 0 | 3.484 秒 |
| 客服 `python -m unittest discover -s deploy/widget-direct/tests -p 'test_*.py'` | PASS / 0 | 5 / 0 / 0 | 0.003 秒 |
| `python C:\Temp\xboard-support-20261015\verify-final-patch.py` | PASS / 0 | 固定归档两阶段补丁重建，25 文件 SHA256 与测试源码一致 | 未单独计时 |

S01–S03、S07–S10 已有页面/组件/协议证据；S04–S06 和 S08–S09 服务端部分使用真实数据库、路由、模型、附件与回调验证。S11 的 DOM/图片失败及迟到删除由 Vue 测试验证，真实浏览器跨系统验收仍为 NOT RUN。S12 的支付入口回归、静态检查、构建与补丁验证通过。

### Red → Green 与独立复核

- 原直达会话断言失败：缺少 `directConversation:true`；修复后目标测试通过。
- 原客服运行时通过原有 20 项回归后，新订单接口返回 404；最终新接口及 6 项新增场景全部通过。
- 首轮新实现的真实数据库测试发现首单出现 3 条二维码。上游 `content_attributes` 使用序列化 JSON 字符串，SQL JSON 查询无法读取标记。改为通过模型读取，并要求存在持久交付记录，重跑全部 26 项通过。
- 独立审查复现二维码删除后旧图片迟到 load 仍报成功；修复后探针返回 `HANDOFF_NOT_RENDERED`，Vue 回归通过。另补真实 `UserOrdersPage` 按钮到订单交付的组件流程。
- 从固定归档重建补丁发现无上下文插入偏移；新增补丁 CLI 测试先失败，再修正空旧范围的定位。补丁工具 15 项测试通过，最终重建逐文件核对 25 个文件一致。

### 限制与发布准备

- NOT RUN：生产发送、真实浏览器端到端（Xboard + 真实客服 + 图片网络）、手机扫码、完整全仓前端套件、远程 CI 和生产部署。没有发送生产订单或改变线上 QR 规则；本地组件和数据库测试不替代上述验收。
- 未采集行或分支覆盖率。客服构建存在上游大包、Browserslist 与缺失 source map 警告，命令均以 0 退出。
- 初期本地挂载/启动失败属于测试环境问题，不计为功能通过；保留失败数据库，最终使用全新 `green3` 数据库，后台任务和邮件均由测试适配器截获。
- 无 Xboard 服务端、订单、支付或路由改动，无数据库迁移。父页面订单文本属于咨询上下文，客服必须独立核验付款。
- 发布顺序：先发布客服补丁并确认认证 capability `purchase_qr_orders:true`，再发布 Xboard 前端。旧客服或回滚版本会明确失败，不降级成不能去重的普通消息。
- 回滚按两项目现有发布流程恢复各自上一个版本；保留原交付表及订单/二维码记录，不自动补发。
- Xboard 保留其他任务暂存改动，本任务未操作其共享 Git 暂存区或提交。发布前应在其他任务结束后统一复核集成版本。
