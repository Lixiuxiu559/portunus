# Portunus

<p align="center">
  <a href="./README.md">English</a> |
  <strong>简体中文</strong>
</p>

> 一个端点，聚合所有 LLM —— 多渠道路由、协议互转、用量记录。

Portunus 是一个轻量的 LLM API 聚合服务：接入多个上游渠道，按模型定价，分组对外暴露统一的 `/v1` 接口，供 Claude Code、Codex 等客户端直接调用。

## ✨ 功能特性

- **多渠道聚合** — 一个入口管理多个上游供应商，统一接入 OpenAI Chat / Responses、Anthropic、Gemini。
- **模型自动同步** — 渠道保存即自动拉取上游模型列表，也支持一键手动同步。
- **协议互转** — 客户端与上游协议不一致时自动转换，流式（SSE）透传。
- **自动故障转移** — 分组内多模型按优先级排序，上游失败自动切换到下一个。
- **按模型定价** — 价格挂在「渠道 × 模型」上，拉取时自动套默认价，可手动覆盖。
- **四维用量计费** — 输入 / 输出 / 缓存读 / 缓存写 token 分开记录与计费。
- **轻量起步** — 单二进制 + SQLite，无外部依赖。

## 🚀 快速开始

### Docker

```bash
docker run -d --name portunus -v /path/to/data:/app/data -p 3060:3060 lixiuxiu559/portunus
```

或使用 Docker Compose：

```bash
docker compose up -d
```

### 从源码运行

**环境要求:** Go 1.26+

```bash
git clone https://github.com/Lixiuxiu559/portunus.git
cd portunus
GOPROXY=https://goproxy.cn,direct go mod tidy   # 网络直连超时可用国内镜像
go run .
```

服务默认监听 `0.0.0.0:3060`，数据库文件 `data/portunus.db`。

### 前端（管理后台）

管理后台位于 `web/`（Electron + React + Vite）。开发服务器会把 `/api`、`/v1` 代理到后端 `localhost:3060`，因此需先启动后端（`go run .`）。

```bash
cd web
pnpm install        # 安装依赖
pnpm run dev:web    # 启动 Vite 并打开 http://localhost:5173
pnpm run dev        # 同时启动 Vite + Electron 桌面窗口
pnpm run build      # 构建渲染层并打包桌面应用
```

## 📝 配置

首次运行自动生成 `config.json`：

```json
{
  "server": { "host": "0.0.0.0", "port": 3060 },
  "database": { "type": "sqlite", "path": "data/portunus.db" }
}
```

| 配置项 | 说明 | 默认值 |
|---|---|---|
| `server.host` | 监听地址 | `0.0.0.0` |
| `server.port` | 监听端口 | `3060` |
| `database.type` | 数据库类型（`sqlite` / `mysql` / `postgres`） | `sqlite` |
| `database.path` | 数据库路径 | `data/portunus.db` |

支持环境变量覆盖：

| 环境变量 | 对应配置 |
|---|---|
| `PORTUNUS_SERVER_PORT` | `server.port` |
| `PORTUNUS_DATABASE_TYPE` | `database.type` |
| `PORTUNUS_DATABASE_PATH` | `database.path` |

## 🔌 API

### 对外接口（`/v1`，需 API Key）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/v1/models` | 模型列表（分组名即模型名） |
| POST | `/v1/chat/completions` | OpenAI Chat |
| POST | `/v1/responses` | OpenAI Responses |
| POST | `/v1/messages` | Anthropic Messages |

鉴权：`Authorization: Bearer <key>` 或 `x-api-key`。

### 管理接口（`/api`）

| 路径 | 说明 |
|---|---|
| `/api/ping` | 健康检查 |
| `/api/channels` | 渠道 CRUD + 模型同步 |
| `/api/models` | 模型 CRUD |
| `/api/groups` | 分组及分组项 CRUD |
| `/api/apikeys` | API Key 管理 |
| `/api/settings` | 全局设置 |
| `/api/logs` | 调用日志与统计 |

## 📖 更多文档

- [ARCHITECTURE.md](ARCHITECTURE.md) — 架构、核心概念与设计决策
- [docs/research](docs/research) — 设计研究笔记