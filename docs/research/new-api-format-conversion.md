# new-api 的 API 格式定义与格式转换机制研究

> 研究时间：2026-08-25。对象：`/Users/lixiuxiu/development_tool/projects/new-API`（QuantumNous/new-api）。
> 目的：为 portunus 的 protocol 协议转换模块提供参考。

---

## 1. 一句话结论

new-api 以 **OpenAI 格式作为内部统一标准**（请求 `dto.GeneralOpenAIRequest`、响应 `dto.OpenAITextResponse` / `dto.ChatCompletionsStreamResponse`），对**每个上游厂商**实现一个独立的 `Adaptor`（适配器）对象，适配器负责「统一请求 → 上游原生请求」和「上游原生响应 → 统一响应」两次转换；客户端侧能「要什么格式就给什么格式」，是靠另一套独立维度 `RelayFormat`（openai / claude / gemini / …）在响应出口按需再转一次。

---

## 2. 包结构 / 目录地图

| 目录 | 职责 |
|---|---|
| `dto/` | 统一 DTO：`openai_request.go`（请求）、`openai_response.go`（响应）、`claude.go`、`gemini.go`、`request_common.go` 等 |
| `relay/channel/` | 适配器核心。`adapter.go` 定义接口，`api_request.go` 提供公共请求执行，其余每个厂商一个子目录（`openai/` `claude/` `gemini/` `ali/` `baidu/` `aws/` `vertex/` …） |
| `relay/` | 调度层：`relay_adaptor.go`（按渠道类型选适配器）、`relay_task.go`（任务类）、`compatible_handler.go`（文本对话主流程）、`*_handler.go`（各 RelayMode 入口） |
| `relay/constant/relay_mode.go` | RelayMode 常量（ChatCompletions / Embeddings / Images / Responses / Gemini …），`Path2RelayMode` 从 URL 推导 |
| `relay/helper/` | 公共工具：`stream_scanner.go`（SSE 流水线）、`common.go`（写回客户端）、`valid_request.go`（按格式解析请求体） |
| `relay/common/relay_info.go` | `RelayInfo` 结构，贯穿全程的请求上下文 |
| `constant/` | `api_type.go`（APIType）、`channel.go`（ChannelType） |
| `common/api_type.go` | `ChannelType2APIType` 渠道→API 类型映射 |
| `types/relay_format.go` | `RelayFormat`：客户端侧格式枚举 |
| `router/relay-router.go` | 路径 → `controller.Relay(c, relayFormat)` 的入口分发 |
| `controller/relay.go` | 编排层：解析请求→鉴权→选渠道→重试→调各 RelayMode handler |

---

## 3. 核心接口：`Adaptor`（relay/channel/adapter.go:15）

每个上游厂商实现同一接口，接口里**同时**承载了请求转换、发请求、响应转换三件事：

```go
type Adaptor interface {
	Init(info *relaycommon.RelayInfo)
	GetRequestURL(info *relaycommon.RelayInfo) (string, error)
	SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error
	// 统一请求 → 上游原生请求（每个厂商一行，未支持的返回 not implemented）
	ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error)
	ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error)
	ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error)
	ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error)
	ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error)
	ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error)
	DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error)
	// 上游原生响应 → 统一格式 + usage（流式/非流式在内部二选一）
	DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError)
	GetModelList() []string
	GetChannelName() string
	ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error)
	ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error)
}
```

关键点：
- 转换方法返回 `any`，由各适配器自己决定原生结构类型（`*dto.ClaudeRequest`、`*dto.GeminiChatRequest`、`map[string]any`…），上层只负责 `json.Marshal`。
- 接口方法很多但**不是抽象基类**——new-api 的做法是接口全量定义 + 适配器里「用到的实现，用不到的直接 `errors.New("not implemented")`」。比如 claude 适配器的 `ConvertGeminiRequest` 就是 stub（relay/channel/claude/adaptor.go:22）。
- 响应里 `usage` 是 `any`，实际传的是 `*dto.Usage`，供上层计费。

另有 `TaskAdaptor` 接口（同文件 :34）服务视频/绘图等任务类接口，含计费估算 `EstimateBilling`、轮询 `FetchTask`/`ParseTaskResult`，与文本路径并行，此处不展开。

---

## 4. 适配器如何注册 / 选中

**没有 map、没有自动注册**，就是 `relay.GetAdaptor(apiType int)` 里一个显式 switch（relay/relay_adaptor.go:53）：

