# cc-switch 读写 Claude Code / Codex 配置文件机制研究

> 研究时间：2026-09-03。对象：`/Users/lixiuxiu/development_tool/projects/cc-switch/`（cc-switch v3.20.0，多供应商 Claude Code / Codex / Gemini 等代理切换器）。
> 目的：为 portunus 管理 LLM 客户端配置（切换 base_url / API key / 模型映射）提供参考，重点厘清 cc-switch 到底读写哪些磁盘文件、路径如何构造、写入是否原子、有无备份与还原。
> 源码范围：后端 `src-tauri/src/`（Rust + Tauri，重点 `config.rs`、`codex_config.rs`、`claude_desktop_config.rs`、`claude_mcp.rs`、`settings.rs`、`services/provider/live.rs`、`services/provider/mod.rs`、`services/proxy.rs`），前端 `src/`（React + TS，重点 `src/config/*Presets.ts`、`src/lib/api/providers.ts`、`src/types.ts`）。

---

## 1. 一句话结论

cc-switch 的核心设计是 **SSOT（Single Source of Truth）**：每份「供应商配置」完整存在 SQLite（`~/.cc-switch/cc-switch.db` 的 `providers.settings_config` 列），切换供应商时把目标供应商的 `settings_config` **整体写入**（不是增量 merge）目标应用的 live 配置文件——Claude Code 写 `~/.claude/settings.json`（JSON 整文件替换），Codex 写 `~/.codex/config.toml`（TOML 整文件替换）+ 视情况写 `~/.codex/auth.json`。「Claude Code CLI」（`~/.claude/settings.json`）和「Claude Desktop」（`~/Library/Application Support/Claude*/claude_desktop_config.json` + 3P profile）是两条完全独立的代码路径（`AppType::Claude` vs `AppType::ClaudeDesktop`）；`~/.claude.json` 只被用于 MCP 服务器同步和 onboarding 跳过，不承载供应商切换。所有写入都走 `atomic_write`（同目录临时文件 + rename，Windows 用 `ReplaceFileW`），但**切换本身不做磁盘文件备份**——回滚靠的是切换前先把当前 live 回填进 DB（backfill），磁盘级备份只存在于「本地代理接管」场景（存进 SQLite `proxy_live_backup` 表，关闭接管时还原）。TOML 编辑用 `toml_edit`（保注释/格式），JSON 写出前递归排序 key 并 pretty 序列化。

---

## 2. Claude Code 配置文件：到底碰哪个

### 2.1 三个「Claude」目标，三条独立路径

`AppType` 枚举（`app_config.rs:378-395`）把 `Claude` 与 `ClaudeDesktop` 定义为两个独立应用，各自有独立的读/写函数集：

| 目标 | AppType | cc-switch 用途 | 路径构造 |
|---|---|---|---|
| `~/.claude/settings.json` | `Claude` | **供应商切换的主战场**（env 里的 ANTHROPIC_* 变量） | `config.rs:187-200` |
| `~/.claude.json` | `Claude` | 仅 MCP 服务器（`mcpServers` 字段）+ `hasCompletedOnboarding` 开关 | `config.rs:46-48, 176-184` |
| `~/Library/Application Support/Claude*/claude_desktop_config.json` 等 | `ClaudeDesktop` | Claude Desktop 3P 网关 profile | `claude_desktop_config.rs:1237-1297` |

另有第四个文件 `~/.claude/config.json`（Claude 插件的 `primaryApiKey`），由 `claude_plugin.rs:6-19` 管理（`CLAUDE_CONFIG_FILE: &str = "config.json"`，`claude_plugin.rs:7`），切换 Claude 供应商时可联动写 `primaryApiKey = "any"`（`claude_plugin.rs:51-88`，前端开关 `enableClaudePluginIntegration`，`useProviderActions.ts:61-68`）。

### 2.2 Claude Code 主配置：`~/.claude/settings.json`

路径构造（`config.rs:186-200`）：

