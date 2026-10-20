package protocol

import (
	"encoding/json"
	"net/http"
	"regexp"
)

// 只匹配明确指向字段不被接受的措辞，不把超限、非法数值或其他参数错误当作字段错误。
var unsupportedMaxTokens = regexp.MustCompile("(?i)(?:\\b(?:unsupported|unknown|unrecognized|unexpected)\\s+(?:parameter|field|argument|keyword argument)\\s*:?\\s*['\"`]?max_tokens\\b|\\bmax_tokens\\b['\"`]?\\s*(?:is\\s+)?(?:not supported|unsupported|not allowed|not permitted|unrecognized|unknown)\\b)")

// ChatTokenBudgetFallback 只对明确的 max_tokens 字段兼容错误构造替代请求。
// 原字段数值与其余 JSON 原始载荷均保留，不同时发送两个字段，也不删除预算。
// 调用方保证只用于跨协议转 Chat、尚未交付内容且当前目标未切换过的请求。
func ChatTokenBudgetFallback(body []byte, status int, errorBody []byte) ([]byte, bool) {
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		return nil, false
	}
	var response struct {
		Error *struct {
			Param   *string `json:"param"`
			Code    string  `json:"code"`
			Type    string  `json:"type"`
			Message string  `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(errorBody, &response) != nil || response.Error == nil {
		return nil, false
	}
	e := response.Error
	if e.Param != nil && *e.Param != "max_tokens" {
		return nil, false
	}
	// 明确的数值/上下文错误优先于自然语言措辞，避免把值限制误判成字段不支持。
	switch e.Code {
	case "invalid_value", "invalid_parameter_value", "context_length_exceeded":
		return nil, false
	}
	unsupportedCode := func(code string) bool {
		switch code {
		case "unsupported_parameter", "unknown_parameter", "unrecognized_parameter":
			return true
		}
		return false
	}
	explicitParam := e.Param != nil && (unsupportedCode(e.Code) || unsupportedCode(e.Type))
	if !explicitParam && !unsupportedMaxTokens.MatchString(e.Message) {
		return nil, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return nil, false
	}
	// 不替换调用方已经提供的新字段，即便它是 null；避免覆盖或推断冲突的预算意图。
	if _, exists := fields["max_completion_tokens"]; exists {
		return nil, false
	}
	limit, exists := fields["max_tokens"]
	if !exists {
		return nil, false
	}
	var n uint64
	if json.Unmarshal(limit, &n) != nil || n == 0 {
		return nil, false
	}
	fields["max_completion_tokens"] = limit
	delete(fields, "max_tokens")
	rewritten, err := json.Marshal(fields)
	if err != nil {
		return nil, false
	}
	return rewritten, true
}
