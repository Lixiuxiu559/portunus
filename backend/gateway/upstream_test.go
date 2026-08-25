package gateway

import (
	"strings"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
)

func TestBuildURL(t *testing.T) {
	cases := []struct {
		name   string
		typ    protocol.Provider
		base   string
		model  string
		stream bool
		want   string
	}{
		{"openai", protocol.ProviderOpenAI, "https://api.openai.com/v1", "gpt-4o", false, "https://api.openai.com/v1/chat/completions"},
		{"responses", protocol.ProviderOpenAIResponses, "https://api.openai.com/v1", "gpt-4o", false, "https://api.openai.com/v1/responses"},
		{"anthropic", protocol.ProviderAnthropic, "https://api.anthropic.com/v1", "claude-3", false, "https://api.anthropic.com/v1/messages"},
		{"gemini", protocol.ProviderGemini, "https://generativelanguage.googleapis.com/v1beta", "gemini-2.5-flash", false, "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent"},
		{"gemini-stream", protocol.ProviderGemini, "https://generativelanguage.googleapis.com/v1beta", "gemini-2.5-flash", true, "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:streamGenerateContent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := channel.Channel{Type: tc.typ, BaseURL: tc.base}
			if got := buildURL(ch, tc.model, tc.stream); got != tc.want {
				t.Errorf("buildURL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildHeaders(t *testing.T) {
	cases := []struct {
		name string
		typ  protocol.Provider
		key  string
		want map[string]string
	}{
		{"openai", protocol.ProviderOpenAI, "sk-1", map[string]string{"Authorization": "Bearer sk-1"}},
		{"responses", protocol.ProviderOpenAIResponses, "sk-1", map[string]string{"Authorization": "Bearer sk-1"}},
		{"anthropic", protocol.ProviderAnthropic, "sk-1", map[string]string{"x-api-key": "sk-1", "anthropic-version": "2023-06-01"}},
		{"gemini", protocol.ProviderGemini, "sk-1", map[string]string{"x-goog-api-key": "sk-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := buildHeaders(channel.Channel{Type: tc.typ, Key: tc.key})
			for k, v := range tc.want {
				if h.Get(k) != v {
					t.Errorf("header %q = %q, want %q", k, h.Get(k), v)
				}
			}
			if h.Get("Content-Type") != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", h.Get("Content-Type"))
			}
		})
	}
}

func TestRewriteModel(t *testing.T) {
	in := []byte(`{"model":"group-name","messages":[{"role":"user","content":"hi"}]}`)
	out, err := rewriteModel(in, "gpt-4o")
	if err != nil {
		t.Fatalf("rewriteModel 失败: %v", err)
	}
	if !strings.Contains(string(out), `"model":"gpt-4o"`) {
		t.Errorf("model 未改写: %s", out)
	}
	if strings.Contains(string(out), "group-name") {
		t.Errorf("group-name 不应残留: %s", out)
	}
}

func TestNextRoundRobin(t *testing.T) {
	const groupID = 99999
	for i := 0; i < 5; i++ {
		if got := nextRoundRobin(groupID, 3); got != i%3 {
			t.Errorf("第 %d 次轮询 = %d, want %d", i, got, i%3)
		}
	}
}
