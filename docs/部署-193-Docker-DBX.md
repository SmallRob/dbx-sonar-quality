# 部署到 193 Docker DBX

本文记录 2026-11-13 在 `10.137.154.193` 的 Docker DBX 上安装、签名、配置与真实验收的结果。文中不包含数据库密码、DBX 登录密码或签名私钥。

## 目标环境

| 项 | 值 |
| --- | --- |
| 外部入口 | `http://10.137.154.193:4224` |
| 容器 | `dbx` |
| 镜像 | `dbx:withgit` |
| 端口 | `4224 -> 4224` |
| 数据卷 | 宿主 `/data/docker_data/volumes/dbx_dbx-data/_data` → 容器 `/app/data` |
| 插件目录 | `/app/data/plugins/io.github.smallrob.sonar-quality/{versions,activations}` |
| 插件数据 | `/app/data/plugin-data/io.github.smallrob.sonar-quality` |

## 已安装版本

- 版本：`0.1.2`
- 安装包：`io.github.smallrob.sonar-quality-0.1.2-linux-x64.dbxp`
- SHA-256：`9503b79f4056affd31075d2c1c418960657444191fa174c2fd896bbc724d3b81`
- 大小：`7,484,326` 字节
- 激活记录：`activations/00000000000000000003-12ef53b3-24cc-4122-be85-92aabc60da19.json`
- provenance：`publisher=smallrob`、`signingKeyId=smallrob.internal-2026`、`source=file`

`0.1.0` 在 DBX 上初始化失败，`0.1.1` 修复了空 `capabilities`，但 UI 还依赖 `localStorage`。`0.1.2` 移除了该依赖并补齐暗色主题，是当前应使用的版本；不要回滚到 `0.1.0` 或 `0.1.1`。

## Workbench 运行环境约束

DBX 把插件 UI 放进 `sandbox="allow-scripts"` 的 iframe，没有 `allow-same-origin`，因此：

- 不能访问 `localStorage` / `sessionStorage`，否则脚本在初始化前抛出 `SecurityError`，表现为页面能显示但所有按钮无响应；
- 主题由宿主写入 `document.documentElement.dataset.dbxTheme`（`dark` / `light`），并通过 CSS 变量注入宿主 tokens；
- 0.1.2 在样式前读取 `data-dbx-theme`，为亮色与暗色分别定义语义色，并覆盖表格、表单、弹窗、通知和徽标。

修改 UI 后必须运行：

```powershell
py -3.12 scripts/verify_frontend.py
```

该检查会拒绝 `localStorage` / `sessionStorage` 和缺少暗色令牌的单文件 UI。

## 签名方式

| 项 | 值 |
| --- | --- |
| 算法 | Ed25519 |
| 仓库密钥 | `smallrob.internal-2026` |
| 公钥 | `PcEBBfxlD4/WnWXCU8UIJ868TDS2gWFi/WFw3LksW1A=` |
| 签名对象 | 包内 `checksums.json` 原始字节 |
| 签名文件 | 包根目录 `signature.json` |

私钥 seed 位于不入库的 `.build/repo-signing.env`。服务器信任库保留原有 `leansoftx.release-2026`，新增 `smallrob.internal-2026`：

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
     dist/io.github.smallrob.sonar-quality-0.1.2-linux-x64.dbxp `
     .build/server/io.github.smallrob.sonar-quality-0.1.2-linux-x64.dbxp `
     --target linux-x64
   ```

2. 从 `.build/repo-signing.env` 载入私钥环境变量，执行：

   ```powershell
   py -3.12 scripts/sign-package.py `
     .build/server/io.github.smallrob.sonar-quality-0.1.2-linux-x64.dbxp `
     .build/signed/io.github.smallrob.sonar-quality-0.1.2-linux-x64.dbxp `
     --artifact-metadata .build/signed/io.github.smallrob.sonar-quality-0.1.2-linux-x64.artifact.json `
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

部署窗口曾临时在 `/data/dbx/.docker-compose.nopull.yml` 加入：

```text
DBX_ALLOW_UNSIGNED_PLUGINS=1
DBX_WEB_CAPTCHA=off
```

安装与验收完成后已移除这两项，恢复原始 compose（2050 字节），备份为 `/data/dbx/.docker-compose.nopull.yml.bak-20261113c`，并用原 `DBX_PASSWORD` 重建 `dbx` 容器。

