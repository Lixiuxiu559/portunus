package protocol

import (
	"encoding/json"
	"testing"
)

func TestCrossProtocolChatDefaultsMaxTokens(t *testing.T) {
	cases := []struct {
		from Provider
		body string
	}{
		{ProviderOpenAIResponses, `{"model":"any-name","input":"hi","max_output_tokens":8192}`},
		{ProviderAnthropic, `{"model":"any-name","messages":[{"role":"user","content":"hi"}],"max_tokens":8192}`},
		{ProviderGemini, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":8192}}`},
	}
	for _, tc := range cases {
		t.Run(string(tc.from), func(t *testing.T) {
			for _, compose := range []bool{false, true} {
				var body []byte
				var err error
				if compose {
					body, _, err = ComposeUpstreamRequest(tc.from, ProviderOpenAI, []byte(tc.body), UpstreamRequest{Model: "arbitrary-provider-alias"})
				} else {
					body, err = ConvertRequest(tc.from, ProviderOpenAI, []byte(tc.body))
				}
				if err != nil {
					t.Fatal(err)
				}
				var got map[string]json.RawMessage
				if err = json.Unmarshal(body, &got); err != nil {
					t.Fatal(err)
				}
				if string(got["max_tokens"]) != "8192" || got["max_completion_tokens"] != nil {
					t.Fatalf("compose=%v: expected only max_tokens=8192; got %s", compose, body)
				}
			}
		})
	}
}

func TestChatTokenBudgetFallback(t *testing.T) {
	body := []byte(`{"model":"unknown-alias","messages":[{"role":"user","content":"keep max_tokens literal"}],"max_tokens":8192,"reasoning_effort":"max","stream":true,"extra":{"large":9007199254740993}}`)
	cases := []struct {
		name      string
		status    int
		errorBody string
		want      bool
	}{
		{"OpenAI明确错误", 400, `{"error":{"code":"unsupported_parameter","param":"max_tokens","message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead."}}`, true},
		{"仅结构化code", 400, `{"error":{"code":"unsupported_parameter","param":"max_tokens"}}`, true},
		{"422结构化错误", 422, `{"error":{"code":"unknown_parameter","param":"max_tokens"}}`, true},
		{"无param但明确字段错误", 400, `{"error":{"message":"Unsupported parameter: 'max_tokens'. Use 'max_completion_tokens' instead."}}`, true},
		{"后置明确错误", 400, `{"error":{"message":"'max_tokens' is not supported with this model"}}`, true},
		{"上限数值错误", 400, `{"error":{"param":"max_tokens","code":"invalid_value","message":"max_tokens must be at most 4096"}}`, false},
		{"结构化数值错误不能被措辞覆盖", 400, `{"error":{"param":"max_tokens","code":"invalid_value","message":"max_tokens is not supported for value 8192; maximum is 4096"}}`, false},
		{"上下文超限", 400, `{"error":{"code":"context_length_exceeded","param":"max_tokens","message":"max_tokens exceeds remaining context"}}`, false},
		{"不支持数值不是字段", 400, `{"error":{"message":"Unsupported value for max_tokens: 8192"}}`, false},
		{"另一参数不支持", 400, `{"error":{"code":"unsupported_parameter","param":"temperature","message":"Unsupported parameter: temperature; max_tokens may be used"}}`, false},
		{"冲突结构化字段优先", 400, `{"error":{"param":"messages","message":"Unsupported parameter: max_tokens"}}`, false},
		{"普通400提及两个字段", 400, `{"error":{"message":"invalid request with max_tokens and max_completion_tokens"}}`, false},
		{"成功流内错误不切换", 200, `{"error":{"param":"max_tokens","code":"unsupported_parameter"}}`, false},
		{"鉴权错误", 401, `{"error":{"param":"max_tokens","code":"unsupported_parameter"}}`, false},
		{"限流不切换", 429, `{"error":{"param":"max_tokens","code":"unsupported_parameter"}}`, false},
		{"服务故障不切换", 500, `{"error":{"param":"max_tokens","code":"unsupported_parameter"}}`, false},
		{"非JSON", 400, `unsupported parameter max_tokens`, false},
		{"缺错误对象", 400, `{"message":"Unsupported parameter: max_tokens"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ChatTokenBudgetFallback(body, tc.status, []byte(tc.errorBody))
			if ok != tc.want {
				t.Fatalf("ok=%v want=%v body=%s", ok, tc.want, got)
			}
			if !ok {
				return
			}
			var before, after map[string]json.RawMessage
			json.Unmarshal(body, &before)
			if err := json.Unmarshal(got, &after); err != nil {
				t.Fatal(err)
			}
			if string(after["max_completion_tokens"]) != "8192" || after["max_tokens"] != nil {
				t.Fatalf("wrong budget fields: %s", got)
			}
			delete(before, "max_tokens")
			delete(after, "max_completion_tokens")
			a, _ := json.Marshal(before)
			b, _ := json.Marshal(after)
			if string(a) != string(b) {
				t.Fatalf("other fields changed: %s vs %s", a, b)
			}
			var original map[string]json.RawMessage
			json.Unmarshal(body, &original)
			if original["max_tokens"] == nil {
				t.Fatal("original input mutated")
			}
		})
	}
}

func TestChatTokenBudgetFallbackRequiresSinglePositiveBudget(t *testing.T) {
	errBody := []byte(`{"error":{"code":"unsupported_parameter","param":"max_tokens"}}`)
	for _, body := range []string{`{}`, `{"max_tokens":null}`, `{"max_tokens":0}`, `{"max_tokens":-1}`, `{"max_tokens":"8192"}`, `{"max_tokens":8192,"max_completion_tokens":8192}`, `{"max_tokens":8192,"max_completion_tokens":null}`} {
		if got, ok := ChatTokenBudgetFallback([]byte(body), 400, errBody); ok {
			t.Fatalf("should not replace budget: %s -> %s", body, got)
		}
	}
}

func TestNativeChatTokenBudgetPassthrough(t *testing.T) {
	for _, fields := range []string{`"max_tokens":8192`, `"max_completion_tokens":8192`, `"max_tokens":4096,"max_completion_tokens":8192`} {
		body := reasoningBody(ProviderOpenAI, fields)
		got, err := ConvertRequest(ProviderOpenAI, ProviderOpenAI, body)
		if err != nil || string(got) != string(body) {
			t.Fatalf("passthrough changed %s: %s %v", body, got, err)
		}
		got, _, err = ComposeUpstreamRequest(ProviderOpenAI, ProviderOpenAI, body, UpstreamRequest{Model: "arbitrary-model"})
		if err != nil {
			t.Fatal(err)
		}
		var before, after map[string]json.RawMessage
		json.Unmarshal(body, &before)
		json.Unmarshal(got, &after)
		for _, key := range []string{"max_tokens", "max_completion_tokens"} {
			if string(before[key]) != string(after[key]) {
				t.Fatalf("changed %s: %s", key, got)
			}
		}
	}
}
