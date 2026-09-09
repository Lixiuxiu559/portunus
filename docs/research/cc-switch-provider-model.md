# cc-switch 供应商（Provider）体系研究：官方预设 vs 自定义供应商的建模方式

> 研究时间：2026-09-09。对象：`/Users/lixiuxiu/development_tool/projects/cc-switch/`（cc-switch，一个多供应商 Claude Code / Codex / Gemini 代理切换器）。
> 目的：为 portunus 的「渠道即上游」抽象（目前只有一种自定义供应商、无官方预设）提供供应商建模参考：官方供应商（Claude / Codex / DeepSeek / Gemini 等）与自定义供应商在本体中的表达差异落在哪里、分发与激活如何走。
> 源码范围：Rust 后端 `src-tauri/src/`（SQLite + Tauri command），前端 `src/`（React），前端预设根在 `src/config/*ProviderPresets.ts`。

---

## 1. 一句话结论

cc-switch 的**官方与自定义不是两个类、也不是硬编码分支，而是同一种数据（`providers` 表里的一行 `Provider`）身上的「category 标签 + 来源」不同**。官方供应商 = 前端预设模板（表单预填，`src/config/*ProviderPresets.ts`）＋ 后端种子记录（启动时写入 DB，`providers_seed.rs`），其 `settings_config` 通常是空壳（让 CLI 走原生 OAuth 登录）；自定义供应商 = 用户手填的同结构 JSON。真正的「分发机制」是**双层键**：数据层由 `category`（official/cn_official/third_party/aggregator/custom…）门控少数行为（封号保护、Codex auth.json 写入策略、UI 能力），执行层由 `AppType`（Claude/Codex/Gemini/OpenCode/…）决定把 `settings_config` 写到哪个 CLI 应用的哪个配置文件；`meta.providerType` 只标记三类特殊托管账号供应商（github_copilot / codex_oauth / xai_oauth），不是全量分发键。

---

## 2. 分层示意图

```
┌─ 前端（React）────────────────────────────────────────────────┐
│  src/config/*ProviderPresets.ts  预设=纯数据（isOfficial,      │
│    category, settingsConfig, theme/icon, endpointCandidates,  │
│    apiFormat, modelCatalog, codexChatReasoning…）             │
│          │选预设(useProviderCategory→category)                 │
│          ▼                                                    │
│  ProviderForm → payload{settingsConfig, category, meta…}      │
│  (invoke add_provider / update_provider)                      │
└──────────────┬───────────────────────────────────────────────┘
               │
┌─ 存储层（SQLite）─────────────────────────────────────────────┐
│  providers(id, app_type[PK], settings_config, category,       │  ← 官方与自定义都在这
│   meta, is_current, in_failover_queue…)                       │
│  启动时 seeds OFFICIAL_SEEDS(5条, category="official")        │
└──────────────┬───────────────────────────────────────────────┘
               │ switch(current) / db.set_current_provider
┌─ 执行层（Rust ProviderService）───────────────────────────────┐
│  switch()→switch_normal()                                     │
│   ├ backfill（live→旧current回填）                            │
│   ├ 双current落盘（本地settings + DB is_current）             │
│   └ write_preflighted_or_current_live→write_live_snapshot     │
│        match AppType:                                         │
│          Claude→~/.claude/settings.json                       │
│          Codex→~/.codex/auth.json + config.toml               │
│          Gemini→~/.gemini/settings.json + .env                │
│          OpenCode/OpenClaw/Hermes→additive 写入原生 config    │
│   category=="official" 在关键点改判（封号、auth.json 归谁管） │
└───────────────────────────────────────────────────────────────┘
```

---

## 3. 数据模型

### 3.1 Rust 侧 `Provider`（一份「可写入某 App 的配置快照」）

`src-tauri/src/provider.rs:11-44`：

```rust
pub struct Provider {
    pub id: String,
    pub name: String,
    pub settings_config: Value,        // 自由 JSON，按 AppType 结构不同
    pub website_url: Option<String>,
    pub category: Option<String>,      // 官方/第三方/自定义… 见 §3.3
    pub created_at: Option<i64>,
    pub sort_index: Option<usize>,
    pub notes: Option<String>,
    pub meta: Option<ProviderMeta>,    // 不写入 live 配置，仅存内部元数据
    pub icon: Option<String>,          // 图标名：如 "anthropic" / "openai"
    pub icon_color: Option<String>,
    pub in_failover_queue: bool,       // 是否加入故障转移队列
}
```

