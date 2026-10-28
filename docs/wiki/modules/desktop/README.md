# 桌面端与开发环境

## 文档信息

| 字段 | 内容 |
|---|---|
| 适用范围 | Electron 主进程、preload、React 渲染层启动与 IPC；Go sidecar 生命周期；Dev/正式身份、数据目录与端口隔离；Vite 代理；页面路由；删除影响弹窗与 Claude Code 客户端配置；构建、发布 CI、应用更新、测试与运维边界 |
| 事实状态 | 已核验 |
| 核验基线 | `b44d75a788f8aa5ef0ad024273d4bf5dd8c95c7e`（dev，2026-10-28） |
| 核验时间 | 2026-10-28 |

## 用途与边界

Portunus 桌面端是本地网关的交付外壳：Electron 主进程负责应用身份、窗口与托盘、IPC、Go 后端守护（sidecar）和更新；React 渲染层提供管理界面；真正的模型聚合、路由和协议转换仍在 Go 后端。桌面壳不实现这些业务能力。

本模块负责：

- 未打包、打包 Dev、正式安装包三种运行形态的身份、数据路径和端口隔离。
- 主进程拉起、健康检查、重启和停止自己创建的 Go 后端；不接管其他进程。
- 渲染层与主进程之间的固定 IPC 白名单，包括更新、监听范围、客户端配置和外部链接。
- Vite 开发代理、打包后 `file://` 页面加载、六个管理页面的路由与懒加载。
- 渠道/模型删除前的影响预览，以及仅桌面端可用的 Claude Code 配置读写。
- 前端与 Go 后端的构建脚本、CI/Release 工作流、正式版更新链路和桌面启动检查。

不负责或不在此展开：

- 网关路由、协议转换、余额查询、日志归并等后端能力，分别由网关、资源、配额和观测模块说明。
- 管理 API 的完整访问控制实现；桌面侧只说明它依赖真实回环连接，不能通过公开反向代理暴露。
- 实际发布状态、更新源可用性或线上 CI 结果。本页依据仓库源码与工作流定义，不证明 GitHub Actions 已在线运行、草稿 Release 已发布或某个安装包已交付。

关键边界：

- 管理面 `/api` 仅供本机使用；局域网对外的是 `/v1` 服务面，仍需 API Key。
- Dev 不复制、不迁移、不读取正式数据库、上游密钥或设置。唯一共享的外部用户状态是用户主动编辑的 `~/.claude/settings.json`；构建脚本和更新验收脚本另用本机更新签名私钥，Dev 应用运行时不会读取它。
- 不能按进程名批量结束应用。代码只停止自己 spawn 的子进程，日常停止应走托盘退出、`Cmd+Q` 或应用菜单。
- `go run .` 是命令行后端模式，默认端口 3060、使用仓库 `data/`，不属于桌面 Dev；Vite 代理固定指向 Dev 的 13061。

## 主要流程

### 运行身份、端口与数据目录

`web/src/main/runtime-profile.js` 的 `profileFor` 决定身份：未打包一律为 `development`；打包后只有 `package.json` 带 `portunusProfile: "development"` 才是 Dev，否则为 `production`。Dev 包由 `web/electron-builder.dev.cjs` 通过 `extraMetadata` 写入该标记。`initializeProfile` 在单实例锁之前执行，Dev 会设置应用名、`userData`、`sessionData` 和日志目录；正式版不覆盖 Electron 默认目录。

`preloadProfile` 从窗口启动参数 `--portunus-profile=` 读取身份，缺少参数时默认 Dev，避免 preload 回退到正式端口。`main.js` 创建窗口时把 `--portunus-profile=${runtime.profile}` 传给 preload。

| 项目 | 正式安装版 | 开发调试版 |
|---|---|---|
| 名称 | Portunus | Portunus Dev（macOS Dock 显示 DEV 徽标） |
| Bundle ID / appId | `com.portunus.app` | `com.portunus.app.dev` |
| 后端端口 | 13060 | 13061 |
| `userData` | Electron 默认；开发说明记录 macOS 当前为 `~/Library/Application Support/@portunus/web` | `app.getPath('appData')/Portunus Dev`；macOS 即 `~/Library/Application Support/Portunus Dev` |
| 会话 / 日志 | 沿用默认路径 | `Portunus Dev/session/`、`Portunus Dev/logs/` |
| 后端工作目录 | 不额外覆盖 | `Portunus Dev/backend/`，不读仓库配置 |
| 数据库 | `userData/data/portunus.db` 等 | 独立 `Portunus Dev/data/`；首次启动新库、新令牌 |
| 更新 | 正式更新服务 | 禁用检查、下载和安装 |
| 构建产物 | `web/dist-electron/` | `web/dist-dev/` |

