# 发布到 DBX Store

本文件描述如何把 SonarQube Quality Hub 提交到 [t8y2/dbx-store](https://github.com/t8y2/dbx-store)。
规范来源是该仓库的 `CONTRIBUTING.md`，本文只讲本插件的具体操作。

## 一句话流程

1. 把本仓库推到 GitHub 公开仓库，打 tag 并发布 Release；
2. Release 触发的 CI 按 5 个平台构建未签名候选 `.dbxp`，并生成 `release-candidates.json`；
3. Fork `t8y2/dbx-store`，开一个 PR，带上 `publishers/smallrob.json` 与 `candidates/io.github.smallrob.sonar-quality.json`；
4. DBX Store 评审并用官方密钥签名，签名产物回写到同一个 PR；
5. CI 变绿后由维护者合并。

## 可复用执行顺序（已按 #530 验证）

后续版本按同一顺序执行，不要凭本地打包结果手写候选：

1. 在插件仓库确认 `manifest.json` 版本、`.dbx-store.json` 的 `source`/`permissions` 一致，提交并推送；
2. 创建并推送 `v<version>` tag，在 GitHub 发布 Release；
3. 等 Release 工作流的 5 个平台全部成功，确认 Release 附件含 5 个 `.dbxp` 和 `release-candidates.json`；
4. 在 `t8y2/dbx-store` 的 clone 中运行 `scripts/sync-release-candidate.mjs`，用 Release 的 `release-candidates.json` 生成候选；
5. Fork 后新建分支，只提交 `candidates/<plugin-id>.json`；不要改动 `plugins/`、`catalog/`，不要重复添加已存在的 publisher；
6. 开 PR 后确认改动只有 1 个文件，并用 `gh pr checks` 或 GitHub Actions 页面观察校验；
7. 校验保持红色且唯一错误为 `open candidate(s) awaiting DBX Store signing` 是预期状态，等待维护者评审和签名。

已安装 `gh` 时可直接使用（本机路径 `C:\asdm-tools\_tools\gh\bin\gh.exe`；`GH_TOKEN` 从本机 Git 凭据注入，不要写入仓库）：

```powershell
$cred = ("protocol=https`nhost=github.com`n`n" | git credential fill)
$env:GH_TOKEN = (($cred | Select-String -Pattern '^password=') -replace '^password=','')
$env:PATH = "C:\asdm-tools\_tools\gh\bin;" + $env:PATH
gh release view v0.1.9 --repo SmallRob/dbx-sonar-quality
gh pr view 530 --repo t8y2/dbx-store
gh pr checks 530 --repo t8y2/dbx-store
```

没有 `gh` 时，用 GitHub REST API 或已经验证过的 `C:\asdm-tools\_tmp\pr_sonar.py` 执行同样的 `check` / `create` 动作。

**不要在本插件仓库保留 `store/candidates/<plugin-id>.json`**：本地打包的压缩参数、构建环境与官方 Release CI 不同，字节哈希必然可能不同；本地副本会误导评审。候选只从 Release 生成，`scripts/build-candidates.py` 只负责本地自测和生成 `dist/release-candidates.json`，不写候选文件。

## 1. 前置条件

- 仓库公开可读，评审者能核对「源码 ↔ 二进制」的对应关系；
- `manifest.json` 的 `id`、`version`、`publisher` 与候选元数据完全一致；
- 仓库里有 `LICENSE`（Apache-2.0）、`README.md`、`.dbx-store.json` 和 CI 工作流。

## 2. 发布 Release

```bash
git tag v0.1.9
git push origin main --tags
# 然后在 GitHub 上创建 Release（tag 用 v0.1.9），触发 .github/workflows/plugin-release.yml
```

CI（`t8y2/dbx/.github/workflows/plugin-release-reusable.yml@plugin-cli-v0.1.10`）会：

- 在 `linux-x64` / `linux-arm64` / `windows-x64` / `darwin-x64` / `darwin-arm64` 五个 runner 上各构建一个 `.dbxp`；
- 校验每个包的字节 sha256 与大小、确认包内**没有** `signature.json`（必须是未签名候选）、确认各平台 manifest 身份一致；
- 汇总出 `release-candidates.json` 并把全部产物挂到 Release。

发布后 Release 附件应包含：

```text
io.github.smallrob.sonar-quality-0.1.9-linux-x64.dbxp
io.github.smallrob.sonar-quality-0.1.9-linux-arm64.dbxp
io.github.smallrob.sonar-quality-0.1.9-windows-x64.dbxp
io.github.smallrob.sonar-quality-0.1.9-darwin-x64.dbxp
io.github.smallrob.sonar-quality-0.1.9-darwin-arm64.dbxp
release-candidates.json
```

## 3. 准备候选元数据

### 3.1 publisher 记录（首次提交才需要）

`publishers/smallrob.json` 已存在于 `t8y2/dbx-store`：

```json
{
  "id": "smallrob",
  "name": "SmallRob",
  "status": "unverified"
}
```

`verified` 标记由 DBX 维护者分配，提交时保持 `unverified`。

### 3.2 候选文件

`candidates/io.github.smallrob.sonar-quality.json` 的 `targets` 必须逐个平台写入
Release 的 `sha256` 与 `size`——这两个值**钉死被评审的字节**，签名时任何不匹配都会被拒。

最省事的方式是用 store 仓库里的脚本从 Release 生成：

```bash
git clone https://github.com/t8y2/dbx-store.git
cd dbx-store
node scripts/sync-release-candidate.mjs \
  --repository SmallRob/dbx-sonar-quality \
  --tag v0.1.9 \
  --release-candidates-url https://github.com/SmallRob/dbx-sonar-quality/releases/download/v0.1.9/release-candidates.json \
  --metadata-url https://raw.githubusercontent.com/SmallRob/dbx-sonar-quality/v0.1.9/.dbx-store.json \
  --output candidates/io.github.smallrob.sonar-quality.json
```

脚本会从 `release-candidates.json` 取包内 manifest 生成的 `plugin` 身份，
并把 `.dbx-store.json` 的元数据合并进来。注意脚本以**包内 manifest 的
permissions 为准**，所以 `.dbx-store.json` 里的 `permissions` 必须与
`manifest.json` 一致——CI 里的 `verify.yml` 会检查这一点。

## 4. 提 PR

Fork `t8y2/dbx-store`，新建分支，把候选文件放进对应目录：

```text
candidates/io.github.smallrob.sonar-quality.json
```

`publishers/smallrob.json` 已存在时不要重复添加。**不要**手工编辑
`plugins/*.json` 或 `catalog/index.json`——它们由签名工作流生成。

PR 模板需要如实填写这几项：

| 项 | 本插件的答案 |
|---|---|
| Plugin ID | `io.github.smallrob.sonar-quality` |
| Version | `0.1.9` |
| Publisher ID | `smallrob` |
| 源码仓库与 tag | `https://github.com/SmallRob/dbx-sonar-quality` @ `v0.1.9` |
| 能力与用户流程 | 工作台 + 工具栏/命令面板入口；只读连接 SonarQube PostgreSQL，浏览项目、扫描、问题、对比和全局质量 |
| 权限 | 仅 `host.workbench`（用于 `openWorkbench` / `closeWorkbench`） |
| 数据访问 | 只读 PostgreSQL `issues`、`project_branches`、`file_sources` 等 Sonar 数据；读取用户填写的连接配置；本地快照写入插件数据目录 |
| 网络访问 | 只连接用户配置的 SonarQube PostgreSQL 主机；不调用 Sonar Web API，不发起其他外网请求 |
| 内置 AI / MCP | 未启用 AI 工具或 MCP 工具 |
| 原生 sidecar 行为 | 含 Go 编译的 sidecar 二进制；不监听端口、不起子进程；在插件数据目录写 `config.json`（0600）和本地问题快照 `.json.gz` |
| License | Apache-2.0 |
| 支持地址 | https://github.com/SmallRob/dbx-sonar-quality/issues |

CI 会校验候选文件，此时**保持红色是预期行为**——错误信息是
`open candidate(s) awaiting DBX Store signing`，它会阻止未签名产物被合并。

## 5. 评审者可能会问的

### 「它会不会修改 SonarQube？」

不会。连接串强制 `default_transaction_read_only=on`，所有 SQL 都是固定查询，
没有写 Sonar 的代码路径；插件只读 Sonar 数据库，写操作只发生在 DBX 插件自己的
`config.json` 和问题快照目录。

### 「为什么直连 PostgreSQL，而不是 Sonar Web API？」

Sonar Web API 在社区版和不同版本上差异较大，且需要额外的 token 管理；
直接读 PostgreSQL 能稳定拿到扫描、规则、源码和指标数据。插件不执行用户输入的
任意 SQL，排序字段使用白名单，筛选值全部参数化。

### 「源码和二进制能对上吗？」

能。Go SDK 和依赖 vendor 随仓库固定，构建不依赖外网 Go module proxy；CI 会跑
`go vet`、`go test`、SDK 测试、五平台交叉编译，并对打包出的二进制做一次真实
JSON-RPC 握手。任何人都能复现同样的构建。

## 6. 版本更新

已上架插件再发新版时，只提交 `candidates/<plugin-id>.json`，且只改 `version`
与 `targets`；其余字段留空表示沿用目录里的现值。版本号必须严格递增，
已上架或已撤销的版本不能重提。
