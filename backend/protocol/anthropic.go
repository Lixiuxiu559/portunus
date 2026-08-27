package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 本文件定义 Anthropic Messages API 的结构，以及 anthropic ↔ openai 的请求 / 响应 / 流式转换。

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

// ThinkingConfig 是 Anthropic 思考配置。
type ThinkingConfig struct {
	Type         string `json:"type"` // enabled
	BudgetTokens int    `json:"budget_tokens"`
}

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

// MessagesStreamEvent 是 Anthropic 流式事件（message_start / content_block_* / message_delta / message_stop）。
type MessagesStreamEvent struct {
	Type         string            `json:"type"`
	Index        *int              `json:"index,omitempty"` // 仅 content_block_* 事件携带，含 0，故用指针避免 omitempty 吞掉 0
	Message      *MessagesResponse `json:"message,omitempty"`       // message_start
	ContentBlock *ContentBlock     `json:"content_block,omitempty"` // content_block_start
	Delta        *StreamDelta      `json:"delta,omitempty"`         // content_block_delta / message_delta
	Usage        *AnthropicUsage   `json:"usage,omitempty"`         // message_delta
}

// StreamDelta 统一承载 content_block_delta 与 message_delta 的 delta 字段。
type StreamDelta struct {
	Type         string  `json:"type,omitempty"` // text_delta / input_json_delta / thinking_delta / signature_delta
	Text         string  `json:"text,omitempty"`
	PartialJSON  string  `json:"partial_json,omitempty"`
	Thinking     string  `json:"thinking,omitempty"`
	Signature    string  `json:"signature,omitempty"`
	StopReason   string  `json:"stop_reason,omitempty"`
	StopSequence *string `json:"stop_sequence,omitempty"`
}

// MarshalJSON 按事件类型区分两种形态：
//   - message_delta（StopReason 非空）：输出 stop_reason + stop_sequence（nil 时为 null）
//   - content_block_delta（Type 非空）：输出 type + 对应增量字段（text/partial_json/thinking/signature）
func (d StreamDelta) MarshalJSON() ([]byte, error) {
	// message_delta：StopReason / StopSequence 是必填键，stop_sequence 未知时为 null
	if d.StopReason != "" {
		m := map[string]any{"stop_reason": d.StopReason}
		if d.StopSequence != nil {
			m["stop_sequence"] = *d.StopSequence
		} else {
			m["stop_sequence"] = nil
		}
		return json.Marshal(m)
	}

	// content_block_delta：按 delta.type 裁字段
	m := map[string]any{"type": d.Type}
	switch d.Type {
	case "text_delta":
		m["text"] = d.Text
	case "input_json_delta":
		m["partial_json"] = d.PartialJSON
	case "thinking_delta":
		m["thinking"] = d.Thinking
	case "signature_delta":
		m["signature"] = d.Signature
	}
	return json.Marshal(m)
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
		out.ToolChoice = req.ToolChoice
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
	var out ChatMessage
	out.Role = m.Role
	var textParts []string

	for _, block := range m.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "thinking", "redacted_thinking":
			// 思考块不传给 OpenAI 上游（上游不认思考内容作为输入），直接丢弃。
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
			toolResults = append(toolResults, ChatMessage{
				Role:       "tool",
				Content:    anthropicToolResultToContent(block.Content),
				ToolCallID: block.ToolUseID,
				Name:       toolNames[block.ToolUseID],
			})
		}
	}

	if len(toolResults) > 0 {
		return toolResults
	}
	// 纯 thinking 等无内容块的消息（既无 text 也无 tool_calls）整条丢弃，
	// 避免生成空的 assistant 消息导致上游 400。
	if len(textParts) == 0 && len(out.ToolCalls) == 0 {
		return nil
	}
	if len(textParts) > 0 {
		out.Content = strings.Join(textParts, "")
	}
	return []ChatMessage{out}
}

// anthropicToolResultToContent 把 tool_result 的 content 转为 OpenAI tool 消息 content。
func anthropicToolResultToContent(c any) any {
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
		out.ToolChoice = req.ToolChoice
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
		out.Usage = &AnthropicUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		}
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

