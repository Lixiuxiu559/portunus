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

// EstimateSetter 可选接口：设置请求输入的预估 token 数。
// 上游未返回 usage 时，转换器据此填充 message_start 的 input_tokens，
// 让 Anthropic 客户端能看到上下文占用。仅转换器实现；调用方用类型断言触发。
type EstimateSetter interface {
	SetEstimateInputTokens(n int)
}

// NewStreamConverter 构建 from→to 的流式转换器，经 OpenAI 规范格式中转。
func NewStreamConverter(from, to Provider) (StreamConverter, error) {
	if !from.Valid() {
		return nil, fmt.Errorf("未知的源协议: %s", from)
	}
	if !to.Valid() {
		return nil, fmt.Errorf("未知的目标协议: %s", to)
	}
	if from == to {
		// 同协议直通：仍按源协议提取 usage，否则流式日志的 token/费用丢失
		return newProtocolAwarePassthrough(from), nil
	}
	first := newToOpenAIStream(from)
	second := newFromOpenAIStream(to)
	return &chainedStreamConverter{first: first, second: second}, nil
}

// protocolAwarePassthrough 同协议直通，按源协议从原始流事件中累积 usage。
type protocolAwarePassthrough struct {
	extract func(payload []byte)
	usage   *Usage
}

func newProtocolAwarePassthrough(p Provider) *protocolAwarePassthrough {
	pt := &protocolAwarePassthrough{}
	switch p {
	case ProviderOpenAI:
		pt.extract = func(payload []byte) {
			var chunk ChatCompletionChunk
			if json.Unmarshal(payload, &chunk) == nil && chunk.Usage != nil {
				pt.usage = chunk.Usage
			}
		}
	case ProviderAnthropic:
		pt.extract = func(payload []byte) {
			var ev MessagesStreamEvent
			if json.Unmarshal(payload, &ev) != nil {
				return
			}
			switch ev.Type {
			case "message_start":
				if ev.Message != nil && ev.Message.Usage != nil {
					u := ev.Message.Usage
					pt.usage = &Usage{
						PromptTokens:     u.InputTokens - u.CacheCreationInputTokens - u.CacheReadInputTokens,
						CacheReadTokens:  u.CacheReadInputTokens,
						CacheWriteTokens: u.CacheCreationInputTokens,
						TotalTokens:      u.InputTokens + u.OutputTokens,
					}
				}
			case "message_delta":
				if ev.Usage != nil {
					if pt.usage == nil {
						pt.usage = &Usage{}
					}
					pt.usage.CompletionTokens = ev.Usage.OutputTokens
					pt.usage.TotalTokens = pt.usage.PromptTokens + pt.usage.CompletionTokens + pt.usage.CacheReadTokens + pt.usage.CacheWriteTokens
				}
			}
		}
	case ProviderOpenAIResponses:
		pt.extract = func(payload []byte) {
			var ev ResponsesStreamEvent
			if json.Unmarshal(payload, &ev) != nil {
				return
			}
			if ev.Type == "response.completed" && ev.Response != nil && ev.Response.Usage != nil {
				u := ev.Response.Usage
				pt.usage = &Usage{
					PromptTokens:     u.InputTokens - u.InputTokensDetails.CachedTokens,
					CompletionTokens: u.OutputTokens,
					TotalTokens:      u.TotalTokens,
					CacheReadTokens:  u.InputTokensDetails.CachedTokens,
				}
			}
		}
	}
	return pt
}

func (p *protocolAwarePassthrough) Convert(payload []byte) ([][]byte, error) {
	if p.extract != nil {
		p.extract(payload)
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
//（from==to 的同协议直通走 protocolAwarePassthrough）。仍解析每一个 chunk 累积
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

// SetEstimateInputTokens 把预估输入 token 转发给第二段转换器（openai→to 方向），
// 由它在 message_start 里填充 input_tokens。
func (c *chainedStreamConverter) SetEstimateInputTokens(n int) {
	if s, ok := c.second.(EstimateSetter); ok {
		s.SetEstimateInputTokens(n)
	}
}

// openAIStreamState 是「→OpenAI」方向各转换器共享的 chunk 元信息累积器，
// 借鉴 new-api 的 ResponseInfo：跨事件累积 id/model/usage/finish_reason/文本。
type openAIStreamState struct {
	id         string
	model      string
	usage      *Usage
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
