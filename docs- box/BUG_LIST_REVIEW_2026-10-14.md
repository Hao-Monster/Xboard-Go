# Bug 报告二次复查与待修复清单

复查日期：2026-10-14（America/Los_Angeles）。本文件只记录审查结论，没有实施业务修复。

## 1. 结论与证据边界

原报告的“25 个确凿且可复现 Bug”不能原样采纳：25 个编号中，**6 项主要断言成立，12 项部分成立，7 项不应按原描述录入已确认生产 Bug**。复合条目拆分后，本清单收录 **29 个原报告相关缺陷，以及 1 个复查时发现的独立缺陷，共 30 项**。其中包括明确标注的异常数据、极端累计值及非标准导入边界，不能都理解为日常必现问题。

- 优先级：P1/High 4 项、P2/Medium 20 项、P3/Low 6 项；没有沿用原报告的 Critical 结论。
- 证据：5 项缺陷已有本轮隔离组件诊断复现，其余 25 项是当前源码调用链确认，尚未执行各自的专项动态反例。
- 新增 7 个组件诊断用例：6 个验证当前缺陷现象，1 个正常行为对照。**诊断 PASS 表示现象得到复现，不表示产品已修复或通过验收。**
- 已明确反证的例子：分销结算 SQL 顺序、V2 应用配置泄漏节点、购买码过期续期语义、0 天套餐卡代表永久、遗漏 Salamander、Cookie 调用旧邀请码生成接口。
- “取消优惠券应返次数”“未知客户端版本应放行高级特性”“敏感设置应排除自动保存”等属于业务或交互决策，不能在审查中擅自改写规则。

### 审查基线

| 项目 | 基线 |
| --- | --- |
| 仓库 | `E:\CodeWorkstation\Xboard-Go` |
| 分支 | `codex/ci-001-tiered-gates` |
| HEAD | `07476e0229efd7ed326843c67dc611c7b1366c1a` |
| 原报告 | `%USERPROFILE%\.codex\attachments\b04abb4b-959d-4d35-a26e-ceee07cf229b\已粘贴的文本.txt`（本机取证路径，用户目录已脱敏） |
| 原报告 SHA-256 | `e6fd6ad9ffcb6169aa25eff60faa9d53d72ee55f4fa977762631ee357705040c` |
| 已有暂存改动 | `docs/project/frontend-parity-2026-10-01.json`、`web/src/App.test.tsx`、`web/src/App.tsx`、`web/src/styles.css`；均来自上一任务，本轮保留 |
| 既有未跟踪文件 | `docs/HANDOVER.md`；未作为当前证据、未改动 |
| 范围 | 当前工作区源码、明确的产品约定、旧 PHP 对应实现、既有隔离测试、附带组件诊断 |
| 不包含 | 生产操作、线上故障发生率、真实付款/邮件/Telegram、真实客户端联网、Redis 集群故障注入、Docker 回滚操作 |

文中的 `BL-xx` 仅为本次审查编号，不是伪造的项目 Requirement/Work Item。源码位置以本轮当前工作区为准；下文不带盘符的路径均相对上述仓库根目录，旧 PHP 路径另行注明。

“静态确认”表示条件、入口和错误结果可由代码链条推出；不表示在生产发生过。“NOT RUN”表示对应专项反例未执行。测试命令、数量和限制见同目录 [测试证据](TEST_RESULTS_2026-10-14.md)。

## 2. 原报告 25 项逐项裁定

