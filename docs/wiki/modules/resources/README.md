# 资源管理：渠道、模型与分组

## 文档信息

| 字段 | 内容 |
|---|---|
| 适用范围 | 管理端渠道与上游连接、模型目录与同步、模型分组与 manual/failover 策略、删除模型/渠道的影响预览与事务级联 |
| 事实状态 | 已核验 |
| 核验基线 | `b44d75a788f8aa5ef0ad024273d4bf5dd8c95c7e`（dev） |
| 核验时间 | 2026-10-28 |
| 核验方式 | 静态核对源码与测试代码；未执行 `go test`、前端测试、构建或手工接口调用 |

## 用途与边界

本模块管理网关的“上游资源”与“对外服务入口”：渠道保存连接配置；模型是渠道下的上游资源目录；分组把多个渠道模型聚合为客户端可见的 `model`，并配置 `manual` 或 `failover` 路由策略。删除模型或渠道前，管理 API 提供只读影响预览；确认后在单个事务内清理分组成员并清空相关激活项。

本模块直接负责：

- 渠道 CRUD、连接预设解析、保存时选择性导入模型。
- 模型手动增删改、按渠道/名称分页查询、全量新增式同步。
- 分组 CRUD、成员增删与优先级、手动激活、故障转移顺序。
- 模型/渠道删除影响预览，以及删除时的分组成员、激活项、余额来源级联清理。
- 路由策略到目标列表的解析（`backend/router`）；实际请求重试、故障转移执行不在本模块。

不负责或需要到其他模块查看：

- `/v1` 鉴权、协议转换、实际转发、重试/故障转移执行、熔断、限流与并发准入：见[网关与协议](../gateway/README.md)。
- 余额查询的网络执行、缓存、适配器与调度：本模块只在渠道 Key 变更或渠道删除时触碰余额来源生命周期；详见[余额与额度](../quota/README.md)。
- 调用日志与独立上游尝试的写入、查询和保留策略：删除资源不会清理历史记录；详见[调用观测](../observability/README.md)。
- 管理 API 的本机回环限制、启动迁移与定时任务装配：详见[运行时与配置](../runtime/README.md)。
- Electron 页面、导航与桌面生命周期：见[桌面端](../desktop/README.md)。

关键边界：

- 分组名就是客户端请求中的 `model`；`/v1/models` 只跳过没有任何分组项行的分组（`backend/gateway/gateway.go:87`）。有历史悬挂成员但无可解析目标的分组仍可能被列出，产品文档把“配置不完整分组的公布规则”列为待确定。
- 同步只新增模型，不删除上游已消失的模型，也不执行生成验证。
- 删除影响预览是只读快照，不是锁或删除前置条件；直接调用 `DELETE` 仍会级联，预览后并发修改可能使展示过期。
- 删除影响预览只覆盖单个模型或渠道的删除场景，不提供独立的“哪些分组引用哪些模型”全量反向引用目录。
- 删除模型/渠道不会自动补位：不新增成员、不提升备用成员、不重排剩余成员优先级。
- 产品不提供价格、货币或费用配置；模型旧计价列仅保留历史数据，不进入当前管理接口。
- 删除渠道/模型/分组都不删除历史调用日志或上游尝试；日志保留由设置中的保留天数单独控制。

## 主要流程

### 渠道保存与连接解析

#### 新建渠道

1. `POST /api/channels`（`backend/api/channel.go:59`）绑定 `channel.CreateRequest` 和可选 `models` 数组。`name`、`key` 是必填字段。
2. `channel.CreateTx`（`backend/channel/service.go:131`）校验名称和 Key 非空、限流范围、并发上限和图标；然后调用 `upstream.Resolve` 展开厂商预设和一次性默认值。
3. API 层在同一个事务中先建渠道，再用 `model.ImportNamesTx` 导入所选模型（`backend/api/channel.go:69`）。模型名非法或导入失败会让渠道一起回滚。
4. 响应 `201`，`channel.ToResponse` 返回脱敏 Key（长度大于 8 时保留前后各 4 位，否则为 `****`）。

保存时的默认值与规则：

