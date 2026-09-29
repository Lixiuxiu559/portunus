package protocol

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// 本文件定义 Anthropic Messages API 的结构，以及 anthropic ↔ openai 的请求 / 响应转换。
// 流式转换与流事件结构在 anthropic_stream.go。

// MessagesRequest 是 Anthropic Messages 请求体。
type MessagesRequest struct {
	Model         string             `json:"model"`
	MaxTokens     int                `json:"max_tokens"`
	System        any                `json:"system,omitempty"` // string 或 []ContentBlock
	Messages      []AnthropicMessage `json:"messages"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	Stream        bool               `json:"stream,omitempty"`
	Tools         []AnthropicTool    `json:"tools,omitempty"`
	ToolChoice    any                `json:"tool_choice,omitempty"`
	Thinking      *ThinkingConfig    `json:"thinking,omitempty"`
}

// AnthropicMessage 是一条 Anthropic 对话消息。
type AnthropicMessage struct {
	Role    string         `json:"role"` // user / assistant
	Content []ContentBlock `json:"content"`
}

// UnmarshalJSON 兼容 content 的两种形态：字符串（等价单个 text 块）或内容块数组。
// Anthropic 规范二者等价，但 Claude Code 等客户端两种都会发，缺了这个字符串就解析失败。
func (m *AnthropicMessage) UnmarshalJSON(data []byte) error {
	var aux struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	m.Role = aux.Role
	if len(aux.Content) == 0 || string(aux.Content) == "null" {
		m.Content = nil
		return nil
	}
	// 字符串 → 单个 text 块
	if aux.Content[0] == '"' {
		var s string
		if err := json.Unmarshal(aux.Content, &s); err != nil {
			return err
		}
		m.Content = []ContentBlock{{Type: "text", Text: s}}
		return nil
	}
	// 数组 → 直接解析
	return json.Unmarshal(aux.Content, &m.Content)
}

// ContentBlock 是 Anthropic 消息 content 数组的一个单元。
type ContentBlock struct {
	Type      string       `json:"type"` // text / image / tool_use / server_tool_use / tool_result / web_search_tool_result / thinking / redacted_thinking
	Text      string       `json:"text,omitempty"`
	ID        string       `json:"id,omitempty"`
	Name      string       `json:"name,omitempty"`        // tool_use
	Input     any          `json:"input,omitempty"`       // tool_use 参数（对象）
	ToolUseID string       `json:"tool_use_id,omitempty"` // tool_result
	Content   any          `json:"content,omitempty"`     // tool_result 结果（字符串或块数组）
	Source    *ImageSource `json:"source,omitempty"`      // image
	Thinking  string       `json:"thinking,omitempty"`    // thinking
	// Signature 是 thinking 块的签名。本网关在签名里承载 reasoning 桥载荷
	// （见 reasoning_bridge.go）：客户端原样回传即可无损还原 reasoning_content。
	Signature string `json:"signature,omitempty"`
	// Data 是 redacted_thinking 的密文载荷。此前缺此字段导致密文被静默丢弃，
	// 只能一律回填占位；现在至少能识别出「密文恰是本网关桥载荷」的情形并还原。
	Data string `json:"data,omitempty"`
}

// MarshalJSON 按 Anthropic 规范的字段裁剪序列化：text 块始终携带 text
// （流式 content_block_start 里 text 块必须含 "text":""，否则客户端拿不到
// 字符串字段会崩）。tool_use / image 等块不带无意义的 text 空字段。
func (b ContentBlock) MarshalJSON() ([]byte, error) {
	m := map[string]any{"type": b.Type}
	switch b.Type {
	case "text":
		m["text"] = b.Text
	case "tool_use":
		// id / name / input 都是必填字段，空串/空对象时也必须输出，
		// 否则客户端拿 undefined 做 slice / 索引会崩（与 text 的 text 同病根）。
		m["id"] = b.ID
		m["name"] = b.Name
		if b.Input != nil {
			m["input"] = b.Input
		} else {
			m["input"] = json.RawMessage(`{}`)
		}
	case "image":
		if b.Source != nil {
			m["source"] = b.Source
		}
	case "tool_result":
		m["tool_use_id"] = b.ToolUseID
		if b.Content != nil {
			m["content"] = b.Content
		}
	case "thinking":
		// thinking / signature 是必填字段，空串时也必须输出（与 text 块的 text 同病根）
		m["thinking"] = b.Thinking
		m["signature"] = b.Signature
	case "redacted_thinking":
		// data 是 redacted_thinking 的唯一内容字段：密文原样回传才能被上游识别。
		m["data"] = b.Data
	case "server_tool_use":
		// 服务端工具调用：id / name / input 与 tool_use 同病根——空串/空对象时
		// 也必须输出，否则客户端拿 undefined 做索引会崩。
		m["id"] = b.ID
		m["name"] = b.Name
		if b.Input != nil {
			m["input"] = b.Input
		} else {
			m["input"] = json.RawMessage(`{}`)
		}
	case "web_search_tool_result":
		// content 是必填字段（搜索结果数组），为空时输出 []。
		m["tool_use_id"] = b.ToolUseID
		if b.Content != nil {
			m["content"] = b.Content
		} else {
			m["content"] = []any{}
		}
	}
	return json.Marshal(m)
}

// ImageSource 是 Anthropic 图片来源。
type ImageSource struct {
	Type      string `json:"type"` // base64 / url
	MediaType string `json:"media_type"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// AnthropicTool 是 Anthropic 工具定义。
// Type 非空表示服务端托管工具（如 Claude Code 的 web_search_20250305）：
// 这类声明没有 input_schema，只有 type/name 与可选字段，此前缺此字段导致
// 入站被当作普通空壳 function 工具、出站又丢回 type——第一现场在此。
type AnthropicTool struct {
	Type        string `json:"type,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema,omitempty"`
}

// ThinkingConfig 定义在 openai.go（canonical 层与 Anthropic 同形状，此处复用）。

// MessagesResponse 是 Anthropic Messages 非流式响应。
type MessagesResponse struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	Role         string          `json:"role"`
	Content      []ContentBlock  `json:"content"`
	Model        string          `json:"model"`
	StopReason   string          `json:"stop_reason"`
	StopSequence string          `json:"stop_sequence,omitempty"`
	Usage        *AnthropicUsage `json:"usage"`
}

