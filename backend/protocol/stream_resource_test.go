package protocol

import (
	"errors"
	"fmt"
	"github.com/Lixiuxiu559/portunus/backend/resource"
	"strings"
	"testing"
)

func streamLimits(total, tool int64) resource.Limits {
	l := resource.Defaults()
	l.MaxAccumulatedBytes = total
	l.MaxToolArgumentsBytes = tool
	return l
}
func resourceKind(t *testing.T, err error, kind string) {
	t.Helper()
	var limit *resource.LimitError
	if !errors.As(err, &limit) || limit.Scope != resource.Stream || limit.Kind != kind {
		t.Fatalf("want stream/%s resource error, got %v", kind, err)
	}
}
func toolChunk(index int, args string) []byte {
	return []byte(fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":%d,"function":{"arguments":%q}}]}}]}`, index, args))
}
func textChunk(text, reason string) []byte {
	return []byte(fmt.Sprintf(`{"choices":[{"delta":{"content":%q,"reasoning_content":%q}}]}`, text, reason))
}

func TestStreamResourceToolExactAndSticky(t *testing.T) {
	for _, to := range []Provider{ProviderAnthropic, ProviderOpenAIResponses, ProviderGemini, ProviderOpenAI} {
		t.Run(string(to), func(t *testing.T) {
			c, err := NewStreamConverter(ProviderOpenAI, to, WithResourceLimits(streamLimits(20, 4)))
			if err != nil {
				t.Fatal(err)
			}
			for _, arg := range []string{"ab", "cd"} {
				if _, err = c.Convert(toolChunk(0, arg)); err != nil {
					t.Fatal(err)
				}
			}
			_, err = c.Convert(toolChunk(0, "e"))
			resourceKind(t, err, "tool_arguments_bytes")
			frames, err := c.Finish()
			resourceKind(t, err, "tool_arguments_bytes")
			if len(frames) != 0 {
				t.Fatal("resource failure manufactured finish frames")
			}
		})
	}
}
func TestStreamResourceTotalAcrossToolsAndText(t *testing.T) {
	c, _ := NewStreamConverter(ProviderOpenAI, ProviderOpenAIResponses, WithResourceLimits(streamLimits(6, 4)))
	for i, arg := range []string{"abc", "def"} {
		if _, err := c.Convert(toolChunk(i, arg)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := c.Convert(textChunk("g", ""))
	resourceKind(t, err, "accumulated_bytes")
	c, _ = NewStreamConverter(ProviderOpenAI, ProviderAnthropic, WithResourceLimits(streamLimits(4, 4)))
	if _, err := c.Convert(textChunk("ab", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Convert(textChunk("", "cd")); err != nil {
		t.Fatal(err)
	}
	_, err = c.Convert(textChunk("e", ""))
	resourceKind(t, err, "accumulated_bytes")
}
func TestStreamResourceLegacySourceBoundaries(t *testing.T) {
	cases := []struct {
		c      StreamConverter
		p      Provider
		events []string
	}{
		{newAnthropicToOpenAIStream(), ProviderAnthropic, []string{`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ab"}}`, `{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"cd"}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"e"}}`}},
		{newResponsesToOpenAIStream(), ProviderOpenAIResponses, []string{`{"type":"response.output_text.delta","delta":"ab"}`, `{"type":"response.reasoning_text.delta","delta":"cd"}`, `{"type":"response.output_text.delta","delta":"e"}`}},
		{newGeminiToOpenAIStream(), ProviderGemini, []string{`{"candidates":[{"content":{"parts":[{"text":"ab"}]}}]}`, `{"candidates":[{"content":{"parts":[{"text":"cd","thought":true}]}}]}`, `{"candidates":[{"content":{"parts":[{"text":"e"}]}}]}`}},
	}
	for _, tc := range cases {
		t.Run(string(tc.p), func(t *testing.T) {
			setStreamLimits(tc.c, streamLimits(4, 4))
			for _, e := range tc.events[:2] {
				if _, err := tc.c.Convert([]byte(e)); err != nil {
					t.Fatal(err)
				}
			}
			_, err := tc.c.Convert([]byte(tc.events[2]))
			resourceKind(t, err, "accumulated_bytes")
			_, err = tc.c.Finish()
			resourceKind(t, err, "accumulated_bytes")
		})
	}
}
func TestStreamResourceEmptyItemsAndRepeatedIndexes(t *testing.T) {
	c := newResponsesFromOpenAI().(*fromOpenAISkeleton)
	l := streamLimits(100, 4)
	l.MaxNativeFragments = 2
	c.setResourceLimits(l)
	for i := 0; i < 2; i++ {
		if _, err := c.Convert(toolChunk(i, "")); err != nil {
			t.Fatal(err)
		}
	}
	_, err := c.Convert(toolChunk(2, ""))
	resourceKind(t, err, "stream_items")
	c = newResponsesFromOpenAI().(*fromOpenAISkeleton)
	c.setResourceLimits(l)
	// Closing tools and reusing an index creates another retained output item.
	if _, err := c.Convert(toolChunk(0, "ab")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Convert(textChunk("c", "")); err != nil {
		t.Fatal(err)
	}
	_, err = c.Convert(toolChunk(0, ""))
	resourceKind(t, err, "stream_items")
}
func TestStreamResourceAttemptIsolationAndNativeWire(t *testing.T) {
	l := streamLimits(4, 4)
	for i := 0; i < 2; i++ {
		c, _ := NewStreamConverter(ProviderOpenAI, ProviderOpenAI, WithResourceLimits(l))
		p := toolChunk(0, "abcd")
		out, err := c.Convert(p)
		if err != nil || len(out) != 1 || string(out[0]) != string(p) {
			t.Fatalf("passthrough %q %v", out, err)
		}
	}
	// Native passthrough does not retain accumulated body text.
	c, _ := NewStreamConverter(ProviderOpenAI, ProviderOpenAI, WithResourceLimits(l))
	for i := 0; i < 10; i++ {
		if _, err := c.Convert(textChunk(strings.Repeat("x", 20), "")); err != nil {
			t.Fatal(err)
		}
	}
}
func TestStreamResourceSessionPreservesFailure(t *testing.T) {
	c, _ := NewStreamConverter(ProviderOpenAI, ProviderAnthropic, WithResourceLimits(streamLimits(4, 4)))
	s := NewStreamSession(ProviderOpenAI, ProviderAnthropic, c)
	p := textChunk("abcde", "")
	_, err := s.Process(SSEEvent{Data: p, HasData: true, Raw: append(append([]byte("data: "), p...), []byte("\n\n")...)})
	resourceKind(t, err, "accumulated_bytes")
	if s.Outcome().Reason != "resource_limit" {
		t.Fatal(s.Outcome())
	}
	frames, err := s.Finish()
	resourceKind(t, err, "accumulated_bytes")
	if len(frames.Frames) > 0 {
		t.Fatal("unexpected finish")
	}
}

func TestStreamResourceLegacyToolDeltas(t *testing.T) {
	cases := []struct {
		c      StreamConverter
		frames []string
	}{
		{newAnthropicToOpenAIStream(), []string{`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"ab"}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"cd"}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"e"}}`}},
		{newResponsesToOpenAIStream(), []string{`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"ab"}`, `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"cd"}`, `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"e"}`}},
	}
	for _, tc := range cases {
		setStreamLimits(tc.c, streamLimits(100, 4))
		for _, f := range tc.frames[:2] {
			if _, err := tc.c.Convert([]byte(f)); err != nil {
				t.Fatal(err)
			}
		}
		_, err := tc.c.Convert([]byte(tc.frames[2]))
		resourceKind(t, err, "tool_arguments_bytes")
		_, err = tc.c.Finish()
		resourceKind(t, err, "tool_arguments_bytes")
	}
}
func TestStreamResourceLegacyDefaults(t *testing.T) {
	c := newResponsesFromOpenAI()
	p := toolChunk(0, strings.Repeat("x", 800000))
	for i := 0; i < 5; i++ {
		if _, err := c.Convert(p); err != nil {
			t.Fatal(err)
		}
	}
	_, err := c.Convert(p)
	resourceKind(t, err, "tool_arguments_bytes")
	_, err = c.Finish()
	resourceKind(t, err, "tool_arguments_bytes")
}
func TestStreamResourceDepthBeforeDecode(t *testing.T) {
	c := newResponsesFromOpenAI().(*fromOpenAISkeleton)
	l := resource.Defaults()
	l.MaxJSONDepth = 2
	c.setResourceLimits(l)
	_, err := c.Convert([]byte(`{"choices":[{"delta":{"content":"x"}}]}`))
	resourceKind(t, err, "json_depth")
	_, err = c.Finish()
	resourceKind(t, err, "json_depth")
}

// One large native frame may fan out into many semantic events. Its immutable
// storage must stay shared while every exported inspector remains isolated.
func TestStreamResourceFanoutSharesNativeStorage(t *testing.T) {
	encoder := &recordingStreamEncoder{}
	source, _ := implFor(ProviderGemini)
	conv := &typedStreamConverter{from: ProviderGemini, decoder: source.newStreamDecoder(), encoder: encoder, limits: resource.Defaults()}
	payload := []byte(`{"candidates":[{"content":{"parts":[` + strings.TrimSuffix(strings.Repeat(`{"text":"x"},`, 100), ",") + `]}}],"opaque":"` + strings.Repeat("p", 64<<10) + `"}`)
	if _, err := conv.Convert(payload); err != nil {
		t.Fatal(err)
	}
	if len(encoder.events) != 101 {
		t.Fatalf("fanout = %d", len(encoder.events))
	}
	first := encoder.events[0].Native
	for _, event := range encoder.events[1:] {
		if event.Native == first {
			t.Fatal("event envelope aliases")
		}
		if &event.Native.native.Original[0] != &first.native.Original[0] {
			t.Fatal("native frame duplicated per semantic event")
		}
	}
	visible := first.Native()
	visible.Original[0] = '!'
	visible.Fragments[0].Raw[0] = '!'
	if first.Original()[0] != '{' || encoder.events[1].Native.Original()[0] != '{' {
		t.Fatal("exported snapshot aliases private storage")
	}
	payload[0] = '!'
	if first.Original()[0] != '{' {
		t.Fatal("capture aliases input")
	}
}
