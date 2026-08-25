# Portunus Web（Electron + React + Vite）

模板骨架，UI 使用 HeroUI v3（版本与 inflow 一致：`@heroui/react` / `@heroui/styles` 3.2.4），主题变量定义在 `src/renderer/index.css`。

## 目录结构

```
web/
  src/
    main/          Electron 主进程（main.js / preload.js）
    renderer/      Vite + React 渲染层（index.html / main.jsx / App.jsx / index.css）
      api/         全局 axios + 分模块接口封装（request / channel / model / group / setting / apikey / log）
  package.json     Electron 固定 42.3.3（与 inflow 一致）
  vite.config.mjs  @vitejs/plugin-react + @tailwindcss/vite；/api、/v1 代理到后端 8080
```

## API 封装

- `src/renderer/api/request.js` — 全局 axios 实例（baseURL `/api`，Vite 代理到后端 8080），成功直接返回业务数据，失败统一 toast + reject。
- 各模块按后端 `/api/*` 路由拆分：`channel` / `model` / `group` / `setting` / `apikey` / `log`。
- 统一出口：`import { listChannels, createModel } from '@/api'`。

## 常用命令

```bash
pnpm install        # 安装依赖
pnpm run dev:web    # 启动 Vite 并打开网页（http://localhost:5173）
pnpm run dev        # 同时启动 Vite + Electron 桌面窗口
pnpm run build      # 构建渲染层并打包桌面应用
```
