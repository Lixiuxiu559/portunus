# cc-switch 上游请求失败后的重试行为研究

> 研究时间：2026-08-27。对象：`/Users/lixiuxiu/development_tool/projects/cc-switch/`（cc-switch，一个多供应商 Claude Code / Codex / Gemini 代理切换器）。
> 目的：为 portunus 网关设计「上游失败重试 / 故障转移」策略提供参考，重点厘清 cc-switch 代理一次上游 LLM 请求失败后是否重试、在哪一层重试、触发条件与失败后如何响应客户端。
> 源码范围：后端转发核心 `src-tauri/src/proxy/`（Rust + axum/hyper/reqwest），前端 `src/`（React + TanStack Query）。

---

## 1. 一句话结论

cc-switch 的「重试」本质是**换下一个 Provider 的故障转移（failover），不是对同一 Provider 的参数化重试**。核心循环 `forward_with_retry_inner` 在 provider 列表上 `for` 循环，失败且「可重试」时直接 `continue` 到下一家；**没有退避算法、没有 sleep/jitter、不读 Retry-After 头**。真正对**同一 Provider 二次请求**的只有三类「整流器（rectifier）」场景（media 降级 / thinking 签名整流 / budget 整流），每 Provider 至多重试 1 次，且仅 Claude/ClaudeAuth 供应商。顶层的 `max_retries` 实际是「最多尝试几家」的上限（`max_attempts = max_retries + 1`），默认 3 → 最多 4 家，并天然受队列 provider 数限制。是否重试**不看 HTTP 方法（幂等性）只看错误类型**；**流式与非流式都会 failover，但 failover 的「安全窗口」不同**：非流式可在读完整 body 前随时换家，流式只在收到首个 chunk 之前能换家，首包一落地响应即提交给客户端、不可再换。全部 provider 失败后，向上游错误透传的响应是**最后一个 provider 的错误**（非聚合），状态码/JSON 体按其映射规则返回。

---

## 2. 重试/故障转移的主循环（Rust proxy 层）

### 2.1 入口与循环结构

每一次客户端请求 → `handlers.rs` 调用 `forwarder.forward_with_retry(...)`（`handlers.rs:205`）→ `forwarder.rs:370` 的 `forward_with_retry`（thin wrapper，记 `total_requests`/`active_connections`）→ `forward_with_retry_inner`（`forwarder.rs:410`）真正执行。

`forward_with_retry_inner` 的核心是一个线性 `for` 循环：

```rust
// forwarder.rs:444
// 依次尝试每个供应商
for provider in providers.iter() {
    ...
    match self.forward(...).await {
        Ok(...) => { ...; return Ok(...); }
        Err(e) => {
            // ...整流器重试（仅 Claude）...
            let category = self.categorize_proxy_error(&e, provider);
            match category {
                ErrorCategory::Retryable => {
                    // forwarder.rs:1067 继续尝试下一个供应商
                    last_error = Some(e);
                    last_provider = Some(provider.clone());
                    continue;
                }
                ErrorCategory::NonRetryable | ErrorCategory::ClientAbort => {
                    // forwarder.rs:1089 直接返回给客户端
                    return Err(ForwardError { error: e, provider: Some(provider.clone()) });
                }
            }
        }
    }
}
```

`providers` 列表由 `ProviderRouter::select_providers`（`provider_router.rs:45`）在 `RequestContext::new` 时**一次性选定**（`handler_context.rs:134-144`），循环只消费这个列表，不会再动态选。

### 2.2 尝试次数上限：`max_retries` 的语义

`AppProxyConfig.max_retries` 的 UI 文案是「请求失败时的重试次数（0 ~ 10）」（`src/i18n/locales/zh.json:2825`，输入范围 `src/components/proxy/AutoFailoverConfigPanel.tsx:69` `{ min: 0, max: 10 }`），默认值 3（SQLite `DEFAULT 3`，`src-tauri/src/database/schema.rs:131`；Rust 侧 `types.rs:47` `max_retries: 3`）。

它被翻译成「最多尝试几家」，而不是「重发同一家几次」：

