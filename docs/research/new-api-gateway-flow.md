# new-api 对外统一调用服务的完整链路研究（gateway 参考）

> 研究时间：2026-08-25。对象：`/Users/lixiuxiu/development_tool/projects/new-API`（QuantumNous/new-api）。
> 目的：明确 portunus `backend/gateway` 应实现的功能。本文聚焦「gateway 层」的对外暴露、鉴权、选渠道、计费日志；**协议转换的细节已在 `new-api-format-conversion.md` 深入覆盖**，此处只补链路位置，不重复展开。

---

## 1. 一句话结论

new-api 的 gateway 是一条 **「路径→RelayFormat→统一编排」的单入口流水线**：所有协议路径（OpenAI Chat / Responses / Anthropic / Gemini）都汇聚到同一个 `controller.Relay`，由三段中间件按序完成 **① TokenAuth 鉴权 → ② Distribute 选渠道**，Relay 内再做 **③ 重试循环（选渠道/调用/转换/计费）**。选渠道用「分组+模型 → 渠道列表（按 priority 降序，同优先级按 weight 加权随机）」的内存索引，失败重试 = 按 retry 次数逐级降 priority 再随机。

```
router(路径→RelayFormat)
  → middleware.TokenAuth()     鉴权：token → 用户/分组/模型白名单/配额
  → middleware.Distribute()    选渠道：model 名 → 命中(分组,渠道)，写入 context
  → controller.Relay()         编排：解析请求 → 预扣费 → 重试循环
       └─ getChannel() 每次重试重选渠道
       └─ relay.TextHelper() → Adaptor 转换/调用/响应 → usage
       └─ PostTextConsumeQuota() 结算 + 写消费日志
```

---

## 2. 对外暴露的协议与路由（router/relay-router.go）

入口在 `router/relay-router.go`。每个 `/v1` 路径硬编码一个 `RelayFormat`，`RelayFormat`（`types/relay_format.go:5`）决定「客户端想要什么格式」，与上游渠道类型完全独立：

| 路径 | RelayFormat | 处理 |
|---|---|---|
| `POST /v1/chat/completions`、`POST /v1/completions` | `openai` | `controller.Relay`（relay-router.go:96） |
| `POST /v1/responses`、`POST /v1/responses/compact` | `openai_responses` | relay-router.go:101 |
| `POST /v1/messages` | `claude` | relay-router.go:88 |
| `POST /v1beta/models/*path`（gemini `:generateContent` 等） | `gemini` | relay-router.go:197 |
| `GET /v1/models`、`GET /v1beta/models`、`GET /v1beta/openai/models` | 按 header 判断格式 | relay-router.go:19-60 |
| `GET/POST /v1/files`、`/v1/fine-tunes` 等 | — | `RelayNotImplemented` 占位（relay-router.go:154-165） |
| 额外：`/v1/embeddings`、`/v1/audio/*`、`/v1/images/*`、`/v1/realtime`(WS)、`/mj/*`、`/suno/*` | 各自格式 | 同路由组 |

要点：
- **`Relay` 第一个参数 `relayFormat` 由路径静态决定**，不是从请求体推断——每个路径写死一个格式（relay-router.go:88-103）。
- Claude Code 走的是 `POST /v1/messages`（`RelayFormatClaude`）；codex/OpenAI 客户端走 `/v1/chat/completions` 或 `/v1/responses`。
- Gemini 的模型名从路径里剥：`extractModelNameFromGeminiPath`（middleware/distributor.go:412）从 `/v1beta/models/gemini-2.0-flash:generateContent` 提取 `gemini-2.0-flash`，因为 gemini 请求体没有 `model` 字段。
- `/v1/models` 的返回格式按请求 header 区分（`x-api-key`+`anthropic-version`→anthropic 格式，`x-goog-api-key`→gemini，否则 openai），因为三个客户端各自解析格式不同（relay-router.go:23-31）。

中间件链（relay-router.go:69-73）：`RouteTag → SystemPerformanceCheck → TokenAuth → ModelRequestRateLimit → Distribute`。

---

## 3. 鉴权链路：TokenAuth（middleware/auth.go:276）

portunus 现在的 `authMiddleware` 只查 `shared.APIKey` 是否 enabled（gateway.go:38），new-api 要复杂得多。完整链路：

1. **Key 归一**（auth.go:314-331）：兼容四种携带方式，统一转成 `Authorization: Bearer <key>`：
   - WS：从 `Sec-WebSocket-Protocol` 里读 `openai-insecure-api-key.<key>`（auth.go:279-292）；
   - Anthropic：`/v1/messages`、`/v1/models` 用 `x-api-key`（auth.go:294-299）；
   - Gemini：`/v1beta/models` 路径用 query `key` 或 `x-goog-api-key`（auth.go:301-313）；
   - OpenAI：`Authorization: Bearer`。
   → 即**所有客户端协议最终都变成同一个 Bearer 头**，后续共用一条校验。
