package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	OwnerResponse             NativeOwner         = "response"
	OwnerEvent                NativeOwner         = "event"
	ReasonResponseExtension   CompatibilityReason = "response_native_extension"
	ReasonUninspectedResponse CompatibilityReason = "response_uninspected"
	ReasonSSEMetadata         CompatibilityReason = "sse_metadata_not_mapped"
)

// ResponseEnvelope holds a source snapshot, never an instruction to splice native
// extensions into another protocol. Private fields keep source data out of JSON logs.
type ResponseEnvelope struct {
	native NativeEnvelope
	wire   *SSEEvent
}

func CaptureResponse(from Provider, body []byte) (*ResponseEnvelope, error) {
	if !from.Valid() {
		return nil, fmt.Errorf("未知的源协议: %s", from)
	}
	e := &ResponseEnvelope{native: NativeEnvelope{Source: SourceDescriptor{Protocol: from, Trust: SourceUnverified}, Original: bytes.Clone(body)}}
	e.captureFragments()
	return e, nil
}

func CaptureResponseEvent(from Provider, ev SSEEvent) (*ResponseEnvelope, error) {
	e, err := CaptureResponse(from, ev.Data)
	if err != nil {
		return nil, err
	}
	e.wire = cloneResponseWire(&ev)
	return e, nil
}

func cloneResponseWire(ev *SSEEvent) *SSEEvent {
	if ev == nil {
		return nil
	}
	out := *ev
	out.Data = bytes.Clone(ev.Data)
	out.Raw = bytes.Clone(ev.Raw)
	if ev.Retry != nil {
		v := *ev.Retry
		out.Retry = &v
	}
	return &out
}
func (e *ResponseEnvelope) clone() *ResponseEnvelope {
	if e == nil {
		return nil
	}
	return &ResponseEnvelope{native: e.Native(), wire: e.Wire()}
}

func (e *ResponseEnvelope) Wire() *SSEEvent  { return cloneResponseWire(e.wire) }
func (e *ResponseEnvelope) Original() []byte { return bytes.Clone(e.native.Original) }
func (e *ResponseEnvelope) Native() NativeEnvelope {
	n := e.native
	n.Original = bytes.Clone(n.Original)
	n.Fragments = append([]NativeFragment(nil), n.Fragments...)
	for i := range n.Fragments {
		n.Fragments[i].Raw = bytes.Clone(n.Fragments[i].Raw)
	}
	return n
}

func (e *ResponseEnvelope) captureFragments() {
	add := func(owner OwnerRef, pos int, raw json.RawMessage) {
		e.native.Fragments = append(e.native.Fragments, NativeFragment{Owner: owner, Position: pos, Kind: NativeOpaque, Raw: bytes.Clone(raw)})
	}
	root := rawObject(e.native.Original)
	if root == nil {
		add(OwnerRef{Kind: OwnerResponse, Item: -1}, 0, e.native.Original)
		return
	}
	keys := make([]string, 0, len(root))
	for k := range root {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		add(OwnerRef{Kind: OwnerResponse, Item: -1, Field: k}, i, root[k])
	}
	var visit func(map[string]json.RawMessage, string, int)
	visit = func(obj map[string]json.RawMessage, field string, parent int) {
		for i, item := range rawArray(obj[field]) {
			kind := OwnerItem
			if field == "content" || field == "parts" || field == "summary" {
				kind = OwnerBlock
			}
			add(OwnerRef{Kind: kind, Item: parent, Field: field}, i, item)
			m := rawObject(item)
			if field == "choices" {
				for _, f := range []string{"message", "delta"} {
					if rawPresent(m[f]) {
						add(OwnerRef{Kind: OwnerItem, Item: i, Field: f}, i, m[f])
					}
				}
			}
			if field == "output" {
				visit(m, "content", i)
				visit(m, "summary", i)
			}
			if field == "candidates" {
				visit(rawObject(m["content"]), "parts", i)
			}
		}
	}
	for _, field := range []string{"choices", "output", "content", "candidates"} {
		visit(root, field, -1)
	}
	// Event wrappers own a response/message or one item/block, independent of the
	// rewritten target sequence.
	for _, field := range []string{"response", "message", "item", "content_block", "part", "delta"} {
		if rawPresent(root[field]) {
			add(OwnerRef{Kind: OwnerEvent, Item: -1, Field: field}, 0, root[field])
		}
	}
	for _, field := range []string{"response", "message"} {
		obj := rawObject(root[field])
		for _, array := range []string{"output", "content", "choices", "candidates"} {
			visit(obj, array, -1)
		}
	}
}

