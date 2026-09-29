package protocol

import (
	"fmt"
	"strings"
	"testing"
)

// hosted web_search 能力矩阵：登记「托管搜索工具声明」在各协议出入口的处置期望。
// 与 capability_matrix_test.go（reasoning 维度）同构的登记模式——探针 × 协议全积
// 组合逐一断言，漏一格或行为回归即红；新协议接入时这里是强制表态点。
//
// canonical 语义基准：Tool.Type == "web_search"（isHostedToolType 判别）。
//
// 请求侧处置期望（from × to 全积）：
//   - to == openai → drop：剥离并计入 ComposeStats.HostedToolsStripped。
//     理由：openai 兼容上游没有 hosted 工具语义，携带声明会被误当空壳 function
//     或直接 400——诚实丢弃优于畸形输出（cc-switch 对拒收 hosted 工具的国产
//     网关同样选择禁用，见调研报告）。
//   - 其余 to → keep：渲染为目标协议字面（anthropic: web_search_20250305 /
//     gemini: googleSearch / responses: web_search；同协议直通原样保留）。
//
// 响应侧合成期望：gemini grounding → anthropic 合成 server_tool_use +
// web_search_tool_result 块（跨任务集成路径）；gemini → openai 保留 canonical
// 载体（grounding_metadata 键），供下游二次转换消费。

// hostedToolProbes 是三个入站协议各带一条 hosted 搜索声明的最小真实请求。
var hostedToolProbes = map[Provider]string{
	ProviderAnthropic: `{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"web_search_20250305","name":"web_search"}]}`,
	ProviderGemini: `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],
		"tools":[{"googleSearch":{}}]}`,
	ProviderOpenAIResponses: `{"model":"m","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"tools":[{"type":"web_search"}]}`,
}

// hostedRenderedIn 检查出站请求体是否携带目标协议的 hosted 搜索字面。
func hostedRenderedIn(t *testing.T, out []byte, to Provider) bool {
	t.Helper()
	tools, _ := unmarshalAny(t, out)["tools"].([]any)
	for _, item := range tools {
		tool, _ := item.(map[string]any)
		switch to {
		case ProviderAnthropic:
			if tt, _ := tool["type"].(string); tt == "web_search_20250305" {
				return true
			}
		case ProviderGemini:
			if _, ok := tool["googleSearch"]; ok {
				return true
			}
		case ProviderOpenAIResponses:
			if tt, _ := tool["type"].(string); tt == "web_search" {
				return true
			}
		}
	}
	return false
}

// TestHostedToolMatrixRequestSide 请求侧全积：三个带 hosted 声明的入站探针
// × 四个出站目标。openai 目标必须剥离并计数，其余目标必须保留语义。
func TestHostedToolMatrixRequestSide(t *testing.T) {
	hosts := []Provider{ProviderAnthropic, ProviderGemini, ProviderOpenAIResponses}
	targets := []Provider{ProviderAnthropic, ProviderGemini, ProviderOpenAIResponses, ProviderOpenAI}
	for _, from := range hosts {
		for _, to := range targets {
			t.Run(fmt.Sprintf("%s→%s", from, to), func(t *testing.T) {
				out, stats, err := ComposeUpstreamRequest(from, to,
					[]byte(hostedToolProbes[from]), UpstreamRequest{Model: "m2"})
				if err != nil {
					t.Fatalf("转换失败: %v", err)
				}
				if to == ProviderOpenAI {
					// drop 格：上游不得收到任何 hosted 形状，且剥离必须计入遥测
					//（不可见的有损行为等于谎言）。
					if hostedRenderedIn(t, out, to) {
						t.Errorf("openai 上游不应收到 hosted 声明: %s", out)
					}
					if stats.HostedToolsStripped < 1 {
						t.Errorf("剥离应计入 HostedToolsStripped, 实际 %d", stats.HostedToolsStripped)
					}
					return
				}
				// keep 格：目标协议字面必须出现。
				if !hostedRenderedIn(t, out, to) {
					t.Errorf("%s 上游应保留 hosted 语义: %s", to, out)
				}
			})
		}
	}
}

// geminiGroundingResp 是带接地数据的 Gemini 非流式响应探针（camelCase 为
// Gemini REST 真实形状）。
const geminiGroundingResp = `{"candidates":[{"content":{"parts":[{"text":"答案"}]},
	"finishReason":"STOP",
	"groundingMetadata":{"webSearchQueries":["golang json"],
		"groundingChunks":[{"web":{"uri":"https://go.dev","title":"Go"}},
			{"web":{"uri":"https://pkg.go.dev","title":"Pkgs"}}]}}],
	"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`

// TestHostedToolMatrixResponseSynthesis 响应侧：gemini grounding → anthropic
// 合成 server_tool_use / web_search_tool_result 块；gemini → openai 保留
// canonical 载体（grounding_metadata）。
func TestHostedToolMatrixResponseSynthesis(t *testing.T) {
	out, err := ConvertResponse(ProviderGemini, ProviderAnthropic, []byte(geminiGroundingResp))
	if err != nil {
		t.Fatalf("gemini→anthropic 转换失败: %v", err)
	}
	s := string(out)
	stuIdx := strings.Index(s, `"server_tool_use"`)
	wsrIdx := strings.Index(s, `"web_search_tool_result"`)
	if stuIdx < 0 || wsrIdx < 0 {
		t.Fatalf("anthropic 响应应合成搜索过程块: %s", s)
	}
	if textIdx := strings.Index(s, `"text"`); textIdx < stuIdx {
		t.Errorf("合成块应在 text 之前（客户端按序拼接 content）: %s", s)
	}

	canonical, err := ConvertResponse(ProviderGemini, ProviderOpenAI, []byte(geminiGroundingResp))
	if err != nil {
		t.Fatalf("gemini→openai 转换失败: %v", err)
	}
	m := unmarshalAny(t, canonical)
	choice, _ := sliceAt(t, m, "choices")[0].(map[string]any)
	msg, _ := choice["message"].(map[string]any)
	gm, ok := msg["grounding_metadata"].(map[string]any)
	if !ok {
		t.Fatalf("canonical 应保留 grounding_metadata 载体: %s", canonical)
	}
	queries, _ := gm["search_queries"].([]any)
	if len(queries) != 1 || queries[0] != "golang json" {
		t.Errorf("search_queries 映射不符: %v", queries)
	}
	sources, _ := gm["sources"].([]any)
	if len(sources) != 2 {
		t.Errorf("sources 映射不符: %v", sources)
	}
}
