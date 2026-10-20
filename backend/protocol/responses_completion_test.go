package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// 使用公开转换入口验证结束原因，避免只验证内部映射函数。
func TestResponsesCompletionFromChat(t *testing.T) {
	for _, tc := range []struct{ finish, status, reason string }{
		{"stop", "completed", ""},
		{"tool_calls", "completed", ""},
		{"length", "incomplete", "max_output_tokens"},
		{"content_filter", "incomplete", "content_filter"},
	} {
		for _, content := range []string{"text", "reasoning", "tool"} {
			t.Run(tc.finish+"/"+content, func(t *testing.T) {
				message := ChatMessage{Role: "assistant"}
				delta := ChunkDelta{}
				switch content {
				case "text":
					message.Content, delta.Content = "partial", "partial"
				case "reasoning":
					message.ReasoningContent, delta.ReasoningContent = "thinking", "thinking"
				case "tool":
					message.ToolCalls = []ToolCall{{ID: "call_1", Type: "function", Function: FunctionCall{Name: "run", Arguments: `{"x":`}}}
					delta.ToolCalls = []ChunkToolCall{{Index: 0, ID: "call_1", Type: "function", Function: ChunkFunctionCall{Name: "run", Arguments: `{"x":`}}}
				}
				body, _ := json.Marshal(ChatCompletionResponse{ID: "resp_1", Model: "model", Choices: []ChatChoice{{Message: message, FinishReason: tc.finish}}, Usage: &Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}})
				out, err := ConvertResponse(ProviderOpenAI, ProviderOpenAIResponses, body)
				if err != nil {
					t.Fatal(err)
				}
				assertCompletion := func(raw []byte) map[string]any {
					t.Helper()
					var resp map[string]any
					if err := json.Unmarshal(raw, &resp); err != nil {
						t.Fatal(err)
					}
					if resp["status"] != tc.status {
						t.Fatalf("status = %v, want %s: %s", resp["status"], tc.status, raw)
					}
					if tc.reason != "" {
						details, ok := resp["incomplete_details"].(map[string]any)
						if !ok || details["reason"] != tc.reason {
							t.Fatalf("incomplete_details = %v, want %s", resp["incomplete_details"], tc.reason)
						}
					} else if resp["incomplete_details"] != nil {
						t.Fatalf("正常完成不应携带 incomplete_details: %s", raw)
					}
					usage, _ := resp["usage"].(map[string]any)
					if usage["input_tokens"] != float64(10) || usage["output_tokens"] != float64(5) {
						t.Fatalf("usage 丢失: %s", raw)
					}
					return resp
				}
				resp := assertCompletion(out)
				if content != "reasoning" {
					item := resp["output"].([]any)[0].(map[string]any)
					if item["status"] != tc.status {
						t.Fatalf("output status = %v, want %s", item["status"], tc.status)
					}
				}

				conv, _ := NewStreamConverter(ProviderOpenAI, ProviderOpenAIResponses)
				chunk, _ := json.Marshal(ChatCompletionChunk{ID: "resp_1", Model: "model", Choices: []ChunkChoice{{Delta: delta}}})
				if _, err := conv.Convert(chunk); err != nil {
					t.Fatal(err)
				}
				chunk, _ = json.Marshal(ChatCompletionChunk{ID: "resp_1", Choices: []ChunkChoice{{FinishReason: tc.finish}}})
				if _, err := conv.Convert(chunk); err != nil {
					t.Fatal(err)
				}
				// 用量独立尾帧不能覆盖之前的结束原因。
				if _, err := conv.Convert([]byte(`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)); err != nil {
					t.Fatal(err)
				}
				finals, err := conv.Finish()
				if err != nil {
					t.Fatal(err)
				}
				var terminalCount, toolCount int
				for _, raw := range finals {
					var ev map[string]any
					if err := json.Unmarshal(raw, &ev); err != nil {
						t.Fatal(err)
					}
					switch ev["type"] {
					case "response.completed", "response.incomplete":
						terminalCount++
						if ev["type"] != "response."+tc.status {
							t.Fatalf("结束事件 = %v, want response.%s", ev["type"], tc.status)
						}
						body, _ := json.Marshal(ev["response"])
						assertCompletion(body)
					case "response.output_item.done":
						if ev["item"].(map[string]any)["type"] != "function_call" {
							break // 正文和思考现也有独立的输出项生命周期。
						}
						toolCount++
						if item := ev["item"].(map[string]any); item["status"] != tc.status || item["arguments"] != `{"x":` {
							t.Fatalf("工具收尾状态/参数不符: %s", raw)
						}
					}
				}
				if terminalCount != 1 || (content == "tool" && toolCount != 1) {
					t.Fatalf("terminal=%d tool=%d", terminalCount, toolCount)
				}
			})
		}
	}
}

func TestResponsesCompletionToChat(t *testing.T) {
	for _, tc := range []struct{ status, reason, finish string }{
		{"completed", "", "tool_calls"},
		{"incomplete", "max_output_tokens", "length"},
		{"incomplete", "content_filter", "content_filter"},
	} {
		t.Run(tc.status+tc.reason, func(t *testing.T) {
			// 即使已有 completed 工具项，响应级截断原因也应优先。
			body := fmt.Sprintf(`{"id":"resp_1","model":"model","status":%q,"incomplete_details":{"reason":%q},"output":[{"type":"function_call","call_id":"c","name":"run","arguments":"{}","status":"completed"},{"type":"message","status":"completed","content":[{"type":"output_text","text":"partial"}]}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`, tc.status, tc.reason)
			out, err := ConvertResponse(ProviderOpenAIResponses, ProviderOpenAI, []byte(body))
			if err != nil {
				t.Fatal(err)
			}
			var resp ChatCompletionResponse
			if err := json.Unmarshal(out, &resp); err != nil {
				t.Fatal(err)
			}
			if resp.Choices[0].FinishReason != tc.finish || resp.Usage == nil || resp.Usage.CompletionTokens != 5 {
				t.Fatalf("非流式结束原因/用量不符: %s", out)
			}
			conv, _ := NewStreamConverter(ProviderOpenAIResponses, ProviderOpenAI)
			for _, event := range []string{
				`{"type":"response.output_item.added","item":{"type":"function_call","call_id":"c","name":"run"}}`,
				fmt.Sprintf(`{"type":%q,"response":%s}`, "response."+tc.status, body),
			} {
				if _, err := conv.Convert([]byte(event)); err != nil {
					t.Fatal(err)
				}
			}
			finals, err := conv.Finish()
			if err != nil || len(finals) != 1 {
				t.Fatalf("Finish = %s, %v", finals, err)
			}
			chunk := parseChunk(t, finals[0])
			if chunk.Choices[0].FinishReason != tc.finish || chunk.Usage == nil || chunk.Usage.CompletionTokens != 5 {
				t.Fatalf("流式结束原因/用量不符: %s", finals[0])
			}
		})
	}
}

func TestResponsesIncompletePassthroughUsage(t *testing.T) {
	conv, _ := NewStreamConverter(ProviderOpenAIResponses, ProviderOpenAIResponses)
	payload := []byte(`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`)
	outs, err := conv.Convert(payload)
	if err != nil || len(outs) != 1 || string(outs[0]) != string(payload) {
		t.Fatalf("同协议内容发生改变: %s, %v", outs, err)
	}
	if usage := conv.Usage(); usage == nil || usage.CompletionTokens != 5 {
		t.Fatalf("直通截断事件用量丢失: %+v", usage)
	}
}

func TestResponsesBudgetAcrossAnthropic(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from, to Provider
		events   []string
		want     string
	}{
		{"responses_to_anthropic", ProviderOpenAIResponses, ProviderAnthropic, []string{
			`{"type":"response.reasoning_summary_text.delta","delta":"thinking"}`,
			`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`,
		}, `"stop_reason":"max_tokens"`},
		{"anthropic_to_responses", ProviderAnthropic, ProviderOpenAIResponses, []string{
			`{"type":"message_start","message":{"id":"m1","model":"model","role":"assistant","usage":{"input_tokens":10,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":5}}`,
			`{"type":"message_stop"}`,
		}, `"reason":"max_output_tokens"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv, err := NewStreamConverter(tc.from, tc.to)
			if err != nil {
				t.Fatal(err)
			}
			var all string
			for _, payload := range tc.events {
				outs, err := conv.Convert([]byte(payload))
				if err != nil {
					t.Fatal(err)
				}
				for _, out := range outs {
					all += string(out)
				}
			}
			outs, err := conv.Finish()
			if err != nil {
				t.Fatal(err)
			}
			for _, out := range outs {
				all += string(out)
			}
			if !strings.Contains(all, tc.want) {
				t.Fatalf("结束原因丢失: %s", all)
			}
			if usage := conv.Usage(); usage == nil || usage.CompletionTokens != 5 {
				t.Fatalf("用量丢失: %+v", usage)
			}
		})
	}
}

func TestResponsesUnknownIncompleteReason(t *testing.T) {
	body := []byte(`{"status":"incomplete","incomplete_details":{"reason":"future_reason"},"output":[]}`)
	if _, err := ConvertResponse(ProviderOpenAIResponses, ProviderOpenAI, body); err == nil {
		t.Fatal("无法表达的截断原因不能静默变为正常完成")
	}
	conv, _ := NewStreamConverter(ProviderOpenAIResponses, ProviderOpenAI)
	if _, err := conv.Convert(append(append([]byte(`{"type":"response.incomplete","response":`), body...), '}')); err == nil {
		t.Fatal("流式未知截断原因应报告转换错误")
	}
}

func TestResponsesMixedToolTruncation(t *testing.T) {
	for _, finish := range []string{"length", "content_filter"} {
		for _, separate := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/separate=%t", finish, separate), func(t *testing.T) {
				conv, _ := NewStreamConverter(ProviderOpenAI, ProviderOpenAIResponses)
				if _, err := conv.Convert([]byte(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"run","arguments":"{\"x\":"}}]}}]}`)); err != nil {
					t.Fatal(err)
				}
				text := `{"choices":[{"delta":{"content":"partial"}}]}`
				if !separate {
					text = fmt.Sprintf(`{"choices":[{"delta":{"content":"partial"},"finish_reason":%q}]}`, finish)
				}
				outs, err := conv.Convert([]byte(text))
				if err != nil {
					t.Fatal(err)
				}
				for _, raw := range outs {
					if strings.Contains(string(raw), `"response.output_item.done"`) {
						t.Fatalf("结束原因确认前不能定稿工具: %s", raw)
					}
				}
				if separate {
					if _, err := conv.Convert([]byte(fmt.Sprintf(`{"choices":[{"delta":{},"finish_reason":%q}]}`, finish))); err != nil {
						t.Fatal(err)
					}
				}
				outs, err = conv.Finish()
				if err != nil {
					t.Fatal(err)
				}
				var sawTool bool
				for _, raw := range outs {
					var ev ResponsesStreamEvent
					if err := json.Unmarshal(raw, &ev); err != nil {
						t.Fatal(err)
					}
					if ev.Type == "response.output_item.done" {
						sawTool = true
						if ev.Item == nil || ev.Item.Status != "incomplete" {
							t.Fatalf("混排截断工具被误标完成: %s", raw)
						}
					}
				}
				if !sawTool {
					t.Fatal("缺少工具关闭事件")
				}
			})
		}
	}
}

func TestResponsesNonCompletedStatusNotStop(t *testing.T) {
	for _, status := range []string{"failed", "cancelled", "in_progress", "queued"} {
		body := fmt.Sprintf(`{"status":%q,"output":[]}`, status)
		if _, err := ConvertResponse(ProviderOpenAIResponses, ProviderOpenAI, []byte(body)); err == nil {
			t.Fatalf("%s 不能映射为正常 stop", status)
		}
	}
}
