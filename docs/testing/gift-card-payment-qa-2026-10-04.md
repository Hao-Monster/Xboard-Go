# 礼品卡支付测试方案与执行记录

本报告验证套餐购买兑换码作为主要支付方式时，制卡、查询、兑换、权益、订单和审计是否一致。结果分为线上浏览器实测、本地自动测试和静态审查；任何一层通过均不替代另一层。首轮为只读产品测试；用户随后授权修复，修复结果单列于文末，不覆盖首轮失败证据。

当前状态：三项已知缺陷已在本地修复，36项组件测试和桌面/手机两项真实浏览器专项通过。尚未推送或部署，线上缺陷和原计划未完成项仍需发布后复测；不能据此声明线上全面验收通过。

## 范围与依据

- 日期：2026-10-04，报告日期采用 America/Los_Angeles。
- 用户指定环境：`https://fast.hjy.ca:8443`。管理员会话由用户提供；私有管理入口、密码、完整兑换码不写入报告。
- 用户明确授权创建 QA 普通账号、模板和兑换码，用于本轮测试。
- 本地代码基线：`883fee99c2e503d7c0afa7802111926769b9709c`；线上精确版本尚未核实，不能假设与本地相同。
- 需求：GIFT-001、GIFT-002、PAY-001；工作项 VER-002；关联 Issue #120；Milestone M3。引用现有工作项，不修改其验收状态。
- 依据：`docs/project/gift-purchase-design.json`、`internal/store/gift_card_purchase.go`、`internal/store/gift_cards.go`、`internal/httpapi/gift_card_handlers.go` 和相关测试。
- 主线为 type 4 套餐购买兑换码；type 1 至 3 为兼容回归。外部售卡收款验证、自动发货、真实退款、生产部署不在本轮范围。

## 验收规则

1. 周期购买码按 `max(now, expiry)` 增加指定日历月。同套餐续期保留总配额、已用流量和原有限制；既有流量重置时间保持，缺失时按规则补齐。
2. 流量购买码无到期日，同套餐按发码时冻结额度追加总额，保留上传和下载使用量。例如总额 200 GiB、已用 150 GiB，再兑 200 GiB 后为总额 400 GiB、已用 150 GiB、剩余 250 GiB。
3. 其他套餐（即使过期）、周期与流量模式切换、未完成订单、禁用或过期码均拒绝，不消费码、不改变权益。
4. 发码时冻结权益；后续模板或套餐调整不改变已发行购买码。每张购买码仅可成功一次。
5. 码核销、权益发放、订单、权益事件和使用记录为同一事务。同用户重试返回既有结果；其他用户不得复用。
6. 订单来源为 `gift_card_purchase`。面板金额为零表示面板没有收款，不等于免费或已验证外部付款，不产生伪造的线上收款或佣金。

## 测试数据与方法

命名前缀为 `QA-GIFT-20261004`。用普通 QA 用户覆盖无套餐、有效周期、过期周期、不限时流量、其他套餐、待支付订单等状态。原计划使用独立浏览器会话，但 Chrome 控制入口不可用，原生 Edge 控制受工具限制；实际采用内置浏览器顺序切换 QA 登录。该浏览器同站点标签共享登录 Cookie，管理员最终对账需要重新登录。创建和核销仅限本轮合成数据。记录账号和模板 ID、批次 ID、脱敏码别名、套餐及订单关联；不在报告中保存口令或可兑换的完整码。

每个成功兑换检查“兑换前状态 → 查询预览 → 确认结果 → 实际套餐权益 → 用户记录与订单 → 管理端核销与记录”。拒绝用例核对失败前后权益、订单数量和码状态。操作无响应不等于成功；出现未确认的提交结果时先查询，避免重复创建。

并发、事务故障注入、月末/闰年与迁移使用隔离数据库或测试夹具，不修改共享环境时钟、不破坏共享服务。测试数据默认保留供复查，不擅自删除不可恢复的记录。

## 测试用例

UI 表示浏览器；DB 表示真实隔离 SQLite 集成；API 表示应用 HTTP 入口；组件测试使用受控 API 替身。下表是计划，执行结果另列，不能将已有测试名称当作本轮通过证据。

