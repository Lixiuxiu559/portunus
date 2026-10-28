# 运行入口与公共基础设施

## 文档信息

| 字段 | 内容 |
|---|---|
| 适用范围 | Go 启动装配、配置、数据库、管理/服务边界与后台任务 |
| 事实状态 | 部分核验 |
| 核验基线 | `9bf6dbb`；2026-10-27 当前工作区 |
| 核验时间 | 2026-10-27 |

## 用途与边界

`main.go` 是后端装配入口：加载 `config.json` 和环境覆盖，初始化主库与日志库，收敛历史余额来源并迁移实体，准备默认设置与 API Key，再注册 `/v1`、额度服务、`/api`、定时任务及 HTTP 服务。

`backend/shared` 提供配置、SQLite、API Key、设置和观测实体/查询。配置结构保留数据库类型字段，但 `InitDB` 当前只接受 SQLite，不能将“预留换库能力”写成已经支持 MySQL/PostgreSQL。

网关通过 `Deps` 接收 HTTP 客户端、进程内健康状态、鉴权与日志写入函数。`quota` 的账户读取由 `main` 注入最小 `ChannelLoader`，避免额度模块导入渠道、上游或网关包。

## 主要流程

### 启动与关闭

```text
读取配置 → 主库与历史来源迁移 → 日志库迁移 → 默认设置/Key
→ 注册 gateway 和 quota → 注册 /api → 启动额度调度与模型/日志定时任务
→ 监听 HTTP → 收到中断或 SIGTERM 后最多等待 5 秒关闭 HTTP
```

- 独立 `go run .` 默认监听 `0.0.0.0:3060`，主库默认 `data/portunus.db`。这不是桌面 Dev 的启动方式。
- `Database.LogPath` 非空时使用独立 SQLite 日志库，否则复用主库；日志迁移包含日志、尝试和切换诊断。
- 熔断、限流冷却、渠道并发等健康状态在进程中共享，不是跨实例持久化协调。

### 管理与服务请求

`/api` 的路由注册前统一安装 `localManagementOnly`，检查真实回环对端、Host 和 Origin。`/v1` 单独使用 API Key 中间件，支持 Bearer 和 `x-api-key`。二者不是同一套授权。

额度管理路由由 `registerQuotaRoutes` 注册，包括适配器目录、摘要读取、NewAPI 单位识别、来源 CRUD、草稿测试和主动刷新。额度网络执行仍归 `quota.Service`，API 层不直接计算余额。

### 定时任务

`cron.Start` 每分钟检查同步设置，按间隔调用 `model.SyncAutoChannels`；每 60 分钟按 `log_retention_days` 清理日志及关联观测。间隔为 0 时分别关闭自动同步或过期清理。额度有自己的有界调度，不由这个 cron 代替。

## 阅读导航

- [桌面 sidecar 与 Dev 数据路径](../desktop/README.md)
- [服务转发与依赖注入](../gateway/README.md)
- [模型同步的资源语义](../resources/README.md)
- [日志库及清理口径](../observability/README.md)

## 依据与核验

核对 `main.go`、`backend/shared/config.go`、`backend/shared/db.go`、`backend/api/api.go`、`backend/api/local_access.go`、`backend/api/log.go`、`backend/cron/cron.go`、`go.mod` 和 `ARCHITECTURE.md`。当前 Go 模块为 `github.com/Lixiuxiu559/portunus`，`go.mod` 声明 Go 1.26.6。

未在本轮执行启动、数据库迁移或关闭测试；未读取任何真实数据库、密钥或用户配置。

## 待确认项

- 架构文档的依赖方向是维护约束，本轮没有执行包级依赖自动检查，不能宣称所有包均经工具证明无循环。
- MySQL/PostgreSQL 仅有配置与抽象预留，实际支持时间和迁移方案未确定。
