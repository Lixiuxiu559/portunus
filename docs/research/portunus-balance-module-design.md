# Portunus 余额/额度查询模块设计（借鉴 cc-switch）

> 本文档是 portunus 余额模块的**设计稿**，回答「余额模块怎么做、如何从 cc-switch 借鉴落地」。
> 借鉴对象研究见同目录 [`cc-switch-balance-query.md`](./cc-switch-balance-query.md)。
> 结论均对照 portunus 现有代码（`backend/`）给出可落地的包结构、数据模型、API 与错误约定。

## 0. 结论先行

cc-switch 是**桌面客户端**，余额查询面向「用户自己手里的各家账号」，凭据来自 CLI 工具的
OAuth 或用户手填的 API Key；portunus 是**服务端聚合网关**，余额查询面向「自己接的渠道
Channel」，凭据就是 Channel 已有的 `Key`。

因此借鉴不是照搬，而是提炼这些**可移植的设计原则**，落到 portunus 已有架构上：

| cc-switch 设计 | 是否移植 | portunus 落点 |
|---|---|---|
| 统一 tier 模型（`name/utilization/resets_at`） | ✅ 全局统一 | `backend/balance` 包定义 `Balance` 实体 |
| 错误通道二分（瞬时 vs 确定性） | ✅ 核心 | Go 里用「`error` vs 结构化 `result`」区分 |
| keep-last-good（失败保留上次成功） | ✅ 核心 | 落库保存快照 + `queried_at` 过期判定 |
| 按供应商白名单硬编码 | ✅ 第一期 | `backend/balance/providers` |
| 自定义 JS 脚本沙箱 | ⚠️ 后置 | 服务端执行用户代码风险高，第二期再议 |
| OAuth 凭据读取（Keychain/auth.json） | ❌ 不移植 | portunus 无桌面端，无 CLI OAuth 凭据 |
| tauri IPC / react-query / 托盘 | ❌ 不移植 | 换成 HTTP 管理 API + 前端轮询 |

---

## 1. 目标与非目标

**目标**：给每个 Channel 提供「查余额/额度」能力，管理面板可看实时余额、按渠道标记
余额不足，辅助 failover 路由决策。

**非目标**（第一期不做）：

- 不移植 cc-switch 的 OAuth 凭据读取（Claude Pro/Codex/Copilot/Gemini 订阅额度）——
  那些凭据在 cc-switch 里来自用户桌面环境，portunus 服务端拿到的是 API Key，只做
  **API-Key 类供应商**的余额/套餐额度。
- 不做自定义 JS 脚本执行（见 §7，风险分析后单独评审）。
- 不做计费/欠费拦截强链路，余额只做「展示 + 预警 + 路由软参考」。

---

## 2. 数据模型

### 2.1 Channel 扩展：余额类型

`backend/channel/channel.go` 的 `Channel` 增加一个字段，区分「这个渠道怎么查余额」：

```go
// 余额查询方式（渠道级）。
type BalanceKind string

const (
    BalanceNone     BalanceKind = ""      // 不查余额（默认）
    BalanceProvider BalanceKind = "provider" // 内置供应商白名单（DeepSeek/StepFun/...）
    // 预留：BalanceScript BalanceKind = "script"  // 自定义脚本，第二期
)
```

`Channel` 加：

```go
BalanceKind BalanceKind `gorm:"default:''" json:"balance_kind"`
```

理由（对照 cc-switch 的 `template_type`）：cc-switch 用 `template_type` 把「官方订阅 /
余额 / coding_plan / 脚本」四条路径分开（`cc-switch/src-tauri/src/commands/provider.rs:560-562`）。
portunus 同样需要一个渠道级字段决定走哪条查询路径。**不建议**用 `Channel.Type`（协议类型）
反推——协议和余额供应商是正交的（同是 OpenAI 协议的中转站各有各的余额端点）。