要点：

- **承载配置的都是同一个自由字段 `settings_config: serde_json::Value`**（`provider.rs:15`），官方与自定义没有独立结构体。它同时作为两个身份：Claude/Gemini 场景是 `~/.claude/settings.json`（`{env:{…}}`）或 `~/.gemini/settings.json` 的内容；Codex 场景是 `{auth:{…}, config:"TOML字符串"}` 两件套（`provider.rs:166-175`）。
- 注释明确写了「SSOT 模式：不再写供应商副本文件」（`provider.rs:7`），即供应商只存一份（DB），不再有副本 JSON。
- `provider_type()` 只是个便捷取法（`provider.rs:101-103`），读的是 `meta.provider_type`。

### 3.2 `ProviderMeta`——官方/特殊供应商差异的主要载体

`provider.rs:434-555`。它不是「供应商主体」，而是不落 live 配置的旁注元数据。重要的有：

| 字段 | 行号 | 作用 |
|---|---|---|
| `provider_type` | `provider.rs:547-550` | 注释原话「供应商类型标识（用于特殊供应商检测）」——值只有 `github_copilot`、`codex_oauth`、`xai_oauth` 三个 |
| `api_format` | `provider.rs:482-486` | Claude/Codex 供应商的 API 格式：`anthropic` / `openai_chat` / `openai_responses`（决定代理转发要不要做格式转换） |
| `auth_binding` / `github_account_id` | `provider.rs:487-494, 551-554` | 托管账号（Copilot / Codex OAuth）绑定 |
| `custom_endpoints` | `provider.rs:435-437` | 多端点测速后选出的端点列表 |
| `usage_script` | `provider.rs:455-456` | 用量查询脚本配置 |
| `api_key_field` | `provider.rs:492-494` | Claude 用 `ANTHROPIC_AUTH_TOKEN` 还是 `ANTHROPIC_API_KEY` |
| `is_partner / partner_promotion_key` | `provider.rs:461-468` | 商业合作商促销标记 |

`Provider` 上的便捷判定都用 `meta`：`is_codex_oauth`（`provider.rs:70-72`）、`is_xai_oauth`（`provider.rs:74-76`）、`is_github_copilot`（`provider.rs:78-81`）、`uses_managed_account_auth`（`provider.rs:83-88`，四类「托管账号认证」合起来）。

### 3.3 `category`——官方/自定义的本质标签

`Provider.category: Option<String>`（`provider.rs:20`）。前端枚举定死取值（`src/types.ts:1-9`）：

```ts
export type ProviderCategory =
  | "official"      // 官方
  | "cn_official"   // 国产官方
  | "cloud_provider"// 云服务商（AWS Bedrock 等）
  | "aggregator"    // 聚合网站
  | "third_party"   // 第三方供应商
  | "custom"        // 自定义
  | "omo" | "omo-slim";
```

**这是全体系最重要的单一字段**：UI 徽标、切换封号保护、Codex auth 写入策略都以 `category === "official"` 为权威信号（详见 §4、§6）。注意 DeepSeek 用 `cn_official`，**不享受 `official` 的任何特殊行为**——它只是「带默认填好的预设值」的模板。

### 3.4 存储：SQLite，不落盘文件

`providers` 表（`src-tauri/src/database/schema.rs:27-43`），**复合主键 `(id, app_type)`**——同一个 id 在不同 app 下是不同行，官方与自定义同表同构：

```sql
CREATE TABLE IF NOT EXISTS providers (
    id TEXT NOT NULL, app_type TEXT NOT NULL, name TEXT NOT NULL,
    settings_config TEXT NOT NULL, website_url TEXT, category TEXT,
    created_at INTEGER, sort_index INTEGER, notes TEXT,
    icon TEXT, icon_color TEXT, meta TEXT NOT NULL DEFAULT '{}',
    is_current BOOLEAN NOT NULL DEFAULT 0, in_failover_queue BOOLEAN NOT NULL DEFAULT 0,
    PRIMARY KEY (id, app_type)
)
```

另有一张 `provider_endpoints`（`schema.rs:50-58`，多端点管理，外键级联删）。`meta` 以 JSON 字符串列存（`schema.rs:39`）。旧版本还往 `~/.cc-switch/config.json` 写一份，现 SSOT 模式已不写副本（`provider.rs:7`、`provider.rs:30` 注释）。`save_provider` 整体 UPDATE（`database/dao/providers.rs:180-216`），**分类列直接落库**。

