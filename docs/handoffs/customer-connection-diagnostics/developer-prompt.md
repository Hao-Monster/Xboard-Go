# 可交给 Go 接手开发人员的提示词

将本目录完整交给接手者，不能只复制本提示词而遗漏设计/测试/参考manifest。下面内容可直接作为接手说明；**当前用户本轮要求先交接需求与详细设计，不包含Go业务实现或服务器部署。** 若之后明确指派本地实施，再按已确认范围执行D0及后续；文档不自动授予跨仓库写入、推送、合并或部署权限。

```text
你负责 Xboard-Go 客户连接诊断与日志中心的接手设计。项目路径 E:\CodeWorkstation\Xboard-Go。PHP 日志系统与共享 Xboard-Node 由原负责人继续推进，你不能同时改共享Node或操作其QA/真实节点。

先完整阅读：
1. 仓库和目标目录适用的 AGENTS.md。
2. docs/handoffs/customer-connection-diagnostics/README.md。
3. requirements.md、design.md、development-plan.md、test-matrix.md、handoff-checklist.md。
4. references/README.md 和 source-manifest.json；核验随包 contract-schema.json、contract-fixtures.json。

本轮工作范围：
- 先完成设计接手/D0/G0材料：实时基线与保护清单、需求差异、A–D待决/推荐方案、测试矩阵核对、模块和接口设计、任务与评审清单。
- 当前没有Go业务开发、提交、推送、PR、合并或部署授权。后续用户明确指派本地实施时，按已确认治理项和决策推进；一般实现选择自行完成，身份/权限/协议/计费/迁入语义冲突交负责人裁决，只暂停相关项。
- 不连接服务器写入，不初始化真实scope、不修改真实数据库/凭据/节点，不上传代码/静态文件/二进制，不重启服务或格式化/删除日志卷。所有环境按Go规则走获批CI/CD。

业务目标：
管理员在原生React日志中心输入完整订单号，系统自动定位实际subscriber及稳定客户日志ID，查询各节点真实目标、DNS、路由/拨号、设备拒绝、连接错误及trace/span；详细诊断15分钟，可看来源明确异常栈。已有日志ID仅只读。普通订单详情、分销列表/详情快捷进入同一中心。日志故障不能改变代理、限制、计费或撤权。首期仅管理员和sing-box实际启用协议，不做客户端改造/Xray/逐包/HTTP正文/网站成功判断/伪造历史。

已核对但须刷新的源基线：
Go本地1759f64e077475d0581cc3f1da032d988c27784e，codex/ci-001-tiered-gates；原有OBS-001仅运维日志、30天/告警预算且非多租户隔离。Go尚无客户scope/ID/租约/中心，不能用运维日志的完成度替代本需求。Node参考748228e并非新日志验收候选；node_id初始化和晚注册WS状态两个缺陷由原负责人修复，必须等确切修复SHA/制品与测试交付。

移交时追加：共享Node本地bootstrap修复提交为0d0cdbcf8fc901013680600c90ec9743c1a644c7，仅本地测试/race/review通过，未推送、未通过原Linux CI或真实闭环；安全工具链阻塞未解除，不能当已准入制品。

开工前四项裁决：
A. 独立Go安装还是接替PHP，是否承接历史，用户ID/scope/存储身份和单一控制权迁入。
B. local/redis模式的租约权威状态、时钟与无Redis行为，禁止静默做不安全内存替代。
C. 独立诊断服务的目标、端点、凭据、卷与容量预算；30GiB/3天等是原参考预算，需核验，不覆盖旧运维库。
D. 节点V1 legacy/V2 machine/WS兼容范围与Go管理员API/路由；machine漏machine_id先拒绝，不能降级。
记录选项、推荐、结论/未决、负责人、日期、适用环境与测试影响。文档建议不视为用户批准。

不可丢失的合同：
- diagnostic_id使用实际NUL分隔的SHA256前24hex，按原样fixtures验证；scope一次显式持久初始化，UUID轮换不改ID。分销关系缺subscriber明确失败，不能回退分销商。
- 三family归属严格分开：connection归客户，resolver_operation无客户名单，node不填虚构客户/trace。DNS仅沿本客户dns.result的单向授权link展开。
- 用户metadata覆盖HTTP/full/direct与merged delta/Redis跨实例full，在业务hash早退前热更新；不触发limiter或内核用户重载，正常撤权继续。
- 15min/10客户租约，Redis TIME、epoch/version字符串、完整快照/ACK最近60s绑定、owner/runtime/session/revision授权、RTT5s、单调截止与同会话只取更早值、30s reconcile。stop不依赖存储health但不能伪造控制写成功。
- 事件UTF8 JSON含换行<=4096B，栈<=12帧/2048B，原goroutine首次捕获并标来源；不改error/panic语义；队列2048/8MiB、baseline错误预留1/4且detail先丢、不阻塞代理。
- 查询默认15min/最长72h，100/200分页，原始总读10000行/16MiB/5s，详情<=1000/2MiB，DNS每页<=32有授权操作批量展开；快照5min/每管理员3、signed cursor绑定scope/客户/模式/过滤/generation，2并发/每分钟60。不得任意LogsQL/无界扫描/N+1。
- 逐节点writer身份强制和viewer分权须用正文/query/header/tenant负面测试证明；不能只用token不同宣称隔离。源event_time/去重不因补传或迁移重写。
- 节点/collector/库三健康各有时效；delayed/partial/unknown与空结果分开；canary正常不代表真实事件产出，空日志不等于无访问。

Go实现方向（后续获指派后）：
复用已有订单subscriber关系、Store/HTTP、nodecoord owner/fence和WS机制；原生React路由/懒加载/API类型，不照搬PHP后台注入、类名或hash query。首期一个查询端点和generation，先两合成客户一隔离节点打通真实成功/失败垂直链路，再完成DNS/租约/调用链/栈/保护/页面；双源迁移D7.3和T20后移，不能为“从零设计”扩大成新的观测平台。

治理和证据：
需求/任务须映射到docs/project真实JSON和Issue/Milestone，不伪造ID、不重开旧OBS-001。Go业务T01–T28目前全NOT RUN；PHP历史测试和Node定向补丁成绩不能算Go通过。先写用例，真实核心/集成/浏览器/性能分别验，0测试或全SKIP不算PASS。最终分别报告源码、自动检查、真实链路、制品、CI、部署、用户业务验收。按AGENTS渐进验证，涉及JSON才generate/check，禁止手改STATUS。

你首次交付应包含：
基线/保护项；CC→正式治理映射计划；A–D裁决表；PHP→Go差异与模块/API/协议设计；逐任务依赖及测试验收；共享Node待交付输入；发布入口/数据迁入/回滚和停止条件。只做本轮已授权工作，不能从本提示词中后续步骤推导部署授权。
```