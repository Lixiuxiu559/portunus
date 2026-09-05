package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// OpenAI→X 方向补齐回归：gemini / responses 的 from 转换器旧实现只透文本，
// tool_calls 与 reasoning_content 被静默丢弃（gemini 客户端 + openai 上游的
// 工具调用收一场空）；共享块骨架（from_openai.go）落地后两者必须完整透出。

func feedOpenAI(t *testing.T, conv StreamConverter, chunks ...string) []string {
	t.Helper()
	var raw []string
	for _, c := range chunks {
		outs, err := conv.Convert([]byte(c))
		if err != nil {
			t.Fatalf("Convert 失败: %v", err)
		}
		for _, o := range outs {
			raw = append(raw, string(o))
		}
	}
	finals, err := conv.Finish()
	if err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	for _, o := range finals {
		raw = append(raw, string(o))
	}
	return raw
}

const (
	fromOpenAIRole  = `{"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"}}]}`
	fromOpenAIReas  = `{"id":"c1","choices":[{"index":0,"delta":{"reasoning_content":"思考中"}}]}`
	fromOpenAIText  = `{"id":"c1","choices":[{"index":0,"delta":{"content":"你好"}}]}`
	fromOpenAITool  = `{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`
	fromOpenAIArgs  = `{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"北京\"}"}}]},"finish_reason":"tool_calls"}]}`
	fromOpenAIUsage = `{"id":"c1","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":5,"total_tokens":105,"prompt_tokens_details":{"cached_tokens":40}}}`
)

func TestFromOpenAIToGeminiCompleteness(t *testing.T) {
	conv, err := NewStreamConverter(ProviderOpenAI, ProviderGemini)
	if err != nil {
		t.Fatal(err)
	}
	raw := feedOpenAI(t, conv, fromOpenAIRole, fromOpenAIReas, fromOpenAIText, fromOpenAITool, fromOpenAIArgs, fromOpenAIUsage)
	all := strings.Join(raw, "\n")

	// 思考摘要：thought 标记 part
	if !strings.Contains(all, `"thought":true`) || !strings.Contains(all, `思考中`) {
		t.Fatalf("gemini 应透出 thought part: %s", all)
	}
	// 正文
	if !strings.Contains(all, `"text":"你好"`) {
		t.Fatalf("gemini 应透出文本 part: %s", all)
	}
	// 工具调用：完整 functionCall（骨架累积参数后整包发出）
	if !strings.Contains(all, `"functionCall"`) || !strings.Contains(all, `"name":"get_weather"`) {
		t.Fatalf("gemini 应透出 functionCall part: %s", all)
	}
	var args map[string]any
	for _, r := range raw {
		var m map[string]any
		if json.Unmarshal([]byte(r), &m) != nil {
			continue
		}
		cands, _ := m["candidates"].([]any)
		if len(cands) == 0 {
			continue
		}
		cand := cands[0].(map[string]any)
		content, _ := cand["content"].(map[string]any)
		if content == nil {
			continue
		}
		parts, _ := content["parts"].([]any)
		for _, p := range parts {
			if fc, ok := p.(map[string]any)["functionCall"].(map[string]any); ok {
				if a, ok := fc["args"].(map[string]any); ok {
					args = a
				}
			}
		}
	}
	if args == nil || args["city"] != "北京" {
		t.Fatalf("functionCall.args 应为累积完整参数 {city:北京}: %s", all)
	}
	// usage 反向还原：promptTokenCount 是含缓存总输入（60 非缓存 + 40 缓存 = 100）
	if !strings.Contains(all, `"promptTokenCount":100`) || !strings.Contains(all, `"candidatesTokenCount":5`) {
		t.Fatalf("gemini 收尾应带 usageMetadata: %s", all)
	}
}

// TestFromOpenAIToolArgsInFirstFrame 回归：vLLM / Ollama / GLM 等上游把整条
// tool_call 连同完整 arguments 放进开场帧——参数不得因「首现即 ToolStart 后
// 跳过」而丢首段（anthropic 收 input_json_delta，gemini 收到完整 functionCall）。
func TestFromOpenAIToolArgsInFirstFrame(t *testing.T) {
	oneShot := `{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"北京\"}"}}]},"finish_reason":"tool_calls"}]}`

	// anthropic：开场帧即应有 input_json_delta
	conv, err := NewStreamConverter(ProviderOpenAI, ProviderAnthropic)
	if err != nil {
		t.Fatal(err)
	}
	raw := feedOpenAI(t, conv, fromOpenAIRole, oneShot)
	all := strings.Join(raw, "\n")
	if !strings.Contains(all, `"input_json_delta"`) || !strings.Contains(all, `北京`) {
		t.Fatalf("anthropic 开场帧的 arguments 应以 input_json_delta 透出: %s", all)
	}
	if !strings.Contains(all, `"stop_reason":"tool_use"`) {
		t.Fatalf("工具调用收尾 stop_reason 应为 tool_use: %s", all)
	}

	// gemini：整包 functionCall
	conv, err = NewStreamConverter(ProviderOpenAI, ProviderGemini)
	if err != nil {
		t.Fatal(err)
	}
	raw = feedOpenAI(t, conv, fromOpenAIRole, oneShot)
	all = strings.Join(raw, "\n")
	if !strings.Contains(all, `"name":"get_weather"`) || !strings.Contains(all, `北京`) {
		t.Fatalf("gemini 应收到完整 functionCall: %s", all)
	}
}

func TestFromOpenAIToResponsesCompleteness(t *testing.T) {
	conv, err := NewStreamConverter(ProviderOpenAI, ProviderOpenAIResponses)
	if err != nil {
		t.Fatal(err)
	}
	raw := feedOpenAI(t, conv, fromOpenAIRole, fromOpenAIReas, fromOpenAIText, fromOpenAITool, fromOpenAIArgs, fromOpenAIUsage)
	all := strings.Join(raw, "\n")

	// 思考增量（与 responses→openai 方向解析的事件类型对称）
	if !strings.Contains(all, `"response.reasoning_summary_text.delta"`) || !strings.Contains(all, `思考中`) {
		t.Fatalf("responses 应透出 reasoning delta: %s", all)
	}
	// 工具调用：output_item.added（call_id/name/output_index）+ 参数增量 +
	// output_item.done（codex 等客户端只在该事件上分发工具执行，缺了工具
	// 调用永远不会被执行；done 的 item 携带完整参数与 completed 状态）
	if !strings.Contains(all, `"response.output_item.added"`) ||
		!strings.Contains(all, `"call_id":"call_1"`) || !strings.Contains(all, `"name":"get_weather"`) {
		t.Fatalf("responses 应透出 function_call item: %s", all)
	}
	if !strings.Contains(all, `"response.function_call_arguments.delta"`) || !strings.Contains(all, `北京`) {
		t.Fatalf("responses 应透出参数增量: %s", all)
	}
	if !strings.Contains(all, `"response.output_item.done"`) ||
		!strings.Contains(all, `"status":"completed"`) || !strings.Contains(all, `"arguments":"{\"city\":\"北京\"}"`) {
		t.Fatalf("responses 应透出 output_item.done（完整参数 item）: %s", all)
	}
	// usage 反向还原：input_tokens 是含缓存总输入（60 + 40 = 100）
	if !strings.Contains(all, `"input_tokens":100`) || !strings.Contains(all, `"output_tokens":5`) {
		t.Fatalf("responses.completed 应带 usage: %s", all)
	}
}
