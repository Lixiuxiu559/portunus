# cc-switch 余额/额度查询实现研究

> 研究对象：[cc-switch](https://github.com/farion1231/cc-switch)（源码位于本机
> `/Users/lixiuxiu/development_tool/projects/cc-switch`）。本文梳理其「余额/额度查询」
> 的完整实现链路，所有结论均直接溯源到源码并标注 `file:line`。

## 一、总体架构

cc-switch 是 Tauri 2 桌面应用：React 前端 + Rust 后端。余额/额度查询的统一数据流：

```
React 组件 (Footer/卡片)
  └─ TanStack Query hook (useQuery)
       └─ invoke("xxx", {...})        ← src/lib/api/*.ts 薄封装
            └─ #[tauri::command]      ← src-tauri/src/commands/*.rs 命令层
                 └─ services/*.rs 服务层         ← 真正发起上游 HTTP 调用
                      └─ 上游官方 API（OAuth 额度接口 / API Key 余额接口 / gRPC-web 账单接口）
```

关键点：**凭据有两种来源**——(1) CLI 工具自带的 OAuth 凭据（Claude Code / Codex CLI /
Gemini CLI / Grok CLI 各自的 Keychain 或 auth.json），(2) 用户手动填的 API Key。查询
额度时直接复用这些凭据，cc-switch 本身不保存、不刷新（Gemini 除外，见 §四.3）。

额度/余额查询在代码里分成**四条互不重叠的路径**，由供应商的 `template_type` 或 distinct
command 决定：

| 路径 | 产物类型 | 覆盖供应商 |
|---|---|---|
| 官方订阅额度（OAuth） | `SubscriptionQuota` | Claude / Codex / Gemini / Grok(xAI) |
| Codex/ChatGPT + xAI 自管 OAuth | `SubscriptionQuota` | Codex OAuth、xAI OAuth（cc-switch 自管 token） |
| 官方余额（API Key） | `UsageResult` | DeepSeek / StepFun / SiliconFlow / OpenRouter / Novita |
| Coding/Token Plan | `SubscriptionQuota` | Kimi / 智谱(个人+团队) / MiniMax / ZenMux / 火山方舟 |
| GitHub Copilot | `CopilotUsageResponse` | GitHub Copilot（OAuth 多账号） |

---

## 二、前端：TanStack Query 层

### 2.1 统一 hook 与 keep-last-good

前端有一个核心的「keep-last-good」（最近一次成功值保留）机制，定义在
`src/lib/query/queries.ts`：

- `KEEP_LAST_GOOD_MS = 10 * 60 * 1000`（10 分钟窗口）— `queries.ts:120`
- `isTransientUsageError()` — 白名单判定「瞬时失败」（网络类文案 + HTTP 5xx/429）
  — `queries.ts:142-167`
- `resolveDisplayUsage()` — 纯决策函数，决定失败时是否继续展示上次成功值
  — `queries.ts:192-243`

核心语义（`queries.ts:169-185` 注释详细说明）：

- **瞬时失败**（5xx/429/网络）且 10 分钟内有过成功 → 继续展示旧值，并把
  `lastQueriedAt` 指向旧成功时刻（相对时间翻红提示）。超出窗口或从无成功 → 透出失败。
- **确定性失败**（鉴权/空 key/未知供应商/4xx/解析失败）→ 立即透出，并**清空 lastGood**，
  防止后续一次网络抖动把「已失效凭据」的旧额度复活。

### 2.2 各路径的 query hook

**官方订阅额度** `src/lib/query/subscription.ts`：

- `useSubscriptionQuota(appId, enabled, autoQuery, interval)` — `subscription.ts:79-106`
  - queryKey: `["subscription", "quota", appId]`（`subscription.ts:16`）
  - 仅对 `claude / codex / gemini / grokbuild` 启用（`subscription.ts:94`）
  - 默认 `REFETCH_INTERVAL = 5 分钟`（`subscription.ts:12`）
- `useCodexOauthQuota` / `useCodexOauthQuotaByAccountId` — `subscription.ts:123-166`
- `useXaiOauthQuota` — `subscription.ts:175-193`
- `useQuotaKeepLastGood()` — 把 `resolveDisplayUsage` 套到订阅结果上的辅助 hook
  — `subscription.ts:47-77`

**脚本/余额路径** `useUsageQuery(providerId, appId, options)` — `queries.ts:245-305`：

- queryKey: `usageKeys.script(providerId, appId)`
- 调用 `usageApi.query()` → `invoke("queryProviderUsage", ...)`
- `retry: 1, retryDelay: 1500`；同样跑过 `resolveDisplayUsage`

**Copilot** `src/lib/query/copilot.ts`：

- `useCopilotQuota(accountId, options)` — `copilot.ts:22-63`
- queryFn 里调用 `copilotGetUsage()/copilotGetUsageForAccount(accountId)`，取出
  `quota_snapshots.premium_interactions` 计算 `utilization`
  （`(entitlement - remaining) / entitlement * 100`）— `copilot.ts:34-39`
- `REFETCH_INTERVAL = 5 分钟`（`copilot.ts:5`）

### 2.3 缓存桥接（后端 → 前端反向同步）

`src/hooks/useUsageCacheBridge.ts` 监听后端 `usage-cache-updated` 事件（托盘触发的
刷新不经前端），把 payload 直接 `setQueryData` 回 React Query 缓存
（`useUsageCacheBridge.ts:27-43`）。这样 rust 侧 `UsageCache` 与前端 React Query
两份缓存不会各自为战。

### 2.4 渲染层

- 订阅额度：`src/components/SubscriptionQuotaFooter.tsx`
  - `SubscriptionQuotaView` 渲染 5 种状态：not_found / parse_error / expired / API 失败 / 成功
    （`SubscriptionQuotaFooter.tsx:106-252`）
  - `TIER_I18N_KEYS` 映射 tier 名 → i18n key（`SubscriptionQuotaFooter.tsx:25-44`）
  - `TierBadge` / `TierBar` 渲染百分比 + 倒计时 + USD 额度（ZenMux）
- Copilot：`src/components/CopilotQuotaFooter.tsx`
- xAI OAuth：`src/components/XaiOauthQuotaFooter.tsx`
- Codex OAuth：`src/components/CodexOauthQuotaFooter.tsx`
- 余额/脚本：`src/components/UsageFooter.tsx`（`toQuotaTier` 把 `UsageData.extra`
  JSON 拆成 QuotaTier，`UsageFooter.tsx:22-44`）

---

## 三、后端：命令层与分发

### 3.1 分开的 Tauri command

| command | 文件 | 对应服务 |
|---|---|---|
| `get_subscription_quota(tool)` | `commands/subscription.rs:19` | `services::subscription::get_subscription_quota` |
| `get_balance(base_url, api_key)` | `commands/balance.rs:4` | `services::balance::get_balance` |
| `get_coding_plan_quota(...)` | `commands/coding_plan.rs:4` | `services::coding_plan::get_coding_plan_quota` |
| `get_codex_oauth_quota(account_id)` | `commands/codex_oauth.rs:28` | `services::subscription::query_codex_quota` |
| `get_xai_oauth_quota(account_id)` | `commands/xai_oauth.rs:70` | `services::subscription_grok::query_grok_quota` |
| `copilot_get_usage[_for_account]` | `commands/copilot.rs:203/211` | `CopilotAuthManager::fetch_usage[_for_account]` |
| `queryProviderUsage(providerId, app)` | `commands/provider.rs:457` | 按 `template_type` 分发 |

### 3.2 核心分发：`query_provider_usage_inner`

`commands/provider.rs:544-764` 是脚本路径的总入口，按供应商 `meta.usage_script.template_type`
分发（`provider.rs:560-562`）：

1. `github_copilot` → `auth_manager.fetch_usage`（§四.4）— `provider.rs:565-598`
2. `token_plan` → `services::coding_plan::get_coding_plan_quota`（§四.5）— `provider.rs:601-694`
3. `balance` → `services::balance::get_balance`（§四.2）— `provider.rs:697-704`
4. `official_subscription` → `services::subscription::get_subscription_quota` 或 xAI OAuth
   分支（§四.1）— `provider.rs:707-758`
5. 兜底 → `ProviderService::query_usage`（JS 脚本路径，用户自定义脚本）— `provider.rs:761-763`

凭据解析辅助：

- `resolve_native_credentials(app_type, provider)` → `provider.resolve_usage_credentials(app_type)`
  — `provider.rs:502-506`
- `resolve_coding_plan_credentials(...)` — ZenMux 特判，优先脚本凭据 — `provider.rs:508-542`

### 3.3 命令层的官方订阅写缓存 + 托盘

`get_subscription_quota`（`commands/subscription.rs:19-42`）与 `queryProviderUsage`
（`commands/provider.rs:482-497`）在 `Ok`（成功或确定性失败）时：

1. `app.emit("usage-cache-updated", payload)` — 触发前端 `useUsageCacheBridge` 同步
2. `state.usage_cache.put_subscription(...)` / `.put_script(...)` — 写入 rust 侧缓存
3. `crate::tray::schedule_tray_refresh(&app)` — 托盘刷新

`Err`（瞬时传输失败）时**不写、不 emit**，保留上一份托盘快照。

---

## 四、后端：服务层实现（各供应商）

### 4.1 官方订阅额度 — `services/subscription.rs`

**整体约定**（`subscription.rs:1-11` 模块注释）：

> `Err(String)` = 瞬时传输失败（前端 reject → retry + 保留上次成功值）；
> `Ok(success:false)` = 确定性失败（鉴权/非 2xx/响应体非法 JSON），立即透出。

入口 `get_subscription_quota(tool)` — `subscription.rs:1239-1352`，`tool` 分发：

**Claude**（`claude`）：

- 凭据读取 `read_claude_credentials()` — `subscription.rs:116-127`
  - 优先 macOS Keychain（service `"Claude Code-credentials"`），回退 `~/.claude/.credentials.json`
    — `subscription.rs:130-176`
  - 兼容 `claudeAiOauth` / `claude.ai_oauth` 两种 key — `subscription.rs:193-195`
  - 过期判定 `is_token_expired()` 兼容秒/毫秒时间戳与 ISO 字符串 — `subscription.rs:246-279`
- 查询 `query_claude_quota(access_token)` — `subscription.rs:344-458`
  - `GET https://api.anthropic.com/api/oauth/usage`，头 `anthropic-beta: oauth-2025-04-20`
  - 解析 `five_hour / seven_day / seven_day_opus / seven_day_sonnet`（`KNOWN_TIERS`，
    `subscription.rs:332-337`）+ 任意未知窗口 + `extra_usage`（超额用量）
- `CredentialStatus::Expired` 时**仍试一把**（token 可能实际仍有效）— `subscription.rs:1251-1264`

**Codex**（`codex`）：

- 凭据读取 `read_codex_credentials()` — `subscription.rs:490-499`
  - 优先 macOS Keychain（service `"Codex Auth"`），回退 `~/.codex/auth.json`
  - 仅 `auth_mode == "chatgpt"`（OAuth）有效 — `subscription.rs:560-567`
  - 过期判定 `is_codex_token_stale()`：距 `last_refresh` > 8 天 — `subscription.rs:614-626`
- 查询 `query_codex_quota(access_token, account_id, tool_label, expired_message)`
  — `subscription.rs:678-767`（**该函数被 CLI 路径与 cc-switch 自管 OAuth 路径共用**）
  - `GET https://chatgpt.com/backend-api/wham/usage`，头 `User-Agent: codex-cli`，
    可选 `ChatGPT-Account-Id` — `subscription.rs:686-694`
  - 解析 `rate_limit.primary_window / secondary_window`，`window_seconds_to_tier_name()`
    把窗口秒数映射为 tier 名（18000→five_hour, 604800→seven_day, 2592000→30_day）
    — `subscription.rs:649-666`

**Gemini**（`gemini`）：

- 凭据读取 `read_gemini_credentials()` — `subscription.rs:794-803`
  - 优先 macOS Keychain（service `"gemini-cli-oauth"`, account `"main-account"`），
    回退 `~/.gemini/oauth_creds.json` — `subscription.rs:807-830`
- **Gemini 特殊：支持 token 刷新**（access_token 仅 ~1h 有效，用 refresh_token 调用
  `https://oauth2.googleapis.com/token` 刷新）— `refresh_gemini_token()`
  `subscription.rs:979-1001`；client_id/secret 是 Gemini CLI 源码的公开值
  `subscription.rs:971-973`
- 查询 `query_gemini_quota(access_token)` — `subscription.rs:1060-1230`，**两步 API**：
  1. `POST .../v1internal:loadCodeAssist` 拿项目 ID — `subscription.rs:1064-1120`
  2. `POST .../v1internal:retrieveUserQuota` 拿分桶配额 — `subscription.rs:1128-1230`
  - 按模型分类（`classify_gemini_model` → pro/flash/flash-lite），每类取最低
    remainingFraction，转成已用百分比 — `subscription.rs:1177-1215`

**Grok**（`grokbuild`）→ 委托 `services::subscription_grok`（§4.6）— `subscription.rs:1349`

### 4.2 官方余额 — `services/balance.rs`

支持 5 家，`detect_provider(base_url)` 按域名识别 — `balance.rs:26-43`：

| 供应商 | 端点 | 单位/字段 |
|---|---|---|
| DeepSeek | `GET https://api.deepseek.com/user/balance` | `balance_infos[].total_balance`（CNY） |
| StepFun | `GET https://api.stepfun.com/v1/accounts` | `balance`（CNY） |
| SiliconFlow | `GET https://api.siliconflow.cn|.com/v1/user/info` | `data.totalBalance`（CNY/USD） |
| OpenRouter | `GET https://openrouter.ai/api/v1/credits` | `total_credits - total_usage`（USD） |
| Novita AI | `GET https://api.novita.ai/v3/user/balance` | `availableBalance / 10000`（USD，金额单位 0.0001） |

统一入口 `get_balance(base_url, api_key)` — `balance.rs:426-454`：空 key → 确定性失败；
未知域名 → `"Unknown balance provider"`。

统一错误通道语义（`balance.rs:1-10` 模块注释）：`Err` = 瞬时（网络/超时/读体中断），
`Ok(success:false)` = 确定性。**关键实现技巧**：每家查询都先 `resp.bytes()` 再
`serde_json::from_slice`，把「读体失败」（瞬时）与「解析失败」（确定性）分开——
reqwest 的 `json()` 会把两者都包成 decode 无法区分（`balance.rs:99-108` 等注释）。

401/403 统一 `make_auth_error(status)` — `balance.rs:53-68`。

### 4.3 Codex OAuth — `commands/codex_oauth.rs`

cc-switch 自管的 ChatGPT 账号（不是 Codex CLI 的 `~/.codex/auth.json`）。
`get_codex_oauth_quota(account_id)` — `codex_oauth.rs:28-63`：

- `CodexOAuthManager` 直接持 `Arc`（不包 RwLock，细粒度锁）— `codex_oauth.rs:19`
- 账号解析：显式 > 默认账号 > `not_found` — `codex_oauth.rs:35-41`
- `manager.get_valid_token_for_account(&id)` 自动刷新 token — `codex_oauth.rs:44-53`
- **复用** `services::subscription::query_codex_quota`，端点协议与 Codex CLI 路径完全一致
  — `codex_oauth.rs:56-62`

### 4.4 GitHub Copilot — `proxy/providers/copilot_auth.rs`

`CopilotAuthManager::fetch_usage_for_account(account_id)` — `copilot_auth.rs:907-964`：

- 端点 `copilot_usage_url(domain)` = `{github_api_base}/copilot_internal/user`
  （github.com → `https://api.github.com/copilot_internal/user`）— `copilot_auth.rs:78-80`
- 鉴权头 `Authorization: token {github_token}`（**不是 Bearer**）— `copilot_auth.rs:923`
- 带 editor/plugin 版本头伪装 VSCode Chat 插件 — `copilot_auth.rs:925-928`
- 响应 `CopilotUsageResponse`：`copilot_plan` / `quota_reset_date` / `quota_snapshots`
  （`chat` / `completions` / `premium_interactions`，每个含 `entitlement`/`remaining`）
  — `src/lib/api/copilot.ts:135-159`
- 前端只用 `premium_interactions` 算利用率 — `src/lib/query/copilot.ts:34-39`

### 4.5 Coding / Token Plan — `services/coding_plan.rs`

`get_coding_plan_quota(...)` — `coding_plan.rs:1276-1340`，覆盖：

| 供应商 | 端点 | 鉴权 | 产物 |
|---|---|---|---|
| Kimi For Coding | `api.kimi.com/coding/v1/usages` | Bearer | five_hour + weekly_limit |
| 智谱 GLM（个人） | `open.bigmodel.cn|api.z.ai/api/monitor/usage/quota/limit` | **无 Bearer**，`Authorization: {key}` | five_hour + weekly_limit |
| 智谱团队版 | 同上 `?type=2` + `bigmodel-organization`/`bigmodel-project` 头 | 无 Bearer | 复用个人版解析 |
| MiniMax | `api.minimaxi.com|.io/v1/api/openplatform/coding_plan/remains` | Bearer | five_hour + weekly_limit（status=1 才有周桶） |
| ZenMux | 用户配置的 base_url（`base_url` 直连） | Bearer | five_hour + weekly（带 USD 额度） |
| 火山方舟 | `open.volcengineapi.com` OpenAPI，**SigV4 AK/SK 签名** | 火山签名 | 5h/周/月（Agent Plan 或 Coding Plan） |

几个值得注意的实现细节：

- **智谱窗口分类** `parse_zhipu_token_tiers` — `coding_plan.rs:247-303`：按 `unit` 字段
  （3=5小时，6=每周）显式分类，**不能用 reset 时间排序**（周期末尾每周窗口会比 5h
  窗口更早重置，issue #3036，`coding_plan.rs:236-246` 注释）。
- **MiniMax** `parse_minimax_tiers` — `coding_plan.rs:644-700`：新接口给「剩余百分比」，
  反转为「已用」；只取 `model_name == "general"`，跳过 video；周桶仅 `current_weekly_status == 1`
  激活。
- **火山方舟** `query_volcengine` — `coding_plan.rs:1102-1175`：走控制面 OpenAPI（**不是**
  推理域名，`open.volcengineapi.com`），强制火山 SigV4 AK/SK 签名（复用推理 Bearer key
  会被 400 InvalidAuthorization 拒绝）。签名的**两处火山特例**（`coding_plan.rs:795-801`）：
  canonical headers 固定顺序 `host;x-date;x-content-sha256;content-type`（不顺字母序）、
  algorithm `HMAC-SHA256` 无 `AWS4` 前缀、scope 结尾 `request`。自动探测：先
  `GetAFPUsage`（Agent Plan），未订阅再 `GetCodingPlanUsage`（Coding Plan）。
- 传输层错误通道的单测锚（`coding_plan.rs:1958-2099`）：用本地 TCP listener 驱动真实
  HTTP 路径，锁定 send 失败/读体中断（→Err）与 401/429/非法 JSON（→Ok(success:false)）。

### 4.6 Grok (xAI) — `services/subscription_grok.rs`

**移植自 CodexBar（steipete/CodexBar）的 Grok provider**（`subscription_grok.rs:1-14`）。

- 凭据：`~/.grok/auth.json`，以 OIDC scope 为 key 的 map，优先 `https://auth.x.ai::<client-id>`
  条目，回退 legacy session；`key` 字段即 Bearer token — `subscription_grok.rs:39-145`
- 查询 `query_grok_quota(access_token, tool_label, relogin_hint)` — `subscription_grok.rs:552-679`：
  - `POST https://grok.com/grok_api_v2.GrokBuildBilling/GetGrokCreditsConfig`，
    发**空 gRPC-web 帧**（1 字节 flags + 4 字节长度 0）— `subscription_grok.rs:560-570`
  - 响应**无公开 .proto**，用通用 protobuf 扫描 `scan_protobuf`（深度 ≤4）按字段路径
    启发式提取已用百分比与重置时间 — `subscription_grok.rs:193-263`
  - `parse_billing_payload` — `subscription_grok.rs:376-431`：百分比取 wire-type 5 (float)
    中路径末段为 1、值域 [0,100] 的最浅字段；重置时间取 varint 落在合理 Unix 秒区间且
    晚于当前时刻的字段；proto3 省略 0 值 percent 时靠「重置时间 + 用量周期标记」特判为 0%
  - gRPC 状态分类：鉴权（16 / 7+bad-credentials）→ Expired；瞬时（4 DEADLINE_EXCEEDED /
    14 UNAVAILABLE / 1+超时文案）→ `Err`；团队主体无个人账单（9 "no personal team"）→
    确定性提示 — `subscription_grok.rs:437-524`
  - `tier_name_for_reset`：重置距今 4-12 天 → weekly，20-45 天 → monthly，否则 → credits
    — `subscription_grok.rs:528-539`

`query_grok_quota` 同样被两个调用点共用（`grokbuild` CLI 路径 与 `xai_oauth` 自管路径），
两者是同一个 OAuth client、token 对 grok.com 账单端点等效（`subscription_grok.rs:546-551`）。

### 4.7 xAI OAuth — `commands/xai_oauth.rs`

`query_xai_oauth_quota_for(state, account_id)` — `xai_oauth.rs:29-66`：

- 走 `XaiOAuthManager`（cc-switch 自管），`get_valid_token_for_account` 自动刷新
- **复用** `services::subscription_grok::query_grok_quota`，协议与 Grok CLI 路径一致
  — `xai_oauth.rs:60-65`

### 4.8 自定义用量脚本（用户可写任意余额查询接口）

**这就是「可自定义余额接口」的答案**：cc-switch 提供一条完全由用户控制的 JS 脚本路径，
可指向任意供应商的任意查询端点，不依赖内置白名单。

**脚本结构**（`src/components/UsageScriptModal.tsx:1589-1605` 的内置示例）：

```js
({
  request: {
    url: "{{baseUrl}}/api/usage",
    method: "POST",
    headers: {
      "Authorization": "Bearer {{apiKey}}",
      "User-Agent": "cc-switch/1.0"
    }
  },
  extractor: function(response) {
    return {
      isValid: !response.error,
      remaining: response.balance,
      unit: "USD"
    };
  }
})
```

分两段：**`request`**（HTTP 请求配置）与 **`extractor`**（把响应转成 `UsageData`）。
extractor 可返回单对象或对象数组（多套餐支持，`usage_script.rs:315-339`）。
`UsageData` 字段全可选：`planName / extra / isValid / invalidMessage / total / used /
remaining / unit`（`src/types.ts:96-105`）。

**模板变量替换**（`usage_script.rs:425-444`）：`{{apiKey}}` / `{{baseUrl}}` /
`{{accessToken}}` / `{{userId}}` 在执行前被替换，避免把敏感凭据写死进脚本。

**执行引擎**：QuickJS（`rquickjs`），在受限 Runtime 里运行（`usage_script.rs:9-68`）：

- **内存限制** 16 MiB、**栈** 256 KiB、**执行时间** 5 秒中断器（`set_interrupt_handler`）
  — `usage_script.rs:35-64`。脚本来自不可信输入（deeplink / 同步导入），必须防
  `while(true)` 挂死后端（DoS），有测试锚 `usage_script.rs:692-727`。
- 分两阶段 eval：先 eval 取 `request` 配置 → Rust 侧发 HTTP → 再 eval 调 `extractor`
  （`Runtime/Context` 在 await 前 drop，不阻塞 async 运行时）— `usage_script.rs:69-227`。

**模板类型**（决定验证规则，`TemplateType` 见 `src/config/constants.ts`）：`custom` /
`general` / `newapi` / `github_copilot` / `token_plan` / `balance` /
`official_subscription`。前面 §三.2 的 `query_provider_usage_inner` 正是按
`template_type` 把非白名单的 `general`/`newapi`/`custom` 落到这条脚本路径兜底
（`provider.rs:760-763`）。

**安全校验**（`usage_script.rs:446-585`）：

- **非 custom 模板**：强制 HTTPS（localhost 除外）+ **同源检查**（请求域名/端口必须与
  `base_url` 一致）— 防脚本借 cc-switch 的凭据去打任意第三方域名。
- **custom 模板**：跳过同源检查、允许非 HTTPS 局域网地址，由用户自担风险
  （`validate_request_url` 的 `is_custom_template` 参数以及测试
  `usage_script.rs:611-627`）。

**执行与格式化**（`services/provider/usage.rs`）：

- `query_usage()` — `usage.rs:126-189`：取库里的 `usage_script`，校验 `enabled`，
  解析凭据（脚本显式值优先、回退 provider 存储凭据，`resolve_script_credentials`
  `usage.rs:101-123`），再 `execute_and_format_usage_result`。
- `test_usage_script()` — `usage.rs:193-228`：前端「测试脚本」按钮用临时内容，不走库。
- `execute_and_format_usage_result` — `usage.rs:13-94`：**瞬时传输失败**
  （`request_failed` / `read_response_failed`）以 `Err` 传播 → 前端 reject → retry；
  其余脚本/配置/HTTP 业务错误折叠成 `success:false` 展示文案。这里同样保持了前面
  §五.1 的统一错误通道语义。

**入口**：前端 `UsageScriptModal.tsx`（用量脚本编辑器，含模板选择、示例、测试按钮），
templated `testUsageScript` command（`provider.rs:769-796`）做脚本测试。

---

## 五、核心设计模式总结

1. **错误通道二分**（贯穿 balance / subscription / coding_plan 三服务）：
   - `Err(String)` = 瞬时传输失败（网络不可达/超时/读体中断）→ 前端 invoke reject →
     react-query retry + 保留上次成功 data（天然 keep-last-good）
   - `Ok(success:false)` = 确定性失败（空 key/未知供应商/鉴权/非 2xx/非法 JSON）→ 立即透出
   - 实现关键：`resp.bytes()` 再 `from_slice`，把读体失败（瞬时）与解析失败（确定性）从
     reqwest 的 `json()` decode 合并里拆开。
2. **keep-last-good（前端 `resolveDisplayUsage`）**：瞬时失败在 10 分钟窗口内继续展示
   上次成功值；确定性失败立即透出并清空 lastGood。
3. **tier 统一模型**：`QuotaTier { name, utilization(0-100), resets_at, used_value_usd?,
   max_value_usd? }`（`types/subscription.ts:7-14` / `services/subscription.rs:28-41`），
   前端 `TIER_I18N_KEYS` 统一映射显示名。所有「百分比窗口」额度都归一到这个模型。
4. **双调用点复用**：`query_codex_quota`（CLI 路径 vs cc-switch 自管 OAuth）与
   `query_grok_quota`（Grok CLI vs xAI OAuth）都参数化了 `tool_label` / `relogin_hint`，
   协议同一套、提示语不同。
5. **缓存双写 + 事件回灌**：后端 `Ok` 写 `UsageCache` 并 emit `usage-cache-updated`；
   前端 `useUsageCacheBridge` 回灌 React Query；托盘刷新不经前端也能同步主界面。
6. **凭据只读不写**（Gemini 例外）：Claude/Codex/Grok 读 CLI 工具的 OAuth 凭据，过期只
   提示重新 login，不代为刷新；Gemini 因 access_token ~1h 有效期，用 refresh_token 刷新。

## 五之二、设计动机与可移植性辨析（为什么这样设计）

这一节不是白描「做了什么」，而是追问「为什么这样做」，并区分哪些是**客户端专属**、哪些是
**可迁移到服务端**的原则——服务端聚合网关（如本项目 portunus）借鉴时不应照搬。

### A. 双结果形状是历史包袱，不该学

cc-switch 并存两套结果形状：

- `UsageResult`（`used/total/remaining/unit`）—— 余额/脚本路径，`src/types.ts:108-112`；
- `SubscriptionQuota`（`tiers[] { name, utilization, resets_at }`）—— 订阅额度路径，
  `src/types/subscription.ts:24-33`。

导致前端要写两套渲染（`UsageFooter.tsx` 的 `toQuotaTier` 兜底转换 vs
`SubscriptionQuotaFooter.tsx` 的 `TierBadge`），后端也要在 `query_provider_usage_inner`
里做 `SubscriptionQuota → UsageResult` 的扁平化（`provider.rs:628-694`、`738-758`）。
**这是演化产物的妥协，不是优点**：余额（绝对值）和百分比窗口本可用一个「字段全可选」的
tier 结构统一表达。新项目应从第一天就用单一 tier 模型。

### B. 错误通道二分是可移植的核心，但 Go 端别靠文案匹配

错误通道二分（瞬时 `Err` vs 确定性 `Ok(success:false)`）是全文最值得移植的原则，且它的
动机在注释里讲得很透：**瞬时失败要保住 react-query 的 retry 和 keep-last-good，确定性失败
要立即透出**（`queries.ts:122-166`、`balance.rs:1-10`）。

但有两个实现细节是**客户端/Rust 专属**，服务端要换：

1. **读体失败 vs 解析失败的区分**靠「先 `bytes()` 再 `from_slice`」绕过 reqwest `json()`
   的合并——这是 reqwest 的行为，Go 的 `io.ReadAll` 与 `json.Unmarshal` 天然分两行，直接
   保持这个粒度即可，不必学它的绕法。
2. **前端 `isTransientUsageError` 匹配错误文案里的 `HTTP <code>`**（`queries.ts:160-164`）
   是「错误分类在前端兜底」的权宜——因为 Rust 端返回 `Ok(success:false)` 时只带字符串文案。
   服务端有结构化错误类型（如 `StatusError`），应**在折叠点就分类**，不把「匹配文案」这种
   脆弱契约留给前端。

### C. OAuth 凭据读取是客户端专属，服务端不移植

Claude/Codex/Gemini/Grok 的额度查询，凭据全部来自**用户桌面环境的 CLI 登录态**：
macOS Keychain 或 `~/.claude/.credentials.json` / `~/.codex/auth.json` 这类本地文件
（`subscription.rs:130-176` 等）。这是 cc-switch 作为「客户端」独有的信息源——服务端拿
不到这些，它拿到的是用户填给渠道的 **API Key**。所以服务端配套的自然对象是
`balance`（§四.2）与 `coding_plan`（§四.5）这两条 **API-Key 路径**，而非订阅额度路径。

### D. 自定义脚本沙箱是「客户端才敢做」的设计

QuickJS 沙箱（§四.8）对 cc-switch 是安全的——单用户本地应用，恶意脚本只害自己。服务端
多租户执行用户代码，等同于开放 SSRF + 内网探测 + 资源耗尽。因此它对服务端的启示**不是**
「照搬」，而是「同样的灵活长尾需求，在服务端需要更严的护栏，甚至干脆用白名单供应商扩展
替代」。SSRF 防护（HTTPS 强制 + 同源校验，`usage_script.rs:446-585`）和资源限制
（内存/栈/超时，`usage_script.rs:35-64`）可作最低安全基线参考。

### E. 前端轮询 + 托盘旁路缓存 = 服务的「cron + 落库快照」等价物

cc-switch 用前端 `refetchInterval`（5 分钟）+ 后端 `UsageCache`（进程内、不持久化）+ 托盘
旁路事件 `usage-cache-updated` 三者配合。服务端没有「前端一直在跑」这个前提，等价做法是：
**服务端 cron 定时冲刷 + 快照落库 + 手动触发 API**。keep-last-good 的窗口判定也从前端
`resolveDisplayUsage`（内存态）移到服务端读侧（按 `queried_at` 判 stale）。

> 完整映射与本项目（portunus）的落地设计见
> [`portunus-balance-module-design.md`](./portunus-balance-module-design.md)。

---

## 六、关键文件清单

| 层 | 文件 | 职责 |
|---|---|---|
| 前端 query | `src/lib/query/queries.ts` | `useUsageQuery` + `resolveDisplayUsage` + `isTransientUsageError`（keep-last-good 核心） |
| 前端 query | `src/lib/query/subscription.ts` | `useSubscriptionQuota` / `useCodexOauthQuota` / `useXaiOauthQuota` |
| 前端 query | `src/lib/query/copilot.ts` | `useCopilotQuota` |
| 前端 api | `src/lib/api/subscription.ts` | `invoke` 封装（getQuota / getCodexOauthQuota / getXaiOauthQuota / getCodingPlanQuota / getBalance） |
| 前端 api | `src/lib/api/copilot.ts` | Copilot `invoke` 封装 + `CopilotUsageResponse` 类型 |
| 前端 bridge | `src/hooks/useUsageCacheBridge.ts` | 监听 `usage-cache-updated` 回灌 React Query |
| 前端渲染 | `src/components/SubscriptionQuotaFooter.tsx` | `SubscriptionQuotaView` + `TierBadge` + `TIER_I18N_KEYS` |
| 前端渲染 | `src/components/UsageFooter.tsx` | 余额/脚本 UsageData 渲染 + `toQuotaTier` |
| 后端 command | `src-tauri/src/commands/provider.rs` | `query_provider_usage_inner` 按 template_type 分发 |
| 后端 command | `src-tauri/src/commands/subscription.rs` | `get_subscription_quota` + 写缓存/托盘 |
| 后端 command | `src-tauri/src/commands/balance.rs` | `get_balance` |
| 后端 command | `src-tauri/src/commands/coding_plan.rs` | `get_coding_plan_quota` |
| 后端 command | `src-tauri/src/commands/copilot.rs` | `copilot_get_usage[_for_account]` |
| 后端 command | `src-tauri/src/commands/codex_oauth.rs` | `get_codex_oauth_quota` |
| 后端 command | `src-tauri/src/commands/xai_oauth.rs` | `get_xai_oauth_quota` |
| 后端 service | `src-tauri/src/services/subscription.rs` | Claude/Codex/Gemini 订阅额度（凭据读取 + 上游查询） |
| 后端 service | `src-tauri/src/services/balance.rs` | DeepSeek/StepFun/SiliconFlow/OpenRouter/Novita 余额 |
| 后端 service | `src-tauri/src/services/coding_plan.rs` | Kimi/智谱/MiniMax/ZenMux/火山 Coding Plan |
| 后端 service | `src-tauri/src/services/subscription_grok.rs` | Grok gRPC-web 账单（protobuf 启发式解析） |
| 后端 service | `src-tauri/src/services/provider/usage.rs` | 脚本路径 `query_usage`/`test_usage_script` 编排 + 错误折叠 |
| 后端引擎 | `src-tauri/src/usage_script.rs` | QuickJS 沙箱执行自定义脚本 + 安全校验 |
| 后端 proxy | `src-tauri/src/proxy/providers/copilot_auth.rs` | Copilot `fetch_usage` 实现 |
| 前端编辑 | `src/components/UsageScriptModal.tsx` | 用量脚本编辑器（模板/示例/测试按钮） |