# Portunus

> 一个端点聚合所有 LLM —— 多渠道路由、协议互转、用量记录。
>
> One endpoint for every LLM — multi-channel routing, protocol translation, and usage logging.

Portunus 是一个 LLM API 聚合服务：多渠道接入、按模型定价、模型分组对外提供、全协议互转、调用日志。对外暴露统一的 `/v1` 接口，供 Claude Code、Codex 等客户端调用。

## 功能特性

- **多渠道接入**：上游渠道支持 `openai` / `openai_responses` / `anthropic` / `gemini` 四种协议，自动从渠道拉取模型列表。
- **按模型定价**：价格挂在「渠道 × 模型」上，拉取时用内置价格表填默认值，可手动覆盖。
- **分组路由**：跨渠道聚合模型，按分组名对外暴露，每个分组配一种路由策略（`manual` / `round_robin` / `failover`）。
- **全协议互转**：客户端协议与上游协议不一致时自动转换，流式（SSE）透传。
- **统一鉴权**：简单 API Key，用于日志与费用归属。
- **调用日志**：记录每次调用的模型、渠道、用量与费用。

## 架构

代码集中在 `backend/`，按功能分包，依赖方向单向：`api` / `gateway` / `cron` → `channel` / `model` / `group` / `protocol` → `shared`。

| 模块 | 职责 |
|---|---|
| `channel` | 渠道：上游连接配置（type / base_url / key），自动拉模型 |
| `model` | 模型：归渠道，每个模型带价格 |
| `group` | 分组：跨渠道聚合模型，必配路由策略 |
| `protocol` | 协议转换：OpenAI Chat / Responses ↔ Anthropic ↔ Gemini |
| `gateway` | 对外 `/v1` LLM 接口，按分组策略路由 |
| `api` | 管理 API（`/api/*`，供前端调用） |
| `cron` | 定时任务：同步模型、日志落库 |
| `shared` | 跨模块公共：配置、数据库、APIKey / 日志实体 |

一次调用的数据流：

```
客户端(任意协议) → /v1 → APIKey 鉴权 → 命中分组 → 按策略选模型项
  → 需要则转协议 → 上游调用 → 转回 → 返回 → 记录日志/费用
```

详见 [ARCHITECTURE.md](ARCHITECTURE.md)。

## 快速开始

### 环境要求

- Go 1.26+

### 安装运行

```bash
git clone https://github.com/Lixiuxiu559/portunus.git
cd portunus
GOPROXY=https://goproxy.cn,direct go mod tidy   # 网络直连 proxy.golang.org 超时可用国内镜像
go run .
```

服务默认监听 `0.0.0.0:3060`，SQLite 数据库位于 `data/portunus.db`。

### 配置

首次运行自动生成默认配置 `config.json`：

```json
{
  "server": { "host": "0.0.0.0", "port": 3060 },
  "database": { "type": "sqlite", "path": "data/portunus.db" }
}
```

支持用环境变量覆盖（`PORTUNUS_SERVER_PORT`、`PORTUNUS_DATABASE_TYPE`、`PORTUNUS_DATABASE_PATH` 等）。`database.type` 可换 `mysql` / `postgres`。

## API

### 对外接口（`/v1`，需 API Key）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/v1/models` | OpenAI 兼容模型列表（分组名即模型名） |
| POST | `/v1/chat/completions` | OpenAI Chat |
| POST | `/v1/responses` | OpenAI Responses |
| POST | `/v1/messages` | Anthropic Messages |

鉴权支持 `Authorization: Bearer <key>` 或 `x-api-key` 两种方式。

### 管理 API（`/api`，供前端调用）

| 路径 | 说明 |
|---|---|
| `/api/ping` | 健康检查 |
| `/api/channels` | 渠道 CRUD |
| `/api/models` | 模型 CRUD |
| `/api/groups` | 分组及分组项 CRUD |
| `/api/apikeys` | API Key 管理 |
| `/api/settings` | 全局设置 |
| `/api/logs` | 调用日志与统计 |

## 项目结构

```
backend/
  channel   渠道：上游连接配置，自动拉模型
  model     模型：归渠道，每个模型带价格
  group     分组：跨渠道聚合模型，必配路由策略
  protocol  协议转换：OpenAI Chat/Responses ↔ Anthropic ↔ Gemini
  api       管理 API（/api/*）
  gateway   对外 /v1 LLM 接口
  cron      定时任务：同步模型、日志落库
  shared    跨模块公共：配置、数据库、APIKey / 日志实体
```

## 文档

- [ARCHITECTURE.md](ARCHITECTURE.md) — 架构与关键决策
- [docs/research](docs/research) — 设计研究笔记（new-API 统一调用流程、协议转换、日志）
