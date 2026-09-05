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

// TestInjectThinkingParam 单元锁定注入形状：Anthropic 客户端自带 thinking 配置时
// 沿用其 budget_tokens；其他协议只发 type=enabled（budget 可选，缺省走上游默认）。
func TestInjectThinkingParam(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)

	// Anthropic 客户端带 budget → 沿用
	out, err := injectThinkingParam(body, []byte(`{"model":"m","thinking":{"type":"enabled","budget_tokens":2048}}`), protocol.ProviderAnthropic)
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("解析注入结果失败: %v", err)
	}
	th, ok := m["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("应注入 thinking: %s", out)
	}
	if th["budget_tokens"] != float64(2048) {
		t.Errorf("应沿用客户端 budget_tokens=2048, 实际 %v", th["budget_tokens"])
	}

	// 非 Anthropic 客户端 → 只发 type=enabled
	out2, err := injectThinkingParam(body, []byte(`{}`), protocol.ProviderOpenAI)
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	m2 := map[string]any{}
	if err := json.Unmarshal(out2, &m2); err != nil {
		t.Fatalf("解析注入结果失败: %v", err)
	}
	th2, ok := m2["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("应注入 thinking: %s", out2)
	}
	if _, has := th2["budget_tokens"]; has {
		t.Errorf("非 Anthropic 客户端不应带 budget_tokens: %s", out2)
	}

	// 非法请求体 → 报错（由上层包装为 convertError 归因）
	if _, err := injectThinkingParam([]byte(`not-json`), nil, protocol.ProviderOpenAI); err == nil {
		t.Error("非法请求体应报错")
	}
}