| 字段 | 当前实现 |
|---|---|
| `provider_id` | 空值按 `generic`；必须是 `upstream.Presets()` 中的预设 ID |
| `profile_id` / `type` | 有 `profile_id` 时精确匹配；无 `profile_id` 时有 `type` 按协议匹配，否则用预设的 `default_profile`；协议必须与配置档一致 |
| `adapter_id` | 空值取配置档的 `adapter_id`；当前只接受 `generic` |
| `base_url` | 为空时填配置档默认地址；否则保留调用方地址；统一去首尾空格和末尾 `/` |
| `connection_options` | `schema_version` 空值视为 1，只接受 1；`model_discovery` 空值取配置档默认 `api`，只接受 `api`/`manual` |
| `rate_limit_scope` | 空值归一化为 `model`，只接受 `model`/`channel`；运行语义见 gateway 模块 |
| `max_concurrent_requests` | 非负且不超过 `9007199254740991`（与前端安全整数上限一致）；0 表示不限制 |
| `icon` | 空值跟随厂商；内置名称需匹配 `[a-z0-9][a-z0-9_-]{0,63}`；自定义图标仅接受不超过 64 KiB、256×256 以内的 PNG data URI |
| `auto_sync` | 默认 `true`，显式 `false` 会保留；模型发现为 `manual` 时强制为 `false` |
| `thinking_compat` | 默认 `true`，显式关闭会保留；保存时用显式更新绕过 GORM `default:true` 对 `false` 的覆盖 |

#### 上游预设与连接校验

- `GET /api/upstream-presets`（`backend/upstream/presets.go:25`）返回预设目录：通用兼容服务（Chat Completions、Responses、Messages、Generate Content 四档）、OpenAI（Chat/Responses）、Anthropic、Gemini、DeepSeek、OpenRouter、SiliconFlow。预设只提供协议、默认地址和帮助信息，不代表账号能力或已做真实调用验证，也不会自动启用余额查询。
- `upstream.Resolve`（`backend/upstream/connection.go:41`）负责保存/草稿的严格校验：未知厂商、未知或不兼容配置档、协议与配置档不一致、非 `generic` 适配器、未知协议或设置版本都会返回错误，由 `resolveConnection` 转成 `400`。
- 地址校验（`validateRoot`）要求 HTTP/HTTPS、主机名非空，拒绝 URL 凭据、查询参数、片段，以及路径以 `/chat/completions`、`/responses`、`/messages`、`/models` 结尾或包含 `:generateContent`、`:streamGenerateContent` 的“操作地址”。`generic` 预设地址为空，因此新建通用渠道必须显式填写 API 根地址。
- `upstream.Open`（`backend/upstream/connection.go:148`）只规范化选项并构建协议接入器，不重新套用预设或校验已保存地址；升级后历史渠道的请求目标不会被静默改写。`FetchModels` 在 `manual` 模型发现模式下直接拒绝。

#### 草稿解析、模型预览与调用验证

三个接口共用 `connectionDraft`（`backend/api/channel_connection.go`）：

| 接口 | 行为 |
|---|---|
| `POST /api/channels/resolve` | 解析草稿并返回白名单字段（协议、预设、配置档、脱敏后的地址与 Chat URL），不要求 Key，不落库 |
| `POST /api/channels/preview-models` | 拉取上游模型列表，只读、不落库；上游失败返回 `502` 和 `stage=model_discovery`；要求有 Key |
| `POST /api/channels/test` | 主动调用验证一个模型；模型最长为 512 字节，输出上限默认 32、允许 1–128；最多 25 秒、禁止重试；通过参数校验后探测结果始终以 HTTP `200` 返回，需看 `success`/`stage` 判断 |

草稿规则：

- 带 `channel_id` 时从后端读取已保存连接，可在省略 `key` 时复用已存凭据；显式提供的字段覆盖草稿，显式空 `base_url` 对已存渠道的草稿直接 `400`。新建草稿的空地址会回落到预设默认地址；`generic` 没有默认地址，因此仍会校验失败。
- 只有新建渠道或连接字段发生变化时才执行严格 `Resolve`；未修改连接的已存渠道保留历史地址行为。
- 草稿、预览和验证都不写渠道或模型；验证使用临时 `channel.Channel` 和 gateway 的独立探测连接池，不写正式日志、熔断状态或路由记录。

#### 编辑渠道

`PUT /api/channels/:id`（`backend/api/channel.go:85`）在事务中执行：

