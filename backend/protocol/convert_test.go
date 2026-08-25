package protocol

import (
	"encoding/json"
	"testing"
)

// unmarshalAny 反序列化输出为 map，供断言使用。
func unmarshalAny(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("反序列化失败: %v\n%s", err, b)
	}
	return m
}

// sliceAt 取出 m 里指定 key 的切片。
func sliceAt(t *testing.T, m map[string]any, key string) []any {
	t.Helper()
	v, ok := m[key].([]any)
	if !ok {
		t.Fatalf("字段 %q 不是数组: %v", key, m[key])
	}
	return v
}

func TestConvertRequestOpenAIToAnthropic(t *testing.T) {
	in := `{
		"model": "claude-3",
		"messages": [
			{"role": "system", "content": "you are helpful"},
			{"role": "user", "content": "hi"},
			{"role": "assistant", "content": "", "tool_calls": [{"id": "c1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\":\"sf\"}"}}]},
			{"role": "tool", "tool_call_id": "c1", "content": "sunny"}
		],
		"max_tokens": 100,
		"stop": ["\n"]
	}`
	out, err := ConvertRequest(ProviderOpenAI, ProviderAnthropic, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	if m["model"] != "claude-3" {
		t.Errorf("model 不匹配: %v", m["model"])
	}
	if m["system"] != "you are helpful" {
		t.Errorf("system 不匹配: %v", m["system"])
	}
	if m["max_tokens"].(float64) != 100 {
		t.Errorf("max_tokens 不匹配: %v", m["max_tokens"])
	}
	msgs := sliceAt(t, m, "messages")
	if len(msgs) != 3 {
		t.Fatalf("消息数不匹配: %d", len(msgs))
	}
	// user 消息
	user := msgs[0].(map[string]any)
	if user["role"] != "user" {
		t.Errorf("user 消息 role 不匹配: %v", user["role"])
	}
	// assistant 消息：content 空 + tool_use 块
	assistant := msgs[1].(map[string]any)
	blocks := assistant["content"].([]any)
	if len(blocks) != 1 || blocks[0].(map[string]any)["type"] != "tool_use" {
		t.Errorf("assistant tool_use 块不匹配: %v", assistant["content"])
	}
	// tool 消息 → user 消息里的 tool_result 块
	toolMsg := msgs[2].(map[string]any)
	toolBlocks := toolMsg["content"].([]any)
	tr := toolBlocks[0].(map[string]any)
	if tr["type"] != "tool_result" || tr["tool_use_id"] != "c1" {
		t.Errorf("tool_result 块不匹配: %v", tr)
	}
}

func TestConvertRequestOpenAIToGemini(t *testing.T) {
	in := `{
		"model": "gemini-2.5-flash",
		"messages": [
			{"role": "system", "content": "be brief"},
			{"role": "user", "content": "hello"}
		],
		"temperature": 0.7,
		"max_tokens": 50
	}`
	out, err := ConvertRequest(ProviderOpenAI, ProviderGemini, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	si := m["systemInstruction"].(map[string]any)
	parts := si["parts"].([]any)
	if parts[0].(map[string]any)["text"] != "be brief" {
		t.Errorf("systemInstruction 不匹配: %v", si)
	}
	contents := sliceAt(t, m, "contents")
	if len(contents) != 1 {
		t.Fatalf("contents 数量不匹配: %d", len(contents))
	}
	first := contents[0].(map[string]any)
	if first["role"] != "user" {
		t.Errorf("role 不匹配: %v", first["role"])
	}
	cfg := m["generationConfig"].(map[string]any)
	if cfg["maxOutputTokens"].(float64) != 50 {
		t.Errorf("maxOutputTokens 不匹配: %v", cfg["maxOutputTokens"])
	}
}

func TestConvertRequestOpenAIToResponses(t *testing.T) {
	in := `{
		"model": "gpt-4o",
		"messages": [
			{"role": "system", "content": "be brief"},
			{"role": "user", "content": "hi"}
		]
	}`
	out, err := ConvertRequest(ProviderOpenAI, ProviderOpenAIResponses, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	if m["instructions"] != "be brief" {
		t.Errorf("instructions 不匹配: %v", m["instructions"])
	}
	input := sliceAt(t, m, "input")
	if len(input) != 1 {
		t.Fatalf("input 数量不匹配: %d", len(input))
	}
	item := input[0].(map[string]any)
	if item["role"] != "user" {
		t.Errorf("input item role 不匹配: %v", item["role"])
	}
}

func TestConvertRequestAnthropicToOpenAI(t *testing.T) {
	in := `{
		"model": "claude-3",
		"system": [{"type": "text", "text": "you are helpful"}],
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "hi"}]},
			{"role": "assistant", "content": [{"type": "tool_use", "id": "c1", "name": "get_weather", "input": {"city": "sf"}}]},
			{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "c1", "content": "sunny"}]}
		],
		"max_tokens": 100
	}`
	out, err := ConvertRequest(ProviderAnthropic, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	msgs := sliceAt(t, m, "messages")
	// system + user + assistant + tool = 4
	if len(msgs) != 4 {
		t.Fatalf("消息数不匹配: %d", len(msgs))
	}
	system := msgs[0].(map[string]any)
	if system["role"] != "system" || system["content"] != "you are helpful" {
		t.Errorf("system 消息不匹配: %v", system)
	}
	assistant := msgs[2].(map[string]any)
	calls := assistant["tool_calls"].([]any)
	call := calls[0].(map[string]any)
	if call["id"] != "c1" {
		t.Errorf("tool_call id 不匹配: %v", call)
	}
	tool := msgs[3].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "c1" {
		t.Errorf("tool 消息不匹配: %v", tool)
	}
}

func TestConvertRequestGeminiToOpenAI(t *testing.T) {
	in := `{
		"systemInstruction": {"parts": [{"text": "be brief"}]},
		"contents": [{"role": "user", "parts": [{"text": "hello"}]}],
		"generationConfig": {"maxOutputTokens": 50}
	}`
	out, err := ConvertRequest(ProviderGemini, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	msgs := sliceAt(t, m, "messages")
	if len(msgs) != 2 {
		t.Fatalf("消息数不匹配: %d", len(msgs))
	}
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Errorf("system 消息 role 不匹配")
	}
	if msgs[1].(map[string]any)["content"] != "hello" {
		t.Errorf("user 消息 content 不匹配: %v", msgs[1])
	}
	if m["max_tokens"].(float64) != 50 {
		t.Errorf("max_tokens 不匹配: %v", m["max_tokens"])
	}
}

func TestConvertRequestResponsesToOpenAI(t *testing.T) {
	in := `{
		"model": "gpt-4o",
		"instructions": "be brief",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hi"}]}
		]
	}`
	out, err := ConvertRequest(ProviderOpenAIResponses, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	msgs := sliceAt(t, m, "messages")
	if len(msgs) != 2 {
		t.Fatalf("消息数不匹配: %d", len(msgs))
	}
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Errorf("system 消息 role 不匹配")
	}
	if msgs[1].(map[string]any)["content"] != "hi" {
		t.Errorf("user 消息 content 不匹配: %v", msgs[1])
	}
}

func TestConvertRequestIdentity(t *testing.T) {
	in := []byte(`{"model":"gpt-4o","messages":[]}`)
	out, err := ConvertRequest(ProviderOpenAI, ProviderOpenAI, in)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	if string(out) != string(in) {
		t.Errorf("恒等路径应原样返回: %s != %s", out, in)
	}
}

func TestConvertRequestInvalidProvider(t *testing.T) {
	if _, err := ConvertRequest(Provider("x"), ProviderOpenAI, []byte(`{}`)); err == nil {
		t.Error("非法源协议应报错")
	}
	if _, err := ConvertRequest(ProviderOpenAI, Provider("x"), []byte(`{}`)); err == nil {
		t.Error("非法目标协议应报错")
	}
}

func TestConvertResponseAnthropicToOpenAI(t *testing.T) {
	in := `{
		"id": "msg_1",
		"type": "message",
		"role": "assistant",
		"model": "claude-3",
		"content": [
			{"type": "text", "text": "hello"},
			{"type": "tool_use", "id": "c1", "name": "get_weather", "input": {"city": "sf"}}
		],
		"stop_reason": "tool_use",
		"usage": {"input_tokens": 10, "output_tokens": 5}
	}`
	out, err := ConvertResponse(ProviderAnthropic, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	if m["id"] != "msg_1" {
		t.Errorf("id 不匹配: %v", m["id"])
	}
	choices := sliceAt(t, m, "choices")
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "hello" {
		t.Errorf("content 不匹配: %v", msg["content"])
	}
	calls := msg["tool_calls"].([]any)
	if calls[0].(map[string]any)["function"].(map[string]any)["name"] != "get_weather" {
		t.Errorf("tool_call 不匹配: %v", calls)
	}
	if choices[0].(map[string]any)["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason 不匹配")
	}
	usage := m["usage"].(map[string]any)
	if usage["prompt_tokens"].(float64) != 10 || usage["completion_tokens"].(float64) != 5 {
		t.Errorf("usage 不匹配: %v", usage)
	}
}

func TestConvertResponseGeminiToOpenAI(t *testing.T) {
	in := `{
		"candidates": [{
			"content": {"role": "model", "parts": [{"text": "hello"}]},
			"finishReason": "STOP"
		}],
		"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15}
	}`
	out, err := ConvertResponse(ProviderGemini, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	choices := sliceAt(t, m, "choices")
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "hello" {
		t.Errorf("content 不匹配: %v", msg["content"])
	}
	if choices[0].(map[string]any)["finish_reason"] != "stop" {
		t.Errorf("finish_reason 不匹配")
	}
	usage := m["usage"].(map[string]any)
	if usage["total_tokens"].(float64) != 15 {
		t.Errorf("usage 不匹配: %v", usage)
	}
}

func TestConvertResponseResponsesToOpenAI(t *testing.T) {
	in := `{
		"id": "resp_1",
		"object": "response",
		"status": "completed",
		"model": "gpt-4o",
		"output": [
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "hi"}], "status": "completed"}
		],
		"usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}
	}`
	out, err := ConvertResponse(ProviderOpenAIResponses, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	choices := sliceAt(t, m, "choices")
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "hi" {
		t.Errorf("content 不匹配: %v", msg["content"])
	}
	if choices[0].(map[string]any)["finish_reason"] != "stop" {
		t.Errorf("finish_reason 不匹配")
	}
}

func TestConvertResponseOpenAIToAnthropic(t *testing.T) {
	in := `{
		"id": "chatcmpl-1",
		"object": "chat.completion",
		"model": "gpt-4o",
		"choices": [{
			"index": 0,
			"message": {"role": "assistant", "content": "hello"},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`
	out, err := ConvertResponse(ProviderOpenAI, ProviderAnthropic, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	if m["stop_reason"] != "end_turn" {
		t.Errorf("stop_reason 不匹配: %v", m["stop_reason"])
	}
	content := sliceAt(t, m, "content")
	if content[0].(map[string]any)["text"] != "hello" {
		t.Errorf("content 不匹配: %v", content)
	}
	usage := m["usage"].(map[string]any)
	if usage["input_tokens"].(float64) != 10 {
		t.Errorf("usage 不匹配: %v", usage)
	}
}

func TestConvertResponseOpenAIToGemini(t *testing.T) {
	in := `{
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "hello"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`
	out, err := ConvertResponse(ProviderOpenAI, ProviderGemini, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	candidates := sliceAt(t, m, "candidates")
	cand := candidates[0].(map[string]any)
	parts := cand["content"].(map[string]any)["parts"].([]any)
	if parts[0].(map[string]any)["text"] != "hello" {
		t.Errorf("content 不匹配: %v", parts)
	}
	if cand["finishReason"] != "STOP" {
		t.Errorf("finishReason 不匹配: %v", cand["finishReason"])
	}
}

func TestConvertResponseOpenAIToResponses(t *testing.T) {
	in := `{
		"id": "chatcmpl-1",
		"model": "gpt-4o",
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "hi"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`
	out, err := ConvertResponse(ProviderOpenAI, ProviderOpenAIResponses, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	if m["status"] != "completed" {
		t.Errorf("status 不匹配: %v", m["status"])
	}
	output := sliceAt(t, m, "output")
	msg := output[0].(map[string]any)
	if msg["type"] != "message" {
		t.Errorf("output item type 不匹配: %v", msg["type"])
	}
}

func TestConvertResponseIdentity(t *testing.T) {
	in := []byte(`{"id":"x","choices":[]}`)
	out, err := ConvertResponse(ProviderGemini, ProviderGemini, in)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	if string(out) != string(in) {
		t.Errorf("恒等路径应原样返回")
	}
}
