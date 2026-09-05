package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 本文件定义流式响应的转换入口与共享的 OpenAI chunk 累加器。
// 流式转换是「有状态」的：每个方向一个 StreamConverter 实例，逐条消费上游事件，产出目标事件。

// StreamConverter 将上游协议的一条流式事件转换为目标协议的一条或多条流式事件。
// 传入 / 返回的都是已剥掉 SSE 帧（data: / event: 等）的纯 JSON 载荷。
type StreamConverter interface {
	// Convert 处理一条上游事件载荷，返回若干目标事件载荷。
	Convert(payload []byte) ([][]byte, error)
	// Finish 上游流结束时调用，返回收尾载荷（补 finish_reason / usage）。
	Finish() ([][]byte, error)
	// Usage 返回累积的用量；未累积到（如直通、openai 上游）返回 nil。
	Usage() *Usage
}

// StreamOption 是 NewStreamConverter 的可选项。
type StreamOption func(*streamOptions)

type streamOptions struct {
	inputEstimate func() int
}

// WithInputEstimate 提供客户端输入 token 的惰性估算 thunk。仅 OpenAI→Anthropic
// 转换器会在发出首个 message_start 前调用它（上游 usage 缺失时兜底填充
// input_tokens，让客户端能看到上下文占用）；其他协议的转换器永不调用，
// body 全量解析的开销只在需要时发生。
func WithInputEstimate(fn func() int) StreamOption {
	return func(o *streamOptions) { o.inputEstimate = fn }
}

// inputEstimateSetter 是接受惰性估算 thunk 的转换器内部能力（仅 anthropic
// from 方向实现）；不导出——何时需要估算是转换器的实现细节，调用方无须知道。
type inputEstimateSetter interface {
	setInputEstimate(fn func() int)
}

// NewStreamConverter 构建 from→to 的流式转换器，经 OpenAI 规范格式中转。
func NewStreamConverter(from, to Provider, opts ...StreamOption) (StreamConverter, error) {
	if !from.Valid() {
		return nil, fmt.Errorf("未知的源协议: %s", from)
	}
	if !to.Valid() {
		return nil, fmt.Errorf("未知的目标协议: %s", to)
	}
	var o streamOptions
	for _, opt := range opts {
		opt(&o)
	}
	if from == to {
		// 同协议直通：仍按源协议提取 usage，否则流式日志的 token/费用丢失
		return newProtocolAwarePassthrough(from), nil
	}
	first := newToOpenAIStream(from)
	second := newFromOpenAIStream(to)
	if o.inputEstimate != nil {
		if es, ok := second.(inputEstimateSetter); ok {
			es.setInputEstimate(o.inputEstimate)
		}
	}
	return &chainedStreamConverter{first: first, second: second}, nil
}

// protocolAwarePassthrough 同协议直通：usage 提取委托给源协议注册的能力项
// newUsageExtractor（usage.go），本结构只是薄壳——没有能力项就取不到 usage，
// 新协议漏提取的问题在注册表编译期就会暴露，而不是流式日志静默丢 token。
type protocolAwarePassthrough struct {
	extract func(payload []byte) *Usage
	usage   *Usage
}

func newProtocolAwarePassthrough(p Provider) *protocolAwarePassthrough {
	if impl, ok := implFor(p); ok {
		return &protocolAwarePassthrough{extract: impl.newUsageExtractor()}
	}
	return &protocolAwarePassthrough{}
}

func (p *protocolAwarePassthrough) Convert(payload []byte) ([][]byte, error) {
	if u := p.extract(payload); u != nil {
		p.usage = u
	}
	return [][]byte{payload}, nil
}
func (*protocolAwarePassthrough) Finish() ([][]byte, error) { return nil, nil }
func (p *protocolAwarePassthrough) Usage() *Usage {
	if p.usage != nil {
		return p.usage
	}
	return nil
}

// newToOpenAIStream 返回「from 协议 → OpenAI chunk」的转换器。
func newToOpenAIStream(from Provider) StreamConverter {
	if impl, ok := implFor(from); ok {
		return impl.newToStream()
	}
	return nil
}

