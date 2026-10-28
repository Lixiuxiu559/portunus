# 桌面与开发环境

## 文档信息

| 字段 | 内容 |
|---|---|
| 适用范围 | Electron 运行身份、sidecar、页面入口、Dev 隔离与构建边界 |
| 事实状态 | 部分核验 |
| 核验基线 | `9bf6dbb`；2026-10-27 当前工作区 |
| 核验时间 | 2026-10-27 |

## 用途与边界

Electron 主进程负责运行身份、窗口、托盘、更新和 Go sidecar；React 渲染层提供管理界面。后端仍通过同一 `/api` 管理面服务界面，通过 `/v1` 服务客户端。Dev 与正式版依靠应用身份和数据路径隔离，不通过复制正式配置或覆盖 HOME 实现。

当前渲染层有渠道、模型、分组、日志、接入、设置六页，使用 HashRouter 和按页懒加载。默认路由及未知页面回退到 `/channels`；现行产品、架构和用户文档已按此界面口径统一。

## 主要流程

### 运行身份与端口

| 项目 | Dev | 正式桌面版 |
|---|---|---|
| 名称 / Bundle ID | Portunus Dev / `com.portunus.app.dev` | Portunus / `com.portunus.app` |
| 端口 | 13061 | 13060 |
| macOS 数据根目录 | `~/Library/Application Support/Portunus Dev` | 沿用现有 `@portunus/web` 数据目录 |
| 构建输出 | `web/dist-dev/` | `web/dist-electron/` |
| 更新 | 禁用 | 正式更新服务 |

`initializeProfile` 在未打包或显式 development 元数据时设置 Dev 名称、userData、session 和日志目录，并在单实例锁前执行。`preloadProfile` 未收到 production 参数时默认 Dev，避免回退正式端口。监听地址由 networkMode 决定，内部管理 URL 始终使用回环地址。

### sidecar 生命周期

主进程按平台选择随包或 `web/bin` 下的后端二进制，注入数据路径、端口和监听地址。sidecar：

1. 启动前确认目标端口空闲，不接管已有服务。
2. 为子进程生成实例标识，健康检查要求 `/api/ping` 返回对应标识且子进程仍存活。
3. 只停止自己创建的子进程；旧进程退出不能覆盖新一代重启状态。
4. Dev 启动失败时显示错误页，不加载会向已有服务发请求的业务页面。

macOS 关窗可继续运行，Windows 关窗隐藏到托盘，Linux 当前关窗退出；真正退出后后端按主进程生命周期停止。跨平台行为依据现有代码与开发文档，本轮未重新实机验证。

### 开发与构建

在 `web/` 执行：

- `pnpm dev`：构建本机 Go 后端，启动 Vite 与 Electron Dev。
- `pnpm build:dev`：构建后端、renderer 及独立目录包；`pnpm preview:dev` 只预览该包。
- `pnpm build:mac` / `pnpm build:win`：正式发布构建，不能用于覆盖 Dev 或作为日常预览。
- `pnpm test`、`pnpm run typecheck`、`pnpm test:e2e`：前端单元测试、类型检查、Dev 启动检查；后者需先退出已有 Dev。

`go run .` 是独立命令行后端，默认 3060 和仓库数据目录，不属于桌面 Dev。正式版与 Dev 不应通过进程名批量结束；更新、构建和清理均须核对应用路径与 Bundle ID。

客户端配置页可按用户操作读写真实 `~/.claude/settings.json`，Dev 与正式版共享这一外部文件，不是应用数据隔离的一部分。首次写入备份及验收用 `PORTUNUS_CLIENT_HOME` 规则见[开发说明](../../../product/DEVELOPMENT.md)。

## 阅读导航

- [后端启动与管理访问边界](../runtime/README.md)
- [网关提供给客户端的服务](../gateway/README.md)
- [渠道页与额度前端边界](../quota/README.md)

## 依据与核验

核对 `web/src/main/runtime-profile.js`、`sidecar.js`、`main.js`、`server-address.js`、`web/src/renderer/App.jsx`、`pageResources.js`、`components/Layout.jsx`、`web/package.json`、`web/scripts/build-dev.mjs`、`preview-dev.mjs`、`build-go.mjs`、`web/electron-builder.dev.cjs` 与 `docs/product/DEVELOPMENT.md`。

2026-10-27 另已按明确路径退出并清理 Dev 的 `web/dist-dev`、`web/dist`、`web/bin/mac` 可再生构建产物，正式应用和数据保留；该次环境清理不等于本轮重新执行桌面测试。初始化 Wiki 未再修改应用源码。

## 待确认项

1. 本轮未执行跨平台关窗/托盘、应用更新、打包预览或局域网实机验证。
2. Dev 数据隔离不覆盖用户主动编辑的外部客户端配置文件，验收写入需使用独立临时 home。
