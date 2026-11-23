# 部署到自托管 Docker DBX

本文记录在自托管 Docker DBX 上安装、签名、配置与验收的通用流程，并以 `0.1.10` 为例说明。文中不包含数据库密码、DBX 登录密码或签名私钥；真实环境的主机地址、容器名、项目名与宿主路径均以占位符表示。

## 目标环境

| 项 | 值 |
| --- | --- |
| 外部入口 | `http://<DBX_HOST>:<DBX_PORT>` |
| 容器 | `dbx` |
| 镜像 | `dbx:withgit` |
| 端口 | `<DBX_PORT> -> <DBX_PORT>` |
| 数据卷 | 宿主 `<DBX_DATA_VOLUME>` → 容器 `/app/data` |
| 插件目录 | `/app/data/plugins/io.github.smallrob.sonar-quality/{versions,activations}` |
| 插件数据 | `/app/data/plugin-data/io.github.smallrob.sonar-quality` |

## 已安装版本

- 版本：`0.1.10`
- 安装包：`io.github.smallrob.sonar-quality-0.1.10-linux-x64.dbxp`
- SHA-256：`26ec8f1be69a2315a2aae6c3f8902986c2e5b442f1725dedf430e32064c32cbe`
- 大小：`6,902,349` 字节
- 激活记录：`activations/00000000000000000001-6282030b-c8c8-4fdf-9421-43b438f2875c.json`
- provenance：`publisher=smallrob`、`signingKeyId=<SIGNING_KEY_ID>`、`source=file`

`0.1.0` 在 DBX 上初始化失败；`0.1.1` 修复空 `capabilities`；`0.1.2` 移除 `localStorage` 并补齐暗色主题；`0.1.3` 增加规则搜索/分页多选浮窗和多规则查询；`0.1.4` 增加 Markdown 问题明细导出；`0.1.5` 修正毫秒时间戳与实时/快照来源标注；`0.1.6` 修复问题详情浮窗按钮布局，并在打开时锁定列表滚动；`0.1.7` 把目录筛选改为可搜索、分页、跨页多选的浮窗，并让问题查询与 Markdown 导出共用多目录条件；`0.1.8` 把顶部扫描记录与对比栏两个扫描选择改为可搜索、分页、单选的浮窗，并让对比默认选中两次留存快照的扫描、禁用相同或无快照的扫描；`0.1.9` 把长 Sonar 版本号折叠为 build number、保留完整版本搜索，并修复 390px 移动端顶部与筛选区溢出；`0.1.10` 修复 Markdown 导出：宿主 `saveFile` 现在按契约传入 `{fileName, contentType}` 元数据加 UTF-8 base64 正文，不再报 `host.saveFile requires transferred binary data or dataBase64`，保存失败或宿主无该接口时回落到内置 Markdown 预览。`0.1.10` 是当前应使用的版本。不要回滚到旧版本。

## Workbench 运行环境约束

DBX 把插件 UI 放进 `sandbox="allow-scripts"` 的 iframe，没有 `allow-same-origin`、`allow-forms`，因此：

- 不能访问 `localStorage` / `sessionStorage`，否则脚本在初始化前抛出 `SecurityError`，表现为页面能显示但所有按钮无响应；
- 不能依赖原生 `<form>` 提交，浏览器会报 `Blocked form submission ... allow-forms`。“应用筛选”“保存并连接”均已改为显式按钮事件；
- 主题由宿主写入 `document.documentElement.dataset.dbxTheme`（`dark` / `light`），并通过 CSS 变量注入宿主 tokens；
- UI 在样式前读取 `data-dbx-theme`，为亮色与暗色分别定义语义色，并覆盖表格、表单、弹窗、通知、浮窗和徽标。
- 保存文件必须走宿主桥，且注意两个限制：`saveFile({fileName, contentType}, dataBase64)` 的整包请求有约 2 MB 上限（超出报 `Plugin bridge request is too large`），只传元数据会报 `requires transferred binary data or dataBase64`；大文件改走 `fileTransfer.beginSave/write/finish`，`write(handleId, offset, data)` 是位置参数、单块上限 8 MiB。`0.1.10` 优先分块、退到单次、最后回落预览。

修改 UI 后必须运行：

```powershell
py -3.12 scripts/verify_frontend.py
```

该检查会拒绝 `localStorage` / `sessionStorage`、原生表单提交和缺少暗色令牌的单文件 UI。

## 签名方式

| 项 | 值 |
| --- | --- |
| 算法 | Ed25519 |
| 仓库密钥 | `<SIGNING_KEY_ID>` |
| 公钥 | `<SIGNING_PUBLIC_KEY>` |
| 签名对象 | 包内 `checksums.json` 原始字节 |
| 签名文件 | 包根目录 `signature.json` |

