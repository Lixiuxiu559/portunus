package protocol

import (
	"encoding/json"
	"fmt"
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
	Type      string       `json:"type"` // text / image / tool_use / tool_result / thinking
	Text      string       `json:"text,omitempty"`
	ID        string       `json:"id,omitempty"`
	Name      string       `json:"name,omitempty"`        // tool_use
	Input     any          `json:"input,omitempty"`       // tool_use 参数（对象）
	ToolUseID string       `json:"tool_use_id,omitempty"` // tool_result
	Content   any          `json:"content,omitempty"`     // tool_result 结果（字符串或块数组）
	Source    *ImageSource `json:"source,omitempty"`      // image
	Thinking  string       `json:"thinking,omitempty"`    // thinking
	Signature string       `json:"signature,omitempty"`   // thinking 块的签名
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
type AnthropicTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema"`
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
	// （部分上游如 DeepSeek 要求 tool 消息必须带 name）。
	toolNameByID := map[string]string{}
	for _, m := range req.Messages {
		if m.Role != "assistant" {
			continue
		}
		for _, block := range m.Content {
			if block.Type == "tool_use" {
				toolNameByID[block.ID] = block.Name
			}
		}
	}

	// messages → chat messages（一条 anthropic 消息可能拆成多条 OpenAI 消息）
	for _, m := range req.Messages {
		out.Messages = append(out.Messages, anthropicMessagesToChat(m, toolNameByID)...)
	}

	// tools → function tools
	for _, t := range req.Tools {
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
func anthropicMessagesToChat(m AnthropicMessage, toolNames map[string]string) []ChatMessage {
	var toolResults []ChatMessage
	var toolImages []any // tool_result 里的图片，转出后拼进紧随的 user 消息
	var out ChatMessage
	out.Role = m.Role
	var textParts []string
	// 依原始顺序收集 text / image 内容块：无图时 content 退化为纯文本字符串
	// （多数 OpenAI 兼容上游对字符串形态最稳），有图才升级为内容块数组。
	var contentParts []any
	hasImage := false

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
		case "thinking", "redacted_thinking":
			// 思考内容映射为 OpenAI 的 reasoning_content 扩展字段。
			// DeepSeek 等兼容方在 thinking 模式下要求多轮对话把上一轮思考内容
			// 原样回传（reasoning_content），直接丢弃会导致上游 400：
			// "The reasoning_content in the thinking mode must be passed back to the API"。
			out.ReasoningContent += block.Thinking
		case "tool_use":
			args := "{}"
			if block.Input != nil {
				if b, err := json.Marshal(block.Input); err == nil {
					args = string(b)
				}
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:       block.ID,
				Type:     "function",
				Function: FunctionCall{Name: block.Name, Arguments: args},
			})
		case "tool_result":
			name := toolNames[block.ToolUseID]
			toolResults = append(toolResults, ChatMessage{
				Role:       "tool",
				Content:    anthropicToolResultText(block.Content),
				ToolCallID: block.ToolUseID,
				Name:       &name,
			})
			// 图片不进 tool 消息：OpenAI 规范 tool content 仅允许文本，
			// DeepSeek 等严格兼容上游对数组 content 直接 400。挪到紧随其后的
			// user 消息（见下方 toolResults 收尾），视觉上游仍能看到图。
			toolImages = append(toolImages, anthropicToolResultImageParts(block.Content)...)
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
		return toolResults
	}
	// 既无 text、无图、无 tool_calls、也无思考内容的消息整条丢弃，
	// 避免生成空的 assistant 消息导致上游 400。
	if len(textParts) == 0 && !hasImage && len(out.ToolCalls) == 0 && out.ReasoningContent == "" {
		return nil
	}
	if hasImage {
		out.Content = contentParts
	} else if len(textParts) > 0 {
		out.Content = strings.Join(textParts, "")
	}
	return []ChatMessage{out}
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
		out.Usage = &Usage{
			PromptTokens:     resp.Usage.InputTokens - resp.Usage.CacheCreationInputTokens - resp.Usage.CacheReadInputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
			CacheReadTokens:  resp.Usage.CacheReadInputTokens,
			CacheWriteTokens: resp.Usage.CacheCreationInputTokens,
		}
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
			out.Content = append(out.Content, ContentBlock{Type: "thinking", Thinking: rc, Signature: "sig"})
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

// toAnthropicUsage 把统一 Usage 归一为 Anthropic usage。
// PromptTokens 已是非缓存输入（见 Usage.UnmarshalJSON），直接作为 input_tokens；
// 缓存读写单独映射到 cache_read/cache_creation_input_tokens。
func toAnthropicUsage(u *Usage) *AnthropicUsage {
	if u == nil {
		return nil
	}
	return &AnthropicUsage{
		InputTokens:              u.PromptTokens,
		OutputTokens:             u.CompletionTokens,
		CacheReadInputTokens:     u.CacheReadTokens,
		CacheCreationInputTokens: u.CacheWriteTokens,
	}
}
