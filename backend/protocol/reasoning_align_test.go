package protocol

import (
	"testing"
)

// 本文件锁定 reasoning_content 的全协议对齐：Gemini thought 摘要与 Responses
// reasoning 输出经 OpenAI 规范格式中转后，与其他协议的思考内容同形状流转
// （下游转 Anthropic 即成 thinking 块）。

// TestConvertResponseGeminiThoughtToReasoningContent 断言 Gemini 非流式响应的
// thought parts（思考摘要）映射为 reasoning_content，正文 parts 不受影响。
func TestConvertResponseGeminiThoughtToReasoningContent(t *testing.T) {
	in := `{
		"candidates":[{"content":{"role":"model","parts":[
			{"text":"先分析用户意图","thought":true},
			{"text":"这是回答"}
		]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}
	}`
	out, err := ConvertResponse(ProviderGemini, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	msg := sliceAt(t, m, "choices")[0].(map[string]any)["message"].(map[string]any)
	if got, _ := msg["reasoning_content"].(string); got != "先分析用户意图" {
		t.Errorf("reasoning_content 应=先分析用户意图, 实际 %q", got)
	}
	if got, _ := msg["content"].(string); got != "这是回答" {
		t.Errorf("content 应=这是回答, 实际 %q", got)
	}
}

// TestStreamGeminiToOpenAIThoughtDelta 断言 Gemini 流式 thought part 增量映射为
// reasoning_content chunk，且经链式转换到 Anthropic 后成为 thinking 块。
func TestStreamGeminiToOpenAIThoughtDelta(t *testing.T) {
	conv, err := NewStreamConverter(ProviderGemini, ProviderOpenAI)
	if err != nil {
		t.Fatalf("构建转换器失败: %v", err)
	}
	outs, err := conv.Convert([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"思考增量","thought":true}]}}]}`))
	if err != nil {
		t.Fatalf("Convert 失败: %v", err)
	}
	var sawReasoning bool
	for _, o := range outs {
		c := parseChunk(t, o)
		if len(c.Choices) > 0 && c.Choices[0].Delta.ReasoningContent == "思考增量" {
			sawReasoning = true
		}
	}
	if !sawReasoning {
		t.Errorf("thought 增量应产出 reasoning_content chunk: %s", outs)
	}

	// 链到 Anthropic：thought 增量应成为 thinking 块
	chain, err := NewStreamConverter(ProviderGemini, ProviderAnthropic)
	if err != nil {
		t.Fatalf("构建链式转换器失败: %v", err)
	}
	events, err := chain.Convert([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"思考增量","thought":true}]}}]}`))
	if err != nil {
		t.Fatalf("链式 Convert 失败: %v", err)
	}
	var sawThinkingDelta bool
	for _, e := range events {
		ev := parseAnthropicEvent(t, e)
		if ev.Type == "content_block_delta" && ev.Delta != nil && ev.Delta.Type == "thinking_delta" && ev.Delta.Thinking == "思考增量" {
			sawThinkingDelta = true
		}
	}
	if !sawThinkingDelta {
		t.Errorf("Gemini thought 增量应转成 Anthropic thinking_delta: %s", events)
	}
}

// TestConvertResponseResponsesReasoningToReasoningContent 断言 Responses 非流式
// 响应的 reasoning item（summary 摘要）映射为 reasoning_content。
func TestConvertResponseResponsesReasoningToReasoningContent(t *testing.T) {
	in := `{
		"id":"resp_1","object":"response","status":"completed","model":"m",
		"output":[
			{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"想一下"}]},
			{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"答"}]}
		]
	}`
	out, err := ConvertResponse(ProviderOpenAIResponses, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	msg := sliceAt(t, m, "choices")[0].(map[string]any)["message"].(map[string]any)
	if got, _ := msg["reasoning_content"].(string); got != "想一下" {
		t.Errorf("reasoning_content 应=想一下, 实际 %q", got)
	}
	if got, _ := msg["content"].(string); got != "答" {
		t.Errorf("content 应=答, 实际 %q", got)
	}
}

// TestStreamResponsesToOpenAIReasoningDelta 断言 Responses 流式思考摘要增量映射为
// reasoning_content chunk，且经链式转换到 Anthropic 后成为 thinking 块。
func TestStreamResponsesToOpenAIReasoningDelta(t *testing.T) {
	conv, err := NewStreamConverter(ProviderOpenAIResponses, ProviderOpenAI)
	if err != nil {
		t.Fatalf("构建转换器失败: %v", err)
	}
	outs, err := conv.Convert([]byte(`{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"delta":"思考增量"}`))
	if err != nil {
		t.Fatalf("Convert 失败: %v", err)
	}
	var sawReasoning bool
	for _, o := range outs {
		c := parseChunk(t, o)
		if len(c.Choices) > 0 && c.Choices[0].Delta.ReasoningContent == "思考增量" {
			sawReasoning = true
		}
	}
	if !sawReasoning {
		t.Errorf("reasoning summary 增量应产出 reasoning_content chunk: %s", outs)
	}

	// 链到 Anthropic：reasoning 增量应成为 thinking 块
	chain, err := NewStreamConverter(ProviderOpenAIResponses, ProviderAnthropic)
	if err != nil {
		t.Fatalf("构建链式转换器失败: %v", err)
	}
	events, err := chain.Convert([]byte(`{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"delta":"思考增量"}`))
	if err != nil {
		t.Fatalf("链式 Convert 失败: %v", err)
	}
	var sawThinkingDelta bool
	for _, e := range events {
		ev := parseAnthropicEvent(t, e)
		if ev.Type == "content_block_delta" && ev.Delta != nil && ev.Delta.Type == "thinking_delta" && ev.Delta.Thinking == "思考增量" {
			sawThinkingDelta = true
		}
	}
	if !sawThinkingDelta {
		t.Errorf("Responses reasoning 增量应转成 Anthropic thinking_delta: %s", events)
	}
}
