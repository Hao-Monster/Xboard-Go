# Contributing to Xboard-Go

## 开始之前

1. 从最新 `main` 创建 `codex/` 或项目约定前缀的分支。
2. 在 [`docs/project/work-items.json`](docs/project/work-items.json) 和 [`docs/project/requirements.json`](docs/project/requirements.json) 中找到工作项；没有对应项时先建立治理记录和 GitHub Issue。
3. 确认工作项的里程碑、决策依赖和风险，不把待决策业务语义写入代码。

## 完成标准

- 改动最小但覆盖所有受影响入口、失败路径和兼容性边界。
- 测试结果绑定到精确提交；旧结果只能作为历史证据。
- 更新受影响的需求、工作项、风险或发布门禁；运行状态生成器。
- PR 使用模板并关联 Issue 和 GitHub 里程碑。

## 本地检查

治理变更至少运行：

```powershell
go test ./internal/projectgovernance ./cmd/projectctl
go run ./cmd/projectctl generate
go run ./cmd/projectctl check
```

代码变更还应运行与影响范围相符的 Go/Web/浏览器检查。CI 是合并门禁，不替代本地目标验证。

## 版本与发布

版本策略见 [`docs/project/VERSIONING.md`](docs/project/VERSIONING.md)。未经门禁验证，不创建 Tag、GitHub Release 或生产部署。
