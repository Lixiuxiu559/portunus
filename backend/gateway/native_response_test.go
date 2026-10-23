package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// 响应诊断只记录静态摘要，不把原生载荷写入调用日志。
func assertNativeResponseSummary(t *testing.T, row *shared.Log, scope string, compatible bool) {
	t.Helper()
	if row == nil || !row.ResponseConversionChecked || row.ResponseConversionScope != scope || row.ResponseConversionCompatible != compatible {
		t.Fatalf("响应诊断不符: %+v", row)
	}
	if compatible && row.ResponseConversionIssueCount != 0 || !compatible && row.ResponseConversionIssueCount == 0 {
		t.Fatalf("响应诊断计数不符: %+v", row)
	}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"native-secret-signature", "native-secret-ciphertext", "native-secret-vendor"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("日志泄漏原生载荷: %s", raw)
		}
	}
}

func TestNativeResponseSameProtocolPreservesCompleteBody(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		provider                protocol.Provider
		path, request, response string
	}{
		{"chat", protocol.ProviderOpenAI, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hi"}]}`, ` {"vendor":"native-secret-vendor","n":9007199254740993,"choices":[{"index":0,"message":{"role":"assistant","content":"hi","tool_calls":[{"id":"call-1","type":"function","function":{"name":"f","arguments":"{}"},"signature":"native-secret-signature"}],"encrypted_content":"native-secret-ciphertext"},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}} `},
		{"messages", protocol.ProviderAnthropic, "/v1/messages", `{"model":"my-model","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`, ` {"id":"msg-1","type":"message","role":"assistant","vendor":"native-secret-vendor","n":9007199254740993,"content":[{"type":"thinking","thinking":"reason","signature":"native-secret-signature"},{"type":"redacted_thinking","data":"native-secret-ciphertext"},{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}} `},
		{"responses", protocol.ProviderOpenAIResponses, "/v1/responses", `{"model":"my-model","input":"hi"}`, ` {"id":"resp-1","status":"completed","vendor":"native-secret-vendor","n":9007199254740993,"output":[{"type":"reasoning","id":"rs-1","summary":[],"encrypted_content":"native-secret-ciphertext","signature":"native-secret-signature"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi","annotations":[]}]}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}} `},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, key, logs := setupGateway(t, tc.provider, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, tc.response)
			}))
			got := doReq(t, r, tc.path, tc.request, key)
			if got.Code != 200 || got.Body.String() != tc.response {
				t.Fatalf("原生响应发生改写: status=%d body=%s", got.Code, got.Body.String())
			}
			row := logs.last()
			assertNativeResponseSummary(t, row, "response_native_v1", true)
			if !row.Success || row.InputToken != 3 || row.OutputToken != 2 || !row.ConversionCompatible {
				t.Fatalf("用量或请求摘要改变: %+v", row)
			}
		})
	}
}

func TestNativeResponseCrossProtocolDiagnosesWithoutRejecting(t *testing.T) {
	response := `{"id":"chat-1","vendor":"native-secret-vendor","n":9007199254740993,"choices":[{"index":0,"message":{"role":"assistant","content":"hello","signature":"native-secret-signature","encrypted_content":"native-secret-ciphertext"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
	r, key, logs := setupGateway(t, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, response) }))
	got := doReq(t, r, "/v1/messages", `{"model":"my-model","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`, key)
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"text":"hello"`) {
		t.Fatalf("正常转换失败: status=%d body=%s", got.Code, got.Body.String())
	}
	for _, secret := range []string{"native-secret-vendor", "native-secret-signature", "native-secret-ciphertext", "9007199254740993"} {
		if strings.Contains(got.Body.String(), secret) {
			t.Fatalf("原生载荷进入目标协议: %s", got.Body.String())
		}
	}
	row := logs.last()
	assertNativeResponseSummary(t, row, "response_native_v1", false)
	if !row.Success || row.InputToken != 3 || row.OutputToken != 2 || !row.ConversionCompatible {
		t.Fatalf("用量或独立请求摘要改变: %+v", row)
	}
}

func TestNativeResponseSameProtocolPreservesCompleteSSE(t *testing.T) {
	raw := ": native prelude\r\nid: opaque-1\r\nevent: vendor.native\r\nretry: 42\r\ndata: {\"vendor\":\"native-secret-vendor\",\r\ndata: \"n\":9007199254740993,\"signature\":\"native-secret-signature\",\"encrypted_content\":\"native-secret-ciphertext\"}\r\n\r\n" + deliveryFinish + "data: [DONE]\n\n"
	r, key, logs := setupGateway(t, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, raw)
	}))
	got := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`, key)
	if got.Code != 200 || got.Body.String() != raw {
		t.Fatalf("原生 SSE 改写: status=%d body=%q", got.Code, got.Body.String())
	}
	row := logs.last()
	assertNativeResponseSummary(t, row, "stream_native_v1", true)
	if !row.Success || !row.StreamTerminalWritten || !row.StreamDeliveryStarted {
		t.Fatalf("终态交付改变: %+v", row)
	}
}