`web/src/main/server-address.js` 是端口与连接地址的单一来源：`DEV_PORT=13061`、`PROD_PORT=13060`；内部 URL 恒为 `http://127.0.0.1:<port>`，监听 host 另由 `networkMode` 决定。`local` 监听 `127.0.0.1`，`lan` 监听 `0.0.0.0`。因此“监听地址”和“界面连接地址”是两件事，LAN 模式也不会让界面改用 `0.0.0.0` 访问。

`web/src/main/app-config.js` 在 `userData/config.json` 保存 `networkMode`，默认 `local`；读取失败回退默认值，写入使用临时文件加 `rename` 原子替换。主进程只注册 `config:get-network-mode`；设置范围走 `config:set-network-mode`，由 `main.js` 写盘后调用 `backend.restart()`，只有当前主进程创建的后端会被重启。

单实例锁以当前 `userData` 为界。Dev 热启动和打包 Dev 共用同一身份，二者不能同时运行；第二次启动会退出并聚焦已有窗口。正式版与 Dev 使用不同身份和端口，可以同时运行。

### 启动、窗口、托盘与关窗差异

主流程位于 `web/src/main/main.js`：

1. 加载运行身份；开发态设置应用图标；忽略 electron-updater 的非致命 `HttpError`。
2. 获取单实例锁；失败直接退出。
3. 在 Dev 下创建 `userData/backend/`，组装 sidecar 参数；`binPath` 打包后取 `resources/bin/portunus[.exe]`，未打包取 `web/bin/{mac|win|linux}/portunus[.exe]`。
4. `app.whenReady()` 后创建托盘（Linux 除外）、创建窗口并 `backend.start()`。
5. 窗口等待 `backend.ready` 后再加载页面。Dev 后端失败时显示内联错误页，不加载会访问端口的业务页面；正式版保留既有降级行为。
6. 打包版在 `loadFile` 成功后检查更新；Dev 加载 Vite 页面并打开 Detached DevTools。

窗口使用 `contextIsolation: true`、`nodeIntegration: false`、`sandbox: false`。关闭 sandbox 是为了让 preload 能 `require('fs')/require('path')`，这是当前 preload 桥工作的前提。`did-finish-load` 后会执行一次 `typeof window.api !== "undefined"` 探测，只用于诊断。

平台关窗和退出行为由代码显式区分：

| 平台 | 点击窗口关闭 | `window-all-closed` | 真正退出 | 后端 |
|---|---|---|---|---|
| macOS | 关闭窗口，应用留在 Dock | 不退出 | `Cmd+Q`、托盘“退出”、Dock 右键退出 | 退出前 `before-quit` 拦截一次，`backend.stopAndWait()` 完成后再次 `app.quit()`；关窗期间后端继续服务 |
| Windows | 拦截 `close` 并隐藏到托盘，除非 `isQuitting` 已置位 | 不退出，托盘仍可恢复窗口 | 托盘“退出”或 `app.quit()` | 退出时 `backend.stop()`；点 X 不停止网关 |
| Linux | 关闭窗口 | 无托盘兜底，`backend.stop()` 后 `app.quit()` | 关窗即退出 | 关窗即停止后端 |

macOS 的 `activate` 会在无窗口时重新创建窗口；`second-instance` 和托盘“打开”共用 `showMainWindow`，最小化先还原，已销毁则重建。托盘只在 macOS 和 Windows 创建；Linux 不创建托盘，保持关窗退出。

不要使用 `killall`、`pkill Portunus` 或按进程名清理。后端是网关，终端里的客户端可能仍在调用；需要停止时使用应用内退出。开发构建、预览和启动检查都只处理自己启动的 Dev 实例，不关闭正式版。

### sidecar 生命周期与注入环境

`web/src/main/sidecar.js` 是与 Electron 无关的状态机，`main.js` 注入二进制、数据目录、端口和 host 回调。虽然模块注释描述了 `binPath=null` 时只轮询外部后端的模式，但桌面 `main.js` 在 Dev 也传入 `web/bin/...` 二进制，因此桌面 Dev 会自行拉起后端，而不是依赖 `go run .`。

状态为 `stopped`、`starting`、`ready`、`failed`。`start()` 的步骤：

1. 已有 `starting/ready` 时直接返回；递增 generation，停止旧子进程。
2. 可选等待 `PORTUNUS_SIDECAR_DELAY_MS`（仅冷启动，重启不生效）。
3. 检查二进制存在；用 `net` 尝试绑定目标端口，端口已被占用时直接失败，不接管已有服务。
4. 生成随机 UUID 作为 `PORTUNUS_INSTANCE_ID`，注入环境变量并 spawn。
5. 轮询 `GET http://127.0.0.1:<port>/api/ping`，要求 HTTP 200 且响应头 `X-Portunus-Instance` 等于本次 UUID；默认 60 次、每 500ms，约 30 秒超时。
6. 每次轮询前后都核对 generation 和子进程仍存活；子进程退出或报错会把当前代标记为 `failed`。
7. 成功后进入 `ready`；`ready` 恒 settle，不 reject，调用方以 `state` 和 `error` 判断结果。