1. 读取旧 Key，用于判断凭据是否变化。
2. `channel.UpdateTx`（`backend/channel/service.go:206`）只应用非 nil 字段；空 Key 保留原凭据；显式空 `base_url` 返回 `400`；名称去空格后为空返回 `400`。
3. 仅当连接配置实际变化时才重新 `Resolve` 并校验；纯名称、限流或并发等元数据编辑不会因历史地址被重新拦截。
4. Key 变化且注入了余额服务时，调用 `quota.InvalidateInheritedSourcesTx`：只对继承渠道凭据的来源递增配置版本并清空缓存状态，不删除来源本身。
5. 同一事务内 `ImportNamesTx` 只新增本次 `models` 中的模型；未勾选的既有模型不会被删除。
6. 事务成功后，若 Key 变化则取消该渠道正在进行中的余额查询。

修改渠道协议或地址会影响该渠道下全部模型；引用这些模型的分组无需改成员，但实际请求目标和可用能力会改变。接口本身不会迁移模型或改分组。

### 模型目录：选择性导入、同步与手动维护

模型实体以 `(channel_id, name)` 唯一（`backend/model/model.go:14`），同一名称可以存在于不同渠道。列表支持按渠道、名称关键词（不区分大小写）过滤，默认每页 20 条，按名称和 ID 排序。

#### 选择性导入

`model.ImportNamesTx`（`backend/model/import.go:20`）是渠道保存与同步共用的导入入口：

- 名称先 trim，空名称或超过 512 字节会让整批导入失败。
- 同批次重复名称去重；单次最多 10000 个。
- 使用 `ON CONFLICT (channel_id, name) DO NOTHING`，只新增，不更新既有模型，保留既有 ID、分组引用和历史数据。
- 校验阶段先于写入；任一非法名称都会整批失败，调用方事务回滚。
- `models` 为空数组表示本次不新增；更新时省略 `models` 同样不新增。两者都不会删除既有模型。保存渠道本身不会触发全量模型发现或同步，自动同步由独立开关和调度触发。

#### 手动模型

- `POST /api/models`：名称非空（只拒绝空字符串，不 trim；导入路径会 trim），渠道必须存在；同渠道同名返回 `409`。
- `PUT /api/models/:id`：可改名或改渠道；渠道必须存在。改渠道后分组成员仍引用同一模型 ID，实际路由目标随之变化。
- `DELETE /api/models/:id`：只暴露事务删除入口 `model.DeleteTx`，级联清理由调用方在同一事务中完成，避免出现悬挂分组引用。删除语义见下节。

#### 同步

同步入口与约束：

| 入口 | 行为 |
|---|---|
| `POST /api/channels/:id/sync` | 读取已保存渠道，拉取上游模型并只新增缺失项，返回新增数量 |
| `POST /api/settings/sync-now` | 同步所有 `auto_sync=true` 且非手动发现的渠道，返回成功渠道数与新增总数，并记录最近同步时间 |
| 后台定时任务 | 每分钟检查一次；全局 `sync_interval` 默认 360 分钟，0 表示关闭；达到间隔后执行与“立即同步”相同的全量新增逻辑 |

`model.SyncFromChannelContext`（`backend/model/sync.go:21`）先用保存的连接配置 `upstream.Open`，再请求上游模型列表，最后在一个事务中导入。`manual` 模型发现模式返回 `400`，不发起网络请求。`SyncAutoChannels` 对单个渠道失败只记录日志并继续，不中断其他渠道。同步失败、获取列表成功都不等于生成可用；同步不会删除上游已消失的模型，也不会更新已有模型。

#### 三种验证含义

| 操作 | 能证明什么 | 不能证明什么 |
|---|---|---|
| 检查/解析连接配置 | 本地字段合法，能展开预设并构造协议端点 | 上游可达、凭据有效、模型可用 |
| 获取模型列表 | 上游模型列表接口成功 | 有生成权限、能完成对话 |
| 调用验证 | 指定模型在该响应模式下的一次真实调用成功 | 其他模型、其他响应模式或后续调用一定成功；验证可能消耗上游额度 |

### 分组与路由策略

分组是客户端可见的 `model` 名称；`Group.Name` 唯一，`Strategy` 必须为 `manual` 或 `failover`（`backend/group/group.go`）。分组项通过 `ModelID` 引用模型，`GroupItem` 对 `(group_id, model_id)` 唯一。

#### 创建分组