```go
func GetAdaptor(apiType int) channel.Adaptor {
	switch apiType {
	case constant.APITypeAli:      return &ali.Adaptor{}
	case constant.APITypeAnthropic: return &claude.Adaptor{}
	case constant.APITypeGemini:    return &gemini.Adaptor{}
	case constant.APITypeOpenAI:    return &openai.Adaptor{}
	// ... 约 30 个厂商
	case constant.APITypeOpenRouter, constant.APITypeXinference:
		return &openai.Adaptor{}   // 复用 openai 适配器
	}
	return nil
}
```

选中的输入 `apiType` 来自渠道的类型字段，链路是：

1. 渠道在 `constant/channel.go` 有 `ChannelType`（OpenAI=1, Anthropic=14, Gemini=24, …）
2. `common/api_type.go:5` `ChannelType2APIType` 把渠道类型归一到 `APIType`（OpenAI=0, Anthropic=1, Gemini=8, …，`constant/api_type.go`）
3. `relay/common/relay_info.go:187` `InitChannelMeta` 里做 `apiType, _ := common.ChannelType2APIType(channelType)` 存进 `info.ApiType`
4. `relay.GetAdaptor(info.ApiType)` 取出适配器实例，`adaptor.Init(info)`

即：**渠道类型(ChannelType) → APIType → 具体 Adaptor 实例**。渠道入库时定死类型，运行期实例是 `&厂商.Adaptor{}` 空结构体，无状态，转换上下文全靠 `RelayInfo`。

---

## 5. 一次 chat 调用的完整链路（file:line）

以文本对话为例：

```
router/relay-router.go  （/v1/chat/completions 等路径）
  └→ controller.Relay(c, types.RelayFormatOpenAI)          controller/relay.go:67
      ├─ helper.GetAndValidateRequest(c, format)           relay/helper/valid_request.go:20
      │    └─ 按 format 解析请求体 → dto.GeneralOpenAIRequest   （内部统一格式）
      ├─ relaycommon.GenRelayInfo(c, format, request, nil) relay/common/relay_info.go（按 format 设 RelayFormat）
      ├─ 计费/预扣费
      └─ 重试循环 for retry <= RetryTimes                 controller/relay.go:189
           ├─ getChannel() → 选中渠道，middleware.SetupContextForSelectedChannel
           │    └─ 把 channel_type/channel_key/base_url 等写进 gin.Context
           ├─ switch relayFormat → relay.TextHelper       controller/relay.go:52
           │    └─ relay.TextHelper(c, info)              relay/compatible_handler.go:26
           │         ├─ info.InitChannelMeta(c)           relay/common/relay_info.go:183
           │         │    └─ apiType = ChannelType2APIType(channel_type)
           │         ├─ adaptor := GetAdaptor(info.ApiType)  relay/relay_adaptor.go:53
           │         ├─ convertedRequest := adaptor.ConvertOpenAIRequest(c, info, request)  ← 请求转换
           │         │    └─ e.g. claude → RequestOpenAI2ClaudeMessage
           │         ├─ json.Marshal(convertedRequest)  → 按渠道设置 RemoveDisabledFields / ParamOverride
           │         ├─ adaptor.DoRequest(c, info, requestBody)  ← 发上游
           │         │    └─ channel.DoApiRequest(a, c, info, body)  relay/channel/api_request.go:290
           │         │         ├─ GetRequestURL() + SetupRequestHeader() + headerOverride
           │         │         └─ doRequest() → http.Client.Do()
           │         └─ usage := adaptor.DoResponse(c, httpResp, info)  ← 响应转换
           │              └─ info.IsStream ? StreamHandler : Handler
           └─ PostTextConsumeQuota() 按 usage 计费结算
```

两条「格式」维度全程独立：
- **上游原生格式**由 `ChannelType` 决定 → 决定 `ConvertOpenAIRequest` / `DoResponse` 内部用哪家的原生结构；
- **客户端想要的格式**由 `RelayFormat` 决定（`types/relay_format.go`：openai / claude / gemini / openai_responses / …）→ 决定响应出口要不要二次转换。

`RelayMode`（`relay/constant/relay_mode.go`，由 URL 路径 `Path2RelayMode` 推导）是第三条维度：它只负责「这是什么类型的接口」（chat / embedding / image / audio…），决定走哪个 `*_handler.go`，跟厂商无关。

---

## 6. 请求转换：统一(OpenAI) → 上游原生

### 6.1 目标本身就是 OpenAI 兼容（openai / ollama / deepseek / 各类代理）

基本是**直通 + 字段清洗**，没有真正的结构转换：

- `compatible_handler.go:110` 调 `adaptor.ConvertOpenAIRequest`，openai 适配器直接原样返回 `request`；
- 之后统一走 `relaycommon.RemoveDisabledFields(jsonData, ...)` 按渠道设置删字段、`ApplyParamOverride` 覆盖参数；
- 例外：OpenRouter Enterprise 等会把 body 再包一层。

