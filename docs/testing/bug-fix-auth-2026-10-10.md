# BUG-20261010-01 / 04 / 05：本地修复证据

基线 `2acd15f9f917b9c75ed39c0ac644cd9e2427fec1`。预期先冻结于 `bug-fix-plan-2026-10-10.md`，本文件只记录随后观察到的实现与测试。Windows/amd64，Go 1.26.8（`GOTOOLCHAIN=auto GOPROXY=off`）；所有身份、邮件和数据库均为隔离 QA fixture，没有真实邮件投递或线上探测。

## 实现

| Bug | 根因修复 | 验收边界 |
| --- | --- | --- |
| 01 | 在 `revokeAllCredentialsTx` 的原事务内撤销待用 link/challenge，取消 pending/claimed 的相关邮件、清空密文与claim；生命周期复用同一清理 | 自助改密、管理员重置/改密/改邮箱/封禁/改admin、staff或distributor角色、密码找回成功的旧凭据失效；其他用户保留；清理故障整笔回滚 |
| 04 | 同步密码工作复用原有两槽池；成功登录保留槽直到升级/状态冲突复查与凭据创建结束；失败立即释放 | 覆盖现代/兼容登录、用户改密、管理员创建/现代与兼容编辑/重置；注册、找回和批量生成仍用同一槽池；不新增密码算法/参数 |
| 05 | HTTP入口解析可信代理链并将client IP放入请求context；`requestIP`读它，原始 `RemoteAddr` 保留 | 不同客户端独立配额；相同client合并；未受信/坏头/重复头/过长头回落peer；节点独立peer和credential限制不变 |

无 schema 迁移。已经投递的邮件无法追回，但旧凭据在兑换处失效。配置中的可信代理仍是信任边界，本次不扩大受信网段。同步新入口忙时返回429与 `Retry-After: 1`；已有注册/找回错误码保持，兼容管理员入口保持其原有错误包络。

## Red → Green

| 命令 | 修复前观察 | 修复后观察 |
| --- | --- | --- |
| `go test ./internal/store -run '^TestCredential' -count=1` | FAIL/exit1，2顶层及8子场景失败，package 5.721s；旧link/challenge保留，清理注入失败没有触发事务失败 | PASS/exit0，package7.387s |
| `go test ./internal/httpapi -run '^TestPasswordWork' -count=1` | FAIL/exit1，package8.318s；饱和仍计算/修改账号、Verify/Hash在槽外。兼容编辑最初fixture命中了禁止改自身角色检查，修正为另一管理员后单例仍因未限流真实FAIL（6.659s） | PASS，纳入下述40事件目标验证 |
| `go test ./internal/httpapi -run '^TestClientIP' -count=1` | FAIL/exit1，package7.801s；可信代理和可信多跳链两子例把独立client计入同一配额 | PASS，纳入下述目标及节点回归 |

凭据测试第一版为两次链接兑换复用了session ID，额外观察到唯一键失败；在实现前改为各自唯一ID，避免该约束混淆无效凭据断言。编译错误、fixture错误均不计作目标Red。

## 已执行扩展检查

- `go test ./internal/httpapi -run 'TestPasswordWork|TestClientIP|TestRequestIP|TestSECNODE005' -count=1`：PASS，exit0，package19.502s，包含旧节点peer/credential保护回归。
- `go test ./internal/store ./internal/httpapi -run '^(TestCredential|TestPasswordWork|TestClientIP|TestUserLifecycleMailOwnershipMigrationOnlyBackfillsProvenLatestReset)' -count=1 -json`：PASS，exit0，40个通过事件（含子测试），0失败/0跳过；store15.183s，httpapi7.551s。
- `go test ./internal/store ./internal/httpapi -count=1`：第一轮FAIL，exit1；httpapi PASS71.097s；store162.726s中只有历史迁移fixture失败（下述），不隐瞒该回归。
- 修正历史fixture后，`go test ./internal/store -count=1`：PASS，exit0，package171.755s；全部store模块回归通过。
- `go vet ./internal/store ./internal/httpapi`：PASS。
- `CGO_ENABLED=1 go test -race ./internal/httpapi -run '^TestPasswordWork' -count=1`：PASS，exit0，package48.446s；本机可用 `C:\msys64\ucrt64\bin\gcc.exe`。这是有界替身测试，不是Argon2压力测试。
- `go run ./cmd/projectctl generate`、`go run ./cmd/projectctl check`：PASS；没有把旧验收证据重新绑定到当前代码。

### 历史迁移fixture的处理

`TestUserLifecycleMailOwnershipMigrationOnlyBackfillsProvenLatestReset` 要构造“旧版本改邮箱后仍保留reset challenge”的历史库，用当前 `UpdateAdminUser` 构造会被本次修复正确撤销challenge，因此旧样本不再成立。只把这一步改成明确模拟旧版本状态的SQL，保留所有owner归属、禁止重绑、未知旧邮件不被新用户接管和schema校验断言；没有弱化迁移预期。原失败用例已单独复验PASS；完整store重跑及集成后全仓结果见最终总报告。

## 独立审查补强

独立只读审查没有发现本组生产diff的阻塞问题，并确认历史fixture未弱化断言。审查指出原隔离用例没有明确检查另一用户的邮件claim/密文和bearer；已补齐两种outbox的完整状态快照对比、bearer所属用户、两种登录链接仍可兑换，并增加distributor角色变更场景。

补充用例首次因缺少合法分销商名称而被输入校验拒绝（exit1，7.894s）；补齐合成名称后，`go test ./internal/store -run '^TestCredential' -count=1` PASS/exit0，2个顶层、9个子场景，0失败/0跳过，11.400s。前一次属于测试fixture错误，不作为新产品缺陷的Red；生产代码未为迎合测试改动。

05仅统一 `requestIP` 消费者的可信代理处理。礼品卡兑换审计中的既有独立 `clientIP(RemoteAddr)` 和工单位置专属解析没有在本任务修改，不能据此宣称全项目IP处理均已统一。

## 证据边界

Go原生coverage提供statement口径，不提供分支覆盖率。全仓覆盖率、独立审查和最终提交绑定在集成总报告记录；本文件不将有限目标测试标成完整安全审计。真实SMTP、部署及线上业务验收均未执行；等待用户授权。