第一期 `BalanceKind` 只有 `provider` 一个非空取值，用白名单供应商 `BaseURL` 的域名
自动识别具体供应商（复用 cc-switch `detect_provider` 的按域名匹配思路，
`cc-switch/src-tauri/src/services/balance.rs:26-43`），用户只需勾选「启用余额查询」，
不必手填供应商名。

### 2.2 Balance 实体（快照持久化）

cc-switch 的 `UsageCache` 是**进程内、写穿式、不持久化**（`usage_cache.rs` 模块注释），
托盘用。portunus 是服务端，余额快照应**落库**，避免重启后前端拿不到、也能留存历史。

新建 `backend/balance/balance.go`：

```go
// Balance 是一条余额查询的结果快照，随每次查询覆盖更新。
type Balance struct {
    ID          int64     `gorm:"primaryKey" json:"id"`
    ChannelID   int64     `gorm:"not null;uniqueIndex" json:"channel_id"` // 一渠道一条最新快照
    Success     bool      `json:"success"`           // 查询是否成功（false = 确定性失败）
    Error       string    `json:"error,omitempty"`   // 失败文案（瞬时失败时为空白，见 §4）
    Tiers       string    `json:"tiers"`             // JSON 数组，序列化 []Tier（见下）
    QueriedAt   time.Time `json:"queried_at"`        // 最近一次查询时刻（成功或确定性失败）
    // 瞬时失败不清 queried_at，也不覆盖 success，见 §5 keep-last-good
    UpdatedAt   time.Time `json:"updated_at"`
}

// Tier 单个额度窗口/账户余额项，统一口径（cc-switch QuotaTier 的服务端等价）。
type Tier struct {
    Name        string  `json:"name"`        // 窗口名：five_hour/seven_day/... 或余额账户名
    Used        float64 `json:"used"`        // 已用
    Total       float64 `json:"total"`       // 总额度（无上限时为 0 或 -1，约定见下）
    Remaining   float64 `json:"remaining"`   // 剩余
    Utilization float64 `json:"utilization"` // 0-100 百分比；余额类可为空，由前端算
    Unit        string  `json:"unit"`        // CNY/USD/requests/%
    ResetsAt    string  `json:"resets_at,omitempty"` // ISO8601，无重置周期则空
}
```

设计取舍（对照 cc-switch）：

- cc-switch 有两套结果形状——`UsageResult`（余额/脚本，`used/total/remaining/unit`）和
  `SubscriptionQuota`（订阅额度，`tiers[]` 百分比窗口），前端要写两套渲染
  （`UsageFooter.tsx` vs `SubscriptionQuotaFooter.tsx`）。portunus **统一成一套 `Tier`**
  ，余额和百分比窗口都能表达：余额类 `Total`/`Remaining` 填绝对值、`Utilization` 留空
  前端用 (total-remaining)/total 算；百分比窗口类 `Utilization` 直接给百分值。省掉双轨。
- `Total` 语义约定：**0 且 `Unit` 为余额货币** = 未知上限；用负数 -1 表「无上限」会污染
  float 比较，统一用「`Total == 0` 时前端显示不设上限」。
- `Tiers` 存 JSON 字符串而非关联表：余额条目是查询结果的瞬时投影，不需要按 tier 检索，
  序列化存储足够，避免一张几乎不查的关联表。

### 2.3 迁移

`main.go` 的 `AutoMigrate` 加 `&balance.Balance{}`；`Channel` 结构变更由 GORM AutoMigrate
自动加列（SQLite 起步，加列安全）。

---

## 3. 包结构与依赖方向

新建 `backend/balance`，遵循项目「依赖单向」约定
（`ARCHITECTURE.md`：`api` / `gateway` → 业务 → `shared`）：

```
backend/balance/
  balance.go            # Balance / Tier 实体 + Service 入口（Query / Get 缓存读）
  provider.go           # BalanceProvider 接口 + 按 BaseURL 域名分发
  providers/
    deepseek.go         # 一家供应商一个文件，仿 model/sync.go 的平铺结构
    stepfun.go
    siliconflow.go
    openrouter.go
    novita.go
    ...                # 后续扩展只加文件，不改分发主逻辑
  api.go                # 或并入 backend/api/，提供 /api/balances 路由
```

