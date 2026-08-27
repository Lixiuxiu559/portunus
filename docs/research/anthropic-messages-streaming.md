# Anthropic Messages API 流式 SSE 事件字段规范研究

> 研究时间：2026-08-27。对象：Anthropic Messages API 流式（`POST /v1/messages` + `stream: true`）的 SSE 事件字段规范。
> 目的：为 portunus 的 protocol 模块（OpenAI ↔ Anthropic 协议转换）提供字段级参考，重点解决流式序列化「缺必填字段导致 Claude Code 客户端崩溃（`undefined is not an object (evaluating 'e.slice')`）」的问题。

> 来源说明：`docs.claude.com` 与 `platform.claude.com` 在研究时对当前网络地区返回 302/307 到 `anthropic.com/app-unavailable-in-region`（区域不可用），无法直接抓取页面正文。因此本笔记的字段级结论以 **Anthropic 官方 SDK 的 OpenAPI 生成源码**为准——它们与官方 API 参考同源（文件头均注明 `File generated from our OpenAPI spec by Stainless`）：
> - Go SDK：`github.com/anthropics/anthropic-sdk-go` 的 `message.go`（类型定义）与 `packages/ssestream/ssestream.go`（SSE 帧解析）
> - TypeScript SDK：`github.com/anthropics/anthropic-sdk-typescript` 的 `src/resources/messages/messages.ts`（类型）与 `src/lib/MessageStream.ts`（客户端拼接逻辑）
>
> 以下每个结论均标注其来源 URL。文中「必填」是指官方 OpenAPI 类型定义的 `api:"required"` / TypeScript 非可选属性；「可空」是指字段值可为 `null` 但键必须出现。

---

## 1. 一句话结论

Anthropic 流式是**一个事件一个 SSE 帧**：每个事件由 `event: <名字>` 行 + `data: <单行 JSON>` 行 + 一个空行组成，事件类型在 `event:` 行和 `data` JSON 里的 `"type"` 字段**各出现一次**；流以 `message_stop` 事件结束，**没有 `data: [DONE]`**（那是 OpenAI 的结束标记）。 序列化时真正会「把人坑死」的是三件事：**(1) 值为 0/空串/空对象/空数组的必填字段被 Go 的 `omitempty` 吞掉**（`index:0`、`text:""`、`input:{}`、`content:[]`）；**(2) `message_delta` 事件的 `delta.stop_reason`/`delta.stop_sequence`/`usage` 丢了或设成空串而非 `null`**；**(3) 只发 `data:` 行而不发 `event:` 行，导致按 `event:` 行分发事件的客户端直接丢事件**。

---

## 2. SSE 帧格式（event: / data: 行怎么组合）

Anthropic 的流响应 `Content-Type: text/event-stream`，每个事件是一段：

```
event: message_start
data: {"type":"message_start","message":{...}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}

```

规则（逐条以官方 Go SDK 的 SSE 解码器为据）：
- **`event:` 行**：SSE 事件名（`message_start` / `content_block_start` / `content_block_delta` / `content_block_stop` / `message_delta` / `message_stop` / `ping` / `error`）。
- **`data:` 行**：一条 JSON，内部 `"type"` 字段**再次**给出事件类型（与 `event:` 行一致）。
- **空行**：`\n\n` 结束一个事件。`data` 可多行（每行一个 `data:`，解码器用 `\n` 拼接），但 Anthropic 实际每个事件都只有单行 `data`。
- 以 `:` 开头的行是注释，被忽略（`": keep-alive"` 这种保活注释不属于本 API 的正式事件）。

关键证据：官方 Go SDK 解码器 `eventStreamDecoder`（`packages/ssestream/ssestream.go`）在遇到**空行**时把「当前累积的 `event` 名 + `data` 字节」包装成一个 `Event{Type, Data}` 返回；`Stream.Next()` 用 `Event().Type`（即 **`event:` 行的值**，不是 `data` 里的 `type`）来 switch 分发：

```go
case "message_start", "message_delta", "message_stop",
     "content_block_start", "content_block_delta", "content_block_stop", ...:
    json.Unmarshal(s.decoder.Event().Data, &nxt)   // data 里的 "type" 决定 Union 变体
case "ping":
    continue                                        // ping 直接跳过
case "error":
    s.err = ed.newAPIError(data)                    // error 事件转成 API 错误
```

来源：`https://github.com/anthropics/anthropic-sdk-go/blob/main/packages/ssestream/ssestream.go`

