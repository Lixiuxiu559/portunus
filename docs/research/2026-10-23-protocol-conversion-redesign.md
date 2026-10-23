# Portunus 协议转换改造方案

> 后续决策：strict/legacy 转换策略配置及请求侧拒绝分支已移除。本文涉及策略开关的内容是阶段历史；当前统一保留诊断、沿用正常调用行为，安全边界始终执行，见[当前诊断模式](2026-10-23-protocol-diagnostics-only.md)。

日期：2026-10-23。状态：总体设计，已分阶段实施部分能力；请求侧与类型化兼容结构的实际范围分别见 [S1 记录](2026-10-23-protocol-native-s1.md)、[S2 记录](2026-10-23-protocol-typed-stream-s2.md)，其余条目仍为计划。

## 1. 结论与范围

保留现有四协议注册表、扩展 OpenAI Chat 请求/响应结构、分组路由和 HTTP 执行方式。在 `backend/protocol` 内增加原生扩展信封、类型化流事件和明确的终态结果，在 `backend/gateway` 内增加交付状态与统一尝试控制。借鉴 AxonHub 的扩展容器、类型流和预提交缓存，不引入其整套 Pipeline、GraphQL 或供应商平台架构。

本次设计的三项决策：

1. 统一语义和原生数据分开存储；只有满足协议、所有者与来源条件的原生数据才能恢复。
2. 跨协议流在内部传递 Go 类型事件，不再经过 OpenAI chunk JSON 二次编码/解码；同协议保留原生转发路径。
3. 重试必须同时满足交付尚未开始、请求允许重放、错误可恢复、候选兼容且预算未耗尽；EOF 不代表语义成功。

不在本期加入 WebSocket、完整 Responses 会话恢复服务、智能负载均衡、新多模态入口或价格计算。`previous_response_id` 等状态型字段的识别与安全拒绝属于本期，自动跨渠道恢复不属于本期。

本文的接口、模块名和默认值均为建议，不代表现有代码或最终 API 承诺。

## 2. 方法、版本与现状事实

基于当前源码逐段核查请求、流转换、SSE、失败分类、兼容重试和日志路径；参考已下载 AxonHub 源码并独立复核关键反例。本次不运行测试，不调用真实上游。

- Portunus：`4aa25279429254bfacf8c3278b22f90f0c37fa71`。
- AxonHub：`c0e3374c3486b9a4cfc88955141dc6b462462e8e`，后续上游变化不自动适用于本文。

| 已确认事实 | 本地证据 | 对设计的约束 |
| --- | --- | --- |
| 四协议统一经扩展 Chat 结构转换 | `backend/protocol/openai.go:8`；`convert.go:8` | 保留现有语义映射，避免重写全部请求转换 |
| providerImpl 集中登记双向转换、流、usage、接入 | `provider_registry.go:8` | 沿注册表扩展，避免另建第二套协议分发 |
| 两段流转换以 `[]byte` 串联 | `stream.go:13,145` | 类型化改造优先替换内部接缝 |
| 已有 blockSink 和工具参数累积 | `from_openai.go:18` | 复用状态机经验，不能回退到仅 text delta |
| 同协议未改写时返回原 body；覆盖模型时经 map[string]any | `convert.go:151` | 当前不能声称全部字节/数字精度保真；改造改用原始片段覆盖 |
| Unframe 按 data 行处理，忽略完整 SSE event/id/retry 及多行 data 结构 | `stream_frame.go:27` | 完整 SSE 解析是原生事件保留的前置工作 |
| ConvertPayload 未输出帧时也会进入提交分支 | `backend/gateway/stream.go:135` | 转换有输入与实际交付不能用同一个布尔值 |
| response.failed 当前未映射；Finish 缺乏统一的终态证据契约 | `backend/protocol/responses.go:648,664`；`anthropic_stream.go:146` | 不能让收尾函数自行补成功 |
| failSpec 将同目标与换目标合并为 Retryable | `backend/gateway/fail.go:113` | 需显式区分 stop_request、next_target、retry_same |
| 内层不可重试只 break，外层仍会换目标 | `relay.go:134` | 请求级错误必须能终止整个候选链 |
| 预算字段兼容重试直接产生另一次 HTTP Do | `upstream.go:53` | 总尝试预算必须在每次实际 Do 前统一扣减 |
| 现有 reasoning v1 是带版本前缀的 base64 桥载荷 | `backend/protocol/reasoning_bridge.go:37` | 维持旧桥兼容，不能把它当供应商原生签名或身份凭证 |

上述是代码行为核查，不是生产影响测量；后续章节均为设计建议。

## 3. 目标调用链与职责