### 6.2 OpenAI → Claude（claude/relay-claude.go:47 `RequestOpenAI2ClaudeMessage`）

一个手工映射函数，核心差异处理：

```go
claudeRequest := dto.ClaudeRequest{ Model, Temperature, Tools, ... }
// 1. tools：OpenAI function → Claude tool（inputSchema 重组）
//    textRequest.Tools[i].Function.Parameters → claudeTool.InputSchema
// 2. tool_choice / parallel_tool_calls → Claude tool_choice{type,name,disable_parallel_tool_use}
// 3. thinking：reasoning_effort / -thinking 后缀 → claudeRequest.Thinking{type, budget_tokens}
// 4. messages：
//    - 多条 system 消息 → 累积成顶层 System 数组
//    - tool 消息 → user 消息里塞 tool_result / tool_use
//    - 图片/PDF content → base64 的 image/document
// 5. max_tokens 缺省 → 按渠道/模型设置填默认
```

### 6.3 OpenAI → Gemini（gemini/relay-gemini.go:201 `CovertOpenAI2Gemini`）

同理，映射到 `dto.GeminiChatRequest{Contents, GenerationConfig}`：

```go
geminiRequest := dto.GeminiChatRequest{
	Contents:         make([]dto.GeminiChatContent, 0, len(textRequest.Messages)),
	GenerationConfig: dto.GeminiChatGenerationConfig{ Temperature: textRequest.Temperature },
}
// Temperature/TopP/MaxOutputTokens/Seed/StopSequences 逐字段搬运
// extra_body.google.thinking_config → 处理 thinking
// messages → Gemini Part（text / inlineData 图片 / functionCall / functionResponse）
// tools → Gemini FunctionDeclaration
```

### 6.4 链式转换（重点套路）

gemini 适配器支持「Claude 客户端请求 → Gemini 上游」：

```go
func (a *Adaptor) ConvertClaudeRequest(c, info, req *dto.ClaudeRequest) (any, error) {
	adaptor := openai.Adaptor{}                       // 先借 openai 适配器
	oaiReq, _ := adaptor.ConvertClaudeRequest(c, info, req)   // Claude → OpenAI
	return a.ConvertOpenAIRequest(c, info, oaiReq.(*dto.GeneralOpenAIRequest))  // OpenAI → Gemini
}
```

即：**不存在的直达路径，就「中转 OpenAI 格式」搭两步**。`RelayInfo.RequestConversionChain`（`relay/common/relay_info.go:167`）记录这条链（如 `["openai","claude"]`）用于后续定位上游格式。

---

## 7. 响应转换：上游原生 → 统一(OpenAI)

### 7.1 非流式（整块读 + 结构映射）

以 Claude 为例（relay/channel/claude/relay-claude.go:936 `ClaudeHandler`）：

```go
responseBody, _ := io.ReadAll(resp.Body)
HandleClaudeResponseData(...)
  ├─ Unmarshal → dto.ClaudeResponse
  ├─ claudeInfo.Usage ← claudeResponse.Usage（InputTokens/OutputTokens/cache…）
  └─ switch info.RelayFormat:
       case RelayFormatOpenAI:
           openaiResponse := ResponseClaude2OpenAI(&claudeResponse)   // → dto.OpenAITextResponse
           openaiResponse.Usage = buildOpenAIStyleUsageFromClaudeUsage(...)  // usage 语义归一
           responseData = json.Marshal(openaiResponse)
       case RelayFormatClaude:
           responseData = data   // 客户端就要 Claude 格式 → 直通
  service.IOCopyBytesGracefully(c, httpResp, responseData)  // 写回客户端
```

OpenAI 上游反过来：`OpenaiHandler`（relay/channel/openai/relay-openai.go:195）解析成 `dto.OpenAITextResponse` 后，若客户端要的是 Claude/Gemini，则 `service.ResponseOpenAI2Claude` / `ResponseOpenAI2Gemini` 反向转出去。

### 7.2 流式（核心难点）：scanner goroutine + dataChan + handler goroutine

统一骨架 `helper.StreamScannerHandler`（relay/helper/stream_scanner.go:37）：

```
上游 resp.Body
  │  bufio.Scanner 按行读（goroutine A: scanner）
  │  过滤: 只取 "data:xxx" / "[DONE]"
  │  经 dataChan(chan string, cap=10) 投递
  ▼
goroutine B: dataHandler —— 每条 data 调用适配器传入的回调：
      dataHandler(data, sr)
        └─ 各厂商 Handler 里做「该条 event → OpenAI chunk」并写回客户端
```

