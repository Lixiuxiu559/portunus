package protocol

import "encoding/json"

// StreamRequestReplaySafe enforces replay safety independently of compatibility diagnostics.
// Function declarations are executed by clients; known search tools only read.
// Other native tools or stateful continuations may already act before output.
func StreamRequestReplaySafe(from Provider, body []byte) bool {
	var request map[string]json.RawMessage
	if json.Unmarshal(body, &request) != nil || request == nil {
		return false
	}
	for _, key := range []string{"previous_response_id", "conversation", "conversation_id", "session_id", "computer", "mcp_servers"} {
		if hasValue(request, key) {
			return false
		}
	}
	for _, raw := range rawList(request["tools"]) {
		tool := rawMap(raw)
		if tool == nil {
			return false
		}
		typ := rawString(tool["type"])
		switch from {
		case ProviderOpenAI, ProviderOpenAIResponses:
			if typ != "function" && !safeSearchTool(typ) {
				return false
			}
		case ProviderAnthropic:
			if typ != "" && typ != "custom" && !safeSearchTool(typ) {
				return false
			}
			if typ == "" && !hasValue(tool, "name", "input_schema") {
				return false
			}
		case ProviderGemini:
			for key := range tool {
				if !oneOf(key, "functionDeclarations", "googleSearch", "google_search", "googleSearchRetrieval") {
					return false
				}
			}
		default:
			return false
		}
	}
	// Historic hosted/native tool items may bind server-side state even when no
	// corresponding declaration is present in this request.
	var value any
	if json.Unmarshal(body, &value) != nil {
		return false
	}
	return replaySafeValue(value)
}
func safeSearchTool(typ string) bool {
	return oneOf(typ, "web_search", "web_search_preview", "web_search_20250305", "web_search_20260209", "web_search_2025_08_26", "web_search_preview_2025_03_11")
}
func replaySafeValue(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		typ, _ := v["type"].(string)
		if oneOf(typ, "computer_call", "computer_call_output", "mcp_call", "mcp_approval_request", "mcp_approval_response", "code_interpreter_call", "local_shell_call", "shell_call", "apply_patch_call") {
			return false
		}
		if typ == "server_tool_use" {
			name, _ := v["name"].(string)
			if name != "web_search" {
				return false
			}
		}
		for key, item := range v {
			if oneOf(key, "encrypted_content", "thoughtSignature", "thought_signature") {
				if text, ok := item.(string); ok && text != "" {
					return false
				}
			}
			if !replaySafeValue(item) {
				return false
			}
		}
	case []any:
		for _, item := range v {
			if !replaySafeValue(item) {
				return false
			}
		}
	}
	return true
}