```text
客户端请求
  → RequestEnvelope：统一 Core + 原生 Native + 能力要求
  → PrepareTarget：检查目标兼容性、续接约束、降级策略
  → PreparedRequest：本次尝试独立的请求体与重放属性
  → AttemptController：预算、取消、重试动作
  → HTTP 执行
      非流式：原生响应 → ResponseEnvelope → 目标协议响应
      流式：完整 SSE 解帧 → SourceDecoder → SemanticEvent
            → 状态/终态校验 → TargetEncoder → 预提交缓存
            → DeliveryWriter → 客户端
```

同协议使用独立快路径：原生 body/SSE frame 转发，旁路 observer 提取 usage、内容类别与终态证据，不把原始数据规范化后再重建。快路径与转换路径共用 AttemptController、DeliveryWriter、Outcome 和预算。

| 层 | 负责 | 不负责 |
| --- | --- | --- |
| protocol | 语义解析、原生恢复资格、事件分类、终态证明、目标编码 | 选择分组、调用 HTTP、决定第几次重试 |
| gateway | 候选遍历、尝试预算、交付状态、取消与超时、失败动作 | 根据 JSON type 猜协议语义 |
| shared/日志 | 持久化结果摘要与尝试元数据 | 保存完整原生正文或接管转换 |

## 4. 原生扩展保留

### 4.1 信封模型

初期保留现有 ChatCompletionRequest/Response 作为 Core，不要求立即重命名或迁移所有字段。新增仅内部使用的信封，概念结构如下：

```go
type RequestEnvelope struct {
    Core         *ChatCompletionRequest
    Native       *NativeEnvelope
    Requirements Requirements
}

type NativeEnvelope struct {
    Source    SourceDescriptor
    Original  json.RawMessage
    Fragments []NativeFragment
}

type NativeFragment struct {
    Owner       OwnerRef             // request / item / block / tool
    Position    int                  // 在原序列中的位置
    Kind        NativeKind           // 已知语义、原生不透明、未知
    Raw         json.RawMessage
    Constraints ReplayConstraints
}
```

这是数据关系示意，实际类型应使用枚举与具名结构，避免把关键约束散在 `map[string]any`。Native 不参与通用 JSON 序列化，不成为默认日志字段；必要时显式标记 `json:"-"`。

`SourceDescriptor` 至少包含 API 格式/变体、来源可信度。对上游产生的数据，还可记录渠道 ID、端点身份、模型及认证配置版本的非敏感标识；不保存或输出真实 key。协议相同不能替代这些绑定条件。模型别名相同也不证明来源相同。

客户端传入的普通 JSON 不能自行证明“曾来自某渠道”。缺少可信绑定时标记 Unknown，不能通过客户端填写 source 字段升级为 Verified。跨请求可信绑定需要服务端签发引用或状态表，属于后续独立能力；本期不假装已经具备。

### 4.2 保留与恢复规则

| 数据 | 保存方式 | 恢复规则 |
| --- | --- | --- |
| model、预算、普通消息、function tool | 统一 Core | 由目标 renderer 编码，覆盖字段只能有一个所有者 |
| 同协议未知顶层字段 | RawMessage 或原始 body | 沿同协议快路径保留；跨协议不直接复制 |
| Responses 特有工具、输入项、未知 item | 有 Owner/Position 的原始片段 | 目标能表达且所有者仍有效才恢复；否则按必要性报不支持 |
| Anthropic 原生 signature、redacted_thinking、Gemini thoughtSignature、Responses encrypted_content | 不透明片段＋绑定条件 | 只有目标原生格式兼容且来源约束成立时恢复，不能互相翻译或制造签名 |
| Portunus reasoning v1 桥 | 标记为 gateway bridge，保留现有解码规则 | 仅还原其约定的 reasoning_content；不能冒充供应商签名或授权信息 |
| previous_response_id / 原生会话引用 | continuation requirement | 已知绑定时仅保留来源匹配候选；绑定不可验证或全部候选不匹配且无可恢复历史时停止，而非盲目换家 |
| 纯观测性字段 | 分类为 telemetry-only | 可以明确忽略并记录类别；不得把未知工具或输入内容归入此类 |

首次改造优先处理：Responses 的原始 tools/tool_choice/input item、reasoning opaque 数据、Anthropic 思考块签名与工具块字段、Gemini part 的原生扩展。未知字段先保留事实，不宣称目标一定支持。

### 4.3 编译目标与能力报告

`PrepareTarget` 在发 HTTP 前返回以下之一：

- `Prepared`：完全可表达；附必要的源绑定和重放属性。
- `PreparedWithLoss`：仅在用户明确启用的降级策略下允许；附结构化 ConversionReport。
- `TargetUnsupported`：本目标不可表达，尝试下一个兼容成员，不发送 HTTP、不增加 HTTP attempt。
- `RequestInvalid`：请求自身错误，立即停止整个请求。
- `ContinuationUnavailable`：状态引用绑定无法验证，或全部候选都不满足已知绑定，停止并要求完整历史或正确来源。已知绑定下单个候选不匹配属于 TargetUnsupported，可继续查找匹配成员。

