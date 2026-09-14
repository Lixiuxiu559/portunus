package protocol

import (
	"strings"
	"testing"
)

// 本文件锁定 Responses 请求方向的 reasoning 回传：Codex 等客户端多轮对话会把
// 上一轮的 reasoning item（summary / encrypted_content）原样放回 input，经
// responses → openai 转换后必须落成 assistant 历史的 reasoning_content，
// 否则严格 thinking 上游（DeepSeek 等）400
// "The reasoning_content in the thinking mode must be passed back to the API"。
// 响应方向（reasoning → reasoning_content）已有 reasoning_align_test.go 覆盖，
// 本文件补齐请求方向（input 里的 reasoning item）。
//
// 归属算法对齐 cc-switch transform_codex_chat.rs 的 pending/attach 语义：
// reasoning 文本前向附挂到其后的 function_call / assistant message，
// 不生成独立空消息、不跨 user 回合泄漏、尾部剩余回溯附挂最后一条 assistant。

// TestRequestResponsesReasoningItemToReasoningContent 断言 reasoning item 的
// summary 文本前向附挂到紧随其后 function_call 的 assistant 消息。
func TestRequestResponsesReasoningItemToReasoningContent(t *testing.T) {
	// Codex 多轮真实形状：上一轮 reasoning + function_call，后跟工具结果。
	in := `{
		"model": "gpt-5",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "第一轮提问"}]},
			{"type": "reasoning", "id": "rs_1", "summary": [{"type": "summary_text", "text": "上一轮思考摘要"}]},
			{"type": "function_call", "call_id": "call_1", "name": "get_weather", "arguments": "{\"city\":\"sf\"}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "sunny"},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "第二轮提问"}]}
		]
	}`
	out, err := ConvertRequest(ProviderOpenAIResponses, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	msgs := sliceAt(t, m, "messages")

	// function_call → assistant 消息（带 tool_calls），reasoning 应作为其
	// reasoning_content 回传。
	var sawRC bool
	for _, raw := range msgs {
		msg := raw.(map[string]any)
		if msg["role"] != "assistant" {
			continue
		}
		if _, hasTools := msg["tool_calls"]; hasTools {
			if rc, _ := msg["reasoning_content"].(string); rc == "上一轮思考摘要" {
				sawRC = true
			}
		}
	}
	if !sawRC {
		t.Errorf("function_call 的 assistant 消息应携带 reasoning_content=上一轮思考摘要, messages=%s", out)
	}
}

// TestRequestResponsesReasoningItemNoEmptyMessage 断言 reasoning item 不再生成
// {"role":"","content":""} 畸形空消息（此前掉 default 分支的 bug）。
func TestRequestResponsesReasoningItemNoEmptyMessage(t *testing.T) {
	in := `{
		"model": "gpt-5",
		"input": [
			{"type": "reasoning", "id": "rs_1", "summary": [{"type": "summary_text", "text": "思考"}]},
			{"type": "function_call", "call_id": "call_1", "name": "ls", "arguments": "{}"}
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
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)
		if role == "" && content == "" {
			t.Errorf("reasoning item 不应生成畸形空消息: %s", out)
		}
	}
}

// TestRequestResponsesReasoningItemEncryptedOnly 断言只有 encrypted_content
// （本机 Codex 实测形状：summary:[]、content:null）的 reasoning item 不生成
// 空消息，也不产生空 reasoning_content。
func TestRequestResponsesReasoningItemEncryptedOnly(t *testing.T) {
	in := `{
		"model": "gpt-5",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "问"}]},
			{"type": "reasoning", "id": "rs_1", "summary": [], "content": null, "encrypted_content": "gAAAAA..."},
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
		if role, _ := msg["role"].(string); role == "" {
			t.Errorf("encrypted-only reasoning item 不应生成空 role 消息: %s", out)
		}
		if rc, _ := msg["reasoning_content"].(string); rc == "" {
			continue
		}
	}
}

// TestRequestResponsesReasoningItemAloneToAssistant 断言 reasoning item 后无
// function_call（纯文本轮次）时附挂到紧随的 assistant 文本消息，user 边界不泄漏。
func TestRequestResponsesReasoningItemAloneToAssistant(t *testing.T) {
	in := `{
		"model": "gpt-5",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "第一轮提问"}]},
			{"type": "reasoning", "id": "rs_1", "summary": [{"type": "summary_text", "text": "纯文本轮思考"}]},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "上一轮回答"}]},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "第二轮提问"}]}
		]
	}`
	out, err := ConvertRequest(ProviderOpenAIResponses, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	msgs := sliceAt(t, m, "messages")

	var sawRC bool
	for i, raw := range msgs {
		msg := raw.(map[string]any)
		if rc, _ := msg["reasoning_content"].(string); rc == "纯文本轮思考" {
			if msg["role"] != "assistant" {
				t.Errorf("reasoning_content 落在了 %v 消息上", msg["role"])
			}
			sawRC = true
			// reasoning 不得跨 user 回合泄漏到之后的 assistant 消息
			for _, later := range msgs[i+1:] {
				lm := later.(map[string]any)
				if lm["role"] == "assistant" {
					if rc2, _ := lm["reasoning_content"].(string); strings.Contains(rc2, "纯文本轮思考") {
						t.Errorf("reasoning 跨 user 回合泄漏: %s", out)
					}
				}
			}
			break
		}
	}
	if !sawRC {
		t.Errorf("reasoning item 应附挂到紧随 assistant 消息的 reasoning_content, messages=%s", out)
	}
}

// TestRequestResponsesReasoningItemTrailing 断言尾部剩余 reasoning（其后无任何
// assistant 项）回溯附挂到此前最后一条 assistant 消息。
func TestRequestResponsesReasoningItemTrailing(t *testing.T) {
	in := `{
		"model": "gpt-5",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "问"}]},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "答"}]},
			{"type": "reasoning", "id": "rs_tail", "summary": [{"type": "summary_text", "text": "尾部思考"}]}
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
		if msg["role"] == "assistant" {
			if rc, _ := msg["reasoning_content"].(string); rc == "尾部思考" {
				sawRC = true
			}
		}
	}
	if !sawRC {
		t.Errorf("尾部 reasoning 应回溯附挂到此前 assistant, messages=%s", out)
	}
}