### 3.5 「当前供应商」是双写

- 设备级本地设置 `~/.cc-switch/settings_local`：`current_provider_claude / _codex / _gemini / …` 每 App 一个字段（`src-tauri/src/settings.rs:991-1004` 读、`settings.rs:1010-1023` 写）。
- DB 表的 `is_current` 列（`schema.rs:40`）。
- 两者以本地 settings 为准、取 `get_effective_current_provider` 时会校验 DB 存在性、不存在则清理回落 DB（`settings.rs:1034-1054`）。

### 3.6 前端 TS 类型

`src/types.ts:11-32` 的 `Provider` 与 Rust 侧几乎镜像（`settingsConfig: Record<string, any>`、`category?`、`meta?`、`icon/iconColor`）。`ProviderMeta` 在 `types.ts:175`。无独立「官方 Provider」类型——**再一次证明官方=数据标签而非类型**。

---

## 4. 类型分发与配置生成：双层键

### 4.1 第一层：`AppType`（决定写哪个 CLI 的哪个文件）

枚举 9 个 app（`src-tauri/src/app_config.rs:380-395`）：`Claude / ClaudeDesktop / Codex / Gemini / GrokBuild / OpenCode / OpenClaw / Hermes / Pi`。

写入分发点 `write_live_snapshot`（`src-tauri/src/services/provider/live.rs:1242-1406`），`match app_type` 全量穷举：

| AppType | 写入目标 | 代码位置 |
|---|---|---|
| Claude | `~/.claude/settings.json`（先 `sanitize_claude_settings_for_live` 剥内部字段） | `live.rs:1244-1248`、`live.rs:168-178` |
| ClaudeDesktop | 必须经切换流程写（函数内直接报错） | `live.rs:1249-1255` |
| Codex | `~/.codex/auth.json` + `config.toml`（含 model catalog），`write_codex_provider_live_with_catalog` | `live.rs:1256-1287`、`codex_config.rs:2298` |
| Gemini | `~/.gemini/settings.json` + `.env`（`write_gemini_live` 里分 OAuth/APIKey 两种） | `live.rs:1288-1291`、`live.rs:1867-1946` |
| GrokBuild | `grok_config::write_grok_provider_live` | `live.rs:1292-1294`、`grok_config.rs:366` |
| OpenCode / OpenClaw / Hermes | **additive 模式**：多个 provider 并行写入原生配置文件（opencode.json / openclaw.json / config.yaml），不是覆盖式 | `live.rs:1295-1398` |
| Pi | 走独立 `pi` 服务，此函数拒绝 | `live.rs:1399-1403` |

`is_additive_mode()`（`app_config.rs:412-420`）把 9 个 app 又切成两类：独占覆盖式（Claude/Codex/Gemini）vs 累加共存式（OpenCode/OpenClaw/Hermes/Pi），这影响切换时要不要「回填+替换」。

### 4.2 第二层：`category == "official"`（同一文件内改判断）

官方在**同一套写入路径里**改行为，而不是新路径。三处典型：

1. **Codex auth.json 归属**：`write_codex_live_for_provider`（`codex_config.rs:2799-2824`）——官方且有登录材料 → 写 `auth.json`；非官方 → 只写 `config.toml`（`experimental_bearer_token` 注入），**保留用户的 ChatGPT 登录缓存不被第三方 key 冲掉**（`codex_config.rs:2814-2816`）。这是「官方 vs 第三方」在写盘时最重要的差异。
2. **切换封号保护**：代理接管模式下切换官方 → 报错拒绝（`mod.rs:4998-5007`），除非 Codex 官方（Codex 官方向来走接管透传，「官方封号」风险倒置，`mod.rs:53-59`）。
3. **Codex catalog 工具画像**：官方 → `NativeResponses`，不走 Anthropic/chat 转换（`codex.rs:316-334`）。

还有 `is_codex_official_provider` 判定（`codex.rs:265-307`）本身也是**多层证据**：固定 id + category，或托管账号绑定 id，或无第三方 upstream 痕迹 + category，且**有存储 API key 的官方卡会退化为第三方**（`codex.rs:296-304`）。

