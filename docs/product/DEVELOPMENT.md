# 开发调试版与正式版隔离

桌面开发、未打包运行和打包预览统一使用 **Portunus Dev**。正式版保留原有身份、数据路径和更新行为，不需要退出即可开发。

| 项目 | 正式安装版 | 开发调试版 |
| --- | --- | --- |
| 名称 | Portunus | Portunus Dev（Dock 显示 DEV） |
| Bundle ID | com.portunus.app | com.portunus.app.dev |
| 后端端口 | 13060 | 13061 |
| macOS 数据根目录 | 沿用原有目录，当前为 `~/Library/Application Support/@portunus/web` | `~/Library/Application Support/Portunus Dev` |
| 数据库与令牌 | 沿用现有数据 | 独立 `data/`；首次启动新库、新令牌 |
| 浏览器会话 | 沿用原有路径 | `session/` |
| Electron 日志 | 沿用原有路径 | `logs/` |
| 后端工作目录 | 保留既有行为 | 独立 `backend/`，不读仓库配置 |
| 更新 | 正式发布源 | 禁用检查、下载和安装，不加载正式更新器 |
| 构建产物 | `web/dist-electron/` | 固定 `web/dist-dev/` |

单实例锁在设置独立 `userData` 后获取。热开发和打包 Dev 共用一个 Dev 身份；两种 Dev 启动方式之间需先退出当前 Dev，正式版可以一直运行。Dev 不自动读取、迁移、复制正式数据库、上游密钥、设置或缓存。需要真实渠道时，在 Dev 中单独添加测试配置。

「服务接入 → 客户端配置」读写当前用户真实的 `~/.claude/settings.json`，Dev 与正式版相同；首次写入前生成 `.portunus.bak`，Dev 界面会显示提示。开发验收可设置 `PORTUNUS_CLIENT_HOME=/tmp/portunus-client-home` 指向临时目录，避免改动真实客户端配置。

## 启动方式

在 `web/` 执行：

```bash
pnpm dev            # 编译 Go → Vite → Electron，主进程管理自己的 Dev 后端
pnpm build:dev      # 编译后端、渲染层，构建独立 Portunus Dev 目录包
pnpm preview:dev    # 启动该目录包，不安装到 /Applications
pnpm test           # 离线单元测试
pnpm test:e2e       # 仅针对 Dev 产物与 13061 的启动检查；先退出已有 Dev
```

macOS 预览路径：`web/dist-dev/mac-arm64/Portunus Dev.app`（Intel 为 `mac/`）。正式安装包仍用 `build:mac` / `build:win`，不要把开发产物拖入 `/Applications` 覆盖正式应用。

独立运行 `go run .` 是命令行后端模式，仍保留其默认 3060 与仓库 `data/`，不属于桌面 Dev；Vite 代理统一指向桌面 Dev 的 13061。

## 失败与停止边界

- 13061 已占用：拒绝启动后端，不接管已有服务，也不关闭占用进程。
- 健康检查要求实例标识匹配且自己的子进程仍存活，避免误认别的服务。
- Dev 启动失败：停在错误页，不让业务界面请求已有服务。
- 退出或切换监听范围：只停止或重启当前主进程创建的子进程，不按进程名批量结束应用。
- 不通过 `HOME`、任意数据目录覆盖参数或正式数据库副本实现隔离。
- 端到端脚本使用 Dev 数据目录（正常启动检查不写业务配置）；不会重写 HOME，也不会停止正式版。

## 持续集成与发布检查

配置提交：`337e216`。工作流见 [CI](../../.github/workflows/ci.yml) 和 [Release](../../.github/workflows/release.yml)。

- 分支 push、PR 和手动触发 CI 时，在 Ubuntu 上执行两个独立任务：`Go checks`、`Web checks`。PR 更新会取消同组旧检查。
- Go 版本取自 `go.mod`；执行 `go test ./... -count=1`、`go vet ./...`、`go build ./...`。
- 前端使用 Node 24、pnpm 10 与依赖缓存；在 `web/` 执行 `pnpm install --frozen-lockfile`、`pnpm test`、`pnpm run typecheck`、`pnpm run build:renderer`。类型检查覆盖 TS/TSX 与 JS 模块互操作，业务 JSX 尚未开启 `checkJs`。仅构建 renderer，跳过 Electron 运行时下载，不启动桌面应用。
- `v*` 标签及手动 Release 均先复用上述 CI，全部通过才进入三平台安装包构建。标签创建草稿 Release；手动 Release 只构建产物。草稿仍需人工确认发布。
- CI 不加载用户配置、正式/Dev 数据或上游密钥，不执行真实生成调用。桌面启动检查 `pnpm test:e2e`、跨设备 LAN 和真实上游验证单独进行，不能以 CI 通过代替。

