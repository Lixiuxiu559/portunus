package protocol

import (
	"encoding/json"
	"fmt"
	"testing"
)

// 本文件锁定 Gemini 方向的托管 web_search 适配：
//   - 入站：tools 里的 hosted 键（googleSearch / googleSearchRetrieval /
//     enterpriseWebSearch）归一到 canonical 统一语义名 web_search
//   - 出站：canonical web_search 渲染为 {"googleSearch": {}} 空对象元素
//   - 发往 openai 上游：hosted 声明剥离并计入 ComposeStats.HostedToolsStripped
//   - 响应：candidate 级 groundingMetadata → canonical GroundingMetadata
//   - 流式：gemini 接地流经 canonical 到 anthropic，合成 server_tool_use /
//     web_search_tool_result 块并排在 text 之前
// 黑盒经公开入口（ConvertRequest/ConvertResponse/NewStreamConverter/
// ComposeUpstreamRequest），风格对齐 convert_test.go。

// toolWithKey 返回 tools 数组里第一个含指定键的元素。
func toolWithKey(t *testing.T, tools []any, key string) map[string]any {
	t.Helper()
	for _, it := range tools {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := m[key]; ok {
			return m
		}
	}
	t.Fatalf("tools 中找不到含键 %q 的元素: %v", key, tools)
	return nil
}

// TestGeminiHostedSearchInboundCanonical gemini 入站 hosted 搜索键（googleSearch
// 及其两个变体键）应以 canonical 统一语义名 web_search 承载，且不产生多余工具。
func TestGeminiHostedSearchInboundCanonical(t *testing.T) {
	for _, key := range []string{"googleSearch", "googleSearchRetrieval", "enterpriseWebSearch"} {
		in := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"` + key + `":{}}]}`
		out, err := ConvertRequest(ProviderGemini, ProviderOpenAI, []byte(in))
		if err != nil {
			t.Fatalf("[%s] 转换失败: %v", key, err)
		}
		tools := sliceAt(t, unmarshalAny(t, out), "tools")
		if len(tools) != 1 {
			t.Fatalf("[%s] 应产出 1 个工具，实际 %d: %s", key, len(tools), out)
		}
		tool := tools[0].(map[string]any)
		if tool["type"] != "web_search" {
			t.Errorf("[%s] type = %v, want web_search（canonical 统一语义名）", key, tool["type"])
		}
		fn, ok := tool["function"].(map[string]any)
		if !ok {
			t.Fatalf("[%s] 缺少 function 对象: %v", key, tool)
		}
		if fn["name"] != "web_search" {
			t.Errorf("[%s] function.name = %v, want web_search", key, fn["name"])
		}
		if _, ok := fn["parameters"]; ok {
			t.Errorf("[%s] hosted 声明不应带 parameters: %v", key, fn)
		}
	}
}

// TestGeminiHostedSearchStrippedForOpenAIUpstream gemini 源经 ComposeUpstreamRequest
// 发往 openai 上游时，hosted 声明被剥离并计数；同元素的 functionDeclarations 保留。
func TestGeminiHostedSearchStrippedForOpenAIUpstream(t *testing.T) {
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[
		{"googleSearch":{}},
		{"functionDeclarations":[{"name":"Bash","parameters":{"type":"object"}}]}
	]}`)
	out, stats, err := ComposeUpstreamRequest(ProviderGemini, ProviderOpenAI, body,
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
}

