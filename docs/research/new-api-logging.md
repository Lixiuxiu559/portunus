# new-api 的使用日志功能研究

> 研究时间：2026-08-25。对象：`/Users/lixiuxiu/development_tool/projects/new-API`（QuantumNous/new-api）。
> 目的：为 portunus 的调用日志 / 计费 / 日志查询提供参考。

---

## 1. 一句话结论

new-api 把「一次调用」记成一条 `Log` 记录：**请求结束后**在 relay 流程里算出费用（`quota`，整数配额单位），同步落一条消费日志，并**同步从用户/令牌的余额里扣减**；查询侧提供管理端 `GET /log` 与用户端 `GET /log/self` 两个带丰富筛选 + 分页的接口，外加 `GET /log/stat` 统计（总 quota、近 60s RPM/TPM）；日志清理**只有手动接口**，无自动定时任务。

---

## 2. 日志数据模型（model/log.go:19-40）

```go
type Log struct {
	Id               int    `gorm:"index:idx_created_at_id,..."`
	UserId           int    `json:"user_id" gorm:"index;index:idx_user_id_id,..."`
	CreatedAt        int64  `json:"created_at" gorm:"bigint;index:idx_created_at_id,..."` // Unix 秒
	Type             int    `json:"type" gorm:"index:idx_created_at_type"`  // 见下方常量
	Content          string `json:"content"`        // 一句话摘要（如「模型 xx，缓存 xx」）
	Username         string `json:"username" gorm:"index;..."`
	TokenName        string `json:"token_name" gorm:"index;default:''"` // 调用方令牌名
	ModelName        string `json:"model_name" gorm:"index;...;default:''"`
	Quota            int    `json:"quota" gorm:"default:0"`             // 费用（配额整数单位）
	PromptTokens     int    `json:"prompt_tokens" gorm:"default:0"`
	CompletionTokens int    `json:"completion_tokens" gorm:"default:0"`
	UseTime          int    `json:"use_time" gorm:"default:0"`          // 耗时（秒）
	IsStream         bool   `json:"is_stream"`
	ChannelId        int    `json:"channel" gorm:"index"`
	ChannelName      string `json:"channel_name" gorm:"->"` // 冗余展示字段，查询时回填
	TokenId          int    `json:"token_id" gorm:"default:0;index"`
	Group            string `json:"group" gorm:"index"`     // 路由时命中的分组
	Ip               string `json:"ip" gorm:"index;default:''"` // 按用户设置决定是否记录
	RequestId        string `json:"request_id,omitempty" gorm:"type:varchar(64);index:..."`
	Other            string `json:"other"` // 扩展信息（JSON 字符串）：缓存/图片/搜索明细、admin_info 等
}
```

要点：
- **`Type` 是日志分类**（model/log.go:42-51，刻意不用 iota 防改值）：`Consume=2`（消费，最主要的）、`Error=5`、`Topup=1`、`Manage=3`、`System=4`、`Refund=6`。查询按 `type` 过滤。
- **`Quota` 是整数配额**，不是浮点金额。`QuotaPerUnit = 500 * 1000`（common/constants.go:22，即 $0.002/1K tokens 量级的缩放），金额与配额之间靠这个单位换算。
- **索引围绕筛选字段建**：`user_id`、`created_at`、`type`、`model_name`、`token_name`、`channel`、`group`、`request_id` 都有索引，保证按这些维度查询不慢。
- **`Other` 存 JSON**，承载结构化的扩展明细（缓存 tokens/比值、图片 tokens、工具调用附加费等），前端可按需解析。
- **日志可用独立库**：默认 `LOG_DB = DB`（model/main.go:215），但可用环境变量 `LOG_SQL_DSN` 把日志隔离到单独数据库（model/main.go:214-228），避免日志膨胀拖累主库。

---

## 3. 日志何时/如何写入（请求结束 → 扣费 → 落库）

以文本对话为例，一次调用的计费落库链路（`service/text_quota.go`）：

```
relay 响应处理完 → 拿到 usage（prompt/completion tokens）
  → service/text_quota.go 里组装 textQuotaSummary
       └─ summary.Quota = int( 公式 ).Round(0)        text_quota.go:288
  → service.PostConsumeQuota(relayInfo, quota, pre, ...)   service/quota.go:404
       └─ 从用户钱包余额扣 quota / 或订阅额度扣 delta
       └─ 从令牌(Token)余额扣 quota
  → model.RecordConsumeLog(ctx, userId, RecordConsumeLogParams{...})  text_quota.go:460
       └─ 组装 Log 行，同步 LOG_DB.Create(log)          model/log.go:204-244
       └─ 可选：数据导出时 gopool.Go 异步写 quota 明细  model/log.go:248-252
```

`RecordConsumeLog`（model/log.go:204）接收 `RecordConsumeLogParams`（channel_id / prompt_tokens / completion_tokens / model_name / token_name / quota / content / token_id / use_time / is_stream / group / other），**同步落库**。受开关 `common.LogConsumeEnabled` 控制（关掉则不记消费日志）。

其他写入入口：
- `RecordErrorLog`（model/log.go:145）：上游报错时记 `Type=Error`，tokens/quota 置 0。
- `RecordTopupLog`（model/log.go:117）：充值记录。
- `RecordTaskBillingLog`（model/log.go:267）：绘图/视频等任务类接口的计费。

---

## 4. 费用（quota）怎么算

**计价模型：全局「模型比值 × 分组比值」，不是按渠道定价。** 价格存于 `setting/ratio_setting/model_ratio.go`（内存中的模型→比值 map + 分组→比值 map，可由管理界面改），relay 时用 `ratio_setting.GetModelRatio` / `GetGroupRatio` 查（relay/helper/price.go:68、HandleGroupRatio）。

