# 2026-10-10 恢复链路修复执行方案

本方案在新增回归测试和生产代码修改前记录。统一需求基线见主工作区 `docs/testing/bug-fix-plan-2026-10-10.md`。

- 基线：`2acd15f9f917b9c75ed39c0ac644cd9e2427fec1`。
- 缺陷：BUG-20261010-02、07、08；Requirement / Work Item `OPS-002`，Issue #117，M2。
- 独享 detached worktree；只修改 lifecycle、其 CLI/测试、Legacy parity 部署步骤及直接文档。
- 不推送、不部署、不恢复真实数据库，不访问远程服务。测试只用临时文件、隔离 SQLite 和容器命令替身。

## 已确认行为与顺序

1. 先新增回归用例，确认因目标行为失败；随后实现，执行目标与模块回归、vet、构建和工作流检查。
2. 候选可能触及数据库以后失败时，停止候选并保留当前数据库/WAL、配置和预升级备份，返回失败；禁止自动回退旧快照。停止失败必须披露。候选激活前失败不停止原服务。
3. Legacy parity 保存失败标记以阻止自动重试；远端全部版本/健康/公开检查成功才报告成功。仅前端/网关变化保留不触碰数据库的自动回退。
4. 显式 rollback 可以接受 `upgraded` 或 `target_failed`，不要求当前目标健康，但必须仍匹配镜像、数据库身份和备份。显式快照恢复有丢弃备份后写入的风险，不能称为无损恢复。
5. journal 追加前保留并验证完整记录，仅去除无换行的半条尾部；非空但没有完整记录、完整坏 JSON 或上限超出必须失败且不能改坏原文件。combined/split 使用同一实现。

## 测试矩阵

| 编号 | 验收场景 | 证据 |
| --- | --- | --- |
| D02-1 | combined/split backend 快照后写入，候选失败不恢复旧库，停止候选且错误状态可读 | lifecycle 状态机及真实临时 SQLite/备份集成测试 |
| D02-2 | 成功升级不停止；激活前备份/校验错误不停止旧版本 | lifecycle 单元测试 |
| D02-3 | 停止失败、取消 context、状态落盘失败不得掩盖，清理使用独立有界 context | lifecycle 和 Docker 参数向量测试 |
| D02-4 | split 仅 frontend/gateway 失败仍回旧组件，不备份/恢复数据库 | split lifecycle 回归 |
| D02-5 | 部署健康失败/命令失败/中断/公开验证失败：标记、停止、保留 DB/配置；无 restore；失败标记阻止下一次发布 | 读取实际工作流的本地 Bash 测试，所有 Docker/HTTP 均替身 |
| D07-1 | 正确的 unhealthy/stopped 目标可显式回滚；失败候选可显式恢复 | combined/split 单元测试 |
| D07-2 | 错镜像/DSN/身份/备份仍拒绝，错误门禁之前无恢复副作用 | lifecycle 单元测试 |
| D08-1 | 两类 journal 的完整记录+半条尾部，Load→Append→Load得到新状态且保留原历史 | 临时文件系统测试 |
| D08-2 | 完整坏 JSON、仅半条、超限均拒绝且文件不变；正常空/完整日志可追加 | 临时文件系统测试及既有回归 |

计划命令：目标 `go test ./internal/lifecycle -run 'Upgrade|Rollback|Journal' -count=1`，模块 `go test ./internal/lifecycle ./cmd/xboard-lifecycle -count=1`、`go vet ./internal/lifecycle ./cmd/xboard-lifecycle`、`go build ./cmd/xboard-lifecycle`；部署脚本对应本地测试、工作流语法、`git diff --check`。可用时运行 lifecycle race/覆盖率；不可用如实记录 NOT RUN。

## 风险与未授权验证

失败停机是明确取舍：保留最新数据优先于自动恢复可用性。运行中后台任务也会写入，不能把断开公网误认为全局停写。真实 Docker/CI 发布、线上恢复演练和业务验收均 NOT RUN，需后续授权。

## 执行记录

环境：Windows amd64，Go 1.26.8，Python 3.13.7，Git Bash；race 使用已安装的 MinGW GCC。所有数据库均为测试临时目录内的 SQLite，工作流 Docker/HTTP 边界使用本地替身，未调用 SSH；没有执行真实发布或恢复。以下最终测试对应的生产代码与测试已提交为 `0c8658b6397efe175d74c6ee6e103533e1da32e5`，后续补充仅更新文档。

### Red → Green

生产代码修改前执行：

| 命令 | 结果 / 退出码 | 数量和耗时 | 目标失败证据 |
| --- | --- | --- | --- |
| `go test ./internal/lifecycle -run 'TestJournalAppend\|TestRollbackAllowsRecordedUnhealthyTarget\|TestRollbackRejectsDatabaseDriftBeforeRestore\|TestFailedUpgrade' -count=1 -json` | FAIL / 1 | 6 项顶层、18 个叶场景失败；0 通过、0 跳过；包 3.773s | 半条尾行追加后坏 JSON；完整坏记录被接受；不健康目标不能回滚；DSN 漂移未拒绝；真实 SQLite 快照后第二条已确认写入丢失，只剩 1 条；未停止候选及清理错误丢失 |
| `go test ./internal/lifecycle -run 'TestFailedBackendUpgrade\|TestFailedFrontendUpgrade\|TestDeploymentRollbackAcceptsRecordedFailedTarget' -count=1 -v` | FAIL / 1 | 1 通过、2 失败、0 跳过；包 1.283s | backend 失败自动恢复旧库；`target_failed` 不能显式恢复；frontend-only 兼容路径通过 |
| `python -m unittest discover -s deploy -p legacy_parity_deploy_test.py -v`（初始 7 项） | FAIL / 1 | 0 通过、7 失败、0 跳过；9.359s | 实际 workflow shell 仍恢复旧库，缺少失败标记/统一公开检查失败边界 |
| `go test ./internal/lifecycle -run TestJournalAppendRejectsCorruptCompleteRecordsWithoutMutation -count=1`（补充空白历史场景） | FAIL / 1 | combined/split 的 2 个空白历史场景失败；包 1.917s | 非空但没有有效完整状态的 journal 被接受；随后补齐拒绝且不修改原文件的检查 |