### 4.3 `meta.providerType`：不是全量分发键

它只用于三类「托管账号」供应商的特殊产物：github_copilot / codex_oauth / xai_oauth（前端预设里就这三个取值，`claudeProviderPresets.ts:60-63`）。影响：代理转发时是否用托管账号 token 注入、是否禁止部分能力（`forwarder.rs:1163-1164` 只给非 codex_oauth/xai_oauth 走某路径；`forwarder.rs:2700` xAI OAuth 的 AuthError 不 failover）。**它不代表「这是官方」**——Codex 官方是 `category=official` + `providerType=codex_oauth` 双标记（`codexProviderPresets.ts:121-136`），普通第三方预设没有 providerType。

### 4.4 代理层的协议分发（与官方/自定义无关）

`ProviderType::from_app_type_and_config`（`forwarder.rs:576`）+ 前端镜像谓词 `providerNeedsRouting`（`src/utils/providerCapabilities.ts:116-164`）按 `api_format` / TOML `wire_api` 决定要不要经过本地代理做协议转换（anthropic↔openai_chat↔openai_responses↔gemini_native）。前端 `meta.apiFormat` 枚举见 `claudeProviderPresets.ts:49-58`。

---

## 5. 官方供应商的实验机制：「预设模板 + 启动种子」双层

### 5.1 前端预设（表单模板，纯数据）

每个 App 一个 `*ProviderPresets.ts`：`claudeProviderPresets.ts`（1546 行，名单 + 元数据）、`codexProviderPresets.ts`、`geminiProviderPresets.ts`、`openclawProviderPresets.ts` 等。预设接口（`claudeProviderPresets.ts:25-74`）关键字段：

```ts
interface ProviderPreset {
  name: string; websiteUrl: string;
  settingsConfig: object;               // 直接预填进表单 JSON
  isOfficial?: boolean;                 // 官方标记（仅 Old-style 语义）
  category?: ProviderCategory;          // 真正权威的分类
  apiFormat?: "anthropic"|"openai_chat"|"openai_responses"|"gemini_native";
  providerType?: "github_copilot"|"codex_oauth"|"xai_oauth";
  requiresOAuth?: boolean;
  apiKeyField?: "ANTHROPIC_AUTH_TOKEN"|"ANTHROPIC_API_KEY";
  templateValues?; endpointCandidates?; theme?; icon?; iconColor?;
  modelsUrl?;
  hidden?: boolean;
}
```

三个官方条目（几乎一模一样的模式，散在不同文件）：

| 官方预设 | 文件:行 | settingsConfig（空壳） |
|---|---|---|
| Claude Official | `claudeProviderPresets.ts:77-92` | `{env:{}}`，`isOfficial:true, category:"official"` |
| OpenAI Official (Codex) | `codexProviderPresets.ts:120-136` | `auth:{}, config:""`，`providerType:"codex_oauth"` |
| Google Official (Gemini) | `geminiProviderPresets.ts:35-53` | `{env:{}}`，`category:"official"`，`partnerPromotionKey:"google-official"` |

**官方的 settings_config 是空壳**——不放 base_url/key，让下游 CLI 走自己原生登录（Claude Code 的 `claude /login`、Codex CLI 的 ChatGPT 登录、Gemini CLI 的 Google OAuth）。Grok Official 同理（`providers_seed.rs:74-83` 注释「空 config = 不写自定义模型表，Grok CLI 回落到自带的 xAI OAuth 登录」）。

「国产官方」如 DeepSeek（`claudeProviderPresets.ts:874-891`）是带默认端点/模型的**纯数据预设**：`ANTHROPIC_BASE_URL=https://api.deepseek.com/anthropic`、默认模型 deepseek-v4-*、`category:"cn_official"`、`modelsUrl` 指定模型列表抓取地址。**后端没有任何 DeepSeek 专属分支**——它就是一行模板数据。Kimi / Zhipu / 火山 / 智谱等同理。

### 5.2 后端种子（启动幂等写入 DB，保证「一键切回官方」）

`database/dao/providers_seed.rs:33-84` 定死 5 条官方种子：`claude-official`、`claude-desktop-official`、`codex-official`、`gemini-official`、`grokbuild-official`（id 常量 `providers_seed.rs:14-16`），每条含固定 id、name、website_url、icon、`settings_config_json`。

