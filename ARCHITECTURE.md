# Portunus 架构

本地运行的多上游 API 聚合网关：集中接入官方 API、New API 实例和其他中转站，以模型分组统一向客户端和应用提供服务。桌面应用是交付形态，Claude Code、Codex 等是调用方。产品对象与阶段范围见 [产品定义](./docs/product/PRODUCT.md)。

## 技术栈

- **语言**：Go 1.26
- **Web 框架**：Gin
- **ORM**：GORM + SQLite（起步，保留换 MySQL / PostgreSQL 的能力）
- **桌面与前端**：Electron + React + Vite；Go 后端作为 sidecar 随应用启动，管理数据保存在本地 SQLite

## 目录结构（backend/）

| 模块 | 职责 |
|---|---|
| `channel` | 渠道：已保存的厂商来源、协议、连接与凭据；展开创建默认值，校验更新 |
| `upstream` | 厂商预设与连接解析：统一校验 API 根地址，包装协议连接工厂，提供模型发现 |
| `quota` | 独立查询来源、内置／自定义适配器、结果缓存、版本隔离与有界后台调度 |
| `model` | 模型：管理渠道中的模型条目，支持发现、搜索与手动添加，供分组引用 |
| `group` | 分组：跨渠道聚合模型，**每个分组必配一个路由策略** |
| `router` | 路由解析：按分组策略决定调用哪个/哪些上游，返回「模型 + 渠道」 |
| `protocol` | 协议转换：客户端 Chat / Responses / Messages 与四类上游协议间按字段语义转换；Gemini 目前只作上游。托管工具声明按协议映射，不支持的目标会剥离并记录遥测；不承诺所有能力等价 |
| `api` | 管理 API：供本机前端调用（渠道 / 模型 / 分组、设置等）；`/api` 仅接受真实回环连接 |
| `gateway` | 对外 `/v1` LLM 接口：供客户端和应用调用，以 Portunus 令牌鉴权并按分组策略路由 |
| `cron` | 定时任务：同步模型、清理过期日志与尝试记录 |
| `shared` | 跨模块公共：配置、数据库初始化、APIKey、日志/尝试实体与查询 |
| `resource` | 无业务依赖的转换资源契约：大小、嵌套深度、静态超限错误与有界读取 |

依赖方向：`api` / `gateway` / `cron` → `router` / `channel` / `model` / `group` / `protocol` → `shared`。模块之间用外键 ID 关联，避免包级循环依赖。`resource` 不依赖业务包，允许 `shared`、`protocol`、`gateway` 共用其限额契约。

请求侧协议装配经 `RequestEnvelope` 保留原始数据和片段，目标检查随 `ComposeStats.Compatibility` 返回。兼容检查只记录选定原生构造的诊断，不据此排除目标或拒绝来源未验证的原生续接；已删除 strict/legacy 策略配置及拒绝分支，统一沿用既有调用行为。原生正文不入日志；摘要带 `request_native_v1` 范围，不能视为所有字段无损或供应商模型能力保证，详见[请求侧实施说明](docs/research/2026-10-23-protocol-native-s1.md)和[诊断模式说明](docs/research/2026-10-23-protocol-diagnostics-only.md)。

跨协议流式转换已采用 `streamDecoder → StreamEvent → streamEncoder`，内部不再经过 Chat JSON 中转；目标 encoder 复用既有块生命周期。保留同帧组合、原有优先级和非 Chat 间用量兼容投影，同协议继续原生 payload 透传。S3 已增加完整 SSE 原始帧保留、源终态观察和客户端交付门；元数据有界预缓存，交付开始或原生操作可能执行后禁止自动重放，缺终态不伪造成功，跨协议有界收集 usage 后编码唯一终态。S4 已将所有显式 Do（含兼容重试）纳入请求总预算（默认 4 次），并增加从选路前累计至首次交付的流式首内容期限（默认 120 秒）；交付后撤销该期限，保留 idle 控制。S6 将非流式 600 秒默认期限改为跨目标累计，统一在实际 Do 前安排可取消指数退避（200ms 起步、2 秒封顶，含兼容重试），详见[累计期限与退避说明](docs/research/2026-10-23-nonstream-deadline-backoff-s6.md)。S5 已增加响应原始信封、类型化事件的原生快照及独立响应诊断摘要；同协议原样返回，跨协议仍仅按已有语义映射输出，完整跨协议扩展恢复仍未承诺。详见[响应原生保留说明](docs/research/2026-10-23-response-native-s5.md)。详见[类型化流实施说明](docs/research/2026-10-23-protocol-typed-stream-s2.md)、[SSE/交付实施说明](docs/research/2026-10-23-protocol-sse-delivery-s3.md)和[请求控制说明](docs/research/2026-10-23-request-control-s4.md)。