要点：
- **生产者/消费者 + channel 解耦**：scanner 读得快、转换写得慢时互不阻塞；
- 另有 ping goroutine 按间隔向客户端发 `: ping` 保活，`writeMutex` 串行化所有写；
- 超时 ticker、`stopChan`、`StreamStatus` 负责收尾与错误归因；
- 回调里可调用 `sr.Stop(err)` / `sr.Error(err)` 中止。

厂商流式转换示例 —— Claude → OpenAI（relay/channel/claude/relay-claude.go:869 `ClaudeStreamHandler`）：

```go
helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
	err = HandleStreamResponseData(c, info, claudeInfo, data)
	if err != nil { sr.Stop(err) }
})
HandleStreamFinalResponse(c, info, claudeInfo)   // 流结束补 usage / [DONE]
```

`HandleStreamResponseData` 内按 `claudeResponse.Type` 逐条映射（relay-claude.go:784、`StreamResponseClaude2OpenAI`:437）：

| Claude 事件 | 映射到 OpenAI chunk |
|---|---|
| `message_start` | 设 `id` / `model` / `role=assistant`，收 usage |
| `content_block_start` | text 首段 / `tool_use` 开个 tool_call |
| `content_block_delta` | `delta.content`；`thinking_delta→delta.reasoning_content`；`input_json_delta→tool_calls[].function.arguments` |
| `message_delta` | 由 `stop_reason` 映射 `finish_reason`（`reasonmap.ClaudeStopReasonToOpenAIFinishReason`），补 usage |
| `message_stop` | 丢弃（末尾 `[DONE]` 由框架补） |

流式期间用**有状态的结构累积元信息**（`ClaudeResponseInfo`，relay-claude.go:582）：跨多个 SSE 事件累积 `ResponseText`、`Usage`、`ResponseId/Model`，`FormatClaudeResponseInfo`（:712）负责归并；流末 `HandleStreamFinalResponse`（:831）若上游没给全 usage，用 `service.ResponseText2Usage` 按已收到的文本回退估算。

OpenAI 上游流式（`OaiStreamHandler`，relay-openai.go:106）是直通：每条 chunk 原样转发（可选 `ForceFormat`/`ThinkingToContent` 加工），从流里抠 usage，最后 `HandleFinalResponse` 补 `[DONE]`。

### 7.3 统一的响应结构

统一出口是 `dto/openai_response.go` 里的 OpenAI 结构：
- 非流式 `dto.OpenAITextResponse{ Id, Object, Choices[]{Message,FinishReason}, Usage }`
- 流式 `dto.ChatCompletionsStreamResponse{ Object:"chat.completion.chunk", Choices[]{Delta,FinishReason} }`
- `dto.Usage{ PromptTokens, CompletionTokens, TotalTokens, PromptTokensDetails{CachedTokens,...} }`

`usage` 语义归一：各家上游的 usage 结构（Claude 的 cache_read/cache_creation、Gemini 的 promptTokenCount）在适配器里折算进 `dto.Usage`，方便上层统一计费。

---

## 8. 对 portunus 的启示 / 可借鉴点

1. **以 OpenAI 格式为内部统一标准，一个厂商一个 Adaptor 实现同一接口**。new-api 的接口方法偏多（含 rerank/embedding/image/audio 等），portunus 现阶段只需 `ConvertRequest` / `DoRequest` / `DoResponse` 三件套 + `GetRequestURL`/`SetupRequestHeader`，接口保持小即可；未实现的返回 `errors.New("not implemented")` 而非 panic。
2. **渠道类型(ChannelType) → GetAdaptor switch 显式映射**足够简单可靠，几十个厂商一个函数搞定；不要一上来就搞反射注册表。portunus 的 `channel` 模块已有类型字段，映射表直接对应。
3. **流式转换的「scanner goroutine + dataChan + handler goroutine」流水线 + 有状态响应累积器**是抄得最值的东西：scanner 只负责剥 `data:` 前缀，转换回调逐条把上游 event 映射成 OpenAI chunk，另配 `ResponseInfo` 累积 usage/text。portunus 的 protocol 流式转换应照这个结构写。
4. **客户端格式(RelayFormat)与上游格式(ChannelType)两个维度分离**：上游走 `ConvertRequest`/`DoResponse` 定型，客户端要什么格式在出口再转一次；并支持「无直达路径就中转 OpenAI 格式」的链式转换。portunus 若支持 OpenAI/Anthropic/Gemini 三类客户端接入，可按此拆两个维度，避免适配器矩阵爆炸。