不能照搬 AxonHub 对 UnsupportedConversion 一律终止请求：Portunus 分组中可能另有可表达能力的成员。只有所有候选都不支持才返回明确的 400 不支持能力错误；鉴权、网络与限流原因仍保留各自最终错误分类。

严格模式下，未知且可能影响语义的原生片段不可跨协议静默剥离。旧版剥离行为仅在显式 `legacy` 转换策略中保留。降级模式不允许放宽身份/续接绑定，也不能把工具结果归属冲突当成可丢失字段。

**同协议原生回传的迁移边界**：严格绑定规则会限制来源不可证明的原生 signature/密文回传，即使协议相同；本期尚无跨请求可信存储，因此这是 strict 的有意行为变化，不承诺对这些调用透明兼容。legacy 保持现有 opaque 转发行为并标记 origin_unknown，不声称已验证来源。S1–S3 的 strict 只在受控灰度启用；普通原生 Claude/Gemini 多轮调用不得未经说明切换 strict。后续默认切换前必须提供可信续接设计或明确接受受限行为，不能以“同协议快路径”绕过此决策。普通未知字段的保留不受这一原生状态绑定规则影响。

### 4.4 合并、不可变与资源约束

1. 同协议原始 body 只覆盖 model 等既有授权字段；使用 RawMessage 级覆盖或定点 patch，避免任意 JSON 数字经过 float64。承诺未修改字段的值保留，不承诺整个文档空白、对象键顺序不变。
2. 渲染已支持字段后恢复允许的原生片段。不能简单将原始 map 覆盖回去，否则 model、预算、tools、流选项会被旧值覆盖。
3. 同一片段只能被统一语义或原生路径消费一次；用 owner ID 和映射表防止工具重复、item 重排、call_id 错配。数组必须保留相对顺序，不能按类型聚合后重新拼接。
4. 每个目标从不可变 RequestEnvelope 生成独立快照；Native 的 slice、map、RawMessage 需深复制或只读共享。兼容重试在该目标快照上改预算字段，不修改原请求，不影响下一目标。
5. 请求扩展与响应扩展分开；响应不能通过未知字段意外改变路由配置或认证头。恢复仅发生在协议 encoder 内。
6. 给扩展总字节、片段数、嵌套层级与单片段设置显式上限；超限返回资源错误，不截断密文/参数后继续。最终阈值在基线样本统计后确定，不在本草案假定生产请求尺寸。
7. 不透明片段仅在当前请求内保留，默认不落库；报告仅保存字段类别、长度和原因，不保存原始值或正文 hash。使用同一日志路径时同样适用。

## 5. 类型化流式事件

### 5.1 接口选择

采用与现有转换器更接近的同步 push/batch 接口，避免同时引入泛型迭代器、后台 goroutine 和完整 Pipeline：

```go
type SourceDecoder interface {
    Decode(frame WireEvent) ([]Event, error)
    End(end TransportEnd) ([]Event, StreamOutcome, error)
}

type TargetEncoder interface {
    Encode(event Event) ([]WireEvent, error)
    End(outcome StreamOutcome) ([]WireEvent, error)
}
```

`Event` 是带 discriminator 的类型联合：kind 对应唯一具名 payload；构造器和 Validate 保证合法组合。不得退化为 `Type string + map[string]any`。时间、来源与 owner 标识是公共元数据。

`WireEvent` 保存完整 SSE 事件语义：event、data、id、retry，必要时持有原始帧。类型化 `Event` 不包含 `data:` 包装。初期仅支持 HTTP/SSE，但类型中不混入 WebSocket 专用概念。

读取仍同步按上游事件推进，编码与写回形成背压；单次 batch 有数量和字节上限。不为每个事件派发 goroutine。

### 5.2 事件集合与标识

| 类别 | 事件示例 | 关键字段/约束 |
| --- | --- | --- |
| 响应生命周期 | ResponseStart、ResponseMetadata | 上游响应 ID、模型，初期只作为尝试私有信息 |
| 内容生命周期 | ItemStart、PartStart、PartEnd、ItemEnd | response/choice/item/part 的稳定标识与有序索引 |
| 文本/思考 | TextDelta、ReasoningDelta、RefusalDelta | 原样增量；空字符串不制造内容；思考也算实质输出 |
| 工具 | ToolStart、ToolArgumentsDelta、ToolEnd、HostedToolState | call ID、名称、原始参数字节；允许交错、晚到 ID、首帧即有参数 |
| 证据与原生数据 | Annotation、Grounding、NativeOpaque | 与所属 item/part 绑定；不丢掉正文后的晚到引用 |
| 用量 | UsageUpdate | provider 原始累计/增量语义＋归一结果；缺失与 0 分开 |
| 结果 | Terminal | completed / incomplete / failed / blocked；原因与证据 |
| 错误 | StreamFailure | 上游错误或解析/校验错误；进入失败裁决，不能当普通 metadata |

