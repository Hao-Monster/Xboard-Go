# 二次复查测试证据

关联：[Bug List](BUG_LIST_REVIEW_2026-10-14.md)。基线 `07476e0229efd7ed326843c67dc611c7b1366c1a`，Windows 本地隔离测试，不含生产服务。

## 1. 测试矩阵与证据等级

| 目的 | 测试层级 / 实际用例 | 状态 | 不能据此推导的结论 |
| --- | --- | --- | --- |
| 结算顺序、关闭交付、券计次、购买码业务规则 | store隔离SQLite，T01的精确测试 | PASS | 不等于权益WS推送、永久用户天数奖励、复杂折抵已复现 |
| 超时及支付入口已有行为 | T01与T06的超时/checkout测试 | PASS | 没有覆盖活跃网关支付+超时任务交叉竞态 |
| 客户端协议识别、展示、兼容策略 | T02，subscription oracle | PASS | 不覆盖Hysteria1+Shadowrocket及首节点不兼容提示的目标组合 |
| V1/V2契约、失败令牌限流、邀请Bearer限制、可信IP | T03，httpapi httptest+隔离SQLite | PASS | 未请求线上；未专项执行V1内部订阅绕过HWID、令牌ID碰撞、Redis通知 |
| 下载账号与节点运行授权差异 | T04，store | PASS | 获取配置不等于获得节点联网权限 |
| 回滚/漂移既有保护 | T05，fake platform | PASS | 不含Docker；未执行rollback_failed两目标反例后再次status/upgrade/rollback |
| 邮件任务、佣金重试、正常日窗/迁移 | T06～T09，隔离SQLite、fake sender/platform | PASS | 不覆盖本报告新取消窗口、坏单阻塞、第三次失联、长批次时钟、秒级导入 |
| 自动保存既有规则 | T10，jsdom组件 | PASS | 不是线上敏感字段交互验收；不证明密钥轮换交互已获用户认可 |
| 多层Modal/NodeFilter Escape | T11的R24两现象+一对照 | PASS：复现现状 | 工单/全局Ctrl+K的完整真实浏览器路径仍NOT RUN |
| 快搜丢元数据 | T11的R25.1，受控Promise+真实UsersPage | PASS：复现组元数据丢失 | 套餐同guard由源码确认；不代表数据库元数据丢失 |
| 删除模板最后一页及恢复边界 | T11的R25.2，真实GiftCardManagementPage | PASS：复现空页；验证筛选恢复 | 兑换码页面同逻辑，独立动态用例NOT RUN |
| 用户订单详情竞态及取消筛选残留 | T11的R25.3a/b，真实UserOrdersPage | PASS：复现现状 | 不等于发生了错误付款，API均为隔离测试替身 |

T01～T09共58条顶层执行记录，其中超时边界测试重复运行一次，57个不同顶层测试。子测试不混入顶层统计：T01另有11个，T08另有8个，T02等含矩阵子用例但未汇总为独立总数。各已完成命令均为0失败、0跳过。

## 2. 环境及可复现命令

工作目录均为 `E:\CodeWorkstation\Xboard-Go`。后端使用Go1.26.8；前端使用已安装的pnpm、Vitest4.1.11和jsdom，不安装新依赖。Go精确测试使用临时隔离SQLite/fake platform/sender，不启动部署候选、容器或线上节点。

如本机默认Go较旧，可在**当前临时PowerShell进程**中使用已核实存在的缓存工具链，再运行以下命令；不修改用户永久环境：

```powershell
$reviewGoRoot = Join-Path $env:USERPROFILE 'go\pkg\mod\golang.org\toolchain@v0.0.1-go1.26.8.windows-amd64'
$env:GOROOT = $reviewGoRoot
$env:GOTOOLCHAIN = 'local'
$env:GOPROXY = 'off'
$env:GOSUMDB = 'off'
$reviewGo = Join-Path $reviewGoRoot 'bin\go.exe'
```

### T01 · 财务/礼品卡契约

```powershell
& $reviewGo test ./internal/store -run '^(TestDistributorSettlementIsPerFinancialOrderAndRepeatSafe|TestDistributorSettlementMonthBoundaries|TestDistributorOrderListSearchOwnershipAndClose|TestCreateOrderConsumesFixedCouponAndCancellationDoesNotRestoreLimit|TestGiftPurchaseCycleNewRenewalAndExpired|TestGiftPurchaseCycleRetainsOrRestoresResetSchedule|TestGiftPurchaseRejectsMismatchPendingAndDisabledWithoutConsumption|TestGiftCardPlanConditionsAndAssignmentMatchLegacyRules|TestOrderKindsResetAndTimeoutBoundary|TestStartPaymentCheckoutCalculatesFeeBindsOrderAndReusesCreatedAttempt)$' -count=1 -v
```

PASS；退出码0；10顶层+11子测试；失败0、跳过0；包报告8.127s。

### T02 · 订阅兼容及oracle