`restart()` 先停止当前子进程，再等待端口释放（默认 30 次、每 100ms），然后重新 spawn 和健康检查；切换监听范围走这条路径。`stop()` 只 kill 当前 `child` 并递增 generation；旧进程延迟退出不会清空新一轮状态。`stopAndWait()` 用于 macOS 退出/更新替换前等待后端退出：子进程 6 秒未退则 `SIGKILL`，最多等 8 秒，避免新版启动时端口仍被占用。

spawn 时注入的关键变量：

| 变量 | 来源 / 用途 |
|---|---|
| `PORTUNUS_INSTANCE_ID` | sidecar 每次启动生成的 UUID；`/api/ping` 用它校验“确实是本次实例” |
| `PORTUNUS_DATABASE_TYPE` | 仅 Dev 注入 `sqlite`；正式版不注入，使用后端自身默认配置 |
| `PORTUNUS_SERVER_HOST` | `hostFor(networkMode)`：`lan → 0.0.0.0`，`local → 127.0.0.1`；每次 spawn 现算 |
| `PORTUNUS_SERVER_PORT` | Dev 13061 / 正式 13060 |
| `PORTUNUS_DATABASE_PATH` | `<userData>/data/portunus.db` |
| `PORTUNUS_DATABASE_LOG_PATH` | `<userData>/data/portunus-log.db` |
| `PORTUNUS_SIDECAR_DELAY_MS` | `main.js` 读取并转换为 `delayMs`，供启动竞态检查放大窗口 |
| `PORTUNUS_CLIENT_HOME` | `client-config.js` 的 `homeDir` 覆盖，验收时可指向临时目录 |

后端 stdout/stderr 由主进程转发到 Electron 控制台，前缀为 `[backend]`；侧车退出日志为 `[sidecar]`。Dev 数据目录与正式隔离，但两者都使用各自的 `userData`，不通过复制正式数据库实现。

### preload 桥与 IPC

`web/src/main/preload.js` 通过 `contextBridge` 暴露 `window.api`，不暴露通用 `invoke`：

- 应用信息：`appName`、`isDevelopment`、`updatesEnabled`、`getAppVersion()`、`platform`。
- 更新：`checkForUpdate`、`downloadUpdate`、`installUpdate`、`getUpdateState`、`confirmRendererReady`、`onUpdateEvent(channel, callback)`。事件 channel 只接受 `update-channels.js` 中的白名单，返回取消订阅函数。
- 网络监听范围：`getNetworkMode`、`setNetworkMode(mode)`。
- 服务地址：`getServerUrl()` 返回 preload 计算出的回环 URL，渲染层不能改。
- 客户端配置：`getPaths`、`readClaude`、`readBackupClaude`、`saveClaude(text)`、`rollbackClaude`。只暴露这五个固定通道，渲染层不能传路径。
- 外部链接：`openExternal(url)`；主进程只接受 `http://` 或 `https://`。

主进程 IPC 注册位置：

- `updater.register(ipcMain)`：`update:get-state`、`update:check`、`update:download`、`update:install`。
- `app-config.register(ipcMain)`：`config:get-network-mode`；set 因需要重启后端而留在 `main.js`。
- `client-config.register(ipcMain)`：五个 `client-config:*` 通道，统一返回 `{ ok, data }` 或 `{ ok: false, error }`，避免主进程异常直接冒泡。
- `main.js`：`app:version`、`app:platform`、`open-external`、`config:set-network-mode`，以及 macOS 更新健康检查 `update:renderer-ready`。

`update:renderer-ready` 只在 macOS 处理：要求窗口未销毁、后端已 `ready`、事件来自主 frame、frame URL（去掉 hash）等于打包后的 `index.html` 文件 URL，再交给 `mac-update-health.confirmRendererReady`。它用于向更新事务标记“新版已成功拉起”，不是普通页面初始化。

### 渲染层路由、Vite 代理与请求路径

`web/src/renderer/index.html` 只有一个 `#root` 和 `main.jsx` 模块入口。`main.jsx` 在 React 渲染前调用 `preloadInitialPage(window.location.hash)`，先加载当前 hash 对应的页面 chunk，避免冷启动先显示 Suspense 再二次提交；未知 hash 在这里只会回退预加载 `channels`。

`App.jsx` 使用 `HashRouter`：

- `/` 重定向到 `/channels`。
- 六个页面：`channels`、`models`、`groups`、`logs`、`clients`、`settings`。
- 页面通过 `pageResources.js` 懒加载；`warmPages` 在空闲时预加载其余页面，导航悬停/聚焦时也会 `preparePage`。
- 路由表没有通配符兜底；未知 hash 的最终渲染不由初始预加载决定，见“待确认项”。

`api/request.js` 以页面协议区分后端地址：

