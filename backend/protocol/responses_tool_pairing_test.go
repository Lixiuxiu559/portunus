package protocol

import (
	"strings"
	"testing"
)

// 本文件锁定 Responses 请求方向并行工具调用的配对不变式：一轮里的多个
// function_call 是平铺的独立 item，转成 OpenAI 后必须合并进同一条 assistant
// 消息，其 tool_calls 才能被紧随的 tool 消息逐条回应。
//
// 此前每个 function_call 各自成一条 assistant 消息，落成
//
//	assistant(tool_calls=[call_A])
//	assistant(tool_calls=[call_B])
//	tool(call_A)
//	tool(call_B)
//
// 第一条 assistant 的 tool_calls 后面跟的是 assistant 而非 tool，严格上游
// （DeepSeek 等）报 400
// "An assistant message with 'tool_calls' must be followed by tool messages
// responding to each 'tool_call_id'. (insufficient tool messages ...)"。
// 单工具轮次不触发、并行（≥2）必触发——线上表现为「时好时坏」。

// assertToolPairing 校验 messages 里每条带 tool_calls 的 assistant 紧随的
// 连续 tool 消息覆盖其全部 tool_call_id（OpenAI 上游的硬约束）。
func assertToolPairing(t *testing.T, out []byte, msgs []any) {
	t.Helper()
	for i, raw := range msgs {
		msg, _ := raw.(map[string]any)
		tcs, _ := msg["tool_calls"].([]any)
		if len(tcs) == 0 {
			continue
		}
		want := map[string]bool{}
		for _, tc := range tcs {
			tcm, _ := tc.(map[string]any)
			id, _ := tcm["id"].(string)
			want[id] = true
		}
		got := map[string]bool{}
		for j := i + 1; j < len(msgs); j++ {
			nxt, _ := msgs[j].(map[string]any)
			if nxt["role"] != "tool" {
				break
			}
			id, _ := nxt["tool_call_id"].(string)
			got[id] = true
		}
		for id := range want {
			if !got[id] {
				t.Errorf("assistant[%d] 的 tool_call %q 后无对应 tool 消息 → 上游必报 400: %s", i, id, out)
			}
		}
	}
}

