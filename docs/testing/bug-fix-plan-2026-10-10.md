# 2026-10-10 Bug 修复方案与测试基线

## 范围与执行顺序

- 用户授权：先记录 Bug List，逐项研究修复方案，再修复，完成后等待部署授权。
- 缺陷台账：`docs/project/bug-list-2026-10-10.json`，编号固定为 BUG-20261010-01 至 11。
- 源码基线：`2acd15f9f917b9c75ed39c0ac644cd9e2427fec1`，开发分支 `codex/ci-001-tiered-gates`。
- 标准对齐模式；仅修复已确认的行为，不新增业务。当前文档在新增回归用例和生产代码修改前冻结预期；执行结果另记，不用实际错误行为反推预期。
- 根工作区一个写入者；三个独享 detached worktree 分别负责恢复链路、停机/邮件和前端。主负责者核查并集成本地提交。
- 保留原有五个未推送提交和未跟踪的 `docs/HANDOVER.md`，不暂存无关文件。
- 不推送、不建 PR、不部署、不修改生产数据。不进行现场漏洞利用、资源耗尽或线上动态探测。测试使用隔离 SQLite、合成身份、低成本替身或本地受控 HTTP 服务。
- 需求/工作项/Issue/Milestone 关联在台账中；#123 已关闭，仅保留历史关联，不重开，不作为本次新关闭事项。

## 逐项研究结论

### 01 — 凭据变更的原子失效

当前 `revokeAllCredentialsTx` 只撤销 cookie/bearer；邮箱、密码、角色、封禁和生命周期变更已经调用它。把待用登录链接、重置挑战、相关未完成邮件及密文清理加入同一事务，避免另加 schema 或密码版本体系。透明密码 hash 升级不等于用户主动改密，不改变此正常登录语义。验证变更前已检查的 challenge 在提交时同样失败、其他用户不受影响、清理失败时整笔账号变更回滚。清理 pending/claimed 邮件不能追回已投递邮件，因此最终兑换时的失效是安全边界。

### 02 — 回滚不能悄悄丢失确认写入

在线 `VACUUM INTO` 是一致性快照，不是写入冻结。候选启动同时启动后台 scheduler/mail/bulk 等任务，单纯阻断公网不能保证零写入。现有 lifecycle 没有全局冻结协议，新增它会扩展架构与发布流程。本次沿用 `deploy/self-hosted/README.md` 与 `docs/operations/stable-release.md` 已有保数据策略：候选可能触及数据库以后发生失败，停止候选、保留当前数据库/WAL和备份，记录需要显式恢复；不得自动恢复旧快照。未激活候选时的失败保持原服务；不改数据库的 frontend/gateway 回退可保留。

取舍：失败后可能需要人工恢复和停机，不以自动恢复可用性换取已确认写入丢失。显式、另行授权的快照恢复仍是有数据时间点损失风险的操作，本次不执行。此处只修改本地流程与测试；实际发布和恢复演练均须后续授权。

### 03 — 完整等待 HTTP 退出

将监听退出、signal/context 取消、`Server.Shutdown` 完成和自定义 WebSocket/telemetry waiter 明确排序。保留既有宽限，不在 `ListenAndServe` 提前返回时退出 main 或关闭依赖。异常监听退出也要终结协调 goroutine，超时不能伪报成功。

### 04 — 统一密码计算并发上限

复用已有两槽 `passwordHashSlots`，覆盖现代/兼容登录（Verify、升级 Hash、冲突重试 Verify）、用户改密及管理员创建/更新/重置。已有注册、密码找回、批量生成维持同一槽池和既有错误契约；不重复获取导致自锁。同步入口忙时返回有界 429/Retry-After，所有错误路径释放槽；批量任务继续可取消等待。回归只注入低成本 hasher 或预占槽，禁止真实 Argon2 压力测试。

### 05 — 可信代理下的普通客户端地址

复用节点入口已经存在的受信代理链解析和长度/跳数上限，在统一 HTTP 入口解析客户端地址供普通限流及记录读取。仅信任配置代理；从右向左找到首个非受信节点，非法/重复/过长头回落到 peer。保留原始 `RemoteAddr`，节点独立 peer 限流保持不变；无代理配置行为保持兼容。

### 06 — 终结失联的最后一次邮件尝试

在 ticket/reminder 的 Claim 事务内每次有界终结租约已过且 attempt>=3 的未完成任务，写 failed_at 和结果未知说明，清理 claim，不再发送第四次。无新任务可取时也必须提交清理。仍在租约内、已成功/取消/失败、其他队列的任务不得受影响。过期 worker 的迟到更新必须被拒绝。使用真实隔离 SQLite 检查状态、并发排他和幂等。

### 07 — 不健康应用仍可成为显式回滚目标

combined 回滚采用身份/镜像/DSN 匹配，不要求当前服务健康。保留操作锁、journal、目标保护和恢复完整性校验；与 split 既有行为一致，不放宽目标身份。与 02 的自动恢复禁用明确区分。

### 08 — journal 部分尾行恢复

在现有独占锁和大小上限内，仅修复没有换行的未完成尾部后追加新记录，保留所有完整记录并同步落盘。完整但损坏的记录必须报错，不能当成尾部忽略。combined/split 共享修复，测试 Load→Append→Load 和边界。

### 09 — 套餐实际预取缓存失效

修改成功后失效实际消费的 `adminDataCache`，不是另一份 staleStore。用请求代次避免旧在途读取覆盖新状态；旧 Promise 的失败只能删除自己的缓存项，不得驱逐替换后的项。正常失败仍展示错误，按受影响关系失效包含套餐的关联数据，不做全局缓存重构。