- 开发态（`http:`，由 Vite 提供页面）：`baseURL = '/api'`，由 Vite 代理到 `127.0.0.1:13061`。
- 打包态（`file:`）：从 preload 的 `getServerUrl()` 取地址，`baseURL = '<serverUrl>/api'`。
- 统一加 `X-Portunus-Local: 1`，超时 30 秒；GET 响应进入内存缓存，除预览/测试类接口外的写操作成功后失效缓存；错误默认 toast 并 reject。
- 只对“请求未到达后端”的 `ERR_NETWORK` / `Network Error` 做静默重试，固定 500ms 间隔，最多 5 次失败判定（最多重试 4 次）；超时、取消和已拿到 HTTP 响应的错误不重试。这用于兜底 sidecar 启动和切换监听范围时的短暂空窗。

`web/vite.config.mjs`：

- `root: 'src/renderer'`，构建输出 `../../dist/renderer`，`@` 指向 renderer 根目录。
- `base` 默认 `/`，打包必须用 `VITE_BASE=./` 覆盖，否则 `file://` 下绝对资源路径会 404。
- 开发服务器绑定 `127.0.0.1:5173`，`strictPort: true`。
- `/api` 代理到 `serverAddress.addressOf({ profile: 'development' }).url`；代理前有 `bypass`，路径以 `.js` 或 `.mjs` 结尾的源码模块请求交回 Vite，避免 `/api/*.js` 被误转发。
- `/v1` 也代理到 Dev 后端，供本机客户端调试。
- `dev-management-access.mjs` 的 `localManagementProxyGuard` 在 Vite 开发服务器上拦截 `/api`：只允许真实回环对端（`::1`、`127.*`、IPv4-mapped 回环），其他来源直接 403。它不替代后端自己的管理访问检查。

开发模式下 `main.js` 加载 `http://localhost:5173`；`pnpm dev` 先用 `wait-on tcp:127.0.0.1:5173` 等 Vite 就绪，再以 `NODE_ENV=development electron .` 启动主进程。只有“未打包且 `NODE_ENV=development`”才使用 Vite；直接 `electron .` 且未设该变量时，会走打包后的 `dist/renderer/index.html`。

### 删除影响弹窗

渠道和模型删除前会先请求只读影响预览：

- 渠道：`GET /api/channels/:id/deletion-impact`，返回受影响分组、渠道模型数 `model_count`、是否有余额来源 `has_quota_source`。
- 模型：`GET /api/models/:id/deletion-impact`，返回受影响分组、移除成员、手动选定项清除标记和是否变空。
- 分组删除目前是普通确认弹窗，不调用影响预览；确认后删除分组，上游渠道和模型保留。

`web/src/renderer/utils/deletionImpact.js` 的 `createDeletionImpactController` 负责状态机：

- `open(target)` 先置 `loading`，用 `AbortController` 加载；切换目标或关闭弹窗时递增 generation 并中止旧请求，迟到响应直接丢弃。
- 响应经 `normalizeDeletionImpact` 校验；分组字段缺失、类型不对，或渠道缺少模型数/余额来源时，按“影响不完整”报错，绝不把缺失当成 0 或“无引用”。
- 只有 `status === 'ready'` 且已有影响数据时才允许确认；`error` 状态下确认按钮禁用，界面提供“重试预览”。
- `confirm()` 使用预览时的 target 执行删除，防止弹窗打开期间换对象后删错；删除中禁止重复提交；删除成功才触发 `onDeleted` 和父页面刷新。
- `buildDeletionImpactView` 只展示后端给出的内容：受影响分组数、每组移除成员数、手动选定项清除、分组将变空；渠道额外展示模型数和余额来源提示。文案明确“分组名称和历史调用记录保留，手动选定项清除后不会自动补位”。

`DeletionImpactBody.jsx` 统一渲染加载、错误和列表状态；错误时显示“无法确认删除影响，已阻止删除”和重试入口。后端删除与级联清理逻辑归资源模块，本页只描述前端影响预览和确认边界。

### Claude Code 客户端配置

`web/src/main/client-config.js` 只支持 Claude Code，目标固定为 `<homeDir>/.claude/settings.json`，备份为同目录 `settings.json.portunus.bak`。`homeDir` 默认 `os.homedir()`，可用 `PORTUNUS_CLIENT_HOME` 覆盖；渲染层不能传入路径。

读取与保存规则：

- 文件不存在时返回默认模板 `{ "env": {} }`，`exists: false`，不创建文件。
- 读取上限 512 KiB；超过时提示在外部编辑器处理。
- 保存前校验：必须是字符串、不超过上限、合法 JSON、顶层必须是对象（不能是数组或 `null`）。
- 首次写入前备份：只有原文件存在且 `.bak` 不存在时复制，`.bak` 始终保留“Portunus 首次改动前”的版本，后续保存不再覆盖。
- 写入使用同目录临时文件、`fsync`、`chmod`、`rename` 原子替换，保留原文件权限；失败删除临时文件，不留下半写配置。
- 如果 `settings.json` 是软链，先 `realpath` 取真实目标，避免 `rename` 把软链替换成普通文件。
- 回滚从 `.portunus.bak` 读取并重新校验，再原子写回当前真实目标；没有备份时明确报错。
- IPC 统一包装为 `{ ok, data | error }`；JSON 解析错误只回传原因，不把可能含令牌的原文带回渲染层。