| 原编号 | 裁定 | 可采纳部分 / 主要纠正 |
| --- | --- | --- |
| 1 | 不成立：代码误读 | 当前先更新订阅兼容字段，再更新订单 `paid_at`，不存在报告所述顺序。独立展示问题另列 BL-30。 |
| 2 | 部分成立 | 即时权益通知遗漏成立：BL-01。关闭交付不等于吊销订阅，不能推定为撤权漏洞。 |
| 3 | 部分成立 | 活跃支付仍可能被超时取消：BL-02。余额只返还一次，不成立“重复退款”。 |
| 4 | 现有规则 / 待业务确认 | 取消不恢复全局券次数有明确测试约定；未付款耗尽风险需风控决策，不直接算 High 实现错误。 |
| 5 | 不成立：功能误解 | 购买码拒绝跨套餐/模式、过期同套餐只延时、保留流量和已有重置计划，均有设计依据。 |
| 6 | 部分成立 | 永久订阅被天数奖励改为限时：BL-03。原文引用的 `PreviewGiftCard/canExtendExpiry` 不存在。 |
| 7 | 不成立：功能误解 | 礼品卡 0/留空天数表示保留用户原到期时间，不表示授予永久权益。 |
| 8 | 成立，限特定组合 | 过期跨套餐管理员派单可能保留旧用量：BL-04；不是所有派单都会发生。 |
| 9 | 部分成立 | 异常扫描早返未显式关闭 Rows：BL-05。普通 HTTP 请求有 context 回收，不能声称 8 次即永久死锁。 |
| 10 | 部分成立 | 累计月份溢出和永久套餐链式折抵基数缺失：BL-06/07。单笔 36 个月的溢出算式错误。 |
| 11 | 部分成立 | Legacy V1 的 HWID、业务标记、成本限流缺口：BL-08/09/10。V2 仅应用设置；无效 token 已有限流。 |
| 12 | 现有兼容策略 / 待确认 | 空版本保守过滤有 PHP parity 和 oracle 测试依据；不等于 Go 新回归，也非所有 Meta Hysteria2 都被删。 |
| 13 | 成立 | 首节点兼容过滤连带删除虚拟提示：BL-11。仅提示节点丢失，响应头等元信息并未全丢。 |
| 14 | 部分成立 | Shadowrocket 将 Hysteria1 编成 Hysteria2：BL-12。混淆参数已有；名称编码另列边界风险。 |
| 15 | 正常生产路径未证实 | 空 AppName helper 有鲁棒性缺口，但 DB/API/迁移约束不允许空名称，不按生产模板损坏录入。 |
| 16 | 成立 | 跨实例漏同步、解绑失败漏清理：BL-13/14。残留存在 TTL，不是永久。 |
| 17 | 部分成立 | 两类凭证 ID 混用：BL-15。Cookie 邀请 CSRF 被反证；login-CSRF 未证；代理 IP 已在当前 HEAD 修复。 |
| 18 | 成立，限取消窗口 | claim 后、发送前取消缺少收敛：BL-16。发送后正常完成的路径可以终结。 |
| 19 | 部分成立 | 单坏记录导致佣金/超时订单批次回滚：BL-17/18。可纠正数据或状态，其他调度任务不会全局停摆。 |
| 20 | 部分成立 | 特定 backend 回滚失败 journal 与运行状态不符：BL-19。不是所有 rollback_failed 都无法 status，也不是绝对不可人工恢复。 |
| 21 | 部分成立 | Telegram 第三次失联及批次时间冻结：BL-20/21。认证邮件有凭据到期取消路径，不是永久 pending。 |
| 22 | 部分成立 | 非整分钟导入接受但运行时截断：BL-22。标准旧流量表时间为 Unix integer，日期字符串必坏论据不足。 |
| 23 | 现有交互 / 待产品确认 | 1000ms 自动保存是已有实现约定；安全路径与旧节点密钥是否要显式确认应单独决策，不直接列已确认 High。 |
| 24 | 成立 | 多层弹窗没有统一栈顶 Escape 仲裁：BL-23。已挂起父层的关闭确认路径是正常反例。 |
| 25 | 成立，拆分影响 | 元数据竞态、尾页删除、详情竞态、状态残留、缓存失效、新组名同步：BL-24～29；均需限定恢复范围。 |

## 3. 已确认缺陷清单

### BL-01 · P1 · 权益写入后未正确通知在线节点（原 #2）

- **触发与影响：** 支付回调、零外部应付结账、管理员标记付款、分销新购/续费/权益修改成功后，WebSocket 节点可能继续使用旧用户列表、额度或权限组。分销实际节点身份是 `subscriber_user_id`，不能误用分销商的财务订单用户 ID。
- **证据：** `internal/httpapi/payment_handlers.go:156` 向 `NotifyUserMutation` 传两个 nil 组；`websocket.go:1543-1553` 在发布组刷新前返回。其他遗漏入口位于 `order_handlers.go:100,271,606`、`distributor_handlers.go:67,104,247,436,522`。
- **边界：** 可能仍清理设备缓存，不是完全无动作；重连的全量快照、HTTP 拉取或其他正确同步能够恢复。没有证明全部节点永久不可用。
- **验证 / 验收：** 静态确认，付款→真实 WS 通知专项 **NOT RUN**。修复验收应覆盖新旧组、分销内部账号、本地及跨副本通知，证明节点收到正确用户增删/额度变更。

### BL-02 · P1 · 超时取消未协调活跃外部支付尝试（原 #3）

- **触发与影响：** 订单创建两小时临界前发起付款，超时任务先将 pending 取消，之后网关成功回调到达，订单已不满足可完成状态，不能开通权益。
- **证据：** `internal/store/orders.go:838-864` 没有活跃支付过滤；手动取消在 `:724-729,757-763` 有保护。`payment_checkout.go:32,127` 可启动仍为 pending 的支付；`:308-309` 的回调遇已取消订单返回状态错误并回滚。
- **边界：** `orders.go:891-907` 先条件更新，再返还余额，重复退款断言不成立。
- **验证 / 验收：** 既有超时和支付测试分别 PASS，但交叉竞态 **NOT RUN**。用隔离网关替身覆盖临界付款、迟到成功、失败/过期尝试及幂等；不要以无限延长 pending 代替支付状态协调。

### BL-03 · P1 · 通用/盲盒天数奖励将永久订阅降为限时（原 #6）