推论（转换层最重要的两条）：
1. **`event:` 行不可省**。如果只发 `data: {...}` 不发 `event:` 行，`Event().Type` 会是空串，switch 不命中任何 case，事件被**静默丢掉**（`Next()` 继续读下一帧，客户端永远收不到这条 content）。
2. **`data` JSON 里的 `"type"` 也不可省**。Go SDK 靠它（`MessageStreamEventUnion.Type`）把 JSON 反序列化成具体变体（`AsMessageStart()` 等）。

结束标记：**Anthropic 没有 `data: [DONE]`，也没有 `finish_reason`**。流正常结束就是 `message_stop` 事件，随后服务端关闭连接。

---

## 3. 流式事件完整清单与字段规范

Anthropic 官方事件共 8 种（6 种内容事件 + `ping` + `error`）。TypeScript 官方 SDK 的 `RawMessageStreamEvent` 类型（`src/resources/messages/messages.ts`）定义的 6 种内容事件如下表，Go SDK `message.go` 里对应的 `MessageStreamEventUnion` 完全一致。

### 3.1 事件总表

| 事件 | `event:` 名 | 必填字段（除 `type` 外） | 可选/空 |
|---|---|---|---|
| message_start | `message_start` | `message`（完整 Message 对象） | — |
| content_block_start | `content_block_start` | `index`、`content_block` | — |
| content_block_delta | `content_block_delta` | `index`、`delta` | — |
| content_block_stop | `content_block_stop` | `index` | — |
| message_delta | `message_delta` | `delta`、`usage` | — |
| message_stop | `message_stop` | （无，仅 `type`） | — |
| ping | `ping` | — | `data` 为 `{"type":"ping"}` |
| error | `error` | — | `data` 为 `{"type":"error","error":{type,message}}` |

`type` 字段在 6 种内容事件里都是**必填**，取值即事件名本身。

### 3.2 message_start

```json
{"type":"message_start","message":{
   "id":"msg_...","type":"message","role":"assistant","model":"...",
   "content":[],"stop_reason":null,"stop_sequence":null,"usage":{...}
}}
```

`message` 是一个完整 `Message` 对象，字段（来源：Go SDK `message.go` 的 `Message` 结构）：

| 字段 | 类型 | 必填/可空 | 说明 |
|---|---|---|---|
| `id` | string | 必填 | 消息 ID |
| `type` | string | 必填 | 固定 `"message"` |
| `role` | string | 必填 | 固定 `"assistant"` |
| `model` | string | 必填 | 模型名 |
| `content` | array | 必填（可为 `[]`） | 内容块数组，流起始为空数组 |
| `stop_reason` | string \| null | 必填、可空 | **流式 `message_start` 里为 `null`**，其余事件非 null |
| `stop_sequence` | string \| null | 必填、可空 | 同上，命中自定义停止序列时为非 null |
| `usage` | object | 必填 | 用量（`input_tokens`/`output_tokens`/`cache_*_input_tokens`…） |

关键点：`Message.stop_reason` 的官方注释明确写着 **"In streaming mode, it is null in the message_start event and non-null otherwise"**（来源：Go SDK `message.go` 的 `Message` 结构 doc 注释，`https://github.com/anthropics/anthropic-sdk-go/blob/main/message.go`）。所以转换层发 `message_start` 时 `stop_reason`/`stop_sequence` **必须显式输出为 `null`，而不是省略或输出空串 `""`**。

### 3.3 content_block_start

```json
{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}
{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_...","name":"get_weather","input":{}}}
```

| 字段 | 类型 | 必填/可空 | 说明 |
|---|---|---|---|
| `index` | number | **必填（0 时也必须出现）** | 内容块的序号，从 0 开始 |
| `content_block` | object | 必填 | 见 3.6 各类型字段 |

来源：Go SDK `message.go` 的 `ContentBlockStartEvent`（`ContentBlock ... api:"required"`、`Index ... api:"required"`）；TS SDK `RawContentBlockStartEvent`（`content_block`、`index` 均为非可选属性）。

### 3.4 content_block_delta

```json
{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"H"}}
{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}
```

| 字段 | 类型 | 必填/可空 | 说明 |
|---|---|---|---|
| `index` | number | **必填（含 0）** | 引用 `content_block_start` 的 `index`，客户端按它把增量拼到对应块 |
| `delta` | object | 必填 | 必须有 `type` 字段 + 该类型的必填字段 |