```rust
// forwarder.rs:168-171
/// max_attempts = max_retries + 1，所以 max_retries=0 表示仅尝试一家、
/// max_retries=3（默认）表示最多 4 家。loop 同时受 providers.len() 自然限制。
max_attempts: usize,

// forwarder.rs:238-242
// max_retries 是「失败后重试次数」语义，attempt 上限 = retries + 1。
let max_attempts = (max_retries as usize).saturating_add(1);
```

循环内的上限检查（放在熔断器 allow 检查之前，避免超限还占用 HalfOpen 名额）：

```rust
// forwarder.rs:452-461
if attempted_providers >= self.max_attempts {
    log::warn!("[{app_type_str}] 已达最大尝试次数上限 ({}/{}), 停止故障转移", ...);
    break;
}
```

即：默认 `max_retries=3` → 最多尝试 4 家；`max_retries=0` → 只尝试 1 家。实际能尝试的 provider 数以 failover 队列长度为准。

### 2.3 failover 开关决定一切

`max_retries` 与超时配置是否生效，都受 `auto_failover_enabled` 单开关门（`handler_context.rs:201-244`）：

```rust
// handler_context.rs:219-224
// 故障转移关闭时强制 max_retries=0（仅尝试 1 个 provider）
let max_retries = if self.app_config.auto_failover_enabled {
    self.app_config.max_retries
} else {
    0
};
```

同时超时参数也只在 failover 开启时才传入（`handler_context.rs:202-217`）：`(non_streaming_timeout, first_byte_timeout, idle_timeout)` 在 failover 关闭时**全部传 0**（0 表示禁用超时）。语义是「不超时 + 不切换」。

`select_providers`（`provider_router.rs:45-118`）在 failover 开启时**只按 failover 队列顺序**返回候选（P1 → P2 → …，忽略「当前供应商」）；failover 关闭时只返回当前供应商 1 家（`provider_router.rs:112-117`）。队列中「不支持 failover」的 provider（Codex 官方账号）会被跳过（`provider_router.rs:98-100` / `provider_supports_failover` `provider_router.rs:18-21`）。

---

## 3. 触发条件：什么错误会换下一家（`categorize_proxy_error`）

分类逻辑在 `forwarder.rs:2688-2735`，是判定 failover 是否会继续的唯一依据：

```rust
fn categorize_proxy_error(&self, error: &ProxyError, provider: &Provider) -> ErrorCategory {
    // 1) Codex 官方账号：任何错误都不 failover（入站 Authorization 会被复用到另一张卡，跨账号）
    if super::providers::is_codex_official_provider(provider) {
        return ErrorCategory::NonRetryable;
    }
    // 2) xAI OAuth 的 AuthError：token 需要重新登录，换卡也没用
    if provider.is_xai_oauth() && matches!(error, ProxyError::AuthError(_)) {
        return ErrorCategory::NonRetryable;
    }
    match error {
        // 网络和上游错误：换下一家
        ProxyError::Timeout(_) => ErrorCategory::Retryable,
        ProxyError::ForwardFailed(_) => ErrorCategory::Retryable,   // 连接失败/发送失败
        ProxyError::ProviderUnhealthy(_) => ErrorCategory::Retryable,
        ProxyError::ConfigError(_) => ErrorCategory::Retryable,
        ProxyError::TransformError(_) => ErrorCategory::Retryable,
        ProxyError::AuthError(_) => ErrorCategory::Retryable,       // 换家可能持有不同 key
        ProxyError::StreamIdleTimeout(_) => ErrorCategory::Retryable,
        // 上游 HTTP 错误按状态码分桶
        ProxyError::UpstreamError { status, .. } => match *status {
            400 | 405 | 406 | 413 | 414 | 415 | 422 | 501 => ErrorCategory::NonRetryable,
            _ => ErrorCategory::Retryable,   // 其他 4xx(401/403/404/408/409/429/451) + 全部 5xx
        },
        ProxyError::NoAvailableProvider => ErrorCategory::NonRetryable,
        _ => ErrorCategory::NonRetryable,
    }
}
```

要点：