// MarshalJSON 按规范序列化：stop_reason / stop_sequence 在空串时输出 null
// （流式 message_start 里二者必须为 null 而非 "" 或省略），content 为 nil 时输出 []。
func (m MessagesResponse) MarshalJSON() ([]byte, error) {
	out := map[string]any{
		"id":      m.ID,
		"type":    m.Type,
		"role":    m.Role,
		"content": m.Content,
		"model":   m.Model,
	}
	if m.StopReason != "" {
		out["stop_reason"] = m.StopReason
	} else {
		out["stop_reason"] = nil
	}
	if m.StopSequence != "" {
		out["stop_sequence"] = m.StopSequence
	} else {
		out["stop_sequence"] = nil
	}
	out["usage"] = m.Usage
	if m.Content == nil {
		out["content"] = []ContentBlock{}
	}
	return json.Marshal(out)
}

// AnthropicUsage 是 Anthropic 用量。
type AnthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// ===== 请求：anthropic → openai =====

// anthropicRequestToOpenAI 将 Anthropic 请求体转换为 OpenAI 规范请求。
func anthropicRequestToOpenAI(body []byte) (*ChatCompletionRequest, error) {
	var req MessagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("解析 anthropic 请求失败: %w", err)
	}

	out := &ChatCompletionRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   intPtr(req.MaxTokens),
		Stop:        req.StopSequences,
		Stream:      req.Stream,
	}

	// system：string 直接拼成一条 system 消息；[]ContentBlock 则把 text 块拼起来
	if sys := req.System; sys != nil {
		if text := anthropicSystemToText(sys); text != "" {
			out.Messages = append(out.Messages, ChatMessage{Role: "system", Content: text})
		}
	}

	// 先收集 assistant.tool_use 的 id → 工具名映射，供 tool_result 补 name 字段
	// （部分上游如 DeepSeek 要求 tool 消息必须带 name）。server_tool_use 同理：
	// web_search_tool_result 的 tool_use_id 指向对应的 server_tool_use 块。
	toolNameByID := map[string]string{}
	for _, m := range req.Messages {
		if m.Role != "assistant" {
			continue
		}
		for _, block := range m.Content {
			if block.Type == "tool_use" || block.Type == "server_tool_use" {
				toolNameByID[block.ID] = block.Name
			}
		}
	}

	// messages → chat messages（一条 anthropic 消息可能拆成多条 OpenAI 消息）
	for _, m := range req.Messages {
		msgs, decoded := anthropicMessagesToChat(m, toolNameByID)
		out.Messages = append(out.Messages, msgs...)
		out.bridgeDecoded += decoded // 桥接命中遥测，见 ChatCompletionRequest.bridgeDecoded
	}

	// tools → function tools；托管工具声明分流（见下方注释）
	for _, t := range req.Tools {
		if t.Type != "" {
			if isVersionedClaudeWebSearchType(t.Type) {
				// 版本化 web_search 声明：归一到 canonical 统一语义名 web_search
				//（归一理由见 openai.go isHostedToolType 注释）。版本间的差异字段
				// MVP 不承载，出站统一字面化到基线版本。不填 Parameters/
				// Description——hosted 工具没有 schema，填空壳会让出站被误渲染成
				// 普通 function（历史上的第一现场）。
				out.Tools = append(out.Tools, Tool{
					Type:     "web_search",
					Function: FunctionDef{Name: t.Name},
				})
				continue
			}
			// 其余未知 hosted 类型（code_execution_* 等）：canonical 无法承载，
			// 剥离并计入遥测（见 ComposeStats.HostedToolsStripped）。
			out.hostedToolsStripped++
			continue
		}
		out.Tools = append(out.Tools, Tool{
			Type: "function",
			Function: FunctionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}
	if req.ToolChoice != nil {
		out.ToolChoice = anthropicToolChoiceToOpenAI(req.ToolChoice)
	}
	return out, nil
}

// isVersionedClaudeWebSearchType 判断 Anthropic 的 type 是否是版本化的
// web_search 服务端工具声明：形如 web_search_20250305，即 web_search_ 前缀
// 后接恰 8 位纯数字日期。参考 new-api 的 isVersionedClaudeWebSearchType——
// 校验而不是只认字面量，是为了上游日后出新日期版本（web_search_20251111）
// 时自动跟随，不必逐个枚举。
func isVersionedClaudeWebSearchType(t string) bool {
	const prefix = "web_search_"
	if !strings.HasPrefix(t, prefix) {
		return false
	}
	date := t[len(prefix):]
	if len(date) != 8 {
		return false
	}
	for _, c := range date {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// anthropicSystemToText 把 system（string 或 content blocks）压成一段文本。
func anthropicSystemToText(sys any) string {
	switch v := sys.(type) {
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, item := range v {
			block, _ := item.(map[string]any)
			if t, _ := block["type"].(string); t == "text" {
				sb.WriteString(fmt.Sprint(block["text"]))
			}
		}
		return sb.String()
	}
	return ""
}

// anthropicMessagesToChat 将一条 Anthropic 消息转成若干条 OpenAI ChatMessage。
// 一条含多个 tool_result 的 user 消息会拆成多条 tool 消息。
// toolNames 是 assistant.tool_use 的 id→name 映射，用于给 tool 消息补 name 字段。
// 第二个返回值是本次消息里从块签名成功还原 reasoning_content 的次数（遥测，见
// ChatCompletionRequest.bridgeDecoded）。
func anthropicMessagesToChat(m AnthropicMessage, toolNames map[string]string) ([]ChatMessage, int) {
	var toolResults []ChatMessage
	var toolImages []any // tool_result 里的图片，转出后拼进紧随的 user 消息
	var out ChatMessage
	out.Role = m.Role
	var textParts []string
	// 依原始顺序收集 text / image 内容块：无图时 content 退化为纯文本字符串
	// （多数 OpenAI 兼容上游对字符串形态最稳），有图才升级为内容块数组。
	var contentParts []any
	hasImage := false
	bridgeDecoded := 0

	for _, block := range m.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
			contentParts = append(contentParts, map[string]any{"type": "text", "text": block.Text})
		case "image":
			if part := imageSourceToContentPart(block.Source); part != nil {
				hasImage = true
				contentParts = append(contentParts, part)
			}
		case "thinking":
			// 思考内容映射为 OpenAI 的 reasoning_content 扩展字段。
			// DeepSeek 等兼容方在 thinking 模式下要求多轮对话把上一轮思考内容
			// 原样回传（reasoning_content），直接丢弃会导致上游 400：
			// "The reasoning_content in the thinking mode must be passed back to the API"。
			// 优先从 signature 的桥载荷还原：那是本网关写出的原始 reasoning_content，
			// 逐字节一致——跨轮前缀因此稳定，上游自动前缀缓存才能命中。签名不是桥
			// 格式（上游原生签名、旧常量 "sig"）时退回 thinking 明文。
			if rc, ok := DecodeReasoningSignature(block.Signature); ok {
				out.ReasoningContent += rc
				bridgeDecoded++
			} else {
				out.ReasoningContent += block.Thinking
			}
		case "redacted_thinking":
			// Claude Code 把历史思考加密为 redacted_thinking 块：密文在 data 字段、
			// 无 thinking 明文（cc-switch 同款场景）。拼 block.Thinking 得空串，
			// 空 reasoning_content 会被严格 thinking 上游视同缺失照样 400。
			// 密文恰是本网关桥载荷时解码还原（无损、前缀稳定）；否则是上游加密
			// 内容、无从还原，注入占位表明「此处曾有思考、内容不可用」。
			//
			// 这里注入是**无条件**的：客户端回了这个块但读不出内容，块本身存在于
			// 历史，占位保留「此处曾有思考」的语义——粒度收窄不适用（垫片的收窄
			// 针对的是「客户端压根没带思考块的消息」，见 thinking_compat.go
			// InjectReasoningPlaceholders，场景不同）。
			if rc, ok := DecodeReasoningSignature(block.Data); ok {
				out.ReasoningContent += rc
				bridgeDecoded++
			} else {
				out.ReasoningContent += ReasoningPlaceholder
			}
		case "tool_use", "server_tool_use":
			// tool_use / server_tool_use 形状完全一致（服务端托管工具调用与普通工具
			// 调用的区别只在语义来源），统一映射为 tool_calls，客户端下一轮才有一条
			// 完整的 assistant 历史可回传。
			out.ToolCalls = append(out.ToolCalls, toolCallFromBlock(block))
		case "tool_result", "web_search_tool_result":
			// tool_result / web_search_tool_result 均映射为 tool 消息。content 归一
			// 按块类型分派：普通 tool_result 抽文本块并收集其中的图片（图片不进 tool
			// 消息——OpenAI 规范 tool content 仅允许文本，DeepSeek 等严格上游对数组
			// content 直接 400，挪到紧随其后的 user 消息）；web_search_tool_result 的
			// content 是 [{type:web_search_result,url,title},...]，需专门归一且不收集
			// 图片。
			name := toolNames[block.ToolUseID]
			content := anthropicToolResultText(block.Content)
			var images []any
			if block.Type == "tool_result" {
				images = anthropicToolResultImageParts(block.Content)
			} else {
				content = anthropicWebSearchResultText(block.Content)
			}
			toolResults = append(toolResults, ChatMessage{
				Role:       "tool",
				Content:    content,
				ToolCallID: block.ToolUseID,
				Name:       &name,
			})
			toolImages = append(toolImages, images...)
		}
	}

	if len(toolResults) > 0 {
		// tool_result 与其他内容块混排：Anthropic 允许 tool_result 后跟 text/image，
		// 拆出的 tool 消息之外，剩余文本与图片（含 tool_result 内的图）拼成一条
		// user 消息补在末尾，不随提前 return 整体丢弃。
		if hasImage || len(toolImages) > 0 {
			parts := contentParts
			parts = append(parts, toolImages...)
			toolResults = append(toolResults, ChatMessage{Role: m.Role, Content: parts})
		} else if len(textParts) > 0 {
			toolResults = append(toolResults, ChatMessage{Role: m.Role, Content: strings.Join(textParts, "")})
		}
		return toolResults, bridgeDecoded
	}
	// 既无 text、无图、无 tool_calls、也无思考内容的消息整条丢弃，
	// 避免生成空的 assistant 消息导致上游 400。
	if len(textParts) == 0 && !hasImage && len(out.ToolCalls) == 0 && out.ReasoningContent == "" {
		return nil, bridgeDecoded
	}
	if hasImage {
		out.Content = contentParts
	} else if len(textParts) > 0 {
		out.Content = strings.Join(textParts, "")
	}
	return []ChatMessage{out}, bridgeDecoded
}

// toolCallFromBlock 把 tool_use / server_tool_use 块序列化为 canonical ToolCall：
// 两者形状完全一致（id / name / input），仅语义来源不同——server_tool_use 是
// Anthropic 服务端托管工具（如 web_search）的调用，客户端回传历史时同样需要
// 一条完整的 tool_calls。input 序列化失败时兜底空对象。
func toolCallFromBlock(block ContentBlock) ToolCall {
	args := "{}"
	if block.Input != nil {
		if b, err := json.Marshal(block.Input); err == nil {
			args = string(b)
		}
	}
	return ToolCall{
		ID:       block.ID,
		Type:     "function",
		Function: FunctionCall{Name: block.Name, Arguments: args},
	}
}

// imageSourceToContentPart 把 Anthropic image 块的 source 转为 OpenAI image_url
// 内容块：base64 源拼成 data URI（media_type 缺省按 png），url 源直接透传。
// 无法识别的源（base64 缺 data、url 为空）返回 nil（跳过该块）。
func imageSourceToContentPart(src *ImageSource) map[string]any {
	if src == nil {
		return nil
	}
	url := src.URL
	if src.Type == "base64" {
		if src.Data == "" {
			return nil // 空 data 只会拼出空载荷 data URI，发给上游必被拒
		}
		media := src.MediaType
		if media == "" {
			media = "image/png"
		}
		url = "data:" + media + ";base64," + src.Data
	}
	if url == "" {
		return nil
	}
	return map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}
}