启动时 `init_default_official_providers`（`database/dao/providers.rs:697-750`）：看 `official_providers_seeded` flag（`providers.rs:701-705`，幂等）→ 逐条 `Provider::with_id` → **`provider.category = Some("official")`**（`providers.rs:731`）→ 保存。入口在 `lib.rs:759`。id 固定还用于：`is_official_seed_id`（`providers_seed.rs:89-91`）判断「该 id 是不是官方种子」，供删除保护与「live 里只有官方种子时跳过导入」用（`database/dao/providers.rs:653-673`）。

### 5.3 前端预设与后端种子是两套独立来源

- 前端预设不落库、只做表单预填（选预设 → `activePreset.id` + `activePreset.category` 写进 payload，`ProviderForm.tsx:1618-1622`；category 由 `useProviderCategory` 跟随，`useProviderCategory.ts:40-43` 选「custom」词条置 custom）。
- 后端种子是 DB 里真实存在的「官方入口」行。
- 二者各自维护、字段对齐靠注释约定（`providers_seed.rs:6-10`）。UI 上官方种子行常带「官方」徽标与隐藏 base_url 展示（`ProviderCard.tsx:477`、`:699` 注释）。

---

## 6. 自定义供应商

### 6.1 表单字段

新增表单默认选「custom」预设（`ProviderForm.tsx:340`）。用户可见字段（`AddProviderDialog.tsx:163-177`）：`name`、`settingsConfig`（**JSON 编辑器**，与预设同构的 `settings_config`）、`websiteUrl`、`icon`、`iconColor`、`notes`；`category`/`meta` 由内部 `presetCategory`/`meta` 注入（`AddProviderDialog.tsx:175-176`）。Codex 自定义模板有专门的生成器：`generateThirdPartyAuth` / `generateThirdPartyConfig`（`codexProviderPresets.ts:49-72`，生成 `{OPENAI_API_KEY}` 交 `auth.json` + 一段 TOML 交 `config.toml`）。

### 6.2 存储与激活路径与官方基本一致

后端 `add`（`mod.rs:4273-4388`）对官方/自定义不区分：normalize（Claude 模型键、common config、usage script，`mod.rs:4284-4288`）→ 校验 `validate_provider_settings` → `save_provider`。差异仅在：

- 首个 provider（`current.is_none()`）自动设为 current 并写 live（`mod.rs:4377-4385`）；additive 模式的 app 是否立即写 live 由 `add_to_live` 参数控制（`mod.rs:4359-4374`）。
- 托管 Codex 的新增走交易式路径（`mod.rs:4293-4354`），那是托管账号专属、与自定义无关。

激活（切换）路径**完全同一条** `switch → switch_normal`（§7），没有「自定义 vs 官方」的分岔；差异只体现在行为门控。

### 6.3 跨应用挂载：两家形态

1. **普通自定义供应商**：按 `(id, app_type)` 归属某个 App，owner 由「在哪个 App 的弹窗里建」决定（`providers` 表复合主键 `schema.rs:42`）。
2. **UniversalProvider（统一供应商）**：跨 Claude/Codex/Gemini 复用的显式建模（`provider.rs:688-732`），字段更接近 portunus 的 Channel（`id/name/providerType/baseUrl/apiKey` + 每应用模型配置），由 `to_claude_provider/to_codex_provider/to_gemini_provider`（`provider.rs:762-907`）**展开成各 App 的常规 Provider**，再走 `sync_universal_to_apps`（`mod.rs:6705`）。预设 `universalProviderPresets.ts` 面向 NewAPI 这类多协议网关。

---

## 7. 切换/激活链路

调用链：前端 `useSwitchProviderMutation` → Tauri command `switch_provider`（`commands/provider.rs:118-133`）→ `ProviderService::switch`（`mod.rs:4943`）。

`switch` 内的前置分派（`mod.rs:4943-5033`）：

1. Pi → 独立 `pi::enable`（`mod.rs:4944-4946`）。
2. OpenCode 的 omo/omo-slim → 独立互斥路径（`mod.rs:4955-4964`）；ClaudeDesktop → 直接 switch_normal（`mod.rs:4966-4968`）。
3. 针对该 app 加切换锁（代理接管与切换互斥，`mod.rs:4974-4980`）。
4. **接管检测**：DB 有 live backup 或 live 文件含接管占位符 → 热切换（只改 DB `is_current` + 代理路由，不重写 upstream live 文件，`mod.rs:4985-5029`）。
5. **官方拦截**：接管模式下切到官方且 `!official_provider_supports_proxy_takeover` → 报错「用代理访问官方 API 可能导致账号被封禁」（`mod.rs:4998-5007`）。Codex 官方是白名单例外（`mod.rs:56-59` + `codex.rs:265-307`）。