S7 已对整条转换链实施可配置资源限额：请求/非流式响应各 32 MiB、原生结构片段 8192、单工具参数 4 MiB、每个有状态阶段累计内容 64 MiB、JSON 深度 128；成功/错误体和兼容重试均受限。超限停止整个请求且不计熔断，交付后不重放、不补成功终态。内部不可变原生快照在单帧拆分的语义事件间共享；同协议流不保留全文，因此不限制已传输的累计正文长度。详见[资源限额说明](docs/research/2026-10-23-conversion-resource-limits-s7.md)。

上游连接依赖为 `channel → upstream → protocol`；`model` 和 `gateway` 从渠道取得连接 DTO 后经 `upstream.Open` 建立连接。`upstream` 不引用渠道、模型或网关，厂商预设与消息语义保持分离。管理 API 的主动调用验证通过 `gateway.ProbeConnection` 复用正式转发能力，网关不反向依赖管理 API。

## 核心概念

- **渠道 Channel**：连接一个上游供应商的配置单元。协议类型 ∈ `openai` / `openai_responses` / `anthropic` / `gemini`。`渠道 1:N 模型`。
- **模型 Model**：从渠道发现后选择导入，或手动新增、归属某渠道的模型，是本项目的「原料库」。模型目录用于发现和引用上游资源，不提供价格配置。
- **分组 Group**：自定义分组名即对外暴露的模型名。从模型库跨渠道挑模型入组（可混协议），按策略路由。
- **路由策略 Strategy**：每个分组必配其一
  - `manual`：手动指定激活项，只打它
  - `failover`：按 priority、成员 ID 升序，符合切换条件的失败尝试下一个
- **服务鉴权**：命名的多把 Portunus API Key 用于 `/v1` 服务访问，以稳定 Key ID 记录调用方归属；支持逐把重置、可恢复禁用及软删除；日志保存鉴权时名称快照，历史用量继续按稳定 ID 归属；上游密钥保存在渠道配置中，两者不混用。尚无多用户或按调用方结算。

## 一次调用数据流

```

客户端(Chat / Responses / Messages) → /v1 → APIKey 鉴权
  → 命中分组 → 按策略选出一个模型项（渠道 + 模型）
  → 判断客户端协议 vs 上游协议 → 不一致则转换
  → 上游调用 → 转回客户端协议 → 返回
  → 经注入 writer 同步写 relay 日志 / 独立 HTTP 尝试，保留 token 与缓存用量
```

## 厂商预设与渠道接入

厂商标识独立于渠道 `type` 协议。后端维护内置预设，经 `GET /api/upstream-presets` 给前端共享表单使用；预设仅在创建时或显式应用时复制到渠道，既有配置不随目录升级改变。运行时不读取预设默认值，不按域名推断厂商。

Base URL 是包含版本或自定义路径前缀的 API 根地址，协议工厂追加 `/chat/completions`、`/responses`、`/messages` 或模型生成路径。新配置拒绝完整操作 URL、URL 凭据、query 与 fragment；旧渠道运行时维持原端点拼接行为。

渠道创建/更新与所选新增模型写入处于一个管理层事务；`model.ImportNamesTx` 统一去重并保留已有模型 ID。模型预览只读、支持取消；手动与定时同步只新增全部发现模型。创建、编辑共用草稿状态、连接字段、模型选择和调用验证组件。

主动调用验证复用 `gateway.relayToTarget`，只验证指定渠道和模型，私有熔断器与内存日志隔离正常调用数据，不进行路由切换或普通失败重试。协议转换、流式处理和超时沿用正式链路，另限制总时间和响应大小。配置预设及模型列表成功不代表真实调用通过，详见[渠道接入说明](docs/upstream-channels.md)。

## 关键决策

