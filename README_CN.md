<p align="center">
  <img src="web/src/renderer/public/logo.png" alt="Portunus Logo" width="180" />
</p>

<h1 align="center">Portunus</h1>

<p align="center">
  <a href="./README.md">English</a> |
  <strong>简体中文</strong>
</p>

> 一个端点，聚合所有 LLM —— 多渠道路由、协议互转、用量记录。

Portunus 是一个轻量的 LLM API 聚合服务：接入多个上游渠道，按模型定价，分组对外暴露统一的 `/v1` 接口，供 Claude Code、Codex 等客户端直接调用，全部操作在一个桌面应用内完成。

## ✨ 功能特性

- **桌面应用，开箱即用** — Electron 壳 + Go 后端 sidecar 打进同一个安装包，装完即用，无需另行部署；应用内自动更新，监听范围可切「仅本机 / 局域网」，明暗双主题。
- **多渠道聚合** — 一个入口管理多个上游供应商，统一接入 OpenAI Chat / Responses、Anthropic、Gemini。
- **协议互转** — 客户端与上游协议不一致时自动转换，SSE 流式透传，流式工具调用全协议可用；模型名追加 `-thinking` 后缀即可开关思考模式（`reasoning_content` 全协议对齐）。
- **智能路由与容错** — 分组三种路由策略：手动指定 / 轮询 / 故障转移，拖拽排序优先级；上游假死快速切换、失败重试与熔断保护，避免单点拖垮请求。
- **客户端一键接入** — 应用内直接管理 Claude Code 与 Codex 的配置文件：自动写入服务地址与令牌、按槽位映射模型（含 1M 上下文槽位），改动前自动备份、支持一键回滚。
- **按模型定价，四维计费** — 价格挂在「渠道 × 模型」上，输入 / 输出 / 缓存读 / 缓存写 token 分开记录与计费；拉取模型时自动套内置默认价，可手动覆盖。
- **调用日志与统计** — 每次调用记录协议、模型、分组、耗时、token 与费用（含流式标记与 request_id），支持按保留天数自动清理过期日志。
- **模型自动同步** — 渠道保存即拉取上游模型列表，支持定时自动同步与一键全量同步。
- **轻量起步** — 单二进制 + SQLite，无外部依赖。

## 🚀 快速开始

### 桌面应用（Electron）

Portunus 以自包含的 Electron 桌面应用交付：Go 后端作为 sidecar 子进程打进安装包，装一个应用即可使用，无需另行部署。

从 [GitHub Releases](https://github.com/Lixiuxiu559/portunus/releases/latest) 下载对应平台的安装包（macOS arm64 DMG/ZIP、Windows x64 NSIS），或在本地构建：

### 安装说明

**macOS（Apple Silicon）**

1. 打开 DMG，将 Portunus 拖入「应用程序」
2. 首次打开若提示 **"已损坏，无法打开"** —— 这是 macOS 对未签名应用的 Gatekeeper 策略，并非真的损坏。在终端执行以下命令后重新打开即可：

```bash
sudo xattr -rd com.apple.quarantine /Applications/Portunus.app
```

> 引入签名与公证后可免去此步骤；在那之前，每次安装后需执行一次上述命令。

**Windows（x64）**

双击安装。SmartScreen 若提示"未知发布者"，点「更多信息 → 仍要运行」。

### 本地构建

```bash
cd web
pnpm install
pnpm run build:mac   # macOS DMG / ZIP（arm64）
pnpm run build:win   # Windows NSIS（x64）
```

产物在 `web/dist-electron/`。推送 `v*` 标签会触发 GitHub Actions 矩阵构建两个平台，并上传 **draft** 草稿 Release —— 人工确认后手动发布。

### 从源码运行

**环境要求:** Go 1.26+

```bash
git clone https://github.com/Lixiuxiu559/portunus.git
cd portunus
GOPROXY=https://goproxy.cn,direct go mod tidy   # 网络直连超时可用国内镜像
go run .
```

服务默认监听 `0.0.0.0:3060`，数据库为 SQLite（落 `data/` 目录）。首次运行会自动生成 `config.json`，所有配置项均可通过 `PORTUNUS_*` 环境变量覆盖（如 `PORTUNUS_SERVER_PORT`、`PORTUNUS_DATABASE_PATH`）。

### 本地开发（管理后台）

管理后台位于 `web/`（Electron + React + Vite）。开发时需先启动后端，Vite 会把 `/api`、`/v1` 代理到后端 `localhost:3060`。

**环境要求:** Node.js 20+ 与 pnpm

```bash
go run .            # 终端 1：启动 Go 后端

cd web              # 终端 2：启动前端
pnpm install
pnpm run dev:web    # 浏览器开发：打开 http://localhost:5173
pnpm run dev        # 或 Electron 桌面窗口（自动等待后端就绪）
```

后端测试：`go test ./...`。

## 👥 贡献者

- **liuchang**

## 📖 更多文档

- [ARCHITECTURE.md](ARCHITECTURE.md) — 架构、核心概念与设计决策
- [docs/research](docs/research) — 设计研究笔记
