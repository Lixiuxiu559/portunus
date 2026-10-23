package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

var ErrStreamIncomplete = errors.New("upstream stream ended without a valid terminal event")
var ErrStreamInvalid = errors.New("invalid upstream stream event")

// StreamFailureError contains only static classifications, never upstream bodies.
type StreamFailureError struct {
	Status int
	Reason string
}

func (e *StreamFailureError) Error() string { return "upstream stream failure: " + e.Reason }

type StreamOutcome struct {
	Status           string
	Reason           string
	TerminalObserved bool
}
type StreamBatch struct {
	Frames       [][]byte
	Meaningful   bool
	Terminal     bool
	Done         bool
	ReplayUnsafe bool
}

// StreamSession observes source semantics separately from conversion and delivery.
// Finish is the only successful cross-protocol closing path. Calling it on an
// incomplete/failed source cannot manufacture end_turn/completed/[DONE].
type StreamSession struct {
	from, to          Provider
	conv              StreamConverter
	framer            *StreamFramer
	outcome           StreamOutcome
	finished, closed  bool
	anthropicStop     string
	choices           map[int]string
	nativeEvent       *ResponseEnvelope
	compatibility     ConversionReport
	compatibilitySeen map[ConversionIssue]bool
}

func NewStreamSession(from, to Provider, conv StreamConverter) *StreamSession {
	return &StreamSession{from: from, to: to, conv: conv, framer: NewStreamFramer(to, conv), outcome: StreamOutcome{Status: "pending"}, choices: map[int]string{}, compatibility: ConversionReport{Scope: "stream_native_v1", Compatible: true}, compatibilitySeen: map[ConversionIssue]bool{}}
}
func (s *StreamSession) Outcome() StreamOutcome { return s.outcome }

// NativeEvent exposes only the current source event, never the whole stream.
func (s *StreamSession) NativeEvent() *ResponseEnvelope { return cloneResponseEvent(s.nativeEvent) }

func (s *StreamSession) Compatibility() ConversionReport {
	report := s.compatibility
	report.Issues = append([]ConversionIssue(nil), report.Issues...)
	return report
}

func (s *StreamSession) captureNativeEvent(ev SSEEvent) error {
	native, err := CaptureResponseEvent(s.from, ev)
	if err != nil {
		return err
	}
	s.nativeEvent = native
	report := native.Compatibility(s.to)
	s.compatibility.Checked = s.compatibility.Checked || report.Checked
	s.compatibility.Compatible = s.compatibility.Compatible && report.Compatible
	for _, issue := range report.Issues {
		if !s.compatibilitySeen[issue] {
			s.compatibilitySeen[issue] = true
			s.compatibility.Issues = append(s.compatibility.Issues, issue)
		}
	}
	return nil
}