核心公式（service/text_quota.go:273-288）：

```go
promptQuota   := baseTokens + cachedTokens*ratio + imageTokens*ratio + cacheCreationTokens*ratio
completionQuota := completionTokens * completionRatio
quota = (promptQuota + completionQuota) * groupRatio   // 取整
// 再叠加 tool 调用附加费、音频输入费等
```

- 缓存（cache read / cache creation）token 按打折的 `ratio` 单独计。
- `groupRatio` 是分组倍率（默认 1.0，可对特殊分组设倍率），最终 `quota = tokens 折算 × groupRatio`。
- 流式/非流式共用同一套计算；`PostConsumeQuota`（service/quota.go:404）里对**用户钱包**与**令牌余额**分别扣减（或走订阅额度），并可选触发「额度将尽」通知。

> 与 portunus 的差异：portunus 是**按 (渠道, 模型) 定价**（`model.Model.InputPrice/OutputPrice`，每 1M token），new-api 是**全局模型比值 × 分组比值**。两者都能算出「一次调用的费用」，但 portunus 更贴近实际账单、new-api 更灵活配置。

---

## 5. 日志查询接口（controller/log.go）

| 路由 | 权限 | 功能 |
|---|---|---|
| `GET /log/` | Admin | `GetAllLogs`：分页查全量日志 |
| `GET /log/self` | User | `GetUserLogs`：只查当前用户 |
| `GET /log/stat` | Admin | `GetLogsStat`：汇总统计 |
| `GET /log/self/stat` | User | `GetLogsSelfStat`：当前用户统计 |
| `GET /log/token` | Token 只读 | `GetLogByKey`：按令牌查最近 `MaxRecentItems` 条 |
| `DELETE /log/` | Admin | `DeleteHistoryLogs`：按 `target_timestamp` 清理旧日志 |

**筛选参数**（query string，GetAllLogs controller/log.go:13-33）：`type`、`start_timestamp`、`end_timestamp`、`username`、`token_name`、`model_name`、`channel`、`group`、`request_id`，加分页（`common.GetPageQuery` 的 page/page_size）。

底层查询 `GetAllLogs`（model/log.go:298-380）是**动态拼 WHERE + Count + Offset/Limit**，查完再批量回填 `ChannelName`（优先走缓存，否则 `IN` 查 channels 表）。

**统计** `SumUsedQuota`（model/log.go:435-489）：
```sql
SELECT sum(quota) FROM logs WHERE type=2 [筛选...]     -- 总费用
SELECT count(*), sum(prompt_tokens)+sum(completion_tokens)
  FROM logs WHERE type=2 [筛选...] AND created_at >= now-60s  -- 近 60s RPM / TPM
```
（RPM=每分钟请求数，TPM=每分钟 token 数，常用于限流/健康检查。）

---

## 6. 日志保留/清理

- **只有手动接口**：`DELETE /log/?target_timestamp=<unix秒>` → `DeleteOldLog`（model/log.go:512-533）按 `created_at < target` **分批 Limit(100) 循环删除**，直到删完或 context 取消，返回删除条数。
- **没有自动定时清理任务**（全项目无调度点引用 `DeleteOldLog` 之外的地方）。清理依赖管理员手动调用或运维脚本。
- 大数据量下的常规做法是：日志走独立库（`LOG_SQL_DSN`）+ 按时间批量清理 + 索引兜底。

---

## 7. 对 portunus 的启示 / 可借鉴点

portunus 现状：`shared.Log` 实体已含 `api_key_id/group_name/channel_id/model_name/status/success/input_token/output_token/cost/duration_ms`，gateway 的 `logCall` 已同步落库，但**缺费用计算、缺查询接口、缺清理**。

1. **费用在写日志时同步算出**：new-api 在 `RecordConsumeLog` 前就通过 `PostConsumeQuota` 算好 quota 一起写。portunus 已有 `model.Model` 的 `InputPrice/OutputPrice`（每 1M token），直接在 gateway 的 `logCall` 里用 `input_token×InputPrice + output_token×OutputPrice`（/1e6）算 `Cost` 落库即可，不必像 new-api 那样搞全局比值体系——portunus 的按 (渠道,模型) 定价天然更贴近实际账单。
2. **加一个 `Other`/扩展 JSON 字段**：new-api 用 `Other` 承载缓存/图片/工具等结构化明细。portunus 目前 `Log` 字段固定，后续若要记 cache 命中、reasoning 等明细，加个 JSON 字段比加列灵活。
3. **查询接口照搬筛选模式**：`GET /api/logs` 参考 `GetAllLogs` 的动态 WHERE + 分页，筛选维度对 portunus 即 `group_name / channel_id / model_name / status / 时间范围 / api_key_id`，分页用 offset/limit + Count 即可，无需一次查出全量。
4. **统计接口**：`GET /api/logs/stat` 返回 `sum(cost)`、`sum(input_token)+sum(output_token)`、近 60s 请求数，成本极低（一条聚合 SQL）。
5. **清理先做手动批量删**：new-api 也是手动 `DELETE` + 分批删，portunus 初期照抄即可（`Delete` where `created_at < t` limit 分批循环），暂不需要定时任务。
6. **索引规划**：日志表增长快，建索引应围绕「查询要筛的列」——portunus 的 `Log` 已有 `api_key_id/group_name/channel_id/model_name/success` 索引，够了；时间范围筛选用 `created_at` 列（若查询压力大再补复合索引）。
