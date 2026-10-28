# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目简介

Portunus 是本地运行的多上游 API 聚合网关，将渠道模型组织为模型分组，统一向客户端和应用提供服务。产品定义见 `docs/product/PRODUCT.md`。

- 模块路径：`github.com/Lixiuxiu559/portunus`
- Go 版本：`1.26.6`
- 技术栈：Gin + GORM（SQLite 起步，保留换 MySQL/PostgreSQL 能力）
- 前端：Electron + React + Vite，默认进入渠道；管理 API 仅允许本机连接访问。

架构详见 `ARCHITECTURE.md`。

## 分支约定

- 日常开发、修复和重构统一在 `dev` 分支进行，修改前先确认当前分支。
- `main` 用于发布；仅在用户明确要求合并或发布时，将验证通过的 `dev` 改动合并到 `main`，更新正式版本号并创建发布标签。
- 发布完成后切回 `dev`，同步 `main` 的发布提交，后续开发继续在 `dev` 进行。详见 [开发说明](docs/product/DEVELOPMENT.md#分支工作流)。

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

## 架构

代码集中在 `backend/` 目录，按功能模块分包，依赖方向单向：

```
backend/
  channel   渠道：已保存的连接配置、凭据和更新校验
  upstream  厂商预设、配置档与连接解析
  quota     独立余额／额度来源、适配器、缓存和有界调度
  model     模型：归渠道的上游资源目录，供模型分组引用
  group     分组：跨渠道聚合模型，每个分组必配路由策略（manual/failover）
  protocol  协议转换：OpenAI Chat/Responses ↔ Anthropic ↔ Gemini
  api       管理 API（/api/*，供前端调用）
  gateway   对外 /v1 LLM 接口（Chat / Responses / Messages，供客户端和应用调用）
  cron      定时任务：同步模型、清理过期日志与尝试记录
  shared    跨模块公共：配置、数据库、APIKey、日志/尝试实体与查询
```

依赖方向以 `ARCHITECTURE.md` 为准；上游连接为 `channel → upstream → protocol`，模型发现和生成均从已保存渠道取得连接。`quota` 独立，不导入生成／路由模块，由 main 注入最小渠道读取函数。模块间用外键 ID 关联，避免包级循环依赖。

一次调用的数据流：

```
客户端(Chat / Responses / Messages) → /v1 → APIKey 鉴权 → 命中分组 → 按策略选模型项
  → 需要则转协议 → 上游调用 → 转回 → 返回 → 经注入 writer 同步记录 relay 日志与独立 HTTP 尝试，汇总 token 与缓存用量
```

桌面局域网模式保留 `/v1` 令牌鉴权服务；管理 `/api` 始终限制真实回环对端，并检查 Host / Origin。不得通过公开反向代理暴露管理接口。模型同步只新增发现的模型；日志记录数与客户端请求数需区分。产品不提供价格配置、默认价格套用或调用费用计算；保留 token、缓存用量和调用诊断。历史数据库 price/currency/cost 列及旧值保留，不删除或清零，也不再进入活跃管理接口与统计聚合。真实调用仍可能由上游收费。

## 桌面开发隔离

桌面本地开发与预览统一使用 Portunus Dev（端口 13061、Bundle ID `com.portunus.app.dev`）。macOS 独立数据目录为 `~/Library/Application Support/Portunus Dev`；不迁移或复制正式配置。`cd web && pnpm dev` 自动编译并管理后端；`pnpm build:dev && pnpm preview:dev` 构建独立预览。不覆盖 `/Applications/Portunus.app`，不停止正式版，不使用 `killall`/`pkill Portunus`。正式发布仍用原有 `build:mac` / `build:win`。详细边界见 `docs/product/DEVELOPMENT.md`。

## 调用观测与统计口径

- `shared.Log` 保持旧 relay 日志与 token 聚合；`shared.UpstreamAttempt` 单独保存显式 `http.Client.Do` 尝试，不重复记 token，也不混入旧记录数。写入走 `Deps.LogWrite` / `Deps.AttemptWrite`，生产默认同步落库。
- `/api/logs/requests` 是日志关联归并，非空请求 ID 合并，缺 ID 的历史行独立。筛选先命中关联项，摘要再取该请求全部日志；后端分组分页，不在前端合并当前页。
- latest_log 按写入 ID 选取，最近时间、渠道与结果必须来自同一行；不能据此推断请求最终结果。连续序号不能证明采集完整，空尝试不能证明调用数为 0。
- 未引入请求终态/完整性凭证前，不将关联项数、旧日志数或显式 Do 数标为准确客户端请求数、最终成功率或物理上游发送数。
- 当前界面与排障口径见[调用记录说明](docs/call-records.md)，历史验证结论仅在其原基线和范围内有效。


## 上游、余额与界面维护

- 新增／编辑渠道保存仅事务导入所选模型，不隐式全量同步；模型发现、配置解析与主动生成验证含义分开。见[上游接入](docs/upstream-channels.md)。
- 余额 GET 只读缓存；查询由明确 POST 或已启用的后台调度触发；每个渠道只有一个查询来源（渠道即一个上游账户），重复创建返回 409。正常成功旧值保留真实查询时间；失败窗口和身份失效分开，不把闲置成功值叫账户过期。详情见[余额／额度说明](docs/channel-quota.md)。
- 新厂商通过 `quota` 适配器注册表扩展；页面不解析厂商响应，不计算金额。自定义查询是结构化 GET/POST 与 JSON Pointer，不执行脚本；Sub2API Key 和厂商套餐专属适配已取消。
