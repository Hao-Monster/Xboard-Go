# 礼品卡修复后功能测试报告

已修复版本的本地功能复测通过，本轮未发现新的产品缺陷。按先方案、再用例、再执行的顺序完成：26条业务用例具备下表标明层级的验证证据；47项组件测试、8项浏览器运行实例、36项常规后端顶层测试及5项补充集成测试通过。数量按层级分别报告，不相加成需求数或覆盖率。线上尚未发布本次修复，本结论不等于线上全面验收通过。

## 版本与执行顺序

- 待测产品提交：`0ade82ebb34f1a30f735b1e6984f714d1cfe4f69`，分支`codex/ci-001-tiered-gates`。本轮仅新增测试与文档，没有修改应用实现。
- 方案：[礼品卡修复后功能测试方案与用例](gift-card-postfix-plan-2026-10-04.md)。2026-10-04 11:57:58Z（用户时区America/Los_Angeles 04:57:58）在测试前完成独立复核并冻结。
- 方案SHA256：`B02114DD2DA3120E03CCE00747A07F0279F799E4E5452AA935ABA32C89FC3EA1`。记录位于`docs-dev/gift-card-postfix-20261004/plan-final-baseline.json`；最终方案校验一致。
- 首轮自动执行约11:58:36Z开始，浏览器专项12:00:00Z开始，人工证据12:07:57Z记录。独立审查要求强化Tab到达重试控件后，仅重跑受影响的6项浏览器恢复测试。
- 环境：Windows、Node24.12.0、pnpm11.10.0、Go1.26.8；本地构建前端`127.0.0.1:4176`代理实际Go服务`127.0.0.1:8086`，隔离内存SQLite。自动浏览器为Chromium 1280×900及移动模拟390×844；人工交互使用Computer Use内置浏览器。

## 用例结果矩阵

C为真实React组件配合受控API，B为真实浏览器及Go/SQLite，D为DB/HTTP集成，M为Computer Use人工式交互。PASS限于列出的层级和实际分支。

| ID | 结果 | 本轮实际验证及证据边界 |
| --- | --- | --- |
| PF-01 | PASS C/B/M | 新用户无记录显示空态、无分页；人工创建独立QA用户复核 |
| PF-02 | PASS B/M | 自动Enter验证7位/非法字符不发查询；人工鼠标点击分别触发tooShort/patternMismatch；空码按钮禁用 |
| PF-03 | PASS C/B/M | 小写规范化大写，Enter只查询；B核对查询后账本仍为0，M预览0→200GB且历史仍空 |
| PF-04 | PASS C，B辅助 | 编辑立即作废旧预览，确认绑定实际查询码；乱序确定性由C提供，B网络结束后的即时否定断言仅为辅助 |
| PF-05 | PASS C | A→B→A之后旧A返回仍不能确认 |
| PF-06 | PASS C | 旧A成功/失败均不能覆盖新B；新增两项验证旧A完成不能解除仍等待的B查询锁 |
| PF-07 | PASS C | 重新查询立即撤销原预览的确认入口 |
| PF-08 | PASS C/D | 受控组件双击只发一次兑换且输入锁定；D唯一发放另有断言。未执行真实浏览器暂缓响应下双击，不声称B层已覆盖 |
| PF-09 | PASS C/B | 当前查询失败无确认或伪成功；恢复后再次Enter查询正常 |
| PF-10 | PASS C/B | 请求到服务端前中止，账本/额度保持0；手动重试后恰好1条usage、1笔订单、200GiB |
| PF-11 | PASS B/D | 真正route.fetch完成HTTP200后丢弃响应；重试返回相同usage.id和order_trade_no，额度仍200GiB，订单/历史仍1条。该场景不直接读取code.usage_count或权益事件，它们由D独立覆盖 |
| PF-12 | PASS C/B | 兑换成功、历史失败同时正确提示，旧码清空且输入恢复；只重试GET历史，POST次数保持1；C从旧第二页验证保留数据 |
| PF-13 | PASS C/B | 初次历史失败不显示空态；Tab到重试后Enter恢复真实空态 |
| PF-14 | PASS C | 0/1/15/16/30/31条逐页收集全部订单ID，完整序列一致，无遗漏/重复，首末按钮正确禁用 |
| PF-15 | PASS C/B/M | B实际16条历史按15+1分页且核对首单；M实际18条按15+3分页 |
| PF-16 | PASS C | 翻页失败保留当前数据，重试目标仍为第二页；未在B注入分页失败 |
| PF-17 | PASS C | 旧第二页等待期间兑换刷新第一页，旧响应不能覆盖新记录；未在B注入历史响应乱序 |
| PF-18 | PASS B/M | B第二页兑换后每次回第一页，总数16→17→18；M在18条末页刷新后重新进入显示第一页15条、总数18，末页仍3条 |
| PF-19 | PASS B/M | B桌面/390px移动无横向溢出；M实际innerWidth390、documentWidth375（含滚动条差异）、tableWidth313，五个字段标签均存在且可读 |
| PF-20 | PASS B/M | B验证输入Tab→查询→确认及Enter，补强后输入Tab可到历史重试；M用Tab/Enter前后翻页并跳过禁用按钮。未做完整WCAG审计 |
| PF-21 | PASS B/D/M | 重复码UI拒绝且无确认；同用户API幂等及跨用户拒绝由D验证；M确认记录仍1条 |
| PF-22 | PASS B/D/M | B真实18×200GiB=3600GiB且永久有效；M首次200GiB、已用0与订单一致；D叠加保留既有已用量 |
| PF-23 | PASS D/C | 周期预览/保留量及跨套餐/pending拒绝通过；补充overlay验证流量→周期和processing不产生副作用。完整日历/全部周期枚举未纳入 |
| PF-24 | PASS D | 并发、三写入失败点回滚、冻结快照专项通过；补充并发核对两用户权益、单次核销及usage/order/event归属 |
| PF-25 | PASS D | 常规session/CSRF/分销allowlist与稳定码身份测试通过；补充Cookie/Bearer跨用户记录详情及缺CSRF写入拒绝通过 |
| PF-26 | PASS D/C | 旧礼品卡类型DB/HTTP、管理组件及订单组件回归通过；旧类型线上UI未运行 |