- **连接失败 / 超时 / 往上游发请求失败**（`reqwest::Error` 经 `map_reqwest_send_error` `forwarder.rs:3399` 转成 `Timeout` / `ForwardFailed`）→ 一律 Retryable。
- **5xx 全部 Retryable**；**429 是 Retryable**（换一家 provider 可能不同配额），但**不读 Retry-After、不等待**，立即换下一家。
- **4xx 里只有 400/405/406/413/414/415/422/501 是 NonRetryable**（注释 `forwarder.rs:2709-2720`：这些是「请求体格式/语义、方法、超限、内容类型、上游协议不支持」类客户端错误，换哪家都会被拒、只会污染熔断器和浪费配额）。**401/403/404/408/409/429/451 等 4xx 仍是 Retryable**（换家可能不同 key/地域/模型映射）。
- **AuthError 默认 Retryable**（真相：换家持有不同 key 可能成功），但有两类白名单例外被强制转 NonRetryable（Codex 官方、xAI OAuth）。
- **整流器内部**有自己的「是否该 failover」判定，见 §4。

### 3.1 是否重试非幂等请求？

**不区分 HTTP 方法**。`forward_with_retry_inner` 把 `method`（`forwarder.rs:404`「透传给上游，支持 GET/POST 等」）原样透传给 `forward`，分类只看 `ProxyError` 类型，不看方法、不看幂等性。GET 和 POST 在同一条 failover 路径上。实际转发场景以 POST 的 LLM 请求为主，未见针对重复提交副作用（如非幂等 POST）做任何规避。

### 3.2 2xx 但「语义失败」也会触发 failover

几个为 failover 兜底的校验，把「HTTP 200 但内容是错误信封」识别为失败，回到 retry loop：

- 非流式成功分支先经 `prepare_success_response_for_failover`（`forwarder.rs:2378`）：非流式读完整 body，读超时/中断回到 loop（`forwarder.rs:2393-2404`）；流式至少等首个 chunk。
- Codex→Anthropic 兼容网关返回 2xx Anthropic 错误信封 → `validate_codex_anthropic_success_response` 返回 Err（`forwarder.rs:2409-2437`）。
- Claude→Responses 网关 2xx 语义失败 → `validate_responses_success_response` / `validate_responses_stream_start`（`forwarder.rs:2439-2546`）。

---

## 4. 同一 Provider 的「整流器重试」（真正的单点重试）

对**同一 Provider 发起第二次请求**只发生在三类整流器，均要求 `is_anthropic_provider`（Claude / ClaudeAuth，`forwarder.rs:576-580`），且每个 Provider 每类各自只重试一次（本地 bool 标志）：

1. **media 降级（上游拒绝图片输入）**：`media_retry_should_trigger`（`forwarder.rs:205-218`）要求 adapter 是 Claude/Codex、rectifier 开启 `request_media_fallback`、未重试过、body 含图片块、错误是 `is_unsupported_image_error`。触发后把图片块替换为 `[Unsupported Image]` 标记，对**同一 provider 重发一次**（`forwarder.rs:608-696`）。
2. **thinking 签名整流**：`should_rectify_thinking_signature` 命中 `Invalid 'signature' in 'thinking' block` 类错误时移除 thinking/signature 字段后同 provider 重发一次（`forwarder.rs:701-849`），`rectifier_retried` 标志防二次（`forwarder.rs:708`）。
3. **budget 整流**：`should_rectify_thinking_budget` 命中 thinking budget 约束错误时修正后同 provider 重发一次（`forwarder.rs:851-1008`），`budget_rectifier_retried` 防二次（`forwarder.rs:859`）。

整流重试失败后的收尾走 `handle_rectifier_retry_failure`（`forwarder.rs:306-361`）：若失败原因是 provider 层错误（`Timeout`/`ForwardFailed`/`UpstreamError status>=500`）→ 记熔断器、记 `last_error`、`continue` 让下一家 failover；否则（客户端错误）→ 直接返回。整流重试「不计入熔断器」（`forwarder.rs:753` 注释），用 `release_permit_neutral` 释放 HalfOpen 名额而非记失败。

> 注意：这三类整流器都受 `RectifierConfig` 总开关 `enabled` 及其子开关管辖（`types.rs:189-241`，默认全开）。它们是「请求体兼容性修复」而非通用重试。

---

## 5. 失败后如何向客户端响应

### 5.1 全部 provider 失败的收尾

循环结束后（`forwarder.rs:1099-1136`）：

