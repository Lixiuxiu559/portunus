package gateway

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
)

// writeRelayError 按客户端协议输出协议形状错误体并结束请求：
// Anthropic 为 {"type":"error","error":{"type","message"}}，OpenAI / Responses 为
// {"error":{"message","type","param","code"}}。消息统一追加 request id——客户端
// 粘贴报错原文即可对齐网关日志，不必另找 X-Request-Id。
func writeRelayError(c *gin.Context, clientProto protocol.Provider, status int, kind relayErrorKind, message string) {
	if rid := c.Writer.Header().Get("X-Request-Id"); rid != "" {
		message = fmt.Sprintf("%s (request id: %s)", message, rid)
	}
	switch clientProto {
	case protocol.ProviderAnthropic:
		c.JSON(status, gin.H{"type": "error", "error": gin.H{"type": anthropicErrorType(kind), "message": message}})
	default:
		c.JSON(status, gin.H{"error": gin.H{"message": message, "type": openAIErrorType(kind), "param": nil, "code": nil}})
	}
}

// anthropicErrorType 映射 Anthropic 官方错误类型枚举。
func anthropicErrorType(kind relayErrorKind) string {
	switch kind {
	case relayErrInvalidRequest:
		return "invalid_request_error"
	case relayErrAuth:
		return "authentication_error"
	case relayErrNotFound:
		return "not_found_error"
	case relayErrRateLimit:
		return "rate_limit_error"
	case relayErrOverloaded:
		return "overloaded_error"
	default:
		return "api_error"
	}
}

// openAIErrorType 映射 OpenAI 错误类型（Responses 客户端同形状）。
func openAIErrorType(kind relayErrorKind) string {
	switch kind {
	case relayErrInvalidRequest, relayErrNotFound:
		return "invalid_request_error"
	case relayErrAuth:
		return "authentication_error"
	case relayErrRateLimit:
		return "rate_limit_error"
	default:
		return "server_error"
	}
}

// clientProtoFromPath 由请求路径推断客户端协议：/v1/messages 是 Anthropic，
// 其余（/chat/completions、/responses）按 OpenAI 形状应答。供路由级中间件
// （鉴权 401 等）在 handler 尚未携带 clientProto 时使用。
func clientProtoFromPath(path string) protocol.Provider {
	if strings.HasSuffix(path, "/messages") {
		return protocol.ProviderAnthropic
	}
	return protocol.ProviderOpenAI
}

// upstreamErrorMessage 从上游错误响应体提取人读消息，兼容常见形状：
// OpenAI {"error":{"message"}}、Anthropic {"type":"error","error":{...}}、
// error 为字符串、顶层 message / msg / detail。提取不到返回空串，由调用方
// 使用通用文案——原始响应体已完整落库（err_msg），不丢排障信息。
func upstreamErrorMessage(body []byte) string {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return ""
	}
	if v, ok := m["error"]; ok {
		switch e := v.(type) {
		case string:
			return e
		case map[string]any:
			if s, _ := e["message"].(string); s != "" {
				return s
			}
		}
	}
	for _, key := range []string{"message", "msg", "detail"} {
		if s, _ := m[key].(string); s != "" {
			return s
		}
	}
	return ""
}