| 编号 | 优先级 | 前置条件和步骤 | 预期结果 | 层级 |
| --- | --- | --- | --- | --- |
| GC-01 | P0 | 管理员创建周期购买码模板，保存并重开 | 类型、套餐、支持周期持久化正确 | UI、组件 |
| GC-02 | P0 | 创建流量购买码模板并生成两个码 | 无到期日、冻结额度、每码单次使用 | UI、DB |
| GC-03 | P0 | 无套餐用户先查询周期码，再确认 | 查询不核销；首次开通与预览一致 | UI、DB、API |
| GC-04 | P0 | 同套餐有效周期用户兑换新码 | 从原到期日续期；额度和用量不变 | UI、DB |
| GC-05 | P0 | 同套餐已过期用户兑换周期码 | 从当前时刻续期；保留既有用量 | UI、DB |
| GC-06 | P0 | 无套餐用户兑换流量码 | 对应套餐、冻结额度、无到期日 | UI、DB、API |
| GC-07 | P0 | 总额200GiB已用150GiB用户再兑200GiB | 总额400GiB、已用150GiB、剩余250GiB | UI、DB |
| GC-08 | P0 | 有效及过期的其他套餐用户分别兑换 | 拒绝；码、权益和订单不变 | UI、DB |
| GC-09 | P0 | 同套餐周期转流量、流量转周期 | 两种切换均拒绝且不核销 | UI、DB |
| GC-10 | P0 | 有待支付订单或处理中订单时兑换 | 拒绝；不自动取消或代付原订单 | UI、DB |
| GC-11 | P0 | 成功后重试、刷新后重试同一码 | 不再次加权益、不新增订单或记录 | UI、DB、API |
| GC-12 | P0 | B用户兑换A已使用的码 | 拒绝，不泄露A身份，不修改B权益 | UI、DB、API |
| GC-13 | P0 | 两账号并发提交同一码 | 最多一个赢家、一笔订单、一次发放 | DB |
| GC-14 | P0 | 成功后交叉检查两端订单、权益和兑换记录 | 身份、套餐、周期、来源、关联订单一致 | UI、API、DB |
| GC-15 | P0 | 发码后修改专用模板/套餐，再兑换老码并生成新码 | 老码按旧快照，新码按新配置 | UI、DB |
| GC-16 | P0 | 查询并兑换禁用、过期、不存在的码 | 明确失败，无权益和核销副作用 | UI、DB、API |
| GC-17 | P0 | 隔离DB注入订单、事件或使用记录写入失败 | 所有写入回滚，恢复后可重试 | DB |
| GC-18 | P0 | 普通用户访问管理接口、跨用户记录、失效会话、缺CSRF | 服务端拒绝，数据隔离有效 | API、UI |
| GC-19 | P1 | 空白、7/8/32/33位、小写、首尾空格及错误字符 | 验证和规范化一致；失败不核销 | UI、组件、DB |
| GC-20 | P1 | 禁用再启用专用测试码，每次用户端查询 | 状态生效；恢复后符合条件可兑换 | UI |
| GC-21 | P0 | 查询A未返回时改B，A返回后尝试确认；双击提交 | 不得显示A权益却消费B；不重复发放 | UI、组件 |
| GC-22 | P1 | 隔离环境模拟查询/兑换响应失败，刷新核对记录 | 无伪成功；结果可核对；重试幂等 | 组件、DB |
| GC-23 | P1 | 导出本轮批次并核对数量、唯一性、模板 | 只包含目标批次，与后台一致 | UI、API |
| GC-24 | P1 | 回归通用卡、旧套餐卡及盲盒卡 | 旧语义不变，旧套餐卡拒绝有效套餐 | UI、DB、API |
| GC-25 | P1 | 月末、跨年、闰年和时区边界续期 | 遵循现有订单日历规则，预览与实际一致 | DB |
| GC-26 | P1 | 桌面/移动视口、键盘、弹窗、错误和成功状态 | 无关键遮挡，焦点和重复提交状态正确 | UI |
| GC-27 | P1 | 兑换超过15次后查历史 | 能访问旧记录或明确说明产品限制 | UI、组件 |
| GC-28 | P1 | 已发码后尝试改字符/次数/模板类型或破坏引用 | 服务端保护单次限制、快照和账本关系 | DB、API、UI |
| GC-29 | P1 | 在线购权益和购买码权益混合后尝试升级抵扣 | 不把未核验外部权益换成面板退款或佣金 | DB |
| GC-30 | P1 | schema67有旧记录时升级68并核对关系 | 旧模板/码/记录完整，购买码可用 | DB |

