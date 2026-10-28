# 运行入口与公共基础设施

## 文档信息

| 字段 | 内容 |
|---|---|
| 适用范围 | Go 进程启动、配置与 SQLite、管理/服务接口注册、API Key 与设置、定时同步和日志清理 |
| 事实状态 | 已核验 |
| 核验基线 | `b44d75a788f8aa5ef0ad024273d4bf5dd8c95c7e`（`dev`）；2026-10-28 源码 |
| 核验时间 | 2026-10-28 |

## 用途与边界

`main.go` 是 Go 后端的唯一生产装配入口：读取配置，初始化主库和日志库，执行迁移，向网关和管理 API 注入实现，启动额度调度、定时任务和 HTTP 服务。模块间尽量通过注入函数或外键 ID 关联，但 `shared.DB`、`shared.LogDB` 是进程级数据库句柄，不是完全隔离的依赖注入容器。

本模块覆盖：

- 配置文件、环境变量覆盖及 SQLite 主库/独立日志库。
- `/api` 的本机管理访问边界、`/v1` 注册与生产 writer 注入。
- 命名服务 API Key、运行设置与错误响应的公共语义。
- 模型自动同步、过期日志/尝试/路由诊断清理和进程关闭。

不覆盖渠道保存、分组选路、协议转换、额度适配器及桌面包构建。独立 `go run .` 默认监听 `0.0.0.0:3060`，使用启动工作目录下的 `config.json` 和 `data/portunus.db`；桌面正式版和 Dev 由 Electron 注入不同端口及数据路径，见[桌面模块](../desktop/README.md)。

## 主要流程

### 配置与启动顺序

`shared.Load("config.json")` 先构造默认值，再读取**当前进程工作目录**中的指定文件，最后应用 `PORTUNUS_*` 环境变量：

| 配置 | 独立 Go 默认值 | 覆盖方式 |
|---|---|---|
| `server.host` / `server.port` | `0.0.0.0` / `3060` | `PORTUNUS_SERVER_HOST` / `PORTUNUS_SERVER_PORT` |
| `database.type` / `database.path` | `sqlite` / `data/portunus.db` | `PORTUNUS_DATABASE_TYPE` / `PORTUNUS_DATABASE_PATH` |
| `database.log_path` | 空，复用主库 | `PORTUNUS_DATABASE_LOG_PATH` |
| `proxy` | 重试、期限、熔断与资源限额的默认值 | `PORTUNUS_PROXY_*` |

配置文件不存在时使用默认值，不会由 `Load` 主动创建；非法 JSON、读取失败、负数限额或部分上下限倒置会使启动失败。桌面主进程在隔离的工作目录和数据路径下启动后端，不能用仓库根的配置推断桌面行为。网关默认值的实际消费见[网关模块](../gateway/README.md)。

`shared.InitDB` 当前只接受 `sqlite`；配置中的 MySQL/PostgreSQL 名称会返回“暂不支持的数据库类型”，目录结构保留扩展点不代表已可切换。SQLite 打开时自动创建父目录。主库随后按以下顺序准备：

1. `quota.DedupeSources` 收敛历史重复余额来源。
2. 迁移渠道、模型、分组及成员、API Key、设置、余额来源/状态。
3. 将旧 `round_robin` 分组迁为 `failover`，保留成员与优先级。
4. `InitLogDB` 打开独立日志库或复用主库，迁移 `Log`、`UpstreamAttempt`、`RoutingTrace`。
5. 补齐缺失设置，并在从未创建过任何 API Key 的空库中创建默认 Key。

这些启动步骤失败会 `log.Fatalf` 终止进程；启动失败不是可忽略的后台警告。没有跨主库/日志库的分布式事务，也没有在此实现业务数据库备份或恢复。

### HTTP 注册与访问边界

`gateway.Register` 注册 `/v1/models`、三个生成入口及 API Key 鉴权。`api.Register` 为 `/api` 注册渠道、模型、分组、设置、API Key、日志与额度接口。`main` 注入的生产网关依赖包括：

- 基于配置的 HTTP client、熔断器、429 冷却和渠道并发 store。
- `shared.AuthenticateAPIKeyIdentity`。
- 同步写入 `LogDB` 的 `LogWrite`、`AttemptWrite`、`RoutingWrite`。其中 `LogWrite` 当前忽略数据库错误，尝试与诊断写入失败只记录告警；写入失败不会额外调用上游。

`localManagementOnly` 保护所有 `/api`，包括 `/api/ping`：真实 TCP 对端必须是 IPv4/IPv6 回环地址，Host 必须是 localhost 或回环地址，Origin 为空、受允许的本机页面，或为带 `X-Portunus-Local: 1` 的桌面 `null`。`X-Forwarded-For` 和 `X-Real-IP` 不改变判定。局域网监听仍开放需令牌鉴权的 `/v1`，但不等于开放管理面。

