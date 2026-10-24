# Go 测试方案与可执行用例矩阵

版本：`go-design-handoff-v1`。**T01–T28均为NOT RUN：Go客户诊断尚未开发，本文是待实现用例，不是测试成绩。** T19需后续现场授权，T20是后续迁移范围；两者不得计作首期已完成。文档自检、PHP历史测试、共享Node修复测试分别记录，不提升为Go通过。

## 1. 测试准备与隔离规则

1. 每轮创建独立临时业务库、独立Redis前缀、独立诊断库/凭据/collector buffer和受控DNS/目标服务。不得复用生产scope、账号、machine、日志卷、source标签或PHP正在使用的QA实例。
2. 使用可重复种子/可注入时钟；合成时间以运行开始的UTC`T0`偏移构造，避免固定旧日期导致被保留策略拒收。单调时间/Redis TIME/网络延迟由受控环境模拟；不改Windows或共享宿主时钟。
3. 测试客户端通过共享节点真实协议入口访问受控TCP/UDP/HTTP echo和DNS服务；内核级结论使用真实依赖路径，不复制核心逻辑、不只Mock observer。
4. fixtures先核验本包manifest，再将有效/非法向量接入Go测试。测试实现后选择具体函数和命令；`go test -run ...`必须确认实际匹配且执行了预期测试，0 tests/全SKIP不是PASS。本文没有声称新增测试文件已经存在。
5. 每用例冻结输入、操作、结果和故障注入；保存原始计数/事件ID、源码SHA、组件版本、数据/配置摘要（不含secret）。失败停在该门槛，保留现场，不改设备限制/鉴权/计费来绕过。

### 合成数据字典

| 代号 | 约束 |
| --- | --- |
| S-A / S-B | 小写UUIDv4 `00000000-0000-4000-8000-000000000001` / `00000000-0000-4000-8000-000000000002`，不同安装 |
| U-A / U-B / D | 普通合成订阅用户101/102、分销商900；UUID全合成，A/B不同，D不是订阅客户 |
| O-A / O-B | 普通订单 `QA-DIAG-A-0001` / `QA-DIAG-B-0001`，完整单号；另建相同前缀/后缀对照和全数字单号 |
| O-D / O-R | 分销首单/续费，购买者D、实际subscriber U-A；另建缺subscriber的破损关系 |
| N-1 / N-2 | 合成node701/702；初期真实链路只用N-1，N-2用旧/离线能力场景；合成machine801/802按节点归属分开 |
| R-1 / R-2 | 非零32hex运行ID；重启切换，旧owner/旧runtime保留作负面输入 |
| DOMAIN | 受控 `a.diag.invalid`、`b.diag.invalid` 等，由隔离DNS显式解析；不是依赖公网DNS可用 |
| STATE | 无租约、baseline、有效detail、到期detail；存储ready/blocked/unknown独立排列 |

既有contract-fixtures中的ID/hash样本原样使用，不能为了适配实现改expected。超过字节/Unicode/非法值用合成字符串，秘密诱饵只用假的`FAKE-*`值。

## 2. 用例矩阵

表中“环境”是必需的验证层，单测通过不能代替更高层。每项状态均为NOT RUN；实施时在独立结果表填写，保留本表作为冻结计划。