## 执行命令与结果要求

```powershell
go test ./internal/store ./internal/httpapi -run 'TestGift' -count=1 -json
pnpm --dir web exec vitest run src/features/giftcards/GiftCardManagementPage.test.tsx src/features/giftcards/UserGiftCardPage.test.tsx src/features/orders/OrdersPage.test.tsx
```

执行日志记录确切命令、退出码、通过/失败/跳过数量、耗时、平台和代码SHA。覆盖率未采集时明确标注，不以通过数量替代覆盖率。现有测试复跑没有修复前失败证据，不声明 Red→Green。

## 实测结论（2026-10-04）

**验收未通过。** 线上主购买链路已实际执行，发现三项可重复问题：查询换码后旧预览覆盖当前输入、兑换历史只能查看最近15条、390px窄屏历史表格横向溢出。下表逐项区分线上、本地及未运行分支；不把本地通过数量折算成业务用例通过率。

收尾阻塞：QA切换使管理员会话退出，已请求用户重新登录，尚未得到恢复确认。后续管理标签刷新又出现`ERR_SSL_PROTOCOL_ERROR`，错误页被浏览器工具URL策略阻止操作；已停止对此错误页操作，没有绕过证书校验。同一时段`curl.exe --silent --show-error --fail --max-time 20 https://fast.hjy.ca:8443/healthz`退出0并返回`data.status=ok`。这是浏览器连接/控制阻塞，不据此判定整个服务故障，亦不影响先前已完成兑换的证据。

### 线上夹具与权益结果

| 夹具 | 初始状态 | 实际结果 |
| --- | --- | --- |
| 用户#8，a2 | 无套餐，额度0 | 首次流量兑换后套餐#1，200GiB、已用0、永久有效；1条兑换记录、1笔已完成订单 |
| 用户#9，b2 | 套餐#1，额度200GiB、已用150GiB；1笔QA待付订单 | 待付订单阻止兑换；取消后兑换得到400GiB、已用150GiB、剩余250GiB；原QA订单取消，无真实付款 |
| 用户#10，c2 | 无套餐 | 两张旧月卡与14张新季卡，共16次成功兑换；额度保持100GiB，截止2030-06-04；16笔用户订单，历史页仅15条 |
| 用户#11，d2 | 套餐#2，额度150GiB、已用25GiB，2026-10-01过期 | 旧月卡从当前时间续到2026-11-04；再完成断网恢复重试及窄屏兑换，各增加一条历史，已用25GiB保留 |
| QA套餐#2 | `QA-GIFT-20261004-CYCLE`，初始100GiB，月/季/一次性价格用于QA | 发旧码后改为150GiB；show=false、sell=false、renew=true，未向普通新购列表公开 |
| 模板#2 | TRAFFIC，套餐#1、type4、一次性 | 5张码；其中2张已用、1张可用、1张禁用、1张设置为过期状态，待后台复核 |
| 模板#3 | MONTH，套餐#2、type4，初始月付 | 发6张旧月卡后改季付，再发14张季卡；旧码100GiB/月，新码150GiB/季，快照独立 |
| 模板#4 | MODE，套餐#2、type4、一次性 | 2张100GiB流量码用于模式不一致拒绝测试，未消费 |

用户#4至#7是前一轮工具会话中创建的QA夹具；凭据因工具会话重置不可用，未用于本轮最终兑换。#5有一笔合成待付订单。未删除账号、模板、码、订单或审计记录。原有非QA用户和套餐#1配置未修改。

数据处置待办：早期QA账号#4的测试口令曾进入工具超时诊断输出，不能用于正式账号；后台重新登录后应禁用这个未使用的QA账号。没有将该口令保存到本报告、截图或Git。

### 30项用例执行矩阵

PASS仅表示该行明确列出的层级和分支；PARTIAL表示原计划仍有未运行子场景。