2. **Token 查库 + 校验**：`model.ValidateUserToken(key)`（auth.go:332），key 剥掉 `sk-` 前缀、取 `-` 分隔第一段（auth.go:328-330，即支持 `sk-短key-渠道id` 的形态，渠道 id 用于管理员指定渠道）。
3. **IP 白名单**（auth.go:351-365）：token 可配置允许访问的 CIDR。
4. **用户态校验**：`GetUserCache` 查用户是否被封禁，`WriteContext` 把用户分组等写进 context（auth.go:367-380）。
5. **分组校验**：token 上的 `Group` 字段（`token.Group`）非空时，校验该分组存在且用户可用，然后**用 token 分组覆盖用户分组**（auth.go:382-398），写 `ContextKeyUsingGroup`。
6. **`SetupContextForToken`**（auth.go:409-439）：把 `token_id / token_key / token_name / token_quota(剩余额度) / token_model_limit(模型白名单) / token_group / cross_group_retry` 全写进 gin.Context；`sk-xxx-渠道id` 形态下管理员可指定固定渠道。

**Token 上绑定的东西**（对照问题 2）：用户（`token.UserId`）、分组（`token.Group`）、可用模型白名单（`token.GetModelLimitsMap()`，auth.go:421-425，在 Distribute 里拦截）、配额（`token.RemainQuota`）、IP 白名单。

> portunus 是单用户、无多用户/配额系统，这一层可大幅简化，但**「Key 归一」和「模型白名单」思路值得抄**：把四种协议的 key 携带方式统一成一种，避免鉴权逻辑分散。

---

## 4. 命中分组 → 选渠道 / 负载均衡 / 重试 / failover

### 4.1 分组与渠道的建模（new-api 侧）

new-api 没有独立的「分组」表，而是**渠道上冗余两个逗号分隔字段**：`channel.Group`（该渠道属于哪些分组）和 `channel.Models`（该渠道支持哪些模型）。启动/定时把这张关系全量灌进内存索引（model/channel_cache.go:21 `InitChannelCache`）：

```
group2model2channels: map[分组]map[模型][]渠道ID   // 渠道按 priority 降序排好（channel_cache.go:60）
```

portunus 的建模更干净：`group.Group`（对外模型名）+ `group.GroupItem`（跨渠道聚合，`ModelID` 引用 `model.Model`，带 `Priority`）。差异是 portunus 的「分组项」是 `(group_id, model_id)` 而非 `(group, channel_id)`——portunus 的**一个 group 对应一个模型名**，new-api 一个 group 可含多个模型名。两者都能落到「模型名 → 渠道列表」，portunus 需要自己建这个索引（见 §7）。

### 4.2 选渠道算法：GetRandomSatisfiedChannel（model/channel_cache.go:96）

核心分三步：
1. 按 `(group, model)` 精确查渠道列表；查不到再按归一化模型名（`gpt-4`↔`gpt-4-*` 通配，channel_cache.go:110）查；
2. 列表为空 → 无可用渠道；只剩一个 → 直接返回；
3. 多个 → **按 priority 分层**：`retry` 参数决定取第几层（`sortedUniquePriorities[retry]`，channel_cache.go:142），层内按 `weight` 加权随机（channel_cache.go:176-188），带平滑处理（权重相差悬殊时拉平，channel_cache.go:163-174）。

> 这就是 new-api 的「负载均衡 + failover」：**重试次数 = 降一个 priority 层级**。第 0 次请求打最高 priority 层（层内加权随机），失败重试打到下一层，直到层用尽。portunus 的 `round_robin`（同层轮询）和 `failover`（按 priority 升序）可以照这个「按 retry 索引选层」的框架落地；`manual` 只打 `ActiveItemID`。

### 4.3 分组命中（Distribute，middleware/distributor.go:30）

`Distribute` 是选渠道中间件，在 `TokenAuth` 之后、`Relay` 之前：
1. `getModelRequest`（distributor.go:181）按路径从请求体/URL 提取 `model` 名（openai 从 body，gemini 从路径，图片/音频有默认名兜底）；
2. 模型白名单拦截：token 配了模型白名单且当前 model 不在内 → 403（distributor.go:57-75）；
3. **亲和性**：`GetPreferredChannelByAffinity`（distributor.go:102）——同一请求内若已选中过渠道且仍满足条件，继续用它（流式场景避免中途换渠道）；`RecordChannelAffinity` 在响应成功后记录（distributor.go:161-163）；
4. 否则 `service.CacheGetRandomSatisfiedChannel` 随机选（distributor.go:130）；
5. `SetupContextForSelectedChannel`（distributor.go:345）把渠道的 `base_url / key / type / param_override / header_override / model_mapping / status_code_mapping` 等全写进 context，并 `GetNextEnabledKey()` 取一个可用 key（多 key 轮询）。