| 用例/需求 | 前置与执行步骤 | 预期与必须证据 | 环境/当前状态 |
| --- | --- | --- | --- |
| T01 订单实际归属（CC02/03） | 建O-A/O-B/O-D/O-R及缺subscriber关系；管理员逐个lookup，再分别query；将分销商ID与实际U-A对照 | O-D/O-R与O-A归同diagnostic_id，O-B不同；破损关系409且不回退D；返回无UUID/token；保存参数化查询/响应与身份断言 | Store真实临时DB＋HTTP；NOT RUN |
| T02 稳定ID与scope（CC02/04） | 同时初始化scope多次；普通配置save尝试改scope；轮换U-A UUID；用S-B同用户ID；lookup相似/全数字trade_no；跑全部ID fixtures | 并发同值、普通save拒绝变更、UUID轮换ID不变、跨scope不同；精确单号不误匹配；实际NUL hash等于fixture，非法ID失败 | 单测＋真实DB/HTTP；NOT RUN |
| T03 同步路径（CC12） | HTTP拉用户、WS full、direct delta、邮箱合并delta、Redis另一副本group刷新full各只改诊断metadata；记录映射及limiter/kernel调用 | 每路径ID/scope更新；业务hash早退不吞；纯诊断变更limiter/用户Add/Update/Reload为0；节点业务更新仍正常 | Go HTTP/WS＋Redis双副本＋共享Node定向；NOT RUN |
| T04 实际协议身份（CC06/12） | 用N-1每种实际启用sing-box协议认证U-A/U-B，含数字用户名及多路复用子流；真实访问受控TCP/UDP目标 | 使用实际认证subscriber；每逻辑流独立连接/trace，未认证事件无客户；输出协议清单、连接事件及归属证据 | 真实节点/内核＋目标服务＋采集；NOT RUN |
| T05 拒绝与网络失败（CC06/08） | 合成规则依次触发device_limit、route拒绝、DNS失败、connect timeout/refused；正常EOF对照；查询错误阶段 | 原始门禁/代理结果不变；原因/阶段准确，无重复close/虚假成功；EOF不自动故障；保存客户端结果和对应事件ID | 真实内核＋受控网络/策略；NOT RUN |
| T06 DNS结果（CC07） | 受控DNS分别返回A/AAAA、NXDOMAIN、SERVFAIL、超时；配置多服务器并明确每次实际使用；再查缓存与纯IP目标 | rcode/服务器/协议/答案/耗时来自真实exchange；cache_hit无虚构网络span；纯IP not_performed、客户端DNS unknown；数组超8正确截断 | 真实DNS/内核＋库查询；NOT RUN |
| T07 共享DNS隔离（CC07/09） | 两客户同时同域名合并解析；A无detail/B后加入有detail；阻塞exchange后依次到期/停止；另做后台刷新与缓存命中 | 每客户自己的dns.result，操作不含客户/等待名单；仅授权link可展开；中途detail标记，不补旧步骤；资格全失效即停止 | 真实共享DNS并发＋租约＋查询；NOT RUN |
| T08 UDP/多目标/长连接（CC06/09） | 一个UDP会话访问两域名及超过64目标促LRU；含代理出口；地址逐出后再访问；跨租约截止保持连接 | 各target独立累计状态，summary有原因/计数，LRU不关闭真实流；socket_peer可异于逻辑目标；过期后detail停而业务继续 | 真实TCP/UDP＋内核＋查询；NOT RUN |
| T09 幂等/乱序（CC11） | 同key重复start/不同参数同key、不同key遇有效会话；先A stop后B start；延迟A快照/ACK/stop到B后；合法request ACK夹带未发session/错误revision | 原session与截止不延长；参数冲突409；全局版本防旧操作覆盖新会话；10客户并发上限原子；pending/partial/active真实 | Redis/Lua＋HTTP/WS时序；NOT RUN |
| T10 故障/时钟/传输（CC11） | 断WS、丢通知、节点重启；WS→REST切换延迟旧响应；RTT>5s、本机UTC偏差、Redis时间回退/权威元数据丢失 | 最新请求权威完整快照；失败不当空；新epoch清旧，version不回退；单调截止不续期；回退暂停新租约；reconcile恢复且旧ACK拒绝 | 真实Redis＋控制面故障注入＋节点；NOT RUN |
| T11 丢失/缓冲/输出堵塞（CC14/16） | 填满node队列、阻塞stdout；collector断线/重启；journal缺口；永久HTTP拒绝、超龄event_time重传；再恢复 | 代理不阻塞；baseline错误含栈仍优先；计数按类型、health合法；cursor/buffer持久；迟到delayed、已知缺口partial、不确定unknown；无无限补传承诺 | Node压力＋真实Linux journald/Vector/VL；NOT RUN |
| T12 权限/伪造（CC05/13） | guest/user/distributor请求全部管理接口；U-A客户模式请求U-B连接；machine跨node/漏machine_id；旧owner；ACK夹带跨客户/未发session或错误revision；writer读/viewer写；正文伪造及重复query/tenant/header覆盖 | 401/403/409等准确拒绝，无legacy降级；固定认证身份不可伪造；浏览器无凭据；原始LogsQL/控制字符/SSRF端点输入拒绝；日志无secret诱饵 | 真实HTTP/WS/Redis＋固定vmauth/VL；NOT RUN |
| T13 查询预算/晚到（CC05/08/14/16） | 写晚到/重复event；早DNS失败后dial失败再成功；填>10000行/16MiB/1001摘要/2MiB，巨大DNS操作；测试精确trace下推/变filter游标/到期 | 去重/累计错误/UDP聚合准确；同一读取预算/5s，不N+1、32DNS上限；截断明确；cursor绑定客户/模式/generation/管理员，过期拒绝；晚到刷新才纳入 | 真实VL＋HTTP流/限额与快照缓存；NOT RUN |
| T14 订单快捷/迟到响应（CC01） | 普通详情、分销列表/详情分别跳转；快速A→B、订单→ID、前后翻页；故意延迟A响应后返回B | 入口order_id正确，不夹UUID/token；切换清旧state/取消请求；旧响应不覆盖B、cursor/trace/coverage不串；保存操作录像 | React测试＋真实浏览器/服务；NOT RUN |
| T15 旧版兼容（CC11/12） | 新面板配旧节点、新节点配旧面板、无ID/能力；节点业务用户更新/重建入站；detail期间重新同步 | 旧代理/限额/计费正常；不支持准确；映射缺失不伪造身份；observer在真实重建后仍生效；不会因metadata重载 | Go/Node版本组合＋真实内核；NOT RUN |
| T16 业务语义回归（CC08/12/17） | 相同受控负载对照原版、baseline/detail：设备限制、流量上报、用户删除、UUID轮换、撤权和强制关闭 | 原业务判定/计费与关闭仍生效；已建连接结束日志保持旧冻结归属；不靠放开限制通过；保存业务状态/字节/事件对照 | 真实DB/节点/受控客户端；NOT RUN |
| T17 保留/硬容量（CC15） | 独立测试卷用隔离event_time证明3天窗口；验证20GiB软目标的真实分区清理与最新分区可超目标；模拟/填充达到参考16/20/24GiB及5GiB紧急线，缩比测试须另有实际参数核验 | 源时间保留、补传不续命；分区清理/软目标与30GiB硬边界区分；警告/停收准确；阈值≠已删历史；已证实提前删除partial、范围不明unknown；版本参数实效可证 | 隔离真实Linux文件系统/VL/guard；NOT RUN |
| T18 挂载/guard/health（CC15/16） | 卷缺失、错误ext4/UUID/块设备、根盘不足；guard退出、陈旧>60s/超时/持久锁存；buffer满；start/stop/query分别执行 | 拒启/写503，不自动创建根盘目录；新租约拒绝，stop不依赖库健康、只读仍可用；重启锁存保持、显式resume才恢复；未知正确 | 隔离Linux挂载＋guard＋Go HTTP；NOT RUN |
| T19 24–48h试点（CC16/17） | 获目标授权后仅一节点，代表负载及至少一次开发任务重叠，真实新请求；持续采集速率/容量/延迟与业务反馈 | P95/积压/OOM/丢弃/业务满足已评审目标；实测与四分区/三天外推分开；不自动扩节点，不用健康替代用户恢复 | 现场P1＋用户验收；NOT RUN（未实施/待授权） |
| T20 双源迁移（后续） | P1报告后在两隔离库演练M1–M7：迟到event/重复/跨切换长连接、离线collector、新旧源失败/回切 | 唯一实际写入目标、无转发环；原身份/event_time/buffer保留；共同预算去重，任源失败partial，generation旧cursor失效；窗口排空才退役 | 后续D7.3/P2；NOT RUN（首期不实施） |
| T21 Trace生命周期（CC09） | TCP、UDP多目标、多路复用，route内/dial内DNS，子操作失败重试成功，异步接管/relay结束 | 真实parent与单调耗时、每已建span有结果；子失败不关root，root只结束一次；共享DNSroot与客户link合法，UI不伪造 | 真实依赖路径＋事件/查询/浏览器；NOT RUN |
| T22 异常栈来源（CC10） | 自有创建点、库已有栈、普通error边界、正常EOF/业务拒绝、无租约/租约到期；长连接后失败 | 来源正确、原goroutine首次捕获，错误/panic语义不变；无logger/sink栈冒充；无资格不抓栈；序列化后sidecar释放 | 真实Node错误路径＋Go展示；NOT RUN |
| T23 字节/Unicode/秘密（CC10/14） | 深栈/长函数/Unicode、FAKE凭据文本、绝对路径、最大必填字段、满队列baseline错误附栈 | UTF8 JSON含换行<=4096B、栈<=12帧/2048B、码点限长；最小错误schema合法，字段/身份/原因保留；无诱饵泄露，优先级不降 | fixture/序列化/队列＋浏览器纯文本；NOT RUN |
| T24 页面/路由/XSS（CC01/02/05） | 1440×900、390×844；导航/刷新/后退/主题/键盘；精确单号/相似单号、ID只读/trace；注入恶意日志文本、认证失效、后台标签 | 原生路由正确；ID模式不能租约；文本不执行、认证失效清数据；轮询离开/后台停止，Dashboard既有轮询不误改；保存截图 | 真实浏览器＋认证Go服务；NOT RUN |
| T25 启动绑定（CC12） | 通过真实newService配置N-1构造observer；在认证scope/映射后发健康和真实连接；原缺陷版本负面对照 | observer node_id为N-1非0，健康与trace实际产出；Node修复SHA/原版失败/修复通过证据由原负责人提供；Go可查到真实业务事件 | Node真实构造＋Go垂直链路；NOT RUN |
| T26 注册/WS时序（CC11/12） | shared machine WS先connected后register；先register后connect对照；disconnect后register；并发register/status，重连及最新owner ACK | 晚注册看到当前状态，不能丢能力/首次snapshot，也不重放过期connected；无race/死锁/旧owner覆盖；每场景事件/版本可追 | Node/Go控制面＋Linux race；NOT RUN |
| T27 local/Redis模式与性能（CC17） | 按已批准B分别启动local/redis模式；无权威Redis时尝试detail；两副本并发start/stop/查询；原基线/baseline/detail同负载含高频失败 | 未支持模式明确错误，无静默内存降级；跨副本权威一致；报告吞吐/P95/CPU/内存/队列，按预算判定而非只报跑通 | Go实际部署模式＋Redis＋性能环境；NOT RUN |
| T28 接替/独立身份（CC04/18） | 先用隔离PHP业务副本/合成数据验证独立新scope；A批准接替才演练保留用户ID/scope、旧历史只读、控制epoch切换/回滚 | 独立环境不串；接替客户ID与历史连续可证，重映射冲突显式失败；无双控制面、无过期租约复活；决策未定只验证独立方案不动真库 | 隔离迁入演练；NOT RUN（决策A待定） |

