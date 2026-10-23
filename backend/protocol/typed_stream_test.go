package protocol

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// 内部协议事件直接交给 encoder：此处故意使用不能经 JSON 无损传递的字符串，
// 防止以后把类型化链重新接成 Marshal/Unmarshal 的隐式 wire 中转。
func TestTypedStreamChainDoesNotSerializeIntermediate(t *testing.T) {
	event := StreamEvent{ID: "response-id", Model: "model", Choices: []StreamChoice{{Index: 3, Delta: StreamEventDelta{
		Role: "assistant", Content: string([]byte{0xff, 'x'}), ReasoningContent: "thinking",
		ToolCalls:         []ChunkToolCall{{Index: 2, ID: "call", Function: ChunkFunctionCall{Name: "f", Arguments: `{"x":`}}},
		GroundingMetadata: &GroundingMetadata{},
	}, FinishReason: "tool_calls"}}, Usage: &Usage{PromptTokens: 2}}
	encoder := &recordingStreamEncoder{}
	converter := &typedStreamConverter{decoder: &fixedStreamDecoder{events: []StreamEvent{event}}, encoder: encoder}
	if _, err := converter.convertNative(nil, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(encoder.events, []StreamEvent{event}) {
		t.Fatalf("类型事件被改写: %#v", encoder.events)
	}
}

type fixedStreamDecoder struct{ events []StreamEvent }

func (s *fixedStreamDecoder) Decode([]byte) ([]StreamEvent, error) { return s.events, nil }
func (*fixedStreamDecoder) End() ([]StreamEvent, error)            { return nil, nil }
func (*fixedStreamDecoder) Usage() *Usage                          { return nil }

type recordingStreamEncoder struct{ events []StreamEvent }

func (s *recordingStreamEncoder) Encode(e StreamEvent) ([][]byte, error) {
	s.events = append(s.events, e)
	return nil, nil
}
func (*recordingStreamEncoder) Finish() ([][]byte, error) { return nil, nil }

func TestTypedOpenAIDecoderKeepsFrameCombinationAndIndexes(t *testing.T) {
	decoder := &openAIStreamDecoder{}
	events, err := decoder.Decode([]byte(`{"id":"r","model":"m","choices":[{"index":3,"delta":{"role":"assistant","content":"text","reasoning_content":"thinking","tool_calls":[{"index":2,"id":"c","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"},{"index":7,"delta":{"content":"second"}}],"usage":{"prompt_tokens":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || len(events[0].Choices) != 2 {
		t.Fatalf("帧组合丢失: %#v", events)
	}
	first := events[0].Choices[0]
	if first.Index != 3 || first.Delta.Role != "assistant" || first.Delta.Content != "text" || first.Delta.ReasoningContent != "thinking" || len(first.Delta.ToolCalls) != 1 || first.FinishReason != "tool_calls" || events[0].Choices[1].Index != 7 {
		t.Fatalf("增量语义丢失: %#v", events)
	}
	if decoder.Usage() == nil || decoder.Usage().PromptTokens != 2 {
		t.Fatal("未累计 usage")
	}
}

func TestTypedProductionFactoriesAndAttemptIsolation(t *testing.T) {
	for source, impl := range providerImpls {
		if impl.newStreamDecoder == nil || impl.newStreamEncoder == nil {
			t.Fatalf("%s 缺少类型化工厂", source)
		}
		for target := range providerImpls {
			converter, err := NewStreamConverter(source, target)
			if err != nil {
				t.Fatal(err)
			}
			if source == target {
				if _, ok := converter.(*protocolAwarePassthrough); !ok {
					t.Fatalf("%s 同协议未走原生直通", source)
				}
				continue
			}
			typed, ok := converter.(*typedStreamConverter)
			if !ok {
				t.Fatalf("%s→%s 未使用类型化链", source, target)
			}
			another, _ := NewStreamConverter(source, target)
			if typed.decoder == another.(*typedStreamConverter).decoder {
				t.Fatal("尝试之间共享 decoder")
			}
			if source != ProviderOpenAI && target != ProviderOpenAI && typed.encoder == another.(*typedStreamConverter).encoder {
				t.Fatal("尝试之间共享 encoder")
			}
		}
	}
	// 两个真实并行推进的状态机不能串 ID、工具或用量。
	first, _ := NewStreamConverter(ProviderAnthropic, ProviderGemini)
	second, _ := NewStreamConverter(ProviderAnthropic, ProviderGemini)
	for _, item := range []struct {
		conv   StreamConverter
		id     string
		tokens int
	}{{first, "a", 3}, {second, "b", 7}} {
		payload, _ := json.Marshal(MessagesStreamEvent{Type: "message_start", Message: &MessagesResponse{ID: item.id, Model: "m", Usage: &AnthropicUsage{InputTokens: item.tokens}}})
		if _, err := item.conv.Convert(payload); err != nil {
			t.Fatal(err)
		}
	}
	if first.Usage().PromptTokens != 3 || second.Usage().PromptTokens != 7 {
		t.Fatal("两个尝试用量串扰")
	}
}

func TestTypedStageSameProtocolPayloadUntouched(t *testing.T) {
	for provider := range providerImpls {
		t.Run(string(provider), func(t *testing.T) {
			called := 0
			converter, err := NewStreamConverter(provider, provider, WithInputEstimate(func() int { called++; return 100 }))
			if err != nil {
				t.Fatal(err)
			}
			for _, payload := range [][]byte{[]byte(" \t{\"unknown\":900719925474099312345} \n"), []byte("not-json"), []byte(`{"type":"future_event","choices":[{"index":6,"delta":{"future":"value"}}]}`)} {
				out, err := converter.Convert(payload)
				if err != nil || len(out) != 1 || !bytes.Equal(out[0], payload) {
					t.Fatalf("同协议原始载荷改变: %q %v", out, err)
				}
			}
			tail, err := converter.Finish()
			if err != nil || len(tail) != 0 || called != 0 {
				t.Fatalf("直通额外收尾或调用估算: %q %v %d", tail, err, called)
			}
		})
	}
}

func TestTypedAnthropicUsagePreludeAndEstimate(t *testing.T) {
	for _, test := range []struct {
		name       string
		prelude    string
		wantCalls  int
		wantTokens int
	}{
		{"zero-real-usage", `{"choices":[],"usage":{"prompt_tokens":0}}`, 0, 0},
		{"real-usage", `{"choices":[],"usage":{"prompt_tokens":12}}`, 0, 12},
		{"no-usage", `{"choices":[]}`, 1, 99},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := 0
			converter, _ := NewStreamConverter(ProviderOpenAI, ProviderAnthropic, WithInputEstimate(func() int { called++; return 99 }))
			before, err := converter.Convert([]byte(test.prelude))
			if err != nil || len(before) != 0 || called != 0 {
				t.Fatalf("usage-only 提前启动: %q %v %d", before, err, called)
			}
			out, err := converter.Convert([]byte(`{"id":"r","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"a"}}]}`))
			if err != nil || len(out) == 0 {
				t.Fatalf("未启动: %q %v", out, err)
			}
			var start MessagesStreamEvent
			if err := json.Unmarshal(out[0], &start); err != nil {
				t.Fatal(err)
			}
			if start.Message == nil || start.Message.Usage == nil || start.Message.Usage.InputTokens != test.wantTokens {
				t.Fatalf("message_start 未采用正确用量: %s", out[0])
			}
			if _, err := converter.Convert([]byte(`{"choices":[{"index":0,"delta":{"content":"b"}}]}`)); err != nil {
				t.Fatal(err)
			}
			if called != test.wantCalls {
				t.Fatalf("估算调用次数=%d want=%d", called, test.wantCalls)
			}
		})
	}
}

func TestTypedLegacyIntermediateUsageProjection(t *testing.T) {
	for _, target := range []Provider{ProviderAnthropic, ProviderOpenAIResponses, ProviderOpenAI} {
		t.Run(string(target), func(t *testing.T) {
			converter, _ := NewStreamConverter(ProviderGemini, target)
			if _, err := converter.Convert([]byte(`{"candidates":[{"content":{"parts":[{"text":"hello"}]}}]}`)); err != nil {
				t.Fatal(err)
			}
			if _, err := converter.Convert([]byte(`{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":3,"totalTokenCount":14,"cachedContentTokenCount":2}}`)); err != nil {
				t.Fatal(err)
			}
			out, err := converter.Finish()
			if err != nil {
				t.Fatal(err)
			}
			sourceUsage := converter.Usage()
			if sourceUsage == nil || sourceUsage.PromptTokens != 9 || sourceUsage.CacheReadTokens != 2 || sourceUsage.TotalTokens != 14 {
				t.Fatalf("源用量被客户端投影修改: %#v", sourceUsage)
			}
			if target == ProviderOpenAI {
				return
			} // Chat 目标仍直接输出源用量。
			typed := converter.(*typedStreamConverter)
			encoder := typed.encoder.(*fromOpenAISkeleton)
			if encoder.usage == nil || encoder.usage.PromptTokens != 9 || encoder.usage.CacheReadTokens != 0 || encoder.usage.CacheWriteTokens != 0 || encoder.usage.TotalTokens != 14 {
				t.Fatalf("目标未保留旧中间wire用量: %#v", encoder.usage)
			}
			if target == ProviderAnthropic {
				var delta MessagesStreamEvent
				for _, payload := range out {
					var event MessagesStreamEvent
					if json.Unmarshal(payload, &event) == nil && event.Type == "message_delta" {
						delta = event
					}
				}
				if delta.Usage == nil || delta.Usage.InputTokens != 9 || delta.Usage.CacheReadInputTokens != 0 {
					t.Fatalf("Anthropic可见用量变化: %#v", delta.Usage)
				}
			}
			if target == ProviderOpenAIResponses {
				var final ResponsesStreamEvent
				for _, payload := range out {
					var event ResponsesStreamEvent
					if json.Unmarshal(payload, &event) == nil && event.Type == "response.completed" {
						final = event
					}
				}
				if final.Response == nil || final.Response.Usage == nil || final.Response.Usage.InputTokens != 9 || final.Response.Usage.InputTokensDetails.CachedTokens != 0 {
					t.Fatalf("Responses可见用量变化: %#v", final.Response)
				}
			}
		})
	}
	// 包含缓存写的投影同样不能污染源值。
	source := &Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 12, CacheReadTokens: 2, CacheWriteTokens: 2}
	projected := legacyIntermediateUsage(source)
	if projected.CacheReadTokens != 0 || projected.CacheWriteTokens != 0 || source.CacheReadTokens != 2 || source.CacheWriteTokens != 2 {
		t.Fatal("缓存投影错误或修改源值")
	}
}