// indexOrZero 解引用 *int，nil 兜底为 0。
func indexOrZero(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// ===== 流式：anthropic → openai =====

// anthropicToOpenAIStream 把 Anthropic 流事件逐条映射为 OpenAI chunk。
type anthropicToOpenAIStream struct {
	st openAIStreamState
}

func newAnthropicToOpenAIStream() *anthropicToOpenAIStream {
	return &anthropicToOpenAIStream{}
}

func (a *anthropicToOpenAIStream) Convert(payload []byte) ([][]byte, error) {
	var ev MessagesStreamEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return nil, fmt.Errorf("解析 anthropic 流事件失败: %w", err)
	}
	var out [][]byte
	switch ev.Type {
	case "message_start":
		if ev.Message != nil {
			a.st.setMeta(ev.Message.ID, ev.Message.Model)
			if ev.Message.Usage != nil {
				a.st.usage = &Usage{
					PromptTokens:     ev.Message.Usage.InputTokens - ev.Message.Usage.CacheCreationInputTokens - ev.Message.Usage.CacheReadInputTokens,
					CacheReadTokens:  ev.Message.Usage.CacheReadInputTokens,
					CacheWriteTokens: ev.Message.Usage.CacheCreationInputTokens,
				}
			}
			if b := a.st.emitRole("assistant"); b != nil {
				out = append(out, b)
			}
		}
	case "content_block_start":
		if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
			out = append(out, a.st.emitChunk(ChunkDelta{ToolCalls: []ChunkToolCall{{
				Index:    indexOrZero(ev.Index),
				ID:       ev.ContentBlock.ID,
				Type:     "function",
				Function: ChunkFunctionCall{Name: ev.ContentBlock.Name},
			}}}, ""))
		}
	case "content_block_delta":
		if ev.Delta == nil {
			break
		}
		switch ev.Delta.Type {
		case "text_delta":
			a.st.text.WriteString(ev.Delta.Text)
			out = append(out, a.st.emitChunk(ChunkDelta{Content: ev.Delta.Text}, ""))
		case "thinking_delta":
			out = append(out, a.st.emitChunk(ChunkDelta{ReasoningContent: ev.Delta.Thinking}, ""))
		case "input_json_delta":
			out = append(out, a.st.emitChunk(ChunkDelta{ToolCalls: []ChunkToolCall{{
				Index:    indexOrZero(ev.Index),
				Function: ChunkFunctionCall{Arguments: ev.Delta.PartialJSON},
			}}}, ""))
		}
	case "message_delta":
		if ev.Delta != nil {
			a.st.finish = anthropicStopToOpenAI(ev.Delta.StopReason)
		}
		if ev.Usage != nil {
			if a.st.usage == nil {
				a.st.usage = &Usage{}
			}
			a.st.usage.CompletionTokens = ev.Usage.OutputTokens
			a.st.usage.TotalTokens = a.st.usage.PromptTokens + a.st.usage.CompletionTokens + a.st.usage.CacheReadTokens + a.st.usage.CacheWriteTokens
		}
	case "message_stop":
		// no-op，收尾由 Finish 统一处理
	}
	return out, nil
}

func (a *anthropicToOpenAIStream) Finish() ([][]byte, error) {
	if b := a.st.emitFinal(); b != nil {
		return [][]byte{b}, nil
	}
	return nil, nil
}

func (a *anthropicToOpenAIStream) Usage() *Usage { return a.st.usage }

// ===== 流式：openai → anthropic =====

// openAIToAnthropicStream 把 OpenAI chunk 逐条映射为 Anthropic 流事件。
type openAIToAnthropicStream struct {
	id        string
	model     string
	started   bool
	blockOpen bool
	blockType string
	blockIdx  int
	toolID    string
	toolName  string
	finish    string
	usage     *AnthropicUsage
}

func newOpenAIToAnthropicStream() *openAIToAnthropicStream {
	return &openAIToAnthropicStream{blockIdx: -1}
}

// ev 构造一个 Anthropic 流事件并序列化。
func (o *openAIToAnthropicStream) ev(typ string, msg *MessagesResponse, block *ContentBlock, delta *StreamDelta) []byte {
	event := MessagesStreamEvent{Type: typ, Message: msg, ContentBlock: block, Delta: delta}
	switch typ {
	case "content_block_start", "content_block_delta", "content_block_stop":
		event.Index = intPtr(o.blockIdx)
	case "message_delta":
		event.Usage = o.usage
	}
	b, _ := json.Marshal(event)
	return b
}

