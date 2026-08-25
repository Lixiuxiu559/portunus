package gateway

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
)

// httpClient 不设总超时，避免截断长流式响应。
var httpClient = &http.Client{}

// buildURL 按渠道类型与模型名拼出上游完整 URL。
func buildURL(ch channel.Channel, modelName string, stream bool) string {
	base := strings.TrimRight(ch.BaseURL, "/")
	switch ch.Type {
	case protocol.ProviderOpenAI:
		return base + "/chat/completions"
	case protocol.ProviderOpenAIResponses:
		return base + "/responses"
	case protocol.ProviderAnthropic:
		return base + "/messages"
	case protocol.ProviderGemini:
		suffix := ":generateContent"
		if stream {
			suffix = ":streamGenerateContent"
		}
		return base + "/models/" + modelName + suffix
	}
	return base
}

// buildHeaders 按渠道类型构造上游请求头。
func buildHeaders(ch channel.Channel) http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	switch ch.Type {
	case protocol.ProviderAnthropic:
		h.Set("x-api-key", ch.Key)
		h.Set("anthropic-version", "2023-06-01")
	case protocol.ProviderGemini:
		h.Set("x-goog-api-key", ch.Key)
	default: // openai / responses
		h.Set("Authorization", "Bearer "+ch.Key)
	}
	return h
}

// doRequest 发送上游 POST 请求，返回响应（body 由调用方负责关闭与读取）。
func doRequest(url string, headers http.Header, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = headers
	return httpClient.Do(req)
}