`group.Create`（`backend/group/service.go:115`）先做只读校验，再在一个事务中写入分组、成员和初始激活项：

- 名称非空、策略合法；名称只拒绝空字符串，不 trim（渠道保存按 trim 后判空）。
- `active_model_id` 非 0 时只允许 `manual`，且必须出现在本次 `items` 中。
- 所有 `items.model_id` 必须存在，否则 `400`。
- 事务内逐项创建成员；若同一模型重复出现，唯一索引冲突返回 `409`，分组、成员和激活项全部回滚；激活项写入失败同样整体回滚。
- 未提供 `active_model_id` 或显式传 0 时保持未激活，兼容旧客户端；`failover` 分组通常不设置激活项。`items` 可以为空，此时创建的是空分组，后续再添加成员。
- 响应中的成员元数据（`model_name`、`channel_id`）由 `group_items` 左连接 `models` 批量读取，不新增数据库列。

#### 更新分组与成员

- `group.Update` 只应用非 nil 的 `name`、`strategy`、`active_item_id`；激活项非 0 时必须属于该分组。切换 `manual`/`failover` 不会清空已有激活项。创建时 `failover` 拒绝 `active_model_id`，但更新接口不按策略限制 `active_item_id`；该字段在 `failover` 路由中不被使用。
- `AddItem` 校验分组存在、模型存在后创建成员；重复模型由唯一索引返回 `409`。
- `UpdateItem` 只改 `priority`；成员必须属于路径中的分组。
- `DeleteItem` 只删除分组项行，不检查影响行数，也不清空 `ActiveItemID`。因此手动删除当前激活成员后，分组的 `active_item_id` 仍指向已删除行；前端会显示“配置不完整：请选择当前模型”，路由解析返回 `ErrNoActiveItem`。这与删除模型/渠道时自动清空激活项的行为不同。
- 分组列表和详情按 `priority`、成员 ID 升序返回；拖拽排序由前端重写优先级，后端不自动重排。

#### 路由解析

`router.Resolve`（`backend/router/router.go:26`）在调用时把分组解析为目标列表：

- 空分组返回 `ErrEmptyGroup`。
- `manual`：只返回 `ActiveItemID` 对应的成员；找不到激活项返回 `ErrNoActiveItem`。
- `failover`：按 `priority` 升序、同优先级按成员 ID 升序返回全部目标，供网关依次尝试。
- 每个目标都会加载模型和渠道。`failover` 会逐项加载，任一历史悬挂成员（引用不存在的模型）都会让解析返回错误；`manual` 只加载激活项，仅当激活项自身悬挂时才报错，其他悬挂成员不影响本次解析。
- 删除资源后不会调整剩余成员的 `priority`；故障转移保持原有相对顺序，手动分组不会自动选择新激活项。

旧版 `round_robin` 策略在启动时由 `group.MigrateLegacyStrategies`（`backend/group/migrate.go:7`）改为 `failover`，保留成员优先级、激活项和时间戳；重复执行无额外变更。创建或更新仍提交 `round_robin`、`adaptive` 等已移除策略会返回 `400` 且不写入。

### 删除影响预览与事务级联

#### 预览接口

| 接口 | 目标校验 | 额外字段 |
|---|---|---|
| `GET /api/models/:id/deletion-impact` | 模型必须存在，否则 `404` | 仅 `groups` |
| `GET /api/channels/:id/deletion-impact` | 渠道必须存在，否则 `404` | `model_count`、`has_quota_source` |

非整数 ID 返回 `400`。响应结构见 `backend/api/deletion_impact.go` 与 `backend/group/deletion_impact.go`：

| 字段 | 含义 |
|---|---|
| `group_id` / `name` / `strategy` | 受影响分组及其策略 |
| `removed_item_ids` | 将随目标删除的分组项 ID，升序 |
| `clears_active` | 分组当前 `active_item_id` 是否指向将被删除的成员；不区分策略，只看是否非 0 且命中 |
| `becomes_empty` | 删除后该分组是否不再有任何分组项行 |
| `model_count` | 渠道下全部模型数量，不只限于被分组引用的模型 |
| `has_quota_source` | 渠道是否存在余额查询来源；仅当注入了 `quota_service` 且配额表存在时计算，否则为 `false` |

预览实现（`group.PreviewDeletionImpact`）：