`delta.type` 五种取值（来源：TS SDK `RawContentBlockDelta` / Go SDK `RawContentBlockDeltaUnion`）：

| delta.type | 额外必填字段 | 说明 |
|---|---|---|
| `text_delta` | `text`（string，**空串也必须出现**） | 文本增量，客户端连续拼接 |
| `input_json_delta` | `partial_json`（string） | 工具输入 JSON 的流式片段，客户端累加后 parse |
| `thinking_delta` | `thinking`（string） | 思考文本增量 |
| `signature_delta` | `signature`（string） | thinking 块结束前的签名 |
| `citations_delta` | `citation`（object） | 引文增量 |

### 3.5 content_block_stop

```json
{"type":"content_block_stop","index":0}
```

只有 `type` + `index` 两个字段（来源：Go SDK `ContentBlockStopEvent`）。`index` **依旧必填含 0**。

### 3.6 message_delta

```json
{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":42}}
```

| 字段 | 类型 | 必填/可空 | 说明 |
|---|---|---|---|
| `delta` | object | 必填 | 见下 |
| `usage` | object | **必填** | 累计用量，至少含 `output_tokens`（number，非空） |

`delta` 子字段（来源：Go SDK `MessageDeltaEventDelta`，四个字段全 `api:"required"`；TS SDK `RawMessageDeltaEvent.Delta` 对应 `| null`）：

| 字段 | 类型 | 必填/可空 | 说明 |
|---|---|---|---|
| `stop_reason` | string \| null | 必填、可空 | 结束原因枚举，见 5.1 |
| `stop_sequence` | string \| null | 必填、可空 | 命中哪个自定义停止序列 |
| `stop_details` | object \| null | 必填、可空（新版规范） | 仅 `stop_reason == "refusal"` 时非 null |
| `container` | object \| null | 必填、可空（新版规范） | 代码执行容器信息，通常 `null` |

**要点**：`message_delta` 是整个流里 `stop_reason` 出现的**唯一**位置——客户端靠它判断对话如何结束（自然结束 / 被 max_tokens 截断 / 等工具调用）。转换层必须保证：`delta` 对象始终存在、`delta.stop_reason` 始终有键（未知时兜底 `"end_turn"`）、`usage` 对象始终存在。

### 3.7 message_stop

```json
{"type":"message_stop"}
```

只有 `type` 一个字段（来源：Go SDK `MessageStopEvent`、TS SDK `RawMessageStopEvent`）。**不要附加 `index` / `delta` / `usage` 等无关字段。** 它标志流结束，等价于 OpenAI 的 `data: [DONE]`。

### 3.8 ping / error

- `ping`：`event: ping` + `data: {"type":"ping"}`，用于保活，SDK 直接 `continue` 跳过（不发给你）。
- `error`：`event: error` + `data: {"type":"error","error":{"type":"overloaded_error","message":"..."}}`。官方 Go SDK 捕获 `event:` 行为 `error` 时，把 `data` 交给 `apierror` 解析成 API 错误并终止流（来源：`packages/ssestream/ssestream.go`）。错误 JSON 走标准 `{"error":{...}}` 信封，`error.type` 常见值：`invalid_request_error`/`authentication_error`/`permission_error`/`rate_limit_error`/`overloaded_error`/`api_error`。

---

## 4. content_block 各类型的字段规范

### 4.1 响应侧（上游 → 客户端）ContentBlock 类型枚举

官方 SDK 的响应 `ContentBlockUnion` / `ContentBlockStartEventContentBlockUnion` 列出的 `type` 枚举（来源：Go SDK `message.go`）：

| `type` | 必填字段 | 可空/可选 | 说明 |
|---|---|---|---|
| `text` | `text`（string）、`type` | `citations`（array \| null） | **`text` 空串也必须出现** |
| `thinking` | `thinking`（string）、`signature`（string）、`type` | — | 思考块 |
| `redacted_thinking` | `data`（string）、`type` | — | 安全打码的思考块 |
| `tool_use` | `id`（string）、`name`（string）、`input`（object）、`type` | `toolset_name`（string \| null）、`caller` | 客户端工具调用；`Input json.RawMessage \`json:"input,required"\`` |
| `server_tool_use` | `id`、`name`、`input`、`type` | `caller` | 服务端工具（web_search/web_fetch/code_execution…） |
| `web_search_tool_result` | `content`、`type` | — | 服务端 web 搜索结果 |
| `web_fetch_tool_result` | `content`、`url`、`retrieved_at`、`type` | — | 服务端 web 抓取结果 |
| `code_execution_tool_result` | `content`、`type` | `return_code`/`stdout`/`stderr` | 旧代码执行结果 |
| `bash_code_execution_tool_result` | `content`、`type` | — | bash 执行结果 |
| `text_editor_code_execution_tool_result` | `content`、`type` | — | 文本编辑器执行结果 |
| `tool_search_tool_result` | `content`、`type` | — | 工具检索结果 |
| `container_upload` | `file_id`、`type` | — | 容器文件上传 |

