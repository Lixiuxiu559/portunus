package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/Lixiuxiu559/portunus/backend/resource"
)

func nativeLimits(options []resource.Limits) resource.Limits {
	if len(options) != 0 {
		return options[0].Normalize()
	}
	return resource.Defaults()
}

// inspectNativeResources walks tokens before allocating the RawMessage index. Only
// protocol structural positions count as native fragments; arbitrary tool data does
// not. Token decoding still allocates individual strings, bounded by the body limit.
func inspectNativeResources(from Provider, body []byte, limits resource.Limits, scope resource.Scope) error {
	max := limits.MaxResponseBytes
	if scope == resource.Request {
		max = limits.MaxRequestBytes
	}
	if scope != resource.Stream {
		if err := resource.CheckSize(int64(len(body)), max, scope, "body_bytes"); err != nil {
			return err
		}
	}
	if err := resource.CheckDepth(body, limits.MaxJSONDepth, scope); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	fragments, accumulated := int64(0), int64(0)
	count := func() error {
		fragments++
		return resource.CheckSize(fragments, int64(limits.MaxNativeFragments), scope, "native_fragments")
	}
	var walk func(string) error
	walk = func(path string) error {
		start := d.InputOffset()
		tok, err := d.Token()
		if err != nil {
			return err
		}
		if scope != resource.Request && tok != nil {
			if at := strings.LastIndex(path, "."); at >= 0 && responseIndexedChild(path[:at], path[at+1:]) {
				if err := count(); err != nil {
					return err
				}
			}
		}
		if delim, ok := tok.(json.Delim); ok {
			switch delim {
			case '{':
				for d.More() {
					key, err := d.Token()
					if err != nil {
						return err
					}
					k := key.(string)
					childKey := k
					if strings.ContainsAny(k, ".[]") {
						childKey = "~opaque"
					}
					child := path + "." + childKey
					if path == "$" {
						if err := count(); err != nil {
							return err
						}
					}
					if err := walk(child); err != nil {
						return err
					}
				}
				if _, err := d.Token(); err != nil {
					return err
				}
			case '[':
				for d.More() {
					if nativeIndexedArray(from, path, scope) {
						if err := count(); err != nil {
							return err
						}
					}
					if err := walk(path + "[]"); err != nil {
						return err
					}
				}
				if _, err := d.Token(); err != nil {
					return err
				}
			}
		}
		if nativeToolValue(from, path, scope) {
			n := int64(len(bytes.TrimLeft(body[start:d.InputOffset()], " \t\r\n:,")))
			if s, ok := tok.(string); ok {
				n = int64(len(s))
			}
			if err := resource.CheckSize(n, limits.MaxToolArgumentsBytes, scope, "tool_arguments_bytes"); err != nil {
				return err
			}
			accumulated += n
		} else if nativeToolResultValue(from, path) {
			// Gemini serializes the complete result object into canonical tool text.
			accumulated += int64(len(bytes.TrimLeft(body[start:d.InputOffset()], " \t\r\n:,")))
		} else if nativeTextValue(path) {
			if s, ok := tok.(string); ok {
				accumulated += int64(len(s))
			}
		}
		return resource.CheckSize(accumulated, limits.MaxAccumulatedBytes, scope, "accumulated_bytes")
	}
	err := walk("$")
	var limit *resource.LimitError
	if errors.As(err, &limit) {
		return err
	}
	// Syntax errors retain the old parser/opaque passthrough behavior.
	if err != nil {
		return nil
	}
	if _, err := d.Token(); err != io.EOF {
		return nil
	}
	return nil
}

func nativeIndexedArray(from Provider, path string, scope resource.Scope) bool {
	if scope == resource.Request {
		field, blocks := "messages", "content"
		if from == ProviderOpenAIResponses {
			field = "input"
		}
		if from == ProviderGemini {
			field, blocks = "contents", "parts"
		}
		return path == "$.tools" || path == "$."+field || path == "$."+field+"[]."+blocks
	}
	for _, root := range []string{"$", "$.response", "$.message"} {
		for _, field := range []string{"choices", "output", "content", "candidates"} {
			base := root + "." + field
			if path == base || field == "output" && (path == base+"[].content" || path == base+"[].summary") || field == "candidates" && path == base+"[].content.parts" {
				return true
			}
		}
	}
	return false
}
func responseIndexedChild(path, key string) bool {
	if path == "$" {
		switch key {
		case "response", "message", "item", "content_block", "part", "delta":
			return true
		}
	}
	return (path == "$.choices[]" || path == "$.response.choices[]" || path == "$.message.choices[]") && (key == "message" || key == "delta")
}

func nativeToolValue(from Provider, path string, scope resource.Scope) bool {
	if scope == resource.Request {
		switch from {
		case ProviderOpenAI:
			return path == "$.messages[].tool_calls[].function.arguments" || path == "$.messages[].function_call.arguments"
		case ProviderAnthropic:
			return path == "$.messages[].content[].input"
		case ProviderOpenAIResponses:
			return path == "$.input[].arguments"
		case ProviderGemini:
			return path == "$.contents[].parts[].functionCall.args"
		}
	}
	switch from {
	case ProviderOpenAI:
		return strings.HasPrefix(path, "$.choices[].") && (path == "$.choices[].message.tool_calls[].function.arguments" || path == "$.choices[].delta.tool_calls[].function.arguments" || path == "$.choices[].message.function_call.arguments")
	case ProviderAnthropic:
		return path == "$.content[].input" || path == "$.content_block.input" || path == "$.delta.partial_json"
	case ProviderOpenAIResponses:
		return path == "$.output[].arguments" || path == "$.response.output[].arguments" || path == "$.item.arguments" || path == "$.arguments"
	case ProviderGemini:
		return path == "$.candidates[].content.parts[].functionCall.args"
	}
	return false
}
func nativeToolResultValue(from Provider, path string) bool {
	return from == ProviderGemini && (path == "$.contents[].parts[].functionResponse.response" || path == "$.candidates[].content.parts[].functionResponse.response")
}
func nativeTextValue(path string) bool {
	// Static protocol paths only; never interpret nested user-defined tool payloads.
	for _, p := range []string{
		"$.system", "$.system[].text", "$.instructions", "$.input",
		"$.messages[].content", "$.messages[].reasoning_content", "$.messages[].content[].text", "$.messages[].content[].thinking",
		"$.messages[].content[].content", "$.messages[].content[].content[].text", "$.messages[].content[].content[].title", "$.messages[].content[].content[].url",
		"$.input[].content", "$.input[].content[].text", "$.input[].summary[].text", "$.input[].output",
		"$.contents[].parts[].text", "$.systemInstruction.parts[].text",
		"$.choices[].message.content", "$.choices[].message.reasoning_content", "$.choices[].delta.content", "$.choices[].delta.reasoning_content",
		"$.choices[].message.content[].text", "$.choices[].delta.content[].text",
		"$.content[].text", "$.content[].thinking", "$.output[].content[].text", "$.output[].summary[].text",
		"$.content[].content", "$.content[].content[].text", "$.content[].content[].title", "$.content[].content[].url",
		"$.response.output[].content[].text", "$.response.output[].summary[].text", "$.candidates[].content.parts[].text",
		"$.delta", "$.delta.text", "$.delta.thinking", "$.text", "$.part.text", "$.content_block.text", "$.content_block.thinking",
	} {
		if path == p {
			return true
		}
	}
	return false
}