```rust
// config.rs:186-200
/// 获取 Claude Code 主配置文件路径
pub fn get_claude_settings_path() -> PathBuf {
    let dir = get_claude_config_dir();
    let settings = dir.join("settings.json");
    if settings.exists() {
        return settings;
    }
    // 兼容旧版命名：若存在旧文件则继续使用
    let legacy = dir.join("claude.json");
    if legacy.exists() {
        return legacy;
    }
    // 默认新建：回落到标准文件名 settings.json（不再生成 claude.json）
    settings
}
```

目录构造（`config.rs:37-43`）：优先取用户在 cc-switch 设置里自定义的目录（`settings.claude_config_dir` → `get_claude_override_dir()`，`settings.rs:901-907`），否则 `get_home_dir().join(".claude")`。

- **不读 `CLAUDE_CONFIG_DIR` 环境变量**。全仓 grep `CLAUDE_CONFIG_DIR` 无命中（只出现测试专用 `CC_SWITCH_TEST_HOME`，`config.rs:23`；Codex 侧同理不读 `CODEX_HOME`，只有 `CODEX_SQLITE_HOME` 用于状态库定位，`codex_state_db.rs:21`）。也就是说 cc-switch 对「用户用环境变量迁移过 Claude 配置目录」的场景不感知，只能靠它自己的设置项手动指定。
- home 目录用 `dirs::home_dir()`（`Cargo.toml:39` `dirs = "5.0"`；`config.rs:30`），Windows 上刻意不信任 `HOME` 环境变量（`config.rs:11-16` 注释：Git/Cygwin 注入的 HOME 可能导致「看起来像数据丢失」）。
- 覆盖路径支持 `~/` 展开（`resolve_override_path`，`settings.rs:722-745`）。

**文件格式**：严格 JSON（`serde_json`），无注释/JSONC 处理。读取 `read_json_file`（`config.rs:266-274`）直接 `serde_json::from_str`；写出前递归排序 key 保证确定性输出（`sort_json_keys`，`config.rs:277-291`），再 `to_string_pretty`（`config.rs:305`）。

### 2.3 Claude Code 写入内容：env 映射整体替换

切换 Claude 供应商时，`settings_config` 的顶层对象**原样成为** `settings.json` 的内容（Claude 预设的形状就是 `{ env: {...} }`，`claudeProviderPresets.ts:78-113`）：

```ts
// claudeProviderPresets.ts:97-106（Kimi 预设示例）
settingsConfig: {
  env: {
    ANTHROPIC_BASE_URL: "https://api.moonshot.cn/anthropic",
    ANTHROPIC_AUTH_TOKEN: "",
    ANTHROPIC_MODEL: "kimi-k2.7-code",
    ANTHROPIC_DEFAULT_HAIKU_MODEL: "kimi-k2.7-code",
    ANTHROPIC_DEFAULT_SONNET_MODEL: "kimi-k2.7-code",
    ANTHROPIC_DEFAULT_OPUS_MODEL: "kimi-k2.7-code",
  },
},
```

写入前的唯一「清洗」是剥掉 cc-switch 内部字段（`sanitize_claude_settings_for_live`，`live.rs:168-178`）：

```rust
// live.rs:168-178
pub(crate) fn sanitize_claude_settings_for_live(settings: &Value) -> Value {
    let mut v = settings.clone();
    if let Some(obj) = v.as_object_mut() {
        // Internal-only fields - never write to Claude Code settings.json
        obj.remove("api_format");
        obj.remove("apiFormat");
        obj.remove("openrouter_compat_mode");
        obj.remove("openrouterCompatMode");
    }
    v
}
```

写入口在 `write_live_snapshot`（`live.rs:1242-1248`）：

```rust
// live.rs:1244-1248
AppType::Claude => {
    let path = get_claude_settings_path();
    let settings = sanitize_claude_settings_for_live(&provider.settings_config);
    write_json_file(&path, &settings)?;
}
```