标识中保留选择索引；当前不支持的多 choice 请求应明确拒绝，不能先设计一个 choice 字段再继续只处理 choices[0]。工具参数片段在需要整包参数的目标（如 Gemini）才累积，并设大小限制；截断时不强行补成合法 JSON。

未知原生事件在同协议路径原样发送且保守地视为可能有内容；跨协议严格路径出现未知语义时中止转换，不能丢弃后继续生成“正常完成”。若未知事件在已交付后才出现，按已交付失败处理，不能倒退重试。

### 5.3 复用现有状态机

- 将 `fromOpenAISkeleton` 拆成类型化事件校验与目标协议 sink/encoder，复用工具累积和块生命周期规则，移除解析中间 chunk JSON 的步骤。
- Anthropic/Responses/Gemini 的 decoder 直接产出 Event；Chat decoder 也产出相同 Event。不得为了兼容旧接口在新核心链中重新 Marshal 成 Chat chunk。
- 每个目标尝试独占 decoder、encoder、ID 映射、usage 和预提交缓存，切换目标时整体丢弃。不要复用失败尝试的 response ID、序号或工具累积器。
- Responses 最终 output 与逐项事件来自同一状态，避免增量已显示但终态 output 缺正文/思考/工具。所需全文累积单独设限，不承诺整条转换链常量内存。
- 原生 `.done` 或终态 full output/full arguments 是快照或完成证据，不再次作为 delta 拼接。使用 owner 和已输出范围做一致性检查；只有可证明尚未输出的部分才允许补全，冲突内容不能静默覆盖已交付内容。
- `ReasoningSignature` 的原生签名、gateway bridge 与思考文本分别保存。已支持的 v1 回传、前缀稳定和预算/effort 映射必须通过原有矩阵回归。

### 5.4 完整 SSE 与同协议快路径

新 SSE reader 按空行结束事件，合并多行 data，处理 CRLF、注释与分片读取，保留 event/id/retry。解析规则不能依赖“每个 data 行就是一个 JSON”。原生同协议快路径可转发保留的帧，不能重建成只剩 data 的形式。

严格模式遇到无效 JSON/超大事件要明确错误；旁路 observer 解析不到的未知类型不应改写 payload。同协议已知错误事件仍必须记为失败，不能因为 HTTP 200 就记成功。解析前也要有字节上限，避免先分配巨量原始帧再检查。

### 5.5 终态与 EOF

`End` 的含义是“校验流结束并清理”，不是“默认成功收尾”。`StreamOutcome` 显式包含：语义终态、结束原因、终态证据、usage 完整性和协议警告。客户端交付状态由 gateway 单独维护。

| 上游协议 | 严格模式终态依据 | 不能单独作为成功依据 |
| --- | --- | --- |
| Chat | 已支持的所有 choice 有有效 finish_reason；允许随后补 usage/[DONE] | HTTP 200、只有 role、只有 [DONE]、无 finish 的 EOF |
| Anthropic | 有效 stop_reason 且收到 message_stop；原生 error 单独判失败 | 只有 message_start 或 EOF |
| Responses | completed / incomplete / failed 及对应有效载荷；保留真实 ID 和状态 | created/in_progress、网关自行合成 completed |
| Gemini | 候选 finishReason 或协议明确的 prompt block 证据；按已支持候选范围校验 | 只有正文和普通 EOF |

预算截断、过滤、拒答、正常无正文结束不等于瞬时故障，不能因“没有文本”自动重试。工具调用完成也不是空响应。缺终态 EOF 分类为 truncated；EOF 前已有内容时结束失败，未交付时才评估重放。

接收到有效终态后允许有界读取 usage 尾帧；尾部统计缺失记录采集不完整，不把已有 completed 无条件改成失败，也不制造第二个终态。矛盾终态记协议异常。目标协议需要的 item/part 收尾必须来自已知 outcome；失败收尾不能发 completed/end_turn 或成功 [DONE] 来掩盖截断。

跨协议编码顺序必须为 `SourceTerminalObserved → CollectAllowedTail → EncodeTargetTerminal → TargetTerminalWritten`。观察到源 finish 时先保存终态，按源协议白名单有界收集 usage/完成快照，达到合法流结束或尾部 deadline 后，统一生成含最终 output/可用 usage 的唯一目标终态。尾部超时标记 usage 不完整，不撤销已确认的语义结果；剩余 request deadline 仍有优先级。目标终态写出后不再编码普通内容、usage 或第二终态。