func (s *StreamSession) convertNative(payload []byte) ([][]byte, error) {
	if typed, ok := s.conv.(*typedStreamConverter); ok {
		return typed.convertNative(payload, s.nativeEvent)
	}
	return s.conv.Convert(payload)
}
func (s *StreamSession) Process(ev SSEEvent) (batch StreamBatch, err error) {
	defer func() {
		if errors.Is(err, ErrStreamIncomplete) {
			s.outcome = StreamOutcome{Status: "truncated", Reason: "missing_terminal"}
		}
		if errors.Is(err, ErrStreamInvalid) {
			s.outcome = StreamOutcome{Status: "failed", Reason: "invalid_event", TerminalObserved: s.outcome.TerminalObserved}
		}
	}()
	if s.finished || s.closed {
		return batch, nil
	}
	if err := s.captureNativeEvent(ev); err != nil {
		return batch, ErrStreamInvalid
	}
	if !ev.HasData || len(bytes.TrimSpace(ev.Data)) == 0 {
		if s.from == s.to {
			batch.Frames = [][]byte{ev.Raw}
		}
		return batch, nil
	}
	if bytes.Equal(bytes.TrimSpace(ev.Data), []byte("[DONE]")) {
		if s.from != ProviderOpenAI && s.from != ProviderOpenAIResponses && s.from != ProviderGemini {
			return batch, ErrStreamInvalid
		}
		if !s.outcome.TerminalObserved {
			return batch, ErrStreamIncomplete
		}
		s.closed = true
		batch.Done = true
		batch.Terminal = true
		if s.from == s.to {
			batch.Frames = [][]byte{ev.Raw}
		}
		return batch, nil
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(ev.Data, &obj) != nil || obj == nil {
		if ev.Event == "error" || ev.Event == "response.failed" {
			_, _, failure := s.fail(nil)
			if s.from == s.to {
				batch.Frames = [][]byte{ev.Raw}
				batch.Terminal = true
			}
			return batch, failure
		}
		if s.from == s.to && !knownJSONEventName(s.from, ev.Event) {
			if s.outcome.TerminalObserved {
				return batch, ErrStreamInvalid
			}
			return StreamBatch{Frames: [][]byte{ev.Raw}, Meaningful: true, ReplayUnsafe: true}, nil
		}
		return batch, ErrStreamInvalid
	}
	payload := ev.Data
	// SSE event names can carry the discriminator even if JSON omits type.
	if ev.Event != "" && rawString(obj["type"]) == "" && (s.from == ProviderAnthropic || s.from == ProviderOpenAIResponses) {
		obj["type"], _ = json.Marshal(ev.Event)
		payload, _ = json.Marshal(obj)
	}
	wasTerminal := s.outcome.TerminalObserved
	done, unknown, err := s.observe(obj, ev.Event)
	if err != nil {
		var failure *StreamFailureError
		if s.from == s.to && errors.As(err, &failure) {
			batch.Frames = [][]byte{ev.Raw}
			batch.Terminal = true
		}
		return batch, err
	}
	unknown = unknown || streamNativeUnknown(s.from, obj)
	if wasTerminal && (unknown || streamPayloadMeaningful(s.from, payload)) {
		return batch, ErrStreamInvalid
	}
	batch.Done = done
	batch.Terminal = s.from == s.to && s.outcome.TerminalObserved
	batch.ReplayUnsafe = unknown
	if s.from == s.to {
		// Conversion is used only for side-band usage extraction; wire bytes stay raw.
		if _, err = s.convertNative(payload); err != nil {
			return batch, ErrStreamInvalid
		}
		batch.Frames = [][]byte{ev.Raw}
		batch.Meaningful = unknown || streamPayloadMeaningful(s.to, payload)
	} else {
		chunks, err := s.convertNative(payload)
		// Prompt blocking is a legitimate empty refusal; preserve its terminal
		// meaning even though the legacy Gemini decoder sees no candidate.
		if s.from == ProviderGemini && s.outcome.TerminalObserved && rawMap(obj["promptFeedback"])["blockReason"] != nil {
			if typed, ok := s.conv.(*typedStreamConverter); ok {
				if decoder, ok := typed.decoder.(*geminiToOpenAIStream); ok {
					decoder.st.finish = "content_filter"
				}
			}
		}
		if err != nil {
			return StreamBatch{}, ErrStreamInvalid
		}
		batch.Frames = s.framer.frameAll(chunks)
		for _, chunk := range chunks {
			batch.Meaningful = batch.Meaningful || streamPayloadMeaningful(s.to, chunk)
		}
	}
	s.closed = done
	return batch, nil
}
func (s *StreamSession) Finish() (StreamBatch, error) {
	if s.finished {
		return StreamBatch{}, nil
	}
	s.finished = true
	if !s.outcome.TerminalObserved || s.outcome.Status == "failed" {
		if !s.outcome.TerminalObserved {
			s.outcome = StreamOutcome{Status: "truncated", Reason: "missing_terminal"}
		}
		return StreamBatch{}, ErrStreamIncomplete
	}
	batch := StreamBatch{Terminal: true, Done: true}
	if s.from == s.to {
		return batch, nil
	}
	chunks, err := s.conv.Finish()
	if err != nil {
		return StreamBatch{}, ErrStreamInvalid
	}
	batch.Frames = s.framer.frameAll(chunks)
	for _, chunk := range chunks {
		batch.Meaningful = batch.Meaningful || streamPayloadMeaningful(s.to, chunk)
	}
	if s.to != ProviderAnthropic {
		batch.Frames = append(batch.Frames, []byte("data: [DONE]\n\n"))
	}
	return batch, nil
}
func rawMap(raw json.RawMessage) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	return m
}
func rawList(raw json.RawMessage) []json.RawMessage {
	var a []json.RawMessage
	_ = json.Unmarshal(raw, &a)
	return a
}
func (s *StreamSession) terminal(status, reason string) error {
	if s.outcome.TerminalObserved && (s.outcome.Status != status || s.outcome.Reason != reason) {
		return ErrStreamInvalid
	}
	s.outcome = StreamOutcome{Status: status, Reason: reason, TerminalObserved: true}
	return nil
}
func (s *StreamSession) fail(obj map[string]json.RawMessage) (bool, bool, error) {
	status := 502
	typ := firstNonEmpty(rawString(obj["type"]), rawString(obj["code"]))
	var wireStatus int
	if json.Unmarshal(obj["code"], &wireStatus) == nil && wireStatus >= 400 && wireStatus <= 599 {
		status = wireStatus
	}
	switch typ {
	case "overloaded_error", "server_error":
		status = 503
	case "rate_limit_error":
		status = 429
	case "authentication_error":
		status = 401
	case "permission_error":
		status = 403
	case "invalid_request_error":
		status = 400
	}
	s.outcome = StreamOutcome{Status: "failed", Reason: "upstream_error", TerminalObserved: true}
	return false, false, &StreamFailureError{Status: status, Reason: "upstream_error"}
}
func (s *StreamSession) observe(obj map[string]json.RawMessage, eventName string) (bool, bool, error) {
	typ := rawString(obj["type"])
	if typ == "" {
		typ = eventName
	}
	if len(obj["error"]) > 0 && string(obj["error"]) != "null" {
		return s.fail(rawMap(obj["error"]))
	}
	if typ == "error" || typ == "response.failed" {
		return s.fail(rawMap(rawMap(obj["response"])["error"]))
	}
	switch s.from {
	case ProviderOpenAI:
		arr, ok := obj["choices"]
		if !ok {
			return false, obj["usage"] == nil, nil
		}
		for _, raw := range rawList(arr) {
			c := rawMap(raw)
			var idx int
			_ = json.Unmarshal(c["index"], &idx)
			if _, ok := s.choices[idx]; !ok {
				s.choices[idx] = ""
			}
			reason := rawString(c["finish_reason"])
			if reason != "" {
				if !oneOf(reason, "stop", "length", "tool_calls", "function_call", "content_filter") {
					return false, false, ErrStreamInvalid
				}
				if s.choices[idx] != "" {
					return false, false, ErrStreamInvalid
				}
				s.choices[idx] = reason
			}
		}
		all := len(s.choices) > 0
		for _, done := range s.choices {
			all = all && done != ""
		}
		if all {
			status, reason := "completed", "stop"
			for _, r := range s.choices {
				if r == "length" || r == "content_filter" {
					status = "incomplete"
					reason = r
				}
			}
			if !s.outcome.TerminalObserved {
				return false, false, s.terminal(status, reason)
			}
		}
		return false, false, nil
	case ProviderAnthropic:
		switch typ {
		case "message_start", "content_block_start", "content_block_delta", "content_block_stop", "ping":
			return false, false, nil
		case "message_delta":
			reason := rawString(rawMap(obj["delta"])["stop_reason"])
			if reason != "" {
				if !oneOf(reason, "end_turn", "max_tokens", "stop_sequence", "tool_use", "pause_turn", "refusal", "model_context_window_exceeded") {
					return false, false, ErrStreamInvalid
				}
				s.anthropicStop = reason
			}
			return false, false, nil
		case "message_stop":
			if s.anthropicStop == "" {
				return false, false, ErrStreamIncomplete
			}
			status := "completed"
			if s.anthropicStop == "max_tokens" || s.anthropicStop == "model_context_window_exceeded" {
				status = "incomplete"
			}
			return true, false, s.terminal(status, s.anthropicStop)
		default:
			return false, true, nil
		}
	case ProviderOpenAIResponses:
		switch typ {
		case "response.completed", "response.incomplete":
			response := rawMap(obj["response"])
			if response == nil {
				return false, false, ErrStreamInvalid
			}
			status := strings.TrimPrefix(typ, "response.")
			wireStatus := rawString(response["status"])
			if wireStatus != "" && wireStatus != status {
				return false, false, ErrStreamInvalid
			}
			reason := status
			if status == "incomplete" {
				reason = rawString(rawMap(response["incomplete_details"])["reason"])
				if !oneOf(reason, "max_output_tokens", "content_filter") {
					reason = "incomplete"
				}
			}
			return true, false, s.terminal(status, reason)
		case "response.created", "response.in_progress", "response.output_text.delta", "response.output_text.done", "response.reasoning_summary_text.delta", "response.reasoning_summary_text.done", "response.reasoning_text.delta", "response.reasoning_text.done", "response.content_part.added", "response.content_part.done", "response.reasoning_summary_part.added", "response.reasoning_summary_part.done", "response.output_item.added", "response.output_item.done", "response.function_call_arguments.delta", "response.function_call_arguments.done", "response.refusal.delta", "response.refusal.done":
			return false, false, nil
		default:
			return false, true, nil
		}
	case ProviderGemini:
		if reason := rawString(rawMap(obj["promptFeedback"])["blockReason"]); reason != "" {
			if !oneOf(reason, "SAFETY", "OTHER", "BLOCKLIST", "PROHIBITED_CONTENT", "IMAGE_SAFETY", "JAILBREAK", "MODEL_ARMOR") {
				reason = "blocked"
			}
			return false, false, s.terminal("incomplete", reason)
		}
		arr, ok := obj["candidates"]
		if !ok {
			return false, obj["usageMetadata"] == nil, nil
		}
		for pos, raw := range rawList(arr) {
			c := rawMap(raw)
			idx := pos
			if c["index"] != nil {
				_ = json.Unmarshal(c["index"], &idx)
			}
			if _, ok := s.choices[idx]; !ok {
				s.choices[idx] = ""
			}
			reason := rawString(c["finishReason"])
			if reason != "" && reason != "FINISH_REASON_UNSPECIFIED" {
				s.choices[idx] = reason
			}
		}
		all := len(s.choices) > 0
		status, reason := "completed", "STOP"
		for _, r := range s.choices {
			all = all && r != ""
			if r != "" && r != "STOP" {
				status = "incomplete"
				reason = r
			}
		}
		if all && !s.outcome.TerminalObserved {
			if !oneOf(reason, "STOP", "MAX_TOKENS", "SAFETY", "RECITATION", "LANGUAGE", "OTHER", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "MALFORMED_FUNCTION_CALL", "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_OTHER", "NO_IMAGE", "UNEXPECTED_TOOL_CALL", "TOO_MANY_TOOL_CALLS", "MISSING_THOUGHT_SIGNATURE") {
				reason = "unknown_finish_reason"
			}
			return false, false, s.terminal(status, reason)
		}
		return false, false, nil
	}
	return false, true, nil
}
func knownJSONEventName(p Provider, name string) bool {
	if name == "" {
		return false
	}
	if p == ProviderAnthropic {
		return oneOf(name, "message_start", "message_delta", "message_stop", "content_block_start", "content_block_delta", "content_block_stop", "ping")
	}
	if p == ProviderOpenAIResponses {
		return oneOf(name, "response.created", "response.in_progress", "response.completed", "response.incomplete", "response.output_text.delta", "response.output_text.done", "response.reasoning_summary_text.delta", "response.reasoning_summary_text.done", "response.reasoning_text.delta", "response.reasoning_text.done", "response.content_part.added", "response.content_part.done", "response.reasoning_summary_part.added", "response.reasoning_summary_part.done", "response.output_item.added", "response.output_item.done", "response.function_call_arguments.delta", "response.function_call_arguments.done", "response.refusal.delta", "response.refusal.done")
	}
	return false
}