注意力在这三类（转换层最常碰到）：
- **text**：`{"type":"text","text":"..."}`，`citations` 可无（客户端按 null 处理）。
- **tool_use**：`{"type":"tool_use","id":"toolu_...","name":"...","input":{...}}`。Go SDK 里 `input` 标签是 `json:"input,required"`（区别于 Param 侧的 `input,omitzero`）——**响应侧 `input` 是必填对象**，空对象必须写成 `{}` 而不是省略/`""`（来源：Go SDK `message.go` `ToolUseBlock`）。
- **thinking**：`{"type":"thinking","thinking":"...","signature":"..."}`（`signature` 可作为 `signature_delta` delta 单独流式下发）。

### 4.2 请求侧 ContentBlockParam 类型枚举

请求里 `messages[].content` 数组、以及 `tool_result` 里的 `content`，可用块类型（来源：Go SDK `message.go` `ContentBlockParamUnion`）：

| `type` | 必填字段 | 说明 |
|---|---|---|
| `text` | `text`、`type` | 文本块 |
| `image` | `source`、`type` | `source.type` = `base64`（`data`+`media_type`）/`url`（`url`）/`file` |
| `document` | `source`、`type` | PDF/文本文档 |
| `tool_use` | `id`、`name`、`input`、`type` | 回传 assistant 的工具调用（多轮） |
| `tool_result` | `tool_use_id`、`type`；`content` 可选 | 工具结果；`content` 可为字符串或块数组 |
| `thinking` | `signature`、`thinking`、`type` | 回传思考块（多轮续写，须原样回传） |
| `redacted_thinking` | `data`、`type` | 回传打码思考块 |
| `search_result` | `content`（`[]TextBlockParam`）、`source`、`title`、`type` | 检索结果（tool_result 内） |
| `server_tool_use` | `id`、`name`、`input`、`type` | 回传服务端工具调用 |

注意：请求侧 Go SDK 把 `tool_use.input` 声明为 `json:"input,omitzero"`（可省略），但响应侧为 `json:"input,required"`。**转换层做「请求转发」时允许省略 input，但做「响应生成」时 input 必须出现**。

---

## 5. 请求侧字段（做 OpenAI → Anthropic 转换要生成的请求体）

### 5.1 顶层必填字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `model` | string | 必填 | 模型 ID |
| `max_tokens` | number | 必填 | 生成上限（官方注释要求，模型可能提前停） |
| `messages` | array | 必填 | 入参对话，每条含 `role` + `content` |

### 5.2 `messages[]` 每个元素

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `role` | string | 必填 | `user` / `assistant`（**没有 `system` role**，system 走顶层 `system` 字段；新版支持 `"system"` 作为 messages 中间消息，但那是进阶功能） |
| `content` | string \| array | 必填 | **两种形态等价**：字符串 = 单个 `{"type":"text","text":"..."}` 块的简写；数组 = content blocks |

来源：Go SDK `message.go` `MessageParam`（Content/Role 均 `api:"required"`）及 `MessageNewParams.Messages` 的 doc 注释（明确写「`content` may be either a single `string` or an array of content blocks」、string 是 text 块的简写、Messages API 不含 `"system"` role）。

### 5.3 顶层 `system`

- 类型：`string` **或** `[]TextBlockParam`（仅 text 块数组，带 `cache_control` 时可给最后一块打缓存断点）。
- Go SDK 声明为 `System []TextBlockParam`（`MessageNewParams`），但仍接受字符串。新版规范（Opus 5 / 4.8 / Fable 5 等）还支持把 `{"role":"system","content":"..."}` 追加到 `messages[]` 尾部作为「对话中系统消息」，与顶层 `system` 是两回事。

### 5.4 `tools` / `tool_choice` / `thinking`