正常路径 `switch_normal`（`mod.rs:5036-5289`）：

1. **backfill 回填**（独占模式 app）：切走前把 live 里的**可共享改动**（用户直接在 CLI 里装的插件/hook/偏好）提取成 common config 片段同步回旧 current 的 `settings_config`，并剥离 live 专用字段（`mod.rs:5067-5115`；`StripCommonConfig` 逻辑在 `live.rs`）。
2. **托管 Codex 交易**：auth/config/catalog 四文件做快照 → 预检 → 写入 → 失败整列回滚（`mod.rs:5128-5181`）。
3. **双写 current**：本地 settings + DB `is_current`（additive app 跳过，无此概念，`mod.rs:5183-5187`）。
4. **写 live**：`write_preflighted_or_current_live`（`mod.rs:3957`）→ common config 合并（`live.rs:673-702`）→ Codex OAuth 托管 token 注入（`live.rs:756-801`）→ `write_live_snapshot` 按 AppType 写（§4.1）。
5. **官方 Codex 收尾**：清除上一个第三方留在 `auth.json` 里的陈旧 key（`mod.rs:5205-5222`，仅官方切换成功且 backfill 完成时）。
6. **additive 模式**：`live_config_managed` 标记翻转（`mod.rs:5248-5275`）。
7. **MCP 重投影**：只重投影本 app 的 MCP，失败降级为警告可自愈（`mod.rs:5284-5286`）。

结果类型仅 `SwitchResult{warnings}`（`mod.rs:120-125`）；切换无事件广播给代理之外的系统，前端靠 react-query 失效重拉。**官方与自定义在这条链路上共享 100% 代码**，唯一分叉是第 5 步的官方拦截与第 8 步（§4.2）的 auth/config 写入策略。

---

## 8. 新增一家官方供应商要动哪里（以「豆包」为例）

分两种情形：

### 情形 A：豆包走现有 CLI（Claude/Codex 语法）
由于 DeepSeek/明细/智谱全是纯数据预设，豆包只要：
1. 在对应 `*ProviderPresets.ts` 追加一条预设（名字、默认 `ANTHROPIC_BASE_URL`/`GEMINI_*`、默认模型、icon/iconColor）。**0 行后端代码**。
2. 备一个图标（`src/icons/extracted/` 下单色 svg，101 个，名称即 `icon` 字段；元数据 `metadata.ts`）。
3. 若要「官方入口」级别待遇（不可删除/跟随启动种子），在 `providers_seed.rs:33` 的 `OFFICIAL_SEEDS` 加一条 + `init_default_official_providers` 自动生效。
4. 多端点测速候选 `endpointCandidates`、模型列表抓取 `modelsUrl`（claude 预设里 DeepSeek 即示范，`claudeProviderPresets.ts:874-891`）。

### 情形 B：豆包要作为全新 CLI App（新 AppType）
代码面大得多（这也是 cc-switch 的扩展性软肋——每个新 app 都是横向切割）：
1. `AppType` 枚举 + `as_str` + `is_additive_mode`（`app_config.rs:380-420`）。
2. `settings.rs` 的 current 字段 ×9（`settings.rs:991-1023` 的 match 必须穷举 AppType，Rust 编译器强制）。
3. 专用的 live 写入模块 + `write_live_snapshot` 加分支（`live.rs:1242-1406`）。
4. `Provider::resolve_usage_credentials` 的 match 追加分支（`provider.rs:163-240`，新增 AppType 会编译失败——这是刻意的穷尽设计，`provider.rs:225` 注释）。
5. 前端 `useProviderCategory` 预设查找追加 app（`useProviderCategory.ts:48-91`）。
6. 各表单/图标/i18n/托盘…

新增**官方**还要额外考虑：是否需要 OAuth 托管（涉及 `auth.rs`/`codex_oauth_auth.rs` 这类注入基建）、是否有特殊鉴权（如 `copilot_auth.rs`、`xai_oauth_auth.rs`）。

