package protocol

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestResponsesForcedFunctionToolChoice(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name     string
			from, to Provider
			body     string
			want     any
		}{
			{"responses_to_chat", ProviderOpenAIResponses, ProviderOpenAI,
				`{"model":"m","input":"sum two numbers","tool_choice":{"type":"function","name":"sum_numbers"}}`,
				map[string]any{"type": "function", "function": map[string]any{"name": "sum_numbers"}}},
			{"chat_to_responses", ProviderOpenAI, ProviderOpenAIResponses,
				`{"model":"m","messages":[{"role":"user","content":"sum two numbers"}],"tool_choice":{"type":"function","function":{"name":"sum_numbers"}}}`,
				map[string]any{"type": "function", "name": "sum_numbers"}},
			{"anthropic_to_responses", ProviderAnthropic, ProviderOpenAIResponses,
				`{"model":"m","max_tokens":128,"messages":[{"role":"user","content":"sum two numbers"}],"tool_choice":{"type":"tool","name":"sum_numbers"}}`,
				map[string]any{"type": "function", "name": "sum_numbers"}},
			{"responses_to_anthropic", ProviderOpenAIResponses, ProviderAnthropic,
				`{"model":"m","input":"sum two numbers","tool_choice":{"type":"function","name":"sum_numbers"}}`,
				map[string]any{"type": "tool", "name": "sum_numbers"}},
		} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				var body map[string]any
				if err := json.Unmarshal([]byte(tc.body), &body); err != nil {
					t.Fatal(err)
				}
				body["stream"] = stream
				in, _ := json.Marshal(body)
				out, _, err := ComposeUpstreamRequest(tc.from, tc.to, in, UpstreamRequest{})
				if err != nil {
					t.Fatal(err)
				}
				var got map[string]any
				if err := json.Unmarshal(out, &got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got["tool_choice"], tc.want) {
					t.Fatalf("tool_choice=%v, want %v", got["tool_choice"], tc.want)
				}
				if got["stream"] == true != stream {
					t.Fatalf("stream=%v, want %t", got["stream"], stream)
				}
			})
		}
	}
}

func TestResponsesToolChoiceCompatibility(t *testing.T) {
	choices := []string{`"auto"`, `"none"`, `"required"`, `{"type":"web_search"}`, `{"type":"function","name":3}`, `{"type":"function","name":""}`, `{"type":"function"}`, `{"type":"function","function":{"name":3}}`, `{"type":"function","function":{"name":"sum_numbers","vendor":true}}`, `{"type":"function","name":"flat","function":{"name":"nested"}}`, `{"type":"future","options":{"x":true}}`}
	for _, from := range []Provider{ProviderOpenAIResponses, ProviderOpenAI} {
		to := ProviderOpenAIResponses
		if from == ProviderOpenAIResponses {
			to = ProviderOpenAI
		}
		for _, choice := range choices {
			t.Run(string(from)+"/"+choice, func(t *testing.T) {
				body := []byte(`{"model":"m","input":"hello","messages":[{"role":"user","content":"hello"}],"tool_choice":` + choice + `}`)
				out, err := ConvertRequest(from, to, body)
				if err != nil {
					t.Fatal(err)
				}
				var got map[string]any
				var want any
				if err := json.Unmarshal(out, &got); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(choice), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got["tool_choice"], want) {
					t.Fatalf("choice changed: %v, want %v", got["tool_choice"], want)
				}
			})
		}
	}
}

func TestResponsesToolChoiceMappingPreservesExtensionsWithoutMutation(t *testing.T) {
	flat := map[string]any{"type": "function", "name": "sum_numbers", "vendor": true}
	nested := map[string]any{"type": "function", "function": map[string]any{"name": "sum_numbers"}, "vendor": true}
	for _, tc := range []struct {
		name     string
		convert  func(any) any
		in, want map[string]any
	}{
		{"to_chat", responsesToolChoiceToOpenAI, flat, nested},
		{"to_responses", openAIToolChoiceToResponses, nested, flat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if got := tc.convert(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("choice=%v, want %v", got, tc.want)
			}
			after, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatalf("source choice mutated: %s -> %s", before, after)
			}
		})
	}
}

func TestResponsesToolChoiceSameProtocolPreserved(t *testing.T) {
	for _, p := range []Provider{ProviderOpenAIResponses, ProviderOpenAI} {
		body := []byte(`{"model":"m","input":"hello","tool_choice":{"type":"function","name":"sum_numbers","vendor":true}}`)
		converted, err := ConvertRequest(p, p, body)
		if err != nil {
			t.Fatal(err)
		}
		if string(converted) != string(body) {
			t.Fatalf("same protocol body changed: %s", converted)
		}
		out, _, err := ComposeUpstreamRequest(p, p, body, UpstreamRequest{Model: "target"})
		if err != nil {
			t.Fatal(err)
		}
		var got, original map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &original); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got["tool_choice"], original["tool_choice"]) {
			t.Fatalf("same protocol choice changed: %v", got["tool_choice"])
		}
	}
}
