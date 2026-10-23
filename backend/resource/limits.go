// Package resource defines conversion resource contracts without business dependencies.
package resource

import (
	"fmt"
	"io"
)

type Scope string

const (
	Request  Scope = "request"
	Response Scope = "response"
	Stream   Scope = "stream"
)

type Limits struct {
	MaxRequestBytes       int64 `json:"max_request_bytes"`
	MaxResponseBytes      int64 `json:"max_response_bytes"`
	MaxNativeFragments    int   `json:"max_native_fragments"`
	MaxToolArgumentsBytes int64 `json:"max_tool_arguments_bytes"`
	MaxAccumulatedBytes   int64 `json:"max_accumulated_bytes"`
	MaxJSONDepth          int   `json:"max_json_depth"`
}

func Defaults() Limits {
	return Limits{MaxRequestBytes: 32 << 20, MaxResponseBytes: 32 << 20, MaxNativeFragments: 8192, MaxToolArgumentsBytes: 4 << 20, MaxAccumulatedBytes: 64 << 20, MaxJSONDepth: 128}
}
func (l Limits) Normalize() Limits {
	d := Defaults()
	if l.MaxRequestBytes <= 0 {
		l.MaxRequestBytes = d.MaxRequestBytes
	}
	if l.MaxResponseBytes <= 0 {
		l.MaxResponseBytes = d.MaxResponseBytes
	}
	if l.MaxNativeFragments <= 0 {
		l.MaxNativeFragments = d.MaxNativeFragments
	}
	if l.MaxToolArgumentsBytes <= 0 {
		l.MaxToolArgumentsBytes = d.MaxToolArgumentsBytes
	}
	if l.MaxAccumulatedBytes <= 0 {
		l.MaxAccumulatedBytes = d.MaxAccumulatedBytes
	}
	if l.MaxJSONDepth <= 0 {
		l.MaxJSONDepth = d.MaxJSONDepth
	}
	return l
}

// LimitError contains only static categories and numeric limits, never source data.
type LimitError struct {
	Scope Scope
	Kind  string
	Limit int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("转换资源超过上限: %s/%s (%d)", e.Scope, e.Kind, e.Limit)
}
func CheckSize(n, max int64, scope Scope, kind string) error {
	if n > max {
		return &LimitError{scope, kind, max}
	}
	return nil
}

// ReadAll reads at most max+1 bytes, including for unknown/chunked lengths.
// It never returns a truncated body as a valid response.
func ReadAll(r io.Reader, max int64, scope Scope) ([]byte, error) {
	if max <= 0 {
		if scope == Request {
			max = Defaults().MaxRequestBytes
		} else {
			max = Defaults().MaxResponseBytes
		}
	}
	// Avoid overflow for directly constructed or extreme configurations.
	const largest = int64(1<<63 - 1)
	if max == largest {
		max--
	}
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if int64(len(b)) > max {
		return nil, &LimitError{scope, "body_bytes", max}
	}
	if err != nil {
		return b, err
	}
	return b, nil
}

// CheckDepth scans before JSON decoding/indexing; braces inside strings are ignored.
// JSON syntax validation remains with the existing protocol parsers.
func CheckDepth(b []byte, max int, scope Scope) error {
	if max <= 0 {
		max = Defaults().MaxJSONDepth
	}
	depth := 0
	quoted, escaped := false, false
	for _, c := range b {
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '{', '[':
			depth++
			if depth > max {
				return &LimitError{scope, "json_depth", int64(max)}
			}
		case '}', ']':
			if depth > 0 {
				depth--
			}
		}
	}
	return nil
}