// TestGeminiHostedSearchOutboundRender canonical web_search 渲染为独立的
// googleSearch 空对象元素，与 functionDeclarations 元素并列。
func TestGeminiHostedSearchOutboundRender(t *testing.T) {
	in := `{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[
		{"type":"web_search","function":{"name":"web_search"}},
		{"type":"function","function":{"name":"Bash","parameters":{"type":"object"}}}
	]}`
	out, err := ConvertRequest(ProviderOpenAI, ProviderGemini, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	tools := sliceAt(t, unmarshalAny(t, out), "tools")
	if len(tools) != 2 {
		t.Fatalf("应产出 2 个 tools 元素，实际 %d: %s", len(tools), out)
	}
	// hosted：一个空对象即完整声明。
	hosted := toolWithKey(t, tools, "googleSearch")
	gs, ok := hosted["googleSearch"].(map[string]any)
	if !ok {
		t.Fatalf("googleSearch 应是空对象: %v", hosted)
	}
	if len(gs) != 0 {
		t.Errorf("googleSearch 应为空对象，实际 %v", gs)
	}
	// function：仍归入 functionDeclarations 元素。
	declsElem := toolWithKey(t, tools, "functionDeclarations")
	decls := declsElem["functionDeclarations"].([]any)
	if len(decls) != 1 || decls[0].(map[string]any)["name"] != "Bash" {
		t.Errorf("functionDeclarations 渲染被误伤: %v", decls)
	}
}

// TestGeminiGroundingResponseParsing 响应 candidate 级 groundingMetadata 映射为
// canonical message 的 grounding_metadata（查询词与来源保序直通）。
func TestGeminiGroundingResponseParsing(t *testing.T) {
	in := `{"candidates":[{"content":{"role":"model","parts":[{"text":"答案是 42"}]},
		"finishReason":"STOP","groundingMetadata":{
			"searchEntryPoint":{"renderedContent":"<div>忽略</div>"},
			"webSearchQueries":["golang 42","go constant"],
			"groundingChunks":[
				{"web":{"uri":"https://go.dev","title":"Go"}},
				{"web":{"uri":"https://x.dev","title":"X"}}
			]
		}}]}`
	out, err := ConvertResponse(ProviderGemini, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	choices := sliceAt(t, unmarshalAny(t, out), "choices")
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	gm, ok := msg["grounding_metadata"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 grounding_metadata: %s", out)
	}
	queries := gm["search_queries"].([]any)
	if len(queries) != 2 || queries[0] != "golang 42" || queries[1] != "go constant" {
		t.Errorf("search_queries 未保序直通: %v", queries)
	}
	sources := gm["sources"].([]any)
	if len(sources) != 2 {
		t.Fatalf("sources 数量不匹配: %v", sources)
	}
	first := sources[0].(map[string]any)
	if first["uri"] != "https://go.dev" || first["title"] != "Go" {
		t.Errorf("source 映射不匹配: %v", first)
	}
}

// TestGeminiGroundingResponseAbsent 无接地数据时不产出空载体（下游据此判断有无接地）。
func TestGeminiGroundingResponseAbsent(t *testing.T) {
	in := `{"candidates":[{"content":{"role":"model","parts":[{"text":"答"}]},"finishReason":"STOP"}]}`
	out, err := ConvertResponse(ProviderGemini, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	choices := sliceAt(t, unmarshalAny(t, out), "choices")
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if _, ok := msg["grounding_metadata"]; ok {
		t.Errorf("无接地数据时不应产出 grounding_metadata: %s", out)
	}
}

// TestGeminiGroundingStreamToAnthropic 流式端到端：gemini 接地流经 canonical 到
// anthropic，应在 text 块之前合成 server_tool_use / web_search_tool_result 块。
// 首个 chunk 同时携带文本与接地，唯一验证「同 chunk 文本 + 接地」的共存路径。
func TestGeminiGroundingStreamToAnthropic(t *testing.T) {
	conv, err := NewStreamConverter(ProviderGemini, ProviderAnthropic)
	if err != nil {
		t.Fatal(err)
	}

	var payloads [][]byte
	payloads = append(payloads, feedStream(t, conv, `{"candidates":[{"content":{"role":"model","parts":[{"text":"答案"}]},
		"groundingMetadata":{"webSearchQueries":["golang"],
		"groundingChunks":[{"web":{"uri":"https://go.dev","title":"Go"}}]}}]}`)...)
	payloads = append(payloads, feedStream(t, conv, `{"candidates":[{"content":{"role":"model","parts":[{"text":"续"}]},"finishReason":"STOP"}]}`)...)
	finals, err := conv.Finish()
	if err != nil {
		t.Fatal(err)
	}
	payloads = append(payloads, finals...)

	// text 块之前必须出现 server_tool_use 与 web_search_tool_result 两个合成块。
	starts := collectBlockStarts(t, payloads)
	if len(starts) != 3 {
		t.Fatalf("应有 3 个 content_block_start（server_tool_use / web_search_tool_result / text），实际 %d: %v", len(starts), starts)
	}
	stu := starts[0]["content_block"].(map[string]any)
	if stu["type"] != "server_tool_use" || stu["name"] != "web_search" {
		t.Errorf("首个块应为 server_tool_use/web_search: %v", stu)
	}
	if q := stu["input"].(map[string]any)["query"]; q != "golang" {
		t.Errorf("server_tool_use input.query = %v, want golang", q)
	}
	wsr := starts[1]["content_block"].(map[string]any)
	if wsr["type"] != "web_search_tool_result" || wsr["tool_use_id"] != stu["id"] {
		t.Errorf("第二个块应为 web_search_tool_result 且与 server_tool_use 配对: %v", wsr)
	}
	results := wsr["content"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["url"] != "https://go.dev" {
		t.Errorf("搜索结果形状不匹配: %v", results)
	}
	txt := starts[2]["content_block"].(map[string]any)
	if txt["type"] != "text" {
		t.Errorf("第三个块应为 text: %v", txt)
	}
	// index 递增 0 / 1 / 2，保证客户端按「先搜索、后回答」编排。
	for i, want := range []float64{0, 1, 2} {
		if starts[i]["index"] != want {
			t.Errorf("第 %d 个 content_block_start index = %v, want %v", i, starts[i]["index"], want)
		}
	}
}

// TestGeminiGroundingLateArrivalStream 接地晚到的真实时序：Gemini 的
// groundingMetadata 通常随末 chunk（带 finishReason）到达，此时首个 text 块
// 已经开写。合成块必须插在已写文本与续写文本之间（关闭当前块 → 插入 → 重开），
// 而不是静默丢失——此用例锁定该回归。
func TestGeminiGroundingLateArrivalStream(t *testing.T) {
	conv, err := NewStreamConverter(ProviderGemini, ProviderAnthropic)
	if err != nil {
		t.Fatal(err)
	}
	var events [][]byte
	for _, payload := range []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"答"}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"案"}]},"finishReason":"STOP",
			"groundingMetadata":{"webSearchQueries":["golang"],
			"groundingChunks":[{"web":{"uri":"https://go.dev","title":"Go"}}]}}],
			"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`,
	} {
		outs, err := conv.Convert([]byte(payload))
		if err != nil {
			t.Fatalf("流转换失败: %v", err)
		}
		events = append(events, outs...)
	}
	events = append(events, mustFinish(t, conv)...)

	// 块顺序必须是 text → server_tool_use → web_search_tool_result → text。
	var seq []string
	for _, ev := range events {
		var e map[string]any
		if err := json.Unmarshal(ev, &e); err != nil {
			t.Fatalf("流事件不是合法 JSON: %v", ev)
		}
		if e["type"] != "content_block_start" {
			continue
		}
		blk, _ := e["content_block"].(map[string]any)
		seq = append(seq, fmt.Sprint(blk["type"]))
	}
	want := []string{"text", "server_tool_use", "web_search_tool_result", "text"}
	if len(seq) != len(want) {
		t.Fatalf("块序列 = %v, want %v", seq, want)
	}
	for i := range want {
		if seq[i] != want[i] {
			t.Fatalf("块序列 = %v, want %v", seq, want)
		}
	}
	// 确定性：同一 chunk 序列重放产出相同字节。
	conv2, err := NewStreamConverter(ProviderGemini, ProviderAnthropic)
	if err != nil {
		t.Fatal(err)
	}
	var events2 [][]byte
	for _, payload := range []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"答"}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"案"}]},"finishReason":"STOP",
			"groundingMetadata":{"webSearchQueries":["golang"],
			"groundingChunks":[{"web":{"uri":"https://go.dev","title":"Go"}}]}}],
			"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`,
	} {
		outs, err := conv2.Convert([]byte(payload))
		if err != nil {
			t.Fatalf("重放转换失败: %v", err)
		}
		events2 = append(events2, outs...)
	}
	events2 = append(events2, mustFinish(t, conv2)...)
	if fmt.Sprint(events) != fmt.Sprint(events2) {
		t.Errorf("同一 chunk 序列两次转换不一致")
	}
}