同协议快路径保持上游原始帧的合法顺序，不为了补统计重建 completed 或追加成功标记。Chat finish_reason 后协议允许的 usage 可继续在其最终结束标记前转发；Responses completed 等真正闭合事件后不再把非法后续内容发给客户端。observer 的内部统计更新不能成为追加客户端帧的理由。

## 6. 失败与重试边界

### 6.1 三个独立状态维度

不要用单个 committed 代表所有事情：

1. **上游执行状态**：未发送、已发送、可能已产生作用、语义终态已观察。
2. **客户端交付状态**：未提交、提交已开始、内容已写、终态已写、写失败/断连。
3. **结果/采集状态**：completed/incomplete/failed/cancelled；usage 是否完整；日志是否采集成功。

客户端“终态已写”只表示服务端 write 成功，不证明客户端实际接收/执行。HTTP 执行计数也不等于供应商真实接收次数。方案不承诺 exactly-once。

### 6.2 预提交缓存和提交门

```text
Preparing → WaitingPrelude → Committing → Streaming → TerminalWritten
                 │               │           │
           可评估安全重试      写错即停止    失败只收尾
```

- ResponseStart、role-only、空块开始、usage 等已知元数据留在本次尝试缓存内，不写 200，不 Flush，也不发送心跳以提前固定响应。
- 第一段正文、思考、拒答、工具调用标识/参数、引用、可见托管工具状态或未知原生事件触发提交。工具名称和 ID 即使尚无参数也按实质输出处理。
- 有效终态可以触发合法空响应的提交；纯失败终态在未提交时进入失败裁决，不先发送它再尝试换目标。
- 提交前先编码并校验待发 batch；编码错误发生在写之前才能保持未提交资格。开始写响应头/首个帧之前即把状态标记为 Committing；Write/Flush 失败后不能假设零交付，不再换家。
- 原子切换提交状态，取消/超时与写入互斥裁决。所有客户端写入包括 SSE 注释心跳只能经过 DeliveryWriter，避免旁路抢先提交。
- 预提交缓存建议初始候选上限 256 KiB、128 个事件，取先到者；需含原始帧和编码后帧的实际占用。超过上限返回 `precommit_buffer_exceeded`，停止整个请求，不为了释放缓存而提交 metadata 或无限换家。
- 等待内容沿用有界首内容时间预算；原始注释/心跳不能无限重置首内容 deadline。提交后使用现有 idle 机制；超时参数实际默认值在实施阶段根据现有配置映射和样本确认。

### 6.3 重放安全不只看有没有输出

普通模型生成在未交付时可以按有限预算允许重试，但上游可能已计算并收费。声明可能执行外部动作的原生工具、状态型会话操作或未知原生操作，发送后默认标记 ReplayUnsafe；即使没有下游内容也不自动重放。纯检索型已知托管工具是否允许重放由具名能力策略决定，不能笼统断言所有 hosted 工具都无副作用。

function tool 只由模型声明、实际执行在客户端的常见路径，提交工具调用前可按普通生成处理，提交后禁止重试。未知原生事件被观察到但尚未写出时，也要收紧上游作用状态，不能把“不认识”当成“只是元数据”。

网关本地适配、解析或资源限制错误不能默认污染上游熔断。供应商返回错误、缺终态或无效协议可作为单独健康信号；是否计入现有模型熔断由统一规则明确，不与客户端错误混淆。

### 6.4 失败动作与作用域

以 `Failure{Phase, Scope, Cause}` 和显式 `Action` 取代单个 Retryable；failSpec 仍是统一裁决入口。Action 建议包括 StopRequest、NextTarget、RetrySame、RetryCompatibility、AbortDeliveredStream。以下均还要经过交付/重放/预算门：

