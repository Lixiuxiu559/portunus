package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// TestRelayThinkingSuffixInjectsParam 锁定 -thinking 后缀约定：分组名带后缀时剥后缀
// 解析分组，并对 OpenAI 兼容上游注入 thinking 开关；上游模型名不带后缀；不带后缀的
// 请求完全不注入（这正是该约定比全局转发安全的地方）。
func TestRelayThinkingSuffixInjectsParam(t *testing.T) {
	setProxyConfigForTest(t, shared.DefaultProxyConfig())

	var gotBody []byte
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"1","object":"chat.completion","model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	})
	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)

	// 带 -thinking 后缀：注入 thinking 开关
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model-thinking","messages":[{"role":"user","content":"hello"}]}`, key)
	if w.Code != 200 {
		t.Fatalf("带 -thinking 后缀应正常服务，实际 %d, body=%s", w.Code, w.Body.String())
	}
	m := map[string]any{}
	if err := json.Unmarshal(gotBody, &m); err != nil {
		t.Fatalf("解析上游请求失败: %v\n%s", err, gotBody)
	}
	th, ok := m["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("上游请求应注入 thinking 参数: %s", gotBody)
	}
	if th["type"] != "enabled" {
		t.Errorf("thinking.type 应=enabled, 实际 %v", th["type"])
	}
	if m["model"] != "upstream-model" {
		t.Errorf("上游模型名不应带后缀: %v", m["model"])
	}

	// 对照：不带后缀不注入
	gotBody = nil
	w2 := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}]}`, key)
	if w2.Code != 200 {
		t.Fatalf("不带后缀应正常服务，实际 %d", w2.Code)
	}
	m2 := map[string]any{}
	if err := json.Unmarshal(gotBody, &m2); err != nil {
		t.Fatalf("解析上游请求失败: %v\n%s", err, gotBody)
	}
	if _, has := m2["thinking"]; has {
		t.Errorf("不带 -thinking 后缀不应注入 thinking 参数: %s", gotBody)
	}
}

// TestRelayThinkingSuffixAnthropicBudget 锁定 budget 复用：anthropic 客户端自带
// thinking 配置（Claude Code 的 effort 映射在 budget_tokens）且分组名带 -thinking
// 后缀时，openai 兼容上游应收到沿用客户端值的 budget_tokens。注入本身由 protocol
// 的 ComposeUpstreamRequest 完成（形状契约见 protocol/compose_test.go）。
func TestRelayThinkingSuffixAnthropicBudget(t *testing.T) {
	setProxyConfigForTest(t, shared.DefaultProxyConfig())

	var gotBody []byte
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"1","object":"chat.completion","model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	})
	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)

	w := doReq(t, r, "/v1/messages", `{"model":"my-model-thinking","max_tokens":64,"thinking":{"type":"enabled","budget_tokens":2048},"messages":[{"role":"user","content":"hello"}]}`, key)
	if w.Code != 200 {
		t.Fatalf("anthropic 客户端 -thinking 应正常服务，实际 %d, body=%s", w.Code, w.Body.String())
	}
	m := map[string]any{}
	if err := json.Unmarshal(gotBody, &m); err != nil {
		t.Fatalf("解析上游请求失败: %v\n%s", err, gotBody)
	}
	th, ok := m["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("openai 上游应收到 thinking 参数: %s", gotBody)
	}
	if th["budget_tokens"] != float64(2048) {
		t.Errorf("应沿用客户端 budget_tokens=2048, 实际 %v", th["budget_tokens"])
	}
	if m["model"] != "upstream-model" {
		t.Errorf("上游模型名不应带后缀: %v", m["model"])
	}
}
