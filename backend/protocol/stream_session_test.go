package protocol

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type byteReader struct{ s string }

func (r *byteReader) Read(p []byte) (int, error) {
	if len(r.s) == 0 {
		return 0, io.EOF
	}
	p[0] = r.s[0]
	r.s = r.s[1:]
	return 1, nil
}
func TestSSEReaderCompleteEvent(t *testing.T) {
	raw := ": keepalive\r\nevent: custom\rid: abc\nretry: 123\nunknown: keep\ndata: {\"x\":\ndata: 1}\r\n\r\n"
	r := NewSSEReader(&byteReader{s: raw}, 1024)
	ev, err := r.ReadEvent()
	if err != nil {
		t.Fatal(err)
	}
	if string(ev.Data) != "{\"x\":\n1}" || ev.Event != "custom" || ev.ID != "abc" || ev.Retry == nil || *ev.Retry != 123 || !ev.HasData {
		t.Fatalf("event mismatch: %#v", ev)
	}
	allRaw := append([]byte(nil), ev.Raw...)
	for {
		next, err := r.ReadEvent()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		allRaw = append(allRaw, next.Raw...)
	}
	if string(allRaw) != raw {
		t.Fatalf("wire bytes changed: %q", allRaw)
	}
}
func TestSSEReaderEOFAndLimit(t *testing.T) {
	for _, raw := range []string{"data: {}", "data: {}\n", "data: {}\r", "data: {}\r\n\n"} {
		r := NewSSEReader(strings.NewReader(raw), len(raw))
		ev, err := r.ReadEvent()
		if err != nil || string(ev.Raw) != raw || string(ev.Data) != "{}" {
			t.Fatalf("raw=%q ev=%#v err=%v", raw, ev, err)
		}
	}
	for _, raw := range []string{"data: " + strings.Repeat("x", 64), ": " + strings.Repeat("x", 64), strings.Repeat("\nx", 64)} {
		r := NewSSEReader(strings.NewReader(raw), 16)
		var err error
		for err == nil {
			_, err = r.ReadEvent()
		}
		if !errors.Is(err, ErrSSEEventTooLarge) {
			t.Fatalf("unexpected %v", err)
		}
	}
}
func newSessionForTest(t *testing.T, from, to Provider) *StreamSession {
	t.Helper()
	conv, err := NewStreamConverter(from, to)
	if err != nil {
		t.Fatal(err)
	}
	return NewStreamSession(from, to, conv)
}
func processTest(t *testing.T, s *StreamSession, payload string) StreamBatch {
	t.Helper()
	ev := SSEEvent{Data: []byte(payload), Raw: []byte("data: " + payload + "\n\n"), HasData: true}
	b, err := s.Process(ev)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestStreamSessionNativeRawAndUnknown(t *testing.T) {
	s := newSessionForTest(t, ProviderAnthropic, ProviderAnthropic)
	raw := []byte(": comment\nevent: vendor.new\nid: 12\ndata: {\"type\":\"vendor.new\",\"secret\":1}\n\n")
	ev, err := NewSSEReader(bytes.NewReader(raw), 1024).ReadEvent()
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Process(ev)
	if err != nil || len(b.Frames) != 1 || !bytes.Equal(b.Frames[0], raw) || !b.Meaningful || !b.ReplayUnsafe {
		t.Fatalf("batch=%#v err=%v", b, err)
	}
	if b, err = s.Finish(); !errors.Is(err, ErrStreamIncomplete) || len(b.Frames) != 0 {
		t.Fatalf("must not invent terminal: %#v %v", b, err)
	}
}
func TestStreamSessionTerminalProtocols(t *testing.T) {
	cases := []struct {
		name     string
		provider Provider
		events   []string
		status   string
		done     bool
	}{
		{"chat", ProviderOpenAI, []string{`{"choices":[{"delta":{},"finish_reason":"stop"}]}`, `{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":1}}`, "[DONE]"}, "completed", true},
		{"chat-length", ProviderOpenAI, []string{`{"choices":[{"delta":{},"finish_reason":"length"}]}`}, "incomplete", false},
		{"anthropic", ProviderAnthropic, []string{`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, `{"type":"message_stop"}`}, "completed", true},
		{"anthropic-refusal", ProviderAnthropic, []string{`{"type":"message_delta","delta":{"stop_reason":"refusal"}}`, `{"type":"message_stop"}`}, "completed", true},
		{"responses", ProviderOpenAIResponses, []string{`{"type":"response.completed","response":{"id":"r","status":"completed"}}`}, "completed", true},
		{"responses-incomplete", ProviderOpenAIResponses, []string{`{"type":"response.incomplete","response":{"id":"r","incomplete_details":{"reason":"max_output_tokens"}}}`}, "incomplete", true},
		{"gemini", ProviderGemini, []string{`{"candidates":[{"finishReason":"STOP"}]}`}, "completed", false},
		{"gemini-block", ProviderGemini, []string{`{"promptFeedback":{"blockReason":"SAFETY"}}`}, "incomplete", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, to := range []Provider{ProviderOpenAI, ProviderOpenAIResponses, ProviderAnthropic, ProviderGemini} {
				s := newSessionForTest(t, tc.provider, to)
				var last StreamBatch
				var output [][]byte
				for _, ev := range tc.events {
					last = processTest(t, s, ev)
					output = append(output, last.Frames...)
				}
				if last.Done != tc.done || s.Outcome().Status != tc.status || !s.Outcome().TerminalObserved {
					t.Fatalf("%s: last=%#v outcome=%#v", to, last, s.Outcome())
				}
				finish, err := s.Finish()
				if err != nil {
					t.Fatal(err)
				}
				if to == tc.provider && len(finish.Frames) != 0 {
					t.Fatalf("native appended frames: %s", finish.Frames)
				}
				output = append(output, finish.Frames...)
				if b, err := s.Finish(); err != nil || len(b.Frames) != 0 {
					t.Fatalf("duplicate finish: %#v %v", b, err)
				}
				if tc.name == "chat" && to == ProviderOpenAIResponses {
					if bytes.Count(bytes.Join(output, nil), []byte(`"type":"response.completed"`)) != 1 {
						t.Fatalf("wrong terminal count: %s", output)
					}
					if !bytes.Contains(bytes.Join(output, nil), []byte(`"input_tokens":3`)) {
						t.Fatalf("missing tail usage: %s", output)
					}
				}
				if tc.name == "gemini-block" && to == ProviderOpenAIResponses && !bytes.Contains(bytes.Join(output, nil), []byte(`"status":"incomplete"`)) {
					t.Fatalf("block became completed: %s", output)
				}
			}
		})
	}
}
func TestStreamSessionFailuresNoSyntheticSuccess(t *testing.T) {
	cases := []struct {
		provider Provider
		payload  string
		failure  bool
	}{
		{ProviderOpenAI, `{"choices":[{"delta":{"role":"assistant"}}]}`, false},
		{ProviderAnthropic, `{"type":"message_start","message":{"id":"r"}}`, false},
		{ProviderOpenAIResponses, `{"type":"response.created","response":{"id":"r"}}`, false},
		{ProviderGemini, `{"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}`, false},
		{ProviderOpenAIResponses, `{"type":"response.failed","response":{"error":{"code":"server_error"}}}`, true},
		{ProviderAnthropic, `{"type":"error","error":{"type":"overloaded_error","message":"private secret"}}`, true},
	}
	for _, tc := range cases {
		for _, to := range []Provider{ProviderOpenAI, ProviderAnthropic, ProviderOpenAIResponses, ProviderGemini} {
			s := newSessionForTest(t, tc.provider, to)
			b, err := s.Process(SSEEvent{Data: []byte(tc.payload), HasData: true, Raw: []byte(tc.payload)})
			if tc.failure {
				var failure *StreamFailureError
				if !errors.As(err, &failure) || strings.Contains(err.Error(), "private") {
					t.Fatalf("wrong failure %v", err)
				}
				if to != tc.provider && len(b.Frames) != 0 {
					t.Fatal("cross-protocol failed event leaked")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			end, err := s.Finish()
			if !errors.Is(err, ErrStreamIncomplete) || len(end.Frames) != 0 {
				t.Fatalf("synthetic success: %#v %v", end, err)
			}
		}
	}
}
func TestStreamSessionMeaningfulAfterEncoding(t *testing.T) {
	s := newSessionForTest(t, ProviderOpenAI, ProviderGemini)
	if b := processTest(t, s, `{"choices":[{"delta":{"role":"assistant"}}]}`); b.Meaningful {
		t.Fatal("role was meaningful")
	}
	b := processTest(t, s, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"run","arguments":"{}"}}]}}]}`)
	if b.Meaningful {
		t.Fatal("Gemini has not emitted buffered tool yet")
	}
	processTest(t, s, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
	b, err := s.Finish()
	if err != nil || !b.Meaningful {
		t.Fatalf("tool finish: %#v %v", b, err)
	}
}
func TestStreamSessionRequiresAllChoicesAndRealAnthropicStop(t *testing.T) {
	s := newSessionForTest(t, ProviderOpenAI, ProviderAnthropic)
	processTest(t, s, `{"choices":[{"index":0,"delta":{},"finish_reason":"length"},{"index":1,"delta":{"role":"assistant"}}]}`)
	if s.Outcome().TerminalObserved {
		t.Fatal("unfinished choice ignored")
	}
	processTest(t, s, `{"choices":[{"index":1,"delta":{},"finish_reason":"stop"}]}`)
	if s.Outcome().Status != "incomplete" {
		t.Fatal(s.Outcome())
	}
	a := newSessionForTest(t, ProviderAnthropic, ProviderOpenAI)
	processTest(t, a, `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`)
	if _, err := a.Finish(); !errors.Is(err, ErrStreamIncomplete) {
		t.Fatal(err)
	}
}
func TestStreamRequestReplaySafety(t *testing.T) {
	for _, tc := range []struct {
		p    Provider
		body string
		safe bool
	}{
		{ProviderOpenAI, `{"messages":[]}`, true},
		{ProviderOpenAI, `{"tools":[{"type":"function","function":{"name":"execute"}}]}`, true},
		{ProviderAnthropic, `{"tools":[{"name":"execute","input_schema":{}}]}`, true},
		{ProviderAnthropic, `{"tools":[{"type":"web_search_20250305","name":"web_search"}]}`, true},
		{ProviderGemini, `{"tools":[{"googleSearch":{}}]}`, true},
		{ProviderOpenAIResponses, `{"previous_response_id":"resp_old"}`, false},
		{ProviderOpenAIResponses, `{"tools":[{"type":"computer_use_preview"}]}`, false},
		{ProviderGemini, `{"tools":[{"codeExecution":{}}]}`, false},
		{ProviderAnthropic, `{"tools":[{"type":"computer_20250124"}]}`, false},
	} {
		if got := StreamRequestReplaySafe(tc.p, []byte(tc.body)); got != tc.safe {
			t.Fatalf("%s %s safe=%t", tc.p, tc.body, got)
		}
	}
}

func TestStreamSessionOpaqueNativeAndErrorOutcome(t *testing.T) {
	for _, p := range []Provider{ProviderOpenAI, ProviderAnthropic, ProviderOpenAIResponses, ProviderGemini} {
		s := newSessionForTest(t, p, p)
		raw := []byte("event: vendor.opaque\nid: 5\ndata: opaque non-JSON\n\n")
		ev, err := NewSSEReader(bytes.NewReader(raw), 1024).ReadEvent()
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.Process(ev)
		if err != nil || !b.Meaningful || !b.ReplayUnsafe || !bytes.Equal(bytes.Join(b.Frames, nil), raw) {
			t.Fatalf("%s: %#v %v", p, b, err)
		}
	}
	for _, tc := range []struct {
		payload, event, status, reason string
		want                           error
	}{
		{"[DONE]", "", "truncated", "missing_terminal", ErrStreamIncomplete},
		{"not-json", "response.created", "failed", "invalid_event", ErrStreamInvalid},
		{`{"type":"response.completed"}`, "", "failed", "invalid_event", ErrStreamInvalid},
	} {
		s := newSessionForTest(t, ProviderOpenAIResponses, ProviderAnthropic)
		_, err := s.Process(SSEEvent{Event: tc.event, Data: []byte(tc.payload), HasData: true})
		if !errors.Is(err, tc.want) || s.Outcome().Status != tc.status || s.Outcome().Reason != tc.reason {
			t.Fatalf("%q: %v %#v", tc.payload, err, s.Outcome())
		}
	}
}
func TestStreamSessionNoEarlyTargetTerminal(t *testing.T) {
	s := newSessionForTest(t, ProviderOpenAI, ProviderAnthropic)
	batch := processTest(t, s, `{"choices":[{"delta":{"content":"answer"},"finish_reason":"stop"}]}`)
	if batch.Terminal || !batch.Meaningful || !s.Outcome().TerminalObserved {
		t.Fatalf("source and target terminals conflated: %#v", batch)
	}
	tail, err := s.Finish()
	if err != nil || !tail.Terminal || !bytes.Contains(bytes.Join(tail.Frames, nil), []byte("message_stop")) {
		t.Fatalf("missing actual terminal %#v %v", tail, err)
	}
}
func TestStreamSessionDuplicateAndOrderedChoiceTerminals(t *testing.T) {
	s := newSessionForTest(t, ProviderOpenAI, ProviderAnthropic)
	processTest(t, s, `{"choices":[{"index":0,"delta":{},"finish_reason":"length"},{"index":1,"delta":{"role":"assistant"}}]}`)
	processTest(t, s, `{"choices":[{"index":1,"delta":{},"finish_reason":"stop"}]}`)
	if s.Outcome().Status != "incomplete" || s.Outcome().Reason != "length" {
		t.Fatal(s.Outcome())
	}
	_, err := s.Process(SSEEvent{HasData: true, Data: []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`)})
	if !errors.Is(err, ErrStreamInvalid) || s.Outcome().Status != "failed" {
		t.Fatalf("duplicate terminal %v %#v", err, s.Outcome())
	}
}
func TestStreamSessionOutcomeReasonsDoNotLeak(t *testing.T) {
	for _, tc := range []struct {
		p    Provider
		body string
	}{
		{ProviderOpenAIResponses, `{"type":"response.incomplete","response":{"incomplete_details":{"reason":"private-secret"}}}`},
		{ProviderGemini, `{"promptFeedback":{"blockReason":"private-secret"}}`},
		{ProviderGemini, `{"candidates":[{"finishReason":"private-secret"}]}`},
	} {
		s := newSessionForTest(t, tc.p, tc.p)
		processTest(t, s, tc.body)
		if strings.Contains(s.Outcome().Reason, "private") {
			t.Fatal(s.Outcome())
		}
	}
}
func TestStreamSessionNativeFailureFrameRetained(t *testing.T) {
	for _, tc := range []struct {
		p   Provider
		raw string
	}{
		{ProviderAnthropic, "event: error\nid: 6\nretry: 42\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\"}}\n\n"},
		{ProviderOpenAIResponses, "event: response.failed\nid: 6\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_error\"}}}\n\n"},
	} {
		ev, err := NewSSEReader(strings.NewReader(tc.raw), 1024).ReadEvent()
		if err != nil {
			t.Fatal(err)
		}
		s := newSessionForTest(t, tc.p, tc.p)
		b, err := s.Process(ev)
		var failure *StreamFailureError
		if !errors.As(err, &failure) || !b.Terminal || !bytes.Equal(bytes.Join(b.Frames, nil), []byte(tc.raw)) || s.Outcome().Status != "failed" {
			t.Fatalf("native failure %#v %v %#v", b, err, s.Outcome())
		}
	}
}
func TestSSEReaderBareCRDispatchWithoutNextByte(t *testing.T) {
	input, output := io.Pipe()
	defer input.Close()
	defer output.Close()
	reader := NewSSEReader(input, 1024)
	type result struct {
		ev  SSEEvent
		err error
	}
	ready := make(chan result, 1)
	go func() { ev, err := reader.ReadEvent(); ready <- result{ev, err} }()
	raw := "data: {\"done\":true}\r\r"
	if _, err := output.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-ready:
		if got.err != nil || string(got.ev.Raw) != raw || string(got.ev.Data) != `{"done":true}` {
			t.Fatalf("%#v %v", got.ev, got.err)
		}
	case <-time.After(time.Second):
		_ = input.Close()
		_ = output.Close()
		<-ready
		t.Fatal("bare CR event waited for a future byte on a persistent connection")
	}
}
func TestSSEReaderSplitCRLFPreservesWholeWire(t *testing.T) {
	raw := ": comment\r\nevent: first\r\ndata: one\r\n\r\nevent: second\r\ndata: two\r\n\r\n"
	reader := NewSSEReader(&byteReader{s: raw}, 4096)
	var wire []byte
	var data []string
	for {
		event, err := reader.ReadEvent()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		wire = append(wire, event.Raw...)
		if event.HasData {
			data = append(data, string(event.Data))
		}
	}
	if string(wire) != raw || strings.Join(data, ",") != "one,two" {
		t.Fatalf("wire=%q data=%#v", wire, data)
	}
}
func TestSSEReaderBOMOnlyAtStreamStart(t *testing.T) {
	bom := "\xef\xbb\xbf"
	raw := bom + "data: first\n\n" + bom + "data: ignored\n\ndata: last\n\n"
	reader := NewSSEReader(strings.NewReader(raw), 1024)
	var wire []byte
	var data []string
	for {
		event, err := reader.ReadEvent()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		wire = append(wire, event.Raw...)
		if event.HasData {
			data = append(data, string(event.Data))
		}
	}
	if string(wire) != raw || strings.Join(data, ",") != "first,last" {
		t.Fatalf("wire=%q data=%#v", wire, data)
	}
}
