# PHP 与共享节点真实联调补充（2026-10-22）

本文件是 `go-design-handoff-v1` 冻结后追加的实测经验。Go 仍为需求/设计交接，所有 Go 实现与现场测试保持 NOT RUN。原 `references/` 快照及其 source-manifest 不重写；它们说明采集时的历史来源，本文件说明随后发现的差异。接手人不得照搬 PHP Lua 或把 PHP/Node 的通过记录算作 Go 验收。

## 当前可引用的共享节点状态

- 启动修复 `0d0cdbcf8fc901013680600c90ec9743c1a644c7` 已推送；用户批准工具链三处安全补丁至 Go1.26.10，提交 `f86f406a571f5b539f53404d9771547dc8be61e9`，完整原 CI39758393658 通过。README 中“尚未推送/Go1.26.9 阻塞”是此前冻结状态。
- WS 已连接时仍须周期对账；漏掉一次启停通知不能等到重连。提交 `587d95e2aaacde570dc204693246a4cb51bc1dc0` 采用30秒校准、单在途请求、5秒无响应后的 HTTP 补偿及迟到/未知/重复快照拒绝，原 CI39763515845 通过。原 TTL、epoch/version、ACK 约束不放宽。
- 第二轮 CI 的实际 PR checkout 为 `d98c56377ded452a64bf2c0dee729e44e9c0fd71`，tree 与审查 head 相同。发布/QA 要使用实际制品 revision 和摘要，不能把 merge 制品改名为 head 制品。
- 上一轮已合格制品的真实 journal→Vector→guard→VictoriaLogs→PHP 订单页已逐 event_id 对齐；随后又发现 PHP 租约状态与 Node 关闭分类缺陷。原负责人正在修复，最终共享节点准入仍应取得最新冻结记录；不得只凭上述两轮绿 CI 宣称现场全部通过。

## 必须写入 Go 实施和验收的六项约束

| 场景 | 真实发现与后果 | Go 实施/测试要求 |
| --- | --- | --- |
| Redis namespace 与 Lua 动态 key | PHP Redis client 仅给 EVAL KEYS 加全局 prefix；Lua 拼接的 literal session/ack key 未加，常规 GET 找不到，出现“节点已 detail，页面 idle、无法 stop” | 采用统一 key 构造策略，Lua 所用 key 必须从同一命名空间传入或派生。用真实 Redis 和非空 namespace 完整走 start/status/snapshot/ACK/stop/revoke/expiry；同一 Redis 中两个 namespace 互不读取/停止对方会话。不要关闭全局隔离来修复。若兼容旧键，限定当前 epoch/subject/session，保持原绝对到期和 TTL，不做无界扫描或隐式迁移 |
| vmauth 实际路径组合 | writer prefix 已含 `/insert/jsonline`，vmauth 又追加请求路径，产生重复路径404和永久丢弃 | 对固定镜像做真实 vmauth→fake guard 回归，捕获最终 path/query/身份 headers/body。writer 只允许精确写路由，viewer 不可写；凭据匹配不等于路由正确。区分 guard accepted、Vector sent、VL 可查询和应用结果 |
| WS 通知丢失 | 正常 WS 连接不保证启停通知到达 | 通知只作加速，保留有界周期权威快照；HTTP 补偿单飞/合并，拒绝过时响应。用真实控制层复现漏 start 与漏 stop，检查重连、超时、并发和 TTL，不靠缩短时钟代替900秒现场测试 |
| 运行期身份初始化 | 内核 observer 未设置 node_id，machine WS 先连接而节点晚注册时漏状态 | 测真实 Service 构造与 machine 初始启动顺序；节点事件、source journal、collector、中央存储、订单归属必须证明同一身份。不能仅手工构造 observer 后断言字段 |
| 连接结果与清理错误 | 成功传输后的 QUIC cleanup Close 错误抢先终结 trace；真正 relay 错误又可能因 first-wins 被丢弃 | 区分业务阶段/relay 的结果与资源清理结果。不靠字符串吞掉 cancel，不改变代理 Close/返回错误。真实依赖测试包含双向正常 EOF、typed 正常结束、真正 relay 失败、超时、强制关闭，以及 trace 只终结一次 |
| 真实鼠标命中 | 浮动分销入口层级比日志中心高，点击详情反而打开分销管理 | Go 原生 React 页面也要做实际主题桌面/移动命中测试，含浮动入口、滚动与弹层。不使用 force、DOM click 或只检查 z-index 数字来证明可点击 |

## 交接清单追加项

- [ ] 接手人读取本补充，登记上述六项与现有 T01–T28 的对应关系；必要时增加细分测试，不重用 PHP PASS。
- [ ] 非空 Redis namespace 的真实服务集成红绿证据；默认 prefix 与自定义 prefix 均覆盖，两个面板隔离负面用例通过。
- [ ] 权威会话状态、节点 ACK、生效 detail 事件、停止/自然到期后 baseline 四层证据一致。界面 idle 不得作为“未采集详细日志”的单独判断依据。
- [ ] 真实900秒到期观察覆盖已有长连接和新连接，证明到期后降为 baseline 且代理继续转发；不重新开启会话延长窗口，不改时钟或 TTL。
- [ ] 获取原负责人后续 PHP/Node 修复的提交、原 CI、实际制品摘要、现场结论与 NOT RUN；不得自行同时写共享节点仓库。
- [ ] 记录已永久丢弃的历史覆盖缺口；修复后的新记录成功不冒充历史补回。

原始来源：PHP `docs/handoffs/customer-connection-diagnostics-progress.md` 和 `2026-10-22-diagnostics-vmauth-route-repair.md`，共享节点 `docs/handoffs/2026-10-22-diagnostics-bootstrap-repair.md`、`2026-10-22-diagnostics-ws-reconcile-repair.md`；这些为当前工作区/候选记录，接手时应再查实际提交状态。此补充不包含凭据、私有配置或可操作的真实客户身份。