`/api/ping` 在设置 `PORTUNUS_INSTANCE_ID` 时返回同名响应头；桌面 sidecar 用它识别自己的子进程，而不是只凭 200 接管端口上已有服务。管理错误经 `respondError` 分类：`StatusError` 使用自带状态，GORM 未找到为 404，包含 SQLite `UNIQUE constraint failed` 的错误为 409，其余为 500“内部错误”。

### 服务 API Key 与运行设置

`shared.APIKey` 保存命名密钥、稳定 ID、`revoked` 和软删除时间：

- 创建/重置生成 24 字节随机数的 URL-safe `sk-` 密钥；名称去除首尾空白后须为 1–80 字符。
- `/api/apikeys` 提供列表、创建、改名、重置、启禁用和删除；`/api/apikey` 及其 regenerate 是面向最早有效 Key 的旧兼容接口。
- `include_deleted=true` 的历史列表只查询 ID、名称、状态和时间，不返回密钥。
- 禁用可恢复；软删除后不再从普通列表返回，鉴权失败。重置使旧密钥失效。改名/重置/禁用使用只针对有效行的条件更新，避免并发误操作。
- 启动时为空名称的旧 Key 补“默认 Key”。只有包含软删除记录在内也完全没有 Key 时才创建默认 Key；全部禁用或删除不会因重启而自动复活。

`shared.Setting` 是字符串键值表。默认 `sync_interval=360` 分钟、`last_sync_at=0`、`log_retention_days=30`；设置接口拒绝负数，0 分别关闭自动同步和自动清理。读取缺失或非法值会回退默认值。它们与 `config.json` 的进程配置不同，存放在数据库中。

### 定时维护与进程关闭

`cron.Start` 只启动一个 goroutine，每分钟 tick：

- 每次重新读取同步间隔和上次同步时间；达到间隔后同步所有 `auto_sync=true` 且支持 API 发现的渠道。只新增模型，不删除上游消失项；同步时间写入失败只记日志。启动时不会立即执行同步，首次检查约在第一个一分钟 tick。模型发现和调用验证的区别见[资源模块](../resources/README.md)。
- 每 60 个 tick 执行一次过期清理，即启动后约一小时首次运行。`DeleteOldLogs` 按日志 `created_at`、尝试和诊断 `started_at` 分批清理；返回数量只计日志行。手动 `DELETE /api/logs?target_timestamp=...` 使用另一批大小，详见[观测模块](../observability/README.md)。修改保留天数后最多约一小时才由下一次清理采用。
- cron 不负责额度查询；`main` 另以可取消 context 启动 `quota.RunScheduler`。余额缓存和调度见[额度模块](../quota/README.md)。

收到 SIGINT/SIGTERM 后，信号 context 被取消，HTTP server 在 5 秒上限内优雅关闭，额度调度收到取消。cron goroutine 没有独立停止函数，随进程退出结束。启动失败使用 `log.Fatalf`，不会进入优雅关闭流程。

### 修改影响

- **新增配置**：同时修改 `Config`/`DefaultConfig`、`Load`/环境覆盖、桌面 sidecar 注入（若适用）与相关测试；注意工作目录与 `userData` 是不同路径来源。
- **新增实体**：决定进入主库还是日志库，并明确迁移、资源删除和过期清理边界；当前没有自动备份或跨库事务。
- **调整管理访问**：必须同时考虑 Electron `file://`、Vite 开发页面、本机 CLI 健康检查与 LAN `/v1`，不能把 CORS 或隐藏按钮当作唯一边界。
- **改变 API Key 生命周期**：同步鉴权、旧兼容接口、历史列表与日志名称快照；不能让重启重新启用禁用/删除的 Key。
- **改变启动顺序**：渠道删除预览依赖注入的额度服务，额度去重依赖主库，日志 writer 依赖日志库；初始化顺序和失败行为需一起核对。

## 阅读导航

- [核心概念](../../concepts.md)：两类凭据、管理/服务边界与观测口径。
- [资源模块](../resources/README.md)：同步、渠道事务与删除级联。
- [网关模块](../gateway/README.md)：生产 `Deps` 的实际消费与默认值语义。
- [额度模块](../quota/README.md)：独立调度和来源版本隔离。
- [观测模块](../observability/README.md)：三类日志实体、查询与清理。
- [桌面模块](../desktop/README.md)：sidecar 环境变量、数据隔离和退出生命周期。

## 依据与核验

核对 `main.go`、`backend/shared/config.go`、`db.go`、`setting.go`、`apikey.go`、`backend/api/api.go`、`local_access.go`、`apikey.go`、`setting.go`、`backend/cron/cron.go` 及 `go.mod`。另追至 `backend/gateway/gateway.go` 和 `backend/quota` 的装配消费点，确认注入接口和职责边界。

本页描述当前源码。主 Agent 已于 2026-10-28 执行 `go test ./...` 与 `go vet ./...` 并通过；未启动 Go 服务或桌面应用，未读取业务数据库、配置中的凭据，未执行备份恢复或真实上游操作。

## 待确认项

无。
