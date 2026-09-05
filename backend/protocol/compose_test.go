package protocol

import (
	"encoding/json"
	"testing"
)

// ComposeUpstreamRequest 行为契约：装配 = 协议转换 + 上游模型名替换 + thinking
// 注入。每行断言产出请求体的关键字段；同协议直通必须保真（canonical 之外的
// 未知字段原样保留），gemini 请求体不含模型字段（在 URL）。

// mustContain 断言 body 为合法 JSON 且包含指定顶层键值。
func mustContain(t *testing.T, body []byte, want map[string]any) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("产出不是合法 JSON: %v（%s）", err, body)
	}
	for k, v := range want {
		got, ok := m[k]
		if !ok {
			t.Fatalf("缺少顶层键 %q：%s", k, body)
		}
		if got != v {
			t.Fatalf("%s = %v, want %v：%s", k, got, v, body)
		}
	}
}

func TestComposeUpstreamRequestCrossProtocol(t *testing.T) {
	anthropicBody := []byte(`{"model":"claude-x","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)

	// 跨协议 anthropic→openai：模型名替换为上游名。
	body, err := ComposeUpstreamRequest(ProviderAnthropic, ProviderOpenAI, anthropicBody,
		UpstreamRequest{Model: "glm-5.3"})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, body, map[string]any{"model": "glm-5.3"})

	// 跨协议 anthropic→openai + thinking 意图：openai 上游消费（DeepSeek 形状）。
	body, err = ComposeUpstreamRequest(ProviderAnthropic, ProviderOpenAI, anthropicBody,
		UpstreamRequest{Model: "glm-5.3", Thinking: &ThinkingConfig{Type: "enabled", BudgetTokens: 2048}})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, body, map[string]any{"model": "glm-5.3"})
	m := map[string]any{} // 注意：复用 map 时 Unmarshal 只增不删，每例必须新建
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	th, ok := m["thinking"].(map[string]any)
	if !ok || th["type"] != "enabled" || th["budget_tokens"] != float64(2048) {
		t.Fatalf("openai 上游应写出 thinking（含 budget_tokens）：%s", body)
	}

	// 跨协议 anthropic→gemini + thinking 意图：gemini 上游忽略，body 无 thinking
	// 也无 model 字段（gemini 的模型名在 URL）。
	body, err = ComposeUpstreamRequest(ProviderAnthropic, ProviderGemini, anthropicBody,
		UpstreamRequest{Model: "gemini-x", Thinking: &ThinkingConfig{Type: "enabled"}})
	if err != nil {
		t.Fatal(err)
	}
	m = map[string]any{}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["model"]; ok {
		t.Fatalf("gemini 请求体不应含 model 字段：%s", body)
	}
	if _, ok := m["thinking"]; ok {
		t.Fatalf("gemini 上游不应消费 thinking：%s", body)
	}
}

func TestComposeUpstreamRequestSameProtocol(t *testing.T) {
	openaiBody := []byte(`{"model":"gpt-x","messages":[{"role":"user","content":"hi"}],"custom_ext":123}`)

	// 直通 openai→openai：model 替换 + thinking 注入，且未知字段 custom_ext 保真。
	body, err := ComposeUpstreamRequest(ProviderOpenAI, ProviderOpenAI, openaiBody,
		UpstreamRequest{Model: "deepseek-chat", Thinking: &ThinkingConfig{Type: "enabled"}})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, body, map[string]any{"model": "deepseek-chat", "custom_ext": float64(123)})
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if th, ok := m["thinking"].(map[string]any); !ok || th["type"] != "enabled" {
		t.Fatalf("openai 直通应注入 thinking：%s", body)
	}

	// 直通零覆盖：body 原样返回。
	body, err = ComposeUpstreamRequest(ProviderOpenAI, ProviderOpenAI, openaiBody, UpstreamRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(openaiBody) {
		t.Fatalf("零覆盖直通应原样返回：%s", body)
	}

	// 直通 anthropic→anthropic：顶层 model 替换，未知字段保真，thinking 不被注入
	// （anthropic 上游靠客户端自带配置透传）。
	anthropicBody := []byte(`{"model":"claude-x","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"metadata":{"a":1}}`)
	body, err = ComposeUpstreamRequest(ProviderAnthropic, ProviderAnthropic, anthropicBody,
		UpstreamRequest{Model: "claude-y", Thinking: &ThinkingConfig{Type: "enabled"}})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, body, map[string]any{"model": "claude-y"})
	m = map[string]any{}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["metadata"]; !ok {
		t.Fatalf("anthropic 直通未知字段应保真：%s", body)
	}
	if _, ok := m["thinking"]; ok {
		t.Fatalf("anthropic 直通不应注入 thinking：%s", body)
	}

	// 直通 gemini→gemini：model/thinking 覆盖均为 no-op，body 原样。
	geminiBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	body, err = ComposeUpstreamRequest(ProviderGemini, ProviderGemini, geminiBody,
		UpstreamRequest{Model: "gemini-y", Thinking: &ThinkingConfig{Type: "enabled"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(geminiBody) {
		t.Fatalf("gemini 直通覆盖应为 no-op，body 原样：%s", body)
	}
}