### 10 — 订单详情只接受最新请求

为详情读取加入代次保护；打开另一单、关闭或卸载令旧读取失效，成功/错误/finally 都检查。已关闭或切换后的操作回调不得重新打开详情。不改金额、订单状态机或后台 API。

### 11 — 工单嵌套确认保留草稿

确认框存在时给父 Modal 传 `suspended={confirmingClose}`，复用现有 overlay 焦点/Escape 规则。Escape 只取消确认，父详情和回复草稿保留，不调用关闭接口；正常确认关闭行为保持不变。

## 可执行测试矩阵（先于执行冻结）

| Bug | 验收与风险 | 测试层级 / 预定文件 | 命令与预期 |
| --- | --- | --- | --- |
| 01 | 改密、管理员改邮箱/密码/角色/封禁、找回成功均撤销旧 link/challenge；并发快照复查；其他用户隔离；清理失败原子回滚 | 真实 SQLite：`internal/store/credential_revocation_test.go`，既有 auth/login/reset/lifecycle suites | `go test ./internal/store -run Credential -count=1`；新目标先因残留凭据 FAIL，修复后 PASS |
| 02 | 候选激活后失败不切换旧快照；保留活动数据/备份；激活前失败无无谓停机；split前端故障只改前端 | lifecycle state-machine + workflow/script contract：`internal/lifecycle/*_test.go`、相关发布测试 | `go test ./internal/lifecycle -count=1` 及现有 workflow 测试；新恢复安全断言先 FAIL 后 PASS；真实发布/恢复 NOT RUN |
| 03 | 慢 HTTP handler 完成前协调函数不返回；正常响应完整；Shutdown超时可见；监听失败无死等；特殊waiter保留 | 本地真实 HTTP：`cmd/xboard/*shutdown*_test.go` | `go test ./cmd/xboard -count=1`，先暴露遗漏等待再 PASS |
| 04 | 所有密码路径共享两槽；饱和不调用 hasher、不变更用户；重试/错误/取消不泄槽；正常行为不变 | 低成本替身 HTTP/handler：`internal/httpapi/password_work_test.go` + registration/reset/admin tests | `go test ./internal/httpapi -run 'PasswordWork|PasswordHash|RegistrationHash|AdminUserGenerate' -count=1`；先 FAIL 后 PASS |
| 05 | 同一可信代理不同client独立限流；同client仍合并；未受信不能伪造；坏头和IPv6；node peer guard不变 | HTTP入口 + parser：`internal/httpapi/client_ip_test.go`、node security tests | `go test ./internal/httpapi -run 'ClientIP|TrustedProxy|Forwarded|Node.*Limit|RequestIP' -count=1`；先 FAIL 后 PASS |
| 06 | 第三claim租约内不动，过期标failed/unknown且无第四send；无候选也commit；迟到complete拒绝；重复/并发不改终态 | 真实 SQLite：`internal/store/*mail*recovery*_test.go` + mailer tests | `go test ./internal/store ./internal/mailer -run 'Mail|Reminder' -count=1`；目标先 FAIL 后 PASS |
| 07 | unhealthy/stopped正确目标可显式rollback；错误ID/image/DSN仍拒绝 | lifecycle backend contract | `go test ./internal/lifecycle -run Rollback -count=1`；新增case先 FAIL 后 PASS |
| 08 | 两类journal部分尾行Load→Append→Load；完整坏行拒绝；大小上限/完整历史/锁不回归 | 文件系统临时目录 | `go test ./internal/lifecycle -run Journal -count=1`；新case先 FAIL 后 PASS |
| 09 | TTL内编辑/状态/排序后重挂不复活旧数据；旧请求成功不覆盖刷新；旧失败不删除新缓存 | React interaction + cache unit：`PlanManagementPage.test.tsx`、`dataPrefetchCache.test.ts` | `pnpm --dir web exec vitest run src/features/plans/PlanManagementPage.test.tsx src/lib/dataPrefetchCache.test.ts`；先 FAIL 后 PASS |
| 10 | A慢B快、迟到错误、关闭/卸载、关闭后操作完成、正常详情流程 | React interaction：`OrderManagementPage.test.tsx` | 对应 `vitest run`；先 FAIL 后 PASS |
| 11 | 管理员/用户：输入草稿→确认→Escape，详情/草稿保留、无API关闭、焦点正常；正常confirm仍关闭 | React interaction：ticket dialog/page tests；本地浏览器 | 对应 `vitest run`；先 FAIL 后 PASS |

测试文件名/最小命令可随现有组织调整，验收预期不变。记录失败原因，编译/环境失败不算 Red。不得改断言迎合旧实现。

## 扩展验证和证据边界

1. 每组目标回归 Red→Green，随后模块测试和 `go vet` / TS typecheck / lint。
2. 公共 HTTP、凭据、缓存与停机路径改动扩大到 `go test ./...`、`go vet ./...`、`go build ./...`，全量前端测试/构建，支持时覆盖率与 race；工具不支持如实 NOT RUN。
3. 治理 JSON 调整执行 `go run ./cmd/projectctl generate`、`check`。原有历史证据陈旧时记录基线失败，不重标 SHA 伪装通过。
4. 真实浏览器仅本地合成环境关键流程；不得把组件测试称为浏览器验收。部署、真实恢复演练、线上业务验收留待授权，当前 NOT RUN。
5. 独立审查逐项对应代码和证据，重点检查丢数据、身份/权限、槽释放、状态终结、过期异步回调。修复审查问题后重跑受影响验证。
6. 本地提交按逻辑分组，最终记录精确提交、PASS/FAIL/NOT RUN和剩余风险；不声明整个项目无Bug。
