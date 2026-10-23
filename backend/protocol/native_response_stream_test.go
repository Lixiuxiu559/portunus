package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestNativeStreamSemanticEventPreservesSourceSnapshot(t *testing.T) {
	raw := []byte(`{"choices":[{"index":0,"delta":{"content":"hello","future_native":{"secret":"kept"}}}]}`)
	encoder := &recordingStreamEncoder{}
	conv := &typedStreamConverter{from: ProviderOpenAI, decoder: &openAIStreamDecoder{}, encoder: encoder}
	if _, err := conv.Convert(raw); err != nil {
		t.Fatal(err)
	}
	if len(encoder.events) != 1 || encoder.events[0].Native == nil {
		t.Fatal("missing native semantic event")
	}
	native := encoder.events[0].Native
	if !bytes.Equal(native.Original(), raw) || native.Native().Source.Protocol != ProviderOpenAI {
		t.Fatal("source snapshot lost")
	}
	if len(native.Native().Fragments) == 0 {
		t.Fatal("native owner fragments missing")
	}
	snapshot := native.Native()
	snapshot.Original[0] = '!'
	snapshot.Fragments[0].Raw[0] = '!'
	raw[0] = '!'
	if native.Original()[0] != '{' || native.Native().Fragments[0].Raw[0] == '!' {
		t.Fatal("snapshot aliases caller memory")
	}
	serialized, err := json.Marshal(encoder.events[0])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(serialized, []byte("secret")) || bytes.Contains(serialized, []byte("Native")) {
		t.Fatalf("native leaked to JSON: %s", serialized)
	}
	if _, err := conv.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeStreamSessionKeepsFullSSEWithoutPayloadRewrite(t *testing.T) {
	retry := uint64(1250)
	payload := []byte(`{"delta":"hello","future_field":900719925474099312345}`)
	raw := []byte("event: response.output_text.delta\r\nid: evt1\r\nretry: 1250\r\ndata: " + string(payload) + "\r\n\r\n")
	encoder := &recordingStreamEncoder{}
	source, _ := implFor(ProviderOpenAIResponses)
	conv := &typedStreamConverter{from: ProviderOpenAIResponses, decoder: source.newStreamDecoder(), encoder: encoder}
	session := NewStreamSession(ProviderOpenAIResponses, ProviderOpenAI, conv)
	event := SSEEvent{Event: "response.output_text.delta", ID: "evt1", Retry: &retry, Data: payload, Raw: raw, HasData: true}
	if _, err := session.Process(event); err != nil {
		t.Fatal(err)
	}
	if len(encoder.events) != 2 || encoder.events[1].Choices[0].Delta.Content != "hello" {
		t.Fatalf("SSE discriminator not consumed: %#v", encoder.events)
	}
	native := encoder.events[1].Native
	if encoder.events[0].Native == native {
		t.Fatal("semantic events share mutable envelope")
	}
	if !bytes.Equal(native.Original(), payload) || !reflect.DeepEqual(native.Wire(), &event) {
		t.Fatal("original SSE changed by type injection")
	}
	event.Data[0] = '!'
	event.Raw[0] = '!'
	retry = 1
	wire := native.Wire()
	if wire.Data[0] != '{' || wire.Raw[0] != 'e' || *wire.Retry != 1250 {
		t.Fatal("wire aliases caller")
	}
	wire.Data[0] = '!'
	wire.Raw[0] = '!'
	*wire.Retry = 2
	if native.Wire().Data[0] != '{' || native.Wire().Raw[0] != 'e' || *native.Wire().Retry != 1250 {
		t.Fatal("wire inspector aliases snapshot")
	}
	current := session.NativeEvent()
	currentWire := current.Wire()
	currentWire.Raw[0] = '!'
	if session.NativeEvent().Wire().Raw[0] != 'e' {
		t.Fatal("session current snapshot mutable")
	}
}

func TestNativeStreamSessionDiagnosticsAndCurrentEventIsolation(t *testing.T) {
	conv, _ := NewStreamConverter(ProviderAnthropic, ProviderOpenAI)
	session := NewStreamSession(ProviderAnthropic, ProviderOpenAI, conv)
	for i := 0; i < 2; i++ {
		payload := []byte(`{"type":"future_native","future_field":{"sensitive":"hidden"}}`)
		if _, err := session.Process(SSEEvent{Data: payload, HasData: true, Raw: []byte("data: " + string(payload) + "\n\n")}); err != nil {
			t.Fatal(err)
		}
	}
	before := session.Compatibility()
	if before.Scope != "stream_native_v1" || before.Compatible || len(before.Issues) == 0 {
		t.Fatalf("missing stream diagnostics: %#v", before)
	}
	seen := map[ConversionIssue]bool{}
	for _, issue := range before.Issues {
		if seen[issue] {
			t.Fatal("duplicate issue")
		}
		seen[issue] = true
	}
	reportJSON, _ := json.Marshal(before)
	if bytes.Contains(reportJSON, []byte("sensitive")) || bytes.Contains(reportJSON, []byte("hidden")) {
		t.Fatal("diagnostics leak source values")
	}
	before.Issues[0].Path = "mutated"
	if session.Compatibility().Issues[0].Path == "mutated" {
		t.Fatal("report aliases state")
	}
	comment := SSEEvent{Raw: []byte(": heartbeat\n\n")}
	if _, err := session.Process(comment); err != nil {
		t.Fatal(err)
	}
	if got := session.NativeEvent().Wire(); !reflect.DeepEqual(got, &comment) {
		t.Fatalf("session retained old source: %#v", got)
	}
	anotherConv, _ := NewStreamConverter(ProviderAnthropic, ProviderOpenAI)
	another := NewStreamSession(ProviderAnthropic, ProviderOpenAI, anotherConv)
	if another.NativeEvent() != nil || !another.Compatibility().Compatible || len(another.Compatibility().Issues) != 0 {
		t.Fatal("attempt state shared")
	}
	if _, err := session.Finish(); !errors.Is(err, ErrStreamIncomplete) {
		t.Fatalf("missing terminal incorrectly succeeds: %v", err)
	}
	saved := session.NativeEvent().Wire()
	if _, err := session.Process(SSEEvent{Data: []byte(`{"type":"message_stop"}`), HasData: true}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved, session.NativeEvent().Wire()) {
		t.Fatal("finished session captured unprocessed event")
	}
}

func TestNativeStreamSameProtocolRawAndFailureSafety(t *testing.T) {
	const wire = ": comment\r\nevent: future_native\r\nid: n1\r\nretry: 9\r\ndata: opaque\r\n\r\n"
	event, err := NewSSEReader(strings.NewReader(wire), 0).ReadEvent()
	if err != nil {
		t.Fatal(err)
	}
	conv, _ := NewStreamConverter(ProviderAnthropic, ProviderAnthropic)
	session := NewStreamSession(ProviderAnthropic, ProviderAnthropic, conv)
	batch, err := session.Process(event)
	if err != nil || !batch.Meaningful || !batch.ReplayUnsafe || string(bytes.Join(batch.Frames, nil)) != wire {
		t.Fatalf("native raw/safety changed: %#v %v", batch, err)
	}
	if !session.Compatibility().Compatible || session.NativeEvent() == nil {
		t.Fatal("same-protocol diagnostics or native missing")
	}
	if _, err := session.Finish(); !errors.Is(err, ErrStreamIncomplete) {
		t.Fatalf("opaque stream acquired terminal: %v", err)
	}
	conv, _ = NewStreamConverter(ProviderAnthropic, ProviderAnthropic)
	session = NewStreamSession(ProviderAnthropic, ProviderAnthropic, conv)
	failure := SSEEvent{Event: "error", HasData: true, Data: []byte("opaque failure"), Raw: []byte("event: error\ndata: opaque failure\n\n")}
	batch, err = session.Process(failure)
	var upstream *StreamFailureError
	if !errors.As(err, &upstream) || !batch.Terminal || !bytes.Equal(bytes.Join(batch.Frames, nil), failure.Raw) || !bytes.Equal(session.NativeEvent().Original(), failure.Data) {
		t.Fatalf("native error handling changed: %#v %v", batch, err)
	}
}

func TestNativeStreamSyntheticEndDoesNotClaimLastEventOwnership(t *testing.T) {
	encoder := &recordingStreamEncoder{}
	source, _ := implFor(ProviderAnthropic)
	conv := &typedStreamConverter{from: ProviderAnthropic, decoder: source.newStreamDecoder(), encoder: encoder}
	if _, err := conv.Convert([]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`)); err != nil {
		t.Fatal(err)
	}
	before := len(encoder.events)
	if before == 0 || encoder.events[before-1].Native == nil {
		t.Fatal("source delta missing native")
	}
	if _, err := conv.Finish(); err != nil {
		t.Fatal(err)
	}
	if len(encoder.events) == before {
		t.Fatal("expected synthetic end")
	}
	for _, event := range encoder.events[before:] {
		if event.Native != nil {
			t.Fatal("synthetic end claimed last source snapshot")
		}
	}
}