`ClaudeConfigPanel.jsx` 只在桌面端显示（没有 `window.api.clientConfig` 时提示浏览器预览不可用）。打开面板时才读取当前文件；页面持有原始文本，表单修改会写回文本，源码编辑器是唯一真相。未保存改动通过 `navGuard` 拦截顶部切页和树内切换。界面提供连接字段、六个模型槽位、保存、重新读取和从备份回滚；Dev 会显示“开发版会读写真实用户目录”的提示。

关键边界：Dev 与正式版共享当前用户真实的 `~/.claude/settings.json`，这不属于应用数据隔离。开发验收应设置 `PORTUNUS_CLIENT_HOME=/tmp/...` 指向临时目录，避免改动真实客户端配置；不要靠复制正式配置来“测试”。

### 构建、预览与命令

所有命令在 `web/` 下执行，实际脚本定义在 `web/package.json`。

| 命令 | 实际行为 | 产物 / 用途 |
|---|---|---|
| `pnpm dev` | `build:go`（当前平台，debug）→ `concurrently` 启动 Vite 与 `wait-on tcp:127.0.0.1:5173` → `cross-env NODE_ENV=development electron .` | 热开发；主进程拉起自己的 Dev 后端 13061 |
| `pnpm dev:web` | `vite --open` | 只启动 Vite（5173）；需要已有 Dev 后端或另行启动后端 |
| `pnpm dev:main` | `cross-env NODE_ENV=development electron .` | 只启动 Electron 主进程；需要已有 Vite，后端由主进程自行拉起 |
| `pnpm build:go` | `node scripts/build-go.mjs`，按当前 OS/arch，`CGO_ENABLED=0` | `web/bin/{mac|win|linux}/portunus[.exe]`；macOS 另出 `portunus-updater`；保留调试信息 |
| `pnpm build:go:release` | 同上，加 `--release`（`-trimpath -ldflags="-s -w"`） | 正式后端，移除路径、符号与 DWARF |
| `pnpm build:renderer` | `VITE_BASE=./ vite build` | `web/dist/renderer/`，供 Electron `file://` 加载 |
| `pnpm build:dev` | `node scripts/build-dev.mjs`：`build:go` → `build:renderer` → `electron-builder --dir --config electron-builder.dev.cjs -c.electronDist=node_modules/electron/dist` | `web/dist-dev/` 的 Portunus Dev 目录包，不生成安装器、不安装到 `/Applications` |
| `pnpm preview:dev` | `node scripts/preview-dev.mjs`：先确认 13061 空闲，再启动已有 `dist-dev` 产物 | 仅预览；要求先执行 `build:dev`；不会关闭占用端口的已有 Dev |
| `pnpm build` | `build:go:release` → `VITE_BASE=./ vite build` → `electron-builder` | 当前平台正式包，输出 `dist-electron/` |
| `pnpm build:mac` | `build:go:release` → `build:renderer` → `electron-builder --mac dmg zip --arm64` | macOS arm64 的 DMG/ZIP |
| `pnpm build:win` | `build-go.mjs windows amd64 --release` → `build:renderer` → `electron-builder --win nsis zip --x64` | Windows x64 的 NSIS/ZIP |
| `pnpm start` | `electron .` | 直接用本机 Electron 启动；不是热开发入口，未打包且未设 `NODE_ENV=development` 时加载已有 `dist/renderer` |
| `pnpm test` | `node --test "src/**/*.test.js"` | 主进程与渲染层纯函数离线单测，不启动 Electron |
| `pnpm run typecheck` | `tsc --noEmit` | TypeScript/TSX 与 JS 模块互操作检查 |
| `pnpm test:e2e` | `node scripts/startup-race-check.mjs` | 仅针对已构建的 Dev 目录包与 13061 的启动检查 |

构建细节：