| 字段 | 形态 | 必填 | 说明 |
|---|---|---|---|
| `tools` | array | 可选 | 每个工具：`name`（必填）、`input_schema`（必填，JSON Schema）、`description`（可选） |
| `tool_choice` | object | 可选 | `{"type":"auto"}` / `{"type":"any"}` / `{"type":"tool","name":"..."}` / `{"type":"none"}`；auto/any/tool 可带 `disable_parallel_tool_use` |
| `thinking` | object | 可选 | `{"type":"enabled","budget_tokens":N}`（老模型）/ `{"type":"adaptive"}`（4.6+ 推荐）；不过 portunus 转换只需透传 |
| `stream` | bool | 可选 | 流式时 `true` |

来源：Go SDK `MessageNewParams`（`Tools []ToolUnionParam`、`ToolChoice ToolChoiceUnionParam`、`Thinking ThinkingConfigParamUnion`、`ToolParam` 的 `InputSchema`/`Name` 为 `api:"required"`）；TS SDK `ToolResultBlockParam`、`ImageBlockParam`、`SearchResultBlockParam`。

---

## 6. 转换层必填字段陷阱清单（本项目的核心产出）

以下字段**在值为 0 / 空串 / 空对象 / 空数组时仍必须序列化出来**。这些都是 Claude Code 客户端（或其底层 SDK）按「字段一定存在」去 `.slice()` / `.at()` / 拼接而假设的，缺了就会 `undefined is not an object (evaluating 'e.slice')` 或「事件被静默丢弃」。

| # | 位置 | 字段 | 坑 | 正确输出 |
|---|---|---|---|---|
| 1 | content_block_start / delta / stop | `index` | Go 的 `omitempty` 会把 `0` 吞掉，客户端按 `index` 定位块时拿到 undefined | `"index":0` 必须出现（用 `*int` 或自定义 Marshal） |
| 2 | content_block_start 的 text 块 | `content_block.text` | 首块文本为空时 `omitempty` 吞掉，客户端对 `text` 做 `.slice()` 崩 | `{"type":"text","text":""}` 空串也要带 |
| 3 | content_block_start 的 tool_use 块 | `content_block.input` | `input` 为空对象时被吞，客户端拿 `input` 去 `JSON.parse`/索引崩 | `"input":{}`（`json.RawMessage("{}")`） |
| 4 | content_block_start 的 tool_use 块 | `content_block.id` `content_block.name` | 官方要求 required；缺失会让客户端拼 tool call 时 undefined | 两个字段都要带 |
| 5 | content_block_delta 的 text_delta | `delta.text` | 空增量也可能出现；`text` 是 `text_delta` 必填 | `"delta":{"type":"text_delta","text":"..."}` 空串也带 |
| 6 | content_block_delta 的 input_json_delta | `delta.partial_json` | 同上，`partial_json` 必填 | `"partial_json":"..."` 空串也带 |
| 7 | message_delta | `delta.stop_reason` | 丢了这个，客户端判断不出流怎么结束（还可能一直不标 finished） | 必须有键；未知时兜底 `"end_turn"` |
| 8 | message_delta | `delta.stop_sequence` | 官方 required（可空）；省略和 `""` 都不对 | `"stop_sequence":null` |
| 9 | message_delta | `usage` | 官方 required；OpenAI 上游常不带 usage，转换层就没得写，导致缺失 | 至少 `{"output_tokens":N}`；input 系列可为 null |
| 10 | message_start | `message.stop_reason` `message.stop_sequence` | 流起始必须是 `null`，不是 `""` 也不是省略 | `"stop_reason":null,"stop_sequence":null` |
| 11 | message_start | `message.content` | 必须是数组（空时 `[]`），不能 null/省略 | `"content":[]` |
| 12 | 每个事件帧 | `event:` 行 | 只发 `data:` 不发 `event:`，客户端按 `event:` 分发会静默丢弃事件 | 每个帧都要 `event: <类型>` + `data: {...}` + 空行 |
| 13 | 每个事件帧 | `data` JSON 的 `"type"` | `data` 里 `type` 决定 SDK 反序列化到哪个变体，缺失则事件无法解析 | 每个事件 JSON 都带 `"type"` |
| 14 | content_block_stop | — | 别画蛇添足加 `content_block`/`delta` | 只有 `type` + `index` |
| 15 | message_stop | — | 别加 `index`/`usage` | 只有 `"type":"message_stop"` |
| 16 | tool_result（请求侧）| `content` | `content` 可以是纯字符串；空结果应给 `""` 而非丢字段 | 至少给字符串或块数组 |