- `attempted_providers == 0`（provider 列表非空但全被熔断器拒绝）→ `ProxyError::NoAvailableProvider`（`forwarder.rs:1110-1113`）。
- 其余 → `Err(ForwardError { error: last_error.unwrap_or(ProxyError::MaxRetriesExceeded), provider: last_provider })`（`forwarder.rs:1133-1136`）。**返回的是最后一个 provider 的错误**（`last_error` 在每次 Retryable 分支被覆盖，`forwarder.rs:1065`），不是聚合、也不是第一个。

`handlers.rs` 接收 `Err` 后 `log_forward_error` 并直接 `return Err(err.error)`（`handlers.rs:217-223`），最终由 `ProxyError::into_response` 序列化为 HTTP 响应。

### 5.2 状态码与响应体映射

`ProxyError::into_response`（`error.rs:82-180`）+ `error_mapper.rs:19-64`：

| 终端错误 | HTTP 状态 | 响应体 |
|---|---|---|
| `UpstreamError` | **透传上游状态码**（`error.rs:89-90`） | 上游 body 若是合法 JSON → **原样透传**；否则包装成 `{"error":{"message":…,"type":"upstream_error"}}`（`error.rs:92-113`） |
| `Timeout` / `StreamIdleTimeout` | 504 | `{"error":{"message":…,"type":"proxy_error"}}` |
| `ForwardFailed` | 502 | 同上 |
| `NoAvailableProvider` / `AllProvidersCircuitOpen` / `NoProvidersConfigured` / `ProviderUnhealthy` / `MaxRetriesExceeded` | 503 | 同上 |
| `AuthError` | 401 | 同上 |
| `ConfigError` / `InvalidRequest` | 400 | 同上 |
| `TransformError` | 422 | 同上 |

关键点：**上游错误响应体做 JSON 透传**（把上游原始 JSON 错误对象直接返回给客户端），非 JSON 才包装；「重试耗尽 `/ 无可用`」类错误统一 503，错误消息是中文本地化字符串（如 `error_mapper.rs:81`「所有 Provider 都失败，重试耗尽」）。

---

## 6. 超时设置（连接/读取）与超时后的行为

### 6.1 全局 reqwest 客户端

`http_client.rs:216-227` 构建全局 `reqwest::Client`：

```rust
let mut builder = Client::builder()
    .timeout(Duration::from_secs(600))      // 整请求总超时 600s
    .connect_timeout(Duration::from_secs(30)) // 连接超时 30s
    .pool_max_idle_per_host(10)
    .tcp_keepalive(Duration::from_secs(60))
    .no_gzip().no_brotli().no_deflate().no_zstd(); // 关闭自动解压，手动解压
```

依赖 `reqwest = { version = "0.12", features = ["rustls-tls","json","stream","socks"] }`（`Cargo.toml:42`）。**没有引入 `reqwest-middleware`，没有 `reqwest-retry`，没有 tower retry 层**。`tower`/`tower-http` 只用于 `cors` 功能（`Cargo.toml:52-53`），与重试无关。

### 6.2 每次转发时的超时

`forward` 函数（`forwarder.rs`）里：

- 走 reqwest（SOCKS5 代理或不需保留 header 大小写，`forwarder.rs:2258-2295`）：
  - 非流式：`request.timeout(non_streaming_timeout)`（0 时 reqwest 全局 600s 兜底，`forwarder.rs:2270-2272`）。
  - 流式：`request.timeout(24h)` 避免长流被总时长误杀，改由「首包/静默期」控制（`forwarder.rs:2266-2269`）。
  - 流式首包等待：`tokio::time::timeout(header_timeout, send)`，超时 → `ProxyError::Timeout("流式响应首包超时…")`（`forwarder.rs:2277-2290`）。
- 走 hyper raw（保留 header 大小写，直连或 HTTP 代理 CONNECT）：`hyper_client::send_request(..., timeout, ...)`（`forwarder.rs:2297-2312`）。

### 6.3 三个应用级超时（仅 failover 开启时生效）

定义在 `AppProxyConfig`（`types.rs:169-176`）与 SQLite 默认（`schema.rs:131-132`）：

| 字段 | 默认 | 范围 | 作用 |
|---|---|---|---|
| `streaming_first_byte_timeout` | 60s | 1–120 | 流式首个字节/chunk 前超时（failover 安全窗口） |
| `streaming_idle_timeout` | 120s | 60–600 / 0 禁用 | 流式两个 chunk 间静默期超时 |
| `non_streaming_timeout` | 600s | 60–1200 | 非流式总超时 |

