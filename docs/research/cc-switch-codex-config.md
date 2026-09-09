# Codex CLI 第三方网关配置研究：cc-switch 的模板与写盘机制 + Codex 0.145.0 键语义核实 + portunus 客户端配置诊断

> 研究时间：2026-09-09。对象：① `/Users/lixiuxiu/development_tool/projects/cc-switch/`（cc-switch 源码）；② Codex CLI 官方源码（github.com/openai/codex，tag `rust-v0.145.0`——与本机安装版本一致）；③ 本机 `~/.codex/` 实际配置。
> 起因：用户感觉 portunus 桌面端「客户端配置」写出的 Codex 配置不对。本文核实「正确的第三方 Codex 配置长什么样」，并对照 portunus 当前实现（`web/src/main/client-config.js:15-21` 的 `CODEX_DEFAULT`、`web/src/renderer/pages/Clients.jsx:55-78` 的正则点改）找出差距。
> 说明：Codex 官方 `docs/config.md` 在 main 分支已缩减为指针页（指向 developers.openai.com/codex 的 config-reference，WebFetch 抓取 403），因此键语义以 **rust-v0.145.0 tag 的源码 + 本机二进制 strings** 为准——这比网页文档更精确，且与本机 `codex-cli 0.145.0` 逐字对应。

---

## 1. 一句话结论

Codex CLI（0.145.0）指向第三方网关的最小正确配置是：**顶层 `model_provider` 指向一个自定义 `[model_providers.<id>]` 表，表内必须有 `base_url` + `wire_api = "responses"`（chat 协议已在本版本被移除，省略时默认 responses 但显式写更稳）+ 一个鉴权来源（三选一：表内 `experimental_bearer_token`、表内 `env_key`、或 `requires_openai_auth = true` 走 `auth.json`）**。cc-switch 所有第三方预设用的就是这套：key 放 `auth.json` 的 `OPENAI_API_KEY`、TOML 四件套 `model_provider/model/wire_api/requires_openai_auth` + `base_url`。portunus 当前的 `CODEX_DEFAULT` 模板只写了 `model_provider` 和 `name` 两个键——**没有 `base_url`（Codex 会静默回落到 `https://api.openai.com/v1`）、没有任何鉴权键（默认不发 Authorization 头，必 401）**，且 UI 的正则点改在键不存在时是静默 no-op（假成功）。

---

## 2. cc-switch 的 Codex 配置机制

### 2.1 Codex 的 settings_config 是「auth.json + config.toml」两件套

`src-tauri/src/provider.rs:161-176`（`resolve_usage_credentials` 的 Codex 分支），注释原话：

> Codex keeps its key in `auth.OPENAI_API_KEY` and its base URL inside a TOML `config` string, not in an `env` map.

即 Codex 供应商的 `settings_config` = `{ auth: {...写 auth.json}, config: "TOML 字符串→写 config.toml" }`，与 Claude 的 `{env:{...}}` 完全不同构。

### 2.2 第三方模板全文（`src/config/codexProviderPresets.ts`）

`generateThirdPartyAuth`（`:48-55`）——生成 **auth.json** 的内容，就一个键：

```ts
export function generateThirdPartyAuth(apiKey: string): Record<string, any> {
  return { OPENAI_API_KEY: apiKey || "" };
}
```

`generateThirdPartyConfig`（`:57-77`）——生成 **config.toml** 的 TOML 字符串（所有第三方预设共用，20+ 家厂商全是它生成的）：

```toml
model_provider = "custom"
model = "gpt-5.6-sol"                # 模板参数，按厂商替换
model_reasoning_effort = "high"
disable_response_storage = true

[model_providers.custom]
name = "kimi"                        # 模板参数
base_url = "https://api.moonshot.cn/v1"   # 模板参数
wire_api = "responses"
requires_openai_auth = true
```

要点：