| 情况 | 动作 | 健康/统计处理 |
| --- | --- | --- |
| 客户端 JSON 错、参数矛盾、工具配对非法 | StopRequest | 400，不计上游故障 |
| 本目标无法表达必要能力 | NextTarget，发送前跳过 | 无 HTTP attempt；单记排除原因 |
| 所有目标均无法表达必要能力 | StopRequest | 明确 unsupported_conversion，不伪装 503 |
| 已知续接绑定与当前候选不匹配 | NextTarget，发送前跳过 | 仅查找匹配来源，不退化为任意渠道 |
| 续接绑定无法验证或全部候选不匹配 | StopRequest | 400 continuation_unavailable，提示完整历史 |
| 明确预算字段不兼容 | RetryCompatibility，同目标最多一次 | 原预算不变；计入总 Do 预算 |
| 429 | NextTarget；按有效 Retry-After 建冷却 | 不立即同家重试；限流与模型故障分开 |
| 连接失败、等响应头超时 | 现有失败分类决定同家/换家 | 等头假死继续优先换家 |
| 未交付时瞬时 5xx/空流/缺终态 | 有界 RetrySame 或 NextTarget | 只有安全重放时允许；有合法空终态不算空流 |
| 未交付时目标特定 401/403/模型不存在 | NextTarget | 目标接入异常；不把上游 key 错报为 Portunus token 错 |
| 编码器 bug、未知转换失败、资源上限 | StopRequest | 网关错误；不靠轮流换家掩盖问题 |
| 已开始交付、写失败、客户端取消 | AbortDeliveredStream 或 StopRequest | 不自动重放；取消不计上游故障 |
| 已确认 incomplete/blocked/refusal | 正常传播对应终态 | 不增大预算、不自动再生成 |

不要把所有 400 都设成全局失败：上游返回的目标特定兼容问题应由窄匹配分类器处理。无法确认属于目标兼容问题时保守停止，避免无边界重复发送相同坏请求。

### 6.5 统一尝试预算与错误结果

- `AttemptController` 维护客户端请求级 MaxHTTPAttempts，包含首次请求、普通重试、换家和预算字段兼容 Do。候选预检查不消耗 HTTP 次数，但受有限候选数与时间限制。
- 扣减和 trace.begin 放在实际 `Client.Do` 前的共同入口，禁止兼容重试绕过控制器；每次 Do 对应一次 attempt 终结记录。
- 总预算不能由每目标 RetryCount 相乘隐式决定。建议 V2 默认总 Do 次数从 4 开始评估；最终值待基线验证，不在本次更改配置。
- 流式有累计“选路至首内容”deadline，开始交付后不以该 deadline 截断长回答，改用客户端取消与流静默控制。非流式总 deadline 跨候选共享；单次 deadline 取请求剩余与单次上限较小值。
- 同家重试使用有上限、可取消的退避与抖动；429 不同家重试。预算/能力排除和本地配置错误不进入无意义退避。
- 同一个请求终止后不得再启动任何 Do。客户端取消、网关 deadline 和供应商超时分开归因。
- 最终错误由 Failure/Outcome 汇总器选取；保留有意义的上游状态与 request_id，不能总用最后一次错误或总称“所有熔断”。

## 7. 同协议与跨协议的兼容承诺

| 场景 | V2 行为 |
| --- | --- |
| 同协议普通请求/未知字段 | 原生快路径，保留字段与数值，只定点覆盖必要字段；不包含需来源绑定的原生状态令牌 |
| 同协议未知原生流事件 | 原样转发，保守提交，不在跨协议 encoder 中重建 |
| 跨协议已覆盖的文本/图片/function/reasoning/搜索 | 沿用现有映射并由既有矩阵约束，类型化后维持语义与时序 |
| 跨协议无法表达的必要能力 | 尝试兼容成员；全部不支持则明确错误 |
| 原生签名、加密 reasoning、状态引用 | strict 保留数据但约束重放，来源不可证明会拒绝；legacy 维持现有未验证转发语义，不能宣称有来源保证 |
| 旧托管工具剥离 | 仅 legacy 策略继续；V2 严格策略显式拒绝或使用允许的降级 |
| 无终态 EOF | V2 记录截断；不会以成功 Finish 掩盖 |

同协议请求字节透传与语义终态校验是不同承诺。V2 可以原样转发数据，同时记录上游缺终态；若已经发送就不能修复上游流，更不能追加成功终态。

## 8. 可观测性与最小持久化调整

保留 request_id、logs 与 upstream_attempts 的现有含义，新增可选元数据和独立请求终态记录，不把旧日志行数重新命名为客户端请求数。

- 协议报告：source/target format、原生片段分类计数、preserved/mapped/dropped/rejected、原因为何、转换版本。只记枚举，不记片段内容。
- 尝试结果：失败 phase、action、是否可能已执行、上游终态、首内容时间、开始提交时间、最后成功写入阶段。
- 请求终态：最终结果、终止原因、实际 Do 数、最终目标、语义终态是否观察、终态是否写出、usage/attempt 采集完整性。
- 用量保留上游报告值；失败尝试可能已产生消耗，不能仅因不向客户端返回就归零，也不能把所有尝试 usage 当作最终回答 usage。

历史行没有新字段时返回 unknown/未采集，不回填猜测。上线初期可以先输出结构化诊断，但依赖数据库终态计算成功率之前，必须完成向后兼容迁移和历史 unknown 处理。

## 9. 分阶段实施与回退

