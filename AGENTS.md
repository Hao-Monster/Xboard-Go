# Xboard-Go 项目协作规则

本文件适用于整个仓库，并补充全局 Agent 规则。

## 项目事实源

- `docs/project/*.json` 是范围、需求、决策、风险、工作项和发布门禁的唯一版本化事实源。
- `docs/project/STATUS.md` 由 `go run ./cmd/projectctl generate` 生成，不得手工编辑。
- `docs-dev/` 是本地历史取证区，保持 Git 忽略；其中内容不是当前权威需求，任何结论必须经过审查后进入 `docs/project/`。
- 开始功能、修复、迁移或发布工作前，先定位对应需求 ID 或工作项 ID，并确认 GitHub Issue 和里程碑。

## 变更流程

1. PR 正文必须填写需求 ID 或工作项 ID、GitHub 里程碑和关联 Issue；确实不适用时填写 `N/A: 原因`。
2. 状态不能只写“完成”。分别更新范围、实现、验证、迁移和验收状态，并附精确提交与可复现证据。
3. 历史测试、旧分支测试或未绑定提交的结果只能标记为 `historical`，不得提升为 `current` 或 `accepted`。
4. 被决策阻塞的需求不得实现未经确认的业务语义；可以继续只读调查、测试设计和不受影响的工程工作。
5. 合并前运行 `go run ./cmd/projectctl check`；更改治理数据后运行 `go run ./cmd/projectctl generate`。
6. 不直接在 `main` 开发，不绕过分支保护，不手工创建未经门禁验证的 Tag 或 Release。

## 安全与环境边界

- 当前只允许本地和隔离 CI/测试环境；生产部署和生产数据操作不在默认授权范围内。
- Go 主进程禁止执行任意 PHP 或用户上传脚本；受信扩展边界见 `docs/project/compatibility-exceptions.json`。
- 不将密钥、生产数据、原始请求正文、邮件正文或旧 PHP 队列载荷写入仓库、Issue、日志或测试快照。
