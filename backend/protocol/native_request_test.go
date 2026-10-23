package protocol

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeEnvelopePreservesAndIsolatesTargets(t *testing.T) {
	body := []byte(`{"model":"group","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello","extra":{"n":9007199254740993}}]},{"type":"vendor_item","id":"secret-item"}],"tools":[{"type":"vendor_tool","payload":{"n":9007199254740993}}],"vendor":{"nested":[9007199254740993,null]}}`)
	envelope, err := CaptureRequest(ProviderOpenAIResponses, body)
	if err != nil {
		t.Fatal(err)
	}
	body[0] = ' '
	n := envelope.Native()
	var blocks, items, tools int
	for _, f := range n.Fragments {
		switch f.Owner.Kind {
		case OwnerItem:
			if f.Position != items || f.Owner.Item != items {
				t.Fatalf("item owner: %+v", f)
			}
			items++
		case OwnerBlock:
			if f.Owner.Item != 0 || f.Position != 0 {
				t.Fatalf("block owner: %+v", f)
			}
			blocks++
		case OwnerTool:
			tools++
		}
	}
	if items != 2 || blocks != 1 || tools != 1 {
		t.Fatalf("fragments: %+v", n)
	}
	n.Original[0] = '!'
	n.Fragments[0].Raw[0] = '!'
	for _, model := range []string{"upstream-a", "upstream-b"} {
		out, stats, err := envelope.PrepareTarget(ProviderOpenAIResponses, UpstreamRequest{Model: model})
		if err != nil {
			t.Fatal(err)
		}
		if !stats.Compatibility.Checked || !stats.Compatibility.Compatible {
			t.Fatalf("same protocol: %+v", stats)
		}
		if !bytes.Contains(out, []byte(`9007199254740993`)) || !bytes.Contains(out, []byte(`"model":"`+model+`"`)) || !bytes.Contains(out, []byte(`"type":"vendor_item"`)) {
			t.Fatalf("raw not preserved: %s", out)
		}
		out[0] = '!'
	}
	if !bytes.Contains(envelope.Original(), []byte(`"model":"group"`)) || envelope.Original()[0] != '{' {
		t.Fatal("source mutated")
	}
	serialized, _ := json.Marshal(envelope)
	if string(serialized) != "{}" {
		t.Fatalf("envelope leak: %s", serialized)
	}
	serialized, _ = json.Marshal(envelope.Native())
	if strings.Contains(string(serialized), "secret-item") || strings.Contains(string(serialized), "9007199254740993") {
		t.Fatalf("native leak: %s", serialized)
	}
}

