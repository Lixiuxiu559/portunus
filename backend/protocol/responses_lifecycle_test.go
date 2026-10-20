package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func responsesLifecycleEvents(t *testing.T, payloads ...string) []map[string]any {
	t.Helper()
	conv, err := NewStreamConverter(ProviderOpenAI, ProviderOpenAIResponses)
	if err != nil {
		t.Fatal(err)
	}
	var raw [][]byte
	for _, p := range payloads {
		out, e := conv.Convert([]byte(p))
		if e != nil {
			t.Fatal(e)
		}
		raw = append(raw, out...)
	}
	out, err := conv.Finish()
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, out...)
	var events []map[string]any
	for _, p := range raw {
		var ev map[string]any
		if err := json.Unmarshal(p, &ev); err != nil {
			t.Fatal(err)
		}
		events = append(events, ev)
	}
	return events
}

func TestResponsesTextLifecycle(t *testing.T) {
	for _, finish := range []string{"stop", "length"} {
		t.Run(finish, func(t *testing.T) {
			tail := `{"choices":[{"delta":{},"finish_reason":"` + finish + `"}]}`
			events := responsesLifecycleEvents(t,
				`{"id":"resp_1","model":"model","choices":[{"delta":{"role":"assistant","content":"O"}}]}`,
				`{"choices":[{"delta":{"content":"K"}}]}`, tail)
			types := make([]string, len(events))
			for i, e := range events {
				types[i] = e["type"].(string)
				if e["sequence_number"] != float64(i) {
					t.Fatalf("sequence_number=%v want %d", e["sequence_number"], i)
				}
			}
			status := "completed"
			if finish == "length" {
				status = "incomplete"
			}
			want := []string{"response.created", "response.output_item.added", "response.content_part.added", "response.output_text.delta", "response.output_text.delta", "response.output_text.done", "response.content_part.done", "response.output_item.done", "response." + status}
			if !reflect.DeepEqual(types, want) {
				t.Fatalf("events=%v want %v", types, want)
			}
			item := events[1]["item"].(map[string]any)
			id, _ := item["id"].(string)
			if id == "" || item["status"] != "in_progress" || item["role"] != "assistant" {
				t.Fatalf("added item=%v", item)
			}
			if content, ok := item["content"].([]any); !ok || len(content) != 0 {
				t.Fatalf("初始content应为空数组: %v", item)
			}
			for _, index := range []int{2, 3, 4, 5, 6} {
				e := events[index]
				if e["item_id"] != id || e["output_index"] != float64(0) || e["content_index"] != float64(0) {
					t.Fatalf("事件索引/ID丢失: %v", e)
				}
			}
			if events[5]["text"] != "OK" {
				t.Fatalf("done text=%v", events[5])
			}
			final := events[len(events)-1]["response"].(map[string]any)
			output := final["output"].([]any)
			if len(output) != 1 || !reflect.DeepEqual(output[0], events[7]["item"]) {
				t.Fatalf("终态output与done不一致: %v", events)
			}
			done := output[0].(map[string]any)
			if !reflect.DeepEqual(done["content"].([]any)[0], events[6]["part"]) {
				t.Fatalf("终态内容与 content_part.done 不一致: %v", events)
			}
			if done["id"] != id || done["status"] != status || done["content"].([]any)[0].(map[string]any)["text"] != "OK" {
				t.Fatalf("done=%v", done)
			}
		})
	}
}