- **预设的 config.toml 里从不写 `experimental_bearer_token`**——key 的正典位置是 `auth.json`（接口注释 `codexProviderPresets.ts:19-20`：「auth: 将写入 ~/.codex/auth.json；config: 将写入 ~/.codex/config.toml」）。
- 每家都显式写 `wire_api = "responses"`（含硬编码模板的 APINebula `:296-306`、APIKEY.FUN `:471-481`、SudoCode `:1000-1010` 等），无一省略。`ProviderForm.tsx:709` 注释：「wire_api is always "responses" for Codex; format controls proxy-layer conversion」。
- 唯一用 `env_key` 的是 Azure 预设（`:1081-1092`）：`env_key = "OPENAI_API_KEY"` + `query_params = { "api-version" = "..." }`——Azure 需要 api-version 查询参数的特殊形态，不是第三方常态。
- 官方 OpenAI 预设是空壳（`:120-136`：`auth:{}, config:"""`），让 CLI 走自己的 ChatGPT 登录。
- 新建自定义供应商的默认模板 `codexTemplates.ts:18-31`（`getCodexCustomTemplate`）与上面四件套完全一致，`auth: { OPENAI_API_KEY: "" }`。

### 2.3 写盘策略：官方 vs 第三方（`src-tauri/src/codex_config.rs`）

核心分派 `write_codex_live_for_provider`（`:2799-2830`），doc 注释（`:2793-2798`）：

> Official providers with usable login material own `auth.json`. Third-party providers only touch `config.toml` when the compatibility setting is enabled so the user's ChatGPT login cache survives provider switches.

```rust
let should_write_auth = (category == Some("official") && codex_auth_has_login_material(auth))
    || (category != Some("official")
        && !crate::settings::preserve_codex_official_auth_on_switch());
if should_write_auth {
    write_codex_live_atomic(auth, config_text)          // auth.json + config.toml 都写
} else {
    let live_config = prepare_codex_provider_live_config(auth, config_text)?;
    write_codex_live_config_atomic(Some(&live_config))  // 只写 config.toml
}
```

两条路径：

1. **写 auth.json 路径**（官方有登录材料，或第三方且未开「保留官方登录」开关）：`write_codex_live_atomic`（`:772-813`）原子写两文件，config 写失败回滚 auth.json。
2. **config-only 路径**（第三方 + `preserve_codex_official_auth_on_switch` 开启）：`prepare_codex_provider_live_config`（`:2832-2841`）把 `auth.OPENAI_API_KEY` 的值（或 config 里已有的 bearer，`:890-894` 的 `extract_codex_api_key` 就是 auth 优先、回落 bearer）注入 config.toml 的 `experimental_bearer_token`，**auth.json 一个字节不动**。doc 注释（`:2826-2831`）：

   > The stored provider keeps its API key in `auth.OPENAI_API_KEY`. Live Codex requests can use a provider-scoped `experimental_bearer_token`, so switching providers only needs to update `config.toml`; `auth.json` stays as the user's long-lived ChatGPT login cache.

- 开关默认值是 **false**（`settings.rs:527`），即 cc-switch 第三方切换**默认会写 auth.json**；「保留 ChatGPT 登录」是用户显式开启的兼容模式。
- `experimental_bearer_token` 的写入位置有讲究：`set_codex_experimental_bearer_token`（`:2348-2387`）——active provider 是自定义 id 时写进 `[model_providers.<id>]` 表内；是保留 id（`CODEX_RESERVED_MODEL_PROVIDER_IDS`，`:344-351`：openai/amazon-bedrock/ollama/lmstudio/oss/ollama-chat）或无 active provider 时写顶层（那是「Mobile 兼容」历史形态，`:2312-2317` 注释；**Codex 本体只消费 provider 表内的该键**，顶层键在官方 schema 里不存在，见 §3.4）。
- model catalog：`write_codex_provider_live_with_catalog`（`:2298-2307`）在写盘前按 DB 里的内联 `modelCatalog` 生成 `model_catalog_json` 指向的 JSON 文件——那是 cc-switch 的增强（模型列表/工具画像），Codex 0.145.0 原生支持 `model_catalog_json` 顶层键但非必需，第三方最小配置不含它。