**结论**：以现有 app 挂载方式新增官方预设 ≈ 1~2 个纯数据文件，扩展性极好；新开一个 CLI app 是横切一刀，成本高——这解释了为什么 cc-switch 一直在加 app（9 个）而非给每个厂商写后端。

---

## 9. 对 portunus 的借鉴点

portunus 现状（`backend/channel/channel.go:10-19`）：

```go
type Channel struct {
    ID int64; Name string; Type protocol.Provider;  // openai/openai_responses/anthropic/gemini（protocol.go:6-11）
    BaseURL string; Key string; AutoSync bool
}
```

即「只有一种自定义供应商」：手填 base_url + key + 协议类型，无分类、无预设、无官方概念。模型在 `backend/model/` 按渠道归属并带默认价（`backend/model/service.go:105-139`）。对照 cc-switch：

### 9.1 可直接搬

- **官方预设 = 种子数据 + 表单模板，不写后端分支**（cc-switch §5）。portunus 可给 Channel 增加一列 `category`（official/cn_official/third_party/custom…），服务端维护一张 seed 表（厂家名、默认 base_url、默认端点路径、内置模型价），前端新建渠道时按厂家预填 base_url + 一键导入模型，**协议转换完全复用现有 `protocol` 包**，不需要每个厂商一套后端逻辑。DeepSeek 在 cc-switch 里就是一行模板——portunus 的 DeepSeek 渠道也应同理。
- **幂等种子标记**：`official_providers_seeded` flag（`providers.rs:701-705`）防重复 seed 的模式可直接抄。
- **category 标签驱动 UI/能力开关而非路由**：`category==="official"` 只影响提示、徽标、封号警告，不影响请求怎么写（`ProviderCard.tsx:279-294` 明确「官方判定只认显式 category」）。portunus 若加官方渠道预置，分类只用于管理端展示与「官方/第三方」提示，不进转发路径。
- **settings_config 归一化到单实体**：cc-switch 把各协议差异全塞进同一个 `Provider.settings_config` 自由 JSON，靠 `AppType` 解析。portunus 已有更强的约束（Type 枚举 + BaseURL/Key 显式字段），预设只需做「默认值注入」即可，比 cc-switch 更干净。

### 9.2 需改造

- **官方 OAuth 类**（Claude/Codex/Gemini 空壳配置走 CLI 原生登录）**不适用**：portunus 是服务端网关，没有本地 CLI 的 OAuth 交互面；「官方渠道」在这里仍必须给 base_url + key。借鉴价值在**分类建模**而非认证形态。
- **provider_type 特殊供应商检测**：借鉴「用显式标记而非启发式猜 URL」。cc-switch 的教训正反面都有：Gemini 认证检测当年靠 name/URL 关键词猜（`gemini_auth.rs:37-80`，`packycode`/`google` 字符串硬编码）是反模式；后来官方判定钉死 `category==="official"`（`ProviderCard.tsx:279-286`）。未来若 portunus 有需要特殊鉴权的上游（AWS Bedrock 签名、火山方舟控制面），应加 `auth_type` 显式列，不要解析 base_url 猜。
- **端点候选/多端点测速**（`endpointCandidates` + `provider_endpoints` 表 + `meta.custom_endpoints`）对 portunus 有启发但需裁剪：portunus 一个 channel 单个 base_url 模型简单，若要支持「测速挑最优端点」可加冗余地址列表，但语义已是「按渠道分裂」而非 cc-switch 的「按 vendor 换 endpoint」。
- **Common config 片段**（`live.rs:673-702` 把用户在自己 CLI 里加的插件/hook 与厂商配置合并剥离）：对应 portunus 的「用户改渠道会被自动同步覆盖吗」问题；portunus 无 live 文件场景，若未来加「网关级公共片段」再参考。

### 9.3 不适用（场景不匹配，别硬搬）

- **切换/激活全链路**（`switch_normal` 的 backfill、双 current、快照回滚、托管 Codex 交易，`mod.rs:5036-5289`）：portunus 渠道常驻 DB、无 live 配置文件、「当前渠道」由分组策略决定，没有「激活时改写某 CLI 配置」这回事。
- **additive vs switch mode**（`app_config.rs:412-420`）、**代理接管热切换**（`mod.rs:4985-5029`）：都是「客户端 CLI 配置文件所有权」特有概念。
- **封号保护**（`mod.rs:4998-5007`）：portunus 自建网关，无「代理官方 API 会被封号」语义。
- **Claude 插件同步 / 托盘菜单 / MCP 投影**（`useProviderActions.ts:58-82`，`mod.rs:5284-5286`）：桌面客户端专属。

