package protocol

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestResponseEnvelopeNativeSnapshots(t *testing.T) {
	for _, p := range []Provider{ProviderOpenAI, ProviderAnthropic, ProviderOpenAIResponses, ProviderGemini} {
		t.Run(string(p), func(t *testing.T) {
			body := []byte(" {\"vendor\":900719925474099312345,\"opaque\":{\"b\":2,\"a\":1}} \n")
			original := bytes.Clone(body)
			e, err := CaptureResponse(p, body)
			if err != nil {
				t.Fatal(err)
			}
			body[0] = 'x'
			out, r, err := e.Render(p)
			if err != nil || !r.Compatible || !bytes.Equal(out, original) {
				t.Fatalf("native render: %q %+v %v", out, r, err)
			}
			out[0] = 'x'
			n := e.Native()
			n.Original[0] = 'x'
			n.Fragments[0].Raw[0] = 'x'
			n.Fragments[0].Owner.Field = "changed"
			if !bytes.Equal(e.Original(), original) || e.Native().Fragments[0].Owner.Field == "changed" {
				t.Fatal("snapshot alias")
			}
			encoded, _ := json.Marshal(e)
			if string(encoded) != "{}" {
				t.Fatalf("envelope serialized: %s", encoded)
			}
			encoded, _ = json.Marshal(e.Native())
			if bytes.Contains(encoded, []byte("900719")) || bytes.Contains(encoded, []byte("opaque")) {
				t.Fatalf("raw leaked: %s", encoded)
			}
		})
	}
}

func TestResponseOpaquePreservesSameProtocol(t *testing.T) {
	for _, body := range []string{"not-json", "null", "[]", "", "17"} {
		out, r, err := ConvertResponseWithReport(ProviderOpenAI, ProviderOpenAI, []byte(body))
		if err != nil || string(out) != body || !r.Compatible {
			t.Fatalf("opaque native %q: %q %+v %v", body, out, r, err)
		}
		e, _ := CaptureResponse(ProviderOpenAI, []byte(body))
		r = e.Compatibility(ProviderAnthropic)
		if r.Compatible || len(r.Issues) != 1 || r.Issues[0].Reason != ReasonUninspectedResponse {
			t.Fatalf("uninspected: %+v", r)
		}
	}
	if _, _, err := ConvertResponseWithReport(Provider("invalid"), ProviderOpenAI, nil); err == nil {
		t.Fatal("invalid source accepted")
	}
	if _, _, err := ConvertResponseWithReport(ProviderOpenAI, Provider("invalid"), nil); err == nil {
		t.Fatal("invalid target accepted")
	}
}

func TestResponseConversionRetainsExistingWire(t *testing.T) {
	fixtures := map[Provider]string{
		ProviderOpenAI:          `{"id":"chat-1","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hello","reasoning_content":"think","tool_calls":[{"id":"call-1","type":"function","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5},"vendor":{"secret":1}}`,
		ProviderAnthropic:       `{"id":"msg-1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"hello"},{"type":"thinking","thinking":"think","signature":"native-secret"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":3}}`,
		ProviderOpenAIResponses: `{"id":"resp-1","model":"m","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello","annotations":[]}]},{"type":"reasoning","summary":[{"type":"summary_text","text":"think"}],"encrypted_content":"secret"}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`,
		ProviderGemini:          `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"},{"text":"think","thought":true,"thoughtSignature":"secret"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3,"totalTokenCount":5}}`,
	}
	for from, body := range fixtures {
		for to := range fixtures {
			if from == to {
				continue
			}
			t.Run(string(from)+"_"+string(to), func(t *testing.T) {
				canonical, err := parseResponseToOpenAI(from, []byte(body))
				if err != nil {
					t.Fatal(err)
				}
				old, err := renderResponseFromOpenAI(to, canonical)
				if err != nil {
					t.Fatal(err)
				}
				out, r, err := ConvertResponseWithReport(from, to, []byte(body))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(old, out) {
					t.Fatalf("changed wire\nold %s\nnew %s", old, out)
				}
				if r.Compatible || r.Scope != "response_native_v1" {
					t.Fatalf("missing extension report: %+v", r)
				}
			})
		}
	}
}