// closeBlock 若当前 block 打开则发出 content_block_stop。
func (o *openAIToAnthropicStream) closeBlock(out *[][]byte) {
	if o.blockOpen {
		*out = append(*out, o.ev("content_block_stop", nil, nil, nil))
		o.blockOpen = false
	}
}

func (o *openAIToAnthropicStream) Convert(payload []byte) ([][]byte, error) {
	var chunk ChatCompletionChunk
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return nil, fmt.Errorf("解析 openai chunk 失败: %w", err)
	}
	o.id = firstNonEmpty(o.id, chunk.ID)
	o.model = firstNonEmpty(o.model, chunk.Model)
	if chunk.Usage != nil {
		o.usage = &AnthropicUsage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens}
	}

	var out [][]byte
	if len(chunk.Choices) == 0 {
		return out, nil
	}
	delta := chunk.Choices[0].Delta
	finish := chunk.Choices[0].FinishReason

	if !o.started {
		o.started = true
		// message 的 usage 是必填对象（不能为 null），首 chunk 尚未拿到上游 usage 时兜底空对象。
		if o.usage == nil {
			o.usage = &AnthropicUsage{}
		}
		out = append(out, o.ev("message_start", &MessagesResponse{
			ID:      o.id,
			Type:    "message",
			Role:    "assistant",
			Model:   o.model,
			Content: []ContentBlock{},
			Usage:   o.usage,
		}, nil, nil))
	}
	if delta.Role != "" {
		return out, nil
	}

	if len(delta.ToolCalls) > 0 {
		tc := delta.ToolCalls[0]
		if !o.blockOpen || o.blockType != "tool_use" {
			o.closeBlock(&out)
			o.blockIdx++
			o.blockOpen = true
			o.blockType = "tool_use"
			o.toolID = tc.ID
			o.toolName = tc.Function.Name
			out = append(out, o.ev("content_block_start", nil, &ContentBlock{Type: "tool_use", ID: o.toolID, Name: o.toolName}, nil))
		}
		if tc.Function.Arguments != "" {
			out = append(out, o.ev("content_block_delta", nil, nil, &StreamDelta{Type: "input_json_delta", PartialJSON: tc.Function.Arguments}))
		}
		return out, nil
	}

	// DeepSeek 等兼容方的 reasoning_content 扩展字段 → Anthropic thinking 块。
	// 官方 OpenAI 没有该字段；有此字段说明上游是兼容方，思考内容应显式转为 thinking_delta。
	if delta.ReasoningContent != "" {
		if !o.blockOpen || o.blockType != "thinking" {
			o.closeBlock(&out)
			o.blockIdx++
			o.blockOpen = true
			o.blockType = "thinking"
			out = append(out, o.ev("content_block_start", nil, &ContentBlock{Type: "thinking"}, nil))
		}
		out = append(out, o.ev("content_block_delta", nil, nil, &StreamDelta{Type: "thinking_delta", Thinking: delta.ReasoningContent}))
		return out, nil
	}

	if delta.Content != "" {
		if !o.blockOpen || o.blockType != "text" {
			o.closeBlock(&out)
			o.blockIdx++
			o.blockOpen = true
			o.blockType = "text"
			out = append(out, o.ev("content_block_start", nil, &ContentBlock{Type: "text", Text: ""}, nil))
		}
		out = append(out, o.ev("content_block_delta", nil, nil, &StreamDelta{Type: "text_delta", Text: delta.Content}))
	}

	if finish != "" {
		o.finish = openAIFinishToAnthropic(finish)
	}
	return out, nil
}

func (o *openAIToAnthropicStream) Finish() ([][]byte, error) {
	var out [][]byte
	if o.blockOpen {
		out = append(out, o.ev("content_block_stop", nil, nil, nil))
		o.blockOpen = false
	}
	if o.started {
		// stop_reason 上游没给时兜底 end_turn；usage 未累积时兜底空对象，
		// 保证 message_delta 的 delta.stop_reason 与 usage 两个必填键始终存在。
		if o.finish == "" {
			o.finish = "end_turn"
		}
		if o.usage == nil {
			o.usage = &AnthropicUsage{}
		}
		out = append(out, o.ev("message_delta", nil, nil, &StreamDelta{StopReason: o.finish}))
		out = append(out, o.ev("message_stop", nil, nil, nil))
	}
	return out, nil
}

func (o *openAIToAnthropicStream) Usage() *Usage { return nil }
