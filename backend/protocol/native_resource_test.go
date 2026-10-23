package protocol

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/resource"
)

func requireNativeLimit(t *testing.T, err error, kind string) {
	t.Helper()
	var limit *resource.LimitError
	if !errors.As(err, &limit) || limit.Kind != kind {
		t.Fatalf("want %s resource limit, got %v", kind, err)
	}
	if strings.Contains(err.Error(), "private-secret") {
		t.Fatal("source value leaked")
	}
}
func TestNativeResourceBodyBoundaries(t *testing.T) {
	body := []byte(`{"model":"x","messages":[]}`)
	l := resource.Defaults()
	l.MaxRequestBytes = int64(len(body))
	if _, err := CaptureRequest(ProviderOpenAI, body, l); err != nil {
		t.Fatal(err)
	}
	l.MaxRequestBytes--
	_, err := CaptureRequest(ProviderOpenAI, body, l)
	requireNativeLimit(t, err, "body_bytes")
	l = resource.Defaults()
	l.MaxResponseBytes = int64(len(body))
	if _, err := CaptureResponse(ProviderOpenAI, body, l); err != nil {
		t.Fatal(err)
	}
	l.MaxResponseBytes--
	_, err = CaptureResponse(ProviderOpenAI, body, l)
	requireNativeLimit(t, err, "body_bytes")
	// Event capture has its own SSE frame bound, not the nonstream response bound.
	if _, err = CaptureResponseEvent(ProviderOpenAI, SSEEvent{Data: body}, l); err != nil {
		t.Fatal(err)
	}
}
func TestNativeResourceFragments(t *testing.T) {
	tests := []struct {
		p       Provider
		body    string
		request bool
		count   int
	}{
		{ProviderOpenAI, `{"messages":[{"content":[{},{}]}]}`, true, 4},
		{ProviderGemini, `{"contents":[{"parts":[{},{}]}]}`, true, 4},
		{ProviderOpenAIResponses, `{"input":[{},{}],"tools":[{}]}`, true, 5},
		{ProviderOpenAI, `{"choices":[{"message":{}},{"delta":null}]}`, false, 4},
		{ProviderOpenAIResponses, `{"output":[{"content":[{},{}],"summary":[{}]}]}`, false, 5},
		{ProviderGemini, `{"candidates":[{"content":{"parts":[{},{}]}}]}`, false, 4},
	}
	for _, tc := range tests {
		t.Run(tc.body, func(t *testing.T) {
			l := resource.Defaults()
			l.MaxNativeFragments = tc.count
			capture := func() error {
				if tc.request {
					_, e := CaptureRequest(tc.p, []byte(tc.body), l)
					return e
				}
				_, e := CaptureResponse(tc.p, []byte(tc.body), l)
				return e
			}
			if err := capture(); err != nil {
				t.Fatal(err)
			}
			l.MaxNativeFragments--
			requireNativeLimit(t, capture(), "native_fragments")
		})
	}
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i < 10000; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"`)
		b.WriteString(strings.Repeat("x", i%20+1))
		b.WriteString(`":0`)
	}
	b.WriteByte('}')
	l := resource.Defaults()
	l.MaxNativeFragments = 20
	_, err := CaptureRequest(ProviderOpenAI, []byte(b.String()), l)
	requireNativeLimit(t, err, "native_fragments")
	// A user-defined input array is one opaque value, not a native fragment list.
	l.MaxNativeFragments = 3
	if _, err := CaptureRequest(ProviderAnthropic, []byte(`{"messages":[{"content":[{"type":"tool_use","input":[1,2,3,4,5]}]}]}`), l); err != nil {
		t.Fatal(err)
	}
}
func TestNativeResourceToolAndText(t *testing.T) {
	tests := []struct {
		p    Provider
		body string
		kind string
		max  int64
	}{
		{ProviderOpenAI, `{"messages":[{"tool_calls":[{"function":{"arguments":"1234"}}]}]}`, "tool_arguments_bytes", 4},
		{ProviderAnthropic, `{"messages":[{"content":[{"input":{"x":1}}]}]}`, "tool_arguments_bytes", 7},
		{ProviderOpenAIResponses, `{"input":[{"arguments":"1234"}]}`, "tool_arguments_bytes", 4},
		{ProviderGemini, `{"contents":[{"parts":[{"functionCall":{"args":{"x":1}}}]}]}`, "tool_arguments_bytes", 7},
		{ProviderOpenAI, `{"messages":[{"content":"123"},{"content":"45"}]}`, "accumulated_bytes", 5},
	}
	for _, tc := range tests {
		t.Run(tc.body, func(t *testing.T) {
			l := resource.Defaults()
			if tc.kind == "tool_arguments_bytes" {
				l.MaxToolArgumentsBytes = tc.max
			} else {
				l.MaxAccumulatedBytes = tc.max
			}
			if _, err := CaptureRequest(tc.p, []byte(tc.body), l); err != nil {
				t.Fatal(err)
			}
			if tc.kind == "tool_arguments_bytes" {
				l.MaxToolArgumentsBytes--
			} else {
				l.MaxAccumulatedBytes--
			}
			_, err := CaptureRequest(tc.p, []byte(tc.body), l)
			requireNativeLimit(t, err, tc.kind)
		})
	}
	l := resource.Defaults()
	l.MaxAccumulatedBytes = 4
	_, err := CaptureResponse(ProviderOpenAI, []byte(`{"choices":[{"message":{"content":"private-secret"}}]}`), l)
	requireNativeLimit(t, err, "accumulated_bytes")
}
func TestNativeResourceDepthAndOpaqueCompatibility(t *testing.T) {
	l := resource.Defaults()
	l.MaxJSONDepth = 1
	body := []byte(`{"opaque":"[{\\\"}]"}`)
	if _, err := CaptureRequest(ProviderOpenAI, body, l); err != nil {
		t.Fatal(err)
	}
	_, err := CaptureRequest(ProviderOpenAI, []byte(`{"x":{}}`), l)
	requireNativeLimit(t, err, "json_depth")
	opaque := []byte("opaque private-secret")
	out, err := ConvertRequest(ProviderOpenAI, ProviderOpenAI, opaque)
	if err != nil || !bytes.Equal(out, opaque) {
		t.Fatalf("opaque request changed: %s %v", out, err)
	}
	out, _, err = ConvertResponseWithReport(ProviderOpenAI, ProviderOpenAI, opaque)
	if err != nil || !bytes.Equal(out, opaque) {
		t.Fatalf("opaque response changed: %s %v", out, err)
	}
	// Numbers are never decoded through float64 during the resource walk.
	if _, err := CaptureRequest(ProviderOpenAI, []byte(`{"n":1e999999,"messages":[]}`)); err != nil {
		t.Fatal(err)
	}
}
func TestNativeResourceOutputAndClone(t *testing.T) {
	body := []byte(`{"id":"x","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	l := resource.Defaults()
	l.MaxResponseBytes = int64(len(body))
	e, err := CaptureResponse(ProviderOpenAI, body, l)
	if err != nil {
		t.Fatal(err)
	}
	c := e.clone()
	if c.limits != l {
		t.Fatal("clone lost limits")
	}
	_, _, err = c.Render(ProviderAnthropic)
	requireNativeLimit(t, err, "body_bytes")
	request := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	l = resource.Defaults()
	l.MaxRequestBytes = int64(len(request))
	_, _, err = ComposeUpstreamRequest(ProviderOpenAI, ProviderOpenAI, request, UpstreamRequest{Model: strings.Repeat("x", 100), Limits: l})
	requireNativeLimit(t, err, "body_bytes")
}

func TestNativeResourceToolResultAndStructuredText(t *testing.T) {
	cases := []struct {
		p       Provider
		body    string
		request bool
		bytes   int64
	}{
		{ProviderAnthropic, `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"12345"}]}]}`, true, 5},
		{ProviderAnthropic, `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":[{"type":"text","text":"12"},{"type":"text","text":"345"}]}]}]}`, true, 5},
		{ProviderAnthropic, `{"messages":[{"role":"user","content":[{"type":"web_search_tool_result","content":[{"type":"web_search_result","title":"123","url":"45"}]}]}]}`, true, 5},
		{ProviderGemini, `{"contents":[{"parts":[{"functionResponse":{"name":"t","response":{"text":"12345"}}}]}]}`, true, 16},
		{ProviderOpenAI, `{"choices":[{"message":{"content":[{"type":"text","text":"12"},{"type":"text","text":"345"}]}}]}`, false, 5},
	}
	for _, tc := range cases {
		t.Run(tc.body, func(t *testing.T) {
			l := resource.Defaults()
			l.MaxAccumulatedBytes = tc.bytes
			check := func() error {
				if tc.request {
					_, err := CaptureRequest(tc.p, []byte(tc.body), l)
					return err
				}
				_, _, err := ConvertResponseWithReport(tc.p, tc.p, []byte(tc.body), l)
				return err
			}
			if err := check(); err != nil {
				t.Fatal(err)
			}
			l.MaxAccumulatedBytes--
			requireNativeLimit(t, check(), "accumulated_bytes")
		})
	}
}
