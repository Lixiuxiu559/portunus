# 领域词汇表（CONTEXT）

Portunus 的领域语言，给架构审视与后续设计提供命名。Go 类型名是规范标识符，中文是对应称呼。

| 词条 | 类型 / 位置 | 含义 |
|---|---|---|
| 渠道 Channel | `channel.Channel` | 连接一个上游供应商的配置单元（type / base_url / key）。协议类型 ∈ openai / openai_responses / anthropic / gemini。1:N 模型。 |
| 模型 Model | `model.Model` | 从渠道拉回、归属某渠道的可用模型，是「原料库」。价格是模型属性（input / output / cache_read / cache_write，每 1M token）。 |
| 分组 Group | `group.Group` | 对外暴露的模型名，跨渠道聚合模型。每个分组必配一个路由策略。 |
| 路由策略 Strategy | `group.Strategy` | manual（打激活项）/ round_robin（组内轮流）/ failover（按 priority 顺序失败换下一个）。 |
| 路由解析 router | `router.Resolve` | 按分组策略决定「实际该调用哪个/哪些上游」，返回 `router.Target`（模型 + 渠道）。策略语义与 ModelID→Model→Channel 解析都归它。 |
| 协议 Provider | `protocol.Provider` | openai / openai_responses / anthropic / gemini。协议转换以 OpenAI Chat Completions 为内部规范格式。 |
| 上游接入器 Upstream | `protocol.Upstream` | 拿协议 + base_url + key 产出上游端点 / 鉴权头 / 模型集（ChatURL / ChatHeaders / FetchModels）。上游装配知识收敛于此，gateway 与 model 复用。 |
| 用量归一 usage | `protocol.Usage` / `usageFromXxx` | 各协议原始用量 → canonical Usage 的唯一数学（PromptTokens 为非缓存输入，缓存读 / 写单列）。收敛于 `protocol/usage.go`；流式直通提取注册为 providerImpls 的 `newUsageExtractor` 能力项，新增协议漏实现即编译失败。 |
| 上游请求装配 ComposeUpstreamRequest | `protocol.ComposeUpstreamRequest` | 把客户端请求体装配为发往指定上游的请求体：协议转换 + 模型名替换 + thinking 注入。模型名落哪个字段（body 或 URL）、thinking 哪个上游消费，是协议知识，归 protocol；gateway 只调此入口，不再自行改写请求体。 |
| 流式帧器 StreamFramer | `protocol.StreamFramer` | SSE 传输帧的唯一权威：解帧（data: 行 / [DONE]）、装帧（anthropic 补 event: 行、openai 系 [DONE] 收尾）、流内 error 事件形状。传输编码与语义映射分层，StreamConverter 只管纯 JSON 载荷。 |
| 块骨架 blockSink | `protocol.fromOpenAISkeleton` | 「OpenAI chunk → 目标协议流」的共享块生命周期骨架（单块互斥 / 并行工具归并 / 纯 role 帧跳过 / usage 捕获），各协议实现 blockSink 回调。新协议不再重写映射循环，静默丢内容类 bug 失去生根的土壤。 |
| 失败处置 failDecision | `gateway.failSpec` | 一次上游尝试失败的统一判定：错误值 → 处置决定（是否重试 / 两档假死 / 熔断喂法 / 归因 errKind / 客户端状态码与错误类别）。失败语义唯一权威，收敛于 `gateway/fail.go` 一张查表；重试循环、熔断 defer、状态码裁决、日志归因都读它。 |
| API Key | `shared.APIKey` | 对外 /v1 接口的鉴权凭据，也用于日志/费用归属。 |
| 调用日志 Log | `shared.Log` | 一次调用的记录（分组/渠道/模型/状态/token/费用/耗时）。实体与失败归因词表（ErrKind 取值）归 shared；错误→类别的判定归 gateway.failSpec；写入经 gateway Deps.LogWrite 注入；查询/统计/清理在 shared（log_query.go，管理端消费），保留期由 cron 每小时自动清理（log_retention_days 可配，0=禁用）。 |

依赖方向：`api` / `gateway` / `cron` → `router` / `channel` / `model` / `group` / `protocol` → `shared`。
