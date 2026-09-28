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
	body, _, err := ComposeUpstreamRequest(ProviderAnthropic, ProviderOpenAI, anthropicBody,
		UpstreamRequest{Model: "glm-5.3"})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, body, map[string]any{"model": "glm-5.3"})

	// 跨协议 anthropic→openai + thinking 意图：openai 上游消费（DeepSeek 形状）。
	body, _, err = ComposeUpstreamRequest(ProviderAnthropic, ProviderOpenAI, anthropicBody,
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
	body, _, err = ComposeUpstreamRequest(ProviderAnthropic, ProviderGemini, anthropicBody,
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

// TestComposeUpstreamRequestStats 断言装配遥测如实反映「桥接命中」与「垫片改写」。
// 这两项是判断缓存问题是否真被修复的唯一可测量依据（此前只能靠猜），必须锁定。
func TestComposeUpstreamRequestStats(t *testing.T) {
	const reasoning = "首轮的原始思考内容"
	sig := EncodeReasoningSignature(reasoning)

	// 第 2 条 assistant 带桥载荷签名 → BridgeDecoded=1；
	// 第 4 条 assistant 带 tool_use 但无思考 → 垫片注入 → ShimRewrites=1。
	// 第 2 条本身不被垫片改写（已有 reasoning_content）。
	body := []byte(`{"model":"m","max_tokens":64,"messages":[
		{"role":"user","content":[{"type":"text","text":"hi"}]},
		{"role":"assistant","content":[
			{"type":"thinking","thinking":"被客户端改写的明文","signature":"` + sig + `"},
			{"type":"text","text":"答案"}
		]},
		{"role":"user","content":[{"type":"text","text":"继续"}]},
		{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"ls","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"ok"}]}
	]}`)
	_, stats, err := ComposeUpstreamRequest(ProviderAnthropic, ProviderOpenAI, body,
		UpstreamRequest{Model: "deepseek-v4-pro", ThinkingCompat: true})
	if err != nil {
		t.Fatal(err)
	}
	if stats.BridgeDecoded != 1 {
		t.Errorf("BridgeDecoded = %d, want 1（签名里的桥载荷应被还原）", stats.BridgeDecoded)
	}
	if stats.ShimRewrites != 1 {
		t.Errorf("ShimRewrites = %d, want 1（仅带 tool_calls 那条应被垫片改写）", stats.ShimRewrites)
	}

	// 客户端没回传桥载荷（签名是旧的常量占位）→ BridgeDecoded=0：
	// 这正是「桥接未生效」的信号，也是本轮改动要证伪/证实的核心观测点。
	plain := []byte(`{"model":"m","max_tokens":64,"messages":[
		{"role":"assistant","content":[{"type":"thinking","thinking":"明文","signature":"sig"},{"type":"text","text":"a"}]}
	]}`)
	_, stats, err = ComposeUpstreamRequest(ProviderAnthropic, ProviderOpenAI, plain,
		UpstreamRequest{ThinkingCompat: true})
	if err != nil {
		t.Fatal(err)
	}
	if stats.BridgeDecoded != 0 {
		t.Errorf("非桥签名不应计入 BridgeDecoded，实际 %d", stats.BridgeDecoded)
	}
	if stats.ShimRewrites != 0 {
		t.Errorf("纯文本 assistant 不应被垫片改写，实际 %d", stats.ShimRewrites)
	}

	// 同协议直通不解析消息：桥接遥测恒为 0（无跨协议重建，也就无所谓命中）。
	direct := []byte(`{"model":"m","messages":[{"role":"assistant","content":"a"}]}`)
	_, stats, err = ComposeUpstreamRequest(ProviderOpenAI, ProviderOpenAI, direct,
		UpstreamRequest{ThinkingCompat: true})
	if err != nil {
		t.Fatal(err)
	}
	if stats.BridgeDecoded != 0 || stats.ShimRewrites != 0 {
		t.Errorf("同协议直通遥测应为零值，实际 %+v", stats)
	}
}

func TestComposeUpstreamRequestSameProtocol(t *testing.T) {
	openaiBody := []byte(`{"model":"gpt-x","messages":[{"role":"user","content":"hi"}],"custom_ext":123}`)

	// 直通 openai→openai：model 替换 + thinking 注入，且未知字段 custom_ext 保真。
	body, _, err := ComposeUpstreamRequest(ProviderOpenAI, ProviderOpenAI, openaiBody,
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
	body, _, err = ComposeUpstreamRequest(ProviderOpenAI, ProviderOpenAI, openaiBody, UpstreamRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(openaiBody) {
		t.Fatalf("零覆盖直通应原样返回：%s", body)
	}

	// 直通 anthropic→anthropic：顶层 model 替换，未知字段保真，thinking 不被注入
	// （anthropic 上游靠客户端自带配置透传）。
	anthropicBody := []byte(`{"model":"claude-x","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"metadata":{"a":1}}`)
	body, _, err = ComposeUpstreamRequest(ProviderAnthropic, ProviderAnthropic, anthropicBody,
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
	body, _, err = ComposeUpstreamRequest(ProviderGemini, ProviderGemini, geminiBody,
		UpstreamRequest{Model: "gemini-y", Thinking: &ThinkingConfig{Type: "enabled"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(geminiBody) {
		t.Fatalf("gemini 直通覆盖应为 no-op，body 原样：%s", body)
	}
}
