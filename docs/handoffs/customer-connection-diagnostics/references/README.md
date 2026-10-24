# 原样参考附件及来源

主交接文档是本目录上一级README/requirements/design/plan/matrix/checklist/prompt。附件用于核对具体协议、fixtures及设计历史；不能把附件中的旧状态/路径/授权当作Go当前指令。

| 文件 | 类型与使用方式 |
| --- | --- |
| [contract-schema.json](contract-schema.json) | 项目自定义schema1合同描述，原样复制；不是通用JSON Schema标准声明，按其枚举/family/预算编写验证器 |
| [contract-fixtures.json](contract-fixtures.json) | 跨语言合成测试向量，原样复制；expected不可为适配实现而改；JSON转义的NUL语义按主设计验证 |
| [PHP详细设计快照](php-design-20261022.md) | 2026-10-22复制时的原文件原字节快照；页首10/18“尚未实现”等是原历史措辞，PHP后来实现/发布不由此文档证明 |
| [PHP开发任务快照](php-development-plan-20261022.md) | 同期原样任务书；目录、PHP类名、分支、禁止改Go的旧任务边界、授权/进度文字仅解释原任务，不约束本次Go新设计 |
| [来源manifest](source-manifest.json) | 本次复制源仓库/完整HEAD、源相对路径、包内路径、字节数、SHA256、原始链接基准；核验完整性 |
| [Go源码定位索引](go-source-index.json) | 本地Go基线的相关文件路径/摘要与用途；是静态定位，不是测试/远端/运行证据 |

## 原文相对链接的解释

两个PHP快照保持原字节，因此原文Markdown相对链接仍以原仓库 `E:\CodeWorkstation\Xboardme\docs\design\` 为基准，**不是本交接包的导航**；脱离原仓库时链接可能不可点击。为保留可核验原始SHA256没有重写这些链接。所有Go要求已在主文档展开，接手不应依赖附件链接才能理解或执行。manifest记录原仓库/路径供完整源码环境定位，不需要访问生产服务器。

## 状态与权限

- 本次不修改附件、重新注册schema版本或宣称它是已批准Go实现。
- manifest的源HEAD是本地抓取基线，不等于当前远端或线上版本；共享Node修复候选由原负责人后续冻结。
- 附件中的PHP发布/部署约束只用于追溯。Go执行遵守当前Go AGENTS与本包A–D裁决；任何部署、凭据、迁移、卷创建/退役和跨仓库写入另核对实际授权。
- 包内不包含生产数据库、真实日志、密码、token、私钥、真实scope或可复用服务配置。

## 本地完整性核验

PowerShell在包根运行（只读）：

```powershell
$base=(Get-Location).Path; $m=Get-Content -LiteralPath (Join-Path $base 'references/source-manifest.json') -Raw | ConvertFrom-Json; foreach($f in $m.copied_files){$p=Join-Path $base $f.package_path; if((Get-Item -LiteralPath $p).Length -ne $f.bytes -or (Get-FileHash -LiteralPath $p -Algorithm SHA256).Hash.ToLowerInvariant() -ne $f.sha256){throw ('Reference mismatch: '+$f.package_path)}}; 'PASS reference bytes/SHA256'
```

这只证明附件完整，不证明业务或安全功能通过；Go业务T01–T28目前均NOT RUN。