// imageSourceFromAny 把 tool_result content 里未经类型化的 image source
// （解析后的 map[string]any）还原为 ImageSource。逐字段类型断言，避免对可能
// 含多 MB base64 载荷的 map 做整串 marshal/unmarshal 往返（每图约 3 倍瞬态分配）。
func imageSourceFromAny(v any) *ImageSource {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	src := &ImageSource{}
	src.Type, _ = m["type"].(string)
	src.MediaType, _ = m["media_type"].(string)
	src.Data, _ = m["data"].(string)
	src.URL, _ = m["url"].(string)
	return src
}

// anthropicToolResultText 提取 tool_result content 的纯文本（OpenAI tool 消息
// content 仅允许文本，图片由 anthropicToolResultImageParts 单独收集）。
func anthropicToolResultText(c any) string {
	switch v := c.(type) {
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, item := range v {
			block, _ := item.(map[string]any)
			if t, _ := block["type"].(string); t == "text" {
				sb.WriteString(fmt.Sprint(block["text"]))
			}
		}
		return sb.String()
	}
	return ""
}

// anthropicWebSearchResultText 把 web_search_tool_result 的 content（形如
// [{"type":"web_search_result","url":..,"title":..},...]）序列化为紧凑文本，
// 每条一行 "title: url"。tool 消息内容为空会让部分严格上游 400，而
// anthropicToolResultText 只抽 text 块、对搜索结果块得空串，故单独归一；
// 无 web_search_result 块时退回通用文本抽取（确定性输出，顺序即数组顺序）。
func anthropicWebSearchResultText(c any) string {
	items, ok := c.([]any)
	if !ok {
		return anthropicToolResultText(c)
	}
	var lines []string
	for _, item := range items {
		block, _ := item.(map[string]any)
		if t, _ := block["type"].(string); t == "web_search_result" {
			title, _ := block["title"].(string)
			url, _ := block["url"].(string)
			lines = append(lines, title+": "+url)
		}
	}
	if len(lines) == 0 {
		return anthropicToolResultText(c)
	}
	return strings.Join(lines, "\n")
}

