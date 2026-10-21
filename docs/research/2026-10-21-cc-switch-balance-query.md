# CC Switch 渠道余额与额度查询调查

日期：2026-10-21。源码：`/Users/lixiuxiu/development_tool/projects/cc-switch`，HEAD `f92ff0b03964188f1cd011d9302ab8869d7a8219`。范围为当前本地版本，不代表远端最新版。仅只读检查代码和已有测试，未执行测试、未读取用户真实凭据、未查询真实上游。

## 结论

CC Switch 把账户余额、编程套餐额度、官方订阅额度和自定义站点查询放在同一个展示入口下，但使用不同查询机制。内置模板调用专门的 Rust 服务；NewAPI/Sub2API 等用声明请求与提取器的 JavaScript 模板。它读取上游已有余额/额度，不靠本地请求日志推算余额。

## 1. 查询入口和配置

统一入口 `src-tauri/src/commands/provider.rs:506`，内部 `:593` 读取供应商 `meta.usage_script.templateType` 后分发：

| 类型 | 实现 | 结果含义 |
|---|---|---|
| `balance` | `services/balance.rs` | 账户货币余额 |
| `token_plan` | `services/coding_plan.rs` | 套餐时间窗口的已用比例、重置时间等 |
| `official_subscription` | `services/subscription.rs` 等 | OAuth/CLI 所属官方账号的订阅额度 |
| NewAPI/Sub2API/custom 等 | `services/provider/usage.rs` → `usage_script.rs` | 配置指定接口返回的余额/配额 |

多数普通渠道需要手动启用查询，选择查询模板；托管 OAuth 账号另有自动展示路径。不能仅凭同一家厂商判断用户想看账户余额还是套餐额度。用户手册与代码对应：`docs/user-manual/zh/2-providers/2.5-usage-query.md`、`src/components/UsageScriptModal.tsx:157`。

配置包含 enabled、code、timeout、templateType、autoQueryInterval，以及查询专用 apiKey/baseUrl/accessToken/userId；套餐还可有 AK/SK、组织/项目 ID。保存在供应商 meta JSON 内，脚本配置结构见 `src-tauri/src/provider.rs:273`。查询脚本的 apiKey/baseUrl 显式非空覆盖优先，否则回退当前供应商配置；accessToken/userId 单独提供。不能把所有查询都理解为复用推理 Key。

## 2. 内置原生适配

`services/balance.rs:27` 按 base URL 识别厂商后，调用厂商专用接口：

| 厂商 | 当前本地代码请求的端点 |
|---|---|
| DeepSeek | `https://api.deepseek.com/user/balance` |
| StepFun | `https://api.stepfun.com/v1/accounts` |
| SiliconFlow | `https://api.siliconflow.cn/v1/user/info`，国际站另用 `.com` |
| OpenRouter | `https://openrouter.ai/api/v1/credits` |
| Novita AI | `https://api.novita.ai/v3/user/balance` |

这些是源码实际实现，不是本次对厂商最新 API 的在线核验。一般 Bearer API Key 鉴权，每次请求设置 15 秒超时。

DeepSeek 把各币种钱包映射成多条余额；OpenRouter 使用 `total_credits - total_usage`，特意不把 `/key` 的限额当账户余额，也不把零余额直接等同 Key 失效。证据：`services/balance.rs:114`、`:294`、`:338`，以及同文件解析测试。

套餐查询支持 Kimi、智谱、MiniMax、ZenMux、火山方舟、OpenCode Go、Command Code 等，接口与解析各自实现。火山方舟用独立 AK/SK 签名；智谱团队版显式选择并带组织/项目 ID。智谱明确没有套餐时回落账户余额，而不是任意查询失败都回落。证据：`services/coding_plan.rs:13`、`:1832`，`commands/provider.rs:650`、`:676`。

官方订阅读取对应 CLI/OAuth 或绑定账号凭据，例如 Claude 的 OAuth usage 和 Codex 的 wham usage；不是拿 OpenAI 推理 API Key 查询 ChatGPT 订阅额度。证据：`services/subscription.rs:135`、`:397`、`:955`。