1. **从零重写**，仅参考 octopus 的功能与交互；许可证自选（octopus 为 AGPL-3.0，不 fork）。
2. **用量与诊断优先**：记录上游 usage、缓存 token、耗时与错误，不提供模型价格配置、默认价格套用或调用费用计算。实际费用由上游账单提供。
3. **三类客户端生成入口、四类上游协议**：客户端使用 Chat Completions / Responses / Messages；上游支持 openai / openai_responses / anthropic / gemini。同协议透传、跨协议映射，能力受目标协议与上游限制。
4. **分组可混协议**，relay 时按需动态转换。
5. **模型自动拉取**：启动同步 + 定时同步 + 手动触发；同步只新增发现的模型，保留已有模型，不自动删除上游缺失项，不进行实际生成验证。
6. **框架沿用 Gin + GORM**（从零是摆脱 octopus 的业务负担，不必连成熟框架一起弃用）。
7. **托管工具只适配、不代跑**：Claude Code 的 WebSearch 等服务端工具由上游执行（new-api / cc-switch 同一立场）。Portunus 在支持的路径上转换声明、历史块与接地数据，上游协议无此能力时剥离并记录遥测，不在网关侧执行搜索（设计考证见 `docs/research/2026-09-29-hosted-websearch-bridging.md`）。

## 管理面与服务面

- 管理面为 `/api`，检查 TCP 对端 `RemoteAddr` 是否为 IPv4/IPv6 回环地址；远端请求返回 403。`X-Forwarded-For`、`X-Real-IP` 等转发头不授予管理访问权。同时校验本机 Host 与允许的本机 Origin，限制 DNS rebinding 和跨站管理请求；桌面 file:// 界面通过应用请求头适配。
- 服务面为 `/v1`，沿用现有令牌鉴权。桌面版默认仅本机监听；在“服务接入”开启局域网模式后，其他设备凭令牌调用服务，不能直接读取管理接口或服务令牌。
- 本机反向代理是受信任的本机连接，不能用于向远端公开 `/api`。开发代理也只允许本机管理访问；公网部署与加密、管理身份认证需另行设计。
- 此调整不迁移数据库、不重置令牌、不改渠道密钥、分组名称或既有监听配置。

## 页面与观测口径

导航以渠道为首页：渠道 → 模型 → 分组 → 日志 → 接入 → 设置。接入页以上方连接配置、下方「接入示例 / 客户端配置」树组织：示例只生成可复制内容，客户端配置在桌面端读写 Claude Code 的 `~/.claude/settings.json`（首写备份、原子写、可回滚）。设置聚焦应用维护。

调用记录按旧 logs 日志行统计；同一客户端请求可能有多条重试/目标切换记录。预算兼容重试已在独立 upstream_attempts 表留痕，不并入原统计，因此旧指标不能命名为准确请求数或完整上游调用数。保留 token 和缓存用量统计，不计算或聚合费用。流式响应向客户端提交后，中途断流不支持无损重放或切换。

为兼容既有数据，模型价格、货币和日志费用的历史数据库列及旧值保留，不删除、不清零；它们不再进入活跃管理请求/响应、调用费用计算或统计聚合。此保留是存储兼容措施，不代表产品继续提供计价能力。


## 分组配置与模型目录

创建和添加成员共用服务端模型目录查询：`GET /api/models` 支持 `channel_id`、`name`、`page`、`page_size`，分组选择器每页请求 20 条，跨页、跨渠道保留完整已选模型快照。分组页只取 1 条模型以读取总数，不再加载前 1000 条作为可选资源全集。

分组列表、详情及创建/更新响应中的成员包含 `model_name`、`channel_id`，由一次批量关联查询读取，不新增数据库列，不逐成员查询。`POST /api/groups` 可一次写入多个成员；可选 `active_model_id` 仅用于 manual 且必须属于初始成员，成员创建与激活同事务完成。省略或传 0 保持旧客户端未激活语义。故障转移按成员 priority 升序尝试。

## 工程质量检查

