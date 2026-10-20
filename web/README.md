# Portunus 管理界面（Electron + React + Vite）

六个主页面为模型分组、上游渠道、上游模型、服务接入、调用记录和设置。HeroUI v3 提供基础组件，主题变量位于 `src/renderer/index.css`，布局约定见[设计样式](../docs/product/DESIGN-STYLE.md)。

## 运行与隔离

在 `web/` 执行：

```bash
pnpm install --frozen-lockfile
pnpm dev                 # 编译并管理 Go Dev 后端，启动 Vite 与 Electron
pnpm run dev:web         # 仅启动 Vite，需已有 Dev 后端
pnpm run build:renderer  # 仅构建 renderer
pnpm run build:dev       # 独立 Dev 目录包
pnpm run preview:dev     # 启动 Dev 目录包，不安装到 /Applications
pnpm test                # 前端/主进程离线单元测试
pnpm run test:e2e        # 桌面启动检查；先退出已有 Dev
```

Vite 地址 `http://127.0.0.1:5173`，`/api`、`/v1` 代理到 Dev 后端 `127.0.0.1:13061`。正式桌面版使用 13060；独立 `go run .` 默认 3060，不是 Vite 当前代理目标。正式版与 Dev 的数据、会话及更新隔离，不复制正式数据库或凭据。详见[开发说明](../docs/product/DEVELOPMENT.md)。

正式构建使用 `build:mac` / `build:win` 或现有发布流程，产物在 `dist-electron/`；Dev 产物在 `dist-dev/`。`pnpm build` 会编译后端并打包正式应用，日常开发优先使用 Dev 命令。

## 源码与 API

- `src/main/`：Electron 主进程、preload、sidecar、服务地址与更新管理。
- `src/renderer/`：React 页面、组件、样式、API 与纯函数测试。
- `vite.config.mjs`：React/Tailwind 插件、本机代理边界、renderer 构建配置。
- `src/renderer/api/request.js`：浏览器用 `/api`，打包后的 file 页面从 preload 取得后端地址；成功直接返回业务数据，失败统一 toast + reject。
- API 模块按 channel/model/group/setting/apikey/log 拆分，统一从 `api/index.js` 导出。管理接口仅用于本机，不用服务令牌替代管理访问边界。

分组选择器向服务端按渠道/名称分页查询，跨页保留已选成员；分组响应直接携带成员名和渠道，不加载前 1000 条目录作为全集。

## 调用记录与尝试链

- `Logs.jsx` 保留日志行视图，新增按请求关联视图；归并使用 `/api/logs/requests`，不能在前端合并当前页。
- 筛选命中任一日志即可返回关联项，摘要包含整个请求；统计卡仍按匹配日志行汇总。
- `LogDetailModal.jsx` 展示单条/最近日志；`RequestAttemptTimeline.jsx` 按页查询 `/api/logs/attempts`，保留错误重试和过期响应失效处理。
- 无 ID 历史日志独立；没有尝试记录不代表实际 0 次，连续序号不证明完整，最近结果不证明最终成功。
- 时间、渠道、结果必须从同一 latest_log 读取，不能将 last_at 时间范围与最近写入记录混用。

操作与统计口径见[调用记录与排障](../docs/call-records.md)。CI 使用锁文件安装、`pnpm test` 与 renderer 构建；不自动执行桌面启动或收费上游验证。