| 用例 | 结果 | 实际证据与未覆盖分支 |
| --- | --- | --- |
| GC-01 | PASS | UI创建周期模板、保存重开，月付改季付后新码预览为3个月 |
| GC-02 | PASS | UI创建流量模板并生成5张单次码；200GiB不限时实际开通 |
| GC-03 | PASS | c2首兑旧月卡得到100GiB，到期2026-11-04，历史与订单存在 |
| GC-04 | PASS | 第二张月卡从11-04续至12-04；14张季卡续至2030-06-04，配额100GiB未变；DB保留用量测试通过 |
| GC-05 | PASS | d2过期状态从当前时间续至11-04，150GiB总额及25GiB用量保留 |
| GC-06 | PASS | a2真实首兑：0→200GiB，永久有效；用户订单和历史均1条 |
| GC-07 | PASS | b2真实200/150→400/150GiB，剩余250GiB |
| GC-08 | PASS | c2有效、d2过期两状态查询其他套餐码均拒绝；本地DB拒绝与不消费断言通过 |
| GC-09 | PASS（分层） | UI周期→流量拒绝；补充DB流量→周期拒绝并核对码、权益、订单、记录、事件无副作用 |
| GC-10 | PASS（分层） | UI待付阻止，取消本轮QA待付订单后可兑换；补充DB processing拒绝，原订单状态及码/权益不变 |
| GC-11 | PASS | UI已用码不再出现确认按钮，权益及订单不增加；API同用户重发幂等通过 |
| GC-12 | PASS | b2查询a2已用码显示无剩余次数，无a2身份信息；API跨用户复用拒绝通过 |
| GC-13 | PASS（DB） | 两个SQLite Store、8并发，唯一赢家；扩展测试核对1次码核销、1笔订单、1条usage、1条权益事件。赢家200→400GiB、输家仍200GiB，双方已用150GiB保留；同一赢家重试允许返回既有成功结果 |
| GC-14 | PARTIAL | 用户端订单、历史、权益对应；订单详情明确站内收款0、外部金额未核验；最后管理端核销对账待重新登录 |
| GC-15 | PASS | UI发旧月码后改套餐100→150GiB、模板月→季；旧码仍100/月，新码150/季；既有周期额度保留 |
| GC-16 | PASS（已列分支） | 空码禁用查询；不存在、禁用、过期状态码均明确拒绝；DB/API无副作用测试通过。UI过期卡使用状态设置，未冒充自然时间越界 |
| GC-17 | PASS（DB） | 订单、使用记录、权益事件三个写入失败点回滚，恢复后可重试 |
| GC-18 | PASS（API） | 普通用户管理拒绝、通用session/CSRF、分销allowlist通过；Cookie/Bearer两种身份跨用户列表及两种详情隔离，用户查询/兑换与管理员建模板/制码缺CSRF拒绝；真实过期Session Cookie访问5路由均401且无副作用 |
| GC-19 | PARTIAL（本地PASS） | UI空值、小写有效码、不存在码；DB空白/7/33/标点/内部空格/非ASCII拒绝，8/32位合法格式不存在码为NotFound，大小写及首尾TrimSpace规范化查询与实际兑换；8/32位合法旧卡实际兑换通过。浏览器原生pattern与首尾空格等全边界未逐项操作，不称前后端全部一致 |
| GC-20 | PARTIAL | UI码禁用与过期状态拒绝已测；重新启用后的用户兑换未运行 |
| GC-21 | FAIL | 线上1800ms延迟下查询A后改B，返回A预览且确认可用；本地受控组件进一步证明确认实际提交B。未用线上错误预览强制消费B |
| GC-22 | PASS（已列分支） | UI断网查询/兑换均显示Failed to fetch；恢复网络重试新增恰好1条记录，无伪成功。提交已成功但仅响应丢失的精确故障窗口未注入 |
| GC-23 | PARTIAL | 本地API导出测试通过；浏览器下载等待未取得文件，不将点击导出算通过 |
| GC-24 | PASS（DB/API） | 本轮Go旧类型专项通过；三个旧类型各自的线上UI兑换未运行 |
| GC-25 | PASS（共享算法） | 独立具体日期断言覆盖月末溢出、闰年及重置日历；完整购买码跨时区链路未单独运行 |
| GC-26 | FAIL | 桌面查询、成功、失败可操作；390px窄屏真实查询/兑换成功，但表格490.36px导致横向溢出；完整键盘可访问性未覆盖 |
| GC-27 | FAIL | c2真实16次兑换，订单16条；刷新重进礼品卡历史只15条，唯一按钮为查询奖励，无分页，首单不可见 |
| GC-28 | PASS（DB） | 单次限制、码/快照不可变、模板类型与套餐/组引用保护测试通过；线上逐项管理表单修改未全部运行 |
| GC-29 | PASS（DB） | 购买码及在线权益混合抵扣隔离专项通过；不进行真实在线付款 |
| GC-30 | PASS（DB） | schema67→68、重复Migrate、旧记录与外键完整；扩展测试在同一升级后DB新建type4并发码、兑换，旧余额/记录及外键保持 |