注意这是**整文件替换**：DB 里存的供应商配置即最终 live 文件内容，用户在 settings.json 里手工加的 hooks/权限等不会被保留。cc-switch 的解法是「通用配置片段（common config snippet）」机制——把跨供应商共享的 JSON 片段存在 DB，写 live 前深合并进去（`json_deep_merge`，`live.rs:244-260`；`build_effective_settings_with_common_config`，`live.rs:673-702`），切走回填时再把片段剥掉（`strip_common_config_from_live_settings`，`live.rs:908-948`）。另外切换前还会把 live 里用户在应用内做的可共享改动（装插件/hook）先同步进片段再回填（`sync_common_config_snippet_from_live`，`services/provider/mod.rs:5442-5513`）。

### 2.4 Claude Desktop：完全独立的另一套

Claude Desktop 路径（`claude_desktop_config.rs`）：

```rust
// claude_desktop_config.rs:1236-1240
#[cfg(target_os = "macos")]
fn macos_paths_from_home(home: &Path) -> ClaudeDesktopPaths {
    let app_support = home.join("Library").join("Application Support");
    paths_from_dirs(app_support.join("Claude"), app_support.join("Claude-3p"))
}
```

- 仅支持 macOS / Windows（`is_supported_platform`，`claude_desktop_config.rs:1213-1215`；Linux 直接报错，`1314-1321`）。
- 涉及 5 个文件（`ClaudeDesktopPaths`，`claude_desktop_config.rs:75-82`）：普通版与 3P 版各自的 `claude_desktop_config.json`（`CONFIG_FILE`，`:18`）、3P 的 `configLibrary/` 目录下的 profile 文件（文件名是固定 UUID `PROFILE_ID = "00000000-0000-4000-8000-000000157210"`，`:14`）与 `_meta.json`（`paths_from_dirs`，`:1284-1297`）。
- 应用供应商时把两份 `claude_desktop_config.json` 的 `deploymentMode` 改成 `"3p"`，并写 profile（含 `inferenceGatewayBaseUrl` / `inferenceGatewayApiKey` / `inferenceModels` 等，`build_gateway_profile`，`:1026-1046`）；恢复官方则改回 `"1p"` 并删除 profile（`apply_provider_to_paths_inner` / `restore_official_at_paths_inner`，`:974-1024`）。
- 写入前对全部 4 个文件做内存快照，失败即回滚（`with_rollback` + `snapshot_files` / `restore_snapshots`，`:955-972, 1062-1099`），回滚写回用 `atomic_write`（`:1091`）。

**结论：cc-switch 切换 Claude Code CLI 供应商时完全不碰 Claude Desktop 的任何文件，反之亦然**（Claude Desktop 的 live 写入走 `claude_desktop_config::apply_provider`，`live.rs:730-738`，且拒绝通用 live 导入，`live.rs:1595-1599`）。

### 2.5 `~/.claude.json` 的有限使用

`get_default_claude_mcp_path()` = `~/.claude.json`（`config.rs:46-48`），只在两处被写：

1. **MCP 服务器**：`claude_mcp.rs` 的 `set_mcp_servers_map`（`claude_mcp.rs:345-411`）读出整个 JSON、只替换根对象的 `mcpServers` 字段再整文件写回（`write_json_value` 用 `atomic_write`，`claude_mcp.rs:113-120`）；Windows 下自动把 npx/npm 命令包装成 `cmd /c`（`wrap_command_for_windows`，`claude_mcp.rs:18`）。
2. **跳过 Claude Code 初次确认**：`set_has_completed_onboarding` / `clear_has_completed_onboarding`（`claude_mcp.rs:150, 175`）增量写/删根对象的 `hasCompletedOnboarding` 布尔字段，由设置项 `skipClaudeOnboarding` 驱动（`useSettings.ts:247-260` → `apply_claude_onboarding_skip` 命令，`lib.rs:1418-1419`）。

即 cc-switch **不会**把供应商配置写进 `~/.claude.json`，也不读它的账户/会话信息。

---

## 3. Codex 配置文件

### 3.1 文件清单与路径构造

`get_codex_config_dir()`（`codex_config.rs:354-360`）：设置覆盖（`settings.codex_config_dir`）或 `~/.codex`。由此派生：

