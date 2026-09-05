package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestStreamResponsesToolUseToAnthropic 覆盖 Claude Code(Anthropic 客户端) 走
// Responses 上游时的工具调用流转换：
// response.output_item.added(function_call) → tool_use 块
// response.function_call_arguments.delta → input_json_delta
// response.completed(output 含 function_call) → stop_reason=tool_use
func TestStreamResponsesToolUseToAnthropic(t *testing.T) {
	conv, err := NewStreamConverter(ProviderOpenAIResponses, ProviderAnthropic)
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
	feed(`{"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress","model":"gpt-4o"}}`)
	feed(`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_xyz","name":"get_weather","arguments":""}}`)
	feed(`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{\"city\": \"Bei"}`)
	feed(`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"jing\"}"}`)
	feed(`{"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"gpt-4o"}}`)
	finals, err := conv.Finish()
	if err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	for _, o := range finals {
		rawEvents = append(rawEvents, string(o))
	}

	var sawToolStart, sawArgDelta, sawToolStop bool
	var stopReason string
	var args string
	for _, raw := range rawEvents {
		ev := parseAnthropicEvent(t, []byte(raw))
		switch ev.Type {
		case "content_block_start":
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
				sawToolStart = true
				if ev.ContentBlock.Name != "get_weather" || ev.ContentBlock.ID != "call_xyz" {
					t.Errorf("tool_use 块 name/id 不匹配: %+v", ev.ContentBlock)
				}
			}
		case "content_block_delta":
			if ev.Delta != nil && ev.Delta.Type == "input_json_delta" {
				sawArgDelta = true
				args += ev.Delta.PartialJSON
			}
		case "content_block_stop":
			sawToolStop = true
		case "message_delta":
			if ev.Delta != nil {
				stopReason = ev.Delta.StopReason
			}
		}
	}
	if !sawToolStart {
		t.Error("未产生 tool_use 的 content_block_start")
	}
	if !sawArgDelta {
		t.Error("未产生 input_json_delta")
	}
	if args != `{"city": "Beijing"}` {
		t.Errorf("拼装的参数不完整: %q", args)
	}
	if !sawToolStop {
		t.Error("未产生 tool_use 的 content_block_stop")
	}
	if stopReason != "tool_use" {
		t.Errorf("stop_reason 应为 tool_use, 实际 %q", stopReason)
	}
}

// TestStreamResponsesToolUseToOpenAI 验证 responses→openai 直转方向也保留工具调用 chunk。
func TestStreamResponsesToolUseToOpenAI(t *testing.T) {
	conv, err := NewStreamConverter(ProviderOpenAIResponses, ProviderOpenAI)
	if err != nil {
		t.Fatalf("构建转换器失败: %v", err)
	}

	var chunks []ChatCompletionChunk
	feed := func(payload string) {
		outs, err := conv.Convert([]byte(payload))
		if err != nil {
			t.Fatalf("Convert 失败: %v", err)
		}
		for _, o := range outs {
			chunks = append(chunks, parseChunk(t, o))
		}
	}
	feed(`{"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress","model":"gpt-4o"}}`)
	feed(`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_xyz","name":"get_weather","arguments":""}}`)
	feed(`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{\"city\": \"Bei"}`)
	feed(`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"jing\"}"}`)
	feed(`{"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"gpt-4o"}}`)
	finals, err := conv.Finish()
	if err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	for _, o := range finals {
		chunks = append(chunks, parseChunk(t, o))
	}
	for _, c := range chunks {
		for _, tc := range c.Choices[0].Delta.ToolCalls {
			if tc.ID == "call_xyz" && tc.Function.Name == "get_weather" {
				return // 找到了工具调用开始 chunk
			}
		}
	}
	t.Errorf("未产生带 call_xyz 的 tool_calls chunk，全部输出: %s", dumpChunks(chunks))
}

func dumpChunks(chunks []ChatCompletionChunk) string {
	var sb strings.Builder
	for _, c := range chunks {
		b, _ := json.Marshal(c)
		sb.Write(b)
		sb.WriteString("\n")
	}
	return sb.String()
}
