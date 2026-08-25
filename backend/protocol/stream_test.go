package protocol

import (
	"encoding/json"
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
