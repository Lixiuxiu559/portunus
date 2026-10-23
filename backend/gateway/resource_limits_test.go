package gateway

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/resource"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func TestResourceRequestBodyBoundedBeforeHTTP(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprintf("unknown_length_%v", unknown), func(t *testing.T) {
			pc := shared.DefaultProxyConfig()
			pc.ResourceLimits.MaxRequestBytes = 128
			var calls atomic.Int32
			attempts := &attemptRecorder{}
			r, key, _ := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.AttemptWrite = attempts.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1) }))
			body := `{"model":"my-model","messages":[{"role":"user","content":"` + strings.Repeat("private-secret", 30) + `"}]}`
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			if unknown {
				req.ContentLength = -1
				req.TransferEncoding = []string{"chunked"}
			}
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != 413 || calls.Load() != 0 || len(attempts.entries()) != 0 {
				t.Fatalf("status=%d HTTP=%d attempts=%d body=%s", w.Code, calls.Load(), len(attempts.entries()), w.Body.String())
			}
			if strings.Contains(w.Body.String(), "private-secret") {
				t.Fatal("request payload leaked in limit error")
			}
		})
	}
}

func TestResourceRequestStructuredLimitsBeforeHTTP(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		limits     resource.Limits
	}{
		{"native_fragments", `{"model":"my-model","messages":[{"role":"user","content":"hi"}],"unknown":1}`, resource.Limits{MaxNativeFragments: 2}},
		{"json_depth", `{"model":"my-model","messages":[{"role":"user","content":"hi"}]}`, resource.Limits{MaxJSONDepth: 2}},
		{"tool_arguments", `{"model":"my-model","messages":[{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"private-secret"}}]}]}`, resource.Limits{MaxToolArgumentsBytes: 4}},
		{"accumulated_text", `{"model":"my-model","messages":[{"role":"user","content":"abc"},{"role":"user","content":"def"}]}`, resource.Limits{MaxAccumulatedBytes: 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pc := shared.DefaultProxyConfig()
			pc.ResourceLimits = tc.limits
			var calls atomic.Int32
			attempts := &attemptRecorder{}
			r, key, logs := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.AttemptWrite = attempts.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
			w := doReq(t, r, "/v1/chat/completions", tc.body, key)
			if w.Code != 413 || calls.Load() != 0 || len(attempts.entries()) != 0 {
				t.Fatalf("status=%d HTTP=%d attempts=%d body=%s", w.Code, calls.Load(), len(attempts.entries()), w.Body.String())
			}
			if strings.Contains(w.Body.String(), "private-secret") {
				t.Fatal("request value leaked")
			}
			if row := logs.last(); row != nil && row.ErrKind != shared.ErrKindResourceLimit {
				t.Fatalf("log=%+v", row)
			}
		})
	}
}

func TestResourceResponseBodyStopsRetriesAndFailover(t *testing.T) {
	for _, status := range []int{200, 503, 400} {
		for _, unknown := range []bool{false, true} {
			t.Run(fmt.Sprintf("status_%d_unknown_%v", status, unknown), func(t *testing.T) {
				pc := shared.DefaultProxyConfig()
				pc.RetryCount = 2
				pc.ResourceLimits.MaxResponseBytes = 128
				var calls atomic.Int32
				var breakers *BreakerStore
				attempts := &attemptRecorder{}
				r, key, logs := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.AttemptWrite = attempts.write; breakers = d.Breakers }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					body := `{"error":{"message":"max_completion_tokens unsupported ` + strings.Repeat("private-secret", 40) + `"}}`
					w.Header().Set("Content-Type", "application/json")
					if !unknown {
						w.Header().Set("Content-Length", strconv.Itoa(len(body)))
					}
					w.WriteHeader(status)
					if unknown {
						w.(http.Flusher).Flush()
					}
					io.WriteString(w, body)
				}))
				requestControlFallback(t)
				w := doReq(t, r, "/v1/responses", `{"model":"my-model","input":"hi","max_output_tokens":64}`, key)
				rows := attempts.entries()
				if w.Code != 502 || calls.Load() != 1 || len(rows) != 1 {
					t.Fatalf("status=%d HTTP=%d rows=%+v body=%s", w.Code, calls.Load(), rows, w.Body.String())
				}
				if rows[0].Status != status || rows[0].ErrKind != shared.ErrKindResourceLimit || rows[0].Success {
					t.Fatalf("attempt=%+v", rows[0])
				}
				if row := logs.last(); row == nil || row.ErrKind != shared.ErrKindResourceLimit || row.Success {
					t.Fatalf("log=%+v", row)
				}
				assertNoLocalControlBreakerFailure(t, breakers, rows[0].ModelID)
				if strings.Contains(w.Body.String(), "private-secret") || strings.Contains(rows[0].ErrMsg, "private-secret") {
					t.Fatal("oversized upstream payload leaked")
				}
			})
		}
	}
}