- `build-go.mjs` 参数优先于当前平台：`node scripts/build-go.mjs darwin arm64 --release` 可交叉编译；输出目录键与 electron-builder 的 `${os}` 宏对齐（`darwin → mac`、`windows → win`、`linux → linux`）。
- `electron-builder.dev.cjs` 只用于开发预览：继承 `package.json` 的 `build`，覆盖 `appId`、`productName`、`output: dist-dev`、`compression: store`、`publish: null` 和 `extraMetadata.portunusProfile=development`；mac 用 `identity: null` 且只出 `dir`，Linux 可执行名为 `portunus-dev`，Windows 也只出 `dir`。`build-dev.mjs` 同时设置 `CSC_IDENTITY_AUTO_DISCOVERY=false` 并指定 `-c.electronDist=node_modules/electron/dist`，不寻找 Apple 签名身份。正式发布继续用 `package.json` 的 `build` 配置。
- 正式构建的 `extraResources` 把 `web/bin/${os}` 复制到应用的 `resources/bin`；主进程打包态从这里取后端二进制。
- 更新签名脚本读取 `PORTUNUS_UPDATE_SIGNING_KEY`（私钥 PEM 文本）或 `PORTUNUS_UPDATE_SIGNING_KEY_FILE`（文件路径）；两者都没有时，`after-artifacts.cjs` 在本机回退到 `~/.config/portunus/update-signing-key.pem`，CI 中缺少 Secret 会直接失败。Release CI 另设 `CSC_IDENTITY_AUTO_DISCOVERY=false`。
- Release CI 明确用 `VITE_BASE=./` 的 env 块，而不是 `VAR=x` 命令前缀，因为 Windows runner 默认 PowerShell 不支持该写法。`build:renderer` 脚本自身仍是 `VITE_BASE=./ vite build`；`build`、`build:mac`、`build:win`、`build:dev` 都会经过这一脚本或同样的前缀，在原生 Windows 上的效果未在本轮执行验证。
- `preview:dev` 的目标路径：macOS `dist-dev/mac-arm64/Portunus Dev.app`（Intel 为 `mac/`），Windows `dist-dev/win-unpacked/Portunus Dev.exe`，Linux `dist-dev/linux-unpacked/portunus-dev`。macOS 使用 `open -n` 启动，不覆盖 `/Applications/Portunus.app`。

### 应用更新

Dev 与正式版走完全不同的更新路径：

- `web/src/main/updater.js` 在 Dev 直接导出 `disabled-updater.js`，不加载 `electron-updater`，避免接触正式更新缓存或安装器。`disabled-updater` 的 `getState` 返回中性状态；检查、下载、安装一律抛错“Portunus Dev 禁用正式更新，请重新构建开发预览”。设置页显示“开发环境 · 13061 · 独立数据 · 禁用正式更新”。
- 正式版加载 `electron-updater`；macOS 使用 `mac-update-installer.js` 的 `PortunusMacUpdater`，Windows/Linux 使用默认 `autoUpdater`。
- `update-service.js` 关闭自动下载（`autoDownload=false`），由服务在检查到新版本后启动下载；事件通过 `update:*` channel 推给渲染层，状态带 `revision`，渲染层 `updateStore` 以 revision 防止旧 IPC 回复覆盖新事件。
- 更新源：主源为 COS `https://portunus-1321735248.cos.ap-shanghai.myqcloud.com/releases/`（generic）；检查失败切 GitHub `Lixiuxiu559/portunus`。下载失败时，只有当前仍在主源且不是取消错误才切备用源，并重新检查同一版本；备用源没有该版本时明确报错。
- 检查到新版本后会后台下载；界面显示进度、完成或错误。下载完成后 `install` 走 `quitAndInstall`。`Layout` 顶部和设置页共享同一份状态，切页不会丢失后台下载结果。
- macOS 更新额外校验：下载 `*.sig` 必须走 HTTPS、限制 16 KiB；`resources/bin/portunus-updater --prepare` 接收归档、签名、目标目录、版本和当前版本，准备更新事务；安装由 helper 完成后替换应用。签名是独立于 Apple Developer ID 的 Ed25519 签名，私钥不进入仓库或安装包。
- 新版启动后，`App.jsx` 的 `ReadyPage` 调用 `confirmRendererReady`；主进程仅在窗口、后端、renderer 地址和版本都匹配时写入健康标记，供更新事务确认。`mac-update-smoke.mjs` 的 tamper 场景以“退出前拒绝且旧应用不退出”为判据；rollback 场景以“恢复旧版”为判据，均为脚本定义，本轮未执行。
- Dev 无 `app-update.yml`，不执行检查；正式包的更新清单由 electron-builder 生成，仓库源码中不保存运行中的更新缓存。

更新失败的处理边界：按 `mac-update-smoke.mjs` 的 rollback 场景，macOS helper 在替换失败时恢复旧版，用户数据目录保持不动；Windows/Linux 由 electron-updater 报错，按常规行为保留旧安装（本轮未实机验证）。当前页不提供手工结束进程或强制替换步骤，失败时应保留错误信息并重新检查更新，不按进程名清理。

### CI、Release 与测试

`.github/workflows/ci.yml`：

- 触发：所有分支 push、PR、`workflow_dispatch`、`workflow_call`；PR 同组旧检查会取消。
- `Go checks`：Ubuntu，Go 版本取自 `go.mod`，执行 `go test ./... -count=1`、`go vet ./...`、`go build ./...`。
- `Web checks`：Ubuntu，Node 24、pnpm 10、锁文件安装；设置 `ELECTRON_SKIP_BINARY_DOWNLOAD=1`，不启动 Electron；执行 `pnpm test`、`pnpm run typecheck`、`pnpm run build:renderer`。
- CI 不加载用户配置、正式/Dev 数据或上游密钥，也不执行真实生成调用。

