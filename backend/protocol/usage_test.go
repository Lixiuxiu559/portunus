package protocol

import "testing"

// TestUsageFromFuncs 是三个归一纯函数的行为契约表：每行 = 原始用量结构 →
// canonical Usage 全字段断言。语义唯一实现在 usage.go；改动口径前先跑本表。
func TestUsageFromFuncs(t *testing.T) {
	// anthropic：input_tokens 含缓存读写，Total = Input + Output。
	want := Usage{PromptTokens: 30, CompletionTokens: 5, TotalTokens: 105, CacheReadTokens: 40, CacheWriteTokens: 30}
	if got := usageFromAnthropic(AnthropicUsage{InputTokens: 100, OutputTokens: 5, CacheReadInputTokens: 40, CacheCreationInputTokens: 30}); got != want {
		t.Errorf("usageFromAnthropic = %+v, want %+v", got, want)
	}
	// gemini：promptTokenCount 含缓存命中，无缓存写。
	want = Usage{PromptTokens: 60, CompletionTokens: 5, TotalTokens: 105, CacheReadTokens: 40}
	if got := usageFromGemini(GeminiUsageMetadata{PromptTokenCount: 100, CandidatesTokenCount: 5, TotalTokenCount: 105, CachedContentTokenCount: 40}); got != want {
		t.Errorf("usageFromGemini = %+v, want %+v", got, want)
	}
	// responses：input_tokens 含缓存命中，无缓存写。
	want = Usage{PromptTokens: 60, CompletionTokens: 5, TotalTokens: 105, CacheReadTokens: 40}
	if got := usageFromResponses(ResponsesUsage{InputTokens: 100, OutputTokens: 5, TotalTokens: 105,
		InputTokensDetails: struct {
			CachedTokens int `json:"cached_tokens"`
		}{CachedTokens: 40},
	}); got != want {
		t.Errorf("usageFromResponses = %+v, want %+v", got, want)
	}
}

// TestGeminiStreamUsageDeductsCache 回归：gemini 流式 usage 必须与非流式同口径
// （扣缓存 + 记 CacheRead）。修复前流式 PromptTokens 未扣 cachedContentTokenCount
// 且 CacheRead 恒 0，缓存段被按全价 input 计费。
func TestGeminiStreamUsageDeductsCache(t *testing.T) {
	conv, err := NewStreamConverter(ProviderGemini, ProviderOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	chunks := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":3,"totalTokenCount":103,"cachedContentTokenCount":40}}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"!"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":5,"totalTokenCount":105,"cachedContentTokenCount":40}}`,
	}
	for _, c := range chunks {
		if _, err := conv.Convert([]byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conv.Finish(); err != nil {
		t.Fatal(err)
	}
	u := conv.Usage()
	if u == nil || u.PromptTokens != 60 || u.CompletionTokens != 5 || u.CacheReadTokens != 40 {
		t.Fatalf("gemini 流式 usage 应与非流式同口径（扣缓存）: %+v", u)
	}
}

// TestGeminiPassthroughUsage 回归：gemini→gemini 同协议直通必须提取 usage——
// 修复前直通的 extract switch 漏 ProviderGemini，usage 永不提取，流式计费丢 token。
// 提取现注册为 providerImpls 的 newUsageExtractor 能力项，新协议漏实现即编译失败。
func TestGeminiPassthroughUsage(t *testing.T) {
	conv, err := NewStreamConverter(ProviderGemini, ProviderGemini)
	if err != nil {
		t.Fatal(err)
	}
	chunks := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]}}]}`, // 无 usage 的普通 chunk
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"!"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":5,"totalTokenCount":105,"cachedContentTokenCount":40}}`,
	}
	for _, c := range chunks {
		if _, err := conv.Convert([]byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	u := conv.Usage()
	if u == nil || u.PromptTokens != 60 || u.CompletionTokens != 5 || u.CacheReadTokens != 40 {
		t.Fatalf("gemini 直通应提取 usage 且与非流式同口径: %+v", u)
	}
}

// TestUsageFromResponseCache 验证各协议响应的缓存 token 被提取进统一 Usage。
func TestUsageFromResponseCache(t *testing.T) {
	// OpenAI: prompt_tokens 含 cached_tokens，应归一化为非缓存输入 + 缓存读。
	u := UsageFromResponse(ProviderOpenAI, []byte(`{
		"id": "chatcmpl-1",
		"usage": {"prompt_tokens": 100, "completion_tokens": 5, "total_tokens": 105,
		          "prompt_tokens_details": {"cached_tokens": 40}}
	}`))
	if u == nil || u.PromptTokens != 60 || u.CacheReadTokens != 40 || u.CacheWriteTokens != 0 {
		t.Fatalf("openai 缓存提取错误: %+v", u)
	}

	// Anthropic: input_tokens 为总输入（含缓存读写），需扣除缓存得到非缓存输入。
	u = UsageFromResponse(ProviderAnthropic, []byte(`{
		"id": "msg_1", "type": "message", "role": "assistant",
		"content": [{"type": "text", "text": "hi"}],
		"usage": {"input_tokens": 100, "output_tokens": 5,
		          "cache_read_input_tokens": 40, "cache_creation_input_tokens": 30}
	}`))
	if u == nil || u.PromptTokens != 30 || u.CompletionTokens != 5 ||
		u.CacheReadTokens != 40 || u.CacheWriteTokens != 30 {
		t.Fatalf("anthropic 缓存提取错误: %+v", u)
	}

	// Gemini: promptTokenCount 含 cachedContentTokenCount。
	u = UsageFromResponse(ProviderGemini, []byte(`{
		"candidates": [{"content": {"role": "model", "parts": [{"text": "hi"}]}}],
		"usageMetadata": {"promptTokenCount": 100, "candidatesTokenCount": 5,
		                  "totalTokenCount": 105, "cachedContentTokenCount": 40}
	}`))
	if u == nil || u.PromptTokens != 60 || u.CacheReadTokens != 40 || u.CacheWriteTokens != 0 {
		t.Fatalf("gemini 缓存提取错误: %+v", u)
	}

	// Responses: input_tokens 含 input_tokens_details.cached_tokens。
	u = UsageFromResponse(ProviderOpenAIResponses, []byte(`{
		"id": "resp_1", "object": "response",
		"output": [{"type": "message", "content": [{"type": "output_text", "text": "hi"}]}],
		"usage": {"input_tokens": 100, "output_tokens": 5, "total_tokens": 105,
		          "input_tokens_details": {"cached_tokens": 40}}
	}`))
	if u == nil || u.PromptTokens != 60 || u.CacheReadTokens != 40 || u.CacheWriteTokens != 0 {
		t.Fatalf("responses 缓存提取错误: %+v", u)
	}
}