| 阶段 | 具体产物 | 退出条件 |
| --- | --- | --- |
| S0 契约与基线 | 协议/字段保留矩阵、事件词表、终态证据表、失败动作表；固定现有样本与资源基线 | 所有关键场景都有预期；无依赖真实供应商的默认测试 |
| S1 原生信封 | envelope、RawMessage 覆盖、原生 owner/source、PrepareTarget、严格降级报告；先覆盖非流式与请求方向 | 未知字段和大数保留、片段仅一次消费、来源约束及目标间隔离通过 |
| S2a 类型化兼容结构 | 四协议 decoder/encoder、具名语义增量帧、旧 API 兼容 wrapper；保留旧 SSE/终态/调用边界 | 新链内部不使用 Chat chunk JSON 中转；固定样本差分、现有功能矩阵与生命周期回归通过 |
| S2b 传输与语义强化 | 完整 SSE、细粒度事件、严格终态 outcome | 与 S3 交付控制共同验证后再启用新失败语义 |
| S3 交付与尝试控制 | 预提交缓存、DeliveryWriter、失败作用域、全局预算、兼容 Do 收口、请求终态 | 失败矩阵和故障注入通过；交付后零自动重放；无终态不伪成功 |
| S4 灰度与清理 | V2 选择开关、影子转换、真实客户端验证、移除内部旧桥接路径 | 达到预定性能/兼容门槛，再切默认；公开旧入口继续薄包装 |

根据后续用户明确的兼容优先要求，S2 拆为 S2a 与 S2b：S2a 仅替换内部数据传递，保持现有终态、SSE、交付和重试契约，可以独立验证接入；这不代表 S2b/S3 已实现。S2b 的新终态/失败语义在生产启用前必须接入 S3 的交付控制。可按协议逐步开发，但缺少 encoder/decoder 任一侧的组合显式走 legacy，不能在同一次响应内混用两个状态机。

选择单位建议为网关/渠道的内部转换版本与兼容策略，不增加分组级客户端参数重复配置。开始实施时默认保持现有分组行为，V2 测试/灰度显式启用；V2 内默认 strict，不暗中继续 legacy 剥离。最终默认切换需列出行为变更说明。

影子转换只在本地对同一份已取得的请求/事件做第二次纯转换，绝不多调用上游；使用独立状态机且有资源限额。差异比对采用语义/生命周期和原始片段检查，忽略必要的网关生成 ID 差异，不忽略 provider 原生 ID 丢失。

回退发生在下一次客户端请求开始前。当前请求若已发送上游，不允许遇到 V2 错误后切回 legacy 再次调用；已经交付更不能回退重放。若故障只是尚未发送时的选择开关，可将后续请求整体切回 legacy。

## 10. 验收场景与质量门

以下是实现验收要求，不代表已新增或执行这些测试：

| 方向 | 必须覆盖的反例 | 期望 |
| --- | --- | --- |
| 原生保留 | 未知字段、null/缺省、JSON 大整数、未知 tool/item、数组顺序 | 同协议不丢值；跨协议不偷渡；不重复工具 |
| 来源绑定 | 同协议不同渠道/模型/认证配置、不可验证密文、伪造 source | 原生数据不能凭协议名重放 |
| 续接候选 | 第一个候选不匹配、后续候选匹配已验证绑定 | 跳过前者并使用后者；全部不匹配才终止 |
| 隔离 | A 渲染改 model/预算后 B 接手；共享 slice/map | B 基于原始请求；无串目标污染 |
| 流内容 | reasoning→tool→text、并行工具、晚到 ID、首帧完整参数、晚到 grounding | 时序与 owner 稳定，参数不丢不补假 JSON |
| SSE | 多行 data、CRLF、event/id/retry、注释、跨网络分片、超大帧 | 完整事件处理；未知原生帧保留；内存有界 |
| 提交边界 | 只有 created/role/usage 后失败；decoder 产出空 batch | 不提前提交；可重放时有限重试 |
| 输出边界 | 首个 reasoning/tool ID 已写、写入一部分后报错、Flush 失败、取消与写竞争 | 不再 Do、不混两次响应 ID、不重复工具 |
| 终态 | EOF 缺终态、response.failed、合法空 completed、incomplete、refusal、blocked | 不伪成功；合法截断/拒答不被空响应重试 |
| 尾部与统计 | finish 后 usage、终态后连接错、日志写失败 | 语义/交付/采集结果分开，usage 不双计 |
| 尾部顺序 | Chat finish 后晚到 usage 转 Responses、usage 尾部超时 | 目标 completed 只写一次并包含已取得 usage；超时标记不完整，不补第二终态 |
| 重试控制 | 全局无效请求、多成员目标不支持、429、预算兼容重试、deadline | 正确 Stop/Next/Retry，所有实际 Do 被预算覆盖 |
| 重放风险 | 发送后可能已执行的原生工具、未知原生操作 | 无输出也不默认重放 |
| 协议回归 | 现有 reasoning、hosted-tools、缓存、token预算、Responses lifecycle 矩阵 | 已支持行为不倒退；有意改变项显式更新契约 |

