package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
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
				u := *ev.Message.Usage
				a.st.rawUsage = &u
				x := usageFromAnthropic(u)
				a.st.usage = &x
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
			// 持有原始用量、经 usageFromAnthropic 重算——Total 数学不在此手写
			if a.st.rawUsage == nil {
				a.st.rawUsage = &AnthropicUsage{}
			}
			a.st.rawUsage.OutputTokens = ev.Usage.OutputTokens
			x := usageFromAnthropic(*a.st.rawUsage)
			a.st.usage = &x
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

// anthropicBlockSink 把块生命周期回调映射为 Anthropic 流事件（由
// fromOpenAISkeleton 驱动，骨架见 from_openai.go）。
//
// Anthropic 流式里多个 content block 可交错 open（按 index 关联），尤其多工具
// 并行时 OpenAI 会同时流式多个 tool_calls（各带独立的 index），每个都需映射为
// 一个独立的 tool_use block。anthropic content block index 按块首次出现顺序
// 从 0 顺序分配，保证连续无空洞。
type anthropicBlockSink struct {
	started bool

	// 单块状态（text / thinking）
	blockOpen bool
	blockType string // "text" | "thinking"，TextEnd 据此决定是否补签名
	blockIdx  int
	// thinkingText 累积本块思考明文。桥签名要用**整块**内容生成
	// （见 reasoning_bridge.go），而 signature_delta 只能在块收尾时发一次，
	// 因此思考增量不能边流边丢。
	thinkingText strings.Builder

	// 并行 tool_use 块：openai tool_calls index → anthropic content block index
	toolIdx map[int]int

	// grounding 是首个 text 块前要补发的托管搜索接地数据（canonical chunk 带来，
	// 见 SetGrounding）；groundingDone 保证只补发一次。它必须在 text 之前落地，
	// 客户端才会把「先搜索、后回答」的顺序编排对。
	grounding     *GroundingMetadata
	groundingDone bool

	nextIdx int
	usage   *AnthropicUsage

	// estimateFn 是请求输入 token 的惰性估算 thunk（见 WithInputEstimate）：
	// message_start 发出前调用一次（上游 usage 缺失时兜底 input_tokens）。
	estimateFn   func() int
	estimate     int
	estimateDone bool
}

// setInputEstimate 设置惰性输入估算 thunk。
func (a *anthropicBlockSink) setInputEstimate(fn func() int) { a.estimateFn = fn }

// estimatedInput 返回估算的输入 token：thunk 缺省按 0 兜底，只调用一次。
func (a *anthropicBlockSink) estimatedInput() int {
	if a.estimateFn == nil {
		return 0
	}
	if !a.estimateDone {
		a.estimate = a.estimateFn()
		a.estimateDone = true
	}
	return a.estimate
}

func newAnthropicFromOpenAI() StreamConverter {
	sink := &anthropicBlockSink{}
	// 同包直接构造骨架并内嵌，而非经 newFromOpenAISkeleton 工厂 + 类型断言：
	// 工厂返回 StreamConverter 接口，断言 *(fromOpenAISkeleton) 一旦工厂改返回
	// 其他实现即运行时 panic。直接构造免去断言，骨架的 Finish/Usage/
	// setInputEstimate 经内嵌自动提升，无需手工转发。
	return &anthropicFromOpenAI{
		fromOpenAISkeleton: &fromOpenAISkeleton{sink: sink},
		sink:               sink,
	}
}

// anthropicFromOpenAI 内嵌 fromOpenAISkeleton 并只 override Convert：在共享骨架之外
// 旁路解析 canonical chunk 的 GroundingMetadata——骨架的 blockSink 契约只覆盖块生命
// 周期，不透传该字段。选择在 anthropic 一侧包装（而非改 from_openai.go 的骨架）是
// 权衡后的最小侵入——接地是 anthropic 出向独有的旁路载荷，包一层既能拿到原始 chunk，
// 又不惊动 gemini / responses 两个骨架消费者。内嵌保证新增的可选接口（如
// setInputEstimate 这类鸭子类型能力）自动承接，不会因包装层静默失效。
type anthropicFromOpenAI struct {
	*fromOpenAISkeleton
	sink *anthropicBlockSink
}

func (c *anthropicFromOpenAI) Convert(payload []byte) ([][]byte, error) {
	// 旁路解析：骨架随后按块生命周期消费同一 payload，此处只负责把接地数据
	// 缓存进 sink，等首个 text 块打开时补发。
	var chunk ChatCompletionChunk
	if json.Unmarshal(payload, &chunk) == nil && len(chunk.Choices) > 0 {
		if gm := chunk.Choices[0].Delta.GroundingMetadata; gm != nil {
			c.sink.SetGrounding(gm)
		}
	}
	// 经嵌入字段名调用被 override 的骨架方法（直接 c.Convert 会递归）。
	return c.fromOpenAISkeleton.Convert(payload)
}