### 缺陷与复现

**QA-GIFT-01，High：输入码与奖励预览可不一致。**

1. 普通QA用户打开礼品卡页，浏览器标签网络延迟设为1800ms。
2. 输入A（月卡），点击查询奖励；请求未返回时改输入B（另一套餐流量卡）。
3. A返回后，输入框仍为B，页面显示A的100GiB/月权益并启用确认。
4. 本地真实组件受控API测试中点击确认，实参为B，正确性断言失败。

影响：用户确认的权益与实际提交码不一致，支付确认交互不可信；不表示服务端套餐限制被绕过。线上未点击该不一致预览消费B。根因定位在 `web/src/features/giftcards/UserGiftCardPage.tsx` 的旧查询响应未校验当前输入、确认读取当前code。证据：`GC21-live-stale-preview`、`stale-preview.jpg`、`repro/run.log`。本轮未修复，无Green结果。

**QA-GIFT-02，Medium：兑换记录超过15条后旧记录不可访问。**

c2完成16次兑换，用户订单页显示16笔（含首单`2026100418120448443122458`），礼品卡历史页刷新后仅15条且无分页。影响用户对账和追溯，未发现订单数据丢失。证据：`GC27-history-limit`、`GC27-order-crosscheck`、`history-limit.jpg`。

**QA-GIFT-03，Medium：移动端兑换历史横向溢出。**

通过CDP设置390×844移动视口，main宽390px，历史表宽490.36px，document/body宽490px。查询、确认和成功流程仍可操作，但历史表未限制在容器内。证据：`GC26-mobile`、`mobile-390.jpg`。普通viewport capability首次设置未实际改变1280px页面，因此未将该次截图冒充移动测试；最终采用生效的CDP视口测量。测试后已恢复网络及视口设置。

低优先级观察：网络失败直接显示英文`Failed to fetch`，反馈准确但缺少中文重试指导。

### 自动化执行证据

