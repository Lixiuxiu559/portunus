package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func reasoningBody(p Provider, fields string) []byte {
	switch p {
	case ProviderOpenAIResponses:
		return []byte(`{"model":"custom-model","input":"hi",` + fields + `}`)
	case ProviderGemini:
		return []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],` + fields + `}`)
	default:
		return []byte(`{"model":"custom-model","max_tokens":4096,"messages":[{"role":"user","content":"hi"}],` + fields + `}`)
	}
}

func TestReasoningEffortCrossProtocol(t *testing.T) {
	fields := map[Provider]string{
		ProviderOpenAI:          `"reasoning_effort":"max"`,
		ProviderOpenAIResponses: `"reasoning":{"effort":"max"}`,
		ProviderAnthropic:       `"output_config":{"effort":"max"},"thinking":{"type":"adaptive"}`,
		ProviderGemini:          `"generationConfig":{"thinkingConfig":{"thinkingLevel":"MAX"}}`,
	}
	for from, field := range fields {
		for to := range fields {
			if from == to {
				continue
			}
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				for _, compose := range []bool{false, true} {
					var out []byte
					var err error
					if compose {
						out, _, err = ComposeUpstreamRequest(from, to, reasoningBody(from, field), UpstreamRequest{Model: "arbitrary-provider-alias"})
					} else {
						out, err = ConvertRequest(from, to, reasoningBody(from, field))
					}
					if err != nil {
						t.Fatal(err)
					}
					var m map[string]any
					if err = json.Unmarshal(out, &m); err != nil {
						t.Fatal(err)
					}
					var got any
					switch to {
					case ProviderOpenAI:
						got = m["reasoning_effort"]
					case ProviderOpenAIResponses:
						if r, ok := m["reasoning"].(map[string]any); ok {
							got = r["effort"]
						}
					case ProviderAnthropic:
						if r, ok := m["output_config"].(map[string]any); ok {
							got = r["effort"]
						}
					case ProviderGemini:
						if r, ok := m["generationConfig"].(map[string]any); ok {
							if th, ok := r["thinkingConfig"].(map[string]any); ok {
								got = th["thinkingLevel"]
							}
						}
					}
					want := "max"
					if to == ProviderGemini {
						want = "MAX"
					}
					if got != want {
						t.Fatalf("compose=%v: effort=%v want=%s body=%s", compose, got, want, out)
					}
					if to == ProviderOpenAI && m["thinking"] != nil {
						t.Fatalf("must not guess Chat thinking extension: %s", out)
					}
				}
			})
		}
	}
}

func TestReasoningUnrepresentableBudget(t *testing.T) {
	for _, to := range []Provider{ProviderOpenAI, ProviderOpenAIResponses} {
		_, err := ConvertRequest(ProviderAnthropic, to, reasoningBody(ProviderAnthropic, `"thinking":{"type":"enabled","budget_tokens":2048}`))
		if err == nil || !strings.Contains(err.Error(), "budget") {
			t.Fatalf("to=%s: expected explicit budget error, got %v", to, err)
		}
	}
}

func TestReasoningNativePassthrough(t *testing.T) {
	fields := map[Provider]string{
		ProviderOpenAI:          `"reasoning_effort":"future-level","thinking":{"type":"disabled","custom":true}`,
		ProviderOpenAIResponses: `"reasoning":{"effort":"future-level","summary":"auto","custom":true}`,
		ProviderAnthropic:       `"output_config":{"effort":"future-level","custom":true},"thinking":{"type":"adaptive","display":"omitted"}`,
		ProviderGemini:          `"generationConfig":{"thinkingConfig":{"thinkingBudget":-1,"custom":true}}`,
	}
	for p, f := range fields {
		body := reasoningBody(p, f)
		out, err := ConvertRequest(p, p, body)
		if err != nil || string(out) != string(body) {
			t.Fatalf("native passthrough %s: %s %v", p, out, err)
		}
		out, _, err = ComposeUpstreamRequest(p, p, body, UpstreamRequest{Model: "custom-model"})
		if err != nil {
			t.Fatal(err)
		}
		var a, b map[string]any
		json.Unmarshal(body, &a)
		json.Unmarshal(out, &b)
		want, _ := json.Marshal(a)
		got, _ := json.Marshal(b)
		if string(want) != string(got) {
			t.Fatalf("native compose %s: %s", p, out)
		}
	}
}

func TestReasoningConversionBoundaries(t *testing.T) {
	cases := []struct {
		name         string
		from, to     Provider
		fields, want string
		fail         bool
	}{
		{"Codex关闭到Claude", ProviderOpenAIResponses, ProviderAnthropic, `"reasoning":{"effort":"none"}`, `"type":"disabled"`, false},
		{"Claude关闭到Codex", ProviderAnthropic, ProviderOpenAIResponses, `"thinking":{"type":"disabled"}`, `"effort":"none"`, false},
		{"Gemini关闭到Chat", ProviderGemini, ProviderOpenAI, `"generationConfig":{"thinkingConfig":{"thinkingBudget":0}}`, `"reasoning_effort":"none"`, false},
		{"Codex关闭到Gemini", ProviderOpenAIResponses, ProviderGemini, `"reasoning":{"effort":"none"}`, `"thinkingBudget":0`, false},
		{"Claude预算到Gemini", ProviderAnthropic, ProviderGemini, `"thinking":{"type":"enabled","budget_tokens":2048}`, `"thinkingBudget":2048`, false},
		{"Gemini预算到Claude", ProviderGemini, ProviderAnthropic, `"generationConfig":{"thinkingConfig":{"thinkingBudget":2048}}`, `"budget_tokens":2048`, false},
		{"动态预算不能变成Claude预算", ProviderGemini, ProviderAnthropic, `"generationConfig":{"thinkingConfig":{"thinkingBudget":-1}}`, "", true},
		{"不能从adaptive推导最高档", ProviderAnthropic, ProviderOpenAI, `"thinking":{"type":"adaptive"}`, "", true},
		{"预算不能丢弃即使有effort", ProviderAnthropic, ProviderOpenAIResponses, `"output_config":{"effort":"high"},"thinking":{"type":"enabled","budget_tokens":2048}`, "", true},
		{"Gemini不能同时生成档位预算", ProviderAnthropic, ProviderGemini, `"output_config":{"effort":"high"},"thinking":{"type":"enabled","budget_tokens":2048}`, "", true},
		{"关闭加非none强度不能静默选一个", ProviderAnthropic, ProviderOpenAI, `"output_config":{"effort":"high"},"thinking":{"type":"disabled"}`, "", true},
		{"未知档位原值发送", ProviderOpenAIResponses, ProviderAnthropic, `"reasoning":{"effort":"future-effort"}`, `"effort":"future-effort"`, false},
		{"错误字段类型", ProviderOpenAIResponses, ProviderOpenAI, `"reasoning":{"effort":12}`, "", true},
		{"错误预算类型", ProviderAnthropic, ProviderOpenAI, `"thinking":{"type":"enabled","budget_tokens":"2048"}`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := ConvertRequest(tc.from, tc.to, reasoningBody(tc.from, tc.fields))
			if tc.fail {
				if _, ok := err.(*ReasoningConversionError); !ok {
					t.Fatalf("want reasoning error, got %v body=%s", err, out)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Fatalf("missing %s: %s", tc.want, out)
			}
		})
	}
}

func TestReasoningUnspecifiedDoesNotEnableThinking(t *testing.T) {
	ps := []Provider{ProviderOpenAI, ProviderOpenAIResponses, ProviderAnthropic, ProviderGemini}
	for _, from := range ps {
		for _, to := range ps {
			if from == to {
				continue
			}
			out, err := ConvertRequest(from, to, reasoningBody(from, `"stream":true`))
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{`"reasoning_effort"`, `"reasoning":`, `"thinking":`, `"thinkingConfig":`, `"output_config":`} {
				if strings.Contains(string(out), key) {
					t.Fatalf("%s→%s injected %s: %s", from, to, key, out)
				}
			}
		}
	}
}

func TestReasoningSuffixPreservesExplicitIntent(t *testing.T) {
	for _, field := range []string{`"thinking":{"type":"disabled"}`, `"reasoning_effort":"none"`} {
		out, _, err := ComposeUpstreamRequest(ProviderOpenAI, ProviderOpenAI, reasoningBody(ProviderOpenAI, field), UpstreamRequest{Model: "any-alias", Thinking: &ThinkingConfig{Type: "enabled"}})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(out), `"type":"enabled"`) {
			t.Fatalf("suffix overrode native intent: %s", out)
		}
	}
	out, _, err := ComposeUpstreamRequest(ProviderAnthropic, ProviderOpenAI, reasoningBody(ProviderAnthropic, `"thinking":{"type":"disabled"}`), UpstreamRequest{Model: "alias", Thinking: &ThinkingConfig{Type: "enabled"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"reasoning_effort":"none"`) || strings.Contains(string(out), `"thinking"`) {
		t.Fatalf("disabled lost: %s", out)
	}
}
