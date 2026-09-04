package protocol

import (
	"encoding/json"
	"strings"
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
	// DeepSeek 等上游要求 tool 消息必带 name（对应 tool_use 块的 name）
	if tool["name"] != "get_weather" {
		t.Errorf("tool 消息 name 不匹配: %v", tool["name"])
	}
}

// TestConvertRequestAnthropicToOpenAIStreamOptions 断言流式请求转 OpenAI 时补 stream_options.include_usage，
// 否则 OpenAI 兼容上游默认不返回 usage chunk，客户端看不到上下文占用。
func TestConvertRequestAnthropicToOpenAIStreamOptions(t *testing.T) {
	// 流式：应补 include_usage=true
	in := `{
		"model": "claude-3",
		"stream": true,
		"messages": [{"role": "user", "content": "hi"}],
		"max_tokens": 100
	}`
	out, err := ConvertRequest(ProviderAnthropic, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	so, ok := m["stream_options"].(map[string]any)
	if !ok {
		t.Fatalf("流式请求应补 stream_options，输出: %s", out)
	}
	if so["include_usage"] != true {
		t.Errorf("stream_options.include_usage 应为 true，实际: %v", so)
	}

	// 非流式：不应补
	inNonStream := `{
		"model": "claude-3",
		"messages": [{"role": "user", "content": "hi"}],
		"max_tokens": 100
	}`
	out2, err := ConvertRequest(ProviderAnthropic, ProviderOpenAI, []byte(inNonStream))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m2 := unmarshalAny(t, out2)
	if _, ok := m2["stream_options"]; ok {
		t.Errorf("非流式请求不应补 stream_options，输出: %s", out2)
	}
}

// TestConvertRequestOpenAIIgnoreStreamOptions 断言 OpenAI→OpenAI 直通时补逻辑不覆盖
// 请求里已有的 stream_options（如显式 include_usage=false）。
func TestConvertRequestOpenAIIgnoreStreamOptions(t *testing.T) {
	in := `{
		"model": "gpt-4o",
		"stream": true,
		"stream_options": {"include_usage": false},
		"messages": [{"role": "user", "content": "hi"}]
	}`
	// from==to 直通，原样返回
	out, err := ConvertRequest(ProviderOpenAI, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	if string(out) != string([]byte(in)) {
		t.Errorf("OpenAI→OpenAI 应原样直通，实际: %s", out)
	}
}

// TestConvertResponseOpenAIToAnthropicUsageCache 断言非流式 OpenAI 响应转 Anthropic 时
// usage 缓存字段完整映射（cache_read_input_tokens / cache_creation_input_tokens）。
func TestConvertResponseOpenAIToAnthropicUsageCache(t *testing.T) {
	in := `{
		"id": "chatcmpl-1",
		"object": "chat.completion",
		"model": "gpt-4o",
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "hi"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150,
		          "prompt_tokens_details": {"cached_tokens": 60, "cache_creation_tokens": 30}}
	}`
	out, err := ConvertResponse(ProviderOpenAI, ProviderAnthropic, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	usage, ok := m["usage"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 usage 字段: %s", out)
	}
	if usage["input_tokens"].(float64) != 10 {
		t.Errorf("input_tokens 应为非缓存输入 10，实际: %v", usage["input_tokens"])
	}
	if usage["output_tokens"].(float64) != 50 {
		t.Errorf("output_tokens 应为 50，实际: %v", usage["output_tokens"])
	}
	if usage["cache_read_input_tokens"].(float64) != 60 {
		t.Errorf("cache_read_input_tokens 应为 60，实际: %v", usage["cache_read_input_tokens"])
	}
	if usage["cache_creation_input_tokens"].(float64) != 30 {
		t.Errorf("cache_creation_input_tokens 应为 30，实际: %v", usage["cache_creation_input_tokens"])
	}
}

// TestEstimateRequestTokens 验证估算函数：各协议可解析、返回正数且随文本变长而变大。
func TestEstimateRequestTokens(t *testing.T) {
	// OpenAI 协议
	short := EstimateRequestTokens(ProviderOpenAI, []byte(`{"messages":[{"role":"user","content":"hi"}]}`))
	if short <= 0 {
		t.Fatalf("OpenAI 短文本估算应 > 0，实际: %d", short)
	}
	longBody := `{"messages":[{"role":"user","content":"` + strings.Repeat("hello ", 200) + `"}]}`
	long := EstimateRequestTokens(ProviderOpenAI, []byte(longBody))
	if long <= short {
		t.Errorf("长文本估算应大于短文本: short=%d long=%d", short, long)
	}

	// Anthropic 原始协议（客户端发给 /v1/messages 的形态）
	anth := EstimateRequestTokens(ProviderAnthropic, []byte(`{
		"model":"claude-x","max_tokens":100,"stream":true,
		"system":"be helpful",
		"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]
	}`))
	if anth <= 0 {
		t.Errorf("Anthropic 请求估算应 > 0，实际: %d", anth)
	}

	// 非法 JSON 返回 0
	if v := EstimateRequestTokens(ProviderOpenAI, []byte(`{`)); v != 0 {
		t.Errorf("非法 JSON 应返回 0，实际: %d", v)
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

// TestConvertRequestAnthropicThinkingToReasoningContent 断言 Anthropic assistant 的
// thinking 块被映射为 OpenAI 的 reasoning_content 字段（DeepSeek 等兼容方在 thinking
// 模式下要求多轮回传上一轮思考内容，否则报 400 "reasoning_content must be passed back"）。
func TestConvertRequestAnthropicThinkingToReasoningContent(t *testing.T) {
	// 混合：thinking + text（Claude Code thinking 模式的典型 assistant 消息）
	in := `{
		"model": "deepseek-chat",
		"messages": [
			{"role":"user","content":[{"type":"text","text":"hi"}]},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"先想一下","signature":"sig"},
				{"type":"text","text":"答案"}
			]}
		],
		"max_tokens": 100
	}`
	out, err := ConvertRequest(ProviderAnthropic, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	msgs := sliceAt(t, m, "messages")
	if len(msgs) != 2 {
		t.Fatalf("消息数不匹配: %d, 输出=%s", len(msgs), out)
	}
	assistant := msgs[1].(map[string]any)
	if got := assistant["reasoning_content"]; got != "先想一下" {
		t.Errorf("assistant 应带 reasoning_content=%q, 实际 %v", "先想一下", got)
	}
	if got := assistant["content"]; got != "答案" {
		t.Errorf("assistant content 应=答案, 实际 %v", got)
	}
	if _, has := assistant["thinking"]; has {
		t.Errorf("不应透出 Anthropic 专有 thinking 字段: %v", assistant)
	}
}

// TestConvertRequestAnthropicPureThinkingKept 断言纯 thinking（无 text 无 tool_calls）
// 的 assistant 消息不再被整条丢弃，而是保留为仅含 reasoning_content 的消息——
// DeepSeek 需要它来回传思考内容，否则下一轮报 400。
func TestConvertRequestAnthropicPureThinkingKept(t *testing.T) {
	in := `{
		"model": "deepseek-chat",
		"messages": [
			{"role":"user","content":[{"type":"text","text":"hi"}]},
			{"role":"assistant","content":[{"type":"thinking","thinking":"我在思考","signature":"sig"}]}
		],
		"max_tokens": 100
	}`
	out, err := ConvertRequest(ProviderAnthropic, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	msgs := sliceAt(t, m, "messages")
	if len(msgs) != 2 {
		t.Fatalf("纯 thinking 的 assistant 消息应被保留，消息数应为 2，实际 %d, 输出=%s", len(msgs), out)
	}
	assistant := msgs[1].(map[string]any)
	if assistant["role"] != "assistant" {
		t.Fatalf("应为 assistant 消息: %v", assistant)
	}
	if got := assistant["reasoning_content"]; got != "我在思考" {
		t.Errorf("assistant 应带 reasoning_content=%q, 实际 %v", "我在思考", got)
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

// TestConvertRequestAnthropicToOpenAIToolNameAlwaysPresent 断言 tool 消息始终带 name 字段。
// 历史被截断（tool_result 引用的 tool_use 不在请求里）时映射查不到 name，
// 此时 name 也必须以空串输出——DeepSeek 等上游反序列化要求字段存在（missing field name 会 400）。
func TestConvertRequestAnthropicToOpenAIToolNameAlwaysPresent(t *testing.T) {
	in := `{
		"model": "claude-3",
		"messages": [
			{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "orphan_1", "content": "sunny"}]}
		],
		"max_tokens": 100
	}`
	out, err := ConvertRequest(ProviderAnthropic, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	msgs := sliceAt(t, m, "messages")
	if len(msgs) != 1 {
		t.Fatalf("消息数不匹配: %d, 输出=%s", len(msgs), out)
	}
	tool := msgs[0].(map[string]any)
	if tool["role"] != "tool" {
		t.Fatalf("应为 tool 消息: %v", tool)
	}
	name, ok := tool["name"]
	if !ok {
		t.Fatalf("tool 消息缺 name 字段（上游会报 missing field name），原始 JSON: %s", out)
	}
	if name != "" {
		t.Errorf("查不到映射时 name 应为空串，实际: %v", name)
	}
}

// TestConvertRequestResponsesToOpenAIToolNameAlwaysPresent 断言 Responses 请求转 OpenAI 时，
// function_call_output 生成的 tool 消息同样始终带 name 字段（与 Anthropic 路径同病根）。
func TestConvertRequestResponsesToOpenAIToolNameAlwaysPresent(t *testing.T) {
	in := `{
		"model": "gpt-4o",
		"input": [
			{"type": "function_call_output", "call_id": "orphan_1", "output": "sunny"}
		]
	}`
	out, err := ConvertRequest(ProviderOpenAIResponses, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	msgs := sliceAt(t, m, "messages")
	if len(msgs) != 1 {
		t.Fatalf("消息数不匹配: %d, 输出=%s", len(msgs), out)
	}
	tool := msgs[0].(map[string]any)
	if tool["role"] != "tool" {
		t.Fatalf("应为 tool 消息: %v", tool)
	}
	if name, ok := tool["name"]; !ok {
		t.Fatalf("tool 消息缺 name 字段，原始 JSON: %s", out)
	} else if name != "" {
		t.Errorf("查不到映射时 name 应为空串，实际: %v", name)
	}
}

// TestConvertRequestResponsesToOpenAIToolNameMapped 断言 function_call 的 name 能映射到
// 对应的 function_call_output 生成的 tool 消息。
func TestConvertRequestResponsesToOpenAIToolNameMapped(t *testing.T) {
	in := `{
		"model": "gpt-4o",
		"input": [
			{"type": "function_call", "call_id": "c1", "name": "get_weather", "arguments": "{}"},
			{"type": "function_call_output", "call_id": "c1", "output": "sunny"}
		]
	}`
	out, err := ConvertRequest(ProviderOpenAIResponses, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	msgs := sliceAt(t, m, "messages")
	if len(msgs) != 2 {
		t.Fatalf("消息数不匹配: %d, 输出=%s", len(msgs), out)
	}
	tool := msgs[1].(map[string]any)
	if tool["name"] != "get_weather" {
		t.Errorf("tool 消息 name 应为 get_weather，实际: %v", tool["name"])
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

// TestConvertRequestAnthropicToOpenAIToolChoice 断言 Anthropic tool_choice 对象
// 被正确映射为 OpenAI tool_choice（字符串或 {type:function}），而不是原样透传
// {type:auto}——否则 OpenAI 兼容上游（Rust serde）会报
// "tool_choice: field `type`: unknown variant `auto`, expected `function`" 的 400。
func TestConvertRequestAnthropicToOpenAIToolChoice(t *testing.T) {
	cases := []struct {
		name string
		in   string // 请求里的 tool_choice 值
		want any    // 期望转换后的 tool_choice
	}{
		{"auto", `{"type":"auto"}`, "auto"},
		{"any", `{"type":"any"}`, "required"},
		{"none", `{"type":"none"}`, "none"},
		{"tool", `{"type":"tool","name":"get_weather"}`, map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := `{"model":"m","max_tokens":100,"messages":[{"role":"user","content":"hi"}],"tool_choice":` + c.in + `}`
			out, err := ConvertRequest(ProviderAnthropic, ProviderOpenAI, []byte(in))
			if err != nil {
				t.Fatalf("转换失败: %v", err)
			}
			m := unmarshalAny(t, out)
			assertJSONEqual(t, m["tool_choice"], c.want)
		})
	}
}

// TestConvertRequestOpenAIToAnthropicToolChoice 断言 OpenAI tool_choice
// （字符串或 {type:function}）被正确映射为 Anthropic tool_choice 对象，
// 而不是原样透传——Anthropic 上游不认 "auto" 字符串或 {type:function}。
func TestConvertRequestOpenAIToAnthropicToolChoice(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want any
	}{
		{"auto", `"auto"`, map[string]any{"type": "auto"}},
		{"required", `"required"`, map[string]any{"type": "any"}},
		{"none", `"none"`, map[string]any{"type": "none"}},
		{"function", `{"type":"function","function":{"name":"get_weather"}}`, map[string]any{"type": "tool", "name": "get_weather"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := `{"model":"m","messages":[{"role":"user","content":"hi"}],"tool_choice":` + c.in + `}`
			out, err := ConvertRequest(ProviderOpenAI, ProviderAnthropic, []byte(in))
			if err != nil {
				t.Fatalf("转换失败: %v", err)
			}
			m := unmarshalAny(t, out)
			assertJSONEqual(t, m["tool_choice"], c.want)
		})
	}
}

// assertJSONEqual 把两个值都 JSON 序列化后比较，屏蔽 map 键序差异。
func assertJSONEqual(t *testing.T, got, want any) {
	t.Helper()
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("不匹配:\n got  %s\n want %s", gotJSON, wantJSON)
	}
}
