package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

type SourceTrust string

const SourceUnverified SourceTrust = "unverified"

type SourceDescriptor struct {
	Protocol Provider
	Trust    SourceTrust
}
type NativeOwner string

const (
	OwnerRequest NativeOwner = "request"
	OwnerItem    NativeOwner = "item"
	OwnerTool    NativeOwner = "tool"
	OwnerBlock   NativeOwner = "block"
)

// OwnerRef indexes the original sequence, never a rewritten canonical sequence.
type OwnerRef struct {
	Kind  NativeOwner
	Item  int
	Field string
}
type NativeKind string

const (
	NativeUnknown NativeKind = "unknown"
	NativeKnown   NativeKind = "known"
	NativeOpaque  NativeKind = "opaque"
)

type NativeFragment struct {
	Owner    OwnerRef
	Position int
	Kind     NativeKind
	Raw      json.RawMessage `json:"-"`
}

// NativeEnvelope is an explicit inspection snapshot. Raw data is never serialized.
type NativeEnvelope struct {
	Source    SourceDescriptor
	Original  json.RawMessage  `json:"-"`
	Fragments []NativeFragment `json:"-"`
}
type RequestEnvelope struct{ native NativeEnvelope }

// Native returns a deep copy so callers and target attempts cannot mutate captured data.
func (e *RequestEnvelope) Native() NativeEnvelope {
	n := e.native
	n.Original = bytes.Clone(n.Original)
	n.Fragments = append([]NativeFragment(nil), n.Fragments...)
	for i := range n.Fragments {
		n.Fragments[i].Raw = bytes.Clone(n.Fragments[i].Raw)
	}
	return n
}
func (e *RequestEnvelope) Original() []byte { return bytes.Clone(e.native.Original) }

type CompatibilityReason string

const (
	ReasonUnknownField           CompatibilityReason = "unknown_top_level_field"
	ReasonUnsupportedTool        CompatibilityReason = "unsupported_native_tool"
	ReasonUnsupportedItem        CompatibilityReason = "unsupported_native_item"
	ReasonUnsupportedBlock       CompatibilityReason = "unsupported_native_block"
	ReasonHostedToolDowngrade    CompatibilityReason = "hosted_tool_downgrade"
	ReasonUnverifiedContinuation CompatibilityReason = "unverified_continuation"
)

// Paths are static protocol paths, never client-provided field names or values.
// This initial report checks top-level unknown fields and selected protocol-native
// constructs. It is not a complete recursive losslessness proof: unknown nested
// metadata (including reasoning item metadata) is preserved in raw fragments for
// same-protocol traffic, but is not generally mapped or diagnosed across protocols.
type ConversionIssue struct {
	Reason CompatibilityReason `json:"reason"`
	Path   string              `json:"path"`
}
type ConversionReport struct {
	Scope      string            `json:"scope,omitempty"`
	Checked    bool              `json:"checked"`
	Compatible bool              `json:"compatible"`
	Issues     []ConversionIssue `json:"issues,omitempty"`
}