// evBlock 构造一个 content_block_* 事件（显式指定 index）并序列化。
func (a *anthropicBlockSink) evBlock(typ string, idx int, block *ContentBlock, delta *StreamDelta) []byte {
	event := MessagesStreamEvent{Type: typ, Index: intPtr(idx), ContentBlock: block, Delta: delta}
	b, _ := json.Marshal(event)
	return b
}

// evMessage 构造一个非 content_block 的事件（message_start / message_stop）。
func (a *anthropicBlockSink) evMessage(typ string, msg *MessagesResponse) []byte {
	b, _ := json.Marshal(MessagesStreamEvent{Type: typ, Message: msg})
	return b
}

// BeginMessage 发出 message_start。message 的 usage 是必填对象（不能为 null）：
// 首个 chunk 已携带上游真实 usage 时直接采用；否则用预估 input_tokens 兜底
// （若无预估则全 0），保证客户端能看到上下文占用。
func (a *anthropicBlockSink) BeginMessage(id, model string, u *Usage) [][]byte {
	a.started = true
	if u != nil {
		a.usage = toAnthropicUsage(u)
	}
	if a.usage == nil {
		a.usage = &AnthropicUsage{InputTokens: a.estimatedInput()}
	}
	return [][]byte{a.evMessage("message_start", &MessagesResponse{
		ID:      id,
		Type:    "message",
		Role:    "assistant",
		Model:   model,
		Content: []ContentBlock{},
		Usage:   a.usage,
	})}
}

// TextDelta / ReasoningDelta 打开（或续写）text / thinking 单块并发增量。
func (a *anthropicBlockSink) TextDelta(s string) [][]byte {
	return a.textBlock("text", s)
}

func (a *anthropicBlockSink) ReasoningDelta(s string) [][]byte {
	// 先开块（开新块会重置累积器），再把本段追加进累积器。
	out := a.textBlock("thinking", s)
	a.thinkingText.WriteString(s)
	return out
}

// SetGrounding 缓存 canonical chunk 携带的托管搜索接地数据（首个非 nil 者胜出：
// 同一响应里接地通常只在开场帧出现，取先到的即可确定性重建）。调用方是包装层
// anthropicFromOpenAI 的旁路解析。
func (a *anthropicBlockSink) SetGrounding(meta *GroundingMetadata) {
	if a.grounding == nil {
		a.grounding = meta
	}
}

// emitGrounding 补发托管搜索的两个块（server_tool_use / web_search_tool_result，
// 均为 start + stop，无 delta）。形状复用 webSearchToolBlocks，与非流式同源；
// 块序数取 nextIdx，保证同一 chunk 序列产出相同 id 与字节。
func (a *anthropicBlockSink) emitGrounding() [][]byte {
	if a.groundingDone || a.grounding == nil {
		return nil
	}
	a.groundingDone = true
	blocks, ok := webSearchToolBlocks(a.grounding, a.nextIdx)
	if !ok {
		return nil
	}
	var out [][]byte
	for _, blk := range blocks {
		block := blk
		out = append(out, a.evBlock("content_block_start", a.nextIdx, &block, nil))
		out = append(out, a.evBlock("content_block_stop", a.nextIdx, nil, nil))
		a.nextIdx++
	}
	return out
}

// textBlock 打开（或续写）单块。骨架保证类型切换前已回调 TextEnd，此处只管
// 「未打开则开块」。
func (a *anthropicBlockSink) textBlock(typ, text string) [][]byte {
	var out [][]byte
	// 托管搜索接地晚到的处理：Gemini 真实流的 grounding 通常随末 chunk（带
	// finishReason）到达，此时 text 块往往已开写——若只在开块时机补发，合成块
	// 会静默丢失。这里关掉当前 text 块、插入合成块、再重开续写：客户端按块序
	// 拼接 content，text → server_tool_use → web_search_tool_result → text 与
	// Anthropic 官方「先说话、后搜索、再作答」的块顺序同构。thinking 块不打断
	// （其后的 text 开块本就会先落地合成块）。
	if typ == "text" && a.blockOpen && a.blockType == "text" &&
		a.grounding != nil && !a.groundingDone {
		out = append(out, a.evBlock("content_block_stop", a.blockIdx, nil, nil))
		out = append(out, a.emitGrounding()...)
		a.blockOpen = false // 走下方开块分支重开 text 块
	}
	if !a.blockOpen {
		// 托管搜索接地在首个 text 块前落地（thinking 块之后、text 之前）。
		if typ == "text" {
			out = append(out, a.emitGrounding()...)
		}
		a.blockOpen = true
		a.blockType = typ
		a.blockIdx = a.nextIdx
		a.nextIdx++
		a.thinkingText.Reset() // 新块开始：清掉上一块的思考累积
		blk := &ContentBlock{Type: typ}
		if typ == "text" {
			// text 块必须含空 "text" 字段，否则客户端拿不到字符串字段。
			blk.Text = ""
		}
		out = append(out, a.evBlock("content_block_start", a.blockIdx, blk, nil))
	}
	delta := &StreamDelta{Type: "text_delta", Text: text}
	if typ == "thinking" {
		delta = &StreamDelta{Type: "thinking_delta", Thinking: text}
	}
	out = append(out, a.evBlock("content_block_delta", a.blockIdx, nil, delta))
	return out
}