- 只读，不开启事务、不写任何表。
- 命中引用时固定执行三次查询：目标模型对应的分组项、这些分组、这些分组的全部成员；查询次数不随分组数量增长。无模型 ID 或无引用时提前返回空切片。
- 结果按分组 ID 升序，组内移除项按 ID 升序；无影响时返回 `[]` 而非 `null`。
- 空组判断统计全部成员行，包括引用已缺失模型的历史悬挂项。因此只要还残留悬挂成员，`becomes_empty=false`，即使删除后没有任何可解析目标。
- `has_quota_source` 的判定与删除路径保持一致：只有实际注入了余额服务时删除才会清理来源，预览不会承诺删除路径不会执行的动作。生产启动总会迁移配额表并注入余额服务（`main.go:43`、`main.go:83`）；未注入的自定义装配不会清理来源，预览也会报告 `false`。

预览不阻止直接删除，也不做“预览结果与删除时数据一致”的二次校验；前端在预览失败或响应字段不完整时禁用确认按钮，但绕过前端的 API 调用仍可直接删除。

#### 确认删除时的级联

删除模型（`backend/api/model.go:96`）在单个事务中：

1. 确认模型存在，不存在返回 `404`。
2. `group.DeleteItemsByModelIDsTx`：找出所有引用目标模型的成员；把 `active_item_id` 指向这些成员的统一清零；删除这些成员。
3. `model.DeleteTx` 删除模型。

删除渠道（`backend/api/channel.go:130`）在单个事务中：

1. 若注入了余额服务，先 `quota.DeleteByChannelTx` 删除该渠道的余额来源及其缓存状态。
2. 收集该渠道全部模型 ID。
3. `group.DeleteItemsByModelIDsTx` 跨分组删除引用这些渠道模型的成员，并清空相关激活项；同一分组中来自其他渠道的成员保留。
4. 删除该渠道全部模型，再删除渠道。
5. 事务成功后取消该渠道进行中的余额查询。

级联条件与保留项：

| 对象 | 删除模型 | 删除渠道 |
|---|---|---|
| 目标模型 | 删除 | 删除该渠道全部模型 |
| 引用目标的分组成员 | 删除 | 删除引用这些模型的分组成员 |
| 分组 | 保留 | 保留 |
| 同组其他成员 | 保留，优先级不变 | 保留，优先级不变 |
| `active_item_id` | 仅当指向被删成员时清零 | 同左 |
| 其他渠道模型/成员 | 不受影响 | 不受影响 |
| 历史调用日志、上游尝试 | 保留 | 保留 |
| 余额来源 | 不涉及 | 注入余额服务时删除 |
| 空分组 | 保留 | 保留 |

删除后不会自动补位：`manual` 分组激活项被清空后解析为 `ErrNoActiveItem`；`failover` 继续按剩余成员的原顺序尝试；没有成员行的分组保留在数据库中，但不会出现在 `/v1/models`。分组名称、历史日志和尝试记录都保留，便于继续查询历史调用。

#### 前端确认弹窗

`web/src/renderer/pages/Models/DeleteModelModal.jsx` 与 `Channels/DeleteChannelModal.jsx` 共用 `web/src/renderer/utils/deletionImpact.js`：

- 打开时按目标 ID 加载预览；失败显示错误并允许重试，未知影响不能确认删除。
- 渠道预览缺少 `model_count` 或 `has_quota_source` 时按“影响不完整”处理，不把缺失降级为 0/无。
- 切换目标或关闭弹窗会丢弃迟到响应；确认删除使用“已预览过的目标”，防止弹窗打开期间换对象后误删。
- 删除失败保留在弹窗中并可重试；成功后关闭并提示。
- 分组删除弹窗没有接影响预览，因为删除分组只移除分组及其成员关联，不删除渠道模型。

### 请求错误、事务边界与修改影响

#### 请求错误

管理 API 统一经 `respondError`/`classifyError`（`backend/api/api.go:47`）映射：