func TestResourceStreamAccumulationStopsAfterDelivery(t *testing.T) {
	for _, kind := range []string{"text", "reasoning", "tool"} {
		t.Run(kind, func(t *testing.T) {
			pc := shared.DefaultProxyConfig()
			pc.RetryCount = 2
			pc.ResourceLimits.MaxAccumulatedBytes = 8
			pc.ResourceLimits.MaxToolArgumentsBytes = 8
			var calls atomic.Int32
			var breakers *BreakerStore
			attempts := &attemptRecorder{}
			r, key, logs := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.AttemptWrite = attempts.write; breakers = d.Breakers }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				for i, s := range []string{"abcd", "efghi"} {
					var delta string
					switch kind {
					case "text":
						delta = fmt.Sprintf(`{"content":%q}`, s)
					case "reasoning":
						delta = fmt.Sprintf(`{"reasoning_content":%q}`, s)
					case "tool":
						if i == 0 {
							delta = fmt.Sprintf(`{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"f","arguments":%q}}]}`, s)
						} else {
							delta = fmt.Sprintf(`{"tool_calls":[{"index":0,"function":{"arguments":%q}}]}`, s)
						}
					}
					fmt.Fprintf(w, "data: {\"id\":\"1\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":%s}]}\n\n", delta)
					w.(http.Flusher).Flush()
				}
				io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			requestControlFallback(t)
			w := doReq(t, r, "/v1/messages", `{"model":"my-model","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, key)
			rows := attempts.entries()
			if w.Code != 200 || calls.Load() != 1 || len(rows) != 1 {
				t.Fatalf("status=%d HTTP=%d rows=%+v body=%s", w.Code, calls.Load(), rows, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "abcd") || !strings.Contains(w.Body.String(), "event: error") || strings.Contains(w.Body.String(), "event: message_stop") || strings.Contains(w.Body.String(), "[DONE]") || strings.Contains(w.Body.String(), "efghi") {
				t.Fatalf("invalid delivered failure body=%s", w.Body.String())
			}
			if row := logs.last(); row == nil || row.Success || row.ErrKind != shared.ErrKindResourceLimit || !row.StreamDeliveryStarted {
				t.Fatalf("log=%+v", row)
			}
			if rows[0].Status != 200 || rows[0].Success || rows[0].ErrKind != shared.ErrKindResourceLimit {
				t.Fatalf("attempt=%+v", rows[0])
			}
			assertNoLocalControlBreakerFailure(t, breakers, rows[0].ModelID)
		})
	}
}

func TestResourceNormalNativeWireAndHeaders(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.ResourceLimits = resource.Limits{MaxRequestBytes: 1024, MaxResponseBytes: 1024, MaxNativeFragments: 32, MaxToolArgumentsBytes: 64, MaxAccumulatedBytes: 64, MaxJSONDepth: 16}
	response := `{ "id":"1", "choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"unknown":{"integer":9007199254740993}}`
	var gotBody, gotAuth, gotContentType string
	r, key, logs := setupGatewayDeps(t, withCfg(pc), protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, response)
	}))
	request := `{"messages":[{"role":"user","content":"hi"}],"model":"my-model","native_extension":{"integer":9007199254740993}}`
	w := doReq(t, r, "/v1/chat/completions", request, key)
	if w.Code != 200 || w.Body.String() != response {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if gotBody != strings.Replace(request, `"my-model"`, `"upstream-model"`, 1) || gotAuth != "Bearer sk-test" || gotContentType != "application/json" {
		t.Fatalf("request=%s auth=%q content-type=%q", gotBody, gotAuth, gotContentType)
	}
	if row := logs.last(); row == nil || !row.Success || !row.ResponseConversionChecked {
		t.Fatalf("log=%+v", row)
	}
}

