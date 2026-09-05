package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 本文件是「OpenAI chunk → 目标协议流」方向的共享骨架。此前 anthropic 手写了
// 200 行块状态机（text/thinking 单块互斥 + 并行 tool_calls 的 index 归并），而
// gemini / responses 只有文本直通——tool_calls 与 reasoning_content 被静默丢弃
// （gemini 客户端 + openai 上游的工具调用只能收一场空）。历史 bug「混排丢内容」
// 「图片保留」同根：没有显式的块生命周期模型，靠桶+布尔拼状态。骨架把块生命
// 周期收敛为一处，各协议实现 blockSink 回调即可，新协议不再重写映射循环。

// blockSink 是目标协议的块生命周期回调，返回产出的事件载荷（可为 nil）。
// 骨架保证的调用顺序：
//
//	BeginMessage → (TextDelta | ReasoningDelta | ToolStart/ToolDelta)*
//	  → （切换或收尾时）TextEnd 关闭当前单块、ToolComplete 按 openai index 序
//	  关闭未完成工具（args 为累积完整参数）→ Finish(finishReason, usage)。
//
// 注意：text / thinking 是互斥单块（类型切换先 TextEnd）；工具调用可并行
// （按 openai 的 tool_calls index 区分），切回文本或收尾时统一完成。
type blockSink interface {
	// BeginMessage 首个带 choices 的 chunk 时回调；u 为该时刻已捕获的 canonical
	// usage（上游首 chunk 即带 usage 时非 nil，anthropic 据此填 message_start 的
	// 真实 input_tokens，而非本地估算）。
	BeginMessage(id, model string, u *Usage) [][]byte
	TextDelta(s string) [][]byte
	ReasoningDelta(s string) [][]byte
	ToolStart(index int, id, name string) [][]byte
	ToolDelta(index int, args string) [][]byte
	ToolComplete(index int, id, name, args string) [][]byte
	TextEnd() [][]byte
	Finish(finishReason string, u *Usage) [][]byte
}

// toolAccum 累积一个并行工具调用的标识与参数增量。
type toolAccum struct {
	id   string
	name string
	args strings.Builder
}

// fromOpenAISkeleton 解析 canonical chunk 的块生命周期并驱动 blockSink。
// 实现 StreamConverter；newFromStream 工厂用它包装各协议的 sink。
type fromOpenAISkeleton struct {
	sink blockSink

	id    string
	model string

	started    bool
	singleOpen bool
	singleType string // "text" | "thinking"

	tools     map[int]*toolAccum // openai tool_calls index → 累积器（仅记录未完成者）
	toolOrder []int              // 首次出现顺序

	finish string
	usage  *Usage
}

// newFromOpenAISkeleton 用指定 sink 构建转换器。
func newFromOpenAISkeleton(sink blockSink) StreamConverter {
	return &fromOpenAISkeleton{sink: sink}
}

// setInputEstimate 转发惰性估算 thunk 给 sink（仅需要的协议实现）。
func (s *fromOpenAISkeleton) setInputEstimate(fn func() int) {
	if bs, ok := s.sink.(inputEstimateSetter); ok {
		bs.setInputEstimate(fn)
	}
}

