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
| API Key | `shared.APIKey` | 对外 /v1 接口的鉴权凭据，也用于日志/费用归属。 |
| 调用日志 Log | `shared.Log` | 一次调用的记录（分组/渠道/模型/状态/token/费用/耗时）。 |

依赖方向：`api` / `gateway` / `cron` → `router` / `channel` / `model` / `group` / `protocol` → `shared`。
