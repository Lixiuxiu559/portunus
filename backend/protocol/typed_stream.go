package protocol

import (
	"encoding/json"
	"fmt"
	"github.com/Lixiuxiu559/portunus/backend/resource"
)

// StreamEvent 是协议无关的语义增量帧。保留同一源帧的字段组合，避免拆帧改变
// tool/reasoning/text 的既有选择优先级。Native 单独保留源协议快照，不参与目标 JSON 编码。
// 当前兼容阶段仍按既有规则消费首个 choice；索引在内部保留以便后续演进。
type StreamEvent struct {
	Native  *ResponseEnvelope `json:"-"`
	ID      string
	Model   string
	Choices []StreamChoice
	Usage   *Usage
}

// StreamChoice 对应一个候选的增量与结束原因；usage 仍属于整帧。
type StreamChoice struct {
	Index        int
	Delta        StreamEventDelta
	FinishReason string
}

// StreamEventDelta 保存增量语义。工具参数是原始字符串片段，不要求每段是合法 JSON。
// 复用具名的工具与 grounding 类型，避免使用任意 map 传递协议语义。
type StreamEventDelta struct {
	Role              string
	Content           string
	ReasoningContent  string
	ToolCalls         []ChunkToolCall
	GroundingMetadata *GroundingMetadata
}

func streamDeltaFromChat(d ChunkDelta) StreamEventDelta {
	return StreamEventDelta{Role: d.Role, Content: d.Content, ReasoningContent: d.ReasoningContent, ToolCalls: d.ToolCalls, GroundingMetadata: d.GroundingMetadata}
}

func streamEventFromChat(c ChatCompletionChunk) StreamEvent {
	e := StreamEvent{ID: c.ID, Model: c.Model, Usage: c.Usage}
	if c.Choices != nil {
		e.Choices = make([]StreamChoice, len(c.Choices))
	}
	for i, choice := range c.Choices {
		e.Choices[i] = StreamChoice{Index: choice.Index, Delta: streamDeltaFromChat(choice.Delta), FinishReason: choice.FinishReason}
	}
	return e
}

// streamDecoder/streamEncoder 按上游推进同步处理 batch；每次转换拥有独立状态。
// End/Finish 继续沿用旧收尾语义，本阶段不增加 EOF 校验或失败重试策略。
type streamDecoder interface {
	Decode([]byte) ([]StreamEvent, error)
	End() ([]StreamEvent, error)
	Usage() *Usage
}

type streamEncoder interface {
	Encode(StreamEvent) ([][]byte, error)
	Finish() ([][]byte, error)
}

type typedStreamConverter struct {
	from                    Provider
	limits                  resource.Limits
	failed                  error
	decoder                 streamDecoder
	encoder                 streamEncoder
	legacyIntermediateUsage bool
}

func (s *typedStreamConverter) encode(events []StreamEvent, err error) ([][]byte, error) {
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for _, event := range events {
		if s.legacyIntermediateUsage {
			event.Usage = legacyIntermediateUsage(event.Usage)
		}
		parts, err := s.encoder.Encode(event)
		if err != nil {
			return nil, err
		}
		out = append(out, parts...)
	}
	return out, nil
}

// legacyIntermediateUsage 保留旧非 Chat→非 Chat 链的有效 wire 语义。
// 旧中间 Usage 序列化为 flat cache 字段，目标端 OpenAI usage 解析器忽略它们；
// 本阶段只移除中间 JSON，不顺带修改客户端可见用量。后续可独立统一该口径。
// 必须复制，decoder 的源用量继续完整保留，供日志读取。
func legacyIntermediateUsage(usage *Usage) *Usage {
	if usage == nil {
		return nil
	}
	projected := *usage
	projected.CacheReadTokens = 0
	projected.CacheWriteTokens = 0
	return &projected
}

func (s *typedStreamConverter) Convert(payload []byte) ([][]byte, error) {
	if s.failed != nil {
		return nil, s.failed
	}
	native, err := CaptureResponseEvent(s.from, SSEEvent{Data: payload, HasData: true}, s.limits)
	if err != nil {
		s.failed = err
		return nil, err
	}
	return s.convertNative(payload, native)
}

// convertNative attaches the original event before encoding. Payload may contain
// an inferred type; Native always retains the unmodified source data and SSE.
func (s *typedStreamConverter) convertNative(payload []byte, native *ResponseEnvelope) (out [][]byte, err error) {
	if s.failed != nil {
		return nil, s.failed
	}
	defer func() {
		if err != nil {
			s.failed = err
		}
	}()
	events, err := s.decoder.Decode(payload)
	if err != nil {
		return nil, err
	}
	for i := range events {
		events[i].Native = cloneResponseEvent(native)
	}
	return s.encode(events, nil)
}

func cloneResponseEvent(native *ResponseEnvelope) *ResponseEnvelope {
	if native == nil {
		return nil
	}
	return native.clone()
}

func (s *typedStreamConverter) resourceLimits() resource.Limits { return s.limits.Normalize() }
func (s *typedStreamConverter) Finish() ([][]byte, error) {
	if s.failed != nil {
		return nil, s.failed
	}
	out, err := s.encode(s.decoder.End())
	if err != nil {
		return nil, err
	}
	tail, err := s.encoder.Finish()
	if err != nil {
		return nil, err
	}
	return append(out, tail...), nil
}

func (s *typedStreamConverter) Usage() *Usage { return s.decoder.Usage() }

type openAIStreamDecoder struct{ usage *Usage }

func (s *openAIStreamDecoder) Decode(payload []byte) ([]StreamEvent, error) {
	var chunk ChatCompletionChunk
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return nil, fmt.Errorf("解析 openai chunk 失败: %w", err)
	}
	if chunk.Usage != nil {
		s.usage = chunk.Usage
	}
	return []StreamEvent{streamEventFromChat(chunk)}, nil
}
func (*openAIStreamDecoder) End() ([]StreamEvent, error) { return nil, nil }
func (s *openAIStreamDecoder) Usage() *Usage             { return s.usage }

type openAIStreamEncoder struct{}

func (*openAIStreamEncoder) Encode(e StreamEvent) ([][]byte, error) {
	c := ChatCompletionChunk{ID: e.ID, Object: "chat.completion.chunk", Model: e.Model, Usage: e.Usage}
	if e.Choices != nil {
		c.Choices = make([]ChunkChoice, len(e.Choices))
	}
	for i, choice := range e.Choices {
		d := choice.Delta
		c.Choices[i] = ChunkChoice{Index: choice.Index, FinishReason: choice.FinishReason, Delta: ChunkDelta{
			Role: d.Role, Content: d.Content, ReasoningContent: d.ReasoningContent, ToolCalls: d.ToolCalls, GroundingMetadata: d.GroundingMetadata,
		}}
	}
	b, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return [][]byte{b}, nil
}
func (*openAIStreamEncoder) Finish() ([][]byte, error) { return nil, nil }

// encodeOpenAIEvents 只供旧 source→OpenAI API 的 wire 边界使用。
func encodeOpenAIEvents(events []StreamEvent, err error) ([][]byte, error) {
	if err != nil {
		return nil, err
	}
	var out [][]byte
	encoder := &openAIStreamEncoder{}
	for _, event := range events {
		parts, err := encoder.Encode(event)
		if err != nil {
			return nil, err
		}
		out = append(out, parts...)
	}
	return out, nil
}
