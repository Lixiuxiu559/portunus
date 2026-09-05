package protocol

import (
	"encoding/json"
)

// 本文件是「X → canonical Usage」归一数学的唯一权威（领域词条见 CONTEXT.md「用量归一」）：
// 各协议的原始用量结构在此统一换算为 Usage（PromptTokens 表示非缓存输入，
// 缓存读 / 写单独计）。此前同一段扣缓存数学在 anthropic.go / anthropic_stream.go /
// stream.go / gemini.go / responses.go 各写一份，已发生两处漂移（gemini 流式不扣
// 缓存、直通提取漏 gemini）；现收敛为三个纯函数，任何口径调整只改这里。

// Usage 是统一 token 用量，供日志 / 计费使用。
// PromptTokens 表示非缓存的输入 token；缓存命中的输入单独记在 CacheReadTokens。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// UnmarshalJSON 解析 OpenAI 规范的 usage，把 prompt_tokens_details.cached_tokens
// 归一化为 CacheReadTokens、cache_creation_tokens 归一化为 CacheWriteTokens
// （并从 PromptTokens 中扣除，得到非缓存输入）。
func (u *Usage) UnmarshalJSON(data []byte) error {
	var aux struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		TotalTokens         int `json:"total_tokens"`
		PromptTokensDetails struct {
			CachedTokens        int `json:"cached_tokens"`
			CacheCreationTokens int `json:"cache_creation_tokens"`
		} `json:"prompt_tokens_details"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	u.PromptTokens = aux.PromptTokens - aux.PromptTokensDetails.CachedTokens - aux.PromptTokensDetails.CacheCreationTokens
	u.CompletionTokens = aux.CompletionTokens
	u.TotalTokens = aux.TotalTokens
	u.CacheReadTokens = aux.PromptTokensDetails.CachedTokens
	u.CacheWriteTokens = aux.PromptTokensDetails.CacheCreationTokens
	return nil
}

// usageFromAnthropic 把 Anthropic 用量换算为 canonical Usage。
// input_tokens 是含缓存读写的总输入，需扣除缓存得非缓存输入；
// Total = Input + Output（三处口径统一，流式 message_start 的中间态 Total
// 同样非零，message_delta 到达后覆盖）。
func usageFromAnthropic(u AnthropicUsage) Usage {
	return Usage{
		PromptTokens:     u.InputTokens - u.CacheCreationInputTokens - u.CacheReadInputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.InputTokens + u.OutputTokens,
		CacheReadTokens:  u.CacheReadInputTokens,
		CacheWriteTokens: u.CacheCreationInputTokens,
	}
}

// usageFromGemini 把 Gemini 用量换算为 canonical Usage。
// promptTokenCount 是含缓存的总输入，缓存命中单独记 CacheRead；
// Gemini 无缓存写概念。
func usageFromGemini(u GeminiUsageMetadata) Usage {
	prompt := u.PromptTokenCount - u.CachedContentTokenCount
	if prompt < 0 {
		// 部分中转上游把 cachedContentTokenCount 单列、不并入 promptTokenCount：
		// 扣减为负说明口径不一，钳 0 防止计费的输入项为负
		prompt = 0
	}
	return Usage{
		PromptTokens:     prompt,
		CompletionTokens: u.CandidatesTokenCount,
		TotalTokens:      u.TotalTokenCount,
		CacheReadTokens:  u.CachedContentTokenCount,
	}
}

// usageFromResponses 把 Responses 用量换算为 canonical Usage。
// input_tokens 是含缓存的总输入；Responses 无缓存写概念。
func usageFromResponses(u ResponsesUsage) Usage {
	return Usage{
		PromptTokens:     u.InputTokens - u.InputTokensDetails.CachedTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.TotalTokens,
		CacheReadTokens:  u.InputTokensDetails.CachedTokens,
	}
}

// toAnthropicUsage 把统一 Usage 归一为 Anthropic usage（anthropic.go 同名函数
// 的归属迁移——反向换算与正向归一同居一处，改口径不会漏一半）。
// PromptTokens 已是非缓存输入，直接作为 input_tokens；缓存读写单独映射到
// cache_read/cache_creation_input_tokens。
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

// toGeminiUsage 把 canonical Usage 反向换算为 Gemini 用量：promptTokenCount 是
// 含缓存的总输入，需补回缓存读 / 写（与 usageFromGemini 的扣减互为逆运算）。
func toGeminiUsage(u *Usage) *GeminiUsageMetadata {
	if u == nil {
		return nil
	}
	return &GeminiUsageMetadata{
		PromptTokenCount:        u.PromptTokens + u.CacheReadTokens + u.CacheWriteTokens,
		CandidatesTokenCount:    u.CompletionTokens,
		TotalTokenCount:         u.TotalTokens,
		CachedContentTokenCount: u.CacheReadTokens,
	}
}

// toResponsesUsage 把 canonical Usage 反向换算为 Responses 用量：input_tokens
// 是含缓存的总输入，需补回缓存读 / 写（与 usageFromResponses 的扣减互为逆运算）。
func toResponsesUsage(u *Usage) *ResponsesUsage {
	if u == nil {
		return nil
	}
	return &ResponsesUsage{
		InputTokens:  u.PromptTokens + u.CacheReadTokens + u.CacheWriteTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
	}
}

// ── 流式直通的 usage 提取（注册为 providerImpls 的能力项 newUsageExtractor）──
// 每个协议实现一个返回有状态闭包的工厂：逐流事件累积，返回当前累计值（无则 nil）。
// 收敛于此使「新增协议必须提供直通 usage 提取」成为编译期强制——此前 extract
// switch 漏写 ProviderGemini，gemini→gemini 流式计费丢 token。

// newOpenAIUsageExtractor OpenAI 流事件的 usage 提取：usage chunk 一次性给出。
func newOpenAIUsageExtractor() func([]byte) *Usage {
	return func(payload []byte) *Usage {
		var chunk ChatCompletionChunk
		if json.Unmarshal(payload, &chunk) != nil || chunk.Usage == nil {
			return nil
		}
		return chunk.Usage
	}
}

// newAnthropicUsageExtractor Anthropic 流事件的 usage 提取：message_start 给初始值，
// message_delta 覆盖 output；持有原始 AnthropicUsage、每次经 usageFromAnthropic
// 换算——Total 数学不在此手写（与归一唯一权威保持同一份）。
func newAnthropicUsageExtractor() func([]byte) *Usage {
	var raw *AnthropicUsage
	return func(payload []byte) *Usage {
		var ev MessagesStreamEvent
		if json.Unmarshal(payload, &ev) != nil {
			return canonical(raw)
		}
		switch ev.Type {
		case "message_start":
			if ev.Message != nil && ev.Message.Usage != nil {
				u := *ev.Message.Usage
				raw = &u
			}
		case "message_delta":
			if ev.Usage != nil {
				if raw == nil {
					raw = &AnthropicUsage{}
				}
				raw.OutputTokens = ev.Usage.OutputTokens
			}
		}
		return canonical(raw)
	}
}

// canonical 把累积的原始用量换算为 canonical Usage；未累积到任何事件时为 nil。
func canonical(raw *AnthropicUsage) *Usage {
	if raw == nil {
		return nil
	}
	u := usageFromAnthropic(*raw)
	return &u
}

// newGeminiUsageExtractor Gemini 流事件的 usage 提取：每条 chunk 的 usageMetadata
// 均为累积值，最后一条生效。
func newGeminiUsageExtractor() func([]byte) *Usage {
	return func(payload []byte) *Usage {
		var resp GenerateContentResponse
		if json.Unmarshal(payload, &resp) != nil || resp.UsageMetadata == nil {
			return nil
		}
		u := usageFromGemini(*resp.UsageMetadata)
		return &u
	}
}

// newResponsesUsageExtractor Responses 流事件的 usage 提取：仅 response.completed
// 携带最终 usage。
func newResponsesUsageExtractor() func([]byte) *Usage {
	return func(payload []byte) *Usage {
		var ev ResponsesStreamEvent
		if json.Unmarshal(payload, &ev) != nil || ev.Type != "response.completed" ||
			ev.Response == nil || ev.Response.Usage == nil {
			return nil
		}
		u := usageFromResponses(*ev.Response.Usage)
		return &u
	}
}