依赖方向：`balance` → `channel` / `protocol` / `shared`；`api` → `balance`。
`api` 层通过 `balance` 包暴露 `Query(channelID)` 与 `Get(channelID)`，不直接碰上游 HTTP。

**要不要放进 `channel` 包？** 不。余额是独立关注点，有自己的实体和上游调用，塞进 `channel`
会让它膨胀；对外用 `balance.Service` 聚合，`channel` 只多一个 `BalanceKind` 字段。

---

## 4. 错误通道二分（核心借鉴点）

cc-switch 最值得移植的是这条贯穿所有余额服务的原则
（`cc-switch/src-tauri/src/services/balance.rs:1-10` 模块注释）：

> `Err` = 瞬时传输失败（网络不可达/超时/读体中断）→ 前端 retry + 保留上次成功值；
> `Ok(success:false)` = 确定性失败（空 key/未知供应商/鉴权/非 2xx/非法 JSON）→ 立即透出。

在 Go / portunus 里对应映射：

| 失败类型 | cc-switch | portunus Go 约定 |
|---|---|---|
| 瞬时（网络/超时/读体中断） | `Err(String)` → reject → retry | **返回 `error`**（HTTP 503 或错误信息），前端/上层可重试 |
| 确定性（空 key/未知域名/401/403/4xx/非法 JSON） | `Ok(success:false)` 带 error 文案 | **返回 `*shared.StatusError` 或正常结果对象**（`success=false`），不重试 |

关键实现技巧要照搬：**读响应体用 `io.ReadAll` 完整读完，`ReadAll` 失败算瞬时、返回 error；
`json.Unmarshal` 失败算确定性、返回结构化失败**。绝不能用「`Unmarshal` 失败」一把抓
（portunus 现有 `protocol/upstream.go:61-68` 里 `ReadAll` 失败和解析失败已经分开，保持这个
粒度，别在余额模块里退化回去）。

此外 cc-switch 前端 `isTransientUsageError`（`queries.ts:142-167`）通过**匹配错误文案里的
`HTTP <code>`** 把 5xx/429 归瞬时、其余 4xx 归确定性。Go 端不建议靠文案匹配——**在
`send`/`ReadAll` 后的 HTTP 状态判断处就直接分类**：`>=500 || ==429` 归瞬时（返回 error）、
`401/403` 归确定性鉴权失败、`0xx/空 key/未知域名` 归确定性。把分类做在源头，不留给前端。

---

## 5. 缓存与 keep-last-good

cc-switch 的 keep-last-good：瞬时失败在 10 分钟窗口内继续展示上次成功值
（`KEEP_LAST_GOOD_MS = 10 分钟`，`queries.ts:120`），确定性失败立即透出并**清空**旧快照。

portunus 落库版：

1. **成功**：`UPDATE balance SET tiers=?, success=true, error='', queried_at=now WHERE channel_id=?`。
2. **确定性失败**：`UPDATE ... SET success=false, error=?, queried_at=now`（立即覆盖，前端看到失败态）。
3. **瞬时失败**：**不写库**，`queried_at` 保持上次成功时刻。读接口返回上次快照，
   `queried_at` 距今超 `KEEP_LAST_GOOD_MS`（10 分钟）则视为过期、标记 `stale=true` 让前端
   提示「余额数据可能过期」。这替代 cc-switch 前端 `resolveDisplayUsage` 的窗口逻辑
   （`queries.ts:192-243`），逻辑落到服务端读侧，前端只管渲染。

