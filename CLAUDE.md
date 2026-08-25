# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目简介

Portunus 是一个 LLM API 聚合服务：多渠道接入、按渠道模型定价、模型分组对外提供、全协议互转、调用日志。

- 模块路径：`github.com/Lixiuxiu559/portunus`
- Go 版本：`1.26.6`
- 技术栈：Gin + GORM（SQLite 起步，保留换 MySQL/PostgreSQL 能力）
- 前端：暂缓，管理 API 已为前端接入预留

架构详见 `ARCHITECTURE.md`。

## 常用命令

```bash
go build ./...       # 编译所有包
go run .             # 启动服务（默认监听 0.0.0.0:8080，数据库 data/portunus.db）
go test ./...        # 运行所有测试
go test ./... -run TestXxx -v   # 运行单个测试（按函数名）
go vet ./...         # 静态检查
go fmt ./...         # 格式化代码
```

依赖管理：网络直连 `proxy.golang.org` 可能超时，可改用国内镜像：

```bash
GOPROXY=https://goproxy.cn,direct go mod tidy
```

运行时会在项目根生成 `data/`（SQLite）和 `config.json`，两者均已被 `.gitignore` 忽略。

## 架构

代码集中在 `backend/` 目录，按功能模块分包，依赖方向单向：

```
backend/
  channel   渠道：上游连接配置，自动拉模型
  model     模型：归渠道，每个模型带价格（内置表填默认 + 手动覆盖）
  group     分组：跨渠道聚合模型，每个分组必配路由策略（manual/round_robin/failover）
  protocol  协议转换：OpenAI Chat/Responses ↔ Anthropic ↔ Gemini
  api       管理 API（/api/*，供前端调用）
  gateway   对外 /v1 LLM 接口（供 claude code / codex 等调用）
  cron      定时任务：定时同步模型、日志落库
  shared    跨模块公共：配置、数据库、APIKey / 日志实体
```

依赖方向：`api` / `gateway` / `cron` → `channel` / `model` / `group` / `protocol` → `shared`。模块间用外键 ID 关联，避免包级循环依赖。

一次调用的数据流：

```
客户端(任意协议) → /v1 → APIKey 鉴权 → 命中分组 → 按策略选模型项
  → 需要则转协议 → 上游调用 → 转回 → 返回 → 异步记日志/费用
```