最终 Green 与回归：

| 命令 | 结果 / 退出码 | 通过 / 失败 / 跳过 | 耗时和说明 |
| --- | --- | --- | --- |
| `go test ./internal/lifecycle ./cmd/xboard-lifecycle -count=1 -json -coverprofile=durability-coverage.out` | PASS / 0 | 61 项顶层测试；包含子测试为 109 条 PASS 事件 / 0 / 0 | lifecycle 14.245s；CLI 12.061s；SQLite 快照后 2 条记录均保留，备份可 Verify，未调用 Restore |
| `go test -race ./internal/lifecycle -count=1 -json` | PASS / 0 | 1 包通过；逐项数量未单独汇总 / 0 / 0 | 包 48.617s；未报告 data race；首次 race 编译时间不计入此包耗时 |
| `python -m unittest discover -s deploy -p legacy_parity_deploy_test.py -v`（最终 12 项） | PASS / 0 | 12 / 0 / 0 | 29.204s；覆盖健康/超时/中断/loopback/公开检查/部分激活/停止/标记删除失败、备份与配置前置失败、旧标记拦截及完整成功 |
| `go vet ./internal/lifecycle ./cmd/xboard-lifecycle` | PASS / 0 | 静态检查，不适用测试计数 | 未单独计时 |
| `go build -o $env:TEMP\xboard-lifecycle-durability-check.exe ./cmd/xboard-lifecycle` | PASS / 0 | 构建，不适用测试计数 | 未单独计时；产物在仓库外 |
| `git diff --check` | PASS / 0 | 差异检查，不适用测试计数 | 未单独计时 |
| `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 -shellcheck= .github/workflows/legacy-parity.yml` | FAIL / 1 | 2 条既有 runner label 诊断 | 修改前 `HEAD` 即基线 `2acd15f9f917b9c75ed39c0ac644cd9e2427fec1`；将该 revision 的 workflow 送入同版本 actionlint 后出现相同两条错误，不是本次引入 |
| `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 -shellcheck= -config-file .local/actionlint-durability.yaml .github/workflows/legacy-parity.yml` | PASS / 0 | 工作流语法/表达式检查，不适用测试计数 | 临时配置仅声明仓库已使用的 self-hosted label `xboard-go-legacy-parity`，未关闭 actionlint 规则；未单独计时 |

复现 actionlint 检查时，先在仓库根的 `.local/actionlint-durability.yaml` 保存以下内容；不提交工作站专属配置：

```yaml
self-hosted-runner:
  labels:
    - xboard-go-legacy-parity
```

`-shellcheck=` 沿用当前检查方式，未执行独立 ShellCheck；实际远端 heredoc 的 shell 行为由上述 12 项隔离测试执行验证。

矩阵 D02-1 至 D08-2 的本地验证均 PASS；这里只声明矩阵列出的验证层级，不把命令替身称为真实 Docker 集成。主负责者另行只读复核本组生产 diff，未发现阻塞项。

### 覆盖率与剩余证据缺口

Go 原生输出是语句覆盖率，不是行或分支覆盖率。本轮 `internal/lifecycle` 为 **69.0% statements**，`cmd/xboard-lifecycle` 为 **46.0% statements**，合计 **67.2% statements**。`go tool cover -func` 核对：新增 Stop/StopDeployment 为 100%；journal append 为 73.9%、历史记录校验为 90.0%；combined Upgrade/Rollback 为 86.7%/70.2%，split Upgrade/Rollback 为 81.8%/71.0%。没有新增或修改行级覆盖率统计，不能从这些包级数字推断全部变更路径都已覆盖。

本轮生成的 profile 已移到 Git-ignored `.local/durability-coverage.out`。重跑时可将覆盖率参数写为 `"-coverprofile=.local/durability-coverage.out"`，然后执行 `go tool cover -func .local/durability-coverage.out`；目录须先存在，profile 不提交。

- 行覆盖率、分支覆盖率：NOT RUN；本轮 Go 覆盖率工具没有提供对应口径。
- 真实磁盘 read/truncate/seek/write/sync 故障注入、断电与磁盘满恢复：NOT RUN；已测完整损坏/部分尾行、上限、状态落盘失败替身，仍不能替代现场存储故障验证。
- 真实 Docker ExecRunner、CLI 在实际容器拓扑中的调度、CI/远端发布和恢复演练：NOT RUN；没有相应执行授权，替身仅验证命令目标、参数与错误传播。
- 全项目回归与业务验收：本组未运行；本组提供两包定向回归，交由主负责者按整批变更影响面整合验证。
- 无新增跳过/only、未弱化断言或关闭门禁。原先期待 backend 自动恢复快照的测试，依据已确认的保数据契约改为检查停止、当前 DSN/数据与备份保留；frontend-only 回退及显式恢复身份门禁继续验证。

BUG-20261010-02 修复的是**失败升级不得自动恢复旧快照导致丢失新写入**，不是实现所有 rollback 无损。显式 snapshot rollback 仍可能丢弃快照后写入或重放外部副作用；真实恢复须另行取得对应环境与数据操作授权，详见 `docs/operations/lifecycle-recovery.md`。
