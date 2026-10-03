# 官方 stable 镜像发布

治理：DIST-001 / Issue #331 / M3。此流程只发布应用镜像，不部署维护者或第三方服务器。

## 发布入口

合入 main 后，在 Actions 中运行 `Publish stable image`，分支选择 main：

- `version`：未使用的 `vMAJOR.MINOR.PATCH`，例如 `v1.0.0`（示例，不代表已发布）。
- `revision`：已合入 main 的完整 40 位提交 SHA。
- `compatible_from`：可选，已验证可以自动升级到本版的最低稳定版本，必须低于目标且属于同一主版本。填写即声明此范围内升级已验证；留空则要求下游人工确认。首次安装不属于升级。

不需要新增 PAT。工作流使用当前仓库的 GITHUB_TOKEN，权限为 contents:write 与 packages:write。
组织策略仍须允许该仓库发布 Packages；若已有同名包，须授予当前仓库 Actions 写权限。

流程构建 Linux amd64 合并镜像，验证首次安装、页面与健康接口、节点下载、重启、保留密钥、拒绝覆盖安装，以及启动失败后恢复旧镜像。
这些检查不证明任意历史数据库都可以升级，也不替代业务验收。

## 第一次发布时的包可见性

目标包为 `ghcr.io/hao-monster/xboard-go`。首次上传候选镜像后，GHCR 包可能默认为私有。
工作流用空 Docker 配置验证匿名拉取，失败即停止，不更新 stable。
包所有者在 GitHub 的 Packages → xboard-go → Package settings → Change visibility 中选择 Public 后，重新运行工作流。
公开镜像任何人可下载；GitHub 不允许将公开包再改回私有。需由所有者确认这一选择。

参考：https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility

## 发布产物与下游契约

- `ghcr.io/hao-monster/xboard-go:vMAJOR.MINOR.PATCH`：流程拒绝覆盖已有版本。
- `ghcr.io/hao-monster/xboard-go:stable`：所有检查完成后指向新版本。
- GitHub 同版本 Release 的 `release.json`：源码 revision、固定 image digest、platforms、升级策略。

下游应先解析 stable 镜像的 digest 与 version 标签，再读取该版本的 release.json，并核对 image digest 一致；实际部署必须使用 digest。
不要使用 GitHub latest Release API：仓库还发布内部测试包和节点版本。
仅填写 compatible_from 时 automatic_upgrade=true；下游必须同时核对当前版本位于 [compatible_from, version) 且主版本一致，再按已实现的备份与迁移流程升级。未知版本、跨主版本、降级或 automatic_upgrade=false 时停止自动升级。
此范围来自发布者验证后的声明，工作流自身不会证明历史数据迁移兼容。第三方部署模板、备份迁移策略是后续接入工作。

## 失败与重试

候选镜像标签包含 run ID 和 attempt，失败不会覆盖原 stable。
若版本镜像或同名 Git 标签已经写入，重新运行会拒绝覆盖：检查失败步骤，使用新版本号重新发布。
Release 与 registry 不支持跨服务原子提交；若 Release 创建成功但 stable 推送失败，旧 stable 仍是下游发现入口，不能仅凭 Release 存在开始更新。
若 stable 推送返回网络错误，结果可能不确定，先核对 registry digest，不能假定旧版本仍在。
正常发布要求版本递增，源码不早于当前 stable；不支持隐式降级。Git 标签使用原子创建，冲突即停止。
并发发布由同一个 concurrency group 串行化。不要在此流程之外手动修改版本或 stable 标签。

## 验证范围

本地：`python -m unittest discover -s deploy -p stable_release_test.py -v`、actionlint、projectctl check。
真实构建、安装、registry 推送与匿名访问以首次 main 发布运行的实际结果为准。
