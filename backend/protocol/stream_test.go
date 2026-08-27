package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// parseChunk 解析 OpenAI chunk。
func parseChunk(t *testing.T, b []byte) ChatCompletionChunk {
	t.Helper()
	var c ChatCompletionChunk
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("解析 chunk 失败: %v\n%s", err, b)
	}
	return c
}

// parseAnthropicEvent 解析 Anthropic 流事件。
func parseAnthropicEvent(t *testing.T, b []byte) MessagesStreamEvent {
	t.Helper()
	var ev MessagesStreamEvent
	if err := json.Unmarshal(b, &ev); err != nil {
		t.Fatalf("解析 anthropic 事件失败: %v\n%s", err, b)
	}
	return ev
}

func TestStreamAnthropicToOpenAI(t *testing.T) {
	conv, err := NewStreamConverter(ProviderAnthropic, ProviderOpenAI)
	if err != nil {
		t.Fatalf("构建转换器失败: %v", err)
	}

	// message_start
	o1, err := conv.Convert([]byte(`{"type":"message_start","message":{"id":"msg_1","model":"claude-3","role":"assistant","content":[],"usage":{"input_tokens":10}}}`))
	if err != nil {
		t.Fatalf("Convert message_start 失败: %v", err)
	}
	if len(o1) != 1 || parseChunk(t, o1[0]).Choices[0].Delta.Role != "assistant" {
		t.Errorf("message_start 应发出 role chunk: %s", o1)
	}

	// content_block_start (text) — 不产生输出
	if o, _ := conv.Convert([]byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)); len(o) != 0 {
		t.Errorf("content_block_start(text) 不应产生输出: %s", o)
	}

	// content_block_delta (text)
	o3, _ := conv.Convert([]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`))
	if len(o3) != 1 || parseChunk(t, o3[0]).Choices[0].Delta.Content != "hello" {
		t.Errorf("text_delta 应发出 content chunk: %s", o3)
	}

	// message_delta
	if _, err := conv.Convert([]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`)); err != nil {
		t.Fatalf("Convert message_delta 失败: %v", err)
	}

	// finish
	final, err := conv.Finish()
	if err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	if len(final) != 1 {
		t.Fatalf("Finish 应发出 1 个 chunk: %d", len(final))
	}
	c := parseChunk(t, final[0])
	if c.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason 不匹配: %v", c.Choices[0].FinishReason)
	}
	if c.Usage == nil || c.Usage.CompletionTokens != 5 {
		t.Errorf("usage 不匹配: %v", c.Usage)
	}
}