`handler_context.rs:202-217`：failover 关闭时三者全传 0（禁用）。`types.rs:20-27` 旧全局 `ProxyConfig` 字段（`streaming_*`/`non_streaming_timeout`）为同一组默认值。

### 6.4 超时后的行为（有无「超时重试」？）

**没有「对同一 provider 的超时重试」**。超时被映射成 `ProxyError::Timeout`，归类为 Retryable，随后走 failover 换下一家（若非流式读 body 阶段超时，见 §2.2 / §5 会经 `prepare_success_response_for_failover` 回 loop；若是流式首包超时，在 `prime_streaming_response` `forwarder.rs:2561-2574` 抛 `Timeout` 回 loop）。**只有一家 provider 或 failover 关闭时，超时即终端**，返回 504。

---

## 7. 流式(SSE) vs 非流式：重试/failover 的差异

两者的 failover 能力相同，差异在**「何时把响应提交给客户端」即 failover 的安全窗口**：

- **非流式**：`forward` 拿到 2xx 后先走 `prepare_success_response_for_failover`（`forwarder.rs:2387-2406`）把完整 body 读进内存并 pool 出 `ProxyResponse::buffered`，读超时（`non_streaming_timeout`）或中断 → `Timeout`/错误回 retry loop。**只要 body 没读完，就能换下一家**。读完才提交给下游。
- **流式**：`forward` 的 2xx 分支走 `prime_streaming_response`（`forwarder.rs:2548-2581`），只等**首个 chunk**（`streaming_first_byte_timeout`），拿到后 `futures::stream::once(first).chain(stream)` 拼回并返回 Ok。**首包到达即算成功、立即返回**。此后响应经 `create_logged_passthrough_stream`（`response_processor.rs:683`）把字节流写给客户端；中途「静默期超时」（`response_processor.rs:722-734`）只 yield 一个 `io::Error` 结束流（已来不及换 provider，响应已提交），或流结束/流错误直接 `break`。`StreamIdleTimeout` 虽是 Retryable（`forwarder.rs:2729`），但因它在「首包之后」才可能发生、而那时已离开 failover 循环，实际**无法再换家**——这是与首包超时（可 failover）的实质区别。

结论：**流式首包前失败（含首包超时/连接失败/发请求失败）→ 可 failover；流式首包后（静默期/中途断连）→ 不可 failover，只对客户端断流。**

---

## 8. 熔断器（Circuit Breaker）：跨请求的「预防性跳过」

与「单请求重试」正交：熔断器按 `app_type:provider_id` 维护跨请求健康状态，影响候选链路选择与放行，避免向已判定不健康的 provider 继续发请求。

- 三态 `Closed / Open / HalfOpen`（`circuit_breaker.rs:14-23`），默认配置（`circuit_breaker.rs:63-73`，SQLite 默认一致 `schema.rs:133-135`）：
  - `failure_threshold = 4`（连续失败 4 次 → Open）
  - `success_threshold = 2`（HalfOpen 连续成功 2 次 → Closed）
  - `timeout_seconds = 60`（Open 60s 后进 HalfOpen 探测）
  - `error_rate_threshold = 0.6` + `min_requests = 10`（错误率 ≥60% 且 ≥10 请求时 → Open）
  - HalfOpen 每次只放行 1 个探测请求（`circuit_breaker.rs:315-333`）
- `select_providers` 阶段对 Open 且未到恢复时间的 provider 直接剔除（`provider_router.rs:103-111`）；`allow_provider_request` 在发请求前再做一次放行。
- **单 provider（failover 关闭或多 provider 场景被裁剪到 1 个）时跳过熔断器检查**（`forwarder.rs:441-442` + `provider_router.rs:112-117`），避免熔断器把所有请求都挡掉。
- 失败仅当 `ErrorCategory::Retryable` 才 `record_result(false,…)` 计入熔断器与 DB 健康度；NonRetryable/ClientAbort 不污染（`forwarder.rs:1032-1093`，`release_permit_neutral`）。

---

## 9. 前端（React）层的重试——不作用于 LLM 转发