一句话：**抄「官方 = 种子预设数据 + 分类标签」，不抄「切换激活机器」**。portunus 的本地等价物是把 `channel.Type` 枚举保持为纯协议、新增 `category` 与 `official_seed` 两列 + 一张厂家预设表，让管理端新建渠道的体验从「手填 base_url」升级为「从预设厂商里挑」，存出来的仍是同一张 Channel 行。

---

## 10. 关键源码索引

| 主题 | 文件 / 行号 |
|---|---|
| `Provider` 结构体 | `src-tauri/src/provider.rs:11-44` |
| `ProviderMeta`（provider_type/api_format/custom_endpoints…） | `provider.rs:434-555` |
| provider_type 特殊判定（codex_oauth/xai_oauth/github_copilot） | `provider.rs:70-88`、`provider.rs:101-103` |
| `ProviderManager` / `UniversalProvider` | `provider.rs:251-254`、`provider.rs:688-732` |
| 前端 `Provider` / `ProviderCategory` 类型 | `src/types.ts:1-9`、`src/types.ts:11-32` |
| providers 表 / provider_endpoints 表 | `src-tauri/src/database/schema.rs:27-60` |
| 官方种子定义 OFFICIAL_SEEDS | `src-tauri/src/database/dao/providers_seed.rs:33-84`（id 常量 14-16） |
| 启动种子写入（category="official"） | `src-tauri/src/database/dao/providers.rs:697-750`（flag 701-705、category 731） |
| 官方种子 id 判定 | `providers_seed.rs:89-91` |
| AppType 枚举 / additive 模式 | `src-tauri/src/app_config.rs:380-420` |
| 按 AppType 写 live（主分发） | `src-tauri/src/services/provider/live.rs:1242-1406` |
| Claude sanitize | `live.rs:168-178` |
| Gemini 写 live（OAuth/APIKey 分型） | `live.rs:1867-1946`、`gemini_auth.rs:37-80` |
| Codex auth/config 写盘策略（官方 vs 第三方） | `src-tauri/src/codex_config.rs:2799-2824` |
| Codex official 判定 | `src-tauri/src/proxy/providers/codex.rs:265-307` |
| 代理协议分发 ProviderType | `src-tauri/src/proxy/forwarder.rs:576`、`src/utils/providerCapabilities.ts:116-164` |
| 默认 current 双写 | `src-tauri/src/settings.rs:991-1023`（effective 校验 1034-1054） |
| switch 入口 / switch_normal | `src-tauri/src/services/provider/mod.rs:4943`、`5036-5289` |
| 接管检测与官方拦截 | `mod.rs:4985-5007`、`official_provider_supports_proxy_takeover` `mod.rs:56-59` |
| backfill 回填 | `mod.rs:5067-5115` |
| common config 合并 | `live.rs:673-702` |
| Codex OAuth 托管 token 注入 | `live.rs:756-867` |
| `add` / `update` / `delete` | `mod.rs:4273-4388`、`4391`、`4800` |
| Tauri command `switch_provider` | `src-tauri/src/commands/provider.rs:118-133` |
| 前端预设文件 | `src/config/claudeProviderPresets.ts`、`codexProviderPresets.ts`、`geminiProviderPresets.ts`、`universalProviderPresets.ts` |
| 官方条目（三例） | `claudeProviderPresets.ts:77-92`、`codexProviderPresets.ts:120-136`、`geminiProviderPresets.ts:35-53` |
| DeepSeek 预设（纯数据示范） | `claudeProviderPresets.ts:874-891` |
| 自定义预设置 category="custom" | `useProviderCategory.ts:40-43` |
| 表单提交 payload 组装 | `ProviderForm.tsx:1590-1636`、`AddProviderDialog.tsx:163-177` |
| 官方徽标 / SSOT 判定说明 | `ProviderCard.tsx:85-118`、`279-294` |
| 前端切换拦截 | `useProviderActions.ts:276-291` |
| 图标资源 | `src/icons/extracted/`（101 个 svg/png）+ `metadata.ts` |
| portunus Channel 现状 | `backend/channel/channel.go:10-19`、`backend/protocol/protocol.go:6-11` |