| 命令/范围 | 状态/退出码 | 通过/失败/跳过 | 时间与环境 |
| --- | --- | --- | --- |
| `go test ./internal/store ./internal/httpapi -run 'TestGift' -count=1 -json` | PASS / 0 | 32顶层；48含子测试通过事件；0失败0跳过 | store3.920s、httpapi1.370s，墙钟9.538s；Windows Go1.26.8，隔离SQLite |
| `pnpm --dir web exec vitest run src/features/giftcards/GiftCardManagementPage.test.tsx src/features/giftcards/UserGiftCardPage.test.tsx src/features/orders/OrdersPage.test.tsx --reporter=verbose --reporter=json --outputFile.json=../docs-dev/gift-card-qa-20261004/vitest-results.json` | PASS / 0 | 3文件30测试；0失败0跳过 | 26.06s、墙钟28.433s；Node24.12.0，pnpm11.10.0，Mock API组件 |
| `pnpm --dir web exec vitest run --config ../docs-dev/gift-card-qa-20261004/repro/vitest.config.mjs --reporter=verbose --reporter=json --outputFile.json=../docs-dev/gift-card-qa-20261004/repro/results.json` | FAIL / 1 | 0通过1失败0跳过 | 3.75s、墙钟5.734s；真实组件+受控查询Promise，目标缺陷复现 |
| `go test ./internal/store -run 'TestAddOrderMonthsMatchesLegacyCarbonOverflow\|TestCalculateNextTrafficResetMatchesLegacyCalendarRules' -count=1 -json` | PASS / 0 | 2顶层、7含子测试事件；0失败0跳过 | 包0.240s、墙钟6.157s |
| `go test ./internal/httpapi -run 'TestAdminAPIRequiresSessionAndCSRF\|TestDistributorAccessIsAnExplicitServerSideAllowlist' -count=1 -json` | PASS / 0 | 2通过0失败0跳过 | 包1.065s、墙钟7.618s |
| `go test '-overlay=docs-dev/gift-card-qa-20261004/supplement/overlay.json' ./internal/store ./internal/httpapi -run '^TestQAGift' -count=1 -json` | PASS / 0 | 4顶层、10含子测试事件；0失败0跳过 | store1.230s、httpapi1.634s，墙钟22.068s；隔离SQLite和实际HTTP入口 |
| `go test '-overlay=docs-dev/gift-card-qa-20261004/supplement/expanded-overlay.json' ./internal/store ./internal/httpapi -run '^TestQAGift' -count=1 -json` | PASS / 0 | 9顶层（原4+新增5）、35含子测试事件；0失败0跳过 | store1.580s、httpapi1.430s，墙钟17.531s；03:23:50–03:24:08；原4不可重复累计为新用例 |

首次Go/组件与缺陷复现的代码基线为HEAD `883fee99`。03:13补充日历/权限测试时发现其他工作正在修改 `internal/httpapi/gift_card_handlers.go` 并新增 `gift_card_code_identity_test.go`；未覆盖或暂存这些文件。补测元数据保存当时路径、修改时间与blob hash，不能称纯净HEAD验证。日历包不受HTTP改动影响；03:18至03:19的4项新增否定测试通过Go overlay加载`docs-dev/.../supplement/`内测试，不改写源码目录，HTTP包同样包含当时其他工作的改动。线上页面显示schema68，但精确应用SHA仍未知。

扩展轮的9项测试也使用overlay；`expanded-*`保存独立日志、执行统计和前后源码hash，不覆盖前一轮4项结果。前后源码hash一致。首轮overlay命令曾因PowerShell参数未加引号而在测试启动前失败，修正参数后执行；该工具配置错误单独保留`setup-attempt-1.*`，不冒充产品Red→Green。

覆盖率：本轮未采集行/分支覆盖率。race、性能容量测试NOT RUN；本轮为功能验证，无性能达标结论。Red→Green：仅有QA-GIFT-01的Red证据，尚无产品修复和Green。

订单日历采用既有Carbon溢出语义，例如2025-01-31加1个月为2025-03-03、2024-01-31加1个月为2024-03-02；不自行改成月底钳制预期。

### 证据目录与边界

本轮原始记录位于 `docs-dev/gift-card-qa-20261004/`：浏览器逐步脱敏证据为`browser-results.jsonl`；自动化为`go-gift-*`、`vitest*`、`calendar.*`、`auth-extra.*`、`repro/`及`supplement/`。截图为`order-detail.jpg`、`user-first-redemption.jpg`、`history-limit.jpg`、`stale-preview.jpg`、`mobile-390.jpg`。`mobile-redeem.jpg`实际是默认1280px，不能作为移动证据。

首次兑换的`GC06-first-redeem`只证明成功提示，当时历史尚未刷新；后续`GC06-entitlement`、`GC14-user-order`、`GC11-repeat-query`补齐权益/订单/历史终态。一次续期曾读取旧状态，已在`GC04-active-cycle-entitlement`用CORRECTION撤销旧解释，再等待记录后确认到期延长；不是数据丢失结论。

测试数据保留供核查，不包含真实收款。原管理员标签的未提交表单未改写。本轮不提交产品代码、不推送、不部署，不提升治理JSON的业务验收状态。测试发现的已知High问题未修复，不能给出可直接作为主要支付方式上线的通过结论。

### 尚未完成的线上收尾

