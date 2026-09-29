package protocol

import (
	"strings"
	"testing"
)

// 本文件锁定 OpenAI Responses 方向的托管工具声明在协议矩阵上的行为：
//   - 入站（responses → canonical）：web_search 各字面（基线 / 版本化 / preview）
//     归一到 canonical 统一语义名 web_search，未知 hosted 类型剥离并计数
//   - 出站（canonical → responses）：hosted 声明渲染为 {"type":"web_search"}，
//     不带 name/description/parameters
//   - 跨任务链路（anthropic → responses）：anthropic 入站的 web_search_20250305
//     经 canonical 中转后由 responses 出站正确衔接
// 黑盒经公开入口（ConvertRequest / ComposeUpstreamRequest），风格对齐 convert_test.go。

// TestResponsesHostedWebSearchInboundCanonical responses 入站 {"type":"web_search"}
// 应归一到 canonical Tool{type:"web_search"}，不填 parameters/description
// （hosted 声明没有 schema）。
func TestResponsesHostedWebSearchInboundCanonical(t *testing.T) {
	in := `{"model":"m","input":"hi","tools":[{"type":"web_search"}]}`
	out, err := ConvertRequest(ProviderOpenAIResponses, ProviderOpenAI, []byte(in))
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

// TestResponsesHostedWebSearchLiteralVariants 其余 web_search 字面
// （版本化 / preview）同样归一到 canonical web_search。
func TestResponsesHostedWebSearchLiteralVariants(t *testing.T) {
	for _, literal := range []string{
		"web_search_2025_08_26",
		"web_search_preview",
		"web_search_preview_2025_03_11",
	} {
		t.Run(literal, func(t *testing.T) {
			in := `{"model":"m","input":"hi","tools":[{"type":"` + literal + `"}]}`
			out, err := ConvertRequest(ProviderOpenAIResponses, ProviderOpenAI, []byte(in))
			if err != nil {
				t.Fatalf("转换失败: %v", err)
			}
			tools := sliceAt(t, unmarshalAny(t, out), "tools")
			if len(tools) != 1 {
				t.Fatalf("应产出 1 个工具，实际 %d: %s", len(tools), out)
			}
			tool := tools[0].(map[string]any)
			if tool["type"] != "web_search" {
				t.Errorf("type = %v, want web_search", tool["type"])
			}
			if fn := tool["function"].(map[string]any); fn["name"] != "web_search" {
				t.Errorf("function.name = %v, want web_search", fn["name"])
			}
		})
	}
}

// TestResponsesHostedUnknownTypeStripped 未知 hosted 类型（file_search 等）
// 入站即剥离并计入遥测：canonical 无法承载，产出无该工具，
// ComposeUpstreamRequest(responses→openai) 的 HostedToolsStripped 计 1。
func TestResponsesHostedUnknownTypeStripped(t *testing.T) {
	in := []byte(`{"model":"m","input":"hi","tools":[{"type":"file_search"}]}`)
	out, err := ConvertRequest(ProviderOpenAIResponses, ProviderOpenAI, in)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	if v, ok := unmarshalAny(t, out)["tools"]; ok {
		if arr, _ := v.([]any); len(arr) != 0 {
			t.Errorf("未知 hosted 类型应被剥离，实际 tools=%v", v)
		}
	}

	_, stats, err := ComposeUpstreamRequest(ProviderOpenAIResponses, ProviderOpenAI, in, UpstreamRequest{Model: "gpt-x"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.HostedToolsStripped != 1 {
		t.Errorf("HostedToolsStripped = %d, want 1", stats.HostedToolsStripped)
	}
}

// TestResponsesHostedOutboundRender canonical hosted 声明（type:"web_search"）
// 出站渲染为 Responses hosted 形状 {"type":"web_search"}，且不带任何空键
// （尤其不得出现 "name":""）。
func TestResponsesHostedOutboundRender(t *testing.T) {
	in := `{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[
		{"type":"web_search","function":{"name":"web_search"}}
	]}`
	out, err := ConvertRequest(ProviderOpenAI, ProviderOpenAIResponses, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	tools := sliceAt(t, unmarshalAny(t, out), "tools")
	if len(tools) != 1 {
		t.Fatalf("应产出 1 个工具，实际 %d: %s", len(tools), out)
	}
	hosted := tools[0].(map[string]any)
	if hosted["type"] != "web_search" {
		t.Errorf("type = %v, want web_search", hosted["type"])
	}
	if _, ok := hosted["name"]; ok {
		t.Errorf("hosted 声明不应带 name 键: %v", hosted)
	}
	if _, ok := hosted["description"]; ok {
		t.Errorf("hosted 声明不应带 description: %v", hosted)
	}
	if _, ok := hosted["parameters"]; ok {
		t.Errorf("hosted 声明不应带 parameters: %v", hosted)
	}
	if strings.Contains(string(out), `"name":""`) {
		t.Errorf("hosted 声明序列化带出空 name 键: %s", out)
	}
}

// TestResponsesHostedMixedWithFunction hosted + 普通 function 工具并存时各自正确
// （双向都不误伤）。
func TestResponsesHostedMixedWithFunction(t *testing.T) {
	// 入站：hosted web_search 与普通 function 各自保留。
	in := `{"model":"m","input":"hi","tools":[
		{"type":"web_search"},
		{"type":"function","name":"Bash","parameters":{"type":"object"}}
	]}`
	out, err := ConvertRequest(ProviderOpenAIResponses, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	tools := sliceAt(t, unmarshalAny(t, out), "tools")
	if len(tools) != 2 {
		t.Fatalf("应产出 2 个工具，实际 %d: %s", len(tools), out)
	}
	if tools[0].(map[string]any)["type"] != "web_search" {
		t.Errorf("第 1 个工具应为 hosted web_search: %v", tools[0])
	}
	fn := tools[1].(map[string]any)
	if fn["type"] != "function" || fn["function"].(map[string]any)["name"] != "Bash" {
		t.Errorf("普通 function 工具被误伤: %v", fn)
	}
	if fn["function"].(map[string]any)["parameters"] == nil {
		t.Errorf("普通 function 工具的 parameters 丢失: %v", fn)
	}

	// 出站：canonical hosted + function 各自渲染。
	back := `{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[
		{"type":"web_search","function":{"name":"web_search"}},
		{"type":"function","function":{"name":"Bash","parameters":{"type":"object"}}}
	]}`
	out, err = ConvertRequest(ProviderOpenAI, ProviderOpenAIResponses, []byte(back))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	tools = sliceAt(t, unmarshalAny(t, out), "tools")
	if len(tools) != 2 {
		t.Fatalf("应产出 2 个工具，实际 %d: %s", len(tools), out)
	}
	if _, ok := tools[0].(map[string]any)["name"]; ok {
		t.Errorf("hosted 声明不应带 name 键: %v", tools[0])
	}
	fn = tools[1].(map[string]any)
	if fn["type"] != "function" || fn["name"] != "Bash" || fn["parameters"] == nil {
		t.Errorf("普通 function 工具渲染被误伤: %v", fn)
	}
}

// TestResponsesHostedSameProtocolPassthrough responses → responses 同协议直通
// 不解析、不改写，hosted 声明原样保留（现状回归）。
func TestResponsesHostedSameProtocolPassthrough(t *testing.T) {
	body := []byte(`{"model":"m","input":"hi","tools":[{"type":"web_search_preview","filters":{"allowed_domains":["go.dev"]}}]}`)
	out, stats, err := ComposeUpstreamRequest(ProviderOpenAIResponses, ProviderOpenAIResponses, body, UpstreamRequest{})
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

// TestResponsesHostedAnthropicToResponsesChain 跨任务链路：anthropic 入站的
// web_search_20250305（任务 1 入站识别）经 canonical 中转后，由 responses 出站
// 衔接为 {"type":"web_search"}；普通 function 工具一并正确。
func TestResponsesHostedAnthropicToResponsesChain(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"}],
		"tools":[
			{"type":"web_search_20250305","name":"web_search"},
			{"name":"Bash","input_schema":{"type":"object"}}
		]}`)
	out, stats, err := ComposeUpstreamRequest(ProviderAnthropic, ProviderOpenAIResponses, body, UpstreamRequest{Model: "gpt-x"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.HostedToolsStripped != 0 {
		t.Errorf("发往支持 hosted 的 responses 上游不应剥离，实际 %d", stats.HostedToolsStripped)
	}
	tools := sliceAt(t, unmarshalAny(t, out), "tools")
	if len(tools) != 2 {
		t.Fatalf("应产出 2 个工具，实际 %d: %s", len(tools), out)
	}
	hosted := tools[0].(map[string]any)
	if hosted["type"] != "web_search" {
		t.Errorf("hosted 声明应衔接为 type=web_search: %v", hosted)
	}
	if _, ok := hosted["name"]; ok {
		t.Errorf("hosted 声明不应带 name 键: %v", hosted)
	}
	fn := tools[1].(map[string]any)
	if fn["type"] != "function" || fn["name"] != "Bash" || fn["parameters"] == nil {
		t.Errorf("普通 function 工具渲染被误伤: %v", fn)
	}
}