## 3. 测量与通过标准

正确性/权限/资源上限不是可选；所有适用失败都应修复再重验。既有预算是待验证目标：baseline吞吐下降<=5%，连接建立P95增量<=5ms；P1可查询P95<=10s、过滤查询P95<=3s并守5s总时限，无持续积压/新增OOM/已知丢弃/业务回归。若环境/预算改变，先记录评审结论再冻结新矩阵，不静默改阈值或删用例。

对每请求记录原始读取行/字节、返回行/字节、DNS操作数/实际查询次数、耗时与取消、快照数/TTL；确认并发2/每分钟60限流不越过公平与资源预算。容量按真实文件系统/VL参数证明，不能把Python模拟阈值等同实际挂载/回收。

### 结果记录模板

```text
用例：Txx（关联CC/真实治理ID）
执行时间UTC / 源码SHA或未提交diff摘要：
环境：OS/CPU/内存/Go/Redis/Node/Vector/vmauth/VL版本与摘要：
输入：合成数据种子、时钟偏移、配置指纹：
命令与实际匹配/执行/跳过数量：
实际操作、故障注入及恢复：
原始输出/事件ID/截图/性能CSV路径与SHA256：
预期 / 实际 / PASS或FAIL或NOT RUN及原因：
业务副作用与资源变化：
review人和日期：
```

脱敏后的统计/摘要可随报告入库；真实日志、凭据、数据库、生产身份、原始请求内容不入Git。场景确实不适用时写清产品/协议依据和裁决记录，不能用笼统“历史问题/环境问题”掩盖缺口。