### 2.4 cc-switch 怎么读回配置（对照 portunus 正则的参照系）

- `extract_codex_base_url`（`:896-920`）doc 注释明确：「Prefers the active `[model_providers.<model_provider>].base_url`… **Deliberately never reads a non-active `[model_providers.*]` section**」——读 base_url 必须先解析顶层 `model_provider` 再定位对应表。
- 前端 `src/utils/providerConfigUtils.ts:441-443` 用正则提取 `experimental_bearer_token`、`:877-885` 写 `wire_api`，但都配合 provider 归属判断（`:1103-1131`「provider-scoped 优先，回落顶层」）。
- 表单回填逻辑 `useCodexConfigState.ts:19`：「auth.json 缺 OPENAI_API_KEY 时回退到 config.toml 的 experimental_bearer_token」。

---

## 3. Codex CLI 0.145.0 官方键语义核实

来源标注：本机 `codex-cli 0.145.0`（npm `@openai/codex@0.145.0`，darwin-arm64）。以下全部取自该版本 tag 源码 `github.com/openai/codex@rust-v0.145.0`，并用本机二进制 strings 交叉验证。

### 3.1 `[model_providers.<id>]` 支持的字段全集

`codex-rs/model-provider-info/src/lib.rs:87-141`，`ModelProviderInfo`（标注 `#[schemars(deny_unknown_fields)]`）：

| 键 | 类型/默认 | 语义（原文/摘译） |
|---|---|---|
| `name` | String，默认 `""` | 显示名（`:90-92`） |
| `base_url` | Option\<String\>，**无默认值** | 省略时回落：ChatGPT 系 auth 模式 → `https://chatgpt.com/backend-api/codex`，否则 → `https://api.openai.com/v1`（`:241-259`） |
| `env_key` | Option\<String\> | 「Environment variable that stores the user's API key for this provider」；运行时读环境变量，缺失即报错（`:283-299`） |
| `env_key_instructions` | Option | env_key 的提示文案 |
| `experimental_bearer_token` | Option\<String\> | 「Value to use with `Authorization: Bearer <token>` header. **Use of this config is discouraged in favor of `env_key` for security reasons**, but this may be necessary when using this programmatically.」（`:101-104`） |
| `auth` | Option（command-backed） | 新形态：外部命令取 token；与 env_key / experimental_bearer_token / requires_openai_auth 互斥（`validate` `:185-212`） |
| `aws` | Option | SigV4；与上述全部互斥（`:154-183`） |
| `wire_api` | enum，**默认 `responses`** | 见 §3.2 |
| `query_params` / `http_headers` / `env_http_headers` | map | 追加查询参数 / 固定头 / 从环境变量取值的头 |
| `request_max_retries` 等 | 默认 4 / 5 / 300000ms / 15000ms | 重试与超时（`:26-33`） |
| `requires_openai_auth` | bool，**默认 false** | 「If true, user is presented with login screen on first run, and login preference and token/key are stored in auth.json. **If false (which is the default), login screen is skipped, and API key (if needed) comes from the `env_key` environment variable.**」（`:132-137`） |
| `supports_websockets` | bool，默认 false | Responses over WebSocket |

内置 provider 只有 4 个：`openai`（requires_openai_auth=true）、`amazon-bedrock`、`ollama`、`lmstudio`（`:432-459`）；用户配置**只能新增、不能覆盖内置 id**（`merge_configured_model_providers` 用 `entry().or_insert()`，`:497-499`）。schema 描述原话：「User-defined provider entries that extend the built-in list. Built-in IDs cannot be overridden.」

### 3.2 `wire_api`：chat 已死

`codex-rs/model-provider-info/src/lib.rs:54-84`：

