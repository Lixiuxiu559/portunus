package gateway

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// 通过真实网关入口锁定同协议 payload 透传；不将其扩大为 SSE 原始帧逐字节保证。
func TestRelayTypedStreamKeepsNativePayloadAndUsage(t *testing.T) {
	const unknown = `{"type":"vendor.native", "n":9007199254740993, "payload":{"unknown":[null, false]}}`
	for _, tc := range []struct {
		name                 string
		provider             protocol.Provider
		path, body, terminal string
	}{
		{"Chat", protocol.ProviderOpenAI, "/v1/chat/completions", `{"model":"my-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`,
			`{"id":"native","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`},
		{"Messages", protocol.ProviderAnthropic, "/v1/messages", `{"model":"my-model","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`,
			`{"type":"message_start","message":{"id":"native","role":"assistant","content":[],"usage":{"input_tokens":3,"output_tokens":2}}}`},
		{"Responses", protocol.ProviderOpenAIResponses, "/v1/responses", `{"model":"my-model","stream":true,"input":"hi"}`,
			`{"type":"response.completed","response":{"id":"native","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls, attempts atomic.Int32
			r, key, logs := setupGatewayDeps(t, func(d *Deps) {
				d.AttemptWrite = func(*shared.UpstreamAttempt) error { attempts.Add(1); return nil }
			}, tc.provider, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\ndata: %s\n\n", unknown, tc.terminal)
				if tc.provider == protocol.ProviderAnthropic {
					fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
				} else {
					fmt.Fprint(w, "data: [DONE]\n\n")
				}
			}))
			w := doReq(t, r, tc.path, tc.body, key)
			if w.Code != 200 || calls.Load() != 1 || attempts.Load() != 1 {
				t.Fatalf("同协议调用行为改变: status=%d calls=%d attempts=%d body=%s", w.Code, calls.Load(), attempts.Load(), w.Body.String())
			}
			if strings.Count(w.Body.String(), "data: "+unknown+"\n\n") != 1 {
				t.Fatalf("原生未知payload未逐字节保留: %s", w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "data: "+tc.terminal+"\n\n") {
				t.Fatalf("原生usage事件被改写: %s", w.Body.String())
			}
			entry := logs.last()
			if entry == nil || !entry.Success || entry.InputToken != 3 || entry.OutputToken != 2 || entry.ConversionPolicy != "" {
				t.Fatalf("透传用量/默认模式改变: %+v", entry)
			}
		})
	}
}