### Go 实现要点（对应本仓库 `backend/protocol/anthropic.go`）

- Hover：`int` 字段带 `omitempty` 会丢 `0` → 用 `*int`（本仓库已用 `Index *int`）+ 需要时解引用，或在 Marshal 前手动置位。
- `string` 字段带 `omitempty` 会丢 `""` → 对「必填字符串」字段（`text`/`partial_json`/`thinking`/`signature`）要么去 `omitempty`，要么像 `ContentBlock` 那样写自定义 `MarshalJSON`（本仓库已做）。
- `any`/`map` 字段带 `omitempty` 会丢 `{}` 和 `[]` → 对 `tool_use.input`（nil 时写 `json.RawMessage("{}")`）和 `message.content`（空时写 `[]`）用自定义序列化。
- `message_delta` 要把 `usage` 和 `delta.stop_reason`/`stop_sequence`（null）补全；不要依赖 OpenAI chunk 带 usage。

已核对本仓库代码：`backend/protocol/anthropic.go` 的 `ContentBlock.MarshalJSON`（text 恒带 `text`、tool_use nil input 兜底 `{}`）、`MessagesStreamEvent.Index *int`（避免吞 0）、`openAIToAnthropicStream.Finish`（补 `message_delta` + `message_stop`）均已落实第 1/2/3/15 条；但 `message_delta` 的 `usage` 仅在收到带 usage 的 chunk 时才写（`o.usage` 为 nil 时因 `omitempty` 被省略）、`stream_delta.stop_sequence` 恒缺省、`message_start` 的 `stop_reason`/`stop_sequence` 未显式输出 `null`——这三处仍在「缺必填字段」风险区内。

---

## 7. OpenAI 与 Anthropic 流式的关键差异对照（踩坑点）

| 维度 | OpenAI（Chat Completions 流式） | Anthropic（Messages 流式） |
|---|---|---|
| 事件命名 | `chat.completion.chunk`，一个结构复用 | 6 种具名事件，各自结构不同 |
| 事件分发 | 基本看 `data` 内容 | **`event:` 行 + `data.type` 双重标志** |
| 内容组织 | `choices[0].delta.content` 直接追加字符串 | `content_block_start`（开块，含 `index`）→ 多个 `content_block_delta` → `content_block_stop`（闭块） |
| 工具调用 | `delta.tool_calls[0].function.arguments` 片段 | `content_block_start(tool_use)` + `input_json_delta(partial_json)` + `content_block_stop` |
| 思考 | `delta.reasoning_content` | `thinking`/`redacted_thinking` 块 + `thinking_delta`/`signature_delta` |
| 结束原因 | `finish_reason`（在最后一个 chunk 的 `choices[0].finish_reason`） | `stop_reason`（在 `message_delta.delta.stop_reason`，独立事件） |
| 结束标记 | **`data: [DONE]`** sentinel | **`message_stop` 事件**（无 `[DONE]`） |
| 用量 | 可选（`stream_options.include_usage`），在末 chunk | `message_start.message.usage` + `message_delta.usage`（累计）两处 |
| 空值处理 | `finish_reason: null`（中途 chunk） | `stop_reason: null` 出现在 `message_start`，`message_delta` 里为具体值 |

finish_reason → stop_reason 映射（转换层用，本仓库 `anthropicStopToOpenAI` / `openAIFinishToAnthropic` 已实现）：`stop`↔`end_turn`、`length`↔`max_tokens`、`tool_calls`↔`tool_use`；另 Anthropic 独有 `stop_sequence`（对应 OpenAI `stop`）、`pause_turn`、`refusal`、`model_context_window_exceeded`。来源：Go SDK `message.go` 的 `StopReason` 枚举及 `Message` doc 注释。

---

## 8. 可直接复制的流式输出样例（text + tool_use 各一块）

```
event: message_start
data: {"type":"message_start","message":{"id":"msg_x","type":"message","role":"assistant","model":"claude-x","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":25,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_01","name":"get_weather","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"SF\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":42}}

event: message_stop
data: {"type":"message_stop"}
```

来源（事件名与字段结构）：`https://docs.claude.com/en/api/messages-streaming`（规范页，当前区域 302 到 app-unavailable-in-region，字段以同源 OpenAPI 生成源码为准）及 `https://github.com/anthropics/anthropic-sdk-go/blob/main/message.go`、`https://github.com/anthropics/anthropic-sdk-typescript/blob/main/src/resources/messages/messages.ts`。