func TestResponsesMixedLifecycle(t *testing.T) {
	events := responsesLifecycleEvents(t,
		`{"id":"resp_mix","model":"model","choices":[{"delta":{"reasoning_content":"think"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":5,"id":"c1","function":{"name":"first","arguments":"{\"x\":"}},{"index":0,"id":"c2","function":{"name":"second","arguments":"{}"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":5,"function":{"arguments":"1}"}}]}}]}`,
		`{"choices":[{"delta":{"content":"done"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
	final := events[len(events)-1]["response"].(map[string]any)
	output, ok := final["output"].([]any)
	if !ok || len(output) != 4 {
		t.Fatalf("混排output=%v", final)
	}
	wantTypes := []string{"reasoning", "function_call", "function_call", "message"}
	ids := map[string]bool{}
	added := map[string]bool{}
	done := map[string]bool{}
	for i, raw := range output {
		it := raw.(map[string]any)
		id, _ := it["id"].(string)
		if id == "" || ids[id] || it["type"] != wantTypes[i] {
			t.Fatalf("输出项冲突: %v", output)
		}
		ids[id] = true
	}
	for _, ev := range events {
		typ := ev["type"].(string)
		if typ == "response.output_item.added" || typ == "response.output_item.done" {
			index, ok := ev["output_index"].(float64)
			if !ok || int(index) >= len(output) {
				t.Fatalf("索引缺失: %v", ev)
			}
			it := ev["item"].(map[string]any)
			id := it["id"].(string)
			if output[int(index)].(map[string]any)["id"] != id {
				t.Fatalf("ID与索引不一致: %v", ev)
			}
			if typ == "response.output_item.added" {
				if added[id] {
					t.Fatal("重复added")
				}
				added[id] = true
			} else {
				if !added[id] || done[id] {
					t.Fatalf("非法done: %v", ev)
				}
				done[id] = true
			}
		}
		if typ == "response.function_call_arguments.delta" {
			idx := int(ev["output_index"].(float64))
			if idx != 1 && idx != 2 {
				t.Fatalf("工具索引与reasoning冲突: %v", ev)
			}
			if ev["item_id"] != output[idx].(map[string]any)["id"] {
				t.Fatalf("工具item_id不符: %v", ev)
			}
		}
		if strings.HasPrefix(typ, "response.reasoning_summary_") {
			if ev["item_id"] != output[0].(map[string]any)["id"] || ev["summary_index"] != float64(0) {
				t.Fatalf("思考索引丢失: %v", ev)
			}
		}
	}
	if len(added) != 4 || len(done) != 4 {
		t.Fatalf("生命周期不完整 added=%v done=%v", added, done)
	}
	if output[1].(map[string]any)["arguments"] != `{"x":1}` || output[2].(map[string]any)["arguments"] != "{}" {
		t.Fatalf("工具参数不完整: %v", output)
	}
	if output[0].(map[string]any)["summary"].([]any)[0].(map[string]any)["text"] != "think" {
		t.Fatalf("思考丢失: %v", output)
	}
}

func TestResponsesLifecycleEmptyAndRoleOnly(t *testing.T) {
	empty := responsesLifecycleEvents(t)
	if len(empty) != 0 {
		t.Fatalf("空流不应编造事件: %v", empty)
	}
	events := responsesLifecycleEvents(t, `{"id":"r","choices":[{"delta":{"role":"assistant"}}]}`, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
	if len(events) != 2 {
		t.Fatalf("role-only不应创建正文项: %v", events)
	}
	output, ok := events[1]["response"].(map[string]any)["output"].([]any)
	if !ok || len(output) != 0 {
		t.Fatalf("空output必须为空数组: %v", events)
	}
}

func TestResponsesEmptyToolArgumentsDone(t *testing.T) {
	events := responsesLifecycleEvents(t,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_empty","function":{"name":"run"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"length"}]}`)
	var sawArguments, sawItem bool
	for _, ev := range events {
		switch ev["type"] {
		case "response.function_call_arguments.done":
			sawArguments = true
			if ev["arguments"] != "" {
				t.Fatalf("空参数不能省略: %v", ev)
			}
		case "response.output_item.done":
			sawItem = true
			item := ev["item"].(map[string]any)
			if item["arguments"] != "" || item["status"] != "incomplete" {
				t.Fatalf("空参数工具定稿不符: %v", item)
			}
		}
	}
	if !sawArguments || !sawItem {
		t.Fatalf("缺少工具定稿事件: %v", events)
	}
}
