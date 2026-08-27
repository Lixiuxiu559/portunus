package protocol

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUpstreamChatURL(t *testing.T) {
	cases := []struct {
		name   string
		typ    Provider
		base   string
		model  string
		stream bool
		want   string
	}{
		{"openai", ProviderOpenAI, "https://api.openai.com/v1", "gpt-4o", false, "https://api.openai.com/v1/chat/completions"},
		{"responses", ProviderOpenAIResponses, "https://api.openai.com/v1", "gpt-4o", false, "https://api.openai.com/v1/responses"},
		{"anthropic", ProviderAnthropic, "https://api.anthropic.com/v1", "claude-3", false, "https://api.anthropic.com/v1/messages"},
		{"gemini", ProviderGemini, "https://generativelanguage.googleapis.com/v1beta", "gemini-2.5-flash", false, "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent"},
		{"gemini-stream", ProviderGemini, "https://generativelanguage.googleapis.com/v1beta", "gemini-2.5-flash", true, "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:streamGenerateContent"},
		{"base-trim", ProviderOpenAI, "https://api.openai.com/v1/", "gpt-4o", false, "https://api.openai.com/v1/chat/completions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := NewUpstream(tc.typ, tc.base, "k")
			if err != nil {
				t.Fatalf("NewUpstream 失败: %v", err)
			}
			if got := u.ChatURL(tc.model, tc.stream); got != tc.want {
				t.Errorf("ChatURL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUpstreamChatHeaders(t *testing.T) {
	cases := []struct {
		name string
		typ  Provider
		key  string
		want map[string]string
	}{
		{"openai", ProviderOpenAI, "sk-1", map[string]string{"Authorization": "Bearer sk-1"}},
		{"responses", ProviderOpenAIResponses, "sk-1", map[string]string{"Authorization": "Bearer sk-1"}},
		{"anthropic", ProviderAnthropic, "sk-1", map[string]string{"x-api-key": "sk-1", "anthropic-version": "2023-06-01"}},
		{"gemini", ProviderGemini, "sk-1", map[string]string{"x-goog-api-key": "sk-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := NewUpstream(tc.typ, "https://x", tc.key)
			if err != nil {
				t.Fatalf("NewUpstream 失败: %v", err)
			}
			h := u.ChatHeaders()
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

func TestUpstreamFetchModelsOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"id":"gpt-4o"},{"id":"gpt-4o-mini"}]}`)
	}))
	defer srv.Close()

	u, err := NewUpstream(ProviderOpenAI, srv.URL, "test-key")
	if err != nil {
		t.Fatalf("NewUpstream 失败: %v", err)
	}
	names, err := u.FetchModels()
	if err != nil {
		t.Fatalf("FetchModels 失败: %v", err)
	}
	if len(names) != 2 || names[0] != "gpt-4o" || names[1] != "gpt-4o-mini" {
		t.Errorf("names = %v, want [gpt-4o gpt-4o-mini]", names)
	}
}

func TestUpstreamFetchModelsAnthropic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("x-api-key") != "test-key" || r.Header.Get("anthropic-version") != "2023-06-01" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"id":"claude-3-opus"},{"id":"claude-3-haiku"}]}`)
	}))
	defer srv.Close()

	u, err := NewUpstream(ProviderAnthropic, srv.URL, "test-key")
	if err != nil {
		t.Fatalf("NewUpstream 失败: %v", err)
	}
	names, err := u.FetchModels()
	if err != nil {
		t.Fatalf("FetchModels 失败: %v", err)
	}
	if len(names) != 2 || names[0] != "claude-3-opus" || names[1] != "claude-3-haiku" {
		t.Errorf("names = %v, want [claude-3-opus claude-3-haiku]", names)
	}
}

func TestUpstreamFetchModelsGemini(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("key") != "test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"models":[{"name":"models/gemini-2.5-flash"},{"name":"models/gemini-2.5-pro"}]}`)
	}))
	defer srv.Close()

	u, err := NewUpstream(ProviderGemini, srv.URL, "test-key")
	if err != nil {
		t.Fatalf("NewUpstream 失败: %v", err)
	}
	names, err := u.FetchModels()
	if err != nil {
		t.Fatalf("FetchModels 失败: %v", err)
	}
	if len(names) != 2 || names[0] != "gemini-2.5-flash" || names[1] != "gemini-2.5-pro" {
		t.Errorf("names = %v, want [gemini-2.5-flash gemini-2.5-pro]", names)
	}
}

func TestUpstreamFetchModelsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	u, err := NewUpstream(ProviderOpenAI, srv.URL, "bad")
	if err != nil {
		t.Fatalf("NewUpstream 失败: %v", err)
	}
	if _, err := u.FetchModels(); err == nil {
		t.Fatal("上游返回 401 时应返回错误")
	}
}

func TestNewUpstreamUnknown(t *testing.T) {
	if _, err := NewUpstream(Provider("unknown"), "https://x", "k"); err == nil {
		t.Fatal("未知协议应返回错误")
	}
}