| 场景 | 状态码 | 说明 |
|---|---|---|
| 路径 ID 非整数、请求体绑定失败、字段校验失败 | `400` | 返回具体中文消息 |
| 未知厂商、配置档、协议、适配器、连接设置或非法根地址 | `400` | `upstream.Resolve` 错误经 `resolveConnection` 包装 |
| 分组引用的模型不存在 | `400` | `ErrModelNotFound` |
| 查询/预览/草稿引用的渠道或模型不存在 | `404` | GORM `ErrRecordNotFound` 映射为“记录不存在” |
| 渠道名、分组名或同渠道模型名重复 | `409` | 依赖唯一索引；当前识别 SQLite 的 `UNIQUE constraint failed` 文本 |
| 其他数据库或内部错误 | `500` | 统一“内部错误”，不返回原始 DB 错误 |
| 模型预览时上游列表请求失败 | `502` | `stage=model_discovery` |
| 手动同步或上游请求失败 | `400` / `500` | 手动模型发现是 `400`；上游普通错误当前落到 `500` |
| 调用验证失败 | `200` | 响应体 `success=false`，按 `stage`/`message` 判断，不把 HTTP 200 当作调用成功 |

删除接口的错误语义不完全一致：删除不存在的模型返回 `404`（删除前显式 `First`），删除不存在的渠道、分组或分组项当前返回 `200`（级联语句为空操作，不检查影响行数）。非整数 ID 仍返回 `400`。

#### 事务边界

| 操作 | 事务范围 | 失败结果 |
|---|---|---|
| 新建渠道 | 渠道 + 所选模型导入 | 全部回滚，不留下半成品渠道 |
| 更新渠道 | 渠道字段 + Key 变更触发的余额来源失效 + 所选模型导入 | 全部回滚；余额来源缓存状态也恢复 |
| 同步模型 | 仅导入阶段；上游列表请求在事务外 | 导入失败不新增部分模型；请求阶段失败不写库 |
| 删除模型 | 存在性检查 + 分组成员清理 + 激活项清零 + 删除模型 | 任一步失败全部回滚 |
| 删除渠道 | 余额来源 + 分组引用清理 + 渠道模型 + 渠道 | 任一步失败全部回滚，包括余额来源和激活项 |
| 创建分组 | 分组 + 全部成员 + 初始激活项 | 唯一冲突或激活写入失败整体回滚 |
| 删除分组 | 分组项 + 分组 | 全部回滚 |
| 更新分组、增删改单个成员 | 单条语句或 `Save`，未包事务 | 分别失败；删除成员不会联动清激活项 |
| 删除影响预览 | 无事务、只读 | 无写入；不保证与后续删除时的数据一致 |

事务提交后的副作用：删除渠道或修改 Key 成功后取消余额查询任务；若事务回滚则不会执行该取消。

#### 修改影响

| 修改点 | 波及范围 |
|---|---|
| `upstream.Resolve` / `Open` | 渠道新建、连接变更、草稿解析、模型预览、调用验证、同步；`Open` 还决定历史渠道的请求端点，不能未经迁移直接改成重新套用预设 |
| `channel.CreateTx` / `UpdateTx` | 渠道保存、草稿继承、余额来源失效、前端表单；默认值和空 Key 语义变化会影响既有调用方 |
| `model.ImportNamesTx` | 渠道保存、模型同步；必须保持只新增、保留既有 ID、整批校验和事务回滚 |
| `group.DeleteItemsByModelIDsTx` | 删除模型、删除渠道、分组项清理测试；激活项只应在命中删除集合时清零，不能改优先级或误删其他成员 |
| `router.Resolve` | 网关的 manual/failover 路由、空组/未激活错误、故障转移顺序；改变跳过或补位逻辑会直接改变调用行为 |
| 删除影响响应字段 | 前端两个删除弹窗和 `deletionImpact.js`；缺失字段被当作未知并阻止确认，不能随意删除或改类型 |
| 分组成员元数据查询 | 分组列表/详情响应和前端成员展示；只用于读取，不新增数据库列 |
| 管理 API 注入 `quota_service` | 渠道删除是否清理余额来源、预览 `has_quota_source` 的取值；预览和删除必须保持同一判断 |

跨模块依赖：gateway 通过 `group.GetByName`/`group.List` 与 `router.Resolve` 在请求时读取本模块数据；quota 通过注入的渠道 Key 读取函数取得凭据；cron 通过 `channel.List` 和 `model.SyncAutoChannels` 做自动同步；前端渠道、模型、分组页面直接调用本模块管理 API。

## 阅读导航