- **触发与影响：** 已有 `plan_id` 且 `expired_at=NULL` 的永久订阅兑换附加天数，结果改为 `now + 天数`，后续可能因到期而失效；“延长有效期”反而缩减权益。
- **证据：** `internal/store/gift_cards.go:1443-1444` 未排除永久账号；`:1582-1588,1614-1618` 计算和持久化有限时间；盲盒子奖励入口见 `:1195-1198`。到期限制见 `internal/store/models.go:970-971`、`runtime.go:275,303`。
- **验证 / 验收：** 静态确认，永久用户兑换专项 **NOT RUN**。分别测试通用卡、盲盒、有限期有效/过期、无套餐用户；永久权益不得因附加天数被缩短。

### BL-04 · P2 · 过期用户跨周期套餐派单仍按升级保留旧用量（原 #8）

- **触发与影响：** 旧套餐过期且有旧流量、管理员分配不同周期套餐、`change_order_event_id=0`。旧用量可能直接超过新套餐额度，造成新开通权益无可用流量。
- **证据：** `internal/store/orders.go:625` 派单分类只看不同 `plan_id`；普通新购在 `:146-169` 区分有效期。`:1003-1007` 的升级处理阻止 New 分支重置用量。
- **边界：** onetime 在 `:992-993` 重置；升级事件配置为 1 时 `:1026-1034` 也重置；同套餐过期派单不属于此组合。没有普通用户越权。
- **验证 / 验收：** 静态确认，组合反例 **NOT RUN**。统一过期派单分类并验证新旧额度、同/跨套餐及事件配置；如产品有意让管理员跨套餐永久沿用量，需先补明确规则再重判。

### BL-05 · P3 · 分销列表扫描失败未显式关闭 Rows（原 #9，条件性资源缺陷）

- **触发与影响：** 合法 JSON 数组但元素不是 int64 的异常订单等导致扫描失败，且调用 context 长期不取消，数据库连接可能滞留。
- **证据：** `internal/store/distributor_queries.go:122-126` 早返漏 Close；正常结束在 `:130-135` 关闭；解码在 `orders.go:1360-1369`。
- **边界：** 普通创建/迁移不产生这种数组；生产 handler 传 `r.Context()`，Go `database/sql` 在 context 取消时关闭 Rows，HTTP handler 返回会取消请求。因此不接受“8次普通请求即永久耗尽连接池”。
- **验证 / 验收：** 静态确认；损坏数据+连接统计专项 **NOT RUN**。用隔离数据库和可控 context 验证扫描出错后连接立即归还。

### BL-06 · P2 · 流量感知折抵累计月份乘法缺少溢出保护（原 #10a，极端边界）

- **触发与影响：** 开启换套餐/折抵且 `change_order_event_id=1`，极大流量套餐累积超过 1024 个月，`int64` 乘法回绕，可能得到 0 折抵。
- **证据：** `internal/store/orders.go:1145-1172` 累计多笔未折抵在线周期订单；`:1192-1203` 做流量乘累计月份；负分母在 `:1531-1545` 影响结果。
- **算术纠正：** `8,388,607 × 2^30 × 36 = 324,259,134,515,970,048`，小于 `9,223,372,036,854,775,807`，三年付本身不溢出。29 笔三年付累计 1044 个月才构成此示例：乘积 `9,403,514,900,963,131,392` 超界。
- **验证 / 验收：** 精确整数算术已核对；29 笔实际订单链 **NOT RUN**。测试 1024/1025 月边界、普通套餐及精确金额，乘法不可先溢出再交给大整数除法。

### BL-07 · P2 · 永久套餐链式升级遗漏上次转入的净折抵价值（原 #10b）

- **触发与影响：** A 剩余价值 10000 分，抵扣升级到 B 永久套餐 20000 分，再支付 10000 分；B 再升级时却只以新增实付 10000 分作为价值基数，遗漏上次继承的折抵。
- **证据：** `internal/store/orders.go:1126` 的 onetime 分支只取 `total_amount+balance_amount`；周期分支 `:1167` 包含 `surplus_amount-surplus_credit`；前次折抵扣外部应付见 `:160-165`。
- **边界：** 此处针对在线订单；购买型礼品卡混入权益的场景在 `:1114-1120` 被禁止，不能作为该例。
- **验证 / 验收：** 静态公式确认，完整链式订单 **NOT RUN**。覆盖零用量、部分用量、超额返余额及多次升级，验证总剩余价值守恒。

### BL-08 · P1 · Legacy V1 配置下发绕过分销 HWID 门禁（原 #11b）

- **触发与影响：** 有效且可用、存在可下发节点的分销内部订阅已开启 HWID 限制，直接用其 token 走 Legacy V1 配置接口；缺失/非法 HWID，或登记名额已满时使用新的 HWID，仍可生成节点配置，且不执行设备登记和计数。
- **证据：** `internal/httpapi/client_app_config_handlers.go:17-37,81-104` 直接认证、加载、渲染；`internal/store/subscriptions.go:213-220` 显式接受 `internal_subscription`。正常订阅入口 `subscription_handlers.go:97-109` 则执行 HWID 门禁；对应项目约定 `DIST-011`。
- **边界：** 配置下载限制绕过不等于绕过节点运行时并发设备限制，更不等于无限联网；V2 只下发应用设置，不属于此缺陷。
- **验证 / 验收：** 静态可达性确认，Legacy V1+分销 HWID 的专项 HTTP 用例 **NOT RUN**。应对所有真实配置下发入口验证缺失/非法、已登记、新设备尚有名额、名额已满等相同策略；正常入口允许有名额的新有效 HWID 自动登记，不能误判该行为为绕过。