func TestNativeResponseCrossProtocolStreamDeltaExtension(t *testing.T) {
	raw := "data: {\"id\":\"chat-1\",\"vendor\":\"native-secret-vendor\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\",\"signature\":\"native-secret-signature\",\"encrypted_content\":\"native-secret-ciphertext\"}}]}\n\n" + deliveryFinish + "data: [DONE]\n\n"
	r, key, logs := setupGateway(t, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, raw)
	}))
	got := doReq(t, r, "/v1/messages", `{"model":"my-model","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`, key)
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"text":"hello"`) || strings.Count(got.Body.String(), "event: message_stop") != 1 {
		t.Fatalf("跨协议正常流失败: %d %s", got.Code, got.Body.String())
	}
	for _, secret := range []string{"native-secret-vendor", "native-secret-signature", "native-secret-ciphertext"} {
		if strings.Contains(got.Body.String(), secret) {
			t.Fatalf("原生扩展进入目标: %s", got.Body.String())
		}
	}
	row := logs.last()
	assertNativeResponseSummary(t, row, "stream_native_v1", false)
	if !row.Success {
		t.Fatalf("诊断导致拒绝: %+v", row)
	}
}

func TestNativeResponseRetryKeepsAttemptDiagnosticsIsolated(t *testing.T) {
	cfg := shared.DefaultProxyConfig()
	cfg.RetryCount = 1
	cfg.MaxHTTPAttempts = 2
	var calls atomic.Int32
	attempts := &attemptRecorder{}
	r, key, logs := setupGatewayDeps(t, func(d *Deps) { withCfg(cfg)(d); d.AttemptWrite = attempts.write }, protocol.ProviderOpenAIResponses, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			io.WriteString(w, "data: {\"type\":\"response.created\",\"vendor\":\"native-secret-vendor\",\"response\":{\"id\":\"failed-native-id\",\"status\":\"in_progress\",\"output\":[]}}\n\n")
			return
		}
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"ok-id\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":2,\"total_tokens\":5}}}\n\n")
	}))
	got := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`, key)
	if got.Code != 200 || calls.Load() != 2 || strings.Contains(got.Body.String(), "failed-native-id") || strings.Contains(got.Body.String(), "native-secret-vendor") {
		t.Fatalf("失败尝试污染最终响应: calls=%d status=%d body=%s", calls.Load(), got.Code, got.Body.String())
	}
	rows := attempts.entries()
	if len(rows) != 2 || rows[0].Success || !rows[1].Success {
		t.Fatalf("HTTP attempt记录改变: %+v", rows)
	}
	logs.mu.Lock()
	entries := append([]*shared.Log(nil), logs.logs...)
	logs.mu.Unlock()
	if len(entries) != 2 {
		t.Fatalf("调用日志数=%d", len(entries))
	}
	assertNativeResponseSummary(t, entries[0], "stream_native_v1", false)
	assertNativeResponseSummary(t, entries[1], "stream_native_v1", true)
	if entries[0].Success || !entries[1].Success || entries[1].InputToken != 3 || entries[1].OutputToken != 2 {
		t.Fatalf("attempt摘要/用量不符: %+v %+v", entries[0], entries[1])
	}
}

func TestNativeResponseUnknownDeliveredEventStillCannotReplay(t *testing.T) {
	cfg := shared.DefaultProxyConfig()
	cfg.RetryCount = 2
	cfg.MaxHTTPAttempts = 3
	var calls atomic.Int32
	attempts := &attemptRecorder{}
	raw := "event: vendor.native\ndata: {\"type\":\"vendor.native\",\"vendor\":\"native-secret-vendor\"}\n\n"
	r, key, logs := setupGatewayDeps(t, func(d *Deps) { withCfg(cfg)(d); d.AttemptWrite = attempts.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, raw)
	}))
	got := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`, key)
	if calls.Load() != 1 || len(attempts.entries()) != 1 || !strings.HasPrefix(got.Body.String(), raw) || strings.Contains(got.Body.String(), "[DONE]") {
		t.Fatalf("交付后发生重放或伪造终态: calls=%d body=%s", calls.Load(), got.Body.String())
	}
	row := logs.last()
	assertNativeResponseSummary(t, row, "stream_native_v1", true)
	if row.Success || !row.StreamDeliveryStarted || row.StreamTerminalWritten {
		t.Fatalf("未完成流安全边界改变: %+v", row)
	}
}
