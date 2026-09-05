package protocol

import (
	"encoding/json"
	"fmt"
	"sort"
)

// 本文件定义 Anthropic 流式事件的结构，以及 anthropic ↔ openai 的流式转换状态机。
// 非流式的请求 / 响应转换与结构体定义在 anthropic.go。

// MessagesStreamEvent 是 Anthropic 流式事件（message_start / content_block_* / message_delta / message_stop）。
type MessagesStreamEvent struct {
	Type         string            `json:"type"`
	Index        *int              `json:"index,omitempty"`         // 仅 content_block_* 事件携带，含 0，故用指针避免 omitempty 吞掉 0
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

func newAnthropicToOpenAIStream() StreamConverter {
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
				u := usageFromAnthropic(*ev.Message.Usage)
				a.st.usage = &u
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
//
// Anthropic 流式状态机里，多个 content block 可交错 open（按 index 关联），
// 尤其多工具并行时 OpenAI 会同时流式多个 tool_calls（各带独立的 index），
// 每个都需映射为一个独立的 tool_use block。这里维护两块状态：
//   - 单块（text / thinking）：同一时刻至多一个 open
//   - 并行 tool_use 块：openai tool_calls index → anthropic content block index，
//     block index 按 open 顺序从 nextIdx 顺序分配，保证 Anthropic 侧 index 连续无空洞
type openAIToAnthropicStream struct {
	id      string
	model   string
	started bool

	// 单块状态（text / thinking）
	blockOpen bool
	blockType string // "text" | "thinking"
	blockIdx  int

	// 并行 tool_use 块状态：只记录实际 open 的 offset，
	// close 时只对 open 过的块发 stop，避免对从未 start 的 index 发孤儿 stop。
	toolOpen map[int]int // openai tool_calls index → anthropic content block index

	nextIdx int // 下一个可分配的 anthropic content block index
	finish  string
	usage   *AnthropicUsage
	// estimate 是请求输入的预估 token 数；message_start 时上游尚未返回 usage，
	// 用它填充 input_tokens，让客户端能看到上下文占用。
	estimate int
}

func newOpenAIToAnthropicStream() StreamConverter {
	return &openAIToAnthropicStream{}
}

// SetEstimateInputTokens 设置请求输入的预估 token 数（供 message_start 填充）。
func (o *openAIToAnthropicStream) SetEstimateInputTokens(n int) {
	o.estimate = n
}

// evBlock 构造一个 content_block_* 事件（显式指定 index）并序列化。
func (o *openAIToAnthropicStream) evBlock(typ string, idx int, block *ContentBlock, delta *StreamDelta) []byte {
	event := MessagesStreamEvent{Type: typ, Index: intPtr(idx), ContentBlock: block, Delta: delta}
	b, _ := json.Marshal(event)
	return b
}

// evMessage 构造一个非 content_block 的事件（message_start / message_delta / message_stop）。
func (o *openAIToAnthropicStream) evMessage(typ string, msg *MessagesResponse, delta *StreamDelta) []byte {
	event := MessagesStreamEvent{Type: typ, Message: msg, Delta: delta}
	if typ == "message_delta" {
		event.Usage = o.usage
	}
	b, _ := json.Marshal(event)
	return b
}

// closeSingleBlock 若 text / thinking 单块打开，发出对应的 content_block_stop。
// thinking 块收尾前必须补发 signature_delta：Anthropic 扩展思考的 thinking 块要带签名
// 才能被客户端在下一轮原样回传（多轮回传才会带上 reasoning_content，否则 DeepSeek 等
// thinking 模式上游报 400 "reasoning_content must be passed back"）。签名值上游
// （DeepSeek 等）不提供，发非空占位符——部分客户端校验签名非空才肯在下一轮回传思考块，
func (o *openAIToAnthropicStream) closeSingleBlock(out *[][]byte) {
	if !o.blockOpen {
		return
	}
	if o.blockType == "thinking" {
		*out = append(*out, o.evBlock("content_block_delta", o.blockIdx, nil, &StreamDelta{
			Type:      "signature_delta",
			Signature: "sig",
		}))
	}
	*out = append(*out, o.evBlock("content_block_stop", o.blockIdx, nil, nil))
	o.blockOpen = false
}

// closeToolBlocks 关闭所有实际 open 的并行 tool_use 块（只对 open 过的发 stop），
// 并按 block index 排序保证事件顺序稳定。关闭后清空状态。
func (o *openAIToAnthropicStream) closeToolBlocks(out *[][]byte) {
	if len(o.toolOpen) == 0 {
		return
	}
	blockIdxs := make([]int, 0, len(o.toolOpen))
	for _, blockIdx := range o.toolOpen {
		blockIdxs = append(blockIdxs, blockIdx)
	}
	sort.Ints(blockIdxs)
	for _, blockIdx := range blockIdxs {
		*out = append(*out, o.evBlock("content_block_stop", blockIdx, nil, nil))
	}
	o.toolOpen = nil
}

// handleTextBlock 处理 text 或 thinking 增量。类型切换时关闭之前的块。
func (o *openAIToAnthropicStream) handleTextBlock(typ, text string) [][]byte {
	var out [][]byte
	if !o.blockOpen || o.blockType != typ {
		o.closeSingleBlock(&out)
		o.closeToolBlocks(&out) // 从 tool 切回 text/thinking，关掉所有 tool 块
		o.blockIdx = o.nextIdx
		o.nextIdx++
		o.blockOpen = true
		o.blockType = typ
		blk := &ContentBlock{Type: typ}
		if typ == "text" {
			// text 块必须含空 "text" 字段，否则客户端拿不到字符串字段（与 content_block_start 同病根）。
			blk.Text = ""
		}
		out = append(out, o.evBlock("content_block_start", o.blockIdx, blk, nil))
	}
	delta := &StreamDelta{Type: "text_delta", Text: text}
	if typ == "thinking" {
		delta = &StreamDelta{Type: "thinking_delta", Thinking: text}
	}
	out = append(out, o.evBlock("content_block_delta", o.blockIdx, nil, delta))
	return out
}

// handleToolCalls 处理并行 tool_calls。每个 OpenAI tool_call 的 index 首次出现时
// 分配一个独立的 anthropic block index 并发 content_block_start（携带当前帧的
// id / name，即使为空也保证后续 input_json_delta 有对应的 start）；
// 其后 arguments 增量发 input_json_delta。
func (o *openAIToAnthropicStream) handleToolCalls(toolCalls []ChunkToolCall) [][]byte {
	var out [][]byte
	o.closeSingleBlock(&out) // 从 text/thinking 切到 tool，关闭单块
	if o.toolOpen == nil {
		o.toolOpen = map[int]int{}
	}
	for _, tc := range toolCalls {
		offset := tc.Index
		if offset < 0 {
			offset = 0
		}
		blockIdx, open := o.toolOpen[offset]
		if !open {
			blockIdx = o.nextIdx
			o.nextIdx++
			o.toolOpen[offset] = blockIdx
			out = append(out, o.evBlock("content_block_start", blockIdx, &ContentBlock{
				Type: "tool_use",
				ID:   tc.ID,
				Name: tc.Function.Name,
			}, nil))
		}
		if tc.Function.Arguments != "" {
			out = append(out, o.evBlock("content_block_delta", blockIdx, nil, &StreamDelta{
				Type:        "input_json_delta",
				PartialJSON: tc.Function.Arguments,
			}))
		}
	}
	return out
}

func (o *openAIToAnthropicStream) Convert(payload []byte) ([][]byte, error) {
	var chunk ChatCompletionChunk
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return nil, fmt.Errorf("解析 openai chunk 失败: %w", err)
	}
	o.id = firstNonEmpty(o.id, chunk.ID)
	o.model = firstNonEmpty(o.model, chunk.Model)
	if chunk.Usage != nil {
		o.usage = toAnthropicUsage(chunk.Usage)
	}

	var out [][]byte
	if len(chunk.Choices) == 0 {
		return out, nil
	}
	delta := chunk.Choices[0].Delta
	finish := chunk.Choices[0].FinishReason

	if !o.started {
		o.started = true
		// message 的 usage 是必填对象（不能为 null），首 chunk 尚未拿到上游 usage 时，
		// 用预估 input_tokens 兜底（若无预估则全 0），保证客户端能看到上下文占用。
		if o.usage == nil {
			o.usage = &AnthropicUsage{InputTokens: o.estimate}
		}
		out = append(out, o.evMessage("message_start", &MessagesResponse{
			ID:      o.id,
			Type:    "message",
			Role:    "assistant",
			Model:   o.model,
			Content: []ContentBlock{},
			Usage:   o.usage,
		}, nil))
	}
	if delta.Role != "" && len(delta.ToolCalls) == 0 && delta.ReasoningContent == "" && delta.Content == "" {
		// 纯 role 标记 chunk（OpenAI 首个 chunk 仅声明 assistant 角色），无可转换内容，忽略。
		// 不能直接按 role 非空 return：官方 OpenAI 的工具开场块 role 与 tool_calls 同帧，
		// 整帧被吞后后续参数帧拿不到 id/name，tool_use 块发空 id 导致客户端 Tool use interrupted。
		return out, nil
	}

	if len(delta.ToolCalls) > 0 {
		out = append(out, o.handleToolCalls(delta.ToolCalls)...)
		return out, nil
	}

	// DeepSeek 等兼容方的 reasoning_content 扩展字段 → Anthropic thinking 块。
	// 官方 OpenAI 没有该字段；有此字段说明上游是兼容方，思考内容应显式转为 thinking_delta。
	if delta.ReasoningContent != "" {
		out = append(out, o.handleTextBlock("thinking", delta.ReasoningContent)...)
		return out, nil
	}

	if delta.Content != "" {
		out = append(out, o.handleTextBlock("text", delta.Content)...)
	}

	if finish != "" {
		o.finish = openAIFinishToAnthropic(finish)
	}
	return out, nil
}

func (o *openAIToAnthropicStream) Finish() ([][]byte, error) {
	var out [][]byte
	o.closeSingleBlock(&out)
	o.closeToolBlocks(&out)
	if o.started {
		// stop_reason 上游没给时兜底 end_turn；usage 未累积时兜底空对象，
		// 保证 message_delta 的 delta.stop_reason 与 usage 两个必填键始终存在。
		if o.finish == "" {
			o.finish = "end_turn"
		}
		if o.usage == nil {
			o.usage = &AnthropicUsage{}
		}
		out = append(out, o.evMessage("message_delta", nil, &StreamDelta{StopReason: o.finish}))
		out = append(out, o.evMessage("message_stop", nil, nil))
	}
	return out, nil
}

func (o *openAIToAnthropicStream) Usage() *Usage { return nil }
