# 日志与节点运行时诊断

Xboard-Go 面板使用结构化 JSON 日志输出到标准输出。容器或 systemd 应收集
标准输出并交给现有日志系统保存；面板本身不把访问令牌、订阅查询参数、节点
用户 UUID 或上报正文写入日志。

## 请求关联

每个 HTTP 请求都会生成一个 `X-Request-ID` 响应头，并在请求完成时记录：

- `request_id`：面板侧关联 ID；
- `method`、`path`、`request_bytes`、`status`、`bytes`、`duration_ms`：请求结果和耗时；
- `remote_ip`：连接来源地址，用于定位来源网络问题。

日志中的 `path` 不包含查询字符串。订阅令牌、节点认证参数等 URL 查询值因此
不会进入请求日志。节点客户端会把面板返回的 `X-Request-ID` 记录在节点日志中，
可用该 ID 在两侧串起一次控制面请求。

成功请求按 `DEBUG` 记录，客户端错误按 `WARN`，服务端错误按 `ERROR`。面板进程
通过 `XBOARD_LOG_LEVEL` 控制最低级别（`debug`、`info`、`warn`、`error`）；需要
临时采集完整请求轨迹时设为 `debug`，问题复现后恢复为 `info`。

## 节点上报诊断

节点配置、用户列表和报告处理会记录请求 ID、节点 ID、机器 ID、数据条数、响应
大小、配置摘要和失败阶段。配置摘要是短 SHA-256 前缀，不是配置内容。报告中的
用户 UUID、IP 列表和原始正文不会写入日志。

管理员可读取最近一次节点运行时快照：

```text
GET /api/v1/admin/nodes/{nodeID}/runtime
```

该接口需要管理员认证，返回最近一次节点上报的 `status`、`metrics`、更新时间、
年龄和 `stale` 标记。`stale=true` 表示超过 5 分钟没有新快照。节点报告中的
`metrics.kernel` 包含内核是否运行、启动尝试次数、启动失败次数以及最近启动、
停止和失败时间，可直接判断“控制面在线但数据面监听器未启动”的情况。

## 节点超时排查顺序

1. 在面板日志中按 `node_id`、`request_id` 和 `path` 检查握手、配置、用户和报告
   请求的状态码与耗时。
2. 在节点日志中按相同 `request_id` 检查请求失败、HTTP 状态码和请求耗时。
3. 查看管理员运行时快照，确认 `stale`、`metrics.kernel.running`、
   `metrics.kernel.start_failures` 与 `metrics.api.failure`。
4. 若控制面请求成功而 `kernel.running=false`，优先检查节点内核启动错误和端口监听；
   若控制面请求出现超时或 4xx/5xx，先修复面板连通性、认证或配置返回。

## 保存与脱敏

日志采集器应设置保留周期、大小上限和访问权限，并在转发前继续过滤
`authorization`、`cookie`、`token`、`password`、`secret` 等字段。不要把日志、
请求正文、完整配置或运行时快照直接提交到 Git、Issue 或工单附件。