func TestNativeCompatibilityDiagnosticsDoNotReject(t *testing.T) {
	cases := []struct {
		name         string
		from, to     Provider
		body         string
		continuation bool
	}{
		{"unknown field", ProviderOpenAI, ProviderAnthropic, `{"model":"m","messages":[],"secret-user-field":"secret-payload"}`, false},
		{"hosted downgrade", ProviderAnthropic, ProviderOpenAI, `{"model":"m","messages":[],"tools":[{"type":"web_search_20250305","name":"web_search"}]}`, false},
		{"native tool", ProviderOpenAIResponses, ProviderAnthropic, `{"model":"m","input":"hi","tools":[{"type":"computer_use_preview"}]}`, false},
		{"native item", ProviderOpenAIResponses, ProviderOpenAI, `{"model":"m","input":[{"type":"computer_call","id":"secret-payload"}]}`, false},
		{"encrypted", ProviderOpenAIResponses, ProviderOpenAIResponses, `{"model":"m","input":[{"type":"reasoning","encrypted_content":"secret-payload"}]}`, true},
		{"signature", ProviderAnthropic, ProviderAnthropic, `{"model":"m","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"x","signature":"secret-payload"}]}]}`, true},
		{"continuation", ProviderOpenAIResponses, ProviderOpenAI, `{"model":"m","previous_response_id":"secret-payload","input":"hi"}`, true},
		{"gemini signature", ProviderGemini, ProviderGemini, `{"contents":[{"role":"model","parts":[{"text":"x","thoughtSignature":"secret-payload"}]}]}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, stats, err := ComposeUpstreamRequest(tc.from, tc.to, []byte(tc.body), UpstreamRequest{})
			if err != nil || len(out) == 0 {
				t.Fatalf("conversion changed: %s %v", out, err)
			}
			if !stats.Compatibility.Checked || stats.Compatibility.Compatible || len(stats.Compatibility.Issues) == 0 {
				t.Fatalf("missing report: %+v", stats)
			}
			if tc.from != tc.to && bytes.Contains(out, []byte("secret-payload")) {
				t.Fatalf("native leaked cross-protocol: %s", out)
			}
			if tc.from == tc.to && !bytes.Contains(out, []byte("secret-payload")) {
				t.Fatalf("same-protocol native continuation lost: %s", out)
			}
			if tc.continuation {
				found := false
				for _, issue := range stats.Compatibility.Issues {
					found = found || issue.Reason == ReasonUnverifiedContinuation
				}
				if !found {
					t.Fatalf("missing continuation diagnostic: %+v", stats.Compatibility)
				}
			}
			report, _ := json.Marshal(stats.Compatibility)
			if strings.Contains(string(report), "secret") {
				t.Fatalf("diagnostic leaked: %s", report)
			}
		})
	}
}
func TestNativeSupportedConversions(t *testing.T) {
	inputs := map[Provider]string{
		ProviderOpenAI:          `{"model":"m","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`,
		ProviderOpenAIResponses: `{"model":"m","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`,
		ProviderAnthropic:       `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`,
		ProviderGemini:          `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}}]}]}`,
	}
	for from, body := range inputs {
		for to := range inputs {
			if _, stats, err := ComposeUpstreamRequest(from, to, []byte(body), UpstreamRequest{}); err != nil || !stats.Compatibility.Compatible {
				t.Errorf("%s -> %s: %+v %v", from, to, stats, err)
			}
		}
	}
}
func TestNativeThinkingRawPrecedence(t *testing.T) {
	for _, extra := range []string{`"thinking":null`, `"thinking":{"type":"disabled"}`, `"reasoning_effort":"none"`, `"thinking":false`} {
		body := []byte(`{"model":"m","messages":[],"n":9007199254740993,` + extra + `}`)
		out, _, err := ComposeUpstreamRequest(ProviderOpenAI, ProviderOpenAI, body, UpstreamRequest{Model: "target", Thinking: &ThinkingConfig{Type: "enabled"}})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(out, []byte("9007199254740993")) {
			t.Fatalf("precision lost: %s", out)
		}
		if strings.Contains(extra, "null") {
			if !bytes.Contains(out, []byte(`"thinking":{"type":"enabled"}`)) {
				t.Fatalf("null precedence: %s", out)
			}
		} else if !bytes.Contains(out, []byte(extra)) {
			t.Fatalf("explicit precedence: %s", out)
		}
	}
}

func TestNativeMalformedInputKeepsValidationError(t *testing.T) {
	_, stats, err := ComposeUpstreamRequest(ProviderOpenAI, ProviderAnthropic, []byte(`{"model":"m","messages":"invalid","vendor":true}`), UpstreamRequest{})
	if err == nil || !strings.Contains(err.Error(), "解析") {
		t.Fatalf("want malformed request error: %v", err)
	}
	if stats.Compatibility.Checked {
		t.Fatalf("malformed request reported as compatible input: %+v", stats)
	}
}

func TestNativeSupportedHostedBridges(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"tools":[{"type":"web_search_20250305","name":"web_search"}]}`)
	for _, to := range []Provider{ProviderAnthropic, ProviderGemini, ProviderOpenAIResponses} {
		_, report, err := ComposeUpstreamRequest(ProviderAnthropic, to, body, UpstreamRequest{})
		if err != nil || !report.Compatibility.Compatible {
			t.Fatalf("supported search bridge %s: %+v %v", to, report, err)
		}
	}
}

func TestNativeCompatibilityBoundaries(t *testing.T) {
	bridgeBody := `{"model":"m","messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":"` + EncodeReasoningSignature("original reasoning") + `"}]}]}`
	cases := []struct {
		name     string
		from, to Provider
		body     string
		want     []CompatibilityReason
	}{
		{name: "redacted bridge", from: ProviderAnthropic, to: ProviderOpenAI, body: bridgeBody},
		{name: "native redacted ciphertext", from: ProviderAnthropic, to: ProviderOpenAI, body: `{"model":"m","messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":"secret-ciphertext"}]}]}`, want: []CompatibilityReason{ReasonUnverifiedContinuation}},
		{name: "responses image not mapped", from: ProviderOpenAIResponses, to: ProviderOpenAI, body: `{"model":"m","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"https://example.test/image.png"}]}]}`, want: []CompatibilityReason{ReasonUnsupportedBlock}},
		{name: "anthropic custom tool stripped", from: ProviderAnthropic, to: ProviderOpenAI, body: `{"model":"m","messages":[],"tools":[{"type":"custom","name":"custom_tool","input_schema":{"type":"object"}}]}`, want: []CompatibilityReason{ReasonUnsupportedTool}},
		{name: "gemini mixed hosted and unknown to chat", from: ProviderGemini, to: ProviderOpenAI, body: `{"contents":[],"tools":[{"googleSearch":{},"codeExecution":{}}]}`, want: []CompatibilityReason{ReasonHostedToolDowngrade, ReasonUnsupportedTool}},
		{name: "gemini mixed hosted and unknown to responses", from: ProviderGemini, to: ProviderOpenAIResponses, body: `{"contents":[],"tools":[{"googleSearch":{},"codeExecution":{}}]}`, want: []CompatibilityReason{ReasonUnsupportedTool}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			converted, stats, err := ComposeUpstreamRequest(tc.from, tc.to, body, UpstreamRequest{})
			if err != nil {
				t.Fatalf("conversion error: %v", err)
			}
			original, _, err := composeUpstream(tc.from, tc.to, body, UpstreamRequest{})
			if err != nil || !bytes.Equal(converted, original) {
				t.Fatalf("conversion changed: %s vs %s, %v", converted, original, err)
			}
			if stats.Compatibility.Scope != "request_native_v1" {
				t.Fatalf("missing report scope: %+v", stats.Compatibility)
			}
			if stats.Compatibility.Compatible != (len(tc.want) == 0) {
				t.Fatalf("incorrect compatibility: %+v", stats.Compatibility)
			}
			for _, reason := range tc.want {
				found := false
				for _, issue := range stats.Compatibility.Issues {
					if issue.Reason == reason {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing %s: %+v", reason, stats)
				}
			}
		})
	}
}
