# SonarQube 质量中心（DBX 插件）

把 SonarQube 的老旧英文界面换成面向开发者的中文工作台。插件只读连接 SonarQube 的 PostgreSQL 数据库，直接读取扫描根源数据，不调用 Sonar Web API，也不向 Sonar 写入任何内容。

- 插件 ID：`io.github.smallrob.sonar-quality`
- 当前版本：`0.1.7`
- 发布者：`smallrob`
- 权限：`host.workbench`
- 许可证：Apache-2.0

## 功能

- **项目切换**：同时查看多个 Sonar 项目，区分评审项目与普通项目。
- **扫描历史**：按分析记录查看问题总量、质量门禁、耗时与指标变化。
- **问题筛选**：按类型、严重级别、规则、目录、文件、关键字筛选，服务端排序分页；规则与目录都支持搜索、分页与跨页多选。
- **Markdown 导出**：按所选严重度导出当前筛选范围内的问题明细，按等级分组，包含规则键、文件行号、工作量、描述与处理建议；样例见 `docs/samples/sonarqube-issues-sample.md`。
- **中文详情**：问题标题、规则说明、修复建议、变更记录与源码上下文；源码从 `file_sources` 解码，不依赖 Sonar Web。详情浮窗打开时锁定列表滚动，只允许详情内容上下滚动，关闭后恢复。
- **全局统计**：跨项目质量矩阵、问题类型分布、技术债、覆盖率与安全热点汇总。
- **扫描对比**：基于本地留存的问题快照，对比两次扫描的新增、持续与已消失问题。
- **只读安全**：连接强制 `default_transaction_read_only=on`，过滤值参数化，排序白名单，不开放任意 SQL。

社区版 SonarQube 只有 `main` 分支，界面不提供分支选择器；后端仍兼容实际分支记录。

## 目录

```text
backend/                 Go sidecar（JSON-RPC、只读 PG、源码解码、快照）
backend/dbxpluginsdk/    官方 Go SDK 的本地固定副本
backend/vendor/          离线构建依赖
ui/index.html            单文件中文 Workbench，无外部前端依赖
scripts/                 五平台打包、Docker 平铺包、签名、前端与联调检查
store/                   市场候选与发布者信息
docs/                    部署与实现说明
manifest.json            插件清单
```

## 构建与测试

需要 Go 1.22+、Python 3.10+、Node.js（仅用于前端语法检查）。

```powershell
$go = "C:\asdm-tools\_tools\go\bin\go.exe"
& $go -C backend test ./...
py -3.12 scripts/verify_frontend.py
```

离线构建五平台未签名市场候选：

```powershell
$env:GO_BINARY = "C:\asdm-tools\_tools\go\bin\go.exe"
py -3.12 scripts/build-candidates.py
```

产物在 `dist/`：linux-x64、linux-arm64、darwin-x64、darwin-arm64、windows-x64。

## 自托管 Docker DBX 部署

DBX Store 的五平台包使用 `bin/<target>/` 布局；部分 Docker DBX Web 安装接口要求 manifest 指定的平铺路径 `bin/<binary>`。部署到自托管 Docker 时先生成平铺包，再用仓库签名：

```powershell
py -3.12 scripts/build-server-package.py `
  dist/io.github.smallrob.sonar-quality-0.1.7-linux-x64.dbxp `
  .build/server/io.github.smallrob.sonar-quality-0.1.7-linux-x64.dbxp `
  --target linux-x64

py -3.12 scripts/sign-package.py `
  .build/server/io.github.smallrob.sonar-quality-0.1.7-linux-x64.dbxp `
  .build/signed/io.github.smallrob.sonar-quality-0.1.7-linux-x64.dbxp `
  --artifact-metadata .build/signed/io.github.smallrob.sonar-quality-0.1.7-linux-x64.artifact.json `
  --target linux-x64
```

签名使用 Ed25519，签名对象是包内 `checksums.json` 的原始字节，结果写入 `signature.json`。私钥只放在不入库的 `.build/repo-signing.env`。

> 市场候选保持未签名，由 DBX Store 在提审通过后用官方密钥签名。自托管部署使用内部仓库密钥（`smallrob.internal-2026`），需要先把对应公钥写入服务器信任库。两者用途不同，不要混用。

服务器安装、信任公钥、配置与验收步骤见 [docs/部署-193-Docker-DBX.md](docs/部署-193-Docker-DBX.md)。

## 配置

在插件 Workbench 的「连接设置」填写 PostgreSQL 主机、端口、数据库、用户名和密码。密码只写入 DBX 插件数据目录下的 `config.json`（`0600`），公共配置接口只返回 `hasPassword`，不回传明文。

默认按 `-Review` 后缀识别评审项目；可在设置中修改。问题快照默认保留 90 天，后台每 60 秒为有扫描记录的项目补齐最新快照。

## 发布前检查

- 五平台包不得包含 `signature.json`，市场候选的 `sha256` 与 `size` 必须由构建脚本生成。
- 不得提交 `.build/`、真实数据库凭据、签名私钥。
- 正式上架前仍需：README/提审材料、GitHub Actions、Git 提交与 `v0.1.7` tag、Release 资产、DBX Store 候选 PR。