func TestStreamGeminiToOpenAI(t *testing.T) {
	conv, err := NewStreamConverter(ProviderGemini, ProviderOpenAI)
	if err != nil {
		t.Fatalf("构建转换器失败: %v", err)
	}

	o1, err := conv.Convert([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]}}]}`))
	if err != nil {
		t.Fatalf("Convert 失败: %v", err)
	}
	// 应发出 role chunk + content chunk
	if len(o1) != 2 {
		t.Fatalf("应发出 2 个 chunk: %d", len(o1))
	}
	if parseChunk(t, o1[0]).Choices[0].Delta.Role != "assistant" {
		t.Errorf("首个 chunk 应是 role: %s", o1[0])
	}
	if parseChunk(t, o1[1]).Choices[0].Delta.Content != "hello" {
		t.Errorf("content chunk 不匹配: %s", o1[1])
	}

	if _, err := conv.Convert([]byte(`{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`)); err != nil {
		t.Fatalf("Convert 失败: %v", err)
	}

	final, _ := conv.Finish()
	if len(final) != 1 {
		t.Fatalf("Finish 应发出 1 个 chunk: %d", len(final))
	}
	c := parseChunk(t, final[0])
	if c.Choices[0].FinishReason != "stop" || c.Usage.TotalTokens != 15 {
		t.Errorf("收尾 chunk 不匹配: %+v", c)
	}
}

func TestStreamOpenAIToAnthropic(t *testing.T) {
	conv, err := NewStreamConverter(ProviderOpenAI, ProviderAnthropic)
	if err != nil {
		t.Fatalf("构建转换器失败: %v", err)
	}

	var events []MessagesStreamEvent
	feed := func(payload string) {
		outs, err := conv.Convert([]byte(payload))
		if err != nil {
			t.Fatalf("Convert 失败: %v", err)
		}
		for _, o := range outs {
			events = append(events, parseAnthropicEvent(t, o))
		}
	}

	feed(`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`)
	feed(`{"choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`)
	feed(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)

	finals, err := conv.Finish()
	if err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	for _, o := range finals {
		events = append(events, parseAnthropicEvent(t, o))
	}

	types := make([]string, 0, len(events))
	for _, e := range events {
		types = append(types, e.Type)
	}
	want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if len(types) != len(want) {
		t.Fatalf("事件序列长度不匹配: %v", types)
	}
	for i, w := range want {
		if types[i] != w {
			t.Errorf("事件[%d] 不匹配: got %s want %s", i, types[i], w)
		}
	}
	// content_block_delta 的文本
	if events[2].Delta == nil || events[2].Delta.Text != "hello" {
		t.Errorf("content_block_delta 文本不匹配: %+v", events[2])
	}
	// message_delta 的 stop_reason
	if events[4].Delta == nil || events[4].Delta.StopReason != "end_turn" {
		t.Errorf("message_delta stop_reason 不匹配: %+v", events[4])
	}
}

func TestStreamIdentity(t *testing.T) {
	conv, err := NewStreamConverter(ProviderOpenAI, ProviderOpenAI)
	if err != nil {
		t.Fatalf("构建转换器失败: %v", err)
	}
	payload := []byte(`{"id":"x","choices":[{"delta":{"content":"y"}}]}`)
	out, err := conv.Convert(payload)
	if err != nil {
		t.Fatalf("Convert 失败: %v", err)
	}
	if len(out) != 1 || string(out[0]) != string(payload) {
		t.Errorf("恒等应原样返回: %s", out)
	}
}

func TestStreamInvalidProvider(t *testing.T) {
	if _, err := NewStreamConverter(Provider("x"), ProviderOpenAI); err == nil {
		t.Error("非法源协议应报错")
	}
}

// TestStreamOpenAIToAnthropicMessageFrames 断言整条流的序列化严格符合规范：
// message_start 的 stop_reason/stop_sequence 为 null、content 为 []；
// message_delta 带 delta.stop_reason、delta.stop_sequence:null、usage 键；
// text_delta 的 delta 不含 stop_sequence 键。
func TestStreamOpenAIToAnthropicMessageFrames(t *testing.T) {
	conv, err := NewStreamConverter(ProviderOpenAI, ProviderAnthropic)
	if err != nil {
		t.Fatalf("构建转换器失败: %v", err)
	}

	var rawEvents []string
	feed := func(payload string) {
		outs, err := conv.Convert([]byte(payload))
		if err != nil {
			t.Fatalf("Convert 失败: %v", err)
		}
		for _, o := range outs {
			rawEvents = append(rawEvents, string(o))
		}
	}
	feed(`{"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`)
	feed(`{"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`)
	feed(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	finals, err := conv.Finish()
	if err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	for _, o := range finals {
		rawEvents = append(rawEvents, string(o))
	}

	byType := map[string]string{}
	for _, raw := range rawEvents {
		ev := parseAnthropicEvent(t, []byte(raw))
		byType[ev.Type] = raw
	}

	// message_start：stop_reason/stop_sequence 必须是 null，content 必须是 []
	ms := byType["message_start"]
	if ms == "" {
		t.Fatal("未产生 message_start 事件")
	}
	if !strings.Contains(ms, `"stop_reason":null`) {
		t.Errorf("message_start 的 stop_reason 应为 null，实际: %s", ms)
	}
	if !strings.Contains(ms, `"stop_sequence":null`) {
		t.Errorf("message_start 的 stop_sequence 应为 null，实际: %s", ms)
	}
	if !strings.Contains(ms, `"content":[]`) {
		t.Errorf("message_start 的 content 应为 []，实际: %s", ms)
	}

	// message_delta：delta 里必须有 stop_reason + stop_sequence:null，usage 键必须存在
	md := byType["message_delta"]
	if md == "" {
		t.Fatal("未产生 message_delta 事件")
	}
	if !strings.Contains(md, `"stop_reason":"end_turn"`) {
		t.Errorf("message_delta 的 delta.stop_reason 应为 end_turn，实际: %s", md)
	}
	if !strings.Contains(md, `"stop_sequence":null`) {
		t.Errorf("message_delta 的 delta.stop_sequence 应为 null，实际: %s", md)
	}
	if !strings.Contains(md, `"usage"`) {
		t.Errorf("message_delta 缺 usage 键，实际: %s", md)
	}

	// content_block_delta（text_delta）不能带 stop_sequence 键（那是 message_delta 专属）
	cd := byType["content_block_delta"]
	if cd == "" {
		t.Fatal("未产生 content_block_delta 事件")
	}
	if strings.Contains(cd, "stop_sequence") {
		t.Errorf("content_block_delta 不应带 stop_sequence 键，实际: %s", cd)
	}
}

func TestStreamOpenAIToAnthropicToolUseHasInput(t *testing.T) {
	conv, err := NewStreamConverter(ProviderOpenAI, ProviderAnthropic)
	if err != nil {
		t.Fatalf("构建转换器失败: %v", err)
	}

	var rawEvents []string
	feed := func(payload string) {
		outs, err := conv.Convert([]byte(payload))
		if err != nil {
			t.Fatalf("Convert 失败: %v", err)
		}
		for _, o := range outs {
			rawEvents = append(rawEvents, string(o))
		}
	}
	feed(`{"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`)
	feed(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`)
	feed(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"Beijing\"}"}}]},"finish_reason":null}]}`)
	feed(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
	finals, err := conv.Finish()
	if err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	for _, o := range finals {
		rawEvents = append(rawEvents, string(o))
	}

	// 找到 tool_use 的 content_block_start，断言必须含 input 键（空时为 {}）
	for _, raw := range rawEvents {
		ev := parseAnthropicEvent(t, []byte(raw))
		if ev.Type == "content_block_start" && ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
			if !strings.Contains(raw, `"input"`) {
				t.Fatalf("tool_use 的 content_block_start 缺 input 字段，原始 JSON: %s", raw)
			}
			return
		}
	}
	t.Fatal("未产生 tool_use 的 content_block_start 事件")
}

func TestStreamOpenAIToAnthropicReasoningContent(t *testing.T) {
	conv, err := NewStreamConverter(ProviderOpenAI, ProviderAnthropic)
	if err != nil {
		t.Fatalf("构建转换器失败: %v", err)
	}

	var rawEvents []string
	feed := func(payload string) {
		outs, err := conv.Convert([]byte(payload))
		if err != nil {
			t.Fatalf("Convert 失败: %v", err)
		}
		for _, o := range outs {
			rawEvents = append(rawEvents, string(o))
		}
	}
	feed(`{"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`)
	// DeepSeek 扩展：reasoning_content 增量
	feed(`{"choices":[{"index":0,"delta":{"reasoning_content":"用户想要天气信息"},"finish_reason":null}]}`)
	feed(`{"choices":[{"index":0,"delta":{"content":"Beijing"},"finish_reason":null}]}`)
	feed(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	finals, err := conv.Finish()
	if err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	for _, o := range finals {
		rawEvents = append(rawEvents, string(o))
	}

	// 断言产生了 thinking 块与 thinking_delta
	var sawThinkingBlock, sawThinkingDelta bool
	for _, raw := range rawEvents {
		ev := parseAnthropicEvent(t, []byte(raw))
		if ev.Type == "content_block_start" && ev.ContentBlock != nil && ev.ContentBlock.Type == "thinking" {
			sawThinkingBlock = true
		}
		if ev.Type == "content_block_delta" && ev.Delta != nil && ev.Delta.Type == "thinking_delta" {
			if ev.Delta.Thinking != "用户想要天气信息" {
				t.Errorf("thinking_delta 文本不匹配: %+v", ev.Delta)
			}
			sawThinkingDelta = true
		}
	}
	if !sawThinkingBlock {
		t.Error("未产生 thinking 的 content_block_start")
	}
	if !sawThinkingDelta {
		t.Error("未产生 thinking_delta")
	}
}

func TestStreamOpenAIToAnthropicContentBlockHasIndex(t *testing.T) {
	conv, err := NewStreamConverter(ProviderOpenAI, ProviderAnthropic)
	if err != nil {
		t.Fatalf("构建转换器失败: %v", err)
	}

	var rawEvents []string
	feed := func(payload string) {
		outs, err := conv.Convert([]byte(payload))
		if err != nil {
			t.Fatalf("Convert 失败: %v", err)
		}
		for _, o := range outs {
			rawEvents = append(rawEvents, string(o))
		}
	}
	feed(`{"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`)
	feed(`{"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`)
	feed(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	finals, err := conv.Finish()
	if err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	for _, o := range finals {
		rawEvents = append(rawEvents, string(o))
	}

	// 找到 content_block_start 事件，断言 index 字段与 content_block 里的 "text" 键都存在（序列化层面）
	for _, raw := range rawEvents {
		ev := parseAnthropicEvent(t, []byte(raw))
		if ev.Type == "content_block_start" {
			if ev.Index == nil {
				t.Fatalf("content_block_start 缺少 index 字段，原始 JSON: %s", raw)
			}
			// text 块必须在序列化 JSON 里带 "text" 键（哪怕是空串），
			// 客户端据此拿到字符串字段，否则 e.slice 崩溃。
			// 用带冒号的 "text": 匹配，避免误匹配 type 字段的 "text" 值。
			if !strings.Contains(raw, `"text":`) {
				t.Fatalf("content_block_start 的 content_block 缺 text 字段，原始 JSON: %s", raw)
			}
			return
		}
	}
	t.Fatal("未产生 content_block_start 事件")
}