// anthropicToolResultImageParts 提取 tool_result content 里的图片，转为 OpenAI
// image_url 内容块数组（data URI / URL），供拼进紧随 tool 消息之后的 user 消息。
func anthropicToolResultImageParts(c any) []any {
	items, ok := c.([]any)
	if !ok {
		return nil
	}
	var parts []any
	for _, item := range items {
		block, _ := item.(map[string]any)
		if t, _ := block["type"].(string); t == "image" {
			if part := imageSourceToContentPart(imageSourceFromAny(block["source"])); part != nil {
				parts = append(parts, part)
			}
		}
	}
	return parts
}

// anthropicToolChoiceToOpenAI 将 Anthropic tool_choice 映射为 OpenAI tool_choice。
// Anthropic 用 {type:auto|any|tool|none}，OpenAI 用 "auto"|"none"|"required" 或
// {type:function,function:{name}}。若不映射直接透传 {type:auto}，OpenAI 兼容上游
// （Rust serde）会报 "tool_choice: field `type`: unknown variant `auto`, expected `function`"。
func anthropicToolChoiceToOpenAI(choice any) any {
	m, ok := choice.(map[string]any)
	if !ok {
		return choice
	}
	switch m["type"] {
	case "auto":
		return "auto"
	case "any":
		return "required"
	case "none":
		return "none"
	case "tool":
		name, _ := m["name"].(string)
		return map[string]any{"type": "function", "function": map[string]any{"name": name}}
	}
	return choice
}