func TestResourceResponseStructuredLimitsStopBeforeDelivery(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		limits     resource.Limits
	}{
		{"native_fragments", `{"id":"1","choices":[{"message":{"content":"a"}},{"message":{"content":"b"}}],"usage":{},"unknown":1}`, resource.Limits{MaxNativeFragments: 4}},
		{"json_depth", `{"choices":[{"message":{"tool_calls":[{"function":{"arguments":"{}"}}]}}]}`, resource.Limits{MaxJSONDepth: 5}},
		{"tool_arguments", `{"choices":[{"message":{"tool_calls":[{"function":{"arguments":"private-secret"}}]}}]}`, resource.Limits{MaxToolArgumentsBytes: 4}},
		{"accumulated_text", `{"choices":[{"message":{"content":"abc"}},{"message":{"content":"def"}}]}`, resource.Limits{MaxAccumulatedBytes: 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pc := shared.DefaultProxyConfig()
			pc.ResourceLimits = tc.limits
			pc.RetryCount = 2
			var calls atomic.Int32
			var breakers *BreakerStore
			attempts := &attemptRecorder{}
			r, key, logs := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.AttemptWrite = attempts.write; breakers = d.Breakers }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, tc.body)
			}))
			requestControlFallback(t)
			w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hi"}]}`, key)
			rows := attempts.entries()
			if w.Code != 502 || calls.Load() != 1 || len(rows) != 1 {
				t.Fatalf("status=%d HTTP=%d rows=%+v body=%s", w.Code, calls.Load(), rows, w.Body.String())
			}
			if rows[0].Status != 200 || rows[0].Success || rows[0].ErrKind != shared.ErrKindResourceLimit {
				t.Fatalf("attempt=%+v", rows[0])
			}
			if row := logs.last(); row == nil || row.Success || row.ErrKind != shared.ErrKindResourceLimit {
				t.Fatalf("log=%+v", row)
			}
			assertNoLocalControlBreakerFailure(t, breakers, rows[0].ModelID)
			if strings.Contains(w.Body.String(), "private-secret") {
				t.Fatal("response payload leaked")
			}
		})
	}
}

func TestResourceCompatibilityRetryBodyBounded(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprintf("unknown_length_%v", unknown), func(t *testing.T) {
			pc := shared.DefaultProxyConfig()
			pc.RetryCount = 2
			pc.ResourceLimits.MaxResponseBytes = 300
			var calls atomic.Int32
			var breakers *BreakerStore
			attempts := &attemptRecorder{}
			r, key, logs := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.AttemptWrite = attempts.write; breakers = d.Breakers }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(400)
					io.WriteString(w, budgetUnsupportedLegacy)
					return
				}
				body := `{"choices":[{"message":{"role":"assistant","content":"` + strings.Repeat("private-secret", 40) + `"},"finish_reason":"stop"}]}`
				w.Header().Set("Content-Type", "application/json")
				if !unknown {
					w.Header().Set("Content-Length", strconv.Itoa(len(body)))
				}
				w.WriteHeader(200)
				if unknown {
					w.(http.Flusher).Flush()
				}
				io.WriteString(w, body)
			}))
			requestControlFallback(t)
			w := doReq(t, r, "/v1/responses", `{"model":"my-model","input":"hi","max_output_tokens":64}`, key)
			rows := attempts.entries()
			if w.Code != 502 || calls.Load() != 2 || len(rows) != 2 {
				t.Fatalf("status=%d HTTP=%d rows=%+v body=%s", w.Code, calls.Load(), rows, w.Body.String())
			}
			if rows[0].Status != 400 || rows[1].Status != 200 || rows[1].ErrKind != shared.ErrKindResourceLimit || rows[1].Success {
				t.Fatalf("attempts=%+v", rows)
			}
			if row := logs.last(); row == nil || row.Success || row.ErrKind != shared.ErrKindResourceLimit {
				t.Fatalf("log=%+v", row)
			}
			assertNoLocalControlBreakerFailure(t, breakers, rows[1].ModelID)
			if strings.Contains(w.Body.String(), "private-secret") {
				t.Fatal("compatibility response leaked")
			}
		})
	}
}