## 自动化命令与结果

| 命令 | 状态/退出码 | 通过/失败/跳过 | 时间 |
| --- | --- | --- | --- |
| `pnpm --dir web exec vitest run src/features/giftcards/GiftCardManagementPage.test.tsx src/features/giftcards/UserGiftCardPage.test.tsx src/features/giftcards/UserGiftCardPage.regression.test.tsx src/features/giftcards/UserGiftCardPage.functional.test.tsx src/features/orders/OrdersPage.test.tsx --reporter=json --outputFile=../docs-dev/gift-card-postfix-20261004/components.json` | PASS / 0 | 47/0/0，5文件；包含新增11项 | 墙钟53.66s |
| `go test ./internal/store ./internal/httpapi -run '^TestGift\|TestAdminAPIRequiresSessionAndCSRF\|TestDistributorAccessIsAnExplicitServerSideAllowlist' -count=1 -json` | PASS / 0 | 36顶层，56含子测试通过事件；0失败0跳过 | 墙钟21.55s；store9.328s、httpapi9.709s |
| `go test '-overlay=docs-dev/gift-card-qa-20261004/supplement/expanded-overlay.json' ./internal/store ./internal/httpapi -run '^TestQAGift(TrafficToCycleRejectsWithoutSideEffects\|ProcessingOrderRejectsWithoutSideEffects\|HistoryAndDetailsAreOwnerScoped\|CookieWritesRequireCSRFBeforeEffects\|ConcurrentRedemptionGrantsExactlyOnce)$' -count=1 -json` | PASS / 0 | 5顶层，11含子测试事件；0失败0跳过 | 墙钟14.18s；store2.689s、httpapi7.990s |
| `pnpm --dir web exec playwright test e2e/zz-gift-card-regressions.spec.ts e2e/zz-gift-card-recovery.spec.ts --project=chromium --project=mobile-chromium '--reporter=list,json'` | PASS / 0 | 8/0/0，无重试或flaky | 30.06s |
| `pnpm --dir web exec playwright test e2e/zz-gift-card-recovery.spec.ts --project=chromium --project=mobile-chromium '--reporter=list,json'`（强化键盘断言后） | PASS / 0 | 6/0/0，无重试；是上行6项复跑，不新增累计覆盖 | 27.42s |
| `pnpm --dir web run build` | PASS / 0 | 不适用；含tsc | 31.75s |
| `pnpm --dir web run lint` | PASS / 0 | 不适用 | 71.64s |
| `pnpm --dir web exec eslint e2e/zz-gift-card-recovery.spec.ts --max-warnings 0`（键盘调整后） | PASS / 0 | 不适用 | 10.68s |
| `git diff --check`及`git diff --cached --check` | PASS / 0 | 不适用 | 无空白错误 |

