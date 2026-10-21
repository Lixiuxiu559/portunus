package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
)

func TestProbeNonStreamProtocols(t *testing.T) {
	cases := []struct {
		name       string
		typ        protocol.Provider
		path, body string
	}{
		{"chat", protocol.ProviderOpenAI, "/chat/completions", `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`},
		{"responses", protocol.ProviderOpenAIResponses, "/responses", `{"id":"resp_1","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}]}`},
		{"anthropic", protocol.ProviderAnthropic, "/messages", `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`},
		{"gemini", protocol.ProviderGemini, "/models/probe-model:generateContent", `{"candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var count atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				if r.URL.Path != tc.path {
					t.Errorf("path=%s", r.URL.Path)
				}
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if tc.typ == protocol.ProviderOpenAI && request["max_tokens"] != float64(32) {
					t.Errorf("budget=%v", request)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			result := ProbeConnection(context.Background(), channel.Channel{Type: tc.typ, BaseURL: srv.URL, Key: "secret"}, ProbeOptions{Model: "probe-model", MaxOutputTokens: 32})
			if !result.Success || result.Stage != "complete" || count.Load() != 1 {
				t.Fatalf("result=%+v requests=%d", result, count.Load())
			}
		})
	}
}

func TestProbeStreamValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		success    bool
	}{
		{"valid", "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", true},
		{"reasoning", "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think\"},\"finish_reason\":\"length\"}]}\n\n", true},
		{"empty", "", false},
		{"done-only", "data: [DONE]\n\n", false},
		{"webpage", "<html>OK</html>", false},
		{"no-finish", "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n", false},
		{"committed-error", "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: {\"error\":{\"message\":\"secret\"}}\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			result := ProbeConnection(context.Background(), channel.Channel{Type: protocol.ProviderOpenAI, BaseURL: srv.URL}, ProbeOptions{Model: "m", Stream: true, MaxOutputTokens: 32})
			if result.Success != tc.success {
				t.Fatalf("%+v", result)
			}
			if strings.Contains(result.Message, "secret") {
				t.Fatal("unsafe result")
			}
		})
	}
}

func TestProbeFailuresAndNoRetry(t *testing.T) {
	var diagnostic bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&diagnostic)
	t.Cleanup(func() { log.SetOutput(previous) })
	for _, tc := range []struct {
		name   string
		status int
		body   string
		stage  string
	}{
		{"unauthorized", 401, "secret", "upstream"},
		{"budget-rejected", 400, `{"error":{"message":"max_tokens unsupported; use max_completion_tokens"}}`, "upstream"},
		{"non-json", 200, "secret", "response"},
		{"no-choices", 200, `{"ok":true}`, "response"},
		{"role-only", 200, `{"choices":[{"message":{"role":"assistant"},"finish_reason":"stop"}]}`, "response"},
		{"no-finish", 200, `{"choices":[{"message":{"content":"OK"}}]}`, "response"},
		{"empty-output", 200, `{"choices":[{"message":{"content":""},"finish_reason":"stop"}]}`, "response"},
		{"large", 200, strings.Repeat("x", probeResponseLimit+1), "response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var count atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			result := ProbeConnection(context.Background(), channel.Channel{Type: protocol.ProviderOpenAI, BaseURL: srv.URL, Key: "secret"}, ProbeOptions{Model: "m", MaxOutputTokens: 32})
			if result.Success || result.Stage != tc.stage || count.Load() != 1 {
				t.Fatalf("result=%+v requests=%d", result, count.Load())
			}
			if strings.Contains(result.Message, "secret") || strings.Contains(result.Message, srv.URL) {
				t.Fatal("unsafe result")
			}
		})
	}
	if strings.Contains(diagnostic.String(), "secret") {
		t.Fatal("probe echoed secret into diagnostic log")
	}
}

func TestProbeCancellationAndInvalidInput(t *testing.T) {
	for _, opt := range []ProbeOptions{{Model: "", MaxOutputTokens: 32}, {Model: "m", MaxOutputTokens: 0}, {Model: "m", MaxOutputTokens: 129}} {
		if result := ProbeConnection(context.Background(), channel.Channel{}, opt); result.Stage != "input" {
			t.Fatalf("%+v", result)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	result := ProbeConnection(ctx, channel.Channel{Type: protocol.ProviderOpenAI, BaseURL: srv.URL}, ProbeOptions{Model: "m", MaxOutputTokens: 32})
	if result.Stage != "cancelled" || result.Success {
		t.Fatalf("%+v", result)
	}
}

func TestProbeRejectsRedirect(t *testing.T) {
	var count atomic.Int32
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1) }))
	defer dest.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	result := ProbeConnection(context.Background(), channel.Channel{Type: protocol.ProviderOpenAI, BaseURL: srv.URL}, ProbeOptions{Model: "m", MaxOutputTokens: 32})
	if result.Success || result.Status != 307 || count.Load() != 0 {
		t.Fatalf("%+v", result)
	}
}

func TestProbeReasoningOutputAndDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"reasoning_content":"thinking"},"finish_reason":"length"}]}`)
	}))
	defer srv.Close()
	result := ProbeConnection(context.Background(), channel.Channel{Type: protocol.ProviderOpenAI, BaseURL: srv.URL}, ProbeOptions{Model: "m", MaxOutputTokens: 1})
	if !result.Success {
		t.Fatalf("reasoning output: %+v", result)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	result = ProbeConnection(ctx, channel.Channel{Type: protocol.ProviderOpenAI, BaseURL: srv.URL}, ProbeOptions{Model: "m", MaxOutputTokens: 1})
	if result.Success || result.Stage != "timeout" {
		t.Fatalf("deadline: %+v", result)
	}
}

func TestProbeOtherProtocolStreams(t *testing.T) {
	cases := []struct {
		name   string
		typ    protocol.Provider
		events []string
	}{
		{"responses", protocol.ProviderOpenAIResponses, []string{
			`{"type":"response.created","response":{"id":"resp_1","status":"in_progress","model":"m"}}`,
			`{"type":"response.output_text.delta","delta":"OK","output_index":0,"content_index":0}`,
			`{"type":"response.completed","response":{"id":"resp_1","status":"completed","model":"m"}}`,
		}},
		{"anthropic", protocol.ProviderAnthropic, []string{
			`{"type":"message_start","message":{"id":"msg_1","model":"m","role":"assistant","content":[],"usage":{"input_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"OK"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		}},
		{"gemini", protocol.ProviderGemini, []string{
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP"}]}`,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, event := range tc.events {
					io.WriteString(w, "data: "+event+"\n\n")
				}
			}))
			defer srv.Close()
			result := ProbeConnection(context.Background(), channel.Channel{Type: tc.typ, BaseURL: srv.URL}, ProbeOptions{Model: "m", Stream: true, MaxOutputTokens: 32})
			if !result.Success {
				t.Fatalf("%+v", result)
			}
		})
	}
}