func TestResourceStreamTerminalDoesNotHideLimitFailure(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.ResourceLimits.MaxJSONDepth = 10
	pc.RetryCount = 2
	var calls atomic.Int32
	attempts := &attemptRecorder{}
	var breakers *BreakerStore
	r, key, logs := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.AttemptWrite = attempts.write; breakers = d.Breakers }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprintf(w, "data: {\"unknown\":%s0%s}\n\n", strings.Repeat("[", 20), strings.Repeat("]", 20))
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	requestControlFallback(t)
	w := doReq(t, r, "/v1/messages", `{"model":"my-model","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, key)
	rows := attempts.entries()
	if w.Code != 200 || calls.Load() != 1 || len(rows) != 1 || !strings.Contains(w.Body.String(), "event: error") || strings.Contains(w.Body.String(), "event: message_stop") {
		t.Fatalf("status=%d HTTP=%d rows=%+v body=%s", w.Code, calls.Load(), rows, w.Body.String())
	}
	if row := logs.last(); row == nil || row.Success || row.ErrKind != shared.ErrKindResourceLimit {
		t.Fatalf("log=%+v", row)
	}
	if rows[0].ErrKind != shared.ErrKindResourceLimit || rows[0].Success {
		t.Fatalf("attempt=%+v", rows[0])
	}
	assertNoLocalControlBreakerFailure(t, breakers, rows[0].ModelID)
}

func TestResourceStreamFailureBeforeDeliveryStopsWholeRequest(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.ResourceLimits.MaxAccumulatedBytes = 4
	pc.RetryCount = 2
	var calls atomic.Int32
	var breakers *BreakerStore
	attempts := &attemptRecorder{}
	r, key, logs := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.AttemptWrite = attempts.write; breakers = d.Breakers }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"private-secret\"}}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	requestControlFallback(t)
	w := doReq(t, r, "/v1/messages", `{"model":"my-model","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`, key)
	rows := attempts.entries()
	if w.Code != 502 || calls.Load() != 1 || len(rows) != 1 {
		t.Fatalf("status=%d HTTP=%d attempts=%+v body=%s", w.Code, calls.Load(), rows, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-secret") || strings.Contains(w.Body.String(), "event: message_start") || strings.Contains(w.Body.String(), "event: message_stop") {
		t.Fatalf("precommit data leaked: %s", w.Body.String())
	}
	if rows[0].Success || rows[0].ErrKind != shared.ErrKindResourceLimit {
		t.Fatalf("attempt=%+v", rows[0])
	}
	if row := logs.last(); row == nil || row.Success || row.ErrKind != shared.ErrKindResourceLimit {
		t.Fatalf("log=%+v", row)
	}
	assertNoLocalControlBreakerFailure(t, breakers, rows[0].ModelID)
}

func TestResourceRequestDepthBeforeMetadataDecode(t *testing.T) {
	var calls atomic.Int32
	attempts := &attemptRecorder{}
	r, key, _ := setupGatewayDeps(t, func(d *Deps) { d.AttemptWrite = attempts.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	// This exceeds encoding/json's own nesting limit. Resource enforcement must
	// happen first, rather than returning the generic metadata parse error.
	body := `{"model":"my-model","native_extension":` + strings.Repeat("[", 11000) + `0` + strings.Repeat("]", 11000) + `}`
	w := doReq(t, r, "/v1/chat/completions", body, key)
	if w.Code != 413 || calls.Load() != 0 || len(attempts.entries()) != 0 {
		t.Fatalf("status=%d HTTP=%d attempts=%+v", w.Code, calls.Load(), attempts.entries())
	}
}
