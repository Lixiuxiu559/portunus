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
	Index        int               `json:"index,omitempty"`
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
	StopReason   string  `json:"stop_reason,omitempty"`
	StopSequence *string `json:"stop_sequence,omitempty"`
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

	// messages → chat messages（一条 anthropic 消息可能拆成多条 OpenAI 消息）
	for _, m := range req.Messages {
		out.Messages = append(out.Messages, anthropicMessagesToChat(m)...)
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
func anthropicMessagesToChat(m AnthropicMessage) []ChatMessage {
	var toolResults []ChatMessage
	var out ChatMessage
	out.Role = m.Role
	var textParts []string

	for _, block := range m.Content {
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
			})
		}
	}

	if len(toolResults) > 0 {
		return toolResults
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
				Index:    ev.Index,
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
				Index:    ev.Index,
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
		event.Index = o.blockIdx
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
		out = append(out, o.ev("message_delta", nil, nil, &StreamDelta{StopReason: o.finish}))
		out = append(out, o.ev("message_stop", nil, nil, nil))
	}
	return out, nil
}

func (o *openAIToAnthropicStream) Usage() *Usage { return nil }