```powershell
& $reviewGo test ./internal/subscription -run 'Test(DetectClientMatchesLegacyFlagPrecedenceAndVersions|PrepareNodesGeneratesLegacyPasswordsPortsAndPresentation|VersionComparisonUsesNumericComponents|OracleRepresentativeOutputMatrix|OracleClientCompatibilityBoundaries)$' -count=1 -v
```

PASS；退出码0；5顶层；失败0、跳过0；包报告6.077s。

### T03 · HTTP契约及安全反证

```powershell
& $reviewGo test ./internal/httpapi -run 'Test(LegacyClientAppConfigTokenAndAccountStateContracts|LegacyClientAppConfigInvalidTokenLimiterIsSharedAcrossVersions|LegacyClientAppConfigV1MatchesRepresentativeNodeOracle|LegacyClientAppConfigV2ShapeSettingsAndPHPHash|LegacyInvitationMutationRequiresBearerAndStrictTransferForm|ClientIPTrustedProxySeparatesOrdinaryQuotas|AccountSessionsCanListAndRevokeOnlyOneSession|NativeAccessTokenLogoutAndRevokeAllLeaveCookieSessionActive|DistributorDirectSubscriptionHWIDAndClaimCompatibility)$' -count=1 -v
```

PASS；退出码0；9顶层；失败0、跳过0；包报告10.253s。

### T04 · 订阅账号与节点运行契约

```powershell
& $reviewGo test ./internal/store -run 'Test(SubscriptionAccountLookupPreservesLegacyEligibilityInputs|NodeRuntimeUsersPreserveAvailabilityRules|SubscriptionRenderConfigLoadsOnlyTheSelectedTemplate|CreateDistributorOrderBuildsIndependentCompletedSubscription)$' -count=1 -v
```

PASS；退出码0；4顶层；失败0、跳过0；包报告13.520s。

### T05 · lifecycle既有漂移与回滚保护

```powershell
& $reviewGo test ./internal/lifecycle -run '^(TestDeploymentFrontendRollbackDoesNotRestoreDatabase|TestDeploymentRollbackAllowsUnhealthyRecordedTarget|TestDeploymentStatusRejectsRuntimeImageDrift|TestDeploymentUpgradeRejectsRuntimeImageDriftBeforeResolvingTarget|TestDeploymentStatusRejectsRuntimeDatabaseDrift|TestDeploymentRollbackRejectsRuntimeDatabaseDriftBeforeRestoreOrActivation|TestDeploymentBackendFailurePreservesBackupAndActiveDatabase)$' -count=1 -v
```

PASS；退出码0；7顶层；失败0、跳过0；包报告1.386s。使用fake platform，没有实际服务切换。

### T06 · 后台任务/store已有恢复路径

```powershell
& $reviewGo test ./internal/store -run '^(TestAdminUserBulkMailClaimRetryCancelAndTemplateSnapshot|TestCommissionProcessingFailureRollsBackCreditAndRetryPaysExactlyOnce|TestCommissionDistributionAndConcurrentTransferPreserveMoney|TestOrderKindsResetAndTimeoutBoundary|TestProcessingOrderRecoveryAndResetPurchaseAreIdempotent|TestTelegramMessageOutboxClaimsRetriesCompletesAndCancels|TestMailLoginClaimCompleteAndExpiredPruningEraseCiphertext|TestPasswordResetRequestConcurrencyQueuesOneCurrentMailAndExpiryRejectsCode|TestDailyScheduleReconcilesImmediatelyAndRejectsStaleRevision|TestImportLegacyNodesPreservesMachineNodeScheduleAndDynamicTrafficBehavior)$' -count=1 -v
```

PASS；退出码0；10顶层；失败0、跳过0；包报告4.163s。

### T07 · 批量邮件已有租约/生命周期保护

```powershell
& $reviewGo test ./internal/bulkops -run '^(TestServiceRecoversExpiredMailLeaseAndStopsAfterThreeAttempts|TestServiceFailsAnExpiredThirdMailAttemptWithoutSendingAgain|TestUserLifecycleCancelledBulkMailNeverReachesSender)$' -count=1 -v
```

PASS；退出码0；3顶层；失败0、跳过0；包报告3.039s。不能将其等同于“取消job后processing目标被自动收敛”。

### T08 · 标准日窗与节点迁移

```powershell
& $reviewGo test ./internal/schedule ./internal/legacymigration -run '^(TestDailyWindowCrossMidnight|TestDailyWindowSameDay|TestDailyWindowRejectsInvalidTimes|TestReadNodesSnapshotPreservesOperationalNodeDomainWithoutPlaintextSecrets|TestReadNodesSnapshotRejectsInFlightReportReceipts|TestReadNodesSnapshotRejectsLossyNodeState)$' -count=1 -v
```

PASS；退出码0；6顶层+8子测试；失败0、跳过0；两个包分别1.099s、13.465s。

### T09 · 三类认证邮件及Telegram普通发送