### BL-09 · P2 · 仅走 Legacy V1 时遗漏领取/配置下发业务标记（原 #11c）

- **触发与影响：** 分销订阅始终只使用 V1 配置接口，领取、配置下发，以及依赖下发标记的首次连接状态可能持续缺失。
- **证据：** V1 handler 不调用正常入口 `subscription_handlers.go:112,197` 的两个标记方法；`internal/store/runtime.go:802-806` 只为 `config_issued_at IS NOT NULL` 的订阅写 `connected_at`。
- **边界 / 验收：** 后续正常订阅下载可补 issued，不是任何情况下永远缺失。静态确认，专项 **NOT RUN**；验证单独 V1 完整生命周期以及重复请求幂等，不能把 V2 metadata 读取记作节点配置下发。

### BL-10 · P2 · Legacy V1 有效请求未纳入成本限流/渲染并发控制（原 #11d）

- **触发与影响：** 持有效 token 高频请求 V1，可绕开普通订阅入口的账号配额和渲染并发槽，增加查询、准备节点、渲染成本。
- **证据：** `internal/httpapi/subscription_handlers.go:78-94` 有统一控制；`client_app_config_handlers.go:17-45` 没有对应控制。
- **边界 / 验收：** 无效 token 在 `client_app_config_handlers.go:88-97` 已有限流，不接受“完全无限流”。资源耗尽/压测 **NOT RUN**，只静态确认策略缺口；验收以隔离可控并发、拒绝码、槽释放和跨入口一致性为准。

### BL-11 · P3 · 首节点兼容过滤连带移除虚拟订阅提示（原 #13）

- **触发与影响：** ShowInfo 开启，首节点不兼容但后续有可用节点；余量/到期等提示继承首节点协议，随后一起被过滤。
- **证据：** `internal/httpapi/subscription_handlers.go:148-165` 先 Present 后 Render；`internal/subscription/core.go:292-315` 克隆首节点；`render.go:28-30` 再过滤。
- **边界 / 验收：** PHP 也有此顺序，属于继承性展示缺陷，不是 Go 独有回归；响应头 `subscription-userinfo`、Surge 独立 Panel 等可能仍正常。混合协议专项 **NOT RUN**；测试首节点不兼容而后续可用时，虚拟提示应仍可显示。

### BL-12 · P2 · Shadowrocket 的 Hysteria1 被输出成 Hysteria2 URI（原 #14a）

- **触发与影响：** 有效 Hysteria v1 节点进入 Shadowrocket renderer，协议被改成 v2，无法表达原节点的 v1 连接信息。
- **证据：** `internal/subscription/uri_outputs.go:78-88` 不分 version 固定 `hysteria2://`；`general.go:236` 及旧 PHP `E:\CodeWorkstation\Xboardme\app\Protocols\Shadowrocket.php:349-405` 存在版本分支。
- **边界 / 验收：** `uri_outputs.go:83-85` 已支持 obfs/obfs-password，不能附带声称漏 Salamander。专项 URI/真实客户端导入 **NOT RUN**；增加 v1/v2 oracle 并验证客户端解析，名称编码另见第4节。

### BL-13 · P2 · runtime 更新以本地连接为条件，跨实例漏完整同步（原 #16a）

- **触发与影响：** Redis 多实例中请求在 A、节点连接在 B；更新节点 runtime 后已连接节点未收到完整 config/users/devices 更新。
- **证据：** `internal/httpapi/runtime_handlers.go:91-95` 先 `hasNode`，否则仅发 MachineNodes 或不发。`websocket.go:1191-1195` 的 MachineNodes 跳过已经存在的节点；`:1300-1302` 的 NotifyNodeFull 本身支持跨实例广播。
- **边界 / 验收：** 重连或其他完整同步可能恢复。静态确认，Redis 双实例专项 **NOT RUN**；测试请求副本与连接副本不同、machineID 为0/非0、新旧节点三类路径。

### BL-14 · P2 · 解绑释放失败时已移除节点不再进入清理列表（原 #16b）

- **触发与影响：** Redis 释放节点所有权出错；本地映射已删除、连接列表已替换，提前返回后旧租约/在线设备状态没有及时清除。
- **证据：** `internal/httpapi/websocket.go:1157-1178` 改列表后出错返回，跳过 `:1185`；`:601-614` 的 unregister 只看到新列表；`internal/nodecoord/redis.go:351-362` 释放 machine key 不级联 node key。
- **边界 / 验收：** 默认租约 180 秒（`redis.go:25`），设备状态 5 分钟（`internal/store/runtime.go:25`），不写成永久残留。故障注入 **NOT RUN**；验证释放失败时保留待清理节点集合并可幂等补偿。