- 枚举只剩一个变体：`Responses`，且标 `#[default]`——**省略 wire_api = responses**（不再是旧文档的「默认 chat」）。
- 反序列化时 `"chat"` 直接报错（`:80` + `:50` 常量）：`` `wire_api = "chat"` is no longer supported.\nHow to fix: set `wire_api = "responses"` in your provider config.\nMore info: https://github.com/openai/codex/discussions/7782 ``（本机二进制 strings 逐字含此错误文案）。
- 其他未知值报 unknown_variant 错误。结论：写不写 `wire_api = "responses"` 在 0.145.0 行为一致，但**显式写是唯一向后兼容老版本、向前防回归的姿势**（cc-switch 全部预设都写）。

### 3.3 鉴权解析顺序（本版本最关键的一张表）

请求头构建在 `codex-rs/model-provider/src/auth.rs`：

```
resolve_provider_auth (:189-207)
 ├─ ① bearer_auth_for_provider (:271-284)
 │     ├─ env_key 指向的环境变量非空 → Authorization: Bearer <env值>（变量缺失直接报错）
 │     └─ experimental_bearer_token（provider 表内）→ Authorization: Bearer <token>
 │   ② 都没有 → 用会话 auth（auth.json 加载出的 CodexAuth）：
 │     ApiKey / Chatgpt / ChatgptAuthTokens / PersonalAccessToken → Bearer token
 │     （ChatGPT 系还会附带 ChatGPT-Account-ID 头，bearer_auth_provider.rs:25-36）
 │   ③ 会话 auth 也没有 → UnauthenticatedAuthProvider：一个鉴权头都不发（:154-158）
```

即：**provider 表内的 `env_key` > `experimental_bearer_token` > `auth.json`（ApiKey 或 ChatGPT tokens）> 无鉴权**。`requires_openai_auth` 不改变这个优先级——它只是让 provider 走「一方 auth 路径」（`provider.rs:224-230` 的 `provider_uses_first_party_auth_path`：requires_openai_auth 且无 env_key/bearer/auth/aws 时，凭据来自 auth.json；为 true 且 auth.json 缺失时首跑弹登录框）。

auth.json 的结构（`codex-rs/login/src/auth/storage.rs:40-60`）：`{ auth_mode?, OPENAI_API_KEY?, tokens?, last_refresh?, agent_identity?, personal_access_token?, bedrock_api_key? }`。只含 `{"OPENAI_API_KEY": "..."}` 的文件经 `resolved_mode`（`codex-rs/login/src/auth/manager.rs:1491-1506`：openai_api_key 非空 → `AuthMode::ApiKey`）解析为 ApiKey 模式（`from_auth_dot_json` `:249-263` → `CodexAuth::from_api_key`）——这正是 `codex login --api-key` 官方写法（`login_with_api_key` `:904-918` 只写这一个键）。

环境变量侧（`manager.rs:836-858`）：`OPENAI_API_KEY` / `CODEX_API_KEY` / `CODEX_ACCESS_TOKEN`。注意 **`preferred_auth_method` 已在 0.145.0 移除**（二进制 strings 0 命中；config.schema.json 顶层 93 键中亦无）——旧教程里的这个键不要再抄。

### 3.4 顶层键

- `model_provider`：默认 **`"openai"`**（`codex-rs/core/src/config/mod.rs:3530-3532`：`model_provider.or(cfg.model_provider).unwrap_or_else(|| "openai".to_string())`）；指向不存在的 id 报 `Model provider \`X\` not found`（`:3538-3540`）。
- 顶层**没有** `experimental_bearer_token`（config.schema.json 顶层 93 键验证，ABSENT）——bearer token 必须写在 `[model_providers.<id>]` 表内。serde 运行时对未知顶层键是静默忽略的（deny_unknown_fields 只进 JSON Schema，用于编辑器校验），**拼错键名不会报错、只是无效**，这是配置排障的隐形坑。
- 无必填顶层键（schema `required: []`）。

---

## 4. 本机 `~/.codex` 现状诊断（token 一律脱敏）

