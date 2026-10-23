package protocol

import (
	"encoding/json"
	"fmt"
	"github.com/Lixiuxiu559/portunus/backend/resource"
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
	limits        resource.Limits
}

// WithInputEstimate 提供客户端输入 token 的惰性估算 thunk。仅 OpenAI→Anthropic
// 转换器会在发出首个 message_start 前调用它（上游 usage 缺失时兜底填充
// input_tokens，让客户端能看到上下文占用）；其他协议的转换器永不调用，
// body 全量解析的开销只在需要时发生。
func WithInputEstimate(fn func() int) StreamOption {
	return func(o *streamOptions) { o.inputEstimate = fn }
}

func WithResourceLimits(limits resource.Limits) StreamOption {
	return func(o *streamOptions) { o.limits = limits.Normalize() }
}

// inputEstimateSetter 是接受惰性估算 thunk 的转换器内部能力（仅 anthropic
// from 方向实现）；不导出——何时需要估算是转换器的实现细节，调用方无须知道。
type inputEstimateSetter interface {
	setInputEstimate(fn func() int)
}

// NewStreamConverter 构建 from→to 的流式转换器，经类型化事件中转。
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
		p := newProtocolAwarePassthrough(from)
		p.setResourceLimits(o.limits)
		return p, nil
	}
	source, _ := implFor(from)
	target, _ := implFor(to)
	decoder := source.newStreamDecoder()
	encoder := target.newStreamEncoder()
	setStreamLimits(decoder, o.limits)
	setStreamLimits(encoder, o.limits)
	if o.inputEstimate != nil {
		if es, ok := encoder.(inputEstimateSetter); ok {
			es.setInputEstimate(o.inputEstimate)
		}
	}
	return &typedStreamConverter{from: from, limits: o.limits.Normalize(), decoder: decoder, encoder: encoder, legacyIntermediateUsage: from != ProviderOpenAI && to != ProviderOpenAI}, nil
}

// protocolAwarePassthrough 同协议直通：usage 提取委托给源协议注册的能力项
// newUsageExtractor（usage.go），本结构只是薄壳——没有能力项就取不到 usage，
// 新协议漏提取的问题在注册表编译期就会暴露，而不是流式日志静默丢 token。
type protocolAwarePassthrough struct {
	extract func(payload []byte) *Usage
	source  Provider
	budget  streamResourceBudget
	usage   *Usage
}

func newProtocolAwarePassthrough(p Provider) *protocolAwarePassthrough {
	if impl, ok := implFor(p); ok {
		return &protocolAwarePassthrough{source: p, extract: impl.newUsageExtractor()}
	}
	return &protocolAwarePassthrough{}
}

func (p *protocolAwarePassthrough) Convert(payload []byte) ([][]byte, error) {
	if err := p.budget.payload(p.source, payload, false); err != nil {
		return nil, err
	}
	if u := p.extract(payload); u != nil {
		p.usage = u
	}
	return [][]byte{payload}, nil
}
func (p *protocolAwarePassthrough) Finish() ([][]byte, error) { return nil, p.budget.err }
func (p *protocolAwarePassthrough) setResourceLimits(l resource.Limits) {
	p.budget.limits = l.Normalize()
}
func (p *protocolAwarePassthrough) resourceLimits() resource.Limits { return p.budget.normalized() }
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
	budget streamResourceBudget
	usage  *Usage
}

func newIdentityStream() StreamConverter { return &identityStream{} }

func (s *identityStream) Convert(payload []byte) ([][]byte, error) {
	if err := s.budget.payload(ProviderOpenAI, payload, false); err != nil {
		return nil, err
	}
	var chunk ChatCompletionChunk
	if json.Unmarshal(payload, &chunk) == nil && chunk.Usage != nil {
		s.usage = chunk.Usage
	}
	return [][]byte{payload}, nil
}
func (s *identityStream) Finish() ([][]byte, error) { return nil, s.budget.err }
func (s *identityStream) Usage() *Usage {
	if s.usage != nil {
		return s.usage
	}
	return nil
}

// sourceStreamState 是「→OpenAI」方向各转换器共享的 chunk 元信息累积器，
// 借鉴 new-api 的 ResponseInfo：跨事件累积 id/model/usage/finish_reason/文本。
type sourceStreamState struct {
	budget     streamResourceBudget
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
func (s *sourceStreamState) setMeta(id, model string) {
	if s.id == "" {
		s.id = id
	}
	if s.model == "" {
		s.model = model
	}
}

// emitChunk 构造内部类型化帧，不序列化为 OpenAI JSON。
func (s *sourceStreamState) emitChunk(delta StreamEventDelta, finish string) StreamEvent {
	return StreamEvent{ID: s.id, Model: s.model, Choices: []StreamChoice{{Index: 0, Delta: delta, FinishReason: finish}}}
}

func (s *sourceStreamState) emitRole(role string) *StreamEvent {
	if s.roleSent || role == "" {
		return nil
	}
	s.roleSent = true
	event := s.emitChunk(StreamEventDelta{Role: role}, "")
	return &event
}

func (s *sourceStreamState) emitFinal() *StreamEvent {
	if s.finishSent {
		return nil
	}
	s.finishSent = true
	event := s.emitChunk(StreamEventDelta{}, s.finish)
	event.Usage = s.usage
	return &event
}

// firstNonEmpty 返回第一个非空字符串，用于跨事件累积 id / model。
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