### BL-15 · P3 · Access Token ID 与 Cookie Session ID 混用（原 #17a）

- **触发与影响：** Bearer 访问令牌 ID 恰与该用户浏览器 session ID 相同，session 列表可能错误标记当前会话；撤销同号会话时还可能不必要地下发清 Cookie。
- **证据：** `internal/store/access_tokens.go:118,127` 将令牌 ID 放到 SessionID；`internal/httpapi/auth_handlers.go:318,336-337` 无凭证类型分支；`internal/store/auth.go:160` 直接比较 ID。
- **边界 / 验收：** 实际撤销在 `auth.go:175-181` 按 user_id 限定，不是跨用户撤销或权限绕过。ID 碰撞专项 **NOT RUN**；测试两类凭证同号与不同号，只有 Cookie 会话能标记当前浏览器 session。

### BL-16 · P2 · 批量邮件 claim 后、发送前取消缺少终结路径（原 #18）

- **触发与影响：** 认领后在发送前取消，active 检查失败直接返回；processing 目标留下，使当前任务持续 cancelling。取消时已认领 worker 失联也可触发。
- **证据：** `internal/store/admin_user_bulk_jobs.go:369` 只扫描 queued/running；同文件`:750` 取消 pending 而不终结 processing；`internal/store/user_lifecycle.go:248` 要求 running；`internal/bulkops/service.go:247` 直接返回；`internal/store/admin_user_bulk_jobs.go:801` 的 job refresh 等待 processing 清零。
- **边界 / 验收：** active 检查之后正常发送完成/失败能收敛，其他邮件任务不受全局阻塞。已有“取消后明确 complete”用例 PASS，目标取消窗口 **NOT RUN**；增加控制认领、取消、active 检查顺序的用例及失联租约回收。

### BL-17 · P2 · 佣金异常单反复回滚整批，缺少单笔隔离（原 #19.1）

- **触发与影响：** 多级分销打开，遍历最多3层时出现重复邀请人，或某笔入账超过余额上限；同一坏单反复入选使该佣金积压批次停滞。
- **证据：** `internal/store/commissions.go:177,224` 单事务及有序批次；`:257-258,278-285` 出错整体返回。`admin_users.go:654` 与 `legacy_human_users_migration.go:325` 没有完整拒绝跨用户循环。
- **边界 / 验收：** 非任意长环都在3层内触发；可纠正邀请关系、余额，或按合法管理流程修改佣金状态（`orders.go:666`）；其他 scheduler 任务仍继续。既有故障后重试 exactly-once 测试 PASS，坏单+健康单专项 **NOT RUN**。验收须保留资金原子性，不能靠吞错继续结算。

### BL-18 · P2 · 超时退款余额越界导致整批超时订单回滚（原 #19.2）

- **触发与影响：** 某订单有余额抵扣，之后账号余额被另行增加，退回抵扣会超过数据库上限，整批取消失败并可能反复阻塞健康订单。
- **证据：** `internal/store/orders.go:832,864` 单事务遇错误返回；`:902-906` 直接加余额；`sqlite.go:1389` 余额上限为 `9_000_000_000_000_000` 分。
- **边界 / 验收：** 正常扣款再退款本身不会越界，须有额外入账；处理余额后可恢复，不等于所有定时任务停摆。超上限组合 **NOT RUN**；验证异常单可观测、健康单可继续处理，且不漏退/多退资金。

### BL-19 · P2 · backend 回滚失败的 journal 无法准确描述实际部署（原 #20）

- **触发A：** 备份 restore 失败，实际仍运行 Target，却写 rollback_failed；`expectedDeploymentForState` 一概期待 Previous，后续 status/upgrade 报 drift。
- **触发B：** 已切到 Previous+recoveryDSN，但健康检查失败；ActiveDSN 尚未记录新的 DSN，后续又因 DSN drift 拒绝。
- **证据：** `internal/lifecycle/split_lifecycle.go:279-290,416-417`；reconcile 在 `:243-247`；Rollback guard 在 `:258` 不接受 rollback_failed；实际先切换再测健康见 `docker.go:402`。
- **边界 / 验收：** frontend-only 某些失败仍可正常 status；人工恢复另有 `docs/operations/lifecycle-recovery.md:19`，不是绝对不可恢复。7 个既有 fake-platform 用例 PASS，A/B 后续命令专项 **NOT RUN**；需要阶段化记录真实镜像/DSN及受控恢复入口，不能关闭 drift 检查来“修复”。

### BL-20 · P2 · Telegram 第三次认领失联后没有租约自动终结（原 #21.1a）

- **触发与影响：** 第三次 claim 后进程退出，attempts 已达3，租约虽过期但无法再认领/自动标失败，记录持续计入 Pending/Claimed。
- **证据：** `internal/store/telegram_outbox.go:68,83`；统计仍计入见 `system_operations.go:145`。
- **边界 / 验收：** 其他消息可继续；显式 complete/fail、配置/接收人变动和生命周期取消仍可能终结，不是绝对无法清除。third-claim crash **NOT RUN**；用可控时间测试失联回收，区分“发送失败”与“发送结果未知”，避免不安全重发。