| 文件 | 构造位置 | 作用 | cc-switch 行为 |
|---|---|---|---|
| `~/.codex/auth.json` | `get_codex_auth_path()` `codex_config.rs:363-365` | 登录凭据（OAuth bundle 或 `OPENAI_API_KEY`） | 官方供应商写入；第三方默认**不碰**（保留 ChatGPT 登录缓存） |
| `~/.codex/config.toml` | `get_codex_config_path()` `codex_config.rs:733-735` | 供应商路由/端点/模型 | 供应商切换的**主写入目标**，整文件替换 |
| `~/.codex/cc-switch-model-catalog.json` | `get_codex_model_catalog_path()` `codex_config.rs:737-739`（文件名常量 `:23`） | cc-switch 生成的模型目录，经 `config.toml` 的 `model_catalog_json` 指针引用 | 按 settings 的 `modelCatalog` 生成，见 §5.3 |
| `~/.codex/sessions/**/*.jsonl`、`archived_sessions/` | `codex_history_migration.rs:392-393` | Codex 会话记录 | 仅在「会话历史分桶迁移」时改写 provider 标注，与供应商切换无关 |
| `~/.codex/state_5.sqlite` | `codex_state_db.rs:18-38` | Codex 线程状态库 | 只读（会话列表标题查询），支持 `config.toml` 的 `sqlite_home` 或 `CODEX_SQLITE_HOME` 重定位 |
| `~/.cc-switch/codex_oauth_auth.json` | `codex_oauth_auth.rs:319` | cc-switch 托管的 ChatGPT OAuth 凭据（非 Codex 目录） | cc-switch 自有数据，与 Codex CLI 登录隔离 |

**不读不写 `~/.codex/history.jsonl`、`~/.codex/config.json`**（全仓无命中）。

### 3.2 `auth.json`：读哪些字段、写哪些字段

读取：

- API key：`extract_codex_auth_api_key` 取根级 `OPENAI_API_KEY`（`codex_config.rs:883-889`）；`extract_codex_api_key` 再回退到 config.toml 里的 bearer token（`:891-894`）。
- 判断是否含登录材料：`codex_auth_has_login_material` / `..._oauth_...` / `..._credential_...`（`:922, 949, 974`）。
- 托管 ChatGPT 账号识别：`extract_codex_managed_oauth_account_id` 只接受 `auth_mode == "chatgpt"` + `tokens.{access_token, account_id, ...}` 形状（`:382-428`）。

写入（ChatGPT OAuth bundle 的标准形状，`codex_managed_oauth_auth_value`，`codex_config.rs:431-460`）：

```rust
// codex_config.rs:454-459
json!({
    "auth_mode": "chatgpt",
    "OPENAI_API_KEY": null,
    "tokens": Value::Object(tokens),   // id_token / access_token / refresh_token / account_id
    "last_refresh": last_refresh,
})
```

第三方供应商的 auth.json 就是 `{"OPENAI_API_KEY": "<key>"}`（前端 `generateThirdPartyAuth`，`codexProviderPresets.ts:51-57`）。

### 3.3 `config.toml`：读哪些字段、写哪些字段

cc-switch 用 `toml_edit::DocumentMut` 解析（**保留注释与未知字段**；`Cargo.toml:41` `toml_edit = "0.22"`），核心字段：