私钥 seed 位于不入库的 `.build/repo-signing.env`。服务器信任库保留原有官方密钥，新增本仓库内部公钥：

```text
GET  /api/plugins/trusted-keys
POST /api/plugins/trusted-keys/save
```

该密钥是**自托管内部仓库密钥**，不是 DBX Store 官方签名。正式上架时，五平台市场候选保持未签名，由 Store 在提审通过后使用官方密钥签名。

## 安装包为什么要平铺

DBX Store 五平台候选使用：

```text
bin/linux-x64/dbx-plugin-sonar-quality
```

Docker DBX Web 安装接口在上传自定义包时，会先检查 manifest 的 `entrypoints.backend.executable`：

```text
bin/dbx-plugin-sonar-quality
```

若不存在会报 `Plugin backend executable does not exist`。因此自托管部署使用 `scripts/build-server-package.py` 把 Linux 二进制平铺到 manifest 路径并重算 `checksums.json`，再签名。市场候选不做这一步。

## 部署步骤

1. 生成平铺 Linux 包：

   ```powershell
   py -3.12 scripts/build-server-package.py `
     dist/io.github.smallrob.sonar-quality-0.1.10-linux-x64.dbxp `
     .build/server/io.github.smallrob.sonar-quality-0.1.10-linux-x64.dbxp `
     --target linux-x64
   ```

2. 从 `.build/repo-signing.env` 载入私钥环境变量，执行：

   ```powershell
   py -3.12 scripts/sign-package.py `
     .build/server/io.github.smallrob.sonar-quality-0.1.10-linux-x64.dbxp `
     .build/signed/io.github.smallrob.sonar-quality-0.1.10-linux-x64.dbxp `
     --artifact-metadata .build/signed/io.github.smallrob.sonar-quality-0.1.10-linux-x64.artifact.json `
     --target linux-x64
   ```

3. 登录 `POST /api/auth/login`（JSON `password`），保存 cookie。
4. 若服务器未信任内部公钥，调用 `POST /api/plugins/trusted-keys/save` 写入。
5. `POST /api/plugins/install`，`multipart/form-data` 字段名 `file`。
6. `POST /api/plugins/activate` 激活插件。
7. 预置连接配置到容器：

   ```sh
   docker cp config.json dbx:/app/data/plugin-data/io.github.smallrob.sonar-quality/config.json
   docker exec dbx chmod 600 /app/data/plugin-data/io.github.smallrob.sonar-quality/config.json
   ```

   配置里的 `password` 是 Sonar PG 密码；公共 `config/get` 只返回 `hasPassword`，不回传密码。

## 安全设置恢复

部署窗口可临时在 compose 中加入：

```text
DBX_ALLOW_UNSIGNED_PLUGINS=1
DBX_WEB_CAPTCHA=off
```

安装与验收完成后应移除这两项，恢复原始 compose，并重建 `dbx` 容器。

恢复后非敏感环境变量示例：

```text
DBX_PORT=<DBX_PORT>
DBX_DISABLE_PASSWORD=false
DBX_DATA_DIR=/app/data
DBX_STATIC_DIR=/app/static
DBX_BACKUP_ROOT=/app/backups
DBX_JAVA_BIN=/usr/bin/java
```

签名包在恢复安全设置并重启后仍可正常加载与调用，说明它不是依赖“允许未签名安装”才工作的。

## 真实联调结果

使用真实 SonarQube 9.9.8 数据库（容器 `sonarqube-db`，Web `sonarqube`）验证：

| 检查 | 结果 |
| --- | --- |
| `quality/config/get` | 返回配置，`hasPassword=true` |
| `quality/config/test` | `ok=true`、`readOnly=true`、schema 兼容 |
| `quality/projects` | 2 个项目 |
| 评审项目 | `example-review-project`，5 次扫描，3491 条未解决问题 |
| `quality/scans` | 5 条扫描记录，最新扫描 `hasSnapshot=true`，其余 4 次为 `false` |
| `quality/compare` | 两次未同时留存快照时返回 400「该历史扫描未留存问题快照」；`0.1.10` UI 会在选择阶段禁用这些扫描，不再让用户点到错误 |
| `quality/overview` | 问题 3491；Code Smell 3246、Bug 94、漏洞 4、安全热点 147 |
| `quality/issues` | 总数 3491，分页与排序正常 |
| `quality/export` | 全部严重度 3491 条、2.48 MB、约 0.2 秒；仅严重 1809 条；按严重度分组与明细字段正确 |
| `quality/issue` | 规则说明、变更记录与 25 行源码上下文正常 |
| `quality/global` | 跨 2 个项目汇总正常 |

`/api/plugins/active` 在重启后可能返回空数组，这是工作台插件的按需启动行为；`/api/plugins/invoke` 会自动拉起 sidecar 并成功执行。

## Workbench UI 验收

`0.1.10` 部署后，在真实 Chrome 中把 UI 放进 `sandbox="allow-scripts"` 的 iframe 运行，分别验证亮色和暗色：

| 检查 | 结果 |
| --- | --- |
| 「连接设置」按钮 | 弹窗正常打开，表单回填主机、端口、数据库、用户 |
| 「测试连接」按钮 | 弹出只读校验成功通知 |
| 规则浮窗 | 135 条规则、7 页、每页 20 行；搜索 `String` 命中 5 条；跨页选中 3 条后按钮显示「已选 3 条规则」 |
| 规则键 | 每条规则下方显示 `java:S1192`、`go:S1192` 等规则键，可区分同名规则 |
| 多规则查询 | 「应用筛选」后 `window.__lastQuery.ruleUuids` 含 3 个 UUID；接口查询 `java:S1192` 854 条、`go:S1192` 128 条，合并为 982 条 |
| 目录浮窗 | 真实 192 个目录、10 页、每页 20 行；浮窗宽 600、高 480，超出屏幕时自动上翻 |
| 目录搜索 | 输入示例路径命中多个目录，路径按两行展示并保留完整内容 |
| 目录跨页多选 | 第 1 页选中 1 个、翻到第 2 页再选 1 个，按钮显示「已选 2 个目录」，`window.__lastQuery.directories` 含 2 条完整路径 |
| 目录清空与关闭 | 「全部目录（清除目录筛选）」「清空已选」可清空；「应用筛选」、外部点击与 `Escape` 均可关闭浮窗 |
| 顶部扫描浮窗 | 真实项目 5 次扫描 + 「最新扫描」共 6 行、1/1 页、宽 470px；每行完整时间为主行，版本与 revision 为次行；搜索版本号命中 1 项；单选某扫描后按钮显示对应时间，查询切换为该扫描 |
| 对比默认 | 真实项目仅最新扫描留存快照：默认基准留空、对比为最新，按钮禁用并提示「仅 1 次扫描留存问题快照；至少需要两次才能比较」；其余 4 行禁用并标注「未留存问题快照」 |
| 对比浮窗 | 基准、对比两个控件均为可搜索、20 条/页单选浮窗；不含「最新扫描」伪选项；相同扫描时按钮禁用并显示红色「不能相同」 |
| 对比结果 | 5 条 mock 均有快照时，默认第二新 → 最新；结果为新增、持续、不再出现、净变化 4 张卡，明细标题带具体扫描时间，`__lastCompare` 参数正确 |
| 扫描分页 | 30 条 mock 下第 1/2 页显示 20 行，第 2/2 页显示 11 行；搜索命中后自动回到 1/1 页并单选成功 |
| 移动端 | 390px 视口下顶部两列选择器不溢出（控件 178px、浮窗 366px），页面 `scrollWidth=390` |
| 导出弹窗 | 默认继承当前严重度筛选；选 `CRITICAL` + `MAJOR` 后请求 `severities` 与 `ruleUuids` 正确，`limit=5000`；文件名含严重度与时间 |
| 导出保存 | `0.1.10` 在真实 DBX 中：全严重度 3,491 条 → `...-blocker-critical-major-minor-info-20261123-1447.md`（2,481,225 字节，394 ms，分块落盘）；仅「阻断」→ `...-blocker-20261123-1447.md`（19 条、15,321 字节），文件头与分组明细完整；宿主无保存接口或写盘失败时打开 Markdown 预览，可复制或浏览器下载 |
| 导出样例 | `docs/samples/sonarqube-issues-sample.md`：脱敏示例，展示导出文件头、严重度汇总与逐条明细字段 |
| 详情浮窗按钮 | 关闭按钮保持 `40×40`，不再随标题栏拉伸；标题、徽标与关闭按钮顶端对齐 |
| 详情浮窗滚动 | 浮窗打开后 `html.drawer-open` 锁定页面滚动；抽屉高度等于视口，只有 `.drawer-body` 上下滚动，详情内容超长仍可到达底部 |
| 关闭恢复滚动 | 关闭按钮、遮罩或 `Escape` 关闭后移除滚动锁，列表恢复滚动 |
| 暗色主题 | `data-dbx-theme="dark"` 生效，浮窗背景 `rgb(23, 30, 38)`，页面、面板、输入框、表格与通知均切换为暗色 |
| 亮色主题 | 保持原有浅色视觉，浮窗背景 `rgb(255, 255, 255)`，不出现残影或低对比 |
| 无表单提交 | 控制台不再出现 `Blocked form submission`；「应用筛选」「保存并连接」均为按钮事件 |
| 部署 UI 一致性 | 容器内 `ui/index.html` 与本地 SHA-256 一致：`7dc4a1d5eaf0788069fcd049f7cc95c115112677adde75675a31aa4d476681f5` |

## 页面验收

1. 打开 `http://<DBX_HOST>:<DBX_PORT>`，用现有 DBX 登录密码登录。
2. 关闭旧的「SonarQube 质量中心」页签并重新打开，确保加载 `0.1.10`；侧栏版本应显示 `v0.1.10`。
3. 首次打开应显示已配置连接与「只读连接」状态。
4. 点击右上角「连接设置」，确认弹窗能打开；修改任一字段后点「测试连接」，应有结果提示。
5. 在项目下拉切换目标项目，查看扫描次数、问题分布与问题列表。
6. 点击顶部「扫描记录」，确认浮窗可搜索版本号、翻页为 20 条/页；点选某次扫描后按钮文本切换，概览展示该次历史度量，问题清单明确提示「历史问题快照不可用」，不会用当前问题冒充历史结果。
7. 进入「扫描记录」：确认真实环境默认对比按钮禁用并提示「仅 1 次扫描留存问题快照」，历史扫描行显示「未留存问题快照」；等后续新扫描产生两次快照后，默认应为第二新 → 最新，比较结果为 4 张卡并列出双向明细。
8. 点击「规则」，搜索并跨页勾选多条规则，点「应用筛选」，确认列表总数与规则计数之和一致。
9. 点击「目录」，搜索路径、翻页并跨页勾选多个目录，点「应用筛选」，确认列表只显示所选目录及其子目录；再点「导出 Markdown」，确认导出范围同样受目录多选限制。
10. 点击「导出 Markdown」，选择一个或多个严重度，确认宿主弹出保存对话框并生成 `.md`；打开文件检查严重度汇总、分组和逐条明细。
11. 点击任一问题，确认中文规则说明和源码上下文可见；浮窗打开后页面列表不再滚动，滚轮只滚动详情内容直到底部，关闭按钮为方形且关闭后列表恢复滚动。

