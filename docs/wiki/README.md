# Portunus

> 本地运行的多上游 API 聚合网关，把渠道模型组织为分组，为客户端和应用提供稳定的 `/v1` 入口。

## 项目简介

Portunus 面向同时使用多个官方 API 或中转站的个人开发者：渠道保存一个上游账户的连接与凭据，模型目录保存该渠道可用的上游资源，分组再把这些资源组合成客户端使用的稳定 `model`。Go 后端同时提供管理面 `/api` 和服务面 `/v1`，主要交付形态是 Electron 桌面应用。

当前客户端生成入口为 Chat Completions、Responses 和 Messages；上游协议包括 OpenAI Chat、OpenAI Responses、Anthropic 与 Gemini。Gemini 只作为上游协议，不是独立的客户端生成入口。分组只支持 `manual`（手动）和 `failover`（固定顺序故障转移），不提供轮询、权重或费用路由。

产品不提供价格配置、默认价格套用、调用费用计算或费用统计；历史价格、货币和费用列仅为存储兼容保留。余额查询只用于观测，不参与选路。模型发现成功不等于真实生成调用通过，一次真实调用成功也不能推广为所有模型和协议均已验证。产品定义见 [PRODUCT.md](../product/PRODUCT.md)。

## 技术栈

| 方面 | 选择 | 依据 |
|---|---|---|
| 后端语言 | Go 1.26.6 | [go.mod](../../go.mod) |
| HTTP 框架 | Gin 1.12.0 | [go.mod](../../go.mod) |
| 存储 | GORM 1.31.2 + SQLite（glebarez/sqlite 1.11.0） | [go.mod](../../go.mod) |
| 桌面壳 | Electron 42.3.3 | [web/package.json](../../web/package.json) |
| 管理界面 | React 19.2.7、React Router 7.18.2、Vite 8.0.16 | [web/package.json](../../web/package.json) |
| 样式组件 | Tailwind CSS 4.3.0、HeroUI 3.2.4 | [web/package.json](../../web/package.json) |
| 打包发布 | electron-builder 26.0.12、GitHub Actions | [web/package.json](../../web/package.json)、[release.yml](../../.github/workflows/release.yml) |
| 测试 | Go testing、`node --test`、`tsc --noEmit` | [ci.yml](../../.github/workflows/ci.yml) |

## 快速上手

命令行后端（仓库根执行）：

```bash
go mod download
go run .                 # 默认监听 0.0.0.0:3060，读取 config.json，数据写入 data/portunus.db
go test ./...
go vet ./...
go build ./...
```

桌面开发统一使用独立的 Portunus Dev（`web/` 下执行）：

```bash
pnpm install
pnpm dev                 # 编译 Go 后端 → Vite 5173 → Electron；Dev 后端固定 127.0.0.1:13061
pnpm test                # 前端与主进程离线单元测试
pnpm run typecheck
pnpm build:dev           # 构建独立 Dev 目录包（web/dist-dev/）
pnpm preview:dev         # 启动已构建的 Dev 预览
```

Dev 与正式版使用不同的身份、端口、数据目录和更新链路：Dev 使用 Portunus Dev 身份（打包 Dev 的 Bundle ID 为 `com.portunus.app.dev`）、固定 13061，macOS 数据目录为 `~/Library/Application Support/Portunus Dev`；正式版使用 13060。不迁移或复制正式配置，不按进程名结束应用。完整命令与边界见[本地开发与测试](指南/本地开发与测试.md)。

## 文档导航

### 系统与概念

- [系统架构](系统架构.md)：说明 Portunus 的进程组成、模块依赖与请求主链路；不展开单模块内部实现和具体配置默认值。
- [核心概念](核心概念.md)：统一渠道、模型、分组、凭据与调用记录的口径及跨模块边界；不替代各模块的操作步骤与实现细节。

### 快速上手与关键流程

- [本地开发与测试](指南/本地开发与测试.md)：说明后端与桌面的本地启动、测试命令及 Dev/正式隔离；不覆盖正式打包发布与线上 CI 运行结果。
- [一次请求的生命周期](指南/请求生命周期.md)：串起 /v1 请求从鉴权、分组解析、目标循环到协议转换、交付与记录的完整链路；不重复各模块的配置项全集。
- [构建、发布与更新](指南/构建发布与更新.md)：说明安装包构建、CI/Release 工作流、更新签名与客户端更新链路；不证明线上工作流或某个安装包已经验证。

### 核心模块

- [运行入口与基础设施](核心模块/运行入口与基础设施.md)：说明 Go 进程启动、配置与数据库、管理接口边界、API Key、设置和定时维护；不负责渠道业务、网关路由与协议转换。
- [渠道、模型与分组](核心模块/渠道模型与分组.md)：说明渠道接入与连接解析、模型导入和同步、分组策略与删除级联；不执行实际转发、余额查询和日志查询。
- [网关转发与保护](核心模块/网关转发与保护.md)：说明 /v1 入口、目标循环与失败处置、限流冷却、熔断、并发准入和主动验证；不负责协议字段转换和资源 CRUD。
- [协议转换与流式交付](核心模块/协议转换与流式交付.md)：说明四种上游协议的请求/响应转换、流式事件与能力映射；不决定选路、重试和资源管理。
- [余额与额度](核心模块/余额与额度.md)：说明独立余额来源、适配器、缓存状态、有界调度与前端查询语义；不参与路由、鉴权或调用费用计算。
- [调用观测与日志](核心模块/调用观测与日志.md)：说明调用日志、显式 HTTP 尝试、路由诊断的写入与查询口径及保留清理；不把记录数当作客户端请求数或成功率。
- [桌面主进程与 sidecar](核心模块/桌面主进程与sidecar.md)：说明 Electron 主进程、运行身份与数据隔离、sidecar 生命周期、IPC 和客户端配置读写；不实现网关业务与渲染层页面。
- [管理界面与请求层](核心模块/管理界面与请求层.md)：说明 React 渲染层的路由、API 请求与缓存、删除影响确认和客户端接入页面；不替代后端接口契约与桌面主进程生命周期。

## 维护说明

本 Wiki 的目录、摘要和来源以 [catalog.json](catalog.json) 为唯一真源；每页开头的一行引用块与 catalog 的 `summary` 一致，页尾列出该页依赖的源码或文档。文档语言为简体中文，目录与文件名同样使用中文。

本版按新结构重建，`catalog.json` 是唯一目录真源；旧版 `wiki.config.json` / `reviews.json` 已移除。

本轮核验于 2026-10-30 在 `dev` 分支工作区完成：`go test ./...`、`go vet ./...`、前端 245 项测试与 `pnpm run typecheck` 均已实际执行并通过。核验为静态源码核对加以上测试，未启动正式或 Dev 桌面应用，未访问真实上游、读取业务凭据或验证安装包；各页结论以其列出的来源和当前代码为准。

<details><summary>来源</summary>

- [README.md](../../README.md)
- [CLAUDE.md](../../CLAUDE.md)
- [docs/product/PRODUCT.md](../product/PRODUCT.md)
- [docs/product/DEVELOPMENT.md](../product/DEVELOPMENT.md)
- [go.mod](../../go.mod)
- [web/package.json](../../web/package.json)

</details>