- GC-14：管理员按QA账号/批次核对码核销次数、使用记录与21笔购买码订单；需恢复管理员登录和浏览器页面。
- GC-19：在浏览器补首尾空格、长度端点及原生输入约束，确认与已通过的后端规范化行为一致。
- GC-20：重新启用本轮禁用码后，用户侧确认恢复可兑。
- GC-23：取得浏览器实际下载文件并核对导出数量、唯一性、模板；本地API导出PASS不替代这一项。
- GC-24：旧通用/套餐/盲盒三个类型的线上UI兑换分支；已有DB/API兼容证据。
- GC-26：修复溢出后重测移动页面，并补完整键盘/焦点检查。
- 线上精确应用SHA尚未核实；不把本地工作树测试等同于当前线上版本证明。

因此，本报告交付了方案、30项用例及已运行证据，但**30项完整计划仍未全部收尾**。已知缺陷的修复与复测、管理员收尾和上述未运行分支完成前，不标记全面功能验收通过。

## 授权修复与本地复测（2026-10-04）

用户明确要求针对已发现bug制定方案并修复。本节采用快速执行模式，范围限于三项已复现前端缺陷及回归证据，不改变购买码后端权益规则。关联现有GIFT-001/GIFT-002/PAY-001、VER-002（Issue 120，M3）；不新建需求ID，不提升治理记录的线上验收状态。开始修复的HEAD为`338833a87a328f68fc6edcbff4fe4e02c957cb7d`，分支为`codex/ci-001-tiered-gates`。

### 修复方案、实现与验收矩阵

| 缺陷/风险 | 修复方案与实际行为 | 回归证据 | 当前状态 |
| --- | --- | --- | --- |
| QA-GIFT-01：A预览与B提交错配 | 每次输入编辑/新查询作废旧请求；预览绑定查询时的规范化兑换码；确认仅提交绑定码。迟到成功、失败及finally不得覆盖当前状态 | 受控Promise+React act验证A→B→A、新B先返回及旧A成功/失败；断言实际提交码；真实浏览器查询与兑换链路 | 本地PASS，线上待部署复测 |
| QA-GIFT-01：快速重复点击或兑换中改码 | 同步提交锁覆盖快速连击；兑换中禁用输入/查询/确认；请求失败后恢复人工重试 | 双击只调用一次兑换、输入锁定、失败提示及重试组件测试 | 本地PASS |
| QA-GIFT-02：15条后记录不可访问 | 增加前后页、页码、总数和边界禁用；请求失败保留当前记录并支持重试目标页；兑换后回第一页，旧翻页响应不得覆盖刷新 | 组件验证翻页错误/重试/竞态；实际Go+SQLite生成16条历史，UI翻页找到最早订单，再兑换至18条 | 本地PASS，线上待部署复测 |
| QA-GIFT-03：390px历史溢出 | 复用现有响应式resource-table；移动端按字段展示卡片，长模板、奖励和订单号允许换行；不隐藏页面溢出掩盖问题 | Chromium 1280×900与移动模拟390×844；真实长文本、字段标签和订单；document横向溢出≤1px；人工检查两张视口截图 | 本地PASS，线上待部署复测 |
| 权益回归 | 本次不改变后端发放；浏览器专项核对真实订阅响应 | 每个浏览器场景18次200GiB购买码兑换后总额度3600GiB、套餐一致、无到期日；最后两次由UI完成 | 本地PASS |

实现文件：`web/src/features/giftcards/UserGiftCardPage.tsx`、`UserGiftCardPage.css`。测试文件：同目录`UserGiftCardPage.regression.test.tsx`及`web/e2e/zz-gift-card-regressions.spec.ts`。样式仅限定礼品卡页，未改共享样式或支付/权益后端。

### Red → Green与执行结果