// responseShape inspects only explicitly known structural levels. User-defined
// tool arguments and opaque values are never recursively interpreted or logged.
type responseShape struct {
	fields     map[string]bool
	children   map[string]*responseShape
	arrays     map[string]*responseShape
	types      map[string]bool
	extensions map[string]bool
}

func responseFields(names string) *responseShape {
	s := &responseShape{fields: map[string]bool{}, children: map[string]*responseShape{}, arrays: map[string]*responseShape{}, extensions: map[string]bool{}}
	for _, k := range strings.Fields(names) {
		s.fields[k] = true
	}
	return s
}
func (s *responseShape) child(k string, c *responseShape) *responseShape {
	s.fields[k] = true
	s.children[k] = c
	return s
}
func (s *responseShape) array(k string, c *responseShape) *responseShape {
	s.fields[k] = true
	s.arrays[k] = c
	return s
}
func (s *responseShape) extend(names string) *responseShape {
	for _, k := range strings.Fields(names) {
		s.fields[k] = true
		s.extensions[k] = true
	}
	return s
}
func (s *responseShape) kinds(names string) *responseShape {
	s.types = map[string]bool{}
	for _, k := range strings.Fields(names) {
		s.types[k] = true
	}
	return s
}

func responseSchema(p Provider, stream bool) *responseShape {
	var root *responseShape
	switch p {
	case ProviderOpenAI:
		fn := responseFields("name arguments")
		tool := responseFields("id index type").child("function", fn).kinds("function")
		msg := responseFields("role content reasoning_content").array("tool_calls", tool).extend("refusal annotations audio")
		// Grounding has a useful canonical projection but target encoders do not all
		// retain it. Report this native carrier conservatively rather than promise losslessness.
		msg.extend("grounding_metadata")
		msg.array("content", responseFields("type text").kinds("text"))
		choice := responseFields("index finish_reason").child("message", msg).child("delta", msg)
		root = responseFields("id object model created usage").array("choices", choice)
		if !stream {
			root.extend("created")
		}
	case ProviderAnthropic:
		block := responseFields("type text thinking id name input").kinds("text thinking tool_use").extend("signature data citations")
		root = responseFields("id type role model stop_reason usage").array("content", block).extend("stop_sequence")
		if stream {
			delta := responseFields("type text thinking partial_json stop_reason stop_sequence").kinds("text_delta thinking_delta input_json_delta").extend("signature")
			root = responseFields("type index usage").child("message", root).child("content_block", block).child("delta", delta)
			root.kinds("message_start content_block_start content_block_delta content_block_stop message_delta message_stop ping error")
		}
	case ProviderOpenAIResponses:
		part := responseFields("type text").kinds("output_text summary_text reasoning_text").extend("annotations")
		item := responseFields("type role call_id name arguments").extend("id status").array("content", part).array("summary", part).kinds("message function_call reasoning").extend("encrypted_content")
		root = responseFields("id object model status usage error incomplete_details").array("output", item)
		if stream {
			root = responseFields("type delta output_index item_id content_index summary_index text arguments sequence_number").child("response", root).child("item", item).child("part", part)
			root.kinds("response.created response.in_progress response.output_text.delta response.reasoning_summary_text.delta response.reasoning_text.delta response.output_item.added response.function_call_arguments.delta response.completed response.incomplete response.failed response.output_item.done response.output_text.done response.content_part.added response.content_part.done response.reasoning_summary_part.added response.reasoning_summary_part.done response.reasoning_summary_text.done response.reasoning_text.done response.function_call_arguments.done")
		}
	case ProviderGemini:
		part := responseFields("text thought").child("functionCall", responseFields("name args")).extend("thoughtSignature inlineData functionResponse executableCode codeExecutionResult fileData")
		content := responseFields("role").array("parts", part)
		candidate := responseFields("index finishReason").child("content", content).extend("groundingMetadata safetyRatings citationMetadata avgLogprobs logprobsResult")
		root = responseFields("usageMetadata").array("candidates", candidate).extend("promptFeedback modelVersion responseId")
	}
	return root
}