恢复后非敏感环境变量为：

```text
DBX_PORT=4224
DBX_DISABLE_PASSWORD=false
DBX_DATA_DIR=/app/data
DBX_STATIC_DIR=/app/static
DBX_BACKUP_ROOT=/app/backups
DBX_JAVA_BIN=/usr/bin/java
```

签名包在恢复安全设置并重启后仍可正常加载与调用，说明它不是依赖“允许未签名安装”才工作的。

## 真实联调结果

使用真实 SonarQube 9.9.8 数据库（容器 `asdm-sonarqube-db`，Web `asdm-sonarqube`）验证：

| 检查 | 结果 |
| --- | --- |
| `quality/config/get` | 返回配置，`hasPassword=true` |
| `quality/config/test` | `ok=true`、`readOnly=true`、schema 兼容 |
| `quality/projects` | 2 个项目 |
| 评审项目 | `ASDM-Code-Analytics-Review`，5 次扫描，3491 条未解决问题 |
| `quality/scans` | 5 条扫描记录，最新 `AaHIuhZpeh8Lf7H4vhIz` |
| `quality/overview` | 问题 3491；Code Smell 3246、Bug 94、漏洞 4、安全热点 147 |
| `quality/issues` | 总数 3491，分页与排序正常 |
| `quality/issue` | 规则说明、变更记录与 25 行源码上下文正常 |
| `quality/global` | 跨 2 个项目汇总正常 |

`/api/plugins/active` 在重启后可能返回空数组，这是工作台插件的按需启动行为；`/api/plugins/invoke` 会自动拉起 sidecar 并成功执行。

## Workbench UI 验收

`0.1.2` 部署后，在真实 Chrome 中把 UI 放进 `sandbox="allow-scripts"` 的 iframe 运行，分别验证亮色和暗色：

| 检查 | 结果 |
| --- | --- |
| 「连接设置」按钮 | 弹窗正常打开，表单回填主机、端口、数据库、用户 |
| 「测试连接」按钮 | 弹出只读校验成功通知 |
| 暗色主题 | `data-dbx-theme="dark"` 生效，页面、面板、输入框、表格与通知均切换为暗色 |
| 亮色主题 | 保持原有浅色视觉，不出现残影或低对比 |
| 部署 UI 一致性 | 容器内 `ui/index.html` 与本地 SHA-256 一致：`ed58c4623dd6431ff34e14b87264b10c24159eb49adfa28895aa4b7cd645e952` |

## 页面验收

1. 打开 `http://10.137.154.193:4224`，用现有 DBX 登录密码登录。
2. 关闭旧的「SonarQube 质量中心」页签并重新打开，确保加载 `0.1.2`；侧栏版本应显示 `v0.1.2`。
3. 首次打开应显示已配置连接与「只读连接」状态。
4. 点击右上角「连接设置」，确认弹窗能打开；修改任一字段后点「测试连接」，应有结果提示。
5. 在项目下拉切换 `ASDM-Code-Analytics-Review`，查看 5 次扫描、问题分布与问题列表。
6. 点击任一问题，确认中文规则说明和源码上下文可见。

## 故障排查

| 现象 | 原因与处理 |
| --- | --- |
| `invalid type: null, expected a sequence` | 插件初始化返回 `capabilities: null`。`0.1.1` 起改为 `[]`；不要安装 `0.1.0`。 |
| 页面能显示但所有按钮无响应 | UI 在 `sandbox=allow-scripts` iframe 中访问了 `localStorage`，脚本初始化中断。`0.1.2` 已移除；`scripts/verify_frontend.py` 会阻止回归。 |
| `Plugin backend executable does not exist` | 上传了标准 `bin/<target>/` 包。改用 `build-server-package.py` 生成平铺包。 |
| `signature verification failed` / 拒绝安装 | 服务器未信任 `smallrob.internal-2026`，先写公钥；或包被重新打包导致 `checksums.json` 与签名不一致。 |
| 配置保存后仍提示未连接 | 检查 `/app/data/plugin-data/io.github.smallrob.sonar-quality/config.json` 权限与密码；`config/test` 会返回具体缺失项。 |
| 历史扫描问题打不开 | 历史扫描的问题快照尚未留存；后台会补齐有扫描记录的最新快照，旧扫描需等待或手动触发。 |
