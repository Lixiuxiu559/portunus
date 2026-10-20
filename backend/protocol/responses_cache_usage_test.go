package protocol

import (
	"encoding/json"
	"fmt"
	"testing"
)

// 经公开入口验证缓存读明细，并锁定含缓存写的总输入口径。
func TestResponsesCacheUsage(t *testing.T) {
	for _, tc := range []struct {
		name        string
		read, write int
		noUsage     bool
	}{
		{name: "read_and_write", read: 40, write: 30},
		{name: "read_only", read: 40},
		{name: "write_only", write: 30},
		{name: "zero_cache"},
		{name: "missing_usage", noUsage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usageField := ""
			if !tc.noUsage {
				usageField = fmt.Sprintf(`,"usage":{"prompt_tokens":100,"completion_tokens":5,"total_tokens":105,"prompt_tokens_details":{"cached_tokens":%d,"cache_creation_tokens":%d}}`, tc.read, tc.write)
			}
			assertUsage := func(t *testing.T, raw []byte) {
				t.Helper()
				var resp ResponsesResponse
				if err := json.Unmarshal(raw, &resp); err != nil {
					t.Fatal(err)
				}
				if tc.noUsage {
					if resp.Usage != nil {
						t.Fatalf("缺失 usage 不应生成用量: %s", raw)
					}
					return
				}
				if resp.Usage == nil {
					t.Fatalf("usage 丢失: %s", raw)
				}
				got := resp.Usage
				if got.InputTokens != 100 || got.OutputTokens != 5 || got.TotalTokens != 105 || got.InputTokensDetails.CachedTokens != tc.read {
					t.Fatalf("Responses usage = %+v，期望 input=100 output=5 total=105 cached=%d", got, tc.read)
				}
			}

			t.Run("non_stream", func(t *testing.T) {
				body := `{"id":"chatcmpl-cache","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]` + usageField + `}`
				out, err := ConvertResponse(ProviderOpenAI, ProviderOpenAIResponses, []byte(body))
				if err != nil {
					t.Fatal(err)
				}
				assertUsage(t, out)
			})

			t.Run("stream", func(t *testing.T) {
				conv, err := NewStreamConverter(ProviderOpenAI, ProviderOpenAIResponses)
				if err != nil {
					t.Fatal(err)
				}
				var events [][]byte
				for _, chunk := range []string{
					`{"id":"chatcmpl-cache","choices":[{"delta":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`,
					`{"id":"chatcmpl-cache","choices":[]` + usageField + `}`,
				} {
					out, err := conv.Convert([]byte(chunk))
					if err != nil {
						t.Fatal(err)
					}
					events = append(events, out...)
				}
				out, err := conv.Finish()
				if err != nil {
					t.Fatal(err)
				}
				events = append(events, out...)
				terminalCount := 0
				for _, raw := range events {
					var ev ResponsesStreamEvent
					if err := json.Unmarshal(raw, &ev); err != nil {
						t.Fatal(err)
					}
					if ev.Type == "response.completed" {
						terminalCount++
						if ev.Response == nil {
							t.Fatalf("完成事件缺失 response: %s", raw)
						}
						body, err := json.Marshal(ev.Response)
						if err != nil {
							t.Fatal(err)
						}
						assertUsage(t, body)
					}
				}
				if terminalCount != 1 {
					t.Fatalf("完成事件数量 = %d，期望 1", terminalCount)
				}
				got := conv.Usage()
				if tc.noUsage {
					if got != nil {
						t.Fatalf("缺失 usage 不应累积用量: %+v", got)
					}
				} else if got == nil || got.PromptTokens != 100-tc.read-tc.write || got.CacheReadTokens != tc.read || got.CacheWriteTokens != tc.write || got.CompletionTokens != 5 || got.TotalTokens != 105 {
					t.Fatalf("canonical usage 口径改变: %+v", got)
				}
			})
		})
	}
}