环境：`codex-cli 0.145.0`（nvm node v24.15.0 全局）。`~/.codex/` 内与 Codex 本体相关的文件：`config.toml`、`auth.json`、`config.toml.portunus.bak`、`cc-switch-model-catalog.json`（14 万字节，cc-switch 生成的模型目录）、`history.jsonl`、`sessions/` 等。

### 4.1 当前 `config.toml`（mtime 2026-09-09 18:33，关键部分摘录）

```toml
model_provider = "custom"
model = "gpt-5.6-luna"
model_reasoning_effort = "high"
disable_response_storage = true
model_catalog_json = "cc-switch-model-catalog.json"

[model_providers.custom]
name = "custom"
wire_api = "responses"
requires_openai_auth = true
base_url = "http://127.0.0.1:13060/v1"
# …… 其余为 mcp_servers / projects / hooks.state，与鉴权路由无关
```

### 4.2 `auth.json`（mtime 2026-09-09 16:10）

```json
{ "OPENAI_API_KEY": "sk-acw…(len=32)" }
```

只含 OPENAI_API_KEY → ApiKey 模式。**总长 32 字符（sk- + 29）与 portunus 签发的 key 不符**（portunus 的 key 是 `sk-` + 24 字节 base64url = 32 字符，总长 35，`backend/shared/apikey.go:17-23`），更像是 cc-switch 当前第三方供应商的中转 key（cc-switch 第三方切换默认写 auth.json，§2.3）。

### 4.3 `config.toml.portunus.bak`（mtime 2026-09-09 10:35，portunus 写前备份）

```toml
model_provider = "custom"
…
[model_providers.custom]
name = "custom"
wire_api = "responses"
requires_openai_auth = true
base_url = "http://127.0.0.1:15721/v1"
experimental_bearer_token = "PROXY_MANAGED"
model_catalog_json = "cc-switch-model-catalog.json"
```

这是 cc-switch 更早一代的「代理接管」形态（本地代理端口 15721 + `PROXY_MANAGED` 占位 token，真实 key 由 cc-switch 代理按请求注入）。

### 4.4 诊断结论

1. **当前 Codex 完全由 cc-switch 托管，与 portunus 无关**：base_url 指向 cc-switch 本地代理（127.0.0.1:13060），鉴权走 auth.json（requires_openai_auth=true + 无 bearer 时按 §3.3 ②取 auth.json）。就 cc-switch 自身的语义这套配置是自洽的。
2. **auth.json 里的 key（sk-acw…，32 位）不是 portunus 的 key 格式**（≈25 位）。就算把 base_url 改成 portunus 服务器，这个 key 也大概率过不了 portunus 的 APIKey 鉴权——「上游地址」与「凭据」来自两个不同体系，是当前配置最可疑的一点。
3. **当前 config.toml 里没有 `experimental_bearer_token` 键** → portunus 客户端「导入令牌」按钮的正则点改（`Clients.jsx:73-78`）不命中、**静默假成功**（toast 提示成功但文件未变）；「填 URL」按钮能命中（base_url 键存在），但改的是 cc-switch 的 `[model_providers.custom]`，且 `model_provider = "custom"`、`requires_openai_auth = true` 原样保留——改完鉴权仍走 auth.json 的旧 key，不是 portunus 令牌。
4. `config.toml.portunus.bak` 证明 portunus 客户端确实写盘过一次（当时备份的是 cc-switch 接管形态的配置），随后 cc-switch 又整体重写覆盖——**两个工具抢同一份文件、后写者赢**，用户感知到的「配置不对/漂移」很大程度源于此。
5. 若想让 Codex 直连 portunus：需要 `model_provider` 指向 `base_url = <portunus>/v1` 的 provider + portunus 签发的 key + `wire_api = "responses"`（portunus 已有 `/v1/responses` 路由，`backend/gateway/gateway.go:21-23`）。

---

## 5. 对 portunus 的修正建议

### 5.1 `CODEX_DEFAULT` 应改成什么样