- `model_provider`：当前激活的 provider id（`active_codex_model_provider_id`，`codex_config.rs:848-854`）。保留 id 白名单 `CODEX_RESERVED_MODEL_PROVIDER_IDS`（`openai/ollama/...`，`:344-351`），cc-switch 自用的 custom id 是 `"custom"`（`CC_SWITCH_CODEX_MODEL_PROVIDER_ID`，`:17`）。
- `model_providers.<id>.base_url`：上游地址。读取时只认激活 provider 的段，绝不读非激活段（`extract_codex_base_url` 及其文档注释，`codex_config.rs:896-920`）。
- `experimental_bearer_token`：**cc-switch 的关键技巧**——第三方切换时把 API key 从 `auth.json` 挪到 `config.toml` 里激活 provider 段（或顶层）的 `experimental_bearer_token`，从而不动用户的 ChatGPT 登录缓存（`set_codex_experimental_bearer_token`，`:2348-2388`；语义见 `write_codex_live_for_provider` 文档 `:2826-2831`）。读取侧 `extract_codex_experimental_bearer_token`（`:2318-2346`）。
- `model_catalog_json`：指向 cc-switch 自有目录文件；只接管「文件名等于 `cc-switch-model-catalog.json`」的指针，用户自管的外部目录绝不触碰（`set_codex_model_catalog_json_field`，`:1928-1983`；所有权判定 `resolve_cc_switch_catalog_path`，`:2115-2134`）。
- `web_search`：黑名单式禁用（`CODEX_WEB_SEARCH_DISABLED = "disabled"`，`:45`）。只对已知会拒绝该工具的网关（MiMo/LongCat/MiniMax/部分 Qwen）按 base_url host 或模型品牌前缀判定（`CODEX_WEB_SEARCH_REJECT_HOSTS`，`:65-77`；判定函数 `:92-115`），并且**只删除值恰为 `"disabled"` 的键**（所有权哨兵，`:37-45` 注释），不动用户自己的设置。
- `[mcp_servers]`：live 里的 MCP 段是 DB `mcp_servers` 表的投影，每次写 live 后由 MCP 同步重写（`sync_single_server_to_codex`，`mcp/codex.rs:419-458`，读改写同文件）；回填 DB 时剥离（`strip_codex_mcp_servers_from_settings`，`codex_config.rs:2759-2789`）。

### 3.4 Codex 写入路由：写不写 auth.json 由类别决定

`write_codex_live_for_provider`（`codex_config.rs:2799-2824`）是 Codex live 写入的总路由：

```rust
// codex_config.rs:2814-2823（核心分支）
let should_write_auth = (category == Some("official") && codex_auth_has_login_material(auth))
    || (category != Some("official")
        && !crate::settings::preserve_codex_official_auth_on_switch());

if should_write_auth {
    write_codex_live_atomic(auth, config_text)
} else {
    let live_config = prepare_codex_provider_live_config(auth, config_text.unwrap_or(""))?;
    write_codex_live_config_atomic(Some(&live_config))
}
```

- **官方 + 有登录材料** → `write_codex_live_atomic`（`:772-819`）：先写 `auth.json` 再写 `config.toml`，第二步失败回滚第一步（读旧字节 → `atomic_write` 回写或 `delete_file`）。
- **第三方** → `prepare_codex_provider_live_config`（`:2832-2843`）把 key 变成 bearer token 注入 config.toml → `write_codex_live_config_atomic`（`:869-881`）**只写 config.toml**，`auth.json` 原样保留。注意此路径受 `preserve_codex_official_auth_on_switch` 开关门控，该设置默认 `false`（`settings.rs:527`）——即默认行为下第三方切换也会整写 auth.json；开启「切换时保留官方登录」兼容设置后才走 config-only 路径。

---

## 4. 切换/写入机制与还原

### 4.1 调用链（前端 → Tauri → service）

```
UI（useProviderActions.ts:169 switchProvider → mutations.ts:304-310 useSwitchProviderMutation）
  → providersApi.switch (src/lib/api/providers.ts:90-92) invoke("switch_provider")
  → Tauri 命令 switch_provider (commands/provider.rs:118-133, spawn_blocking)
  → ProviderService::switch (services/provider/mod.rs:4943-5033)
  → switch_normal (mod.rs:5036-5289)
```

`switch_normal` 的五步（注释见 `mod.rs:4943` 函数文档 `Switch flow`）：

