package protocol

import (
	"encoding/json"
	"fmt"
)

// 本文件是协议转换的非流式入口，所有转换都经 OpenAI 规范格式中转。

// ConvertRequest 将 from 协议的请求体转换为 to 协议的请求体。
// from == to 时原样返回。
func ConvertRequest(from, to Provider, body []byte) ([]byte, error) {
	if !from.Valid() {
		return nil, fmt.Errorf("未知的源协议: %s", from)
	}
	if !to.Valid() {
		return nil, fmt.Errorf("未知的目标协议: %s", to)
	}
	if from == to {
		return body, nil
	}
	req, err := parseRequestToOpenAI(from, body)
	if err != nil {
		return nil, err
	}
	return renderRequestFromOpenAI(to, req)
}

// ConvertResponse 将 from 协议的响应体转换为 to 协议的响应体。
func ConvertResponse(from, to Provider, body []byte) ([]byte, error) {
	if !from.Valid() {
		return nil, fmt.Errorf("未知的源协议: %s", from)
	}
	if !to.Valid() {
		return nil, fmt.Errorf("未知的目标协议: %s", to)
	}
	if from == to {
		return body, nil
	}
	resp, err := parseResponseToOpenAI(from, body)
	if err != nil {
		return nil, err
	}
	return renderResponseFromOpenAI(to, resp)
}

// parseRequestToOpenAI 按 from 协议解析请求体为 OpenAI 规范请求。
func parseRequestToOpenAI(from Provider, body []byte) (*ChatCompletionRequest, error) {
	switch from {
	case ProviderOpenAI:
		var req ChatCompletionRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("解析 openai 请求失败: %w", err)
		}
		return &req, nil
	case ProviderAnthropic:
		return anthropicRequestToOpenAI(body)
	case ProviderGemini:
		return geminiRequestToOpenAI(body)
	case ProviderOpenAIResponses:
		return responsesRequestToOpenAI(body)
	}
	return nil, fmt.Errorf("暂不支持解析 %s 请求", from)
}

// renderRequestFromOpenAI 将 OpenAI 规范请求渲染为 to 协议请求体。
func renderRequestFromOpenAI(to Provider, req *ChatCompletionRequest) ([]byte, error) {
	switch to {
	case ProviderOpenAI:
		return json.Marshal(req)
	case ProviderAnthropic:
		return anthropicRequestFromOpenAI(req)
	case ProviderGemini:
		return geminiRequestFromOpenAI(req)
	case ProviderOpenAIResponses:
		return responsesRequestFromOpenAI(req)
	}
	return nil, fmt.Errorf("暂不支持渲染 %s 请求", to)
}

// parseResponseToOpenAI 按 from 协议解析响应体为 OpenAI 规范响应。
func parseResponseToOpenAI(from Provider, body []byte) (*ChatCompletionResponse, error) {
	switch from {
	case ProviderOpenAI:
		var resp ChatCompletionResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("解析 openai 响应失败: %w", err)
		}
		return &resp, nil
	case ProviderAnthropic:
		return anthropicResponseToOpenAI(body)
	case ProviderGemini:
		return geminiResponseToOpenAI(body)
	case ProviderOpenAIResponses:
		return responsesResponseToOpenAI(body)
	}
	return nil, fmt.Errorf("暂不支持解析 %s 响应", from)
}

// renderResponseFromOpenAI 将 OpenAI 规范响应渲染为 to 协议响应体。
func renderResponseFromOpenAI(to Provider, resp *ChatCompletionResponse) ([]byte, error) {
	switch to {
	case ProviderOpenAI:
		return json.Marshal(resp)
	case ProviderAnthropic:
		return anthropicResponseFromOpenAI(resp)
	case ProviderGemini:
		return geminiResponseFromOpenAI(resp)
	case ProviderOpenAIResponses:
		return responsesResponseFromOpenAI(resp)
	}
	return nil, fmt.Errorf("暂不支持渲染 %s 响应", to)
}

// UsageFromResponse 从 from 协议的响应体中提取统一用量，供日志 / 计费。
// 解析失败或响应无 usage 时返回 nil。
func UsageFromResponse(from Provider, body []byte) *Usage {
	resp, err := parseResponseToOpenAI(from, body)
	if err != nil {
		return nil
	}
	return resp.Usage
}