func TestResponseDiagnosticPathsStaticDeduplicated(t *testing.T) {
	body := []byte(`{"secret_dynamic_top":1,"output":[{"type":"vendor_secret","secret_a":1},{"type":"vendor_other","secret_b":2},{"type":"message","content":[{"type":"output_text","text":"ok","annotations":[{"secret_annotation":1}]}] }]}`)
	e, _ := CaptureResponse(ProviderOpenAIResponses, body)
	r := e.Compatibility(ProviderAnthropic)
	if r.Compatible {
		t.Fatal("unknown fields not diagnosed")
	}
	seen := map[ConversionIssue]bool{}
	for _, issue := range r.Issues {
		if seen[issue] {
			t.Fatal("duplicate issue")
		}
		seen[issue] = true
		if strings.Contains(issue.Path, "secret") || strings.Contains(issue.Path, "[0]") {
			t.Fatalf("dynamic path: %+v", issue)
		}
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "secret") {
		t.Fatalf("diagnostic leak: %s", b)
	}
}

func TestResponseNativeFragmentsAndSSESnapshot(t *testing.T) {
	raw := []byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"first"},{"type":"vendor","n":9007199254740993}]}]}`)
	e, _ := CaptureResponse(ProviderOpenAIResponses, raw)
	found := false
	for _, f := range e.Native().Fragments {
		if f.Owner.Kind == OwnerBlock && f.Owner.Item == 0 && f.Position == 1 {
			found = true
			if !bytes.Contains(f.Raw, []byte("9007199254740993")) {
				t.Fatal("fragment precision lost")
			}
		}
	}
	if !found {
		t.Fatal("missing block owner/position")
	}
	retry := uint64(300)
	ev := SSEEvent{Event: "response.output_item.added", ID: "private-id", Retry: &retry, Data: []byte(`{"type":"response.output_item.added","item":{"type":"vendor","secret":1}}`), Raw: []byte(": ping\r\nid: private-id\r\nevent: response.output_item.added\r\nretry: 300\r\ndata: raw\r\n\r\n"), HasData: true}
	snap, _ := CaptureResponseEvent(ProviderOpenAIResponses, ev)
	ev.Raw[0] = 'x'
	ev.Data[0] = 'x'
	retry = 900
	w := snap.Wire()
	if w.Raw[0] != ':' || *w.Retry != 300 || w.Data[0] != '{' {
		t.Fatal("input alias")
	}
	w.Raw[0] = 'x'
	*w.Retry = 2
	w.Data[0] = 'x'
	if snap.Wire().Raw[0] != ':' || *snap.Wire().Retry != 300 {
		t.Fatal("output alias")
	}
	if !snap.Compatibility(ProviderOpenAIResponses).Compatible {
		t.Fatal("same protocol report")
	}
	report := snap.Compatibility(ProviderAnthropic)
	if report.Compatible || report.Scope != "stream_native_v1" {
		t.Fatalf("event report %+v", report)
	}
	for _, i := range report.Issues {
		if strings.Contains(i.Path, "private") || strings.Contains(i.Path, "secret") {
			t.Fatal("event diagnostic leak")
		}
	}
	b, _ := json.Marshal(snap)
	if string(b) != "{}" {
		t.Fatalf("event serialized: %s", b)
	}
}

func TestResponseKnownTextAndThinkingNotDeclaredLost(t *testing.T) {
	fixtures := map[Provider]string{
		ProviderOpenAI:          `{"id":"x","model":"m","choices":[{"message":{"role":"assistant","content":"text","reasoning_content":"think"},"finish_reason":"stop"}]}`,
		ProviderAnthropic:       `{"content":[{"type":"text","text":"text"},{"type":"thinking","thinking":"think"}]}`,
		ProviderOpenAIResponses: `{"output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"think"}]},{"type":"message","content":[{"type":"output_text","text":"text","annotations":[]}]}]}`,
		ProviderGemini:          `{"candidates":[{"content":{"role":"model","parts":[{"text":"text"},{"text":"think","thought":true}]}}]}`,
	}
	for p, body := range fixtures {
		e, _ := CaptureResponse(p, []byte(body))
		target := ProviderOpenAI
		if p == target {
			target = ProviderAnthropic
		}
		r := e.Compatibility(target)
		if !r.Compatible {
			t.Fatalf("%s plain mapped content reported lost: %+v", p, r)
		}
	}
}
