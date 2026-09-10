package protocol

import (
	"encoding/json"
	"testing"
)

// thinking 兼容垫片回归测试：严格 thinking 上游（DeepSeek V4 等）要求多轮对话
// 把 assistant 的 reasoning_content 原样回传，客户端（Claude Code 辅助请求 /
// 上下文压缩后的历史）可能不带思考块，缺失即 400
// "The reasoning_content in the thinking mode must be passed back to the API"。
// 垫片对缺 reasoning_content 且有实际内容的 assistant 历史注入占位。

func rcOf(t *testing.T, body []byte, index int) string {
	t.Helper()
	var m struct {
		Messages []struct {
			Role             string `json:"role"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("解析失败: %v\n%s", err, body)
	}
	n := 0
	for _, msg := range m.Messages {
		if msg.Role == "assistant" {
			if n == index {
				return msg.ReasoningContent
			}
			n++
		}
	}
	t.Fatalf("第 %d 条 assistant 不存在\n%s", index, body)
	return ""
}

func TestInjectReasoningMissing(t *testing.T) {
	body := []byte(`{"model":"m","messages":[
		{"role":"user","content":"q"},
		{"role":"assistant","content":"a"},
		{"role":"user","content":"next"}
	]}`)
	out, err := InjectReasoningPlaceholders(body)
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	if got := rcOf(t, out, 0); got != ReasoningPlaceholder {
		t.Errorf("assistant 缺 reasoning_content 未注入: got=%q\n%s", got, out)
	}
}

func TestInjectReasoningExistingKept(t *testing.T) {
	body := []byte(`{"model":"m","messages":[
		{"role":"assistant","content":"a","reasoning_content":"真实思考"}
	]}`)
	out, err := InjectReasoningPlaceholders(body)
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	if got := rcOf(t, out, 0); got != "真实思考" {
		t.Errorf("已有 reasoning_content 被覆盖: got=%q\n%s", got, out)
	}
}

func TestInjectReasoningToolCallsAndEmptyAssistant(t *testing.T) {
	body := []byte(`{"model":"m","messages":[
		{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"ls","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"c1","content":"ok"},
		{"role":"assistant","content":""},
		{"role":"user","content":"next"}
	]}`)
	out, err := InjectReasoningPlaceholders(body)
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	if got := rcOf(t, out, 0); got != ReasoningPlaceholder {
		t.Errorf("纯 tool_calls 助手未注入: got=%q\n%s", got, out)
	}
	// 空助手（无 content、无 tool_calls）不注入，避免给无意义消息添字段
	if got := rcOf(t, out, 1); got != "" {
		t.Errorf("空 assistant 被注入: got=%q\n%s", got, out)
	}
}

func TestInjectReasoningUnknownFieldsPreserved(t *testing.T) {
	body := []byte(`{"model":"m","top_level_extra":{"a":1},"messages":[
		{"role":"assistant","content":"a","custom_field":"keep-me"}
	]}`)
	out, err := InjectReasoningPlaceholders(body)
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if _, ok := m["top_level_extra"]; !ok {
		t.Errorf("顶层未知字段丢失\n%s", out)
	}
	msgs := m["messages"].([]any)
	if msgs[0].(map[string]any)["custom_field"] != "keep-me" {
		t.Errorf("消息内未知字段丢失\n%s", out)
	}
}

func TestInjectReasoningNoMessagesAndInvalid(t *testing.T) {
	// 无 messages：原样返回
	out, err := InjectReasoningPlaceholders([]byte(`{"model":"m"}`))
	if err != nil || string(out) != `{"model":"m"}` {
		t.Errorf("无 messages 应原样返回: out=%s err=%v", out, err)
	}
	// 非法 JSON：报错
	if _, err := InjectReasoningPlaceholders([]byte(`not json`)); err == nil {
		t.Errorf("非法 JSON 应报错")
	}
}

// 组装钩子：ThinkingCompat 开启才注入，且仅 openai 兼容上游
func TestComposeUpstreamThinkingCompat(t *testing.T) {
	body := []byte(`{"model":"m","messages":[
		{"role":"user","content":"q"},{"role":"assistant","content":"a"},{"role":"user","content":"next"}
	]}`)
	out, err := ComposeUpstreamRequest(ProviderOpenAI, ProviderOpenAI, body, UpstreamRequest{ThinkingCompat: true})
	if err != nil {
		t.Fatalf("组装失败: %v", err)
	}
	if got := rcOf(t, out, 0); got != ReasoningPlaceholder {
		t.Errorf("开启垫片未注入: got=%q\n%s", got, out)
	}

	out, err = ComposeUpstreamRequest(ProviderOpenAI, ProviderOpenAI, body, UpstreamRequest{})
	if err != nil {
		t.Fatalf("组装失败: %v", err)
	}
	if got := rcOf(t, out, 0); got != "" {
		t.Errorf("未开启垫片不应注入: got=%q\n%s", got, out)
	}

	// 跨协议（anthropic→openai）同样生效
	anthropicBody := []byte(`{"model":"m","messages":[
		{"role":"user","content":[{"type":"text","text":"q"}]},
		{"role":"assistant","content":[{"type":"text","text":"a"}]},
		{"role":"user","content":[{"type":"text","text":"next"}]}
	]}`)
	out, err = ComposeUpstreamRequest(ProviderAnthropic, ProviderOpenAI, anthropicBody, UpstreamRequest{ThinkingCompat: true})
	if err != nil {
		t.Fatalf("组装失败: %v", err)
	}
	if got := rcOf(t, out, 0); got != ReasoningPlaceholder {
		t.Errorf("跨协议未注入: got=%q\n%s", got, out)
	}
}
