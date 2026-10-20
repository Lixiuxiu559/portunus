package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
)

// 通过 /v1/responses 验证上游结束原因经过完整转发、SSE 收尾及日志落库。
func TestResponsesCompletionGateway(t *testing.T) {
	for _, tc := range []struct{ finish, status, reason string }{
		{"stop", "completed", ""},
		{"length", "incomplete", "max_output_tokens"},
		{"content_filter", "incomplete", "content_filter"},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.finish, stream), func(t *testing.T) {
				calls := 0
				router, key, logs := setupGateway(t, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintln(w, `data: {"id":"r1","model":"upstream-model","choices":[{"index":0,"delta":{"content":"partial"}}]}`)
						fmt.Fprintln(w)
						fmt.Fprintf(w, "data: {\"id\":\"r1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":%q}]}\n\n", tc.finish)
						fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\ndata: [DONE]\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(w, `{"id":"r1","model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"partial"},"finish_reason":%q}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`, tc.finish)
					}
				}))
				w := doReq(t, router, "/v1/responses", fmt.Sprintf(`{"model":"my-model","input":"hi","stream":%t,"max_output_tokens":5}`, stream), key)
				if w.Code != http.StatusOK || calls != 1 {
					t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
				}
				var response protocol.ResponsesResponse
				if stream {
					terminalCount := 0
					for _, line := range strings.Split(w.Body.String(), "\n") {
						if !strings.HasPrefix(line, "data: {") {
							continue
						}
						var ev protocol.ResponsesStreamEvent
						if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
							t.Fatal(err)
						}
						if ev.Type == "response.completed" || ev.Type == "response.incomplete" {
							terminalCount++
							if ev.Type != "response."+tc.status || ev.Response == nil {
								t.Fatalf("错误结束事件: %s", line)
							}
							response = *ev.Response
						}
					}
					if terminalCount != 1 || !strings.HasSuffix(w.Body.String(), "data: [DONE]\n\n") {
						t.Fatalf("SSE 收尾不符: %s", w.Body.String())
					}
				} else {
					body, _ := io.ReadAll(w.Body)
					if err := json.Unmarshal(body, &response); err != nil {
						t.Fatal(err)
					}
					if len(response.Output) != 1 || response.Output[0].Content[0].Text != "partial" {
						t.Fatalf("部分输出丢失: %s", body)
					}
				}
				if response.Status != tc.status || response.Usage == nil || response.Usage.OutputTokens != 5 {
					t.Fatalf("状态/用量不符: %+v", response)
				}
				if tc.reason != "" && (response.IncompleteDetails == nil || response.IncompleteDetails.Reason != tc.reason) {
					t.Fatalf("截断原因丢失: %+v", response)
				}
				if log := logs.last(); log == nil || log.OutputToken != 5 || log.InputToken != 10 {
					t.Fatalf("日志用量不符: %+v", log)
				}
			})
		}
	}
}
