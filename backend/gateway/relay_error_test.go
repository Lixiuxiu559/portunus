package gateway

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// TestRelayErrorShapeAnthropicCircuitOpen 锁定协议化错误体：Anthropic 客户端在
// 分组全部目标熔断时收到 {"type":"error","error":{"type":"overloaded_error",...}}、
// Retry-After 与 request id——而不是 {"error":"..."} 通用体。Claude Code 靠协议内
// 错误类型与状态码归类重试，笼统错误体只会变成一句无意义的报错文案。
func TestRelayErrorShapeAnthropicCircuitOpen(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 0
	pc.CircuitFailureThreshold = 1
	setProxyConfigForTest(t, pc)

	r, key := setupGateway(t, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	var m model.Model
	if err := shared.DB.First(&m).Error; err != nil {
		t.Fatalf("查询模型失败: %v", err)
	}
	breakerRecord(m.ID, true) // 阈值 1，一次即开路

	w := doReq(t, r, "/v1/messages", `{"model":"my-model","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`, key)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d, want 503, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"type":"error"`) || !strings.Contains(body, `"overloaded_error"`) {
		t.Errorf("Anthropic 客户端应收到协议内错误体: %s", body)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Errorf("全目标熔断 503 应携带 Retry-After 头（最早冷却剩余秒数）")
	}
	if !strings.Contains(body, "request id: ") {
		t.Errorf("错误消息应追加 request id 便于对齐日志: %s", body)
	}
}

// TestRelay429RetryAfterPassthrough 锁定限流节奏透传：上游 429 带 Retry-After 时，
// 客户端收到同样的头与上游错误原文（按客户端协议重组），而不是笼统「上游调用失败」。
func TestRelay429RetryAfterPassthrough(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 0
	setProxyConfigForTest(t, pc)

	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "17")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limited by upstream","code":"rate_limit_exceeded"}}`))
	})
	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}]}`, key)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("状态码 = %d, want 429, body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got != "17" {
		t.Errorf("Retry-After 应透传上游原值 17, got %q", got)
	}
	body := w.Body.String()
	if !strings.Contains(body, "rate limited by upstream") {
		t.Errorf("错误消息应取上游原文: %s", body)
	}
	if !strings.Contains(body, `"rate_limit_error"`) {
		t.Errorf("OpenAI 错误体 type 应为 rate_limit_error: %s", body)
	}
}

// TestRelay404ShapeOpenAI 锁定 404 错误体形状与 request id 追加（OpenAI 客户端）。
func TestRelay404ShapeOpenAI(t *testing.T) {
	r, key := setupGateway(t, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	w := doReq(t, r, "/v1/chat/completions", `{"model":"no-such-group","messages":[]}`, key)
	if w.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d, want 404", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"invalid_request_error"`) {
		t.Errorf("OpenAI 404 错误体 type 应为 invalid_request_error: %s", body)
	}
	if !strings.Contains(body, "request id: ") {
		t.Errorf("错误消息应追加 request id: %s", body)
	}
}

// TestUpstreamErrorMessage 覆盖各上游错误体形状的消息提取。
func TestUpstreamErrorMessage(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"openai", `{"error":{"message":"boom","type":"server_error"}}`, "boom"},
		{"anthropic", `{"type":"error","error":{"type":"api_error","message":"overloaded"}}`, "overloaded"},
		{"error 为字符串", `{"error":"quota exceeded"}`, "quota exceeded"},
		{"顶层 message", `{"message":"bad key"}`, "bad key"},
		{"顶层 msg", `{"msg":"坏了"}`, "坏了"},
		{"非 JSON", `plain text`, ""},
		{"空体", ``, ""},
	}
	for _, tc := range cases {
		if got := upstreamErrorMessage([]byte(tc.body)); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
