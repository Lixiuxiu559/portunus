package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// 本文件锁定 Claude Code 的托管 web_search 工具在各条转换链上的行为：
//   - 入站（anthropic → canonical → openai）：版本化 web_search_* 声明经
//     canonical Tool.Type 透传，未知 hosted 类型剥离并计数
//   - 出站（canonical → anthropic）：hosted 声明原样写出，不写 input_schema
//   - 发往 openai 上游：hosted 声明剥离并计入 ComposeStats.HostedToolsStripped
//   - 历史消息里的 server_tool_use / web_search_tool_result 与 tool_use /
//     tool_result 对称映射
//   - 响应里的 GroundingMetadata 合成 server_tool_use + web_search_tool_result
//     （非流式与流式形状一致）
// 黑盒经公开入口（ConvertRequest/ConvertResponse/NewStreamConverter/
// ComposeUpstreamRequest），风格对齐 convert_test.go。

// eventTypes 提取一组流事件载荷的顶层 type 序列。
func eventTypes(t *testing.T, payloads [][]byte) []string {
	t.Helper()
	types := make([]string, 0, len(payloads))
	for _, p := range payloads {
		var ev map[string]any
		if err := json.Unmarshal(p, &ev); err != nil {
			t.Fatalf("流事件不是合法 JSON: %v\n%s", err, p)
		}
		t, _ := ev["type"].(string)
		types = append(types, t)
	}
	return types
}

// contentBlockAt 取出 content 数组第 i 个块。
func contentBlockAt(t *testing.T, m map[string]any, i int) map[string]any {
	t.Helper()
	blocks := sliceAt(t, m, "content")
	if i >= len(blocks) {
		t.Fatalf("content 只有 %d 个块，取第 %d 个", len(blocks), i)
	}
	block, ok := blocks[i].(map[string]any)
	if !ok {
		t.Fatalf("第 %d 个块不是对象: %v", i, blocks[i])
	}
	return block
}

// TestHostedWebSearchInboundCanonical 入站 web_search_20250305 应以 canonical
// 统一语义名 web_search 承载，且不填 parameters/description（hosted 声明没有 schema）。
func TestHostedWebSearchInboundCanonical(t *testing.T) {
	in := `{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"web_search_20250305","name":"web_search"}]}`
	out, err := ConvertRequest(ProviderAnthropic, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	tools := sliceAt(t, unmarshalAny(t, out), "tools")
	if len(tools) != 1 {
		t.Fatalf("应产出 1 个工具，实际 %d: %s", len(tools), out)
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "web_search" {
		t.Errorf("type = %v, want web_search（canonical 统一语义名）", tool["type"])
	}
	fn, ok := tool["function"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 function 对象: %v", tool)
	}
	if fn["name"] != "web_search" {
		t.Errorf("function.name = %v, want web_search", fn["name"])
	}
	if _, ok := fn["parameters"]; ok {
		t.Errorf("hosted 声明不应带 parameters: %v", fn)
	}
	if _, ok := fn["description"]; ok {
		t.Errorf("hosted 声明不应带 description: %v", fn)
	}
}

// TestHostedWebSearchStrippedForOpenAIUpstream 发往 openai 上游时剥离 hosted
// 声明并计数；普通 function 工具必须保留（不能误伤）。
func TestHostedWebSearchStrippedForOpenAIUpstream(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"}],
		"tools":[
			{"type":"web_search_20250305","name":"web_search"},
			{"name":"Bash","input_schema":{"type":"object"}}
		]}`)
	out, stats, err := ComposeUpstreamRequest(ProviderAnthropic, ProviderOpenAI, body,
		UpstreamRequest{Model: "gpt-x"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.HostedToolsStripped != 1 {
		t.Errorf("HostedToolsStripped = %d, want 1", stats.HostedToolsStripped)
	}
	tools := sliceAt(t, unmarshalAny(t, out), "tools")
	if len(tools) != 1 {
		t.Fatalf("应只剩 1 个 function 工具，实际 %d: %s", len(tools), out)
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["function"].(map[string]any)["name"] != "Bash" {
		t.Errorf("function 工具被误伤: %v", tool)
	}

	// 只有 hosted 声明时，tools 整体为空（omitempty → 顶层无该键）。
	onlyHosted := []byte(`{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"web_search_20250305","name":"web_search"}]}`)
	out, stats, err = ComposeUpstreamRequest(ProviderAnthropic, ProviderOpenAI, onlyHosted,
		UpstreamRequest{Model: "gpt-x"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.HostedToolsStripped != 1 {
		t.Errorf("HostedToolsStripped = %d, want 1", stats.HostedToolsStripped)
	}
	m := unmarshalAny(t, out)
	if v, ok := m["tools"]; ok {
		if arr, _ := v.([]any); len(arr) != 0 {
			t.Errorf("hosted 声明应被剥离，实际 tools=%v: %s", v, out)
		}
	}
}

// TestHostedWebSearchSameProtocolPassthrough anthropic → anthropic 同协议直通
// 不解析、不改写，hosted 声明原样保留（现状回归）。
func TestHostedWebSearchSameProtocolPassthrough(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":3}]}`)
	out, stats, err := ComposeUpstreamRequest(ProviderAnthropic, ProviderAnthropic, body, UpstreamRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(body) {
		t.Errorf("同协议直通应逐字节原样:\n got=%s\nwant=%s", out, body)
	}
	if stats.HostedToolsStripped != 0 {
		t.Errorf("直通不应计数剥离，实际 %d", stats.HostedToolsStripped)
	}
}