### 4.4 重试循环（controller/relay.go:189-235）

`controller.Relay` 里 `for retry <= RetryTimes`：
- 每次循环 `getChannel()`（relay.go:286）——注意**不是复用 Distribute 的结果**，而是用 `RetryParam{retry}` 重新调 `CacheGetRandomSatisfiedChannel`，所以第 n 次重试会选到第 n 层 priority；
- 调对应格式的 helper（`TextHelper` / `ClaudeHelper` / `GeminiHelper` / `ResponsesHelper`…，relay.go:211-220）；
- 失败 → `shouldRetry`（relay.go:318）判断是否值得重试（渠道错误/5xx 可重试，4xx/特定 code 不可重试）；
- 失败超阈值 → `processChannelError`（relay.go:350）异步 `gopool.Go` 自动禁用渠道（`ShouldDisableChannel` + `DisableChannel`，relay.go:354-358），并记错误日志。

### 4.5 `auto` 分组（service/channel_select.go:83）

token 分组为 `auto` 时，按用户可用分组列表逐组尝试：当前组 priority 层用尽 → 切下一个组（channel_select.go:106-154），即跨分组 failover。portunus 无多用户分组概念，可跳过。

---

## 5. 协议转换在链路中的位置（衔接已有研究）

`new-api-format-conversion.md` 已详述 Adaptor 机制。这里只标它在 gateway 流水线里的位置：

```
relay.TextHelper (relay/compatible_handler.go:26)
  ├─ info.InitChannelMeta(c)              渠道类型 → APIType → GetAdaptor()
  ├─ adaptor.ConvertOpenAIRequest()       统一请求 → 上游原生
  ├─ adaptor.DoRequest()                  发上游（GetRequestURL + SetupRequestHeader + http.Do）
  ├─ adaptor.DoResponse()                 上游原生 → 统一响应（流式/非流式二选一）→ 写回客户端
  └─ service.PostTextConsumeQuota()       usage → 结算 + 写日志
```

三条格式维度全程独立（详见既有文档 §5）：
- `RelayFormat`（客户端想要的格式）由路径决定；
- `ChannelType`（上游原生格式）由选中渠道决定；
- `RelayMode`（接口类型：chat/embedding/image…）由 URL 推导。

---

## 6. 计费与异步日志落库

### 6.1 结算：PostTextConsumeQuota（service/text_quota.go:320）

在响应写回客户端**之后**同步执行（compatible_handler.go:91、214）：
1. 由 `usage`（`dto.Usage`，上游语义已归一）算费用摘要 `summary`：模型价 × tokens（输入/输出/缓存）再乘分组倍率（text_quota.go:330 `calculateTextQuotaSummary`）；
2. 扣用户额度、渠道用量（text_quota.go:367-368）；
3. `SettleBilling` 结算（text_quota.go:371）；
4. 写消费日志（text_quota.go:460 → `model.RecordConsumeLog`，model/log.go:204）。

日志**不是**独立后台线程，而是请求 goroutine 内、流式返回完成后的同步 DB 写入（`LOG_DB.Create(log)`，model/log.go:244）。真正的异步只在：额度告警通知（`gopool.Go`，quota.go:451）、数据导出（log.go:249）、失败自动禁用渠道（relay.go:355）。

> 对 portunus 的启示：不必为「异步日志」单独起 goroutine/队列，**流式返回后同步写 `shared.Log` 即可**（一次 DB insert 开销可接受）；cron 包保留做模型同步和日志归档这类周期性任务即可。portunus 的 `shared.Log` 已有 `InputToken/OutputToken/Cost/DurationMs` 字段，正好承接。

### 6.2 预扣费/退款（controller/relay.go:160-178）

请求前按估算 tokens 预扣额度（`PreConsumeBilling`），失败（`newAPIError != nil`）则 `Refund` 退回。portunus 无用户额度体系，可跳过；但**「估算 tokens 用于日志/计费」的思路保留**（`EstimateRequestToken`，relay.go:144）。

---

## 7. portunus gateway 实现清单（按优先级）

对照 §1-§6，映射到 portunus `backend/gateway`（及协作包）的具体功能点。标注「已有 / 缺口」。

### P0 — gateway 层骨架（必须）