## 故障排查

| 现象 | 原因与处理 |
| --- | --- |
| `invalid type: null, expected a sequence` | 插件初始化返回 `capabilities: null`。`0.1.1` 起改为 `[]`；不要安装 `0.1.0`。 |
| 页面能显示但所有按钮无响应 | UI 在 `sandbox=allow-scripts` iframe 中访问了 `localStorage`，脚本初始化中断。`0.1.2` 已移除；`scripts/verify_frontend.py` 会阻止回归。 |
| 点击「应用筛选」或「保存并连接」无反应 | 原生 `<form>` 提交被 `sandbox` 拦截（无 `allow-forms`）。`0.1.3` 已改为按钮事件；检查是否又引入了 `type="submit"` / `requestSubmit()`。 |
| `Plugin backend executable does not exist` | 上传了标准 `bin/<target>/` 包。改用 `build-server-package.py` 生成平铺包。 |
| `signature verification failed` / 拒绝安装 | 服务器未信任本仓库公钥，先写公钥；或包被重新打包导致 `checksums.json` 与签名不一致。 |
| 配置保存后仍提示未连接 | 检查 `/app/data/plugin-data/io.github.smallrob.sonar-quality/config.json` 权限与密码；`config/test` 会返回具体缺失项。 |
| 导出按钮不可用 | 当前筛选结果为空，或问题接口失败；先确认列表已加载出问题。 |
| 导出的时间显示异常 | 历史 `0.1.4` 把毫秒时间戳按秒解析；`0.1.5` 已兼容秒/毫秒，重新导出即可。 |
| `host.saveFile requires transferred binary data or dataBase64` | UI 只传了 `options`、没传正文。宿主契约是 `saveFile({fileName, contentType}, dataBase64)` 或 `saveFile({fileName, contentType, dataBase64})`；`0.1.10` 已按此实现，`suggestedName`/`content` 是错误键名。 |
| 导出无文件/无响应 | 宿主未提供 `window.dbxPlugin.saveFile` 时会显示预览；可在预览中复制，或尝试浏览器下载。 |
| 对比按钮不可用 | 基准或对比扫描未通过 `hasSnapshot=true` 校验：`quality/scans` 中未留存问题快照的扫描会被禁用；需要至少两次留存快照的扫描才能比较。插件会自动留存每个项目的最新扫描，后续新扫描可逐步形成可对比窗口。 |
| 历史扫描问题打不开 | 历史扫描的问题快照尚未留存；概览仍展示历史度量，问题清单会提示「历史问题快照不可用」；后台每 60 秒为有扫描记录的项目补齐最新快照，旧扫描需等待或手动触发。 |