// openAIToolChoiceToAnthropic 将 OpenAI tool_choice 映射为 Anthropic tool_choice。
// OpenAI 用 "auto"|"none"|"required" 或 {type:function,function:{name}}，
// Anthropic 用 {type:auto|any|tool|none}。
func openAIToolChoiceToAnthropic(choice any) any {
	switch v := choice.(type) {
	case string:
		switch v {
		case "none":
			return map[string]any{"type": "none"}
		case "required":
			return map[string]any{"type": "any"}
		case "auto":
			return map[string]any{"type": "auto"}
		}
	case map[string]any:
		if v["type"] == "function" {
			if fn, ok := v["function"].(map[string]any); ok {
				name, _ := fn["name"].(string)
				return map[string]any{"type": "tool", "name": name}
			}
		}
	}
	return choice
}

// ===== 请求：openai → anthropic =====

// anthropicRequestFromOpenAI 将 OpenAI 规范请求转为 Anthropic 请求体。
func anthropicRequestFromOpenAI(req *ChatCompletionRequest) ([]byte, error) {
	out := MessagesRequest{
		Model:         req.Model,
		StopSequences: stopToStrings(req.Stop),
		Stream:        req.Stream,
		Temperature:   req.Temperature,
		TopP:          req.TopP,
	}
	if req.MaxTokens != nil {
		out.MaxTokens = *req.MaxTokens
	}
	if out.MaxTokens == 0 && req.MaxCompletionTokens != nil {
		out.MaxTokens = *req.MaxCompletionTokens
	}

	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			out.System = appendSystem(out.System, chatContentToText(m.Content))
		case "tool":
			out.Messages = append(out.Messages, AnthropicMessage{
				Role: "user",
				Content: []ContentBlock{{
					Type:      "tool_result",
					ToolUseID: m.ToolCallID,
					Content:   chatContentToText(m.Content),
				}},
			})
		case "assistant":
			msg := AnthropicMessage{Role: "assistant"}
			if text := chatContentToText(m.Content); text != "" {
				msg.Content = append(msg.Content, ContentBlock{Type: "text", Text: text})
			}
			for _, tc := range m.ToolCalls {
				msg.Content = append(msg.Content, ContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: rawJSONOrEmptyObject(tc.Function.Arguments),
				})
			}
			out.Messages = append(out.Messages, msg)
		case "user":
			out.Messages = append(out.Messages, AnthropicMessage{
				Role:    "user",
				Content: chatContentToAnthropicBlocks(m.Content),
			})
		}
	}

	for _, t := range req.Tools {
		if isHostedToolType(t.Type) {
			// 托管工具声明：canonical 的统一语义名（web_search）字面化为 Anthropic
			// 基线版本 web_search_20250305。绝不写 input_schema/description——
			// hosted 声明没有 schema，填了会被上游当作普通 function 工具。
			name := t.Function.Name
			if name == "" {
				// responses 出站不写 name 是协议差异（hosted 声明只认 type），但
				// Anthropic 的 hosted 声明 name 必填：openai 入站只带
				// {"type":"web_search"} 无 function.name 时，缺兜底会渲染出
				// "name":"" 空键污染声明，这里回填 canonical 统一语义名。
				name = "web_search"
			}
			out.Tools = append(out.Tools, AnthropicTool{Type: "web_search_20250305", Name: name})
			continue
		}
		schema := t.Function.Parameters
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		out.Tools = append(out.Tools, AnthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: schema,
		})
	}
	if req.ToolChoice != nil {
		out.ToolChoice = openAIToolChoiceToAnthropic(req.ToolChoice)
	}
	return json.Marshal(out)
}