- [公共概念](../../concepts.md)：渠道、模型、分组与策略术语。
- [网关与协议](../gateway/README.md)：`/v1/models`、实际转发、故障转移执行与协议转换。
- [余额与额度](../quota/README.md)：余额来源、继承凭据、刷新与缓存。
- [调用观测](../observability/README.md)：调用日志与上游尝试的保留和查询。
- [运行时与配置](../runtime/README.md)：启动迁移、管理 API 本机限制、定时同步设置。
- [桌面端](../desktop/README.md)：桌面页面与生命周期（不构成本模块职责）。
- 上游渠道接入说明：`docs/upstream-channels.md`（基线文件；当前工作区未提供，故不设置失效链接）。
- 架构知识统一由本 Wiki 维护；产品定义见 `docs/product/PRODUCT.md`（普通路径引用）。

## 依据与核验

| 来源 | 核对内容 |
|---|---|
| `backend/channel/channel.go`、`service.go`、`icon.go` | 渠道实体、请求/响应、Key 脱敏、创建/更新/删除事务入口、连接叠加与校验、图标限制 |
| `backend/upstream/presets.go`、`connection.go` | 厂商预设目录、`Resolve` 默认展开与地址校验、`Open` 历史兼容、模型发现 |
| `backend/model/model.go`、`service.go`、`import.go`、`sync.go` | 模型唯一约束、列表/手动 CRUD、选择性导入、同步与自动同步 |
| `backend/group/group.go`、`service.go`、`deletion_impact.go`、`migrate.go` | 分组实体、创建/更新/成员操作、事务级联清理、影响预览、旧策略迁移 |
| `backend/router/router.go` | manual/failover 目标解析、空组与未激活错误、优先级排序 |
| `backend/api/channel.go`、`channel_connection.go`、`model.go`、`group.go`、`deletion_impact.go`、`api.go`、`setting.go` | HTTP 路由、保存事务、草稿解析/预览/验证、删除预览与级联、错误映射、同步设置 |
| `main.go` | 启动迁移、余额服务注入、定时任务装配 |
| `backend/api/channel_test.go`、`channel_connection_test.go`、`deletion_impact_test.go`、`group_creation_test.go`、`group_strategy_test.go` | 渠道保存事务、草稿继承、删除预览只读/级联/回滚、分组创建与旧策略拒绝 |
| `backend/group/deletion_impact_test.go`、`backend/model/import_test.go`、`backend/router/router_test.go` | 预览查询次数与空组判定、导入幂等与回滚、路由顺序与空组错误 |
| `backend/gateway/gateway.go`、`backend/gateway/probe.go` | `/v1/models` 跳过空分组、调用验证结果契约（辅助核对跨模块边界） |
| `docs/upstream-channels.md`、`docs/product/PRODUCT.md` | 面向用户的操作口径、三种验证含义、产品范围与待确定项 |

本次核对读取了上述关键源码、测试代码和文档，按基线 `b44d75a788f8aa5ef0ad024273d4bf5dd8c95c7e` 静态比对。主 Agent 已于 2026-10-28 执行 `go test ./...`、前端 245 项测试和类型检查并通过；未执行桌面端到端检查、构建或真实接口调用。

## 待确认项

1. **唯一冲突识别依赖 SQLite 文本**：`classifyError` 通过 `UNIQUE constraint failed` 判断 `409`。项目保留切换 MySQL/PostgreSQL 的能力，换库后同冲突可能落为 `500`，需要增加数据库特定错误映射。影响渠道/模型/分组保存的错误契约。
2. **历史悬挂成员与 `/v1/models` 公布规则**：分组项引用已缺失模型时，预览不会判定为空组，`/v1/models` 也因存在成员行而继续列出；实际调用会解析失败。产品文档已把“配置不完整分组的公布规则”列为实施前待确定项，需确认是保持现状还是在列表/预览中按可解析目标过滤。
3. **手动删除分组成员不清激活项**：`group.DeleteItem` 只删行，`active_item_id` 保留悬挂引用；删除模型/渠道则会自动清零。需要产品确认手动移除激活成员时是否应同时清空激活项、禁止删除，或维持当前“配置不完整”提示。
4. **分组创建的模型存在性预校验在事务外**：`group.Create` 先用只读查询校验模型存在，再进入事务写分组项；`group_items` 只有指向 `groups` 的外键，对 `models` 没有外键约束，并发删除模型与创建分组之间没有锁。是否需要在事务内复查或增加外键，取决于管理端并发假设与数据库选型。