### BL-21 · P2 · Mailer/Telegram 批处理复用起始时间，后续租约已过期（原 #21.2）

- **触发与影响：** 批次前面发送耗时超过30秒，后面仍以批次初始 now 认领，租约写入时对实时 worker 已过期；多 worker 时可能重复认领/发送，认证邮件资格判断也落后真实时间。
- **证据：** `internal/mailer/worker.go:73`、`internal/telegrambot/worker.go:60` 循环最多100次复用 now；claim 租约30秒。
- **边界 / 验收：** 单 worker 不等于必然重复；真实时间 prune/发送前 fencing 会阻止部分过期发送。长批次+可控时钟+双 worker 专项 **NOT RUN**；每次认领/失败安排应按当前时钟，保留确定性测试能力。

### BL-22 · P3 · 导入接受秒级日窗，而运行时只支持整分钟（原 #22.1，导入边界）

- **触发与影响：** 非标准源数据包含非整分钟值，如3600与3630秒；导入接受，运行时均变成01:00而报相同边界，或截掉秒改变开关时刻。
- **证据：** `internal/store/legacy_nodes_migration.go:503,682` 校验/原样导入；`schedules.go:158,250` 丢秒重建；`internal/schedule/calculator.go:40,93` 拒绝相等时间且仅支持 HH:MM。
- **边界 / 验收：** Go 和旧 PHP 正常公开接口只产生整分钟，未证明线上有此源数据。普通迁移/日窗测试 PASS，非整分钟专项 **NOT RUN**。应明确拒绝不支持输入或真正支持秒，不能只改格式为 HH:MM:SS 后交给不支持它的 parser。

### BL-23 · P2 · 多层弹窗 Escape 先关闭底层，可能丢草稿（原 #24）

- **触发与影响：** 工单内提现确认、编辑器内 NodeFilter、编辑弹窗上方 Ctrl+K 搜索，父层未 suspend；Escape 被父层 document capture 监听先处理，关闭错误层。
- **证据：** `web/src/components/Overlay.tsx:110-141`；`features/tickets/TicketDetailDialog.tsx:77-88` 只挂起关闭工单确认；`CommissionWithdrawalPanel.tsx:46-50` 未通知父层；`features/nodes/NodeFilter.tsx:38-53` 冒泡晚于 capture；`App.tsx:246-251,616-621` 没有全局栈协调。
- **验证：** 本轮真实 Modal 嵌套 harness、真实 Modal+NodeFilter 组件均复现父级抢先；显式 suspended 对照正常，3个诊断 PASS。完整工单/Ctrl+K 浏览器流程 **NOT RUN**，其接入由源码确认。
- **验收：** Escape 只关闭最上层可交互层；底层草稿/焦点保留；覆盖上述3入口以及现有正常关闭确认，不可一律禁用 Escape。

### BL-24 · P2 · 用户列表快搜使首次权限组/套餐结果被丢弃（原 #25.1）

- **触发与影响：** 页面首次组/套餐请求未返回前，在邮箱框输入；onChange 本身就调用 runQuery，提升共享 requestVersion。初始 allSettled 整体 return，成功元数据不写入，页面仍空组/空套餐且不显示失败重试。
- **证据：** `web/src/features/users/UsersPage.tsx:88-118,121-138,263`；元数据重试只在错误状态展示，原成功响应被丢弃不会设置该错误。
- **验证 / 验收：** 本轮延迟组响应+快搜输入诊断 PASS，确认已返回的组不出现在选项且没有加载错误；套餐相同 guard 由源码确认。重新挂载可恢复，不写“全站永久丢数据”。用户查询与元数据加载应独立仲裁，旧用户列表不得覆盖新搜索。

### BL-25 · P2 · 礼品卡末页删除后留在空页且分页栏消失（原 #25.2）

- **触发与影响：** 第2页只有1条，总21条；删除后仍请求第2页，返回空 items/total20，Pagination 因总页数1而隐藏，当前页面无上一页入口。
- **证据：** `web/src/features/giftcards/GiftCardManagementPage.tsx:60-69,94-104,299-303`；后端 `internal/store/gift_cards.go:436,452,575,591` 返回请求页码，没有按新总数钳制页码。
- **验证 / 验收：** 模板页诊断 PASS；更改搜索重置page1也实际验证可恢复。兑换码用同样逻辑，独立用例 **NOT RUN**。修复后删除模板/码均应回退到有效页，不以“筛选能自救”视为正常。

### BL-26 · P2 · 用户订单详情旧请求覆盖最新选择（原 #25.3a）

- **触发与影响：** 连续打开订单A/B，B先返回、A后返回，弹窗从B换回A；用户可能误以为仍操作最新选择。
- **证据：** `web/src/features/orders/UserOrdersPage.tsx:36-46` 无请求代次/目标核对，与已强化的管理员详情不同。
- **验证 / 验收：** 本轮受控 Promise 的真实页面组件诊断 PASS。需验证反序响应、关闭后响应、同单重开和错误回包，列表状态查询也应检查同类竞态；本条统计仅针对原报告的详情场景。