// TestRequestResponsesParallelToolCallsMerged 断言同轮多个平铺 function_call
// 合并为一条 assistant，且 tool_calls 与紧随 tool 消息逐条配对。
func TestRequestResponsesParallelToolCallsMerged(t *testing.T) {
	in := `{
		"model": "flash",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "读两个文件"}]},
			{"type": "reasoning", "id": "rs_1", "summary": [{"type": "summary_text", "text": "先并行读"}]},
			{"type": "function_call", "call_id": "call_00_A", "name": "Read", "arguments": "{\"file_path\":\"/tmp/a\"}"},
			{"type": "function_call", "call_id": "call_01_B", "name": "Read", "arguments": "{\"file_path\":\"/tmp/b\"}"},
			{"type": "function_call_output", "call_id": "call_00_A", "output": "aaa"},
			{"type": "function_call_output", "call_id": "call_01_B", "output": "bbb"},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "总结"}]}
		]
	}`
	out, err := ConvertRequest(ProviderOpenAIResponses, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	msgs := sliceAt(t, m, "messages")

	assertToolPairing(t, out, msgs)

	// 两个并行 function_call 必须落在同一条 assistant 消息里
	var merged int
	for _, raw := range msgs {
		msg := raw.(map[string]any)
		if msg["role"] != "assistant" {
			continue
		}
		if tcs, _ := msg["tool_calls"].([]any); len(tcs) == 2 {
			merged++
		}
	}
	if merged != 1 {
		t.Errorf("两个并行 function_call 应合并为恰好一条 assistant（含 2 个 tool_calls），实际 %d 条: %s", merged, out)
	}
}

// TestRequestResponsesParallelToolCallsReasoningAttached 断言 reasoning item
// 的思考文本附挂到合并后的那条 assistant（而非只落第一条）。
func TestRequestResponsesParallelToolCallsReasoningAttached(t *testing.T) {
	in := `{
		"model": "flash",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "读两个文件"}]},
			{"type": "reasoning", "id": "rs_1", "summary": [{"type": "summary_text", "text": "先并行读"}]},
			{"type": "function_call", "call_id": "call_00_A", "name": "Read", "arguments": "{}"},
			{"type": "function_call", "call_id": "call_01_B", "name": "Read", "arguments": "{}"},
			{"type": "function_call_output", "call_id": "call_00_A", "output": "aaa"},
			{"type": "function_call_output", "call_id": "call_01_B", "output": "bbb"}
		]
	}`
	out, err := ConvertRequest(ProviderOpenAIResponses, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	msgs := sliceAt(t, m, "messages")

	var sawRC bool
	for _, raw := range msgs {
		msg := raw.(map[string]any)
		if tcs, _ := msg["tool_calls"].([]any); len(tcs) == 2 {
			if rc, _ := msg["reasoning_content"].(string); rc == "先并行读" {
				sawRC = true
			}
		}
	}
	if !sawRC {
		t.Errorf("reasoning 应附挂到合并后的 assistant（含 2 个 tool_calls），messages=%s", out)
	}
}

// TestRequestResponsesSequentialToolCallTurnsSeparate 断言分属不同轮次
// （各自 function_call_output 隔开）的 function_call 不被错误合并——合并的
// 边界是「连续 function_call」，tool 结果到来即 flush。
func TestRequestResponsesSequentialToolCallTurnsSeparate(t *testing.T) {
	in := `{
		"model": "flash",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "读文件"}]},
			{"type": "function_call", "call_id": "call_1", "name": "Read", "arguments": "{}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "aaa"},
			{"type": "function_call", "call_id": "call_2", "name": "Read", "arguments": "{}"},
			{"type": "function_call_output", "call_id": "call_2", "output": "bbb"}
		]
	}`
	out, err := ConvertRequest(ProviderOpenAIResponses, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	msgs := sliceAt(t, m, "messages")

	assertToolPairing(t, out, msgs)

	var assistantWithTools int
	for _, raw := range msgs {
		msg := raw.(map[string]any)
		if tcs, _ := msg["tool_calls"].([]any); len(tcs) > 0 {
			assistantWithTools++
			if len(tcs) != 1 {
				t.Errorf("跨轮次的 function_call 不应合并，实际一条 assistant 带 %d 个 tool_calls: %s", len(tcs), out)
			}
		}
	}
	if assistantWithTools != 2 {
		t.Errorf("两轮各一个 function_call 应落成 2 条 assistant，实际 %d 条: %s", assistantWithTools, out)
	}
}

// TestRequestResponsesEmptyReasoningNoSeparator 断言空文本 reasoning item
// （encrypted-only 的常见形状，summary:[] + content:null）不会凭空追加 "\n\n"
// 分隔符污染上一段真实思考内容。
func TestRequestResponsesEmptyReasoningNoSeparator(t *testing.T) {
	in := `{
		"model": "flash",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "问"}]},
			{"type": "reasoning", "id": "rs_1", "summary": [{"type": "summary_text", "text": "真实思考"}]},
			{"type": "reasoning", "id": "rs_2", "summary": [], "content": null, "encrypted_content": "gAAAAA..."},
			{"type": "function_call", "call_id": "call_1", "name": "ls", "arguments": "{}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "ok"}
		]
	}`
	out, err := ConvertRequest(ProviderOpenAIResponses, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	msgs := sliceAt(t, m, "messages")

	for _, raw := range msgs {
		msg := raw.(map[string]any)
		rc, _ := msg["reasoning_content"].(string)
		if strings.HasSuffix(rc, "\n\n") {
			t.Errorf("空 reasoning item 不应追加悬挂的 \\n\\n 分隔符，实际 %q: %s", rc, out)
		}
	}
}