func oneOf(v string, values ...string) bool {
	for _, s := range values {
		if v == s {
			return true
		}
	}
	return false
}

// Meaningfulness is assessed after encoding, not from source deltas: an encoder
// may buffer tool arguments until Finish, or drop an unsupported source event.
func streamPayloadMeaningful(provider Provider, payload []byte) bool {
	m := rawMap(payload)
	if m == nil {
		return false
	}
	switch provider {
	case ProviderOpenAI:
		for _, raw := range rawList(m["choices"]) {
			d := rawMap(rawMap(raw)["delta"])
			if hasValue(d, "content", "reasoning_content", "refusal", "grounding_metadata") || chatToolMeaningful(d) {
				return true
			}
		}
	case ProviderAnthropic:
		switch rawString(m["type"]) {
		case "content_block_delta":
			return hasValue(rawMap(m["delta"]), "text", "thinking", "partial_json", "signature")
		case "content_block_start":
			b := rawMap(m["content_block"])
			return hasValue(b, "text", "thinking", "id", "name", "content", "data")
		case "message_start", "message_delta", "message_stop", "content_block_stop", "ping":
			return false
		default:
			return true
		}
	case ProviderOpenAIResponses:
		typ := rawString(m["type"])
		if strings.HasSuffix(typ, ".delta") {
			return hasValue(m, "delta")
		}
		if hasValue(rawMap(m["part"]), "text", "refusal") || hasValue(m, "text", "arguments", "refusal") {
			return true
		}
		if typ == "response.output_item.added" || typ == "response.output_item.done" {
			item := rawMap(m["item"])
			return hasValue(item, "call_id", "name") || oneOf(rawString(item["type"]), "web_search_call", "computer_call")
		}
	case ProviderGemini:
		for _, raw := range rawList(m["candidates"]) {
			c := rawMap(raw)
			if hasValue(c, "groundingMetadata") {
				return true
			}
			for _, p := range rawList(rawMap(c["content"])["parts"]) {
				if hasValue(rawMap(p), "text", "thoughtSignature", "functionCall", "inlineData", "fileData", "executableCode", "codeExecutionResult") {
					return true
				}
			}
		}
	}
	return false
}