// appendSystem 把一段 system 文本累积到 out.System（保持 string 或升级为 []ContentBlock）。
func appendSystem(cur any, text string) any {
	if text == "" {
		return cur
	}
	if cur == nil {
		return text
	}
	switch v := cur.(type) {
	case string:
		return []ContentBlock{{Type: "text", Text: v}, {Type: "text", Text: text}}
	case []ContentBlock:
		return append(v, ContentBlock{Type: "text", Text: text})
	}
	return text
}

// chatContentToText 把 OpenAI content（string / []ContentPart / nil）压成纯文本。
func chatContentToText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, item := range v {
			part, _ := item.(map[string]any)
			if t, _ := part["type"].(string); t == "text" {
				sb.WriteString(fmt.Sprint(part["text"]))
			}
		}
		return sb.String()
	}
	return ""
}

// chatContentToAnthropicBlocks 把 OpenAI 多模态 content 转成 Anthropic content blocks。
func chatContentToAnthropicBlocks(content any) []ContentBlock {
	switch v := content.(type) {
	case string:
		return []ContentBlock{{Type: "text", Text: v}}
	case []any:
		var blocks []ContentBlock
		for _, item := range v {
			part, _ := item.(map[string]any)
			t, _ := part["type"].(string)
			switch t {
			case "text":
				blocks = append(blocks, ContentBlock{Type: "text", Text: fmt.Sprint(part["text"])})
			case "image_url":
				if url, ok := part["image_url"].(map[string]any); ok {
					src := url["url"].(string)
					block := ContentBlock{Type: "image", Source: &ImageSource{Type: "url", URL: src}}
					if strings.HasPrefix(src, "data:") {
						block.Source = &ImageSource{Type: "base64", MediaType: mediaTypeFromDataURL(src), Data: strings.SplitN(src, ",", 2)[1]}
					}
					blocks = append(blocks, block)
				}
			}
		}
		return blocks
	}
	return nil
}

