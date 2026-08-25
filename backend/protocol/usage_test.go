package protocol

import "testing"

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