// Unknown native operations tighten replay safety even when a legacy encoder
// ignores them. Same-protocol traffic still forwards their original frame.
func streamNativeUnknown(p Provider, m map[string]json.RawMessage) bool {
	switch p {
	case ProviderOpenAI:
		for _, c := range rawList(m["choices"]) {
			d := rawMap(rawMap(c)["delta"])
			for k := range d {
				if !oneOf(k, "role", "content", "reasoning_content", "refusal", "tool_calls", "function_call", "grounding_metadata") {
					return true
				}
			}
		}
	case ProviderAnthropic:
		if rawString(m["type"]) == "content_block_start" {
			b := rawMap(m["content_block"])
			if rawString(b["type"]) == "server_tool_use" && rawString(b["name"]) != "web_search" {
				return true
			}
			return !oneOf(rawString(b["type"]), "text", "thinking", "redacted_thinking", "tool_use", "server_tool_use", "web_search_tool_result")
		}
		if rawString(m["type"]) == "content_block_delta" {
			return !oneOf(rawString(rawMap(m["delta"])["type"]), "text_delta", "thinking_delta", "input_json_delta", "signature_delta")
		}
	case ProviderOpenAIResponses:
		if rawString(m["type"]) == "response.output_item.added" {
			return !oneOf(rawString(rawMap(m["item"])["type"]), "message", "reasoning", "function_call")
		}
	case ProviderGemini:
		for _, c := range rawList(m["candidates"]) {
			for _, part := range rawList(rawMap(rawMap(c)["content"])["parts"]) {
				for k := range rawMap(part) {
					if !oneOf(k, "text", "thought", "thoughtSignature", "functionCall", "functionResponse", "inlineData", "fileData") {
						return true
					}
				}
			}
		}
	}
	return false
}

func chatToolMeaningful(d map[string]json.RawMessage) bool {
	if hasValue(rawMap(d["function_call"]), "name", "arguments") {
		return true
	}
	for _, tool := range rawList(d["tool_calls"]) {
		m := rawMap(tool)
		if hasValue(m, "id") || hasValue(rawMap(m["function"]), "name", "arguments") {
			return true
		}
	}
	return false
}

func hasValue(m map[string]json.RawMessage, keys ...string) bool {
	for _, key := range keys {
		v := bytes.TrimSpace(m[key])
		if len(v) > 0 && !bytes.Equal(v, []byte(`""`)) && !bytes.Equal(v, []byte("null")) && !bytes.Equal(v, []byte("[]")) && !bytes.Equal(v, []byte("{}")) {
			return true
		}
	}
	return false
}