### BL-27 · P2 · 待支付筛选下取消订单仍保留在列表（原 #25.3b）

- **触发与影响：** “待支付”标签中取消订单成功，原地替换为已取消却不移出当前筛选列表。
- **证据：** `web/src/features/orders/UserOrdersPage.tsx:66-69,73,130-132` 更新没有按当前status过滤。
- **验证 / 验收：** 本轮取消后待支付表格出现“已取消”诊断 PASS；刷新/重新筛选可恢复，不是后台订单状态未保存。验收同时覆盖全部列表保留、单状态列表移除和关闭详情后的晚响应。

### BL-28 · P3 · 部分写操作未失效对应预取缓存（原 #25.4a）

- **触发与影响：** 已预取缓存，在8秒有效期内创建/修改用户、派单、增改权限组后快速离开再回来，可再次装载旧快照。TTL 到期只阻止将来复用，不会自动刷新已经显示的旧列表。
- **证据：** `web/src/lib/dataPrefetchCache.ts:23-47` consume 不删除；`App.tsx:110-117` 缓存users/orders/plans+groups；`features/users/UsersPage.tsx:459-488`、`features/orders/OrderManagementPage.tsx:167`、`features/admin/ServerGroupsPage.tsx:253-269` 写后缺对应失效。
- **边界 / 验收：** 不是所有页面都遗漏，PlanManagementPage 已失效 plans/orders，管理员订单其他 mutation 也已 invalidate。没有服务器数据丢失或权限回退。静态确认，快切页面专项 **NOT RUN**；覆盖写后同页、跨页、在途预取及TTL边界。

### BL-29 · P3 · 弹窗内创建权限组后父表仍显示 #ID（原 #25.4b）

- **触发与影响：** 在套餐/节点编辑器中新建组并保存，子组件保留了新组，父页面 groups 没更新，表格只能用 `#id` 显示；刷新目录后可恢复。
- **证据：** `web/src/features/plans/PlanManagementPage.tsx:241,366-369,402,453`；`features/nodes/NodeDefinitionModal.tsx:65,509`；`NodeManagementPage.tsx:196-205,483,539` 保存只刷新节点列表，不刷新组目录。
- **验证 / 验收：** 静态确认，两个编辑入口专项 **NOT RUN**。保存后应立即显示组名，重开编辑器也应有新组选项；不应误写为服务器权限分配失败。

### BL-30 · P2 · 已结算订阅后续新续费行继承“已结算”展示（复查新增，非原 #1）

- **触发与影响：** 先结算原分销单，使订阅级兼容状态为已结算；随后创建新续费订单，财务订单 `paid_at=NULL`，列表却从订阅级字段继承已结算，与未结算过滤/结算预览不一致。
- **证据：** `internal/store/distributors.go:243-285` 新续单不写paid_at且不重置订阅兼容状态；`distributor_queries.go:287-290` 仅在paid_at非空时覆盖，没有处理空值；`:318` 过滤依据订单paid_at。
- **边界 / 验收：** 静态确认，先结算→再续费→列未结算专项 **NOT RUN**。这只是显示/返回契约错误，不能偷换为原报告的SQL顺序错误、重复扣款或结算金额漏记。每笔财务订单展示状态应与其paid_at一致。

## 4. 不纳入已确认缺陷的断言与待决策项