```powershell
& $reviewGo test ./internal/mailer ./internal/telegrambot -run '^(TestWorkerDecryptsAndDeliversPasswordResetCodeBeforeTicketMail|TestWorkerDecryptsAndDeliversRegistrationEmailCode|TestWorkerDecryptsAndDeliversOneTimeLoginLink|TestWorkerDeliversPersistentTelegramMessageAndBoundsRetries)$' -count=1 -v
```

PASS；退出码0；4顶层；失败0、跳过0；两个包分别3.197s、3.259s。使用fake sender，没有真实邮件/Telegram外发。

### T10 · 自动保存契约

```powershell
pnpm --dir web exec vitest run src/components/AutoSaveForm.test.tsx src/features/settings/NodeAgentSettingsPage.test.tsx src/features/settings/SiteSettingsPage.test.tsx
```

PASS；退出码0；3文件、9测试；失败0、跳过0；29.25s。

### T11 · 本轮专项组件诊断

```powershell
pnpm --dir web exec vitest run --config "../docs- box/evidence/review.vitest.config.mts" --reporter verbose
```

PASS；退出码0；1文件、7测试；失败0、跳过0；10.11s。

| 诊断名称 | 结果 | 所用边界 |
| --- | --- | --- |
| R24 unsuspended nested Modal | 复现父层先收到Escape，子层未收到 | 真实Overlay组件，动态打开子层 |
| R24 suspended control | 正常：仅子层收到Escape | 显式挂起父层的正向对照 |
| R24 Modal + NodeFilter | 复现父层capture抢先，下拉未处理Escape | 真实两个组件 |
| R25.1 delayed metadata + quick input | 复现成功返回的组未写入且无失败提示 | 受控Promise，实际邮箱onChange |
| R25.2 delete last template on page2 | 复现空页+无上一页；筛选重置能恢复 | API契约替身匹配后端不钳制页码的行为 |
| R25.3a inverse detail response order | 复现A慢响应覆盖已显示的B | 两个受控Promise |
| R25.3b cancel in pending filter | 复现待支付表格仍显示已取消订单 | 成功取消API替身+真实页面 |

证据源：[frontend-review.test.tsx](evidence/frontend-review.test.tsx) 与 [review.vitest.config.mts](evidence/review.vitest.config.mts)。这些是审查诊断，不是应永久保持绿色的产品验收测试；修复后应将相应断言改为期望行为并纳入正规测试套件。本轮没有通过修改产品代码让诊断变绿。

## 3. 启动失败、中止与未运行项

- **Go准备阶段：环境失败，非产品失败。** 系统默认Go1.25.13低于go.mod要求1.26.8，禁网络的自动切换/校验及继承旧GOROOT导致测试前失败。使用已存在工具链并纠正进程GOROOT后，T01成功；没有下载新工具链或依赖。
- **首次诊断执行：中止，退出码1，无完整测试结论。** 初版使用mergeConfig，include数组被拼接，意外包含全套前端测试。确认合并语义后主动中止，改成直接覆盖include，随后T11仅执行目标7例并PASS。期间jsdom提示scrollTo未实现；不能把这次不完整运行统计成全套PASS或已定位的产品失败。
- **覆盖率：NOT RUN。** 本轮不是实现/修复；没有生成新的行/分支覆盖率，不引用旧覆盖率。
- **专项后端反例：NOT RUN。** Bug List中除已注明组件诊断外的条目，均需按各自验收场景补用例；正常/契约测试通过不等于新边界已覆盖。
- **真实浏览器、Shadowrocket/Surge/Surfboard导入和联网、Redis多实例与故障注入、外部网关/真实邮件、Docker回滚、生产迁移：NOT RUN。** 本轮未启动这些系统、未使用真实凭证或修改生产。
- **全量前后端回归、race、lint、typecheck、应用构建：NOT RUN。** 未修改应用源码；本轮诊断仅经过Vitest转译运行，不冒称已做独立TypeScript类型检查。
- **Red→Green修复证据：不适用。** 本轮是二次审查，没有修复；T11验证的是现有反例并提供复现路径。

包报告秒数不包含所有编译/进程启动的墙钟成本，不能作为性能基准。所有结果只针对上述精确基线与范围，不构成线上发布批准。

## 4. 文档交付检查

- `git diff --check`：PASS，退出码0；没有本轮应用源码diff。
- 对4个新产物逐一执行 `git diff --no-index --check -- NUL <文件>`：无空白错误输出；no-index因存在新文件差异返回1，此处不是测试失败。包装检查同时要求无诊断且返回码不大于1，最终退出码0。
- Markdown本地产物链接：PASS，均可解析到已保存文件。
- 编号/统计核对：原报告编号1～25完整；BL-01～BL-30完整；P1=4、P2=20、P3=6。
- 分工只读复查涵盖财务、订阅/认证及后台任务；成稿后财务与订阅/认证部分再次独立复核，编号/分类统计由主代理重新核对。修正文案时保留“新有效HWID有名额会自动登记”的正常行为，未把原报告未声称的“所有名称均导入失败”作为反证对象。
- 未修改、暂存或提交原有4个暂存文件及 `docs/HANDOVER.md`；未执行远端写操作。