// mustFinish 收集转换器的收尾事件（测试 helper）。
func mustFinish(t *testing.T, conv StreamConverter) [][]byte {
	t.Helper()
	finals, err := conv.Finish()
	if err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	return finals
}

// feedStream 把一条流式事件载荷喂给转换器并返回产出（测试 helper，风格同 mustFinish）。
func feedStream(t *testing.T, conv StreamConverter, payload string) [][]byte {
	t.Helper()
	out, err := conv.Convert([]byte(payload))
	if err != nil {
		t.Fatalf("流转换失败: %v", err)
	}
	return out
}

// collectBlockStarts 解析一组流事件载荷，返回其中所有 content_block_start 事件
// （测试 helper：多份用例都要按 index 序检查合成块的 type / 形状）。
func collectBlockStarts(t *testing.T, payloads [][]byte) []map[string]any {
	t.Helper()
	var starts []map[string]any
	for _, p := range payloads {
		var ev map[string]any
		if err := json.Unmarshal(p, &ev); err != nil {
			t.Fatalf("流事件不是合法 JSON: %v\n%s", err, p)
		}
		if ev["type"] == "content_block_start" {
			starts = append(starts, ev)
		}
	}
	return starts
}

// TestGeminiGroundingToolCallOnlyStreamToAnthropic 接地随末 chunk 到达、整条流无任何
// text delta（只有工具调用增量 / 空 candidates）时，接地也必须在收尾前补发合成块。
// 此前 emitGrounding 只在首个 text 块打开时调用，无 text 的流会静默丢失接地——本
// 用例锁定 anthropicBlockSink.Finish 的兜底（见该处注释）。
func TestGeminiGroundingToolCallOnlyStreamToAnthropic(t *testing.T) {
	conv, err := NewStreamConverter(ProviderGemini, ProviderAnthropic)
	if err != nil {
		t.Fatal(err)
	}
	var payloads [][]byte
	// 首个 chunk 只有 functionCall（gemini 侧的工具调用增量），无任何文本。
	payloads = append(payloads, feedStream(t, conv, `{"candidates":[{"content":{"role":"model",
		"parts":[{"functionCall":{"name":"Bash","args":{"cmd":"ls"}}}]}}]}`)...)
	// 末 chunk 只带 groundingMetadata 与 finishReason，无 parts 文本。
	payloads = append(payloads, feedStream(t, conv, `{"candidates":[{"content":{"role":"model","parts":[]},
		"finishReason":"STOP","groundingMetadata":{"webSearchQueries":["golang"],
		"groundingChunks":[{"web":{"uri":"https://go.dev","title":"Go"}}]}}]}`)...)
	payloads = append(payloads, mustFinish(t, conv)...)

	starts := collectBlockStarts(t, payloads)
	var kinds []string
	for _, s := range starts {
		blk, _ := s["content_block"].(map[string]any)
		kinds = append(kinds, fmt.Sprint(blk["type"]))
	}
	if len(starts) != 2 {
		t.Fatalf("无 text 的流应补发 2 个合成块（server_tool_use / web_search_tool_result），实际 %d: %v", len(starts), kinds)
	}
	if kinds[0] != "server_tool_use" || kinds[1] != "web_search_tool_result" {
		t.Errorf("合成块顺序 = %v, want [server_tool_use web_search_tool_result]", kinds)
	}
}

// TestGeminiGroundingConversionDeterministic 同一输入两次转换必须逐字节一致——
// 缓存前缀稳定的前提（见 prefix_stability_test.go）。
func TestGeminiGroundingConversionDeterministic(t *testing.T) {
	req := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"googleSearch":{}}]}`)
	a, err := ConvertRequest(ProviderGemini, ProviderOpenAI, req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ConvertRequest(ProviderGemini, ProviderOpenAI, req)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Errorf("同一请求两次转换不一致:\n A=%s\n B=%s", a, b)
	}

	resp := []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"答"}]},"finishReason":"STOP",
		"groundingMetadata":{"webSearchQueries":["q"],"groundingChunks":[{"web":{"uri":"https://a.dev","title":"A"}}]}}]}`)
	ra, err := ConvertResponse(ProviderGemini, ProviderOpenAI, resp)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := ConvertResponse(ProviderGemini, ProviderOpenAI, resp)
	if err != nil {
		t.Fatal(err)
	}
	if string(ra) != string(rb) {
		t.Errorf("同一响应两次转换不一致:\n A=%s\n B=%s", ra, rb)
	}
}