// TextEnd 关闭当前单块。thinking 块收尾前必须补发 signature_delta：Anthropic
// 扩展思考的 thinking 块要带签名才能被客户端在下一轮原样回传（多轮回传才会
// 带上 reasoning_content，否则 DeepSeek 等 thinking 模式上游报 400
// "reasoning_content must be passed back"）。签名值上游（DeepSeek 等）不提供，
// 这里改发**承载整块思考内容的桥载荷**（见 reasoning_bridge.go）：客户端原样
// 回传时网关逐字节还原 reasoning_content，跨轮前缀稳定，上游前缀缓存才命中。
func (a *anthropicBlockSink) TextEnd() [][]byte {
	if !a.blockOpen {
		return nil
	}
	var out [][]byte
	if a.blockType == "thinking" {
		out = append(out, a.evBlock("content_block_delta", a.blockIdx, nil, &StreamDelta{
			Type:      "signature_delta",
			Signature: a.thinkingSignature(),
		}))
	}
	out = append(out, a.evBlock("content_block_stop", a.blockIdx, nil, nil))
	a.blockOpen = false
	a.thinkingText.Reset()
	return out
}

// thinkingSignature 返回本块思考内容的桥签名。
//
// 调用前提保证 a.thinkingText 恒非空：骨架只在 delta.ReasoningContent 非空时回调
// ReasoningDelta（见 from_openai.go），而 ReasoningDelta 先开块（重置累积器）再
// WriteString(s)，故 thinking 块收尾时累积必非空。历史上此处还有「空内容退回 'sig'
// 常量占位」的兜底（reasoningSignatureFallback），那是 c942424 时代直发常量签名的
// 实现残迹，骨架化后不可达，已删除。
func (a *anthropicBlockSink) thinkingSignature() string {
	return EncodeReasoningSignature(a.thinkingText.String())
}

// ToolStart 为新的并行工具调用分配 anthropic content block index 并发
// content_block_start（携带当前帧的 id / name，即使为空也保证后续
// input_json_delta 有对应的 start）。
func (a *anthropicBlockSink) ToolStart(index int, id, name string) [][]byte {
	blockIdx := a.nextIdx
	a.nextIdx++
	if a.toolIdx == nil {
		a.toolIdx = map[int]int{}
	}
	a.toolIdx[index] = blockIdx
	return [][]byte{a.evBlock("content_block_start", blockIdx, &ContentBlock{
		Type: "tool_use",
		ID:   id,
		Name: name,
	}, nil)}
}

// ToolDelta 发工具参数增量（input_json_delta）。
func (a *anthropicBlockSink) ToolDelta(index int, args string) [][]byte {
	blockIdx, ok := a.toolIdx[index]
	if !ok || args == "" {
		return nil
	}
	return [][]byte{a.evBlock("content_block_delta", blockIdx, nil, &StreamDelta{
		Type:        "input_json_delta",
		PartialJSON: args,
	})}
}

// ToolComplete 关闭该工具的 content block（参数已按增量流出，无需重发）。
func (a *anthropicBlockSink) ToolComplete(index int, _, _, _ string) [][]byte {
	blockIdx, ok := a.toolIdx[index]
	if !ok {
		return nil
	}
	delete(a.toolIdx, index)
	return [][]byte{a.evBlock("content_block_stop", blockIdx, nil, nil)}
}

// Finish 发 message_delta（stop_reason 与 usage，两个必填键始终存在：
// stop_reason 兜底 end_turn、usage 兜底空对象）与 message_stop。
// 收尾前补一次接地兜底：grounding 正常随首个 text 块补发（见 textBlock），但流里
// 若从未开 text 块（纯工具调用、纯思考），接地会静默丢失。有 text 的场景
// groundingDone 已置位，此处幂等 no-op。
func (a *anthropicBlockSink) Finish(finishReason string, u *Usage) [][]byte {
	if !a.started {
		return nil
	}
	if u != nil {
		a.usage = toAnthropicUsage(u)
	}
	if a.usage == nil {
		a.usage = &AnthropicUsage{}
	}
	delta := &StreamDelta{StopReason: openAIFinishToAnthropic(finishReason)}
	deltaEvent, _ := json.Marshal(MessagesStreamEvent{Type: "message_delta", Delta: delta, Usage: a.usage})
	out := a.emitGrounding()
	return append(out,
		deltaEvent,
		a.evMessage("message_stop", nil),
	)
}
