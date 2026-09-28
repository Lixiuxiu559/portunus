package protocol

import (
	"encoding/json"
	"testing"
)

// thinking 兼容垫片回归测试：严格 thinking 上游（DeepSeek V4 等）要求多轮对话
// 把 assistant 的 reasoning_content 原样回传，客户端（Claude Code 辅助请求 /
// 上下文压缩后的历史）可能不带思考块，缺失即 400。
//
// 注入范围只限带 tool_calls 的 assistant 消息（见 thinking_compat.go 的粒度说明）。
// 本文件除锁定「该注入的注入了」，还锁定「不该动的没被动」——后者是缓存契约：
// 垫片每改写一条历史消息就改变一次前缀，被改写点之后的上游前缀缓存整段失效。

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

// TestInjectReasoningPlainTextAssistantUntouched 是本次收窄的核心契约：
// 纯文本 assistant（无 tool_calls）缺 reasoning_content 时**不得**注入占位。
// 严格上游只对带 tool_calls 的消息要求回传，对纯文本注入属于无谓改写——
// 真实对话里纯文本 assistant 远多于工具调用消息，一并注入会让前缀漂移面积成倍放大。
func TestInjectReasoningPlainTextAssistantUntouched(t *testing.T) {
	body := []byte(`{"model":"m","messages":[
		{"role":"user","content":"q"},
		{"role":"assistant","content":"a"},
		{"role":"assistant","content":[{"type":"text","text":"块数组正文"}]},
		{"role":"user","content":"next"}
	]}`)
	out, _, err := InjectReasoningPlaceholders(body)
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	if got := rcOf(t, out, 0); got != "" {
		t.Errorf("纯文本 assistant 不应被注入: got=%q\n%s", got, out)
	}
	if got := rcOf(t, out, 1); got != "" {
		t.Errorf("块数组纯文本 assistant 不应被注入: got=%q\n%s", got, out)
	}
	// 无任何改写时必须逐字节原样返回（连 map 往返的键序变化都不该发生）
	if string(out) != string(body) {
		t.Errorf("无改写时不应重新序列化:\n got %s\nwant %s", out, body)
	}
}

// TestInjectReasoningToolCallInjected 断言带 tool_calls 的 assistant 仍被注入——
// 这是严格上游真正报错（"reasoning_content is missing in assistant tool call
// message"）的场景，垫片的原始职责，不能因收窄而丢。
func TestInjectReasoningToolCallInjected(t *testing.T) {
	body := []byte(`{"model":"m","messages":[
		{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"ls","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"c1","content":"ok"},
		{"role":"assistant","content":"","tool_calls":[{"id":"c2","type":"function","function":{"name":"cat","arguments":"{}"}}],"reasoning_content":"真实思考"},
		{"role":"assistant","content":""},
		{"role":"user","content":"next"}
	]}`)
	out, _, err := InjectReasoningPlaceholders(body)
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	if got := rcOf(t, out, 0); got != ReasoningPlaceholder {
		t.Errorf("纯 tool_calls 助手未注入: got=%q\n%s", got, out)
	}
	// 已有真实思考的 tool_calls 助手不被占位覆盖
	if got := rcOf(t, out, 1); got != "真实思考" {
		t.Errorf("已有 reasoning_content 被覆盖: got=%q\n%s", got, out)
	}
	// 空助手（无 content、无 tool_calls）不注入，避免给无意义消息添字段
	if got := rcOf(t, out, 2); got != "" {
		t.Errorf("空 assistant 被注入: got=%q\n%s", got, out)
	}
}

// TestInjectReasoningEmptyToolCallsUntouched 断言 tool_calls 为空数组时不注入：
// 空数组表示没有工具调用，不属于「工具调用消息」。
func TestInjectReasoningEmptyToolCallsUntouched(t *testing.T) {
	body := []byte(`{"model":"m","messages":[
		{"role":"assistant","content":"a","tool_calls":[]}
	]}`)
	out, _, err := InjectReasoningPlaceholders(body)
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	if got := rcOf(t, out, 0); got != "" {
		t.Errorf("空 tool_calls 不应触发注入: got=%q\n%s", got, out)
	}
}

