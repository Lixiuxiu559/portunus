package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
)

// hosted web_search 的端到端链路验证：真实走 /v1 的路由、鉴权、分组选路与
// 协议转换（非 protocol 包内黑盒），锁定三件事——
//  1. Claude Code（anthropic）的 web_search_20250305 声明到达 gemini 渠道时
//     变成 googleSearch，grounding 响应回流合成为 server_tool_use 块；
//  2. 同一声明到 openai 渠道（DeepSeek/GLM 场景）被剥离并计入日志遥测，
//     上游不再收到空壳 function（历史上的畸形输出）；
//  3. 流式链路的合成块先于文本。

// TestRelayHostedWebSearchAnthropicToGemini gemini 渠道：声明抵达为
// googleSearch，grounding 回流合成为 anthropic 搜索过程块。
func TestRelayHostedWebSearchAnthropicToGemini(t *testing.T) {
	var gotBody []byte
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"答案"}]},
			"finishReason":"STOP",
			"groundingMetadata":{"webSearchQueries":["golang"],
				"groundingChunks":[{"web":{"uri":"https://go.dev","title":"Go"}}]}}],
			"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`))
	})

	r, key, _ := setupGateway(t, protocol.ProviderGemini, upstream)
	w := doReq(t, r, "/v1/messages", `{"model":"my-model","max_tokens":64,
		"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"web_search_20250305","name":"web_search"}]}`, key)

	if w.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
	}
	var upReq map[string]any
	if err := json.Unmarshal(gotBody, &upReq); err != nil {
		t.Fatalf("解析上游请求失败: %v", err)
	}
	tools, _ := upReq["tools"].([]any)
	found := false
	for _, item := range tools {
		tool, _ := item.(map[string]any)
		if _, ok := tool["googleSearch"]; ok {
			found = true
		}
	}
	if !found {
		t.Errorf("gemini 上游应收到 googleSearch 声明: %s", gotBody)
	}

	body := w.Body.String()
	for _, want := range []string{`"server_tool_use"`, `"web_search_tool_result"`, `"type":"text"`, `"text":"答案"`} {
		if !strings.Contains(body, want) {
			t.Errorf("anthropic 客户端响应缺少 %s: %s", want, body)
		}
	}
	if strings.Index(body, `"server_tool_use"`) > strings.Index(body, `"type":"text"`) {
		t.Errorf("合成块应在 text 块之前: %s", body)
	}
}

// TestRelayHostedWebSearchStrippedToOpenAI openai 渠道（DeepSeek/GLM 场景）：
// hosted 声明被剥离，上游收到零畸形工具，剥离计入日志遥测。
func TestRelayHostedWebSearchStrippedToOpenAI(t *testing.T) {
	var gotBody []byte
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","object":"chat.completion","model":"upstream-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	})

	r, key, logs := setupGateway(t, protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/messages", `{"model":"my-model","max_tokens":64,
		"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"web_search_20250305","name":"web_search"}]}`, key)

	if w.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(string(gotBody), "web_search") {
		t.Errorf("openai 上游不应收到 hosted 声明: %s", gotBody)
	}
	last := logs.last()
	if last == nil {
		t.Fatal("未记录调用日志")
	}
	if last.HostedToolsStripped != 1 {
		t.Errorf("HostedToolsStripped = %d, want 1（剥离必须计入日志遥测）", last.HostedToolsStripped)
	}
}

// TestRelayHostedWebSearchStreamAnthropicToGemini 流式链路：grounding 随
// Gemini SSE chunk 到达，anthropic 客户端的合成块事件先于文本。
func TestRelayHostedWebSearchStreamAnthropicToGemini(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"答\"}]}}]}\n\n"))
		w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"案\"}]},\"finishReason\":\"STOP\",\"groundingMetadata\":{\"webSearchQueries\":[\"golang\"],\"groundingChunks\":[{\"web\":{\"uri\":\"https://go.dev\",\"title\":\"Go\"}}]}}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":5}}\n\n"))
		w.(http.Flusher).Flush()
	})

	r, key, _ := setupGateway(t, protocol.ProviderGemini, upstream)
	w := doReq(t, r, "/v1/messages", `{"model":"my-model","max_tokens":64,"stream":true,
		"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"web_search_20250305","name":"web_search"}]}`, key)

	if w.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	stu := strings.Index(body, `"server_tool_use"`)
	wsr := strings.Index(body, `"web_search_tool_result"`)
	if stu < 0 || wsr < 0 {
		t.Fatalf("流式响应缺少合成块: %s", body)
	}
	// 晚到时序（Gemini 真实形状）：grounding 随末 chunk 到达，合成块插在
	// 已写文本与续写文本之间（text → 合成块 → text），配对顺序不变。
	if stu >= wsr {
		t.Errorf("server_tool_use 应先于 web_search_tool_result: %s", body)
	}
	if !strings.Contains(body, `"text":"答"`) || !strings.Contains(body, `"text":"案"`) {
		t.Errorf("两段文本应完整保留: %s", body)
	}
}
