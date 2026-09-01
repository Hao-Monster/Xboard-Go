# 项目状态模型

禁止使用单一“完成百分比”。每个需求必须分别记录五个维度。

| 维度 | 值 | 含义 |
| --- | --- | --- |
| `scope_status` | `decided` / `blocked` | 业务行为已确定，或仍依赖未解决决策 |
| `implementation_status` | `implemented` / `partial` / `blocked` / `not_started` | 代码实现状态 |
| `verification_status` | `current` / `historical` / `partial` / `none` | 当前候选提交证据、旧证据、部分证据或无证据 |
| `migration_status` | `current` / `historical` / `partial` / `not_assessed` / `not_applicable` | 数据/协议迁移证据状态 |
| `acceptance_status` | `accepted` / `pending` / `rejected` | 是否达到正式验收口径 |

## 验收不变量

`accepted` 必须同时满足：

1. 范围已决定；
2. 实现完整；
3. 验证状态为 `current`，且证据包含精确 40 位提交哈希、日期、命令/用例和结果；
4. 迁移状态为 `current` 或 `not_applicable`；
5. 对应工作项和发布门禁没有未解决阻塞。

历史 CI 通过、旧分支测试、无法定位提交的截图或口头结论不能提升为 `current`。生成状态页只汇总事实源，不自行推断完成度。
