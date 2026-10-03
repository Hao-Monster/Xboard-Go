# 自动部署官方 stable

此目录是独立、私有 GitHub 部署仓库的完整根目录。仅支持全新 Linux amd64、Docker Engine 和 Compose v2 已安装的服务器。应用使用官方 GHCR digest；部署脚本保存在自己的仓库，不执行上游 main 的漂移脚本。后续模板修订需要自行审查更新。

## 首次配置

1. 下载官方稳定版 Release 中的 `xboard-go-deploy-template.zip`，解压，以其内容创建自己的 **Private** GitHub 仓库。确认 `.github/workflows/deploy.yml` 已上传（隐藏目录不可遗漏），并位于默认分支。不要把外层 `self-hosted` 目录一起上传。
2. 服务器准备 Python 3、Docker Engine、Compose v2，以及专用 SSH 账号。该账号须可通过 `sudo -n python3` 执行部署，因此实际拥有服务器管理权限；仅将专用部署私钥交给此私有仓库。SSH 端口必须允许 GitHub hosted runner 连接。
3. 仓库 Settings → Secrets and variables → Actions → Secrets 添加 `SSH_PRIVATE_KEY`（专用私钥）及 `SSH_KNOWN_HOSTS`（由服务器控制台等可信渠道取得并核对的完整 known_hosts 行；非 22 端口格式为 `[host]:port`）。不能仅信任首次网络扫描结果。
4. Variables 添加下表配置。首次安装前完成域名解析与 TLS 反向代理，转发到服务器 `127.0.0.1:7080`。本模板不安装反向代理、不签发证书。

| Variable | 值 |
|---|---|
| DEPLOY_HOST | SSH 主机名或 IPv4 |
| DEPLOY_USER | SSH 专用账号 |
| DEPLOY_PORT | 默认 22 |
| PANEL_URL | `https://panel.example.com`，无尾斜线 |
| ADMIN_EMAIL | 首次管理员邮箱 |
| INSTALL_DIR | 默认 `/opt/xboard-go`，仅支持 /opt 的直接子目录 |
| APP_PORT | 默认 7080 |
| BIND_ADDRESS | 默认 127.0.0.1；需要直接公网暴露时才选 0.0.0.0 |
| AUTO_UPDATE | 首次安装验证后设为 `true` |

5. Actions → Deploy Xboard-Go stable → Run workflow → mode 选择 `install`。首次安装不会接管已有目录、项目容器或数据卷。
6. 成功后在自己的安全服务器会话中读取 `/opt/xboard-go/secrets/admin-password` 和 `.env` 的 `XBOARD_ADMIN_PATH`，登录 `PANEL_URL/该路径/`。不要把密码贴入 Actions 日志。检查外部 HTTPS 和登录后再开启 AUTO_UPDATE。

## 自动更新与失败处理

- 计划每 15 分钟检查一次，GitHub 调度可能延迟。未安装时定时运行跳过；只有手动 install 才初始化。
- stable 镜像标签、Release 元数据的版本、源码 SHA、digest 必须一致。部署使用 digest。相同 digest 保持当前版本；未知版本、降级、跨主版本及没有兼容声明的升级将失败并保持原服务。
- 兼容更新先拉镜像，再停止应用，以复制 SQLite/WAL、附件、应用备份卷、配置和加密密钥形成一致恢复集。此过程有短暂停机。恢复集保存在 `/opt/xboard-go-backup-时间-随机值/`，权限 0700；不自动清理。请监控磁盘容量，并自行将恢复集加密备份到异机。
- 更新失败会停止应用，保留数据和恢复集，不自动恢复数据库或假称回滚成功。持久化 `deployment-failed` 文件阻止后续计划任务自动重试；只有所有者完成恢复并核对 `.env`、installed.json、容器 digest 与健康状态一致后，才可移除该标记并重新启用 AUTO_UPDATE。数据库兼容性未确认时不要直接切回旧镜像。
- 需要人工批准的不兼容升级没有一键绕过；先确认官方迁移说明、备份和停机计划再单独实施。手动 update 同样尊重兼容范围。
- 安装后不会用新 Variables 改写域名、密钥或已有配置。配置变更由服务器所有者单独管理。
- 若升级失败，先将 AUTO_UPDATE 设为 `false`，查看失败步骤；通过安全会话检查容器日志（注意脱敏）。恢复需从同一个恢复集一起还原配置/加密密钥和完整 data 卷，并使用其 installed.json 记录的旧 digest；恢复备份会丢弃备份时间以后的写入，必须由所有者确认后执行。

## 本地校验

`python3 -m unittest discover -s . -p 'test_*.py' -v`

此命令验证元数据、版本与部署失败处理；真实 Docker/SSH/公网 TLS 验收需在具备相应环境的 CI 或服务器执行。