// CaptureRequest snapshots raw fragments without forcing same-protocol traffic through
// canonical parsing. Semantic validation remains in the existing cross-protocol parser.
func CaptureRequest(from Provider, body []byte) (*RequestEnvelope, error) {
	if !from.Valid() {
		return nil, fmt.Errorf("未知的源协议: %s", from)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("解析 %s 请求失败: %w", from, err)
	}
	if root == nil {
		return nil, fmt.Errorf("解析 %s 请求失败: 请求必须为对象", from)
	}
	e := &RequestEnvelope{native: NativeEnvelope{Source: SourceDescriptor{Protocol: from, Trust: SourceUnverified}, Original: bytes.Clone(body)}}
	add := func(owner OwnerRef, pos int, kind NativeKind, raw json.RawMessage) {
		e.native.Fragments = append(e.native.Fragments, NativeFragment{Owner: owner, Position: pos, Kind: kind, Raw: bytes.Clone(raw)})
	}
	known := requestFieldNames(from)
	keys := make([]string, 0, len(root))
	for k := range root {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		kind := NativeKnown
		if !known[k] {
			kind = NativeUnknown
		}
		add(OwnerRef{Kind: OwnerRequest, Item: -1, Field: k}, i, kind, root[k])
	}
	for i, raw := range rawArray(root["tools"]) {
		add(OwnerRef{Kind: OwnerTool, Item: -1, Field: "tools"}, i, NativeKnown, raw)
	}
	field, blocks := "messages", "content"
	if from == ProviderOpenAIResponses {
		field = "input"
	}
	if from == ProviderGemini {
		field = "contents"
		blocks = "parts"
	}
	for i, raw := range rawArray(root[field]) {
		add(OwnerRef{Kind: OwnerItem, Item: i, Field: field}, i, NativeKnown, raw)
		obj := rawObject(raw)
		for j, block := range rawArray(obj[blocks]) {
			add(OwnerRef{Kind: OwnerBlock, Item: i, Field: blocks}, j, NativeKnown, block)
		}
	}
	return e, nil
}
func (e *RequestEnvelope) PrepareTarget(to Provider, up UpstreamRequest) ([]byte, ComposeStats, error) {
	var stats ComposeStats
	if !to.Valid() {
		return nil, stats, fmt.Errorf("未知的目标协议: %s", to)
	}
	from := e.native.Source.Protocol
	var parsed *ChatCompletionRequest
	// Parse first for cross-protocol requests: malformed client input keeps its existing
	// error classification; compatibility diagnostics do not replace input validation.
	if from != to {
		var err error
		parsed, err = parseRequestToOpenAI(from, e.native.Original)
		if err != nil {
			return nil, stats, err
		}
	}
	// Compatibility is diagnostic only; preserve the existing conversion behavior.
	report := e.compatibility(to)
	stats.Compatibility = report
	var out []byte
	var base ComposeStats
	var err error
	if parsed != nil {
		out, base, err = composeParsedUpstream(to, parsed, up)
	} else {
		out, base, err = composeUpstream(from, to, bytes.Clone(e.native.Original), up)
	}
	base.Compatibility = report
	if err != nil {
		return nil, base, err
	}
	if up.ThinkingCompat && to == ProviderOpenAI {
		out, base.ShimRewrites, err = InjectReasoningPlaceholders(out)
	}
	return out, base, err
}
func rawArray(raw json.RawMessage) []json.RawMessage {
	var a []json.RawMessage
	_ = json.Unmarshal(raw, &a)
	return a
}
func rawObject(raw json.RawMessage) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	return m
}
func rawString(raw json.RawMessage) string { var s string; _ = json.Unmarshal(raw, &s); return s }
func rawPresent(raw json.RawMessage) bool {
	return len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
func hasExplicitThinkingRaw(m map[string]json.RawMessage) bool {
	return rawPresent(m["thinking"]) || rawString(m["reasoning_effort"]) == "none"
}
func requestFieldNames(p Provider) map[string]bool {
	var value any
	switch p {
	case ProviderOpenAI:
		value = ChatCompletionRequest{}
	case ProviderAnthropic:
		value = MessagesRequest{}
	case ProviderOpenAIResponses:
		value = ResponsesRequest{}
	case ProviderGemini:
		value = GenerateContentRequest{}
	}
	names := map[string]bool{}
	t := reflect.TypeOf(value)
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			names[name] = true
		}
	}
	switch p {
	case ProviderOpenAI:
		names["reasoning_effort"] = true
	case ProviderAnthropic:
		names["output_config"] = true
	case ProviderOpenAIResponses:
		names["reasoning"] = true
		names["previous_response_id"] = true
	}
	return names
}
func (e *RequestEnvelope) compatibility(to Provider) ConversionReport {
	report := ConversionReport{Scope: "request_native_v1", Checked: true, Compatible: true}
	add := func(reason CompatibilityReason, path string) {
		for _, issue := range report.Issues {
			if issue.Reason == reason && issue.Path == path {
				return
			}
		}
		report.Compatible = false
		report.Issues = append(report.Issues, ConversionIssue{Reason: reason, Path: path})
	}
	from := e.native.Source.Protocol
	cross := from != to
	root := rawObject(e.native.Original)
	if from == ProviderOpenAIResponses && rawPresent(root["previous_response_id"]) && rawString(root["previous_response_id"]) != "" {
		add(ReasonUnverifiedContinuation, "$.previous_response_id")
	}
	for _, f := range e.native.Fragments {
		obj := rawObject(f.Raw)
		typ := rawString(obj["type"])
		switch f.Owner.Kind {
		case OwnerRequest:
			if cross && f.Kind == NativeUnknown {
				add(ReasonUnknownField, "$.*")
			}
		case OwnerTool:
			if !cross {
				continue
			}
			supported, hosted := false, false
			switch from {
			case ProviderOpenAI:
				supported = typ == "function"
				hosted = typ == "web_search"
			case ProviderAnthropic:
				supported = typ == ""
				hosted = isVersionedClaudeWebSearchType(typ)
			case ProviderOpenAIResponses:
				supported = typ == "function"
				hosted = isResponsesWebSearchType(typ)
			case ProviderGemini:
				supported = true
				for key := range obj {
					switch key {
					case "functionDeclarations":
					case "googleSearch", "googleSearchRetrieval", "enterpriseWebSearch":
						hosted = true
					default:
						supported = false
					}
				}
			}
			if hosted {
				if to == ProviderOpenAI {
					add(ReasonHostedToolDowngrade, "$.tools[]")
				}
			}
			if !supported && (!hosted || from == ProviderGemini) {
				add(ReasonUnsupportedTool, "$.tools[]")
			}
		case OwnerItem:
			if from == ProviderOpenAIResponses {
				if typ == "reasoning" && rawPresent(obj["encrypted_content"]) {
					add(ReasonUnverifiedContinuation, "$.input[].encrypted_content")
				}
				if cross {
					switch typ {
					case "", "message", "function_call", "function_call_output", "reasoning":
					default:
						add(ReasonUnsupportedItem, "$.input[]")
					}
				}
			}
		case OwnerBlock:
			if from == ProviderAnthropic {
				sig := rawString(obj["signature"])
				_, signatureBridge := DecodeReasoningSignature(sig)
				_, dataBridge := DecodeReasoningSignature(rawString(obj["data"]))
				if (sig != "" && !signatureBridge) || (typ == "redacted_thinking" && rawPresent(obj["data"]) && !dataBridge) {
					add(ReasonUnverifiedContinuation, "$.messages[].content[]")
				}
				if cross {
					switch typ {
					case "text", "image", "tool_use", "tool_result", "server_tool_use", "web_search_tool_result", "thinking", "redacted_thinking":
					default:
						add(ReasonUnsupportedBlock, "$.messages[].content[]")
					}
				}
			}
			if from == ProviderGemini && rawPresent(obj["thoughtSignature"]) {
				add(ReasonUnverifiedContinuation, "$.contents[].parts[].thoughtSignature")
			}
			if cross && from == ProviderGemini {
				for key := range obj {
					switch key {
					case "text", "thought", "inlineData", "functionCall", "functionResponse", "thoughtSignature":
					default:
						add(ReasonUnsupportedBlock, "$.contents[].parts[]")
					}
				}
			}
			if cross && from == ProviderOpenAI {
				switch typ {
				case "text", "image_url":
				default:
					add(ReasonUnsupportedBlock, "$.messages[].content[]")
				}
			}
			if cross && from == ProviderOpenAIResponses {
				switch typ {
				case "input_text", "output_text", "summary_text":
				default:
					add(ReasonUnsupportedBlock, "$.input[].content[]")
				}
			}
		}
	}
	return report
}