| 命令 | 状态/退出码 | 通过/失败/跳过 | 耗时与环境 |
| --- | --- | --- | --- |
| `pnpm --dir web exec vitest run src/features/giftcards/UserGiftCardPage.regression.test.tsx --reporter=json --outputFile=../docs-dev/gift-card-qa-20261004/fix-red.json`（修改产品前） | FAIL / 1 | 0/6/0 | JSON开始至最后结果10.70s；失败为旧预览、缺分页等目标行为，不是环境错误 |
| `pnpm --dir web exec vitest run src/features/giftcards/UserGiftCardPage.test.tsx src/features/giftcards/UserGiftCardPage.regression.test.tsx --reporter=json --outputFile=../docs-dev/gift-card-qa-20261004/fix-green.json`（修复后） | PASS / 0 | 11/0/0 | JSON开始至最后结果11.77s；Windows、Vitest、真实React组件+受控API |
| `pnpm --dir web exec vitest run src/features/giftcards/GiftCardManagementPage.test.tsx src/features/giftcards/UserGiftCardPage.test.tsx src/features/giftcards/UserGiftCardPage.regression.test.tsx src/features/orders/OrdersPage.test.tsx --reporter=json --outputFile=../docs-dev/gift-card-qa-20261004/fix-regression.json`（最终组件回归） | PASS / 0 | 36/0/0 | JSON开始至最后结果31.01s；4文件，包含原30项及新增6项 |
| `pnpm --dir web exec playwright test e2e/zz-gift-card-regressions.spec.ts --project=chromium --project=mobile-chromium '--reporter=list,json'` | PASS / 0 | 2/0/0，无重试或flaky | 16.73s；2026-10-04 11:21:32Z启动；本机Vite构建产物、实际Go HTTP服务、隔离SQLite、Chromium |
| `pnpm --dir web run typecheck` | PASS / 0 | 不适用 | TypeScript；单独检查通过，构建亦包含tsc |
| `pnpm --dir web run lint` | PASS / 0 | 不适用 | ESLint完整检查；未加规则豁免 |
| `pnpm --dir web run build` | PASS / 0 | 不适用 | tsc与Vite构建；Vite阶段343ms，命令总耗时未单独存档 |
| `git diff --check` | PASS / 0 | 不适用 | 无空白错误；Windows行尾提示不影响结果 |

Playwright执行前设置`$env:PLAYWRIGHT_JSON_OUTPUT_FILE='../docs-dev/gift-card-qa-20261004/fix-e2e.json'`。账号、套餐、模板、码全部为本机合成夹具，测试明确拒绝非localhost/127.0.0.1主机；未向线上新增修复夹具。浏览器测试仅延迟真实查询响应以模拟竞态，兑换和权益接口使用实际应用入口。受控组件测试提供迟到响应处理的确定性证据；浏览器`response.finished()`不单独等同于React完成消费，因此E2E的即时否定断言只计为链路辅助证据。

### 执行中失败、审查与边界

- 既有`zz-gift-cards.spec.ts`与新专项首次合跑4项均在账号准备阶段失败：旧共享helper要求管理页可见“退出”按钮，当前管理菜单不满足这个选择器。旧脚本未进入兑换流程，仍标FAIL（前置脚本不兼容），不冒充产品回归通过；原始记录为`fix-e2e-baseline-failure.json`。本次专项改为使用真实管理API创建本地夹具，再以实际普通用户界面完成兑换。
- 新专项准备过程曾错误断言退出HTTP 200（实际契约204），以及在移动端未先打开折叠菜单；已修正测试预期与导航步骤，保留`fix-e2e-fixture-failure.json`和`fix-e2e-mobile-navigation-failure.json`。两项最终正常首跑通过，不用重试掩盖失败。PowerShell reporter参数首次解析失败发生于测试启动前，亦不计产品Red。
- 独立只读审查覆盖码与预览绑定、重复提交锁、历史请求顺序、失败重试和CSS范围，未发现阻塞问题；审查提出的E2E迟到响应证据限制已在上文披露。
- 原始JSON与截图保存在忽略的`docs-dev/gift-card-qa-20261004/`，修复截图为`fix-desktop.png`、`fix-mobile.png`。不将原始运行产物、真实凭据或用户无关文件纳入提交。
- 本次未采集行/分支覆盖率，未跑全量前端E2E、race、容量或跨浏览器内核测试；没有修改Go，因此本修复轮未重复全量后端测试。已验证的是上述三项缺陷及其受影响路径。
- 未推送、未创建PR、未部署。线上QA-GIFT-01/02/03仍以首轮失败状态保留，需按CI流程发布后重跑；管理员对账、启用/导出及原计划的其他未完成项不因本地修复而自动关闭。