func (s *fromOpenAISkeleton) Convert(payload []byte) ([][]byte, error) {
	var chunk ChatCompletionChunk
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return nil, fmt.Errorf("解析 openai chunk 失败: %w", err)
	}
	s.id = firstNonEmpty(s.id, chunk.ID)
	s.model = firstNonEmpty(s.model, chunk.Model)
	if chunk.Usage != nil {
		s.usage = chunk.Usage
	}

	var out [][]byte
	if len(chunk.Choices) == 0 {
		return out, nil // 仅携带 usage / 心跳的 chunk：等首个带 choices 的帧再开流，
		// 否则无 id/model 的预告帧会触发空 id/model 的 message_start
	}
	if !s.started {
		s.started = true
		out = append(out, s.sink.BeginMessage(s.id, s.model, s.usage)...)
	}
	delta := chunk.Choices[0].Delta

	if fr := chunk.Choices[0].FinishReason; fr != "" {
		s.finish = fr // 先于 role 守卫捕获：Azure / llama.cpp 的收尾帧 role 与 finish 同帧
	}

	// 纯 role 标记 chunk（首个 chunk 仅声明 assistant 角色）无可转换内容，忽略。
	// 不能按 role 非空直接 return：官方 OpenAI 的工具开场块 role 与 tool_calls
	// 同帧，整帧被吞后后续参数帧拿不到 id/name，tool_use 块发空 id 导致客户端
	// Tool use interrupted。
	if delta.Role != "" && len(delta.ToolCalls) == 0 && delta.ReasoningContent == "" && delta.Content == "" {
		return out, nil
	}

	if len(delta.ToolCalls) > 0 {
		out = append(out, s.closeSingle()...) // 从 text/thinking 切到 tool，关闭单块
		for _, tc := range delta.ToolCalls {
			idx := tc.Index
			if idx < 0 {
				idx = 0
			}
			acc := s.tools[idx]
			if acc == nil {
				acc = &toolAccum{id: tc.ID, name: tc.Function.Name}
				if s.tools == nil {
					s.tools = map[int]*toolAccum{}
				}
				s.tools[idx] = acc
				s.toolOrder = append(s.toolOrder, idx)
				out = append(out, s.sink.ToolStart(idx, tc.ID, tc.Function.Name)...)
			} else {
				// 后续帧补充 id / name（开场帧可能为空），供 ToolComplete 使用
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
			}
			// 开场帧即携带参数（vLLM / Ollama 等整帧发全量 arguments）与后续增量帧
			// 一视同仁：不累积不下发会丢工具参数首段
			if tc.Function.Arguments != "" {
				acc.args.WriteString(tc.Function.Arguments)
				out = append(out, s.sink.ToolDelta(idx, tc.Function.Arguments)...)
			}
		}
		return out, nil
	}

	// DeepSeek 等兼容方的 reasoning_content 扩展字段 → 目标协议的思考块。
	// 官方 OpenAI 没有该字段；有此字段说明上游是兼容方，思考内容应显式透出。
	if delta.ReasoningContent != "" {
		out = append(out, s.openSingle("thinking")...)
		out = append(out, s.sink.ReasoningDelta(delta.ReasoningContent)...)
		return out, nil
	}

	if delta.Content != "" {
		out = append(out, s.openSingle("text")...)
		out = append(out, s.sink.TextDelta(delta.Content)...)
	}
	return out, nil
}

func (s *fromOpenAISkeleton) Finish() ([][]byte, error) {
	var out [][]byte
	out = append(out, s.closeSingle()...)
	out = append(out, s.completeTools()...)
	out = append(out, s.sink.Finish(s.finish, s.usage)...)
	return out, nil
}

// Usage 返回捕获的 canonical usage。链式转换的 Usage() 取 to-openai 段，
// 此值仅为接口完整性。
func (s *fromOpenAISkeleton) Usage() *Usage { return s.usage }

// openSingle 打开 text / thinking 单块：类型切换先关闭旧块（thinking 的签名
// 收尾语义在 sink 的 TextEnd）；从工具切回文本时工具块已在工具分支被关闭，
// 此处无条件完成剩余工具——保证新单块打开前不会有任何工具块仍 open。
func (s *fromOpenAISkeleton) openSingle(typ string) [][]byte {
	var out [][]byte
	if s.singleOpen && s.singleType != typ {
		s.singleOpen = false
		out = append(out, s.sink.TextEnd()...)
	}
	out = append(out, s.completeTools()...)
	if !s.singleOpen {
		s.singleOpen = true
		s.singleType = typ
	}
	return out
}

// closeSingle 若单块打开则关闭（切到工具或收尾时）。
func (s *fromOpenAISkeleton) closeSingle() [][]byte {
	if !s.singleOpen {
		return nil
	}
	s.singleOpen = false
	return s.sink.TextEnd()
}

// completeTools 按首次出现顺序完成所有未完成的工具调用，随后清空状态。
func (s *fromOpenAISkeleton) completeTools() [][]byte {
	if len(s.toolOrder) == 0 {
		return nil
	}
	var out [][]byte
	for _, idx := range s.toolOrder {
		acc := s.tools[idx]
		if acc == nil {
			continue
		}
		out = append(out, s.sink.ToolComplete(idx, acc.id, acc.name, acc.args.String())...)
	}
	s.tools = nil
	s.toolOrder = nil
	return out
}