// TestHostedWebSearchOutboundRender openai→anthropic 渲染 hosted 声明时把
// canonical 语义名 web_search 字面化为基线版本 web_search_20250305，绝不写
// input_schema/description（hosted 声明没有 schema）。
func TestHostedWebSearchOutboundRender(t *testing.T) {
	in := `{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[
		{"type":"web_search","function":{"name":"web_search"}},
		{"type":"function","function":{"name":"Bash","parameters":{"type":"object"}}}
	]}`
	out, err := ConvertRequest(ProviderOpenAI, ProviderAnthropic, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	tools := sliceAt(t, unmarshalAny(t, out), "tools")
	if len(tools) != 2 {
		t.Fatalf("应产出 2 个工具，实际 %d: %s", len(tools), out)
	}
	hosted := tools[0].(map[string]any)
	if hosted["type"] != "web_search_20250305" || hosted["name"] != "web_search" {
		t.Errorf("hosted 声明应字面化为 web_search_20250305: %v", hosted)
	}
	if _, ok := hosted["input_schema"]; ok {
		t.Errorf("hosted 声明不应写 input_schema: %v", hosted)
	}
	if _, ok := hosted["description"]; ok {
		t.Errorf("hosted 声明不应写 description: %v", hosted)
	}
	// 普通 function 工具仍是完整声明。
	fn := tools[1].(map[string]any)
	if fn["type"] != nil || fn["name"] != "Bash" || fn["input_schema"] == nil {
		t.Errorf("普通 function 工具渲染被误伤: %v", fn)
	}
}

// TestHostedWebSearchOutboundNameFallback openai 入站只带 {"type":"web_search"}
// （无 function.name）时，anthropic 出站的 hosted 声明 name 必须兜底为
// web_search——anthropic 的 hosted 声明 name 必填，空串会渲染出 "name":""
// 污染声明（responses 出站不写 name 是协议差异，不能照搬到这里）。
func TestHostedWebSearchOutboundNameFallback(t *testing.T) {
	in := `{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[
		{"type":"web_search","function":{}}
	]}`
	out, err := ConvertRequest(ProviderOpenAI, ProviderAnthropic, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	tools := sliceAt(t, unmarshalAny(t, out), "tools")
	if len(tools) != 1 {
		t.Fatalf("应产出 1 个工具，实际 %d: %s", len(tools), out)
	}
	hosted := tools[0].(map[string]any)
	if hosted["type"] != "web_search_20250305" || hosted["name"] != "web_search" {
		t.Errorf("hosted 声明 name 应兜底为 web_search: %v", hosted)
	}
	if strings.Contains(string(out), `"name":""`) {
		t.Errorf("不应渲染出空 name 键: %s", out)
	}
}

// TestHostedServerToolHistoryInbound 历史里的 server_tool_use 与
// web_search_tool_result 应分别映射为 tool_calls 与 tool 消息，形状与
// tool_use / tool_result 对称。
func TestHostedServerToolHistoryInbound(t *testing.T) {
	in := `{"model":"m","max_tokens":64,"messages":[
		{"role":"user","content":"搜索一下"},
		{"role":"assistant","content":[
			{"type":"text","text":"我来搜索"},
			{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"golang"}}
		]},
		{"role":"user","content":[
			{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[
				{"type":"web_search_result","url":"https://go.dev","title":"The Go Programming Language"},
				{"type":"web_search_result","url":"https://pkg.go.dev","title":"Go Packages"}
			]}
		]}
	]}`
	out, err := ConvertRequest(ProviderAnthropic, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	msgs := sliceAt(t, unmarshalAny(t, out), "messages")
	if len(msgs) != 3 {
		t.Fatalf("应产出 3 条消息（user / assistant+tool_calls / tool），实际 %d: %s", len(msgs), out)
	}

	// assistant：server_tool_use → tool_calls[0]
	assistant := msgs[1].(map[string]any)
	if assistant["role"] != "assistant" {
		t.Fatalf("第 2 条应为 assistant: %v", assistant)
	}
	if assistant["content"] != "我来搜索" {
		t.Errorf("assistant 文本内容丢失: %v", assistant["content"])
	}
	calls := assistant["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("应有 1 个 tool_call，实际 %d", len(calls))
	}
	call := calls[0].(map[string]any)
	if call["id"] != "srvtoolu_1" || call["type"] != "function" {
		t.Errorf("tool_call 标识不匹配: %v", call)
	}
	fn := call["function"].(map[string]any)
	if fn["name"] != "web_search" {
		t.Errorf("tool_call name = %v, want web_search", fn["name"])
	}
	if fn["arguments"] != `{"query":"golang"}` {
		t.Errorf("tool_call arguments = %v", fn["arguments"])
	}

	// tool 消息：web_search_tool_result → tool 消息，内容为每条 "title: url"
	toolMsg := msgs[2].(map[string]any)
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "srvtoolu_1" {
		t.Fatalf("第 3 条应为 tool 消息: %v", toolMsg)
	}
	if toolMsg["name"] != "web_search" {
		t.Errorf("tool 消息 name 应由 server_tool_use 映射补全: %v", toolMsg["name"])
	}
	want := "The Go Programming Language: https://go.dev\nGo Packages: https://pkg.go.dev"
	if toolMsg["content"] != want {
		t.Errorf("tool 消息内容不匹配:\n got=%q\nwant=%q", toolMsg["content"], want)
	}
}

// TestHostedGroundingResponseSynthesis 响应带 GroundingMetadata 时，应在 text
// 块前合成 server_tool_use + web_search_tool_result 两个块。
func TestHostedGroundingResponseSynthesis(t *testing.T) {
	in := `{"id":"chatcmpl-1","object":"chat.completion","model":"m","choices":[{"index":0,
		"message":{"role":"assistant","content":"答案是 42","grounding_metadata":{
			"search_queries":["golang 42"],
			"sources":[
				{"uri":"https://go.dev","title":"Go"},
				{"uri":"https://x.dev","title":"X"}
			]}},
		"finish_reason":"stop"}]}`
	out, err := ConvertResponse(ProviderOpenAI, ProviderAnthropic, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	m := unmarshalAny(t, out)
	blocks := sliceAt(t, m, "content")
	if len(blocks) != 3 {
		t.Fatalf("应有 3 个块（server_tool_use / web_search_tool_result / text），实际 %d: %s", len(blocks), out)
	}

	stu := contentBlockAt(t, m, 0)
	if stu["type"] != "server_tool_use" || stu["name"] != "web_search" {
		t.Errorf("第 1 个块应为 server_tool_use/web_search: %v", stu)
	}
	id, _ := stu["id"].(string)
	if !strings.HasPrefix(id, "srvtoolu_gateway_") {
		t.Errorf("server_tool_use id 前缀不符: %q", id)
	}
	if query := stu["input"].(map[string]any)["query"]; query != "golang 42" {
		t.Errorf("server_tool_use input.query = %v, want golang 42", query)
	}

	wsr := contentBlockAt(t, m, 1)
	if wsr["type"] != "web_search_tool_result" || wsr["tool_use_id"] != id {
		t.Errorf("第 2 个块应与 server_tool_use 配对: %v", wsr)
	}
	results := wsr["content"].([]any)
	if len(results) != 2 {
		t.Fatalf("搜索来源应全部保留，实际 %d", len(results))
	}
	first := results[0].(map[string]any)
	if first["type"] != "web_search_result" || first["url"] != "https://go.dev" || first["title"] != "Go" {
		t.Errorf("搜索结果形状不匹配: %v", first)
	}

	text := contentBlockAt(t, m, 2)
	if text["type"] != "text" || text["text"] != "答案是 42" {
		t.Errorf("text 块应在最后: %v", text)
	}
}

// TestHostedGroundingStreamSynthesis 流式：接地数据 + 文本 + 收尾，事件序列应为
// message_start → server_tool_use(start/stop) → web_search_tool_result(start/stop)
// → text(start/delta/stop) → message_delta → message_stop。
func TestHostedGroundingStreamSynthesis(t *testing.T) {
	conv, err := NewStreamConverter(ProviderOpenAI, ProviderAnthropic)
	if err != nil {
		t.Fatal(err)
	}

	// 开场帧只带接地数据（无内容），接地应被缓存、等 text 块打开时补发。
	var payloads [][]byte
	payloads = append(payloads, feedStream(t, conv, `{"id":"c1","model":"m","choices":[{"index":0,
		"delta":{"role":"assistant","grounding_metadata":{"search_queries":["golang"],
		"sources":[{"uri":"https://go.dev","title":"Go"}]}},"finish_reason":""}]}`)...)
	payloads = append(payloads, feedStream(t, conv, `{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"答案"},"finish_reason":""}]}`)...)
	payloads = append(payloads, feedStream(t, conv, `{"id":"c1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)...)
	payloads = append(payloads, mustFinish(t, conv)...)

	want := []string{
		"message_start",
		"content_block_start", "content_block_stop", // server_tool_use
		"content_block_start", "content_block_stop", // web_search_tool_result
		"content_block_start", "content_block_delta", // text
		"content_block_stop",
		"message_delta", "message_stop",
	}
	got := eventTypes(t, payloads)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("事件序列不匹配:\n got=%v\nwant=%v", got, want)
	}

	// 前四个 content_block_* 的 content_block 形状与块 index 顺序。
	starts := collectBlockStarts(t, payloads)
	if len(starts) != 3 {
		t.Fatalf("应有 3 个 content_block_start，实际 %d", len(starts))
	}
	stu := starts[0]["content_block"].(map[string]any)
	if stu["type"] != "server_tool_use" || stu["name"] != "web_search" {
		t.Errorf("首个块应为 server_tool_use/web_search: %v", stu)
	}
	if _, ok := stu["input"]; !ok {
		t.Errorf("server_tool_use 必须带 input: %v", stu)
	}
	wsr := starts[1]["content_block"].(map[string]any)
	if wsr["type"] != "web_search_tool_result" || wsr["tool_use_id"] != stu["id"] {
		t.Errorf("第二个块应与 server_tool_use 配对: %v", wsr)
	}
	txt := starts[2]["content_block"].(map[string]any)
	if txt["type"] != "text" {
		t.Errorf("第三个块应为 text: %v", txt)
	}
	// index 顺序 0 / 1 / 2，保证客户端按序编排。
	for i, want := range []float64{0, 1, 2} {
		if starts[i]["index"] != want {
			t.Errorf("第 %d 个 content_block_start index = %v, want %v", i, starts[i]["index"], want)
		}
	}
}

// TestHostedUnknownTypeStripped 未知 hosted 类型（code_execution_*）入站即剥离
// 并计数：canonical 无法承载，产出无该工具。
func TestHostedUnknownTypeStripped(t *testing.T) {
	in := []byte(`{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"code_execution_20241022","name":"code_execution"}]}`)
	out, err := ConvertRequest(ProviderAnthropic, ProviderOpenAI, in)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	if v, ok := unmarshalAny(t, out)["tools"]; ok {
		if arr, _ := v.([]any); len(arr) != 0 {
			t.Errorf("未知 hosted 类型应被剥离，实际 tools=%v", v)
		}
	}

	_, stats, err := ComposeUpstreamRequest(ProviderAnthropic, ProviderOpenAI, in, UpstreamRequest{Model: "gpt-x"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.HostedToolsStripped != 1 {
		t.Errorf("HostedToolsStripped = %d, want 1", stats.HostedToolsStripped)
	}
}

// TestHostedConversionDeterministic 同一输入两次转换必须逐字节一致——缓存前缀
// 稳定的前提（见 prefix_stability_test.go 的考据精神）。
func TestHostedConversionDeterministic(t *testing.T) {
	req := []byte(`{"model":"m","max_tokens":64,"messages":[
		{"role":"assistant","content":[{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"x"}}]},
		{"role":"user","content":[{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[{"type":"web_search_result","url":"https://a.dev","title":"A"}]}]}
	],"tools":[{"type":"web_search_20250305","name":"web_search"}]}`)
	a, err := ConvertRequest(ProviderAnthropic, ProviderOpenAI, req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ConvertRequest(ProviderAnthropic, ProviderOpenAI, req)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Errorf("同一请求两次转换不一致:\n A=%s\n B=%s", a, b)
	}

	resp := []byte(`{"id":"c","object":"chat.completion","model":"m","choices":[{"index":0,
		"message":{"role":"assistant","content":"答","grounding_metadata":{"search_queries":["q"],"sources":[{"uri":"https://a.dev","title":"A"}]}},
		"finish_reason":"stop"}]}`)
	ra, err := ConvertResponse(ProviderOpenAI, ProviderAnthropic, resp)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := ConvertResponse(ProviderOpenAI, ProviderAnthropic, resp)
	if err != nil {
		t.Fatal(err)
	}
	if string(ra) != string(rb) {
		t.Errorf("同一响应两次转换不一致:\n A=%s\n B=%s", ra, rb)
	}
}