CI 配置交付时，本地已通过工作流 actionlint 检查、Go 全量测试/静态检查/构建，以及临时干净目录锁文件安装后的 94 项前端测试和 renderer 构建。后续请求关联改动的验证为 101 项前端测试、全量 Go 测试、shared/api race、vet 与构建，见[请求关联验证报告](../research/2026-10-21-request-associations/README.md)。这些是各自提交时的验证结果。尚未验证 GitHub 线上首次运行，未修改远端分支保护或 ruleset。构建大 chunk 和 Node module type 提示仍为现有非阻断项。

若需要禁止失败的 PR 合并，待远端产生实际检查结果后，将 `Go checks`、`Web checks` 设为必需状态检查。新增工作流本身不会启用合并限制。

## 调用记录诊断接口

管理 `/api` 仅接受真实回环连接，并检查 Host / Origin；服务 API 令牌不授予管理权限。操作说明见[调用记录与排障](../call-records.md)。

| GET 路径 | 用途与口径 |
| --- | --- |
| `/api/logs` | 符合条件的日志行，返回 `{total,data}` |
| `/api/logs/stat` | 同一筛选条件的日志行统计与 token/缓存用量，不按请求归并 |
| `/api/logs/requests` | 服务端归并分页；任一行命中即选中关联项，摘要取该关联项全部日志 |
| `/api/logs/attempts` | `request_id` 必填且精确匹配，按 sequence、id 升序返回独立尝试 |
| `/api/logs/routing` | `request_id` 必填且精确匹配，返回独立RoutingTrace；缺记录data:null，不从旧日志回填 |

前三个接口共用筛选：`api_key_id`、`channel_id`、`group_name`、`model_name`、`request_id`（精确）、`client_protocol`、`success`、`start_time` / `end_time`（RFC3339）。分页接口接受 `page` / `page_size`，默认 1 / 20，每页最多 100；stat 不受分页影响。归并返回 `{total,data:[{key,request_id,log_count,latest_log,first_at,last_at}]}`，latest_log 按最大写入 ID 选取，first_at/last_at 是时间范围。

`shared.Log` 保存一轮 relay 日志，`shared.UpstreamAttempt` 单独保存每次显式 `http.Client.Do` 尝试，包含预算兼容重试；不重复计 token。主进程注入的 `Deps.LogWrite` / `Deps.AttemptWrite` 当前同步写入 LogDB，支持独立日志库；cron 负责同步模型与过期清理，不负责日志落库。尝试记录写入失败会告警，不因此额外调用上游。

自动保留期由 `log_retention_days` 控制（0 禁用），每小时清理。`DELETE /api/logs?target_timestamp=<Unix秒>` 使用相同截止时间清理旧日志（created_at）、尝试（started_at）和切换诊断（started_at），返回的 `deleted` 仅计旧日志行，不包含尝试与诊断行数。

RoutingTrace已提供网关路由结束摘要，但不是客户端完整接收或跨表采集完整性凭证；不把关联项数、日志行数和已采集Do数称为准确客户端请求数，也不据此推断最终成功率。

## 隔离复现与验证边界

日志详情、上游尝试与归并的历史验证分别见[详情报告](../research/2026-10-21-log-details/README.md)、[尝试报告](../research/2026-10-21-upstream-attempts/README.md)和[归并报告](../research/2026-10-21-request-associations/README.md)。本地合成上游、真实浏览器、实际 main 进程、外部真实上游与打包桌面应用是不同验证范围，不能相互替代。

复现报告中的 fixture 时，先确认其独立回环端口未被占用；使用临时合成数据，不读取或复制正式/Dev 数据，不覆盖 HOME，不接管已有端口进程。验证结束只关闭自己启动的服务。桌面 `pnpm test:e2e` 则按上文先退出已有 Dev，不停止正式版。历史报告记录的测试数量和结果保留在原提交基线上。

## 协议转换与故障转移实际验收

本轮停止新增能力，使用当前构建、独立Dev数据及Dev中已配置渠道验证普通对话、流式输出、函数工具闭环和主失败切备用。故障注入只使用临时本机渠道/分组，原Dev分组顺序不改，正式版不重启也不替换。执行结果和未覆盖范围见[实际验收记录](../research/2026-10-23-protocol-failover-acceptance/README.md)，可延期项见[集中待办](../research/2026-10-23-protocol-failover-backlog.md)。