// mediaTypeFromDataURL 从 data URL 提取 mime type。
func mediaTypeFromDataURL(url string) string {
	if i := strings.Index(url, ":"); i >= 0 {
		if j := strings.Index(url[i:], ";"); j >= 0 {
			return url[i+1 : i+j]
		}
		return url[i+1:]
	}
	return ""
}

// ===== 响应：anthropic → openai =====

// anthropicResponseToOpenAI 将 Anthropic 响应体转换为 OpenAI 规范响应。
func anthropicResponseToOpenAI(body []byte) (*ChatCompletionResponse, error) {
	var resp MessagesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析 anthropic 响应失败: %w", err)
	}

	msg := ChatMessage{Role: "assistant"}
	var textParts []string
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "thinking":
			// 思考内容映射为 reasoning_content 扩展字段，与流式入向
			// （anthropicToOpenAIStream 的 thinking_delta）行为一致——此前非流式漏写，
			// 上游返回的思考块被整块丢弃（见 docs/research/2026-09-29-capability-matrix-design.md
			// §1.2 响应入向行）。优先从 signature 的桥载荷还原逐字节原文，非桥签名
			// （上游原生签名、旧常量 "sig"）退 thinking 明文——与请求入向行为一致。
			if rc, ok := DecodeReasoningSignature(block.Signature); ok {
				msg.ReasoningContent += rc
			} else {
				msg.ReasoningContent += block.Thinking
			}
		case "redacted_thinking":
			// 与流式入向对齐：anthropicToOpenAIStream 对 redacted_thinking 事件整体
			// 忽略（无对应 case），故非流式也只在密文恰为本网关桥载荷时还原，其余
			// 跳过——不注入占位（占位是请求出向喂严格 thinking 上游的手段，见本文件
			// 请求入向），保证同协议内流式 / 非流式一致。
			if rc, ok := DecodeReasoningSignature(block.Data); ok {
				msg.ReasoningContent += rc
			}
		case "tool_use":
			args := "{}"
			if block.Input != nil {
				if b, err := json.Marshal(block.Input); err == nil {
					args = string(b)
				}
			}
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID:       block.ID,
				Type:     "function",
				Function: FunctionCall{Name: block.Name, Arguments: args},
			})
		}
	}
	if len(textParts) > 0 {
		msg.Content = strings.Join(textParts, "")
	}

	out := &ChatCompletionResponse{
		ID:     resp.ID,
		Object: "chat.completion",
		Model:  resp.Model,
		Choices: []ChatChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: anthropicStopToOpenAI(resp.StopReason),
		}},
	}
	if resp.Usage != nil {
		u := usageFromAnthropic(*resp.Usage)
		out.Usage = &u
	}
	return out, nil
}