[CI](.github/workflows/ci.yml) 在分支提交、PR 及手动触发时执行 Go 测试、静态检查与构建，以及锁文件安装、前端测试与 renderer 构建。[发布流程](.github/workflows/release.yml) 复用同一检查，成功后才打包。配置和本地验证已完成，GitHub 线上运行及远端必需状态检查尚未验证/配置，详见[开发说明](docs/product/DEVELOPMENT.md#持续集成与发布检查)。

调用记录已支持渠道与 request_id 精确组合筛选，列表总数与统计共用过滤；前端提供实际渠道/模型展示、单条日志详情和请求 ID/错误摘要复制。详情使用现有日志响应，不新增存储字段；渠道名称来自当前配置，历史以 ID 为准。独立尝试记录已补齐显式 HTTP 调用，请求关联归并和尝试链界面已实现；旧日志行数与关联项数仍不代表准确客户端请求数或完整物理上游发送数。验证范围见[日志详情验证](docs/research/2026-10-21-log-details/README.md)。


## 上游尝试记录

日志库独立表 `upstream_attempts` 记录每次显式 `http.Client.Do`；主进程注入 `Deps.AttemptWrite`，按同一 `request_id` 和递增 `sequence` 关联，`relay_attempt` 标识外层 relay，`reason` 区分 initial/retry/failover/budget_compat。兼容重试在第一条 HTTP 失败记录结束后再开启下一条，取消/连接失败和流中断各保留真实处理结果；装配失败和熔断跳过不生成 HTTP 行。

此表不重复记录 token，不改旧 Log 汇总。只保留目标标识、协议、预算字段名、状态、耗时和截断错误，不存请求/响应正文、URL 或鉴权头。显式 Do 记录不等同于 Transport 内部重发、重定向次数或上游确实收到请求的证明；success 也不等同于模型 completed。

管理查询为 `GET /api/logs/attempts?request_id=...&page=1&page_size=20`，必须指定精确请求 ID，按 sequence/id 升序分页。迁移不回填历史，过期尝试按 started_at 与日志共用截止时间清理，原 deleted 返回值仅计算旧日志。写入失败只警告，不因观测失败额外调用上游；缺失记录不能当作完整链路。见[验证记录](docs/research/2026-10-21-upstream-attempts/README.md)。


## 请求关联归并与尝试链界面

`GET /api/logs/requests` 根据旧 LogFilter 选中关联项：任一日志匹配即可命中，但摘要从同请求 ID 的全部持久化日志计算，防止筛选失败渠道时隐藏后续成功。无 ID/NULL 的历史日志各自独立，key 使用 request:/log: 前缀。数据库在同一读事务中分组分页并批量读取最新/时间边界日志，非空页固定 3 次查询，不加载全量日志到内存。latest_log 按最大写入 ID 选取，first_at/last_at 为时间范围；界面最近时间、渠道和结果统一来自 latest_log。

日志页保留原记录视图与统计卡，新增关联归并视图，详情按页读取独立尝试。连续序号仅表示当前页连续；缺口、未采集、无请求 ID 均保留不完整/未知提示。不以空数组证明 0 次实际调用，不以最近日志结果推断请求最终成功。当前不提供准确请求成功率，后续需要请求终态和采集完整性证据。见[验证记录](docs/research/2026-10-21-request-associations/README.md)。


## 渠道余额与额度查询

`backend/quota` 独立负责来源配置、适配器、只读执行、缓存状态和有界调度；不依赖 channel、upstream、protocol 或 gateway。主进程注入事务内的 ChannelLoader，API 管理层协调渠道 Key 变更与来源版本失效，同事务提交后取消旧请求。查询结果只用于展示，不参与模型路由、熔断、用量或费用统计。

来源与状态分表；凭据不进入读取 DTO。每次修改使用 config_revision 乐观锁，网络位于事务外，写回再次检查身份、版本及启用状态。单来源一个请求、全局两个请求，定时任务使用两个 worker 和有界队列；修改、删除、禁用及应用退出取消旧任务。正常成功记录随时间变旧仍显示上次查询值和真实查询时间；临时错误保留受时间限制的旧快照，鉴权失效后不再展示旧身份余额。

首批适配器为 DeepSeek、硅基流动中国站、OpenRouter 账户及 NewAPI 账户。适配器注册表提供配置描述符，前端配置面板与摘要共享独立 hooks；读取缓存与发起查询分开。金额与额度使用十进制字符串，未知、0、无限分离；NewAPI 默认原始 QUOTA，显式换算后保留原始额度供详情核对。见[使用说明](docs/channel-quota.md)和[实施验证](docs/research/2026-10-21-quota-ab-implementation.md)。

`useQuotaSummaries` 负责批量缓存读取、卡片刷新命令、来源互斥与迟到响应隔离；202 后只读 GET 轮询，打开页面不主动查询上游。`useQuotaDraft` 负责草稿测试／保存；“保存并查询”显式启用来源并立即查询一次，间隔按表单保留。`QuotaSummary` 负责摘要金额、真实查询时间和动作，完整条目由详情展示，不按厂商分支计算金额。失败保留窗口从上次成功起算为 max(10 分钟, 2 × 新鲜期)，不能用于抹除正常闲置成功值。后续修正与验证见[余额展示记录](docs/research/2026-10-21-balance-display/README.md)。


自定义查询使用 `custom.balance` 及类型化 CustomConfig，在同一适配器/执行器边界中构造 GET 或声明的只读 POST、JSON Pointer 提取与精确十进制换算。不运行脚本、不增表、不另建调度。配置仅保存 Token 占位符；更换查询根地址清除旧专用 Token。JSON 条件保存标量原文（equals_json），避免前端 JSON Number 舍入；无变化配置按值比较，空请求头统一为 nil。表单与序列化分别放在 CustomQuotaEditor、customQuotaModel，渠道页不解析响应或计算金额。

## 桌面布局与界面层次

`Layout.jsx` 与 `workspace.css` 维护顶部胶囊导航和内容区外壳；六个路由和默认渠道入口保持不变。`resource-style.css` 统一标题、工具栏和资源卡节奏，页面级样式处理各自密度与响应式。顶部导航复用 `useLiquidNav` 的玻璃动效和 Tabs 键盘交互，窄窗口换行避免遮挡品牌与外观开关；内容跳转避免修改 HashRouter hash。

渠道本地搜索只索引公开配置元信息，模型仍在服务端分页筛选；日志常用/高级筛选分层并持续展示已生效条件，小窗口通过页面滚动保留表格和完整筛选。额度卡片紧凑摘要与完整详情分开；配置表单内滚动，提交与错误保留固定区。该轮只改视图、布局和本地目录搜索，不增加 API、金额计算、数据库或第二套状态框架。验证见[布局改进记录](docs/research/2026-10-21-layout-refresh/README.md)。

当前仅支持手动与故障转移；启动时把历史 `round_robin` 分组迁为 `failover`，保留成员和优先级，新的创建/更新请求拒绝旧策略。固定主备不使用权重或动态评分。迁移验证及故障转移改进记录（429冷却和有限恢复探测已实施）见[策略收敛与故障转移说明](docs/research/2026-10-23-two-strategies-and-failover.md)。

429 已按作用域独立冷却：渠道设置 `rate_limit_scope` 默认model，仅冷却该连接对应上游模型名；显式channel时影响该连接全部模型。真实429跳过同目标且不计故障熔断，固定故障转移顺延，手动不换成员。进程内有界RateLimitStore按连接指纹隔离变更，选路与实际Do前都检查冷却（含退避后/兼容重试），跳过不扣预算、不造attempt；配置默认30秒/上限300秒。已交付、原生操作、取消及资源等安全边界优先。详见[作用域冷却说明](docs/research/2026-10-23-scoped-rate-limit-cooldown.md)。

半开恢复按模型ID跨分组共享一个真实relay探测名额：`Allow`仅预览，实际Do前在退避、控制预算和限流检查后原子取得租约，兼容重试共用，完整body/流式结束并关闭后释放，先于最终观测写入。成功按已有阈值恢复（默认2次），探测健康失败立即重开，429/取消/本地错误中性释放。租约不按固定TTL提前失效，代次隔离迟到结果；忙时故障转移仍按固定主备顺延、手动不切成员，不制造HTTP attempt。详见[半开探测租约说明](docs/research/2026-10-23-half-open-probe-leases.md)。

切换诊断由独立 `RoutingTrace` 保存，每请求最多256条元数据事件、显式记录丢弃数并独立保留最终摘要。覆盖候选跳过、Do前准入、半开租约、固定备用选择及真实停止原因，不改变Log/用量/HTTP attempt口径；生产注入RoutingWrite，失败不重试上游。管理接口 `/api/logs/routing` 精确按request_id查询，日志详情独立展示，缺失不猜测。详见[切换诊断说明](docs/research/2026-10-23-routing-diagnostics.md)。

渠道可选 `max_concurrent_requests`，默认0不限。进程共享ChannelConcurrencyStore按渠道ID统计/v1生成relay，跨模型跨分组，连接/上限变动不分桶且不限调用也计在途；配置随选路快照，后续新解析请求读新值。实际Do前退避/控制/冷却后原子准入，整body/stream/兼容Do共用名额，关闭body后、最终writers前释放；满载本地channel_capacity503，固定failover顺延、manual不切换，拒绝不造HTTP attempt、不计熔断。详见[渠道并发说明](docs/research/2026-10-23-channel-concurrency.md)。


## 协议与故障转移验收收口（2026-10-23）

暂停新增功能，当前main构建已完成独立Dev真实上游普通对话、流式、工具闭环，以及本机故障主渠道切真实备用验收。本轮只修复Responses/Chat指定函数tool_choice的flat/nested请求格式转换；同协议透传不变。DeepSeek默认思考模式不支持强制工具选择仍是上游约束，auto或显式关闭思考的路径已实测。正式版未替换，SDK/CLI及打包桌面不在本轮范围。见[实际验收](docs/research/2026-10-23-protocol-failover-acceptance/README.md)和[集中待办](docs/research/2026-10-23-protocol-failover-backlog.md)，后续按实际问题修复，不继续堆加能力。