`.github/workflows/release.yml`：

- 触发：`v*` 标签或手动 `workflow_dispatch`；先复用 CI 检查。
- 矩阵：macOS arm64（DMG/ZIP）、Windows x64（NSIS/ZIP）、Linux x64（AppImage）；各平台先 `build-go.mjs <goos> <goarch> --release`，再安装依赖、`vite build`、`electron-builder --publish never`。
- 构建环境设置 `CSC_IDENTITY_AUTO_DISCOVERY=false`、`VITE_BASE=./`、`PORTUNUS_UPDATE_SIGNING_KEY`；macOS ZIP 由 `afterAllArtifactBuild` 调用 `sign-mac-update.mjs` 生成 `.sig`，CI 缺少签名 Secret 会直接失败。
- 产物上传后，只有标签触发才创建 draft Release；手动触发只构建产物，不发布。Release 只收集安装包、更新清单和签名等必要文件，排除 `*unpacked*`，并生成固定文件名副本。草稿仍需人工确认后发布。

测试范围：

- `pnpm test` 覆盖主进程身份/地址/sidecar/preload/更新服务/客户端配置/开发代理守卫等纯模块，以及渲染层删除影响、Claude 配置、更新 store、页面资源等工具函数；测试代码存在不等于本轮已执行。
- `pnpm test:e2e` 的 `startup-race-check.mjs` 只针对 macOS 的 `dist-dev` 目录包：先检查 13061 和 CDP 9333 空闲，默认重建 Dev 包，以 `PORTUNUS_SIDECAR_DELAY_MS=3000` 启动，经 CDP 统计 `[HTTP Error]`、`/api` 成功/失败和页面加载。normal 模式要求窗口内 0 次 `[HTTP Error]` 且至少 1 个 `/api` 成功；degraded 模式临时移走内置后端二进制，要求错误页加载且业务 API 请求为 0。退出码 0 通过、1 判据失败、2 环境错误。它不是跨平台或多场景 E2E 套件。
- `mac-update-smoke.mjs` 是独立手动验收脚本，不在 `package.json` 脚本中；仅在 macOS 运行，需要已构建的 `web/bin/mac` 二进制和更新签名私钥，使用临时目录、本地 HTTPS 和 fixture Electron 应用验证 success/tamper/rollback，不启动或修改 `/Applications/Portunus.app`。脚本用 `PORTUNUS_SMOKE_CASE`、`PORTUNUS_SMOKE_SCENARIO`、`PORTUNUS_SMOKE_PORT`、`PORTUNUS_SMOKE_URL` 和 `NODE_EXTRA_CA_CERTS` 驱动 fixture，并从 `PORTUNUS_UPDATE_SIGNING_KEY` 或 `PORTUNUS_UPDATE_SIGNING_KEY_FILE` 读取签名私钥。

本页编写时未执行上述测试、未启动 Electron/Go、未访问更新源、未运行构建，也未查看 GitHub Actions 的线上运行结果。CI 配置和工作流定义不能当作“线上已通过”或“草稿 Release 已发布”的证据。

### 运维排查与回退

- 健康检查：`GET /api/ping`，Dev 为 `http://127.0.0.1:13061/api/ping`，正式为 13060；要求 200 且响应头 `X-Portunus-Instance` 与当前 sidecar UUID 一致。只看端口通不判断归属。
- 日志入口：主进程控制台中的 `[backend]`、`[sidecar]`、`[main]`、`[updater]`；Dev 的应用日志目录为 `Portunus Dev/logs/`，后端工作目录为 `Portunus Dev/backend/`。
- 13061 被占用：sidecar 拒绝启动，Dev 显示错误页；`preview:dev` 和 `test:e2e` 也会在预检阶段退出。处理方式是退出占用 13061 的已有 Dev（通过其界面），或换时再试；不要结束正式版，也不要按进程名清理。
- 后端二进制缺失：执行 `pnpm build:go`（Dev）或对应正式构建；sidecar 会报“后端二进制缺失”。
- Dev 页面空白或启动失败：确认 13061 空闲、`pnpm build:go` 已执行、Vite 5173 已就绪；必要时重新 `pnpm build:dev` 后用 `pnpm preview:dev`。不要删除正式数据目录或覆盖正式应用。
- 正式构建检查：`pnpm build` / `build:mac` / `build:win` 只用于正式包；Release CI 产物先进入 draft，未人工确认前不应视为已发布。正式版和 Dev 可同时运行，但同一身份只能有一个实例。
- 更新失败：保留界面错误；macOS 按验收脚本设计由更新 helper 回滚旧版，Windows/Linux 保留旧安装。Dev 的更新入口永远不可用，出现“禁用正式更新”是预期行为。
- Claude 配置改错：在“接入 → 客户端配置 → Claude Code”使用“从备份回滚”，会把 `settings.json.portunus.bak` 原子写回；备份不会被后续保存覆盖。开发验收优先用 `PORTUNUS_CLIENT_HOME` 指向临时目录，避免触碰真实配置。
- 停止应用：优先托盘“退出”、macOS `Cmd+Q` 或 Dock 退出；Linux 关窗即退出。代码只停止自己创建的后端子进程，`stopAndWait` 的强杀也仅针对该子进程。