1. **Backfill（回填）**：切走前把当前 live 文件内容读回来（`read_live_settings`），剥掉通用片段与注入字段后存回 DB 当前供应商行（`mod.rs:5075-5115`，`state.db.save_provider`）。**这就是 cc-switch 的「还原」机制**：live 文件永远可以从 DB SSOT 重建，无需磁盘备份。
2. 更新本地 `current_provider_codex/claude`（`settings.rs:1010-1026`，存 `~/.cc-switch/settings.json`）。
3. 更新 DB `is_current`。
4. **写 live**：`write_preflighted_or_current_live`（`mod.rs:3957-3967`）→ `write_live_snapshot`（`live.rs:1242`）按 AppType 分发。
5. MCP 重投影（`mod.rs:5284-5286`）。

托管 Codex OAuth 账号场景额外升级为四文件事务（`mod.rs:5129-5181`）：先 `CodexLiveStateSnapshot::capture()`（`codex_config.rs:200-217`，快照 auth/config/catalog/marker 四个文件的内容+Unix 权限），失败用 `restore_preserving_newer_same_account_auth` 回滚（`:227` 起，同账号更新的 token 会被保留，避免把 CLI 刚轮换的 token 回滚成旧代）。

### 4.2 代理接管（takeover）：唯一做磁盘级备份的场景

cc-switch 自带本地 LLM 代理（默认 `http://127.0.0.1:15721`），开启接管后 live 文件指向本地代理、真实 key 只留在 DB：

- **备份**：`start_with_takeover`（`services/proxy.rs:1002` 起）第一步 `backup_live_configs`（`proxy.rs:1825-1889`）把各应用 live 的 JSON 序列化存入 SQLite 表 `proxy_live_backup`（建表 `schema.rs:264-270`：`app_type TEXT PRIMARY KEY, original_config TEXT NOT NULL, backed_up_at TEXT NOT NULL`；DAO `database/dao/proxy.rs:779-788` `INSERT OR REPLACE`）。备份前检测 live 是否已含代理占位符（`live_has_proxy_placeholder_for_app`，`proxy.rs:2776-2790`），防止把代理配置当「原始 live」固化。
- **改写 live**：`takeover_live_configs`（`proxy.rs:2002-2056`）。Claude 侧改 `env.ANTHROPIC_BASE_URL` 为代理地址、把 4 个 token 键换成占位符 `PROXY_MANAGED`、接管 12 个模型 env 键（`CLAUDE_MODEL_OVERRIDE_ENV_KEYS`，`proxy.rs:32-45`；写入逻辑 `apply_claude_takeover_fields_with_policy_and_models`，`proxy.rs:486-567`）。
- **恢复**：`stop_with_restore`（`proxy.rs:1745-1787`）→ `restore_live_configs`（`proxy.rs:2256`）→ `restore_live_config_for_app_with_fallback_inner`（`proxy.rs:2289`）三级兜底：
  1. 优先从 `proxy_live_backup` 还原；
  2. 备份缺失/损坏 → 从 SSOT（DB 当前供应商）重建 live（`restore_live_from_ssot_for_app`，`proxy.rs:2385-2421`）；
  3. 再不行 → 仅清理占位符与本地代理地址（`cleanup_takeover_placeholders_in_live_for_app`，`proxy.rs:2423`）。
- 恢复后删除备份行（`proxy.rs:1772-1776`）。
- 接管期间的「热切换」不写 live 文件，只更新 DB current 并刷新备份（`update_live_backup_from_provider`，`proxy.rs:2790-2935`）与代理路由。

### 4.3 原子写入实现

所有配置文件写入最终都落到 `config.rs` 的 `atomic_write`（`config.rs:327-334` 入口，实现在 `atomic_write_with_unix_mode`，`config.rs:336-499`）：

```rust
// config.rs:364-368 临时文件命名（同目录，O_CREAT 新建，防碰撞重试 16 次）
let candidate = parent.join(format!(
    "{file_name}.tmp.{}.{ts}.{counter}",
    std::process::id()
));
```