**自动刷新**：cc-switch 前端 `refetchInterval`（默认 5 分钟）+ 托盘旁路。portunus 已有
`cron` 框架（`backend/cron/cron.go`，`tickInterval=1min` + 读 `sync_interval` 设置）。余额
复用同一 ticker，新增一个 `balance_interval` 设置（`backend/shared/setting.go` 加 SettingKey），
到点遍历 `BalanceKind != BalanceNone` 的渠道逐个 `balance.Query`。单渠道失败只 log 不中断
（对照 `model.SyncAutoChannels` 的容错写法，`sync.go:44-64`）。

---

## 6. 管理 API

`backend/api` 新增 `registerBalanceRoutes`，挂到 `/api/balances`：

```
GET    /api/balances/:channelId      # 读缓存快照（含 stale 标记，不触发上游）
POST   /api/balances/:channelId/query # 强制立即查询上游并落库
GET    /api/balances                 # 列出所有启用余额的渠道快照（供面板总览）
```

- `GET` 只读库，毫秒级；`POST .../query` 触发一次上游调用（可复用 `syncChannelModels`
  的「手动触发」模式，`api/channel.go:144-161`）。
- 错误处理复用 `classifyError` / `respondError`（`api/api.go:35-54`）：瞬时失败映射到
  `StatusError{503}`，确定性失败返回 `200 + {success:false, error:...}`（与 cc-switch 把
  确定性失败当正常结果返回一致，前端据此渲染「查询失败 + 刷新」而非报错页）。

---

## 7. 自定义脚本：后置与风险

cc-switch 第二亮点是自定义 JS 脚本路径（沙箱 + `request`/`extractor` 约定）
——见 `cc-switch-balance-query.md` §4.8。但**服务端执行用户代码的风险远高于桌面端**：

- cc-switch 是本地单用户，恶意脚本只害自己；portunus 是多租户服务端，恶意脚本 = SSRF /
  内网探测 / 资源耗尽，攻击面完全不同。
- Go 生态没有 rquickjs 那样成熟的嵌入式 QuickJS，引入 goja/otto 有兼容性坑。

**建议**：第一期**不做**脚本执行。用「供应商白名单 + 域名分发」覆盖主流 API-Key 供应商，
第二期若确有长尾需求，再评审「白名单扩展成本 vs 脚本沙箱风险」，届时可参考 cc-switch 的
SSRF 防护（HTTPS 强制 + 同源校验）与资源限制（内存/栈/超时）作为最低安全基线。

---

## 8. 分期落地建议

**第一期（MVP）**：

1. `Channel` 加 `BalanceKind`；`AutoMigrate` 加 `Balance`。
2. `backend/balance` 包 + `BalanceProvider` 接口 + DeepSeek/OpenRouter 两家（先服务自己接的
   渠道），跑通「查询 → 落库 → 读缓存 → 前端展示」闭环。
3. 错误通道二分 + keep-last-good 的读侧 stale 判定。
4. `cron` 加 `balance_interval` 自动刷新 + `POST /api/balances/:id/query` 手动触发。

**第二期**：

1. 补齐 StepFun / SiliconFlow / Novita / 智谱 / MiniMax / 火山方舟等 cc-switch 已验证的
   供应商（迁移成本低，直接平铺新文件）。
2. 余额不足预警（前端标红 + 可选：failover 路由把这渠道降权）。
3. 评审自定义脚本沙箱是否值得引入。

---

## 9. 与 cc-switch 的差异速查

| 维度 | cc-switch | portunus |
|---|---|---|
| 凭据来源 | CLI OAuth + 手填 API Key | Channel.Key |
| 结果形状 | 双轨（UsageResult + SubscriptionQuota） | 统一 `Tier` |
| keep-last-good | 前端 React Query 内存态 | 服务端落库 + 读侧 stale |
| 缓存 | 进程内 UsageCache | SQLite 持久化快照 |
| 自动刷新 | 前端 refetchInterval + 托盘 | 服务端 cron |
| 触发入口 | tauri IPC | HTTP 管理 API |
| 自定义脚本 | QuickJS 沙箱（已实现） | 后置，评估风险后再定 |