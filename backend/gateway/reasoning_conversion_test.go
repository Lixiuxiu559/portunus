package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
)

// 捕获网关实际发送的 HTTP 请求，避免只验证协议转换函数而遗漏组合请求时的覆盖。
func TestRelayReasoningConversion(t *testing.T) {
	cases := []struct {
		name     string
		upstream protocol.Provider
		path     string
		body     string
		want     map[string]any
		absent   []string
	}{
		{
			name: "Claude到Chat保留max且不猜测开关", upstream: protocol.ProviderOpenAI, path: "/v1/messages",
			body: `{"model":"my-model","max_tokens":64,"thinking":{"type":"adaptive"},"output_config":{"effort":"max"},"messages":[{"role":"user","content":"hello"}]}`,
			want: map[string]any{"reasoning_effort": "max"}, absent: []string{"thinking", "output_config"},
		},
		{
			name: "Codex到Chat保留high", upstream: protocol.ProviderOpenAI, path: "/v1/responses",
			body: `{"model":"my-model","reasoning":{"effort":"high"},"input":"hello"}`,
			want: map[string]any{"reasoning_effort": "high"}, absent: []string{"reasoning", "thinking"},
		},
		{
			name: "Claude到Responses保留max", upstream: protocol.ProviderOpenAIResponses, path: "/v1/messages",
			body: `{"model":"my-model","max_tokens":64,"output_config":{"effort":"max"},"messages":[{"role":"user","content":"hello"}]}`,
			want: map[string]any{"reasoning": map[string]any{"effort": "max"}}, absent: []string{"output_config", "thinking"},
		},
		{
			name: "Codex到Anthropic保留high", upstream: protocol.ProviderAnthropic, path: "/v1/responses",
			body: `{"model":"my-model","reasoning":{"effort":"high"},"input":"hello"}`,
			want: map[string]any{"output_config": map[string]any{"effort": "high"}}, absent: []string{"reasoning", "thinking"},
		},
		{
			name: "Chat同协议保留未知扩展", upstream: protocol.ProviderOpenAI, path: "/v1/chat/completions",
			body: `{"model":"my-model","reasoning_effort":"future-effort","thinking":{"type":"custom","future_flag":true},"messages":[{"role":"user","content":"hello"}]}`,
			want: map[string]any{"reasoning_effort": "future-effort", "thinking": map[string]any{"type": "custom", "future_flag": true}},
		},
		{
			name: "Claude同协议保留未知扩展", upstream: protocol.ProviderAnthropic, path: "/v1/messages",
			body: `{"model":"my-model","max_tokens":64,"thinking":{"type":"adaptive","future_flag":true},"output_config":{"effort":"max","future_flag":"keep"},"messages":[{"role":"user","content":"hello"}]}`,
			want: map[string]any{"thinking": map[string]any{"type": "adaptive", "future_flag": true}, "output_config": map[string]any{"effort": "max", "future_flag": "keep"}},
		},
		{
			name: "Codex同协议保留未知扩展", upstream: protocol.ProviderOpenAIResponses, path: "/v1/responses",
			body: `{"model":"my-model","reasoning":{"effort":"high","summary":"auto","future_flag":true},"input":"hello"}`,
			want: map[string]any{"reasoning": map[string]any{"effort": "high", "summary": "auto", "future_flag": true}},
		},
		{
			name: "显式关闭优先于thinking后缀", upstream: protocol.ProviderOpenAI, path: "/v1/chat/completions",
			body: `{"model":"my-model-thinking","thinking":{"type":"disabled"},"messages":[{"role":"user","content":"hello"}]}`,
			want: map[string]any{"thinking": map[string]any{"type": "disabled"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotBody []byte
			upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				switch tc.upstream {
				case protocol.ProviderAnthropic:
					_, _ = io.WriteString(w, `{"id":"msg-1","type":"message","role":"assistant","model":"upstream-model","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
				case protocol.ProviderOpenAIResponses:
					_, _ = io.WriteString(w, `{"id":"resp-1","object":"response","status":"completed","model":"upstream-model","output":[{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
				default:
					_, _ = io.WriteString(w, `{"id":"chat-1","object":"chat.completion","model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
				}
			})
			r, key, _ := setupGateway(t, tc.upstream, upstream)
			w := doReq(t, r, tc.path, tc.body, key)
			if w.Code != http.StatusOK {
				t.Fatalf("网关返回 %d: %s", w.Code, w.Body.String())
			}
			var got map[string]any
			if err := json.Unmarshal(gotBody, &got); err != nil {
				t.Fatalf("解析上游请求: %v; body=%s", err, gotBody)
			}
			if got["model"] != "upstream-model" {
				t.Errorf("上游模型应为未知品牌的 upstream-model，实际 %v", got["model"])
			}
			for field, want := range tc.want {
				if !reflect.DeepEqual(got[field], want) {
					t.Errorf("上游 %s = %#v，期望 %#v；请求=%s", field, got[field], want, gotBody)
				}
			}
			for _, field := range tc.absent {
				if value, exists := got[field]; exists {
					t.Errorf("上游不应携带 %s=%#v；请求=%s", field, value, gotBody)
				}
			}
		})
	}
}

func TestRelayReasoningUnsupportedBudget(t *testing.T) {
	var calls atomic.Int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	r, key, logs := setupGateway(t, protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/messages", `{"model":"my-model","max_tokens":4096,"thinking":{"type":"enabled","budget_tokens":2048},"messages":[{"role":"user","content":"hello"}]}`, key)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("无法表达的预算应返回400，实际 %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("解析错误响应: %v", err)
	}
	if response.Error.Type != "invalid_request_error" || !strings.Contains(response.Error.Message, "budget") {
		t.Errorf("期望包含budget的invalid_request_error，实际 %s", w.Body.String())
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("转换失败不应请求上游，实际请求 %d 次", got)
	}
	entry := logs.last()
	if entry == nil {
		t.Fatal("转换失败应记录调用日志")
	}
	if entry.ErrKind != "convert_error" {
		t.Errorf("转换失败不应标记上游故障，实际 err_kind=%q", entry.ErrKind)
	}
}

func TestRelayReasoningConversionStream(t *testing.T) {
	var gotBody []byte
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chat-1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":null}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"chat-1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	})
	r, key, _ := setupGateway(t, protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/messages", `{"model":"my-model","stream":true,"max_tokens":64,"thinking":{"type":"adaptive"},"output_config":{"effort":"max"},"messages":[{"role":"user","content":"hello"}]}`, key)
	if w.Code != http.StatusOK {
		t.Fatalf("流式请求返回 %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(gotBody, &got); err != nil {
		t.Fatalf("解析上游请求: %v; body=%s", err, gotBody)
	}
	if got["stream"] != true || got["reasoning_effort"] != "max" {
		t.Errorf("上游应收到stream=true和reasoning_effort=max，实际 %s", gotBody)
	}
	if _, exists := got["thinking"]; exists {
		t.Errorf("上游不应注入非标准thinking开关，实际 %s", gotBody)
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Errorf("客户端应收到SSE，实际 Content-Type=%q", w.Header().Get("Content-Type"))
	}
	for _, marker := range []string{"event: message_start", "event: content_block_delta", `"text":"hello"`, "event: message_stop"} {
		if !strings.Contains(w.Body.String(), marker) {
			t.Errorf("客户端Anthropic SSE缺少 %q: %s", marker, w.Body.String())
		}
	}
}