表格中的正则竖线为Markdown转义，实际PowerShell参数使用普通`|`。浏览器运行设置`XBOARD_E2E_EXTERNAL_SERVER=true`、`XBOARD_E2E_BASE_URL=http://127.0.0.1:4176`，JSON分别写`browser.json`及`browser-keyboard.json`。本地服务按现有Playwright的bootstrap/SQLite约定启动；未执行真实付款，未连接线上创建测试数据。

新增组件文件`web/src/features/giftcards/UserGiftCardPage.functional.test.tsx`，新增浏览器文件`web/e2e/zz-gift-card-recovery.spec.ts`。Go补充overlay属于前一轮本地测试附件，本轮确实重新运行，但尚未纳入仓库常规测试套件，不把它描述为CI覆盖。

## 人工交互证据

Computer Use在本地管理界面创建普通QA用户与单次购买码，再用普通用户界面完成：空态→小写查询→预览→Tab/Enter确认→200GiB永久权益→一条历史→一笔已完成兑换码购买订单→再次查询拒绝。订单号`2026100420033391203096608`在历史和用户订单页一致，页面明确外部金额未核验。

另外选择自动化已创建的18条记录用户，通过键盘翻页、390px布局和整页刷新验证历史完整性。最终无浏览器error日志。只关闭本轮创建的本地标签并重置视口；既有线上管理/用户标签保留。收尾检查4176/8086已无监听，本机内存测试环境未持续运行。

人工证据：`docs-dev/gift-card-postfix-20261004/manual-results.json`、`manual-success.png`、`manual-mobile-page2.png`、`manual-mobile-final.png`。这些是忽略目录中的原始附件；报告与可重复自动测试纳入本地提交。测试截图只展示合成账号和已掩码兑换记录，不作为线上截图。

## 失败记录与独立审查

本轮功能测试无FAIL/skip，不存在通过修改产品或降低断言换取通过。Computer Use曾用role=button定位实际为generic的“账号菜单”失败一次，查看当前DOM后改用getByLabel成功；这是工具定位问题，不是产品缺陷。旧`zz-gift-cards.spec.ts`已知管理导航前置不兼容，本轮NOT RUN，没有将其改成skip或声称通过。

独立审查确认冻结计划早于执行、响应丢失确实发生在服务端提交之后、重试核对真实账本、分页完整序列断言有效。指出的多页刷新、键盘翻页与Tab到历史重试证据已分别由M和强化B补齐。没有发现阻塞本轮修复范围的产品问题。

本轮为修复后验收，不新增Red→Green声明；三项缺陷原始Red与修复Green见前一份`gift-card-payment-qa-2026-10-04.md`，未覆盖旧证据。

## 限制和未运行项目

- 行/分支覆盖率未采集；47项组件数不表示代码覆盖率。没有新增生产代码，也未修改覆盖率门禁。
- 线上发布后测试NOT RUN：未授权推送或部署，线上精确应用SHA未重新核实。
- 原30项线上计划的管理员对账、导出文件、重新启用、旧类型UI等仍未关闭；本轮本地人工流程不冒充其线上证据。
- 实体手机触摸与软键盘、其他浏览器内核、全部窄屏宽度、完整键盘可访问性审计NOT RUN。390px模拟及实际操作已通过。
- race、负载容量、全量E2E、部署级迁移/发布回滚演练NOT RUN；本轮没有涉及这些实现变更。既有SQLite迁移与事务回滚测试已随后端专项通过。
- 响应丢失场景直接证明usage/order/实际权益唯一；码次数和权益事件由DB分层证据支持。既有回滚测试未逐字段对比全部订阅状态，周期测试也未构成完整跨时区证明。
- 当前没有已知的本轮修复范围High缺陷。本轮结论为本地修复后功能测试通过，正式业务验收仍由用户确认。