// Compatibility is deliberately diagnostic: unchanged canonical conversion remains
// authoritative. A clean report covers these inspected levels, not full semantic equivalence.
func (e *ResponseEnvelope) Compatibility(to Provider) ConversionReport {
	scope := "response_native_v1"
	if e.wire != nil {
		scope = "stream_native_v1"
	}
	report := ConversionReport{Scope: scope, Checked: true, Compatible: true}
	add := func(reason CompatibilityReason, path string) {
		for _, issue := range report.Issues {
			if issue.Reason == reason && issue.Path == path {
				return
			}
		}
		report.Compatible = false
		report.Issues = append(report.Issues, ConversionIssue{Reason: reason, Path: path})
	}
	if !to.Valid() {
		add(ReasonUninspectedResponse, "response")
		return report
	}
	if e.native.Source.Protocol == to {
		return report
	}
	if e.wire != nil {
		if e.wire.ID != "" {
			add(ReasonSSEMetadata, "sse.id")
		}
		if e.wire.Retry != nil {
			add(ReasonSSEMetadata, "sse.retry")
		}
		if !e.wire.HasData {
			if e.wire.Event != "" {
				add(ReasonSSEMetadata, "sse.event")
			}
			return report
		}
		if bytes.Equal(bytes.TrimSpace(e.wire.Data), []byte("[DONE]")) {
			return report
		}
	}
	root := rawObject(e.native.Original)
	if root == nil {
		add(ReasonUninspectedResponse, "response")
		return report
	}
	var inspect func(map[string]json.RawMessage, *responseShape, string)
	inspect = func(m map[string]json.RawMessage, s *responseShape, path string) {
		if s == nil {
			return
		}
		if m == nil {
			add(ReasonUninspectedResponse, path)
			return
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if s.types != nil && !s.types[rawString(m["type"])] {
			add(ReasonResponseExtension, path+".type")
		}
		for _, k := range keys {
			raw := m[k]
			if !s.fields[k] {
				add(ReasonResponseExtension, path+".*")
				continue
			}
			if s.extensions[k] && rawPresent(raw) {
				// Empty annotation arrays are mandatory formatting, not a dropped annotation.
				if k != "annotations" || !bytes.Equal(bytes.TrimSpace(raw), []byte("[]")) {
					add(ReasonResponseExtension, path+"."+k)
				}
			}
			if child := s.children[k]; child != nil && rawPresent(raw) {
				inspect(rawObject(raw), child, path+"."+k)
			}
			if child := s.arrays[k]; child != nil && rawPresent(raw) {
				a := rawArray(raw)
				// Chat content is either ordinary text or a structured block array.
				if k == "content" && e.native.Source.Protocol == ProviderOpenAI && len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] == '"' {
					continue
				}
				if a == nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("[]")) {
					add(ReasonUninspectedResponse, path+"."+k)
					continue
				}
				for _, item := range a {
					inspect(rawObject(item), child, path+"."+k+"[]")
				}
			}
		}
	}
	inspect(root, responseSchema(e.native.Source.Protocol, e.wire != nil), "response")
	if e.wire != nil && e.wire.Event != "" {
		typ := rawString(root["type"])
		if typ == "" || typ != e.wire.Event {
			add(ReasonSSEMetadata, "sse.event")
		}
	}
	return report
}

func (e *ResponseEnvelope) Render(to Provider) ([]byte, ConversionReport, error) {
	report := e.Compatibility(to)
	if !to.Valid() {
		return nil, report, fmt.Errorf("未知的目标协议: %s", to)
	}
	if e.native.Source.Protocol == to {
		return e.Original(), report, nil
	}
	resp, err := parseResponseToOpenAI(e.native.Source.Protocol, e.native.Original)
	if err != nil {
		return nil, report, err
	}
	out, err := renderResponseFromOpenAI(to, resp)
	return out, report, err
}

func ConvertResponseWithReport(from, to Provider, body []byte) ([]byte, ConversionReport, error) {
	e, err := CaptureResponse(from, body)
	if err != nil {
		return nil, ConversionReport{}, err
	}
	return e.Render(to)
}