| 原断言 | 反证 / 处理建议 |
| --- | --- |
| #1 先写paid_at导致子表永不结算 | `distributor_operations.go:88,100` 顺序相反，追溯早期实现也如此；两项精确结算测试 PASS。属于误读，不标“本轮已修”。 |
| #2 关闭delivery必须撤销内部订阅 | `distributor_queries.go:491` 是关闭未领取交付；claim/续费确实被拒绝。`docs/project/requirements.json:3623` 当前二维码直接承载长期订阅链接。要撤销权益须明确独立业务授权。 |
| #3 取消导致重复退余额 | `orders.go:891-907` 条件状态更新成功后才退款；重复取消不能再次退款。 |
| #4 取消不返券次数是无意遗漏 | `coupons_test.go:100,120-135` 明确测试该行为。未付款耗尽风险应决定支付计次/预占释放/限流策略，不能只改计数器而破坏既有语义。 |
| #5 过期购买码必须视为新购并重置用量 | `docs/project/gift-purchase-design.json:23-27` 明确保留流量、拒绝跨套餐/模式，过期也同样处理；`gift_card_purchase.go:159-160` 缺重置计划会补建。相关精确测试 PASS。 |
| #6 预览函数已经保护永久账号 | 报告所称 `PreviewGiftCard/canExtendExpiry` 不存在；不能作为证据。BL-03依据实际兑换链成立。 |
| #7 有效期0代表永久 | `GiftCardManagementPage.tsx:227` 明示“留空则保留用户当前到期时间”。可决定为过期保留场景增提示/拒绝或提供独立永久选项，但不是当前已承诺的永久开通。 |
| #11 V2泄漏节点 / 耗尽流量还能下载就能联网 | V2 handler只读app设置，既有测试明确封禁账号V2可返回。`SUB-001`区分下载与运行授权，`runtime.go:273-274`节点用户要求未封禁且有剩余流量。 |
| #12 空版本过滤必为回归 | `render.go:81-84,163-196` 与旧PHP全局wildcard规则一致；`oracle_matrix_test.go:28-29`空版本预期已测试。若要改成乐观放行，先确认兼容策略；客户端包装应用版本也不等于内核版本。 |
| #14 Hysteria2遗漏Salamander | `uri_outputs.go:83-85`已有obfs及obfs-password，本轮未修过。 |
| #14 节点名称未URL编码的兼容影响 | `uri_outputs.go:88`裸拼名称是事实，`%`/字面`%20`值得边界测试；真实Shadowrocket解码/导入未测，先列低优先兼容风险，不把连接失败视为已复现。PHP也裸拼只说明继承性，不是排除缺陷的理由。 |
| #15 站点名未配置就输出坏Surge/Surfboard | helper `managed_config.go:190-197,45-50`局部fallback确实没回填模板；但`sqlite.go:947`非空默认与长度约束、`site_settings.go:356,406`及迁移正规化阻止空名称进入正常HTTP渲染。仅helper鲁棒性待补。 |
| #17 login少Origin中间件即可CSRF | 原生接口严格`application/json`（`runtime_handlers.go:563-567`），当前源码无跨域放行配置；普通表单不满足，跨站JSON须预检。未检查部署反代配置/浏览器利用链，不认定已证漏洞。 |
| #17 GET invite/save允许Cookie CSRF | `invitation_handlers.go:232-235`仅Accept AccessToken，Cookie返回403；对应测试本轮PASS。 |
| #17 requestIP总是RemoteAddr | `server.go:829`全局withClientIP，`ratelimit.go:246-258`使用可信代理解析上下文；当前已含`8a28ad326`修复，信任链测试PASS。 |
| #21 三类认证邮件永久pending | password-reset、registration、login-link均有凭据到期prune/替换/生命周期取消。第三次失联到到期之间的claimed和“结果未知”记录不足可改进，但不能写永久队列卡死；过期取消本身合理。 |
| #22 标准v2_stat_server存日期字符串必迁移失败 | 旧PHP `E:\CodeWorkstation\Xboardme\app\Models\StatServer.php:22`使用dateFormat U；`database\migrations\2023_03_19_000000_create_v2_tables.php:115-116`为integer。非标准字符串源样本及支持契约未给出，不采纳标准迁移必坏结论。 |
| #23 未按保存按钮自动提交就是Bug | `frontend-parity-2026-10-01.json:1171`明确1000ms自动保存，`AutoSaveForm.tsx:16-27`实现且测试PASS。不过该项验收状态仍awaiting_user_preview，不夸大成用户已验收所有敏感交互。 |

### #23 敏感自动保存需要单独决策

安全路径的有效中间值停顿1秒确会保存并跳转；但跳转目标对应已保存的新路径，不是已证明无效URL锁死。连续输入会重置防抖，无效/短于8字符的值不提交。相关位置：`SiteSettingsPage.tsx:66,157,166`、`App.tsx:581`。

旧单节点密钥选择 generate/clear 会自动执行，没有独立确认；警告只写“保存后”，对不熟悉自动保存的管理员存在误操作风险。实际后端是 `node_agent_settings_handlers.go:146` 的 `DisconnectLegacy`，不是断开所有机器凭据节点。正常preserve不会修改密钥，成功后也重置为preserve。

**建议产品决定：** 安全路径应用、旧节点密钥生成/清空是否改为显式确认动作。此项属于高影响交互待确认，不计为已确认P1；本轮没有操作真实设置或改变既定自动保存规则。

## 5. 验证与交付状态

- 后端：9组精确命令，58条顶层测试执行记录（同一超时边界测试运行两次，57个不同顶层测试），均PASS。主要用于核实契约、正常路径和反证，不替代清单中标注NOT RUN的专项。
- 前端：既有设置相关3文件9测试PASS；本轮隔离诊断1文件7测试PASS，其中6个缺陷现象、1个正常对照。诊断源文件和专用配置保留在 `docs- box/evidence/`，不加入应用测试配置。
- 初次诊断配置将include数组合并而意外选中全套测试，已主动中止；随后改为显式覆盖include并完成7测试。不能把被中止的全套执行算PASS。工具链准备阶段的失败及环境修正见测试证据。
- 本轮不主张全仓库回归、覆盖率、race、真实浏览器/客户端、线上业务或生产回滚验收通过。没有修复业务源码，没有推送/PR/部署，没有修改线上数据。
- 只新增本目录报告与复现证据；不暂存、不提交，也不混入上一任务已有暂存区。后续修复应按风险拆批，优先BL-01/02/03/08，并在修复前补各自专项反例。