前端确实有 `retry`，但**全部是 TanStack Query 对「前端 ↔ Tauri 后端（invoke）」查询的重试，跟真实 LLM 请求转发无关**（真实转发走 Rust proxy 的 HTTP 服务，React Query 不参与）。

- 全局默认：`queryClient.ts:3-14` → queries `retry: 1`，mutations `retry: false`。
- 用量/额度假格查询 `useUsageQuery`：`retry: 1, retryDelay: 1500`（`queries.ts:273-274`），配合 `isTransientUsageError`（`queries.ts:142-167`：**5xx 与 429 视为瞬时**，其余 4xx 确定性）与 keep-last-good 窗口展示。
- `subscription.ts:102/147/189` 等订阅类查询 `retry: 1`；`copilot.ts:61` `retry: 1`；`failover.ts:24` provider 健康/熔断信息查询 `retry: false`。
- **前端没有针对「LLM 代理请求失败」的重试逻辑**。

唯一与 LLM 转发略有相关的前端重试是**连通性检查**（不触达真实模型、只探测 base_url 可达性，且不碰熔断器）：`StreamCheckService::check_with_retry`（`services/stream_check.rs:90-120`）对「超时/abort」类失败重试 `config.max_retries` 次（默认 1，`ConnectivityCheckConfigPanel.tsx:23`），判定函数 `should_retry`（`stream_check.rs:273-276`）仅匹配文案含 `timeout`/`abort`/`timed out`，无退避、无 Retry-After。

---

## 10. Retry-After / 429 / 503 限流的特殊处理

- **`Retry-After` 头完全不做解析、不用于重试延迟**。全仓唯一出现 `retry-after` 的地方是 `response_processor.rs:812` 的 `is_safe_diagnostic_header` 白名单——仅用于把该头**记录进诊断日志**展示，不参与任何决策。
- **429**：在 `categorize_proxy_error` 中属 Retryable（`forwarder.rs:2721-2724`），即「立即换下一家 provider」，**不等待**。前端 `isTransientUsageError` 把 429 归为瞬时（`queries.ts:163`），仅用于用量展示的 keep-last-good 窗口，与转发重试无关。
- **503**：属 5xx，自然 Retryable，同样立即换下一家、不等待。
- 无任何指数退避/抖动/固定延迟实现（全仓 `grep backoff|exponential|jitter` 无命中；转发路径无 `sleep`）。唯一 `tokio::time::sleep(Duration::from_millis(10))` 出现在 `forwarder.rs:3980`，位于（测试之外的）某辅助路径，与重试退避无关。

---

## 11. 关键源码索引

| 主题 | 文件 / 行号 |
|---|---|
| 主 failover 循环 | `src-tauri/src/proxy/forwarder.rs:410-1137`（`forward_with_retry_inner`） |
| `max_attempts = max_retries + 1` | `forwarder.rs:168-171`、`238-242` |
| 上限检查 | `forwarder.rs:452-461` |
| 可重试/不可重试分类 | `forwarder.rs:2688-2735`（`categorize_proxy_error`） |
| Retryable → continue / NonRetryable → return | `forwarder.rs:1035-1094` |
| 整流器（同 provider 重试一次） | `forwarder.rs:583-1008`，收尾 `forwarder.rs:306-361` |
| 非流式读体回 loop | `forwarder.rs:2378-2407` |
| 流式首包 priming | `forwarder.rs:2548-2581` |
| failover 开关→max_retries/超时门控 | `src-tauri/src/proxy/handler_context.rs:201-244` |
| 候选 provider 选择 | `src-tauri/src/proxy/provider_router.rs:45-118` |
| 终端错误→HTTP 映射 | `src-tauri/src/proxy/error.rs:82-180`、`error_mapper.rs:19-64` |
| reqwest 全局超时 | `src-tauri/src/proxy/http_client.rs:216-227` |
| 每次转发超时 | `forwarder.rs:2234-2313` |
| 流式静默期超时（不可 failover） | `src-tauri/src/proxy/response_processor.rs:683-737` |
| 熔断器默认配置 | `circuit_breaker.rs:63-73`、`database/schema.rs:131-135` |
| 前端 React Query retry | `src/lib/query/queryClient.ts:3-14`、`queries.ts:273-274` |
| 连通性检查重试 | `src-tauri/src/services/stream_check.rs:90-120`、`273-276` |