| # | 功能点 | new-api 出处 | portunus 状态 |
|---|---|---|---|
| 1 | **多协议路由**：`POST /v1/chat/completions`、`POST /v1/responses`、`POST /v1/messages`、`POST /v1beta/models/*path`(gemini)，各带独立 RelayFormat 常量 | relay-router.go:69-200 | **缺口**：gateway.go:14 只有 `/models`，其余 3 个 POST 是 `relayNotImplemented` 占位 |
| 2 | **Key 携带方式归一**：Bearer / `x-api-key` / `x-goog-api-key` / query `key`（gemini）→ 统一取 key 校验 | auth.go:294-331 | **部分**：gateway.go:27 只认 Bearer + `x-api-key`，缺 gemini 两种 |
| 3 | **APIKey 校验后注入上下文**（现 `api_key_id`）+ 扩展 token 绑定信息（模型白名单可选） | auth.go:409-439 | **已有**：gateway.go:43 `c.Set("api_key_id", ...)` |
| 4 | **Relay 统一编排函数**：解析请求 → 预扣/估算 → 重试循环 → 各格式 helper 分发 | controller/relay.go:67 | **缺口**：需新增 |
| 5 | **错误响应按协议格式化**（OpenAI 的 `{"error":{...}}` vs Anthropic 的 `{"type":"error",...}`） | relay.go:88-105 | **缺口** |
| 6 | **`/v1/models` 按协议返回**（`anthropic-version` header / `x-goog-api-key` 区分格式） | relay-router.go:23-41 | **部分**：gateway.go:57 固定 OpenAI 格式 |

### P1 — 核心 relay 业务（第二优先）

| # | 功能点 | new-api 出处 | portunus 状态 |
|---|---|---|---|
| 7 | **按模型名选中分组**（请求 `model` → 匹配 `group.Name`，否则 404/503） | distributor.go:130 | **缺口**：`group` 包只有 CRUD，无「model 名→group」解析 |
| 8 | **按策略选模型项**：manual / round_robin / failover 三策略产出一个 `(channel, model)` | channel_cache.go:96（按 retry 降 priority + 加权随机） | **缺口**：`group.Group.Strategy` 已定义，但无 Pick 逻辑 |
| 9 | **发起上游调用**：拼 URL / header（BaseURL+key）、http 请求、读响应 | channel 包无对应 | **缺口**：`channel.Channel` 只有配置字段，无 DoRequest |
| 10 | **响应转回客户端格式 + 提取 usage** | 已有 research 文档 §7 | **缺口**：`protocol.Converter` 接口已定义未实现 |
| 11 | **流式 SSE 转换**（scanner goroutine + dataChan + handler） | 已有 research 文档 §7.2 | **缺口** |
| 12 | **计费落库**：usage × model 价格 → 写 `shared.Log`（InputToken/OutputToken/Cost） | text_quota.go:320、log.go:204 | **缺口**：`shared.Log` 已建，无写入逻辑 |
| 13 | **failover 重试循环**：失败换下一 priority 层；manual 只打激活项 | relay.go:189、channel_cache.go:139 | **缺口** |

### P2 — 增强（可后置）

| # | 功能点 | new-api 出处 | portunus 状态 |
|---|---|---|---|
| 14 | **内存渠道索引**：`group→model→[channel]`（按 priority 排序）+ 定时刷新 | channel_cache.go:21、SyncChannelCache:88 | **缺口**：可由 group/channel 查询现造，规模上来再缓存 |
| 15 | **失败自动禁用渠道**（重试耗尽后 gopool 异步禁用） | relay.go:350-358 | **缺口** |
| 16 | 多 key 轮询、模型名映射、参数/头覆盖 | distributor.go:370-383 | **缺口**（非必需） |
| 17 | 渠道亲和性（同请求流式不换渠道） | distributor.go:102 | **缺口**（非必需） |

### 依赖方向（需保持） 

gateway 新增逻辑应只调用 `group` / `channel` / `model` / `protocol` / `shared`，不反向依赖：
- 「选 group / 选 item」逻辑放 `group` 包新增 service（或 gateway 内私有函数，因依赖 `channel`/`model` 查询——按架构约定放 gateway，避免 group 反向依赖 model/channel）；
- 「发起上游请求 + 拼 URL」逻辑放 `channel` 包（新增 `DoRequest` 等），gateway 只管编排；
- 计费取价依赖 `model.Model` 的价格字段（InputPrice/OutputPrice/CacheReadPrice/CacheWritePrice）。

---

## 8. 一句话给实现的建议

抄 new-api 三个「便宜的套路」即可跑通 gateway：**① 每条路径写死一个 RelayFormat，全走同一个 Relay 编排；② 选渠道 = 按 retry 次数降 priority 层、层内加权随机；③ 流式返回后同步写 usage 计费日志**。多用户/额度/多 key/自动禁用等新-api 重功能按需裁剪，不必照搬。