## 阅读导航

- [核心概念](../../concepts.md)：运行身份、服务地址、路由与观测的公共术语。
- [资源模块](../resources/README.md)：渠道、模型、分组以及删除影响的后端数据来源。
- 开发说明：`docs/product/DEVELOPMENT.md`（普通路径引用，包含 Dev/正式隔离、启动命令和验证边界）。

## 依据与核验

核验基线为 `b44d75a788f8aa5ef0ad024273d4bf5dd8c95c7e`（dev，2026-10-28）。本轮完成源码与脚本静态核对；主 Agent 已执行并通过前端 245 项测试和 `pnpm --dir web run typecheck`，未启动桌面应用或执行构建。

主要源码：

- 主进程与运行环境：`web/src/main/main.js`、`runtime-profile.js`、`server-address.js`、`app-config.js`、`preload.js`、`sidecar.js`、`tray.js`。
- 客户端配置：`web/src/main/client-config.js`、`web/src/renderer/pages/Clients/ClaudeConfigPanel.jsx`、`web/src/renderer/utils/claudeConfig.js`。
- 更新：`web/src/main/updater.js`、`disabled-updater.js`、`update-service.js`、`update-channels.js`、`mac-update-installer.js`、`mac-update-health.js`、`web/src/renderer/utils/updateStore.js`、`web/src/renderer/hooks/useUpdater.js`。
- 渲染层：`web/src/renderer/index.html`、`main.jsx`、`App.jsx`、`pageResources.js`、`components/Layout.jsx`、`api/request.js`、`api/retryPolicy.js`、`utils/deletionImpact.js`、`components/DeletionImpactBody.jsx`、`pages/Channels/DeleteChannelModal.jsx`、`pages/Models/DeleteModelModal.jsx`、`pages/Groups/DeleteGroupModal.jsx`。
- 构建与开发脚本：`web/package.json`、`vite.config.mjs`、`electron-builder.dev.cjs`、`dev-management-access.mjs`、`scripts/build-go.mjs`、`build-dev.mjs`、`preview-dev.mjs`、`startup-race-check.mjs`、`after-artifacts.cjs`、`sign-mac-update.mjs`、`mac-update-smoke.mjs`。
- 工作流与产品说明：`.github/workflows/ci.yml`、`.github/workflows/release.yml`、`docs/product/DEVELOPMENT.md`、`docs/product/PRODUCT.md`。

核验范围：

- 已逐项追过主进程启动链、sidecar 状态机、preload channel 白名单、Vite 代理、页面路由、删除影响控制器、Claude 配置原子写和更新服务分支。
- 已核对脚本中的实际路径、端口、输出目录、环境变量和工作流触发条件。
- 未执行 `pnpm test:e2e`、`pnpm build:dev`、任何正式打包、`mac-update-smoke.mjs`，也未启动 Electron、Go 后端或访问 COS/GitHub 更新源。
- 未在 Windows、Linux 真机验证关窗/托盘，未验证 GitHub Actions 首次线上运行、分支保护或 draft Release 发布。

## 待确认项

1. **GitHub Actions 线上首跑与发布门禁**：仓库文档明确记录尚未验证 GitHub Actions 首次线上运行，也未配置远端必需状态检查。当前只能确认工作流定义；实际运行结果、分支保护是否生效、草稿 Release 是否被人工发布，均需在远端查看。影响正式发布的准入判断。
2. **未知 hash 的页面行为**：路由表只有六个页面和根重定向，没有通配符；`preloadInitialPage` 对未知 hash 仅回退预加载 `channels`，但 React Router 没有对应兜底 route。是否需要把未知地址重定向到 `/channels`，还是保留空白内容，属于产品/交互选择；当前源码未给出明确答案。
3. **原生 Windows 的构建脚本路径**：Release CI 已改用 env 块设置 `VITE_BASE` 并注释了 Windows PowerShell 不支持 `VAR=x` 前缀；但 `pnpm build`、`build:win`、`build:dev` 仍会经过 `build:renderer`（`VITE_BASE=./ vite build`）或同样的前缀。该路径在原生 Windows 上的实际行为本轮未验证，正式 CI 不受影响。
4. **Windows/Linux 关窗、托盘和更新的实机行为**：代码已按平台分支，macOS 以外的真实窗口生命周期、Linux AppImage 无托盘退出、Windows 隐藏到托盘和更新安装均未在本轮执行验证；若作为发布门槛，需要补充对应平台验收。