## 3. NewAPI/Sub2API 模板

NewAPI 模板位于 `src/components/UsageScriptModal.tsx:95`：

```text
GET {{baseUrl}}/api/user/self
Authorization: Bearer {{accessToken}}
New-Api-User: {{userId}}

remaining = quota / 500000
used      = used_quota / 500000
total     = (quota + used_quota) / 500000
unit      = USD
```

这是用户账号管理接口凭据，与生成 API Key 分开。500000 和 USD 是模板写死的换算假设，不能认为所有站点、版本和计价设置都一致。未查到独立 OneAPI 模板；其兼容性需要针对实际站点验证。

Sub2API 模板（同文件 `:123`）使用推理 API Key Bearer 请求 `/v1/usage`，处理 baseUrl 末尾有无 `/v1`，解析 remaining、quota.limit、quota.used；不限量 `remaining=-1` 不当普通余额展示。

## 4. 脚本引擎的真实形态

脚本返回 `{request:{url,method,headers,body?}, extractor(response){...}}`。Rust 在 QuickJS 第一次执行脚本提取请求，释放运行时后执行 HTTP，再建第二个 QuickJS 运行时执行 extractor；不是直接给脚本开放任意异步 fetch。

每个 JS 阶段 5 秒、16 MiB 内存、256 KiB 栈；HTTP 超时 2–30 秒，默认配置 10 秒。非 custom 模板对 HTTPS（回环例外）以及 host/port 有校验，custom 放宽这些限制。因此只能说有基础约束，不能当作完整 SSRF 防护直接移植。占位符是字符串替换，不是结构化凭据注入。

证据：`src-tauri/src/usage_script.rs:33`、`:69`、`:126`、`:154`、`:184`、`:247`、`:424`、`:497`。

## 5. 统一数据与刷新

货币/脚本结果为 `UsageResult{success,data:UsageData[],error}`，每条数据可含 planName、remaining、total、used、unit、isValid、extra。套餐原生模型为 `SubscriptionQuota`，其中 tier 包含 name、utilization、resetsAt；卡片入口再适配展示。证据：`provider.rs:327`、`:351`，`services/subscription.rs:30`、`:68`。

普通查询由前端 React Query 管理，手动刷新可用；定时按配置分钟执行，最少 1 分钟，0 禁用定时。普通供应商仅当前使用中的卡片开启定时（累加应用按已加入配置判断）。网络/超时/读体失败重试一次，延迟 1.5 秒。短暂故障最多保留 10 分钟的上次成功值并保留其真实更新时间；鉴权等确定失败立即显示并清理旧可信快照。托盘另有进程内缓存，重启不保留。

证据：`src/components/UsageFooter.tsx:115`、`src/lib/query/queries.ts:175`、`:246`、`services/usage_cache.rs:1`。

## 6. 对 Portunus 的建议（推断，尚未实施）

1. 余额/额度查询作为渠道的独立可选能力，与协议转换、模型发现分开；可显示上游余额，但不恢复本地模型计价和费用计算。
2. 使用已保存的厂商 ID 和显式查询方式选择适配器，避免重复 CC Switch 的 URL 子串识别。
3. 区分账户余额、API Key 限额、时间窗口套餐额度；结果标明单位、范围、刷新时间，不混成一个数字。
4. 查询凭据允许继承渠道 Key，也必须能单独配置管理 Token、用户 ID 等；不要求所有厂商都能用现有 Key 查余额。
5. 先实现明确的原生适配器以及 NewAPI/Sub2API 结构化查询配置；通用脚本执行后置，避免第一版引入新的运行时和安全边界。
6. 缓存上次成功值，但陈旧状态与时间必须可见；查询失败不直接把渠道判为不可调用或触发生成熔断。

置信度：实现机制高，依据入口、服务、前端和测试交叉核对。未确认：真实厂商/站点权限、NewAPI 换算参数兼容性、动态网络及代理行为。本次不修改业务代码。