// newFromOpenAIStream 返回「OpenAI chunk → to 协议」的转换器。
func newFromOpenAIStream(to Provider) StreamConverter {
	if impl, ok := implFor(to); ok {
		return impl.newFromStream()
	}
	return nil
}

// identityStream OpenAI 协议段的直通转换器，用于跨协议链路里的 OpenAI 端点
// （from==to 的同协议直通走 protocolAwarePassthrough）。仍解析每一个 chunk 累积
// usage：否则 OpenAI 端点返回的 usage 会在日志落库时丢失（chain 的 Usage() 只取第一段）。
type identityStream struct {
	usage *Usage
}

func newIdentityStream() StreamConverter { return &identityStream{} }

func (s *identityStream) Convert(payload []byte) ([][]byte, error) {
	var chunk ChatCompletionChunk
	if json.Unmarshal(payload, &chunk) == nil && chunk.Usage != nil {
		s.usage = chunk.Usage
	}
	return [][]byte{payload}, nil
}
func (*identityStream) Finish() ([][]byte, error) { return nil, nil }
func (s *identityStream) Usage() *Usage {
	if s.usage != nil {
		return s.usage
	}
	return nil
}

// chainedStreamConverter 串联「from→openai」与「openai→to」两个转换器。
type chainedStreamConverter struct {
	first  StreamConverter
	second StreamConverter
}

func (c *chainedStreamConverter) Convert(payload []byte) ([][]byte, error) {
	mid, err := c.first.Convert(payload)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for _, m := range mid {
		parts, err := c.second.Convert(m)
		if err != nil {
			return nil, err
		}
		out = append(out, parts...)
	}
	return out, nil
}

func (c *chainedStreamConverter) Finish() ([][]byte, error) {
	mid, err := c.first.Finish()
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for _, m := range mid {
		parts, err := c.second.Convert(m)
		if err != nil {
			return nil, err
		}
		out = append(out, parts...)
	}
	tail, err := c.second.Finish()
	if err != nil {
		return nil, err
	}
	out = append(out, tail...)
	return out, nil
}

func (c *chainedStreamConverter) Usage() *Usage { return c.first.Usage() }

// openAIStreamState 是「→OpenAI」方向各转换器共享的 chunk 元信息累积器，
// 借鉴 new-api 的 ResponseInfo：跨事件累积 id/model/usage/finish_reason/文本。
type openAIStreamState struct {
	id         string
	model      string
	usage      *Usage
	rawUsage   *AnthropicUsage // anthropic 原始用量，经 usageFromAnthropic 重算（gemini/responses 方向不用）
	finish     string
	text       strings.Builder
	roleSent   bool
	finishSent bool
}

// setMeta 一次性写入 id / model。
func (s *openAIStreamState) setMeta(id, model string) {
	if s.id == "" {
		s.id = id
	}
	if s.model == "" {
		s.model = model
	}
}

// emitChunk 构造一个 OpenAI chunk 并序列化。
func (s *openAIStreamState) emitChunk(delta ChunkDelta, finish string) []byte {
	c := &ChatCompletionChunk{
		ID:     s.id,
		Object: "chat.completion.chunk",
		Model:  s.model,
		Choices: []ChunkChoice{{
			Index:        0,
			Delta:        delta,
			FinishReason: finish,
		}},
	}
	b, _ := json.Marshal(c)
	return b
}

// emitRole 发出 role chunk（仅在首个内容前一次）。
func (s *openAIStreamState) emitRole(role string) []byte {
	if s.roleSent || role == "" {
		return nil
	}
	s.roleSent = true
	return s.emitChunk(ChunkDelta{Role: role}, "")
}

// emitFinal 发出收尾 chunk（含 finish_reason 与 usage）。
func (s *openAIStreamState) emitFinal() []byte {
	if s.finishSent {
		return nil
	}
	s.finishSent = true
	c := &ChatCompletionChunk{
		ID:     s.id,
		Object: "chat.completion.chunk",
		Model:  s.model,
		Choices: []ChunkChoice{{
			Index:        0,
			Delta:        ChunkDelta{},
			FinishReason: s.finish,
		}},
		Usage: s.usage,
	}
	b, _ := json.Marshal(c)
	return b
}

// firstNonEmpty 返回第一个非空字符串，用于跨 chunk 累积 id / model。
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