// anthropicStopToOpenAI 映射 stop_reason → finish_reason。
func anthropicStopToOpenAI(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "stop_sequence":
		return "stop"
	}
	return ""
}

// ===== 响应：openai → anthropic =====

// anthropicResponseFromOpenAI 将 OpenAI 规范响应转为 Anthropic 响应体。
func anthropicResponseFromOpenAI(resp *ChatCompletionResponse) ([]byte, error) {
	out := MessagesResponse{
		ID:    resp.ID,
		Type:  "message",
		Role:  "assistant",
		Model: resp.Model,
	}
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		// reasoning_content（DeepSeek 等 thinking 模式的思考内容）先于 text 块映射为
		// thinking 块，客户端下一轮才能把它原样回传；否则上游 400
		// "reasoning_content must be passed back"。
		if rc := choice.Message.ReasoningContent; rc != "" {
			out.Content = append(out.Content, ContentBlock{
				Type:     "thinking",
				Thinking: rc,
				// 签名承载原始 reasoning_content 的桥载荷：客户端下一轮回传时
				// 逐字节还原，跨轮前缀稳定（见 reasoning_bridge.go）。
				Signature: EncodeReasoningSignature(rc),
			})
		}
		// 托管搜索接地：在 text 之前补 server_tool_use + web_search_tool_result
		// 两个块，客户端才能看到搜索过程并把结果回传。idSeq 取当前块位置，
		// 同一输入必产同一 id（确定性，前缀稳定）。
		if blocks, ok := webSearchToolBlocks(choice.Message.GroundingMetadata, len(out.Content)); ok {
			out.Content = append(out.Content, blocks...)
		}
		if text := chatContentToText(choice.Message.Content); text != "" {
			out.Content = append(out.Content, ContentBlock{Type: "text", Text: text})
		}
		for _, tc := range choice.Message.ToolCalls {
			out.Content = append(out.Content, ContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: rawJSONOrEmptyObject(tc.Function.Arguments),
			})
		}
		out.StopReason = openAIFinishToAnthropic(choice.FinishReason)
	}
	if resp.Usage != nil {
		out.Usage = toAnthropicUsage(resp.Usage)
	}
	return json.Marshal(out)
}

// webSearchToolBlocks 依据 canonical 接地数据合成 server_tool_use +
// web_search_tool_result 两个 Anthropic 块（非流式与流式共用，保证两处形状一致）。
// idSeq 是本次响应内的块序号，决定 id："srvtoolu_gateway_<seq>"——必须是输入的
// 确定性函数，同一输入产同一字节，否则客户端跨轮缓存前缀漂移（项目铁律，见
// prefix_stability_test.go）。无接地数据时返回 ok=false。
func webSearchToolBlocks(gm *GroundingMetadata, idSeq int) ([]ContentBlock, bool) {
	if gm == nil || (len(gm.SearchQueries) == 0 && len(gm.Sources) == 0) {
		return nil, false
	}
	id := "srvtoolu_gateway_" + strconv.Itoa(idSeq)
	query := ""
	if len(gm.SearchQueries) > 0 {
		query = gm.SearchQueries[0]
	}
	results := make([]any, 0, len(gm.Sources))
	for _, s := range gm.Sources {
		results = append(results, map[string]any{
			"type":  "web_search_result",
			"url":   s.URI,
			"title": s.Title,
		})
	}
	return []ContentBlock{
		{Type: "server_tool_use", ID: id, Name: "web_search", Input: map[string]any{"query": query}},
		{Type: "web_search_tool_result", ToolUseID: id, Content: results},
	}, true
}

// openAIFinishToAnthropic 映射 finish_reason → stop_reason。
func openAIFinishToAnthropic(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	}
	return "end_turn"
}

// rawJSONOrEmptyObject 返回参数 JSON 原始字节，空串兜底为 "{}"。
func rawJSONOrEmptyObject(s string) json.RawMessage {
	if s == "" {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(s)
}

// intPtr 返回 int 指针。
func intPtr(v int) *int { return &v }