- 写临时文件 → `flush` → 非 Windows `fs::rename` 原子替换（`config.rs:490-497`）；Windows 优先 `ReplaceFileW`（保留属性），不支持时回退 `fs::rename`（`config.rs:410-486`）。
- Unix 上继承目标文件原权限（`config.rs:404-407`）；凭据文件可用 `atomic_write_private` 强制 0600（`config.rs:332-334`，当前仅 Pi 配置使用）。
- JSON 写入统一走 `write_json_file`（`config.rs:314-316` → `write_json_file_with_contents` `:294-311`：排序 key + pretty）；TOML/纯文本走 `write_text_file`（`config.rs:319-324`）。
- 写入前 TOOML 语法预校验：`validate_config_toml`（`codex_config.rs:832-839`）确保不会把非法 TOML 写进 live。

### 4.4 是否保留用户自定义字段

- **Codex（TOML）：是**。`toml_edit` 保注释保格式，改字段是「点改」而非重建（如 `update_codex_toml_field`，`codex_config.rs:2898-2985`，支持 `base_url`/`wire_api`/`model`/`model_catalog_json` 四个白名单字段，其余一律拒绝）。但注意**供应商切换本身**仍是用 DB 里存的完整 config.toml 文本整文件替换 live——用户手改 live 里未回填的部分会丢（靠 backfill 缓解）。
- **Claude（JSON）：否**。`write_json_file` 整文件替换，live 中 DB 之外的顶层字段不保留（除非落在通用片段里）。
- **`~/.claude.json` / `~/.claude/config.json` / Claude Desktop `claude_desktop_config.json`：是**。这三处都是「读出 → 只改目标键 → 写回」的增量模式（`claude_mcp.rs:345-411`、`claude_plugin.rs:51-88`、`claude_desktop_config.rs:1101-1113`）。

---

## 5. 数据模型：一份「供应商配置」长什么样

### 5.1 Rust 侧 `Provider`

`provider.rs:10-44`：

```rust
// provider.rs:10-44（节选）
pub struct Provider {
    pub id: String,
    pub name: String,
    #[serde(rename = "settingsConfig")]
    pub settings_config: Value,        // ← live 文件的内容来源（按 AppType 形状不同）
    pub website_url: Option<String>,
    pub category: Option<String>,      // official / cn_official / aggregator / third_party / custom ...
    pub notes: Option<String>,
    pub meta: Option<ProviderMeta>,    // 不写入 live，仅存 DB（provider.rs:30 注释）
    pub icon: Option<String>,
    pub icon_color: Option<String>,
    #[serde(rename = "inFailoverQueue")]
    pub in_failover_queue: bool,
}
```

`ProviderMeta`（`provider.rs:434` 起，字段延伸至约 `:525`）存不进 live 的元数据：`api_format`（anthropic/openai_chat/openai_responses）、`api_key_field`（ANTHROPIC_AUTH_TOKEN vs API_KEY）、`claude_desktop_mode`（direct/proxy）、`claude_desktop_model_routes`、`common_config_enabled`、用量脚本、限额等。

前端 TS 镜像（`src/types.ts:11-31`），`settingsConfig` 字段注释直接点明两种形状（`types.ts:14`）：

```ts
settingsConfig: Record<string, any>; // 应用配置对象：Claude 为 settings.json；Codex 为 { auth, config }
```

### 5.2 各 AppType 的 `settings_config` → 目标文件键映射

| AppType | settings_config 形状 | 映射到 live |
|---|---|---|
| Claude | `{ env: { ANTHROPIC_BASE_URL, ANTHROPIC_AUTH_TOKEN \| ANTHROPIC_API_KEY, ANTHROPIC_MODEL, ... } }`（`claudeProviderPresets.ts:78-113`；key 解析优先级见 `resolve_usage_credentials`，`provider.rs:226-239`） | 整个对象 = `~/.claude/settings.json` |
| Codex | `{ auth: {...}, config: "<TOML 字符串>" }`（前端预设注释 `codexProviderPresets.ts:19-20`「auth → ~/.codex/auth.json；config → ~/.codex/config.toml」） | `auth` → `auth.json`；`config` 文本 → `config.toml`；`modelCatalog` → `cc-switch-model-catalog.json` + `config.toml` 的 `model_catalog_json` 指针 |
| Gemini | `{ env: {...}, config: {...} }`（`live.rs:1600-1632`） | `.env` + `settings.json` |
| ClaudeDesktop | 由 `meta.claude_desktop_mode` + routes 推导 | 3P profile JSON（`claude_desktop_config.rs:974-1011`） |

