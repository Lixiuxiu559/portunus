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
go run .             # 启动服务（默认监听 0.0.0.0:3060，数据库 data/portunus.db）
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

## Docker

单镜像：前端（nginx 托管 + 反代）和后端（Go 二进制，同容器监听 127.0.0.1:3060）打包为一个镜像。

```bash
docker compose up -d    # 纯拉 Docker Hub 镜像直接起，对外 3060
```

compose 默认只用 `image:`（远程镜像），不触发本地构建；要本地构建时取消 compose 里 `build:` 段注释（build 优先于 image）。

宿主机 3060 端口三用 —— `http://localhost:3060/` Web 管理后台、`/api/*` 管理 API（nginx 反代）、`/v1/*` LLM 网关（nginx 反代，已为 SSE 关缓冲放宽超时）。

发布镜像（需先 `HTTPS_PROXY=http://127.0.0.1:7890 docker login`；直连 auth.docker.io 被墙，必须走代理）：

```bash
# 构建并推送多架构镜像（amd64 + arm64）
HTTPS_PROXY=http://127.0.0.1:7890 docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -f Dockerfile -t lijx559/portunus:latest --push .
```

推送后 `docker compose pull && docker compose up -d` 更新本地。

注意：macOS 系统代理（如未启动的 clash 127.0.0.1:7890）会被自动注入为 build-arg 且 ENV 无法覆盖，Dockerfile 已在各联网 RUN 前 unset 处理；基础镜像默认走 DaoCloud 国内加速（`--build-arg BASE_REGISTRY=library` 切回官方源）。

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