现状（`web/src/main/client-config.js:15-21`）只有 `model_provider` + `name`，按 §3.1/§3.3 逐项对表：缺 `base_url`（Codex 静默回落 api.openai.com）、缺鉴权（requires_openai_auth 默认 false → 不发任何鉴权头）、缺 `model`（Codex 用内置默认 gpt-5.x 系，portunus 无此模型必 404/400）、`disable_response_storage` 未关（Codex 会发 store 相关字段，第三方网关应关）。

建议模板（鉴权选 **bearer token 内嵌** 路线，理由见 §5.3）：

```toml
model_provider = "portunus"
# model 必须是 portunus 分组里真实存在的模型名
model = "在此填入 portunus 中的模型名"
model_reasoning_effort = "high"
disable_response_storage = true

[model_providers.portunus]
name = "Portunus"
base_url = "http://127.0.0.1:3060/v1"
wire_api = "responses"
experimental_bearer_token = ""
```

设计意图：

- **模板必须预置 `base_url` 和 `experimental_bearer_token` 两个键**——这是让 `Clients.jsx:65-78` 的正则点改能命中的前提（`String.replace` 不命中就是 no-op）。base_url 占位用 portunus 默认本地端口 3060；`experimental_bearer_token = ""` 空占位由「导入令牌」填入（保存前应校验非空，见 §5.2）。
- `wire_api = "responses"` 显式写：与 cc-switch 全部预设对齐，0.145.0 上是冗余但向后兼容老版本。
- **不写 `requires_openai_auth`**（默认 false）：否则 Codex 认为该 provider 需要 auth.json 凭据，首跑弹登录框（`model-provider-info/src/lib.rs:132-137`），而我们的鉴权实际由 bearer token 承担（§3.3 ①优先级保证 bearer 生效）。
- `model` 键是必须补的（现状模板没有）：后续可在 UI 加模型下拉（分组模型列表 API 已有）；短期至少在模板注释/占位提示。

### 5.2 `Clients.jsx` 正则点改要跟着动的地方

1. **键缺失时的行为**：`applyBaseUrl`/`applyToken`（`:65-78`）不命中时静默 no-op，但 `handleFillUrl`（`:234-238`）/`handleImportToken`（`:240-249`）照样 toast 成功。模板补键后此问题缓解，但仍建议：替换 0 命中时给出明确警告或自动插入键值对（cc-switch 用 toml_edit 保结构写入，`codex_config.rs:2348-2387`，portunus 已依赖 smol-toml 可解析后定点插值）。
2. **读取应锚定 active provider**：`readCodexBase`/`readCodexToken`（`:55-62`）用 `/^\s*base_url\s*=/m` 取**全文第一个**匹配——配置里有多个 provider 段时可能读到非 active 段。参照 cc-switch `extract_codex_base_url` 的注释（§2.4）：先读顶层 `model_provider`，再定位 `[model_providers.<id>]` 内的键。
3. **hint 文案与实现对齐**：`:577-581` 说「写入 `[model_providers.portunus]` 段并激活 `model_provider = "portunus"`，其余 provider 原样保留」——但点改逻辑从不写这两个东西，只原位替换 base_url/token 值。模板补齐后文案可成立，但更准确的表述是「模板预置 provider 表骨架，UI 只点改 base_url 与令牌两个值」。
4. （可选）保存前校验：令牌为空占位时提醒「Codex 将以空 Bearer 调用，必 401」；base_url 为空串时同理（空串是 Some("")，不会回落默认，直接请求失败）。

### 5.3 要不要碰 auth.json：不要

三个理由：