第三方 Codex 的 TOML 模板由前端生成（`generateThirdPartyConfig`，`codexProviderPresets.ts:60-77`）：

```toml
model_provider = "custom"
model = "gpt-5.6-sol"
model_reasoning_effort = "high"
disable_response_storage = true

[model_providers.custom]
name = "..."
base_url = "..."
wire_api = "responses"
requires_openai_auth = true
```

### 5.3 模型映射（modelCatalog）

前端 `CodexCatalogModel`（`types.ts:264-285`：`model/displayName/contextWindow/inputModalities/baseInstructions/reasoningLevels/defaultReasoningLevel`）→ 后端 `codex_model_catalog_from_settings`（`codex_config.rs:1884`）生成完整目录 JSON 写入 `~/.codex/cc-switch-model-catalog.json`，并在 `config.toml` 顶层写 `model_catalog_json = "cc-switch-model-catalog.json"`（`prepare_codex_config_text_with_model_catalog`，`codex_config.rs:2006-2039`）。读取（编辑表单回显）只反解 cc-switch 自有目录文件，用户自管目录不动（`read_codex_model_catalog_simplified_from_live`，`codex_config.rs:2068-2091`；读取上限 32 MiB，`:2066`）。

Claude 侧模型映射直接是 env 键（`ANTHROPIC_DEFAULT_{HAIKU,SONNET,OPUS,FABLE}_MODEL`），代理接管时由 `build_claude_takeover_model_fields`（`proxy.rs:569-624`）统一接管为稳定别名。

### 5.4 持久化位置

- 供应商 SSOT：SQLite `providers` 表（`database/schema.rs:27-43`，`settings_config TEXT`、`meta TEXT DEFAULT '{}'`、`is_current`），主键 `(id, app_type)`。
- cc-switch 自身设置：`~/.cc-switch/settings.json`（`settings.rs:564-572`，`settings_path()`），存 `current_provider_*`、各应用配置目录覆盖、`preserve_codex_official_auth_on_switch` 等。
- 接管备份：`proxy_live_backup` 表（`schema.rs:264-270`）。
- 旧版 JSON 应用配置 `~/.cc-switch/config.json`（`get_app_config_path`，`config.rs:239-241`）仍保留加载兼容（`app_config.rs:580-614`），但运行时 SSOT 已是数据库。

---

## 6. 对 portunus 的可借鉴点

1. **SSOT + 回填，而不是磁盘备份**：cc-switch 切换前把 live 回填进自己的存储，切换失败/切回都能从 SSOT 重建 live，避免了「备份文件过期/丢失」一类问题；磁盘级备份只在代理接管这种「live 被外人改写」的场景才有必要。
2. **凭据与路由分离**（Codex 模式）：登录态（auth.json）与路由配置（config.toml）拆开后，第三方切换可以完全不碰用户的长效登录缓存（`experimental_bearer_token` 进 config.toml），值得 portunus 在生成客户端配置时参考。
3. **所有权哨兵**：cc-switch 只删除/修改「明显是自己写的值」（如 `web_search = "disabled"`、文件名为 `cc-switch-model-catalog.json` 的指针、`PROXY_MANAGED` 占位符），这是与用户手改内容共存的安全底线（`codex_config.rs:37-45, 1928-1983`）。
4. **原子写 + 写前校验**：temp 文件 + rename（Windows `ReplaceFileW`）+ TOML/JSON 写前语法校验（`config.rs:336-499`、`codex_config.rs:832-839`），防止半写状态损坏用户配置。
5. **整文件替换 vs 增量改写要分场景**：供应商切换（语义上是「整份配置」）用整文件替换最简单；共享文件（MCP、onboarding 标志）必须读-改-写保住无关字段。