func TestInjectReasoningExistingKept(t *testing.T) {
	body := []byte(`{"model":"m","messages":[
		{"role":"assistant","content":"a","reasoning_content":"真实思考"}
	]}`)
	out, _, err := InjectReasoningPlaceholders(body)
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	if got := rcOf(t, out, 0); got != "真实思考" {
		t.Errorf("已有 reasoning_content 被覆盖: got=%q\n%s", got, out)
	}
}

// TestInjectReasoningUnknownFieldsPreserved 断言注入路径（发生改写、走 map 往返）
// 不丢 canonical 之外的未知字段。消息须带 tool_calls 才会真正触发注入。
func TestInjectReasoningUnknownFieldsPreserved(t *testing.T) {
	body := []byte(`{"model":"m","top_level_extra":{"a":1},"messages":[
		{"role":"assistant","content":"a","custom_field":"keep-me","tool_calls":[{"id":"c1","type":"function","function":{"name":"ls","arguments":"{}"}}]}
	]}`)
	out, _, err := InjectReasoningPlaceholders(body)
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	if got := rcOf(t, out, 0); got != ReasoningPlaceholder {
		t.Fatalf("前置条件不成立：带 tool_calls 的助手应被注入，got=%q\n%s", got, out)
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
	// 无 messages：原样返回，改写数为 0
	out, n, err := InjectReasoningPlaceholders([]byte(`{"model":"m"}`))
	if err != nil || string(out) != `{"model":"m"}` || n != 0 {
		t.Errorf("无 messages 应原样返回且改写数为 0: out=%s n=%d err=%v", out, n, err)
	}
	// 非法 JSON：报错
	if _, _, err := InjectReasoningPlaceholders([]byte(`not json`)); err == nil {
		t.Errorf("非法 JSON 应报错")
	}
}

// 组装钩子：ThinkingCompat 开启才注入，且仅 openai 兼容上游
func TestComposeUpstreamThinkingCompat(t *testing.T) {
	body := []byte(`{"model":"m","messages":[
		{"role":"user","content":"q"},
		{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"ls","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"c1","content":"ok"},
		{"role":"user","content":"next"}
	]}`)
	out, _, err := ComposeUpstreamRequest(ProviderOpenAI, ProviderOpenAI, body, UpstreamRequest{ThinkingCompat: true})
	if err != nil {
		t.Fatalf("组装失败: %v", err)
	}
	if got := rcOf(t, out, 0); got != ReasoningPlaceholder {
		t.Errorf("开启垫片未注入: got=%q\n%s", got, out)
	}

	out, _, err = ComposeUpstreamRequest(ProviderOpenAI, ProviderOpenAI, body, UpstreamRequest{})
	if err != nil {
		t.Fatalf("组装失败: %v", err)
	}
	if got := rcOf(t, out, 0); got != "" {
		t.Errorf("未开启垫片不应注入: got=%q\n%s", got, out)
	}

	// 跨协议（anthropic→openai）同样生效
	anthropicBody := []byte(`{"model":"m","messages":[
		{"role":"user","content":[{"type":"text","text":"q"}]},
		{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"ls","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"ok"}]},
		{"role":"user","content":[{"type":"text","text":"next"}]}
	]}`)
	out, _, err = ComposeUpstreamRequest(ProviderAnthropic, ProviderOpenAI, anthropicBody, UpstreamRequest{ThinkingCompat: true})
	if err != nil {
		t.Fatalf("组装失败: %v", err)
	}
	if got := rcOf(t, out, 0); got != ReasoningPlaceholder {
		t.Errorf("跨协议未注入: got=%q\n%s", got, out)
	}
}