1. **技术上不需要**：provider 表内 `experimental_bearer_token` 的优先级高于 auth.json（§3.3），写 config.toml 即完成鉴权，auth.json 里是什么（用户的 ChatGPT 登录缓存或旧 key）都不影响。
2. **安全上不该碰**：覆盖 auth.json 会冲掉用户 ChatGPT 登录缓存——cc-switch 为此专门做了 `preserve_codex_official_auth_on_switch` 机制，把「第三方 key 走 bearer token 注入、auth.json 不动」作为保护路径（`codex_config.rs:2793-2841`，§2.3）；portunus 作为后装的第三方工具，更没有理由破坏用户的官方登录态。
3. **官方正道存在但不由 portunus 代劳**：把 key 写进 auth.json 的官方途径是 `codex login --api-key sk-xxx`（`login_with_api_key`，`manager.rs:904-918`）。若用户偏好 cc-switch 主流形态（`requires_openai_auth = true` + auth.json），可在文档里引导用户手动执行，而不是 portunus 直接改写。

### 5.4 顺带的共管问题

`config.toml.portunus.bak` 与当前 config.toml 的对比说明 cc-switch 与 portunus 在抢同一份文件。portunus 客户端至少应在 Codex 面板展示「检测到 model_provider 指向非 portunus 的 provider（如 cc-switch 的 custom）」的提示，避免用户在 portunus 里改了 base_url 却被 cc-switch 下次切换整体覆盖、或反之。

---

## 6. 关键源码索引

| 主题 | 位置 |
|---|---|
| cc-switch 第三方 auth.json 模板 | `src/config/codexProviderPresets.ts:48-55` |
| cc-switch 第三方 config.toml 模板（四件套） | `codexProviderPresets.ts:57-77`（硬编码变体 ：296-306、:471-481、:1000-1010） |
| 官方 OpenAI 空壳预设 | `codexProviderPresets.ts:120-136` |
| Azure env_key + query_params 特例 | `codexProviderPresets.ts:1081-1092` |
| 自定义供应商默认模板 | `src/config/codexTemplates.ts:18-31` |
| Codex settings_config 两件套注释 | `src-tauri/src/provider.rs:161-176` |
| 官方 vs 第三方写盘分派 | `src-tauri/src/codex_config.rs:2799-2830` |
| config-only + bearer 注入（保 ChatGPT 登录） | `codex_config.rs:2832-2841`（extract 回落 ：890-894） |
| 原子写两文件 / 只写 config | `codex_config.rs:772-813` / `:869-881` |
| bearer token 写入位置（provider 内 vs 顶层） | `codex_config.rs:2348-2387`（提取 :2318-2346；保留 id :344-351） |
| base_url 只认 active provider | `codex_config.rs:896-920` |
| preserve 开关（默认 false） | `src-tauri/src/settings.rs:387, :527, :965-975` |
| portunus 现模板 / 正则点改 | `web/src/main/client-config.js:15-21`；`web/src/renderer/pages/Clients.jsx:55-78, :234-249, :575-581` |
| ModelProviderInfo 字段全集 | `codex-rs/model-provider-info/src/lib.rs:87-141`（tag rust-v0.145.0） |
| wire_api：仅 responses、chat 报错 | `model-provider-info/src/lib.rs:50, :54-84`（公告 discussions/7782） |
| requires_openai_auth / experimental_bearer_token 语义 | `model-provider-info/src/lib.rs:132-137` / `:101-104` |
| base_url 省略时的回落 | `model-provider-info/src/lib.rs:241-259` |
| 鉴权优先级（env_key > bearer > auth.json > 无） | `codex-rs/model-provider/src/auth.rs:271-284, :189-207, :154-158`；`provider.rs:224-230` |
| auth.json 结构 / ApiKey 解析 / login --api-key | `codex-rs/login/src/auth/storage.rs:40-60`；`manager.rs:249-263, :904-918, :1491-1506` |
| model_provider 默认 "openai" | `codex-rs/core/src/config/mod.rs:3526-3545` |
| config schema（顶层无 bearer token） | `codex-rs/core/config.schema.json`（93 顶层键） |
| portunus 网关路由（/v1/responses 存在） | `backend/gateway/gateway.go:21-23` |
| portunus key 格式（sk- + 32 位 = 35 字符） | `backend/shared/apikey.go:17-23` |
