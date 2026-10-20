# 领域词汇表（CONTEXT）

Portunus 的领域语言，给架构审视与后续设计提供命名。Go 类型名是规范标识符，中文是对应称呼。

| 词条 | 类型 / 位置 | 含义 |
|---|---|---|
| 渠道 Channel | `channel.Channel` | 连接一个上游供应商的配置单元（type / base_url / key）。协议类型 ∈ openai / openai_responses / anthropic / gemini。1:N 模型。 |
| 模型 Model | `model.Model` | 归属某渠道的上游模型资源，支持发现与手动添加，供分组引用。产品不配置价格；历史价格列仅保留存储兼容，不进入活跃接口。 |
| 分组 Group | `group.Group` | 对外暴露的模型名，跨渠道聚合模型。每个分组必配一个路由策略。 |
| 路由策略 Strategy | `group.Strategy` | manual（打激活项）/ round_robin（组内轮流）/ failover（按 priority 顺序失败换下一个）。 |
| 路由解析 router | `router.Resolve` | 按分组策略决定「实际该调用哪个/哪些上游」，返回 `router.Target`（模型 + 渠道）。策略语义与 ModelID→Model→Channel 解析都归它。 |
| 协议 Provider | `protocol.Provider` | openai / openai_responses / anthropic / gemini。协议转换以 OpenAI Chat Completions 为内部规范格式。 |
| 上游接入器 Upstream | `protocol.Upstream` | 拿协议 + base_url + key 产出上游端点 / 鉴权头 / 模型集（ChatURL / ChatHeaders / FetchModels）。上游装配知识收敛于此，gateway 与 model 复用。 |
| 用量归一 usage | `protocol.Usage` / `usageFromXxx` | 各协议原始用量 → canonical Usage 的唯一数学（PromptTokens 为非缓存输入，缓存读 / 写单列）。收敛于 `protocol/usage.go`；流式直通提取注册为 providerImpls 的 `newUsageExtractor` 能力项，新增协议漏实现即编译失败。 |
| 上游请求装配 ComposeUpstreamRequest | `protocol.ComposeUpstreamRequest` | 把客户端请求体装配为发往指定上游的请求体：协议转换 + 模型名替换 + thinking 注入。模型名落哪个字段（body 或 URL）、thinking 哪个上游消费，是协议知识，归 protocol；gateway 只调此入口，不再自行改写请求体。 |
| 流式帧器 StreamFramer | `protocol.StreamFramer` | SSE 传输帧的唯一权威：解帧（data: 行 / [DONE]）、装帧（anthropic 补 event: 行、openai 系 [DONE] 收尾）、流内 error 事件形状。传输编码与语义映射分层，StreamConverter 只管纯 JSON 载荷。 |
| 块骨架 blockSink | `protocol.fromOpenAISkeleton` | 「OpenAI chunk → 目标协议流」的共享块生命周期骨架（单块互斥 / 并行工具归并 / 纯 role 帧跳过 / usage 捕获），各协议实现 blockSink 回调。新协议不再重写映射循环，静默丢内容类 bug 失去生根的土壤。 |
| 托管工具 hosted tool | `protocol.isHostedToolType` / `Tool.Type == "web_search"` | 上游服务端执行的工具声明（如 Claude Code 的 WebSearch）。canonical 以统一语义名 `web_search` 承载（非原字面直通），各协议出入口做语义↔字面映射（anthropic ↔ `web_search_<date>`、gemini ↔ `googleSearch`、responses ↔ `web_search`），出站字面化到基线版本；发往 openai 上游前剥离并计入 `ComposeStats.HostedToolsStripped`（诚实降级，不发空壳 function）。历史块 `server_tool_use` / `web_search_tool_result` 与 tool_use / tool_result 同构转换。归一理由与设计考证见 `docs/research/2026-09-29-hosted-websearch-bridging.md`。 |
| 接地数据 grounding | `protocol.GroundingMetadata` | 上游托管搜索的执行证据（Gemini 的 groundingMetadata），canonical 中转载体（`ChatMessage` / `ChunkDelta` 的 `grounding_metadata`，流式两段链间传递、不出网）。anthropic 出站据此合成 `server_tool_use` + `web_search_tool_result` 块（`webSearchToolBlocks` 单一形状源）；流式晚到时（Gemini 真实流的 grounding 随末 chunk）关块-插入-重开，`Finish` 兜底。 |
| 能力覆盖矩阵 capability matrix | `protocol.providerImpls` × 期望登记表 | 每协议 × 6 位置（请求/响应 × 入向/出向 × 流式与否）对能力的处置声明：keep / drop / skip（带理由）。**按能力维度分文件登记**：思考内容 → `capability_matrix_test.go`，托管工具 → `hosted_tool_matrix_test.go`（入站探针 × 出站目标全积，openai 出站为 drop 格带理由）。新协议接入必须在登记表表态，矩阵测试穷尽 providerImpls 全 key，未登记即红。skip 的理由引用官方回传语义考证（`docs/research/2026-09-29-capability-matrix-design.md` §1.2）。 |
| 失败处置 failDecision | `gateway.failSpec` | 一次上游尝试失败的统一判定：错误值 → 处置决定（是否重试 / 两档假死 / 熔断喂法 / 归因 errKind / 客户端状态码与错误类别）。失败语义唯一权威，收敛于 `gateway/fail.go` 一张查表；重试循环、熔断 defer、状态码裁决、日志归因都读它。 |
| API Key | `shared.APIKey` | 对外 /v1 接口的鉴权凭据，日志记录其 ID；不授予管理 /api 权限，不用于费用计算。 |
| 调用日志 Log | `shared.Log` | 一轮 relay 处理的持久化记录，包含分组/渠道/模型/状态/token/耗时。普通重试、切换目标可产生多行；预算兼容重试在独立尝试表采集，不向旧 Log 增加行。ErrKind 词表归 shared，判定归 gateway.failSpec；写入经 Deps.LogWrite 注入，查询/统计/清理在 log_query.go。不计算费用，历史费用列只保留存储兼容。 |
| 上游尝试 UpstreamAttempt | `shared.UpstreamAttempt` | 一次显式 http.Client.Do 的处理记录；含连接失败与取消，不等于物理发送或上游收到请求的证明。按 request_id + sequence 关联，relay_attempt 标识外层处理轮次；reason 区分 initial/retry/failover/budget_compat。由 Deps.AttemptWrite 独立写入日志库，不重复记 token。 |
| 请求关联项 LogRequest | `shared.LogRequest` / `ListLogRequests` | 按非空 request_id 关联持久化日志；空/NULL ID 每行独立。筛选命中任一行即可选中，摘要包含整个关联项；latest_log 为最大写入 ID，不代表最终客户端结果。关联项数不是准确客户端请求数。 |
| 采集完整性 | `requestAttempts.js` / 尝试链提示 | 序号缺口提示可能不完整；连续只能证明当前页连续，空记录不能证明实际调用为零。当前没有请求终态和完整性凭证，不推断最终成功率。 |
| 日志留存 | `shared.DeleteOldLogs` / `cron.cleanupTick` | log_retention_days 控制保留期（0 禁用），每小时清理；旧 Log 按 created_at、尝试按 started_at 删除，API deleted 仍仅为旧日志行数。 |
| 后端守护 sidecar | `web/src/main/sidecar.js` | Electron 主进程内、生命周期受控的 Go 后端。状态机 starting → ready → failed → stopped，`ready` 恒 settle（失败也 resolve、绝不 reject，失败经 `state='failed'` + `error` 表达）。electron-free：环境（binPath/dataDir/port/host）经 `createBackend(deps)` 注入，spawn/ping/portFree 三个 adapter 生产走内置默认、测试覆盖。 |
| 服务地址 server-address | `web/src/main/server-address.js` | 后端地址唯一来源（纯模块）：{host 监听, port, url 内部回环, baseURL}。host 随 networkMode（lan→0.0.0.0 / local→127.0.0.1），url 恒 127.0.0.1 与 host 解耦；port 按模式固定（Dev 13061 / 正式 13060）。 |

依赖方向：`api` / `gateway` / `cron` → `router` / `channel` / `model` / `group` / `protocol` → `shared`。