自动化以本地 mock 上游、故障注入、可控时钟和 writer 为主；对真实 SDK/CLI 的验证在隔离配置下主动执行。差异中涉及新 strict 语义的行为需要明确记录，不以全部字节相等替代正确性。

性能门先测基线再定数值：单事件分配、CPU、峰值内存、首内容额外延迟、慢客户端背压、取消释放。基准重点证明跨协议不再有中间 JSON 往返；不预先承诺吞吐提升倍数。

## 11. 模块落点建议

| 现有模块 | 建议演进 |
| --- | --- |
| protocol/openai.go | 保留 Core 类型；跨协议扩展逐步移入 envelope |
| protocol/provider_registry.go | 增加 Prepare/Decoder/Encoder/Observer 能力，作为唯一分发表 |
| protocol/convert.go | 保留导出兼容入口；内部调用 envelope→prepare→render |
| protocol/stream.go | 兼容 wrapper；新核心放到 event/decoder/encoder/outcome 相关文件 |
| protocol/from_openai.go | 抽出类型化 lifecycle 与工具状态，复用目标 sink 规则 |
| protocol/stream_frame.go | 分为完整 SSE reader/writer 与协议错误编码职责 |
| gateway/stream.go | 聚焦交付门、缓存和超时，不判断具体 JSON 类型 |
| gateway/fail.go | 统一失败 phase/scope/action；保留外部错误协议渲染 |
| gateway/relay.go、upstream.go | AttemptController 管所有 Do；保持预算兼容窄规则 |
| gateway/attempt.go、shared | 保留尝试语义，附新阶段与请求终态；不落原生正文 |

不需要立即拆独立 Go module。是否最终抽出可复用库应等接口稳定，不把拆包作为前置任务。

## 12. 参考、置信度与未确认项

AxonHub 可参考的实现思想：

- [统一请求以 Chat 为基础](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/llm/model.go#L33)。
- [ProviderExtensions 与原始片段](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/llm/provider_extensions.go)。
- [Inbound/Outbound 接口](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/llm/transformer/interfaces.go)。
- [类型化流与交付能力](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/llm/streams/stream.go)。
- [流式内容前缓存](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/llm/pipeline/stream.go)。
- [续接来源与恢复错误](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/llm/transformer/shared/responses_continuation.go)。

本方案借鉴机制、重新按本仓库接口设计，不计划直接复制 LGPL 的 llm 模块。若后续选择代码复用，另行核查文件级授权与分发方式。

- 现状事实置信度高：直接源码核查并独立抽查；不等于线上故障已经被复现。
- 方案适配置信度中：接口方向与现有注册表一致，但尚无实现、基准或真实客户端回归。
- 待实施前确认：扩展尺寸和缓存上限、主要真实协议样本、首内容 deadline 默认值、哪些托管工具属于可重放检索、V2 灰度入口及 strict 迁移范围。
- 未解决范围：无状态网关如何跨请求证明客户端回传原生片段的来源；本期采取保守限制，后续若要透明续接应单独设计可信引用/会话存储。

此文档创建时为未跟踪文件；无需入库可删除。未修改 `.gitignore`，未改业务代码、配置或测试。

## 13. 后续实施状态

2026-10-23：S1 请求原生扩展保留/目标检查、S2a 类型化流内核和 S3 完整 SSE/终态/交付控制已在工作区实施。具体边界和验证见 [S3 实施记录](2026-10-23-protocol-sse-delivery-s3.md)。默认仍为 legacy。S4 已实现统一请求级总 Do 预算与跨目标流式首内容累计 deadline，详见 [S4 实施记录](2026-10-23-request-control-s4.md)。完整响应原生扩展、非流式跨目标总 deadline、退避调度及跨请求可信续接仍是后续设计，不能视作当前行为。

2026-10-23 S5：响应原始快照、片段、类型事件 Native 和响应诊断已实现，详见[S5 实施说明](2026-10-23-response-native-s5.md)。跨协议只沿用既有映射，不将任意原生扩展恢复到不兼容格式；非流式累计 deadline、退避与可信跨请求续接仍是后续范围。

2026-10-23 S6：非流式跨目标累计 deadline 和可取消、有界抖动退避已实现，普通/兼容/换目标均纳统一发送控制，详见[S6 实施记录](2026-10-23-nonstream-deadline-backoff-s6.md)。可信跨请求续接、供应商原生扩展的完整跨协议映射仍不属于已实现能力。
